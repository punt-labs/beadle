package pgp

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shortGPGHome creates a GPG homedir with a path short enough for
// gpg-agent's Unix socket (108-byte limit). Go's t.TempDir() paths
// are too long, so we use /tmp directly and register cleanup.
func shortGPGHome(t *testing.T) (home string) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "bg-*")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })

	home = filepath.Join(dir, "g")
	require.NoError(t, os.Mkdir(home, 0700))
	return home
}

// genKey creates an ephemeral GPG keypair in the given homedir.
func genKey(t *testing.T, gpgBin string, base []string, name, email string) {
	t.Helper()
	script := fmt.Sprintf(`%%no-protection
Key-Type: RSA
Key-Length: 2048
Name-Real: %s
Name-Email: %s
Expire-Date: 0
%%commit
`, name, email)

	cmd := exec.Command(gpgBin, append(base, "--gen-key")...)
	cmd.Stdin = bytes.NewBufferString(script)
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run(), "gpg key generation failed")
}

// buildSignedMessage generates an ephemeral GPG key and constructs a
// multipart/signed (RFC 3156) message. The public key is included as
// an attachment so Verify() can import it without any external keyring.
func buildSignedMessage(t *testing.T, gpgBin string) []byte {
	t.Helper()

	home := shortGPGHome(t)
	base := []string{"--homedir", home, "--batch", "--no-tty"}
	genKey(t, gpgBin, base, "Test Signer", "test@example.com")

	// The signed body — CRLF line endings are mandatory for RFC 3156.
	// gpg will sign these exact bytes, so the MIME part in the final
	// message must reproduce them verbatim.
	bodyPart := "Content-Type: text/plain; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: 7bit\r\n" +
		"\r\n" +
		"Hello, this is a signed test message.\r\n"

	// Detach-sign the body
	signCmd := exec.Command(gpgBin, append(base,
		"--detach-sign", "--armor", "-u", "test@example.com")...)
	signCmd.Stdin = bytes.NewBufferString(bodyPart)
	var sigBuf bytes.Buffer
	signCmd.Stdout = &sigBuf
	signCmd.Stderr = os.Stderr
	require.NoError(t, signCmd.Run(), "gpg detach-sign failed")

	// Export public key
	exportCmd := exec.Command(gpgBin, append(base, "--export", "--armor", "test@example.com")...)
	var keyBuf bytes.Buffer
	exportCmd.Stdout = &keyBuf
	require.NoError(t, exportCmd.Run(), "gpg export failed")

	// Assemble multipart/signed RFC 3156 message with attached key
	boundary := "TestBoundary12345"
	var msg bytes.Buffer
	fmt.Fprintf(&msg, "From: Test Signer <test@example.com>\r\n")
	fmt.Fprintf(&msg, "To: recipient@example.com\r\n")
	fmt.Fprintf(&msg, "Subject: Signed Test\r\n")
	fmt.Fprintf(&msg, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&msg, "Content-Type: multipart/signed; boundary=%s; micalg=pgp-sha256; protocol=\"application/pgp-signature\"\r\n", boundary)
	fmt.Fprintf(&msg, "\r\n")
	fmt.Fprintf(&msg, "--%s\r\n", boundary)
	msg.WriteString(bodyPart)
	fmt.Fprintf(&msg, "\r\n--%s\r\n", boundary)
	fmt.Fprintf(&msg, "Content-Type: application/pgp-signature; name=\"signature.asc\"\r\n")
	fmt.Fprintf(&msg, "\r\n")
	msg.Write(bytes.TrimSpace(sigBuf.Bytes()))
	fmt.Fprintf(&msg, "\r\n--%s\r\n", boundary)
	fmt.Fprintf(&msg, "Content-Type: application/pgp-keys\r\n")
	fmt.Fprintf(&msg, "\r\n")
	msg.Write(bytes.TrimSpace(keyBuf.Bytes()))
	fmt.Fprintf(&msg, "\r\n--%s--\r\n", boundary)

	return msg.Bytes()
}

func TestVerify_ValidSignature(t *testing.T) {
	gpgBin, err := exec.LookPath("gpg")
	if err != nil {
		t.Skip("gpg not installed")
	}

	raw := buildSignedMessage(t, gpgBin)

	result, err := Verify(gpgBin, raw)
	require.NoError(t, err)

	assert.True(t, result.Valid, "signature should be valid")
	assert.True(t, result.KeyImported, "key should be imported from message attachment")
	assert.Contains(t, result.Signer, "Test Signer")
	assert.NotEmpty(t, result.KeyID)
	assert.Contains(t, result.Output, "Good signature")
}

func TestVerify_TamperedBody(t *testing.T) {
	gpgBin, err := exec.LookPath("gpg")
	if err != nil {
		t.Skip("gpg not installed")
	}

	raw := buildSignedMessage(t, gpgBin)

	// Tamper with the body after signing — signature should no longer match
	tampered := bytes.Replace(raw,
		[]byte("Hello, this is a signed test message."),
		[]byte("Hello, this message has been tampered with!"),
		1)

	result, err := Verify(gpgBin, tampered)
	require.NoError(t, err)

	assert.False(t, result.Valid, "tampered message should fail verification")
	assert.Contains(t, result.Output, "BAD signature")
}

func TestVerify_NotSigned(t *testing.T) {
	gpgBin, err := exec.LookPath("gpg")
	if err != nil {
		t.Skip("gpg not installed")
	}

	plain := []byte("From: test@example.com\r\nSubject: Not signed\r\nContent-Type: text/plain\r\n\r\nJust a plain message.\r\n")

	_, err = Verify(gpgBin, plain)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not multipart/signed")
}

func TestExtractQuoted(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`Good signature from "Alice <alice@example.com>"`, "Alice <alice@example.com>"},
		{`no quotes here`, ""},
		{`one "quote only`, ""},
		{`empty ""`, ""},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, extractQuoted(tt.input), "input: %s", tt.input)
	}
}
