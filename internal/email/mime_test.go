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

func TestTruncate(t *testing.T) {
	assert.Equal(t, "abc", truncate("abc", 10))
	assert.Equal(t, "ab...", truncate("abcdef", 2))
}
