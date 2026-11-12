package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/render"
)

// Subprocess entrypoint for CLI execution testing
func TestCreateGoalWorkPackageSubprocess(t *testing.T) {
	if os.Getenv("GO_WANT_GOALS_WORK_PACKAGE_SUBPROCESS") != "1" {
		return
	}
	targetDir := os.Getenv("SUBPROCESS_TARGET_DIR")
	rawArgs := os.Getenv("SUBPROCESS_ARGS")
	actionArgs := strings.Split(rawArgs, "||")
	jsonOut := os.Getenv("SUBPROCESS_JSON") == "1"

	ui := render.New(os.Stdout, false)
	runGoals(ui, "", targetDir, "", "", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, jsonOut, false, false, false, false, "", "", "", false, actionArgs)
	os.Exit(0)
}

func setupWorkPackageTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	cmd := exec.Command("git", "init", "-b", "main")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init failed: %v\n%s", err, string(out))
	}

	_ = exec.Command("git", "-C", dir, "config", "user.name", "Sila Tester").Run()
	_ = exec.Command("git", "-C", dir, "config", "user.email", "test@sila.local").Run()

	readme := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readme, []byte("# Test Repo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_ = exec.Command("git", "-C", dir, "add", "README.md").Run()
	_ = exec.Command("git", "-C", dir, "commit", "-m", "chore: initial commit").Run()

	goalsDir := filepath.Join(dir, "goals")
	if err := os.MkdirAll(goalsDir, 0755); err != nil {
		t.Fatal(err)
	}
	goalsReadme := filepath.Join(goalsDir, "README.md")
	_ = os.WriteFile(goalsReadme, []byte("# Goals Registry\n"), 0644)
	_ = exec.Command("git", "-C", dir, "add", "goals/").Run()
	_ = exec.Command("git", "-C", dir, "commit", "-m", "docs(goals): init registry").Run()

	return dir
}

func makeTestWorkPackage(slug string) *goals.WorkPackage {
	return &goals.WorkPackage{
		SchemaVersion: goals.WorkPackageSchemaVersion,
		GoalSlug:      slug,
		Revision:      1,
		Purpose:       "Verify work package handling in CLI and MCP",
		Constraints:   []string{"Strict pure Go stdlib only", "Zero network calls"},
		NonGoals:      []string{"No external database"},
		Tasks: []goals.TaskContract{
			{
				ID:        "T-001",
				Title:     "Define interfaces",
				Context:   "Context for T-001",
				Why:       "Rationale for T-001",
				DependsOn: []string{},
				Files: goals.TaskFiles{
					Read:   []string{"README.md"},
					Edit:   []string{"README.md"},
					Create: []string{},
					Forbid: []string{".git/*"},
				},
				References: []goals.Reference{
					{
						Path:    "README.md",
						Symbol:  "Heading",
						Excerpt: "# Test Repo",
						SHA256:  "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
					},
				},
				Decisions: []goals.Decision{
					{
						ID:        "D-001",
						Chosen:    "Decision 1",
						Rationale: "Reason 1",
						Rejected:  []string{"Option B"},
					},
				},
				Steps: []goals.Step{
					{
						ID:        "S-001",
						Action:    "Run check",
						Expected:  "Pass",
						OnFailure: "Fix",
					},
				},
				Interfaces:  []string{"Interface 1"},
				Constraints: []string{"Constraint 1"},
				Examples: []goals.Example{
					{
						Input:       "input",
						Output:      "output",
						Explanation: "explanation",
					},
				},
				AcceptanceIDs: []string{"AC-001"},
				Checks: []goals.Check{
					{
						ID:               "C-001",
						Executable:       "true",
						Args:             []string{},
						Cwd:              "cli",
						TimeoutSeconds:   30,
						ExpectedExitCode: 0,
						Scope:            goals.ScopeUnit,
						Assertions: []goals.Assertion{
							{
								Kind:     goals.AssertionStdoutExact,
								Target:   "stdout",
								Expected: "ok",
							},
						},
						Capability: goals.CapabilityLocal,
					},
				},
				StopConditions: []string{"blocked"},
				ReturnFormat:   goals.TaskResultReturnFormat,
			},
			{
				ID:        "T-002",
				Title:     "Implement logic",
				Context:   "Context for T-002",
				Why:       "Rationale for T-002",
				DependsOn: []string{"T-001"},
				Files: goals.TaskFiles{
					Read:   []string{"README.md"},
					Edit:   []string{"README.md"},
					Create: []string{},
					Forbid: []string{".git/*"},
				},
				References: []goals.Reference{},
				Decisions:  []goals.Decision{},
				Steps: []goals.Step{
					{
						ID:        "S-002",
						Action:    "Run check 2",
						Expected:  "Pass",
						OnFailure: "Fix",
					},
				},
				Interfaces:  []string{"Interface 2"},
				Constraints: []string{"Constraint 2"},
				Examples: []goals.Example{
					{
						Input:       "input2",
						Output:      "output2",
						Explanation: "explanation2",
					},
				},
				AcceptanceIDs: []string{"AC-002"},
				Checks: []goals.Check{
					{
						ID:               "C-002",
						Executable:       "true",
						Args:             []string{},
						Cwd:              "cli",
						TimeoutSeconds:   30,
						ExpectedExitCode: 0,
						Scope:            goals.ScopeUnit,
						Assertions: []goals.Assertion{
							{
								Kind:     goals.AssertionStdoutExact,
								Target:   "stdout",
								Expected: "ok",
							},
						},
						Capability: goals.CapabilityLocal,
					},
				},
				StopConditions: []string{"blocked"},
				ReturnFormat:   goals.TaskResultReturnFormat,
			},
		},
		Acceptance: []goals.AcceptanceCase{
			{
				ID:            "AC-001",
				Requirement:   "Req 1",
				Given:         "Given 1",
				Action:        "Action 1",
				Expected:      "Expected 1",
				RequiredScope: goals.ScopeUnit,
				Mandatory:     true,
				CheckIDs:      []string{"C-001"},
			},
			{
				ID:            "AC-002",
				Requirement:   "Req 2",
				Given:         "Given 2",
				Action:        "Action 2",
				Expected:      "Expected 2",
				RequiredScope: goals.ScopeUnit,
				Mandatory:     true,
				CheckIDs:      []string{"C-002"},
			},
		},
		DependencyConditions: []goals.DependencyCondition{},
		UnresolvedDecisions:  []string{},
		RetiredTaskIDs:       []string{},
		RetiredAcceptanceIDs: []string{},
	}
}

