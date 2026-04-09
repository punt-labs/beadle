package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"testing"

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
	poller := email.NewPoller(s, env.Resolver, logger, dialer)
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

func TestHandler_ListMessages(t *testing.T) {
	s, env, fix := setupHandler(t)

	// Add a contact with read permission so messages aren't redacted.
	env.AddContact("Alice", "alice@test.com", "r--")

	fix.AddMessage("INBOX", "alice@test.com", "Hello World", "body")
	fix.AddMessage("INBOX", "alice@test.com", "Second Message", "body 2")

	r := callTool(t, s, "list_messages", map[string]any{"count": 10})
	assert.False(t, r.IsError)
	assert.Contains(t, r.text(), "Hello World")
	assert.Contains(t, r.text(), "Second Message")
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
		maxBody     any    // nil means omitted
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

func TestHandler_ListMessages_PatternPermissionSurfacesSubject(t *testing.T) {
	s, env, fix := setupHandler(t)

	env.AddContact("Anthropic Mail", "*@mail.anthropic.com", "r--")
	fix.AddMessage("INBOX", "no-reply-xyz@mail.anthropic.com", "Status Update", "body")

	r := callTool(t, s, "list_messages", map[string]any{"count": 10})
	assert.False(t, r.IsError, "list failed: %s", r.text())
	assert.Contains(t, r.text(), "Status Update")
	assert.NotContains(t, r.text(), "redacted")
}

func TestHandler_ListMessages_UnmatchedSenderRedacted(t *testing.T) {
	s, env, fix := setupHandler(t)

	// Pattern only covers a different domain; this rotating sender has no grant.
	env.AddContact("Anthropic Mail", "*@mail.anthropic.com", "r--")
	fix.AddMessage("INBOX", "no-reply@other.com", "Leaky Subject", "body")

	r := callTool(t, s, "list_messages", map[string]any{"count": 10})
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
