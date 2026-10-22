package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
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

// TestSetPollInterval_AcceptsEveryValidInterval proves set_poll_interval
// accepts every value in email.PollIntervals(), so a value added to that
// slice is automatically covered here without touching this test again.
func TestSetPollInterval_AcceptsEveryValidInterval(t *testing.T) {
	for _, interval := range email.PollIntervals() {
		t.Run(interval, func(t *testing.T) {
			s, _ := newMarkerServer(t)
			callMCP(t, s, "initialize", 0, initParams())

			result := callTool(t, s, "set_poll_interval", map[string]any{"interval": interval})
			assert.False(t, result.IsError, "expected %q to be accepted", interval)
		})
	}
}

// TestSetPollInterval_InvalidErrorListsFullSet proves the error message for
// an invalid interval names exactly the set in email.PollIntervals() — the
// two surfaces (this error text and validPollIntervals) cannot drift
// silently. The fixture "bogus" is not a substring of any valid interval,
// so a match here cannot be satisfied by the echoed input alone. The
// enumerated list is isolated (text after ": must be " up to ", or n") and
// split on ", ", then compared with assert.ElementsMatch rather than
// per-value Contains, since Contains("5m") would wrongly pass against a
// list that only contains "15m".
func TestSetPollInterval_InvalidErrorListsFullSet(t *testing.T) {
	s, _ := newMarkerServer(t)
	callMCP(t, s, "initialize", 0, initParams())

	result := callTool(t, s, "set_poll_interval", map[string]any{"interval": "bogus"})
	require.True(t, result.IsError)
	text := result.text()
	_, rest, found := strings.Cut(text, ": must be ")
	require.True(t, found, "expected %q to contain the %q separator", text, ": must be ")
	rest, found = strings.CutSuffix(rest, ", or n")
	require.True(t, found, "expected %q to end with %q", text, ", or n")
	assert.ElementsMatch(t, email.PollIntervals(), strings.Split(rest, ", "))
}

