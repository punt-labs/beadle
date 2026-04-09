package mcp

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFormatTable_Basic(t *testing.T) {
	cols := []column{
		{header: "NAME", minWidth: 4},
		{header: "EMAIL", minWidth: 5, variable: true},
		{header: "PERM", minWidth: 3},
	}
	rows := [][]string{
		{"Sam", "sam@example.com", "rwx"},
		{"Vendor", "vendor@example.com", "r--"},
	}
	got := formatTable(cols, rows)

	// Header starts with ▶
	assert.Contains(t, got, "\u25b6")
	assert.Contains(t, got, "NAME")
	assert.Contains(t, got, "EMAIL")
	assert.Contains(t, got, "PERM")

	// Rows start with 3-space prefix
	lines := splitLines(got)
	assert.True(t, len(lines) >= 3, "expected header + 2 rows")
	for _, line := range lines[1:] {
		assert.True(t, len(line) >= 3 && line[:3] == "   ",
			"data row should start with 3-space prefix: %q", line)
	}

	// Content present
	assert.Contains(t, got, "Sam")
	assert.Contains(t, got, "sam@example.com")
	assert.Contains(t, got, "rwx")
}

func TestFormatTable_Empty(t *testing.T) {
	cols := []column{{header: "X", minWidth: 1}}
	got := formatTable(cols, nil)
	assert.Equal(t, "", got)
}

func TestFormatTable_Truncation(t *testing.T) {
	cols := []column{
		{header: "A", minWidth: 2},
		{header: "B", minWidth: 5, variable: true},
	}
	rows := [][]string{
		{"OK", "This is a very long string that should be truncated to fit the column width budget"},
	}
	got := formatTable(cols, rows)
	// Should contain truncation marker
	assert.Contains(t, got, "…")
}

func TestTruncateRunes(t *testing.T) {
	assert.Equal(t, "abc", truncateRunes("abc", 5))
	assert.Equal(t, "ab…", truncateRunes("abcde", 3))
	assert.Equal(t, "", truncateRunes("abc", 0))
	assert.Equal(t, "abc", truncateRunes("abc", 3))
}

func TestPad(t *testing.T) {
	assert.Equal(t, "hi   ", pad("hi", 5))
	assert.Equal(t, "hello", pad("hello", 5))
	assert.Equal(t, "toolong", pad("toolong", 3))
}

func TestFmtKV(t *testing.T) {
	got := fmtKV([][2]string{
		{"From", "sam@example.com"},
		{"To", "claude@punt-labs.com"},
	})
	assert.Contains(t, got, "From:")
	assert.Contains(t, got, "To:")
	// All lines start with 3-space prefix
	for _, line := range splitLines(got) {
		assert.True(t, len(line) >= 3 && line[:3] == "   ",
			"KV line should start with 3-space prefix: %q", line)
	}
}

func splitLines(s string) []string {
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
