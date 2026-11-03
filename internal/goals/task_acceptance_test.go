package goals

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func setupAcceptanceGoalWithWorkPackage(t *testing.T, slug, taskID string, numCases int, mandatory []bool) (string, *WorkPackage, string) {
	t.Helper()
	dir := t.TempDir()
	goalDir := filepath.Join(dir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatalf("mkdir goalDir: %v", err)
	}

	var cases []AcceptanceCase
	var caseIDs []string
	var checks []Check
	for i := 1; i <= numCases; i++ {
		cid := fmt.Sprintf("AC-%03d", i)
		chkID := fmt.Sprintf("C-%03d", i)
		caseIDs = append(caseIDs, cid)
		isMandatory := true
		if i-1 < len(mandatory) {
			isMandatory = mandatory[i-1]
		}
		cases = append(cases, AcceptanceCase{
			ID:            cid,
			Requirement:   fmt.Sprintf("Requirement %d", i),
			Given:         "system ready",
			Action:        "run check",
			Expected:      "success",
			RequiredScope: ScopeUnit,
			Mandatory:     isMandatory,
			CheckIDs:      []string{chkID},
		})
		checks = append(checks, Check{
			ID:               chkID,
			Executable:       "go",
			Args:             []string{"test"},
			Cwd:              "cli",
			TimeoutSeconds:   30,
			ExpectedExitCode: 0,
			Scope:            ScopeUnit,
			Capability:       CapabilityLocal,
		})
	}
	if numCases == 0 {
		checks = append(checks, Check{
			ID:               "C-001",
			Executable:       "go",
			Args:             []string{"test"},
			Cwd:              "cli",
			TimeoutSeconds:   30,
			ExpectedExitCode: 0,
			Scope:            ScopeUnit,
			Capability:       CapabilityLocal,
		})
	}

	wp := &WorkPackage{
		SchemaVersion: WorkPackageSchemaVersion,
		GoalSlug:      slug,
		Revision:      1,
		Purpose:       "Testing acceptance gates",
		Constraints:   []string{"pure-go"},
		NonGoals:      []string{"external-network"},
		Tasks: []TaskContract{
			{
				ID:             taskID,
				Title:          "Task Title",
				Context:        "Task Context",
				Why:            "Task Why",
				DependsOn:      []string{},
				Files:          TaskFiles{Read: []string{"read.go"}, Edit: []string{"edit.go"}},
				AcceptanceIDs:  caseIDs,
				Checks:         checks,
				StopConditions: []string{"failure"},
				ReturnFormat:   "sila/task-result/v1",
			},
		},
		Acceptance:           cases,
		DependencyConditions: []DependencyCondition{},
		UnresolvedDecisions:  []string{},
	}

	if err := SaveWorkPackage(dir, slug, wp); err != nil {
		t.Fatalf("save work package: %v", err)
	}

	specDigest, err := WorkPackageDigest(wp)
	if err != nil {
		t.Fatalf("compute spec digest: %v", err)
	}

	return dir, wp, specDigest
}

func createReceipt(t *testing.T, dir, slug, taskID, attemptID, caseID string, outcome EvidenceOutcome, endedAt time.Time, candidate TaskCandidate, artifacts []EvidenceArtifact, specDigest string) string {
	t.Helper()
	ev := &TaskEvidence{
		SchemaVersion: TaskEvidenceSchemaVersion,
		AttemptID:     attemptID,
		GoalSlug:      slug,
		TaskID:        taskID,
		CaseID:        caseID,
		CheckID:       "C-001",
		SpecDigest:    specDigest,
		PacketDigest:  "pktdigest123",
		Candidate:     candidate,
		Scope:         ScopeUnit,
		Outcome:       outcome,
		Origin:        OriginRunner,
		Executable:    "go",
		Args:          []string{"test"},
		Cwd:           "cli",
		ExitCode:      new(int),
		Artifacts:     artifacts,
		StartedAt:     endedAt.Add(-time.Second),
		EndedAt:       endedAt,
	}
	if outcome == OutcomePass {
		*ev.ExitCode = 0
		ev.Assertions = []AssertionObservation{
			{Kind: AssertionGoTestPass, Target: "cli/test", Expected: true, Observed: true, Passed: true},
		}
	} else {
		*ev.ExitCode = 1
		ev.Assertions = []AssertionObservation{
			{Kind: AssertionGoTestPass, Target: "cli/test", Expected: true, Observed: false, Passed: false},
		}
	}

	if err := SaveTaskEvidence(dir, slug, ev); err != nil {
		t.Fatalf("save task evidence: %v", err)
	}

	return filepath.Join("goals", slug, "evidence", taskID, attemptID, "receipt.json")
}

