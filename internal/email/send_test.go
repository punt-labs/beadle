package email

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/punt-labs/beadle/internal/pgp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gpgHome creates a GPG homedir with a short path for the Unix socket limit.
func gpgHome(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "bg-*")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })
	home := filepath.Join(dir, "g")
	require.NoError(t, os.Mkdir(home, 0700))
	return home
}

// gpgGenKey creates an ephemeral GPG keypair with a 1-year expiry.
func gpgGenKey(t *testing.T, gpgBin, home, name, email string) {
	t.Helper()
	script := fmt.Sprintf(`%%no-protection
Key-Type: RSA
Key-Length: 2048
Name-Real: %s
Name-Email: %s
Expire-Date: 1y
%%commit
`, name, email)
	cmd := exec.Command(gpgBin, "--homedir", home, "--batch", "--no-tty", "--gen-key")
	cmd.Stdin = bytes.NewBufferString(script)
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run(), "gpg key generation failed")
}

// gpgGenKeyNoExpiry creates an ephemeral GPG keypair without an expiration date.
func gpgGenKeyNoExpiry(t *testing.T, gpgBin, home, name, email string) {
	t.Helper()
	script := fmt.Sprintf(`%%no-protection
Key-Type: RSA
Key-Length: 2048
Name-Real: %s
Name-Email: %s
Expire-Date: 0
%%commit
`, name, email)
	cmd := exec.Command(gpgBin, "--homedir", home, "--batch", "--no-tty", "--gen-key")
	cmd.Stdin = bytes.NewBufferString(script)
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run(), "gpg key generation failed")
}

func TestComposeRaw_NoAttachments(t *testing.T) {
	raw, err := ComposeRaw("a@b.com", []string{"c@d.com"}, nil, "Hi", "Hello", nil)
	require.NoError(t, err)

	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	require.NoError(t, err)

	assert.Equal(t, "a@b.com", msg.Header.Get("From"))
	assert.Equal(t, "c@d.com", msg.Header.Get("To"))
	assert.Equal(t, "Hi", msg.Header.Get("Subject"))
	assert.Equal(t, "text/plain; charset=utf-8", msg.Header.Get("Content-Type"))
	assert.Empty(t, msg.Header.Get("Cc"))

	body, err := io.ReadAll(msg.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), "Hello")
}

func TestComposeRaw_EmptyAttachments(t *testing.T) {
	raw, err := ComposeRaw("a@b.com", []string{"c@d.com"}, nil, "Hi", "Hello", []OutboundAttachment{})
	require.NoError(t, err)

	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	require.NoError(t, err)
	assert.Equal(t, "text/plain; charset=utf-8", msg.Header.Get("Content-Type"))
}

func TestComposeRaw_MultipleToRecipients(t *testing.T) {
	raw, err := ComposeRaw("a@b.com", []string{"c@d.com", "e@f.com"}, nil, "Hi", "Hello", nil)
	require.NoError(t, err)

	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	require.NoError(t, err)

	assert.Equal(t, "c@d.com, e@f.com", msg.Header.Get("To"))
	assert.Empty(t, msg.Header.Get("Cc"))
}

func TestComposeRaw_WithCc(t *testing.T) {
	raw, err := ComposeRaw("a@b.com", []string{"c@d.com"}, []string{"x@y.com", "z@w.com"}, "Hi", "Hello", nil)
	require.NoError(t, err)

	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	require.NoError(t, err)

	assert.Equal(t, "c@d.com", msg.Header.Get("To"))
	assert.Equal(t, "x@y.com, z@w.com", msg.Header.Get("Cc"))
}

func TestComposeRaw_BccNotInHeaders(t *testing.T) {
	// Bcc recipients should never appear in the message headers.
	// ComposeRaw does not accept bcc — they are envelope-only (handled by SMTPSend).
	// This test verifies that even with Cc, no Bcc header is written.
	raw, err := ComposeRaw("a@b.com", []string{"c@d.com"}, []string{"cc@example.com"}, "Hi", "Hello", nil)
	require.NoError(t, err)

	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	require.NoError(t, err)

	assert.Empty(t, msg.Header.Get("Bcc"), "Bcc header must not appear in composed message")
}

