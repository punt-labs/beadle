package mcp

import (
	"fmt"
	"strings"

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

// maxDisplayNameRunes caps the display-name portion of the FROM cell so
// a long name does not squeeze the SUBJECT column down to its minimum
// width. The EMAIL column is never truncated — it is the identifier
// beadle's permission system is keyed on (beadle-0he).
//
// maxRelayLabelRunes caps the domain label inside a "(via X)" relay
// annotation so an unusually long primary label cannot blow the FROM
// cell out by itself (beadle-z34).
const (
	maxDisplayNameRunes = 20
	maxRelayLabelRunes  = 12
)

// formatMessages formats a list of message summaries as a table.
// total is the total number of messages matching the query criteria.
//
// Every row surfaces the sender's display name and email address so
// the operator can identify unknown senders and decide whether to add
// them to the contact book. The EMAIL column is always populated, even
// when the sender has no read permission and the subject is redacted.
func formatMessages(msgs []channel.MessageSummary, total int) string {
	if len(msgs) == 0 {
		return "No messages."
	}
	cols := []column{
		{header: "R", minWidth: 1}, // read status: ● unread, space read
		{header: "ID", minWidth: 2},
		{header: "FROM", minWidth: 10},
		{header: "EMAIL", minWidth: 24},
		{header: "DATE", minWidth: 12},
		{header: "T", minWidth: 1}, // trust: ✓ trusted, + verified, ? unverified, ✗ untrusted
		{header: "SUBJECT", minWidth: 10, variable: true},
	}
	rows := make([][]string, len(msgs))
	for i, m := range msgs {
		marker := " "
		if m.Unread {
			marker = "●"
		}
		name, addr := splitSender(m.From)
		rows[i] = []string{
			marker,
			m.ID,
			formatFromCell(name, addr),
			addr,
			m.Date.Format("Jan 02 15:04"),
			trustIcon(m.TrustLevel),
			m.Subject,
		}
	}
	table := formatTable(cols, rows)
	return fmt.Sprintf("showing %d of %d messages\n%s", len(msgs), total, table)
}

// splitSender returns the display name and email address for a raw
// From header value. The display name is empty when the header carries
// only a bare address, so the FROM column does not repeat the email.
// When the header is unparseable the email may be empty and the name
// falls back to the trimmed raw value (via ExtractDisplayName).
func splitSender(from string) (name, addr string) {
	addr = email.ExtractEmailAddress(from)
	name = email.ExtractDisplayName(from)
	// ExtractDisplayName falls back to the address when no distinct
	// name is present; collapse that to empty so we do not duplicate
	// content across the FROM and EMAIL columns.
	if name == addr {
		name = ""
	}
	return name, addr
}

// formatFromCell returns the text rendered in the FROM column. For an
// ordinary sender whose display name corresponds to the email address
// identity, the cell is just the truncated name. When the display name
// does not correspond to the email identity — the notification-relay
// and display-name-spoof cases — the cell is annotated as
// "<name> (via <domain>)" so a skimming reader cannot misattribute the
// message to the named person (beadle-z34).
func formatFromCell(name, addr string) string {
	short := truncateRunes(name, maxDisplayNameRunes)
	if name == "" || addr == "" {
		return short
	}
	if !isRelay(name, addr) {
		return short
	}
	label := relayDomainLabel(addr)
	if label == "" {
		return short
	}
	return short + " (via " + label + ")"
}

// isRelay reports whether the display name identity fails to correspond
// to the email address. The check is deliberately conservative: a name
// is considered to correspond to the address when any alphabetic name
// token of length >= 2 shares a prefix with any local-part token or
// domain label. This keeps "Alice Chen <alice@example.com>",
// "jim <jim@example.com>", and "Jim Freeman <jim@punt-labs.com>" out of
// the relay bucket, while catching "J Freeman <notifications@github.com>"
// and display-name spoofs.
//
// A small set of automation local-parts ("noreply", "notifications",
// "alerts", ...) and the "[bot]" / "-bot" suffix are always treated as
// relays even when token overlap would otherwise match, because their
// purpose is to carry other parties' identities.
func isRelay(name, addr string) bool {
	if name == "" {
		return false
	}
	local, domain := splitAddress(addr)
	if local == "" || domain == "" {
		return false
	}
	if isAutomationLocal(local) || isBotName(name) {
		return true
	}
	nameTokens := tokenize(name)
	if len(nameTokens) == 0 {
		return false
	}
	addrTokens := append(tokenize(local), domainLabels(domain)...)
	for _, nt := range nameTokens {
		if len(nt) < 2 {
			continue
		}
		for _, at := range addrTokens {
			if len(at) < 2 {
				continue
			}
			if strings.HasPrefix(nt, at) || strings.HasPrefix(at, nt) {
				return false
			}
		}
	}
	return true
}

// relayDomainLabel returns the primary label of addr's domain, capped
// at maxRelayLabelRunes. For "notifications@github.com" this is
// "github"; for "bot@ci.vercel.app" this is "vercel". Returns "" when
// the domain cannot be parsed.
func relayDomainLabel(addr string) string {
	_, domain := splitAddress(addr)
	labels := domainLabels(domain)
	if len(labels) == 0 {
		return ""
	}
	// For 2-label domains ("github.com") use the first label.
	// For 3+-label domains ("ci.vercel.app") use the second-to-last
	// label — the registrable name, not the subdomain or TLD.
	var label string
	switch {
	case len(labels) >= 2:
		label = labels[len(labels)-2]
	default:
		label = labels[0]
	}
	return truncateRunes(label, maxRelayLabelRunes)
}

// splitAddress returns the local-part and domain of an email address.
// Both parts are lowercased. Empty strings are returned on malformed
// input.
func splitAddress(addr string) (local, domain string) {
	addr = strings.ToLower(strings.TrimSpace(addr))
	at := strings.LastIndex(addr, "@")
	if at <= 0 || at == len(addr)-1 {
		return "", ""
	}
	return addr[:at], addr[at+1:]
}

// domainLabels returns the lowercased dot-separated labels of a domain,
// with empty labels dropped.
func domainLabels(domain string) []string {
	parts := strings.Split(domain, ".")
	out := parts[:0]
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// tokenize splits s into lowercased alphabetic tokens. Digits and
// punctuation act as separators. Single-character tokens are kept —
// the caller decides whether to use them.
func tokenize(s string) []string {
	s = strings.ToLower(s)
	var out []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		if r >= 'a' && r <= 'z' {
			cur.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return out
}

// isAutomationLocal reports whether local is one of the well-known
// automation local-parts that carry other parties' identities. The set
// is narrow and explicit: every entry is a local-part string (not a
// prefix match) that we have observed on real notification traffic or
// that RFC 2142 / common practice reserves for non-human senders.
func isAutomationLocal(local string) bool {
	switch local {
	case "noreply", "no-reply", "donotreply", "do-not-reply",
		"notifications", "notification",
		"alerts", "alert",
		"updates", "update",
		"mailer-daemon", "postmaster":
		return true
	}
	// "*-bot" and "*[bot]" suffixes on the local-part itself.
	if strings.HasSuffix(local, "-bot") || strings.HasSuffix(local, "[bot]") {
		return true
	}
	return false
}

// isBotName reports whether the display name itself carries a "[bot]"
// marker or "bot" suffix. GitHub, Dependabot, Renovate, and similar
// automation consistently tag their display names this way.
func isBotName(name string) bool {
	n := strings.ToLower(name)
	if strings.Contains(n, "[bot]") {
		return true
	}
	if strings.HasSuffix(n, " bot") || strings.HasSuffix(n, "-bot") {
		return true
	}
	return false
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