func TestTaskAcceptanceCompleteness(t *testing.T) {
	// AC-004: Nine passes and one mandatory fail yield accepted=false; zero cases never pass.

	// Case 1: Zero cases never pass
	t.Run("zero cases never pass", func(t *testing.T) {
		dir, _, specDigest := setupAcceptanceGoalWithWorkPackage(t, "zero-cases-goal", "T-001", 0, nil)
		selection := ReceiptSelection{
			SchemaVersion:   ReceiptSelectionSchemaVersion,
			GoalSlug:        "zero-cases-goal",
			TaskID:          "T-001",
			SpecDigest:      specDigest,
			CandidateDigest: "canddigest1",
			ReceiptPaths:    []string{},
		}
		review := AcceptanceReview{
			SchemaVersion:   AcceptanceReviewSchemaVersion,
			Reviewer:        "reviewer",
			Producer:        "producer",
			CandidateDigest: "canddigest1",
			SpecDigest:      specDigest,
			Disposition:     "accept",
			ReviewedAt:      time.Now().UTC().Format(time.RFC3339),
		}

		res, err := EvaluateTaskAcceptance(dir, "zero-cases-goal", "T-001", selection, review)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Accepted {
			t.Fatalf("expected accepted=false for zero cases, got true")
		}
		if res.EvidenceStatus != EvidenceStatusIncomplete {
			t.Errorf("expected evidence_status=incomplete, got %s", res.EvidenceStatus)
		}
	})

	// Case 2: Nine passes and one mandatory fail yield accepted=false
	t.Run("nine passes and one mandatory fail", func(t *testing.T) {
		dir, _, specDigest := setupAcceptanceGoalWithWorkPackage(t, "ten-cases-goal", "T-001", 10, nil)
		candidate := TaskCandidate{Commit: "commit1", ContentDigest: "canddigest1"}
		now := time.Now().UTC()

		var receiptPaths []string
		for i := 1; i <= 10; i++ {
			cid := fmt.Sprintf("AC-%03d", i)
			attID := fmt.Sprintf("att-%03d", i)
			outcome := OutcomePass
			if i == 10 {
				outcome = OutcomeFail
			}
			rPath := createReceipt(t, dir, "ten-cases-goal", "T-001", attID, cid, outcome, now.Add(time.Duration(i)*time.Second), candidate, nil, specDigest)
			receiptPaths = append(receiptPaths, rPath)
		}

		selection := ReceiptSelection{
			SchemaVersion:   ReceiptSelectionSchemaVersion,
			GoalSlug:        "ten-cases-goal",
			TaskID:          "T-001",
			SpecDigest:      specDigest,
			CandidateDigest: "canddigest1",
			ReceiptPaths:    receiptPaths,
		}
		review := AcceptanceReview{
			SchemaVersion:   AcceptanceReviewSchemaVersion,
			Reviewer:        "reviewer",
			Producer:        "producer",
			CandidateDigest: "canddigest1",
			SpecDigest:      specDigest,
			Disposition:     "accept",
			ReviewedAt:      now.Add(time.Minute).Format(time.RFC3339),
		}

		res, err := EvaluateTaskAcceptance(dir, "ten-cases-goal", "T-001", selection, review)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Accepted {
			t.Fatalf("expected accepted=false when one mandatory case fails, got true")
		}
		if res.EvidenceStatus != EvidenceStatusFailed {
			t.Errorf("expected evidence_status=failed, got %s", res.EvidenceStatus)
		}
		if len(res.FailedCaseIDs) != 1 || res.FailedCaseIDs[0] != "AC-010" {
			t.Errorf("expected failed_case_ids=[AC-010], got %v", res.FailedCaseIDs)
		}
	})

	// Case 3: Missing mandatory case yields accepted=false, incomplete
	t.Run("missing mandatory case", func(t *testing.T) {
		dir, _, specDigest := setupAcceptanceGoalWithWorkPackage(t, "missing-case-goal", "T-001", 3, nil)
		candidate := TaskCandidate{Commit: "commit1", ContentDigest: "canddigest1"}
		now := time.Now().UTC()

		// Provide receipts only for AC-001 and AC-002; omit AC-003
		r1 := createReceipt(t, dir, "missing-case-goal", "T-001", "att-001", "AC-001", OutcomePass, now, candidate, nil, specDigest)
		r2 := createReceipt(t, dir, "missing-case-goal", "T-001", "att-002", "AC-002", OutcomePass, now, candidate, nil, specDigest)

		selection := ReceiptSelection{
			SchemaVersion:   ReceiptSelectionSchemaVersion,
			GoalSlug:        "missing-case-goal",
			TaskID:          "T-001",
			SpecDigest:      specDigest,
			CandidateDigest: "canddigest1",
			ReceiptPaths:    []string{r1, r2},
		}
		review := AcceptanceReview{
			SchemaVersion:   AcceptanceReviewSchemaVersion,
			Reviewer:        "reviewer",
			Producer:        "producer",
			CandidateDigest: "canddigest1",
			SpecDigest:      specDigest,
			Disposition:     "accept",
			ReviewedAt:      now.Add(time.Minute).Format(time.RFC3339),
		}

		res, err := EvaluateTaskAcceptance(dir, "missing-case-goal", "T-001", selection, review)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Accepted {
			t.Fatalf("expected accepted=false when mandatory case is missing, got true")
		}
		if res.EvidenceStatus != EvidenceStatusIncomplete {
			t.Errorf("expected evidence_status=incomplete, got %s", res.EvidenceStatus)
		}
		if len(res.MissingCaseIDs) != 1 || res.MissingCaseIDs[0] != "AC-003" {
			t.Errorf("expected missing_case_ids=[AC-003], got %v", res.MissingCaseIDs)
		}
	})
}

