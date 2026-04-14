package daemon

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestBuildContract(t *testing.T) {
	tests := []struct {
		name    string
		meta    EmailMeta
		wantID  string
		wantSub string
	}{
		{
			name: "basic email",
			meta: EmailMeta{
				MessageID: "1205",
				From:      "jim@punt-labs.com",
				Subject:   "Schedule a team meeting",
			},
			wantID:  "1205",
			wantSub: "Schedule a team meeting",
		},
		{
			name: "special characters in subject",
			meta: EmailMeta{
				MessageID: "42",
				From:      "alice@example.com",
				Subject:   "Re: [beadle] PR #123: fix bug",
			},
			wantID:  "42",
			wantSub: "Re: [beadle] PR #123: fix bug",
		},
		{
			name: "empty subject",
			meta: EmailMeta{
				MessageID: "99",
				From:      "bob@example.com",
				Subject:   "",
			},
			wantID:  "99",
			wantSub: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := BuildContract(tt.meta)

			// Parse as YAML to verify structure.
			var doc map[string]any
			require.NoError(t, yaml.Unmarshal([]byte(out), &doc))

			assert.Equal(t, "beadle-daemon", doc["leader"])
			assert.Equal(t, "claude-session", doc["worker"])

			eval, ok := doc["evaluator"].(map[string]any)
			require.True(t, ok, "evaluator must be a map")
			assert.Equal(t, "beadle-daemon", eval["handle"])

			inputs, ok := doc["inputs"].(map[string]any)
			require.True(t, ok, "inputs must be a map")
			trigger, ok := inputs["trigger"].(map[string]any)
			require.True(t, ok, "inputs.trigger must be a map")
			assert.Equal(t, "email", trigger["type"])
			assert.Equal(t, tt.wantID, trigger["message_id"])
			assert.Equal(t, tt.meta.From, trigger["from"])
			assert.Equal(t, tt.wantSub, trigger["subject"])

			ws, ok := doc["write_set"].([]any)
			require.True(t, ok, "write_set must be a list")
			assert.Equal(t, 1, len(ws))

			budget, ok := doc["budget"].(map[string]any)
			require.True(t, ok, "budget must be a map")
			assert.Equal(t, 1, budget["rounds"])
			assert.Equal(t, false, budget["reflection_after_each"])
		})
	}
}

func TestBuildContract_ContainsRequiredFields(t *testing.T) {
	meta := EmailMeta{
		MessageID: "100",
		From:      "test@example.com",
		Subject:   "Test subject",
	}
	out := BuildContract(meta)

	required := []string{
		"leader: beadle-daemon",
		"worker: claude-session",
		"type: email",
		"message_id:",
		"write_set:",
		"success_criteria:",
		"budget:",
	}
	for _, r := range required {
		assert.True(t, strings.Contains(out, r), "contract must contain %q", r)
	}
}

func TestEscapeYAMLValue(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"simple text", `"simple text"`},
		{"", `""`},
		{"has: colon", `"has: colon"`},
		{"has #comment", `"has #comment"`},
		{`has "quotes"`, `"has \"quotes\""`},
		{"has\nnewline", `"has\nnewline"`},
		{"has\rcarriage", `"has\rcarriage"`},
		{"has\ttab", `"has\ttab"`},
		{`has\backslash`, `"has\\backslash"`},
		{"99", `"99"`},
		{"true", `"true"`},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got := escapeYAMLValue(tt.in)
			assert.Equal(t, tt.want, got)
		})
	}
}
