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

	"github.com/punt-labs/beadle/internal/pgp"
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
// Bcc recipients are intentionally excluded from headers per RFC 822.
func ComposeRaw(from string, to, cc []string, subject, textBody string, attachments []OutboundAttachment) ([]byte, error) {
	allAddrs := make([]string, 0, len(to)+len(cc))
	allAddrs = append(allAddrs, to...)
	allAddrs = append(allAddrs, cc...)
	for _, field := range append([]string{from, subject}, allAddrs...) {
		if strings.ContainsAny(field, "\r\n") {
			return nil, fmt.Errorf("header field contains CR/LF")
		}
	}

	var msg bytes.Buffer
	fmt.Fprintf(&msg, "From: %s\r\n", from)
	fmt.Fprintf(&msg, "To: %s\r\n", strings.Join(to, ", "))
	if len(cc) > 0 {
		fmt.Fprintf(&msg, "Cc: %s\r\n", strings.Join(cc, ", "))
	}
	fmt.Fprintf(&msg, "Subject: %s\r\n", subject)
	fmt.Fprintf(&msg, "MIME-Version: 1.0\r\n")

	if len(attachments) == 0 {
		fmt.Fprintf(&msg, "Content-Type: text/plain; charset=utf-8\r\n")
		fmt.Fprintf(&msg, "\r\n")
		fmt.Fprintf(&msg, "%s\r\n", textBody)
		return msg.Bytes(), nil
	}

	mw := multipart.NewWriter(&msg)
	ct := mime.FormatMediaType("multipart/mixed", map[string]string{"boundary": mw.Boundary()})
	fmt.Fprintf(&msg, "Content-Type: %s\r\n", ct)
	fmt.Fprintf(&msg, "\r\n")

	// Text body part
	textHeader := make(textproto.MIMEHeader)
	textHeader.Set("Content-Type", "text/plain; charset=utf-8")
	tw, err := mw.CreatePart(textHeader)
	if err != nil {
		return nil, fmt.Errorf("create text part: %w", err)
	}
	fmt.Fprintf(tw, "%s\r\n", textBody)

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

// ComposeSignedRaw builds a PGP/MIME signed RFC 822 message (RFC 3156).
// The body (text/plain or multipart/mixed with attachments) is signed with
// gpg --detach-sign, then wrapped in a multipart/signed envelope.
func ComposeSignedRaw(from string, to, cc []string, subject, textBody string, attachments []OutboundAttachment, gpgBinary, signer, passphrase string) ([]byte, error) {
	allAddrs := make([]string, 0, len(to)+len(cc))
	allAddrs = append(allAddrs, to...)
	allAddrs = append(allAddrs, cc...)
	for _, field := range append([]string{from, subject}, allAddrs...) {
		if strings.ContainsAny(field, "\r\n") {
			return nil, fmt.Errorf("header field contains CR/LF")
		}
	}

	// Canonicalize to CRLF (RFC 3156 requires canonical line endings in signed parts).
	textBody = strings.ReplaceAll(textBody, "\r\n", "\n")
	textBody = strings.ReplaceAll(textBody, "\n", "\r\n")

	// Build the body part that will be signed.
	var bodyPart []byte
	if len(attachments) == 0 {
		bodyPart = []byte("Content-Type: text/plain; charset=utf-8\r\n" +
			"Content-Transfer-Encoding: 7bit\r\n" +
			"\r\n" +
			textBody + "\r\n")
	} else {
		bp, err := buildMixedBodyPart(textBody, attachments)
		if err != nil {
			return nil, err
		}
		bodyPart = bp
	}

	sig, err := pgp.DetachSignBody(gpgBinary, signer, passphrase, bodyPart)
	if err != nil {
		return nil, fmt.Errorf("sign body: %w", err)
	}

	boundary, err := pgp.RandomBoundary()
	if err != nil {
		return nil, fmt.Errorf("generate boundary: %w", err)
	}

	var msg bytes.Buffer
	fmt.Fprintf(&msg, "From: %s\r\n", from)
	fmt.Fprintf(&msg, "To: %s\r\n", strings.Join(to, ", "))
	if len(cc) > 0 {
		fmt.Fprintf(&msg, "Cc: %s\r\n", strings.Join(cc, ", "))
	}
	fmt.Fprintf(&msg, "Subject: %s\r\n", subject)
	fmt.Fprintf(&msg, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&msg, "Content-Type: multipart/signed; boundary=%q; micalg=pgp-sha256; protocol=\"application/pgp-signature\"\r\n", boundary)
	fmt.Fprintf(&msg, "\r\n")
	fmt.Fprintf(&msg, "--%s\r\n", boundary)
	msg.Write(bodyPart)
	fmt.Fprintf(&msg, "\r\n--%s\r\n", boundary)
	fmt.Fprintf(&msg, "Content-Type: application/pgp-signature; name=\"signature.asc\"\r\n")
	fmt.Fprintf(&msg, "Content-Disposition: attachment; filename=\"signature.asc\"\r\n")
	fmt.Fprintf(&msg, "\r\n")
	msg.Write(bytes.TrimSpace(sig))
	fmt.Fprintf(&msg, "\r\n--%s--\r\n", boundary)

	return msg.Bytes(), nil
}

// buildMixedBodyPart assembles a complete multipart/mixed MIME part (including
// its Content-Type header and all boundaries) for use as the signed body in a
// multipart/signed envelope.
func buildMixedBodyPart(textBody string, attachments []OutboundAttachment) ([]byte, error) {
	// Canonicalize to CRLF (RFC 3156 requires canonical line endings in signed parts).
	textBody = strings.ReplaceAll(textBody, "\r\n", "\n")
	textBody = strings.ReplaceAll(textBody, "\n", "\r\n")

	for _, att := range attachments {
		for _, field := range []string{att.ContentType, att.Filename} {
			if strings.ContainsAny(field, "\r\n") {
				return nil, fmt.Errorf("attachment header field contains CR/LF")
			}
		}
	}

	// We need to control the exact bytes, so we build the multipart by hand
	// using a multipart.Writer into a sub-buffer, then prepend the Content-Type.
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)

	ct := mime.FormatMediaType("multipart/mixed", map[string]string{"boundary": mw.Boundary()})
	header := fmt.Sprintf("Content-Type: %s\r\n\r\n", ct)

	// Text part
	textHeader := make(textproto.MIMEHeader)
	textHeader.Set("Content-Type", "text/plain; charset=utf-8")
	tw, err := mw.CreatePart(textHeader)
	if err != nil {
		return nil, fmt.Errorf("create text part: %w", err)
	}
	fmt.Fprintf(tw, "%s\r\n", textBody)

	// Attachment parts
	for _, att := range attachments {
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

	// Concatenate header + body to form the complete MIME part.
	var result bytes.Buffer
	result.WriteString(header)
	result.Write(body.Bytes())
	return result.Bytes(), nil
}

// ComposeEncryptedSignedRaw builds a PGP/MIME encrypted+signed message (RFC 3156).
// Signs the body first, then encrypts the signed body to the recipient keys.
// The result is a multipart/encrypted envelope containing the signed message.
func ComposeEncryptedSignedRaw(from string, to, cc []string, subject, textBody string,
	attachments []OutboundAttachment, gpgBinary, signer, passphrase string,
	recipientKeyIDs []string) ([]byte, error) {

	allAddrs := make([]string, 0, len(to)+len(cc))
	allAddrs = append(allAddrs, to...)
	allAddrs = append(allAddrs, cc...)
	for _, field := range append([]string{from, subject}, allAddrs...) {
		if strings.ContainsAny(field, "\r\n") {
			return nil, fmt.Errorf("header field contains CR/LF")
		}
	}

	// Canonicalize to CRLF (RFC 3156 requires canonical line endings in signed parts).
	textBody = strings.ReplaceAll(textBody, "\r\n", "\n")
	textBody = strings.ReplaceAll(textBody, "\n", "\r\n")

	// Build the body part that will be signed.
	var bodyPart []byte
	if len(attachments) == 0 {
		bodyPart = []byte("Content-Type: text/plain; charset=utf-8\r\n" +
			"Content-Transfer-Encoding: 7bit\r\n" +
			"\r\n" +
			textBody + "\r\n")
	} else {
		bp, err := buildMixedBodyPart(textBody, attachments)
		if err != nil {
			return nil, err
		}
		bodyPart = bp
	}

	sig, err := pgp.DetachSignBody(gpgBinary, signer, passphrase, bodyPart)
	if err != nil {
		return nil, fmt.Errorf("sign body: %w", err)
	}

	signBoundary, err := pgp.RandomBoundary()
	if err != nil {
		return nil, fmt.Errorf("generate sign boundary: %w", err)
	}

	// Assemble the inner multipart/signed part.
	var signedPart bytes.Buffer
	fmt.Fprintf(&signedPart, "Content-Type: multipart/signed; boundary=%q; micalg=pgp-sha256; protocol=\"application/pgp-signature\"\r\n", signBoundary)
	fmt.Fprintf(&signedPart, "\r\n")
	fmt.Fprintf(&signedPart, "--%s\r\n", signBoundary)
	signedPart.Write(bodyPart)
	fmt.Fprintf(&signedPart, "\r\n--%s\r\n", signBoundary)
	fmt.Fprintf(&signedPart, "Content-Type: application/pgp-signature; name=\"signature.asc\"\r\n")
	fmt.Fprintf(&signedPart, "Content-Disposition: attachment; filename=\"signature.asc\"\r\n")
	fmt.Fprintf(&signedPart, "\r\n")
	signedPart.Write(bytes.TrimSpace(sig))
	fmt.Fprintf(&signedPart, "\r\n--%s--\r\n", signBoundary)

	// Encrypt the signed body to all recipient keys (+ self if signer is set).
	ciphertext, err := pgp.Encrypt(gpgBinary, recipientKeyIDs, signer, signedPart.Bytes())
	if err != nil {
		return nil, fmt.Errorf("encrypt signed body: %w", err)
	}

	encBoundary, err := pgp.RandomBoundary()
	if err != nil {
		return nil, fmt.Errorf("generate encrypt boundary: %w", err)
	}

	// Assemble the outer RFC 822 message with multipart/encrypted envelope.
	var msg bytes.Buffer
	fmt.Fprintf(&msg, "From: %s\r\n", from)
	fmt.Fprintf(&msg, "To: %s\r\n", strings.Join(to, ", "))
	if len(cc) > 0 {
		fmt.Fprintf(&msg, "Cc: %s\r\n", strings.Join(cc, ", "))
	}
	fmt.Fprintf(&msg, "Subject: %s\r\n", subject)
	fmt.Fprintf(&msg, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&msg, "Content-Type: multipart/encrypted; boundary=%q; protocol=\"application/pgp-encrypted\"\r\n", encBoundary)
	fmt.Fprintf(&msg, "\r\n")
	fmt.Fprintf(&msg, "--%s\r\n", encBoundary)
	fmt.Fprintf(&msg, "Content-Type: application/pgp-encrypted\r\n")
	fmt.Fprintf(&msg, "Content-Description: PGP/MIME version identification\r\n")
	fmt.Fprintf(&msg, "\r\n")
	fmt.Fprintf(&msg, "Version: 1\r\n")
	fmt.Fprintf(&msg, "\r\n")
	fmt.Fprintf(&msg, "--%s\r\n", encBoundary)
	fmt.Fprintf(&msg, "Content-Type: application/octet-stream; name=\"encrypted.asc\"\r\n")
	fmt.Fprintf(&msg, "Content-Disposition: inline; filename=\"encrypted.asc\"\r\n")
	fmt.Fprintf(&msg, "\r\n")
	msg.Write(bytes.TrimSpace(ciphertext))
	fmt.Fprintf(&msg, "\r\n--%s--\r\n", encBoundary)

	return msg.Bytes(), nil
}

// ResendAttachment is a file attachment in the Resend API format.
type ResendAttachment struct {
	Filename string `json:"filename"`
	Content  string `json:"content"` // base64-encoded
}

// SendRequest is the payload for sending an email.
// To, Cc, and Bcc are string arrays matching the Resend API format.
type SendRequest struct {
	To          []string           `json:"to"`
	Cc          []string           `json:"cc,omitempty"`
	Bcc         []string           `json:"bcc,omitempty"`
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
