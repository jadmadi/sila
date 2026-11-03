package main

import (
	"bytes"
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

func TestGoalsAcceptSubprocess(t *testing.T) {
	if os.Getenv("GO_WANT_GOALS_ACCEPT_SUBPROCESS") != "1" {
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
	runGoals(ui, "", targetDir, "", "", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, jsonOut, false, false, false, false, "", "", "", false, actionArgs)
	os.Exit(0)
}

func runAcceptCLI(t *testing.T, targetDir string, jsonOut bool, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestGoalsAcceptSubprocess$")
	cmd.Env = append(os.Environ(),
		"GO_WANT_GOALS_ACCEPT_SUBPROCESS=1",
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
			t.Fatalf("runAcceptCLI failed to execute: %v", err)
		}
	}
	return stdout.String(), stderr.String(), exitCode
}

func setupAcceptCLIFixture(t *testing.T) (string, string, string) {
	t.Helper()
	dir := t.TempDir()
	slug := "accept-cli-goal"

	runGit := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\nOutput: %s", args, err, string(out))
		}
	}

	runGit("init", "-b", "main")
	runGit("config", "user.name", "Accept Tester")
	runGit("config", "user.email", "tester@sila.local")

	_ = os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".sila/\n.bin/\ngoals/*/claim.json\n*.lock\n"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Accept Test Repo\n"), 0644)
	runGit("add", ".gitignore", "README.md")
	runGit("commit", "-m", "chore: initial commit")

	goalDir := filepath.Join(dir, "goals", slug)
	_ = os.MkdirAll(goalDir, 0755)

	goalMd := `# Goal: accept-cli-goal

## Tasks
- [ ] T-001 First task
`
	_ = os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalMd), 0644)

	wp := goals.WorkPackage{
		SchemaVersion: goals.WorkPackageSchemaVersion,
		GoalSlug:      slug,
		Revision:      1,
		Purpose:       "CLI acceptance testing",
		Constraints:   []string{"Pure Go"},
		Tasks: []goals.TaskContract{
			{
				ID:            "T-001",
				Title:         "First task",
				Context:       "CLI test context",
				Why:           "Validation",
				AcceptanceIDs: []string{"AC-001"},
				ReturnFormat:  "sila/task-result/v1",
				Files: goals.TaskFiles{
					Edit: []string{"README.md"},
				},
				Checks: []goals.Check{
					{
						ID:               "C-001",
						Executable:       "true",
						Scope:            goals.ScopeUnit,
						Capability:       goals.CapabilityLocal,
						TimeoutSeconds:   10,
						ExpectedExitCode: 0,
						Assertions: []goals.Assertion{
							{
								Kind:     goals.AssertionStdoutExact,
								Target:   "stdout",
								Expected: "",
							},
						},
					},
				},
			},
		},
		Acceptance: []goals.AcceptanceCase{
			{
				ID:            "AC-001",
				Requirement:   "Must pass",
				Given:         "Initial state",
				Action:        "Execute check",
				Expected:      "Success",
				RequiredScope: "unit",
				Mandatory:     true,
				CheckIDs:      []string{"C-001"},
			},
		},
	}

	wpData, err := json.MarshalIndent(wp, "", "  ")
	if err != nil {
		t.Fatalf("marshal work-package: %v", err)
	}
	_ = os.WriteFile(filepath.Join(goalDir, "work-package.json"), wpData, 0644)

	specDigest, err := goals.WorkPackageDigest(&wp)
	if err != nil {
		t.Fatalf("compute spec digest: %v", err)
	}

	specRev := goals.SpecReview{
		SchemaVersion: goals.SpecReviewSchemaVersion,
		SpecDigest:    specDigest,
		Reviewer:      "@planner",
		ReviewedAt:    time.Now().UTC().Format(time.RFC3339),
		Verdict:       goals.VerdictReady,
		Shortcuts: []goals.ShortcutCounter{
			{
				IncorrectImplementation: "stub success without running checks",
				ExcludedByIDs:           []string{"T-001", "AC-001"},
				Resolution:              "T-001 step 1 requires real check execution",
			},
			{
				IncorrectImplementation: "swallow test failures silently",
				ExcludedByIDs:           []string{"T-001"},
				Resolution:              "AC-001 asserts expected exit code 0",
			},
			{
				IncorrectImplementation: "bypass acceptance evaluation",
				ExcludedByIDs:           []string{"AC-001"},
				Resolution:              "EvaluateTaskAcceptance verifies all declared cases",
			},
		},
		UnresolvedBlockers: []string{},
	}
	if err := goals.SaveSpecReview(dir, slug, specRev); err != nil {
		t.Fatalf("save spec review: %v", err)
	}

	runGit("add", "goals/")
	runGit("commit", "-m", "docs(goals): add accept-cli-goal")

	return dir, slug, specDigest
}

