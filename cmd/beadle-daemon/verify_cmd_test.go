package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/punt-labs/beadle/internal/daemon"
	"github.com/punt-labs/beadle/internal/identity"
	"github.com/punt-labs/beadle/internal/testenv"
)

func TestRunVerify_GoodSignatureWithExplicitSigner(t *testing.T) {
	gpgBin := gpgBinary(t)
	home := testenv.ShortGPGHome(t)
	t.Setenv("GNUPGHOME", home)
	fpr := testenv.GenOwnerKey(t, gpgBin, home, "verify-good@example.com", "1y")

	path := filepath.Join(t.TempDir(), "sysreport.yaml")
	require.NoError(t, os.WriteFile(path, []byte(minimalCommandYAML), 0o600))
	var signOut bytes.Buffer
	dataDir, resolver := unconfiguredDaemon(t)
	require.NoError(t, runSign(&signOut, dataDir, resolver, path, fpr, gpgBin, false))

	var out bytes.Buffer
	err := runVerify(&out, path, fpr, gpgBin)
	require.NoError(t, err)
	assert.Contains(t, out.String(), "good")
	assert.Contains(t, out.String(), fpr)
}

func TestRunVerify_WrongKeyIsReported(t *testing.T) {
	gpgBin := gpgBinary(t)
	home := testenv.ShortGPGHome(t)
	t.Setenv("GNUPGHOME", home)
	signerFpr := testenv.GenOwnerKey(t, gpgBin, home, "verify-signer@example.com", "1y")
	otherFpr := testenv.GenOwnerKey(t, gpgBin, home, "verify-other@example.com", "1y")

	path := filepath.Join(t.TempDir(), "sysreport.yaml")
	require.NoError(t, os.WriteFile(path, []byte(minimalCommandYAML), 0o600))
	var signOut bytes.Buffer
	dataDir, resolver := unconfiguredDaemon(t)
	require.NoError(t, runSign(&signOut, dataDir, resolver, path, signerFpr, gpgBin, false))

	var out bytes.Buffer
	err := runVerify(&out, path, otherFpr, gpgBin)
	require.Error(t, err)
	assert.Contains(t, err.Error(), string(daemon.ReasonWrongKey))
	assert.Empty(t, out.String(), "nothing is printed to w on a failing verdict -- it's returned as the error")
}

func TestRunVerify_MissingSignatureIsReported(t *testing.T) {
	gpgBin := gpgBinary(t)
	fpr := "0123456789ABCDEF0123456789ABCDEF01234567"

	path := filepath.Join(t.TempDir(), "sysreport.yaml")
	require.NoError(t, os.WriteFile(path, []byte(minimalCommandYAML), 0o600)) // never signed

	var out bytes.Buffer
	err := runVerify(&out, path, fpr, gpgBin)
	require.Error(t, err)
	assert.Contains(t, err.Error(), string(daemon.ReasonMissing))
}

func TestRunVerify_TamperedFileIsReportedInvalid(t *testing.T) {
	gpgBin := gpgBinary(t)
	home := testenv.ShortGPGHome(t)
	t.Setenv("GNUPGHOME", home)
	fpr := testenv.GenOwnerKey(t, gpgBin, home, "verify-tamper@example.com", "1y")

	path := filepath.Join(t.TempDir(), "sysreport.yaml")
	require.NoError(t, os.WriteFile(path, []byte(minimalCommandYAML), 0o600))
	var signOut bytes.Buffer
	dataDir, resolver := unconfiguredDaemon(t)
	require.NoError(t, runSign(&signOut, dataDir, resolver, path, fpr, gpgBin, false))

	signed, err := os.ReadFile(path)
	require.NoError(t, err)
	tampered := bytes.Replace(signed, []byte("report disk and cpu"), []byte("report disk and ALSO run rm -rf"), 1)
	require.NoError(t, os.WriteFile(path, tampered, 0o600))

	var out bytes.Buffer
	err = runVerify(&out, path, fpr, gpgBin)
	require.Error(t, err)
	assert.Contains(t, err.Error(), string(daemon.ReasonInvalid))
}

func TestRunVerify_RejectsNonFingerprintSigner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sysreport.yaml")
	require.NoError(t, os.WriteFile(path, []byte(minimalCommandYAML), 0o600))

	var out bytes.Buffer
	err := runVerify(&out, path, "verify@example.com", "gpg")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a full 40-hex OpenPGP fingerprint")
}

// TestResolveVerifySigner_AbsentDaemonJSONNamesInitRemedy proves verify's
// "no --signer given" default path answers plainly when daemon.json does
// not exist yet -- unlike the daemon's own startup path, which stays
// silent about the same absence because it is the ordinary unconfigured
// case for a long-running process. A CLI invocation asking "is this
// recipe's signature valid" has no reason to stay quiet about why it
// cannot answer.
func TestResolveVerifySigner_AbsentDaemonJSONNamesInitRemedy(t *testing.T) {
	dataDir := t.TempDir()
	resolver := identity.NewResolver(t.TempDir(), dataDir, "")

	_, err := resolveVerifySigner(dataDir, resolver)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "beadle-daemon init")
}

// TestResolveVerifySigner_AbsentDaemonJSONIsErrNotExist proves the returned
// error still carries os.ErrNotExist in its chain despite the added
// human-readable wrapping -- checkSignerMatchesAuthorizer (sign_cmd.go)
// depends on errors.Is(err, os.ErrNotExist) to tell "nothing to compare
// against yet" apart from every other resolution failure, which it must
// never ignore.
func TestResolveVerifySigner_AbsentDaemonJSONIsErrNotExist(t *testing.T) {
	dataDir := t.TempDir()
	resolver := identity.NewResolver(t.TempDir(), dataDir, "")

	_, err := resolveVerifySigner(dataDir, resolver)
	require.Error(t, err)
	assert.True(t, errors.Is(err, os.ErrNotExist))
}

// TestResolveVerifySigner_ResolvesFromDaemonJSON proves the default path
// end to end: a real daemon.json (as beadle-daemon init would write one)
// resolves to the fingerprint it names.
func TestResolveVerifySigner_ResolvesFromDaemonJSON(t *testing.T) {
	dataDir := t.TempDir()
	resolver := identity.NewResolver(t.TempDir(), dataDir, "")

	var initOut bytes.Buffer
	require.NoError(t, runInit(&initOut, dataDir, resolver, "", testFingerprint, "gpg", false, true))

	fpr, err := resolveVerifySigner(dataDir, resolver)
	require.NoError(t, err)
	assert.Equal(t, testFingerprint, fpr)
}