func TestComposeRaw_OneAttachment(t *testing.T) {
	atts := []OutboundAttachment{{
		Filename:    "report.pdf",
		ContentType: "application/pdf",
		Data:        []byte("fake-pdf-content"),
	}}

	raw, err := ComposeRaw("a@b.com", []string{"c@d.com"}, nil, "Report", "See attached.", atts)
	require.NoError(t, err)

	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	require.NoError(t, err)

	ct := msg.Header.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(ct)
	require.NoError(t, err)
	assert.Equal(t, "multipart/mixed", mediaType)

	mr := multipart.NewReader(msg.Body, params["boundary"])

	// Part 1: text body
	part, err := mr.NextPart()
	require.NoError(t, err)
	assert.Equal(t, "text/plain; charset=utf-8", part.Header.Get("Content-Type"))
	body, err := io.ReadAll(part)
	require.NoError(t, err)
	assert.Equal(t, "See attached.\r\n", string(body))

	// Part 2: attachment
	part, err = mr.NextPart()
	require.NoError(t, err)
	assert.Equal(t, "application/pdf", part.Header.Get("Content-Type"))
	assert.Contains(t, part.Header.Get("Content-Disposition"), "report.pdf")
	assert.Equal(t, "base64", part.Header.Get("Content-Transfer-Encoding"))

	attBody, err := io.ReadAll(part)
	require.NoError(t, err)
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(attBody)))
	require.NoError(t, err)
	assert.Equal(t, []byte("fake-pdf-content"), decoded)

	// No more parts
	_, err = mr.NextPart()
	assert.ErrorIs(t, err, io.EOF)
}

func TestComposeRaw_MultipleAttachments(t *testing.T) {
	atts := []OutboundAttachment{
		{Filename: "a.txt", ContentType: "text/plain", Data: []byte("aaa")},
		{Filename: "b.png", ContentType: "image/png", Data: []byte("png-data")},
		{Filename: "c.bin", ContentType: "application/octet-stream", Data: []byte("binary")},
	}

	raw, err := ComposeRaw("a@b.com", []string{"c@d.com"}, nil, "Files", "Here are files.", atts)
	require.NoError(t, err)

	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	require.NoError(t, err)

	_, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	require.NoError(t, err)
	mr := multipart.NewReader(msg.Body, params["boundary"])

	// Text body + 3 attachments = 4 parts
	partCount := 0
	for {
		_, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		partCount++
	}
	assert.Equal(t, 4, partCount)
}

func TestComposeRaw_HeaderInjectionWithAttachments(t *testing.T) {
	atts := []OutboundAttachment{{
		Filename:    "evil.txt",
		ContentType: "text/plain",
		Data:        []byte("data"),
	}}

	tests := []struct {
		name    string
		from    string
		to      []string
		subject string
	}{
		{"from CR", "a\r@b.com", []string{"c@d.com"}, "Hi"},
		{"to LF", "a@b.com", []string{"c\n@d.com"}, "Hi"},
		{"subject CRLF", "a@b.com", []string{"c@d.com"}, "Hi\r\nBcc: evil@evil.com"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ComposeRaw(tc.from, tc.to, nil, tc.subject, "body", atts)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "CR/LF")
		})
	}
}

func TestComposeRaw_HeaderInjectionInCc(t *testing.T) {
	_, err := ComposeRaw("a@b.com", []string{"c@d.com"}, []string{"evil\r\n@hack.com"}, "Hi", "body", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "CR/LF")
}

func TestComposeRaw_AttachmentHeaderInjection(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		filename    string
	}{
		{"content-type CR", "text/plain\r\nBcc: evil@evil.com", "ok.txt"},
		{"filename LF", "text/plain", "evil\n.txt"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			atts := []OutboundAttachment{{
				Filename:    tc.filename,
				ContentType: tc.contentType,
				Data:        []byte("data"),
			}}
			_, err := ComposeRaw("a@b.com", []string{"c@d.com"}, nil, "Hi", "body", atts)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "CR/LF")
		})
	}
}

func TestComposeRaw_NonASCIIFilename(t *testing.T) {
	atts := []OutboundAttachment{{
		Filename:    "rapport_été.pdf",
		ContentType: "application/pdf",
		Data:        []byte("pdf-data"),
	}}

	raw, err := ComposeRaw("a@b.com", []string{"c@d.com"}, nil, "Report", "See attached.", atts)
	require.NoError(t, err)

	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	require.NoError(t, err)

	_, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	require.NoError(t, err)
	mr := multipart.NewReader(msg.Body, params["boundary"])

	// Skip text part
	_, err = mr.NextPart()
	require.NoError(t, err)

	// Attachment part — verify filename round-trips correctly
	part, err := mr.NextPart()
	require.NoError(t, err)
	_, dispParams, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
	require.NoError(t, err)
	assert.Equal(t, "rapport_été.pdf", dispParams["filename"])
}

func TestComposeSignedRaw_ProducesVerifiableMessage(t *testing.T) {
	gpgBin, err := exec.LookPath("gpg")
	if err != nil {
		t.Skip("gpg not installed")
	}

	home := gpgHome(t)
	gpgGenKey(t, gpgBin, home, "Send Test", "sendtest@example.com")
	t.Setenv("GNUPGHOME", home)

	raw, err := ComposeSignedRaw(
		"sendtest@example.com",
		[]string{"recipient@example.com"},
		nil,
		"Signed Subject",
		"Hello from the signed send test.",
		nil,
		gpgBin, "sendtest@example.com", "",
	)
	require.NoError(t, err)

	// Parse and verify structure.
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	require.NoError(t, err)

	ct := msg.Header.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(ct)
	require.NoError(t, err)
	assert.Equal(t, "multipart/signed", mediaType)
	assert.Equal(t, "pgp-sha256", params["micalg"])
	assert.Equal(t, "application/pgp-signature", params["protocol"])

	assert.Equal(t, "sendtest@example.com", msg.Header.Get("From"))
	assert.Equal(t, "recipient@example.com", msg.Header.Get("To"))
	assert.Equal(t, "Signed Subject", msg.Header.Get("Subject"))

	// Verify with gpg via the pgp package.
	result, verifyErr := pgp.Verify(gpgBin, raw)
	require.NoError(t, verifyErr)
	assert.True(t, result.Valid, "signed message should verify")
}

