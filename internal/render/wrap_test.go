package render

import (
	"strings"
	"testing"
)

func TestWrapHonorsWidthAndIndent(t *testing.T) {
	text := "  • Activity/domains/review/skills: `sila standup last 24h`, `sila digest --since=24h`, `sila recap`"
	wrapped := WrapAt(text, 40)
	for _, line := range strings.Split(wrapped, "\n") {
		if VisibleWidth(line) > 40 {
			t.Fatalf("line exceeds 40 columns (%d): %q", VisibleWidth(line), line)
		}
		if !strings.HasPrefix(line, "  ") {
			t.Fatalf("continuation lost indentation: %q", line)
		}
	}
	if !strings.Contains(wrapped, "`sila standup last 24h`") {
		t.Fatalf("wrapped inside a backticked command:\n%s", wrapped)
	}
}

func TestWrapCountsAnsiAndWideRunes(t *testing.T) {
	colored := "\x1b[31m" + "hello world" + "\x1b[0m"
	if got := VisibleWidth(colored); got != 11 {
		t.Fatalf("ANSI width = %d, want 11", got)
	}
	if got := VisibleWidth("你好世界"); got != 8 {
		t.Fatalf("CJK width = %d, want 8", got)
	}
	wrapped := WrapAt("你好世界 你好世界", 8)
	for _, line := range strings.Split(wrapped, "\n") {
		if VisibleWidth(line) > 8 {
			t.Fatalf("wide-rune line exceeds width: %q", line)
		}
	}
}

func TestTerminalWidthResolution(t *testing.T) {
	t.Setenv("COLUMNS", "")
	SetWidthOverride(0)
	if got := TerminalWidth(); got != 100 {
		t.Fatalf("default width = %d, want 100", got)
	}
	t.Setenv("COLUMNS", "40")
	if got := TerminalWidth(); got != 40 {
		t.Fatalf("COLUMNS width = %d, want 40", got)
	}
	t.Setenv("COLUMNS", "20")
	if got := TerminalWidth(); got != 40 {
		t.Fatalf("clamped width = %d, want 40", got)
	}
	SetWidthOverride(120)
	if got := TerminalWidth(); got != 120 {
		t.Fatalf("override width = %d, want 120", got)
	}
	SetWidthOverride(0)
}
