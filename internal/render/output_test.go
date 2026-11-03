package render

import (
	"path/filepath"
	"strings"
	"testing"
)

func testUI() UI { return New(nil, true) }

func TestRenderHeaderForms(t *testing.T) {
	ui := testUI()
	cases := []struct {
		h    Header
		want string
	}{
		{Header{Command: "agents check", Scope: "repo", Title: "Instruction Audit"}, "sila · agents check · repo — Instruction Audit"},
		{Header{Command: "handoff", Title: "active session handoff"}, "sila · handoff — active session handoff"},
		{Header{Command: "goals", Scope: "repo"}, "sila · goals · repo"},
		{Header{Command: "doctor"}, "sila · doctor"},
	}
	for _, tc := range cases {
		got := strings.TrimSuffix(RenderHeader(ui, tc.h), "\n")
		if got != tc.want {
			t.Errorf("RenderHeader(%+v) = %q, want %q", tc.h, got, tc.want)
		}
	}
	withMode := strings.TrimSuffix(RenderHeader(ui, Header{Command: "onboard", Scope: "repo", Mode: "dry-run"}), "\n")
	if withMode != "sila · onboard · repo (dry-run)" {
		t.Errorf("mode suffix = %q", withMode)
	}
	if strings.HasPrefix(strings.TrimSpace(RenderHeader(ui, Header{Command: "goals"})), "✔") {
		t.Fatal("header must not be emoji-first")
	}
}

func TestRenderCardShapeAndEmptyFields(t *testing.T) {
	ui := testUI()
	card := Card{
		Verb:   "Recorded",
		Object: "memo",
		ID:     "fact_abc",
		Fields: []Field{
			{Label: "Directive", Value: "Use pure-Go SQLite"},
			{Label: "Project", Value: DisplayProject("sila-cli", "sila-cli")},
			{Label: "Files", Value: ""},
		},
	}
	out := RenderCard(ui, card)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if lines[0] != "✔ Recorded memo fact_abc" {
		t.Fatalf("title = %q", lines[0])
	}
	if !strings.Contains(out, "  Directive: Use pure-Go SQLite\n") {
		t.Errorf("missing directive field: %q", out)
	}
	if !strings.Contains(out, "  Files:     none\n") {
		t.Errorf("empty list must render none: %q", out)
	}
	if strings.Contains(out, "✓") {
		t.Fatal("success card must not use ✓")
	}
}

func TestRenderErrorShape(t *testing.T) {
	ui := testUI()
	out := RenderError(ui, "decision not found: D1", "sila decision list")
	if !strings.HasPrefix(out, "✖ decision not found: D1\n") {
		t.Fatalf("error line = %q", out)
	}
	if !strings.Contains(out, "  fix: sila decision list\n") {
		t.Fatalf("missing fix line: %q", out)
	}
	if strings.Contains(out, "sila decision view error:") {
		t.Fatal("old error prefix leaked")
	}
}

func TestDisplayProject(t *testing.T) {
	cases := []struct{ friendly, id, want string }{
		{"sila-cli", "sila-cli", "sila-cli"},
		{"repo", "repo-41819e49", "repo (repo-41819e49)"},
		{"", "repo-41819e49", "repo-41819e49"},
		{"repo", "", "repo"},
		{"", "", "<unknown>"},
	}
	for _, tc := range cases {
		if got := DisplayProject(tc.friendly, tc.id); got != tc.want {
			t.Errorf("DisplayProject(%q,%q) = %q, want %q", tc.friendly, tc.id, got, tc.want)
		}
	}
}

func TestDisplayPathClasses(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	if got := DisplayPath(root, filepath.Join(root, "internal", "agents", "x.go")); got != filepath.Join("internal", "agents", "x.go") {
		t.Errorf("inside repo = %q", got)
	}
	outside := filepath.Join(t.TempDir(), "elsewhere.go")
	if got := DisplayPath(root, outside); got != outside {
		t.Errorf("outside repo = %q, want %q", got, outside)
	}
	underHome := filepath.Join(home, "notes", "x.md")
	if got := DisplayPath(root, underHome); got != filepath.Join("~", "notes", "x.md") {
		t.Errorf("under home = %q", got)
	}
	if got := DisplayPath("", ""); got != "" {
		t.Errorf("empty path = %q", got)
	}
}

func TestGlyphsAreColorIndependent(t *testing.T) {
	plain := New(nil, true)
	colorUI := New(nil, false)
	card := Card{Verb: "Recorded", Object: "memo", ID: "fact_1"}
	if plainCard := RenderCard(plain, card); !strings.Contains(plainCard, GlyphSuccess) {
		t.Fatalf("plain card missing success glyph: %q", plainCard)
	}
	if colorCard := RenderCard(colorUI, card); !strings.Contains(colorCard, GlyphSuccess) {
		t.Fatalf("colored card missing success glyph: %q", colorCard)
	}
	if GlyphSuccess == "✓" || GlyphFailure != "✖" || GlyphWarning != "▲" {
		t.Fatal("canonical glyph set changed")
	}
}
