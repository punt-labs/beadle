package pgp

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSign_ProducesVerifiableMessage(t *testing.T) {
	gpgBin, err := exec.LookPath("gpg")
	if err != nil {
		t.Skip("gpg not installed")
	}

	// Generate an ephemeral key with a known passphrase
	home := shortGPGHome(t)
	base := []string{"--homedir", home, "--batch", "--no-tty"}

	passphrase := "test-passphrase-123"
	genScript := `%echo Generating test key
Key-Type: RSA
Key-Length: 2048
Name-Real: Sign Test
Name-Email: signtest@example.com
Passphrase: ` + passphrase + `
Expire-Date: 1y
%commit
`
	cmd := exec.Command(gpgBin, append(base, "--gen-key")...)
	cmd.Stdin = bytes.NewBufferString(genScript)
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run(), "key generation failed")

	// Sign a message using the ephemeral key's homedir
	// We need to tell Sign() to use this homedir, but Sign() uses
	// the system keyring. For testing, we set GNUPGHOME.
	t.Setenv("GNUPGHOME", home)

	signed, err := Sign(gpgBin, "signtest@example.com", passphrase,
		"recipient@example.com", "Test Subject", "Hello from the signing test.")
	require.NoError(t, err)

	assert.NotEmpty(t, signed.Raw)
	assert.NotEmpty(t, signed.Boundary)
	assert.Contains(t, string(signed.Raw), "multipart/signed")
	assert.Contains(t, string(signed.Raw), "application/pgp-signature")
	assert.Contains(t, string(signed.Raw), "BEGIN PGP SIGNATURE")

	// Round-trip: verify the signed message
	result, err := Verify(gpgBin, signed.Raw)
	require.NoError(t, err)

	assert.True(t, result.Valid, "signed message should verify")
	assert.Contains(t, result.Signer, "Sign Test")
}

func TestSign_BadPassphrase(t *testing.T) {
	gpgBin, err := exec.LookPath("gpg")
	if err != nil {
		t.Skip("gpg not installed")
	}

	home := shortGPGHome(t)
	base := []string{"--homedir", home, "--batch", "--no-tty"}

	genScript := `%no-protection
Key-Type: RSA
Key-Length: 2048
Name-Real: No Pass
Name-Email: nopass@example.com
Expire-Date: 1y
%commit
`
	cmd := exec.Command(gpgBin, append(base, "--gen-key")...)
	cmd.Stdin = bytes.NewBufferString(genScript)
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run())

	t.Setenv("GNUPGHOME", home)

	// Sign with wrong passphrase — should still work for a no-passphrase key
	// (gpg ignores the passphrase if key has none)
	signed, err := Sign(gpgBin, "nopass@example.com", "wrong-passphrase",
		"to@example.com", "Subject", "Body")
	require.NoError(t, err)
	assert.Contains(t, string(signed.Raw), "BEGIN PGP SIGNATURE")
}

func TestRandomBoundary(t *testing.T) {
	seen := make(map[string]bool)
	for range 100 {
		b, err := RandomBoundary()
		require.NoError(t, err)
		assert.True(t, len(b) > 16, "boundary too short: %s", b)
		assert.False(t, seen[b], "duplicate boundary: %s", b)
		seen[b] = true
	}
}

func TestSign_NonExpiringKeyRejected(t *testing.T) {
	gpgBin, err := exec.LookPath("gpg")
	if err != nil {
		t.Skip("gpg not installed")
	}

	home := shortGPGHome(t)
	base := []string{"--homedir", home, "--batch", "--no-tty"}

	// Generate a key with no expiry — CheckKeyExpiry should block Sign().
	genScript := `%no-protection
Key-Type: RSA
Key-Length: 2048
Name-Real: No Expiry
Name-Email: noexpiry@example.com
Expire-Date: 0
%commit
`
	cmd := exec.Command(gpgBin, append(base, "--gen-key")...)
	cmd.Stdin = bytes.NewBufferString(genScript)
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run())

	t.Setenv("GNUPGHOME", home)

	_, err = Sign(gpgBin, "noexpiry@example.com", "",
		"to@example.com", "Subject", "Body")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "signing key rejected")
}

func TestDetachSign_TempFileCleanup(t *testing.T) {
	gpgBin, err := exec.LookPath("gpg")
	if err != nil {
		t.Skip("gpg not installed")
	}

	home := shortGPGHome(t)
	base := []string{"--homedir", home, "--batch", "--no-tty"}
	genKey(t, gpgBin, base, "Cleanup Test", "cleanup@example.com")
	t.Setenv("GNUPGHOME", home)

	// Count temp files before
	pattern := filepath.Join(os.TempDir(), "beadle-pp-*")
	before, _ := filepath.Glob(pattern)

	_, err = detachSign(gpgBin, "cleanup@example.com", "", []byte("test data"))
	require.NoError(t, err)

	// Count temp files after — should not have leaked
	after, _ := filepath.Glob(pattern)
	assert.Equal(t, len(before), len(after), "passphrase temp file leaked")
}
