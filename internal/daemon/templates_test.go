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

func TestDefaultMCPRegistry(t *testing.T) {
	reg := DefaultMCPRegistry()
	assert.Len(t, reg, 3)
	assert.Contains(t, reg, "ethos")
	assert.Contains(t, reg, "beadle-email")
	assert.Contains(t, reg, "biff")
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

			path, err := tmpl.BuildSystemPrompt(tt.missionID)
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
