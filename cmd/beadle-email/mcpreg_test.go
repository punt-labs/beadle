package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
		{"plugin present, --standalone -> explicit opt-in wins", true, true, mcpRegisterStandalone},
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
	tests := []struct {
		name   string
		output string
		want   bool
	}{
		{"beadle listed", installed, true},
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

func TestProjectScopeRegistration(t *testing.T) {
	project := `beadle-email:
  Scope: Project config (shared via .mcp.json)
  Status: ✔ Connected
`
	user := `beadle-email:
  Scope: User config
  Status: ✔ Connected
`
	tests := []struct {
		name   string
		output string
		want   bool
	}{
		{"project scope", project, true},
		{"user scope", user, false},
		{"empty output (server absent)", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, projectScopeRegistration(tt.output))
		})
	}
}

func TestMCPDriftChecks(t *testing.T) {
	pluginList := "  ❯ beadle@punt-labs\n"
	noPlugin := "  ❯ biff@punt-labs\n"
	standaloneList := "plugin:biff:tty: biff mcp - ✔ Connected\nbeadle-email: /x/beadle-email serve - ✔ Connected\n"
	pluginServerList := "plugin:beadle:email: beadle-email serve - ✔ Connected\n"
	projectGet := "beadle-email:\n  Scope: Project config (shared via .mcp.json)\n"
	userGet := "beadle-email:\n  Scope: User config\n"

	find := func(checks []doctorCheck, name string) (doctorCheck, bool) {
		for _, c := range checks {
			if c.Name == name {
				return c, true
			}
		}
		return doctorCheck{}, false
	}

	t.Run("standalone coexists with plugin -> WARN", func(t *testing.T) {
		checks := mcpDriftChecks(pluginList, standaloneList, userGet)
		c, ok := find(checks, "mcp_registration")
		assert.True(t, ok)
		assert.Equal(t, "WARN", c.Status)
		assert.Contains(t, c.Detail, "coexists")
	})

	t.Run("plugin only, no standalone -> OK", func(t *testing.T) {
		checks := mcpDriftChecks(pluginList, pluginServerList, "")
		c, ok := find(checks, "mcp_registration")
		assert.True(t, ok)
		assert.Equal(t, "OK", c.Status)
		_, hasScope := find(checks, "mcp_scope")
		assert.False(t, hasScope, "no project-scope warning expected")
	})

	t.Run("no plugin, standalone only -> OK", func(t *testing.T) {
		checks := mcpDriftChecks(noPlugin, standaloneList, userGet)
		c, ok := find(checks, "mcp_registration")
		assert.True(t, ok)
		assert.Equal(t, "OK", c.Status)
		assert.Contains(t, c.Detail, "plugin not installed")
	})

	t.Run("nothing registered -> OK", func(t *testing.T) {
		checks := mcpDriftChecks(noPlugin, "plugin:biff:tty: biff mcp\n", "")
		c, ok := find(checks, "mcp_registration")
		assert.True(t, ok)
		assert.Equal(t, "OK", c.Status)
		assert.Contains(t, c.Detail, "no beadle MCP registration")
	})

	t.Run("project-scope registration -> WARN mcp_scope", func(t *testing.T) {
		checks := mcpDriftChecks(pluginList, standaloneList, projectGet)
		c, ok := find(checks, "mcp_scope")
		assert.True(t, ok)
		assert.Equal(t, "WARN", c.Status)
		assert.Contains(t, c.Detail, "project scope")
	})
}
