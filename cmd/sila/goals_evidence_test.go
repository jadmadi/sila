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

// Subprocess entrypoint for goals evidence CLI execution testing
func TestGoalsEvidenceSubprocess(t *testing.T) {
	if os.Getenv("GO_WANT_GOALS_EVIDENCE_SUBPROCESS") != "1" {
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

func runEvidenceCLI(t *testing.T, targetDir string, jsonOut bool, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestGoalsEvidenceSubprocess$")
	cmd.Env = append(os.Environ(),
		"GO_WANT_GOALS_EVIDENCE_SUBPROCESS=1",
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
			t.Fatalf("runEvidenceCLI failed to execute: %v", err)
		}
	}
	return stdout.String(), stderr.String(), exitCode
}

func setupEvidenceCLIFixture(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	slug := "evidence-cli-goal"

	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\nOutput: %s", args, err, string(out))
		}
	}

	run("init", "-b", "main")
	run("config", "user.name", "CLI Tester")
	run("config", "user.email", "clitester@sila.local")

	coreSrc := "package main\n\nfunc Answer() int { return 42 }\n"
	if err := os.WriteFile(filepath.Join(dir, "answer.go"), []byte(coreSrc), 0644); err != nil {
		t.Fatal(err)
	}
	hCore := sha256.Sum256([]byte(coreSrc))
	coreSHA := hex.EncodeToString(hCore[:])

	run("add", "answer.go")
	run("commit", "-m", "init answer")

	goalDir := filepath.Join(dir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatal(err)
	}

	pkg := &goals.WorkPackage{
		SchemaVersion: goals.WorkPackageSchemaVersion,
		GoalSlug:      slug,
		Revision:      1,
		Purpose:       "Verify goals evidence CLI commands",
		Constraints:   []string{"pure-Go stdlib"},
		NonGoals:      []string{"no external deps"},
		Tasks: []goals.TaskContract{
			{
				ID:        "T-001",
				Title:     "Compute Answer",
				Context:   "Verify answer logic",
				Why:       "Foundational answer",
				DependsOn: []string{},
				Files: goals.TaskFiles{
					Read:   []string{"answer.go"},
					Edit:   []string{"answer.go"},
					Create: []string{},
					Forbid: []string{"research/*"},
				},
				References: []goals.Reference{
					{
						Path:    "answer.go",
						Symbol:  "Answer",
						Excerpt: "func Answer() int",
						SHA256:  coreSHA,
					},
				},
				Decisions:     []goals.Decision{},
				Steps:         []goals.Step{{ID: "S-001", Action: "Test answer", Expected: "42", OnFailure: "Halt"}},
				Interfaces:    []string{},
				Constraints:   []string{},
				Examples:      []goals.Example{},
				AcceptanceIDs: []string{"AC-001"},
				Checks: []goals.Check{
					{
						ID:               "C-001",
						Executable:       "echo",
						Args:             []string{"answer 42"},
						Cwd:              ".",
						TimeoutSeconds:   10,
						ExpectedExitCode: 0,
						Scope:            goals.ScopeUnit,
						Assertions: []goals.Assertion{
							{Kind: goals.AssertionStdoutExact, Target: "stdout", Expected: "answer 42"},
						},
						Capability: goals.CapabilityLocal,
					},
				},
				StopConditions: []string{"timeout"},
				ReturnFormat:   "sila/task-result/v1",
			},
		},
		Acceptance: []goals.AcceptanceCase{
			{
				ID:            "AC-001",
				Requirement:   "Answer is 42",
				Given:         "Codebase",
				Action:        "Execute check",
				Expected:      "Pass",
				RequiredScope: goals.ScopeUnit,
				Mandatory:     true,
				CheckIDs:      []string{"C-001"},
			},
		},
		DependencyConditions: []goals.DependencyCondition{},
		UnresolvedDecisions:  []string{},
		RetiredTaskIDs:       []string{},
		RetiredAcceptanceIDs: []string{},
	}

	pkgBytes, err := json.MarshalIndent(pkg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "work-package.json"), pkgBytes, 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "goals/"+slug+"/work-package.json")
	run("commit", "-m", "add work package")

	return dir, slug
}

