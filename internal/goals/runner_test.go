package goals

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunVerificationContract_Pass(t *testing.T) {
	dir := t.TempDir()
	slug := "runner-pass-goal"
	goalDir := filepath.Join(dir, "goals", slug)
	_ = os.MkdirAll(goalDir, 0755)

	c, err := CreateVerificationContract(dir, "echo 'hello from contract runner'", 10, 5, 0, nil)
	if err != nil {
		t.Fatalf("create contract: %v", err)
	}
	if err := SaveContract(dir, slug, c); err != nil {
		t.Fatalf("save contract: %v", err)
	}

	res, err := RunVerificationContract(context.Background(), dir, slug)
	if err != nil {
		t.Fatalf("RunVerificationContract failed: %v", err)
	}
	if !res.Passed {
		t.Errorf("expected contract to pass, got failure: %s", res.FailureReason)
	}
	if res.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", res.ExitCode)
	}
	if !strings.Contains(res.OutputSnippet, "hello from contract runner") {
		t.Errorf("expected output to contain echo text, got: %s", res.OutputSnippet)
	}
}

func TestRunVerificationContract_Fail(t *testing.T) {
	dir := t.TempDir()
	slug := "runner-fail-goal"
	goalDir := filepath.Join(dir, "goals", slug)
	_ = os.MkdirAll(goalDir, 0755)

	c, err := CreateVerificationContract(dir, "exit 2", 10, 5, 0, nil)
	if err != nil {
		t.Fatalf("create contract: %v", err)
	}
	if err := SaveContract(dir, slug, c); err != nil {
		t.Fatalf("save contract: %v", err)
	}

	res, err := RunVerificationContract(context.Background(), dir, slug)
	if err != nil {
		t.Fatalf("RunVerificationContract returned error on normal non-zero exit: %v", err)
	}
	if res.Passed {
		t.Errorf("expected contract to fail on exit 2")
	}
	if res.ExitCode != 2 {
		t.Errorf("expected exit code 2, got %d", res.ExitCode)
	}
	if !strings.Contains(res.FailureReason, "exited with code 2") {
		t.Errorf("unexpected failure reason: %s", res.FailureReason)
	}
}

func TestRunVerificationContract_Timeout(t *testing.T) {
	dir := t.TempDir()
	slug := "runner-timeout-goal"
	goalDir := filepath.Join(dir, "goals", slug)
	_ = os.MkdirAll(goalDir, 0755)

	// Contract with 1 second timeout running a 5 second sleep
	c, err := CreateVerificationContract(dir, "sleep 5", 1, 5, 0, nil)
	if err != nil {
		t.Fatalf("create contract: %v", err)
	}
	if err := SaveContract(dir, slug, c); err != nil {
		t.Fatalf("save contract: %v", err)
	}

	res, err := RunVerificationContract(context.Background(), dir, slug)
	if err != nil {
		t.Fatalf("RunVerificationContract unexpected error: %v", err)
	}
	if res.Passed {
		t.Errorf("expected timeout failure")
	}
	if !strings.Contains(res.FailureReason, "timed out after") {
		t.Errorf("unexpected failure reason for timeout: %s", res.FailureReason)
	}
}

