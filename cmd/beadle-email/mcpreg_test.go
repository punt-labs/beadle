package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecideMCP(t *testing.T) {
	tests := []struct {
		name          string
		pluginPresent bool
		standalone    bool
		want          mcpDecision
	}{
		{"plugin present, no opt-in -> plugin provides", true, false, mcpPluginProvides},
		{"no plugin, no opt-in -> advise install", false, false, mcpAdviseInstall},
		{"no plugin, --standalone -> register standalone", false, true, mcpRegisterStandalone},
		{"plugin present, --standalone -> warn about duplicate then register", true, true, mcpRegisterStandaloneWarnDuplicate},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, decideMCP(tt.pluginPresent, tt.standalone))
		})
	}
}

func TestBeadlePluginInstalled(t *testing.T) {
	installed := `Installed plugins:

  ❯ beadle@punt-labs
    Version: 0.15.0
    Scope: user
    Status: ✔ enabled

  ❯ biff@punt-labs
    Version: 1.11.2
    Scope: user
    Status: ✔ enabled
`
	absent := `Installed plugins:

  ❯ biff@punt-labs
    Version: 1.11.2
    Scope: user
    Status: ✔ enabled
`
	disabled := `Installed plugins:

  ❯ beadle@punt-labs
    Version: 0.15.0
    Scope: user
    Status: ✘ disabled

  ❯ biff@punt-labs
    Version: 1.11.2
    Scope: user
    Status: ✔ enabled
`
	tests := []struct {
		name   string
		output string
		want   bool
	}{
		{"beadle installed and enabled", installed, true},
		{"beadle installed but disabled -> not an active source", disabled, false},
		{"beadle absent", absent, false},
		{"empty output", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, beadlePluginInstalled(tt.output))
		})
	}
}

func TestStandaloneMCPRegistered(t *testing.T) {
	// The plugin server is "plugin:beadle:email"; a standalone one is
	// "beadle-email". Only the latter is a standalone duplicate.
	both := `plugin:beadle:email: beadle-email serve - ✘ Failed to connect
plugin:biff:tty: biff mcp - ✔ Connected
beadle-email: /Users/x/beadle/beadle-email serve - ✔ Connected
`
	pluginOnly := `plugin:beadle:email: beadle-email serve - ✔ Connected
plugin:biff:tty: biff mcp - ✔ Connected
`
	standaloneOnly := `beadle-email: /Users/x/.local/bin/beadle-email serve - ✔ Connected
plugin:biff:tty: biff mcp - ✔ Connected
`
	tests := []struct {
		name   string
		output string
		want   bool
	}{
		{"standalone alongside plugin", both, true},
		{"plugin server only is not standalone", pluginOnly, false},
		{"standalone only", standaloneOnly, true},
		{"empty output", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, standaloneMCPRegistered(tt.output))
		})
	}
}

func TestMCPFileDeclaresServer(t *testing.T) {
	dir := t.TempDir()

	declares := filepath.Join(dir, "declares.json")
	require.NoError(t, os.WriteFile(declares, []byte(`{"mcpServers":{"beadle-email":{"command":"beadle-email","args":["serve"]}}}`), 0o600))

	other := filepath.Join(dir, "other.json")
	require.NoError(t, os.WriteFile(other, []byte(`{"mcpServers":{"biff":{"command":"biff"}}}`), 0o600))

	malformed := filepath.Join(dir, "malformed.json")
	require.NoError(t, os.WriteFile(malformed, []byte(`{not json`), 0o600))

	t.Run("declares beadle-email", func(t *testing.T) {
		ok, err := mcpFileDeclaresServer(declares, mcpServerName)
		require.NoError(t, err)
		assert.True(t, ok)
	})
	t.Run("declares other server only", func(t *testing.T) {
		ok, err := mcpFileDeclaresServer(other, mcpServerName)
		require.NoError(t, err)
		assert.False(t, ok)
	})
	t.Run("missing file is not an error", func(t *testing.T) {
		ok, err := mcpFileDeclaresServer(filepath.Join(dir, "nope.json"), mcpServerName)
		require.NoError(t, err)
		assert.False(t, ok)
	})
	t.Run("malformed file is an error", func(t *testing.T) {
		_, err := mcpFileDeclaresServer(malformed, mcpServerName)
		require.Error(t, err)
	})
}

