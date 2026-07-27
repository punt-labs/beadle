package mcp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func enableReq(action string) mcplib.CallToolRequest {
	return mcplib.CallToolRequest{
		Params: mcplib.CallToolParams{
			Name:      "enable",
			Arguments: map[string]any{"action": action},
		},
	}
}

func hostExists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	if err == nil {
		return true
	}
	require.True(t, os.IsNotExist(err), "unexpected stat error: %v", err)
	return false
}

// TestEnableToolDualSurface drives the MCP enable tool through both verbs and
// asserts the same marker + import the CLI writes appear then disappear — the
// §2.14 requirement that both surfaces are one door to one source of truth.
func TestEnableToolDualSurface(t *testing.T) {
	root := t.TempDir()
	h := &handler{repoRoot: func() (string, error) { return root, nil }}
	marker := filepath.Join(root, ".punt-labs", "beadle", "enabled")
	host := filepath.Join(root, "CLAUDE.md")

	res, err := h.enable(context.Background(), enableReq("enable"))
	require.NoError(t, err)
	require.False(t, res.IsError, "enable must not error")
	assert.True(t, hostExists(t, marker), "enable writes the marker")
	data, readErr := os.ReadFile(host)
	require.NoError(t, readErr)
	assert.Equal(t, "@.punt-labs/beadle/CLAUDE.md\n", string(data), "enable adds the import")

	res, err = h.enable(context.Background(), enableReq("disable"))
	require.NoError(t, err)
	require.False(t, res.IsError, "disable must not error")
	assert.False(t, hostExists(t, marker), "disable removes the marker")
	assert.False(t, hostExists(t, host), "disable prunes the import, emptying the created file")
}

func TestEnableToolRejectsBadAction(t *testing.T) {
	root := t.TempDir()
	h := &handler{repoRoot: func() (string, error) { return root, nil }}

	tests := []string{"", "enabled", "on", "true"}
	for _, action := range tests {
		t.Run("action="+action, func(t *testing.T) {
			res, err := h.enable(context.Background(), enableReq(action))
			require.NoError(t, err)
			assert.True(t, res.IsError, "a non-enum action is rejected")
			assert.False(t, hostExists(t, filepath.Join(root, ".punt-labs", "beadle", "enabled")),
				"a rejected action writes nothing")
		})
	}
}

func TestEnableToolReportsRepoRootError(t *testing.T) {
	h := &handler{repoRoot: func() (string, error) { return "", assert.AnError }}
	res, err := h.enable(context.Background(), enableReq("enable"))
	require.NoError(t, err)
	assert.True(t, res.IsError, "an unresolved repo root surfaces as a tool error, not a panic")
}
