package pgp

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildEncryptedMessage generates an ephemeral GPG keypair and constructs
// a multipart/encrypted (RFC 3156) message encrypted to that key.
// Returns the raw message bytes and the GPG homedir so callers can
// set GNUPGHOME for decryption.
func buildEncryptedMessage(t *testing.T, gpgBin string, plaintext string) (raw []byte, home string) {
	t.Helper()

	home = shortGPGHome(t)
	base := []string{"--homedir", home, "--batch", "--no-tty"}
	genKey(t, gpgBin, base, "Decrypt Test", "decrypt@example.com")

	// Encrypt the plaintext to the ephemeral key.
	encCmd := exec.Command(gpgBin, append(base,
		"--encrypt", "--armor",
		"--trust-model", "always",
		"--recipient", "decrypt@example.com",
	)...)
	encCmd.Stdin = bytes.NewBufferString(plaintext)
	var encBuf bytes.Buffer
	encCmd.Stdout = &encBuf
	encCmd.Stderr = os.Stderr
	require.NoError(t, encCmd.Run(), "gpg encrypt failed")

	// Assemble multipart/encrypted RFC 3156 message
	boundary := "EncryptBoundary12345"
	var msg bytes.Buffer
	fmt.Fprintf(&msg, "From: sender@example.com\r\n")
	fmt.Fprintf(&msg, "To: decrypt@example.com\r\n")
	fmt.Fprintf(&msg, "Subject: Encrypted Test\r\n")
	fmt.Fprintf(&msg, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&msg, "Content-Type: multipart/encrypted; boundary=%s; protocol=\"application/pgp-encrypted\"\r\n", boundary)
	fmt.Fprintf(&msg, "\r\n")
	fmt.Fprintf(&msg, "--%s\r\n", boundary)
	fmt.Fprintf(&msg, "Content-Type: application/pgp-encrypted\r\n")
	fmt.Fprintf(&msg, "Content-Description: PGP/MIME version identification\r\n")
	fmt.Fprintf(&msg, "\r\n")
	fmt.Fprintf(&msg, "Version: 1\r\n")
	fmt.Fprintf(&msg, "\r\n")
	fmt.Fprintf(&msg, "--%s\r\n", boundary)
	fmt.Fprintf(&msg, "Content-Type: application/octet-stream; name=\"encrypted.asc\"\r\n")
	fmt.Fprintf(&msg, "Content-Disposition: inline; filename=\"encrypted.asc\"\r\n")
	fmt.Fprintf(&msg, "\r\n")
	msg.Write(bytes.TrimSpace(encBuf.Bytes()))
	fmt.Fprintf(&msg, "\r\n--%s--\r\n", boundary)

	return msg.Bytes(), home
}

func TestDecrypt_RoundTrip(t *testing.T) {
	gpgBin, err := exec.LookPath("gpg")
	if err != nil {
		t.Skip("gpg not installed")
	}

	plaintext := "Hello, this is a secret message."
	raw, home := buildEncryptedMessage(t, gpgBin, plaintext)
	t.Setenv("GNUPGHOME", home)

	result, err := Decrypt(gpgBin, "", raw)
	require.NoError(t, err)

	assert.Contains(t, string(result.Plaintext), plaintext)
	assert.NotEmpty(t, result.Output)
}

