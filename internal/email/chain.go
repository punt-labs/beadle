package email

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/punt-labs/beadle/internal/secret"
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
//
// When cfg.GPGSigner is non-empty, all outbound SMTP is PGP-signed (RFC 3156).
// Resend fallback is blocked when signing is configured because Resend cannot
// preserve raw MIME.
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

	signing := cfg.GPGSigner != ""

	// Resolve passphrase once if signing is enabled.
	var passphrase string
	if signing {
		pp, ppErr := cfg.GPGPassphrase()
		if ppErr != nil {
			if !errors.Is(ppErr, secret.ErrNotFound) {
				return nil, fmt.Errorf("gpg passphrase: %w", ppErr)
			}
			// ErrNotFound is OK — key may not need a passphrase.
		} else {
			passphrase = pp
		}
	}

	// 1. Proton Bridge SMTP
	if SMTPAvailable(cfg) {
		var raw []byte
		var composeErr error

		if signing {
			raw, composeErr = ComposeSignedRaw(cfg.FromAddress, to, cc, subject, body, attachments, cfg.GPGBinary, cfg.GPGSigner, passphrase)
		} else {
			raw, composeErr = ComposeRaw(cfg.FromAddress, to, cc, subject, body, attachments)
		}
		if composeErr != nil {
			return nil, composeErr
		}

		if err := SMTPSend(cfg, cfg.FromAddress, allRecipients, raw); err != nil {
			logger.Warn("smtp send failed, falling back to resend", "err", err)
			if signing {
				return nil, fmt.Errorf("smtp send failed: %w; resend fallback blocked for signed mail", err)
			}
		} else {
			return &SendResult{
				Status:      "sent",
				Method:      "proton-bridge-smtp",
				Signed:      signing,
				From:        cfg.FromAddress,
				To:          toStr,
				Cc:          ccStr,
				BccCount:    len(bcc),
				Subject:     subject,
				Attachments: len(attachments),
			}, nil
		}
	}

	// 2. Resend API fallback — blocked when signing is configured.
	if signing {
		return nil, fmt.Errorf("pgp-signed email requires SMTP transport; Resend API cannot preserve raw MIME")
	}

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
