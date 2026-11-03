package goals

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setupTestSuccessorGoal(t *testing.T, dir, slug, predSlug string) *WorkPackage {
	t.Helper()
	goalDir := filepath.Join(dir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatalf("mkdir goalDir: %v", err)
	}

	goalMD := "# Goal: " + slug + "\n\n- **Tier**: roadmap\n- **Shape**: ship\n- **Depends On**: " + predSlug + "\n- **Files To Touch**: release.go\n"
	factsMD := "# Facts: " + slug + "\n\n- Contracts: release.go\n"
	planMD := "# Plan: " + slug + "\n\n- [ ] Step 1\n"

	_ = os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalMD), 0644)
	_ = os.WriteFile(filepath.Join(goalDir, "facts.md"), []byte(factsMD), 0644)
	_ = os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte(planMD), 0644)

	_ = os.WriteFile(filepath.Join(dir, "release.go"), []byte("package sample\n"), 0644)

	wp := &WorkPackage{
		SchemaVersion: WorkPackageSchemaVersion,
		GoalSlug:      slug,
		Revision:      1,
		Purpose:       "Successor release goal requiring proceed",
		Tasks: []TaskContract{
			{
				ID:            "T-001",
				Title:         "Release implementation",
				Context:       "Context for release task",
				Why:           "Fulfill release charter",
				Files:         TaskFiles{Edit: []string{"release.go"}},
				AcceptanceIDs: []string{"AC-001"},
				Checks: []Check{
					{
						ID:               "C-001",
						Executable:       "go",
						Args:             []string{"test"},
						TimeoutSeconds:   10,
						ExpectedExitCode: 0,
						Scope:            "unit",
						Capability:       "local",
						Assertions:       []Assertion{{Kind: "go_test_pass", Target: "test", Expected: true}},
					},
				},
				ReturnFormat: TaskResultReturnFormat,
			},
		},
		Acceptance: []AcceptanceCase{
			{
				ID:            "AC-001",
				Requirement:   "Release must pass tests",
				Given:         "Code is modified",
				Action:        "Run tests",
				Expected:      "Tests pass cleanly",
				RequiredScope: "unit",
				Mandatory:     true,
				CheckIDs:      []string{"C-001"},
			},
		},
		DependencyConditions: []DependencyCondition{
			{
				Kind:     DepConditionDecisionEquals,
				GoalSlug: predSlug,
				Artifact: "decision.json",
				Pointer:  "/decision",
				Equals:   "proceed",
			},
			{
				Kind:     DepConditionAcceptedGoal,
				GoalSlug: predSlug,
			},
		},
	}

	if err := SaveWorkPackage(dir, slug, wp); err != nil {
		t.Fatalf("SaveWorkPackage: %v", err)
	}

	review := validSpecReviewFixture(wp)
	if err := SaveSpecReview(dir, slug, review); err != nil {
		t.Fatalf("SaveSpecReview: %v", err)
	}

	return wp
}

