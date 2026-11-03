package goals

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindGoalsRoot_UpwardSearch(t *testing.T) {
	tmp := t.TempDir()
	projRoot := filepath.Join(tmp, "myproj")
	nestedSubdir := filepath.Join(projRoot, "cmd", "sila", "nested", "sub")
	if err := os.MkdirAll(nestedSubdir, 0755); err != nil {
		t.Fatal(err)
	}

	goalDir := filepath.Join(projRoot, "goals", "test-goal")
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte("# Test Goal\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// 1. Upward search from deeply nested subdirectory
	found := FindGoalsRoot(nestedSubdir)
	if found != projRoot {
		t.Fatalf("expected FindGoalsRoot to return %q, got %q", projRoot, found)
	}

	// 2. ResolveGoalsRootDir from nested subdirectory
	resolved := ResolveGoalsRootDir(nestedSubdir)
	if resolved != projRoot {
		t.Fatalf("expected ResolveGoalsRootDir to return %q, got %q", projRoot, resolved)
	}

	// 3. ScanGoals from nested subdirectory
	audit, err := ScanGoals(nestedSubdir)
	if err != nil {
		t.Fatalf("ScanGoals error: %v", err)
	}
	if len(audit.Goals) != 1 || audit.Goals[0].Slug != "test-goal" {
		t.Fatalf("expected 1 goal 'test-goal', got %+v", audit.Goals)
	}
}

func TestFindGoalsRoot_DownwardWorkspace(t *testing.T) {
	ws := t.TempDir()
	cliDir := filepath.Join(ws, "cli")
	workerDir := filepath.Join(ws, "worker")
	researchDir := filepath.Join(ws, "research")

	for _, d := range []string{cliDir, workerDir, researchDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	goalDir := filepath.Join(cliDir, "goals", "cli-core-goal")
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte("# CLI Core Goal\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Downward discovery from workspace root with single goals child
	found := FindGoalsRoot(ws)
	if found != cliDir {
		t.Fatalf("expected FindGoalsRoot(%q) to discover %q, got %q", ws, cliDir, found)
	}

	resolved := ResolveGoalsRootDir(ws)
	if resolved != cliDir {
		t.Fatalf("expected ResolveGoalsRootDir(%q) to discover %q, got %q", ws, cliDir, resolved)
	}

	// ScanGoals from workspace root
	audit, err := ScanGoals(ws)
	if err != nil {
		t.Fatalf("ScanGoals from workspace root error: %v", err)
	}
	if len(audit.Goals) != 1 || audit.Goals[0].Slug != "cli-core-goal" {
		t.Fatalf("expected 'cli-core-goal', got %+v", audit.Goals)
	}
}

func TestFindGoalsRoot_MultiChildPreference(t *testing.T) {
	ws := t.TempDir()
	cliDir := filepath.Join(ws, "cli")
	workerDir := filepath.Join(ws, "worker")

	for _, d := range []string{cliDir, workerDir} {
		if err := os.MkdirAll(filepath.Join(d, "goals", "my-goal"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "goals", "my-goal", "goal.md"), []byte("# Goal\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// When both cli and worker have goals, preference should favor "cli"
	found := FindGoalsRoot(ws)
	if found != cliDir {
		t.Fatalf("expected FindGoalsRoot to prefer 'cli' (%q), got %q", cliDir, found)
	}

	// ResolveProjectDir with explicit child folder path
	resolvedWorker, err := ResolveProjectDir(nil, workerDir)
	if err != nil {
		t.Fatalf("ResolveProjectDir(%q) error: %v", workerDir, err)
	}
	if resolvedWorker != workerDir {
		t.Fatalf("expected %q, got %q", workerDir, resolvedWorker)
	}
}

func TestResolveProjectDir_FallbackEmptyDir(t *testing.T) {
	emptyDir := t.TempDir()
	audit, err := ScanGoals(emptyDir)
	if err != nil {
		t.Fatalf("expected no error on empty dir, got: %v", err)
	}
	if len(audit.Goals) != 0 {
		t.Fatalf("expected 0 goals in empty dir, got %d", len(audit.Goals))
	}
}

func TestExplicitProjectWithoutGoalsStaysSelected(t *testing.T) {
	parent := t.TempDir()
	if err := os.MkdirAll(filepath.Join(parent, "goals", "ancestor"), 0755); err != nil {
		t.Fatal(err)
	}
	worker := filepath.Join(parent, "worker")
	if err := os.MkdirAll(worker, 0755); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveProjectDir(nil, worker)
	if err != nil || got != worker {
		t.Fatalf("explicit root changed: got %q, err %v", got, err)
	}
	if err := os.WriteFile(filepath.Join(worker, ".git"), []byte("gitdir: ../worktrees/worker\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := FindGoalsRoot(worker); got != "" {
		t.Fatalf("discovery crossed repository boundary: %s", got)
	}
	audit, err := ScanGoals(worker)
	if err != nil || len(audit.Goals) != 0 {
		t.Fatalf("scan adopted ancestor goals: %+v, %v", audit, err)
	}
}
