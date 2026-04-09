package mcp

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/punt-labs/beadle/internal/channel"
)

// beadle-z34: when the display name does not correspond to the email
// address identity, the FROM column must carry a "(via <domain>)"
// annotation so a skimming reader cannot misattribute the message.

func TestFormatFromCell(t *testing.T) {
	tests := []struct {
		name    string
		display string
		addr    string
		want    string
	}{
		{
			name:    "github notification relay with person name",
			display: "J Freeman",
			addr:    "notifications@github.com",
			want:    "J Freeman (via github)",
		},
		{
			name:    "ordinary sender with matching local-part",
			display: "Alice Chen",
			addr:    "alice@example.com",
			want:    "Alice Chen",
		},
		{
			name:    "ordinary sender name matches domain label",
			display: "Jim Freeman",
			addr:    "jim@punt-labs.com",
			want:    "Jim Freeman",
		},
		{
			name:    "lowercase display name exact-matches local-part",
			display: "jim",
			addr:    "jim@example.com",
			want:    "jim",
		},
		{
			name:    "display name spoof from unrelated domain",
			display: "Jim Freeman",
			addr:    "attacker@evil.example",
			want:    "Jim Freeman (via evil)",
		},
		{
			name:    "noreply automation even when name matches",
			display: "GitHub",
			addr:    "noreply@github.com",
			want:    "GitHub (via github)",
		},
		{
			name:    "dependabot bot marker in display name",
			display: "dependabot[bot]",
			addr:    "49699333+dependabot[bot]@users.noreply.github.com",
			want:    "dependabot[bot] (via github)",
		},
		{
			// Display name chosen to avoid token collision with the
			// "ci" subdomain label — "Build Server" has no tokens
			// that prefix-match "ci", "vercel", "app", or "bot", so
			// isRelay flags it and relayDomainLabel picks the
			// registrable label "vercel" (second-to-last) instead of
			// the subdomain "ci" (last non-TLD).
			name:    "subdomain relay uses registrable label",
			display: "Build Server",
			addr:    "bot@ci.vercel.app",
			want:    "Build Server (via vercel)",
		},
		{
			name:    "bare address (no display name) unchanged",
			display: "",
			addr:    "ops@vendor.example",
			want:    "",
		},
		{
			name:    "empty address falls through",
			display: "Unknown",
			addr:    "",
			want:    "Unknown",
		},
		{
			name:    "token-prefix match: Rob to robert",
			display: "Rob",
			addr:    "robert@example.com",
			want:    "Rob",
		},
		{
			name:    "single-letter initial does not match by itself",
			display: "J Q",
			addr:    "notifications@github.com",
			want:    "J Q (via github)",
		},
		{
			// Truncation happens before annotation — a 32-rune name
			// becomes 19 runes + "…" and the annotation is appended
			// to that. The rendered cell is 19 + "… (via github)" so
			// the truncated marker lives inside the cell, not after
			// the annotation. Regression guard for the
			// truncation+annotation interaction.
			name:    "long name triggers both truncation and annotation",
			display: "A Very Long Name That Definitely Exceeds Twenty",
			addr:    "notifications@github.com",
			want:    "A Very Long Name Th… (via github)",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := formatFromCell(tc.display, tc.addr)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestFormatMessages_RelayAnnotation(t *testing.T) {
	// End-to-end through formatMessages: the rendered table should
	// show "(via github)" next to the display name and the raw
	// notifications@github.com address in the EMAIL column.
	when := time.Date(2026, 4, 7, 17, 15, 0, 0, time.UTC)
	msgs := []channel.MessageSummary{{
		ID:         "20",
		From:       "J Freeman <notifications@github.com>",
		Date:       when,
		Subject:    "[redacted — no read permission]",
		TrustLevel: channel.Unverified,
		Unread:     true,
	}}

	got := formatMessages(msgs, 1)

	assert.Contains(t, got, "J Freeman (via github)",
		"FROM cell must annotate the relay so the reader does not attribute the message to J Freeman")
	assert.Contains(t, got, "notifications@github.com",
		"EMAIL column must still carry the raw address")
}

func TestFormatMessages_OrdinarySenderNotAnnotated(t *testing.T) {
	// A legitimate sender whose display name corresponds to the
	// email address must not be annotated.
	when := time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC)
	msgs := []channel.MessageSummary{{
		ID:         "8",
		From:       "Alice Chen <alice@example.com>",
		Date:       when,
		Subject:    "lunch thursday?",
		TrustLevel: channel.Unverified,
	}}

	got := formatMessages(msgs, 1)

	assert.Contains(t, got, "Alice Chen")
	assert.Contains(t, got, "alice@example.com")
	assert.NotContains(t, got, "(via",
		"ordinary senders must not carry a relay annotation")
}

func TestFormatMessages_DisplayNameSpoofAnnotated(t *testing.T) {
	// The worst case: attacker uses a trusted party's display name
	// from a hostile domain. beadle's permission system correctly
	// keys on the email address, so the message gets no trust. But
	// the table must also refuse to render the spoofed name as if
	// it identified the sender.
	when := time.Date(2026, 4, 7, 10, 0, 0, 0, time.UTC)
	msgs := []channel.MessageSummary{{
		ID:         "99",
		From:       "Jim Freeman <attacker@evil.example>",
		Date:       when,
		Subject:    "urgent wire transfer",
		TrustLevel: channel.Untrusted,
	}}

	got := formatMessages(msgs, 1)

	assert.Contains(t, got, "Jim Freeman (via evil)",
		"spoofed display name must be annotated so the reader sees it came from elsewhere")
	assert.Contains(t, got, "attacker@evil.example")
}

func TestFormatMessages_NameMatchesLocalPartNotAnnotated(t *testing.T) {
	// `"jim" <jim@example.com>` is the exact case the design doc
	// called out as a false positive risk for any naive heuristic.
	// Must not be annotated.
	when := time.Date(2026, 4, 7, 9, 0, 0, 0, time.UTC)
	msgs := []channel.MessageSummary{{
		ID:         "1",
		From:       `"jim" <jim@example.com>`,
		Date:       when,
		Subject:    "status",
		TrustLevel: channel.Unverified,
	}}

	got := formatMessages(msgs, 1)

	assert.NotContains(t, got, "(via",
		"display name equal to local-part must not be annotated")
}

func TestIsRelay(t *testing.T) {
	tests := []struct {
		name    string
		display string
		addr    string
		want    bool
	}{
		{"github relay", "J Freeman", "notifications@github.com", true},
		{"ordinary match", "Alice Chen", "alice@example.com", false},
		{"noreply automation", "GitHub", "noreply@github.com", true},
		{"do-not-reply automation", "Bank", "do-not-reply@bank.example", true},
		{"bot suffix on local", "Renovate", "renovate-bot@renovate.example", true},
		{"bot marker in name", "dependabot[bot]", "x@y.example", true},
		{"bare address (no name to mislead)", "", "noreply@github.com", false},
		{"domain label matches name", "Punt Labs", "x@punt-labs.com", false},
		{"first name alone matches local", "Alice", "alice.chen@example.com", false},
		{"last name alone matches local", "Chen", "alice.chen@example.com", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isRelay(tc.display, tc.addr)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestRelayDomainLabel(t *testing.T) {
	tests := []struct {
		name string
		addr string
		want string
	}{
		{"two-label domain", "x@github.com", "github"},
		{"three-label domain", "x@ci.vercel.app", "vercel"},
		{"four-label domain", "x@a.b.example.co.uk", "co"},
		{"empty address", "", ""},
		{"no at sign", "garbage", ""},
		{"very long label truncated", "x@" + strings.Repeat("a", 40) + ".com",
			"aaaaaaaaaaa…"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := relayDomainLabel(tc.addr)
			assert.Equal(t, tc.want, got)
		})
	}
}
