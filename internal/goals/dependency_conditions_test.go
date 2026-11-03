package goals

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func setupTestPredecessorGoal(t *testing.T, dir, slug string) string {
	t.Helper()
	goalDir := filepath.Join(dir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatalf("mkdir goalDir: %v", err)
	}

	goalMd := `# Goal: ` + slug + `
## Metadata
- **Shape**: ship
- **Tier**: roadmap
- **Status**: pending
`
	if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalMd), 0644); err != nil {
		t.Fatalf("write goal.md: %v", err)
	}

	wp := &WorkPackage{
		SchemaVersion: WorkPackageSchemaVersion,
		GoalSlug:      slug,
		Revision:      1,
		Purpose:       "predecessor assessment goal",
		Tasks: []TaskContract{
			{
				ID:            "T-001",
				Title:         "Produce assessment decision",
				Context:       "Assessment goal execution context",
				Why:           "Produce decision.json for downstream goals",
				Files:         TaskFiles{Create: []string{"decision.json"}},
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
				Requirement:   "Assessment must produce decision.json",
				Given:         "Assessment task is executed",
				Action:        "Inspect decision.json output",
				Expected:      "decision.json is written with proceed or revise",
				RequiredScope: "unit",
				Mandatory:     true,
				CheckIDs:      []string{"C-001"},
			},
		},
	}
	if err := SaveWorkPackage(dir, slug, wp); err != nil {
		t.Fatalf("SaveWorkPackage: %v", err)
	}

	return goalDir
}

