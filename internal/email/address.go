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

	// Fallback: extract from angle brackets (handles malformed display
	// names like "github-actions[bot] <notifications@github.com>" where
	// unquoted brackets cause RFC 5322 parse failure).
	if start := strings.LastIndex(from, "<"); start != -1 {
		if end := strings.Index(from[start:], ">"); end != -1 {
			candidate := strings.TrimSpace(from[start+1 : start+end])
			if strings.Contains(candidate, "@") {
				return candidate
			}
		}
	}

	// Fallback: bare email with no special characters
	if strings.Contains(from, "@") && !strings.ContainsAny(from, " <>\"\t") {
		return from
	}
	return ""
}

// ExtractDisplayName extracts the display name from a From header value.
// Returns the name part before angle brackets, or the email address if
// no display name is present.
func ExtractDisplayName(from string) string {
	from = strings.TrimSpace(from)
	if from == "" {
		return ""
	}

	// Try stdlib parser first.
	addr, err := mail.ParseAddress(from)
	if err == nil && addr != nil {
		if addr.Name != "" {
			return addr.Name
		}
		return addr.Address
	}

	// Fallback: extract text before angle brackets.
	if idx := strings.LastIndex(from, "<"); idx > 0 {
		name := strings.TrimSpace(from[:idx])
		if name != "" {
			return name
		}
	}

	return from
}
