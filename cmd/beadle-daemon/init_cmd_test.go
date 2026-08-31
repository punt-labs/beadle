package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/punt-labs/beadle/internal/identity"
	"github.com/punt-labs/beadle/internal/testenv"
)

// writeTestIdentity writes a minimal ethos identity + beadle extension
// under ethosDir, resolvable by identity.Resolver.ResolveHandle(handle).
func writeTestIdentity(t *testing.T, ethosDir, handle, gpgKeyID string) {
	t.Helper()
	idDir := filepath.Join(ethosDir, "identities")
	require.NoError(t, os.MkdirAll(idDir, 0o750))
	idYAML := "handle: " + handle + "\nname: Test\nemail: " + handle + "@example.com\n"
	require.NoError(t, os.WriteFile(filepath.Join(idDir, handle+".yaml"), []byte(idYAML), 0o600))

	if gpgKeyID != "" {
		extDir := filepath.Join(idDir, handle+".ext")
		require.NoError(t, os.MkdirAll(extDir, 0o750))
		extYAML := "gpg_key_id: " + gpgKeyID + "\n"
		require.NoError(t, os.WriteFile(filepath.Join(extDir, "beadle.yaml"), []byte(extYAML), 0o600))
	}
}

// testFingerprint names a key in no keyring -- valid 40-hex shape, never
// imported anywhere. Tests that need runInit to succeed WITHOUT a real,
// usable key pass --no-verify-key explicitly; tests that need to prove
// init's own key-usability probe use a real generated key instead (see
// TestRunInit_HandleHappyPath, TestRunInit_FingerprintHappyPath,
// TestRunInit_RejectsKeyAbsentFromKeyring).
const testFingerprint = "0123456789ABCDEF0123456789ABCDEF01234567"

func TestRunInit_HandleHappyPath(t *testing.T) {
	gpgBin := gpgBinary(t)
	home := testenv.ShortGPGHome(t)
	t.Setenv("GNUPGHOME", home)
	fpr := testenv.GenOwnerKey(t, gpgBin, home, "init-handle@example.com", "1y")

	dataDir := t.TempDir()
	ethosDir := t.TempDir()
	writeTestIdentity(t, ethosDir, "operator", fpr)
	resolver := identity.NewResolver(ethosDir, dataDir, "")

	var out bytes.Buffer
	err := runInit(&out, dataDir, resolver, "operator", "", gpgBin, false, false)
	require.NoError(t, err)
	assert.Contains(t, out.String(), fpr)

	data, err := os.ReadFile(filepath.Join(dataDir, "daemon.json"))
	require.NoError(t, err)
	var cfg struct {
		OwnerHandle string `json:"owner_handle"`
	}
	require.NoError(t, json.Unmarshal(data, &cfg))
	assert.Equal(t, "operator", cfg.OwnerHandle)
}

func TestRunInit_FingerprintHappyPath(t *testing.T) {
	gpgBin := gpgBinary(t)
	home := testenv.ShortGPGHome(t)
	t.Setenv("GNUPGHOME", home)
	fpr := testenv.GenOwnerKey(t, gpgBin, home, "init-fingerprint@example.com", "1y")

	dataDir := t.TempDir()
	resolver := identity.NewResolver(t.TempDir(), dataDir, "")

	var out bytes.Buffer
	err := runInit(&out, dataDir, resolver, "", fpr, gpgBin, false, false)
	require.NoError(t, err)
	assert.Contains(t, out.String(), fpr)

	data, err := os.ReadFile(filepath.Join(dataDir, "daemon.json"))
	require.NoError(t, err)
	var cfg struct {
		OwnerGPGKeyID string `json:"owner_gpg_key_id"`
	}
	require.NoError(t, json.Unmarshal(data, &cfg))
	assert.Equal(t, fpr, cfg.OwnerGPGKeyID)
}

