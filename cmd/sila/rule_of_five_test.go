package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/assess"
	"github.com/jadmadi/sila/internal/goals"
)

func TestGateRuleOfFiveCompletion_NoActiveGoals(t *testing.T) {
	tempDir := t.TempDir()
	check := gateRuleOfFiveCompletion(tempDir)
	if check.Status != "pass" {
		t.Fatalf("expected pass when no active claimed goals exist, got %s: %s", check.Status, check.Detail)
	}
}

func TestGateRuleOfFiveCompletion_DetectsHollowStubsInStaged(t *testing.T) {
	tempDir := t.TempDir()
	goalSlug := "test-gate-rule-of-five"
	goalDir := filepath.Join(tempDir, "goals", goalSlug)
	_ = os.MkdirAll(goalDir, 0755)

	goalMd := `# Goal: Test Gate Rule of Five

## Files to Touch
- internal/assess/stub.go

## Strict Acceptance Criteria
1. Full implementation without stubs.
`
	_ = os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalMd), 0644)
	_ = os.WriteFile(filepath.Join(goalDir, "facts.md"), []byte("# Facts\n"), 0644)

	// Create charter
	_, err := goals.CreateCharter(tempDir, goalSlug, "@test-agent", "session-123",
		[]string{"internal/assess/stub.go"}, nil, nil, 30*time.Minute)
	if err != nil {
		t.Fatalf("failed to create charter: %v", err)
	}
	charter := &goals.GoalCharter{
		GoalSlug:   goalSlug,
		Agent:      "test-agent",
		AllowPaths: []string{"internal/assess/stub.go"},
	}
	_ = goals.SaveCharter(tempDir, charter)

	// Write file with hollow stub
	stubDir := filepath.Join(tempDir, "internal", "assess")
	_ = os.MkdirAll(stubDir, 0755)
	stubPath := filepath.Join(stubDir, "stub.go")
	_ = os.WriteFile(stubPath, []byte("package assess\n\nfunc Stub() {\n\tpanic(\"TODO: implement\")\n}\n"), 0644)

	rep, err := assess.CheckRuleOfFiveCompletion(tempDir, goalSlug, []string{"internal/assess/stub.go"})
	if err != nil {
		t.Fatalf("CheckRuleOfFiveCompletion error: %v", err)
	}
	if rep == nil || rep.Clean {
		t.Fatalf("expected CheckRuleOfFiveCompletion to detect hollow stub, got clean")
	}

	foundStubViolation := false
	for _, v := range rep.Violations {
		if len(v) > 0 && (v[:7] == "Point 3" || v[:7] == "Point 5") {
			foundStubViolation = true
			break
		}
	}
	if !foundStubViolation {
		t.Errorf("expected Point 3 or Point 5 violation for hollow stub, got: %v", rep.Violations)
	}
}

func TestGateRuleOfFiveCompletion_DetectsBlastRadiusViolation(t *testing.T) {
	tempDir := t.TempDir()
	goalSlug := "test-blast-radius-gate"
	goalDir := filepath.Join(tempDir, "goals", goalSlug)
	_ = os.MkdirAll(goalDir, 0755)

	goalMd := `# Goal: Test Blast Radius Gate

## Files to Touch
- internal/core/handler.go
`
	_ = os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalMd), 0644)

	// Staged file outside allowed charter boundary
	leakyFiles := []string{
		"internal/core/handler.go",
		"worker/unauthorized.ts",
	}

	rep, err := assess.CheckRuleOfFiveCompletion(tempDir, goalSlug, leakyFiles)
	if err != nil {
		t.Fatalf("CheckRuleOfFiveCompletion error: %v", err)
	}
	if rep == nil || rep.Clean {
		t.Fatalf("expected blast radius violation for unauthorized file")
	}
	if rep.BlastRadius.Passed {
		t.Errorf("expected BlastRadius.Passed = false")
	}
	if len(rep.BlastRadius.CollateralLeaks) != 1 || rep.BlastRadius.CollateralLeaks[0] != "worker/unauthorized.ts" {
		t.Errorf("expected collateral leak worker/unauthorized.ts, got: %v", rep.BlastRadius.CollateralLeaks)
	}
}
