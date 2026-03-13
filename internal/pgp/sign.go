package pgp

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"os/exec"
)

// SignedMessage holds the raw RFC 3156 multipart/signed message bytes
// ready to send via SMTP.
type SignedMessage struct {
	Raw      []byte
	Boundary string
}

// Sign creates a PGP/MIME signed message (RFC 3156) from a plain text body.
//
// It composes the body as a MIME part, detach-signs it with gpg, and wraps
// both in a multipart/signed envelope. The passphrase is passed to gpg via
// a temp file descriptor to avoid exposing it in process arguments.
func Sign(gpgBinary, signer, passphrase, to, subject, textBody string) (*SignedMessage, error) {
	boundary, err := randomBoundary()
	if err != nil {
		return nil, fmt.Errorf("generate boundary: %w", err)
	}

	// The signed body part — CRLF line endings are mandatory (RFC 3156).
	bodyPart := "Content-Type: text/plain; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: 7bit\r\n" +
		"\r\n" +
		textBody + "\r\n"

	sig, err := detachSign(gpgBinary, signer, passphrase, []byte(bodyPart))
	if err != nil {
		return nil, err
	}

	// Assemble the full RFC 822 message with multipart/signed envelope
	var msg bytes.Buffer
	fmt.Fprintf(&msg, "From: %s\r\n", signer)
	fmt.Fprintf(&msg, "To: %s\r\n", to)
	fmt.Fprintf(&msg, "Subject: %s\r\n", subject)
	fmt.Fprintf(&msg, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&msg, "Content-Type: multipart/signed; boundary=%q; micalg=pgp-sha256; protocol=\"application/pgp-signature\"\r\n", boundary)
	fmt.Fprintf(&msg, "\r\n")
	fmt.Fprintf(&msg, "--%s\r\n", boundary)
	msg.WriteString(bodyPart)
	fmt.Fprintf(&msg, "\r\n--%s\r\n", boundary)
	fmt.Fprintf(&msg, "Content-Type: application/pgp-signature; name=\"signature.asc\"\r\n")
	fmt.Fprintf(&msg, "Content-Disposition: attachment; filename=\"signature.asc\"\r\n")
	fmt.Fprintf(&msg, "\r\n")
	msg.Write(bytes.TrimSpace(sig))
	fmt.Fprintf(&msg, "\r\n--%s--\r\n", boundary)

	return &SignedMessage{Raw: msg.Bytes(), Boundary: boundary}, nil
}

// detachSign runs gpg --detach-sign --armor, passing the passphrase via
// a temporary file to avoid exposing it in process arguments or ps output.
func detachSign(gpgBinary, signer, passphrase string, data []byte) ([]byte, error) {
	// Write passphrase to a temp file (mode 600) so gpg can read it
	// via --passphrase-file. This avoids --passphrase (visible in ps)
	// and the stdin conflict with --passphrase-fd 0.
	f, err := os.CreateTemp("", "beadle-pp-*")
	if err != nil {
		return nil, fmt.Errorf("create passphrase file: %w", err)
	}
	defer os.Remove(f.Name())

	if _, err := f.WriteString(passphrase); err != nil {
		f.Close()
		return nil, fmt.Errorf("write passphrase file: %w", err)
	}
	f.Close()

	cmd := exec.Command(gpgBinary,
		"--batch", "--no-tty",
		"--pinentry-mode", "loopback",
		"--passphrase-file", f.Name(),
		"--detach-sign", "--armor",
		"-u", signer,
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdin = bytes.NewReader(data)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("gpg detach-sign: %w: %s", err, stderr.String())
	}

	return stdout.Bytes(), nil
}

func randomBoundary() (string, error) {
	var buf [16]byte
	chars := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	for i := range buf {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		if err != nil {
			return "", err
		}
		buf[i] = chars[n.Int64()]
	}
	return "Beadle" + string(buf[:]), nil
}
