package mcp

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"

	"github.com/punt-labs/beadle/internal/channel"
)

// assertAllRowsExactWidth asserts every non-empty line in rendered (after
// the leading "showing N of M messages" count line) is exactly want runes
// wide. Uses utf8.RuneCountInString — multibyte glyphs (▶, ●, ✓, …, em
// dash) count as 1 rune each.
func assertAllRowsExactWidth(t *testing.T, rendered string, want int) {
	t.Helper()
	lines := strings.Split(rendered, "\n")
	// Drop the "showing N of M messages" leader.
	if len(lines) > 0 && strings.HasPrefix(lines[0], "showing ") {
		lines = lines[1:]
	}
	for i, line := range lines {
		if line == "" {
			continue
		}
		got := utf8.RuneCountInString(line)
		assert.Equal(t, want, got,
			"line %d has width %d, want %d: %q", i, got, want, line)
	}
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

func TestFormatMessages_HeaderRowShape(t *testing.T) {
	when := time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC)
	msgs := []channel.MessageSummary{{
		ID: "1", From: "Alice Chen <alice@example.com>", Date: when,
		Subject: "hello", TrustLevel: channel.Unverified,
	}}
	got := formatMessages(msgs, 1)
	lines := strings.Split(got, "\n")
	// lines[0] is the count line; lines[1] is the header.
	header := lines[1]
	assert.True(t, strings.HasPrefix(header, "\u25b6"),
		"header must start with ▶, got %q", header)
	assert.Contains(t, header, "R")
	assert.Contains(t, header, "FROM")
	assert.Contains(t, header, "DATE")
	assert.Contains(t, header, "T")
	assert.Contains(t, header, "SUBJECT")
	assert.NotContains(t, header, "ID", "ID header is removed in DES-018")
	assert.NotContains(t, header, "EMAIL", "EMAIL column is removed in DES-018")
}

func TestFormatMessages_RowWidthExactly80_ShortEmail(t *testing.T) {
	when := time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC)
	msgs := []channel.MessageSummary{{
		ID:         "7",
		From:       "Alice Chen <alice@example.com>",
		Date:       when,
		Subject:    "lunch thursday?",
		TrustLevel: channel.Unverified,
	}}
	got := formatMessages(msgs, 1)
	assert.Contains(t, got, "Alice Chen <alice@example.com>")
	assertAllRowsExactWidth(t, got, 80)
}

func TestFormatMessages_RowWidthExactly80_LongEmail(t *testing.T) {
	when := time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC)
	msgs := []channel.MessageSummary{{
		ID:         "319",
		From:       "Copilot <notifications@github.com>",
		Date:       when,
		Subject:    "Re: [punt-labs/beadle] fix list_messages format",
		TrustLevel: channel.Unverified,
		Unread:     true,
	}}
	got := formatMessages(msgs, 1)
	assert.Contains(t, got, "notifications@github.com",
		"email must never be truncated")
	assertAllRowsExactWidth(t, got, 80)
}

func TestFormatMessages_RowWidthExactly80_BotNameTruncates(t *testing.T) {
	when := time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC)
	msgs := []channel.MessageSummary{{
		ID:         "322",
		From:       "cursor[bot] <notifications@github.com>",
		Date:       when,
		Subject:    "Re: [punt-labs/beadle] something",
		TrustLevel: channel.Unverified,
		Unread:     true,
	}}
	got := formatMessages(msgs, 1)
	// 10-char nameCap: "cursor[bot]" (11) → "cursor[bo…" (10).
	assert.Contains(t, got, "cursor[bo…")
	assert.Contains(t, got, "notifications@github.com")
	assert.NotContains(t, got, "cursor[bot]",
		"full bracketed bot name should not fit")
	assertAllRowsExactWidth(t, got, 80)
}

func TestFormatMessages_BareEmailNoAngleBrackets(t *testing.T) {
	when := time.Date(2026, 4, 7, 9, 0, 0, 0, time.UTC)
	msgs := []channel.MessageSummary{{
		ID:         "7",
		From:       "ops@vendor.example",
		Date:       when,
		Subject:    "Nightly report",
		TrustLevel: channel.Unverified,
	}}
	got := formatMessages(msgs, 1)
	assert.Contains(t, got, "ops@vendor.example")
	assert.NotContains(t, got, "<ops@vendor.example>",
		"bare email must not be wrapped in angle brackets")
	assert.Equal(t, 1, strings.Count(got, "ops@vendor.example"),
		"bare email should appear exactly once")
	assertAllRowsExactWidth(t, got, 80)
}

