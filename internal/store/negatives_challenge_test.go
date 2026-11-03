package store

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

// TestChallengerNegativeSignalsDriftSuppression performs comprehensive empirical stress-testing
// on DetectNegativeSignals and QueryNegativeSignals to verify drift suppression mechanics.
func TestChallengerNegativeSignalsDriftSuppression(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sila.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer st.Close()

	proj1, err := st.EnsureProject(filepath.Join(dir, "proj1"))
	if err != nil {
		t.Fatalf("EnsureProject proj1 failed: %v", err)
	}
	proj2, err := st.EnsureProject(filepath.Join(dir, "proj2"))
	if err != nil {
		t.Fatalf("EnsureProject proj2 failed: %v", err)
	}

	fact1 := Fact{
		ID:        "f_rule_zero_trust",
		ProjectID: proj1.ID,
		Claim:     "Zero trust network boundaries enforced",
		Kind:      "invariant",
		Tier:      string(TierSemantic),
		FirstSeen: time.Now().UTC().Add(-30 * 24 * time.Hour).Format(time.RFC3339),
	}
	fact2 := Fact{
		ID:        "f_rule_memory_limits",
		ProjectID: proj1.ID,
		Claim:     "No unbounded memory allocation loops",
		Kind:      "invariant",
		Tier:      string(TierSemantic),
		FirstSeen: time.Now().UTC().Add(-30 * 24 * time.Hour).Format(time.RFC3339),
	}
	factProj2 := Fact{
		ID:        "f_rule_proj2",
		ProjectID: proj2.ID,
		Claim:     "Zero trust network boundaries enforced",
		Kind:      "invariant",
		Tier:      string(TierSemantic),
		FirstSeen: time.Now().UTC().Add(-30 * 24 * time.Hour).Format(time.RFC3339),
	}

	for _, f := range []Fact{fact1, fact2, factProj2} {
		if err := st.SaveFact(f); err != nil {
			t.Fatalf("SaveFact %s failed: %v", f.ID, err)
		}
	}

	assertParity := func(t *testing.T, projID string, msg string) ([]NegativeSignal, []NegativeSignal) {
		t.Helper()
		ctx := context.Background()
		dur := 7 * 24 * time.Hour

		det, errDet := st.DetectNegativeSignals(ctx, projID, dur)
		if errDet != nil {
			t.Fatalf("[%s] DetectNegativeSignals failed: %v", msg, errDet)
		}
		qry, errQry := st.QueryNegativeSignals(ctx, projID, dur)
		if errQry != nil {
			t.Fatalf("[%s] QueryNegativeSignals failed: %v", msg, errQry)
		}

		if len(det) != len(qry) {
			t.Fatalf("[%s] Count mismatch: Detect=%d, Query=%d", msg, len(det), len(qry))
		}
		for i := range det {
			if det[i].RuleID != qry[i].RuleID || det[i].Status != qry[i].Status || det[i].SuspicionScore != qry[i].SuspicionScore {
				t.Fatalf("[%s] Parity mismatch at index %d: Detect=%+v, Query=%+v", msg, i, det[i], qry[i])
			}
		}
		return det, qry
	}

	// 1. Initial state: Both rules in proj1 should be detected as negative signals (never asserted)
	det1, _ := assertParity(t, proj1.ID, "Initial state proj1")
	if len(det1) != 2 {
		t.Fatalf("expected 2 negative signals in proj1, got %d", len(det1))
	}

	// 2. Insert FAILED heartbeat for fact1 across different evidence types
	evidenceTypes := []string{"file", "commit", "tool", "test_run", "gate"}
	for _, et := range evidenceTypes {
		hbFail := EpistemicHeartbeat{
			ProjectID:     proj1.ID,
			SessionID:     "fail_session_" + et,
			Agent:         "challenger",
			InvariantRule: fact1.Claim,
			EvidenceType:  et,
			EvidenceRef:   "evidence_fail_" + et,
			Status:        "fail",
		}
		if err := st.RecordHeartbeat(hbFail); err != nil {
			t.Fatalf("RecordHeartbeat failed: %v", err)
		}
	}

	// Assert that ALL failed heartbeats did NOT suppress negative drift for fact1
	detAfterFail, _ := assertParity(t, proj1.ID, "After failed heartbeats")
	if len(detAfterFail) != 2 {
		t.Fatalf("expected 2 negative signals after failed heartbeats (no suppression), got %d", len(detAfterFail))
	}

	// Verify fact1 is still present in negative signals
	foundFact1 := false
	for _, s := range detAfterFail {
		if s.RuleID == fact1.ID {
			foundFact1 = true
			if s.Status != "never_asserted" {
				t.Errorf("expected fact1 status to remain never_asserted, got %q", s.Status)
			}
		}
	}
	if !foundFact1 {
		t.Fatalf("expected fact1 to still be flagged as negative signal")
	}

	// 3. Insert direct ungrounded heartbeats via DB (bypassing RecordHeartbeat)
	// (a) pass status but EMPTY evidence_ref
	_, err = st.DB.Exec(`
		INSERT INTO epistemic_heartbeats (id, project_id, session_id, agent, invariant_rule, evidence_type, evidence_ref, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, "hb_ungrounded_empty", proj1.ID, "s1", "a1", fact1.Claim, "file", "", "pass", time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("raw DB insert failed: %v", err)
	}

	// Verify ungrounded empty evidence does NOT suppress signal
	detAfterUngrounded, _ := assertParity(t, proj1.ID, "After ungrounded empty evidence insert")
	if len(detAfterUngrounded) != 2 {
		t.Fatalf("ungrounded heartbeat with empty evidence mistakenly suppressed signal: got %d signals, expected 2", len(detAfterUngrounded))
	}

	// 4. Now record a valid PASSED heartbeat for fact1 using exact claim match
	hbPass1 := EpistemicHeartbeat{
		ProjectID:     proj1.ID,
		SessionID:     "pass_session_1",
		Agent:         "challenger",
		InvariantRule: fact1.Claim,
		EvidenceType:  "test_run",
		EvidenceRef:   "TestZeroTrustGatePass",
		Status:        "pass",
	}
	if err := st.RecordHeartbeat(hbPass1); err != nil {
		t.Fatalf("RecordHeartbeat pass1 failed: %v", err)
	}

	// Verify fact1 IS now suppressed, but fact2 is NOT suppressed!
	detAfterPass1, _ := assertParity(t, proj1.ID, "After pass1")
	if len(detAfterPass1) != 1 {
		t.Fatalf("expected exactly 1 negative signal remaining (fact2), got %d", len(detAfterPass1))
	}
	if detAfterPass1[0].RuleID != fact2.ID {
		t.Fatalf("expected remaining negative signal to be fact2 (%s), got %s", fact2.ID, detAfterPass1[0].RuleID)
	}

	// 5. Cross-project isolation: verify factProj2 in proj2 is NOT suppressed
	detProj2, _ := assertParity(t, proj2.ID, "Check proj2 isolation")
	if len(detProj2) != 1 {
		t.Fatalf("expected 1 negative signal in proj2, got %d", len(detProj2))
	}
	if detProj2[0].RuleID != factProj2.ID {
		t.Fatalf("expected proj2 signal to be %s, got %s", factProj2.ID, detProj2[0].RuleID)
	}

	// 6. Test rule ID matching variant for fact2: invariant_rule containing fact ID
	hbPass2ByID := EpistemicHeartbeat{
		ProjectID:     proj1.ID,
		SessionID:     "pass_session_2",
		Agent:         "challenger",
		InvariantRule: "Audit check passed for " + fact2.ID + " on main",
		EvidenceType:  "gate",
		EvidenceRef:   "commit_9876543210fedcba",
		Status:        "pass",
	}
	if err := st.RecordHeartbeat(hbPass2ByID); err != nil {
		t.Fatalf("RecordHeartbeat pass2 failed: %v", err)
	}

	// Now fact2 must ALSO be suppressed in proj1
	detAfterPass2, _ := assertParity(t, proj1.ID, "After pass2 by rule ID")
	if len(detAfterPass2) != 0 {
		t.Fatalf("expected 0 negative signals after fact2 pass, got %d: %+v", len(detAfterPass2), detAfterPass2)
	}

	// 7. Test state transitions with expired pass vs recent fail
	// Create fact3 with a pass 10 days ago (expired), and a fail 1 hour ago
	fact3 := Fact{
		ID:        "f_rule_decay_test",
		ProjectID: proj1.ID,
		Claim:     "Audit trails are immutable",
		Kind:      "invariant",
		Tier:      string(TierSemantic),
		FirstSeen: time.Now().UTC().Add(-40 * 24 * time.Hour).Format(time.RFC3339),
	}
	if err := st.SaveFact(fact3); err != nil {
		t.Fatalf("SaveFact fact3 failed: %v", err)
	}

	// Record passed heartbeat 10 days ago (older than 7-day maxSilenceAge)
	_ = os.MkdirAll(proj1.Path, 0755)
	_ = os.WriteFile(filepath.Join(proj1.Path, "old_file.go"), []byte("package old"), 0644)
	oldPassTime := time.Now().UTC().Add(-10 * 24 * time.Hour).Format(time.RFC3339)
	_, err = st.DB.Exec(`
		INSERT INTO epistemic_heartbeats (id, project_id, session_id, agent, invariant_rule, evidence_type, evidence_ref, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, "hb_old_pass", proj1.ID, "s_old", "a_old", fact3.Claim, "file", "old_file.go", "pass", oldPassTime)
	if err != nil {
		t.Fatalf("insert old pass failed: %v", err)
	}

	// Record recent failed heartbeat 1 hour ago
	hbRecentFail := EpistemicHeartbeat{
		ProjectID:     proj1.ID,
		SessionID:     "s_recent_fail",
		Agent:         "challenger",
		InvariantRule: fact3.Claim,
		EvidenceType:  "gate",
		EvidenceRef:   "commit_failed_run",
		Status:        "fail",
	}
	if err := st.RecordHeartbeat(hbRecentFail); err != nil {
		t.Fatalf("RecordHeartbeat recent fail failed: %v", err)
	}

	// The recent fail must NOT update last_heartbeat; last_heartbeat remains 10 days ago,
	// which is past the 7-day cutoff, so status should be "silenced"
	detFact3, _ := assertParity(t, proj1.ID, "Expired pass with recent fail")
	if len(detFact3) != 1 {
		t.Fatalf("expected 1 negative signal for fact3, got %d", len(detFact3))
	}
	if detFact3[0].RuleID != fact3.ID {
		t.Fatalf("expected fact3 signal, got %s", detFact3[0].RuleID)
	}
	if detFact3[0].Status != "silenced" {
		t.Fatalf("expected status 'silenced' for expired pass, got %q", detFact3[0].Status)
	}
	if detFact3[0].LastAssertedAt != oldPassTime {
		t.Fatalf("expected LastAssertedAt to be %q, got %q", oldPassTime, detFact3[0].LastAssertedAt)
	}
}

// TestChallengerConcurrencyStress ensures that concurrent queries and heartbeat records
// run race-free under go test -race.
func TestChallengerConcurrencyStress(t *testing.T) {
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
		ID:        "f_concurrent_1",
		ProjectID: proj.ID,
		Claim:     "Concurrency invariant check",
		Kind:      "invariant",
		Tier:      string(TierSemantic),
		FirstSeen: time.Now().UTC().Add(-10 * 24 * time.Hour).Format(time.RFC3339),
	}
	if err := st.SaveFact(fact); err != nil {
		t.Fatalf("SaveFact failed: %v", err)
	}

	var wg sync.WaitGroup
	ctx := context.Background()

	// 5 readers running DetectNegativeSignals
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_, _ = st.DetectNegativeSignals(ctx, proj.ID, 7*24*time.Hour)
			}
		}()
	}

	// 5 readers running QueryNegativeSignals
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_, _ = st.QueryNegativeSignals(ctx, proj.ID, 7*24*time.Hour)
			}
		}()
	}

	// 5 writers alternating pass and fail heartbeats
	for i := 0; i < 5; i++ {
		wg.Add(1)
		workerID := i
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				status := "pass"
				if (j+workerID)%2 == 0 {
					status = "fail"
				}
				_ = st.RecordHeartbeat(EpistemicHeartbeat{
					ProjectID:     proj.ID,
					SessionID:     "conc_session",
					Agent:         "conc_worker",
					InvariantRule: fact.Claim,
					EvidenceType:  "gate",
					EvidenceRef:   "evidence_conc",
					Status:        status,
				})
			}
		}()
	}

	wg.Wait()
}

