package render

import (
	"os"
	"strings"
	"testing"
)

func clearColorEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"NO_COLOR", "CLICOLOR", "CLICOLOR_FORCE", "SILA_FORCE_COLOR",
		"TERM", "COLORTERM", "SILA_THEME", "COLORFGBG",
	} {
		t.Setenv(key, "")
	}
}

func TestColorPrecedence(t *testing.T) {
	clearColorEnv(t)
	if New(os.Stdout, true).ColorEnabled() {
		t.Fatal("--no-color must always disable color")
	}

	t.Setenv("SILA_FORCE_COLOR", "1")
	if !New(os.Stdout, false).ColorEnabled() {
		t.Fatal("force-enable must enable color")
	}

	t.Setenv("NO_COLOR", "1")
	if New(os.Stdout, false).ColorEnabled() {
		t.Fatal("NO_COLOR must win over force-enable")
	}

	t.Setenv("NO_COLOR", "")
	t.Setenv("CLICOLOR", "0")
	if !New(os.Stdout, false).ColorEnabled() {
		t.Fatal("force-enable must win over CLICOLOR=0")
	}

	t.Setenv("SILA_FORCE_COLOR", "")
	t.Setenv("CLICOLOR", "")
	t.Setenv("TERM", "dumb")
	if New(os.Stdout, false).ColorEnabled() {
		t.Fatal("TERM=dumb must disable color")
	}

	// An empty NO_COLOR is treated as absent.
	clearColorEnv(t)
	t.Setenv("SILA_FORCE_COLOR", "1")
	t.Setenv("NO_COLOR", "")
	if !New(os.Stdout, false).ColorEnabled() {
		t.Fatal("empty NO_COLOR must not disable color")
	}
}

func TestColorDepthAndTheme(t *testing.T) {
	clearColorEnv(t)
	t.Setenv("SILA_FORCE_COLOR", "1")

	ui := New(os.Stdout, false)
	if ui.ColorDepth() != 16 {
		t.Fatalf("default depth = %d, want 16", ui.ColorDepth())
	}
	if got := ui.Red("x"); !strings.Contains(got, "\x1b[31m") || strings.Contains(got, "38;") {
		t.Fatalf("16-color red = %q", got)
	}

	t.Setenv("TERM", "xterm-256color")
	ui = New(os.Stdout, false)
	if ui.ColorDepth() != 256 {
		t.Fatalf("256 depth = %d", ui.ColorDepth())
	}
	if got := ui.Red("x"); !strings.Contains(got, "38;5;203") || strings.Contains(got, "38;2") {
		t.Fatalf("256-color red = %q", got)
	}

	t.Setenv("COLORTERM", "truecolor")
	ui = New(os.Stdout, false)
	if ui.ColorDepth() != 24 {
		t.Fatalf("truecolor depth = %d", ui.ColorDepth())
	}
	if got := ui.Red("x"); !strings.Contains(got, "38;2") {
		t.Fatalf("truecolor red = %q", got)
	}

	t.Setenv("SILA_THEME", "light")
	ui = New(os.Stdout, false)
	if ui.Theme() != "light" {
		t.Fatalf("theme = %q, want light", ui.Theme())
	}
	if got := ui.Red("x"); !strings.Contains(got, "38;2;185;28;28") {
		t.Fatalf("light red = %q", got)
	}
}

func TestColorFGBGLightTheme(t *testing.T) {
	clearColorEnv(t)
	t.Setenv("SILA_FORCE_COLOR", "1")
	t.Setenv("COLORFGBG", "0;15")
	if got := New(os.Stdout, false).Theme(); got != "light" {
		t.Fatalf("theme = %q, want light from COLORFGBG", got)
	}
}

func TestNoColorEmitsNoEscapes(t *testing.T) {
	clearColorEnv(t)
	t.Setenv("SILA_FORCE_COLOR", "1")
	t.Setenv("COLORTERM", "truecolor")
	ui := New(os.Stdout, true)
	if got := ui.Bold(ui.Red("x")); got != "x" {
		t.Fatalf("styled output = %q, want plain x", got)
	}
	if ui.ColorDepth() != 0 {
		t.Fatalf("disabled depth = %d, want 0", ui.ColorDepth())
	}
}