func TestFormatMessages_MultibyteDisplayName(t *testing.T) {
	when := time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC)
	msgs := []channel.MessageSummary{{
		ID:         "12",
		From:       "Renée Müller <r@example.com>",
		Date:       when,
		Subject:    "hallo",
		TrustLevel: channel.Unverified,
	}}
	got := formatMessages(msgs, 1)
	assert.Contains(t, got, "Renée Müller")
	assert.Contains(t, got, "r@example.com")
	assertAllRowsExactWidth(t, got, 80)
}

func TestFormatMessages_FourDigitID_PrefixGrowsSubjectShrinks(t *testing.T) {
	when := time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC)
	msgs := []channel.MessageSummary{{
		ID:         "1234",
		From:       "Alice Chen <alice@example.com>",
		Date:       when,
		Subject:    "hi",
		TrustLevel: channel.Unverified,
	}}
	got := formatMessages(msgs, 1)
	lines := strings.Split(got, "\n")
	header := lines[1]
	data := lines[2]
	// With idSlot = 4, header prefix is "▶   " (▶ + 3 spaces) and the
	// data row begins with the right-aligned ID "1234".
	assert.True(t, strings.HasPrefix(header, "\u25b6   "),
		"header must start with ▶+3 spaces at idSlot=4, got %q", header)
	assert.True(t, strings.HasPrefix(data, "1234"),
		"data row must begin with the 4-char ID, got %q", data)
	// Full-row width is still 80.
	assertAllRowsExactWidth(t, got, 80)
}

func TestFormatMessages_SingleMessageShape(t *testing.T) {
	when := time.Date(2026, 4, 6, 12, 0, 0, 0, time.UTC)
	msgs := []channel.MessageSummary{{
		ID:         "8",
		From:       "Alice Chen <alice@example.com>",
		Date:       when,
		Subject:    "lunch thursday?",
		TrustLevel: channel.Trusted,
		Unread:     false,
	}}
	got := formatMessages(msgs, 1)
	lines := strings.Split(got, "\n")
	data := lines[2]
	// Right-aligned "  8" in a 3-wide slot.
	assert.True(t, strings.HasPrefix(data, "  8"),
		"data row must start with right-aligned '  8', got %q", data)
	// DATE slot renders "Apr 06".
	assert.Contains(t, data, "Apr 06")
	// Trust glyph for Trusted is ✓.
	assert.Contains(t, data, "✓")
	// Read marker (R column) is a single space — the row contains
	// "  8  " (prefix + sep) followed by " " (read) then sep.
	// Asserted indirectly by the row-width and by absence of ● on a
	// read message.
	assert.NotContains(t, data, "●")
	assertAllRowsExactWidth(t, got, 80)
}

func TestFormatMessages_FullInboxOfGithubRelays(t *testing.T) {
	when := time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)
	older := time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC)
	oldest := time.Date(2026, 4, 6, 12, 0, 0, 0, time.UTC)
	msgs := []channel.MessageSummary{
		{ID: "319", From: "Copilot <notifications@github.com>", Date: when,
			Subject: "Re: [punt-labs/beadle] 1", TrustLevel: channel.Unverified, Unread: true},
		{ID: "320", From: "Pat Singh <notifications@github.com>", Date: when,
			Subject: "Re: [punt-labs/beadle] 2", TrustLevel: channel.Unverified, Unread: true},
		{ID: "322", From: "cursor[bot] <notifications@github.com>", Date: when,
			Subject: "Re: [punt-labs/beadle] 3", TrustLevel: channel.Unverified, Unread: true},
		{ID: "335", From: "vercel[bot] <notifications@github.com>", Date: when,
			Subject: "Re: [punt-labs/public] 4", TrustLevel: channel.Unverified, Unread: true},
		{ID: "340", From: "Sam Jackson <sam@example.co.uk>", Date: when,
			Subject: "Re: [punt-labs/punt-kit] 5", TrustLevel: channel.Trusted, Unread: true},
		{ID: "8", From: "Claude Agento <claude@punt-labs.com>", Date: older,
			Subject: "doctor fix landed", TrustLevel: channel.Trusted},
		{ID: "7", From: "Alice Chen <alice@example.com>", Date: oldest,
			Subject: "lunch thursday?", TrustLevel: channel.Unverified},
	}
	got := formatMessages(msgs, 7)
	assertAllRowsExactWidth(t, got, 80)
	assert.Contains(t, got, "Copilot <notifications@github.com>")
	assert.Contains(t, got, "cursor[bo…")
	assert.Contains(t, got, "vercel[bo…")
	assert.Contains(t, got, "Claude Agento <claude@punt-labs.com>")
	assert.NotContains(t, got, "(via ",
		"z34 relay annotation is removed in DES-018")
}

