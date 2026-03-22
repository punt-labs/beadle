//go:build integration

package email_test

import (
	"io"
	"log/slog"
	"net"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/punt-labs/beadle/internal/email"
	"github.com/punt-labs/beadle/internal/testserver"
)

func dialFixture(t *testing.T, f *testserver.Fixture) *email.Client {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client, err := email.Dial(f.Config, logger)
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })
	return client
}

func TestDial_Connect(t *testing.T) {
	f := testserver.NewFixture(t)
	client := dialFixture(t, f)
	assert.NotNil(t, client)
}

func TestListFolders(t *testing.T) {
	f := testserver.NewFixture(t)

	// INBOX is seeded by default. Add Archive.
	f.AddMessage("Archive", "sender@test.com", "Archived", "old message")

	client := dialFixture(t, f)
	folders, err := client.ListFolders()
	require.NoError(t, err)

	names := make([]string, len(folders))
	for i, folder := range folders {
		names[i] = folder.Name
	}
	assert.Contains(t, names, "INBOX")
	assert.Contains(t, names, "Archive")
}

func TestListMessages_Basic(t *testing.T) {
	f := testserver.NewFixture(t)
	f.AddMessage("INBOX", "alice@test.com", "Hello", "body 1")
	f.AddMessage("INBOX", "bob@test.com", "World", "body 2")
	f.AddMessage("INBOX", "carol@test.com", "Test", "body 3")

	client := dialFixture(t, f)
	lr, err := client.ListMessages("INBOX", 10, false)
	require.NoError(t, err)

	assert.Equal(t, 3, lr.Total)
	assert.Len(t, lr.Messages, 3)
}

func TestListMessages_UnreadOnly(t *testing.T) {
	f := testserver.NewFixture(t)

	// 2 read (Seen flag) + 1 unread.
	f.AddMessageWithFlags("INBOX", "alice@test.com", "Read 1", "body", []imap.Flag{imap.FlagSeen})
	f.AddMessageWithFlags("INBOX", "bob@test.com", "Read 2", "body", []imap.Flag{imap.FlagSeen})
	f.AddMessage("INBOX", "carol@test.com", "Unread", "body")

	client := dialFixture(t, f)
	lr, err := client.ListMessages("INBOX", 10, true)
	require.NoError(t, err)

	assert.Equal(t, 1, lr.Total)
	assert.Len(t, lr.Messages, 1)
}

func TestFetchMessage(t *testing.T) {
	f := testserver.NewFixture(t)
	uid := f.AddMessage("INBOX", "alice@test.com", "Hello Alice", "Hello from the test")

	client := dialFixture(t, f)
	msg, err := client.FetchMessage("INBOX", uid)
	require.NoError(t, err)

	assert.Contains(t, msg.Subject, "Hello Alice")
	assert.Contains(t, msg.Body, "Hello from the test")
}

func TestMoveMessage(t *testing.T) {
	f := testserver.NewFixture(t)
	uid := f.AddMessage("INBOX", "alice@test.com", "To Move", "moving this")

	// Create Archive folder.
	f.AddMessage("Archive", "system@test.com", "Old", "placeholder")

	client := dialFixture(t, f)

	err := client.MoveMessage("INBOX", uid, "Archive")
	require.NoError(t, err)

	// Verify INBOX is now empty (need a fresh client since Select changes state).
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client2, err := email.Dial(f.Config, logger)
	require.NoError(t, err)
	defer client2.Close()

	lr, err := client2.ListMessages("INBOX", 10, false)
	require.NoError(t, err)
	assert.Equal(t, 0, lr.Total)
}

func TestSMTPSend(t *testing.T) {
	f := testserver.NewFixture(t)

	raw := []byte("From: test@test.com\r\nTo: recipient@test.com\r\nSubject: Test\r\n\r\nHello")
	err := email.SMTPSend(f.Config, "test@test.com", []string{"recipient@test.com"}, raw)
	require.NoError(t, err)

	sent := f.SentMessages()
	require.Len(t, sent, 1)
	assert.Equal(t, "test@test.com", sent[0].From)
	assert.Contains(t, sent[0].To, "recipient@test.com")
}

func TestSMTPAvailable_WhenUp(t *testing.T) {
	f := testserver.NewFixture(t)
	assert.True(t, email.SMTPAvailable(f.Config))
}

func TestSMTPAvailable_WhenDown(t *testing.T) {
	// Allocate an ephemeral port, then close the listener to guarantee nothing is on it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().(*net.TCPAddr)
	ln.Close()

	cfg := &email.Config{
		IMAPHost: "127.0.0.1",
		SMTPPort: addr.Port,
	}
	assert.False(t, email.SMTPAvailable(cfg))
}
