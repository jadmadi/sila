package goals

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAtomicRetrospectiveGoalCreation(t *testing.T) {
	tmp := t.TempDir()

	g, err := CreateGoal(CreateGoalParams{
		RootDir:     tmp,
		Slug:        "retrospective-feature",
		Title:       "Retrospective Feature",
		Description: "A feature that was already implemented previously",
		Status:      "implemented",
		Commit:      "8f4e21a",
		Tasks: []string{
			"Phase 1: Design interface",
			"Phase 2: Implement core algorithm",
		},
	})
	if err != nil {
		t.Fatalf("CreateGoal failed: %v", err)
	}

	if g.Status != "implemented" {
		t.Errorf("expected g.Status == 'implemented', got %q", g.Status)
	}
	if g.Commit != "8f4e21a" {
		t.Errorf("expected g.Commit == '8f4e21a', got %q", g.Commit)
	}
	if g.Task != "Retrospective" {
		t.Errorf("expected g.Task == 'Retrospective', got %q", g.Task)
	}
	if g.TasksMet != 2 || g.TasksTotal != 2 {
		t.Errorf("expected TasksMet == 2 and TasksTotal == 2, got %d/%d", g.TasksMet, g.TasksTotal)
	}

	goalDir := filepath.Join(tmp, "goals", "retrospective-feature")

	// 1. Verify goal.md metadata
	goalBytes, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
	if err != nil {
		t.Fatalf("reading goal.md: %v", err)
	}
	goalContent := string(goalBytes)
	if !strings.Contains(goalContent, "- **Status**: implemented") {
		t.Errorf("goal.md missing status: %s", goalContent)
	}
	if !strings.Contains(goalContent, "- **Commit**: 8f4e21a") {
		t.Errorf("goal.md missing commit: %s", goalContent)
	}

	// 2. Verify plan.md checkboxes are all checked
	planBytes, err := os.ReadFile(filepath.Join(goalDir, "plan.md"))
	if err != nil {
		t.Fatalf("reading plan.md: %v", err)
	}
	planContent := string(planBytes)
	if strings.Contains(planContent, "- [ ]") {
		t.Errorf("plan.md contains unchecked items: %s", planContent)
	}
	if !strings.Contains(planContent, "- [x] Phase 1: Design interface") {
		t.Errorf("plan.md missing checked Phase 1: %s", planContent)
	}
	if !strings.Contains(planContent, "- [x] Phase 2: Implement core algorithm") {
		t.Errorf("plan.md missing checked Phase 2: %s", planContent)
	}

	// 3. Verify retrospective.json exists
	retroRec, err := GetActiveRetrospective(tmp, "retrospective-feature")
	if err != nil {
		t.Fatalf("GetActiveRetrospective failed: %v", err)
	}
	if retroRec == nil || retroRec.Commit != "8f4e21a" {
		t.Fatalf("unexpected retroRec: %+v", retroRec)
	}

	// 4. Verify ScanGoals recognizes it as implemented
	audit, err := ScanGoals(tmp)
	if err != nil {
		t.Fatalf("ScanGoals failed: %v", err)
	}
	if audit.Total != 1 || audit.ImplementedCount != 1 {
		t.Errorf("expected 1 implemented goal, got total=%d implemented=%d", audit.Total, audit.ImplementedCount)
	}
	if audit.Goals[0].Status != "implemented" || audit.Goals[0].Commit != "8f4e21a" {
		t.Errorf("unexpected audit goal: %+v", audit.Goals[0])
	}

	// 5. Verify SyncReadme renders in Implemented table
	readmeBytes, err := os.ReadFile(filepath.Join(tmp, "goals", "README.md"))
	if err != nil {
		t.Fatalf("reading README.md: %v", err)
	}
	readme := string(readmeBytes)
	if !strings.Contains(readme, "## 🟢 Implemented") {
		t.Errorf("README missing implemented section: %s", readme)
	}
	if !strings.Contains(readme, "[`retrospective-feature`]") || !strings.Contains(readme, "8f4e21a") {
		t.Errorf("README missing retrospective goal entry: %s", readme)
	}

	// 6. Test Reopening clears implementation
	reopenRec, _, err := ReopenGoal(tmp, "retrospective-feature", "tester", "needs refactoring", false)
	if err != nil {
		t.Fatalf("ReopenGoal failed: %v", err)
	}
	if !reopenRec.Active {
		t.Fatalf("expected reopen record active")
	}

	auditAfterReopen, err := ScanGoals(tmp)
	if err != nil {
		t.Fatalf("ScanGoals after reopen failed: %v", err)
	}
	if auditAfterReopen.Goals[0].Status != "pending" || !auditAfterReopen.Goals[0].Reopened {
		t.Errorf("expected goal to be pending [REOPENED] after reopen, got %+v", auditAfterReopen.Goals[0])
	}
}
