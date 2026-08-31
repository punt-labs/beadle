package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// fingerprintOf returns email's full 40-hex fingerprint from home's
// keyring. testenv.GenOwnerKey does the same lookup internally but only
// for keys it generated itself (always with an empty passphrase); this is
// the passphrase-protected companion, needed after testenv.GenKeyWithPassphrase.
func fingerprintOf(t *testing.T, gpgBin, home, email string) string {
	t.Helper()
	out, err := exec.Command(gpgBin, "--homedir", home, "--batch", "--no-tty",
		"--list-keys", "--with-colons", "--", email).Output()
	require.NoError(t, err)
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) > 9 && fields[0] == "fpr" {
			return fields[9]
		}
	}
	t.Fatalf("no fingerprint found for %s", email)
	return ""
}

// TestRunSign_PassphraseProtectedKeyWithoutPassphraseIsActionable is the
// regression test for the "no passphrase given" defect: a real
// passphrase-protected signing key, with no BEADLE_GPG_PASSPHRASE set and
// nothing in the (real, unmocked) secret.Get chain on this test host, must
// fail with a message that names the credential and every place it can
// come from -- not gpg's bare "No passphrase given", which tells an
// operator nothing about where to set one. The passphrase itself must
// never appear in the error.
func TestRunSign_PassphraseProtectedKeyWithoutPassphraseIsActionable(t *testing.T) {
	gpgBin := gpgBinary(t)
	home := testenv.ShortGPGHome(t)
	t.Setenv("GNUPGHOME", home)
	const passphrase = "hunter2-do-not-log-me"
	testenv.GenKeyWithPassphrase(t, gpgBin, home, "Passphrase Test", "passphrase-test@example.com", passphrase)
	fpr := fingerprintOf(t, gpgBin, home, "passphrase-test@example.com")

	path := filepath.Join(t.TempDir(), "sysreport.yaml")
	require.NoError(t, os.WriteFile(path, []byte(minimalCommandYAML), 0o600))

	var out bytes.Buffer
	err := runSign(&out, path, fpr, gpgBin)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no passphrase was resolved")
	assert.Contains(t, err.Error(), "gpg-passphrase")
	assert.Contains(t, err.Error(), "BEADLE_GPG_PASSPHRASE")
	assert.NotContains(t, err.Error(), passphrase, "the passphrase itself must never appear in an error message")

	// Nothing must have been written to path -- a failed sign leaves the
	// original, unsigned file exactly as it was.
	unchanged, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	assert.Equal(t, minimalCommandYAML, string(unchanged))
}

// TestRunSign_PassphraseProtectedKeyWithEnvPassphraseSucceeds is the
// companion happy path: the same passphrase-protected key signs
// successfully once BEADLE_GPG_PASSPHRASE resolves it, proving the
// credential chain runSign documents in its error message actually works.
func TestRunSign_PassphraseProtectedKeyWithEnvPassphraseSucceeds(t *testing.T) {
	gpgBin := gpgBinary(t)
	home := testenv.ShortGPGHome(t)
	t.Setenv("GNUPGHOME", home)
	const passphrase = "hunter2-do-not-log-me"
	testenv.GenKeyWithPassphrase(t, gpgBin, home, "Passphrase Test", "passphrase-env@example.com", passphrase)
	fpr := fingerprintOf(t, gpgBin, home, "passphrase-env@example.com")
	t.Setenv("BEADLE_GPG_PASSPHRASE", passphrase)

	path := filepath.Join(t.TempDir(), "sysreport.yaml")
	require.NoError(t, os.WriteFile(path, []byte(minimalCommandYAML), 0o600))

	var out bytes.Buffer
	err := runSign(&out, path, fpr, gpgBin)
	require.NoError(t, err)
	assert.Contains(t, out.String(), "round-trip verified")
}

func TestRunSign_MissingFile(t *testing.T) {
	var out bytes.Buffer
	err := runSign(&out, filepath.Join(t.TempDir(), "nope.yaml"), "0123456789ABCDEF0123456789ABCDEF01234567", "gpg")
	require.Error(t, err)
}
