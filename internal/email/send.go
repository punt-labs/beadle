package email

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"time"
)

// OutboundAttachment represents a file to attach to an outgoing email.
// Data is already read from disk — the email package never touches the filesystem.
type OutboundAttachment struct {
	Filename    string // Base filename (used in Content-Disposition)
	ContentType string // MIME type (e.g., "application/pdf")
	Data        []byte // Raw file contents
}

const resendAPIURL = "https://api.resend.com/emails"

// ComposeRaw builds an RFC 822 message for SMTP delivery.
// When attachments is empty, produces a simple text/plain message.
// When attachments is non-empty, produces a multipart/mixed message.
// Returns an error if header fields contain CR/LF (header injection).
func ComposeRaw(from, to, subject, textBody string, attachments []OutboundAttachment) ([]byte, error) {
	for _, field := range []string{from, to, subject} {
		if strings.ContainsAny(field, "\r\n") {
			return nil, fmt.Errorf("header field contains CR/LF")
		}
	}

	var msg bytes.Buffer
	fmt.Fprintf(&msg, "From: %s\r\n", from)
	fmt.Fprintf(&msg, "To: %s\r\n", to)
	fmt.Fprintf(&msg, "Subject: %s\r\n", subject)
	fmt.Fprintf(&msg, "MIME-Version: 1.0\r\n")

	if len(attachments) == 0 {
		fmt.Fprintf(&msg, "Content-Type: text/plain; charset=utf-8\r\n")
		fmt.Fprintf(&msg, "\r\n")
		fmt.Fprintf(&msg, "%s\r\n", textBody)
		return msg.Bytes(), nil
	}

	mw := multipart.NewWriter(&msg)
	fmt.Fprintf(&msg, "Content-Type: multipart/mixed; boundary=%s\r\n", mw.Boundary())
	fmt.Fprintf(&msg, "\r\n")

	// Text body part
	textHeader := make(textproto.MIMEHeader)
	textHeader.Set("Content-Type", "text/plain; charset=utf-8")
	tw, err := mw.CreatePart(textHeader)
	if err != nil {
		return nil, fmt.Errorf("create text part: %w", err)
	}
	fmt.Fprintf(tw, "%s", textBody)

	// Attachment parts
	for _, att := range attachments {
		for _, field := range []string{att.ContentType, att.Filename} {
			if strings.ContainsAny(field, "\r\n") {
				return nil, fmt.Errorf("attachment header field contains CR/LF")
			}
		}
		attHeader := make(textproto.MIMEHeader)
		attHeader.Set("Content-Type", att.ContentType)
		attHeader.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": att.Filename}))
		attHeader.Set("Content-Transfer-Encoding", "base64")

		aw, createErr := mw.CreatePart(attHeader)
		if createErr != nil {
			return nil, fmt.Errorf("create attachment part %q: %w", att.Filename, createErr)
		}
		encoded := base64.StdEncoding.EncodeToString(att.Data)
		for i := 0; i < len(encoded); i += 76 {
			end := i + 76
			if end > len(encoded) {
				end = len(encoded)
			}
			fmt.Fprintf(aw, "%s\r\n", encoded[i:end])
		}
	}

	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("close multipart writer: %w", err)
	}

	return msg.Bytes(), nil
}

// ResendAttachment is a file attachment in the Resend API format.
type ResendAttachment struct {
	Filename string `json:"filename"`
	Content  string `json:"content"` // base64-encoded
}

// SendRequest is the payload for sending an email.
type SendRequest struct {
	To          string             `json:"to"`
	Subject     string             `json:"subject"`
	Text        string             `json:"text,omitempty"`
	HTML        string             `json:"html,omitempty"`
	Attachments []ResendAttachment `json:"attachments,omitempty"`
}

// SendResponse is the Resend API response.
type SendResponse struct {
	ID string `json:"id"`
}

// Send delivers an email via the Resend API.
func Send(cfg *Config, req SendRequest) (*SendResponse, error) {
	apiKey, err := cfg.ResendAPIKey()
	if err != nil {
		return nil, fmt.Errorf("read api key: %w", err)
	}

	payload := struct {
		From string `json:"from"`
		SendRequest
	}{
		From:        cfg.FromAddress,
		SendRequest: req,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequest(http.MethodPost, resendAPIURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("resend API error %d: %s", resp.StatusCode, string(respBody))
	}

	var result SendResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	return &result, nil
}

