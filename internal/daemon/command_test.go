package daemon

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const validCommandYAML = `name: wall
description: Broadcast a message to all active agents via biff
signature: deadbeef
runner: claude
mode: passthrough
output_schema: text
args:
  - name: message
    type: string
    max_length: 500
    required: true
  - name: channel
    type: enum
    values: [general, alerts]
    required: false
    default: general
write_set: []
budget:
  rounds: 1
  reflection_after_each: false
timeout: 2m
prompt: |
  Read the message arg from the mission contract and call biff wall.
tools:
  - Bash
mcp_servers:
  - ethos
  - biff
env_vars:
  - BIFF_TOKEN
`

const validCLICommandYAML = `name: format
runner: cli
mode: process
binary: jq
fixed_args: ["-r", ".summary"]
output_schema: text
timeout: 10s
`

func writeYAML(t *testing.T, dir, name, content string) {
	t.Helper()
	err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)
	require.NoError(t, err)
}

func TestLoadCommands(t *testing.T) {
	tests := []struct {
		name      string
		files     map[string]string
		wantNames []string
		wantErr   bool
	}{
		{
			name:      "valid single command",
			files:     map[string]string{"wall.yaml": validCommandYAML},
			wantNames: []string{"wall"},
		},
		{
			name: "multiple valid commands",
			files: map[string]string{
				"wall.yaml": validCommandYAML,
				"deploy.yaml": `name: deploy
prompt: deploy the thing
output_schema: text
budget:
  rounds: 2
`,
			},
			wantNames: []string{"wall", "deploy"},
		},
		{
			name: "skip missing name",
			files: map[string]string{
				"bad.yaml": `prompt: do something
output_schema: text
budget:
  rounds: 1
`,
			},
			wantNames: []string{},
		},
		{
			name: "skip claude runner missing prompt",
			files: map[string]string{
				"bad.yaml": `name: noprompt
output_schema: text
budget:
  rounds: 1
`,
			},
			wantNames: []string{},
		},
		{
			name: "skip claude runner zero budget rounds",
			files: map[string]string{
				"bad.yaml": `name: nobudget
prompt: hello
output_schema: text
budget:
  rounds: 0
`,
			},
			wantNames: []string{},
		},
		{
			name: "skip unknown fields",
			files: map[string]string{
				"bad.yaml": `name: unknown
prompt: hello
output_schema: text
budget:
  rounds: 1
extra_field: should_fail
`,
			},
			wantNames: []string{},
		},
		{
			name: "skip invalid arg type",
			files: map[string]string{
				"bad.yaml": `name: badarg
prompt: hello
output_schema: text
budget:
  rounds: 1
args:
  - name: x
    type: float
    required: true
`,
			},
			wantNames: []string{},
		},
		{
			name: "skip enum with no values",
			files: map[string]string{
				"bad.yaml": `name: badenum
prompt: hello
output_schema: text
budget:
  rounds: 1
args:
  - name: x
    type: enum
    required: true
`,
			},
			wantNames: []string{},
		},
		{
			name:      "empty directory",
			files:     map[string]string{},
			wantNames: []string{},
		},
		{
			name: "ignore non-yaml files",
			files: map[string]string{
				"readme.txt":  "not yaml",
				"config.json": `{"key": "value"}`,
			},
			wantNames: []string{},
		},
		{
			name: "valid with defaults applied",
			files: map[string]string{
				"minimal.yaml": `name: minimal
prompt: do the thing
output_schema: text
budget:
  rounds: 1
`,
			},
			wantNames: []string{"minimal"},
		},
		{
			name: "skip invalid timeout",
			files: map[string]string{
				"bad.yaml": `name: badtimeout
prompt: hello
output_schema: text
budget:
  rounds: 1
timeout: not-a-duration
`,
			},
			wantNames: []string{},
		},
		{
			name: "skip cli runner missing binary and steps",
			files: map[string]string{
				"bad.yaml": `name: nobinary
runner: cli
output_schema: text
`,
			},
			wantNames: []string{},
		},
		{
			name: "skip claude runner with binary",
			files: map[string]string{
				"bad.yaml": `name: claudebin
runner: claude
binary: jq
prompt: hello
output_schema: text
budget:
  rounds: 1
`,
			},
			wantNames: []string{},
		},
		{
			name: "skip output_schema number",
			files: map[string]string{
				"bad.yaml": `name: numschema
prompt: hello
output_schema: 42
budget:
  rounds: 1
`,
			},
			wantNames: []string{},
		},
		{
			name: "skip output_schema invalid string",
			files: map[string]string{
				"bad.yaml": `name: jsonschema
prompt: hello
output_schema: json
budget:
  rounds: 1
`,
			},
			wantNames: []string{},
		},
		{
			name: "valid cli runner single binary",
			files: map[string]string{
				"fmt.yaml": validCLICommandYAML,
			},
			wantNames: []string{"format"},
		},
		{
			name: "valid claude runner with schema object",
			files: map[string]string{
				"sum.yaml": `name: summarize
prompt: summarize
output_schema:
  type: object
  properties:
    title: { type: string }
budget:
  rounds: 1
`,
			},
			wantNames: []string{"summarize"},
		},
		{
			name: "skip step[0] stdin not pipe",
			files: map[string]string{
				"bad.yaml": `name: badstep
runner: cli
output_schema: text
steps:
  - binary: echo
    stdin: stdout
`,
			},
			wantNames: []string{},
		},
		{
			name: "skip step[1] stdin not stdout",
			files: map[string]string{
				"bad.yaml": `name: badstep2
runner: cli
output_schema: text
steps:
  - binary: echo
    stdin: pipe
  - binary: cat
    stdin: pipe
`,
			},
			wantNames: []string{},
		},
		{
			name: "skip duplicate arg positions",
			files: map[string]string{
				"bad.yaml": `name: duppos
runner: cli
binary: echo
output_schema: text
args:
  - name: a
    type: string
    position: 1
  - name: b
    type: string
    position: 1
`,
			},
			wantNames: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, content := range tt.files {
				writeYAML(t, dir, name, content)
			}

			cmds, err := LoadCommands(dir, "gpg", "", nil)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			var got []string
			for name := range cmds {
				got = append(got, name)
			}
			assert.ElementsMatch(t, tt.wantNames, got)
		})
	}
}

