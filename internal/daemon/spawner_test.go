package daemon

import (
	"encoding/json"
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
