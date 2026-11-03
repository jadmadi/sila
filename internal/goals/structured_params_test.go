package goals

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateGoalWithStructuredInvariantsAndTasks(t *testing.T) {
	tmp := t.TempDir()

	invariants := []string{
		"Zero ambient telemetry; all network calls require explicit consent.",
		"Pure Go 1.26+, zero CGO runtime dependency.",
		"- Preserve offline-first local verification.",
	}
	tasks := []string{
		"Phase 1 — Schema Definitions: Declare new parameters and data models",
		"- [ ] Phase 2 — Implementation: Wire handlers and business logic",
		"Phase 3 — Test Verification: Add unit and integration test coverage",
		"Attribution commit and goal closure",
		"Post-merge telemetry review",
	}
	filesToTouch := []string{"internal/goals/goals.go", "internal/mcp/server.go"}

	g, err := CreateGoal(CreateGoalParams{
		RootDir:      tmp,
		Slug:         "structured-package-goal",
		Title:        "Structured Package Goal",
		Description:  "Goal with explicit invariants and tasks",
		FilesToTouch: filesToTouch,
		Invariants:   invariants,
		Tasks:        tasks,
	})
	if err != nil {
		t.Fatalf("CreateGoal failed: %v", err)
	}

	if g.TasksTotal != 5 {
		t.Errorf("expected TasksTotal = 5, got %d", g.TasksTotal)
	}

	goalDir := filepath.Join(tmp, "goals", "structured-package-goal")

	// 1. Verify facts.md
	factsBytes, err := os.ReadFile(filepath.Join(goalDir, "facts.md"))
	if err != nil {
		t.Fatalf("reading facts.md failed: %v", err)
	}
	factsContent := string(factsBytes)

	for _, inv := range invariants {
		cleanInv := strings.TrimPrefix(inv, "- ")
		if !strings.Contains(factsContent, cleanInv) {
			t.Errorf("facts.md missing invariant: %q\nGot:\n%s", cleanInv, factsContent)
		}
	}
	for _, f := range filesToTouch {
		if !strings.Contains(factsContent, f) {
			t.Errorf("facts.md missing fileToTouch: %q\nGot:\n%s", f, factsContent)
		}
	}

	// 2. Verify plan.md
	planBytes, err := os.ReadFile(filepath.Join(goalDir, "plan.md"))
	if err != nil {
		t.Fatalf("reading plan.md failed: %v", err)
	}
	planContent := string(planBytes)

	if !strings.Contains(planContent, "- [ ] Phase 1 — Schema Definitions") {
		t.Errorf("plan.md missing Phase 1 task\nGot:\n%s", planContent)
	}
	if !strings.Contains(planContent, "- [ ] Phase 2 — Implementation") {
		t.Errorf("plan.md missing Phase 2 task\nGot:\n%s", planContent)
	}
	if !strings.Contains(planContent, "- [ ] **Phase 4**: Attribution commit and goal closure") {
		t.Errorf("plan.md missing Phase 4 task\nGot:\n%s", planContent)
	}

	// 3. Verify GetGoalTasks correctly extracts all 5 tasks
	extractedTasks, err := GetGoalTasks(tmp, "structured-package-goal")
	if err != nil {
		t.Fatalf("GetGoalTasks failed: %v", err)
	}
	if len(extractedTasks) != 5 {
		t.Errorf("expected 5 extracted tasks, got %d", len(extractedTasks))
	}
}

func TestCreateGoalDefaultInvariantsAndTasksFallback(t *testing.T) {
	tmp := t.TempDir()

	g, err := CreateGoal(CreateGoalParams{
		RootDir: tmp,
		Slug:    "default-package-goal",
	})
	if err != nil {
		t.Fatalf("CreateGoal failed: %v", err)
	}

	if g.TasksTotal != 4 {
		t.Errorf("expected default TasksTotal = 4, got %d", g.TasksTotal)
	}

	goalDir := filepath.Join(tmp, "goals", "default-package-goal")

	factsBytes, err := os.ReadFile(filepath.Join(goalDir, "facts.md"))
	if err != nil {
		t.Fatalf("reading facts.md failed: %v", err)
	}
	factsContent := string(factsBytes)
	if !strings.Contains(factsContent, "Ground all implementations in official repository standards.") {
		t.Errorf("facts.md missing default invariant: %s", factsContent)
	}

	planBytes, err := os.ReadFile(filepath.Join(goalDir, "plan.md"))
	if err != nil {
		t.Fatalf("reading plan.md failed: %v", err)
	}
	planContent := string(planBytes)
	if !strings.Contains(planContent, "- [ ] **Phase 1 — Contracts & Schemas**") {
		t.Errorf("plan.md missing default Phase 1: %s", planContent)
	}
}
