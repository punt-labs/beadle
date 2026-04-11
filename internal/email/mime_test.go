package email

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseMIME_PlainText(t *testing.T) {
	raw := []byte("From: test@example.com\r\nSubject: Hello\r\nContent-Type: text/plain\r\n\r\nHello, world!")

	body, attachments, headers := ParseMIME(raw)

	assert.Equal(t, "Hello, world!", body)
	assert.Empty(t, attachments)
	assert.Equal(t, "test@example.com", headers["From"])
	assert.Equal(t, "Hello", headers["Subject"])
}

func TestParseMIME_NoContentType(t *testing.T) {
	raw := []byte("From: test@example.com\r\nSubject: Test\r\n\r\nPlain text body")

	body, _, _ := ParseMIME(raw)

	assert.Equal(t, "Plain text body", body)
}

func TestParseMIMEStructure_SinglePart(t *testing.T) {
	raw := []byte("From: test@example.com\r\nContent-Type: text/plain\r\n\r\nBody content")

	parts, err := ParseMIMEStructure(raw)

	assert.NoError(t, err)
	assert.Len(t, parts, 1)
	assert.Equal(t, "text/plain", parts[0].ContentType)
	assert.Contains(t, parts[0].Content, "Body content")
}

func TestExtractPart_SinglePart(t *testing.T) {
	raw := []byte("From: test@example.com\r\nContent-Type: text/plain\r\n\r\nBody content")

	part, data, err := ExtractPart(raw, 0)
	assert.NoError(t, err)
	assert.Equal(t, "text/plain", part.ContentType)
	assert.Equal(t, "Body content", string(data))
}

func TestExtractPart_SinglePartOutOfRange(t *testing.T) {
	raw := []byte("From: test@example.com\r\nContent-Type: text/plain\r\n\r\nBody")

	_, _, err := ExtractPart(raw, 1)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "out of range")
}

func TestExtractPart_Multipart(t *testing.T) {
	raw := []byte("From: test@example.com\r\n" +
		"Content-Type: multipart/mixed; boundary=BOUNDARY\r\n" +
		"\r\n" +
		"--BOUNDARY\r\n" +
		"Content-Type: text/plain\r\n" +
		"\r\n" +
		"Hello\r\n" +
		"--BOUNDARY\r\n" +
		"Content-Type: application/pdf\r\n" +
		"Content-Disposition: attachment; filename=\"report.pdf\"\r\n" +
		"\r\n" +
		"fake-pdf\r\n" +
		"--BOUNDARY--\r\n")

	part, data, err := ExtractPart(raw, 0)
	assert.NoError(t, err)
	assert.Equal(t, "text/plain", part.ContentType)
	assert.Contains(t, string(data), "Hello")

	part, data, err = ExtractPart(raw, 1)
	assert.NoError(t, err)
	assert.Equal(t, "application/pdf", part.ContentType)
	assert.Equal(t, "report.pdf", part.Filename)
	assert.Equal(t, "attachment", part.Disposition)
	assert.Contains(t, string(data), "fake-pdf")
}

func TestExtractPart_MultipartOutOfRange(t *testing.T) {
	raw := []byte("From: test@example.com\r\n" +
		"Content-Type: multipart/mixed; boundary=BOUNDARY\r\n" +
		"\r\n" +
		"--BOUNDARY\r\n" +
		"Content-Type: text/plain\r\n" +
		"\r\n" +
		"Hello\r\n" +
		"--BOUNDARY--\r\n")

	_, _, err := ExtractPart(raw, 5)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "out of range")
}

