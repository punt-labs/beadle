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
		{"RFC 5322", "Sam Jackson <sam@example.com>", "sam@example.com"},
		{"quoted display name", `"Sam Jackson" <sam@example.com>`, "sam@example.com"},
		{"bot brackets", "github-actions[bot] <notifications@github.com>", "notifications@github.com"},
		{"empty", "", ""},
		{"whitespace only", "   ", ""},
		{"no at sign", "not-an-email", ""},
		{"angle brackets only", "<sam@example.com>", "sam@example.com"},
		{"spaces around", "  user@example.com  ", "user@example.com"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractEmailAddress(tc.input)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestExtractDisplayName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"RFC 5322", "Sam Jackson <sam@example.com>", "Sam Jackson"},
		{"quoted", `"Sam Jackson" <sam@example.com>`, "Sam Jackson"},
		{"bare email", "user@example.com", "user@example.com"},
		{"bot brackets", "github-actions[bot] <notifications@github.com>", "github-actions[bot]"},
		{"empty", "", ""},
		{"angle only", "<sam@example.com>", "sam@example.com"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractDisplayName(tc.input)
			assert.Equal(t, tc.want, got)
		})
	}
}
