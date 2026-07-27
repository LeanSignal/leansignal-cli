// Package output renders command results.
//
// Three rules keep the output scriptable:
//
//   - `-o json` prints the server's own bytes, re-indented but never reshaped.
//   - tables are space-padded plain text, never box-drawing — `awk` and `cut`
//     must keep working.
//   - colour is off whenever stdout is not a terminal, or NO_COLOR is set.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Format is an output encoding.
type Format string

// Supported output formats.
const (
	FormatTable Format = "table"
	FormatWide  Format = "wide"
	FormatJSON  Format = "json"
	FormatYAML  Format = "yaml"
	FormatName  Format = "name" // ids only, one per line — for xargs
)

// Formats lists every accepted -o value, for help text and completion.
var Formats = []string{
	string(FormatTable), string(FormatWide), string(FormatJSON),
	string(FormatYAML), string(FormatName),
}

// ParseFormat validates a -o value.
func ParseFormat(s string) (Format, error) {
	f := Format(strings.ToLower(strings.TrimSpace(s)))
	switch f {
	case FormatTable, FormatWide, FormatJSON, FormatYAML, FormatName:
		return f, nil
	default:
		return "", fmt.Errorf("invalid output format %q: expected one of %s", s, strings.Join(Formats, ", "))
	}
}

// Printer writes command results in the selected format.
type Printer struct {
	Format    Format
	Out       io.Writer
	Err       io.Writer
	NoHeaders bool
	Color     bool
}

// New builds a Printer for the given format.
func New(format Format, noHeaders, color bool) *Printer {
	return &Printer{Format: format, Out: os.Stdout, Err: os.Stderr, NoHeaders: noHeaders, Color: color}
}

// Wide reports whether extra columns should be rendered.
func (p *Printer) Wide() bool { return p.Format == FormatWide }

// Structured reports whether the caller can skip building a table because the
// raw document will be printed as-is.
func (p *Printer) Structured() bool { return p.Format == FormatJSON || p.Format == FormatYAML }

// Emit renders a result. raw is the server's response body; build is called
// only for the human-readable formats.
func (p *Printer) Emit(raw []byte, build func() (*Table, error)) error {
	switch p.Format {
	case FormatJSON:
		return p.writeJSON(raw)
	case FormatYAML:
		return p.writeYAML(raw)
	case FormatTable, FormatWide, FormatName:
		if build == nil {
			return p.writeJSON(raw)
		}

		table, err := build()
		if err != nil {
			return err
		}

		return p.writeTable(table)
	default:
		return fmt.Errorf("unsupported output format %q", p.Format)
	}
}

// Message prints a human-facing confirmation line. It is suppressed for
// structured output so that `-o json` stays machine-parseable.
func (p *Printer) Message(format string, args ...any) {
	if p.Structured() || p.Format == FormatName {
		return
	}

	fmt.Fprintf(p.Out, format+"\n", args...)
}

// Note prints an advisory to stderr, so it never pollutes piped stdout.
func (p *Printer) Note(format string, args ...any) {
	fmt.Fprintf(p.Err, format+"\n", args...)
}

func (p *Printer) writeJSON(raw []byte) error {
	if len(raw) == 0 {
		return nil
	}

	var pretty any
	if err := json.Unmarshal(raw, &pretty); err != nil {
		// Not JSON (a proxied text response, say) — pass it through untouched.
		_, err := p.Out.Write(ensureNewline(raw))

		return err
	}

	enc := json.NewEncoder(p.Out)
	enc.SetIndent("", "  ")

	return enc.Encode(pretty)
}

func (p *Printer) writeYAML(raw []byte) error {
	if len(raw) == 0 {
		return nil
	}

	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		_, err := p.Out.Write(ensureNewline(raw))

		return err
	}

	enc := yaml.NewEncoder(p.Out)
	enc.SetIndent(2)

	if err := enc.Encode(doc); err != nil {
		return err
	}

	return enc.Close()
}

func ensureNewline(b []byte) []byte {
	if len(b) > 0 && b[len(b)-1] != '\n' {
		return append(b, '\n')
	}

	return b
}
