package mcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSplitAddresses(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"single", "a@b.com", []string{"a@b.com"}},
		{"two comma-separated", "a@b.com,c@d.com", []string{"a@b.com", "c@d.com"}},
		{"whitespace around commas", " a@b.com , c@d.com , e@f.com ", []string{"a@b.com", "c@d.com", "e@f.com"}},
		{"trailing comma", "a@b.com,", []string{"a@b.com"}},
		{"only commas", ",,", nil},
		{"spaces only", "  ", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := splitAddresses(tc.in)
			assert.Equal(t, tc.want, got)
		})
	}
}
