package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
)

func setupTestGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// git init
	cmd := exec.Command("git", "init", "-b", "main")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init failed: %v\n%s", err, string(out))
	}

	// git config user
	_ = exec.Command("git", "-C", dir, "config", "user.name", "Sila Tester").Run()
	_ = exec.Command("git", "-C", dir, "config", "user.email", "test@sila.local").Run()

	// Initial commit so HEAD is valid
	readme := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readme, []byte("# Test Repo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_ = exec.Command("git", "-C", dir, "add", "README.md").Run()
	_ = exec.Command("git", "-C", dir, "commit", "-m", "chore: initial commit").Run()

	return dir
}

func createTestGoal(t *testing.T, repoDir, slug, title string) {
	t.Helper()
	goalDir := filepath.Join(repoDir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatal(err)
	}

	goalContent := `# Goal: ` + title + `

## Goal Description
Test description for ` + slug + `.

## Dependencies & Execution Order
- **Mode**: Independent
- **Depends On**: none
- **Sequence**: 1
- **Shape**: ship
- **Tier**: roadmap

## References
- Execution Plan: plan.md

## Done Condition
1. Implemented
`
	if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalContent), 0644); err != nil {
		t.Fatal(err)
	}

	planContent := `# Plan: ` + title + `
- [x] Phase 1: Complete
`
	if err := os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte(planContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Initial goals/README.md
	goalsReadme := filepath.Join(repoDir, "goals", "README.md")
	_ = os.WriteFile(goalsReadme, []byte("# Goals\nInitial registry\n"), 0644)
	_ = exec.Command("git", "-C", repoDir, "add", "goals/").Run()
	_ = exec.Command("git", "-C", repoDir, "commit", "-m", "docs(goals): init goal registry").Run()
}

