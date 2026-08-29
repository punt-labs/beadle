package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/punt-labs/beadle/internal/email"
	mcptools "github.com/punt-labs/beadle/internal/mcp"
	"github.com/punt-labs/beadle/internal/testenv"
	"github.com/punt-labs/beadle/internal/testserver"
)

const testEmail = "test@test.com"

// setupHandler creates a fully wired MCP server with test env and mail fixture.
func setupHandler(t *testing.T) (*server.MCPServer, *testenv.Env, *testserver.Fixture) {
	t.Helper()

	env := testenv.New(t, testEmail)
	fix := testserver.NewFixture(t)
	env.WriteConfig(fix.Config)

	s := server.NewMCPServer("beadle-email", "test", server.WithToolCapabilities(false))
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	dialer := testserver.TestDialer{Password: "testpass"}
	mcptools.RegisterTools(s, env.Resolver, logger, mcptools.WithDialer(dialer))

	// Initialize the MCP session.
	callMCP(t, s, "initialize", 0, map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "1.0"},
	})

	return s, env, fix
}

func callMCP(t *testing.T, s *server.MCPServer, method string, id int, params any) json.RawMessage {
	t.Helper()
	req := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		req["params"] = params
	}
	raw, err := json.Marshal(req)
	require.NoError(t, err)
	resp := s.HandleMessage(context.Background(), raw)
	out, err := json.Marshal(resp)
	require.NoError(t, err)
	return out
}

func callTool(t *testing.T, s *server.MCPServer, name string, args map[string]any) toolResult {
	t.Helper()
	resp := callMCP(t, s, "tools/call", 1, map[string]any{
		"name":      name,
		"arguments": args,
	})
	var result struct {
		Result toolResult `json:"result"`
	}
	require.NoError(t, json.Unmarshal(resp, &result))
	return result.Result
}

