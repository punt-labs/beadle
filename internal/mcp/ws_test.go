package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mcptools "github.com/punt-labs/beadle/internal/mcp"
)

func TestWSServer_Health(t *testing.T) {
	s := server.NewMCPServer("beadle-email", "test", server.WithToolCapabilities(false))
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	ws := mcptools.NewWSServer(s, "1.2.3", logger)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", ws.HandleHealth)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/health")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "ok", body["status"])
	assert.Equal(t, "1.2.3", body["version"])
}

func TestWSServer_MCPInitialize(t *testing.T) {
	s := server.NewMCPServer("beadle-email", "test", server.WithToolCapabilities(false))
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	ws := mcptools.NewWSServer(s, "test", logger)

	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", ws.HandleMCP)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/mcp"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer conn.Close()

	initReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test", "version": "1.0"},
		},
	}
	require.NoError(t, conn.WriteJSON(initReq))

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := conn.ReadMessage()
	require.NoError(t, err)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(msg, &resp))
	assert.Equal(t, "2.0", resp["jsonrpc"])
	assert.NotNil(t, resp["result"], "initialize should return a result")

	result, ok := resp["result"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "beadle-email", result["serverInfo"].(map[string]any)["name"])
}

func TestWSServer_ListenAndServe(t *testing.T) {
	s := server.NewMCPServer("beadle-email", "test", server.WithToolCapabilities(false))
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	ws := mcptools.NewWSServer(s, "test", logger)

	// Find a free port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		ws.ListenAndServe(ctx, port)
	}()

	// Wait for the server to be ready.
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	require.Eventually(t, func() bool {
		c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err != nil {
			return false
		}
		c.Close()
		return true
	}, 2*time.Second, 50*time.Millisecond, "server did not start")

	// Health endpoint.
	resp, err := http.Get(fmt.Sprintf("http://%s/health", addr))
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// WebSocket initialize.
	wsURL := fmt.Sprintf("ws://%s/mcp", addr)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer conn.Close()

	initReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test", "version": "1.0"},
		},
	}
	require.NoError(t, conn.WriteJSON(initReq))

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := conn.ReadMessage()
	require.NoError(t, err)

	var respJSON map[string]any
	require.NoError(t, json.Unmarshal(msg, &respJSON))
	assert.NotNil(t, respJSON["result"])

	cancel()
}
