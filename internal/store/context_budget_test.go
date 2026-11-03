package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContextBudgetMeasurement(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// 1. Empty dir: no always-load files
	b := st.MeasureContextBudget(dir, nil)
	if b.TotalTokens != 0 || b.Status != "light" {
		t.Fatalf("expected 0 tokens and light status, got %+v", b)
	}

	// 2. Add lean AGENTS.md (e.g. 400 chars -> 100 tokens)
	agentsContent := strings.Repeat("A quick brown fox jumps over the lazy dog.\n", 10)
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(agentsContent), 0644); err != nil {
		t.Fatal(err)
	}

	handoff := &Handoff{
		Summary:   "Building new authentication layer",
		OpenItems: []string{"- [ ] Add JWT verification", "- [ ] Write tests"},
		Decisions: []string{"Pure Go implementation"},
	}

	b = st.MeasureContextBudget(dir, handoff)
	if b.TotalTokens == 0 {
		t.Fatalf("expected non-zero tokens, got 0")
	}
	if b.Status != "light" {
		t.Fatalf("expected light status, got %s", b.Status)
	}
	summary := b.FormatSummary()
	if !strings.Contains(summary, "Light") {
		t.Fatalf("expected Light in summary: %s", summary)
	}

	// 3. Add heavy CLAUDE.md (> 6000 tokens = > 24,000 chars)
	heavyContent := strings.Repeat("Rule: Keep functions small and explicit.\n", 800) // ~32,000 chars -> ~8,000 tok
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte(heavyContent), 0644); err != nil {
		t.Fatal(err)
	}

	b = st.MeasureContextBudget(dir, handoff)
	if b.Status != "watch" {
		t.Fatalf("expected watch status due to heavy file, got %s", b.Status)
	}
	if len(b.TrimTargets) == 0 {
		t.Fatalf("expected trim target for CLAUDE.md, got none")
	}
}
