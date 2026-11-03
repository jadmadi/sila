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

func TestGoalsDependenciesSubprocess(t *testing.T) {
	if os.Getenv("GO_WANT_GOALS_DEPENDENCIES_SUBPROCESS") != "1" {
		return
	}
	targetDir := os.Getenv("SUBPROCESS_TARGET_DIR")
	rawArgs := os.Getenv("SUBPROCESS_ARGS")
	var actionArgs []string
	if rawArgs != "" {
		actionArgs = strings.Split(rawArgs, "||")
	}
	jsonOut := os.Getenv("SUBPROCESS_JSON") == "1"

	ui := render.New(os.Stdout, false)
	runGoals(
		ui, "", targetDir, "", "", "", "", false, false,
		"", "", "", "", "", 0, false, false, false, false,
		false, false, false, false, jsonOut, false, false,
		false, false, "", "", "", false, actionArgs,
	)
	os.Exit(0)
}

func runGoalSubprocess(
	t *testing.T, targetDir string, jsonOut bool, args ...string,
) (string, string, int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestGoalsDependenciesSubprocess$")
	cmd.Env = append(
		os.Environ(),
		"GO_WANT_GOALS_DEPENDENCIES_SUBPROCESS=1",
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
			t.Fatalf("runGoalSubprocess failed: %v", err)
		}
	}
	return stdout.String(), stderr.String(), exitCode
}

func setupCLITestPredecessor(t *testing.T, dir, slug, decisionVal string) string {
	t.Helper()
	goalDir := filepath.Join(dir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatalf("mkdir goalDir: %v", err)
	}

	goalMD := "# Goal: " + slug + "\n\n- Tier: roadmap\n- Shape: ship\n"
	planMD := "# Plan: " + slug + "\n\n- [x] Step 1\n"
	factsMD := "# Facts: " + slug + "\n\n- Contracts: decision.json\n"

	_ = os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalMD), 0644)
	_ = os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte(planMD), 0644)
	_ = os.WriteFile(filepath.Join(goalDir, "facts.md"), []byte(factsMD), 0644)

	wp := &goals.WorkPackage{
		SchemaVersion: goals.WorkPackageSchemaVersion,
		GoalSlug:      slug,
		Revision:      1,
		Purpose:       "Predecessor goal providing decision",
		Tasks: []goals.TaskContract{
			{
				ID:            "T-001",
				Title:         "Evaluate options",
				Context:       "Evaluation context",
				Why:           "Produce decision artifact",
				Files:         goals.TaskFiles{Create: []string{"decision.json"}},
				AcceptanceIDs: []string{"AC-001"},
				Checks: []goals.Check{
					{
						ID:               "C-001",
						Executable:       "go",
						Args:             []string{"test"},
						TimeoutSeconds:   10,
						ExpectedExitCode: 0,
						Scope:            "unit",
						Capability:       "local",
						Assertions: []goals.Assertion{
							{Kind: "go_test_pass", Target: "test", Expected: true},
						},
					},
				},
				ReturnFormat: goals.TaskResultReturnFormat,
			},
		},
		Acceptance: []goals.AcceptanceCase{
			{
				ID:            "AC-001",
				Requirement:   "Decision is made",
				Given:         "Evaluation is complete",
				Action:        "Write decision",
				Expected:      "Decision written cleanly",
				RequiredScope: "unit",
				Mandatory:     true,
				CheckIDs:      []string{"C-001"},
			},
		},
	}
	if err := goals.SaveWorkPackage(dir, slug, wp); err != nil {
		t.Fatalf("SaveWorkPackage: %v", err)
	}

	review := &goals.SpecReview{
		SchemaVersion: goals.SpecReviewSchemaVersion,
		SpecDigest:    "digest",
		Reviewer:      "@architect",
		ReviewedAt:    time.Now().UTC().Format(time.RFC3339),
		Verdict:       "ready",
	}
	if err := goals.SaveSpecReview(dir, slug, *review); err != nil {
		t.Fatalf("SaveSpecReview: %v", err)
	}

	setCLITestPredecessorDecision(t, dir, slug, decisionVal)
	return goalDir
}

