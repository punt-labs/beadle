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
	_, err := parseWorkerOutput("m-test-004", []byte(""), 0)
	require.Error(t, err)
}

func TestValidMissionID(t *testing.T) {
	tests := []struct {
		id    string
		valid bool
	}{
		{"m-2026-04-14-001", true},
		{"m-test-001", true},
		{"abc123", true},
		{"", false},
		{"has spaces", false},
		{"has\nnewline", false},
		{"has\x00null", false},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			assert.Equal(t, tt.valid, validMissionIDRe.MatchString(tt.id))
		})
	}
}
