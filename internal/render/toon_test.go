package render

import (
	"strings"
	"testing"
)

func TestFormatTOONTable(t *testing.T) {
	headers := []string{"id", "title", "status"}
	rows := [][]string{
		{"1", "Fix auth bug", "open"},
		{"2", "Add pagination, filters", "closed"},
		{"3", "Quote \"test\"", "open"},
	}

	out := FormatTOONTable("tasks", headers, rows)
	expectedHeader := "tasks[3]{id,title,status}:"
	if !strings.HasPrefix(out, expectedHeader) {
		t.Fatalf("expected header %q, got: %s", expectedHeader, out)
	}

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines, got %d", len(lines))
	}

	// Line 1: 1,Fix auth bug,open
	if !strings.Contains(lines[1], "1,Fix auth bug,open") {
		t.Errorf("line 1 mismatch: %s", lines[1])
	}
	// Line 2 has comma, so must be quoted
	if !strings.Contains(lines[2], `"Add pagination, filters"`) {
		t.Errorf("line 2 expected quoted comma value, got: %s", lines[2])
	}
	// Line 3 has quotes, so must be escaped
	if !strings.Contains(lines[3], `"Quote \"test\""`) {
		t.Errorf("line 3 expected escaped quotes, got: %s", lines[3])
	}
}

func TestFormatTOONObject(t *testing.T) {
	fields := [][2]string{
		{"number", "42"},
		{"title", "Fix auth bug"},
		{"state", "open"},
	}
	out := FormatTOONObject("task", fields)
	expected := "task:\n  number: 42\n  title: Fix auth bug\n  state: open\n"
	if out != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, out)
	}
}
