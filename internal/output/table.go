package output

import (
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

// Table is a headers-plus-rows result set.
type Table struct {
	Headers []string
	Rows    [][]string
	// Empty is the message shown when there are no rows (stderr, so an empty
	// stdout stays an empty stdout for scripts).
	Empty string
	// IDColumn is the column index printed by `-o name`. Defaults to 0.
	IDColumn int
}

// NewTable starts a table with the given headers.
func NewTable(headers ...string) *Table {
	return &Table{Headers: headers, Rows: [][]string{}}
}

// Add appends a row. Cells are converted with Cell.
func (t *Table) Add(cells ...any) {
	row := make([]string, 0, len(cells))
	for _, c := range cells {
		row = append(row, Cell(c))
	}

	t.Rows = append(t.Rows, row)
}

func (p *Printer) writeTable(t *Table) error {
	if t == nil {
		return nil
	}

	if p.Format == FormatName {
		for _, row := range t.Rows {
			if t.IDColumn < len(row) {
				fmt.Fprintln(p.Out, row[t.IDColumn])
			}
		}

		return nil
	}

	if len(t.Rows) == 0 {
		msg := t.Empty
		if msg == "" {
			msg = "No resources found."
		}

		fmt.Fprintln(p.Err, msg)

		return nil
	}

	w := tabwriter.NewWriter(p.Out, 0, 0, 3, ' ', 0)

	if !p.NoHeaders && len(t.Headers) > 0 {
		fmt.Fprintln(w, strings.Join(t.Headers, "\t"))
	}

	for _, row := range t.Rows {
		fmt.Fprintln(w, strings.Join(row, "\t"))
	}

	return w.Flush()
}

// Cell renders a value for a table cell, keeping empty values as a visible
// dash rather than a blank that shifts columns.
func Cell(v any) string {
	switch val := v.(type) {
	case nil:
		return "-"
	case string:
		if val == "" {
			return "-"
		}

		return oneLine(val)
	case bool:
		return strconv.FormatBool(val)
	case *bool:
		if val == nil {
			return "-"
		}

		return strconv.FormatBool(*val)
	case time.Time:
		return Age(val)
	case *time.Time:
		if val == nil || val.IsZero() {
			return "-"
		}

		return Age(*val)
	case []string:
		if len(val) == 0 {
			return "-"
		}

		return strings.Join(val, ",")
	case int:
		return strconv.Itoa(val)
	case int64:
		return strconv.FormatInt(val, 10)
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64)
	default:
		return oneLine(fmt.Sprint(val))
	}
}

// Truncate shortens a cell to n runes, marking the cut with an ellipsis.
func Truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}

	if n <= 1 {
		return "…"
	}

	return string(r[:n-1]) + "…"
}

// Age renders a timestamp the way kubectl does: a compact relative duration.
func Age(t time.Time) string {
	if t.IsZero() {
		return "-"
	}

	d := time.Since(t)
	if d < 0 {
		d = -d
	}

	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	default:
		return fmt.Sprintf("%dy", int(d.Hours()/24/365))
	}
}

// Bytes renders a byte count in SI units, matching what the web app shows.
func Bytes(n int64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}

	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}

	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "kMGTPE"[exp])
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")

	return strings.TrimSpace(s)
}
