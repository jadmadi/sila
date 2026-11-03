package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
)

func TestMigratedSuccessCardsShareShape(t *testing.T) {
	ui := render.New(nil, true)
	cards := []render.Card{
		{Verb: "Recorded", Object: "memo", ID: "fact_1", Fields: []render.Field{{Label: "Directive", Value: "d"}, {Label: "Project", Value: "repo"}, {Label: "Files", Value: ""}}},
		{Verb: "Recorded", Object: "lesson", ID: "fact_2", Fields: []render.Field{{Label: "Symptom", Value: "s"}, {Label: "Cause", Value: "c"}, {Label: "Fix", Value: "f"}, {Label: "Project", Value: "repo"}, {Label: "Grounding", Value: "grounded"}}},
		{Verb: "Recorded", Object: "architectural case", ID: "fact_3", Fields: []render.Field{{Label: "Intent", Value: "i"}, {Label: "Approach", Value: "a"}, {Label: "Invariant", Value: "v"}, {Label: "Project", Value: "repo"}, {Label: "Grounding", Value: "grounded"}}},
		{Verb: "Feedback recorded", ID: "fb_1", Fields: []render.Field{{Label: "Type", Value: "idea"}, {Label: "Agent", Value: "agent"}, {Label: "Content", Value: "c"}, {Label: "Project", Value: "repo"}}},
		{Verb: "Saved", Object: "handoff", ID: "hoff_1", Fields: []render.Field{{Label: "Project", Value: "repo"}, {Label: "Summary", Value: "s"}, {Label: "Open items", Value: "none"}, {Label: "Repo files", Value: "none"}}},
		{Verb: "Ingested", Object: "document", ID: "doc_1", Fields: []render.Field{{Label: "Title", Value: "t"}, {Label: "Project", Value: "repo"}, {Label: "URL", Value: ""}, {Label: "Format", Value: "markdown"}}},
	}
	for _, card := range cards {
		out := render.RenderCard(ui, card)
		if !strings.HasPrefix(out, render.GlyphSuccess+" ") {
			t.Errorf("card does not start with canonical success glyph: %q", out)
		}
		if strings.Contains(out, "✓") {
			t.Errorf("card uses the retired glyph: %q", out)
		}
		if !strings.Contains(out, "Project:") {
			t.Errorf("card missing Project field: %q", out)
		}
	}
}

func TestSameProjectAcrossCards(t *testing.T) {
	label := render.DisplayProject("sila-cli", "sila-cli")
	if label != "sila-cli" {
		t.Fatalf("label = %q", label)
	}
	mixed := render.DisplayProject("repo", "repo-41819e49")
	if mixed != "repo (repo-41819e49)" {
		t.Fatalf("mixed label = %q", mixed)
	}
	if render.DisplayProject("", "repo-41819e49") != "repo-41819e49" {
		t.Fatal("id-only label changed")
	}
}

func TestMigratedCommandsUseCanonicalGlyphsAndErrors(t *testing.T) {
	files := []string{
		"memo.go", "lesson.go", "case.go", "feedback.go", "handoff.go", "doc.go",
	}
	for _, name := range files {
		data, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if strings.Contains(string(data), "✓") {
			t.Errorf("%s still contains the retired ✓ glyph", name)
		}
	}
	for _, name := range []string{"memo.go", "lesson.go", "case.go", "feedback.go", "handoff.go"} {
		data, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if strings.Contains(string(data), "sila ") && strings.Contains(string(data), " error:") {
			t.Errorf("%s still contains a legacy 'sila X error:' prefix", name)
		}
	}
}

func TestCoverageFailureUsesCanonicalErrorShape(t *testing.T) {
	ui := render.New(nil, true)
	out := render.RenderError(ui, "coverage failed: no Go module found", "run 'sila coverage' from the module root")
	if !strings.HasPrefix(out, "✖ coverage failed:") || !strings.Contains(out, "fix: run 'sila coverage'") {
		t.Fatalf("coverage error shape = %q", out)
	}
	if strings.Contains(out, "exit status 1") {
		t.Fatal("raw go test output leaked into the one-line cause")
	}
}