// isolatedTempDir returns a temp dir under /tmp, deliberately OUTSIDE the
// workspace tree. projectScopeMCPFile walks to the filesystem root, and the
// repo's TMPDIR points into the workspace .tmp/ — so a t.TempDir() root would
// sit below the real workspace .mcp.json and the up-walk would find it,
// defeating isolation. /tmp has no beadle-email .mcp.json ancestor.
func isolatedTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "beadle-mcpscan-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestProjectScopeMCPFile(t *testing.T) {
	t.Run("no .mcp.json anywhere -> empty", func(t *testing.T) {
		start := isolatedTempDir(t)
		got, err := projectScopeMCPFile(start)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	// The shadowing case: a project-scope entry lives in a .mcp.json at the
	// workspace root, and a user-scope entry (in ~/.claude.json, which this scan
	// never reads) would shadow it in `claude mcp get`. The filesystem scan
	// finds the project entry regardless, walking up from a nested working dir.
	t.Run("project entry found up-tree even when user scope would shadow it", func(t *testing.T) {
		root := isolatedTempDir(t)
		mcpPath := filepath.Join(root, ".mcp.json")
		require.NoError(t, os.WriteFile(mcpPath, []byte(`{"mcpServers":{"beadle-email":{"command":"beadle-email","args":["serve"]}}}`), 0o600))

		nested := filepath.Join(root, "repo", "cmd", "beadle-email")
		require.NoError(t, os.MkdirAll(nested, 0o750))

		got, err := projectScopeMCPFile(nested)
		require.NoError(t, err)
		assert.Equal(t, mcpPath, got)
	})

	t.Run(".mcp.json without beadle-email -> empty", func(t *testing.T) {
		root := isolatedTempDir(t)
		require.NoError(t, os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(`{"mcpServers":{"biff":{"command":"biff"}}}`), 0o600))
		got, err := projectScopeMCPFile(root)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("malformed .mcp.json surfaces an error", func(t *testing.T) {
		root := isolatedTempDir(t)
		require.NoError(t, os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(`{bad`), 0o600))
		_, err := projectScopeMCPFile(root)
		require.Error(t, err)
	})
}

func TestMCPRegistrationCheck(t *testing.T) {
	pluginList := "  ❯ beadle@punt-labs\n    Status: ✔ enabled\n"
	noPlugin := "  ❯ biff@punt-labs\n    Status: ✔ enabled\n"
	standaloneList := "plugin:biff:tty: biff mcp - ✔ Connected\nbeadle-email: /x/beadle-email serve - ✔ Connected\n"
	pluginServerList := "plugin:beadle:email: beadle-email serve - ✔ Connected\n"

	t.Run("coexistence, user scope -> WARN with -s user remedy", func(t *testing.T) {
		c := mcpRegistrationCheck(pluginList, standaloneList, "")
		assert.Equal(t, "WARN", c.Status)
		assert.Contains(t, c.Detail, "coexists")
		assert.Contains(t, c.Detail, "remove -s user")
	})

	t.Run("coexistence, project scope -> WARN with -s project remedy and path", func(t *testing.T) {
		c := mcpRegistrationCheck(pluginList, standaloneList, "/ws/.mcp.json")
		assert.Equal(t, "WARN", c.Status)
		assert.Contains(t, c.Detail, "remove -s project")
		assert.Contains(t, c.Detail, "/ws/.mcp.json")
	})

	t.Run("plugin only, no standalone -> OK", func(t *testing.T) {
		c := mcpRegistrationCheck(pluginList, pluginServerList, "")
		assert.Equal(t, "OK", c.Status)
	})

	t.Run("no plugin, standalone only -> OK", func(t *testing.T) {
		c := mcpRegistrationCheck(noPlugin, standaloneList, "")
		assert.Equal(t, "OK", c.Status)
		assert.Contains(t, c.Detail, "plugin not installed")
	})

	t.Run("disabled plugin + standalone -> OK (not an active duplicate)", func(t *testing.T) {
		disabledPlugin := "  ❯ beadle@punt-labs\n    Status: ✘ disabled\n"
		c := mcpRegistrationCheck(disabledPlugin, standaloneList, "")
		assert.Equal(t, "OK", c.Status)
		assert.Contains(t, c.Detail, "plugin not installed")
	})

	t.Run("nothing registered -> OK", func(t *testing.T) {
		c := mcpRegistrationCheck(noPlugin, "plugin:biff:tty: biff mcp\n", "")
		assert.Equal(t, "OK", c.Status)
		assert.Contains(t, c.Detail, "no beadle MCP registration")
	})
}

func TestProjectScopeCheck(t *testing.T) {
	t.Run("project file found -> WARN naming file and -s project", func(t *testing.T) {
		c := projectScopeCheck("/ws/.mcp.json", nil)
		require.NotNil(t, c)
		assert.Equal(t, "mcp_scope", c.Name)
		assert.Equal(t, "WARN", c.Status)
		assert.Contains(t, c.Detail, "/ws/.mcp.json")
		assert.Contains(t, c.Detail, "remove -s project")
	})

	t.Run("scan error -> WARN, never silent", func(t *testing.T) {
		c := projectScopeCheck("", assert.AnError)
		require.NotNil(t, c)
		assert.Equal(t, "WARN", c.Status)
		assert.Contains(t, c.Detail, "cannot determine MCP project scope")
	})

	t.Run("no project entry -> nil", func(t *testing.T) {
		assert.Nil(t, projectScopeCheck("", nil))
	})
}