// TestChallengerParityDeepEqual tests exact struct equality between QueryNegativeSignals and DetectNegativeSignals
func TestChallengerParityDeepEqual(t *testing.T) {
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

	for i := 1; i <= 5; i++ {
		fact := Fact{
			ID:        "f_parity_" + string(rune('0'+i)),
			ProjectID: proj.ID,
			Claim:     "Parity claim " + string(rune('0'+i)),
			Kind:      "invariant",
			Tier:      string(TierSemantic),
			FirstSeen: time.Now().UTC().Add(-time.Duration(i*5) * 24 * time.Hour).Format(time.RFC3339),
		}
		if err := st.SaveFact(fact); err != nil {
			t.Fatalf("SaveFact failed: %v", err)
		}
	}

	ctx := context.Background()
	det, err := st.DetectNegativeSignals(ctx, proj.ID, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("DetectNegativeSignals failed: %v", err)
	}
	qry, err := st.QueryNegativeSignals(ctx, proj.ID, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("QueryNegativeSignals failed: %v", err)
	}

	if !reflect.DeepEqual(det, qry) {
		t.Fatalf("reflect.DeepEqual failed: DetectNegativeSignals and QueryNegativeSignals differ:\nDetect: %+v\nQuery:  %+v", det, qry)
	}
}