func TestLoadCommands_DuplicateNames(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "wall1.yaml", `name: wall
prompt: first
output_schema: text
budget:
  rounds: 1
`)
	writeYAML(t, dir, "wall2.yaml", `name: wall
prompt: second
output_schema: text
budget:
  rounds: 1
`)

	cmds, err := LoadCommands(dir, "gpg", "", nil)
	require.NoError(t, err)
	// One wins, one is skipped. Only one entry for "wall".
	assert.Len(t, cmds, 1)
	assert.Contains(t, cmds, "wall")
}

func TestLoadCommands_NonexistentDir(t *testing.T) {
	_, err := LoadCommands(filepath.Join(t.TempDir(), "does-not-exist"), "gpg", "", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read command dir")
}

func TestLoadCommands_FieldValues(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "wall.yaml", validCommandYAML)

	cmds, err := LoadCommands(dir, "gpg", "", nil)
	require.NoError(t, err)
	require.Contains(t, cmds, "wall")

	cmd := cmds["wall"]
	assert.Equal(t, "wall", cmd.Name)
	assert.Equal(t, "Broadcast a message to all active agents via biff", cmd.Description)
	assert.Equal(t, "deadbeef", cmd.Signature)
	assert.Equal(t, "claude", cmd.Runner)
	assert.Equal(t, "passthrough", cmd.Mode)
	assert.Equal(t, "text", cmd.OutputSchema)
	assert.Equal(t, "2m", cmd.Timeout)
	assert.Equal(t, 1, cmd.Budget.Rounds)
	assert.False(t, cmd.Budget.ReflectionAfterEach)
	assert.Equal(t, []string{"Bash"}, cmd.Tools)
	assert.Equal(t, []string{"ethos", "biff"}, cmd.MCPServers)
	assert.Equal(t, []string{"BIFF_TOKEN"}, cmd.EnvVars)

	require.Len(t, cmd.Args, 2)
	assert.Equal(t, "message", cmd.Args[0].Name)
	assert.Equal(t, "string", cmd.Args[0].Type)
	assert.Equal(t, 500, cmd.Args[0].MaxLength)
	assert.True(t, cmd.Args[0].Required)
	assert.Equal(t, "channel", cmd.Args[1].Name)
	assert.Equal(t, "enum", cmd.Args[1].Type)
	assert.Equal(t, []string{"general", "alerts"}, cmd.Args[1].Values)
	assert.False(t, cmd.Args[1].Required)
	assert.Equal(t, "general", cmd.Args[1].Default)
}

