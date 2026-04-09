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
//
// The leftmost slot is a fixed 3-char prefix: "▶  " on the header row
// and "   " on data rows. Callers that need a per-row prefix (e.g. a
// right-aligned ID) should use formatTableWithPrefixes instead. The two
// functions share budget math — if one changes, check the other.
// See DESIGN.md § DES-018.
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

// formatTableWithPrefixes renders a table where the leftmost slot is a
// per-row prefix string of fixed prefixWidth, instead of the constant
// 3-char ▶ prefix used by formatTable. The header row places ▶ at
// position 1 and pads the remaining (prefixWidth - 1) runes with
// spaces. Data rows substitute prefixes[i] for that block.
//
// The prefix is followed by the standard colSep before the first
// column, matching formatTable. Variable-column budget is computed
// against (tableWidth - prefixWidth - fixedTotal - sepTotal), where
// sepTotal includes every colSep emitted on the row, including the
// separator between the prefix block and the first data column.
//
// Caller invariant: each entry in prefixes must already be exactly
// prefixWidth runes wide AND len(prefixes) must equal len(rows). The
// function panics on a length mismatch (programmer bug per beadle
// CLAUDE.md "Panics are for programmer bugs only"); it does not pad
// or extend prefixes.
//
// Budget math is parallel to formatTable; both functions enforce the
// same 80-char tableWidth. See DESIGN.md § DES-018.
func formatTableWithPrefixes(
	cols []column,
	rows [][]string,
	prefixes []string,
	prefixWidth int,
) string {
	if len(rows) == 0 {
		return ""
	}
	if len(prefixes) != len(rows) {
		panic(fmt.Sprintf("formatTableWithPrefixes: len(prefixes)=%d != len(rows)=%d", len(prefixes), len(rows)))
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

	// Constrain variable column to the remaining budget.
	varIdx := -1
	for i, c := range cols {
		if c.variable {
			varIdx = i
			break
		}
	}
	if varIdx >= 0 {
		sepTotal := len(colSep) * n // prefix + n-1 inter-column separators
		fixedTotal := 0
		for i, w := range widths {
			if i != varIdx {
				fixedTotal += w
			}
		}
		budget := tableWidth - prefixWidth - fixedTotal - sepTotal
		if budget < cols[varIdx].minWidth {
			budget = cols[varIdx].minWidth
		}
		// Pin the variable column to the full budget so every rendered
		// row is exactly tableWidth runes wide. DES-018 requires this
		// invariant for list_messages.
		widths[varIdx] = budget
	}

	// Header row: ▶ at position 1, spaces for the rest of the prefix.
	var b strings.Builder
	b.WriteString("\u25b6")
	if prefixWidth > 1 {
		b.WriteString(strings.Repeat(" ", prefixWidth-1))
	}
	b.WriteString(colSep)
	for i, c := range cols {
		if i > 0 {
			b.WriteString(colSep)
		}
		b.WriteString(pad(c.header, widths[i]))
	}

	// Data rows.
	for idx, row := range rows {
		b.WriteByte('\n')
		b.WriteString(prefixes[idx])
		b.WriteString(colSep)
		for i := 0; i < n; i++ {
			if i > 0 {
				b.WriteString(colSep)
			}
			cell := ""
			if i < len(row) {
				cell = row[i]
			}
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
	// Find max key width (including colon).
	maxKey := 0
	for _, p := range pairs {
		w := len(p[0]) + 1 // +1 for colon
		if w > maxKey {
			maxKey = w
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
