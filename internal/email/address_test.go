package email

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractEmailAddress(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"bare email", "user@example.com", "user@example.com"},
		{"RFC 5322", "Jim Freeman <jim@punt-labs.com>", "jim@punt-labs.com"},
		{"quoted display name", `"Jim Freeman" <jim@punt-labs.com>`, "jim@punt-labs.com"},
		{"bot brackets", "github-actions[bot] <notifications@github.com>", "notifications@github.com"},
		{"empty", "", ""},
		{"whitespace only", "   ", ""},
		{"no at sign", "not-an-email", ""},
		{"angle brackets only", "<jim@punt-labs.com>", "jim@punt-labs.com"},
		{"spaces around", "  user@example.com  ", "user@example.com"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractEmailAddress(tc.input)
			assert.Equal(t, tc.want, got)
		})
	}
}
