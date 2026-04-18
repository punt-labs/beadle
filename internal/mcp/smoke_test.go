package mcp_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"

	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/punt-labs/beadle/internal/identity"
	mcptools "github.com/punt-labs/beadle/internal/mcp"
)

// newTestServer creates an MCP server with registered tools.
// The resolver points at empty dirs (no identity configured).
func newTestServer(t *testing.T, resolver *identity.Resolver) *server.MCPServer {
	t.Helper()
	s := server.NewMCPServer("beadle-email", "test", server.WithToolCapabilities(false))
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	mcptools.RegisterTools(s, resolver, logger)
	return s
}

func handleJSON(t *testing.T, s *server.MCPServer, method string, id int, params any) json.RawMessage {
	t.Helper()
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
	}
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

func TestMCPSmoke_Initialize(t *testing.T) {
	ethosDir := t.TempDir()
	beadleDir := t.TempDir()
	resolver := identity.NewResolver(ethosDir, beadleDir, "")
	s := newTestServer(t, resolver)

	resp := handleJSON(t, s, "initialize", 1, map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "1.0"},
	})

	var result struct {
		Result struct {
			ServerInfo struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(resp, &result))
	assert.Equal(t, "beadle-email", result.Result.ServerInfo.Name)
	assert.Equal(t, "test", result.Result.ServerInfo.Version)
}

func TestMCPSmoke_ToolRegistration(t *testing.T) {
	ethosDir := t.TempDir()
	beadleDir := t.TempDir()
	resolver := identity.NewResolver(ethosDir, beadleDir, "")
	s := newTestServer(t, resolver)

	// Must initialize first.
	handleJSON(t, s, "initialize", 1, map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "1.0"},
	})

	resp := handleJSON(t, s, "tools/list", 2, nil)

	var result struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(resp, &result))

	names := make([]string, len(result.Result.Tools))
	for i, tool := range result.Result.Tools {
		names[i] = tool.Name
	}

	expectedTools := []string{
		"list_messages", "read_message", "list_folders", "send_email",
		"verify_signature", "show_mime", "check_trust", "move_message",
		"batch_move_messages", "download_attachment", "list_contacts",
		"find_contact", "add_contact", "remove_contact", "whoami",
		"switch_identity",
	}

	for _, expected := range expectedTools {
		assert.Contains(t, names, expected, "missing tool: %s", expected)
	}
	assert.Equal(t, len(expectedTools), len(names), "unexpected tool count")
}

func TestMCPSmoke_IdentityError(t *testing.T) {
	// Resolver with empty dirs — identity resolution will fail.
	ethosDir := t.TempDir()
	beadleDir := t.TempDir()
	resolver := identity.NewResolver(ethosDir, beadleDir, "")
	s := newTestServer(t, resolver)

	// Initialize first.
	handleJSON(t, s, "initialize", 1, map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "1.0"},
	})

	// Call list_messages — should return an error result, not panic.
	resp := handleJSON(t, s, "tools/call", 2, map[string]any{
		"name":      "list_messages",
		"arguments": map[string]any{},
	})

	var result struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(resp, &result))

	// The tool should return an error (identity not found), not crash.
	assert.True(t, result.Result.IsError, "expected error result")
	require.NotEmpty(t, result.Result.Content)
	assert.Contains(t, result.Result.Content[0].Text, "identity")
}
