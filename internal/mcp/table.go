package mcp

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Table layout constants matching biff's format_table pattern.
// ▶ prefix on header, 3-space prefix on data rows, 80-col budget.
const (
	tableWidth   = 80
	colSep       = "  "
	headerPrefix = "\u25b6  " // ▶ + 2 spaces
	rowPrefix    = "   "      // 3 spaces (same width as headerPrefix)
	prefixLen    = 3
)

// column describes one column in a table.
type column struct {
	header   string
	minWidth int
	// If variable is true, this column gets the remaining width budget
	// and its content is truncated when it exceeds that budget.
	variable bool
}

// formatTable renders rows as a fixed-width table with header.
// At most one column should have variable=true.
func formatTable(cols []column, rows [][]string) string {
	if len(rows) == 0 {
		return ""
	}
	n := len(cols)

	// Measure content widths.
	widths := make([]int, n)
	for i, c := range cols {
		widths[i] = max(c.minWidth, utf8.RuneCountInString(c.header))
	}
	for _, row := range rows {
		for i := 0; i < n && i < len(row); i++ {
			w := utf8.RuneCountInString(row[i])
			if w > widths[i] {
				widths[i] = w
			}
		}
	}

	// Find variable column and constrain it to remaining budget.
	varIdx := -1
	for i, c := range cols {
		if c.variable {
			varIdx = i
			break
		}
	}
	if varIdx >= 0 {
		sepTotal := len(colSep) * (n - 1)
		fixedTotal := 0
		for i, w := range widths {
			if i != varIdx {
				fixedTotal += w
			}
		}
		budget := tableWidth - prefixLen - fixedTotal - sepTotal
		if budget < cols[varIdx].minWidth {
			budget = cols[varIdx].minWidth
		}
		if widths[varIdx] > budget {
			widths[varIdx] = budget
		}
	}

	// Render header.
	var b strings.Builder
	b.WriteString(headerPrefix)
	for i, c := range cols {
		if i > 0 {
			b.WriteString(colSep)
		}
		b.WriteString(pad(c.header, widths[i]))
	}

	// Render rows.
	for _, row := range rows {
		b.WriteByte('\n')
		b.WriteString(rowPrefix)
		for i := 0; i < n; i++ {
			if i > 0 {
				b.WriteString(colSep)
			}
			cell := ""
			if i < len(row) {
				cell = row[i]
			}
			// Truncate variable column content.
			if i == varIdx {
				cell = truncateRunes(cell, widths[i])
			}
			b.WriteString(pad(cell, widths[i]))
		}
	}
	return b.String()
}

// pad right-pads s with spaces to width w (rune-aware).
func pad(s string, w int) string {
	n := utf8.RuneCountInString(s)
	if n >= w {
		return s
	}
	return s + strings.Repeat(" ", w-n)
}

// truncateRunes truncates s to max runes, adding … if truncated.
func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes-1]) + "…"
}

// fmtKV formats a key-value header block (From, To, Subject, etc.)
// with aligned keys. Uses the same 3-space prefix as table rows.
func fmtKV(pairs [][2]string) string {
	// Find max key width.
	maxKey := 0
	for _, p := range pairs {
		if len(p[0]) > maxKey {
			maxKey = len(p[0])
		}
	}
	var b strings.Builder
	for i, p := range pairs {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(rowPrefix)
		fmt.Fprintf(&b, "%-*s  %s", maxKey, p[0]+":", p[1])
	}
	return b.String()
}
