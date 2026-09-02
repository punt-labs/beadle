package daemon

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// CommandArg describes a single typed argument in a command definition.
// Values carries json:",omitempty" for the same nil-vs-empty-slice reason
// Command's own slice fields do -- see Command's doc comment.
type CommandArg struct {
	Name      string   `yaml:"name" json:"name"`
	Type      string   `yaml:"type" json:"type"`               // string, enum, int, bool
	Values    []string `yaml:"values" json:"values,omitempty"` // for enum type
	MaxLength int      `yaml:"max_length" json:"max_length"`   // for string type
	Required  bool     `yaml:"required" json:"required"`
	Default   string   `yaml:"default" json:"default"`
	Position  int      `yaml:"position" json:"position"` // positional index for CLI arg assembly; 0 = named flag
}

// Step is one binary in a compound CLI command chain. FixedArgs carries
// json:",omitempty" for the same reason as Command's own slice fields --
// see Command's doc comment.
type Step struct {
	Binary    string   `yaml:"binary" json:"binary"`
	FixedArgs []string `yaml:"fixed_args" json:"fixed_args,omitempty"`
	Stdin     string   `yaml:"stdin" json:"stdin"` // "pipe" or "stdout"
}

// Command is a GPG-signed YAML command definition for the pipeline
// orchestrator. The json tags exist for exactly one purpose --
// CanonicalCommandBytes' signed payload (signature.go) -- and are not a
// second on-disk format: every command file on disk is still YAML, read
// and written exclusively through DecodeCommandFile below.
//
// Every slice-typed field carries json:",omitempty". Unlike yaml.v3, which
// marshals a nil slice and a non-nil empty slice identically (both "[]"),
// encoding/json marshals them as "null" and "[]" respectively -- two
// different byte sequences for a distinction the rest of this codebase
// never treats as meaningful (ValidateArgs, the runners, and every YAML
// fixture in this repo all read "not present" and "present but empty" the
// same way). Without omitempty, a Command built as a Go literal with a
// nil slice and the same Command after a YAML marshal/unmarshal round
// trip (which turns that nil into a non-nil empty slice) canonicalize to
// different bytes and a genuinely unmodified command would fail its own
// signature. omitempty makes both cases marshal identically by omitting
// the field entirely either way.
type Command struct {
	Name         string       `yaml:"name" json:"name"`
	Description  string       `yaml:"description" json:"description"`
	Signature    string       `yaml:"signature" json:"signature"`
	Runner       string       `yaml:"runner" json:"runner"` // claude | cli
	Mode         string       `yaml:"mode" json:"mode"`     // process | passthrough
	Args         []CommandArg `yaml:"args" json:"args,omitempty"`
	OutputSchema any          `yaml:"output_schema" json:"output_schema"` // "text" or map[string]any
	Binary       string       `yaml:"binary" json:"binary"`               // cli runner: single-binary
	FixedArgs    []string     `yaml:"fixed_args" json:"fixed_args,omitempty"`
	Steps        []Step       `yaml:"steps" json:"steps,omitempty"` // cli runner: compound steps
	WriteSet     []string     `yaml:"write_set" json:"write_set,omitempty"`
	Budget       struct {
		Rounds              int  `yaml:"rounds" json:"rounds"`
		ReflectionAfterEach bool `yaml:"reflection_after_each" json:"reflection_after_each"`
	} `yaml:"budget" json:"budget"`
	Timeout string `yaml:"timeout" json:"timeout"` // duration string (2m, 30m, etc.)
	Prompt  string `yaml:"prompt" json:"prompt"`
	// Tools is the claude runner's built-in tool grant, threaded to
	// WorkerSpawner.Run's --tools flag (spawner.go). Empty means none --
	// the worker gets zero built-in tools, not a default set. Only meaningful
	// for the claude runner; ValidateCommand rejects it on a cli recipe. MCP
	// tools (send_email, ethos mission calls) are a separate grant, scoped by
	// MCPServers below -- a recipe with Tools: [] and an MCP server declared
	// still reaches that server's tools.
	Tools      []string `yaml:"tools" json:"tools,omitempty"`
	MCPServers []string `yaml:"mcp_servers" json:"mcp_servers,omitempty"`
	EnvVars    []string `yaml:"env_vars" json:"env_vars,omitempty"`
}

