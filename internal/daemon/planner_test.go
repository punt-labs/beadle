package daemon

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRulePlanner(t *testing.T) {
	deployCmd := CommandCall{Command: "deploy", Args: map[string]any{"env": "prod"}}
	statusCmd := CommandCall{Command: "status", Args: nil}

	tests := []struct {
		name    string
		rules   []RuleEntry
		meta    EmailMeta
		body    string
		want    []CommandCall
		wantErr string
	}{
		{
			name: "match in subject",
			rules: []RuleEntry{
				{Pattern: `(?i)deploy`, Commands: []CommandCall{deployCmd}},
			},
			meta: EmailMeta{Subject: "Please deploy now"},
			body: "no keywords here",
			want: []CommandCall{deployCmd},
		},
		{
			name: "match in body",
			rules: []RuleEntry{
				{Pattern: `(?i)deploy`, Commands: []CommandCall{deployCmd}},
			},
			meta: EmailMeta{Subject: "Action needed"},
			body: "please deploy to production",
			want: []CommandCall{deployCmd},
		},
		{
			name: "first match wins",
			rules: []RuleEntry{
				{Pattern: `deploy`, Commands: []CommandCall{deployCmd}},
				{Pattern: `deploy|status`, Commands: []CommandCall{statusCmd}},
			},
			meta: EmailMeta{Subject: "deploy"},
			body: "",
			want: []CommandCall{deployCmd},
		},
		{
			name: "no match returns error",
			rules: []RuleEntry{
				{Pattern: `deploy`, Commands: []CommandCall{deployCmd}},
			},
			meta:    EmailMeta{Subject: "hello"},
			body:    "world",
			wantErr: "no rule matches",
		},
		{
			name:    "empty rules returns error",
			rules:   []RuleEntry{},
			meta:    EmailMeta{Subject: "anything"},
			body:    "",
			wantErr: "no rule matches",
		},
		{
			name: "multiple commands in one rule",
			rules: []RuleEntry{
				{Pattern: `pipeline`, Commands: []CommandCall{statusCmd, deployCmd}},
			},
			meta: EmailMeta{Subject: "run pipeline"},
			body: "",
			want: []CommandCall{statusCmd, deployCmd},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := NewRulePlanner(tt.rules)
			require.NoError(t, err)

			got, err := p.Plan(context.Background(), tt.meta, tt.body)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNewRulePlanner_InvalidRegex(t *testing.T) {
	_, err := NewRulePlanner([]RuleEntry{
		{Pattern: `[invalid`, Commands: nil},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "compile rule 0")
}

func TestStubPlanner(t *testing.T) {
	cmds := []CommandCall{{Command: "test", Args: map[string]any{"k": "v"}}}
	p := &StubPlanner{Result: cmds, Err: nil}

	got, err := p.Plan(context.Background(), EmailMeta{}, "")
	require.NoError(t, err)
	assert.Equal(t, cmds, got)
}

func TestStubPlanner_Error(t *testing.T) {
	p := &StubPlanner{Err: assert.AnError}

	_, err := p.Plan(context.Background(), EmailMeta{}, "")
	require.ErrorIs(t, err, assert.AnError)
}

func TestLLMPlanner_NotImplemented(t *testing.T) {
	p := &LLMPlanner{}
	_, err := p.Plan(context.Background(), EmailMeta{}, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not yet implemented")
}