func runSubprocessGoals(targetDir string, jsonOut bool, args ...string) ([]byte, error) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestCreateGoalWorkPackageSubprocess$")
	jsonEnv := "0"
	if jsonOut {
		jsonEnv = "1"
	}
	cmd.Env = append(os.Environ(),
		"GO_WANT_GOALS_WORK_PACKAGE_SUBPROCESS=1",
		"SUBPROCESS_TARGET_DIR="+targetDir,
		"SUBPROCESS_ARGS="+strings.Join(args, "||"),
		"SUBPROCESS_JSON="+jsonEnv,
	)
	return cmd.CombinedOutput()
}

func TestCreateGoalWorkPackage(t *testing.T) {
	t.Run("CreateWithValidWorkPackage", func(t *testing.T) {
		repoDir := setupWorkPackageTestRepo(t)

		wp := makeTestWorkPackage("pkg-feature")
		wpData, err := json.MarshalIndent(wp, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		wpFile := filepath.Join(repoDir, "wp.json")
		if err := os.WriteFile(wpFile, wpData, 0644); err != nil {
			t.Fatal(err)
		}

		out, err := runSubprocessGoals(repoDir, true, "create", "pkg-feature", "--work-package=wp.json", "--files=README.md", "--json")
		if err != nil {
			t.Fatalf("expected success creating goal with work package, got err: %v\nOutput: %s", err, string(out))
		}

		var resp map[string]any
		if err := json.Unmarshal(out, &resp); err != nil {
			t.Fatalf("failed to parse JSON response: %v\nOutput: %s", err, string(out))
		}
		if resp["created"] != true {
			t.Fatalf("expected created: true, got %+v", resp)
		}

		goalDir := filepath.Join(repoDir, "goals", "pkg-feature")

		// Verify goal.md contains reference to work-package.json
		goalMD, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
		if err != nil {
			t.Fatalf("read goal.md: %v", err)
		}
		if !strings.Contains(string(goalMD), "work-package.json") {
			t.Errorf("expected goal.md to reference work-package.json, got:\n%s", string(goalMD))
		}

		// Verify plan.md lists T-001 and T-002
		planMD, err := os.ReadFile(filepath.Join(goalDir, "plan.md"))
		if err != nil {
			t.Fatalf("read plan.md: %v", err)
		}
		planStr := string(planMD)
		if !strings.Contains(planStr, "T-001") || !strings.Contains(planStr, "T-002") {
			t.Errorf("expected plan.md to list T-001 and T-002, got:\n%s", planStr)
		}

		// Verify work-package.json exists and decodes cleanly
		wpDisk, err := os.ReadFile(filepath.Join(goalDir, "work-package.json"))
		if err != nil {
			t.Fatalf("read work-package.json: %v", err)
		}
		decodedWP, err := goals.DecodeWorkPackage(wpDisk)
		if err != nil {
			t.Fatalf("decode persisted work-package.json: %v", err)
		}
		if decodedWP.GoalSlug != "pkg-feature" || len(decodedWP.Tasks) != 2 {
			t.Errorf("unexpected decoded work package content: %+v", decodedWP)
		}
	})

	t.Run("CreateWithInvalidWorkPackageFailsAndNoDir", func(t *testing.T) {
		repoDir := setupWorkPackageTestRepo(t)

		badWP := `{"schema_version":"sila/work-package/v1","goal_slug":"bad-slug","purpose":"","tasks":[]}`
		badFile := filepath.Join(repoDir, "bad.json")
		if err := os.WriteFile(badFile, []byte(badWP), 0644); err != nil {
			t.Fatal(err)
		}

		out, err := runSubprocessGoals(repoDir, false, "create", "bad-slug", "--work-package=bad.json")
		if err == nil {
			t.Fatalf("expected non-zero exit code for invalid work package, got success. Output:\n%s", string(out))
		}

		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 0 {
			t.Fatalf("expected non-zero exit code, got 0")
		}

		// Verify no partial goal directory created (AC-011)
		goalDir := filepath.Join(repoDir, "goals", "bad-slug")
		if _, statErr := os.Stat(goalDir); !os.IsNotExist(statErr) {
			t.Errorf("AC-011 violation: partial goal directory %s was created on validation failure", goalDir)
		}
	})

	t.Run("ConflictTasksAndWorkPackageFails", func(t *testing.T) {
		repoDir := setupWorkPackageTestRepo(t)

		wp := makeTestWorkPackage("pkg-feature")
		wpData, _ := json.MarshalIndent(wp, "", "  ")
		wpFile := filepath.Join(repoDir, "wp.json")
		_ = os.WriteFile(wpFile, wpData, 0644)

		out, err := runSubprocessGoals(repoDir, false, "create", "pkg-feature", "--work-package=wp.json", "--tasks=Phase 1: Conflict")
		if err == nil {
			t.Fatalf("expected failure when both --work-package and --tasks provided, got success. Output:\n%s", string(out))
		}

		goalDir := filepath.Join(repoDir, "goals", "pkg-feature")
		if _, statErr := os.Stat(goalDir); !os.IsNotExist(statErr) {
			t.Errorf("partial goal directory %s was created on conflicting parameters", goalDir)
		}
	})

	t.Run("MismatchedGoalSlugFails", func(t *testing.T) {
		repoDir := setupWorkPackageTestRepo(t)

		wp := makeTestWorkPackage("pkg-feature")
		wpData, _ := json.MarshalIndent(wp, "", "  ")
		wpFile := filepath.Join(repoDir, "wp.json")
		_ = os.WriteFile(wpFile, wpData, 0644)

		// package has goal_slug "pkg-feature", but CLI requests "other-feature"
		out, err := runSubprocessGoals(repoDir, false, "create", "other-feature", "--work-package=wp.json")
		if err == nil {
			t.Fatalf("expected failure on mismatched goal_slug, got success. Output:\n%s", string(out))
		}

		goalDir := filepath.Join(repoDir, "goals", "other-feature")
		if _, statErr := os.Stat(goalDir); !os.IsNotExist(statErr) {
			t.Errorf("partial goal directory %s was created on mismatched slug", goalDir)
		}
	})

	t.Run("LegacyTasksStillWork", func(t *testing.T) {
		repoDir := setupWorkPackageTestRepo(t)

		out, err := runSubprocessGoals(repoDir, true, "create", "legacy-feature", "--files=README.md", "--tasks=Phase 1: Task A; Phase 2: Task B", "--json")
		if err != nil {
			t.Fatalf("expected success creating goal with legacy tasks, got: %v\nOutput: %s", err, string(out))
		}

		goalDir := filepath.Join(repoDir, "goals", "legacy-feature")
		planMD, err := os.ReadFile(filepath.Join(goalDir, "plan.md"))
		if err != nil {
			t.Fatalf("read plan.md: %v", err)
		}
		if !strings.Contains(string(planMD), "Phase 1") {
			t.Errorf("expected plan.md to contain legacy tasks, got:\n%s", string(planMD))
		}

		// Ensure work-package.json does NOT exist
		wpPath := filepath.Join(goalDir, "work-package.json")
		if _, statErr := os.Stat(wpPath); !os.IsNotExist(statErr) {
			t.Errorf("expected no work-package.json for legacy goal, but file exists")
		}
	})
}
