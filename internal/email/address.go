package email

import "strings"

// ExtractEmailAddress extracts the email address from a From header value.
// Handles "Name <email@example.com>" and bare "email@example.com" formats.
func ExtractEmailAddress(from string) string {
	from = strings.TrimSpace(from)
	if from == "" {
		return ""
	}
	// Try "Name <email>" format
	if start := strings.LastIndex(from, "<"); start >= 0 {
		if end := strings.Index(from[start:], ">"); end >= 0 {
			return strings.TrimSpace(from[start+1 : start+end])
		}
	}
	// Bare email address
	if strings.Contains(from, "@") {
		return from
	}
	return ""
}
