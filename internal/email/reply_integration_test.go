//go:build integration

package email_test

import (
	"bytes"
	"io"
	"log/slog"
	"net/mail"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/punt-labs/beadle/internal/email"
	"github.com/punt-labs/beadle/internal/testserver"
)

// seedOriginal adds a message with the given threading headers and returns its
// UID. Reply-To is included only when replyTo is non-empty.
func seedOriginal(t *testing.T, f *testserver.Fixture, messageID, references, inReplyTo, replyTo string) uint32 {
	t.Helper()
	var b bytes.Buffer
	b.WriteString("From: Alice <alice@test.com>\r\n")
	b.WriteString("To: me@test.com\r\n")
	b.WriteString("Subject: [punt-labs/beadle] Question\r\n")
	b.WriteString("Message-ID: " + messageID + "\r\n")
	if references != "" {
		b.WriteString("References: " + references + "\r\n")
	}
	if inReplyTo != "" {
		b.WriteString("In-Reply-To: " + inReplyTo + "\r\n")
	}
	if replyTo != "" {
		b.WriteString("Reply-To: " + replyTo + "\r\n")
	}
	b.WriteString("Date: Sat, 25 Jul 2026 09:30:00 +0000\r\n")
	b.WriteString("Content-Type: text/plain\r\n\r\n")
	b.WriteString("What is the status?")
	return f.AddRawMessage("INBOX", b.Bytes())
}

func TestReply_FetchThread(t *testing.T) {
	tests := []struct {
		name       string
		messageID  string
		references string
		inReplyTo  string
		replyTo    string
		wantRefs   []string
		wantTo     string
	}{
		{
			name:       "existing references, reply-to set",
			messageID:  "<orig@test>",
			references: "<root@test>",
			replyTo:    "list@test.com",
			wantRefs:   []string{"<root@test>", "<orig@test>"},
			wantTo:     "list@test.com",
		},
		{
			name:      "no references falls back to in-reply-to; reply-to absent uses from",
			messageID: "<orig@test>",
			inReplyTo: "<root@test>",
			wantRefs:  []string{"<root@test>", "<orig@test>"},
			wantTo:    "alice@test.com",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := testserver.NewFixture(t)
			uid := seedOriginal(t, f, tt.messageID, tt.references, tt.inReplyTo, tt.replyTo)
			client := dialFixture(t, f)

			rc, err := client.FetchThread("INBOX", uid)
			require.NoError(t, err)

			assert.Equal(t, "<orig@test>", rc.MessageID)
			assert.Equal(t, tt.wantRefs, rc.References)
			assert.Equal(t, tt.wantTo, rc.ReplyTo)
			assert.Equal(t, "[punt-labs/beadle] Question", rc.Subject)
			assert.Contains(t, rc.Body, "What is the status?")
			assert.False(t, rc.Date.IsZero(), "Date should parse")
		})
	}
}

// TestReply_SendThreadedUnsigned drives the full reply send through the
// in-process SMTP server and asserts the threading headers land at the top
// level of the delivered message, alongside a Re: subject.
func TestReply_SendThreadedUnsigned(t *testing.T) {
	f := testserver.NewFixture(t)
	uid := seedOriginal(t, f, "<orig@test>", "<root@test>", "", "")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := dialFixture(t, f)

	rc, err := client.FetchThread("INBOX", uid)
	require.NoError(t, err)

	subject := email.ReplySubject(rc.Subject, email.RepoTag{})
	threading := email.Threading{InReplyTo: rc.MessageID, References: rc.References}
	body := email.QuoteBody("Status is green.", rc.From, rc.Date, rc.Body)

	_, err = email.TrySendChain(f.Config, logger,
		[]string{rc.ReplyTo}, nil, nil,
		subject, body, "",
		nil, nil, email.RepoTag{}, threading,
	)
	require.NoError(t, err)

	sent := f.SentMessages()
	require.Len(t, sent, 1)
	assert.Contains(t, sent[0].To, "alice@test.com")

	msg, err := mail.ReadMessage(bytes.NewReader(sent[0].Raw))
	require.NoError(t, err)
	assert.Equal(t, "<orig@test>", msg.Header.Get("In-Reply-To"))
	assert.Equal(t, "<root@test> <orig@test>", msg.Header.Get("References"))
	assert.Equal(t, "Re: [punt-labs/beadle] Question", msg.Header.Get("Subject"))

	got, err := io.ReadAll(msg.Body)
	require.NoError(t, err)
	assert.Contains(t, string(got), "Status is green.")
	assert.Contains(t, string(got), "> What is the status?")
}
