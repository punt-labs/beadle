package email

import (
	"bytes"
	"fmt"
	"strings"
)

// Threading carries the RFC 5322 reply-linkage headers (In-Reply-To and
// References). A zero Threading writes no headers, so composing a fresh message
// is unaffected — only a reply sets these.
type Threading struct {
	InReplyTo  string   // the parent's Message-ID, e.g. "<abc@host>"
	References []string // the conversation chain: parent References + parent Message-ID
}

// empty reports whether t carries no reply linkage.
func (t Threading) empty() bool {
	return t.InReplyTo == "" && len(t.References) == 0
}

// writeHeaders appends the In-Reply-To and References header lines to buf, or
// nothing when t is empty. Like the X-Beadle-* headers, these are top-level
// RFC 822 headers written outside any signed or encrypted body, so they never
// alter a PGP/MIME signature. A value containing CR or LF is rejected to
// prevent header injection.
func (t Threading) writeHeaders(buf *bytes.Buffer) error {
	if t.empty() {
		return nil
	}
	refs := strings.Join(t.References, " ")
	if strings.ContainsAny(t.InReplyTo, "\r\n") || strings.ContainsAny(refs, "\r\n") {
		return fmt.Errorf("threading header contains CR/LF")
	}
	if t.InReplyTo != "" {
		fmt.Fprintf(buf, "In-Reply-To: %s\r\n", t.InReplyTo)
	}
	if refs != "" {
		fmt.Fprintf(buf, "References: %s\r\n", refs)
	}
	return nil
}

// headers returns the In-Reply-To/References header map for the Resend JSON
// path, or nil when t is empty. Values with CR/LF are dropped by the caller's
// merge into a validated map; the raw-MIME path rejects them in writeHeaders.
func (t Threading) headers() map[string]string {
	if t.empty() {
		return nil
	}
	h := make(map[string]string, 2)
	if t.InReplyTo != "" {
		h["In-Reply-To"] = t.InReplyTo
	}
	if refs := strings.Join(t.References, " "); refs != "" {
		h["References"] = refs
	}
	return h
}