func TestTaskAcceptanceCandidate(t *testing.T) {
	// AC-005: Mixed candidate or spec digests, stale review, forged artifact hash, and undisposed newer failure reject.

	t.Run("mixed candidate digest between selection and review", func(t *testing.T) {
		dir, _, specDigest := setupAcceptanceGoalWithWorkPackage(t, "cand-mismatch-goal", "T-001", 1, nil)
		candidate := TaskCandidate{Commit: "c1", ContentDigest: "digest-A"}
		now := time.Now().UTC()
		r1 := createReceipt(t, dir, "cand-mismatch-goal", "T-001", "att-1", "AC-001", OutcomePass, now, candidate, nil, specDigest)

		selection := ReceiptSelection{
			SchemaVersion:   ReceiptSelectionSchemaVersion,
			GoalSlug:        "cand-mismatch-goal",
			TaskID:          "T-001",
			SpecDigest:      specDigest,
			CandidateDigest: "digest-A",
			ReceiptPaths:    []string{r1},
		}
		review := AcceptanceReview{
			SchemaVersion:   AcceptanceReviewSchemaVersion,
			Reviewer:        "reviewer",
			Producer:        "producer",
			CandidateDigest: "digest-B", // Mismatched candidate digest!
			SpecDigest:      specDigest,
			Disposition:     "accept",
			ReviewedAt:      now.Add(time.Minute).Format(time.RFC3339),
		}

		res, err := EvaluateTaskAcceptance(dir, "cand-mismatch-goal", "T-001", selection, review)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Accepted {
			t.Fatalf("expected accepted=false on candidate digest mismatch")
		}
	})

	t.Run("spec digest mismatch", func(t *testing.T) {
		dir, _, specDigest := setupAcceptanceGoalWithWorkPackage(t, "spec-mismatch-goal", "T-001", 1, nil)
		candidate := TaskCandidate{Commit: "c1", ContentDigest: "digest-A"}
		now := time.Now().UTC()
		r1 := createReceipt(t, dir, "spec-mismatch-goal", "T-001", "att-1", "AC-001", OutcomePass, now, candidate, nil, specDigest)

		selection := ReceiptSelection{
			SchemaVersion:   ReceiptSelectionSchemaVersion,
			GoalSlug:        "spec-mismatch-goal",
			TaskID:          "T-001",
			SpecDigest:      "wrong-spec-digest",
			CandidateDigest: "digest-A",
			ReceiptPaths:    []string{r1},
		}
		review := AcceptanceReview{
			SchemaVersion:   AcceptanceReviewSchemaVersion,
			Reviewer:        "reviewer",
			Producer:        "producer",
			CandidateDigest: "digest-A",
			SpecDigest:      specDigest,
			Disposition:     "accept",
			ReviewedAt:      now.Add(time.Minute).Format(time.RFC3339),
		}

		res, err := EvaluateTaskAcceptance(dir, "spec-mismatch-goal", "T-001", selection, review)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Accepted {
			t.Fatalf("expected accepted=false on spec digest mismatch")
		}
	})

	t.Run("stale review rejected", func(t *testing.T) {
		dir, _, specDigest := setupAcceptanceGoalWithWorkPackage(t, "stale-review-goal", "T-001", 1, nil)
		candidate := TaskCandidate{Commit: "c1", ContentDigest: "digest-A"}
		now := time.Now().UTC()
		// Evidence ended at now
		r1 := createReceipt(t, dir, "stale-review-goal", "T-001", "att-1", "AC-001", OutcomePass, now, candidate, nil, specDigest)

		selection := ReceiptSelection{
			SchemaVersion:   ReceiptSelectionSchemaVersion,
			GoalSlug:        "stale-review-goal",
			TaskID:          "T-001",
			SpecDigest:      specDigest,
			CandidateDigest: "digest-A",
			ReceiptPaths:    []string{r1},
		}
		review := AcceptanceReview{
			SchemaVersion:   AcceptanceReviewSchemaVersion,
			Reviewer:        "reviewer",
			Producer:        "producer",
			CandidateDigest: "digest-A",
			SpecDigest:      specDigest,
			Disposition:     "accept",
			ReviewedAt:      now.Add(-time.Hour).Format(time.RFC3339), // Review is 1 hour before evidence!
		}

		res, err := EvaluateTaskAcceptance(dir, "stale-review-goal", "T-001", selection, review)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Accepted {
			t.Fatalf("expected accepted=false on stale review")
		}
	})

	t.Run("forged artifact hash rejected", func(t *testing.T) {
		dir, _, specDigest := setupAcceptanceGoalWithWorkPackage(t, "forged-art-goal", "T-001", 1, nil)
		candidate := TaskCandidate{Commit: "c1", ContentDigest: "digest-A"}
		now := time.Now().UTC()

		// Write an actual artifact file
		artDir := filepath.Join(dir, "goals", "forged-art-goal", "evidence", "artifacts")
		_ = os.MkdirAll(artDir, 0755)
		artPath := filepath.Join(artDir, "output.txt")
		_ = os.WriteFile(artPath, []byte("real content"), 0644)

		// Declare forged artifact with wrong hash
		fakeHash := hex.EncodeToString(sha256.New().Sum([]byte("wrong")))
		artifacts := []EvidenceArtifact{
			{Path: "artifacts/output.txt", SHA256: fakeHash},
		}

		r1 := createReceipt(t, dir, "forged-art-goal", "T-001", "att-1", "AC-001", OutcomePass, now, candidate, artifacts, specDigest)

		selection := ReceiptSelection{
			SchemaVersion:   ReceiptSelectionSchemaVersion,
			GoalSlug:        "forged-art-goal",
			TaskID:          "T-001",
			SpecDigest:      specDigest,
			CandidateDigest: "digest-A",
			ReceiptPaths:    []string{r1},
		}
		review := AcceptanceReview{
			SchemaVersion:   AcceptanceReviewSchemaVersion,
			Reviewer:        "reviewer",
			Producer:        "producer",
			CandidateDigest: "digest-A",
			SpecDigest:      specDigest,
			Disposition:     "accept",
			ReviewedAt:      now.Add(time.Minute).Format(time.RFC3339),
		}

		res, err := EvaluateTaskAcceptance(dir, "forged-art-goal", "T-001", selection, review)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Accepted {
			t.Fatalf("expected accepted=false on forged artifact hash")
		}
	})

	t.Run("undisposed newer failure rejected", func(t *testing.T) {
		dir, _, specDigest := setupAcceptanceGoalWithWorkPackage(t, "undisposed-fail-goal", "T-001", 1, nil)
		candidate := TaskCandidate{Commit: "c1", ContentDigest: "digest-A"}
		t1 := time.Now().UTC().Add(-time.Hour)
		t2 := time.Now().UTC() // Newer time

		// Selected older pass
		rOldPass := createReceipt(t, dir, "undisposed-fail-goal", "T-001", "att-old", "AC-001", OutcomePass, t1, candidate, nil, specDigest)

		// On-disk newer failure!
		_ = createReceipt(t, dir, "undisposed-fail-goal", "T-001", "att-new", "AC-001", OutcomeFail, t2, candidate, nil, specDigest)

		selection := ReceiptSelection{
			SchemaVersion:            ReceiptSelectionSchemaVersion,
			GoalSlug:                 "undisposed-fail-goal",
			TaskID:                   "T-001",
			SpecDigest:               specDigest,
			CandidateDigest:          "digest-A",
			ReceiptPaths:             []string{rOldPass},
			NewerFailureDispositions: []NewerFailureDisposition{}, // None provided!
		}
		review := AcceptanceReview{
			SchemaVersion:   AcceptanceReviewSchemaVersion,
			Reviewer:        "reviewer",
			Producer:        "producer",
			CandidateDigest: "digest-A",
			SpecDigest:      specDigest,
			Disposition:     "accept",
			ReviewedAt:      t2.Add(time.Minute).Format(time.RFC3339),
		}

		res, err := EvaluateTaskAcceptance(dir, "undisposed-fail-goal", "T-001", selection, review)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Accepted {
			t.Fatalf("expected accepted=false on undisposed newer failure")
		}
	})
}

