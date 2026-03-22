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
	// After removal, find returns either "not found" or "No contacts."
	assert.True(t, r.IsError || r.text() == "No contacts.",
		"expected not-found or no-contacts, got: %s", r.text())
}