func TestGoalsAcceptCLI(t *testing.T) {
	repoDir, slug, specDigest := setupAcceptCLIFixture(t)

	// 1. Missing flags/arguments
	stdout, stderr, exitCode := runAcceptCLI(t, repoDir, true, "accept")
	if exitCode == 0 {
		t.Fatalf("expected non-zero exit on missing arguments, got 0")
	}
	var errResp map[string]any
	if err := json.Unmarshal([]byte(stdout), &errResp); err == nil {
		if errResp["accepted"] != false {
			t.Errorf("expected accepted: false on missing arguments, got %v", errResp)
		}
	} else if !strings.Contains(stderr, "Usage:") {
		t.Errorf("expected usage on stderr, got %s", stderr)
	}

	// 2. Setup candidate commit and evidence receipt
	cand, err := goals.CaptureTaskCandidate(repoDir, slug)
	if err != nil {
		t.Fatalf("capture candidate: %v", err)
	}
	candDigest := goals.TaskCandidateDigest(*cand)

	now := time.Now().UTC()
	evidenceDir := filepath.Join(repoDir, "goals", slug, "evidence", "T-001")
	_ = os.MkdirAll(evidenceDir, 0755)
	receiptPath := filepath.Join(evidenceDir, "C-001-pass.json")
	ev := goals.TaskEvidence{
		SchemaVersion: goals.TaskEvidenceSchemaVersion,
		AttemptID:     "att-001",
		GoalSlug:      slug,
		TaskID:        "T-001",
		CaseID:        "AC-001",
		CheckID:       "C-001",
		SpecDigest:    specDigest,
		PacketDigest:  "pkg-001",
		Candidate:     *cand,
		Scope:         goals.ScopeUnit,
		Outcome:       goals.OutcomePass,
		Origin:        goals.OriginRunner,
		Executable:    "true",
		ExitCode:      new(int),
		Assertions: []goals.AssertionObservation{
			{
				Kind:     goals.AssertionStdoutExact,
				Target:   "stdout",
				Expected: "",
				Observed: "",
				Passed:   true,
			},
		},
		StartedAt: now.Add(-2 * time.Second),
		EndedAt:   now.Add(-time.Second),
	}
	*ev.ExitCode = 0
	evData, _ := json.MarshalIndent(ev, "", "  ")
	_ = os.WriteFile(receiptPath, evData, 0644)

	// Create Selection file
	auxDir := t.TempDir()
	sel := goals.ReceiptSelection{
		SchemaVersion:   goals.ReceiptSelectionSchemaVersion,
		GoalSlug:        slug,
		TaskID:          "T-001",
		SpecDigest:      specDigest,
		CandidateDigest: candDigest,
		ReceiptPaths:    []string{receiptPath},
	}
	selPath := filepath.Join(auxDir, "selection.json")
	selData, _ := json.MarshalIndent(sel, "", "  ")
	_ = os.WriteFile(selPath, selData, 0644)

	// Create Review file with timestamp strictly after EndedAt
	rev := goals.AcceptanceReview{
		SchemaVersion:   goals.AcceptanceReviewSchemaVersion,
		Reviewer:        "@verifier",
		Producer:        "@implementer",
		CandidateDigest: candDigest,
		SpecDigest:      specDigest,
		Disposition:     "accept",
		CaseDispositions: []goals.CaseDisposition{
			{
				CaseID:   "AC-001",
				Accepted: true,
				Reason:   "verified clean check execution",
			},
		},
		ReviewedAt: now.Format(time.RFC3339),
	}
	revPath := filepath.Join(auxDir, "review.json")
	revData, _ := json.MarshalIndent(rev, "", "  ")
	_ = os.WriteFile(revPath, revData, 0644)

	// 3. Evaluate task acceptance via CLI
	stdout, stderr, exitCode = runAcceptCLI(t, repoDir, true, "accept", slug, "T-001", "--selection="+selPath, "--review="+revPath, "--json")
	if exitCode != 0 {
		t.Fatalf("expected accept to exit 0, got %d. stderr: %s, stdout: %s", exitCode, stderr, stdout)
	}

	var accResult map[string]any
	if err := json.Unmarshal([]byte(stdout), &accResult); err != nil {
		t.Fatalf("failed to decode acceptance result JSON: %v\nOutput: %s", err, stdout)
	}
	if accResult["accepted"] != true {
		t.Errorf("expected accepted: true, got %v", accResult["accepted"])
	}
	if accResult["acceptance_status"] != "accepted" {
		t.Errorf("expected acceptance_status: accepted, got %v", accResult["acceptance_status"])
	}

	// Verify persistence on disk
	savedPath := goals.TaskAcceptancePath(repoDir, slug, "T-001")
	if _, err := os.Stat(savedPath); err != nil {
		t.Fatalf("expected acceptance file at %s, got: %v", savedPath, err)
	}

	// 4. AC-011: Idempotency check - repeating identical acceptance succeeds and preserves evidence
	stdout2, stderr2, exitCode2 := runAcceptCLI(t, repoDir, true, "accept", slug, "T-001", "--selection="+selPath, "--review="+revPath, "--json")
	if exitCode2 != 0 {
		t.Fatalf("expected idempotent re-acceptance to exit 0, got %d. stderr: %s", exitCode2, stderr2)
	}
	var accResult2 map[string]any
	if err := json.Unmarshal([]byte(stdout2), &accResult2); err != nil {
		t.Fatalf("failed to decode re-acceptance JSON: %v", err)
	}
	if accResult2["accepted"] != true {
		t.Errorf("expected accepted: true on re-acceptance, got %v", accResult2["accepted"])
	}

	// 5. AC-011: Failed attempt cannot replace accepted history
	badRev := rev
	badRev.Disposition = "request_changes"
	badRev.CaseDispositions[0].Accepted = false
	badRev.CaseDispositions[0].Reason = "regression observed"
	badRevPath := filepath.Join(auxDir, "bad-review.json")
	badRevData, _ := json.MarshalIndent(badRev, "", "  ")
	_ = os.WriteFile(badRevPath, badRevData, 0644)

	stdout3, _, exitCode3 := runAcceptCLI(t, repoDir, true, "accept", slug, "T-001", "--selection="+selPath, "--review="+badRevPath, "--json")
	if exitCode3 == 0 {
		t.Fatalf("expected failed acceptance to exit non-zero, got 0")
	}
	var failResult map[string]any
	if err := json.Unmarshal([]byte(stdout3), &failResult); err == nil {
		if failResult["accepted"] != false {
			t.Errorf("expected accepted: false on failed attempt, got %v", failResult["accepted"])
		}
	}

	// Ensure the existing acceptance record still shows accepted: true on disk!
	persisted, err := goals.LoadTaskAcceptance(repoDir, slug, "T-001")
	if err != nil || persisted == nil || !persisted.Accepted {
		t.Fatalf("failed attempt corrupted or replaced accepted history on disk! %+v, err=%v", persisted, err)
	}
}

