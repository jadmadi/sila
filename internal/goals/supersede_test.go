package goals

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSupersedeGoal(t *testing.T) {
	dir := t.TempDir()
	goalDir := filepath.Join(dir, "goals", "old-goal")
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte("# Goal: Old Goal\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte("# Plan\n- [ ] Task 1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "facts.md"), []byte("# Facts\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Supersede the goal
	rec, err := SupersedeGoal(dir, "old-goal", "new-architecture", "@agent", "absorbed into new architecture")
	if err != nil {
		t.Fatalf("SupersedeGoal failed: %v", err)
	}
	if rec.GoalSlug != "old-goal" || rec.SupersededBy != "new-architecture" {
		t.Fatalf("unexpected record: %+v", rec)
	}

	// ScanGoals should reflect status = "superseded"
	res, err := ScanGoals(dir)
	if err != nil {
		t.Fatalf("ScanGoals failed: %v", err)
	}
	if res.SupersededCount != 1 {
		t.Fatalf("expected 1 superseded goal, got %d", res.SupersededCount)
	}
	if len(res.Goals) != 1 || res.Goals[0].Status != "superseded" {
		t.Fatalf("expected goal status superseded, got %q", res.Goals[0].Status)
	}
	if !res.Goals[0].Superseded || res.Goals[0].SupersededBy != "new-architecture" {
		t.Fatalf("expected Superseded fields populated, got %+v", res.Goals[0])
	}
	if res.TotalTasks != 0 || res.CompletedTasks != 0 {
		t.Fatalf("expected 0 total tasks for superseded goal, got total=%d, completed=%d", res.TotalTasks, res.CompletedTasks)
	}
}

func TestCarriedForwardFromCommitTrailer(t *testing.T) {
	dir := t.TempDir()
	goalDir := filepath.Join(dir, "goals", "absorbed-feature")
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"goal.md", "plan.md", "facts.md"} {
		if err := os.WriteFile(filepath.Join(goalDir, name), []byte("# Goal: Absorbed\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	git(t, dir, "init", "-q")
	git(t, dir, "config", "user.email", "test@test.com")
	git(t, dir, "config", "user.name", "Tester")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "feat: initial commit\n\nCarried-Forward-From: absorbed-feature")

	res, err := ScanGoals(dir)
	if err != nil {
		t.Fatalf("ScanGoals failed: %v", err)
	}
	if len(res.Goals) != 1 || res.Goals[0].Status != "superseded" {
		t.Fatalf("expected goal status superseded from trailer, got %q", res.Goals[0].Status)
	}
	if res.SupersededCount != 1 {
		t.Fatalf("expected SupersededCount 1, got %d", res.SupersededCount)
	}
}