type toolResult struct {
	IsError bool `json:"isError"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

func (r toolResult) text() string {
	if len(r.Content) == 0 {
		return ""
	}
	return r.Content[0].Text
}

// setupHandlerWithPoller creates a fully wired MCP server including the
// background poller. The poller is stopped automatically at test cleanup.
func setupHandlerWithPoller(t *testing.T) (*server.MCPServer, *testenv.Env, *testserver.Fixture) {
	t.Helper()

	env := testenv.New(t, testEmail)
	fix := testserver.NewFixture(t)
	env.WriteConfig(fix.Config)

	s := server.NewMCPServer("beadle-email", "test", server.WithToolCapabilities(true))
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	dialer := testserver.TestDialer{Password: "testpass"}
	onNewMail := func(newCount uint32) {
		s.SendNotificationToAllClients(mcp.MethodNotificationToolsListChanged, nil)
		s.SendNotificationToAllClients("notifications/claude/channel", map[string]any{
			"content": fmt.Sprintf("%d new message(s) in inbox.", newCount),
		})
	}
	poller := email.NewPoller(onNewMail, env.Resolver, logger, dialer)
	mcptools.RegisterTools(s, env.Resolver, logger, mcptools.WithDialer(dialer), mcptools.WithPoller(poller))

	callMCP(t, s, "initialize", 0, map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "1.0"},
	})

	t.Cleanup(func() { poller.Stop() })
	return s, env, fix
}

// --- Handler Tests ---

func TestHandler_Whoami(t *testing.T) {
	s, _, _ := setupHandler(t)
	r := callTool(t, s, "whoami", nil)

	assert.False(t, r.IsError)
	assert.Contains(t, r.text(), testEmail)
	assert.Contains(t, r.text(), "ethos")
}

func TestHandler_ListFolders(t *testing.T) {
	s, _, fix := setupHandler(t)
	fix.AddMessage("Archive", "x@test.com", "Old", "old msg")

	r := callTool(t, s, "list_folders", nil)
	assert.False(t, r.IsError)
	assert.Contains(t, r.text(), "INBOX")
	assert.Contains(t, r.text(), "Archive")
}

// TestHandler_ListFolders_FailsClosedOnCorruptIdentityConfig proves
// resolveIdentityAndConfig's dedup onto email.LoadIdentityConfig preserved
// its existing fail-closed behavior: a corrupt identity-scoped config is a
// hard tool error, never a silent fallback to email.DefaultConfigPath().
func TestHandler_ListFolders_FailsClosedOnCorruptIdentityConfig(t *testing.T) {
	s, env, _ := setupHandler(t)

	idConfigPath := filepath.Join(env.IdentityDir(), "email.json")
	require.NoError(t, os.WriteFile(idConfigPath, []byte(`{not json`), 0o640))

	r := callTool(t, s, "list_folders", nil)
	assert.True(t, r.IsError, "a corrupt identity config must fail the tool call, not silently fall back")
	assert.Contains(t, r.text(), "load config")
}

func TestHandler_ListMessages(t *testing.T) {
	s, env, fix := setupHandler(t)

	// Add a contact with read permission so messages aren't redacted.
	env.AddContact("Alice", "alice@test.com", "r--")

	fix.AddMessage("INBOX", "alice@test.com", "Hello World", "body")
	fix.AddMessage("INBOX", "alice@test.com", "Second Message", "body 2")

	// Seeds are untagged; list every repo so they are not scoped out.
	r := callTool(t, s, "list_messages", map[string]any{"count": 10, "all_repos": true})
	assert.False(t, r.IsError)
	assert.Contains(t, r.text(), "Hello World")
	assert.Contains(t, r.text(), "Second Message")
}

func TestHandler_ListMessages_CountWrongType(t *testing.T) {
	s, _, _ := setupHandler(t)
	// Pass count as a string — intParam must return an error, not silently use fallback.
	r := callTool(t, s, "list_messages", map[string]any{"count": "500"})
	assert.True(t, r.IsError, "wrong-type count must produce an error result")
	assert.Contains(t, r.text(), "count", "error message should name the parameter")
}

func TestHandler_ListMessages_CountFractional(t *testing.T) {
	s, _, _ := setupHandler(t)
	// A fractional count must be rejected before dialing IMAP.
	r := callTool(t, s, "list_messages", map[string]any{"count": float64(10.5)})
	assert.True(t, r.IsError, "fractional count must produce an error result")
	assert.Contains(t, r.text(), "count", "error message should name the parameter")
}

func TestHandler_ReadMessage_Permitted(t *testing.T) {
	s, env, fix := setupHandler(t)
	env.AddContact("Alice", "alice@test.com", "r--")

	uid := fix.AddMessage("INBOX", "alice@test.com", "Readable", "secret content")

	r := callTool(t, s, "read_message", map[string]any{
		"message_id": fmt.Sprintf("%d", uid),
	})
	assert.False(t, r.IsError)
	assert.Contains(t, r.text(), "secret content")
}

func TestHandler_ReadMessage_MaxBodyLength(t *testing.T) {
	longBody := "abcdefghijklmnopqrstuvwxyz" // 26 chars

	tests := []struct {
		name        string
		maxBody     any // nil means omitted
		wantFull    bool
		wantTrunc   bool
		wantError   bool
		wantErrMsg  string // substring expected in error message
		wantOrigLen string // substring for truncation indicator
	}{
		{"omitted returns full body", nil, true, false, false, "", ""},
		{"zero returns full body", float64(0), true, false, false, "", ""},
		{"longer than body returns full body", float64(100), true, false, false, "", ""},
		{"equal to body length returns full body", float64(26), true, false, false, "", ""},
		{"shorter than body truncates", float64(10), false, true, false, "", "26 chars total"},
		{"negative returns error", float64(-1), false, false, true, "non-negative", ""},
		{"fractional returns error", float64(10.5), false, false, true, "whole number", ""},
		{"string type returns error", "big", false, false, true, "max_body_length", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, env, fix := setupHandler(t)
			env.AddContact("Alice", "alice@test.com", "r--")
			uid := fix.AddMessage("INBOX", "alice@test.com", "Long Email", longBody)

			args := map[string]any{"message_id": fmt.Sprintf("%d", uid)}
			if tt.maxBody != nil {
				args["max_body_length"] = tt.maxBody
			}

			r := callTool(t, s, "read_message", args)

			if tt.wantError {
				assert.True(t, r.IsError)
				assert.Contains(t, r.text(), tt.wantErrMsg)
				return
			}
			assert.False(t, r.IsError, "read failed: %s", r.text())

			if tt.wantFull {
				assert.Contains(t, r.text(), longBody)
				assert.NotContains(t, r.text(), "[truncated")
			}
			if tt.wantTrunc {
				assert.Contains(t, r.text(), "abcdefghij")
				assert.NotContains(t, r.text(), longBody)
				assert.Contains(t, r.text(), tt.wantOrigLen)
			}
		})
	}
}

func TestHandler_ReadMessage_MaxBodyLength_UTF8(t *testing.T) {
	// "café🎉" is 5 runes (c-a-f-é-🎉) but 9 bytes.
	// Truncating to 3 runes should yield "caf", not split a multi-byte char.
	body := "café🎉"

	s, env, fix := setupHandler(t)
	env.AddContact("Alice", "alice@test.com", "r--")
	uid := fix.AddMessage("INBOX", "alice@test.com", "UTF8 Email", body)

	r := callTool(t, s, "read_message", map[string]any{
		"message_id":      fmt.Sprintf("%d", uid),
		"max_body_length": float64(3),
	})

	require.False(t, r.IsError, "read failed: %s", r.text())
	assert.Contains(t, r.text(), "caf")
	assert.NotContains(t, r.text(), body)
	assert.Contains(t, r.text(), "5 chars total")
}

func TestHandler_ReadMessage_Denied(t *testing.T) {
	s, _, fix := setupHandler(t)
	// No contact added — unknown sender has no permissions.

	uid := fix.AddMessage("INBOX", "stranger@evil.com", "Malicious", "bad content")

	r := callTool(t, s, "read_message", map[string]any{
		"message_id": fmt.Sprintf("%d", uid),
	})
	assert.True(t, r.IsError)
	assert.Contains(t, r.text(), "permission")
}

func TestHandler_SendEmail_OK(t *testing.T) {
	s, env, fix := setupHandler(t)
	env.AddContact("Bob", "bob@test.com", "-w-")

	r := callTool(t, s, "send_email", map[string]any{
		"to":      "bob@test.com",
		"subject": "Test Send",
		"body":    "Hello Bob",
	})
	assert.False(t, r.IsError, "send failed: %s", r.text())

	sent := fix.SentMessages()
	require.Len(t, sent, 1)
	assert.Contains(t, sent[0].To, "bob@test.com")
}

func TestHandler_SendEmail_Denied(t *testing.T) {
	s, env, _ := setupHandler(t)
	// Contact exists but without write permission.
	env.AddContact("Bob", "bob@test.com", "r--")

	r := callTool(t, s, "send_email", map[string]any{
		"to":      "bob@test.com",
		"subject": "Test Send",
		"body":    "Hello Bob",
	})
	assert.True(t, r.IsError)
	assert.Contains(t, r.text(), "permission")
}

// replyOriginal seeds an original message with known threading headers and
// returns its UID. The Message-ID and References let a reply's threading be
// asserted exactly.
func replyOriginal(t *testing.T, fix *testserver.Fixture) uint32 {
	t.Helper()
	raw := "From: Alice <alice@test.com>\r\n" +
		"To: me@test.com\r\n" +
		"Subject: [punt-labs/beadle] Question\r\n" +
		"Message-ID: <orig@test>\r\n" +
		"References: <root@test>\r\n" +
		"Date: Sat, 25 Jul 2026 09:30:00 +0000\r\n" +
		"Content-Type: text/plain\r\n" +
		"\r\n" +
		"What is the status?"
	return fix.AddRawMessage("INBOX", []byte(raw))
}

func TestHandler_ReplyMessage_ThreadsAndQuotes(t *testing.T) {
	s, env, fix := setupHandler(t)
	env.AddContact("Alice", "alice@test.com", "-w-")
	uid := replyOriginal(t, fix)

	r := callTool(t, s, "reply_message", map[string]any{
		"message_id": fmt.Sprintf("%d", uid),
		"body":       "Status is green.",
	})
	require.False(t, r.IsError, "reply failed: %s", r.text())

	sent := fix.SentMessages()
	require.Len(t, sent, 1)
	assert.Contains(t, sent[0].To, "alice@test.com")

	raw := string(sent[0].Raw)
	// Threading headers are present and correct (References = original chain +
	// original Message-ID).
	assert.Contains(t, raw, "In-Reply-To: <orig@test>")
	assert.Contains(t, raw, "References: <root@test> <orig@test>")
	// Re: prepended, [owner/repo] tag preserved, not doubled.
	assert.Contains(t, raw, "Subject: Re: [punt-labs/beadle] Question")
	assert.NotContains(t, raw, "Re: Re:")
	// The new text and the quoted original both appear.
	assert.Contains(t, raw, "Status is green.")
	assert.Contains(t, raw, "> What is the status?")
}

func TestHandler_ReplyMessage_DeniedReadOnly(t *testing.T) {
	s, env, fix := setupHandler(t)
	// Read-only sender: readable, but not writable — reply must be refused.
	env.AddContact("Alice", "alice@test.com", "r--")
	uid := replyOriginal(t, fix)

	r := callTool(t, s, "reply_message", map[string]any{
		"message_id": fmt.Sprintf("%d", uid),
		"body":       "trying to reply",
	})
	assert.True(t, r.IsError)
	assert.Contains(t, r.text(), "permission")
	assert.Empty(t, fix.SentMessages(), "no reply may be sent to an r-- contact")
}

func TestHandler_ReplyMessage_DeniedUnknown(t *testing.T) {
	s, _, fix := setupHandler(t)
	// No contact for the sender at all.
	uid := replyOriginal(t, fix)

	r := callTool(t, s, "reply_message", map[string]any{
		"message_id": fmt.Sprintf("%d", uid),
		"body":       "trying to reply",
	})
	assert.True(t, r.IsError)
	assert.Contains(t, r.text(), "permission")
	assert.Empty(t, fix.SentMessages())
}

func TestHandler_ReplyMessage_MissingBody(t *testing.T) {
	s, env, fix := setupHandler(t)
	env.AddContact("Alice", "alice@test.com", "-w-")
	uid := replyOriginal(t, fix)

	r := callTool(t, s, "reply_message", map[string]any{
		"message_id": fmt.Sprintf("%d", uid),
	})
	assert.True(t, r.IsError)
	assert.Contains(t, r.text(), "body is required")
}

func TestHandler_ReplyMessage_NoMessageID_WarnsNoThreading(t *testing.T) {
	s, env, fix := setupHandler(t)
	env.AddContact("Alice", "alice@test.com", "-w-")
	// No Message-ID header: nothing to thread against.
	raw := "From: Alice <alice@test.com>\r\n" +
		"To: me@test.com\r\n" +
		"Subject: [punt-labs/beadle] Question\r\n" +
		"Date: Sat, 25 Jul 2026 09:30:00 +0000\r\n" +
		"Content-Type: text/plain\r\n\r\n" +
		"What is the status?"
	uid := fix.AddRawMessage("INBOX", []byte(raw))

	r := callTool(t, s, "reply_message", map[string]any{
		"message_id": fmt.Sprintf("%d", uid),
		"body":       "Status is green.",
	})
	require.False(t, r.IsError, "reply failed: %s", r.text())
	assert.Contains(t, r.text(), "without threading headers")

	sent := fix.SentMessages()
	require.Len(t, sent, 1)
	delivered := string(sent[0].Raw)
	assert.NotContains(t, delivered, "In-Reply-To:")
	assert.NotContains(t, delivered, "References:")
	// The quote is still assembled — a missing Message-ID does not affect it.
	assert.Contains(t, delivered, "> What is the status?")
}

func TestHandler_ReplyMessage_UnquotableBody_OmitsQuote(t *testing.T) {
	s, env, fix := setupHandler(t)
	env.AddContact("Alice", "alice@test.com", "-w-")
	// Multipart with only an attachment part: ParseMIME yields "(no text body)".
	raw := "From: Alice <alice@test.com>\r\n" +
		"To: me@test.com\r\n" +
		"Subject: [punt-labs/beadle] Question\r\n" +
		"Message-ID: <orig@test>\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"b\"\r\n\r\n" +
		"--b\r\n" +
		"Content-Type: application/octet-stream\r\n" +
		"Content-Disposition: attachment; filename=\"x.bin\"\r\n\r\n" +
		"BINARYDATA\r\n" +
		"--b--\r\n"
	uid := fix.AddRawMessage("INBOX", []byte(raw))

	r := callTool(t, s, "reply_message", map[string]any{
		"message_id": fmt.Sprintf("%d", uid),
		"body":       "Got it, thanks.",
	})
	require.False(t, r.IsError, "reply failed: %s", r.text())
	assert.Contains(t, r.text(), "could not be extracted")

	sent := fix.SentMessages()
	require.Len(t, sent, 1)
	delivered := string(sent[0].Raw)
	// No diagnostic sentinel is shipped, and no quote block is emitted.
	assert.NotContains(t, delivered, "(no text body)")
	assert.NotContains(t, delivered, "> ")
	assert.Contains(t, delivered, "Got it, thanks.")
	// Threading is intact — the original has a Message-ID.
	assert.Contains(t, delivered, "In-Reply-To: <orig@test>")
}

func TestHandler_MoveMessage(t *testing.T) {
	s, env, fix := setupHandler(t)
	env.AddContact("Alice", "alice@test.com", "r--")

	uid := fix.AddMessage("INBOX", "alice@test.com", "To Archive", "archive me")
	fix.AddMessage("Archive", "system@test.com", "Placeholder", "x")

	r := callTool(t, s, "move_message", map[string]any{
		"message_id":  fmt.Sprintf("%d", uid),
		"destination": "Archive",
	})
	assert.False(t, r.IsError, "move failed: %s", r.text())
	assert.Contains(t, r.text(), "moved")
}

func TestHandler_BatchMoveMessages(t *testing.T) {
	s, env, fix := setupHandler(t)
	env.AddContact("Alice", "alice@test.com", "r--")

	uid1 := fix.AddMessage("INBOX", "alice@test.com", "Msg 1", "body 1")
	uid2 := fix.AddMessage("INBOX", "alice@test.com", "Msg 2", "body 2")
	uid3 := fix.AddMessage("INBOX", "alice@test.com", "Msg 3", "body 3")
	fix.AddMessage("Archive", "system@test.com", "Placeholder", "x")

	r := callTool(t, s, "batch_move_messages", map[string]any{
		"message_ids": []any{
			fmt.Sprintf("%d", uid1),
			fmt.Sprintf("%d", uid2),
			fmt.Sprintf("%d", uid3),
		},
		"destination": "Archive",
	})
	assert.False(t, r.IsError, "batch move failed: %s", r.text())
	assert.Contains(t, r.text(), "moved 3 messages")
	assert.Contains(t, r.text(), "Archive")
}

func TestHandler_MoveMessage_NotFound(t *testing.T) {
	s, env, fix := setupHandler(t)
	env.AddContact("Alice", "alice@test.com", "r--")
	fix.AddMessage("Archive", "system@test.com", "Placeholder", "x")

	r := callTool(t, s, "move_message", map[string]any{
		"message_id":  "9999",
		"destination": "Archive",
	})
	assert.False(t, r.IsError, "a missing UID is not an error")
	assert.Contains(t, r.text(), "not found")
	assert.NotContains(t, r.text(), "moved #", "must not claim a move that did not happen")
}

func TestHandler_BatchMoveMessages_Shortfall(t *testing.T) {
	s, env, fix := setupHandler(t)
	env.AddContact("Alice", "alice@test.com", "r--")

	uid1 := fix.AddMessage("INBOX", "alice@test.com", "Msg 1", "body")
	uid2 := fix.AddMessage("INBOX", "alice@test.com", "Msg 2", "body")
	fix.AddMessage("Archive", "system@test.com", "Placeholder", "x")

	// Two present + one absent UID: only two move; the shortfall is reported.
	r := callTool(t, s, "batch_move_messages", map[string]any{
		"message_ids": []any{fmt.Sprintf("%d", uid1), fmt.Sprintf("%d", uid2), "9999"},
		"destination": "Archive",
	})
	assert.False(t, r.IsError, "batch move failed: %s", r.text())
	assert.Contains(t, r.text(), "moved 2 of 3 messages to Archive (1 not found)")
}

func TestHandler_BatchMoveMessages_DuplicateUIDNoShortfall(t *testing.T) {
	s, env, fix := setupHandler(t)
	env.AddContact("Alice", "alice@test.com", "r--")

	uid1 := fix.AddMessage("INBOX", "alice@test.com", "Msg 1", "body")
	uid2 := fix.AddMessage("INBOX", "alice@test.com", "Msg 2", "body")
	fix.AddMessage("Archive", "system@test.com", "Placeholder", "x")

	// uid1 appears twice: the request has 2 distinct UIDs, both present, so it
	// reports "moved 2 messages" — a repeat must not manufacture a shortfall.
	r := callTool(t, s, "batch_move_messages", map[string]any{
		"message_ids": []any{fmt.Sprintf("%d", uid1), fmt.Sprintf("%d", uid1), fmt.Sprintf("%d", uid2)},
		"destination": "Archive",
	})
	assert.False(t, r.IsError, "batch move failed: %s", r.text())
	assert.Contains(t, r.text(), "moved 2 messages to Archive")
	assert.NotContains(t, r.text(), "not found")
}

func TestHandler_BatchMarkMessages_DuplicateUIDNoShortfall(t *testing.T) {
	s, env, fix := setupHandler(t)
	env.AddContact("Alice", "alice@test.com", "r--")

	uid1 := fix.AddMessage("INBOX", "alice@test.com", "Msg 1", "body")
	uid2 := fix.AddMessage("INBOX", "alice@test.com", "Msg 2", "body")

	r := callTool(t, s, "batch_mark_messages", map[string]any{
		"message_ids": []any{fmt.Sprintf("%d", uid1), fmt.Sprintf("%d", uid2), fmt.Sprintf("%d", uid1)},
	})
	assert.False(t, r.IsError, "batch mark failed: %s", r.text())
	assert.Contains(t, r.text(), "marked 2 messages read")
	assert.NotContains(t, r.text(), "not found")
}

func TestHandler_BatchMoveMessages_InvalidUID(t *testing.T) {
	s, env, fix := setupHandler(t)
	env.AddContact("Alice", "alice@test.com", "r--")

	uid1 := fix.AddMessage("INBOX", "alice@test.com", "Msg 1", "body 1")
	fix.AddMessage("Archive", "system@test.com", "Placeholder", "x")

	r := callTool(t, s, "batch_move_messages", map[string]any{
		"message_ids": []any{
			fmt.Sprintf("%d", uid1),
			"not-a-number",
		},
		"destination": "Archive",
	})
	assert.True(t, r.IsError, "invalid UID should produce error")
	assert.Contains(t, r.text(), "#not-a-number")
	assert.Contains(t, r.text(), "invalid")
}

func TestHandler_BatchMoveMessages_Empty(t *testing.T) {
	s, _, _ := setupHandler(t)

	r := callTool(t, s, "batch_move_messages", map[string]any{
		"message_ids": []any{},
	})
	assert.False(t, r.IsError, "batch move failed: %s", r.text())
	assert.Contains(t, r.text(), "moved 0 messages")
}

func TestHandler_BatchMoveMessages_MissingParam(t *testing.T) {
	s, _, _ := setupHandler(t)

	r := callTool(t, s, "batch_move_messages", map[string]any{})
	assert.True(t, r.IsError, "missing message_ids should produce error")
	assert.Contains(t, r.text(), "message_ids is required")
}

// splitLines splits rendered tool output into lines for width assertions.
func splitLines(s string) []string {
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

// listUnreadMarker returns the list_messages output, used to assert a message's
// read state via the "●" marker the table prints for unread mail.
func listUnreadMarker(t *testing.T, s *server.MCPServer) string {
	t.Helper()
	r := callTool(t, s, "list_messages", map[string]any{"count": 10, "all_repos": true})
	require.False(t, r.IsError, "list failed: %s", r.text())
	return r.text()
}

func TestHandler_MarkMessage_SetAndClearSeen(t *testing.T) {
	s, env, fix := setupHandler(t)
	env.AddContact("Alice", "alice@test.com", "r--")

	uid := fix.AddMessage("INBOX", "alice@test.com", "Mark me", "body")

	// Seeded messages start unread.
	assert.Contains(t, listUnreadMarker(t, s), "●", "message starts unread")

	// Mark read.
	r := callTool(t, s, "mark_message", map[string]any{"message_id": fmt.Sprintf("%d", uid)})
	assert.False(t, r.IsError, "mark read failed: %s", r.text())
	assert.Contains(t, r.text(), "marked")
	assert.Contains(t, r.text(), "read")
	assert.NotContains(t, listUnreadMarker(t, s), "●", "message is read after mark")

	// Mark unread again.
	r = callTool(t, s, "mark_message", map[string]any{"message_id": fmt.Sprintf("%d", uid), "seen": false})
	assert.False(t, r.IsError, "mark unread failed: %s", r.text())
	assert.Contains(t, r.text(), "unread")
	assert.Contains(t, listUnreadMarker(t, s), "●", "message is unread after clearing Seen")
}

func TestHandler_ReadMessage_DoesNotMarkSeen(t *testing.T) {
	s, env, fix := setupHandler(t)
	env.AddContact("Alice", "alice@test.com", "r--")

	uid := fix.AddMessage("INBOX", "alice@test.com", "Peek me", "body")

	r := callTool(t, s, "read_message", map[string]any{"message_id": fmt.Sprintf("%d", uid)})
	require.False(t, r.IsError, "read failed: %s", r.text())

	// Reading must not set \Seen — the message is still unread.
	assert.Contains(t, listUnreadMarker(t, s), "●", "read_message must not mark the message seen")
}

func TestHandler_BatchMarkMessages(t *testing.T) {
	s, env, fix := setupHandler(t)
	env.AddContact("Alice", "alice@test.com", "r--")

	uid1 := fix.AddMessage("INBOX", "alice@test.com", "Msg 1", "body")
	uid2 := fix.AddMessage("INBOX", "alice@test.com", "Msg 2", "body")

	r := callTool(t, s, "batch_mark_messages", map[string]any{
		"message_ids": []any{fmt.Sprintf("%d", uid1), fmt.Sprintf("%d", uid2)},
	})
	assert.False(t, r.IsError, "batch mark failed: %s", r.text())
	assert.Contains(t, r.text(), "marked 2 messages read")
	assert.NotContains(t, listUnreadMarker(t, s), "●", "both messages read after batch mark")
}

func TestHandler_MarkMessage_NotFound(t *testing.T) {
	s, _, _ := setupHandler(t)

	r := callTool(t, s, "mark_message", map[string]any{"message_id": "9999"})
	assert.False(t, r.IsError, "a missing UID is not an error")
	assert.Contains(t, r.text(), "not found")
	assert.NotContains(t, r.text(), "marked #9999 read", "must not claim a mark that did not happen")
}

func TestHandler_BatchMarkMessages_Shortfall(t *testing.T) {
	s, env, fix := setupHandler(t)
	env.AddContact("Alice", "alice@test.com", "r--")

	uid1 := fix.AddMessage("INBOX", "alice@test.com", "Msg 1", "body")

	// One present + one absent UID: only one is modified; shortfall reported.
	r := callTool(t, s, "batch_mark_messages", map[string]any{
		"message_ids": []any{fmt.Sprintf("%d", uid1), "9999"},
	})
	assert.False(t, r.IsError, "batch mark failed: %s", r.text())
	assert.Contains(t, r.text(), "marked 1 of 2 messages read (1 not found)")
}

func TestHandler_BatchMarkMessages_InvalidUID(t *testing.T) {
	s, _, _ := setupHandler(t)

	r := callTool(t, s, "batch_mark_messages", map[string]any{
		"message_ids": []any{"1", "not-a-number"},
	})
	assert.True(t, r.IsError, "invalid UID should produce error")
	assert.Contains(t, r.text(), "#not-a-number")
}

func TestHandler_BatchMarkMessages_Empty(t *testing.T) {
	s, _, _ := setupHandler(t)

	r := callTool(t, s, "batch_mark_messages", map[string]any{"message_ids": []any{}})
	assert.False(t, r.IsError, "empty batch failed: %s", r.text())
	assert.Contains(t, r.text(), "marked 0 messages read")
}

func TestHandler_BatchMarkMessages_MissingParam(t *testing.T) {
	s, _, _ := setupHandler(t)

	r := callTool(t, s, "batch_mark_messages", map[string]any{})
	assert.True(t, r.IsError, "missing message_ids should produce error")
	assert.Contains(t, r.text(), "message_ids is required")
}

func TestHandler_Contacts_CRUD(t *testing.T) {
	s, _, _ := setupHandler(t)

	// Add a contact.
	r := callTool(t, s, "add_contact", map[string]any{
		"name":  "Charlie",
		"email": "charlie@test.com",
	})
	assert.False(t, r.IsError, "add failed: %s", r.text())

	// Find the contact.
	r = callTool(t, s, "find_contact", map[string]any{
		"query": "Charlie",
	})
	assert.False(t, r.IsError)
	assert.Contains(t, r.text(), "charlie@test.com")

	// List contacts.
	r = callTool(t, s, "list_contacts", nil)
	assert.False(t, r.IsError)
	assert.Contains(t, r.text(), "Charlie")

	// Remove the contact.
	r = callTool(t, s, "remove_contact", map[string]any{
		"name": "Charlie",
	})
	assert.False(t, r.IsError)

	// Verify removal.
	r = callTool(t, s, "find_contact", map[string]any{
		"query": "Charlie",
	})
	// After removal with no remaining contacts, find returns a non-error empty result.
	assert.False(t, r.IsError)
	assert.Equal(t, "No contacts.", r.text())
}

// --- Pattern Contact Tests ---

func TestHandler_AddContact_PatternRejectsRWX(t *testing.T) {
	s, _, _ := setupHandler(t)

	r := callTool(t, s, "add_contact", map[string]any{
		"name":        "Anthropic Mail",
		"email":       "*@mail.anthropic.com",
		"permissions": "rwx",
	})
	assert.True(t, r.IsError)
	assert.Contains(t, r.text(), "pattern contacts may only grant read")
}

func TestHandler_AddContact_PatternRejectsRW(t *testing.T) {
	s, _, _ := setupHandler(t)

	r := callTool(t, s, "add_contact", map[string]any{
		"name":        "Anthropic Mail",
		"email":       "*@mail.anthropic.com",
		"permissions": "rw-",
	})
	assert.True(t, r.IsError)
	assert.Contains(t, r.text(), "pattern contacts may only grant read")
}

func TestHandler_AddContact_PatternAcceptsReadOnly(t *testing.T) {
	s, _, _ := setupHandler(t)

	r := callTool(t, s, "add_contact", map[string]any{
		"name":        "Anthropic Mail",
		"email":       "*@mail.anthropic.com",
		"permissions": "r--",
	})
	assert.False(t, r.IsError, "add failed: %s", r.text())

	r = callTool(t, s, "find_contact", map[string]any{
		"query": "*@mail.anthropic.com",
	})
	assert.False(t, r.IsError)
	assert.Contains(t, r.text(), "Anthropic Mail")
}

func TestHandler_ListMessages_OffsetPaging(t *testing.T) {
	s, env, fix := setupHandler(t)
	env.AddContact("Alice", "alice@test.com", "r--")

	for i := range 25 {
		fix.AddMessage("INBOX", "alice@test.com", fmt.Sprintf("msg-%02d", i), "body")
	}

	// Page 0: newest 10 (msg-15..msg-24).
	r := callTool(t, s, "list_messages", map[string]any{"count": 10, "offset": float64(0), "all_repos": true})
	assert.False(t, r.IsError, "list failed: %s", r.text())
	assert.Contains(t, r.text(), "msg-24")
	assert.Contains(t, r.text(), "msg-15")
	assert.NotContains(t, r.text(), "msg-14")

	// Page 1: next 10 (msg-05..msg-14).
	r = callTool(t, s, "list_messages", map[string]any{"count": 10, "offset": float64(10), "all_repos": true})
	assert.False(t, r.IsError, "list failed: %s", r.text())
	assert.Contains(t, r.text(), "msg-14")
	assert.Contains(t, r.text(), "msg-05")
	assert.NotContains(t, r.text(), "msg-15")
}

func TestHandler_ListMessages_PastEndPageShowsTotal(t *testing.T) {
	s, env, fix := setupHandler(t)
	env.AddContact("Alice", "alice@test.com", "r--")

	for i := range 25 {
		fix.AddMessage("INBOX", "alice@test.com", fmt.Sprintf("msg-%02d", i), "body")
	}

	// A page far past the end must show the true total, not read as an empty
	// mailbox.
	r := callTool(t, s, "list_messages", map[string]any{"count": 10, "offset": float64(9999), "all_repos": true})
	assert.False(t, r.IsError, "list failed: %s", r.text())
	assert.Contains(t, r.text(), "showing 0 of 25 messages")
	assert.Contains(t, r.text(), "page past end")
	assert.NotContains(t, r.text(), "No messages.", "a past-end page is not an empty mailbox")
}

func TestHandler_ListMessages_NegativeOffset(t *testing.T) {
	s, _, _ := setupHandler(t)

	r := callTool(t, s, "list_messages", map[string]any{"offset": float64(-1), "all_repos": true})
	assert.True(t, r.IsError, "negative offset should error")
	assert.Contains(t, r.text(), "offset must be non-negative")
}

func TestHandler_ListMessages_NonPositiveCount(t *testing.T) {
	s, _, _ := setupHandler(t)

	for _, c := range []float64{0, -1} {
		r := callTool(t, s, "list_messages", map[string]any{"count": c, "all_repos": true})
		assert.True(t, r.IsError, "count %v should error", c)
		assert.Contains(t, r.text(), "count must be positive")
	}
}

func TestHandler_SearchMessages_NonPositiveCount(t *testing.T) {
	s, _, _ := setupHandler(t)

	for _, c := range []float64{0, -3} {
		r := callTool(t, s, "search_messages", map[string]any{"text": "x", "count": c, "all_repos": true})
		assert.True(t, r.IsError, "count %v should error", c)
		assert.Contains(t, r.text(), "count must be positive")
	}
}

func TestHandler_SearchMessages_ByFromAndSubject(t *testing.T) {
	s, env, fix := setupHandler(t)
	env.AddContact("Alice", "alice@test.com", "r--")
	env.AddContact("Bob", "bob@test.com", "r--")

	fix.AddMessage("INBOX", "alice@test.com", "release plan", "body")
	fix.AddMessage("INBOX", "bob@test.com", "lunch", "body")

	// from filters to alice's mail.
	r := callTool(t, s, "search_messages", map[string]any{"from": "alice@test.com", "all_repos": true})
	assert.False(t, r.IsError, "search failed: %s", r.text())
	assert.Contains(t, r.text(), "release plan")
	assert.NotContains(t, r.text(), "lunch")

	// subject filters to the release mail.
	r = callTool(t, s, "search_messages", map[string]any{"subject": "release", "all_repos": true})
	assert.False(t, r.IsError, "search failed: %s", r.text())
	assert.Contains(t, r.text(), "release plan")
	assert.NotContains(t, r.text(), "lunch")

	// Every rendered line fits the 80-rune budget.
	for _, line := range splitLines(r.text()) {
		assert.LessOrEqual(t, utf8.RuneCountInString(line), 80, "row over 80 runes: %q", line)
	}
}

func TestHandler_SearchMessages_RequiresCriterion(t *testing.T) {
	s, _, _ := setupHandler(t)

	r := callTool(t, s, "search_messages", map[string]any{"all_repos": true})
	assert.True(t, r.IsError, "empty search should error")
	assert.Contains(t, r.text(), "at least one of")
}

func TestHandler_SearchMessages_BadSince(t *testing.T) {
	s, _, _ := setupHandler(t)

	r := callTool(t, s, "search_messages", map[string]any{"since": "last tuesday", "all_repos": true})
	assert.True(t, r.IsError, "bad since should error")
	assert.Contains(t, r.text(), "invalid date")
}

func TestHandler_SearchMessages_NegativeOffset(t *testing.T) {
	s, _, _ := setupHandler(t)

	r := callTool(t, s, "search_messages", map[string]any{"text": "x", "offset": float64(-1), "all_repos": true})
	assert.True(t, r.IsError, "negative offset should error")
	assert.Contains(t, r.text(), "offset must be non-negative")
}

func TestHandler_SearchMessages_ScopesToCurrentRepo(t *testing.T) {
	slug := email.ResolveRepoTag(context.Background(), nil, "").Slug
	if slug == "" {
		t.Skip("no git remote resolved; repo scoping is a no-op here")
	}

	s, env, fix := setupHandler(t)
	env.AddContact("Alice", "alice@test.com", "r--")

	raw := fmt.Sprintf("From: alice@test.com\r\n%s: %s\r\nSubject: scoped release\r\n"+
		"Content-Type: text/plain\r\n\r\nbody", email.HeaderRepo, slug)
	fix.AddRawMessage("INBOX", []byte(raw))
	fix.AddMessage("INBOX", "alice@test.com", "untagged release", "body")

	// Default scope: only the current repo's matching mail.
	r := callTool(t, s, "search_messages", map[string]any{"subject": "release"})
	assert.False(t, r.IsError, "search failed: %s", r.text())
	assert.Contains(t, r.text(), "scoped release")
	assert.NotContains(t, r.text(), "untagged release")

	// all_repos widens to every repo.
	r = callTool(t, s, "search_messages", map[string]any{"subject": "release", "all_repos": true})
	assert.False(t, r.IsError, "search failed: %s", r.text())
	assert.Contains(t, r.text(), "scoped release")
	assert.Contains(t, r.text(), "untagged release")
}

func TestHandler_ListMessages_ScopesToCurrentRepo(t *testing.T) {
	slug := email.ResolveRepoTag(context.Background(), nil, "").Slug
	if slug == "" {
		t.Skip("no git remote resolved; repo scoping is a no-op here")
	}

	s, env, fix := setupHandler(t)
	env.AddContact("Alice", "alice@test.com", "r--")

	raw := fmt.Sprintf("From: alice@test.com\r\n%s: %s\r\nSubject: scoped hit\r\n"+
		"Content-Type: text/plain\r\n\r\nbody", email.HeaderRepo, slug)
	fix.AddRawMessage("INBOX", []byte(raw))
	fix.AddMessage("INBOX", "alice@test.com", "untagged miss", "body")

	// Default: only the current repo's mail.
	r := callTool(t, s, "list_messages", map[string]any{"count": 10})
	assert.False(t, r.IsError, "list failed: %s", r.text())
	assert.Contains(t, r.text(), "scoped hit")
	assert.NotContains(t, r.text(), "untagged miss")

	// Override: every repo.
	r = callTool(t, s, "list_messages", map[string]any{"count": 10, "all_repos": true})
	assert.False(t, r.IsError, "list failed: %s", r.text())
	assert.Contains(t, r.text(), "scoped hit")
	assert.Contains(t, r.text(), "untagged miss")
}

func TestHandler_ListMessages_PatternPermissionSurfacesSubject(t *testing.T) {
	s, env, fix := setupHandler(t)

	env.AddContact("Anthropic Mail", "*@mail.anthropic.com", "r--")
	fix.AddMessage("INBOX", "no-reply-xyz@mail.anthropic.com", "Status Update", "body")

	r := callTool(t, s, "list_messages", map[string]any{"count": 10, "all_repos": true})
	assert.False(t, r.IsError, "list failed: %s", r.text())
	assert.Contains(t, r.text(), "Status Update")
	assert.NotContains(t, r.text(), "redacted")
}

func TestHandler_ListMessages_UnmatchedSenderRedacted(t *testing.T) {
	s, env, fix := setupHandler(t)

	// Pattern only covers a different domain; this rotating sender has no grant.
	env.AddContact("Anthropic Mail", "*@mail.anthropic.com", "r--")
	fix.AddMessage("INBOX", "no-reply@other.com", "Leaky Subject", "body")

	r := callTool(t, s, "list_messages", map[string]any{"count": 10, "all_repos": true})
	assert.False(t, r.IsError, "list failed: %s", r.text())
	assert.NotContains(t, r.text(), "Leaky Subject")
	assert.Contains(t, r.text(), "redacted")
}

// --- Identity Switching Tests ---

func TestHandler_SwitchIdentity_Valid(t *testing.T) {
	s, env, _ := setupHandler(t)

	// Add a second identity (human).
	env.AddIdentity("sam", "Sam Jackson", "sam@test.com")

	// Switch to the human identity.
	r := callTool(t, s, "switch_identity", map[string]any{
		"handle": "sam",
	})
	assert.False(t, r.IsError, "switch failed: %s", r.text())
	assert.Contains(t, r.text(), "switched to sam")
	assert.Contains(t, r.text(), "sam@test.com")

	// Verify whoami reflects the switch.
	r = callTool(t, s, "whoami", nil)
	assert.False(t, r.IsError)
	assert.Contains(t, r.text(), "sam@test.com")
	assert.Contains(t, r.text(), "override")
}

func TestHandler_SwitchIdentity_Reset(t *testing.T) {
	s, env, _ := setupHandler(t)
	env.AddIdentity("sam", "Sam Jackson", "sam@test.com")

	// Switch to human.
	callTool(t, s, "switch_identity", map[string]any{"handle": "sam"})

	// Reset to default.
	r := callTool(t, s, "switch_identity", map[string]any{"handle": ""})
	assert.False(t, r.IsError)
	assert.Contains(t, r.text(), "reset")
	assert.Contains(t, r.text(), testEmail)

	// Verify whoami shows default identity.
	r = callTool(t, s, "whoami", nil)
	assert.False(t, r.IsError)
	assert.Contains(t, r.text(), testEmail)
	assert.NotContains(t, r.text(), "override")
}

func TestHandler_SwitchIdentity_UnknownHandle(t *testing.T) {
	s, _, _ := setupHandler(t)

	r := callTool(t, s, "switch_identity", map[string]any{
		"handle": "nonexistent",
	})
	assert.True(t, r.IsError)
	assert.Contains(t, r.text(), "resolve identity")

	// Verify the default identity is still active.
	r = callTool(t, s, "whoami", nil)
	assert.False(t, r.IsError)
	assert.Contains(t, r.text(), testEmail)
}

func TestHandler_SwitchIdentity_WithMailOps(t *testing.T) {
	s, env, fix := setupHandler(t)

	// Add human identity with its own config pointing at the test servers.
	env.AddIdentity("sam", "Sam Jackson", "sam@test.com")
	env.WriteConfigForIdentity("sam@test.com", fix.Config)

	// Add a contact with write permission under the human identity.
	// (Contacts are per-identity, but for simplicity we test with the default contacts.)
	env.AddContact("Bob", "bob@test.com", "-w-")

	// Switch to human.
	r := callTool(t, s, "switch_identity", map[string]any{"handle": "sam"})
	assert.False(t, r.IsError, "switch failed: %s", r.text())

	// Send email as the human identity.
	r = callTool(t, s, "send_email", map[string]any{
		"to":      "bob@test.com",
		"subject": "From Sam",
		"body":    "Hello from the human",
	})
	// This may error due to contacts not being set up for sam@test.com identity.
	// The key verification is that the switch happened — check whoami.
	r = callTool(t, s, "whoami", nil)
	assert.Contains(t, r.text(), "sam@test.com")
}

// --- Poll Tool Tests ---

func TestHandler_GetPollStatus(t *testing.T) {
	s, _, _ := setupHandlerWithPoller(t)

	r := callTool(t, s, "get_poll_status", nil)
	assert.False(t, r.IsError, "get_poll_status failed: %s", r.text())
	assert.Contains(t, r.text(), "disabled")
}

func TestHandler_SetPollInterval_Valid(t *testing.T) {
	s, _, _ := setupHandlerWithPoller(t)

	r := callTool(t, s, "set_poll_interval", map[string]any{"interval": "10m"})
	assert.False(t, r.IsError, "set_poll_interval failed: %s", r.text())
	assert.Contains(t, r.text(), "10m")

	r = callTool(t, s, "get_poll_status", nil)
	assert.False(t, r.IsError)
	assert.Contains(t, r.text(), "10m")
	assert.Contains(t, r.text(), "yes")
}

func TestHandler_SetPollInterval_Disable(t *testing.T) {
	s, _, _ := setupHandlerWithPoller(t)

	callTool(t, s, "set_poll_interval", map[string]any{"interval": "5m"})
	r := callTool(t, s, "set_poll_interval", map[string]any{"interval": "n"})
	assert.False(t, r.IsError)
	assert.Contains(t, r.text(), "disabled")
}

func TestHandler_SetPollInterval_Invalid(t *testing.T) {
	s, _, _ := setupHandlerWithPoller(t)

	r := callTool(t, s, "set_poll_interval", map[string]any{"interval": "3m"})
	assert.True(t, r.IsError)
	assert.Contains(t, r.text(), "invalid")
}
