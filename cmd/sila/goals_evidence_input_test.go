package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/assess"
	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/store"
)

func runCertifyCLI(t *testing.T, cwd string, args []string) ([]byte, []byte, int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestCertifyCLIProbe$")
	probe := strings.Join(append([]string{"sila"}, args...), "\x1f")
	cmd.Env = append(os.Environ(), "SILA_CERTIFY_CLI_PROBE="+probe)
	cmd.Dir = cwd

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("failed to run helper subprocess: %v", err)
		}
	}
	return []byte(stdout.String()), []byte(stderr.String()), exitCode
}

func TestCertifyCLIProbe(t *testing.T) {
	if probe := os.Getenv("SILA_CERTIFY_CLI_PROBE"); probe != "" {
		os.Args = strings.Split(probe, "\x1f")
		main()
		os.Exit(0)
	}
}

func setupFixtureGoal(t *testing.T, tmp, slug, agent string) string {
	t.Helper()
	goalDir := filepath.Join(tmp, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatalf("failed to create goal dir: %v", err)
	}

	goalMd := "# Goal: " + slug + "\n\n## Files to Touch\n- pkg/demo.go\n\n## Acceptance criteria\n- AC-001: Pass test\n"
	if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalMd), 0644); err != nil {
		t.Fatalf("failed to write goal.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "facts.md"), []byte("# Facts\n"), 0644); err != nil {
		t.Fatalf("failed to write facts.md: %v", err)
	}

	demoPkg := filepath.Join(tmp, "pkg")
	_ = os.MkdirAll(demoPkg, 0755)
	_ = os.WriteFile(filepath.Join(demoPkg, "demo.go"), []byte("package demo\nfunc Run() int { return 1 }\n"), 0644)

	// Save charter and claim
	charter := &goals.GoalCharter{
		GoalSlug:   slug,
		Agent:      agent,
		AllowPaths: []string{"pkg/demo.go"},
	}
	if err := goals.SaveCharter(tmp, charter); err != nil {
		t.Fatalf("failed to save charter: %v", err)
	}
	claim := &goals.GoalClaim{
		GoalSlug:  slug,
		Agent:     agent,
		ClaimedAt: time.Now().UTC(),
		Status:    "in_progress",
	}
	claimBytes, _ := json.Marshal(claim)
	_ = os.WriteFile(filepath.Join(goalDir, "claim.json"), claimBytes, 0644)
	_ = os.WriteFile(filepath.Join(goalDir, ".claim.lock"), []byte("lock-token-123"), 0644)

	// Init dummy store so CLI commands don't fail on store creation
	dbPath := filepath.Join(tmp, ".sila", "sila.db")
	_ = os.MkdirAll(filepath.Dir(dbPath), 0755)
	st, err := store.Open(dbPath)
	if err == nil {
		_ = st.Close()
	}

	return goalDir
}

func TestCertifyMarkdownEvidenceCLI(t *testing.T) {
	if os.Getenv("SILA_CERTIFY_CLI_PROBE") != "" {
		TestCertifyCLIProbe(t)
		return
	}

	tmp := t.TempDir()
	slug := "markdown-valid-cert"
	goalDir := setupFixtureGoal(t, tmp, slug, "implementer-bot")

	// Create valid markdown review evidence
	reviewContent := `# Review Report: Deprecation Warning Verification

Verdict: ACCEPTED WITH FINDINGS

## Narrative
The reviewer observed warning keywords in documentation, but verified all tests pass.

## Current acceptance
| case_id | status |
|---------|--------|
| AC-001  | PASS   |
`
	reviewPath := filepath.Join(tmp, "review.md")
	if err := os.WriteFile(reviewPath, []byte(reviewContent), 0644); err != nil {
		t.Fatalf("failed to write review: %v", err)
	}

	// 1. Success path: independent auditor certifies valid markdown review
	args := []string{
		"goals", "--project=" + tmp, "certify", slug,
		"--evidence=review.md",
		"--agent=auditor-bot",
		"--note=Accepted with findings",
		"--json",
	}
	stdout, stderr, code := runCertifyCLI(t, tmp, args)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d, stderr: %s, stdout: %s", code, stderr, stdout)
	}

	var successResp map[string]any
	if err := json.Unmarshal(stdout, &successResp); err != nil {
		t.Fatalf("failed to parse success JSON: %v, output: %s", err, stdout)
	}
	if successResp["accepted"] != true {
		t.Fatalf("expected accepted=true, got %v", successResp["accepted"])
	}
	if successResp["certification"] == nil {
		t.Fatalf("expected certification object in success response")
	}

	// Verify certification.json was written and the claim lock was released.
	// claim.json is retained unless a verified closing commit exists in git.
	if _, err := os.Stat(filepath.Join(goalDir, "certification.json")); err != nil {
		t.Fatalf("certification.json was not created on success: %v", err)
	}
	if _, err := os.Stat(filepath.Join(goalDir, ".claim.lock")); err == nil {
		t.Fatalf(".claim.lock was not released on success")
	}
	if _, err := os.Stat(filepath.Join(goalDir, "claim.json")); err != nil {
		t.Fatalf("claim.json must be retained without a verified closing commit")
	}

	// 2. Reviewer independence: producer cannot self-certify
	slugSelf := "markdown-self-cert"
	setupFixtureGoal(t, tmp, slugSelf, "self-agent")
	// Set ImplementedBy in goal.md isnad header
	isnadGoalMd := "# Goal: " + slugSelf + "\n\n## Chain of Custody (Isnad)\n- **Implemented by**: @self-agent\n\n## Files to Touch\n- pkg/demo.go\n\n## Acceptance criteria\n- AC-001: Pass\n"
	_ = os.WriteFile(filepath.Join(tmp, "goals", slugSelf, "goal.md"), []byte(isnadGoalMd), 0644)

	selfArgs := []string{
		"goals", "--project=" + tmp, "certify", slugSelf,
		"--evidence=review.md",
		"--agent=self-agent",
		"--json",
	}
	stdout, stderr, code = runCertifyCLI(t, tmp, selfArgs)
	if code != 1 {
		t.Fatalf("expected exit code 1 for self-audit, got %d, stderr: %s", code, stderr)
	}
	var selfResp map[string]any
	_ = json.Unmarshal(stdout, &selfResp)
	if selfResp["code"] != "SELF_AUDIT_PROHIBITED" {
		t.Fatalf("expected code SELF_AUDIT_PROHIBITED, got %v", selfResp["code"])
	}
}