func setCLITestPredecessorDecision(t *testing.T, dir, slug, decisionVal string) {
	t.Helper()
	goalDir := filepath.Join(dir, "goals", slug)

	decObj := map[string]any{
		"decision":    decisionVal,
		"reviewed_by": "@architect",
	}
	decBytes, _ := json.MarshalIndent(decObj, "", "  ")
	decPath := filepath.Join(goalDir, "decision.json")
	if err := os.WriteFile(decPath, decBytes, 0644); err != nil {
		t.Fatalf("write decision.json: %v", err)
	}

	decSum := sha256.Sum256(decBytes)
	decHex := hex.EncodeToString(decSum[:])

	evDir := filepath.Join(goalDir, "evidence", "T-001")
	if err := os.MkdirAll(evDir, 0755); err != nil {
		t.Fatalf("mkdir evDir: %v", err)
	}
	exit0 := 0
	ev := &goals.TaskEvidence{
		SchemaVersion: goals.TaskEvidenceSchemaVersion,
		AttemptID:     "att-1",
		GoalSlug:      slug,
		TaskID:        "T-001",
		CaseID:        "AC-001",
		CheckID:       "C-001",
		Outcome:       "pass",
		Origin:        "runner",
		Executable:    "go",
		ExitCode:      &exit0,
		Artifacts: []goals.EvidenceArtifact{
			{
				Path:   "decision.json",
				SHA256: decHex,
			},
		},
		StartedAt: time.Now().UTC().Add(-time.Minute),
		EndedAt:   time.Now().UTC(),
	}
	evBytes, _ := json.MarshalIndent(ev, "", "  ")
	receiptPath := filepath.Join(evDir, "receipt.json")
	if err := os.WriteFile(receiptPath, evBytes, 0644); err != nil {
		t.Fatalf("write receipt.json: %v", err)
	}

	wp, _ := goals.LoadWorkPackage(dir, slug)
	specDigest, _ := goals.WorkPackageDigest(wp)

	taskAcc := &goals.AcceptanceResult{
		SchemaVersion:  goals.AcceptanceResultSchemaVersion,
		GoalSlug:       slug,
		TaskID:         "T-001",
		Accepted:       true,
		EvidenceStatus: goals.EvidenceStatusAccepted,
		SpecDigest:     specDigest,
		ReceiptPaths:   []string{filepath.Join("evidence", "T-001", "receipt.json")},
		ReviewedAt:     time.Now().UTC().Format(time.RFC3339),
	}
	if err := goals.SaveTaskAcceptance(dir, slug, "T-001", taskAcc, nil, nil); err != nil {
		t.Fatalf("SaveTaskAcceptance: %v", err)
	}

	goalAcc := &goals.AcceptanceResult{
		SchemaVersion:  goals.AcceptanceResultSchemaVersion,
		GoalSlug:       slug,
		Accepted:       true,
		EvidenceStatus: goals.EvidenceStatusAccepted,
		SpecDigest:     specDigest,
		ReceiptPaths:   []string{filepath.Join("evidence", "T-001", "receipt.json")},
		ReviewedAt:     time.Now().UTC().Format(time.RFC3339),
	}
	if err := goals.SaveGoalAcceptance(dir, slug, goalAcc); err != nil {
		t.Fatalf("SaveGoalAcceptance: %v", err)
	}
}

