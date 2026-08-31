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

const testFingerprint = "0123456789ABCDEF0123456789ABCDEF01234567"

func TestRunInit_HandleHappyPath(t *testing.T) {
	dataDir := t.TempDir()
	ethosDir := t.TempDir()
	writeTestIdentity(t, ethosDir, "operator", testFingerprint)
	resolver := identity.NewResolver(ethosDir, dataDir, "")

	var out bytes.Buffer
	err := runInit(&out, dataDir, resolver, "operator", "", false)
	require.NoError(t, err)
	assert.Contains(t, out.String(), testFingerprint)

	data, err := os.ReadFile(filepath.Join(dataDir, "daemon.json"))
	require.NoError(t, err)
	var cfg struct {
		OwnerHandle string `json:"owner_handle"`
	}
	require.NoError(t, json.Unmarshal(data, &cfg))
	assert.Equal(t, "operator", cfg.OwnerHandle)
}

func TestRunInit_FingerprintHappyPath(t *testing.T) {
	dataDir := t.TempDir()
	resolver := identity.NewResolver(t.TempDir(), dataDir, "")

	var out bytes.Buffer
	err := runInit(&out, dataDir, resolver, "", testFingerprint, false)
	require.NoError(t, err)
	assert.Contains(t, out.String(), testFingerprint)

	data, err := os.ReadFile(filepath.Join(dataDir, "daemon.json"))
	require.NoError(t, err)
	var cfg struct {
		OwnerGPGKeyID string `json:"owner_gpg_key_id"`
	}
	require.NoError(t, json.Unmarshal(data, &cfg))
	assert.Equal(t, testFingerprint, cfg.OwnerGPGKeyID)
}

func TestRunInit_RefusesToClobberWithoutForce(t *testing.T) {
	dataDir := t.TempDir()
	resolver := identity.NewResolver(t.TempDir(), dataDir, "")

	var out bytes.Buffer
	require.NoError(t, runInit(&out, dataDir, resolver, "", testFingerprint, false))

	const otherFingerprint = "FEDCBA9876543210FEDCBA9876543210FEDCBA98"
	err := runInit(&out, dataDir, resolver, "", otherFingerprint, false)
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
	require.NoError(t, runInit(&out, dataDir, resolver, "", testFingerprint, false))

	const otherFingerprint = "FEDCBA9876543210FEDCBA9876543210FEDCBA98"
	require.NoError(t, runInit(&out, dataDir, resolver, "", otherFingerprint, true))

	data, err := os.ReadFile(filepath.Join(dataDir, "daemon.json"))
	require.NoError(t, err)
	assert.Contains(t, string(data), otherFingerprint)
}

func TestRunInit_RequiresExactlyOneSource(t *testing.T) {
	dataDir := t.TempDir()
	resolver := identity.NewResolver(t.TempDir(), dataDir, "")
	var out bytes.Buffer

	err := runInit(&out, dataDir, resolver, "", "", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exactly one")

	err = runInit(&out, dataDir, resolver, "operator", testFingerprint, false)
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

	err := runInit(&out, dataDir, resolver, "operator", "", false)
	require.Error(t, err)

	_, statErr := os.Stat(filepath.Join(dataDir, "daemon.json"))
	assert.True(t, os.IsNotExist(statErr), "an unresolvable owner config must never be written")
}
