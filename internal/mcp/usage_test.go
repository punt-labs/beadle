package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/punt-labs/beadle/internal/usage"
)

func TestUsageMiddlewareOutcomes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PUNT_TELEMETRY", "")
	t.Setenv("BEADLE_TELEMETRY", "")
	client := usage.New(nil)
	t.Cleanup(client.Shutdown)
	h := &handler{usage: client}
	for _, tc := range []struct {
		name   string
		result *mcplib.CallToolResult
		err    error
		denied bool
	}{
		{name: "ok", result: &mcplib.CallToolResult{}},
		{name: "error", result: mcplib.NewToolResultError("private result")},
		{name: "error", err: errors.New("private error")},
		{name: "error"},
		{name: "denied", result: mcplib.NewToolResultError("private sender"), denied: true},
	} {
		next := h.observeUsage(func(ctx context.Context, _ mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
			if tc.denied {
				usage.PermissionDenied(ctx)
			}
			return tc.result, tc.err
		})
		request := makeRequest(t, nil)
		request.Params.Name = "read_message"
		result, err := next(t.Context(), request)
		assert.Same(t, tc.result, result)
		assert.Equal(t, tc.err, err)
	}
	client.Shutdown()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(home, ".punt-labs", "beadle", "telemetry.db")+"?mode=ro")
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()
	rows, err := db.QueryContext(t.Context(), "SELECT attributes FROM signals WHERE name='beadle.mcp.tool' ORDER BY start_ns")
	require.NoError(t, err)
	defer func() { require.NoError(t, rows.Close()) }()
	var outcomes []string
	for rows.Next() {
		var data string
		require.NoError(t, rows.Scan(&data))
		var attrs struct {
			Tool    string
			Outcome string
		}
		require.NoError(t, json.Unmarshal([]byte(data), &attrs))
		assert.Equal(t, "read_message", attrs.Tool)
		assert.NotContains(t, data, "private")
		outcomes = append(outcomes, attrs.Outcome)
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, []string{"ok", "error", "error", "error", "denied"}, outcomes)
	_, err = os.Stat(filepath.Join(home, ".punt-labs", "beadle", "telemetry.db"))
	require.NoError(t, err)
}
