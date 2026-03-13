package email

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"net/mail"
	"strings"

	"github.com/emersion/go-message"

	"github.com/punt-labs/beadle/internal/channel"
)

// MIMEPart describes one part of a MIME message structure.
type MIMEPart struct {
	Index       int    `json:"index"`
	ContentType string `json:"content_type"`
	Filename    string `json:"filename,omitempty"`
	Disposition string `json:"disposition,omitempty"`
	Size        int    `json:"size"`
	Content     string `json:"content,omitempty"` // For text parts
}

// ParseMIME extracts the plain-text body, attachments, and selected headers
// from raw RFC822 bytes.
func ParseMIME(raw []byte) (body string, attachments []channel.Attachment, headers map[string]string) {
	headers = make(map[string]string)

	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return "(parse error)", nil, headers
	}

	// Extract selected headers
	for _, key := range []string{
		"From", "To", "Date", "Subject",
		"X-Pm-Content-Encryption", "X-Pm-Origin",
		"Content-Type",
	} {
		if v := msg.Header.Get(key); v != "" {
			headers[key] = v
		}
	}

	ct := msg.Header.Get("Content-Type")
	if ct == "" {
		ct = "text/plain"
	}

	mediaType, _, _ := mime.ParseMediaType(ct)

	// Simple single-part message
	if !strings.HasPrefix(mediaType, "multipart/") {
		data, err := io.ReadAll(msg.Body)
		if err != nil {
			return "(read error)", nil, headers
		}
		return string(data), nil, headers
	}

	// Multipart — walk parts using go-message
	entity, err := message.Read(bytes.NewReader(raw))
	if err != nil {
		return "(mime parse error)", nil, headers
	}

	mr := entity.MultipartReader()
	if mr == nil {
		return "(not multipart)", nil, headers
	}

	var plainBody, htmlBody string

	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}

		partCT, _, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
		disp, params, _ := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
		filename := params["filename"]

		partData, err := io.ReadAll(part.Body)
		if err != nil {
			continue
		}

		switch {
		case disp == "attachment":
			attachments = append(attachments, channel.Attachment{
				Filename:    filename,
				ContentType: partCT,
				Size:        len(partData),
			})
		case partCT == "text/plain" && plainBody == "":
			plainBody = string(partData)
		case partCT == "text/html" && htmlBody == "":
			htmlBody = string(partData)
		case partCT == "application/pgp-signature",
			partCT == "application/pgp-keys":
			// Skip — handled by trust/PGP verification
		default:
			// Includes nested multipart — for v0.1 the top-level
			// walk handles common cases
		}
	}

	if plainBody != "" {
		body = plainBody
	} else if htmlBody != "" {
		body = htmlBody
	} else {
		body = "(no text body)"
	}

	return body, attachments, headers
}

// ParseMIMEStructure returns a detailed breakdown of all MIME parts.
func ParseMIMEStructure(raw []byte) ([]MIMEPart, error) {
	entity, err := message.Read(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("parse mime: %w", err)
	}

	mr := entity.MultipartReader()
	if mr == nil {
		// Single-part message
		data, _ := io.ReadAll(entity.Body)
		ct, _, _ := mime.ParseMediaType(entity.Header.Get("Content-Type"))
		if ct == "" {
			ct = "text/plain"
		}
		content := ""
		if strings.HasPrefix(ct, "text/") || strings.Contains(ct, "pgp") {
			content = truncate(string(data), 4000)
		}
		return []MIMEPart{{
			Index:       0,
			ContentType: ct,
			Size:        len(data),
			Content:     content,
		}}, nil
	}

	var parts []MIMEPart
	idx := 0

	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}

		ct, _, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
		if ct == "" {
			ct = "application/octet-stream"
		}
		disp, params, _ := mime.ParseMediaType(part.Header.Get("Content-Disposition"))

		data, _ := io.ReadAll(part.Body)

		content := ""
		if strings.HasPrefix(ct, "text/") || strings.Contains(ct, "pgp") {
			content = truncate(string(data), 4000)
		}

		parts = append(parts, MIMEPart{
			Index:       idx,
			ContentType: ct,
			Filename:    params["filename"],
			Disposition: disp,
			Size:        len(data),
			Content:     content,
		})
		idx++
	}

	return parts, nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
