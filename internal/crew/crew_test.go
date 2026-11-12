package crew

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/assess"
)

func setupCrewRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmds := [][]string{
		{"init"},
		{"config", "user.email", "test@test.com"},
		{"config", "user.name", "Test"},
	}
	for _, args := range cmds {
		c := exec.Command("git", args...)
		c.Dir = dir
		if err := c.Run(); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add", "."},
		{"commit", "-m", "initial"},
	} {
		c := exec.Command("git", args...)
		c.Dir = dir
		if err := c.Run(); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	return dir
}

func setupTestCrewManager(t *testing.T) (*CrewManager, string) {
	t.Helper()
	dir := setupCrewRepo(t)
	mgr, err := NewCrewManager(dir)
	if err != nil {
		t.Fatalf("NewCrewManager: %v", err)
	}
	// Default to inert test command so unit tests never launch real external CLI binaries.
	mgr.SetCommandBuilder(func(harness, goalSlug, worktreePath string) string {
		return "sleep 60"
	})
	t.Cleanup(func() {
		_ = mgr.KillAll()
	})
	return mgr, dir
}

func TestNewCrewManager(t *testing.T) {
	dir := setupCrewRepo(t)
	mgr, err := NewCrewManager(dir)
	if err != nil {
		t.Fatalf("NewCrewManager: %v", err)
	}
	if mgr.RepoRoot() != dir {
		t.Errorf("RepoRoot = %s, want %s", mgr.RepoRoot(), dir)
	}
	if mgr.StateDir() == "" {
		t.Error("StateDir should not be empty")
	}
	// State dir should exist.
	if _, err := os.Stat(mgr.StateDir()); err != nil {
		t.Errorf("StateDir should exist: %v", err)
	}
}

func TestNewCrewManagerNotARepo(t *testing.T) {
	dir := t.TempDir()
	_, err := NewCrewManager(dir)
	if err == nil {
		t.Error("expected error for non-git directory")
	}
}

func TestSpawnCrewmate(t *testing.T) {
	mgr, _ := setupTestCrewManager(t)

	cm, err := mgr.Spawn("devin", "test-goal", "HEAD")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Kill(cm.ID) })
	if cm.ID == "" {
		t.Error("ID should not be empty")
	}
	if cm.Harness != "devin" {
		t.Errorf("Harness = %s, want devin", cm.Harness)
	}
	if cm.GoalSlug != "test-goal" {
		t.Errorf("GoalSlug = %s, want test-goal", cm.GoalSlug)
	}
	if cm.Worktree == "" {
		t.Error("Worktree should not be empty")
	}
	if !strings.Contains(cm.Worktree, "sila-worktrees") {
		t.Errorf("Worktree should contain sila-worktrees, got %s", cm.Worktree)
	}
	if cm.Branch != "sila/crew-"+cm.ID {
		t.Errorf("Branch = %s, want sila/crew-%s", cm.Branch, cm.ID)
	}
	if cm.Status != StatusRunning {
		t.Errorf("Status = %s, want running", cm.Status)
	}

	// Verify worktree exists on disk.
	if _, err := os.Stat(cm.Worktree); err != nil {
		t.Errorf("worktree should exist: %v", err)
	}

	// Verify state file was persisted.
	statePath := filepath.Join(mgr.StateDir(), cm.ID+".json")
	if _, err := os.Stat(statePath); err != nil {
		t.Errorf("state file should exist: %v", err)
	}

	// Cleanup.
	mgr.Kill(cm.ID)
}

func TestSpawnMissingHarness(t *testing.T) {
	dir := setupCrewRepo(t)
	mgr, err := NewCrewManager(dir)
	if err != nil {
		t.Fatalf("NewCrewManager: %v", err)
	}
	_, err = mgr.Spawn("", "test-goal", "HEAD")
	if err == nil {
		t.Error("expected error for missing harness")
	}
}

