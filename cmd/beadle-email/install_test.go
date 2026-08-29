package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/punt-labs/beadle/internal/paths"
)

// TestInstallCmd_DoctorStepReportsRootConfigWhenNoIdentity is a
// characterization test: it proves install's doctor step (step 4), which
// invokes doctorCmd with no flag set, reports on the root config it just
// ensured exists when no identity resolves. Its sibling below proves the
// half of this composed behavior that actually discriminates: that install's
// doctor step, like a bare `doctor` invocation, prefers an identity-scoped
// config over the root one when both exist.
func TestInstallCmd_DoctorStepReportsRootConfigWhenNoIdentity(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dataDir, err := paths.DataDir()
	require.NoError(t, err)
	configPath := filepath.Join(dataDir, "email.json")
	writeConfigFixture(t, configPath, "install@test.com")

	// doctorCmd's -c/--config default is baked in at package init, from
	// whatever HOME was set when this test binary started — not the fake
	// HOME t.Setenv just installed. A real process never sees this gap
	// (HOME is fixed for its whole lifetime), so reproduce a fresh process's
	// default here: set the flag's value directly (not via FlagSet.Set,
	// which would also mark it Changed and take the explicit-flag branch
	// instead of the fallback branch this test exercises). Register the
	// restore before mutating and before runRootCmd's own snapshot, so
	// t.Cleanup's LIFO order restores runRootCmd's snapshot first and this
	// pristine one last, leaving no leaked state for later tests.
	cfgFlag := doctorCmd.Flags().Lookup("config")
	t.Cleanup(snapshotConfigFlag(t, doctorCmd))
	require.NoError(t, cfgFlag.Value.Set(configPath))

	out, _ := runRootCmd(t, "install")
	checks := doctorChecks(t, out)

	cfgCheck, ok := findCheck(checks, "config")
	require.True(t, ok, "install's doctor step must report a config check")
	assert.Equal(t, "OK", cfgCheck.Status)
	assert.Equal(t, configPath, cfgCheck.Detail,
		"with no identity resolved, install's doctor step must report the root config it just ensured exists")
}

// TestInstallCmd_DoctorStepMatchesBareDoctorWhenIdentityConfigExists proves
// install's doctor step defers to the identity-scoped config when one exists
// and loads cleanly, exactly like a bare `doctor` invocation run immediately
// afterward — install and doctor must never disagree about which config is
// in effect. install's doctor step inherits this precedence entirely from
// doctorCmd's own loadConfigForCmd; install sets no flag and adds no logic
// of its own, so this test is a composition proof, not a guard against
// install-specific behavior.
func TestInstallCmd_DoctorStepMatchesBareDoctorWhenIdentityConfigExists(t *testing.T) {
	setupDefaultIdentityHome(t, "agent@test.com")

	idConfigPath, err := paths.IdentityConfigPath("agent@test.com")
	require.NoError(t, err)
	writeConfigFixture(t, idConfigPath, "identity@test.com")

	dataDir, err := paths.DataDir()
	require.NoError(t, err)
	rootConfigPath := filepath.Join(dataDir, "email.json")
	writeConfigFixture(t, rootConfigPath, "install@test.com")

	installOut, _ := runRootCmd(t, "install")
	installCfgCheck, ok := findCheck(doctorChecks(t, installOut), "config")
	require.True(t, ok, "install's doctor step must report a config check")
	assert.Equal(t, "OK", installCfgCheck.Status)
	assert.Equal(t, idConfigPath, installCfgCheck.Detail,
		"install's doctor step must prefer the identity-scoped config over the root one, like doctor does")

	doctorOut, _ := runRootCmd(t, "doctor")
	doctorCfgCheck, ok := findCheck(doctorChecks(t, doctorOut), "config")
	require.True(t, ok, "doctor must report a config check")
	assert.Equal(t, installCfgCheck.Detail, doctorCfgCheck.Detail,
		"install and doctor must agree on which config is in effect")
}