func TestTaskAcceptanceReview(t *testing.T) {
	// AC-006: All current required cases pass with valid review yields accepted=true and exact selected receipt IDs

	t.Run("self-review prohibited", func(t *testing.T) {
		dir, _, specDigest := setupAcceptanceGoalWithWorkPackage(t, "self-review-goal", "T-001", 1, nil)
		candidate := TaskCandidate{Commit: "c1", ContentDigest: "digest-A"}
		now := time.Now().UTC()
		r1 := createReceipt(t, dir, "self-review-goal", "T-001", "att-1", "AC-001", OutcomePass, now, candidate, nil, specDigest)

		selection := ReceiptSelection{
			SchemaVersion:   ReceiptSelectionSchemaVersion,
			GoalSlug:        "self-review-goal",
			TaskID:          "T-001",
			SpecDigest:      specDigest,
			CandidateDigest: "digest-A",
			ReceiptPaths:    []string{r1},
		}
		review := AcceptanceReview{
			SchemaVersion:   AcceptanceReviewSchemaVersion,
			Reviewer:        "@alice",
			Producer:        "@alice", // Self review!
			CandidateDigest: "digest-A",
			SpecDigest:      specDigest,
			Disposition:     "accept",
			ReviewedAt:      now.Add(time.Minute).Format(time.RFC3339),
		}

		res, err := EvaluateTaskAcceptance(dir, "self-review-goal", "T-001", selection, review)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Accepted {
			t.Fatalf("expected accepted=false on self-review")
		}
	})

	t.Run("complete pass with valid review yields accepted=true", func(t *testing.T) {
		dir, _, specDigest := setupAcceptanceGoalWithWorkPackage(t, "success-goal", "T-001", 2, nil)
		candidate := TaskCandidate{Commit: "c1", ContentDigest: "digest-A"}
		now := time.Now().UTC()

		r1 := createReceipt(t, dir, "success-goal", "T-001", "att-1", "AC-001", OutcomePass, now, candidate, nil, specDigest)
		r2 := createReceipt(t, dir, "success-goal", "T-001", "att-2", "AC-002", OutcomePass, now, candidate, nil, specDigest)

		selection := ReceiptSelection{
			SchemaVersion:   ReceiptSelectionSchemaVersion,
			GoalSlug:        "success-goal",
			TaskID:          "T-001",
			SpecDigest:      specDigest,
			CandidateDigest: "digest-A",
			ReceiptPaths:    []string{r1, r2},
		}
		review := AcceptanceReview{
			SchemaVersion:   AcceptanceReviewSchemaVersion,
			Reviewer:        "reviewer-bob",
			Producer:        "producer-alice",
			CandidateDigest: "digest-A",
			SpecDigest:      specDigest,
			Disposition:     "accept",
			CaseDispositions: []CaseDisposition{
				{CaseID: "AC-001", Accepted: true, Reason: "verified"},
				{CaseID: "AC-002", Accepted: true, Reason: "verified"},
			},
			ReviewedAt: now.Add(time.Minute).Format(time.RFC3339),
		}

		res, err := EvaluateTaskAcceptance(dir, "success-goal", "T-001", selection, review)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.Accepted {
			t.Fatalf("expected accepted=true, got false (diagnostics: %+v)", res.Diagnostics)
		}
		if res.EvidenceStatus != EvidenceStatusAccepted {
			t.Errorf("expected evidence_status=accepted, got %s", res.EvidenceStatus)
		}
		if len(res.ReceiptPaths) != 2 || res.ReceiptPaths[0] != r1 || res.ReceiptPaths[1] != r2 {
			t.Errorf("expected exact receipt paths %v, got %v", []string{r1, r2}, res.ReceiptPaths)
		}
	})

	t.Run("disposed newer failure permits older pass", func(t *testing.T) {
		dir, _, specDigest := setupAcceptanceGoalWithWorkPackage(t, "disposed-fail-goal", "T-001", 1, nil)
		candidate := TaskCandidate{Commit: "c1", ContentDigest: "digest-A"}
		t1 := time.Now().UTC().Add(-time.Hour)
		t2 := time.Now().UTC()

		rOldPass := createReceipt(t, dir, "disposed-fail-goal", "T-001", "att-old", "AC-001", OutcomePass, t1, candidate, nil, specDigest)
		rNewFail := createReceipt(t, dir, "disposed-fail-goal", "T-001", "att-new", "AC-001", OutcomeFail, t2, candidate, nil, specDigest)

		selection := ReceiptSelection{
			SchemaVersion:   ReceiptSelectionSchemaVersion,
			GoalSlug:        "disposed-fail-goal",
			TaskID:          "T-001",
			SpecDigest:      specDigest,
			CandidateDigest: "digest-A",
			ReceiptPaths:    []string{rOldPass},
			NewerFailureDispositions: []NewerFailureDisposition{
				{
					ReceiptPath: rNewFail,
					Reason:      "Newer attempt was run with faulty test environment; confirmed older attempt is authoritative",
					Reviewer:    "reviewer-bob",
				},
			},
		}
		review := AcceptanceReview{
			SchemaVersion:   AcceptanceReviewSchemaVersion,
			Reviewer:        "reviewer-bob",
			Producer:        "producer-alice",
			CandidateDigest: "digest-A",
			SpecDigest:      specDigest,
			Disposition:     "accept",
			ReviewedAt:      t2.Add(time.Minute).Format(time.RFC3339),
		}

		res, err := EvaluateTaskAcceptance(dir, "disposed-fail-goal", "T-001", selection, review)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !res.Accepted {
			t.Fatalf("expected accepted=true with disposed newer failure, got false: %+v", res.Diagnostics)
		}
		if res.EvidenceStatus != EvidenceStatusAccepted {
			t.Errorf("expected evidence_status=accepted, got %s", res.EvidenceStatus)
		}
	})
}
