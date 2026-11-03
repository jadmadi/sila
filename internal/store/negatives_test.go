package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEpistemicHeartbeatsAndNegativeSignals(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sila.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer st.Close()

	proj, err := st.EnsureProject(dir)
	if err != nil {
		t.Fatalf("EnsureProject failed: %v", err)
	}
	projectID := proj.ID

	// 1. Insert an invariant fact
	fact1 := Fact{
		ID:        "f_inv_1",
		ProjectID: projectID,
		Claim:     "Always compile with CGO_ENABLED=0",
		Kind:      "invariant",
		Tier:      string(TierSemantic),
		FirstSeen: time.Now().UTC().Add(-30 * 24 * time.Hour).Format(time.RFC3339),
	}
	if err := st.SaveFact(fact1); err != nil {
		t.Fatalf("SaveFact fact1 failed: %v", err)
	}

	// 2. Insert a second invariant fact
	fact2 := Fact{
		ID:        "f_inv_2",
		ProjectID: projectID,
		Claim:     "Pure-Go SQLite only",
		Kind:      "invariant",
		Tier:      string(TierSemantic),
		FirstSeen: time.Now().UTC().Add(-10 * 24 * time.Hour).Format(time.RFC3339),
	}
	if err := st.SaveFact(fact2); err != nil {
		t.Fatalf("SaveFact fact2 failed: %v", err)
	}

	// Initially, both should be detected as negative signals (never asserted)
	signals, err := st.DetectNegativeSignals(context.Background(), projectID, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("DetectNegativeSignals failed: %v", err)
	}
	if len(signals) != 2 {
		t.Fatalf("expected 2 negative signals, got %d: %+v", len(signals), signals)
	}

	// 3. Record an epistemic heartbeat for fact1
	_ = os.MkdirAll(filepath.Join(dir, "cmd", "sila"), 0755)
	_ = os.WriteFile(filepath.Join(dir, "cmd", "sila", "main.go"), []byte("package main"), 0644)
	_ = os.MkdirAll(filepath.Join(dir, "internal", "store"), 0755)
	_ = os.WriteFile(filepath.Join(dir, "internal", "store", "store.go"), []byte("package store"), 0644)

	hb := EpistemicHeartbeat{
		ProjectID:     projectID,
		SessionID:     "sess_1",
		Agent:         "agent_tester",
		InvariantRule: fact1.Claim,
		EvidenceType:  "file",
		EvidenceRef:   "cmd/sila/main.go",
		Status:        "pass",
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
	}
	if err := st.RecordHeartbeat(hb); err != nil {
		t.Fatalf("RecordHeartbeat failed: %v", err)
	}

	// Verify heartbeat listing
	hbs, err := st.ListRecentHeartbeats(projectID, 10)
	if err != nil {
		t.Fatalf("ListRecentHeartbeats failed: %v", err)
	}
	if len(hbs) != 1 {
		t.Fatalf("expected 1 heartbeat, got %d", len(hbs))
	}
	if hbs[0].InvariantRule != fact1.Claim {
		t.Fatalf("heartbeat rule = %q, want %q", hbs[0].InvariantRule, fact1.Claim)
	}

	// Now detect negative signals again: fact1 should be clean; only fact2 should remain
	signalsAfter, err := st.DetectNegativeSignals(context.Background(), projectID, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("DetectNegativeSignals failed: %v", err)
	}
	if len(signalsAfter) != 1 {
		t.Fatalf("expected 1 negative signal after heartbeat, got %d: %+v", len(signalsAfter), signalsAfter)
	}
	if signalsAfter[0].RuleID != fact2.ID {
		t.Fatalf("remaining signal = %q, want %q", signalsAfter[0].RuleID, fact2.ID)
	}

	// 4. Record an old heartbeat for fact2 (e.g. 15 days ago) to test "silenced" detection
	hbOld := EpistemicHeartbeat{
		ProjectID:     projectID,
		SessionID:     "sess_old",
		Agent:         "agent_tester",
		InvariantRule: fact2.Claim,
		EvidenceType:  "file",
		EvidenceRef:   "internal/store/store.go",
		Status:        "pass",
		CreatedAt:     time.Now().UTC().Add(-15 * 24 * time.Hour).Format(time.RFC3339),
	}
	if err := st.RecordHeartbeat(hbOld); err != nil {
		t.Fatalf("RecordHeartbeat old failed: %v", err)
	}

	signalsOld, err := st.DetectNegativeSignals(context.Background(), projectID, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("DetectNegativeSignals failed: %v", err)
	}
	if len(signalsOld) != 1 {
		t.Fatalf("expected 1 negative signal for silenced fact2, got %d", len(signalsOld))
	}
	if signalsOld[0].Status != "silenced" {
		t.Fatalf("expected status 'silenced', got %q", signalsOld[0].Status)
	}
	if signalsOld[0].SuspicionScore <= 0 {
		t.Fatalf("expected positive suspicion score, got %f", signalsOld[0].SuspicionScore)
	}
}

