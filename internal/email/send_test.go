package email

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComposeRaw_NoAttachments(t *testing.T) {
	raw, err := ComposeRaw("a@b.com", "c@d.com", "Hi", "Hello", nil)
	require.NoError(t, err)

	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	require.NoError(t, err)

	assert.Equal(t, "a@b.com", msg.Header.Get("From"))
	assert.Equal(t, "c@d.com", msg.Header.Get("To"))
	assert.Equal(t, "Hi", msg.Header.Get("Subject"))
	assert.Equal(t, "text/plain; charset=utf-8", msg.Header.Get("Content-Type"))

	body, err := io.ReadAll(msg.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), "Hello")
}

func TestComposeRaw_EmptyAttachments(t *testing.T) {
	raw, err := ComposeRaw("a@b.com", "c@d.com", "Hi", "Hello", []OutboundAttachment{})
	require.NoError(t, err)

	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	require.NoError(t, err)
	assert.Equal(t, "text/plain; charset=utf-8", msg.Header.Get("Content-Type"))
}

func TestComposeRaw_OneAttachment(t *testing.T) {
	atts := []OutboundAttachment{{
		Filename:    "report.pdf",
		ContentType: "application/pdf",
		Data:        []byte("fake-pdf-content"),
	}}

	raw, err := ComposeRaw("a@b.com", "c@d.com", "Report", "See attached.", atts)
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

	raw, err := ComposeRaw("a@b.com", "c@d.com", "Files", "Here are files.", atts)
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
		to      string
		subject string
	}{
		{"from CR", "a\r@b.com", "c@d.com", "Hi"},
		{"to LF", "a@b.com", "c\n@d.com", "Hi"},
		{"subject CRLF", "a@b.com", "c@d.com", "Hi\r\nBcc: evil@evil.com"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ComposeRaw(tc.from, tc.to, tc.subject, "body", atts)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "CR/LF")
		})
	}
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
			_, err := ComposeRaw("a@b.com", "c@d.com", "Hi", "body", atts)
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

	raw, err := ComposeRaw("a@b.com", "c@d.com", "Report", "See attached.", atts)
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
