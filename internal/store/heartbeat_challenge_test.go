package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestHeartbeatValidationAdversarialChallenge empirically verifies the validation
// invariants of RecordHeartbeat against adversarial edge cases.
func TestHeartbeatValidationAdversarialChallenge(t *testing.T) {
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

	t.Run("Empty and Whitespace EvidenceRef Rejections", func(t *testing.T) {
		adversarialEmptyRefs := []string{
			"",
			" ",
			"\t",
			"\n",
			"\r\n",
			"   \t\n\r   ",
			"\u00A0",       // non-breaking space
			"\u2000\u2001", // en quad / em quad
			"\u3000",       // ideographic space
			"  \u3000 \t  ",
		}

		for _, badRef := range adversarialEmptyRefs {
			err := st.RecordHeartbeat(EpistemicHeartbeat{
				ProjectID:     proj.ID,
				InvariantRule: "Invariant rule",
				EvidenceType:  "file",
				EvidenceRef:   badRef,
				Status:        "pass",
			})
			if err == nil {
				t.Fatalf("expected error for empty/whitespace evidence_ref %q, got nil", badRef)
			}
			if !strings.Contains(err.Error(), "evidence_ref is required") {
				t.Fatalf("expected evidence_ref error message, got: %v", err)
			}
		}
	})

	t.Run("Invalid Status Rejections", func(t *testing.T) {
		adversarialStatuses := []string{
			"",
			" ",
			"unknown",
			"pending",
			"passed",
			"failed",
			"PASS",
			"FAIL",
			"Pass",
			"Fail",
			"true",
			"false",
			"1",
			"0",
			"ok",
			"error",
			"skipped",
			"' OR '1'='1",
		}

		for _, badStatus := range adversarialStatuses {
			err := st.RecordHeartbeat(EpistemicHeartbeat{
				ProjectID:     proj.ID,
				InvariantRule: "Invariant rule",
				EvidenceType:  "file",
				EvidenceRef:   "cmd/sila/main.go",
				Status:        badStatus,
			})
			if err == nil {
				t.Fatalf("expected error for invalid status %q, got nil", badStatus)
			}
			if !strings.Contains(err.Error(), "invalid heartbeat status") {
				t.Fatalf("expected invalid heartbeat status error message, got: %v", err)
			}
		}
	})

	t.Run("Invalid EvidenceType Rejections", func(t *testing.T) {
		adversarialTypes := []string{
			"magic",
			"arbitrary",
			"custom",
			"FILE",
			"COMMIT",
			"tool_run",
			"system",
			"eval",
			"../escape",
			"file; DROP TABLE epistemic_heartbeats;--",
		}

		for _, badType := range adversarialTypes {
			err := st.RecordHeartbeat(EpistemicHeartbeat{
				ProjectID:     proj.ID,
				InvariantRule: "Invariant rule",
				EvidenceType:  badType,
				EvidenceRef:   "some/ref.go",
				Status:        "pass",
			})
			if err == nil {
				t.Fatalf("expected error for invalid evidence_type %q, got nil", badType)
			}
			if !strings.Contains(err.Error(), "invalid evidence_type") {
				t.Fatalf("expected invalid evidence_type error, got: %v", err)
			}
		}
	})

	t.Run("Missing Required Fields", func(t *testing.T) {
		// Missing ProjectID
		err := st.RecordHeartbeat(EpistemicHeartbeat{
			ProjectID:     "",
			InvariantRule: "Rule",
			EvidenceRef:   "file.go",
			Status:        "pass",
		})
		if err == nil || !strings.Contains(err.Error(), "project_id is required") {
			t.Fatalf("expected project_id required error, got: %v", err)
		}

		// Missing InvariantRule
		err = st.RecordHeartbeat(EpistemicHeartbeat{
			ProjectID:     proj.ID,
			InvariantRule: "",
			EvidenceRef:   "file.go",
			Status:        "pass",
		})
		if err == nil || !strings.Contains(err.Error(), "invariant_rule is required") {
			t.Fatalf("expected invariant_rule required error, got: %v", err)
		}
	})

	t.Run("Valid Insert and Field Preservation", func(t *testing.T) {
		validTypes := []string{"file", "commit", "tool", "test_run", "gate"}

		for _, et := range validTypes {
			ref := "ref/to/" + et
			if et == "file" {
				filePath := filepath.Join(dir, ref)
				_ = os.MkdirAll(filepath.Dir(filePath), 0755)
				_ = os.WriteFile(filePath, []byte("file evidence"), 0644)
			}

			hb := EpistemicHeartbeat{
				ProjectID:     proj.ID,
				SessionID:     "sess_valid_" + et,
				Agent:         "agent_tester",
				InvariantRule: "Rule_" + et,
				EvidenceType:  et,
				EvidenceRef:   ref,
				Status:        "pass",
				CreatedAt:     time.Now().UTC().Format(time.RFC3339),
			}

			if err := st.RecordHeartbeat(hb); err != nil {
				t.Fatalf("RecordHeartbeat failed for valid type %q: %v", et, err)
			}
		}

		// Verify empty evidence_type defaults to "file"
		defaultPath := filepath.Join(dir, "ref", "default.go")
		_ = os.MkdirAll(filepath.Dir(defaultPath), 0755)
		_ = os.WriteFile(defaultPath, []byte("package ref"), 0644)

		hbDefault := EpistemicHeartbeat{
			ProjectID:     proj.ID,
			SessionID:     "sess_default",
			Agent:         "agent_tester",
			InvariantRule: "Rule_default_type",
			EvidenceType:  "",
			EvidenceRef:   "ref/default.go",
			Status:        "pass",
		}
		if err := st.RecordHeartbeat(hbDefault); err != nil {
			t.Fatalf("RecordHeartbeat failed with empty evidence_type: %v", err)
		}

		// Verify retrieval
		recent, err := st.ListRecentHeartbeats(proj.ID, 50)
		if err != nil {
			t.Fatalf("ListRecentHeartbeats failed: %v", err)
		}
		if len(recent) < len(validTypes)+1 {
			t.Fatalf("expected at least %d heartbeats, got %d", len(validTypes)+1, len(recent))
		}

		var foundDefault bool
		for _, item := range recent {
			if item.InvariantRule == "Rule_default_type" {
				foundDefault = true
				if item.EvidenceType != "file" {
					t.Fatalf("expected defaulted evidence_type 'file', got %q", item.EvidenceType)
				}
				if item.Status != "pass" {
					t.Fatalf("expected status 'pass', got %q", item.Status)
				}
			}
		}
		if !foundDefault {
			t.Fatal("defaulted evidence_type heartbeat not found in recent list")
		}
	})

	t.Run("Heartbeat Status Update / Upsert", func(t *testing.T) {
		initial := EpistemicHeartbeat{
			ProjectID:     proj.ID,
			SessionID:     "sess_upsert",
			Agent:         "agent_tester",
			InvariantRule: "Invariant Upsert Rule",
			EvidenceType:  "gate",
			EvidenceRef:   "commit_123456",
			Status:        "fail",
			CreatedAt:     time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339),
		}
		if err := st.RecordHeartbeat(initial); err != nil {
			t.Fatalf("initial fail heartbeat failed: %v", err)
		}

		// Re-record with pass on same key identity
		updated := EpistemicHeartbeat{
			ProjectID:     proj.ID,
			SessionID:     "sess_upsert",
			Agent:         "agent_tester",
			InvariantRule: "Invariant Upsert Rule",
			EvidenceType:  "gate",
			EvidenceRef:   "commit_123456",
			Status:        "pass",
			CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		}
		if err := st.RecordHeartbeat(updated); err != nil {
			t.Fatalf("updated pass heartbeat failed: %v", err)
		}

		recent, err := st.ListRecentHeartbeats(proj.ID, 50)
		if err != nil {
			t.Fatalf("ListRecentHeartbeats failed: %v", err)
		}

		var found bool
		for _, item := range recent {
			if item.InvariantRule == "Invariant Upsert Rule" && item.EvidenceRef == "commit_123456" {
				found = true
				if item.Status != "pass" {
					t.Fatalf("expected updated status 'pass', got %q", item.Status)
				}
			}
		}
		if !found {
			t.Fatal("upserted heartbeat not found")
		}
	})

	t.Run("Nonexistent Evidence File Rejections", func(t *testing.T) {
		err := st.RecordHeartbeat(EpistemicHeartbeat{
			ProjectID:     proj.ID,
			InvariantRule: "Rule nonexistent",
			EvidenceType:  "file",
			EvidenceRef:   filepath.Join(dir, "nonexistent-file.go"),
			Status:        "pass",
		})
		if err == nil {
			t.Fatal("expected error for nonexistent evidence file, got nil")
		}
		if !strings.Contains(err.Error(), "evidence file does not exist") {
			t.Fatalf("expected 'evidence file does not exist' error, got: %v", err)
		}
	})

	t.Run("SQL Injection Resilience", func(t *testing.T) {
		injectionHb := EpistemicHeartbeat{
			ProjectID:     proj.ID,
			SessionID:     "sess'); DROP TABLE epistemic_heartbeats;--",
			Agent:         "agent'--",
			InvariantRule: "Rule' OR '1'='1",
			EvidenceType:  "gate",
			EvidenceRef:   "gate_check' OR '1'='1",
			Status:        "pass",
		}
		if err := st.RecordHeartbeat(injectionHb); err != nil {
			t.Fatalf("RecordHeartbeat failed on parameterized input: %v", err)
		}

		// Check table still exists and functions
		_, err := st.ListRecentHeartbeats(proj.ID, 10)
		if err != nil {
			t.Fatalf("table damaged or query failed: %v", err)
		}
	})

	t.Run("Failed Status Invariant Negative Signal Non-Suppression", func(t *testing.T) {
		fact := Fact{
			ID:        "f_chal_inv",
			ProjectID: proj.ID,
			Claim:     "No dynamic unverified reflection",
			Kind:      "invariant",
			Tier:      string(TierSemantic),
			FirstSeen: time.Now().UTC().Add(-10 * 24 * time.Hour).Format(time.RFC3339),
		}
		if err := st.SaveFact(fact); err != nil {
			t.Fatalf("SaveFact failed: %v", err)
		}

		// Record fail heartbeat
		failHb := EpistemicHeartbeat{
			ProjectID:     proj.ID,
			SessionID:     "sess_eval",
			Agent:         "evaluator",
			InvariantRule: fact.Claim,
			EvidenceType:  "test_run",
			EvidenceRef:   "TestReflectionCheck",
			Status:        "fail",
		}
		if err := st.RecordHeartbeat(failHb); err != nil {
			t.Fatalf("failed status heartbeat record failed: %v", err)
		}

		// Detect negative signals: MUST NOT be suppressed by fail heartbeat
		signals, err := st.DetectNegativeSignals(context.Background(), proj.ID, 7*24*time.Hour)
		if err != nil {
			t.Fatalf("DetectNegativeSignals failed: %v", err)
		}

		var signalFound bool
		for _, s := range signals {
			if s.RuleID == fact.ID {
				signalFound = true
				break
			}
		}
		if !signalFound {
			t.Fatal("negative signal was suppressed by failed heartbeat; must NOT be suppressed")
		}
	})
}

// TestRecordHeartbeat_NonexistentEvidence verifies that recording a heartbeat with a nonexistent
// evidence file fails and returns an error (AC-001).
func TestRecordHeartbeat_NonexistentEvidence(t *testing.T) {
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

	nonexistentPath := filepath.Join(dir, "nonexistent-evidence-file.txt")

	err = st.RecordHeartbeat(EpistemicHeartbeat{
		ProjectID:     proj.ID,
		InvariantRule: "Must verify evidence file exists on disk",
		EvidenceType:  "file",
		EvidenceRef:   nonexistentPath,
		Status:        "pass",
	})

	if err == nil {
		t.Fatal("expected error when recording heartbeat with nonexistent evidence file, got nil")
	}

	if !strings.Contains(err.Error(), "evidence file does not exist") {
		t.Fatalf("unexpected error message: %v", err)
	}
}
