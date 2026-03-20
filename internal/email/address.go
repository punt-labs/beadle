package email

import (
	"net/mail"
	"strings"
)

// ExtractEmailAddress extracts the email address from a From header value.
// Uses net/mail.ParseAddress for RFC 5322 compliance. Falls back to bare
// address detection for simple "user@domain" strings.
func ExtractEmailAddress(from string) string {
	from = strings.TrimSpace(from)
	if from == "" {
		return ""
	}

	// Use stdlib parser for proper RFC 5322 handling
	addr, err := mail.ParseAddress(from)
	if err == nil && addr != nil {
		return strings.TrimSpace(addr.Address)
	}

	// Fallback: bare email with no special characters
	if strings.Contains(from, "@") && !strings.ContainsAny(from, " <>\"\t") {
		return from
	}
	return ""
}
