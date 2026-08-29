package mcp_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/punt-labs/beadle/internal/email"
	mcptools "github.com/punt-labs/beadle/internal/mcp"
	"github.com/punt-labs/beadle/internal/testenv"
	"github.com/punt-labs/beadle/internal/testserver"
)

// newMarkerServer builds an MCP server with the poll tools registered and
// returns the server and the UnreadMarker that owns get_poll_status's
// description. Tool capabilities are on so AddTool fires tools/list_changed.
func newMarkerServer(t *testing.T) (*server.MCPServer, *mcptools.UnreadMarker) {
	t.Helper()

	env := testenv.New(t, testEmail)
	fix := testserver.NewFixture(t)
	env.WriteConfig(fix.Config)

	s := server.NewMCPServer("beadle-email", "test", server.WithToolCapabilities(true))
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	dialer := testserver.TestDialer{Password: "testpass"}
	poller := email.NewPoller(nil, env.Resolver, logger, dialer)
	marker := mcptools.RegisterTools(s, env.Resolver, logger, mcptools.WithDialer(dialer), mcptools.WithPoller(poller))
	require.NotNil(t, marker)
	return s, marker
}

// recordingSession is a ClientSession whose notification channel the test reads,
// so it can observe the tools/list_changed AddTool fires. A real transport's
// session is opaque from outside the server package; this stand-in satisfies the
// same interface.
type recordingSession struct {
	id   string
	ch   chan mcplib.JSONRPCNotification
	init atomic.Bool
}

func (s *recordingSession) SessionID() string { return s.id }
func (s *recordingSession) Initialize()       { s.init.Store(true) }
func (s *recordingSession) Initialized() bool { return s.init.Load() }
func (s *recordingSession) NotificationChannel() chan<- mcplib.JSONRPCNotification {
	return s.ch
}

// assertNotify requires that a tools/list_changed notification is already
// queued. AddTool sends synchronously to the buffered channel, so a change has
// arrived by the time Update returns.
func assertNotify(t *testing.T, ch <-chan mcplib.JSONRPCNotification) {
	t.Helper()
	select {
	case n := <-ch:
		assert.Equal(t, mcplib.MethodNotificationToolsListChanged, n.Method)
	case <-time.After(2 * time.Second):
		t.Fatal("expected a tools/list_changed notification, got none")
	}
}

// assertNoNotify requires that no notification is queued: an unchanged bucket
// must not re-register the tool.
func assertNoNotify(t *testing.T, ch <-chan mcplib.JSONRPCNotification) {
	t.Helper()
	select {
	case n := <-ch:
		t.Fatalf("expected no notification, got %s", n.Method)
	case <-time.After(50 * time.Millisecond):
	}
}

// pollStatusDesc returns get_poll_status's current description from tools/list.
func pollStatusDesc(t *testing.T, s *server.MCPServer) string {
	t.Helper()
	resp := callMCP(t, s, "tools/list", 99, nil)
	var out struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"tools"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(resp, &out))
	for _, tool := range out.Result.Tools {
		if tool.Name == "get_poll_status" {
			return tool.Description
		}
	}
	t.Fatal("get_poll_status not registered")
	return ""
}

func initParams() map[string]any {
	return map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "1.0"},
	}
}

// TestUnreadMarker_ReRegistersOnBucketChangeOnly proves the marker re-registers
// get_poll_status — and so fires tools/list_changed — exactly when the rendered
// bucket changes, and clears at zero.
func TestUnreadMarker_ReRegistersOnBucketChangeOnly(t *testing.T) {
	s, marker := newMarkerServer(t)

	sess := &recordingSession{id: "marker-test", ch: make(chan mcplib.JSONRPCNotification, 16)}
	require.NoError(t, s.RegisterSession(context.Background(), sess))
	sess.Initialize() // only initialized sessions receive broadcasts

	// Registration of the other tools happened before the session existed;
	// start from a clean channel.
	drain(sess.ch)

	tests := []struct {
		name       string
		unread     uint32
		wantNotify bool
	}{
		{"0 to 3 enters the count", 3, true},
		{"3 to 4 changes the exact count", 4, true},
		{"4 to 12 crosses into the 10+ bucket", 12, true},
		{"12 to 40 stays in the 10+ bucket", 40, false},
		{"40 to 60 crosses into the 50+ bucket", 60, true},
		{"60 to 0 clears the marker", 0, true},
		{"0 to 0 stays clear", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			marker.Update(tt.unread)
			if tt.wantNotify {
				assertNotify(t, sess.ch)
			} else {
				assertNoNotify(t, sess.ch)
			}
		})
	}
}

