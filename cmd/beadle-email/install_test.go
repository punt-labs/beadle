package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/punt-labs/beadle/internal/paths"
)

// TestInstallCmd_DoctorStepReportsJustWrittenConfig proves install's doctor
// step (step 4) reports on the config path install just wrote/selected, not
// on an identity-scoped config that happens to also exist. Before the fix,
// doctorConfig was assigned directly (doctorConfig = configPath) without
// registering the -c/--config flag as Changed, so loadConfigForCmd's
// explicit-config gate never saw it and doctor silently fell back to the
// identity-scoped config instead of the config install just printed
// "wrote %s" for.
func TestInstallCmd_DoctorStepReportsJustWrittenConfig(t *testing.T) {
	setupDefaultIdentityHome(t, "agent@test.com")

	idConfigPath, err := paths.IdentityConfigPath("agent@test.com")
	require.NoError(t, err)
	writeConfigFixture(t, idConfigPath, "identity@test.com")

	dataDir, err := paths.DataDir()
	require.NoError(t, err)
	configPath := filepath.Join(dataDir, "email.json")
	writeConfigFixture(t, configPath, "install@test.com")

	out, _ := runRootCmd(t, "install")
	checks := doctorChecks(t, out)

	cfgCheck, ok := findCheck(checks, "config")
	require.True(t, ok, "install's doctor step must report a config check")
	assert.Equal(t, "OK", cfgCheck.Status)
	assert.Equal(t, configPath, cfgCheck.Detail,
		"install's doctor step must report the config it just wrote, not the identity-scoped one")
}
