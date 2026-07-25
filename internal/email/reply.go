package email

import (
	"bytes"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"time"
)

// ReplyContext carries what a reply needs from the message it answers: the
// threading linkage, the address to reply to, the subject to build a Re: line
// from, and the original sender, date, and body for the quoted attribution.
type ReplyContext struct {
	MessageID  string    // the original's Message-ID → the reply's In-Reply-To
	References []string  // the reply's References chain: original References + Message-ID
	ReplyTo    string    // the reply recipient: Reply-To if set, else From (bare address)
	From       string    // the original From header, for the quote attribution line
	Subject    string    // the original subject, for the Re: line
	Date       time.Time // the original Date, for the quote attribution line
	Body       string    // the original plain-text body, for quoting (empty when not Quotable)
	Quotable   bool      // whether Body is a real body, not a ParseMIME failure placeholder
}

// FetchThread reads the message at uid in folder and returns the context needed
// to compose a threaded reply. It fetches with PEEK, so reading a message to
// reply to never marks it seen. References is built per RFC 5322 §3.6.4, and
// the reply recipient is Reply-To when present, else From.
func (c *Client) FetchThread(folder string, uid uint32) (*ReplyContext, error) {
	raw, err := c.FetchRaw(folder, uid)
	if err != nil {
		return nil, err
	}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("parse reply source uid %d: %w", uid, err)
	}
	h := msg.Header
	messageID := strings.TrimSpace(h.Get("Message-ID"))
	replyTo := ExtractEmailAddress(h.Get("Reply-To"))
	if replyTo == "" {
		replyTo = ExtractEmailAddress(h.Get("From"))
	}
	date, _ := h.Date() // zero when the Date header is absent or unparseable

	// ParseMIME returns a placeholder, never an error, when it cannot extract a
	// body. A reply must not quote that placeholder into outbound mail, so treat
	// it as no body: drop it and mark the context not quotable.
	body, _, _ := ParseMIME(raw)
	quotable := !extractionFailed(body)
	if !quotable {
		body = ""
	}

	return &ReplyContext{
		MessageID:  messageID,
		References: buildReferences(h.Get("References"), h.Get("In-Reply-To"), messageID),
		ReplyTo:    replyTo,
		From:       strings.TrimSpace(h.Get("From")),
		Subject:    h.Get("Subject"),
		Date:       date,
		Body:       body,
		Quotable:   quotable,
	}, nil
}

// buildReferences constructs a reply's References chain per RFC 5322 §3.6.4:
// the parent's References (or its In-Reply-To when it carries no References),
// followed by the parent's Message-ID. When the parent has no Message-ID the
// result is the parent's chain unchanged, possibly empty. Message-IDs carry no
// internal whitespace, so splitting on whitespace recovers the tokens.
func buildReferences(references, inReplyTo, messageID string) []string {
	var chain []string
	switch {
	case strings.TrimSpace(references) != "":
		chain = strings.Fields(references)
	case strings.TrimSpace(inReplyTo) != "":
		chain = strings.Fields(inReplyTo)
	}
	if id := strings.TrimSpace(messageID); id != "" {
		chain = append(chain, id)
	}
	return chain
}

// reMarker matches a leading "Re:" reply marker, case-insensitively. Unlike
// replyPrefix it ignores Fwd:, so a reply to a forwarded message becomes
// "Re: Fwd: ..." while a reply to an existing "Re:" gains no second one.
var reMarker = regexp.MustCompile(`(?i)^\s*re\s*:`)

// ReplySubject builds the subject line for a reply to original, preserving the
// repo tag idempotently. It prepends "Re: " only when original does not already
// begin with a Re: marker, then runs the result through tag.subject so the
// "[owner/repo]" tag lands after the marker and is never doubled. Running the
// output back through ReplySubject with the same tag is a no-op.
func ReplySubject(original string, tag RepoTag) string {
	s := original
	if !reMarker.MatchString(s) {
		s = "Re: " + s
	}
	return tag.subject(s)
}

// QuoteBody assembles a reply body: the author's new text, a blank line, an
// attribution line naming the original sender, then the original body with
// each line prefixed "> " (RFC 3676 quoting). A blank original body yields just
// the new text and the attribution line.
func QuoteBody(newText, originalFrom string, originalDate time.Time, originalBody string) string {
	var b strings.Builder
	b.WriteString(newText)
	b.WriteString("\n\n")
	b.WriteString(attributionLine(originalFrom, originalDate))
	b.WriteString("\n")

	body := strings.TrimRight(strings.ReplaceAll(originalBody, "\r\n", "\n"), "\n")
	if body == "" {
		return b.String()
	}
	for _, line := range strings.Split(body, "\n") {
		if line == "" {
			b.WriteString(">\n")
			continue
		}
		b.WriteString("> ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// attributionLine builds the "On <date>, <sender> wrote:" line that precedes a
// quoted original. It degrades gracefully: a missing date drops the date, a
// missing sender drops the sender, and when both are absent it names neither.
func attributionLine(from string, date time.Time) string {
	const layout = "Mon, 2 Jan 2006 15:04 MST"
	switch {
	case from != "" && !date.IsZero():
		return fmt.Sprintf("On %s, %s wrote:", date.Format(layout), from)
	case from != "":
		return fmt.Sprintf("%s wrote:", from)
	case !date.IsZero():
		return fmt.Sprintf("On %s, the original message read:", date.Format(layout))
	default:
		return "Quoting the original message:"
	}
}
