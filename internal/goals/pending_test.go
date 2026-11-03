package goals

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPendingGoalDiagnostics(t *testing.T) {
	dir := t.TempDir()

	// Goal 1: root-task (pending, ready)
	dir1 := filepath.Join(dir, "goals", "root-task")
	_ = os.MkdirAll(dir1, 0755)
	_ = os.WriteFile(filepath.Join(dir1, "goal.md"), []byte("# Goal: Root Task\n"), 0644)
	_ = os.WriteFile(filepath.Join(dir1, "plan.md"), []byte("# Plan\n- [ ] Step 1\n"), 0644)
	_ = os.WriteFile(filepath.Join(dir1, "facts.md"), []byte("# Facts\n"), 0644)

	// Goal 2: blocked-task (depends on root-task)
	dir2 := filepath.Join(dir, "goals", "blocked-task")
	_ = os.MkdirAll(dir2, 0755)
	_ = os.WriteFile(filepath.Join(dir2, "goal.md"), []byte("# Goal: Blocked Task\n\n## Dependencies\n- **Depends On**: root-task\n"), 0644)
	_ = os.WriteFile(filepath.Join(dir2, "plan.md"), []byte("# Plan\n- [ ] Step 1\n"), 0644)
	_ = os.WriteFile(filepath.Join(dir2, "facts.md"), []byte("# Facts\n"), 0644)

	res, err := ScanGoals(dir)
	if err != nil {
		t.Fatalf("ScanGoals failed: %v", err)
	}

	var rootG, blockedG *Goal
	for i := range res.Goals {
		if res.Goals[i].Slug == "root-task" {
			rootG = &res.Goals[i]
		}
		if res.Goals[i].Slug == "blocked-task" {
			blockedG = &res.Goals[i]
		}
	}

	if rootG == nil || blockedG == nil {
		t.Fatalf("expected both goals found")
	}

	if !strings.Contains(rootG.PendingReason, "ready") {
		t.Errorf("expected rootG pending reason to indicate ready, got %q", rootG.PendingReason)
	}
	if !strings.Contains(blockedG.PendingReason, "blocked by root-task") {
		t.Errorf("expected blockedG pending reason to specify blocked by root-task, got %q", blockedG.PendingReason)
	}
}