func TestGoalsCommit_Success(t *testing.T) {
	repoDir := setupTestGitRepo(t)
	createTestGoal(t, repoDir, "alpha-feature", "Alpha Feature")

	// Create implementation code change
	codeFile := filepath.Join(repoDir, "alpha.go")
	if err := os.WriteFile(codeFile, []byte("package main\n\nfunc Alpha() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("git", "-C", repoDir, "add", "alpha.go").Run(); err != nil {
		t.Fatal(err)
	}

	ui := render.New(os.Stdout, true)
	res, err := executeGoalsCommit(ui, repoDir, []string{"commit", "alpha-feature"}, "feat(core): add alpha feature", true)
	if err != nil {
		t.Fatalf("executeGoalsCommit failed: %v", err)
	}

	if !res.Success {
		t.Errorf("expected Success=true")
	}
	if res.Slug != "alpha-feature" {
		t.Errorf("expected slug 'alpha-feature', got %q", res.Slug)
	}
	if res.Stage1SHA == "" {
		t.Errorf("expected non-empty Stage1SHA")
	}
	expectedAttribution := "(goals/alpha-feature/goal.md)"
	if !strings.Contains(res.Stage1Msg, expectedAttribution) {
		t.Errorf("expected Stage1Msg to contain %q, got %q", expectedAttribution, res.Stage1Msg)
	}
	if !res.RegistrySync {
		t.Errorf("expected RegistrySync=true")
	}
	if res.Stage2SHA == "" {
		t.Errorf("expected non-empty Stage2SHA")
	}

	// Verify git history has 2 new commits
	logCmd := exec.Command("git", "-C", repoDir, "log", "-n", "2", "--oneline")
	out, err := logCmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	logLines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(logLines) < 2 {
		t.Fatalf("expected at least 2 commit lines, got:\n%s", string(out))
	}
	// Most recent commit should be docs(goals): sync...
	if !strings.Contains(logLines[0], "docs(goals): sync goal registry after verifying alpha-feature") {
		t.Errorf("expected top commit to be docs(goals) sync, got: %s", logLines[0])
	}
	// Prior commit should be feat(core): ... (goals/alpha-feature/goal.md)
	if !strings.Contains(logLines[1], "feat(core): add alpha feature (goals/alpha-feature/goal.md)") {
		t.Errorf("expected second commit to be implementation commit, got: %s", logLines[1])
	}
}

func TestGoalsCommitUncheckedTasksGate(t *testing.T) {
	repoDir := setupTestGitRepo(t)
	createTestGoal(t, repoDir, "gate-feature", "Gate Feature")

	plan := "# Plan: Gate Feature\n\n- [ ] Phase 1: Implement feature\n- [ ] Phase 2 — Attribution Commit: Commit with (goals/gate-feature/goal.md)\n"
	if err := os.WriteFile(filepath.Join(repoDir, "goals", "gate-feature", "plan.md"), []byte(plan), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "gate.go"), []byte("package main\n\nfunc Gate() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("git", "-C", repoDir, "add", "gate.go", "goals/gate-feature/plan.md").Run(); err != nil {
		t.Fatal(err)
	}

	ui := render.New(os.Stdout, true)
	_, err := executeGoalsCommit(ui, repoDir, []string{"commit", "gate-feature"}, "feat(gate): add gate", true)
	if err == nil || !strings.Contains(err.Error(), "unchecked task") {
		t.Fatalf("expected unchecked-task gate failure, got %v", err)
	}

	res, err := executeGoalsCommitWithOptions(ui, repoDir, []string{"commit", "gate-feature"}, "feat(gate): add gate", true, true)
	if err != nil {
		t.Fatalf("commit with --check-tasks failed: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected successful commit result: %+v", res)
	}
	planBytes, err := os.ReadFile(filepath.Join(repoDir, "goals", "gate-feature", "plan.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(planBytes), "- [x] Phase 1: Implement feature") {
		t.Errorf("expected non-attribution task auto-checked, got:\n%s", planBytes)
	}
	if !strings.Contains(string(planBytes), "- [ ] Phase 2 — Attribution Commit") {
		t.Errorf("attribution task must remain unchecked, got:\n%s", planBytes)
	}
}

func TestGoalsCommit_UnstagesReadmeIfStaged(t *testing.T) {
	repoDir := setupTestGitRepo(t)
	createTestGoal(t, repoDir, "beta-feature", "Beta Feature")

	// User modifies code and prematurely stages goals/README.md
	codeFile := filepath.Join(repoDir, "beta.go")
	_ = os.WriteFile(codeFile, []byte("package main\n\nfunc Beta() {}\n"), 0644)
	readmeFile := filepath.Join(repoDir, "goals", "README.md")
	_ = os.WriteFile(readmeFile, []byte("# Goals\nPrematurely edited\n"), 0644)

	// Stage BOTH
	_ = exec.Command("git", "-C", repoDir, "add", "beta.go", "goals/README.md").Run()

	ui := render.New(os.Stdout, true)
	res, err := executeGoalsCommit(ui, repoDir, []string{"commit", "beta-feature"}, "feat(beta): implement beta", true)
	if err != nil {
		t.Fatalf("executeGoalsCommit failed: %v", err)
	}

	if !res.Success {
		t.Errorf("expected Success=true")
	}

	// Verify Stage 1 commit ONLY touched beta.go
	diffCmd := exec.Command("git", "-C", repoDir, "diff-tree", "--no-commit-id", "--name-only", "-r", res.Stage1SHA)
	out, _ := diffCmd.Output()
	stage1Files := strings.TrimSpace(string(out))
	if strings.Contains(stage1Files, "goals/README.md") {
		t.Errorf("Stage 1 commit MUST NOT contain goals/README.md, got files:\n%s", stage1Files)
	}
	if !strings.Contains(stage1Files, "beta.go") {
		t.Errorf("Stage 1 commit missing beta.go")
	}
}

func TestGoalsCommit_NoStagedChangesError(t *testing.T) {
	repoDir := setupTestGitRepo(t)
	createTestGoal(t, repoDir, "gamma-feature", "Gamma Feature")

	ui := render.New(os.Stdout, true)
	_, err := executeGoalsCommit(ui, repoDir, []string{"commit", "gamma-feature"}, "feat: test", true)
	if err == nil {
		t.Fatalf("expected error when no changes are staged")
	}
	if !strings.Contains(err.Error(), "no staged changes to commit") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestGoalsCommit_NonexistentGoalError(t *testing.T) {
	repoDir := setupTestGitRepo(t)

	ui := render.New(os.Stdout, true)
	_, err := executeGoalsCommit(ui, repoDir, []string{"commit", "nonexistent-slug"}, "feat: test", true)
	if err == nil {
		t.Fatalf("expected error for nonexistent goal")
	}
	if !strings.Contains(err.Error(), "goal definition not found") {
		t.Errorf("unexpected error message: %v", err)
	}
}
