package testenv

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// IsolateTempDir points os.TempDir() at a private directory for the
// duration of the test by overriding $TMPDIR. Production code that calls
// os.CreateTemp("", …) — the pgp passphrase files, config rewrites — then
// writes into this isolated directory instead of the shared system temp
// dir, so a test that globs or counts os.TempDir() cannot observe another
// package's writes and cannot collide with them under parallel go test.
//
// Passphrase files are ordinary files, so the deep t.TempDir() path is
// fine here; the 108-byte Unix-socket limit that forces ShortGPGHome to
// use /tmp does not apply. GPG sockets live under the explicit --homedir,
// not $TMPDIR.
func IsolateTempDir(t testing.TB) {
	t.Helper()
	t.Setenv("TMPDIR", t.TempDir())
}

// ShortGPGHome creates a GPG homedir with a path short enough for
// gpg-agent's Unix socket (108-byte limit). Go's t.TempDir() paths
// are too long, so we use /tmp directly and register cleanup.
func ShortGPGHome(t testing.TB) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "bg-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	home := filepath.Join(dir, "g")
	require.NoError(t, os.Mkdir(home, 0o700))
	return home
}

// GenKey creates an ephemeral GPG keypair with a 1-year expiry in the given homedir.
func GenKey(t testing.TB, gpgBin, home, name, email string) {
	t.Helper()
	base := []string{"--homedir", home, "--batch", "--no-tty"}
	script := fmt.Sprintf(`%%no-protection
Key-Type: RSA
Key-Length: 2048
Name-Real: %s
Name-Email: %s
Expire-Date: 1y
%%commit
`, name, email)

	cmd := exec.Command(gpgBin, append(base, "--gen-key")...)
	cmd.Stdin = bytes.NewBufferString(script)
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run(), "gpg key generation failed")
}

// GenKeyNoExpiry creates an ephemeral GPG keypair without an expiration date.
func GenKeyNoExpiry(t testing.TB, gpgBin, home, name, email string) {
	t.Helper()
	base := []string{"--homedir", home, "--batch", "--no-tty"}
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

// GenKeyWithPassphrase creates an ephemeral GPG keypair with a passphrase and 1-year expiry.
func GenKeyWithPassphrase(t testing.TB, gpgBin, home, name, email, passphrase string) {
	t.Helper()
	base := []string{"--homedir", home, "--batch", "--no-tty"}
	script := fmt.Sprintf(`%%echo Generating test key
Key-Type: RSA
Key-Length: 2048
Name-Real: %s
Name-Email: %s
Passphrase: %s
Expire-Date: 1y
%%commit
`, name, email, passphrase)

	cmd := exec.Command(gpgBin, append(base, "--gen-key")...)
	cmd.Stdin = bytes.NewBufferString(script)
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run(), "gpg key generation failed")
}

// GenOwnerKey generates an ephemeral keypair in home for email and returns
// its full 40-hex fingerprint -- the shape daemon.VerifySignature's
// ownerKeyID parameter requires. expire is a gpg --quick-generate-key
// expiration spec, e.g. "1y" or "0" for non-expiring.
func GenOwnerKey(t testing.TB, gpgBin, home, email, expire string) string {
	t.Helper()
	cmd := exec.Command(gpgBin, "--homedir", home, "--batch", "--no-tty",
		"--pinentry-mode", "loopback", "--passphrase", "",
		"--quick-generate-key", fmt.Sprintf("Test <%s>", email), "default", "default", expire)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Run(), "key generation failed for %s: %s", email, stderr.String())

	listCmd := exec.Command(gpgBin, "--homedir", home, "--batch", "--no-tty",
		"--list-keys", "--with-colons", "--", email)
	var stdout bytes.Buffer
	listCmd.Stdout = &stdout
	require.NoError(t, listCmd.Run())
	for _, line := range strings.Split(stdout.String(), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) > 9 && fields[0] == "fpr" {
			return fields[9]
		}
	}
	t.Fatalf("no fingerprint found for %s", email)
	return ""
}