var validArgTypes = map[string]bool{
	"string": true,
	"enum":   true,
	"int":    true,
	"bool":   true,
}

// validToolNames is the set of Claude Code built-in tool names a recipe may
// grant itself via Tools. A name outside this set is almost always a typo,
// and a typo here must fail recipe validation loudly -- the alternative is
// the tool silently granting nothing (an unrecognized name that never
// matches anything the worker actually has) or, worse, someone assuming a
// typo'd name still works. Mirrors the tool names the installed `claude`
// CLI's own --tools flag recognizes (verified directly against its binary);
// it does not include MCP-server-scoped tools, which are named and gated
// separately (see Tools' doc comment above).
var validToolNames = map[string]bool{
	"Bash":         true,
	"Read":         true,
	"Edit":         true,
	"Write":        true,
	"Glob":         true,
	"Grep":         true,
	"Agent":        true,
	"Task":         true,
	"WebFetch":     true,
	"WebSearch":    true,
	"NotebookEdit": true,
	"TodoWrite":    true,
	"BashOutput":   true,
	"KillShell":    true,
	"ExitPlanMode": true,
}

// verificationError wraps an operational failure that happened while
// attempting to verify a command file's signature -- the gpg binary
// couldn't run, a temp homedir couldn't be created, and the like -- as
// opposed to a *SignatureError, which is a definitive signature verdict
// (missing, wrong key, expired, invalid). VerifySignature returns both
// kinds through the same error interface; this wrapper is how loadCommand
// tags the operational kind so LoadCommands can log it distinctly instead
// of folding it into the generic "skip invalid command file" path an
// ordinary parse or validation failure takes. It can only occur on the
// enforcement-active path (ownerKeyID != ""), since that is the only place
// loadCommand constructs one.
type verificationError struct {
	err error
}

func (e *verificationError) Error() string { return e.err.Error() }
func (e *verificationError) Unwrap() error { return e.err }

// LoadCommands scans dir for *.yaml files, parses each as a Command,
// validates required fields, and returns a map keyed by command name.
// Invalid files are logged and skipped. gpgBinary and ownerKeyID thread
// through to loadCommand for signature verification (see loadCommand);
// ownerKeyID == "" disables verification entirely. logger receives every
// skip/reject log line; a nil logger falls back to slog.Default().
func LoadCommands(dir, gpgBinary, ownerKeyID string, logger *slog.Logger) (map[string]*Command, error) {
	if logger == nil {
		logger = slog.Default()
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read command dir %s: %w", dir, err)
	}

	cmds := make(map[string]*Command)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		cmd, err := loadCommand(path, gpgBinary, ownerKeyID)
		if err != nil {
			var sigErr *SignatureError
			var verErr *verificationError
			switch {
			case errors.As(err, &sigErr):
				logger.Error("reject command file: signature verification failed",
					"path", path, "reason", sigErr.Reason, "detail", sigErr.Detail)
			case errors.As(err, &verErr):
				logger.Error("signature verification unavailable, skipping command file",
					"path", path, "error", err)
			default:
				logger.Warn("skip invalid command file", "path", path, "error", err)
			}
			continue
		}
		if _, dup := cmds[cmd.Name]; dup {
			logger.Warn("skip duplicate command name", "name", cmd.Name, "path", path)
			continue
		}
		cmds[cmd.Name] = cmd
	}
	return cmds, nil
}

