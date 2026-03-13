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

func TestTruncate(t *testing.T) {
	assert.Equal(t, "abc", truncate("abc", 10))
	assert.Equal(t, "ab...", truncate("abcdef", 2))
}