func TestSpawnMissingGoalSlug(t *testing.T) {
	dir := setupCrewRepo(t)
	mgr, err := NewCrewManager(dir)
	if err != nil {
		t.Fatalf("NewCrewManager: %v", err)
	}
	_, err = mgr.Spawn("devin", "", "HEAD")
	if err == nil {
		t.Error("expected error for missing goal slug")
	}
}

func TestListCrewmates(t *testing.T) {
	mgr, _ := setupTestCrewManager(t)

	// Initially empty.
	crewmates, err := mgr.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(crewmates) != 0 {
		t.Errorf("expected 0 crewmates, got %d", len(crewmates))
	}

	// Spawn one.
	cm, err := mgr.Spawn("claude", "test-goal-2", "HEAD")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Kill(cm.ID) })

	// List should now contain 1.
	crewmates, err = mgr.List()
	if err != nil {
		t.Fatalf("List after spawn: %v", err)
	}
	if len(crewmates) != 1 {
		t.Errorf("expected 1 crewmate, got %d", len(crewmates))
	}
	if crewmates[0].ID != cm.ID {
		t.Errorf("crewmate ID = %s, want %s", crewmates[0].ID, cm.ID)
	}

	// Cleanup.
	mgr.Kill(cm.ID)
}

func TestGetCrewmate(t *testing.T) {
	mgr, _ := setupTestCrewManager(t)

	cm, err := mgr.Spawn("agy", "test-goal-3", "HEAD")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Kill(cm.ID) })

	got, err := mgr.Get(cm.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != cm.ID {
		t.Errorf("ID = %s, want %s", got.ID, cm.ID)
	}
	if got.Harness != "agy" {
		t.Errorf("Harness = %s, want agy", got.Harness)
	}

	// Cleanup.
	mgr.Kill(cm.ID)
}

func TestGetCrewmateNotFound(t *testing.T) {
	dir := setupCrewRepo(t)
	mgr, err := NewCrewManager(dir)
	if err != nil {
		t.Fatalf("NewCrewManager: %v", err)
	}
	_, err = mgr.Get("nonexistent-id")
	if err == nil {
		t.Error("expected error for nonexistent crewmate")
	}
}

func TestKillCrewmate(t *testing.T) {
	mgr, _ := setupTestCrewManager(t)

	cm, err := mgr.Spawn("codex", "test-goal-4", "HEAD")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Kill(cm.ID) })

	// Verify worktree exists.
	if _, err := os.Stat(cm.Worktree); err != nil {
		t.Fatalf("worktree should exist before kill: %v", err)
	}

	if err := mgr.Kill(cm.ID); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	// Verify worktree is cleaned up.
	if _, err := os.Stat(cm.Worktree); err == nil {
		t.Error("worktree should not exist after kill")
	}

	// Verify status is killed.
	got, err := mgr.Get(cm.ID)
	if err != nil {
		t.Fatalf("Get after kill: %v", err)
	}
	if got.Status != StatusKilled {
		t.Errorf("Status = %s, want killed", got.Status)
	}
}

func TestSuperviseEmpty(t *testing.T) {
	dir := setupCrewRepo(t)
	mgr, err := NewCrewManager(dir)
	if err != nil {
		t.Fatalf("NewCrewManager: %v", err)
	}
	crewmates, err := mgr.Supervise()
	if err != nil {
		t.Fatalf("Supervise: %v", err)
	}
	if len(crewmates) != 0 {
		t.Errorf("expected 0 crewmates, got %d", len(crewmates))
	}
}

func TestSuperviseWithCrewmate(t *testing.T) {
	assess.AssertNoGoroutineLeaks(t)
	mgr, _ := setupTestCrewManager(t)

	cm, err := mgr.Spawn("opencode", "test-goal-5", "HEAD")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Kill(cm.ID) })

	crewmates, err := mgr.Supervise()
	if err != nil {
		t.Fatalf("Supervise: %v", err)
	}
	if len(crewmates) != 1 {
		t.Errorf("expected 1 crewmate, got %d", len(crewmates))
	}

	// Cleanup.
	mgr.Kill(cm.ID)
}

