package goals

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupGateTestGoal scaffolds a goal directory with a valid goal.md, facts.md, plan.md, and optional work-package.
func setupGateTestGoal(t *testing.T, tmpDir, slug string, withPackage, withReview bool) *WorkPackage {
	t.Helper()
	goalDir := filepath.Join(tmpDir, "goals", slug)
	_ = os.MkdirAll(goalDir, 0755)

	goalMD := "# Goal: " + slug + "\n\n- **Tier**: roadmap\n- **Shape**: ship\n- **Files To Touch**: engine.go\n"
	factsMD := "# Facts: " + slug + "\n\n- Contracts: engine.go\n"
	planMD := "# Plan: " + slug + "\n\n- [ ] Step 1\n"

	_ = os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalMD), 0644)
	_ = os.WriteFile(filepath.Join(goalDir, "facts.md"), []byte(factsMD), 0644)
	_ = os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte(planMD), 0644)

	// Create referenced source file on disk
	_ = os.WriteFile(filepath.Join(tmpDir, "engine.go"), []byte("package engine\n"), 0644)

	if !withPackage {
		return nil
	}

	_, pkg := setupValidReadinessFixture(t)
	pkg.GoalSlug = slug
	pkg.Tasks[0].Files.Read = []string{}
	pkg.Tasks[0].Files.Edit = []string{"engine.go"}
	pkg.Tasks[0].Files.Create = []string{"engine_new.go"}
	pkg.Tasks[0].References = []Reference{}

	if err := SaveWorkPackage(tmpDir, slug, pkg); err != nil {
		t.Fatalf("SaveWorkPackage: %v", err)
	}

	if withReview {
		review := validSpecReviewFixture(pkg)
		if err := SaveSpecReview(tmpDir, slug, review); err != nil {
			t.Fatalf("SaveSpecReview: %v", err)
		}
	}

	return pkg
}

// TestClaimRequiresTaskReadiness verifies AC-007 and AC-008:
// - Claim fails on unready structured package leaving no claim or charter files.
// - Ready package can be claimed, and active lease remains protected.
func TestClaimRequiresTaskReadiness(t *testing.T) {
	tmpDir := t.TempDir()
	slug := "unready-claim-goal"

	// Package without spec review is not ready
	setupGateTestGoal(t, tmpDir, slug, true, false)

	// AC-007: Claiming unready goal must fail
	_, err := ClaimGoal(tmpDir, slug, "agent-tester", "attempt claim", false)
	if err == nil {
		t.Fatalf("expected claim on unready goal to fail, got nil")
	}
	if !strings.Contains(err.Error(), "SPEC_NOT_READY") && !strings.Contains(err.Error(), "SPEC_REVIEW_MISSING") {
		t.Errorf("expected SPEC_NOT_READY / SPEC_REVIEW_MISSING in error, got: %v", err)
	}

	// AC-007: No claim.json or charter file should exist on disk
	goalDir := filepath.Join(tmpDir, "goals", slug)
	if fileExists(filepath.Join(goalDir, "claim.json")) {
		t.Errorf("claim.json was written despite readiness failure!")
	}
	if fileExists(filepath.Join(goalDir, "charter.json")) {
		t.Errorf("charter.json was written despite readiness failure!")
	}

	// Add spec review to make it ready
	pkg, _ := LoadWorkPackage(tmpDir, slug)
	review := validSpecReviewFixture(pkg)
	if err := SaveSpecReview(tmpDir, slug, review); err != nil {
		t.Fatalf("SaveSpecReview: %v", err)
	}

	// AC-008: Ready package can be claimed successfully
	claim, err := ClaimGoal(tmpDir, slug, "agent-tester", "claim ready goal", false)
	if err != nil {
		t.Fatalf("expected claim on ready goal to succeed, got: %v", err)
	}
	if claim == nil || claim.Agent != "agent-tester" {
		t.Errorf("unexpected claim returned: %+v", claim)
	}

	// AC-008: Active lease remains protected against competing agent
	_, err = ClaimGoal(tmpDir, slug, "competing-agent", "competing claim", false)
	if err == nil {
		t.Fatalf("expected competing claim on active lease to fail, got nil")
	}
}

// TestReadinessForceBoundary verifies AC-009:
// - Structured --force request cannot bypass SPEC_NOT_READY.
func TestReadinessForceBoundary(t *testing.T) {
	tmpDir := t.TempDir()
	slug := "force-boundary-goal"

	// Structured goal with missing review
	setupGateTestGoal(t, tmpDir, slug, true, false)

	// AC-009: force=true MUST NOT bypass readiness on structured goals
	_, err := ClaimGoal(tmpDir, slug, "agent-tester", "attempt forced claim", true)
	if err == nil {
		t.Fatalf("expected --force claim on unready structured goal to fail, got nil")
	}
	if !strings.Contains(err.Error(), "SPEC_NOT_READY") {
		t.Errorf("expected SPEC_NOT_READY in error for forced unready claim, got: %v", err)
	}
}

// TestReadinessLegacy verifies AC-009:
// - Legacy goals without work-package.json can still be claimed with existing rules.
func TestReadinessLegacy(t *testing.T) {
	tmpDir := t.TempDir()
	slug := "legacy-ready-goal"

	// Legacy goal without work-package.json
	setupGateTestGoal(t, tmpDir, slug, false, false)

	// CheckWorkPackageReady returns nil, nil for legacy goals
	diags, err := CheckWorkPackageReady(tmpDir, slug)
	if err != nil || len(diags) != 0 {
		t.Errorf("expected CheckWorkPackageReady to return nil, nil on legacy goal, got diags=%v, err=%v", diags, err)
	}

	// AC-009: Legacy goal claims cleanly with existing rules
	claim, err := ClaimGoal(tmpDir, slug, "legacy-agent", "claiming legacy goal", false)
	if err != nil {
		t.Fatalf("expected legacy claim to succeed, got: %v", err)
	}
	if claim == nil || claim.Agent != "legacy-agent" {
		t.Errorf("unexpected legacy claim result: %+v", claim)
	}
}
