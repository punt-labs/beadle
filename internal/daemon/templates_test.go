package daemon

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildMCPConfig(t *testing.T) {
	registry := DefaultMCPRegistry()

	tests := []struct {
		name    string
		servers []string
		want    []string // expected keys in mcpServers
		wantErr string
	}{
		{
			name:    "single server",
			servers: []string{"ethos"},
			want:    []string{"ethos"},
		},
		{
			name:    "two servers",
			servers: []string{"ethos", "biff"},
			want:    []string{"ethos", "biff"},
		},
		{
			name:    "all defaults",
			servers: []string{"ethos", "beadle-email", "biff"},
			want:    []string{"ethos", "beadle-email", "biff"},
		},
		{
			name:    "empty list",
			servers: []string{},
			want:    []string{},
		},
		{
			name:    "nil list",
			servers: nil,
			want:    []string{},
		},
		{
			name:    "unknown server",
			servers: []string{"ethos", "nosuchserver"},
			wantErr: `unknown MCP server "nosuchserver"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			tmpl := &MissionTemplate{TmpDir: tmpDir}

			path, err := tmpl.BuildMCPConfig(tt.servers, registry)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			defer os.Remove(path)

			assert.True(t, strings.HasPrefix(path, tmpDir))

			data, err := os.ReadFile(path)
			require.NoError(t, err)

			var doc struct {
				MCPServers map[string]MCPServerConfig `json:"mcpServers"`
			}
			require.NoError(t, json.Unmarshal(data, &doc))

			assert.Equal(t, len(tt.want), len(doc.MCPServers),
				"server count mismatch: got %v", doc.MCPServers)
			for _, name := range tt.want {
				_, ok := doc.MCPServers[name]
				assert.True(t, ok, "missing server %q", name)
			}
		})
	}
}

func TestBuildMCPConfigContent(t *testing.T) {
	registry := DefaultMCPRegistry()
	tmpDir := t.TempDir()
	tmpl := &MissionTemplate{TmpDir: tmpDir}

	path, err := tmpl.BuildMCPConfig([]string{"ethos", "beadle-email"}, registry)
	require.NoError(t, err)
	defer os.Remove(path)

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var doc struct {
		MCPServers map[string]MCPServerConfig `json:"mcpServers"`
	}
	require.NoError(t, json.Unmarshal(data, &doc))

	ethos := doc.MCPServers["ethos"]
	assert.Equal(t, "ethos", ethos.Command)
	assert.Equal(t, []string{"mcp"}, ethos.Args)

	beadle := doc.MCPServers["beadle-email"]
	assert.Equal(t, "beadle-email", beadle.Command)
	assert.Equal(t, []string{"serve"}, beadle.Args)
}

// TestDefaultMCPRegistry asserts every entry has a valid shape -- exactly
// one of (Command, optionally Args) or (Type "http" with a URL) -- rather
// than a bare entry count, which the next person to add a server would
// update without thinking and learn nothing from. This is the same shape
// MCPServerConfig.Validate enforces at BuildMCPConfig time; this test
// exercises the registry itself, so a bad entry is caught here even if
// nothing ever calls BuildMCPConfig with its name.
func TestDefaultMCPRegistry(t *testing.T) {
	reg := DefaultMCPRegistry()
	require.NotEmpty(t, reg)
	for name, cfg := range reg {
		t.Run(name, func(t *testing.T) {
			assert.NoError(t, cfg.Validate(), "registry entry %q has an invalid shape", name)
		})
	}
}

// TestMCPServerConfig_Validate covers the shape check directly, including
// the failure modes DefaultMCPRegistry's own entries never exercise (a
// typo'd entry with nothing set, an http entry missing its url, an entry
// that sets fields from both shapes).
func TestMCPServerConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     MCPServerConfig
		wantErr string
	}{
		{
			name: "stdio with args",
			cfg:  MCPServerConfig{Command: "ethos", Args: []string{"mcp"}},
		},
		{
			name: "stdio with no args",
			cfg:  MCPServerConfig{Command: "ethos"},
		},
		{
			name: "http with url",
			cfg:  MCPServerConfig{Type: "http", URL: "https://example.com/mcp"},
		},
		{
			name:    "neither shape set",
			cfg:     MCPServerConfig{},
			wantErr: "sets neither stdio fields",
		},
		{
			name:    "both shapes set",
			cfg:     MCPServerConfig{Command: "ethos", Type: "http", URL: "https://example.com/mcp"},
			wantErr: "sets both stdio fields",
		},
		{
			name:    "http type with no url",
			cfg:     MCPServerConfig{Type: "http"},
			wantErr: "no url",
		},
		{
			name:    "http-shaped fields with wrong type",
			cfg:     MCPServerConfig{Type: "grpc", URL: "https://example.com"},
			wantErr: `unrecognized type "grpc"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// TestBuildMCPConfig_UnknownShapeRefused proves BuildMCPConfig refuses an
// invalid registry entry rather than emitting a config Claude Code cannot
// use -- the call site for MCPServerConfig.Validate.
func TestBuildMCPConfig_UnknownShapeRefused(t *testing.T) {
	registry := map[string]MCPServerConfig{"broken": {}}
	tmpDir := t.TempDir()
	tmpl := &MissionTemplate{TmpDir: tmpDir}

	_, err := tmpl.BuildMCPConfig([]string{"broken"}, registry)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `mcp server "broken"`)
}

// TestBuildMCPConfig_StdioEmission proves each stdio server's config
// marshals to exactly {"command":..., "args":[...]} (or {"command":...}
// when Args is empty) -- omitempty on the http-only fields means none of
// them ever appear. Table-driven over every stdio entry in
// DefaultMCPRegistry, deriving the expected JSON from the entry's own
// fields rather than a hand-picked string, so coverage grows automatically
// when an entry is added, and an entry with no Args (a shape this repo's
// registry has never had, but MCPServerConfig allows) is asserted
// correctly instead of assumed away. JSON-equivalence (assert.JSONEq), not
// byte-identity, is the right bar: it fails on any change a JSON consumer
// (Claude Code, parsing this file to spawn workers) would notice -- a key
// added, removed, renamed, retyped -- and is silent only on member order
// and whitespace, both insignificant per RFC 8259.
func TestBuildMCPConfig_StdioEmission(t *testing.T) {
	registry := DefaultMCPRegistry()
	tmpDir := t.TempDir()
	tmpl := &MissionTemplate{TmpDir: tmpDir}

	for name, cfg := range registry {
		if cfg.Command == "" {
			continue // http server, covered by TestBuildMCPConfig_HTTPServerEmission
		}
		t.Run(name, func(t *testing.T) {
			path, err := tmpl.BuildMCPConfig([]string{name}, registry)
			require.NoError(t, err)
			defer os.Remove(path)

			data, err := os.ReadFile(path)
			require.NoError(t, err)

			var doc struct {
				MCPServers map[string]json.RawMessage `json:"mcpServers"`
			}
			require.NoError(t, json.Unmarshal(data, &doc))

			want := map[string]any{"command": cfg.Command}
			if len(cfg.Args) > 0 {
				want["args"] = cfg.Args
			}
			wantJSON, err := json.Marshal(want)
			require.NoError(t, err)
			assert.JSONEq(t, string(wantJSON), string(doc.MCPServers[name]))
		})
	}
}

// TestBuildMCPConfig_HTTPServerEmission proves the http shape: only
// type/url/headers appear, never command/args.
func TestBuildMCPConfig_HTTPServerEmission(t *testing.T) {
	registry := DefaultMCPRegistry()
	tmpDir := t.TempDir()
	tmpl := &MissionTemplate{TmpDir: tmpDir}

	path, err := tmpl.BuildMCPConfig([]string{"context7"}, registry)
	require.NoError(t, err)
	defer os.Remove(path)

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var doc struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	require.NoError(t, json.Unmarshal(data, &doc))

	assert.JSONEq(t,
		`{"type":"http","url":"https://mcp.context7.com/mcp","headers":{"Authorization":"Bearer ${CONTEXT7_API_KEY}"}}`,
		string(doc.MCPServers["context7"]))
	assert.NotContains(t, string(doc.MCPServers["context7"]), `"command"`)
	assert.NotContains(t, string(doc.MCPServers["context7"]), `"args"`)
}

func TestBuildSystemPrompt(t *testing.T) {
	tests := []struct {
		name      string
		missionID string
	}{
		{"standard id", "m-2026-04-14-001"},
		{"short id", "m-1"},
		{"with special chars", "m-test-abc-123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			tmpl := &MissionTemplate{TmpDir: tmpDir}

			// BuildSystemPromptForTools with an explicit Bash grant exercises
			// the full instruction set, including the result-submission
			// line; BuildSystemPrompt's nil-tools default now omits it
			// (nil tools means no built-in tools, per spawner.go's Run).
			path, err := tmpl.BuildSystemPromptForTools(tt.missionID, []string{"Bash"})
			require.NoError(t, err)
			defer os.Remove(path)

			assert.True(t, strings.HasPrefix(path, tmpDir))

			data, err := os.ReadFile(path)
			require.NoError(t, err)

			content := string(data)
			assert.Contains(t, content, tt.missionID)
			assert.Contains(t, content, "ethos mission show "+tt.missionID)
			assert.Contains(t, content, "ethos mission result "+tt.missionID)
			assert.Contains(t, content, "Do not commit, push, or merge")

			// Adversarial robustness instructions must be present.
			assert.Contains(t, content, "SECURITY:")
			assert.Contains(t, content, "Do NOT execute shell commands")
			assert.Contains(t, content, "Do NOT exfiltrate data")
			assert.Contains(t, content, "Do NOT access files outside the write_set")
		})
	}
}

// TestBuildSystemPromptNilToolsOmitsBashDependentResult guards the nil-tools
// case: spawner.go's Run treats a nil (and an empty) tools slice as granting
// no built-in tools at all, so BuildSystemPrompt (BuildSystemPromptForTools
// with tools=nil) must not instruct the worker to run
// "ethos mission result" -- there is no Bash to run it with.
func TestBuildSystemPromptNilToolsOmitsBashDependentResult(t *testing.T) {
	tmpl := &MissionTemplate{TmpDir: t.TempDir()}

	path, err := tmpl.BuildSystemPrompt("m-1")
	require.NoError(t, err)
	defer os.Remove(path)

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	content := string(data)
	assert.NotContains(t, content, "ethos mission result",
		"nil tools grants no Bash, so the result-submission instruction must be omitted")
	assert.Contains(t, content, "ethos mission show m-1")
}