// DecodeCommandFile reads path and decodes it as a Command. loadCommand,
// `beadle-daemon sign`, and `beadle-daemon verify` all call this single
// function so their notion of "what a command file is" cannot drift --
// three independent hand-maintained copies of this decode existed before
// this function did, and verify's entire value ("would the daemon load
// this file") depends on every reader agreeing.
func DecodeCommandFile(path string) (*Command, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var cmd Command
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&cmd); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &cmd, nil
}

func loadCommand(path, gpgBinary, ownerKeyID string) (*Command, error) {
	cmd, err := DecodeCommandFile(path)
	if err != nil {
		return nil, err
	}

	if ownerKeyID != "" {
		if err := VerifySignature(cmd, gpgBinary, ownerKeyID); err != nil {
			var sigErr *SignatureError
			if errors.As(err, &sigErr) {
				return nil, fmt.Errorf("verify signature %s: %w", path, sigErr)
			}
			return nil, fmt.Errorf("verify signature %s: %w", path, &verificationError{err: err})
		}
	}
	// ownerKeyID == "" means signing enforcement is not configured --
	// VerifySignature is never called, and loadCommand behaves exactly as
	// it does today.

	if err := ValidateCommand(cmd); err != nil {
		return nil, fmt.Errorf("validate %s: %w", path, err)
	}
	return cmd, nil
}

// ValidateCommand checks cmd's shape against the command-file schema,
// writing scalar defaults (Runner, Mode) into cmd along the way. Exported
// so `beadle-daemon sign` can run the identical check before signing -- an
// invalid recipe must never sign green and fail only later, at daemon
// startup, far from the mistake that caused it. Because it writes
// defaults, a caller that still needs cmd's pre-default canonical bytes
// (sign does, to compute what actually gets signed) must validate a COPY,
// never cmd itself.
func ValidateCommand(cmd *Command) error {
	if cmd.Name == "" {
		return fmt.Errorf("missing required field: name")
	}

	// Default runner and mode.
	if cmd.Runner == "" {
		cmd.Runner = "claude"
	}
	if cmd.Runner != "claude" && cmd.Runner != "cli" {
		return fmt.Errorf("invalid runner %q (want claude, cli)", cmd.Runner)
	}
	if cmd.Mode == "" {
		cmd.Mode = "process"
	}
	if cmd.Mode != "process" && cmd.Mode != "passthrough" {
		return fmt.Errorf("invalid mode %q (want process, passthrough)", cmd.Mode)
	}

	// Runner-conditional validation.
	switch cmd.Runner {
	case "claude":
		if cmd.Prompt == "" {
			return fmt.Errorf("claude runner requires prompt")
		}
		if cmd.Budget.Rounds <= 0 {
			return fmt.Errorf("claude runner requires budget.rounds > 0")
		}
		if cmd.Binary != "" {
			return fmt.Errorf("binary is not valid for claude runner")
		}
		if len(cmd.Steps) > 0 {
			return fmt.Errorf("steps is not valid for claude runner")
		}
		if len(cmd.FixedArgs) > 0 {
			return fmt.Errorf("fixed_args is not valid for claude runner")
		}
	case "cli":
		if cmd.Binary == "" && len(cmd.Steps) == 0 {
			return fmt.Errorf("cli runner requires binary or steps")
		}
		if cmd.Binary != "" && len(cmd.Steps) > 0 {
			return fmt.Errorf("cli runner: set binary or steps, not both")
		}
		if len(cmd.Tools) > 0 {
			return fmt.Errorf("tools is not valid for cli runner")
		}
	}

	// Tools validation: every declared name must be a real Claude Code
	// built-in tool. A typo here must fail at load, not silently grant
	// nothing (an unrecognized name matches no real tool) or everything.
	for _, tool := range cmd.Tools {
		if !validToolNames[tool] {
			return fmt.Errorf("unrecognized tool %q (see internal/daemon/command.go's validToolNames)", tool)
		}
	}

	// OutputSchema validation.
	if cmd.OutputSchema == nil {
		return fmt.Errorf("missing required field: output_schema")
	}
	switch v := cmd.OutputSchema.(type) {
	case string:
		if v != "text" {
			return fmt.Errorf("output_schema string must be \"text\", got %q", v)
		}
	case map[string]any:
		// valid JSON Schema object
	default:
		return fmt.Errorf("output_schema must be \"text\" or a JSON Schema object, got %T", cmd.OutputSchema)
	}

	if cmd.Timeout != "" {
		if _, err := time.ParseDuration(cmd.Timeout); err != nil {
			return fmt.Errorf("invalid timeout %q: %w", cmd.Timeout, err)
		}
	}

	// Arg validation.
	seenPos := make(map[int]string)
	for i, a := range cmd.Args {
		if a.Name == "" {
			return fmt.Errorf("arg[%d]: missing name", i)
		}
		if !validArgTypes[a.Type] {
			return fmt.Errorf("arg %q: unrecognized type %q", a.Name, a.Type)
		}
		if a.Type == "enum" && len(a.Values) == 0 {
			return fmt.Errorf("arg %q: enum type requires non-empty values list", a.Name)
		}
		if a.Position > 0 {
			if other, dup := seenPos[a.Position]; dup {
				return fmt.Errorf("arg %q: duplicate position %d (conflicts with %q)", a.Name, a.Position, other)
			}
			seenPos[a.Position] = a.Name
		}
	}

	// Compound step validation.
	for i, step := range cmd.Steps {
		if step.Binary == "" {
			return fmt.Errorf("step[%d]: missing binary", i)
		}
		if i == 0 && step.Stdin != "pipe" {
			return fmt.Errorf("step[0]: stdin must be \"pipe\", got %q", step.Stdin)
		}
		if i > 0 && step.Stdin != "stdout" {
			return fmt.Errorf("step[%d]: stdin must be \"stdout\", got %q", i, step.Stdin)
		}
	}

	return nil
}

