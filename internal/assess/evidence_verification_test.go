package assess

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupTestGoal(t *testing.T, slug string, goalMdContent, factsContent string, files ...string) string {
	t.Helper()
	tempDir := t.TempDir()
	goalDir := filepath.Join(tempDir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatalf("failed to create goal dir: %v", err)
	}
	if goalMdContent != "" {
		if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalMdContent), 0644); err != nil {
			t.Fatalf("failed to write goal.md: %v", err)
		}
	}
	if factsContent != "" {
		if err := os.WriteFile(filepath.Join(goalDir, "facts.md"), []byte(factsContent), 0644); err != nil {
			t.Fatalf("failed to write facts.md: %v", err)
		}
	}
	for _, f := range files {
		fp := filepath.Join(tempDir, f)
		_ = os.MkdirAll(filepath.Dir(fp), 0755)
		_ = os.WriteFile(fp, []byte("package auth\n\nfunc Auth() bool { return true }\n"), 0644)
	}
	return tempDir
}

func TestManualEvidenceDisclosureBoundary(t *testing.T) {
	// AC-004: Clean accepted-with-findings Markdown review generates no false warning offenses
	slug := "test-warning-review"
	rootDir := setupTestGoal(t, slug, "# Goal: test-warning-review\n## Files to Touch\n- pkg/auth.go\n", "", "pkg/auth.go")

	docText := `# Review of deprecated warning behaviors

Verdict: ACCEPTED WITH FINDINGS

## Executive summary
We verified all authentication flows. Notice that the word warning appears here
multiple times in the narrative review without being a runtime log.

## Current acceptance
| case_id | status |
|---------|--------|
| AC-001  | PASS   |
`
	input, err := ParseEvidenceInput("review.md", []byte(docText))
	if err != nil {
		t.Fatalf("unexpected error parsing evidence: %v", err)
	}

	opts := RuleOfFiveOptions{
		AllowWarnings:     true,
		AllowSkippedTests: false,
	}

	rep := VerifyManualEvidence(rootDir, slug, input, []string{"pkg/auth.go"}, opts)
	if rep.StreamInspect.SilentWarnings != 0 {
		t.Fatalf("expected 0 silent warnings from review narrative, got %d", rep.StreamInspect.SilentWarnings)
	}
	if len(rep.StreamInspect.Offenses) != 0 {
		t.Fatalf("expected 0 stream offenses, got %v", rep.StreamInspect.Offenses)
	}
	if !rep.Disclosure.Passed {
		t.Fatalf("expected disclosure to pass, details: %s", rep.Disclosure.Details)
	}
	if !rep.Clean {
		t.Fatalf("expected clean report for valid review document, violations: %v", rep.Violations)
	}

	// AC-005: Actual undisclosed warning in stdout fence fails Point 5
	docWithStdout := `# Review
Verdict: ACCEPTED WITH FINDINGS

` + "```stdout" + `
=== RUN   TestLogin
--- PASS: TestLogin (0.01s)
warning: deprecated crypto API in use
PASS
` + "```" + `
`
	inputUndisclosed, err := ParseEvidenceInput("review.md", []byte(docWithStdout))
	if err != nil {
		t.Fatalf("unexpected error parsing evidence: %v", err)
	}

	repUndisclosed := VerifyManualEvidence(rootDir, slug, inputUndisclosed, []string{"pkg/auth.go"}, opts)
	if repUndisclosed.StreamInspect.SilentWarnings != 1 {
		t.Fatalf("expected 1 silent warning from actual stdout fence, got %d", repUndisclosed.StreamInspect.SilentWarnings)
	}
	if repUndisclosed.Clean {
		t.Fatalf("expected report with undisclosed warning to fail Point 5")
	}
	hasPoint5Violation := false
	for _, v := range repUndisclosed.Violations {
		if strings.HasPrefix(v, "Point 5") {
			hasPoint5Violation = true
			break
		}
	}
	if !hasPoint5Violation {
		t.Fatalf("expected Point 5 violation for undisclosed warning, got: %v", repUndisclosed.Violations)
	}

	// AC-005: Explicit matching disclosure in ## Limitations satisfies Point 5
	docWithDisclosure := `# Review
Verdict: ACCEPTED WITH FINDINGS

## Limitations
- warning: deprecated crypto API in use is expected during migration

` + "```stdout" + `
=== RUN   TestLogin
--- PASS: TestLogin (0.01s)
warning: deprecated crypto API in use
PASS
` + "```" + `
`
	inputDisclosed, err := ParseEvidenceInput("review.md", []byte(docWithDisclosure))
	if err != nil {
		t.Fatalf("unexpected error parsing evidence: %v", err)
	}

	repDisclosed := VerifyManualEvidence(rootDir, slug, inputDisclosed, []string{"pkg/auth.go"}, opts)
	if repDisclosed.StreamInspect.SilentWarnings != 1 {
		t.Fatalf("expected silent warning count 1 to be preserved, got %d", repDisclosed.StreamInspect.SilentWarnings)
	}
	if !repDisclosed.Disclosure.Passed {
		t.Fatalf("expected Point 5 to pass with explicit disclosure, got: %s", repDisclosed.Disclosure.Details)
	}
	if !repDisclosed.Clean {
		t.Fatalf("expected clean report when warning is allowed and disclosed, got violations: %v", repDisclosed.Violations)
	}

	// When AllowWarnings is false, Point 2 fails even if disclosed
	optsStrictWarnings := RuleOfFiveOptions{
		AllowWarnings:     false,
		AllowSkippedTests: false,
	}
	repStrict := VerifyManualEvidence(rootDir, slug, inputDisclosed, []string{"pkg/auth.go"}, optsStrictWarnings)
	if repStrict.Clean {
		t.Fatalf("expected report to fail Point 2 when AllowWarnings is false")
	}
	hasPoint2Warning := false
	for _, v := range repStrict.Violations {
		if strings.HasPrefix(v, "Point 2") && strings.Contains(v, "silent warning") {
			hasPoint2Warning = true
			break
		}
	}
	if !hasPoint2Warning {
		t.Fatalf("expected Point 2 violation for warnings under strict options, got: %v", repStrict.Violations)
	}
}