func TestCertifyRejectedEvidenceNoMutation(t *testing.T) {
	if os.Getenv("SILA_CERTIFY_CLI_PROBE") != "" {
		TestCertifyCLIProbe(t)
		return
	}

	tmp := t.TempDir()

	assertNoMutation := func(t *testing.T, goalDir string) {
		t.Helper()
		if _, err := os.Stat(filepath.Join(goalDir, "certification.json")); err == nil {
			t.Fatalf("certification.json must NOT exist after rejected certification")
		}
		if _, err := os.Stat(filepath.Join(goalDir, "claim.json")); err != nil {
			t.Fatalf("claim.json must be preserved after rejected certification")
		}
		if _, err := os.Stat(filepath.Join(goalDir, ".claim.lock")); err != nil {
			t.Fatalf(".claim.lock must be preserved after rejected certification")
		}
	}

	// 1. Missing evidence path
	t.Run("MissingEvidencePath", func(t *testing.T) {
		slug := "reject-missing-evidence"
		goalDir := setupFixtureGoal(t, tmp, slug, "agent-1")

		args := []string{"goals", "--project=" + tmp, "certify", slug, "--evidence=does-not-exist.md", "--agent=auditor", "--json"}
		stdout, stderr, code := runCertifyCLI(t, tmp, args)
		if code != 1 {
			t.Fatalf("expected exit code 1, got %d, out: %s, err: %s", code, stdout, stderr)
		}
		var resp map[string]any
		if err := json.Unmarshal(stdout, &resp); err != nil {
			t.Fatalf("invalid JSON output: %v, out: %s, err: %s", err, stdout, stderr)
		}
		if resp["accepted"] != false || resp["certification"] != nil {
			t.Fatalf("expected accepted=false and no certification, got: %v", resp)
		}
		if resp["code"] != "EVIDENCE_NOT_FOUND" {
			t.Fatalf("expected code EVIDENCE_NOT_FOUND, got: %v", resp["code"])
		}
		assertNoMutation(t, goalDir)
	})

	// 2. Oversize evidence (> 1MiB)
	t.Run("OversizeEvidence", func(t *testing.T) {
		slug := "reject-oversize-evidence"
		goalDir := setupFixtureGoal(t, tmp, slug, "agent-2")

		hugePath := filepath.Join(tmp, "huge.md")
		hugeData := make([]byte, assess.MaxEvidenceInputSize+100)
		for i := range hugeData {
			hugeData[i] = ' '
		}
		_ = os.WriteFile(hugePath, hugeData, 0644)

		args := []string{"goals", "--project=" + tmp, "certify", slug, "--evidence=huge.md", "--agent=auditor", "--json"}
		stdout, stderr, code := runCertifyCLI(t, tmp, args)
		if code != 1 {
			t.Fatalf("expected exit code 1, got %d, out: %s, err: %s", code, stdout, stderr)
		}
		var resp map[string]any
		if err := json.Unmarshal(stdout, &resp); err != nil {
			t.Fatalf("invalid JSON output: %v, out: %s, err: %s", err, stdout, stderr)
		}
		if resp["code"] != "EVIDENCE_TOO_LARGE" {
			t.Fatalf("expected code EVIDENCE_TOO_LARGE, got: %v", resp["code"])
		}
		assertNoMutation(t, goalDir)
	})

	// 3. Real skipped-test fence in markdown
	t.Run("SkippedTestFence", func(t *testing.T) {
		slug := "reject-skipped-test"
		goalDir := setupFixtureGoal(t, tmp, slug, "agent-3")

		skipMd := `# Review
Verdict: ACCEPTED

` + "```stdout" + `
=== RUN   TestImportant
--- SKIP: TestImportant (0.00s)
PASS
` + "```" + `
`
		skipPath := filepath.Join(tmp, "skip.md")
		_ = os.WriteFile(skipPath, []byte(skipMd), 0644)

		args := []string{"goals", "--project=" + tmp, "certify", slug, "--evidence=skip.md", "--agent=auditor", "--json"}
		stdout, stderr, code := runCertifyCLI(t, tmp, args)
		if code != 1 {
			t.Fatalf("expected exit code 1, got %d, out: %s, err: %s", code, stdout, stderr)
		}
		var resp map[string]any
		if err := json.Unmarshal(stdout, &resp); err != nil {
			t.Fatalf("invalid JSON output: %v, out: %s, err: %s", err, stdout, stderr)
		}
		if resp["code"] != "EVIDENCE_AUDIT_BLOCKED" {
			t.Fatalf("expected code EVIDENCE_AUDIT_BLOCKED, got: %v", resp["code"])
		}
		assertNoMutation(t, goalDir)
	})

	// 4. Negative verdict (FAIL)
	t.Run("NegativeVerdict", func(t *testing.T) {
		slug := "reject-negative-verdict"
		goalDir := setupFixtureGoal(t, tmp, slug, "agent-4")

		failMd := `# Review
Verdict: FAIL
`
		failPath := filepath.Join(tmp, "fail.md")
		_ = os.WriteFile(failPath, []byte(failMd), 0644)

		args := []string{"goals", "--project=" + tmp, "certify", slug, "--evidence=fail.md", "--agent=auditor", "--json"}
		stdout, stderr, code := runCertifyCLI(t, tmp, args)
		if code != 1 {
			t.Fatalf("expected exit code 1, got %d, out: %s, err: %s", code, stdout, stderr)
		}
		var resp map[string]any
		if err := json.Unmarshal(stdout, &resp); err != nil {
			t.Fatalf("invalid JSON output: %v, out: %s, err: %s", err, stdout, stderr)
		}
		if resp["code"] != "EVIDENCE_AUDIT_BLOCKED" {
			t.Fatalf("expected code EVIDENCE_AUDIT_BLOCKED, got: %v", resp["code"])
		}
		assertNoMutation(t, goalDir)
	})

	// 5. Current NOT RUN case in acceptance table
	t.Run("CurrentNotRunCase", func(t *testing.T) {
		slug := "reject-not-run-case"
		goalDir := setupFixtureGoal(t, tmp, slug, "agent-5")

		notRunMd := `# Review
Verdict: ACCEPTED

## Current acceptance
| case_id | status |
|---------|--------|
| AC-001  | NOT RUN |
`
		notRunPath := filepath.Join(tmp, "not_run.md")
		_ = os.WriteFile(notRunPath, []byte(notRunMd), 0644)

		args := []string{"goals", "--project=" + tmp, "certify", slug, "--evidence=not_run.md", "--agent=auditor", "--json"}
		stdout, stderr, code := runCertifyCLI(t, tmp, args)
		if code != 1 {
			t.Fatalf("expected exit code 1, got %d, out: %s, err: %s", code, stdout, stderr)
		}
		var resp map[string]any
		if err := json.Unmarshal(stdout, &resp); err != nil {
			t.Fatalf("invalid JSON output: %v, out: %s, err: %s", err, stdout, stderr)
		}
		if resp["code"] != "EVIDENCE_AUDIT_BLOCKED" {
			t.Fatalf("expected code EVIDENCE_AUDIT_BLOCKED, got: %v", resp["code"])
		}
		assertNoMutation(t, goalDir)
	})

	// 6. Text mode rejection gives actionable stderr and exit 1
	t.Run("TextModeActionableStderr", func(t *testing.T) {
		slug := "reject-text-mode"
		goalDir := setupFixtureGoal(t, tmp, slug, "agent-6")

		args := []string{"goals", "--project=" + tmp, "certify", slug, "--evidence=nonexistent.md", "--agent=auditor"}
		_, stderr, code := runCertifyCLI(t, tmp, args)
		if code != 1 {
			t.Fatalf("expected exit code 1, got %d", code)
		}
		if !strings.Contains(string(stderr), "sila goals certify error:") {
			t.Fatalf("expected actionable stderr message, got: %s", stderr)
		}
		assertNoMutation(t, goalDir)
	})
}