// ValidateArgs checks that args satisfies cmd's declared argument schema.
// Returns a descriptive error on the first violation.
func ValidateArgs(cmd *Command, args map[string]any) error {
	// Build lookup of declared arg names.
	declared := make(map[string]*CommandArg, len(cmd.Args))
	for i := range cmd.Args {
		declared[cmd.Args[i].Name] = &cmd.Args[i]
	}

	// Reject unknown arg names.
	for name := range args {
		if _, ok := declared[name]; !ok {
			return fmt.Errorf("unknown arg %q for command %q", name, cmd.Name)
		}
	}

	// Check each declared arg.
	for _, a := range cmd.Args {
		v, present := args[a.Name]
		if !present {
			if a.Required {
				return fmt.Errorf("missing required arg %q for command %q", a.Name, cmd.Name)
			}
			continue
		}

		switch a.Type {
		case "string":
			s, ok := v.(string)
			if !ok {
				return fmt.Errorf("arg %q: expected string, got %T", a.Name, v)
			}
			if a.MaxLength > 0 && len(s) > a.MaxLength {
				return fmt.Errorf("arg %q: length %d exceeds max_length %d", a.Name, len(s), a.MaxLength)
			}
		case "int":
			switch v.(type) {
			case int, int64, float64:
				// accept numeric types (JSON/YAML decode as float64 or int)
			default:
				return fmt.Errorf("arg %q: expected int, got %T", a.Name, v)
			}
		case "bool":
			if _, ok := v.(bool); !ok {
				return fmt.Errorf("arg %q: expected bool, got %T", a.Name, v)
			}
		case "enum":
			s, ok := v.(string)
			if !ok {
				return fmt.Errorf("arg %q: expected string for enum, got %T", a.Name, v)
			}
			found := false
			for _, allowed := range a.Values {
				if s == allowed {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("arg %q: value %q not in allowed values %v", a.Name, s, a.Values)
			}
		}
	}
	return nil
}