func TestManualEvidenceCannotWaiveFailures(t *testing.T) {
	// AC-006: Skipped tests, hollow stubs, negative verdict, blocking cases cannot be waived
	slug := "test-unwaivable-goal"
	rootDir := setupTestGoal(t, slug, "# Goal: test-unwaivable-goal\n## Files to Touch\n- pkg/handler.go\n", "", "pkg/handler.go")

	opts := RuleOfFiveOptions{
		AllowWarnings:     true,
		AllowSkippedTests: false,
	}

	// 1. Skipped test in stdout fence with attempted disclosure
	docSkip := `# Review
Verdict: ACCEPTED

## Limitations
- skipped test TestCriticalAuth due to missing test credentials

` + "```stdout" + `
=== RUN   TestCriticalAuth
--- SKIP: TestCriticalAuth (0.00s)
PASS
` + "```" + `
`
	inputSkip, err := ParseEvidenceInput("review.md", []byte(docSkip))
	if err != nil {
		t.Fatalf("unexpected error parsing evidence: %v", err)
	}
	repSkip := VerifyManualEvidence(rootDir, slug, inputSkip, []string{"pkg/handler.go"}, opts)
	if repSkip.Clean {
		t.Fatalf("expected skipped test to block even when disclosed in Limitations")
	}
	hasPoint2Skip := false
	for _, v := range repSkip.Violations {
		if strings.HasPrefix(v, "Point 2") && strings.Contains(v, "skipped test") {
			hasPoint2Skip = true
			break
		}
	}
	if !hasPoint2Skip {
		t.Fatalf("expected Point 2 violation for skipped test, got: %v", repSkip.Violations)
	}

	// 2. Negative document verdict (e.g. FAIL)
	docFail := `# Review
Verdict: FAIL
`
	inputFail, err := ParseEvidenceInput("review.md", []byte(docFail))
	if err != nil {
		t.Fatalf("unexpected error parsing evidence: %v", err)
	}
	repFail := VerifyManualEvidence(rootDir, slug, inputFail, []string{"pkg/handler.go"}, opts)
	if repFail.Clean {
		t.Fatalf("expected negative verdict FAIL to block verification")
	}
	hasVerdictViolation := false
	for _, v := range repFail.Violations {
		if strings.Contains(v, "Evidence-Input (Verdict)") {
			hasVerdictViolation = true
			break
		}
	}
	if !hasVerdictViolation {
		t.Fatalf("expected Evidence-Input (Verdict) violation, got: %v", repFail.Violations)
	}

	// 3. Current blocking case in acceptance table (e.g. NOT RUN)
	docBlocked := `# Review
Verdict: ACCEPTED

## Current acceptance
| case_id | status |
|---------|--------|
| AC-001  | PASS   |
| AC-002  | NOT RUN |
`
	inputBlocked, err := ParseEvidenceInput("review.md", []byte(docBlocked))
	if err != nil {
		t.Fatalf("unexpected error parsing evidence: %v", err)
	}
	repBlocked := VerifyManualEvidence(rootDir, slug, inputBlocked, []string{"pkg/handler.go"}, opts)
	if repBlocked.Clean {
		t.Fatalf("expected NOT RUN case to block verification")
	}
	hasCaseViolation := false
	for _, v := range repBlocked.Violations {
		if strings.Contains(v, "Evidence-Input (Case)") && strings.Contains(v, "AC-002") {
			hasCaseViolation = true
			break
		}
	}
	if !hasCaseViolation {
		t.Fatalf("expected Evidence-Input (Case) violation for AC-002, got: %v", repBlocked.Violations)
	}

	// 4. Boundary / collateral blast radius violation
	docPass := `# Review
Verdict: ACCEPTED
`
	inputPass, err := ParseEvidenceInput("review.md", []byte(docPass))
	if err != nil {
		t.Fatalf("unexpected error parsing evidence: %v", err)
	}
	// pkg/secret.go is outside charter
	repLeak := VerifyManualEvidence(rootDir, slug, inputPass, []string{"pkg/handler.go", "pkg/secret.go"}, opts)
	if repLeak.Clean {
		t.Fatalf("expected collateral leak to block verification")
	}
	hasPoint4Violation := false
	for _, v := range repLeak.Violations {
		if strings.HasPrefix(v, "Point 4") {
			hasPoint4Violation = true
			break
		}
	}
	if !hasPoint4Violation {
		t.Fatalf("expected Point 4 violation for collateral leak, got: %v", repLeak.Violations)
	}

	// 5. Hollow stub in touched file cannot be waived by Limitations
	docStub := `# Review
Verdict: ACCEPTED

## Limitations
- hollow stub in handler.go to be implemented later
`
	inputStub, err := ParseEvidenceInput("review.md", []byte(docStub))
	if err != nil {
		t.Fatalf("unexpected error parsing evidence: %v", err)
	}
	_ = os.WriteFile(filepath.Join(rootDir, "pkg", "handler.go"), []byte("package handler\n\nfunc Handle() {\n\tpanic(\"TODO: implement\")\n}\n"), 0644)
	repStub := VerifyManualEvidence(rootDir, slug, inputStub, []string{"pkg/handler.go"}, opts)
	if repStub.Clean {
		t.Fatalf("expected hollow stub to block verification even when disclosed")
	}
	hasPoint3Violation := false
	for _, v := range repStub.Violations {
		if strings.HasPrefix(v, "Point 3") {
			hasPoint3Violation = true
			break
		}
	}
	if !hasPoint3Violation {
		t.Fatalf("expected Point 3 violation for hollow stub, got: %v", repStub.Violations)
	}
}
