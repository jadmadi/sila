package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/render"
)

// Subprocess entrypoint for goals readiness CLI execution testing
func TestGoalsReadinessSubprocess(t *testing.T) {
	if os.Getenv("GO_WANT_GOALS_READINESS_SUBPROCESS") != "1" {
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

func runReadinessCLI(t *testing.T, targetDir string, jsonOut bool, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestGoalsReadinessSubprocess$")
	cmd.Env = append(os.Environ(),
		"GO_WANT_GOALS_READINESS_SUBPROCESS=1",
		"SUBPROCESS_TARGET_DIR="+targetDir,
		"SUBPROCESS_ARGS="+strings.Join(args, "||"),
	)
	if jsonOut {
		cmd.Env = append(cmd.Env, "SUBPROCESS_JSON=1")
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
			t.Fatalf("run command: %v", err)
		}
	}
	return stdout.String(), stderr.String(), exitCode
}

func setupReadinessTestRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := setupWorkPackageTestRepo(t)
	slug := "readiness-test-goal"

	// Create referenced source file
	enginePath := filepath.Join(dir, "engine.go")
	engineSrc := "package engine\n\nfunc Start() {}\n"
	_ = os.WriteFile(enginePath, []byte(engineSrc), 0644)
	h := sha256.Sum256([]byte(engineSrc))
	engineSHA := hex.EncodeToString(h[:])

	// Create goal package
	goalDir := filepath.Join(dir, "goals", slug)
	_ = os.MkdirAll(goalDir, 0755)
	_ = os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte("# Goal: "+slug+"\n- **Tier**: roadmap\n- **Shape**: ship\n"), 0644)
	_ = os.WriteFile(filepath.Join(goalDir, "facts.md"), []byte("# Facts\n"), 0644)
	_ = os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte("# Plan\n- [ ] Task 1\n"), 0644)

	wp := &goals.WorkPackage{
		SchemaVersion:       goals.WorkPackageSchemaVersion,
		GoalSlug:            slug,
		Revision:            1,
		Purpose:             "Implement readiness verification",
		Constraints:         []string{"cli-only"},
		NonGoals:            []string{"network"},
		UnresolvedDecisions: []string{},
		Tasks: []goals.TaskContract{
			{
				ID:        "T-001",
				Title:     "Initialize engine",
				Context:   "Core engine pipeline",
				Why:       "Enable acceleration",
				DependsOn: []string{},
				Files: goals.TaskFiles{
					Edit:   []string{"engine.go"},
					Create: []string{"engine_new.go"},
				},
				References: []goals.Reference{
					{
						Path:    "engine.go",
						Symbol:  "Start",
						Excerpt: "func Start()",
						SHA256:  engineSHA,
					},
				},
				Decisions: []goals.Decision{
					{ID: "D-001", Chosen: "stdlib", Rationale: "zero deps", Rejected: []string{}},
				},
				Steps: []goals.Step{
					{ID: "S-001", Action: "invoke Start", Expected: "engine starts", OnFailure: "fail"},
				},
				Checks: []goals.Check{
					{
						ID:               "C-001",
						Executable:       "go",
						Args:             []string{"test"},
						TimeoutSeconds:   10,
						ExpectedExitCode: 0,
						Scope:            goals.ScopeUnit,
						Capability:       goals.CapabilityLocal,
						Assertions: []goals.Assertion{
							{Kind: goals.AssertionGoTestPass, Target: "TestEngine", Expected: true},
						},
					},
				},
				AcceptanceIDs: []string{"AC-001"},
				ReturnFormat:  goals.TaskResultReturnFormat,
			},
		},
		Acceptance: []goals.AcceptanceCase{
			{
				ID:            "AC-001",
				Requirement:   "Start initiates engine",
				Given:         "Config",
				Action:        "Invoke",
				Expected:      "Clean start",
				RequiredScope: goals.ScopeUnit,
				Mandatory:     true,
				CheckIDs:      []string{"C-001"},
			},
		},
	}
	if err := goals.SaveWorkPackage(dir, slug, wp); err != nil {
		t.Fatalf("SaveWorkPackage: %v", err)
	}

	return dir, slug
}