func TestBuildHarnessCommand(t *testing.T) {
	cases := []struct {
		harness  string
		contains string
	}{
		{"devin", "devin"},
		{"claude", "claude"},
		{"claude-code", "claude"},
		{"agy", "agy"},
		{"antigravity", "Antigravity"},
		{"opencode", "opencode"},
		{"codex", "codex"},
		{"mimocode", "mimocode"},
		{"mimo", "mimocode"},
		{"custom-tool", "custom-tool"},
	}
	for _, c := range cases {
		cmd := buildHarnessCommand(c.harness, "test-goal", "/tmp/worktree")
		if !strings.Contains(cmd, c.contains) {
			t.Errorf("buildHarnessCommand(%q) should contain %q, got %q", c.harness, c.contains, cmd)
		}
		// All commands should prime with sila context.
		if !strings.Contains(cmd, "sila prime") {
			t.Errorf("buildHarnessCommand(%q) should contain 'sila prime'", c.harness)
		}
	}
}

func TestProcessAlive(t *testing.T) {
	// Test with current process PID (should be alive).
	pid := os.Getpid()
	if !processAlive(pid) {
		t.Error("current process should be alive")
	}
	// Test with a nonexistent PID (should not be alive).
	if processAlive(999999) {
		t.Error("PID 999999 should not be alive")
	}
}

func TestDeleteCrewmate(t *testing.T) {
	mgr, _ := setupTestCrewManager(t)

	cm, err := mgr.Spawn("mimocode", "test-goal-6", "HEAD")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Kill(cm.ID) })
	mgr.Kill(cm.ID)

	// Delete the state file.
	if err := mgr.Delete(cm.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Should no longer be found.
	_, err = mgr.Get(cm.ID)
	if err == nil {
		t.Error("expected error after delete")
	}
}

func TestGenerateCrewID(t *testing.T) {
	id1 := generateCrewID("devin")
	id2 := generateCrewID("devin")
	if id1 == id2 {
		t.Error("IDs should be unique")
	}
	if !strings.HasPrefix(id1, "devin-") {
		t.Errorf("ID should start with 'devin-', got %s", id1)
	}
}

