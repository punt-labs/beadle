package daemon

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompileSchema(t *testing.T) {
	tests := []struct {
		name    string
		schema  any
		wantNil bool
		wantErr string
	}{
		{
			name:    "text returns nil schema",
			schema:  "text",
			wantNil: true,
		},
		{
			name:    "invalid string",
			schema:  "json",
			wantErr: "unexpected output_schema string",
		},
		{
			name: "valid map compiles",
			schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title": map[string]any{"type": "string"},
				},
			},
			wantNil: false,
		},
		{
			name:    "invalid type",
			schema:  42,
			wantErr: "unexpected type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := CompileSchema(tt.schema)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			if tt.wantNil {
				assert.Nil(t, s)
			} else {
				assert.NotNil(t, s)
			}
		})
	}
}

func TestValidateOutput(t *testing.T) {
	schema, err := CompileSchema(map[string]any{
		"type":     "object",
		"required": []any{"title"},
		"properties": map[string]any{
			"title":   map[string]any{"type": "string"},
			"summary": map[string]any{"type": "string"},
		},
	})
	require.NoError(t, err)

	tests := []struct {
		name    string
		output  string
		wantErr string
	}{
		{
			name:   "valid JSON matching schema",
			output: `{"title":"hello","summary":"world"}`,
		},
		{
			name:    "missing required field",
			output:  `{"summary":"world"}`,
			wantErr: "does not match schema",
		},
		{
			name:    "invalid JSON",
			output:  `not json at all`,
			wantErr: "not valid JSON",
		},
		{
			name:   "extra fields allowed",
			output: `{"title":"hello","extra":"ok"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateOutput(schema, tt.output)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateOutput_NilSchema(t *testing.T) {
	err := ValidateOutput(nil, "anything goes here")
	assert.NoError(t, err)
}