func TestGoalsEvidenceCLI(t *testing.T) {
	dir, slug := setupEvidenceCLIFixture(t)

	// 1. Run evidence checks via CLI: sila goals evidence <slug> T-001 --run --json
	stdout, stderr, exitCode := runEvidenceCLI(t, dir, true, "evidence", slug, "T-001", "--run")
	if exitCode != 0 {
		t.Fatalf("expected exit code 0 for passing checks, got %d. stderr: %s, stdout: %s", exitCode, stderr, stdout)
	}

	var res map[string]any
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatalf("parse JSON stdout: %v, raw: %s", err, stdout)
	}

	if res["all_passed"] != true {
		t.Errorf("expected all_passed: true, got %+v", res)
	}
	if res["accepted"] != false {
		t.Errorf("AC-012 failed: evidence collection must return accepted: false, got %+v", res)
	}
	attemptID, _ := res["attempt_id"].(string)
	if attemptID == "" {
		t.Fatalf("expected non-empty attempt_id in JSON return")
	}

	// 2. List attempts via CLI: sila goals evidence <slug> T-001 --json
	stdoutList, _, exitList := runEvidenceCLI(t, dir, true, "evidence", slug, "T-001")
	if exitList != 0 {
		t.Fatalf("expected exit 0 for list attempts, got %d", exitList)
	}
	var listRes map[string]any
	_ = json.Unmarshal([]byte(stdoutList), &listRes)
	attempts, _ := listRes["attempts"].([]any)
	if len(attempts) != 1 || attempts[0] != attemptID {
		t.Errorf("expected attempt %s in list, got: %v", attemptID, attempts)
	}

	// 3. AC-011: Import receipt with origin=runner is rejected
	fakeReceipt := &goals.TaskEvidence{
		SchemaVersion: goals.TaskEvidenceSchemaVersion,
		AttemptID:     "att-imported-001",
		GoalSlug:      slug,
		TaskID:        "T-001",
		CaseID:        "AC-001",
		CheckID:       "C-001",
		SpecDigest:    strings.Repeat("1", 64),
		PacketDigest:  strings.Repeat("2", 64),
		Candidate: goals.TaskCandidate{
			Commit:        strings.Repeat("a", 40),
			ContentDigest: strings.Repeat("b", 64),
			ToolVersions:  map[string]string{"go": "go1.26.0"},
			OS:            "linux",
			Arch:          "amd64",
		},
		Scope:      goals.ScopeUnit,
		Outcome:    goals.OutcomePass,
		Origin:     goals.OriginRunner, // Forbidden on import!
		Executable: "echo",
		Args:       []string{"answer 42"},
		Cwd:        ".",
		Assertions: []goals.AssertionObservation{
			{Kind: goals.AssertionStdoutExact, Target: "stdout", Expected: "answer 42", Observed: "answer 42", Passed: true},
		},
		Artifacts: []goals.EvidenceArtifact{},
		StartedAt: time.Now().Add(-5 * time.Second),
		EndedAt:   time.Now(),
	}

	importPath := filepath.Join(dir, "runner_origin.json")
	fakeBytes, _ := json.MarshalIndent(fakeReceipt, "", "  ")
	_ = os.WriteFile(importPath, fakeBytes, 0644)

	_, stderrBadOrigin, exitBadOrigin := runEvidenceCLI(t, dir, false, "evidence", slug, "T-001", "--import="+importPath)
	if exitBadOrigin == 0 {
		t.Fatalf("AC-011 failed: expected nonzero exit when importing receipt with origin=runner")
	}
	if !strings.Contains(stderrBadOrigin, "cannot claim origin=runner") {
		t.Errorf("AC-011 failed: unexpected error message: %s", stderrBadOrigin)
	}

	// 4. AC-011: Import receipt with goal slug mismatch is rejected
	fakeReceipt.Origin = goals.OriginImported
	fakeReceipt.GoalSlug = "other-goal-slug"
	otherGoalBytes, _ := json.MarshalIndent(fakeReceipt, "", "  ")
	_ = os.WriteFile(importPath, otherGoalBytes, 0644)

	_, stderrSlugMismatch, exitSlugMismatch := runEvidenceCLI(t, dir, false, "evidence", slug, "T-001", "--import="+importPath)
	if exitSlugMismatch == 0 {
		t.Fatalf("AC-011 failed: expected nonzero exit when importing receipt with mismatched goal slug")
	}
	if !strings.Contains(stderrSlugMismatch, "does not match target goal") {
		t.Errorf("AC-011 failed: unexpected error message: %s", stderrSlugMismatch)
	}

	// Clean up temporary import file so working tree is clean for step 5
	_ = os.Remove(importPath)

	// 5. AC-012: CLI exits nonzero on failed check
	pkgPath := filepath.Join(dir, "goals", slug, "work-package.json")
	rawPkg, _ := os.ReadFile(pkgPath)
	var wp goals.WorkPackage
	_ = json.Unmarshal(rawPkg, &wp)
	wp.Tasks[0].Checks[0].ExpectedExitCode = 42 // Impossible exit code for echo -> fails!
	updatedPkgBytes, _ := json.MarshalIndent(wp, "", "  ")
	_ = os.WriteFile(pkgPath, updatedPkgBytes, 0644)
	exec.Command("git", "-C", dir, "add", "goals/"+slug+"/work-package.json").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "update with failing check expectation").Run()

	stdoutFail, _, exitFail := runEvidenceCLI(t, dir, true, "evidence", slug, "T-001", "--run")
	if exitFail == 0 {
		t.Fatalf("AC-012 failed: expected nonzero exit code on failed required check, got 0")
	}
	var failRes map[string]any
	_ = json.Unmarshal([]byte(stdoutFail), &failRes)
	if failRes["all_passed"] != false {
		t.Errorf("AC-012 failed: expected all_passed: false on failed check, got: %+v", failRes)
	}
}
