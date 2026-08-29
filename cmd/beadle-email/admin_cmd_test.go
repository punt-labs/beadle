package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/punt-labs/beadle/internal/identity"
	"github.com/punt-labs/beadle/internal/paths"
)

// writeConfigFixture writes a minimal valid email.json config at path,
// creating parent directories as needed.
func writeConfigFixture(t *testing.T, path, imapUser string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(`{"imap_host":"127.0.0.1","imap_user":"`+imapUser+`"}`), 0o600))
}

// configFlagCmd returns a bare cobra.Command carrying only the -c/--config
// flag loadConfigForCmd inspects, so its Changed() state is tested in
// isolation from the doctor/status command singletons.
func configFlagCmd(t *testing.T) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().StringP("config", "c", "", "Config file path")
	return cmd
}

// TestLoadConfigForCmd_IdentityWinsWhenNotExplicit proves the default
// precedence: with no explicit -c, the identity-scoped config is preferred
// over fallbackPath when identity resolution succeeded and that config
// loads.
func TestLoadConfigForCmd_IdentityWinsWhenNotExplicit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	id := &identity.Identity{Email: "agent@test.com"}
	idConfigPath, err := paths.IdentityConfigPath(id.Email)
	require.NoError(t, err)
	writeConfigFixture(t, idConfigPath, "identity@test.com")

	fallbackPath := filepath.Join(home, "fallback-email.json")
	writeConfigFixture(t, fallbackPath, "fallback@test.com")

	cmd := configFlagCmd(t)
	cfg, usedPath, err := loadConfigForCmd(cmd, id, nil, fallbackPath)
	require.NoError(t, err)
	assert.Equal(t, "identity@test.com", cfg.IMAPUser)
	assert.Equal(t, idConfigPath, usedPath)
}

// TestLoadConfigForCmd_IdErrIgnoresIdentityEvenIfNonNil proves the nil-hazard
// fix: a caller must not pass a stale/zero-value id through when idErr is
// non-nil. loadConfigForCmd enforces this itself by only forwarding id to
// LoadIdentityConfig when idErr is nil — passing a non-nil id alongside a
// non-nil idErr must still fall back, never dereference the unresolved id.
func TestLoadConfigForCmd_IdErrIgnoresIdentityEvenIfNonNil(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// A populated id that would win if consulted — it must not be.
	id := &identity.Identity{Email: "agent@test.com"}
	idConfigPath, err := paths.IdentityConfigPath(id.Email)
	require.NoError(t, err)
	writeConfigFixture(t, idConfigPath, "identity@test.com")

	fallbackPath := filepath.Join(home, "fallback-email.json")
	writeConfigFixture(t, fallbackPath, "fallback@test.com")

	cmd := configFlagCmd(t)
	cfg, usedPath, err := loadConfigForCmd(cmd, id, errors.New("resolution failed"), fallbackPath)
	require.NoError(t, err)
	assert.Equal(t, "fallback@test.com", cfg.IMAPUser)
	assert.Equal(t, fallbackPath, usedPath)
}

// TestLoadConfigForCmd_ExplicitFlagWinsOverIdentity proves an explicit
// -c/--config always wins, skipping identity-config lookup entirely, even
// when a valid identity config exists.
func TestLoadConfigForCmd_ExplicitFlagWinsOverIdentity(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	id := &identity.Identity{Email: "agent@test.com"}
	idConfigPath, err := paths.IdentityConfigPath(id.Email)
	require.NoError(t, err)
	writeConfigFixture(t, idConfigPath, "identity@test.com")

	explicitPath := filepath.Join(home, "explicit-email.json")
	writeConfigFixture(t, explicitPath, "explicit@test.com")

	cmd := configFlagCmd(t)
	require.NoError(t, cmd.Flags().Set("config", explicitPath))

	cfg, usedPath, err := loadConfigForCmd(cmd, id, nil, explicitPath)
	require.NoError(t, err)
	assert.Equal(t, "explicit@test.com", cfg.IMAPUser)
	assert.Equal(t, explicitPath, usedPath)
}

