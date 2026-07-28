package output_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/leansignal/leansignal-cli/internal/output"
)

func newPrinter(format output.Format) (*output.Printer, *bytes.Buffer, *bytes.Buffer) {
	var out, errOut bytes.Buffer

	p := output.New(format, false, false)
	p.Out = &out
	p.Err = &errOut

	return p, &out, &errOut
}

func TestParseFormat(t *testing.T) {
	if _, err := output.ParseFormat("JSON"); err != nil {
		t.Errorf("ParseFormat should be case-insensitive: %v", err)
	}

	if _, err := output.ParseFormat("toml"); err == nil {
		t.Error("expected an error for an unsupported format")
	}
}

// -o json must print the server's own document, not a reshaped one.
func TestJSONOutputPreservesTheServerDocument(t *testing.T) {
	p, out, _ := newPrinter(output.FormatJSON)

	raw := []byte(`{"items":[{"id":"1","name":"demand","extra_field":"kept"}]}`)

	if err := p.Emit(raw, func() (*output.Table, error) {
		t.Fatal("the table builder must not run for structured output")

		return nil, nil
	}); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	if !strings.Contains(out.String(), "extra_field") {
		t.Errorf("a field absent from the CLI's own model was dropped:\n%s", out.String())
	}
}

// An empty result must leave stdout empty so `| wc -l` stays honest; the
// explanation goes to stderr.
func TestEmptyTableWritesTheMessageToStderr(t *testing.T) {
	p, out, errOut := newPrinter(output.FormatTable)

	table := output.NewTable("NAME")
	table.Empty = "No demands."

	if err := p.Emit(nil, func() (*output.Table, error) { return table, nil }); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	if out.Len() != 0 {
		t.Errorf("stdout should be empty, got %q", out.String())
	}

	if !strings.Contains(errOut.String(), "No demands.") {
		t.Errorf("stderr should carry the empty message, got %q", errOut.String())
	}
}

func TestTableIsPlainTextWithHeaders(t *testing.T) {
	p, out, _ := newPrinter(output.FormatTable)

	table := output.NewTable("NAME", "STATUS")
	table.Add("agent-1", "connected")

	if err := p.Emit(nil, func() (*output.Table, error) { return table, nil }); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	got := out.String()
	if !strings.HasPrefix(got, "NAME") || !strings.Contains(got, "agent-1") {
		t.Errorf("unexpected table:\n%s", got)
	}

	// Box-drawing characters would break awk/cut pipelines.
	for _, c := range []string{"│", "┌", "─", "|"} {
		if strings.Contains(got, c) {
			t.Errorf("table contains the box-drawing character %q:\n%s", c, got)
		}
	}
}

// -o name emits bare ids, one per line, for xargs.
func TestNameOutputEmitsIDsOnly(t *testing.T) {
	p, out, _ := newPrinter(output.FormatName)

	table := output.NewTable("ID", "NAME")
	table.Add("id-1", "first")
	table.Add("id-2", "second")

	if err := p.Emit(nil, func() (*output.Table, error) { return table, nil }); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	if got := out.String(); got != "id-1\nid-2\n" {
		t.Errorf("got %q", got)
	}
}

// Messages must not contaminate machine-readable output.
func TestMessageSuppressedForStructuredOutput(t *testing.T) {
	p, out, _ := newPrinter(output.FormatJSON)
	p.Message("Created demand %q", "x")

	if out.Len() != 0 {
		t.Errorf("stdout should stay clean for -o json, got %q", out.String())
	}
}

func TestCellRendersEmptyValuesAsADash(t *testing.T) {
	if got := output.Cell(""); got != "-" {
		t.Errorf("empty string cell = %q, want -", got)
	}

	if got := output.Cell((*time.Time)(nil)); got != "-" {
		t.Errorf("nil time cell = %q, want -", got)
	}

	// Newlines would break the one-row-per-line contract.
	if got := output.Cell("a\nb"); strings.Contains(got, "\n") {
		t.Errorf("cell kept a newline: %q", got)
	}
}

func TestAgeAndBytes(t *testing.T) {
	if got := output.Age(time.Now().Add(-90 * time.Minute)); got != "1h" {
		t.Errorf("Age(90m) = %q, want 1h", got)
	}

	if got := output.Bytes(1_500_000); got != "1.5 MB" {
		t.Errorf("Bytes(1.5e6) = %q, want 1.5 MB", got)
	}
}

func TestTruncate(t *testing.T) {
	if got := output.Truncate("abcdef", 4); got != "abc…" {
		t.Errorf("Truncate = %q", got)
	}

	if got := output.Truncate("abc", 10); got != "abc" {
		t.Errorf("Truncate should leave short strings alone, got %q", got)
	}
}