func TestDecrypt_EncryptedAndSigned(t *testing.T) {
	gpgBin, err := exec.LookPath("gpg")
	if err != nil {
		t.Skip("gpg not installed")
	}

	home := shortGPGHome(t)
	base := []string{"--homedir", home, "--batch", "--no-tty"}

	// Generate sender and recipient keys in the same keyring.
	genKey(t, gpgBin, base, "Sender", "sender@example.com")
	genKey(t, gpgBin, base, "Recipient", "recipient@example.com")

	plaintext := "Signed and encrypted message."

	// Sign and encrypt in one gpg invocation.
	cmd := exec.Command(gpgBin, append(base,
		"--sign", "--encrypt", "--armor",
		"--trust-model", "always",
		"--local-user", "sender@example.com",
		"--recipient", "recipient@example.com",
	)...)
	cmd.Stdin = bytes.NewBufferString(plaintext)
	var encBuf bytes.Buffer
	cmd.Stdout = &encBuf
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run(), "gpg sign+encrypt failed")

	// Assemble multipart/encrypted message
	boundary := "SignEncBoundary99"
	var msg bytes.Buffer
	fmt.Fprintf(&msg, "From: sender@example.com\r\n")
	fmt.Fprintf(&msg, "To: recipient@example.com\r\n")
	fmt.Fprintf(&msg, "Subject: Signed+Encrypted\r\n")
	fmt.Fprintf(&msg, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&msg, "Content-Type: multipart/encrypted; boundary=%s; protocol=\"application/pgp-encrypted\"\r\n", boundary)
	fmt.Fprintf(&msg, "\r\n")
	fmt.Fprintf(&msg, "--%s\r\n", boundary)
	fmt.Fprintf(&msg, "Content-Type: application/pgp-encrypted\r\n")
	fmt.Fprintf(&msg, "\r\n")
	fmt.Fprintf(&msg, "Version: 1\r\n")
	fmt.Fprintf(&msg, "\r\n")
	fmt.Fprintf(&msg, "--%s\r\n", boundary)
	fmt.Fprintf(&msg, "Content-Type: application/octet-stream; name=\"encrypted.asc\"\r\n")
	fmt.Fprintf(&msg, "\r\n")
	msg.Write(bytes.TrimSpace(encBuf.Bytes()))
	fmt.Fprintf(&msg, "\r\n--%s--\r\n", boundary)

	t.Setenv("GNUPGHOME", home)

	result, err := Decrypt(gpgBin, "", msg.Bytes())
	require.NoError(t, err)

	assert.Contains(t, string(result.Plaintext), plaintext)
	assert.True(t, result.Signed, "decrypted message should be signed")
	assert.Contains(t, result.Signer, "Sender")
}

func TestDecrypt_WrongKey(t *testing.T) {
	gpgBin, err := exec.LookPath("gpg")
	if err != nil {
		t.Skip("gpg not installed")
	}

	// Encrypt to one key...
	plaintext := "You can't read this."
	raw, _ := buildEncryptedMessage(t, gpgBin, plaintext)

	// ...but set GNUPGHOME to a different keyring that doesn't have the
	// private key.
	otherHome := shortGPGHome(t)
	otherBase := []string{"--homedir", otherHome, "--batch", "--no-tty"}
	genKey(t, gpgBin, otherBase, "Wrong Key", "wrong@example.com")
	t.Setenv("GNUPGHOME", otherHome)

	_, err = Decrypt(gpgBin, "", raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gpg decrypt")
}

func TestDecrypt_NotEncrypted(t *testing.T) {
	gpgBin, err := exec.LookPath("gpg")
	if err != nil {
		t.Skip("gpg not installed")
	}

	plain := []byte("From: test@example.com\r\nSubject: Not encrypted\r\nContent-Type: text/plain\r\n\r\nJust plain text.\r\n")

	_, err = Decrypt(gpgBin, "", plain)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not multipart/encrypted")
}

func TestIsEncrypted(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{
			name: "multipart/encrypted with pgp protocol",
			raw:  "Content-Type: multipart/encrypted; boundary=foo; protocol=\"application/pgp-encrypted\"\r\n\r\nbody",
			want: true,
		},
		{
			name: "multipart/signed is not encrypted",
			raw:  "Content-Type: multipart/signed; boundary=foo; protocol=\"application/pgp-signature\"\r\n\r\nbody",
			want: false,
		},
		{
			name: "plain text",
			raw:  "Content-Type: text/plain\r\n\r\nhello",
			want: false,
		},
		{
			name: "no content-type",
			raw:  "Subject: test\r\n\r\nhello",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsEncrypted([]byte(tt.raw)))
		})
	}
}

func TestDecrypt_TempFileCleanup(t *testing.T) {
	gpgBin, err := exec.LookPath("gpg")
	if err != nil {
		t.Skip("gpg not installed")
	}

	plaintext := "Cleanup test message."
	raw, home := buildEncryptedMessage(t, gpgBin, plaintext)
	t.Setenv("GNUPGHOME", home)

	// Count passphrase temp files before
	before := countTempFiles(t, "beadle-pp-")

	_, err = Decrypt(gpgBin, "", raw)
	require.NoError(t, err)

	// Count after — should not have leaked
	after := countTempFiles(t, "beadle-pp-")
	assert.Equal(t, before, after, "passphrase temp file leaked")
}

func countTempFiles(t *testing.T, prefix string) int {
	t.Helper()
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) {
			n++
		}
	}
	return n
}
