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
	tmpDir := t.TempDir()
	tmpl := &MissionTemplate{TmpDir: tmpDir}

	path, err := tmpl.BuildMCPConfig()
	require.NoError(t, err)
	defer os.Remove(path)

	assert.True(t, strings.HasPrefix(path, tmpDir), "file should be in TmpDir")

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	// Must be valid JSON.
	var doc map[string]any
	require.NoError(t, json.Unmarshal(data, &doc))

	// Must contain mcpServers with ethos and beadle-email.
	servers, ok := doc["mcpServers"].(map[string]any)
	require.True(t, ok, "mcpServers must be an object")

	ethos, ok := servers["ethos"].(map[string]any)
	require.True(t, ok, "ethos server entry must exist")
	assert.Equal(t, "ethos", ethos["command"])

	beadle, ok := servers["beadle-email"].(map[string]any)
	require.True(t, ok, "beadle-email server entry must exist")
	assert.Equal(t, "beadle-email", beadle["command"])
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
		})
	}
}
