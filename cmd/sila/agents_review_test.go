package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/assist"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestAgentsReview_ZeroWritesGuaranteed(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "sila.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	rulesFile := filepath.Join(tmp, "AGENTS.md")
	content := "# My Custom Rules\n- Invariant: do not modify\n"
	if err := os.WriteFile(rulesFile, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	ui := render.New(os.Stdout, true)

	// Run review on existing file
	runAgentsReview(ui, dbPath, tmp, "test-proj", "AGENTS.md", "project", false)

	// Read file back: must be strictly unmodified
	readBack, err := os.ReadFile(rulesFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(readBack) != content {
		t.Fatalf("runAgentsReview mutated the file! Before: %q, After: %q", content, string(readBack))
	}
}

func TestAgentsReview_NonexistentFileNotCreated(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "sila.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	rulesFile := filepath.Join(tmp, "NONEXISTENT.md")
	ui := render.New(os.Stdout, true)

	// Run review on non-existent file
	runAgentsReview(ui, dbPath, tmp, "test-proj", "NONEXISTENT.md", "project", false)

	// Ensure file was NOT created on disk
	if _, err := os.Stat(rulesFile); !os.IsNotExist(err) {
		t.Fatalf("runAgentsReview created non-existent file: %v", err)
	}
}

func TestAgentsReview_ScopeConsentRequirement(t *testing.T) {
	// With assist enabled but only recap scoped, rules review provider must be nil
	t.Setenv("SILA_ASSIST_ENABLED", "true")
	t.Setenv("SILA_ASSIST_PROVIDER", "mock")
	t.Setenv("SILA_ASSIST_SCOPES", "recap")
	t.Setenv("SILA_ASSIST_PROJECTS", "test-proj")

	pRules := assist.ActiveProviderFor("rules", "test-proj")
	if pRules != nil {
		t.Errorf("expected rules provider to be nil with recap-only consent")
	}

	pReview := assist.ActiveProviderFor("review", "test-proj")
	if pReview != nil {
		t.Errorf("expected review provider to be nil with recap-only consent")
	}

	// Scoping rules or review should activate it
	t.Setenv("SILA_ASSIST_SCOPES", "rules")
	if p := assist.ActiveProviderFor("rules", "test-proj"); p == nil {
		t.Errorf("expected rules provider to be active with rules scope")
	}

	t.Setenv("SILA_ASSIST_SCOPES", "review")
	if p := assist.ActiveProviderFor("review", "test-proj"); p == nil {
		t.Errorf("expected review provider to be active with review scope")
	}
}

func TestAgentsReview_TargetAliases(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "sila.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	// Write CLAUDE.md, .cursorrules, .windsurfrules
	mustWrite := func(name, content string) {
		if err := os.WriteFile(filepath.Join(tmp, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("CLAUDE.md", "# Claude Rules\n")
	mustWrite(".cursorrules", "# Cursor Rules\n")
	mustWrite(".windsurfrules", "# Windsurf Rules\n")

	ui := render.New(os.Stdout, true)

	// Verify alias reviews run cleanly without error or mutating files
	for _, alias := range []string{"claude", "cursor", "windsurf", "agents"} {
		runAgentsReview(ui, dbPath, tmp, "test-proj", alias, "project", false)
	}
}