// TestSetPollIntervalTool_DescriptionsListFullSet proves both set_poll_interval
// surface strings — the tool description and the "interval" parameter
// description — list every element of email.PollIntervals(), so the MCP
// client-facing text cannot drift from the Go set.
func TestSetPollIntervalTool_DescriptionsListFullSet(t *testing.T) {
	s, _ := newMarkerServer(t)
	callMCP(t, s, "initialize", 0, initParams())

	resp := callMCP(t, s, "tools/list", 99, nil)
	var out struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
				InputSchema struct {
					Properties map[string]struct {
						Description string `json:"description"`
					} `json:"properties"`
				} `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(resp, &out))

	var tool *struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		InputSchema struct {
			Properties map[string]struct {
				Description string `json:"description"`
			} `json:"properties"`
		} `json:"inputSchema"`
	}
	for i := range out.Result.Tools {
		if out.Result.Tools[i].Name == "set_poll_interval" {
			tool = &out.Result.Tools[i]
			break
		}
	}
	require.NotNil(t, tool, "set_poll_interval not registered")

	intervalProp, ok := tool.InputSchema.Properties["interval"]
	require.True(t, ok, "interval property not found")

	_, descRest, found := strings.Cut(tool.Description, "Valid intervals: ")
	require.True(t, found, "expected tool description %q to contain %q", tool.Description, "Valid intervals: ")
	descRest, found = strings.CutSuffix(descRest, ". Use 'n' to disable.")
	require.True(t, found, "expected tool description %q to end with %q", tool.Description, ". Use 'n' to disable.")
	assert.ElementsMatch(t, email.PollIntervals(), strings.Split(descRest, ", "))

	_, propRest, found := strings.Cut(intervalProp.Description, "Polling interval: ")
	require.True(t, found, "expected interval property description %q to contain %q", intervalProp.Description, "Polling interval: ")
	propRest, found = strings.CutSuffix(propRest, ", or n (disable)")
	require.True(t, found, "expected interval property description %q to end with %q", intervalProp.Description, ", or n (disable)")
	assert.ElementsMatch(t, email.PollIntervals(), strings.Split(propRest, ", "))
}

// cronExpr derives the expected CronCreate expression for a poll interval
// value, used to prove inbox.md's cron table matches the real duration
// rather than a hand-typed guess. t.Fatalf on any shape this derivation
// does not cover, so a future interval value (e.g. a multi-hour-and-minute
// duration) forces this function to be extended rather than silently
// passing an unverified row.
func cronExpr(t *testing.T, value string) string {
	t.Helper()
	d, err := time.ParseDuration(value)
	require.NoError(t, err, "interval %q does not parse as a duration", value)

	switch {
	case d < time.Hour && d%time.Minute == 0:
		return fmt.Sprintf("*/%d * * * *", int(d/time.Minute))
	case d == time.Hour:
		return "0 * * * *"
	case d > time.Hour && d%time.Hour == 0:
		return fmt.Sprintf("0 */%d * * *", int(d/time.Hour))
	default:
		t.Fatalf("interval %q (%s) has no known cron-expression shape; extend cronExpr", value, d)
		return ""
	}
}

// argumentHintValues parses the argument-hint tokens between "[" and "]",
// dropping the non-interval tokens "<filter text>", "n", and "status".
func argumentHintValues(t *testing.T, doc string) []string {
	t.Helper()
	openIdx := strings.Index(doc, "[")
	closeIdx := strings.Index(doc, "]")
	require.True(t, openIdx >= 0 && closeIdx > openIdx, "argument-hint brackets not found in inbox.md")
	raw := strings.Split(doc[openIdx+1:closeIdx], " | ")
	var values []string
	for _, tok := range raw {
		if tok == "<filter text>" || tok == "n" || tok == "status" {
			continue
		}
		values = append(values, tok)
	}
	return values
}

// cronTableRows parses the cron-expression table lines: each row is
// "`value` → `expr`". Returns the keys found and a map to the full rendered
// row text, keyed by value, for exact-row comparison against cronExpr.
func cronTableRows(doc string) (keys []string, rows map[string]string) {
	rows = make(map[string]string)
	cronLine := regexp.MustCompile("`([a-z0-9]+)` → `([^`]+)`")
	for _, m := range cronLine.FindAllStringSubmatch(doc, -1) {
		keys = append(keys, m[1])
		rows[m[1]] = fmt.Sprintf("`%s` → `%s`", m[1], m[2])
	}
	return keys, rows
}

// TestInboxDoc_ListsFullIntervalSet proves plugin/commands/inbox.md carries
// the interval set in exactly its two intended places — the argument-hint
// and the cron table — and that both are set-equal to email.PollIntervals()
// in both directions, and that every cron row's expression is the one a
// real time.ParseDuration of that value would produce. A per-value
// substring check ("5m" inside "15m") cannot distinguish a present value
// from an absent one sharing a suffix, so this uses exact token/row
// parsing and assert.ElementsMatch instead.
func TestInboxDoc_ListsFullIntervalSet(t *testing.T) {
	data, err := os.ReadFile("../../plugin/commands/inbox.md")
	if err != nil {
		t.Fatalf("reading plugin/commands/inbox.md: %v", err)
	}
	doc := string(data)
	want := email.PollIntervals()

	hintValues := argumentHintValues(t, doc)
	assert.ElementsMatch(t, want, hintValues, "argument-hint values must equal email.PollIntervals()")

	cronKeys, cronRows := cronTableRows(doc)
	assert.ElementsMatch(t, want, cronKeys, "cron-table keys must equal email.PollIntervals()")

	for _, interval := range want {
		wantRow := fmt.Sprintf("`%s` → `%s`", interval, cronExpr(t, interval))
		gotRow, ok := cronRows[interval]
		if !ok {
			t.Fatalf("inbox.md cron table missing a row for %q", interval)
		}
		assert.Equal(t, wantRow, gotRow, "cron row for %q", interval)
	}
}

// TestReadmeDoc_ListsFullIntervalSet proves README.md's set_poll_interval
// tool-table row and /inbox slash-command row both name every element of
// email.PollIntervals(), so the README cannot drift from the Go set.
func TestReadmeDoc_ListsFullIntervalSet(t *testing.T) {
	data, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatalf("reading README.md: %v", err)
	}
	doc := string(data)

	var toolRow string
	var slashRows []string
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "| `set_poll_interval` |") {
			toolRow = line
		}
		if strings.HasPrefix(line, "| `/inbox ") {
			slashRows = append(slashRows, line)
		}
	}
	require.NotEmpty(t, toolRow, "README.md set_poll_interval tool-table row not found")
	require.NotEmpty(t, slashRows, "README.md /inbox slash-command rows not found")
	// Several "| `/inbox " rows exist (n, status, the interval example); only
	// one carries the full set. Join them so the assertion targets whichever
	// row actually lists the values, without hardcoding which row that is.
	slashText := strings.Join(slashRows, "\n")

	for _, interval := range email.PollIntervals() {
		assert.Contains(t, toolRow, interval, "README.md set_poll_interval row missing %q", interval)
		assert.Contains(t, slashText, interval, "README.md /inbox rows missing %q", interval)
	}
}