func TestHeartbeatValidation(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sila.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer st.Close()

	proj, err := st.EnsureProject(dir)
	if err != nil {
		t.Fatalf("EnsureProject failed: %v", err)
	}

	// 1. Missing evidence_ref must fail
	err = st.RecordHeartbeat(EpistemicHeartbeat{
		ProjectID:     proj.ID,
		InvariantRule: "Test rule",
		EvidenceType:  "file",
		EvidenceRef:   "",
		Status:        "pass",
	})
	if err == nil {
		t.Fatal("expected error for empty evidence_ref, got nil")
	}

	// 2. Whitespace-only evidence_ref must fail
	err = st.RecordHeartbeat(EpistemicHeartbeat{
		ProjectID:     proj.ID,
		InvariantRule: "Test rule",
		EvidenceType:  "file",
		EvidenceRef:   "   ",
		Status:        "pass",
	})
	if err == nil {
		t.Fatal("expected error for whitespace-only evidence_ref, got nil")
	}

	// 3. Missing status must fail
	err = st.RecordHeartbeat(EpistemicHeartbeat{
		ProjectID:     proj.ID,
		InvariantRule: "Test rule",
		EvidenceType:  "file",
		EvidenceRef:   "file.go",
		Status:        "",
	})
	if err == nil {
		t.Fatal("expected error for empty status, got nil")
	}

	// 4. Invalid status string must fail
	for _, badStatus := range []string{"pending", "unknown", "passed", "skipped", "error"} {
		err = st.RecordHeartbeat(EpistemicHeartbeat{
			ProjectID:     proj.ID,
			InvariantRule: "Test rule",
			EvidenceType:  "file",
			EvidenceRef:   "file.go",
			Status:        badStatus,
		})
		if err == nil {
			t.Fatalf("expected error for status %q, got nil", badStatus)
		}
	}

	// 5. Invalid evidence_type must fail
	err = st.RecordHeartbeat(EpistemicHeartbeat{
		ProjectID:     proj.ID,
		InvariantRule: "Test rule",
		EvidenceType:  "arbitrary_type",
		EvidenceRef:   "evidence_123",
		Status:        "pass",
	})
	if err == nil {
		t.Fatal("expected error for invalid evidence_type, got nil")
	}

	// 6. Allowed evidence_types with pass and fail must succeed
	allowedTypes := []string{"file", "commit", "tool", "test_run", "gate"}
	for _, et := range allowedTypes {
		for _, stVal := range []string{"pass", "fail"} {
			ref := "evidence_" + et
			if et == "file" {
				ref = filepath.Join(dir, "evidence_file")
				_ = os.WriteFile(ref, []byte("evidence"), 0644)
			}
			err = st.RecordHeartbeat(EpistemicHeartbeat{
				ProjectID:     proj.ID,
				InvariantRule: "Test rule " + et + " " + stVal,
				EvidenceType:  et,
				EvidenceRef:   ref,
				Status:        stVal,
			})
			if err != nil {
				t.Fatalf("expected success for evidence_type=%q status=%q, got: %v", et, stVal, err)
			}
		}
	}

	// 7. Nonexistent file evidence with pass status must fail
	err = st.RecordHeartbeat(EpistemicHeartbeat{
		ProjectID:     proj.ID,
		InvariantRule: "Test rule nonexistent",
		EvidenceType:  "file",
		EvidenceRef:   filepath.Join(dir, "does_not_exist.go"),
		Status:        "pass",
	})
	if err == nil {
		t.Fatal("expected error for nonexistent file evidence, got nil")
	}
}