func TestComposeSignedRaw_WithCc(t *testing.T) {
	gpgBin, err := exec.LookPath("gpg")
	if err != nil {
		t.Skip("gpg not installed")
	}

	home := gpgHome(t)
	gpgGenKey(t, gpgBin, home, "Cc Test", "cctest@example.com")
	t.Setenv("GNUPGHOME", home)

	raw, err := ComposeSignedRaw(
		"cctest@example.com",
		[]string{"to@example.com"},
		[]string{"cc1@example.com", "cc2@example.com"},
		"Cc Test",
		"Body with cc.",
		nil,
		gpgBin, "cctest@example.com", "",
	)
	require.NoError(t, err)

	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	require.NoError(t, err)
	assert.Equal(t, "cc1@example.com, cc2@example.com", msg.Header.Get("Cc"))
}

func TestComposeSignedRaw_WithAttachments(t *testing.T) {
	gpgBin, err := exec.LookPath("gpg")
	if err != nil {
		t.Skip("gpg not installed")
	}

	home := gpgHome(t)
	gpgGenKey(t, gpgBin, home, "Att Test", "att@example.com")
	t.Setenv("GNUPGHOME", home)

	atts := []OutboundAttachment{{
		Filename:    "report.pdf",
		ContentType: "application/pdf",
		Data:        []byte("fake-pdf-content"),
	}}

	raw, err := ComposeSignedRaw(
		"att@example.com",
		[]string{"to@example.com"},
		nil,
		"With Attachment",
		"See attached.",
		atts,
		gpgBin, "att@example.com", "",
	)
	require.NoError(t, err)

	// Outer structure: multipart/signed.
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	require.NoError(t, err)

	ct := msg.Header.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(ct)
	require.NoError(t, err)
	assert.Equal(t, "multipart/signed", mediaType)

	// First part should be multipart/mixed (the signed body).
	mr := multipart.NewReader(msg.Body, params["boundary"])
	part, err := mr.NextPart()
	require.NoError(t, err)

	innerCT := part.Header.Get("Content-Type")
	innerMedia, _, err := mime.ParseMediaType(innerCT)
	require.NoError(t, err)
	assert.Equal(t, "multipart/mixed", innerMedia)

	// Second part should be the PGP signature.
	sigPart, err := mr.NextPart()
	require.NoError(t, err)
	assert.Contains(t, sigPart.Header.Get("Content-Type"), "application/pgp-signature")

	sigBody, err := io.ReadAll(sigPart)
	require.NoError(t, err)
	assert.Contains(t, string(sigBody), "BEGIN PGP SIGNATURE")

	// Verify the signed message.
	result, verifyErr := pgp.Verify(gpgBin, raw)
	require.NoError(t, verifyErr)
	assert.True(t, result.Valid, "signed message with attachment should verify")
}

func TestComposeSignedRaw_NonExpiringKeyRejected(t *testing.T) {
	gpgBin, err := exec.LookPath("gpg")
	if err != nil {
		t.Skip("gpg not installed")
	}

	home := gpgHome(t)
	gpgGenKeyNoExpiry(t, gpgBin, home, "No Expiry", "noexpiry@example.com")
	t.Setenv("GNUPGHOME", home)

	_, err = ComposeSignedRaw(
		"noexpiry@example.com",
		[]string{"to@example.com"},
		nil,
		"Subject",
		"Body",
		nil,
		gpgBin, "noexpiry@example.com", "",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "signing key rejected")
}

func TestComposeSignedRaw_HeaderInjection(t *testing.T) {
	gpgBin, err := exec.LookPath("gpg")
	if err != nil {
		t.Skip("gpg not installed")
	}

	tests := []struct {
		name    string
		from    string
		to      []string
		subject string
	}{
		{"from CR", "a\r@b.com", []string{"c@d.com"}, "Hi"},
		{"to LF", "a@b.com", []string{"c\n@d.com"}, "Hi"},
		{"subject CRLF", "a@b.com", []string{"c@d.com"}, "Hi\r\nBcc: evil@evil.com"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ComposeSignedRaw(tc.from, tc.to, nil, tc.subject, "body", nil,
				gpgBin, "signer@example.com", "")
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "CR/LF")
		})
	}
}
