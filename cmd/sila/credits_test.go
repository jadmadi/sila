package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/render"
)

func TestCreditsCommand(t *testing.T) {
	ui := render.New(os.Stdout, true)

	// Verify all credit entries have non-empty licenses, entities, and categories
	for _, c := range projectCredits {
		if c.Entity == "" {
			t.Errorf("credit entry missing Entity: %+v", c)
		}
		if c.License == "" {
			t.Errorf("credit entry %q has empty License", c.Entity)
		}
		// Strict License Compatibility Audit: Only permissive licenses (MIT or BSD-3-Clause)
		// are permitted in Sila's credits to guarantee 100% AGPL v3 and commercial licensing compatibility.
		// Non-commercial licenses (e.g. PolyForm) or proprietary/GPL licenses are strictly forbidden.
		if c.License != "MIT" && c.License != "BSD-3-Clause" && c.License != "Apache-2.0" {
			t.Errorf("credit entry %q has non-permissive or incompatible license: %q (must be MIT, BSD-3-Clause, or Apache-2.0)", c.Entity, c.License)
		}
		// Strict Independence & Anti-Self-Crediting Audit: Sila must never credit itself as a 3rd-party dependency
		// or assign 3rd-party authors to the Sila repository URL.
		if c.URL == "https://github.com/jadmadi/sila" {
			t.Errorf("credit entry %q illegally points to Sila repository (self-crediting / improper attribution)", c.Entity)
		}
		if c.Category == "" {
			t.Errorf("credit entry %q has empty Category", c.Entity)
		}
		if c.Impact == "" {
			t.Errorf("credit entry %q has empty Impact", c.Entity)
		}
	}

	// Test normal human-readable output
	runCredits(ui, false, false)

	// Test JSON output
	runCredits(ui, true, false)

	// Test TOON output
	runCredits(ui, false, true)

	// Test with query filter (Backpass, Kun, Sibyl, License name, non-existent)
	runCredits(ui, false, false, "backpass")
	runCredits(ui, true, false, "backpass")
	runCredits(ui, false, true, "kun")
	runCredits(ui, false, false, "sibyl")
	runCredits(ui, true, false, "sibyl")
	runCredits(ui, false, false, "MIT")
	runCredits(ui, false, false, "BSD")
	runCredits(ui, true, false, "BSD")
	runCredits(ui, false, false, "nonexistent-query-string-xyz")
	runCredits(ui, true, false, "nonexistent-query-string-xyz")
	runCredits(ui, false, true, "nonexistent-query-string-xyz")
}

func TestOpinionsCommand(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_opinions_cmd.db")
	ui := render.New(os.Stdout, true)
	project := "testcmdproj"

	// 1. Add opinion
	runOpinions(ui, dbPath, project, "arch", "", false, false, []string{"add", "Always use pure-Go SQLite"})

	// 2. List opinions (standard, JSON, TOON)
	runOpinions(ui, dbPath, project, "", "", false, false, []string{"list"})
	runOpinions(ui, dbPath, project, "", "", true, false, []string{"list"})
	runOpinions(ui, dbPath, project, "", "", false, true, []string{"list"})

	// 3. Compile opinions
	outFile := filepath.Join(t.TempDir(), "OPINIONS.md")
	runOpinions(ui, dbPath, project, "", outFile, false, false, []string{"compile"})

	if _, err := os.Stat(outFile); err != nil {
		t.Fatalf("expected compiled OPINIONS.md file, got: %v", err)
	}
}