func setupCLITestSuccessor(t *testing.T, dir, slug, predSlug string) {
	t.Helper()
	goalDir := filepath.Join(dir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatalf("mkdir goalDir: %v", err)
	}

	goalMD := "# Goal: " + slug + "\n\n- Tier: roadmap\n- Shape: ship\n- Depends On: " + predSlug + "\n"
	planMD := "# Plan: " + slug + "\n\n- [ ] Step 1\n"
	factsMD := "# Facts: " + slug + "\n\n- Contracts: release.go\n"

	_ = os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalMD), 0644)
	_ = os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte(planMD), 0644)
	_ = os.WriteFile(filepath.Join(goalDir, "facts.md"), []byte(factsMD), 0644)

	wp := &goals.WorkPackage{
		SchemaVersion: goals.WorkPackageSchemaVersion,
		GoalSlug:      slug,
		Revision:      1,
		Purpose:       "Successor release goal requiring proceed",
		Tasks: []goals.TaskContract{
			{
				ID:            "T-001",
				Title:         "Ship release",
				Context:       "Ship context",
				Why:           "Fulfill release charter",
				Files:         goals.TaskFiles{Create: []string{"release.go"}},
				AcceptanceIDs: []string{"AC-001"},
				Checks: []goals.Check{
					{
						ID:               "C-001",
						Executable:       "go",
						Args:             []string{"test"},
						TimeoutSeconds:   10,
						ExpectedExitCode: 0,
						Scope:            "unit",
						Capability:       "local",
						Assertions: []goals.Assertion{
							{Kind: "go_test_pass", Target: "test", Expected: true},
						},
					},
				},
				ReturnFormat: goals.TaskResultReturnFormat,
			},
		},
		Acceptance: []goals.AcceptanceCase{
			{
				ID:            "AC-001",
				Requirement:   "Release builds",
				Given:         "Code is written",
				Action:        "Build binary",
				Expected:      "Build passes cleanly",
				RequiredScope: "unit",
				Mandatory:     true,
				CheckIDs:      []string{"C-001"},
			},
		},
		DependencyConditions: []goals.DependencyCondition{
			{
				Kind:     goals.DepConditionDecisionEquals,
				GoalSlug: predSlug,
				Artifact: "decision.json",
				Pointer:  "/decision",
				Equals:   "proceed",
			},
			{
				Kind:     goals.DepConditionAcceptedGoal,
				GoalSlug: predSlug,
			},
		},
	}
	if err := goals.SaveWorkPackage(dir, slug, wp); err != nil {
		t.Fatalf("SaveWorkPackage: %v", err)
	}

	review := &goals.SpecReview{
		SchemaVersion: goals.SpecReviewSchemaVersion,
		SpecDigest:    "digest",
		Reviewer:      "@architect",
		ReviewedAt:    time.Now().UTC().Format(time.RFC3339),
		Verdict:       "ready",
	}
	if err := goals.SaveSpecReview(dir, slug, *review); err != nil {
		t.Fatalf("SaveSpecReview: %v", err)
	}
}