func TestParseMIME_NestedMultipartAlternative(t *testing.T) {
	// multipart/signed wrapping multipart/alternative (text + HTML) + signature
	raw := []byte("From: jim@example.com\r\n" +
		"Subject: Signed reply\r\n" +
		"Content-Type: multipart/signed; boundary=SIG; micalg=pgp-sha256; protocol=\"application/pgp-signature\"\r\n" +
		"\r\n" +
		"--SIG\r\n" +
		"Content-Type: multipart/alternative; boundary=ALT\r\n" +
		"\r\n" +
		"--ALT\r\n" +
		"Content-Type: text/plain\r\n" +
		"\r\n" +
		"Hello from GPG Mail\r\n" +
		"--ALT\r\n" +
		"Content-Type: text/html\r\n" +
		"\r\n" +
		"<p>Hello from GPG Mail</p>\r\n" +
		"--ALT--\r\n" +
		"--SIG\r\n" +
		"Content-Type: application/pgp-signature\r\n" +
		"\r\n" +
		"-----BEGIN PGP SIGNATURE-----\r\nfake\r\n-----END PGP SIGNATURE-----\r\n" +
		"--SIG--\r\n")

	body, attachments, headers := ParseMIME(raw)

	assert.Equal(t, "Hello from GPG Mail", body)
	assert.Empty(t, attachments)
	assert.Equal(t, "jim@example.com", headers["From"])
}

func TestParseMIME_NestedMultipartMixed(t *testing.T) {
	// multipart/signed wrapping multipart/mixed (text + attachment) + signature
	raw := []byte("From: jim@example.com\r\n" +
		"Subject: Signed with attachment\r\n" +
		"Content-Type: multipart/signed; boundary=SIG; micalg=pgp-sha256; protocol=\"application/pgp-signature\"\r\n" +
		"\r\n" +
		"--SIG\r\n" +
		"Content-Type: multipart/mixed; boundary=MIX\r\n" +
		"\r\n" +
		"--MIX\r\n" +
		"Content-Type: text/plain\r\n" +
		"\r\n" +
		"See attached report\r\n" +
		"--MIX\r\n" +
		"Content-Type: application/pdf\r\n" +
		"Content-Disposition: attachment; filename=\"report.pdf\"\r\n" +
		"\r\n" +
		"fake-pdf-data\r\n" +
		"--MIX--\r\n" +
		"--SIG\r\n" +
		"Content-Type: application/pgp-signature\r\n" +
		"\r\n" +
		"-----BEGIN PGP SIGNATURE-----\r\nfake\r\n-----END PGP SIGNATURE-----\r\n" +
		"--SIG--\r\n")

	body, attachments, _ := ParseMIME(raw)

	assert.Equal(t, "See attached report", body)
	assert.Len(t, attachments, 1)
	assert.Equal(t, "report.pdf", attachments[0].Filename)
	assert.Equal(t, "application/pdf", attachments[0].ContentType)
}

func TestParseMIME_TripleNested(t *testing.T) {
	// multipart/signed → multipart/mixed → multipart/alternative + attachment + signature
	raw := []byte("From: jim@example.com\r\n" +
		"Subject: Triple nested\r\n" +
		"Content-Type: multipart/signed; boundary=SIG; micalg=pgp-sha256; protocol=\"application/pgp-signature\"\r\n" +
		"\r\n" +
		"--SIG\r\n" +
		"Content-Type: multipart/mixed; boundary=MIX\r\n" +
		"\r\n" +
		"--MIX\r\n" +
		"Content-Type: multipart/alternative; boundary=ALT\r\n" +
		"\r\n" +
		"--ALT\r\n" +
		"Content-Type: text/plain\r\n" +
		"\r\n" +
		"Deep text body\r\n" +
		"--ALT\r\n" +
		"Content-Type: text/html\r\n" +
		"\r\n" +
		"<p>Deep text body</p>\r\n" +
		"--ALT--\r\n" +
		"--MIX\r\n" +
		"Content-Type: image/png\r\n" +
		"Content-Disposition: attachment; filename=\"screenshot.png\"\r\n" +
		"\r\n" +
		"fake-png-data\r\n" +
		"--MIX--\r\n" +
		"--SIG\r\n" +
		"Content-Type: application/pgp-signature\r\n" +
		"\r\n" +
		"-----BEGIN PGP SIGNATURE-----\r\nfake\r\n-----END PGP SIGNATURE-----\r\n" +
		"--SIG--\r\n")

	body, attachments, _ := ParseMIME(raw)

	assert.Equal(t, "Deep text body", body)
	assert.Len(t, attachments, 1)
	assert.Equal(t, "screenshot.png", attachments[0].Filename)
	assert.Equal(t, "image/png", attachments[0].ContentType)
}

func TestTruncate(t *testing.T) {
	assert.Equal(t, "abc", truncate("abc", 10))
	assert.Equal(t, "ab...", truncate("abcdef", 2))
}