func drain(ch <-chan mcplib.JSONRPCNotification) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

// TestUnreadMarker_RegisteredDescriptionMatchesLastUpdate proves the invariant
// the F1 fix protects: after a sequence of Updates the registered
// get_poll_status description equals the marker for the last count, never a
// stale earlier one. Update holds its mutex across the m.cur write and the
// AddTool call, so the registration can never lag m.cur.
func TestUnreadMarker_RegisteredDescriptionMatchesLastUpdate(t *testing.T) {
	const base = "Show background inbox poller state: interval, active, last check time, unread count."

	tests := []struct {
		name   string
		counts []uint32
		want   string
	}{
		{"ends mid-bucket", []uint32{3, 7, 12, 40, 60}, base + " (50+ unread)"},
		{"ends on exact count", []uint32{60, 200, 5}, base + " (5 unread)"},
		{"ends cleared", []uint32{3, 200, 0}, base},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, marker := newMarkerServer(t)
			callMCP(t, s, "initialize", 0, initParams())
			for _, n := range tt.counts {
				marker.Update(n)
			}
			assert.Equal(t, tt.want, pollStatusDesc(t, s))
		})
	}
}

// TestUnreadMarker_DescriptionCarriesCount proves the count a client reads from
// get_poll_status's description tracks the marker, and clears at zero.
func TestUnreadMarker_DescriptionCarriesCount(t *testing.T) {
	s, marker := newMarkerServer(t)
	callMCP(t, s, "initialize", 0, initParams())

	assert.NotContains(t, pollStatusDesc(t, s), "unread)", "no marker before any unread")

	marker.Update(3)
	assert.Contains(t, pollStatusDesc(t, s), "(3 unread)")

	marker.Update(42)
	assert.Contains(t, pollStatusDesc(t, s), "(10+ unread)")

	marker.Update(0)
	assert.NotContains(t, pollStatusDesc(t, s), "unread)", "marker cleared at zero")
}

// TestSetPollInterval_DataDirFailureErrorsNotPanics proves the tool returns a
// clean error result, rather than panicking, when the fallback config path's
// underlying paths.DataDir() call fails. Regression guard for eagerly
// evaluating the panicking email.DefaultConfigPath() as a call argument on
// every set_poll_interval invocation — an MCP tool handler must not crash the
// server on an environment failure.
func TestSetPollInterval_DataDirFailureErrorsNotPanics(t *testing.T) {
	s, _ := newMarkerServer(t)
	callMCP(t, s, "initialize", 0, initParams())

	t.Setenv("HOME", "")

	var result toolResult
	require.NotPanics(t, func() {
		result = callTool(t, s, "set_poll_interval", map[string]any{"interval": "5m"})
	})
	assert.True(t, result.IsError, "expected an error result, not a crash")
}

// TestServerInstructions_Exposed proves the server surfaces the inbox/poll
// protocol in its initialize response.
func TestServerInstructions_Exposed(t *testing.T) {
	require.NotEmpty(t, mcptools.ServerInstructions)
	assert.Contains(t, mcptools.ServerInstructions, "get_poll_status")
	assert.Contains(t, mcptools.ServerInstructions, "/inbox")

	s := server.NewMCPServer("beadle-email", "test",
		server.WithToolCapabilities(true),
		server.WithInstructions(mcptools.ServerInstructions),
	)
	resp := callMCP(t, s, "initialize", 1, initParams())
	var out struct {
		Result struct {
			Instructions string `json:"instructions"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(resp, &out))
	assert.Equal(t, mcptools.ServerInstructions, out.Result.Instructions)
}