// TestLoadConfigForCmd_FallsBackWhenIdentityConfigAbsent proves the absent
// (not corrupt) case still falls back cleanly.
func TestLoadConfigForCmd_FallsBackWhenIdentityConfigAbsent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	id := &identity.Identity{Email: "agent@test.com"}
	fallbackPath := filepath.Join(home, "fallback-email.json")
	writeConfigFixture(t, fallbackPath, "fallback@test.com")

	cmd := configFlagCmd(t)
	cfg, usedPath, err := loadConfigForCmd(cmd, id, nil, fallbackPath)
	require.NoError(t, err)
	assert.Equal(t, "fallback@test.com", cfg.IMAPUser)
	assert.Equal(t, fallbackPath, usedPath)
}

// --- doctorCmd / statusCmd wiring-level tests ---
//
// These drive the commands through rootCmd.Execute(), the real CLI entry
// point, rather than calling loadConfigForCmd directly — reverting
// doctorCmd or statusCmd to always load DefaultConfigPath() (the original
// reported bug) fails these even though loadConfigForCmd itself would still
// pass.

// setupDefaultIdentityHome creates a temp HOME with a beadle default-identity
// file naming emailAddr, isolating identity resolution from ethos state on
// the real machine (no repo-local .punt-labs/ethos.yaml is reachable from
// the test binary's working directory, and the fake HOME has no global
// ethos "active" file, so resolution falls through to this default file).
func setupDefaultIdentityHome(t *testing.T, emailAddr string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	beadleDir := filepath.Join(home, ".punt-labs", "beadle")
	require.NoError(t, os.MkdirAll(beadleDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(beadleDir, "default-identity"), []byte(emailAddr+"\n"), 0o600))
	return home
}

// snapshotConfigFlag captures a command's -c/--config flag value and Changed
// state right now, and returns a func restoring exactly that captured state.
// Callers register the returned func with t.Cleanup before running the
// command, so an earlier test's flag mutation never leaks into a later test
// sharing the package-level doctorCmd/statusCmd singletons.
//
// This must snapshot rather than recompute email.DefaultConfigPath() at
// cleanup time: t.Cleanup runs LIFO, so a cleanup registered after a test's
// own t.Setenv("HOME", ...) cleanup fires BEFORE it — recomputing the
// default there resolves it under the test's about-to-be-removed temp HOME
// and leaks that soon-to-be-deleted path into the package-level flag for
// whichever test runs next.
func snapshotConfigFlag(t *testing.T, cmd *cobra.Command) func() {
	t.Helper()
	f := cmd.Flags().Lookup("config")
	require.NotNil(t, f)
	value := f.Value.String()
	changed := f.Changed
	return func() {
		require.NoError(t, f.Value.Set(value))
		f.Changed = changed
	}
}

// captureStdout redirects os.Stdout for the duration of fn and returns what
// was written.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	defer func() { os.Stdout = old }()
	fn()
	require.NoError(t, w.Close())
	var buf bytes.Buffer
	_, err = io.Copy(&buf, r)
	require.NoError(t, err)
	return buf.String()
}

// runRootCmd executes rootCmd with args, capturing JSON stdout and the
// command's error. Restores the JSON global flag and the doctor/status
// -c/--config flags afterward, so a caller that needs Execute's error (e.g. a
// fail-closed assertion) never has to reach past this helper into rootCmd's
// package-level state directly.
func runRootCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	defer func(prev bool) { g.JSON = prev }(g.JSON)
	t.Cleanup(snapshotConfigFlag(t, doctorCmd))
	t.Cleanup(snapshotConfigFlag(t, statusCmd))

	rootCmd.SetArgs(append(args, "--json"))
	var err error
	out := captureStdout(t, func() {
		err = rootCmd.Execute() // doctor/status may legitimately return a non-nil error (FAIL checks)
	})
	return out, err
}

func doctorChecks(t *testing.T, jsonOut string) []doctorCheck {
	t.Helper()
	var checks []doctorCheck
	require.NoError(t, json.Unmarshal([]byte(jsonOut), &checks))
	return checks
}

func findCheck(checks []doctorCheck, name string) (doctorCheck, bool) {
	for _, c := range checks {
		if c.Name == name {
			return c, true
		}
	}
	return doctorCheck{}, false
}

