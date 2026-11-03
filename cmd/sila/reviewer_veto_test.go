package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/assess"
	"github.com/jadmadi/sila/internal/goals"
)

func TestGateReviewerVetoSentry_CleanWhenNoVetoes(t *testing.T) {
	tempDir := t.TempDir()
	check := gateReviewerVetoSentry(tempDir)
	if check.Status != "pass" {
		t.Fatalf("expected pass status for clean directory, got: %s (detail: %s)", check.Status, check.Detail)
	}
	if !gateClean([]gateCheck{check}) {
		t.Fatalf("expected gateClean=true when no vetoes exist")
	}
}

func TestGateReviewerVetoSentry_BlocksOnActiveVeto(t *testing.T) {
	tempDir := t.TempDir()
	goalSlug := "test-vetoed-goal"

	// Record an active veto
	_, err := assess.RecordReviewerVeto(tempDir, goalSlug, "@mystery-auditor", "sess-123", "blocking security flaw", "auth.go")
	if err != nil {
		t.Fatalf("record veto: %v", err)
	}

	check := gateReviewerVetoSentry(tempDir)
	if check.Status != "fail" {
		t.Fatalf("expected fail status when active veto exists, got: %s", check.Status)
	}
	if !strings.Contains(check.Detail, goalSlug) || !strings.Contains(check.Detail, "@mystery-auditor") {
		t.Fatalf("expected detail to contain goal and auditor, got: %s", check.Detail)
	}

	// Verify that gateClean fails closed
	if gateClean([]gateCheck{check}) {
		t.Fatalf("expected gateClean=false when reviewer-veto failed")
	}
}

func TestCertifyGoal_RejectsSelfAuditAndActiveVeto(t *testing.T) {
	tempDir := t.TempDir()
	goalSlug := "test-self-audit-goal"
	goalDir := filepath.Join(tempDir, "goals", goalSlug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Create goal.md with ImplementedBy = @antigravity
	goalMdContent := `# Goal: Test
## Chain of Custody (Isnad)
- **Registered By**: @antigravity
- **Spec Audited By**: @reviewer
- **Implemented By**: @antigravity
- **Mystery Audited By**: Pending Mystery Audit
`
	if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalMdContent), 0644); err != nil {
		t.Fatalf("write goal.md: %v", err)
	}
	evidenceFile := filepath.Join(goalDir, "evidence.log")
	if err := os.WriteFile(evidenceFile, []byte("all tests passed\n"), 0644); err != nil {
		t.Fatalf("write evidence.log: %v", err)
	}

	// 1. Attempt self-audit certification by the producer (@antigravity)
	_, err := goals.CertifyGoal(tempDir, goalSlug, evidenceFile, "@antigravity", "self-certifying")
	if err == nil {
		t.Fatalf("expected self-audit error when producer certifies own goal, got nil")
	}
	if !strings.Contains(err.Error(), "self-audit prohibited") {
		t.Fatalf("expected error to mention 'self-audit prohibited', got: %v", err)
	}

	// 2. Record an active reviewer veto
	_, err = assess.RecordReviewerVeto(tempDir, goalSlug, "@mystery-auditor", "sess-456", "regression caught", "code.go")
	if err != nil {
		t.Fatalf("record veto: %v", err)
	}

	// 3. Attempt certification by an independent reviewer (@independent-auditor) while veto is active
	_, err = goals.CertifyGoal(tempDir, goalSlug, evidenceFile, "@independent-auditor", "attempting certification")
	if err == nil {
		t.Fatalf("expected error when certifying a goal with active veto, got nil")
	}
	if !strings.Contains(err.Error(), "active non-overrulable reviewer veto") {
		t.Fatalf("expected error to mention 'active non-overrulable reviewer veto', got: %v", err)
	}
}
