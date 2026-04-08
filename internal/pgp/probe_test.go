package pgp

import (
	"bytes"
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKeyRequiresPassphrase_UnprotectedKey(t *testing.T) {
	gpgBin, err := exec.LookPath("gpg")
	if err != nil {
		t.Skip("gpg not installed")
	}

	home := shortGPGHome(t)
	base := []string{"--homedir", home, "--batch", "--no-tty"}

	// %no-protection asks gpg to generate a key with no passphrase,
	// which is exactly what KeyRequiresPassphrase should detect.
	genScript := `%no-protection
Key-Type: RSA
Key-Length: 2048
Name-Real: Probe Unprotected
Name-Email: probe-unprotected@example.com
Expire-Date: 0
%commit
`
	cmd := exec.Command(gpgBin, append(base, "--gen-key")...)
	cmd.Stdin = bytes.NewBufferString(genScript)
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run(), "unprotected key generation failed")

	t.Setenv("GNUPGHOME", home)

	needsPass, err := KeyRequiresPassphrase(gpgBin, "probe-unprotected@example.com")
	require.NoError(t, err)
	assert.False(t, needsPass, "unprotected key must not require a passphrase")
}

func TestKeyRequiresPassphrase_ProtectedKey(t *testing.T) {
	gpgBin, err := exec.LookPath("gpg")
	if err != nil {
		t.Skip("gpg not installed")
	}

	home := shortGPGHome(t)
	base := []string{"--homedir", home, "--batch", "--no-tty"}

	passphrase := "probe-test-passphrase-789"
	genScript := `%echo Generating protected test key
Key-Type: RSA
Key-Length: 2048
Name-Real: Probe Protected
Name-Email: probe-protected@example.com
Passphrase: ` + passphrase + `
Expire-Date: 0
%commit
`
	cmd := exec.Command(gpgBin, append(base, "--gen-key")...)
	cmd.Stdin = bytes.NewBufferString(genScript)
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run(), "protected key generation failed")

	t.Setenv("GNUPGHOME", home)

	needsPass, err := KeyRequiresPassphrase(gpgBin, "probe-protected@example.com")
	require.NoError(t, err)
	assert.True(t, needsPass, "passphrase-protected key must require a passphrase")
}

func TestKeyRequiresPassphrase_MissingKey(t *testing.T) {
	gpgBin, err := exec.LookPath("gpg")
	if err != nil {
		t.Skip("gpg not installed")
	}

	// Empty ephemeral homedir — no keys exist in it.
	home := shortGPGHome(t)
	t.Setenv("GNUPGHOME", home)

	// A missing key looks like a protected key to the probe (both return
	// non-zero exit). This test documents that behavior so the caller
	// knows to run `gpg --list-keys` first. See probe.go godoc.
	needsPass, err := KeyRequiresPassphrase(gpgBin, "nonexistent@example.com")
	require.NoError(t, err)
	assert.True(t, needsPass,
		"missing key should look like protected key — caller must verify existence first")
}
