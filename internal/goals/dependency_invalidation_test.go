package goals

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func setupChainedGoal(t *testing.T, dir, slug, predSlug string) *WorkPackage {
	t.Helper()
	goalDir := filepath.Join(dir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", goalDir, err)
	}

	depsClause := ""
	if predSlug != "" {
		depsClause = "- **Depends On**: " + predSlug + "\n"
	}
	goalMD := "# Goal: " + slug + "\n\n- **Tier**: roadmap\n- **Shape**: ship\n" + depsClause + "- **Files To Touch**: " + slug + ".go\n"
	factsMD := "# Facts: " + slug + "\n\n- Contracts: " + slug + ".go\n"
	planMD := "# Plan: " + slug + "\n\n- [ ] Step 1\n"

	_ = os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalMD), 0644)
	_ = os.WriteFile(filepath.Join(goalDir, "facts.md"), []byte(factsMD), 0644)
	_ = os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte(planMD), 0644)
	_ = os.WriteFile(filepath.Join(dir, slug+".go"), []byte("package sample\n"), 0644)

	var conds []DependencyCondition
	if predSlug != "" {
		conds = []DependencyCondition{
			{
				Kind:     DepConditionAcceptedGoal,
				GoalSlug: predSlug,
			},
		}
	}

	wp := &WorkPackage{
		SchemaVersion: WorkPackageSchemaVersion,
		GoalSlug:      slug,
		Revision:      1,
		Purpose:       "Goal " + slug,
		Tasks: []TaskContract{
			{
				ID:            "T-001",
				Title:         "Task 1 for " + slug,
				Context:       "Execution context",
				Why:           "Purpose of task",
				Files:         TaskFiles{Edit: []string{slug + ".go"}},
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
				Requirement:   "Task must pass",
				Given:         "Code is ready",
				Action:        "Run tests",
				Expected:      "Clean pass",
				RequiredScope: "unit",
				Mandatory:     true,
				CheckIDs:      []string{"C-001"},
			},
		},
		DependencyConditions: conds,
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

// TestDependencyInvalidation exercises AC-007 and AC-009:
// AC-007: Reopening A blocks B and C in A->B->C; independent D stays ready.
// AC-009: Restoring equivalent accepted artifacts permits explicit revalidation; old evidence is not silently relabeled fresh.
func TestDependencyInvalidation(t *testing.T) {
	tmp := t.TempDir()

	// 1. Setup chain: goal-a -> goal-b -> goal-c, plus independent goal-d
	setupAcceptedPredecessor(t, tmp, "goal-a", "proceed")
	setupChainedGoal(t, tmp, "goal-b", "goal-a")
	setupChainedGoal(t, tmp, "goal-c", "goal-b")
	setupChainedGoal(t, tmp, "goal-d", "") // independent

	// Accept goal-b and goal-c
	wpB, _ := LoadWorkPackage(tmp, "goal-b")
	digB, _ := WorkPackageDigest(wpB)
	bAcc := &AcceptanceResult{
		SchemaVersion:  AcceptanceResultSchemaVersion,
		GoalSlug:       "goal-b",
		Accepted:       true,
		EvidenceStatus: EvidenceStatusAccepted,
		SpecDigest:     digB,
	}
	_ = SaveGoalAcceptance(tmp, "goal-b", bAcc)

	wpC, _ := LoadWorkPackage(tmp, "goal-c")
	digC, _ := WorkPackageDigest(wpC)
	cAcc := &AcceptanceResult{
		SchemaVersion:  AcceptanceResultSchemaVersion,
		GoalSlug:       "goal-c",
		Accepted:       true,
		EvidenceStatus: EvidenceStatusAccepted,
		SpecDigest:     digC,
	}
	_ = SaveGoalAcceptance(tmp, "goal-c", cAcc)

	// Verify AffectedDependentTasks mapping before reopen
	affected, err := AffectedDependentTasks(tmp, "goal-a")
	if err != nil {
		t.Fatalf("AffectedDependentTasks: %v", err)
	}

	hasB := false
	hasC := false
	hasD := false
	for _, ref := range affected {
		if ref.GoalSlug == "goal-b" {
			hasB = true
		}
		if ref.GoalSlug == "goal-c" {
			hasC = true
		}
		if ref.GoalSlug == "goal-d" {
			hasD = true
		}
	}
	if !hasB || !hasC {
		t.Errorf("expected goal-b and goal-c in affected tasks, got: %+v", affected)
	}
	if hasD {
		t.Errorf("independent goal-d must not appear in affected tasks of goal-a")
	}

	// 2. AC-007: Reopen goal-a
	reopenRec, _, err := ReopenGoal(tmp, "goal-a", "tester", "defect in schema", false)
	if err != nil {
		t.Fatalf("ReopenGoal: %v", err)
	}
	if reopenRec == nil || !reopenRec.Active {
		t.Fatalf("expected active reopen record")
	}

	// Scan repository state
	audit, err := ScanGoals(tmp)
	if err != nil {
		t.Fatalf("ScanGoals: %v", err)
	}

	goalMap := make(map[string]Goal)
	for _, g := range audit.Goals {
		goalMap[g.Slug] = g
	}

	// Assert goal-b and goal-c are blocked
	gB := goalMap["goal-b"]
	if !gB.Blocked {
		t.Errorf("AC-007: expected goal-b to be blocked after goal-a reopened")
	}

	gC := goalMap["goal-c"]
	if !gC.Blocked {
		t.Errorf("AC-007: expected goal-c to be blocked after goal-a reopened")
	}

	// Assert goal-d remains ready
	gD := goalMap["goal-d"]
	if gD.Blocked {
		t.Errorf("AC-007: independent goal-d must not be blocked")
	}
	foundReadyD := false
	for _, g := range audit.Goals {
		if g.Status == "pending" && !g.Blocked && len(g.FileConflicts) == 0 && g.Slug == "goal-d" {
			foundReadyD = true
			break
		}
	}
	if !foundReadyD {
		t.Errorf("AC-007: independent goal-d must stay in ready list")
	}

	// 3. AC-009: Verify goal-b acceptance is marked stale due to reopened prerequisite
	accB, err := EvaluateGoalAcceptance(tmp, "goal-b")
	if err != nil {
		t.Fatalf("EvaluateGoalAcceptance: %v", err)
	}
	if accB.Accepted {
		t.Errorf("AC-009: goal-b acceptance must not remain accepted when prerequisite is reopened")
	}
	if accB.EvidenceStatus != EvidenceStatusStale {
		t.Errorf("AC-009: expected EvidenceStatusStale for goal-b, got: %s", accB.EvidenceStatus)
	}

	// Restore goal-a
	_ = ResolveReopen(tmp, "goal-a", "commit-fix-123")
	updatePredecessorDecision(t, tmp, "goal-a", "proceed")

	// Verify explicit revalidation permits acceptance
	accB2, err := EvaluateGoalAcceptance(tmp, "goal-b")
	if err != nil {
		t.Fatalf("EvaluateGoalAcceptance after restore: %v", err)
	}
	// After restoring goal-a, dependency condition is satisfied
	foundDepDrift := false
	for _, d := range accB2.Diagnostics {
		if d.Code == "DEPENDENCY_DRIFT" {
			foundDepDrift = true
		}
	}
	if foundDepDrift {
		t.Errorf("AC-009: expected no DEPENDENCY_DRIFT after restoring prerequisite")
	}
}

// TestActiveDependencyDrift exercises AC-008:
// Active B retains ownership but packet validation and acceptance reject dependency drift.
func TestActiveDependencyDrift(t *testing.T) {
	tmp := t.TempDir()

	// 1. Setup predecessor goal-a with decision artifact
	setupAcceptedPredecessor(t, tmp, "goal-a", "proceed")

	// 2. Setup successor goal-b requiring decision="proceed"
	setupTestSuccessorGoal(t, tmp, "goal-b", "goal-a")

	// 3. Active claim on goal-b
	claim, err := ClaimGoal(tmp, "goal-b", "agent-worker", "in progress", false)
	if err != nil {
		t.Fatalf("ClaimGoal: %v", err)
	}
	if claim == nil || claim.Agent != "agent-worker" {
		t.Fatalf("expected active claim held by agent-worker")
	}

	// Read initial decision artifact hash
	decPath := filepath.Join(tmp, "goals", "goal-a", "decision.json")
	initialBytes, err := os.ReadFile(decPath)
	if err != nil {
		t.Fatalf("read decision.json: %v", err)
	}
	h := sha256.Sum256(initialBytes)
	initialHex := hex.EncodeToString(h[:])

	wpB, _ := LoadWorkPackage(tmp, "goal-b")
	specDigB, _ := WorkPackageDigest(wpB)

	// Create TaskPacket for B with dependency output pin to goal-a's decision.json
	packetB := &TaskPacket{
		SchemaVersion: TaskPacketSchemaVersion,
		GoalSlug:      "goal-b",
		TaskID:        "T-001",
		SpecDigest:    specDigB,
		DependencyOutputs: []DependencyOutput{
			{
				GoalSlug: "goal-a",
				TaskID:   "T-001",
				Artifact: filepath.Join("goals", "goal-a", "decision.json"),
				SHA256:   initialHex,
			},
		},
	}

	// Validate packet before drift: must have zero errors
	initialDiags := ValidateTaskPacket(tmp, packetB)
	if len(initialDiags) != 0 {
		t.Fatalf("expected 0 packet diagnostics before drift, got: %+v", initialDiags)
	}

	// 4. Cause dependency drift: modify goal-a's decision artifact to "revise"
	tamperedObj := map[string]any{
		"decision":    "revise",
		"reviewed_by": "@architect",
	}
	tamperedBytes, _ := json.MarshalIndent(tamperedObj, "", "  ")
	_ = os.WriteFile(decPath, tamperedBytes, 0644)

	// AC-008: Verify active B retains ownership
	claimPath := filepath.Join(tmp, "goals", "goal-b", "claim.json")
	if !fileExists(claimPath) {
		t.Errorf("AC-008: active B must retain claim file on disk")
	}
	currentClaim, _ := readClaim(claimPath)
	if currentClaim == nil || currentClaim.Agent != "agent-worker" {
		t.Errorf("AC-008: claim must still be held by @agent-worker, got: %+v", currentClaim)
	}

	// AC-008: Packet validation must reject dependency drift
	packetDiags := ValidateTaskPacket(tmp, packetB)
	foundPacketDrift := false
	for _, d := range packetDiags {
		if d.Code == DiagPacketDependencyStale {
			foundPacketDrift = true
			break
		}
	}
	if !foundPacketDrift {
		t.Errorf("AC-008: ValidateTaskPacket did not report PACKET_DEPENDENCY_STALE after artifact modified: %+v", packetDiags)
	}

	// AC-008: Acceptance evaluation must reject dependency drift
	accB, err := EvaluateGoalAcceptance(tmp, "goal-b")
	if err != nil {
		t.Fatalf("EvaluateGoalAcceptance: %v", err)
	}
	if accB.Accepted {
		t.Errorf("AC-008: EvaluateGoalAcceptance must reject acceptance when dependency drifted")
	}
	if accB.EvidenceStatus != EvidenceStatusStale {
		t.Errorf("AC-008: expected EvidenceStatusStale for drifted goal-b, got: %s", accB.EvidenceStatus)
	}
}