// TestConditionalDependencyCLI exercises AC-010, AC-011, and AC-012 through the CLI interface.
func TestConditionalDependencyCLI(t *testing.T) {
	tmp := t.TempDir()

	// Initialize git repo in tmp so git commit scans succeed
	cmd := exec.Command("git", "init")
	cmd.Dir = tmp
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init failed: %v, out: %s", err, string(out))
	}

	// 1. Setup assessment goal with decision = "revise"
	setupCLITestPredecessor(t, tmp, "assessment", "revise")

	// 2. Setup release goal requiring decision = "proceed"
	setupCLITestSuccessor(t, tmp, "release", "assessment")

	// ------------------------------------------------------------------------
	// Scenario 1 (AC-010): Decision mismatch (observed=revise, required=proceed)
	// ------------------------------------------------------------------------

	// Check CLI goals blocked (JSON)
	stdout, _, exitCode := runGoalSubprocess(t, tmp, true, "blocked")
	if exitCode != 0 {
		t.Fatalf("goals blocked --json failed with exit code %d", exitCode)
	}
	var blockedRes struct {
		Filter string `json:"filter"`
		Goals  []struct {
			Slug     string                    `json:"slug"`
			Blocked  bool                      `json:"blocked"`
			Blockers []goals.DependencyBlocker `json:"blockers"`
		} `json:"goals"`
	}
	if err := json.Unmarshal([]byte(stdout), &blockedRes); err != nil {
		t.Fatalf("decode goals blocked JSON: %v, raw: %s", err, stdout)
	}
	var relBlockedGoal *goals.DependencyBlocker
	for _, g := range blockedRes.Goals {
		if g.Slug == "release" {
			if !g.Blocked {
				t.Errorf("expected release to be blocked in blocked list")
			}
			for i := range g.Blockers {
				if g.Blockers[i].Code == goals.DiagDependencyDecisionMismatch {
					relBlockedGoal = &g.Blockers[i]
					break
				}
			}
		}
	}
	if relBlockedGoal == nil {
		t.Fatalf("AC-010: release goal missing decision mismatch blocker in blocked JSON: %s", stdout)
	}
	if relBlockedGoal.GoalSlug != "release" || relBlockedGoal.SourceGoal != "assessment" {
		t.Errorf("AC-010: blocker slugs mismatch: %+v", relBlockedGoal)
	}
	if relBlockedGoal.Required != "proceed" || relBlockedGoal.Observed != "revise" {
		t.Errorf("AC-010: blocker values mismatch: required=%v observed=%v", relBlockedGoal.Required, relBlockedGoal.Observed)
	}

	// Check CLI goals ready (human output): release must not be listed
	stdout, _, exitCode = runGoalSubprocess(t, tmp, false, "ready")
	if exitCode != 0 {
		t.Fatalf("goals ready failed with exit code %d", exitCode)
	}
	if strings.Contains(stdout, "release") {
		t.Errorf("release must not be listed in goals ready output")
	}

	// Check CLI goals view release (human output)
	stdout, _, exitCode = runGoalSubprocess(t, tmp, false, "view", "release")
	if exitCode != 0 {
		t.Fatalf("goals view release failed with exit code %d", exitCode)
	}
	if !strings.Contains(stdout, "decision mismatch on assessment") {
		t.Errorf("AC-010: human output should name decision mismatch: %s", stdout)
	}
	if !strings.Contains(stdout, "required proceed") || !strings.Contains(stdout, "observed revise") {
		t.Errorf("AC-010: human output should name required and observed values: %s", stdout)
	}

	// Check CLI goals view release (JSON output)
	stdout, _, exitCode = runGoalSubprocess(t, tmp, true, "view", "release")
	if exitCode != 0 {
		t.Fatalf("goals view release --json failed with exit code %d", exitCode)
	}
	var viewRes goals.GoalView
	if err := json.Unmarshal([]byte(stdout), &viewRes); err != nil {
		t.Fatalf("decode goals view JSON: %v, raw: %s", err, stdout)
	}
	if !viewRes.Blocked {
		t.Errorf("AC-010: expected view.Blocked to be true")
	}
	var foundBlocker *goals.DependencyBlocker
	for i := range viewRes.Blockers {
		if viewRes.Blockers[i].Code == goals.DiagDependencyDecisionMismatch {
			foundBlocker = &viewRes.Blockers[i]
			break
		}
	}
	if foundBlocker == nil {
		t.Fatalf("AC-010: view.Blockers missing decision mismatch blocker: %+v", viewRes.Blockers)
	}
	if foundBlocker.GoalSlug != "release" || foundBlocker.SourceGoal != "assessment" {
		t.Errorf("AC-010: view blocker slugs: %+v", foundBlocker)
	}
	if foundBlocker.Required != "proceed" || foundBlocker.Observed != "revise" {
		t.Errorf("AC-010: view blocker values: required=%v, observed=%v", foundBlocker.Required, foundBlocker.Observed)
	}

	// ------------------------------------------------------------------------
	// Scenario 2 (AC-011): Missing artifact reported distinctly from revise
	// ------------------------------------------------------------------------
	decPath := filepath.Join(tmp, "goals", "assessment", "decision.json")
	if err := os.Remove(decPath); err != nil {
		t.Fatalf("remove decision.json: %v", err)
	}

	// Check CLI goals view release (JSON)
	stdout, _, exitCode = runGoalSubprocess(t, tmp, true, "view", "release")
	if exitCode != 0 {
		t.Fatalf("goals view release --json failed after removing artifact: %d", exitCode)
	}
	var viewMissing goals.GoalView
	if err := json.Unmarshal([]byte(stdout), &viewMissing); err != nil {
		t.Fatalf("decode view JSON: %v", err)
	}
	var missingBlocker *goals.DependencyBlocker
	for i := range viewMissing.Blockers {
		if viewMissing.Blockers[i].Code == goals.DiagDependencyArtifactNotFound {
			missingBlocker = &viewMissing.Blockers[i]
			break
		}
	}
	if missingBlocker == nil {
		t.Fatalf("AC-011: expected DEPENDENCY_ARTIFACT_NOT_FOUND blocker: %+v", viewMissing.Blockers)
	}
	if missingBlocker.Artifact != "decision.json" {
		t.Errorf("AC-011: expected artifact decision.json, got: %s", missingBlocker.Artifact)
	}

	// Check CLI goals view release (human output)
	stdout, _, exitCode = runGoalSubprocess(t, tmp, false, "view", "release")
	if exitCode != 0 {
		t.Fatalf("goals view release failed: %d", exitCode)
	}
	if !strings.Contains(stdout, "missing artifact") {
		t.Errorf("AC-011: human output should distinctly report missing artifact: %s", stdout)
	}
	if strings.Contains(stdout, "observed revise") {
		t.Errorf("AC-011: human output should not claim revise when artifact is missing")
	}

	// ------------------------------------------------------------------------
	// Scenario 3 (AC-012): Decision is "stop" (successor blocked, pred not failed)
	// ------------------------------------------------------------------------
	setCLITestPredecessorDecision(t, tmp, "assessment", "stop")

	// Check CLI goals view release (human output)
	stdout, _, exitCode = runGoalSubprocess(t, tmp, false, "view", "release")
	if exitCode != 0 {
		t.Fatalf("goals view release failed: %d", exitCode)
	}
	if !strings.Contains(stdout, "observed stop") {
		t.Errorf("AC-012: human output should report observed stop: %s", stdout)
	}
	if strings.Contains(stdout, "failed") {
		t.Errorf("AC-012: output must not claim prerequisite failed on valid stop decision: %s", stdout)
	}

	// Check predecessor assessment view: status must be implemented
	stdout, _, exitCode = runGoalSubprocess(t, tmp, true, "view", "assessment")
	if exitCode != 0 {
		t.Fatalf("goals view assessment failed: %d", exitCode)
	}
	var viewPred goals.GoalView
	if err := json.Unmarshal([]byte(stdout), &viewPred); err != nil {
		t.Fatalf("decode assessment view: %v", err)
	}
	if viewPred.Status != "implemented" {
		t.Errorf("AC-012: assessment status must remain implemented, got: %s", viewPred.Status)
	}

	// ------------------------------------------------------------------------
	// Scenario 4: Decision is "proceed" -> release unblocks
	// ------------------------------------------------------------------------
	setCLITestPredecessorDecision(t, tmp, "assessment", "proceed")

	// Check CLI goals ready: release is now listed
	stdout, _, exitCode = runGoalSubprocess(t, tmp, false, "ready")
	if exitCode != 0 {
		t.Fatalf("goals ready failed: %d", exitCode)
	}
	if !strings.Contains(stdout, "release") {
		t.Errorf("release must now appear in goals ready output: %s", stdout)
	}

	// Check CLI goals view release (JSON): blocked is false
	stdout, _, exitCode = runGoalSubprocess(t, tmp, true, "view", "release")
	if exitCode != 0 {
		t.Fatalf("goals view release failed: %d", exitCode)
	}
	var viewProceed goals.GoalView
	if err := json.Unmarshal([]byte(stdout), &viewProceed); err != nil {
		t.Fatalf("decode viewProceed: %v", err)
	}
	if viewProceed.Blocked {
		t.Errorf("expected release to be unblocked when decision is proceed: %+v", viewProceed.Blockers)
	}
}
