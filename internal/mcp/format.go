package mcp

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/punt-labs/beadle/internal/channel"
	"github.com/punt-labs/beadle/internal/contacts"
	"github.com/punt-labs/beadle/internal/email"

	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// textResult returns pre-formatted plain text as the MCP tool result.
// This is what all MCP clients see — human-readable by default.
func textResult(s string) (*mcplib.CallToolResult, error) {
	return mcplib.NewToolResultText(s), nil
}

// Column widths for list_messages output. See DESIGN.md § DES-018 for
// the slot table and the 80-character row budget.
const (
	fromColWidth = 37
	dateColWidth = 6
	idMinSlot    = 3
)

// formatMessages formats a list of message summaries as a table.
// total is the total number of messages matching the query criteria.
//
// Layout is pinned by DES-018: ID lives in a right-aligned row prefix
// slot (no header), FROM carries "Name <email>" in a 37-char cell, DATE
// is 6 chars, trust and read markers are 1 char each, SUBJECT absorbs
// the remaining width. Email is never truncated; the display name
// gives way first.
func formatMessages(msgs []channel.MessageSummary, total int) string {
	if len(msgs) == 0 {
		return "No messages."
	}

	// ID slot grows with the widest ID in the batch, never below 3.
	idSlot := idMinSlot
	for _, m := range msgs {
		if n := utf8.RuneCountInString(m.ID); n > idSlot {
			idSlot = n
		}
	}

	cols := []column{
		{header: "R", minWidth: 1},
		{header: "FROM", minWidth: fromColWidth},
		{header: "DATE", minWidth: dateColWidth},
		{header: "T", minWidth: 1},
		{header: "SUBJECT", minWidth: 10, variable: true},
	}

	rows := make([][]string, len(msgs))
	prefixes := make([]string, len(msgs))
	for i, m := range msgs {
		marker := " "
		if m.Unread {
			marker = "●"
		}
		name, addr := splitSender(m.From)
		rows[i] = []string{
			marker,
			formatFromCell(name, addr, fromColWidth),
			formatDateCell(m.Date),
			trustIcon(m.TrustLevel),
			sanitizeSubject(m.Subject),
		}
		prefixes[i] = leftPadRunes(m.ID, idSlot)
	}

	table := formatTableWithPrefixes(cols, rows, prefixes, idSlot)
	return fmt.Sprintf("showing %d of %d messages\n%s", len(msgs), total, table)
}

// splitSender returns the display name and email address for a raw
// From header value. The display name is empty when the header carries
// only a bare address, so the FROM column does not duplicate the email.
func splitSender(from string) (name, addr string) {
	addr = email.ExtractEmailAddress(from)
	name = strings.TrimSpace(email.ExtractDisplayName(from))
	// ExtractDisplayName falls back to the address when no distinct
	// name is present; collapse that to empty so downstream rendering
	// can distinguish a bare-email sender.
	if name == addr {
		name = ""
	}
	return name, addr
}

// formatFromCell renders the FROM cell for a single message. The cell
// holds either a bare email (no display name) or "Name <email>", with
// the display name truncated so the whole cell fits in maxWidth runes.
// Email is never truncated: if the wrapped " <addr>" form would not
// fit, the function returns the email alone. The cell only overflows
// maxWidth when the address itself is longer than maxWidth.
// See DES-018 § "FROM column rules".
//
// All length math is rune-count; multibyte glyphs count as 1. This is
// correct for Latin and Cyrillic but undercounts CJK display width (a
// CJK rune is 1 rune but 2 terminal columns). Out of scope for DES-018.
func formatFromCell(name, addr string, maxWidth int) string {
	if addr == "" && name == "" {
		return ""
	}
	if addr == "" {
		return truncateRunes(name, maxWidth)
	}
	if name == "" {
		return addr
	}
	wrappedLen := utf8.RuneCountInString(addr) + 3 // " <" + addr + ">"
	nameCap := maxWidth - wrappedLen
	if nameCap <= 0 {
		// Pathological long email: accept row overflow rather than
		// truncate the address (DES-018 rule 3).
		return addr
	}
	return truncateRunes(name, nameCap) + " <" + addr + ">"
}

