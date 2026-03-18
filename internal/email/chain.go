package email

import (
	"encoding/base64"
	"fmt"
	"log/slog"
	"strings"
)

// SendResult is the result of a send attempt through the transport chain.
// Bcc is intentionally omitted — BCC addresses are confidential by design.
type SendResult struct {
	Status      string `json:"status"`
	Method      string `json:"method"`
	Signed      bool   `json:"signed"`
	ID          string `json:"id,omitempty"`
	From        string `json:"from"`
	To          string `json:"to"`
	Cc          string `json:"cc,omitempty"`
	BccCount    int    `json:"bcc_count,omitempty"`
	Subject     string `json:"subject"`
	Attachments int    `json:"attachments"`
}

// TrySendChain attempts to send via the best available method:
// 1. Proton Bridge SMTP (primary)
// 2. Resend API (fallback)
func TrySendChain(cfg *Config, logger *slog.Logger, to, cc, bcc []string, subject, body, html string, attachments []OutboundAttachment) (*SendResult, error) {
	// Validate BCC addresses for CR/LF injection.
	for _, addr := range bcc {
		if strings.ContainsAny(addr, "\r\n") {
			return nil, fmt.Errorf("bcc address contains CR/LF")
		}
	}

	allRecipients := make([]string, 0, len(to)+len(cc)+len(bcc))
	allRecipients = append(allRecipients, to...)
	allRecipients = append(allRecipients, cc...)
	allRecipients = append(allRecipients, bcc...)

	toStr := strings.Join(to, ", ")
	ccStr := strings.Join(cc, ", ")

	// 1. Proton Bridge SMTP
	if SMTPAvailable(cfg) {
		raw, composeErr := ComposeRaw(cfg.FromAddress, to, cc, subject, body, attachments)
		if composeErr != nil {
			return nil, composeErr
		}
		if err := SMTPSend(cfg, cfg.FromAddress, allRecipients, raw); err != nil {
			logger.Warn("smtp send failed, falling back to resend", "err", err)
		} else {
			return &SendResult{
				Status:      "sent",
				Method:      "proton-bridge-smtp",
				From:        cfg.FromAddress,
				To:          toStr,
				Cc:          ccStr,
				BccCount:    len(bcc),
				Subject:     subject,
				Attachments: len(attachments),
			}, nil
		}
	}

	// 2. Resend API fallback
	var resendAtts []ResendAttachment
	for _, att := range attachments {
		resendAtts = append(resendAtts, ResendAttachment{
			Filename: att.Filename,
			Content:  base64.StdEncoding.EncodeToString(att.Data),
		})
	}

	resp, err := Send(cfg, SendRequest{
		To:          to,
		Cc:          cc,
		Bcc:         bcc,
		Subject:     subject,
		Text:        body,
		HTML:        html,
		Attachments: resendAtts,
	})
	if err != nil {
		return nil, err
	}

	return &SendResult{
		Status:      "sent",
		Method:      "resend",
		ID:          resp.ID,
		From:        cfg.FromAddress,
		To:          toStr,
		Cc:          ccStr,
		BccCount:    len(bcc),
		Subject:     subject,
		Attachments: len(attachments),
	}, nil
}