// BuildPGPSignedMessage returns a real PGP-signed multipart/signed RFC822
// message (RFC 3156) with the signing key attached, generating a fresh
// ephemeral GPG keypair for from. It returns both the raw bytes and the
// signing key's short key ID.
func BuildPGPSignedMessage(t testing.TB, gpgBin, from, subject, body string) (raw []byte, keyID string) {
	t.Helper()

	home := ShortGPGHome(t)
	base := []string{"--homedir", home, "--batch", "--no-tty"}

	GenKey(t, gpgBin, home, "Test Signer", from)

	listCmd := exec.Command(gpgBin, append(base, "--list-keys", "--with-colons", from)...)
	var listBuf bytes.Buffer
	listCmd.Stdout = &listBuf
	require.NoError(t, listCmd.Run())
	for _, line := range bytes.Split(listBuf.Bytes(), []byte("\n")) {
		parts := bytes.Split(line, []byte(":"))
		if len(parts) >= 5 && string(parts[0]) == "pub" {
			keyID = string(parts[4])
			break
		}
	}
	require.NotEmpty(t, keyID, "failed to extract key ID")

	bodyPart := "Content-Type: text/plain; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: 7bit\r\n" +
		"\r\n" + body + "\r\n"

	signCmd := exec.Command(gpgBin, append(base, "--detach-sign", "--armor", "-u", from)...)
	signCmd.Stdin = bytes.NewBufferString(bodyPart)
	var sigBuf bytes.Buffer
	signCmd.Stdout = &sigBuf
	require.NoError(t, signCmd.Run())

	exportCmd := exec.Command(gpgBin, append(base, "--export", "--armor", from)...)
	var keyBuf bytes.Buffer
	exportCmd.Stdout = &keyBuf
	require.NoError(t, exportCmd.Run())

	boundary := "DaemonTestBoundary12345"
	var msg bytes.Buffer
	fmt.Fprintf(&msg, "From: %s\r\n", from)
	fmt.Fprintf(&msg, "To: test@test.com\r\n")
	fmt.Fprintf(&msg, "Subject: %s\r\n", subject)
	fmt.Fprintf(&msg, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	fmt.Fprintf(&msg, "Message-ID: <%d@test>\r\n", time.Now().UnixNano())
	fmt.Fprintf(&msg, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&msg, "Content-Type: multipart/signed; boundary=%s; micalg=pgp-sha256; protocol=\"application/pgp-signature\"\r\n", boundary)
	fmt.Fprintf(&msg, "\r\n--%s\r\n", boundary)
	msg.WriteString(bodyPart)
	fmt.Fprintf(&msg, "\r\n--%s\r\n", boundary)
	fmt.Fprintf(&msg, "Content-Type: application/pgp-signature; name=\"signature.asc\"\r\n\r\n")
	msg.Write(bytes.TrimSpace(sigBuf.Bytes()))
	fmt.Fprintf(&msg, "\r\n--%s\r\n", boundary)
	fmt.Fprintf(&msg, "Content-Type: application/pgp-keys\r\n\r\n")
	msg.Write(bytes.TrimSpace(keyBuf.Bytes()))
	fmt.Fprintf(&msg, "\r\n--%s--\r\n", boundary)

	return msg.Bytes(), keyID
}

// BuildTrustedMessage returns raw RFC822 bytes carrying Proton E2E trust
// headers but no PGP signature -- SMTP-injectable, so never sufficient on
// its own for x-bit execution (see internal/daemon/handler.go's
// verifyTrust).
func BuildTrustedMessage(from, subject, body string) []byte {
	return []byte(fmt.Sprintf(
		"From: %s\r\nSubject: %s\r\nDate: %s\r\nMessage-ID: <%d@test>\r\n"+
			"X-Pm-Content-Encryption: end-to-end\r\nX-Pm-Origin: internal\r\n"+
			"Content-Type: text/plain\r\n\r\n%s",
		from, subject, time.Now().Format(time.RFC1123Z), time.Now().UnixNano(), body,
	))
}
