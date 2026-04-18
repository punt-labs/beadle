package daemon

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// CommandArg describes a single typed argument in a command definition.
type CommandArg struct {
	Name      string   `yaml:"name"`
	Type      string   `yaml:"type"`       // string, enum, int, bool
	Values    []string `yaml:"values"`     // for enum type
	MaxLength int      `yaml:"max_length"` // for string type
	Required  bool     `yaml:"required"`
	Default   string   `yaml:"default"`
	Position  int      `yaml:"position"` // positional index for CLI arg assembly; 0 = named flag
}

// Step is one binary in a compound CLI command chain.
type Step struct {
	Binary    string   `yaml:"binary"`
	FixedArgs []string `yaml:"fixed_args"`
	Stdin     string   `yaml:"stdin"` // "pipe" or "stdout"
}

// Command is a GPG-signed YAML command definition for the pipeline orchestrator.
type Command struct {
	Name         string       `yaml:"name"`
	Description  string       `yaml:"description"`
	Signature    string       `yaml:"signature"`
	Runner       string       `yaml:"runner"` // claude | cli
	Mode         string       `yaml:"mode"`   // process | passthrough
	Args         []CommandArg `yaml:"args"`
	OutputSchema any          `yaml:"output_schema"` // "text" or map[string]any
	Binary       string       `yaml:"binary"`        // cli runner: single-binary
	FixedArgs    []string     `yaml:"fixed_args"`    // cli runner: single-binary args
	Steps        []Step       `yaml:"steps"`         // cli runner: compound steps
	WriteSet     []string     `yaml:"write_set"`
	Budget       struct {
		Rounds              int  `yaml:"rounds"`
		ReflectionAfterEach bool `yaml:"reflection_after_each"`
	} `yaml:"budget"`
	Timeout    string   `yaml:"timeout"` // duration string (2m, 30m, etc.)
	Prompt     string   `yaml:"prompt"`
	Tools      []string `yaml:"tools"`
	MCPServers []string `yaml:"mcp_servers"`
	EnvVars    []string `yaml:"env_vars"`
}

var validArgTypes = map[string]bool{
	"string": true,
	"enum":   true,
	"int":    true,
	"bool":   true,
}

// LoadCommands scans dir for *.yaml files, parses each as a Command,
// validates required fields, and returns a map keyed by command name.
// Invalid files are logged and skipped.
func LoadCommands(dir string) (map[string]*Command, error) {
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
		cmd, err := loadCommand(path)
		if err != nil {
			slog.Warn("skip invalid command file", "path", path, "error", err)
			continue
		}
		if _, dup := cmds[cmd.Name]; dup {
			slog.Warn("skip duplicate command name", "name", cmd.Name, "path", path)
			continue
		}
		cmds[cmd.Name] = cmd
	}
	return cmds, nil
}

func loadCommand(path string) (*Command, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var cmd Command
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&cmd); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	if err := validateCommand(&cmd); err != nil {
		return nil, fmt.Errorf("validate %s: %w", path, err)
	}
	return &cmd, nil
}

func validateCommand(cmd *Command) error {
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

// VerifySignature is a stub for GPG signature verification of command files.
// The signing workflow is not yet defined; the Signature field exists so
// YAML can carry the signature for future verification.
func VerifySignature(cmd *Command, gpgBinary string) error {
	return nil
}