func TestLoadCommands_DefaultRunnerMode(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "min.yaml", `name: min
prompt: hello
output_schema: text
budget:
  rounds: 1
`)
	cmds, err := LoadCommands(dir, "gpg", "", nil)
	require.NoError(t, err)
	require.Contains(t, cmds, "min")
	assert.Equal(t, "claude", cmds["min"].Runner)
	assert.Equal(t, "process", cmds["min"].Mode)
}

func TestValidateArgs(t *testing.T) {
	cmd := &Command{
		Name: "test",
		Args: []CommandArg{
			{Name: "message", Type: "string", MaxLength: 10, Required: true},
			{Name: "count", Type: "int", Required: false},
			{Name: "verbose", Type: "bool", Required: false},
			{Name: "env", Type: "enum", Values: []string{"dev", "prod"}, Required: true},
		},
	}

	tests := []struct {
		name    string
		args    map[string]any
		wantErr string
	}{
		{
			name:    "all valid",
			args:    map[string]any{"message": "hello", "count": 5, "verbose": true, "env": "dev"},
			wantErr: "",
		},
		{
			name:    "required only",
			args:    map[string]any{"message": "hi", "env": "prod"},
			wantErr: "",
		},
		{
			name:    "missing required message",
			args:    map[string]any{"env": "dev"},
			wantErr: "missing required arg \"message\"",
		},
		{
			name:    "missing required env",
			args:    map[string]any{"message": "hi"},
			wantErr: "missing required arg \"env\"",
		},
		{
			name:    "wrong type for string",
			args:    map[string]any{"message": 42, "env": "dev"},
			wantErr: "expected string",
		},
		{
			name:    "wrong type for int",
			args:    map[string]any{"message": "hi", "env": "dev", "count": "five"},
			wantErr: "expected int",
		},
		{
			name:    "wrong type for bool",
			args:    map[string]any{"message": "hi", "env": "dev", "verbose": "yes"},
			wantErr: "expected bool",
		},
		{
			name:    "max_length exceeded",
			args:    map[string]any{"message": "this string is too long", "env": "dev"},
			wantErr: "exceeds max_length",
		},
		{
			name:    "max_length exact boundary",
			args:    map[string]any{"message": "0123456789", "env": "dev"},
			wantErr: "",
		},
		{
			name:    "enum value not allowed",
			args:    map[string]any{"message": "hi", "env": "staging"},
			wantErr: "not in allowed values",
		},
		{
			name:    "enum wrong type",
			args:    map[string]any{"message": "hi", "env": 42},
			wantErr: "expected string for enum",
		},
		{
			name:    "unknown arg",
			args:    map[string]any{"message": "hi", "env": "dev", "bogus": "val"},
			wantErr: "unknown arg \"bogus\"",
		},
		{
			name:    "int as float64",
			args:    map[string]any{"message": "hi", "env": "dev", "count": float64(3)},
			wantErr: "",
		},
		{
			name:    "int as int64",
			args:    map[string]any{"message": "hi", "env": "dev", "count": int64(7)},
			wantErr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateArgs(cmd, tt.args)
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			}
		})
	}
}

func TestValidateArgs_NoArgs(t *testing.T) {
	cmd := &Command{Name: "simple", Args: nil}
	err := ValidateArgs(cmd, map[string]any{})
	assert.NoError(t, err)
}

func TestValidateArgs_EmptyArgsMap(t *testing.T) {
	cmd := &Command{
		Name: "test",
		Args: []CommandArg{
			{Name: "opt", Type: "string", Required: false},
		},
	}
	err := ValidateArgs(cmd, map[string]any{})
	assert.NoError(t, err)
}

// capturingHandler is a slog.Handler that records every log.Record it
// receives, so a test can assert on the level, message, and structured
// attrs a call site logged -- not just its side effects (e.g. whether an
// entry made it into LoadCommands' returned map).
type capturingHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *capturingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *capturingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r)
	return nil
}

