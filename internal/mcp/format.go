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

// formatMessages formats a list of message summaries as a table.
// total is the total number of messages matching the query criteria.
func formatMessages(msgs []channel.MessageSummary, total int) string {
	if len(msgs) == 0 {
		return "No messages."
	}
	cols := []column{
		{header: "R", minWidth: 1},  // read status: ● unread, space read
		{header: "ID", minWidth: 2},
		{header: "FROM", minWidth: 10},
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
		rows[i] = []string{
			marker,
			m.ID,
			email.ExtractDisplayName(m.From),
			m.Date.Format("Mar 02 15:04"),
			trustIcon(m.TrustLevel),
			m.Subject,
		}
	}
	table := formatTable(cols, rows)
	return fmt.Sprintf("showing %d of %d\n%s", len(msgs), total, table)
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
