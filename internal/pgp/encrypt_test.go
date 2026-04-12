package pgp

import (
	"bytes"
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncrypt_RoundTrip(t *testing.T) {
	gpgBin, err := exec.LookPath("gpg")
	if err != nil {
		t.Skip("gpg not installed")
	}

	home := shortGPGHome(t)
	base := []string{"--homedir", home, "--batch", "--no-tty"}
	genKey(t, gpgBin, base, "Recipient", "recipient@example.com")
	t.Setenv("GNUPGHOME", home)

	plaintext := []byte("Hello, this is a secret message.")
	ciphertext, err := Encrypt(gpgBin, []string{"recipient@example.com"}, "", plaintext)
	require.NoError(t, err)
	assert.Contains(t, string(ciphertext), "-----BEGIN PGP MESSAGE-----")

	// Decrypt and verify round-trip.
	cmd := exec.Command(gpgBin, "--homedir", home, "--batch", "--no-tty", "--decrypt")
	cmd.Stdin = bytes.NewReader(ciphertext)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Run(), "gpg decrypt failed: %s", stderr.String())
	assert.Equal(t, plaintext, stdout.Bytes())
}

func TestEncrypt_MultipleRecipients(t *testing.T) {
	gpgBin, err := exec.LookPath("gpg")
	if err != nil {
		t.Skip("gpg not installed")
	}

	home := shortGPGHome(t)
	base := []string{"--homedir", home, "--batch", "--no-tty"}
	genKey(t, gpgBin, base, "Alice", "alice@example.com")
	genKey(t, gpgBin, base, "Bob", "bob@example.com")
	t.Setenv("GNUPGHOME", home)

	plaintext := []byte("Message for both Alice and Bob.")
	ciphertext, err := Encrypt(gpgBin, []string{"alice@example.com", "bob@example.com"}, "", plaintext)
	require.NoError(t, err)

	// Decrypt as Alice.
	cmd := exec.Command(gpgBin, "--homedir", home, "--batch", "--no-tty", "--decrypt")
	cmd.Stdin = bytes.NewReader(ciphertext)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run(), "decrypt as alice failed")
	assert.Equal(t, plaintext, stdout.Bytes())

	// Both keys in the same keyring, so Bob can also decrypt.
	cmd = exec.Command(gpgBin, "--homedir", home, "--batch", "--no-tty", "--decrypt")
	cmd.Stdin = bytes.NewReader(ciphertext)
	stdout.Reset()
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run(), "decrypt as bob failed")
	assert.Equal(t, plaintext, stdout.Bytes())
}

func TestEncrypt_SelfEncryption(t *testing.T) {
	gpgBin, err := exec.LookPath("gpg")
	if err != nil {
		t.Skip("gpg not installed")
	}

	home := shortGPGHome(t)
	base := []string{"--homedir", home, "--batch", "--no-tty"}
	genKey(t, gpgBin, base, "Recipient", "recipient@example.com")
	genKey(t, gpgBin, base, "Sender", "sender@example.com")
	t.Setenv("GNUPGHOME", home)

	plaintext := []byte("Encrypt to recipient and self.")
	ciphertext, err := Encrypt(gpgBin, []string{"recipient@example.com"}, "sender@example.com", plaintext)
	require.NoError(t, err)

	// Sender can decrypt their own sent mail.
	cmd := exec.Command(gpgBin, "--homedir", home, "--batch", "--no-tty", "--decrypt")
	cmd.Stdin = bytes.NewReader(ciphertext)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run(), "decrypt as sender (self) failed")
	assert.Equal(t, plaintext, stdout.Bytes())
}

func TestEncrypt_MissingKey(t *testing.T) {
	gpgBin, err := exec.LookPath("gpg")
	if err != nil {
		t.Skip("gpg not installed")
	}

	home := shortGPGHome(t)
	t.Setenv("GNUPGHOME", home)

	_, err = Encrypt(gpgBin, []string{"nonexistent@example.com"}, "", []byte("data"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gpg encrypt")
}

func TestEncrypt_NoRecipients(t *testing.T) {
	_, err := Encrypt("gpg", nil, "", []byte("data"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no recipient key IDs")
}
