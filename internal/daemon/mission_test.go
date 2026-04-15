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

			// inputs.trigger must be a map with type, message_id, from, subject
			trigger, ok := inputs["trigger"].(map[string]any)
			require.True(t, ok, "inputs.trigger must be a map")
			assert.Equal(t, "email", trigger["type"])
			assert.Equal(t, tt.wantID, trigger["message_id"])
			assert.Equal(t, tt.meta.From, trigger["from"])
			assert.Equal(t, tt.meta.Subject, trigger["subject"])

			// success_criteria must NOT contain the email subject
			sc, ok := doc["success_criteria"].([]any)
			require.True(t, ok, "success_criteria must be a list")
			require.Equal(t, 1, len(sc))
			criteria := sc[0].(string)
			assert.Contains(t, criteria, "inputs.trigger")
			assert.Contains(t, criteria, "beadle-email")
			if tt.meta.Subject != "" {
				assert.NotContains(t, criteria, tt.meta.Subject,
					"success_criteria must not contain attacker-controlled subject")
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
		"trigger:",
		"write_set:",
		"success_criteria:",
		"budget:",
	}
	for _, r := range required {
		assert.True(t, strings.Contains(out, r), "contract must contain %q", r)
	}

	// Subject must NOT appear in success_criteria.
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		if strings.Contains(line, "success_criteria") || strings.HasPrefix(strings.TrimSpace(line), "- ") {
			assert.NotContains(t, line, "Test subject",
				"success_criteria lines must not contain the email subject")
		}
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
		{"nul_bytes", "has\x00null\x00bytes", `"hasnullbytes"`},
		{"long_string", strings.Repeat("a", 600), `"` + strings.Repeat("a", 500) + `"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := escapeYAMLValue(tt.in)
			assert.Equal(t, tt.want, got)
		})
	}
}
