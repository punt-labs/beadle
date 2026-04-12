// Package channel defines the shared contract for Beadle communication channels.
//
// Email is the first implementation. Future channels (Signal, etc.) implement
// the same interface so the MCP layer and trust model stay the same.
package channel

import "time"

// TrustLevel classifies how much a message should be trusted.
type TrustLevel string

const (
	Trusted    TrustLevel = "trusted"    // Proton-to-Proton, E2E verified by Proton
	Verified   TrustLevel = "verified"   // External, PGP signature valid
	Untrusted  TrustLevel = "untrusted"  // External, PGP signature invalid
	Unverified TrustLevel = "unverified" // External, no signature present
)

// Attachment is a file attached to a message.
type Attachment struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Size        int    `json:"size"`
}

// Message is a channel-agnostic message with trust metadata.
type Message struct {
	ID          string            `json:"id"`
	From        string            `json:"from"`
	To          string            `json:"to"`
	Date        time.Time         `json:"date"`
	Subject     string            `json:"subject"`
	Body        string            `json:"body"`
	TrustLevel  TrustLevel        `json:"trust_level"`
	Channel     string            `json:"channel"`
	Encryption  string            `json:"encryption"`
	Attachments []Attachment      `json:"attachments,omitempty"`
	RawHeaders  map[string]string `json:"raw_headers,omitempty"`
}

// Folder is a mailbox/channel folder.
type Folder struct {
	Name        string `json:"name"`
	NumMessages int    `json:"num_messages,omitempty"`
}

// MessageSummary is a lightweight listing entry.
type MessageSummary struct {
	ID         string     `json:"id"`
	From       string     `json:"from"`
	Date       time.Time  `json:"date"`
	Subject    string     `json:"subject"`
	TrustLevel TrustLevel `json:"trust_level"`
	HasSig     bool       `json:"has_sig,omitempty"`
	Unread     bool       `json:"unread"`
}