func setupAcceptedPredecessor(t *testing.T, dir, slug, decisionVal string) (string, string) {
	t.Helper()
	goalDir := setupTestPredecessorGoal(t, dir, slug)

	// Create decision.json
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

	// Create accepted evidence receipt recording decision.json
	evDir := filepath.Join(goalDir, "evidence", "T-001")
	if err := os.MkdirAll(evDir, 0755); err != nil {
		t.Fatalf("mkdir evDir: %v", err)
	}
	exit0 := 0
	ev := &TaskEvidence{
		SchemaVersion: TaskEvidenceSchemaVersion,
		AttemptID:     "att-1",
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

	// Create task acceptance
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

	// Create goal acceptance
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

	return decPath, decHex
}

// TestDependencyConditions exercises AC-001 and AC-003:
// AC-001: Accepted assessment decision=proceed unlocks; accepted assessment decision=revise or stop remains blocked.
// AC-003: Missing goal/task, pointer, wrong JSON type, traversal, and a dependency cycle reject deterministically.
func TestDependencyConditions(t *testing.T) {
	tmp := t.TempDir()

	// 1. AC-001: Decision equals "proceed" unlocks cleanly
	setupAcceptedPredecessor(t, tmp, "assessment-proceed", "proceed")
	proceedCond := []DependencyCondition{
		{
			Kind:     DepConditionDecisionEquals,
			GoalSlug: "assessment-proceed",
			Artifact: "decision.json",
			Pointer:  "/decision",
			Equals:   "proceed",
		},
		{
			Kind:     DepConditionAcceptedGoal,
			GoalSlug: "assessment-proceed",
		},
		{
			Kind:     DepConditionAcceptedTask,
			GoalSlug: "assessment-proceed",
			TaskID:   "T-001",
		},
	}
	diagsProceed := EvaluateDependencyConditions(tmp, proceedCond)
	if len(diagsProceed) != 0 {
		t.Fatalf("expected zero diagnostics for decision=proceed, got: %+v", diagsProceed)
	}

	// 2. AC-001: Decision equals "revise" or "stop" blocks with DEPENDENCY_DECISION_MISMATCH
	setupAcceptedPredecessor(t, tmp, "assessment-revise", "revise")
	reviseCond := []DependencyCondition{
		{
			Kind:     DepConditionDecisionEquals,
			GoalSlug: "assessment-revise",
			Artifact: "decision.json",
			Pointer:  "/decision",
			Equals:   "proceed",
		},
	}
	diagsRevise := EvaluateDependencyConditions(tmp, reviseCond)
	if len(diagsRevise) == 0 {
		t.Fatalf("expected diagnostics for decision=revise with expected=proceed, got 0")
	}
	foundMismatch := false
	for _, d := range diagsRevise {
		if d.Code == DiagDependencyDecisionMismatch {
			foundMismatch = true
			break
		}
	}
	if !foundMismatch {
		t.Fatalf("expected DEPENDENCY_DECISION_MISMATCH for revise decision, got: %+v", diagsRevise)
	}

	// 3. AC-003: Missing goal rejects deterministically
	missingGoalCond := []DependencyCondition{
		{
			Kind:     DepConditionAcceptedGoal,
			GoalSlug: "nonexistent-goal",
		},
	}
	diagsMissing := EvaluateDependencyConditions(tmp, missingGoalCond)
	if len(diagsMissing) == 0 || diagsMissing[0].Code != DiagDependencyGoalNotFound {
		t.Fatalf("expected DEPENDENCY_GOAL_NOT_FOUND, got: %+v", diagsMissing)
	}

	// 4. AC-003: Path traversal rejects deterministically
	traversalCond := []DependencyCondition{
		{
			Kind:     DepConditionDecisionEquals,
			GoalSlug: "../outside",
			Artifact: "decision.json",
			Pointer:  "/decision",
			Equals:   "proceed",
		},
		{
			Kind:     DepConditionDecisionEquals,
			GoalSlug: "assessment-proceed",
			Artifact: "../outside.json",
			Pointer:  "/decision",
			Equals:   "proceed",
		},
	}
	diagsTraversal := EvaluateDependencyConditions(tmp, traversalCond)
	if len(diagsTraversal) < 2 {
		t.Fatalf("expected 2 traversal diagnostics, got: %+v", diagsTraversal)
	}
	for _, d := range diagsTraversal {
		if d.Code != DiagDependencyPathTraversal {
			t.Errorf("expected DEPENDENCY_PATH_TRAVERSAL, got %s", d.Code)
		}
	}

	// 5. AC-003: Missing pointer in artifact rejects
	badPointerCond := []DependencyCondition{
		{
			Kind:     DepConditionDecisionEquals,
			GoalSlug: "assessment-proceed",
			Artifact: "decision.json",
			Pointer:  "/nonexistent_field",
			Equals:   "proceed",
		},
	}
	diagsBadPointer := EvaluateDependencyConditions(tmp, badPointerCond)
	if len(diagsBadPointer) == 0 || diagsBadPointer[0].Code != DiagDependencyPointerNotFound {
		t.Fatalf("expected DEPENDENCY_POINTER_NOT_FOUND, got: %+v", diagsBadPointer)
	}

	// 6. AC-003: Wrong JSON type (pointer points to object, not scalar)
	nonScalarCond := []DependencyCondition{
		{
			Kind:     DepConditionDecisionEquals,
			GoalSlug: "assessment-proceed",
			Artifact: "decision.json",
			Pointer:  "", // points to root object
			Equals:   "proceed",
		},
	}
	diagsNonScalar := EvaluateDependencyConditions(tmp, nonScalarCond)
	if len(diagsNonScalar) == 0 || diagsNonScalar[0].Code != DiagDependencyValueTypeMismatch {
		t.Fatalf("expected DEPENDENCY_VALUE_TYPE_MISMATCH, got: %+v", diagsNonScalar)
	}

	// 7. AC-003: Dependency cycle rejects deterministically
	cycleGoalDir := filepath.Join(tmp, "goals", "cycle-a")
	_ = os.MkdirAll(cycleGoalDir, 0755)
	_ = os.WriteFile(filepath.Join(cycleGoalDir, "goal.md"), []byte("# Goal: cycle-a\n- **Depends On**: cycle-b\n"), 0644)
	cycleGoalDirB := filepath.Join(tmp, "goals", "cycle-b")
	_ = os.MkdirAll(cycleGoalDirB, 0755)
	_ = os.WriteFile(filepath.Join(cycleGoalDirB, "goal.md"), []byte("# Goal: cycle-b\n- **Depends On**: cycle-a\n"), 0644)

	cycleCond := []DependencyCondition{
		{
			Kind:     DepConditionAcceptedGoal,
			GoalSlug: "cycle-a",
		},
	}
	diagsCycle := EvaluateDependencyConditions(tmp, cycleCond)
	foundCycle := false
	for _, d := range diagsCycle {
		if d.Code == DiagDependencyCycle {
			foundCycle = true
			break
		}
	}
	if !foundCycle {
		t.Fatalf("expected DEPENDENCY_CYCLE diagnostic for cycle-a, got: %+v", diagsCycle)
	}
}

// TestDependencyDecisionIntegrity exercises AC-002:
// Editing decision.json after acceptance blocks on artifact digest before value comparison.
func TestDependencyDecisionIntegrity(t *testing.T) {
	tmp := t.TempDir()

	decPath, originalSHA := setupAcceptedPredecessor(t, tmp, "assessment-tamper", "proceed")

	cond := []DependencyCondition{
		{
			Kind:     DepConditionDecisionEquals,
			GoalSlug: "assessment-tamper",
			Artifact: "decision.json",
			SHA256:   originalSHA,
			Pointer:  "/decision",
			Equals:   "proceed",
		},
	}

	// Initially valid
	diagsInitial := EvaluateDependencyConditions(tmp, cond)
	if len(diagsInitial) != 0 {
		t.Fatalf("expected clean initial evaluation, got: %+v", diagsInitial)
	}

	// Tamper with decision.json by adding whitespace or editing content
	tamperedContent := `{"decision": "proceed", "reviewed_by": "@architect", "tampered": true}`
	if err := os.WriteFile(decPath, []byte(tamperedContent), 0644); err != nil {
		t.Fatalf("write tampered decision.json: %v", err)
	}

	// AC-002: Blocks on artifact digest mismatch BEFORE checking value
	diagsTampered := EvaluateDependencyConditions(tmp, cond)
	if len(diagsTampered) == 0 {
		t.Fatalf("expected diagnostics after tampering with decision.json, got 0")
	}
	foundDigestMismatch := false
	for _, d := range diagsTampered {
		if d.Code == DiagDependencyArtifactMismatch {
			foundDigestMismatch = true
			break
		}
	}
	if !foundDigestMismatch {
		t.Fatalf("expected DEPENDENCY_ARTIFACT_DIGEST_MISMATCH before value comparison, got: %+v", diagsTampered)
	}
}
