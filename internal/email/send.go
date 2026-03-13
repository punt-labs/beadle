package email

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const resendAPIURL = "https://api.resend.com/emails"

// ComposeRaw builds a simple RFC 822 message (no signing, no HTML).
// Used for Proton Bridge SMTP where Proton handles its own encryption.
// Returns an error if header fields contain CR/LF (header injection).
func ComposeRaw(from, to, subject, textBody string) ([]byte, error) {
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
	fmt.Fprintf(&msg, "Content-Type: text/plain; charset=utf-8\r\n")
	fmt.Fprintf(&msg, "\r\n")
	fmt.Fprintf(&msg, "%s\r\n", textBody)
	return msg.Bytes(), nil
}

// SendRequest is the payload for sending an email.
type SendRequest struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	Text    string `json:"text,omitempty"`
	HTML    string `json:"html,omitempty"`
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

	payload := map[string]string{
		"from":    cfg.FromAddress,
		"to":      req.To,
		"subject": req.Subject,
	}
	if req.Text != "" {
		payload["text"] = req.Text
	}
	if req.HTML != "" {
		payload["html"] = req.HTML
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