// formatDateCell renders the DATE cell for a single message. Zero
// times render as 6 spaces so missing Date headers do not fall through
// to Go's default "Jan 01" rendering of the zero value.
func formatDateCell(t time.Time) string {
	if t.IsZero() {
		return strings.Repeat(" ", dateColWidth)
	}
	return t.Format("Jan 02")
}

// sanitizeSubject replaces newline, carriage return, and tab with a
// single space so a malformed sender cannot break the row-width
// invariant by injecting control characters into the subject.
func sanitizeSubject(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		return r
	}, s)
}

// leftPadRunes right-aligns s inside a field of width w by prepending
// spaces. Returns s unchanged when already at or above w runes.
func leftPadRunes(s string, w int) string {
	n := utf8.RuneCountInString(s)
	if n >= w {
		return s
	}
	return strings.Repeat(" ", w-n) + s
}

// formatMessage formats a full message for display.
func formatMessage(msg *channel.Message) string {
	pairs := [][2]string{
		{"From", msg.From},
		{"To", msg.To},
		{"Date", msg.Date.Format("Mon, 02 Jan 2006 15:04:05 -0700")},
		{"Subject", msg.Subject},
		{"Trust", string(msg.TrustLevel)},
	}
	if msg.Encryption != "" {
		pairs = append(pairs, [2]string{"Crypto", msg.Encryption})
	}
	if len(msg.Attachments) > 0 {
		pairs = append(pairs, [2]string{"Attach", fmt.Sprintf("%d file(s)", len(msg.Attachments))})
	}
	s := fmtKV(pairs)
	if len(msg.Attachments) > 0 {
		for i, a := range msg.Attachments {
			s += fmt.Sprintf("\n   [%d] %s (%s, %d bytes)", i, a.Filename, a.ContentType, a.Size)
		}
	}
	s += "\n\n" + msg.Body
	return s
}

// formatFolders formats a list of folders as a table.
func formatFolders(folders []channel.Folder) string {
	if len(folders) == 0 {
		return "No folders."
	}
	cols := []column{
		{header: "FOLDER", minWidth: 10, variable: true},
	}
	rows := make([][]string, len(folders))
	for i, f := range folders {
		rows[i] = []string{f.Name}
	}
	return formatTable(cols, rows)
}

// formatSendResult formats a send result.
func formatSendResult(r *email.SendResult) string {
	s := fmt.Sprintf("sent to %s via %s", r.To, r.Method)
	if r.Cc != "" {
		s += fmt.Sprintf(" cc:%s", r.Cc)
	}
	if r.Encrypted {
		s += " [encrypted+signed]"
	} else if r.Signed {
		s += " [signed]"
	}
	return s
}

// formatVerifyResult formats a signature verification result.
func formatVerifyResult(r *verifyResult) string {
	if r.Valid {
		s := "verified"
		if r.KeyID != "" {
			s += fmt.Sprintf(" · key %s", r.KeyID)
		}
		if r.Signer != "" {
			s += fmt.Sprintf(" · %s", r.Signer)
		}
		return s
	}
	return fmt.Sprintf("invalid signature · %s", r.GPGOutput)
}

// formatMIME formats MIME structure as a table.
func formatMIME(parts []email.MIMEPart) string {
	if len(parts) == 0 {
		return "No MIME parts."
	}
	cols := []column{
		{header: "#", minWidth: 1},
		{header: "TYPE", minWidth: 10, variable: true},
		{header: "FILE", minWidth: 4},
		{header: "SIZE", minWidth: 4},
	}
	rows := make([][]string, len(parts))
	for i, p := range parts {
		filename := ""
		if p.Filename != "" {
			filename = p.Filename
		}
		size := ""
		if p.Size > 0 {
			size = fmt.Sprintf("%d B", p.Size)
		}
		rows[i] = []string{
			fmt.Sprintf("%d", p.Index),
			p.ContentType,
			filename,
			size,
		}
	}
	return formatTable(cols, rows)
}

