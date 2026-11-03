package goals

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
)

func TestCountCheckboxes(t *testing.T) {
	content := `# Execution Plan

## Steps
- [ ] Task 1: Setup database schema
- [x] Task 2: Implement core models
- [X] Task 3: Add API routes
* [ ] Task 4: Write documentation
* [x] Task 5: Verify edge cases
+ [ ] Task 6: Cleanup logs
+ [x] Task 7: Final review

Some normal text and bullet points:
- This is just a note, not a task
* Another note
`
	met, total := countCheckboxes(content)
	if met != 4 {
		t.Errorf("countCheckboxes met = %d, expected 4", met)
	}
	if total != 7 {
		t.Errorf("countCheckboxes total = %d, expected 7", total)
	}
}

func TestRenderProgressBar(t *testing.T) {
	ui := render.New(os.Stdout, false)

	// 0/4
	b0 := renderProgressBar(ui, 0, 4, 10)
	if !strings.Contains(b0, "░░░░░░░░░░") {
		t.Errorf("expected empty progress bar, got %s", b0)
	}

	// 2/4 (50%)
	b50 := renderProgressBar(ui, 2, 4, 10)
	if !strings.Contains(b50, "█████░░░░░") {
		t.Errorf("expected 50%% progress bar, got %s", b50)
	}

	// 4/4 (100%)
	b100 := renderProgressBar(ui, 4, 4, 10)
	if !strings.Contains(b100, "██████████") {
		t.Errorf("expected 100%% progress bar, got %s", b100)
	}

	// Empty total
	bEmpty := renderProgressBar(ui, 0, 0, 10)
	if bEmpty != "" {
		t.Errorf("expected empty string for total=0, got %q", bEmpty)
	}
}

func TestScanGoals_TaskProgress(t *testing.T) {
	tmp := t.TempDir()

	g, err := CreateGoal(CreateGoalParams{
		RootDir: tmp,
		Slug:    "progress-goal",
		Title:   "Progress Goal Test",
	})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	if g.TasksTotal != 4 || g.TasksMet != 0 {
		t.Errorf("expected 4 total tasks and 0 met after create, got total=%d, met=%d", g.TasksTotal, g.TasksMet)
	}

	// Initial scan
	audit1, err := ScanGoals(tmp)
	if err != nil {
		t.Fatalf("scan goals 1: %v", err)
	}
	if audit1.TotalTasks != 4 || audit1.CompletedTasks != 0 {
		t.Errorf("audit1 expected total=4, completed=0, got total=%d, completed=%d", audit1.TotalTasks, audit1.CompletedTasks)
	}

	// Mark 2 tasks completed in plan.md
	planPath := filepath.Join(tmp, "goals", "progress-goal", "plan.md")
	planContent := `# Plan: Progress Goal Test

## Execution Steps
- [x] **Phase 1 — Contracts & Schemas**: Define models.
- [x] **Phase 2 — Core Implementation**: Implement business logic.
- [ ] **Phase 3 — Verification & Tests**: Write unit tests.
- [ ] **Phase 4 — Attribution Commit**: Commit and verify.
`
	if err := os.WriteFile(planPath, []byte(planContent), 0644); err != nil {
		t.Fatalf("write plan: %v", err)
	}

	audit2, err := ScanGoals(tmp)
	if err != nil {
		t.Fatalf("scan goals 2: %v", err)
	}
	if audit2.TotalTasks != 4 || audit2.CompletedTasks != 2 {
		t.Errorf("audit2 expected total=4, completed=2, got total=%d, completed=%d", audit2.TotalTasks, audit2.CompletedTasks)
	}
	if audit2.Goals[0].TasksMet != 2 || audit2.Goals[0].TasksTotal != 4 {
		t.Errorf("audit2 goal expected met=2, total=4, got met=%d, total=%d", audit2.Goals[0].TasksMet, audit2.Goals[0].TasksTotal)
	}

	// Test GetGoalTasks
	tasks, err := GetGoalTasks(tmp, "progress-goal")
	if err != nil {
		t.Fatalf("GetGoalTasks failed: %v", err)
	}
	if len(tasks) != 4 {
		t.Fatalf("expected 4 tasks, got %d", len(tasks))
	}
	if !tasks[0].Completed || !tasks[1].Completed || tasks[2].Completed || tasks[3].Completed {
		t.Errorf("unexpected task completion states: %+v", tasks)
	}

	// Test ToggleGoalTask check 3
	t3, err := ToggleGoalTask(tmp, "progress-goal", 3, true)
	if err != nil {
		t.Fatalf("ToggleGoalTask 3 check failed: %v", err)
	}
	if !t3.Completed {
		t.Errorf("expected task 3 to be completed after toggle")
	}

	// Verify ScanGoals sees 3 met
	audit3, err := ScanGoals(tmp)
	if err != nil {
		t.Fatalf("scan goals 3: %v", err)
	}
	if audit3.CompletedTasks != 3 {
		t.Errorf("expected 3 completed tasks after toggle, got %d", audit3.CompletedTasks)
	}

	// Test ToggleGoalTask uncheck 1
	t1, err := ToggleGoalTask(tmp, "progress-goal", 1, false)
	if err != nil {
		t.Fatalf("ToggleGoalTask 1 uncheck failed: %v", err)
	}
	if t1.Completed {
		t.Errorf("expected task 1 to be incomplete after uncheck")
	}

	audit4, err := ScanGoals(tmp)
	if err != nil {
		t.Fatalf("scan goals 4: %v", err)
	}
	if audit4.CompletedTasks != 2 {
		t.Errorf("expected 2 completed tasks after unchecking task 1, got %d", audit4.CompletedTasks)
	}
}