func TestDoctorCmd_UsesIdentityScopedConfigOverDefault(t *testing.T) {
	setupDefaultIdentityHome(t, "agent@test.com")

	idConfigPath, err := paths.IdentityConfigPath("agent@test.com")
	require.NoError(t, err)
	writeConfigFixture(t, idConfigPath, "identity@test.com")

	out, _ := runRootCmd(t, "doctor")
	checks := doctorChecks(t, out)

	cfgCheck, ok := findCheck(checks, "config")
	require.True(t, ok, "doctor must report a config check")
	assert.Equal(t, "OK", cfgCheck.Status)
	assert.Equal(t, idConfigPath, cfgCheck.Detail,
		"doctor must use the identity-scoped config, not DefaultConfigPath()")
}

func TestStatusCmd_UsesIdentityScopedConfigOverDefault(t *testing.T) {
	setupDefaultIdentityHome(t, "agent@test.com")

	idConfigPath, err := paths.IdentityConfigPath("agent@test.com")
	require.NoError(t, err)
	writeConfigFixture(t, idConfigPath, "identity@test.com")

	out, _ := runRootCmd(t, "status")
	var status map[string]string
	require.NoError(t, json.Unmarshal([]byte(out), &status))

	assert.Equal(t, idConfigPath, status["config"],
		"status must use the identity-scoped config, not DefaultConfigPath()")
	assert.Equal(t, "identity@test.com", status["imap_user"])
}

func TestDoctorCmd_ExplicitConfigFlagOverridesIdentity(t *testing.T) {
	home := setupDefaultIdentityHome(t, "agent@test.com")

	idConfigPath, err := paths.IdentityConfigPath("agent@test.com")
	require.NoError(t, err)
	writeConfigFixture(t, idConfigPath, "identity@test.com")

	explicitPath := filepath.Join(home, "explicit-email.json")
	writeConfigFixture(t, explicitPath, "explicit@test.com")

	out, _ := runRootCmd(t, "doctor", "-c", explicitPath)
	checks := doctorChecks(t, out)

	cfgCheck, ok := findCheck(checks, "config")
	require.True(t, ok)
	assert.Equal(t, "OK", cfgCheck.Status)
	assert.Equal(t, explicitPath, cfgCheck.Detail,
		"an explicit -c must win over the identity-scoped config")
}

func TestStatusCmd_ExplicitConfigFlagOverridesIdentity(t *testing.T) {
	home := setupDefaultIdentityHome(t, "agent@test.com")

	idConfigPath, err := paths.IdentityConfigPath("agent@test.com")
	require.NoError(t, err)
	writeConfigFixture(t, idConfigPath, "identity@test.com")

	explicitPath := filepath.Join(home, "explicit-email.json")
	writeConfigFixture(t, explicitPath, "explicit@test.com")

	out, _ := runRootCmd(t, "status", "-c", explicitPath)
	var status map[string]string
	require.NoError(t, json.Unmarshal([]byte(out), &status))

	assert.Equal(t, explicitPath, status["config"])
	assert.Equal(t, "explicit@test.com", status["imap_user"])
}

func TestDoctorCmd_FailsClosedOnCorruptIdentityConfig(t *testing.T) {
	home := setupDefaultIdentityHome(t, "agent@test.com")

	idConfigPath, err := paths.IdentityConfigPath("agent@test.com")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(idConfigPath), 0o700))
	require.NoError(t, os.WriteFile(idConfigPath, []byte(`{not json`), 0o600))

	// A fallback the corrupt identity config must NOT silently fall back to.
	fallbackPath := filepath.Join(home, ".punt-labs", "beadle", "email.json")
	writeConfigFixture(t, fallbackPath, "fallback@test.com")

	out, _ := runRootCmd(t, "doctor")
	checks := doctorChecks(t, out)

	cfgCheck, ok := findCheck(checks, "config")
	require.True(t, ok)
	assert.Equal(t, "FAIL", cfgCheck.Status,
		"a corrupt identity config must fail the config check, never report OK against a fallback")
}

func TestStatusCmd_FailsClosedOnCorruptIdentityConfig(t *testing.T) {
	home := setupDefaultIdentityHome(t, "agent@test.com")

	idConfigPath, err := paths.IdentityConfigPath("agent@test.com")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(idConfigPath), 0o700))
	require.NoError(t, os.WriteFile(idConfigPath, []byte(`{not json`), 0o600))

	fallbackPath := filepath.Join(home, ".punt-labs", "beadle", "email.json")
	writeConfigFixture(t, fallbackPath, "fallback@test.com")

	_, execErr := runRootCmd(t, "status")
	assert.Error(t, execErr, "status must fail closed on a corrupt identity config, not report the fallback")
}