func TestSpawnWithRole(t *testing.T) {
	mgr, _ := setupTestCrewManager(t)

	cmRev, err := mgr.SpawnWithRole("devin", "test-goal-rev", "HEAD", RoleClassReviewer)
	if err != nil {
		t.Fatalf("SpawnWithRole reviewer: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Kill(cmRev.ID) })
	if !cmRev.IsReviewer() {
		t.Errorf("expected IsReviewer=true, got role=%v", cmRev.RoleClass)
	}
	if cmRev.IsProducer() {
		t.Errorf("expected IsProducer=false for reviewer")
	}

	cmProd, err := mgr.Spawn("claude", "test-goal-prod", "HEAD")
	if err != nil {
		t.Fatalf("Spawn producer: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Kill(cmProd.ID) })
	if !cmProd.IsProducer() {
		t.Errorf("expected IsProducer=true, got role=%v", cmProd.RoleClass)
	}
	if cmProd.IsReviewer() {
		t.Errorf("expected IsReviewer=false for producer")
	}

	cmOrch, err := mgr.SpawnWithRole("agy", "test-goal-orch", "HEAD", RoleClassOrchestrator)
	if err != nil {
		t.Fatalf("SpawnWithRole orchestrator: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Kill(cmOrch.ID) })
	if !cmOrch.IsOrchestrator() {
		t.Errorf("expected IsOrchestrator=true, got role=%v", cmOrch.RoleClass)
	}
}

func TestSpawnCrewmateWithAssignedPersonas(t *testing.T) {
	mgr, dir := setupTestCrewManager(t)

	// Create a goal with assigned review squad personas
	goalDir := filepath.Join(dir, "goals", "persona-crew-goal")
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatal(err)
	}
	goalMD := `# Goal: Persona Crew Goal
## Dependencies & Execution Order
- **Mode**: Independent
- **Personas**: accessibility-sentinel, performance-oracle
`
	if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalMD), 0644); err != nil {
		t.Fatal(err)
	}

	cm, err := mgr.Spawn("claude", "persona-crew-goal", "HEAD")
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Kill(cm.ID) })

	// Verify AssignedPersonas on Crewmate
	if len(cm.AssignedPersonas) != 2 || cm.AssignedPersonas[0] != "accessibility-sentinel" || cm.AssignedPersonas[1] != "performance-oracle" {
		t.Fatalf("expected [accessibility-sentinel, performance-oracle], got %v", cm.AssignedPersonas)
	}

	// Verify SILA_CONTEXT.md in the worktree
	ctxPath := filepath.Join(cm.Worktree, "SILA_CONTEXT.md")
	data, err := os.ReadFile(ctxPath)
	if err != nil {
		t.Fatalf("read SILA_CONTEXT.md: %v", err)
	}
	ctxStr := string(data)
	if !strings.Contains(ctxStr, "Assigned Review Squad & Quality Standards") {
		t.Errorf("expected review squad section in SILA_CONTEXT.md: %s", ctxStr)
	}
	if !strings.Contains(ctxStr, "accessibility-sentinel") || !strings.Contains(ctxStr, "performance-oracle") {
		t.Errorf("expected reviewers in SILA_CONTEXT.md: %s", ctxStr)
	}
	if !strings.Contains(ctxStr, "Evaluation Rubric") {
		t.Errorf("expected Evaluation Rubric in SILA_CONTEXT.md: %s", ctxStr)
	}

	// Verify persistence preserves AssignedPersonas
	saved, err := mgr.Get(cm.ID)
	if err != nil {
		t.Fatalf("Get crewmate failed: %v", err)
	}
	if len(saved.AssignedPersonas) != 2 || saved.AssignedPersonas[0] != "accessibility-sentinel" {
		t.Fatalf("saved crewmate missing AssignedPersonas: %v", saved.AssignedPersonas)
	}
}

func TestCrewManager_SetCommandBuilder(t *testing.T) {
	if !tmuxAvailable() {
		t.Skip("tmux required for this spawn behavior")
	}
	mgr, _ := setupTestCrewManager(t)
	var capturedCmd string
	mgr.SetCommandBuilder(func(harness, goalSlug, worktreePath string) string {
		capturedCmd = harness + ":" + goalSlug
		return "sleep 60"
	})

	cm, err := mgr.Spawn("devin", "test-cmd-builder", "HEAD")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Kill(cm.ID) })

	if capturedCmd != "devin:test-cmd-builder" {
		t.Errorf("expected capturedCmd 'devin:test-cmd-builder', got %q", capturedCmd)
	}
}

func TestCrewManager_SetLauncher(t *testing.T) {
	mgr, _ := setupTestCrewManager(t)
	launched := false
	mgr.SetLauncher(func(harness, goalSlug, worktreePath, session, window string) (int, error) {
		launched = true
		return 12345, nil
	})

	cm, err := mgr.Spawn("devin", "test-mock-launcher", "HEAD")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Kill(cm.ID) })

	if !launched {
		t.Error("expected custom launcher to be invoked")
	}
	if cm.PID != 12345 {
		t.Errorf("expected PID 12345, got %d", cm.PID)
	}
}

func TestCrewManager_KillAll(t *testing.T) {
	mgr, _ := setupTestCrewManager(t)

	cm1, err := mgr.Spawn("devin", "goal-1", "HEAD")
	if err != nil {
		t.Fatalf("Spawn 1: %v", err)
	}
	cm2, err := mgr.Spawn("claude", "goal-2", "HEAD")
	if err != nil {
		t.Fatalf("Spawn 2: %v", err)
	}

	if err := mgr.KillAll(); err != nil {
		t.Fatalf("KillAll: %v", err)
	}

	s1, _ := mgr.Get(cm1.ID)
	if s1.Status != StatusKilled {
		t.Errorf("cm1 status = %v, want killed", s1.Status)
	}
	s2, _ := mgr.Get(cm2.ID)
	if s2.Status != StatusKilled {
		t.Errorf("cm2 status = %v, want killed", s2.Status)
	}
}
