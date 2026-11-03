package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// setupRepo creates a temp git repo with one commit and returns its path.
func setupRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@test.com")
	runGit(t, dir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "initial commit")
	return dir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s: %v", args, string(out), err)
	}
}

func TestNewManager(t *testing.T) {
	dir := setupRepo(t)
	mgr, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if mgr.RepoRoot() != dir {
		t.Errorf("RepoRoot = %s, want %s", mgr.RepoRoot(), dir)
	}
}

func TestNewManagerNotARepo(t *testing.T) {
	dir := t.TempDir()
	_, err := NewManager(dir)
	if err == nil {
		t.Error("expected error for non-git directory")
	}
}

func TestCreateAndListWorktree(t *testing.T) {
	dir := setupRepo(t)
	mgr, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	wt, err := mgr.Create("test-sandbox", "HEAD")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.Contains(wt.Path, "sila-worktrees") {
		t.Errorf("path should contain sila-worktrees, got %s", wt.Path)
	}
	if wt.Branch != "sila/test-sandbox" {
		t.Errorf("branch = %s, want sila/test-sandbox", wt.Branch)
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Errorf("worktree path should exist: %v", err)
	}

	// List should include the new worktree.
	wts, err := mgr.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	found := false
	for _, w := range wts {
		if strings.Contains(w.Path, "test-sandbox") {
			found = true
			if w.Branch != "sila/test-sandbox" && w.Branch != "refs/heads/sila/test-sandbox" {
				t.Errorf("listed branch = %s, want sila/test-sandbox", w.Branch)
			}
		}
	}
	if !found {
		t.Error("worktree not found in list")
	}
}

func TestCreateWorktreeWithExistingBranch(t *testing.T) {
	dir := setupRepo(t)
	mgr, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// First create succeeds.
	_, err = mgr.Create("reuse-test", "HEAD")
	if err != nil {
		t.Fatalf("first Create: %v", err)
	}

	// Remove the worktree but keep the branch.
	if err := mgr.Remove("reuse-test", true); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	// Second create should reuse the existing branch.
	wt, err := mgr.Create("reuse-test", "HEAD")
	if err != nil {
		t.Fatalf("second Create (reuse branch): %v", err)
	}
	if wt.Branch != "sila/reuse-test" {
		t.Errorf("branch = %s, want sila/reuse-test", wt.Branch)
	}

	// Cleanup.
	mgr.Remove("reuse-test", true)
}

func TestRemoveWorktree(t *testing.T) {
	dir := setupRepo(t)
	mgr, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	wt, err := mgr.Create("to-remove", "HEAD")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := mgr.Remove("to-remove", true); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	if _, err := os.Stat(wt.Path); err == nil {
		t.Error("worktree path should not exist after remove")
	}
}

func TestRemoveWorktreeByName(t *testing.T) {
	dir := setupRepo(t)
	mgr, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	_, err = mgr.Create("by-name", "HEAD")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Remove by name (not full path).
	if err := mgr.Remove("by-name", true); err != nil {
		t.Fatalf("Remove by name: %v", err)
	}
}

func TestPrune(t *testing.T) {
	dir := setupRepo(t)
	mgr, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	wt, err := mgr.Create("prune-test", "HEAD")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Manually remove the directory (simulating stale worktree).
	os.RemoveAll(wt.Path)

	// Prune should clean up the stale entry.
	if err := mgr.Prune(); err != nil {
		t.Fatalf("Prune: %v", err)
	}
}

func TestRunInWorktree(t *testing.T) {
	dir := setupRepo(t)
	mgr, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	_, err = mgr.Create("run-test", "HEAD")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer mgr.Remove("run-test", true)

	// Run a simple command.
	stdout, stderr, exitErr := mgr.Run("run-test", "echo", "hello")
	if exitErr != nil {
		t.Fatalf("Run: %v (stderr: %s)", exitErr, stderr)
	}
	if !strings.Contains(stdout, "hello") {
		t.Errorf("stdout = %q, want to contain 'hello'", stdout)
	}
}

func TestSeedContext(t *testing.T) {
	dir := setupRepo(t)
	mgr, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	_, err = mgr.Create("seed-test", "HEAD")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer mgr.Remove("seed-test", true)

	err = mgr.SeedContext("seed-test", "test-project", "Test handoff summary")
	if err != nil {
		t.Fatalf("SeedContext: %v", err)
	}

	// Verify SILA_CONTEXT.md was written.
	wtPath := filepath.Join(mgr.WorktreeDir(), "seed-test")
	ctxPath := filepath.Join(wtPath, "SILA_CONTEXT.md")
	data, err := os.ReadFile(ctxPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "test-project") {
		t.Errorf("context should contain project ID")
	}
	if !strings.Contains(content, "Test handoff summary") {
		t.Errorf("context should contain handoff summary")
	}
	if !strings.Contains(content, "sila worktree") {
		t.Errorf("context should mention sila worktree")
	}
}

func TestParseWorktreeList(t *testing.T) {
	raw := `worktree /home/user/project
HEAD abc1234567890abcdef1234567890abcdef1234
branch refs/heads/main

worktree /home/user/project/.git/sila-worktrees/sandbox
HEAD def5678901234567890abcdef1234567890abcd
branch refs/heads/sila/sandbox

`
	wts := parseWorktreeList(raw)
	if len(wts) != 2 {
		t.Fatalf("expected 2 worktrees, got %d", len(wts))
	}
	if !wts[0].IsPrimary {
		t.Error("first worktree should be primary")
	}
	if wts[1].IsPrimary {
		t.Error("second worktree should not be primary")
	}
	if wts[1].Branch != "sila/sandbox" {
		t.Errorf("second branch = %s, want sila/sandbox", wts[1].Branch)
	}
}

func TestWorktreeDir(t *testing.T) {
	dir := setupRepo(t)
	mgr, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	wtDir := mgr.WorktreeDir()
	if !strings.Contains(wtDir, "sila-worktrees") {
		t.Errorf("WorktreeDir should contain sila-worktrees, got %s", wtDir)
	}
}

func TestPilotRun(t *testing.T) {
	dir := setupRepo(t)
	mgr, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	res, err := mgr.PilotRun("test-pilot", "HEAD", 10*time.Second, "echo", "pilot-success")
	if err != nil {
		t.Fatalf("PilotRun failed: %v", err)
	}

	if !res.Success {
		t.Fatalf("expected PilotRun success, got failed with exit code %d: %s", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "pilot-success") {
		t.Fatalf("expected stdout to contain 'pilot-success', got %q", res.Stdout)
	}
	if !res.CleanedUp {
		t.Fatalf("expected PilotRun worktree to be cleaned up")
	}

	// Verify worktree dir was cleaned up
	if _, err := os.Stat(res.Worktree); err == nil {
		t.Fatalf("expected worktree %s to be deleted after pilot", res.Worktree)
	}
}