func (h *capturingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *capturingHandler) WithGroup(string) slog.Handler      { return h }

// find returns the first captured record at level with the given message,
// and its attrs as a map, or ok == false if none matches.
func (h *capturingHandler) find(level slog.Level, msg string) (attrs map[string]any, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if r.Level != level || r.Message != msg {
			continue
		}
		attrs = make(map[string]any)
		r.Attrs(func(a slog.Attr) bool {
			attrs[a.Key] = a.Value.Any()
			return true
		})
		return attrs, true
	}
	return nil, false
}

// installCapturingHandler swaps slog's package-level default for a
// capturingHandler for the duration of t, restoring the previous default on
// cleanup. LoadCommands takes an explicit *slog.Logger and falls back to
// slog.Default() only when that argument is nil -- the case most subtests in
// this file exercise -- so installCapturingHandler intercepts that fallback
// path to observe the log lines.
func installCapturingHandler(t *testing.T) *capturingHandler {
	t.Helper()
	h := &capturingHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return h
}

// writeSignedCommand builds a minimal claude-runner Command named name,
// signs its canonical bytes with signerEmail's key in gpgHome, marshals the
// signed command to YAML, and writes it to dir/filename.
func writeSignedCommand(t *testing.T, gpgBin, gpgHome, dir, filename, signerEmail, name string) {
	t.Helper()
	cmd := &Command{
		Name:         name,
		Runner:       "claude",
		Mode:         "process",
		Prompt:       "do the thing",
		OutputSchema: "text",
	}
	cmd.Budget.Rounds = 1

	canon, err := CanonicalCommandBytes(cmd)
	require.NoError(t, err)
	cmd.Signature = signCanonical(t, gpgBin, gpgHome, signerEmail, canon)

	data, err := yaml.Marshal(cmd)
	require.NoError(t, err)
	writeYAML(t, dir, filename, string(data))
}