// formatTrustResult formats a trust classification.
func formatTrustResult(r email.TrustResult) string {
	s := string(r.Level)
	if r.Encryption != "" {
		s += " · " + r.Encryption
	}
	if r.Reason != "" {
		s += "\n" + r.Reason
	}
	return s
}

// formatMoveResult formats a move operation result.
func formatMoveResult(r *moveResult) string {
	return fmt.Sprintf("moved #%s → %s", r.MessageID, r.Destination)
}

// formatBatchMoveResult formats a batch move summary.
func formatBatchMoveResult(count int, destination string) string {
	return fmt.Sprintf("moved %d messages to %s", count, destination)
}

// formatDownloadResult formats a download result.
func formatDownloadResult(r *downloadResult) string {
	return fmt.Sprintf("%s: %s (%d bytes)\n%s", r.Status, r.Filename, r.Size, r.Path)
}

// formatContacts formats a list of contacts as a table.
func formatContacts(cs []contactResult) string {
	if len(cs) == 0 {
		return "No contacts."
	}
	cols := []column{
		{header: "NAME", minWidth: 6},
		{header: "EMAIL", minWidth: 10, variable: true},
		{header: "PERM", minWidth: 3},
		{header: "ALIASES", minWidth: 4},
	}
	rows := make([][]string, len(cs))
	for i, c := range cs {
		aliases := ""
		if len(c.Aliases) > 0 {
			aliases = strings.Join(c.Aliases, ", ")
		}
		rows[i] = []string{
			c.Name,
			c.Email,
			c.Permissions,
			aliases,
		}
	}
	return formatTable(cols, rows)
}

// formatContactAdded formats an add result.
func formatContactAdded(c contactResult) string {
	return fmt.Sprintf("added %s <%s>", c.Name, c.Email)
}

// formatContactRemoved formats a remove result.
func formatContactRemoved(r removeContactResult) string {
	return fmt.Sprintf("removed %s", r.Name)
}

// contactsToResultsWithPerms converts contacts with effective permissions.
func contactsToResultsWithPerms(cs []contacts.Contact, identityEmail string) []contactResult {
	results := make([]contactResult, 0, len(cs))
	for _, c := range cs {
		results = append(results, contactToResultWithPerms(c, identityEmail))
	}
	return results
}

// formatTrustResultWithPerm formats a trust result with identity permission.
func formatTrustResultWithPerm(r email.TrustResult, perm string) string {
	s := formatTrustResult(r)
	s += " · perm:" + perm
	return s
}

// formatPollStatus formats the poller status for display.
func formatPollStatus(st email.PollStatus) string {
	active := "no"
	if st.Active {
		active = "yes"
	}
	interval := st.Interval
	if interval == "" || interval == "n" {
		interval = "disabled"
	}
	pairs := [][2]string{
		{"Interval", interval},
		{"Active", active},
		{"Unseen", fmt.Sprintf("%d", st.Unseen)},
	}
	if !st.LastCheck.IsZero() {
		pairs = append(pairs, [2]string{"Last check", st.LastCheck.Format("15:04:05")})
	}
	if st.ConsecFails > 0 {
		pairs = append(pairs, [2]string{"Failures", fmt.Sprintf("%d", st.ConsecFails)})
	}
	if st.LastError != "" {
		pairs = append(pairs, [2]string{"Last error", st.LastError})
	}
	return fmtKV(pairs)
}

// trustIcon returns a single-character trust indicator.
func trustIcon(level channel.TrustLevel) string {
	switch level {
	case channel.Trusted:
		return "✓"
	case channel.Verified:
		return "+"
	case channel.Untrusted:
		return "✗"
	default:
		return "?"
	}
}
