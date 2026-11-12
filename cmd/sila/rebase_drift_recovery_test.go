package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/assess"
	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/render"
)

func runGateSubprocessHelper() {
	cwd := os.Getenv("SUBPROCESS_CWD")
	if cwd != "" {
		if err := os.Chdir(cwd); err != nil {
			fmt.Fprintf(os.Stderr, "chdir error: %v\n", err)
			os.Exit(2)
		}
	}
	jsonOut := os.Getenv("SUBPROCESS_JSON") == "1"
	preCommit := os.Getenv("SUBPROCESS_PRECOMMIT") == "1"
	ui := render.New(os.Stdout, false)
	if preCommit {
		runGatePreCommit(ui, jsonOut, false, false, false, false, false, false)
	} else {
		runGateDiscipline(ui, jsonOut, false, false, false, false)
	}
	os.Exit(0)
}

func initRebaseDriftTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	gitRun := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\nOutput: %s", args, err, string(out))
		}
	}

	gitRun("init", "-b", "main")
	gitRun("config", "user.name", "Test User")
	gitRun("config", "user.email", "test@example.com")

	// Initial commit on main
	file1 := filepath.Join(dir, "README.md")
	if err := os.WriteFile(file1, []byte("# Test Repo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitRun("add", "README.md")
	gitRun("commit", "-m", "chore: initial commit")

	// Branch out to feature branch
	gitRun("checkout", "-b", "feat/test-drift")
	return dir
}

func setupActiveGoalCharter(t *testing.T, dir, slug string) {
	t.Helper()
	goalDir := filepath.Join(dir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatal(err)
	}
	goalMd := fmt.Sprintf("# Goal: %s\n\n## Files to Touch\n- feature.go\n", slug)
	if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalMd), 0644); err != nil {
		t.Fatal(err)
	}
	charterDir := filepath.Join(dir, ".sila", "charters")
	if err := os.MkdirAll(charterDir, 0755); err != nil {
		t.Fatal(err)
	}
	charter := &goals.GoalCharter{
		GoalSlug:    slug,
		Agent:       "test-agent",
		SessionID:   "session-1",
		AllowPaths:  []string{"feature.go"},
		CreatedAt:   time.Now().UTC(),
		MaxDuration: 30 * time.Minute,
	}
	if err := goals.SaveCharter(dir, charter); err != nil {
		t.Fatalf("save charter: %v", err)
	}
}

func runGateCLI(t *testing.T, targetDir string, jsonOut bool, preCommit bool) (string, string, int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestGateRebaseDriftErrorsFailClosed$")
	cmd.Env = append(os.Environ(),
		"GO_WANT_GATE_REBASE_DRIFT_SUBPROCESS=1",
		"SUBPROCESS_CWD="+targetDir,
	)
	if jsonOut {
		cmd.Env = append(cmd.Env, "SUBPROCESS_JSON=1")
	}
	if preCommit {
		cmd.Env = append(cmd.Env, "SUBPROCESS_PRECOMMIT=1")
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("runGateCLI failed to execute: %v", err)
		}
	}
	return stdout.String(), stderr.String(), exitCode
}

