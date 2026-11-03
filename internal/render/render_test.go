package render

import (
	"strings"
	"testing"
)

func TestVisibleWidthIgnoresANSI(t *testing.T) {
	u := New(nil, true)
	cell := u.Green("he") + "llo"
	if visibleWidth(cell) != 5 {
		t.Fatalf("visibleWidth=%d, want 5 (colored(+2) + 'llo')", visibleWidth(cell))
	}
}

func TestTableAlignment(t *testing.T) {
	u := New(nil, true)
	headers := []string{"tool", "state", "total", "new"}
	rows := [][]string{
		{"mimocode", "ok", "430", "59"},
		{"devin", "ok", "3787", "7"},
	}
	table := u.Table(headers, rows, []string{"l", "l", "r", "r"})
	for _, want := range []string{"tool", "state", "total", "new", "mimocode", "3787", "59"} {
		if !contains(table, want) {
			t.Fatalf("table missing %q:\n%s", want, table)
		}
	}
	// all content lines (header + rows, which start with '│') have the same
	// visible width; frame lines differ only because box-drawing glyphs are
	// multi-byte.
	base := -1
	for _, ln := range splitLines(table) {
		if !strings.HasPrefix(ln, "│") {
			continue
		}
		vw := visibleWidth(ln)
		if base < 0 {
			base = vw
		} else if vw != base {
			t.Fatalf("content widths differ (%d vs %d):\n%s", vw, base, table)
		}
	}
}

func TestFormatBytes(t *testing.T) {
	cases := map[int64]string{0: "0B", 1023: "1023B", 1024: "1.0 KB", 1 << 20: "1.0 MB", 1 << 30: "1.0 GB"}
	for in, want := range cases {
		if got := FormatBytes(in); got != want {
			t.Fatalf("FormatBytes(%d)=%q, want %q", in, got, want)
		}
	}
}

func TestFormatNumber(t *testing.T) {
	cases := map[int64]string{
		0:        "0",
		999:      "999",
		1000:     "1,000",
		17793:    "17,793",
		1000000:  "1,000,000",
		12345678: "12,345,678",
	}
	for in, want := range cases {
		if got := FormatNumber(in); got != want {
			t.Fatalf("FormatNumber(%d)=%q, want %q", in, got, want)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func splitLines(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '\n' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func TestTableGoldenSnapshot(t *testing.T) {
	u := New(nil, true) // no color
	headers := []string{"project", "confidence", "verified", "tools", "claim"}
	rows := [][]string{
		{"sila", "high", "yes", "devin,mimocode", "SQLite FTS5 full-text search"},
		{"almatjar", "mid", "no", "opencode", "Sector layout engine presets"},
	}
	got := u.Table(headers, rows, []string{"l", "l", "l", "l", "l"})

	want := `┌──────────┬────────────┬──────────┬────────────────┬──────────────────────────────┐
│ project  │ confidence │ verified │ tools          │ claim                        │
├──────────┼────────────┼──────────┼────────────────┼──────────────────────────────┤
│ sila     │ high       │ yes      │ devin,mimocode │ SQLite FTS5 full-text search │
│ almatjar │ mid        │ no       │ opencode       │ Sector layout engine presets │
└──────────┴────────────┴──────────┴────────────────┴──────────────────────────────┘`

	if strings.TrimSpace(got) != strings.TrimSpace(want) {
		t.Fatalf("table snapshot mismatch:\nGOT:\n%s\nWANT:\n%s", got, want)
	}
}

func TestWrapKeepsLinesWithinBudgetAndIndents(t *testing.T) {
	text := "The TodoWrite tool has not been used recently and the todo list may be stale"
	indent := strings.Repeat(" ", 23)
	got := Wrap(text, 40, indent)

	if !strings.Contains(got, "\n"+indent) {
		t.Errorf("wrapped text must indent continuations, got:\n%s", got)
	}
	for i, line := range strings.Split(got, "\n") {
		if i == 0 {
			if len(line) > 40 {
				t.Errorf("first line is %d columns, budget was 40: %q", len(line), line)
			}
			continue
		}
		if !strings.HasPrefix(line, indent) {
			t.Errorf("continuation line %d lost its indent: %q", i, line)
		}
		if len(line)-len(indent) > 40 {
			t.Errorf("continuation line %d is %d columns of text, budget was 40: %q", i, len(line)-len(indent), line)
		}
	}

	// Single words longer than the budget are not broken: splitting them would
	// corrupt a command or a path.
	long := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if got := Wrap(long, 10, "  "); got != long {
		t.Errorf("an unbreakable word was altered: %q", got)
	}
	if got := Wrap("", 20, ""); got != "" {
		t.Errorf("empty text = %q, want empty", got)
	}
}
