package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestGoalsStructuredParamsCLI(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "goals_structured_test.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.EnsureProject(tmp)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	ui := render.New(os.Stdout, true)

	// Create goal with --invariants and --tasks via CLI args
	runGoals(ui, dbPath, tmp, "agent", "", "", "", false, false, "", "CLI Structured Goal", "Description", "", "internal/structured/goal.go", 1, true, false, false, false, false, false, false, false, true, false, false, false, false, "", "", "", false,
		[]string{
			"create",
			"cli-structured-goal",
			"--invariants=Zero ambient telemetry; Pure Go 1.26+, zero CGO",
			"--tasks=Phase 1: Schemas; Phase 2: Implementation; Phase 3: Tests",
		})

	goalDir := filepath.Join(tmp, "goals", "cli-structured-goal")

	factsBytes, err := os.ReadFile(filepath.Join(goalDir, "facts.md"))
	if err != nil {
		t.Fatalf("facts.md not found: %v", err)
	}
	factsContent := string(factsBytes)
	if !strings.Contains(factsContent, "Zero ambient telemetry") {
		t.Errorf("facts.md missing invariant: %s", factsContent)
	}
	if !strings.Contains(factsContent, "Pure Go 1.26+, zero CGO") {
		t.Errorf("facts.md missing invariant: %s", factsContent)
	}

	planBytes, err := os.ReadFile(filepath.Join(goalDir, "plan.md"))
	if err != nil {
		t.Fatalf("plan.md not found: %v", err)
	}
	planContent := string(planBytes)
	if !strings.Contains(planContent, "- [ ] Phase 1: Schemas") {
		t.Errorf("plan.md missing Phase 1: %s", planContent)
	}
	if !strings.Contains(planContent, "- [ ] Phase 2: Implementation") {
		t.Errorf("plan.md missing Phase 2: %s", planContent)
	}
	if !strings.Contains(planContent, "- [ ] Phase 3: Tests") {
		t.Errorf("plan.md missing Phase 3: %s", planContent)
	}

	audit, err := goals.ScanGoals(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(audit.Goals) != 1 {
		t.Fatalf("expected 1 goal, got %d", len(audit.Goals))
	}
	if audit.Goals[0].TasksTotal != 3 {
		t.Errorf("expected 3 tasks in audit, got %d", audit.Goals[0].TasksTotal)
	}
}