func TestFormatMessages_RedactedSubjectStillSurfacesEmail(t *testing.T) {
	when := time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC)
	msgs := []channel.MessageSummary{{
		ID:         "20",
		From:       "Pat Singh <notifications@github.com>",
		Date:       when,
		Subject:    "[redacted — no read permission]",
		TrustLevel: channel.Unverified,
		Unread:     true,
	}}
	got := formatMessages(msgs, 1)
	assert.Contains(t, got, "notifications@github.com")
	assertAllRowsExactWidth(t, got, 80)
}

func TestFormatMessages_DateNoTimeNoYear(t *testing.T) {
	when := time.Date(2026, 4, 8, 17, 19, 0, 0, time.UTC)
	msgs := []channel.MessageSummary{{
		ID:         "1",
		From:       "Alice Chen <alice@example.com>",
		Date:       when,
		Subject:    "hi",
		TrustLevel: channel.Unverified,
	}}
	got := formatMessages(msgs, 1)
	assert.Contains(t, got, "Apr 08")
	assert.NotContains(t, got, "17:19", "time-of-day must not appear")
	assert.NotContains(t, got, "2026", "year must not appear")
}

func TestFormatMessages_NoDateHeader_BlankCell(t *testing.T) {
	var zero time.Time
	msgs := []channel.MessageSummary{{
		ID:         "1",
		From:       "Alice Chen <alice@example.com>",
		Date:       zero,
		Subject:    "hi",
		TrustLevel: channel.Unverified,
	}}
	got := formatMessages(msgs, 1)
	assert.NotContains(t, got, "Jan 01",
		"zero time.Time must not render as the default Jan 01")
	assertAllRowsExactWidth(t, got, 80)
}

func TestFormatMessages_SubjectWithNewline_Stripped(t *testing.T) {
	when := time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC)
	msgs := []channel.MessageSummary{{
		ID:         "1",
		From:       "Alice Chen <alice@example.com>",
		Date:       when,
		Subject:    "Re: hello\nworld",
		TrustLevel: channel.Unverified,
	}}
	got := formatMessages(msgs, 1)
	assert.Contains(t, got, "hello world",
		"newline in subject must be replaced with a space")
	assertAllRowsExactWidth(t, got, 80)
}

func TestFormatFromCell_BareEmail(t *testing.T) {
	got := formatFromCell("", "ops@vendor.example", 37)
	assert.Equal(t, "ops@vendor.example", got)
}

func TestFormatFromCell_NameAndEmailFits(t *testing.T) {
	got := formatFromCell("Alice Chen", "alice@example.com", 37)
	assert.Equal(t, "Alice Chen <alice@example.com>", got)
}

func TestFormatFromCell_NameTruncatesEmailIntact(t *testing.T) {
	// For notifications@github.com (24 chars), the wrapped suffix
	// " <notifications@github.com>" consumes 27 chars, leaving 10 for
	// the name. "cursor[bot]" (11 runes) → "cursor[bo…" (10 runes).
	got := formatFromCell("cursor[bot]", "notifications@github.com", 37)
	assert.Equal(t, "cursor[bo… <notifications@github.com>", got)
	assert.Equal(t, 37, utf8.RuneCountInString(got),
		"cell should render exactly at maxWidth")
}

func TestFormatFromCell_LongEmailOverflows(t *testing.T) {
	// Documented edge case: when the bare email is already > maxWidth,
	// the function returns the email alone, accepting row overflow.
	// DES-018 § "FROM column rules" rule 3 — email is never truncated.
	addr := "very-long-local-part@subdomain.example.org"
	got := formatFromCell("X", addr, 37)
	assert.Equal(t, addr, got)
	assert.Greater(t, utf8.RuneCountInString(got), 37,
		"long email deliberately overflows the cell")
}

func TestFormatMessages_EmptyFrom(t *testing.T) {
	when := time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC)
	msgs := []channel.MessageSummary{{
		ID:         "99",
		From:       "",
		Date:       when,
		Subject:    "hello",
		TrustLevel: channel.Unverified,
	}}
	got := formatMessages(msgs, 1)
	assert.Contains(t, got, "99", "row should render without panic")
	assert.Contains(t, got, "hello", "subject should render")
	assertAllRowsExactWidth(t, got, 80)
}

func TestFormatDateCell_Zero_BlankCell(t *testing.T) {
	got := formatDateCell(time.Time{})
	assert.Equal(t, "      ", got, "zero time must render as 6 spaces")
	assert.Equal(t, dateColWidth, utf8.RuneCountInString(got))
}

func TestSanitizeSubject_StripsNewlinesAndTabs(t *testing.T) {
	got := sanitizeSubject("a\nb\rc\td")
	assert.Equal(t, "a b c d", got)
}
