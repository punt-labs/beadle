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
		name       string
		state      pluginState
		standalone bool
		want       mcpDecision
	}{
		{"enabled plugin, no opt-in -> plugin provides", pluginEnabled, false, mcpPluginProvides},
		{"absent plugin, no opt-in -> advise install", pluginAbsent, false, mcpAdviseInstall},
		{"disabled plugin, no opt-in -> advise install", pluginDisabled, false, mcpAdviseInstall},
		{"unknown plugin, no opt-in -> advise install", pluginUnknown, false, mcpAdviseInstall},
		{"absent plugin, --standalone -> register standalone", pluginAbsent, true, mcpRegisterStandalone},
		{"disabled plugin, --standalone -> register standalone (legitimate)", pluginDisabled, true, mcpRegisterStandalone},
		{"enabled plugin, --standalone -> warn duplicate then register", pluginEnabled, true, mcpRegisterStandaloneWarnDuplicate},
		{"unknown plugin, --standalone -> warn duplicate (query failed, don't silently skip)", pluginUnknown, true, mcpRegisterStandaloneWarnDuplicate},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, decideMCP(tt.state, tt.standalone))
		})
	}
}

func TestDuplicateWarning(t *testing.T) {
	t.Run("enabled plugin -> already provides", func(t *testing.T) {
		w := duplicateWarning(pluginEnabled)
		assert.Contains(t, w, "already provides")
	})
	t.Run("unknown plugin -> could not confirm", func(t *testing.T) {
		w := duplicateWarning(pluginUnknown)
		assert.Contains(t, w, "could not confirm")
	})
}

func TestBeadlePluginState(t *testing.T) {
	enabled := `Installed plugins:

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
		want   pluginState
	}{
		{"beadle installed and enabled", enabled, pluginEnabled},
		{"beadle installed but disabled", disabled, pluginDisabled},
		{"beadle absent", absent, pluginAbsent},
		{"empty output", "", pluginAbsent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, beadlePluginState(tt.output))
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
	t.Run("enabled plugin + standalone -> WARN, remedy always -s user", func(t *testing.T) {
		c := mcpRegistrationCheck(pluginEnabled, true)
		assert.Equal(t, "WARN", c.Status)
		assert.Contains(t, c.Detail, "coexists")
		assert.Contains(t, c.Detail, "remove -s user")
		// The coexistence check never advises -s project: the standalone signal
		// is the user-resolved mcp-list entry. Project scope is a SEPARATE check.
		assert.NotContains(t, c.Detail, "-s project")
	})

	t.Run("enabled plugin, no standalone -> OK", func(t *testing.T) {
		c := mcpRegistrationCheck(pluginEnabled, false)
		assert.Equal(t, "OK", c.Status)
		assert.Contains(t, c.Detail, "plugin provides")
	})

	t.Run("disabled plugin + standalone -> OK, says installed-but-disabled (not 'not installed')", func(t *testing.T) {
		c := mcpRegistrationCheck(pluginDisabled, true)
		assert.Equal(t, "OK", c.Status)
		assert.Contains(t, c.Detail, "disabled")
		assert.NotContains(t, c.Detail, "not installed")
	})

	t.Run("disabled plugin, no standalone -> OK, says installed-but-disabled", func(t *testing.T) {
		c := mcpRegistrationCheck(pluginDisabled, false)
		assert.Equal(t, "OK", c.Status)
		assert.Contains(t, c.Detail, "disabled")
	})

	t.Run("absent plugin, standalone only -> OK", func(t *testing.T) {
		c := mcpRegistrationCheck(pluginAbsent, true)
		assert.Equal(t, "OK", c.Status)
		assert.Contains(t, c.Detail, "plugin not installed")
	})

	t.Run("nothing registered -> OK", func(t *testing.T) {
		c := mcpRegistrationCheck(pluginAbsent, false)
		assert.Equal(t, "OK", c.Status)
		assert.Contains(t, c.Detail, "no beadle MCP registration")
	})
}

// TestObservationsIndependent proves the three doctor observations do not share
// signals: a coexisting enabled plugin + standalone AND a project-scope
// .mcp.json produce a user-scope coexistence remedy and a distinct project-scope
// remedy — neither rewrites the other.
func TestObservationsIndependent(t *testing.T) {
	reg := mcpRegistrationCheck(pluginEnabled, true)
	scope := projectScopeCheck("/ws/.mcp.json", nil)

	assert.Equal(t, "mcp_registration", reg.Name)
	assert.Contains(t, reg.Detail, "remove -s user")
	assert.NotContains(t, reg.Detail, "-s project")

	require.NotNil(t, scope)
	assert.Equal(t, "mcp_scope", scope.Name)
	assert.Contains(t, scope.Detail, "remove -s project")
	assert.Contains(t, scope.Detail, "/ws/.mcp.json")
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
