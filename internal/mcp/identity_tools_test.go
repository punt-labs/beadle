package mcp_test

import (
	"os"
	"path/filepath"
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

// TestSwitchIdentity_DataDirFailureDoesNotPanic is a characterization test
// pinning current fail-clean behavior: a paths.DataDir() failure inside the
// preflight (surfaced through paths.IdentityConfigPath) is handled, not
// silently discarded and then dereferenced — the switch itself still
// succeeds and reports, never panics, on the environment failure. Verified
// (by restoring the pre-fix preflight in a scratch worktree) to already pass
// unchanged against the pre-fix code — the preflight already guarded on a
// HOME/DataDir failure before ever reaching the line this fix changed. Kept
// as a guard on real, current behavior.
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

// TestSwitchIdentity_StatErrorOtherThanNotExistIsNotMisreportedAsAbsent
// proves the preflight distinguishes "no config" from any other os.Stat
// failure: a non-ENOENT error (here, a path component that is a file instead
// of a directory) must not produce the "no email config" warning, since the
// config's actual absence was never established.
func TestSwitchIdentity_StatErrorOtherThanNotExistIsNotMisreportedAsAbsent(t *testing.T) {
	s, env, _ := setupHandler(t)
	env.AddIdentity("sam", "Sam Jackson", "sam@test.com")

	configPath, err := paths.IdentityConfigPath("sam@test.com")
	require.NoError(t, err)
	// Replace the identity directory with a file, so stat-ing configPath
	// (a path through it) fails with ENOTDIR, not ENOENT.
	identityDir := filepath.Dir(configPath)
	require.NoError(t, os.RemoveAll(identityDir))
	require.NoError(t, os.WriteFile(identityDir, []byte("not a directory"), 0o600))

	r := callTool(t, s, "switch_identity", map[string]any{"handle": "sam"})
	assert.False(t, r.IsError, "switch itself must still succeed: %s", r.text())
	assert.NotContains(t, r.text(), "WARNING",
		"a non-ENOENT stat error must not be misreported as 'no email config'")
}
