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
		name   string
		meta   EmailMeta
		wantID string
	}{
		{
			name: "basic email",
			meta: EmailMeta{
				MessageID: "1205",
				From:      "jim@punt-labs.com",
				Subject:   "Schedule a team meeting",
			},
			wantID: "1205",
		},
		{
			name: "special characters in subject",
			meta: EmailMeta{
				MessageID: "42",
				From:      "alice@example.com",
				Subject:   "Re: [beadle] PR #123: fix bug",
			},
			wantID: "42",
		},
		{
			name: "empty subject",
			meta: EmailMeta{
				MessageID: "99",
				From:      "bob@example.com",
				Subject:   "",
			},
			wantID: "99",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := BuildContract(tt.meta)

			// Parse as YAML to verify structure.
			var doc map[string]any
			require.NoError(t, yaml.Unmarshal([]byte(out), &doc))

			assert.Equal(t, "claude", doc["leader"])
			assert.Equal(t, "bwk", doc["worker"])

			eval, ok := doc["evaluator"].(map[string]any)
			require.True(t, ok, "evaluator must be a map")
			assert.Equal(t, "mdm", eval["handle"])

			inputs, ok := doc["inputs"].(map[string]any)
			require.True(t, ok, "inputs must be a map")
			ticket, ok := inputs["ticket"].(string)
			require.True(t, ok, "inputs.ticket must be a string")
			assert.Contains(t, ticket, "email:"+tt.wantID)
			assert.Contains(t, ticket, tt.meta.From)

			sc, ok := doc["success_criteria"].([]any)
			require.True(t, ok, "success_criteria must be a list")
			if tt.meta.Subject != "" {
				require.Greater(t, len(sc), 0)
				assert.Contains(t, sc[0], tt.meta.Subject)
			}

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
		"leader: claude",
		"worker: bwk",
		"ticket:",
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
		name string
		in   string
		want string
	}{
		{"simple_text", "simple text", `"simple text"`},
		{"empty", "", `""`},
		{"colon", "has: colon", `"has: colon"`},
		{"comment", "has #comment", `"has #comment"`},
		{"quotes", `has "quotes"`, `"has \"quotes\""`},
		{"newline", "has\nnewline", `"has\nnewline"`},
		{"carriage_return", "has\rcarriage", `"has\rcarriage"`},
		{"tab", "has\ttab", `"has\ttab"`},
		{"backslash", `has\backslash`, `"has\\backslash"`},
		{"numeric", "99", `"99"`},
		{"boolean", "true", `"true"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := escapeYAMLValue(tt.in)
			assert.Equal(t, tt.want, got)
		})
	}
}
