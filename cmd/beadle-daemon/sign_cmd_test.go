package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/punt-labs/beadle/internal/daemon"
	"github.com/punt-labs/beadle/internal/testenv"
)

// gpgBinary returns the gpg binary path or fails the test naming the
// install remedy. Per docs/TESTING.md, a missing external dependency is a
// test failure, never a t.Skip.
func gpgBinary(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("gpg")
	if err != nil {
		t.Fatalf("gpg not found on PATH: install it (apt install gnupg / brew install gnupg): %v", err)
	}
	return bin
}

const minimalCommandYAML = `name: sysreport
description: report disk and cpu
runner: cli
binary: beadle-sysreport
output_schema: text
`

func TestRunSign_RoundTrip(t *testing.T) {
	gpgBin := gpgBinary(t)
	home := testenv.ShortGPGHome(t)
	t.Setenv("GNUPGHOME", home)
	fpr := testenv.GenOwnerKey(t, gpgBin, home, "sign-test@example.com", "1y")

	path := filepath.Join(t.TempDir(), "sysreport.yaml")
	require.NoError(t, os.WriteFile(path, []byte(minimalCommandYAML), 0o600))

	var out bytes.Buffer
	err := runSign(&out, path, fpr, gpgBin)
	require.NoError(t, err)
	assert.Contains(t, out.String(), "round-trip verified")

	signed, err := os.ReadFile(path)
	require.NoError(t, err)

	var command daemon.Command
	require.NoError(t, yaml.Unmarshal(signed, &command))
	assert.Equal(t, "sysreport", command.Name)
	assert.NotEmpty(t, command.Signature)

	// The signature this command produced must independently verify
	// through daemon.VerifySignature -- the exact function beadle-daemon
	// itself calls when loading command files.
	require.NoError(t, daemon.VerifySignature(&command, gpgBin, fpr))
}

func TestRunSign_RejectsNonFingerprintSigner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sysreport.yaml")
	require.NoError(t, os.WriteFile(path, []byte(minimalCommandYAML), 0o600))

	var out bytes.Buffer
	err := runSign(&out, path, "sign-test@example.com", "gpg")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a full 40-hex OpenPGP fingerprint")
}

// TestRunSign_TamperedFileFailsVerification proves the artifact runSign
// produces is genuinely tamper-evident: a signed file whose body is edited
// after signing must fail daemon.VerifySignature -- the same check
// beadle-daemon's LoadCommands runs before trusting a command file. This is
// the regression guard the mission exists to protect: if signing and
// verification ever computed "canonical" differently, a tampered file
// could pass unnoticed.
func TestRunSign_TamperedFileFailsVerification(t *testing.T) {
	gpgBin := gpgBinary(t)
	home := testenv.ShortGPGHome(t)
	t.Setenv("GNUPGHOME", home)
	fpr := testenv.GenOwnerKey(t, gpgBin, home, "tamper-test@example.com", "1y")

	path := filepath.Join(t.TempDir(), "sysreport.yaml")
	require.NoError(t, os.WriteFile(path, []byte(minimalCommandYAML), 0o600))

	var out bytes.Buffer
	require.NoError(t, runSign(&out, path, fpr, gpgBin))

	signed, err := os.ReadFile(path)
	require.NoError(t, err)
	tampered := bytes.Replace(signed, []byte("report disk and cpu"), []byte("report disk and ALSO run rm -rf"), 1)
	require.NotEqual(t, signed, tampered, "test fixture must actually change the body")

	var command daemon.Command
	require.NoError(t, yaml.Unmarshal(tampered, &command))

	err = daemon.VerifySignature(&command, gpgBin, fpr)
	require.Error(t, err)
	var sigErr *daemon.SignatureError
	require.ErrorAs(t, err, &sigErr)
	assert.Equal(t, daemon.ReasonInvalid, sigErr.Reason)
}

func TestRunSign_MissingFile(t *testing.T) {
	var out bytes.Buffer
	err := runSign(&out, filepath.Join(t.TempDir(), "nope.yaml"), "0123456789ABCDEF0123456789ABCDEF01234567", "gpg")
	require.Error(t, err)
}