func TestGateRebaseDriftErrorsFailClosed(t *testing.T) {
	if os.Getenv("GO_WANT_GATE_REBASE_DRIFT_SUBPROCESS") == "1" {
		runGateSubprocessHelper()
		return
	}

	t.Run("NoActiveChartersPasses", func(t *testing.T) {
		dir := initRebaseDriftTestRepo(t)
		check := gateRebaseDriftSentry(dir)
		if check.Status != "pass" {
			t.Fatalf("expected pass with no active charters, got %s: %s", check.Status, check.Detail)
		}
		if check.Name != "rebase-drift" {
			t.Fatalf("expected check name rebase-drift, got %s", check.Name)
		}
	})

	t.Run("MalformedBaselineFailsClosed", func(t *testing.T) {
		dir := initRebaseDriftTestRepo(t)
		slug := "demo"

		// Add modified feature file and commit so diff exists against main
		f := filepath.Join(dir, "feature.go")
		if err := os.WriteFile(f, []byte("package main\n\nfunc Run() int { return 1 }\n"), 0644); err != nil {
			t.Fatal(err)
		}
		gitRun := func(args ...string) {
			cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v failed: %v\nOutput: %s", args, err, string(out))
			}
		}
		gitRun("add", "feature.go")
		gitRun("commit", "-m", "feat: implement demo")

		setupActiveGoalCharter(t, dir, slug)

		// Write malformed baseline JSON
		baselinePath := filepath.Join(dir, "goals", slug, "baseline.json")
		if err := os.WriteFile(baselinePath, []byte("{invalid-json-data"), 0644); err != nil {
			t.Fatal(err)
		}

		// 1. Direct unit check assertion
		check := gateRebaseDriftSentry(dir)
		if check.Status != "fail" {
			t.Fatalf("expected fail status on malformed baseline, got: %s", check.Status)
		}
		if !strings.Contains(check.Detail, slug) {
			t.Errorf("expected failure detail to name goal slug %q, got: %s", slug, check.Detail)
		}
		if !strings.Contains(check.Detail, "parse baseline.json") {
			t.Errorf("expected failure detail to state parse error, got: %s", check.Detail)
		}

		// 2. Subprocess CLI gate execution
		stdout, _, exitCode := runGateCLI(t, dir, true, true)
		if exitCode == 0 {
			t.Fatalf("expected nonzero exit code from gate CLI on malformed baseline, got %d", exitCode)
		}
		var rep gateReport
		if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
			t.Fatalf("failed to parse JSON gate output: %v\nOutput: %s", err, stdout)
		}
		if rep.Clean {
			t.Fatalf("expected gate clean=false on malformed baseline")
		}
		foundCheck := false
		for _, c := range rep.Checks {
			if c.Name == "rebase-drift" {
				foundCheck = true
				if c.Status != "fail" {
					t.Errorf("expected rebase-drift check status fail in gate report, got: %s", c.Status)
				}
				if !strings.Contains(c.Detail, slug) {
					t.Errorf("expected gate report detail to name goal slug %q, got: %s", slug, c.Detail)
				}
			}
		}
		if !foundCheck {
			t.Errorf("expected rebase-drift check in gate report checks list")
		}
	})

	t.Run("CharterReadFailureFailsClosed", func(t *testing.T) {
		dir := initRebaseDriftTestRepo(t)
		// Corrupt charters by creating .sila/charters as a regular file
		silaDir := filepath.Join(dir, ".sila")
		if err := os.MkdirAll(silaDir, 0755); err != nil {
			t.Fatal(err)
		}
		chartersFile := filepath.Join(silaDir, "charters")
		if err := os.WriteFile(chartersFile, []byte("regular file, not directory"), 0644); err != nil {
			t.Fatal(err)
		}

		// 1. Direct unit check assertion
		check := gateRebaseDriftSentry(dir)
		if check.Status != "fail" {
			t.Fatalf("expected fail status on charter directory read failure, got: %s", check.Status)
		}
		if !strings.Contains(check.Detail, "failed to list active charters") {
			t.Errorf("expected detail to state failed to list active charters, got: %s", check.Detail)
		}

		// 2. Subprocess CLI gate execution
		stdout, _, exitCode := runGateCLI(t, dir, true, true)
		if exitCode == 0 {
			t.Fatalf("expected nonzero exit code from gate CLI on charter read failure, got %d", exitCode)
		}
		var rep gateReport
		if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
			t.Fatalf("failed to parse JSON gate output: %v\nOutput: %s", err, stdout)
		}
		if rep.Clean {
			t.Fatalf("expected gate clean=false on charter read failure")
		}
	})

	t.Run("GitErrorFailsClosed", func(t *testing.T) {
		dir := initRebaseDriftTestRepo(t)
		slug := "git-fail-goal"

		f := filepath.Join(dir, "feature.go")
		if err := os.WriteFile(f, []byte("package main\n\nfunc Run() int { return 2 }\n"), 0644); err != nil {
			t.Fatal(err)
		}
		gitRun := func(args ...string) {
			cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v failed: %v\nOutput: %s", args, err, string(out))
			}
		}
		gitRun("add", "feature.go")
		gitRun("commit", "-m", "feat: commit before git corruption")

		setupActiveGoalCharter(t, dir, slug)

		// Corrupt git repo by renaming .git to corrupt git operations
		gitDir := filepath.Join(dir, ".git")
		corruptDir := filepath.Join(dir, ".git_corrupt")
		if err := os.Rename(gitDir, corruptDir); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Rename(corruptDir, gitDir) }()

		// 1. Direct unit check assertion
		check := gateRebaseDriftSentry(dir)
		if check.Status != "fail" {
			t.Fatalf("expected fail status on git error, got: %s", check.Status)
		}
		if !strings.Contains(check.Detail, slug) {
			t.Errorf("expected failure detail to name goal slug %q, got: %s", slug, check.Detail)
		}

		// 2. Subprocess CLI gate execution
		_, _, exitCode := runGateCLI(t, dir, false, true)
		if exitCode == 0 {
			t.Fatalf("expected nonzero exit code on corrupted git repo")
		}
	})

	t.Run("UnwritableBaselineOnRetestFailsClosed", func(t *testing.T) {
		dir := initRebaseDriftTestRepo(t)
		slug := "write-fail-goal"

		gitRun := func(args ...string) {
			cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v failed: %v\nOutput: %s", args, err, string(out))
			}
		}

		// Add initial feature and commit
		f := filepath.Join(dir, "feature.go")
		if err := os.WriteFile(f, []byte("package main\n\nfunc Run() int { return 10 }\n"), 0644); err != nil {
			t.Fatal(err)
		}
		gitRun("add", "feature.go")
		gitRun("commit", "-m", "feat: initial commit for write-fail-goal")

		// Add go.mod and passing test file so go test succeeds during re-test
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testrepo\n\ngo 1.22\n"), 0644); err != nil {
			t.Fatal(err)
		}
		testFile := filepath.Join(dir, "feature_test.go")
		if err := os.WriteFile(testFile, []byte("package main\n\nimport \"testing\"\n\nfunc TestRun(t *testing.T) {}\n"), 0644); err != nil {
			t.Fatal(err)
		}
		gitRun("add", "go.mod", "feature_test.go")
		gitRun("commit", "-m", "chore: add go.mod and tests")

		setupActiveGoalCharter(t, dir, slug)

		// Record initial baseline
		repInit, err := assess.CheckRebaseDrift(context.Background(), dir, slug, "main", nil)
		if err != nil {
			t.Fatalf("initial CheckRebaseDrift failed: %v", err)
		}
		if repInit.BaselinePatch == "" {
			t.Fatalf("expected non-empty initial baseline patch")
		}

		// Configure test runbook in AGENTS.md that specifies go test
		agentsMd := "# AGENTS\n\n## Verification\n- Run: `go test ./...`\n"
		if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(agentsMd), 0644); err != nil {
			t.Fatal(err)
		}

		// Now introduce semantic code change (drift)
		if err := os.WriteFile(f, []byte("package main\n\nfunc Run() int { return 20 }\n"), 0644); err != nil {
			t.Fatal(err)
		}
		gitRun("add", "feature.go")
		gitRun("commit", "-m", "feat: drift commit")

		// Make baseline file read-only so StoreBaselinePatch fails
		baselinePath := assess.BaselinePatchPath(dir, slug)
		if err := os.Chmod(baselinePath, 0400); err != nil {
			t.Fatalf("chmod baseline file: %v", err)
		}
		defer func() { _ = os.Chmod(baselinePath, 0644) }()

		// 1. Direct unit check assertion
		check := gateRebaseDriftSentry(dir)
		if check.Status != "fail" {
			t.Fatalf("expected fail status on unwritable baseline target, got: %s", check.Status)
		}
		if !strings.Contains(check.Detail, slug) {
			t.Errorf("expected failure detail to name goal slug %q, got: %s", slug, check.Detail)
		}
		if !strings.Contains(check.Detail, "store baseline patch") {
			t.Errorf("expected failure detail to mention store baseline patch, got: %s", check.Detail)
		}

		// 2. Subprocess CLI gate execution
		stdout, _, exitCode := runGateCLI(t, dir, true, true)
		if exitCode == 0 {
			t.Fatalf("expected nonzero exit code from gate CLI on unwritable baseline target, got %d", exitCode)
		}
		var rep gateReport
		if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
			t.Fatalf("failed to parse JSON gate output: %v\nOutput: %s", err, stdout)
		}
		if rep.Clean {
			t.Fatalf("expected gate clean=false on unwritable baseline target")
		}
	})
}