func TestCircuitBreaker_MaxRounds(t *testing.T) {
	dir := t.TempDir()
	slug := "circuit-breaker-max-goal"
	goalDir := filepath.Join(dir, "goals", slug)
	_ = os.MkdirAll(goalDir, 0755)

	// Goal contract with MaxRounds: 3
	c, err := CreateVerificationContract(dir, "exit 1", 10, 3, 0, nil)
	if err != nil {
		t.Fatalf("create contract: %v", err)
	}
	_ = SaveContract(dir, slug, c)

	// Create initial claim and lock
	claim := &GoalClaim{
		GoalSlug:  slug,
		Agent:     "test-agent",
		Status:    "in_progress",
		ClaimedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	data, _ := json.MarshalIndent(claim, "", "  ")
	_ = os.WriteFile(filepath.Join(goalDir, "claim.json"), data, 0644)
	_ = os.WriteFile(filepath.Join(goalDir, ".claim.lock"), []byte("locked"), 0644)

	// Iteration 1: failure
	cl, tripped, err := RecordClaimIteration(dir, slug, false, "syntax error line 10")
	if err != nil || tripped || cl.IterationCount != 1 {
		t.Fatalf("round 1 unexpected: tripped=%v count=%d err=%v", tripped, cl.IterationCount, err)
	}

	// Iteration 2: failure
	cl, tripped, err = RecordClaimIteration(dir, slug, false, "syntax error line 12")
	if err != nil || tripped || cl.IterationCount != 2 {
		t.Fatalf("round 2 unexpected: tripped=%v count=%d err=%v", tripped, cl.IterationCount, err)
	}

	// Iteration 3: failure -> reaches MaxRounds (3) -> TRIPS!
	cl, tripped, err = RecordClaimIteration(dir, slug, false, "syntax error line 14")
	if err != nil {
		t.Fatalf("round 3 error: %v", err)
	}
	if !tripped {
		t.Errorf("expected circuit breaker to trip at round 3")
	}
	if cl.Status != "halted_circuit_breaker" {
		t.Errorf("expected status halted_circuit_breaker, got %s", cl.Status)
	}
	// Verify lock file was automatically evicted
	if fileExists(filepath.Join(goalDir, ".claim.lock")) {
		t.Errorf("expected .claim.lock to be removed on circuit breaker trip")
	}
}

func TestCircuitBreaker_RepeatedErrors(t *testing.T) {
	dir := t.TempDir()
	slug := "circuit-breaker-repeat-goal"
	goalDir := filepath.Join(dir, "goals", slug)
	_ = os.MkdirAll(goalDir, 0755)

	// Goal contract with MaxRounds: 10
	c, err := CreateVerificationContract(dir, "exit 1", 10, 10, 0, nil)
	if err != nil {
		t.Fatalf("create contract: %v", err)
	}
	_ = SaveContract(dir, slug, c)

	claim := &GoalClaim{
		GoalSlug:  slug,
		Agent:     "test-agent",
		Status:    "in_progress",
		ClaimedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	data, _ := json.MarshalIndent(claim, "", "  ")
	_ = os.WriteFile(filepath.Join(goalDir, "claim.json"), data, 0644)
	_ = os.WriteFile(filepath.Join(goalDir, ".claim.lock"), []byte("locked"), 0644)

	errSignature := "undefined: FooBar"

	// Round 1
	_, tripped, _ := RecordClaimIteration(dir, slug, false, errSignature)
	if tripped {
		t.Fatalf("unexpected trip round 1")
	}
	// Round 2
	_, tripped, _ = RecordClaimIteration(dir, slug, false, errSignature)
	if tripped {
		t.Fatalf("unexpected trip round 2")
	}
	// Round 3 with same error -> TRIPS on 3 consecutive identical errors
	cl, tripped, _ := RecordClaimIteration(dir, slug, false, errSignature)
	if !tripped {
		t.Errorf("expected circuit breaker to trip on 3rd identical error")
	}
	if cl.ConsecutiveFailures != 3 {
		t.Errorf("expected 3 consecutive failures, got %d", cl.ConsecutiveFailures)
	}
}

func TestAutoCertifyGoal_Success(t *testing.T) {
	dir := t.TempDir()
	slug := "autocert-goal"
	goalDir := filepath.Join(dir, "goals", slug)
	_ = os.MkdirAll(goalDir, 0755)
	_ = os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte("# Autocert Goal\n"), 0644)

	c, err := CreateVerificationContract(dir, "echo 'tests passed'", 10, 5, 0, nil)
	if err != nil {
		t.Fatalf("create contract: %v", err)
	}
	_ = SaveContract(dir, slug, c)

	// Claim goal
	_ = os.WriteFile(filepath.Join(goalDir, "claim.json"), []byte(`{"goal_slug":"autocert-goal","status":"in_progress"}`), 0644)
	_ = os.WriteFile(filepath.Join(goalDir, ".claim.lock"), []byte("locked"), 0644)

	cert, res, err := AutoCertifyGoal(context.Background(), dir, slug, "@antigravity", "e2e passed")
	if err != nil {
		t.Fatalf("AutoCertifyGoal failed: %v", err)
	}
	if !res.Passed {
		t.Errorf("expected result to pass")
	}
	if !cert.Verified {
		t.Errorf("expected cert to be verified")
	}
	if fileExists(filepath.Join(goalDir, ".claim.lock")) {
		t.Errorf("expected .claim.lock to be cleaned up after certification")
	}
	if !fileExists(filepath.Join(goalDir, "certification.json")) {
		t.Errorf("expected certification.json to exist")
	}
}
