package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestCLIGoalsAttribute(t *testing.T) {
	tmp := t.TempDir()

	// Initialize git repo in tmp
	_ = exec.Command("git", "-C", tmp, "init").Run()
	_ = exec.Command("git", "-C", tmp, "config", "user.email", "test@example.com").Run()
	_ = exec.Command("git", "-C", tmp, "config", "user.name", "Test User").Run()

	dummyFile := filepath.Join(tmp, "work.go")
	_ = os.WriteFile(dummyFile, []byte("package work\n"), 0644)
	_ = exec.Command("git", "-C", tmp, "add", "work.go").Run()
	_ = exec.Command("git", "-C", tmp, "commit", "-m", "feat: swarm worker implementation").Run()

	shaCmd := exec.Command("git", "-C", tmp, "rev-parse", "HEAD")
	shaOut, err := shaCmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse: %v", err)
	}
	commitSHA := strings.TrimSpace(string(shaOut))

	// Create goal
	slug := "swarm-worker-task"
	_, err = goals.CreateGoal(goals.CreateGoalParams{
		RootDir:     tmp,
		Slug:        slug,
		Title:       "Swarm Worker Task",
		Description: "Post-swarm attribution test",
		Tasks:       []string{"Phase 1"},
	})
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}

	ui := render.New(nil, false)
	dbPath := filepath.Join(tmp, "knowledge.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	st.Close()

	// 1. Run attribute via CLI runner
	attrOut := captureStdout(t, func() {
		runGoals(ui, dbPath, tmp, "antigravity", "", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, false, false, false, false, false, "", "", "", false, []string{"attribute", slug, "--commit=" + commitSHA})
	})
	if !strings.Contains(attrOut, "successfully attributed to commit") {
		t.Errorf("expected success attribution message, got: %s", attrOut)
	}

	// 2. Run JSON output
	attrJSON := captureStdout(t, func() {
		runGoals(ui, dbPath, tmp, "antigravity", "", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, true, false, false, false, false, "", "", "", false, []string{"attribute", slug, "--commit=" + commitSHA})
	})
	if !strings.Contains(attrJSON, `"status": "implemented"`) || !strings.Contains(attrJSON, `"success": true`) {
		t.Errorf("expected JSON success payload, got: %s", attrJSON)
	}

	// 3. Verify retrospective.json exists
	retroPath := filepath.Join(tmp, "goals", slug, "retrospective.json")
	if _, err := os.Stat(retroPath); err != nil {
		t.Errorf("retrospective.json does not exist: %v", err)
	}
}
