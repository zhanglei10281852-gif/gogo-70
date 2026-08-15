package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"CableMend/internal/numeric"
)

// textWriter renders deterministic plain-text reports. Column widths derive only
// from the content, and floats always use the configured decimal count, so the
// same input always produces byte-identical output.
type textWriter struct {
	w        io.Writer
	err      error
	decimals int
}

func newText(w io.Writer, decimals int) *textWriter {
	return &textWriter{w: w, decimals: decimals}
}

// line writes one formatted line.
func (t *textWriter) line(format string, args ...any) {
	if t.err != nil {
		return
	}
	_, t.err = fmt.Fprintf(t.w, format+"\n", args...)
}

// blank writes an empty line.
func (t *textWriter) blank() { t.line("") }

// heading writes a section heading underlined with dashes.
func (t *textWriter) heading(title string) {
	t.line("%s", title)
	t.line("%s", strings.Repeat("-", len(title)))
}

// kv writes an aligned key and value.
func (t *textWriter) kv(key, value string) {
	t.line("  %-26s %s", key+":", value)
}

// num writes an aligned key and float value.
func (t *textWriter) num(key string, value float64) {
	t.kv(key, t.f(value))
}

// count writes an aligned key and integer value.
func (t *textWriter) count(key string, value int) {
	t.kv(key, strconv.Itoa(value))
}

// yesNo writes an aligned key and boolean value.
func (t *textWriter) yesNo(key string, value bool) {
	if value {
		t.kv(key, "yes")
		return
	}
	t.kv(key, "no")
}

// list writes a bulleted list, or a dash when empty.
func (t *textWriter) list(title string, items []string) {
	if len(items) == 0 {
		t.kv(title, "-")
		return
	}
	t.kv(title, items[0])
	for _, item := range items[1:] {
		t.line("  %-26s %s", "", item)
	}
}

// table writes a left-aligned table with a header rule.
func (t *textWriter) table(headers []string, rows [][]string) {
	if len(headers) == 0 {
		return
	}
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, row := range rows {
		for i := range headers {
			if i < len(row) && len(row[i]) > widths[i] {
				widths[i] = len(row[i])
			}
		}
	}
	t.line("  %s", joinCells(headers, widths))
	rule := make([]string, len(headers))
	for i := range headers {
		rule[i] = strings.Repeat("-", widths[i])
	}
	t.line("  %s", joinCells(rule, widths))
	for _, row := range rows {
		cells := make([]string, len(headers))
		for i := range headers {
			if i < len(row) {
				cells[i] = row[i]
			}
		}
		t.line("  %s", joinCells(cells, widths))
	}
	if len(rows) == 0 {
		t.line("  (no rows)")
	}
}

func joinCells(cells []string, widths []int) string {
	parts := make([]string, len(cells))
	for i, c := range cells {
		parts[i] = padRight(c, widths[i])
	}
	return strings.TrimRight(strings.Join(parts, "  "), " ")
}

func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

// f formats a float with the configured precision.
func (t *textWriter) f(v float64) string { return numeric.Format(v, t.decimals) }

// fixed formats a float with an explicit precision.
func (t *textWriter) fixed(v float64, decimals int) string { return numeric.Format(v, decimals) }

// done returns the first write error, if any.
func (t *textWriter) done() error { return t.err }

// joinOrDash renders a string slice, or a dash when empty.
func joinOrDash(values []string) string {
	if len(values) == 0 {
		return "-"
	}
	return strings.Join(values, ", ")
}

// boolWord renders a boolean as a fixed word.
func boolWord(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}
