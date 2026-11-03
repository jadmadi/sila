package crew

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type mockAuditor struct {
	result *VictoryAuditResult
	err    error
	calls  int
}

func (m *mockAuditor) AuditVictory(cm *Crewmate) (*VictoryAuditResult, error) {
	m.calls++
	if m.err != nil {
		return nil, m.err
	}
	return m.result, nil
}

func TestSentinelOrchestrator_ApprovedVictory(t *testing.T) {
	dir := setupCrewRepo(t)
	mgr, err := NewCrewManager(dir)
	if err != nil {
		t.Fatalf("NewCrewManager: %v", err)
	}

	cm := &Crewmate{
		ID:        "test-worker-1",
		Harness:   "claude",
		GoalSlug:  "sample-goal",
		Worktree:  dir,
		Status:    StatusCompleted,
		SpawnedAt: time.Now().Add(-10 * time.Minute),
	}
	if err := mgr.SaveCrewmate(cm); err != nil {
		t.Fatalf("save: %v", err)
	}

	mock := &mockAuditor{
		result: &VictoryAuditResult{
			Passed:       true,
			Verdict:      VerdictApproved,
			AuditorAgent: "isolated-auditor",
			Notes:        "verified clean test run",
		},
	}

	cfg := DefaultSentinelConfig()
	orchestrator := NewSentinelOrchestrator(mgr, cfg, mock)

	rep, err := orchestrator.RunPass()
	if err != nil {
		t.Fatalf("RunPass: %v", err)
	}

	if rep.Approved != 1 {
		t.Errorf("expected 1 approved, got %d", rep.Approved)
	}
	if rep.Rejected != 0 {
		t.Errorf("expected 0 rejected, got %d", rep.Rejected)
	}
	if rep.HaltedRunaway != 0 {
		t.Errorf("expected 0 halted runaway, got %d", rep.HaltedRunaway)
	}

	saved, _ := mgr.Get("test-worker-1")
	if saved.Status != StatusCompleted {
		t.Errorf("expected StatusCompleted, got %s", saved.Status)
	}
	if saved.AuditorVerdict != string(VerdictApproved) {
		t.Errorf("expected AuditorVerdict APPROVED, got %s", saved.AuditorVerdict)
	}
}

func TestSentinelOrchestrator_SelfCertificationRejectionAndBoundedRetries(t *testing.T) {
	dir := setupCrewRepo(t)
	mgr, err := NewCrewManager(dir)
	if err != nil {
		t.Fatalf("NewCrewManager: %v", err)
	}

	cm := &Crewmate{
		ID:         "test-worker-self-cert",
		Harness:    "devin",
		GoalSlug:   "sample-goal",
		Worktree:   dir,
		Status:     StatusCompleted,
		RetryCount: 0,
		MaxRetries: 2,
		SpawnedAt:  time.Now().Add(-5 * time.Minute),
	}
	_ = mgr.SaveCrewmate(cm)

	mock := &mockAuditor{
		result: &VictoryAuditResult{
			Passed:        false,
			SelfCertified: true,
			Verdict:       VerdictSelfCertificationRejected,
			AuditorAgent:  "isolated-auditor",
			Errors:        []string{"uncommitted changes in worktree"},
		},
	}

	cfg := SentinelConfig{MaxRetries: 2, AuditorAgent: "isolated-auditor"}
	orchestrator := NewSentinelOrchestrator(mgr, cfg, mock)

	// Pass 1: Victory rejected, should retry (retry count 1/2)
	rep1, err := orchestrator.RunPass()
	if err != nil {
		t.Fatalf("pass 1 failed: %v", err)
	}
	if rep1.Rejected != 1 || rep1.HaltedRunaway != 0 {
		t.Fatalf("pass 1: expected 1 rejected and 0 halted, got rep: %+v", rep1)
	}

	cm1, _ := mgr.Get("test-worker-self-cert")
	if cm1.Status != StatusRunning {
		t.Errorf("pass 1: expected status reset to running for retry, got %s", cm1.Status)
	}
	if cm1.RetryCount != 1 {
		t.Errorf("pass 1: expected retry_count 1, got %d", cm1.RetryCount)
	}

	// Worker finishes again claiming victory
	cm1.Status = StatusCompleted
	_ = mgr.SaveCrewmate(cm1)

	// Pass 2: Victory rejected again, reaches max_retries (2/2) -> Halted runaway!
	rep2, err := orchestrator.RunPass()
	if err != nil {
		t.Fatalf("pass 2 failed: %v", err)
	}
	if rep2.HaltedRunaway != 1 {
		t.Fatalf("pass 2: expected 1 halted runaway, got rep: %+v", rep2)
	}

	cm2, _ := mgr.Get("test-worker-self-cert")
	if cm2.Status != StatusFailed {
		t.Errorf("pass 2: expected status StatusFailed when retry budget exhausted, got %s", cm2.Status)
	}
	if cm2.RetryCount != 2 {
		t.Errorf("pass 2: expected retry_count 2, got %d", cm2.RetryCount)
	}
}

func TestDefaultVictoryAuditor_SelfCertificationDirtyCheck(t *testing.T) {
	dir := setupCrewRepo(t)
	auditor := NewDefaultVictoryAuditor("test-auditor")

	cm := &Crewmate{
		ID:       "dirty-worker",
		Worktree: dir,
		Branch:   "main",
	}

	// 1. Create an uncommitted file to simulate self-certifying without atomic commit
	dirtyFile := filepath.Join(dir, "dirty.txt")
	if err := os.WriteFile(dirtyFile, []byte("uncommitted change"), 0644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}

	res, err := auditor.AuditVictory(cm)
	if err != nil {
		t.Fatalf("AuditVictory returned unexpected error: %v", err)
	}

	if res.Passed {
		t.Fatalf("expected audit to fail for dirty worktree")
	}
	if res.Verdict != VerdictSelfCertificationRejected {
		t.Fatalf("expected VerdictSelfCertificationRejected, got %s", res.Verdict)
	}
	if !res.SelfCertified {
		t.Fatalf("expected SelfCertified=true")
	}
}

func TestSentinelOrchestrator_AuditorErrorHandled(t *testing.T) {
	dir := setupCrewRepo(t)
	mgr, _ := NewCrewManager(dir)

	cm := &Crewmate{
		ID:       "error-worker",
		Worktree: dir,
		Status:   StatusCompleted,
	}
	_ = mgr.SaveCrewmate(cm)

	mock := &mockAuditor{
		err: errors.New("auditor context crashed"),
	}

	orchestrator := NewSentinelOrchestrator(mgr, DefaultSentinelConfig(), mock)
	rep, err := orchestrator.RunPass()
	if err != nil {
		t.Fatalf("RunPass error: %v", err)
	}
	if rep.Rejected != 1 {
		t.Fatalf("expected 1 rejected on auditor error, got %d", rep.Rejected)
	}
}
