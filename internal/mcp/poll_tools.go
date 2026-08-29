package mcp

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/punt-labs/beadle/internal/email"
	"github.com/punt-labs/beadle/internal/paths"
)

// ServerInstructions primes the connecting agent on the inbox/poll protocol.
// This mailbox is the agent's own: a change in the unread count fires
// tools/list_changed and an approximate, bucketed count rides on
// get_poll_status's description. The MCP spec bounds the instructions length,
// so keep this short.
const ServerInstructions = "This mailbox is your own agent inbox. " +
	"When the unread count changes the server fires a tools/list_changed notification, " +
	"and get_poll_status's description shows an approximate count, bucketed " +
	"(exact 1-9, then 10+/50+/100+); it clears once the inbox is read down. " +
	"Process new mail with the /inbox command, or with list_messages then read_message, " +
	"replying with reply_message where a response is warranted. " +
	"Set a recurring check with set_poll_interval (e.g. 5m)."

func setPollIntervalTool() mcplib.Tool {
	return mcplib.NewTool("set_poll_interval",
		mcplib.WithDescription(
			"Set the background inbox polling interval. "+
				"The server checks INBOX periodically and sends a notification when new mail arrives. "+
				"Valid intervals: 1m, 5m, 10m, 15m, 30m, 1h, 2h. Use 'n' to disable.",
		),
		mcplib.WithString("interval",
			mcplib.Required(),
			mcplib.Description("Polling interval: 1m, 5m, 10m, 15m, 30m, 1h, 2h, or n (disable)"),
		),
	)
}

// getPollStatusTool builds the get_poll_status tool. The marker suffix carries
// the repo's unread count so a tools/list_changed notification is meaningful;
// it is empty when there is no unread mail.
func getPollStatusTool(marker string) mcplib.Tool {
	return mcplib.NewTool("get_poll_status",
		mcplib.WithDescription("Show background inbox poller state: interval, active, last check time, unread count."+marker),
	)
}

// unreadBucket renders an unread count into the marker appended to
// get_poll_status's description. Bucketing bounds prompt-cache churn: only a
// change of bucket re-registers the tool, so a steady count causes none. Counts
// 1-9 render the exact number — at that scale each new message is worth a
// distinct signal — and coarsen to thresholds above. The empty string means no
// unread mail and no marker.
func unreadBucket(n uint32) string {
	switch {
	case n == 0:
		return ""
	case n < 10:
		return fmt.Sprintf(" (%d unread)", n)
	case n < 50:
		return " (10+ unread)"
	case n < 100:
		return " (50+ unread)"
	default:
		return " (100+ unread)"
	}
}

// UnreadMarker keeps get_poll_status's description carrying the repo's unread
// count. Re-registering the tool with AddTool overwrites its entry and, under
// WithToolCapabilities(true), fires tools/list_changed — so a connected client
// both sees the new count and is woken to check it. Bucketing plus change
// detection bound the cost: an unchanged bucket re-registers nothing, and a
// zero count clears the marker.
type UnreadMarker struct {
	srv *server.MCPServer
	fn  server.ToolHandlerFunc

	mu  sync.Mutex
	cur string // last rendered marker; "" means no unread
}

// newUnreadMarker registers get_poll_status carrying the marker for n and
// returns a marker that keeps it current.
func newUnreadMarker(srv *server.MCPServer, fn server.ToolHandlerFunc, n uint32) *UnreadMarker {
	m := &UnreadMarker{srv: srv, fn: fn, cur: unreadBucket(n)}
	srv.AddTool(getPollStatusTool(m.cur), fn)
	return m
}

// Update re-registers get_poll_status when n falls in a different bucket than
// the current marker. Calls that leave the bucket unchanged do nothing, so a
// steady inbox produces no churn; a zero count clears the marker.
//
// The whole check-set-register runs under m.mu: onNewMail and the
// get_poll_status handler call Update from different goroutines, so committing
// m.cur and registering the matching description must be atomic. Splitting them
// would let two bucket changes commit m.cur in one order and AddTool in the
// opposite one, leaving the registered description diverged from m.cur — and,
// because change detection keys off m.cur, stuck there until the next change.
// The lock order is m.mu → mcp-go's internal tool locks; SendNotificationToAllClients
// never re-enters Update, so there is no cycle.
func (m *UnreadMarker) Update(n uint32) {
	marker := unreadBucket(n)
	m.mu.Lock()
	defer m.mu.Unlock()
	if marker == m.cur {
		return
	}
	m.cur = marker
	m.srv.AddTool(getPollStatusTool(marker), m.fn)
}

func (h *handler) setPollInterval(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	interval, err := req.RequireString("interval")
	if err != nil {
		return mcplib.NewToolResultError("interval is required"), nil
	}

	if !email.ValidPollInterval(interval) {
		return mcplib.NewToolResultError(
			fmt.Sprintf("invalid interval %q: must be 1m, 5m, 10m, 15m, 30m, 1h, 2h, or n", interval),
		), nil
	}

	// Persist to the default identity's config — the same path the poller
	// reads on restart. We bypass session identity overrides because the
	// poller always runs as the default identity.
	id, err := h.resolver.Resolve()
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("resolve identity: %v", err)), nil
	}
	// Build the fallback path via the non-panicking paths.DataDir() rather
	// than email.DefaultConfigPath() — DefaultConfigPath panics via
	// paths.MustDataDir() on a HOME-resolution failure, and an MCP tool
	// handler must return a clean error, never crash the server, on an
	// environment failure.
	dataDir, err := paths.DataDir()
	if err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("resolve data dir: %v", err)), nil
	}
	cfg, configPath, loadErr := email.LoadIdentityConfig(id, filepath.Join(dataDir, "email.json"))
	if loadErr != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("load config: %v", loadErr)), nil
	}
	cfg.PollInterval = interval
	if saveErr := email.SaveConfig(configPath, cfg); saveErr != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("save config: %v", saveErr)), nil
	}

	if err := h.poller.SetInterval(interval); err != nil {
		return mcplib.NewToolResultError(fmt.Sprintf("set interval: %v", err)), nil
	}

	if interval == "n" || interval == "" {
		return textResult("polling disabled")
	}
	return textResult(fmt.Sprintf("polling set to %s", interval))
}

func (h *handler) getPollStatus(_ context.Context, _ mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	st := h.poller.Status()
	return textResult(formatPollStatus(st))
}