// TestGoalsReadinessCLI tests CLI readiness reporting, exit codes, and review import.
func TestGoalsReadinessCLI(t *testing.T) {
	dir, slug := setupReadinessTestRepo(t)

	// 1. Initial check: missing spec-review produces exit code 1 and SPEC_REVIEW_MISSING
	stdout, _, exitCode := runReadinessCLI(t, dir, true, "readiness", slug, "--json")
	if exitCode != 1 {
		t.Fatalf("expected exit code 1 for unreviewed goal, got %d. Output: %s", exitCode, stdout)
	}

	// AC-011: --json emits valid JSON on stdout
	var rep ReadinessReport
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatalf("failed to parse JSON from stdout: %v\nOutput: %s", err, stdout)
	}
	if rep.Ready {
		t.Errorf("expected ready=false without review")
	}
	foundReviewMissing := false
	for _, d := range rep.Diagnostics {
		if d.Code == goals.DiagSpecReviewMissing {
			foundReviewMissing = true
			break
		}
	}
	if !foundReviewMissing {
		t.Errorf("expected SPEC_REVIEW_MISSING in diagnostics: %+v", rep.Diagnostics)
	}

	// 2. AC-012: Review import with mismatched digest fails without mutating goal (exit 1)
	mismatchedReview := goals.SpecReview{
		SchemaVersion: goals.SpecReviewSchemaVersion,
		SpecDigest:    "0000000000000000000000000000000000000000000000000000000000000000",
		Reviewer:      "planner-agent",
		ReviewedAt:    time.Now().UTC().Format(time.RFC3339),
		Verdict:       goals.VerdictReady,
		Shortcuts: []goals.ShortcutCounter{
			{IncorrectImplementation: "impl 1", ExcludedByIDs: []string{"T-001"}, Resolution: "res 1"},
			{IncorrectImplementation: "impl 2", ExcludedByIDs: []string{"T-001"}, Resolution: "res 2"},
			{IncorrectImplementation: "impl 3", ExcludedByIDs: []string{"T-001"}, Resolution: "res 3"},
		},
		UnresolvedBlockers: []string{},
	}
	mismatchedPath := filepath.Join(dir, "mismatched-review.json")
	data, _ := json.Marshal(mismatchedReview)
	_ = os.WriteFile(mismatchedPath, data, 0644)

	stdout, _, exitCode = runReadinessCLI(t, dir, true, "readiness", slug, "--review="+mismatchedPath, "--json")
	if exitCode != 1 {
		t.Fatalf("expected exit code 1 for mismatched review import, got %d", exitCode)
	}
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	foundStale := false
	for _, d := range rep.Diagnostics {
		if d.Code == goals.DiagSpecReviewStale {
			foundStale = true
			break
		}
	}
	if !foundStale {
		t.Errorf("expected SPEC_REVIEW_STALE in diagnostics for mismatched digest: %+v", rep.Diagnostics)
	}

	// Verify goal was not mutated (spec-review.json does not exist on disk)
	if goals.FileExists(filepath.Join(dir, "goals", slug, "spec-review.json")) {
		t.Errorf("spec-review.json was saved despite mismatched digest!")
	}

	// 3. Valid review import succeeds (exit 0)
	wp, _ := goals.LoadWorkPackage(dir, slug)
	validDigest, _ := goals.WorkPackageDigest(wp)
	validReview := mismatchedReview
	validReview.SpecDigest = validDigest

	validReviewPath := filepath.Join(dir, "valid-review.json")
	validData, _ := json.Marshal(validReview)
	_ = os.WriteFile(validReviewPath, validData, 0644)

	stdout, _, exitCode = runReadinessCLI(t, dir, true, "readiness", slug, "--review="+validReviewPath, "--json")
	if exitCode != 0 {
		t.Fatalf("expected exit code 0 for valid review import, got %d. Output: %s", exitCode, stdout)
	}
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if !rep.Ready || len(rep.Diagnostics) != 0 {
		t.Errorf("expected ready=true with 0 diagnostics, got: %+v", rep)
	}

	// 4. Subsequent readiness read without flags is non-mutating and exits 0
	stdout, _, exitCode = runReadinessCLI(t, dir, true, "readiness", slug, "--json")
	if exitCode != 0 {
		t.Fatalf("expected exit code 0 for ready goal read, got %d", exitCode)
	}

	// 5. Usage error on missing slug exits 2
	_, _, exitCode = runReadinessCLI(t, dir, false, "readiness")
	if exitCode != 2 {
		t.Fatalf("expected exit code 2 on missing slug, got %d", exitCode)
	}
}