func TestStructuredCertifyEntryPoints(t *testing.T) {
	repoDir, slug, specDigest := setupAcceptCLIFixture(t)

	// Create claim to verify claim preservation on failure (AC-012)
	claim, err := goals.ClaimGoal(repoDir, slug, "@implementer", "working on goal", false)
	if err != nil || claim == nil {
		t.Fatalf("failed to claim goal: %v", err)
	}
	claimPath := filepath.Join(repoDir, "goals", slug, "claim.json")
	if _, err := os.Stat(claimPath); err != nil {
		t.Fatalf("claim file must exist: %v", err)
	}

	// AC-010 Entrypoint 1: Direct certify on incomplete structured goal must reject
	stdout, _, exitCode := runAcceptCLI(t, repoDir, true, "certify", slug, "--json")
	if exitCode == 0 {
		t.Fatalf("expected direct certify to fail on incomplete goal, got exit code 0")
	}
	var resDirect map[string]any
	if err := json.Unmarshal([]byte(stdout), &resDirect); err != nil {
		t.Fatalf("expected valid JSON on direct certify error: %v, got: %s", err, stdout)
	}
	if resDirect["accepted"] != false {
		t.Errorf("expected accepted: false on incomplete direct certify, got %v", resDirect)
	}
	// AC-012: Ensure claim.json is preserved on failure
	if _, err := os.Stat(claimPath); err != nil {
		t.Fatalf("AC-012 violation: claim.json was deleted on direct certify failure: %v", err)
	}

	// AC-010 Entrypoint 2: Auto-run certify on incomplete structured goal must reject
	stdoutAuto, _, exitCodeAuto := runAcceptCLI(t, repoDir, true, "certify", slug, "--auto-run", "--json")
	if exitCodeAuto == 0 {
		t.Fatalf("expected auto-run certify to fail on incomplete goal, got exit code 0")
	}
	var resAuto map[string]any
	if err := json.Unmarshal([]byte(stdoutAuto), &resAuto); err != nil {
		t.Fatalf("expected valid JSON on auto-run error: %v, got: %s", err, stdoutAuto)
	}
	if resAuto["accepted"] != false {
		t.Errorf("expected accepted: false on incomplete auto-run certify, got %v", resAuto)
	}
	// AC-012: Ensure claim.json is preserved on failure
	if _, err := os.Stat(claimPath); err != nil {
		t.Fatalf("AC-012 violation: claim.json was deleted on auto-run failure: %v", err)
	}

	// AC-010 Entrypoint 3: Imported legacy evidence on structured goal must reject with EVIDENCE_FORMAT_UNSUPPORTED
	auxDir := t.TempDir()
	arbEvidencePath := filepath.Join(auxDir, "arbitrary.txt")
	_ = os.WriteFile(arbEvidencePath, []byte("all tests passed\n"), 0644)
	stdoutEv, _, exitCodeEv := runAcceptCLI(t, repoDir, true, "certify", slug, "--evidence="+arbEvidencePath, "--json")
	if exitCodeEv == 0 {
		t.Fatalf("expected imported evidence certify to fail on structured goal, got 0")
	}
	var resEv map[string]any
	if err := json.Unmarshal([]byte(stdoutEv), &resEv); err != nil {
		t.Fatalf("expected valid JSON on imported evidence error: %v, got: %s", err, stdoutEv)
	}
	if resEv["accepted"] != false {
		t.Errorf("expected accepted: false on imported evidence certify, got %v", resEv)
	}
	errMsg, _ := resEv["error"].(string)
	if !strings.Contains(errMsg, "EVIDENCE_FORMAT_UNSUPPORTED") {
		t.Errorf("expected error to contain EVIDENCE_FORMAT_UNSUPPORTED, got: %s", errMsg)
	}
	// AC-012: Ensure claim.json is preserved on failure
	if _, err := os.Stat(claimPath); err != nil {
		t.Fatalf("AC-012 violation: claim.json was deleted on evidence error: %v", err)
	}

	// Now satisfy acceptance for T-001
	cand, err := goals.CaptureTaskCandidate(repoDir, slug)
	if err != nil {
		t.Fatalf("capture candidate: %v", err)
	}
	candDigest := goals.TaskCandidateDigest(*cand)

	evidenceDir := filepath.Join(repoDir, "goals", slug, "evidence", "T-001")
	_ = os.MkdirAll(evidenceDir, 0755)
	receiptPath := filepath.Join(evidenceDir, "C-001-pass.json")
	nowCert := time.Now().UTC()
	ev := goals.TaskEvidence{
		SchemaVersion: goals.TaskEvidenceSchemaVersion,
		AttemptID:     "att-001",
		GoalSlug:      slug,
		TaskID:        "T-001",
		CaseID:        "AC-001",
		CheckID:       "C-001",
		SpecDigest:    specDigest,
		PacketDigest:  "pkg-001",
		Candidate:     *cand,
		Scope:         goals.ScopeUnit,
		Outcome:       goals.OutcomePass,
		Origin:        goals.OriginRunner,
		Executable:    "true",
		ExitCode:      new(int),
		Assertions: []goals.AssertionObservation{
			{
				Kind:     goals.AssertionStdoutExact,
				Target:   "stdout",
				Expected: "",
				Observed: "",
				Passed:   true,
			},
		},
		StartedAt: nowCert.Add(-2 * time.Second),
		EndedAt:   nowCert.Add(-time.Second),
	}
	*ev.ExitCode = 0
	evData, _ := json.MarshalIndent(ev, "", "  ")
	_ = os.WriteFile(receiptPath, evData, 0644)

	sel := goals.ReceiptSelection{
		SchemaVersion:   goals.ReceiptSelectionSchemaVersion,
		GoalSlug:        slug,
		TaskID:          "T-001",
		SpecDigest:      specDigest,
		CandidateDigest: candDigest,
		ReceiptPaths:    []string{receiptPath},
	}
	selPath := filepath.Join(auxDir, "sel.json")
	selData, _ := json.MarshalIndent(sel, "", "  ")
	_ = os.WriteFile(selPath, selData, 0644)

	rev := goals.AcceptanceReview{
		SchemaVersion:   goals.AcceptanceReviewSchemaVersion,
		Reviewer:        "@verifier",
		Producer:        "@implementer",
		CandidateDigest: candDigest,
		SpecDigest:      specDigest,
		Disposition:     "accept",
		CaseDispositions: []goals.CaseDisposition{
			{
				CaseID:   "AC-001",
				Accepted: true,
				Reason:   "verified clean check execution",
			},
		},
		ReviewedAt: nowCert.Format(time.RFC3339),
	}
	revPath := filepath.Join(auxDir, "rev.json")
	revData, _ := json.MarshalIndent(rev, "", "  ")
	_ = os.WriteFile(revPath, revData, 0644)

	// Accept task T-001
	_, _, acceptExit := runAcceptCLI(t, repoDir, true, "accept", slug, "T-001", "--selection="+selPath, "--review="+revPath, "--json")
	if acceptExit != 0 {
		t.Fatalf("accept failed with exit %d", acceptExit)
	}

	// AC-011: Complete structured goal certifies once
	stdoutCert, stderrCert, exitCert := runAcceptCLI(t, repoDir, true, "certify", slug, "--json")
	if exitCert != 0 {
		t.Fatalf("expected complete goal to certify with exit 0, got %d. stderr: %s, stdout: %s", exitCert, stderrCert, stdoutCert)
	}
	var certResult map[string]any
	if err := json.Unmarshal([]byte(stdoutCert), &certResult); err != nil {
		t.Fatalf("failed to decode certification JSON: %v", err)
	}
	if certResult["accepted"] != true {
		t.Errorf("expected accepted: true on complete goal certification, got %v", certResult)
	}

	// Certification file must exist
	certPath := filepath.Join(repoDir, "goals", slug, "certification.json")
	if _, err := os.Stat(certPath); err != nil {
		t.Fatalf("certification.json was not created: %v", err)
	}

	// Claim file is retained until commit attribution closes goal
	if _, err := os.Stat(claimPath); err != nil {
		t.Errorf("expected claim.json to be retained upon certification until closing commit, but got error: %v", err)
	}

	// AC-011: Repeated certify is idempotent
	stdoutCert2, stderrCert2, exitCert2 := runAcceptCLI(t, repoDir, true, "certify", slug, "--json")
	if exitCert2 != 0 {
		t.Fatalf("expected repeated certify to exit 0, got %d. stderr: %s, stdout: %s", exitCert2, stderrCert2, stdoutCert2)
	}
}