// TestLoadCommands_SignatureEnforcement covers §4 of
// docs/wire-verifysignature.md: LoadCommands' loadCommand call site enforces
// VerifySignature when ownerKeyID is configured, and a rejected file logs at
// slog.Error with structured reason/detail fields distinct from the
// slog.Warn path an ordinary parse/validation failure already takes.
func TestLoadCommands_SignatureEnforcement(t *testing.T) {
	gpgBin := gpgBinary(t)
	home := shortGPGHome(t)
	const ownerEmail = "owner-enforcement@example.com"
	const otherEmail = "other-enforcement@example.com"
	ownerFpr := genOwnerKey(t, gpgBin, home, ownerEmail, "1y")
	genOwnerKey(t, gpgBin, home, otherEmail, "1y")
	t.Setenv("GNUPGHOME", home)

	t.Run("signed and valid loads", func(t *testing.T) {
		dir := t.TempDir()
		writeSignedCommand(t, gpgBin, home, dir, "good.yaml", ownerEmail, "good")

		cmds, err := LoadCommands(dir, gpgBin, ownerFpr, nil)
		require.NoError(t, err)
		assert.Contains(t, cmds, "good")
	})

	t.Run("unsigned is rejected and logged at slog.Error", func(t *testing.T) {
		dir := t.TempDir()
		writeYAML(t, dir, "unsigned.yaml", `name: unsigned
prompt: hello
output_schema: text
budget:
  rounds: 1
`)
		h := installCapturingHandler(t)

		cmds, err := LoadCommands(dir, gpgBin, ownerFpr, nil)
		require.NoError(t, err)
		assert.NotContains(t, cmds, "unsigned")

		attrs, ok := h.find(slog.LevelError, "reject command file: signature verification failed")
		require.True(t, ok, "expected a slog.Error record for the rejected file")
		assert.Equal(t, ReasonMissing, attrs["reason"])
	})

	t.Run("signed by an unrelated keypair is rejected as wrong-key", func(t *testing.T) {
		dir := t.TempDir()
		writeSignedCommand(t, gpgBin, home, dir, "wrongkey.yaml", otherEmail, "wrongkey")
		h := installCapturingHandler(t)

		cmds, err := LoadCommands(dir, gpgBin, ownerFpr, nil)
		require.NoError(t, err)
		assert.NotContains(t, cmds, "wrongkey")

		attrs, ok := h.find(slog.LevelError, "reject command file: signature verification failed")
		require.True(t, ok, "expected a slog.Error record for the rejected file")
		assert.Equal(t, ReasonWrongKey, attrs["reason"])
	})

	t.Run("a rejected file is excluded while a sibling valid file still loads", func(t *testing.T) {
		dir := t.TempDir()
		writeSignedCommand(t, gpgBin, home, dir, "good.yaml", ownerEmail, "good")
		writeYAML(t, dir, "bad.yaml", `name: bad
prompt: hello
output_schema: text
budget:
  rounds: 1
`)

		cmds, err := LoadCommands(dir, gpgBin, ownerFpr, nil)
		require.NoError(t, err)
		assert.Contains(t, cmds, "good")
		assert.NotContains(t, cmds, "bad")
	})

	t.Run("ownerKeyID empty disables verification -- unsigned file loads with zero rejection", func(t *testing.T) {
		dir := t.TempDir()
		writeYAML(t, dir, "unsigned.yaml", `name: unsigned
prompt: hello
output_schema: text
budget:
  rounds: 1
`)
		h := installCapturingHandler(t)

		cmds, err := LoadCommands(dir, gpgBin, "", nil)
		require.NoError(t, err)
		assert.Contains(t, cmds, "unsigned")

		_, ok := h.find(slog.LevelError, "reject command file: signature verification failed")
		assert.False(t, ok, "no signature rejection must be logged when ownerKeyID is unset")
	})

	t.Run("operational verification failure logs distinctly, not as generic skip", func(t *testing.T) {
		dir := t.TempDir()
		cmd := &Command{
			Name:         "unavailable",
			Runner:       "claude",
			Mode:         "process",
			Prompt:       "do the thing",
			OutputSchema: "text",
			Signature:    "bogus-signature-content",
		}
		cmd.Budget.Rounds = 1
		data, err := yaml.Marshal(cmd)
		require.NoError(t, err)
		writeYAML(t, dir, "unavailable.yaml", string(data))

		h := installCapturingHandler(t)

		// A gpg binary that cannot run at all is an operational failure
		// inside VerifySignature (export never runs to completion), not a
		// signature verdict -- distinct from both the SignatureError path
		// and an ordinary parse/validation failure.
		cmds, err := LoadCommands(dir, filepath.Join(t.TempDir(), "no-such-gpg-binary"), ownerFpr, nil)
		require.NoError(t, err)
		assert.NotContains(t, cmds, "unavailable")

		_, rejected := h.find(slog.LevelError, "reject command file: signature verification failed")
		assert.False(t, rejected, "an operational failure is not a signature verdict")
		_, skipped := h.find(slog.LevelWarn, "skip invalid command file")
		assert.False(t, skipped, "an operational failure while enforcement is active must not be logged as a generic skip")

		attrs, ok := h.find(slog.LevelError, "signature verification unavailable, skipping command file")
		require.True(t, ok, "expected the distinct operational-failure Error record")
		assert.Contains(t, attrs["path"], "unavailable.yaml")
	})

	t.Run("rejection is observable through a logger passed into LoadCommands, not just the package default", func(t *testing.T) {
		dir := t.TempDir()
		writeYAML(t, dir, "unsigned.yaml", `name: unsigned
prompt: hello
output_schema: text
budget:
  rounds: 1
`)
		explicit := &capturingHandler{}
		explicitLogger := slog.New(explicit)
		// The package default is a distinct handler here, so a pass if the
		// record only reached slog.Default() -- and not the logger actually
		// passed in -- is impossible.
		defaultHandler := installCapturingHandler(t)

		cmds, err := LoadCommands(dir, gpgBin, ownerFpr, explicitLogger)
		require.NoError(t, err)
		assert.NotContains(t, cmds, "unsigned")

		_, ok := explicit.find(slog.LevelError, "reject command file: signature verification failed")
		assert.True(t, ok, "expected the rejection to be logged through the explicitly passed logger")

		_, onDefault := defaultHandler.find(slog.LevelError, "reject command file: signature verification failed")
		assert.False(t, onDefault, "the rejection must not also land on the package default when an explicit logger is passed")
	})
}
