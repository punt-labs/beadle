package mcp

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/punt-labs/beadle/internal/channel"
)

func TestFormatMessages_SurfacesSenderEmail(t *testing.T) {
	// beadle-0he regression: every row of list_messages output must
	// surface both the display name and the email address, regardless
	// of contact state or read permission.
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

	assert.Contains(t, got, "FROM", "header should include FROM column")
	assert.Contains(t, got, "EMAIL", "header should include EMAIL column")
	assert.Contains(t, got, "J Freeman", "display name must be visible")
	assert.Contains(t, got, "notifications@github.com", "email must be visible even when subject is redacted")
}

func TestFormatMessages_BareEmailNoDisplayName(t *testing.T) {
	// When the From header carries only a bare address, the FROM column
	// should stay empty rather than duplicating the email.
	when := time.Date(2026, 4, 7, 9, 0, 0, 0, time.UTC)
	msgs := []channel.MessageSummary{{
		ID:         "7",
		From:       "ops@vendor.example",
		Date:       when,
		Subject:    "Nightly report",
		TrustLevel: channel.Unverified,
	}}

	got := formatMessages(msgs, 1)

	assert.Contains(t, got, "ops@vendor.example", "email must be visible")
	assert.Contains(t, got, "Nightly report")

	// The email must appear exactly once — once in the EMAIL column,
	// not also duplicated in FROM.
	assert.Equal(t, 1, strings.Count(got, "ops@vendor.example"),
		"bare email should not be duplicated across FROM and EMAIL")
}

func TestFormatMessages_LongDisplayNameTruncatedEmailIntact(t *testing.T) {
	// When the display name is long enough that FROM + EMAIL + SUBJECT
	// would blow the 80-col budget, the display name is truncated with
	// an ellipsis. The email is never truncated.
	longName := "A Very Long Display Name That Exceeds The Column Budget"
	longEmail := "very-long-local-part-for-testing@subdomain.example.org"

	when := time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC)
	msgs := []channel.MessageSummary{{
		ID:         "42",
		From:       longName + " <" + longEmail + ">",
		Date:       when,
		Subject:    "hello",
		TrustLevel: channel.Unverified,
	}}

	got := formatMessages(msgs, 1)

	assert.Contains(t, got, longEmail, "email must never be truncated")
	assert.NotContains(t, got, longName, "long display name should be truncated")
	// truncateRunes keeps (maxRunes - 1) runes and appends "…" for a
	// total width of maxRunes. With maxDisplayNameRunes == 20, the
	// first 19 runes of longName are "A Very Long Display" (ends at
	// 'y'), so the truncated output is exactly this literal. Fails
	// loudly if maxDisplayNameRunes changes.
	assert.Contains(t, got, "A Very Long Display…",
		"display name should be ellipsis-truncated at maxDisplayNameRunes")
}

func TestFormatMessages_EmptyListNoTable(t *testing.T) {
	got := formatMessages(nil, 0)
	assert.Equal(t, "No messages.", got)
}

func TestFormatMessages_CountLine(t *testing.T) {
	when := time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC)
	msgs := []channel.MessageSummary{{
		ID: "1", From: "a@b.com", Date: when, Subject: "x",
		TrustLevel: channel.Unverified,
	}}
	got := formatMessages(msgs, 42)
	assert.Contains(t, got, "showing 1 of 42 messages")
}