func TestFailedHeartbeatDoesNotSuppressNegativeSignals(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sila.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer st.Close()

	proj, err := st.EnsureProject(dir)
	if err != nil {
		t.Fatalf("EnsureProject failed: %v", err)
	}

	fact := Fact{
		ID:        "f_audit_1",
		ProjectID: proj.ID,
		Claim:     "No hardcoded credentials or tokens",
		Kind:      "invariant",
		Tier:      string(TierSemantic),
		FirstSeen: time.Now().UTC().Add(-20 * 24 * time.Hour).Format(time.RFC3339),
	}
	if err := st.SaveFact(fact); err != nil {
		t.Fatalf("SaveFact failed: %v", err)
	}

	// Initially detected as negative signal (never asserted)
	signals, err := st.DetectNegativeSignals(context.Background(), proj.ID, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("DetectNegativeSignals failed: %v", err)
	}
	if len(signals) != 1 {
		t.Fatalf("expected 1 negative signal, got %d", len(signals))
	}

	// Also verify QueryNegativeSignals alias returns the same
	querySignals, err := st.QueryNegativeSignals(context.Background(), proj.ID, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("QueryNegativeSignals failed: %v", err)
	}
	if len(querySignals) != 1 {
		t.Fatalf("expected 1 negative signal via QueryNegativeSignals, got %d", len(querySignals))
	}

	// Record a FAILED heartbeat (status = "fail") with valid non-empty evidence
	failedHb := EpistemicHeartbeat{
		ProjectID:     proj.ID,
		SessionID:     "gate_audit",
		Agent:         "auditor",
		InvariantRule: fact.Claim,
		EvidenceType:  "gate",
		EvidenceRef:   "commit_abc123456",
		Status:        "fail",
	}
	if err := st.RecordHeartbeat(failedHb); err != nil {
		t.Fatalf("RecordHeartbeat with status=fail failed: %v", err)
	}

	// The failed heartbeat must NOT suppress the negative drift signal!
	signalsAfterFail, err := st.DetectNegativeSignals(context.Background(), proj.ID, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("DetectNegativeSignals failed: %v", err)
	}
	if len(signalsAfterFail) != 1 {
		t.Fatalf("expected failed heartbeat to NOT suppress negative signal, but got %d signals", len(signalsAfterFail))
	}
	if signalsAfterFail[0].RuleID != fact.ID {
		t.Fatalf("expected signal rule ID %q, got %q", fact.ID, signalsAfterFail[0].RuleID)
	}

	// Now record a PASSED heartbeat (status = "pass") with valid non-empty evidence
	passedHb := EpistemicHeartbeat{
		ProjectID:     proj.ID,
		SessionID:     "gate_audit",
		Agent:         "auditor",
		InvariantRule: fact.Claim,
		EvidenceType:  "gate",
		EvidenceRef:   "commit_def789012",
		Status:        "pass",
	}
	if err := st.RecordHeartbeat(passedHb); err != nil {
		t.Fatalf("RecordHeartbeat with status=pass failed: %v", err)
	}

	// The passed heartbeat must suppress the negative signal (rule is clean)
	signalsAfterPass, err := st.DetectNegativeSignals(context.Background(), proj.ID, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("DetectNegativeSignals failed: %v", err)
	}
	if len(signalsAfterPass) != 0 {
		t.Fatalf("expected passed heartbeat to suppress negative signal, but got %d signals: %+v", len(signalsAfterPass), signalsAfterPass)
	}
}