// TestRunInit_RejectsKeyAbsentFromKeyring is the regression test for the
// key-usability probe itself: a syntactically valid fingerprint that is
// not in any keyring must be rejected, not written -- catching a mistyped
// fingerprint at init time is exactly the gap this fix closes, since
// every recipe signed against it would otherwise fail wrong-key at daemon
// startup, days later, far from the typo.
func TestRunInit_RejectsKeyAbsentFromKeyring(t *testing.T) {
	gpgBin := gpgBinary(t)
	home := testenv.ShortGPGHome(t)
	t.Setenv("GNUPGHOME", home) // empty keyring -- testFingerprint is in no keyring here

	dataDir := t.TempDir()
	resolver := identity.NewResolver(t.TempDir(), dataDir, "")

	var out bytes.Buffer
	err := runInit(&out, dataDir, resolver, "", testFingerprint, gpgBin, false, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not usable for signing")
	assert.Contains(t, err.Error(), "--no-verify-key")

	_, statErr := os.Stat(filepath.Join(dataDir, "daemon.json"))
	assert.True(t, os.IsNotExist(statErr), "an unusable authorizer key must never be written")
}

// TestRunInit_RejectsNonExpiringKey proves the probe brings the
// non-expiring-key rejection (internal/pgp.CheckKeyExpiry) forward to init
// time, rather than surfacing it only the first time a recipe is signed.
func TestRunInit_RejectsNonExpiringKey(t *testing.T) {
	gpgBin := gpgBinary(t)
	home := testenv.ShortGPGHome(t)
	t.Setenv("GNUPGHOME", home)
	fpr := testenv.GenOwnerKey(t, gpgBin, home, "non-expiring@example.com", "0") // "0" = never expires

	dataDir := t.TempDir()
	resolver := identity.NewResolver(t.TempDir(), dataDir, "")

	var out bytes.Buffer
	err := runInit(&out, dataDir, resolver, "", fpr, gpgBin, false, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not usable for signing")

	_, statErr := os.Stat(filepath.Join(dataDir, "daemon.json"))
	assert.True(t, os.IsNotExist(statErr), "a non-expiring key must never be written as the authorizer")
}

// TestRunInit_NoVerifyKeyBypassesProbe proves the explicit escape: an
// operator configuring the key before it has been imported on this
// machine can still write daemon.json.
func TestRunInit_NoVerifyKeyBypassesProbe(t *testing.T) {
	dataDir := t.TempDir()
	resolver := identity.NewResolver(t.TempDir(), dataDir, "")

	var out bytes.Buffer
	err := runInit(&out, dataDir, resolver, "", testFingerprint, "gpg", false, true)
	require.NoError(t, err)
	assert.Contains(t, out.String(), testFingerprint)
}

func TestRunInit_RefusesToClobberWithoutForce(t *testing.T) {
	dataDir := t.TempDir()
	resolver := identity.NewResolver(t.TempDir(), dataDir, "")

	var out bytes.Buffer
	require.NoError(t, runInit(&out, dataDir, resolver, "", testFingerprint, "gpg", false, true))

	const otherFingerprint = "FEDCBA9876543210FEDCBA9876543210FEDCBA98"
	err := runInit(&out, dataDir, resolver, "", otherFingerprint, "gpg", false, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")

	// The original file must be untouched.
	data, err := os.ReadFile(filepath.Join(dataDir, "daemon.json"))
	require.NoError(t, err)
	assert.Contains(t, string(data), testFingerprint)
	assert.NotContains(t, string(data), otherFingerprint)
}

func TestRunInit_ForceOverwrites(t *testing.T) {
	dataDir := t.TempDir()
	resolver := identity.NewResolver(t.TempDir(), dataDir, "")

	var out bytes.Buffer
	require.NoError(t, runInit(&out, dataDir, resolver, "", testFingerprint, "gpg", false, true))

	const otherFingerprint = "FEDCBA9876543210FEDCBA9876543210FEDCBA98"
	require.NoError(t, runInit(&out, dataDir, resolver, "", otherFingerprint, "gpg", true, true))

	data, err := os.ReadFile(filepath.Join(dataDir, "daemon.json"))
	require.NoError(t, err)
	assert.Contains(t, string(data), otherFingerprint)
}

func TestRunInit_RequiresExactlyOneSource(t *testing.T) {
	dataDir := t.TempDir()
	resolver := identity.NewResolver(t.TempDir(), dataDir, "")
	var out bytes.Buffer

	err := runInit(&out, dataDir, resolver, "", "", "gpg", false, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exactly one")

	err = runInit(&out, dataDir, resolver, "operator", testFingerprint, "gpg", false, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exactly one")

	// Neither branch may have written a file.
	_, statErr := os.Stat(filepath.Join(dataDir, "daemon.json"))
	assert.True(t, os.IsNotExist(statErr))
}

func TestRunInit_UnresolvableHandleWritesNothing(t *testing.T) {
	dataDir := t.TempDir()
	ethosDir := t.TempDir() // no identity written -- "operator" does not resolve
	resolver := identity.NewResolver(ethosDir, dataDir, "")
	var out bytes.Buffer

	err := runInit(&out, dataDir, resolver, "operator", "", "gpg", false, true)
	require.Error(t, err)

	_, statErr := os.Stat(filepath.Join(dataDir, "daemon.json"))
	assert.True(t, os.IsNotExist(statErr), "an unresolvable owner config must never be written")
}
