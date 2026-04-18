package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

			cmds, err := LoadCommands(dir)
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

	cmds, err := LoadCommands(dir)
	require.NoError(t, err)
	// One wins, one is skipped. Only one entry for "wall".
	assert.Len(t, cmds, 1)
	assert.Contains(t, cmds, "wall")
}

func TestLoadCommands_NonexistentDir(t *testing.T) {
	_, err := LoadCommands(filepath.Join(t.TempDir(), "does-not-exist"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read command dir")
}

func TestLoadCommands_FieldValues(t *testing.T) {
	dir := t.TempDir()
	writeYAML(t, dir, "wall.yaml", validCommandYAML)

	cmds, err := LoadCommands(dir)
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
	cmds, err := LoadCommands(dir)
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

func TestVerifySignature_Stub(t *testing.T) {
	cmd := &Command{Name: "test", Signature: "deadbeef"}
	err := VerifySignature(cmd, "gpg")
	assert.NoError(t, err)
}
