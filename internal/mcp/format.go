package mcp

import (
	"fmt"
	"strings"
	"time"

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
func formatMessages(msgs []channel.MessageSummary) string {
	if len(msgs) == 0 {
		return "No messages."
	}
	var b strings.Builder
	for _, m := range msgs {
		unread := " "
		if m.Unread {
			unread = "*"
		}
		fmt.Fprintf(&b, "%s %-4s %-20s %-12s %-8s %s\n",
			unread,
			m.ID,
			truncate(m.From, 20),
			m.Date.Format("Jan 02 15:04"),
			string(m.TrustLevel),
			truncate(m.Subject, 40),
		)
	}
	return b.String()
}

// formatMessage formats a full message for display.
func formatMessage(msg *channel.Message) string {
	var b strings.Builder
	fmt.Fprintf(&b, "From:    %s\n", msg.From)
	fmt.Fprintf(&b, "To:      %s\n", msg.To)
	fmt.Fprintf(&b, "Date:    %s\n", msg.Date.Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Subject: %s\n", msg.Subject)
	fmt.Fprintf(&b, "Trust:   %s\n", msg.TrustLevel)
	if msg.Encryption != "" {
		fmt.Fprintf(&b, "Crypto:  %s\n", msg.Encryption)
	}
	if len(msg.Attachments) > 0 {
		fmt.Fprintf(&b, "Attach:  %d file(s)\n", len(msg.Attachments))
		for i, a := range msg.Attachments {
			fmt.Fprintf(&b, "  [%d] %s (%s, %d bytes)\n", i, a.Filename, a.ContentType, a.Size)
		}
	}
	fmt.Fprintf(&b, "\n%s", msg.Body)
	return b.String()
}

// formatFolders formats a list of folders.
func formatFolders(folders []channel.Folder) string {
	if len(folders) == 0 {
		return "No folders."
	}
	var b strings.Builder
	for _, f := range folders {
		fmt.Fprintln(&b, f.Name)
	}
	return b.String()
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

// formatMIME formats MIME structure.
func formatMIME(parts []email.MIMEPart) string {
	if len(parts) == 0 {
		return "No MIME parts."
	}
	var b strings.Builder
	for _, p := range parts {
		fmt.Fprintf(&b, "[%d] %s", p.Index, p.ContentType)
		if p.Filename != "" {
			fmt.Fprintf(&b, " (%s)", p.Filename)
		}
		if p.Size > 0 {
			fmt.Fprintf(&b, " %d bytes", p.Size)
		}
		fmt.Fprintln(&b)
	}
	return b.String()
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

// formatContacts formats a list of contacts.
func formatContacts(cs []contactResult) string {
	if len(cs) == 0 {
		return "No contacts."
	}
	var b strings.Builder
	for _, c := range cs {
		fmt.Fprintf(&b, "%-20s %-30s", c.Name, c.Email)
		if len(c.Aliases) > 0 {
			fmt.Fprintf(&b, "  [%s]", strings.Join(c.Aliases, ", "))
		}
		fmt.Fprintln(&b)
	}
	return b.String()
}

// formatContactAdded formats an add result.
func formatContactAdded(c contactResult) string {
	return fmt.Sprintf("added %s <%s>", c.Name, c.Email)
}

// formatContactRemoved formats a remove result.
func formatContactRemoved(r removeContactResult) string {
	return fmt.Sprintf("removed %s", r.Name)
}

func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-1]) + "…"
}

// contactsToResults converts a slice of contacts.Contact to contactResult.
func contactsToResults(cs []contacts.Contact) []contactResult {
	results := make([]contactResult, 0, len(cs))
	for _, c := range cs {
		results = append(results, contactToResult(c))
	}
	return results
}
