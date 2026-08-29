package mcp_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/punt-labs/beadle/internal/paths"
)

// TestSwitchIdentity_WarnsWhenIdentityConfigAbsent proves the switch_identity
// preflight warns against paths.IdentityConfigPath(id.Email) — the same path
// email.LoadIdentityConfig consults — rather than a hand-rolled duplicate of
// that layout that could silently drift from it.
func TestSwitchIdentity_WarnsWhenIdentityConfigAbsent(t *testing.T) {
	s, env, _ := setupHandler(t)
	env.AddIdentity("sam", "Sam Jackson", "sam@test.com")

	expectedPath, err := paths.IdentityConfigPath("sam@test.com")
	require.NoError(t, err)

	r := callTool(t, s, "switch_identity", map[string]any{"handle": "sam"})
	assert.False(t, r.IsError, "switch failed: %s", r.text())
	assert.Contains(t, r.text(), "WARNING")
	assert.Contains(t, r.text(), expectedPath)
}

// TestSwitchIdentity_NoWarningWhenIdentityConfigPresent proves the preflight
// is silent once the target identity has its own email config.
func TestSwitchIdentity_NoWarningWhenIdentityConfigPresent(t *testing.T) {
	s, env, fix := setupHandler(t)
	env.AddIdentity("sam", "Sam Jackson", "sam@test.com")
	env.WriteConfigForIdentity("sam@test.com", fix.Config)

	r := callTool(t, s, "switch_identity", map[string]any{"handle": "sam"})
	assert.False(t, r.IsError, "switch failed: %s", r.text())
	assert.NotContains(t, r.text(), "WARNING")
}

// TestSwitchIdentity_DataDirFailureDoesNotPanic proves a paths.DataDir()
// failure inside the preflight (surfaced through paths.IdentityConfigPath)
// is handled, not silently discarded and then dereferenced — the switch
// itself must still succeed and report, never panic, on the environment
// failure. Regression guard for beadleDir, _ := paths.DataDir().
func TestSwitchIdentity_DataDirFailureDoesNotPanic(t *testing.T) {
	s, env, _ := setupHandler(t)
	env.AddIdentity("sam", "Sam Jackson", "sam@test.com")

	t.Setenv("HOME", "")

	var r toolResult
	require.NotPanics(t, func() {
		r = callTool(t, s, "switch_identity", map[string]any{"handle": "sam"})
	})
	assert.False(t, r.IsError, "switch itself must still succeed: %s", r.text())
	assert.NotContains(t, r.text(), "WARNING", "no path could be resolved to check, so no warning is issued")
}
