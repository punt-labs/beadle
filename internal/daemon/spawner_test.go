package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseWorkerOutput_ValidJSON(t *testing.T) {
	payload := workerJSON{
		Result:    "Mission complete",
		SessionID: "sess-abc-123",
		IsError:   false,
	}
	jsonBytes, err := json.Marshal(payload)
	require.NoError(t, err)

	result, err := parseWorkerOutput("m-test-001", jsonBytes, 0)
	require.NoError(t, err)

	assert.Equal(t, "m-test-001", result.MissionID)
	assert.Equal(t, "Mission complete", result.Output)
	assert.Equal(t, "sess-abc-123", result.SessionID)
	assert.False(t, result.IsError)
	assert.Equal(t, 0, result.ExitCode)
}

func TestParseWorkerOutput_ErrorJSON(t *testing.T) {
	payload := workerJSON{
		Result:    "something went wrong",
		SessionID: "sess-err-456",
		IsError:   true,
	}
	jsonBytes, err := json.Marshal(payload)
	require.NoError(t, err)

	result, err := parseWorkerOutput("m-test-002", jsonBytes, 1)
	require.NoError(t, err)

	assert.Equal(t, "m-test-002", result.MissionID)
	assert.Equal(t, "something went wrong", result.Output)
	assert.True(t, result.IsError)
	assert.Equal(t, 1, result.ExitCode)
}

func TestParseWorkerOutput_InvalidJSON(t *testing.T) {
	_, err := parseWorkerOutput("m-test-003", []byte("not json"), 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse worker output")
}

func TestParseWorkerOutput_EmptyOutput(t *testing.T) {
	result, err := parseWorkerOutput("m-test-004", []byte(""), 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty output")
	assert.True(t, result.IsError)
}

// writeFakeClaudeBinary writes a shell script named "claude" that emits a
// well-formed workerJSON payload to stdout and exits 0, and returns the
// directory it lives in. WorkerSpawner.Run finds it via exec.LookPath,
// which resolves against the process's own PATH -- so the caller must also
// t.Setenv("PATH", dir) (or prepend dir to the real PATH) for it to be
// found.
func writeFakeClaudeBinary(t *testing.T) (dir string) {
	t.Helper()
	dir = t.TempDir()
	script := "#!/bin/sh\n" + `echo '{"result":"ok","session_id":"s1","is_error":false}'` + "\n"
	path := filepath.Join(dir, "claude")
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
	return dir
}

// recordsText concatenates every captured record's message and attr values
// into one string a test can substring-search, so "this secret never
// appears anywhere in the log" is one assertion instead of an
// attr-by-attr enumeration that would need updating every time a new log
// field is added.
func (h *capturingHandler) recordsText() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var sb strings.Builder
	for _, r := range h.records {
		sb.WriteString(r.Message)
		sb.WriteString(" ")
		r.Attrs(func(a slog.Attr) bool {
			fmt.Fprintf(&sb, "%s=%v ", a.Key, a.Value.Any())
			return true
		})
	}
	return sb.String()
}

// TestWorkerSpawner_Run_EnvOverrideValueNeverLogged is the regression test
// for the mission's hard constraint on context7's CONTEXT7_API_KEY: any
// envOverrides value passed to Run -- the same mechanism ClaudeRunner uses
// to thread a declared env_vars entry into the worker subprocess -- must
// reach that subprocess's environment without ever being written to a log
// line, in this file or any other call site Run reaches. Run's own log
// calls list only envOverrides' *keys* ("envKeys"); this test proves the
// *value* genuinely never appears anywhere in the captured output, not
// just at the one call site a human reading the code happened to check.
func TestWorkerSpawner_Run_EnvOverrideValueNeverLogged(t *testing.T) {
	dir := writeFakeClaudeBinary(t)
	t.Setenv("PATH", dir)

	h := &capturingHandler{}
	logger := slog.New(h)
	s := &WorkerSpawner{APIKey: "test-api-key", Logger: logger}

	const secretValue = "sekrit-context7-value-do-not-log-me"
	tmpDir := t.TempDir()
	mcpPath := filepath.Join(tmpDir, "mcp.json")
	require.NoError(t, os.WriteFile(mcpPath, []byte(`{"mcpServers":{}}`), 0o600))
	promptPath := filepath.Join(tmpDir, "prompt.txt")
	require.NoError(t, os.WriteFile(promptPath, []byte("prompt"), 0o600))

	result, err := s.Run(context.Background(), "m-test-005", mcpPath, promptPath,
		map[string]string{"CONTEXT7_API_KEY": secretValue})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	assert.NotContains(t, h.recordsText(), secretValue,
		"an envOverrides value must never appear in a log line")
	assert.Contains(t, h.recordsText(), "CONTEXT7_API_KEY",
		"the env var NAME is fine to log -- only the value is sensitive")
}

func TestValidMissionID(t *testing.T) {
	tests := []struct {
		name  string
		id    string
		valid bool
	}{
		{"canonical", "m-2026-04-14-001", true},
		{"short", "m-test-001", true},
		{"single_digit", "m-1", true},
		{"no_m_prefix", "abc123", false},
		{"empty", "", false},
		{"spaces", "m-has spaces", false},
		{"newline", "m-has\nnewline", false},
		{"null_byte", "m-has\x00null", false},
		{"uppercase", "M-TEST-001", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.valid, ValidMissionID(tt.id))
		})
	}
}