func updatePredecessorDecision(t *testing.T, dir, slug, decisionVal string) {
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
	exit0 := 0
	ev := &TaskEvidence{
		SchemaVersion: TaskEvidenceSchemaVersion,
		AttemptID:     "att-2",
		GoalSlug:      slug,
		TaskID:        "T-001",
		CaseID:        "AC-001",
		CheckID:       "C-001",
		Outcome:       "pass",
		Origin:        "runner",
		Executable:    "go",
		ExitCode:      &exit0,
		Artifacts: []EvidenceArtifact{
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

	wp, _ := LoadWorkPackage(dir, slug)
	specDigest, _ := WorkPackageDigest(wp)

	taskAcc := &AcceptanceResult{
		SchemaVersion:  AcceptanceResultSchemaVersion,
		GoalSlug:       slug,
		TaskID:         "T-001",
		Accepted:       true,
		EvidenceStatus: EvidenceStatusAccepted,
		SpecDigest:     specDigest,
		ReceiptPaths:   []string{filepath.Join("evidence", "T-001", "receipt.json")},
		ReviewedAt:     time.Now().UTC().Format(time.RFC3339),
	}
	if err := SaveTaskAcceptance(dir, slug, "T-001", taskAcc, nil, nil); err != nil {
		t.Fatalf("SaveTaskAcceptance: %v", err)
	}

	goalAcc := &AcceptanceResult{
		SchemaVersion:  AcceptanceResultSchemaVersion,
		GoalSlug:       slug,
		Accepted:       true,
		EvidenceStatus: EvidenceStatusAccepted,
		SpecDigest:     specDigest,
		ReceiptPaths:   []string{filepath.Join("evidence", "T-001", "receipt.json")},
		ReviewedAt:     time.Now().UTC().Format(time.RFC3339),
	}
	if err := SaveGoalAcceptance(dir, slug, goalAcc); err != nil {
		t.Fatalf("SaveGoalAcceptance: %v", err)
	}
}

// TestDependencyGateParity exercises AC-004 and AC-005:
// AC-004: ready, blocked, view, and claim agree when a predecessor says revise.
// AC-005: Changing the accepted decision through a valid new revision changes eligibility on the next read.
func TestDependencyGateParity(t *testing.T) {
	tmp := t.TempDir()

	// 1. Setup predecessor assessment with revise decision
	setupAcceptedPredecessor(t, tmp, "assessment", "revise")

	// 2. Setup successor release goal requiring proceed
	setupTestSuccessorGoal(t, tmp, "release", "assessment")

	// 3. AC-004: Verify agreement across ready list, blocked list, view, and claim
	audit, err := ScanGoals(tmp)
	if err != nil {
		t.Fatalf("ScanGoals: %v", err)
	}

	var releaseGoal *Goal
	for i := range audit.Goals {
		if audit.Goals[i].Slug == "release" {
			releaseGoal = &audit.Goals[i]
			break
		}
	}
	if releaseGoal == nil {
		t.Fatalf("release goal not found in audit result")
	}

	if !releaseGoal.Blocked {
		t.Errorf("AC-004: expected releaseGoal.Blocked to be true, got false")
	}
	if releaseGoal.Independent {
		t.Errorf("AC-004: expected releaseGoal.Independent to be false, got true")
	}
	if !strings.Contains(releaseGoal.BlockedReason, "DEPENDENCY_DECISION_MISMATCH") {
		t.Errorf("AC-004: expected BlockedReason to mention DEPENDENCY_DECISION_MISMATCH, got: %q", releaseGoal.BlockedReason)
	}

	// Ready filter check: release must NOT be ready
	for _, g := range audit.Goals {
		if g.Status == "pending" && !g.Blocked && len(g.FileConflicts) == 0 {
			if g.Slug == "release" {
				t.Errorf("AC-004: release goal must not appear in ready list")
			}
		}
	}

	// View check: view must show blocked
	view, err := BuildGoalView(tmp, releaseGoal)
	if err != nil {
		t.Fatalf("BuildGoalView: %v", err)
	}
	if !view.Blocked {
		t.Errorf("AC-004: expected view.Blocked to be true, got false")
	}
	if !strings.Contains(view.BlockedReason, "DEPENDENCY_DECISION_MISMATCH") {
		t.Errorf("AC-004: expected view.BlockedReason to contain DEPENDENCY_DECISION_MISMATCH, got: %q", view.BlockedReason)
	}

	// Claim check: claim without force must fail
	claim, err := ClaimGoal(tmp, "release", "@agent", "attempt claim", false)
	if err == nil {
		t.Errorf("AC-004: expected claim to fail when predecessor says revise, got claim: %+v", claim)
	}
	if !strings.Contains(err.Error(), "DEPENDENCY_DECISION_MISMATCH") {
		t.Errorf("AC-004: expected claim error to name DEPENDENCY_DECISION_MISMATCH, got: %v", err)
	}

	// 4. AC-005: Update predecessor decision to proceed and verify immediate change on next read
	updatePredecessorDecision(t, tmp, "assessment", "proceed")

	audit2, err := ScanGoals(tmp)
	if err != nil {
		t.Fatalf("ScanGoals after update: %v", err)
	}

	var releaseGoal2 *Goal
	for i := range audit2.Goals {
		if audit2.Goals[i].Slug == "release" {
			releaseGoal2 = &audit2.Goals[i]
			break
		}
	}
	if releaseGoal2 == nil {
		t.Fatalf("release goal not found in second audit")
	}

	if releaseGoal2.Blocked {
		t.Errorf("AC-005: expected releaseGoal.Blocked to be false after proceed, got true (reason: %s)", releaseGoal2.BlockedReason)
	}

	foundInReady := false
	for _, g := range audit2.Goals {
		if g.Status == "pending" && !g.Blocked && len(g.FileConflicts) == 0 {
			if g.Slug == "release" {
				foundInReady = true
				break
			}
		}
	}
	if !foundInReady {
		t.Errorf("AC-005: release goal must appear in ready list after predecessor decision updated to proceed")
	}

	view2, err := BuildGoalView(tmp, releaseGoal2)
	if err != nil {
		t.Fatalf("BuildGoalView after update: %v", err)
	}
	if view2.Blocked {
		t.Errorf("AC-005: expected view.Blocked to be false, got true")
	}

	// Claim must now succeed cleanly
	claim2, err := ClaimGoal(tmp, "release", "@agent", "valid claim", false)
	if err != nil {
		t.Fatalf("AC-005: expected claim to succeed after proceed, got: %v", err)
	}
	if claim2 == nil || claim2.GoalSlug != "release" {
		t.Errorf("AC-005: expected valid claim object, got: %+v", claim2)
	}
}

// TestConditionalClaimForce exercises Requirement 2:
// Evaluate before lease writes and forbid --force from overriding a structured decision gate.
func TestConditionalClaimForce(t *testing.T) {
	tmp := t.TempDir()

	// Predecessor with revise decision
	setupAcceptedPredecessor(t, tmp, "assessment", "revise")

	// Successor requiring proceed
	setupTestSuccessorGoal(t, tmp, "release", "assessment")

	// Attempt claim with force=true
	claim, err := ClaimGoal(tmp, "release", "@agent", "forced attempt", true)
	if err == nil {
		t.Fatalf("expected forced claim to fail on structured decision gate, got claim: %+v", claim)
	}

	if !strings.Contains(err.Error(), "cannot be overridden with --force") {
		t.Errorf("expected error to forbid --force override, got: %v", err)
	}
	if !strings.Contains(err.Error(), "DEPENDENCY_DECISION_MISMATCH") {
		t.Errorf("expected error to name DEPENDENCY_DECISION_MISMATCH, got: %v", err)
	}

	// Assert no lease or claim file was created
	claimPath := filepath.Join(tmp, "goals", "release", "claim.json")
	if fileExists(claimPath) {
		t.Errorf("claim.json was written despite forced claim rejection!")
	}
}

// TestDependencyLegacy exercises AC-006:
// Unrelated legacy goal lists and force behavior retain compatibility.
func TestDependencyLegacy(t *testing.T) {
	tmp := t.TempDir()

	// Setup legacy predecessor (unimplemented) and legacy successor
	setupGoalWithMeta(t, tmp, "legacy-a", "Legacy A", "", 1)
	setupGoalWithMeta(t, tmp, "legacy-b", "Legacy B", "legacy-a", 2)

	audit, err := ScanGoals(tmp)
	if err != nil {
		t.Fatalf("ScanGoals: %v", err)
	}

	var legacyB *Goal
	for i := range audit.Goals {
		if audit.Goals[i].Slug == "legacy-b" {
			legacyB = &audit.Goals[i]
			break
		}
	}
	if legacyB == nil {
		t.Fatalf("legacy-b not found in scan")
	}
	if !legacyB.Blocked {
		t.Errorf("expected legacy-b to be blocked by legacy-a")
	}

	// 1. Regular claim without force fails
	_, err = ClaimGoal(tmp, "legacy-b", "@agent", "attempt", false)
	if err == nil {
		t.Fatalf("expected regular claim on legacy-b to fail without --force")
	}
	if !strings.Contains(err.Error(), "use --force to override") {
		t.Errorf("expected legacy claim error to advise --force, got: %v", err)
	}

	// 2. Claim with force succeeds for legacy goal
	claim, err := ClaimGoal(tmp, "legacy-b", "@agent", "forced takeover", true)
	if err != nil {
		t.Fatalf("expected forced claim on legacy goal to succeed, got: %v", err)
	}
	if claim == nil || claim.GoalSlug != "legacy-b" {
		t.Errorf("expected valid claim on legacy-b, got: %+v", claim)
	}

	claimPath := filepath.Join(tmp, "goals", "legacy-b", "claim.json")
	if !fileExists(claimPath) {
		t.Errorf("claim.json was not written for successful forced legacy claim")
	}
}
