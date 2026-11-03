package goals

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupTestGoalWithClaim(t *testing.T, slug string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	goalDir := filepath.Join(dir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatalf("mkdir goalDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte("# Goal: "+slug+"\n"), 0644); err != nil {
		t.Fatalf("write goal.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte("# Plan\n- [ ] Task 1\n"), 0644); err != nil {
		t.Fatalf("write plan.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "facts.md"), []byte("# Facts\n"), 0644); err != nil {
		t.Fatalf("write facts.md: %v", err)
	}

	// Create claim and lock
	claimContent := `{"agent":"tester","claimed_at":"2026-10-12T00:00:00Z","status":"active"}`
	if err := os.WriteFile(filepath.Join(goalDir, "claim.json"), []byte(claimContent), 0644); err != nil {
		t.Fatalf("write claim.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, ".claim.lock"), []byte("lock-token"), 0644); err != nil {
		t.Fatalf("write .claim.lock: %v", err)
	}

	return dir, goalDir
}

func TestCertifyRejectsInvalidEvidence(t *testing.T) {
	cases := []struct {
		name        string
		content     string
		errContains string
	}{
		{
			name:        "empty text",
			content:     "",
			errContains: "EVIDENCE_FORMAT_UNSUPPORTED",
		},
		{
			name:        "whitespace only",
			content:     "   \n\t  ",
			errContains: "EVIDENCE_FORMAT_UNSUPPORTED",
		},
		{
			name:        "plain text log",
			content:     "=== RUN TestSomething\n--- PASS: TestSomething (0.01s)\nPASS\n",
			errContains: "EVIDENCE_FORMAT_UNSUPPORTED",
		},
		{
			name:        "invalid JSON syntax",
			content:     `{"facts": [{"id": "f1", "passed": true}`,
			errContains: "EVIDENCE_FORMAT_UNSUPPORTED",
		},
		{
			name:        "empty facts array",
			content:     `{"facts": []}`,
			errContains: "zero facts",
		},
		{
			name:        "empty JSON object",
			content:     `{}`,
			errContains: "zero facts",
		},
		{
			name:        "all false results",
			content:     `{"facts": [{"id": "f1", "passed": false}]}`,
			errContains: "passed=false",
		},
		{
			name: "one false in list",
			content: `{"facts": [
				{"id": "f1", "passed": true},
				{"id": "f2", "passed": false}
			]}`,
			errContains: "passed=false",
		},
		{
			name: "duplicate fact IDs",
			content: `{"facts": [
				{"id": "f1", "passed": true},
				{"id": "f1", "passed": true}
			]}`,
			errContains: "duplicate",
		},
		{
			name: "empty fact ID",
			content: `{"facts": [
				{"id": "", "passed": true}
			]}`,
			errContains: "empty fact ID",
		},
		{
			name: "conflicting passed true verified false",
			content: `{"facts": [
				{"id": "f1", "passed": true, "verified": false}
			]}`,
			errContains: "contradictory",
		},
		{
			name: "conflicting passed false verified true",
			content: `{"facts": [
				{"id": "f1", "passed": false, "verified": true}
			]}`,
			errContains: "contradictory",
		},
		{
			name: "neither passed nor verified set",
			content: `{"facts": [
				{"id": "f1", "fact": "something"}
			]}`,
			errContains: "neither",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, goalDir := setupTestGoalWithClaim(t, "rejection-test")
			evFile := filepath.Join(dir, "evidence.txt")
			if err := os.WriteFile(evFile, []byte(tc.content), 0644); err != nil {
				t.Fatalf("write evidence file: %v", err)
			}

			cert, err := CertifyGoal(dir, "rejection-test", evFile, "@reviewer", "attempted certify")
			if err == nil {
				t.Fatalf("expected error for case %q, got success: %+v", tc.name, cert)
			}
			if !strings.Contains(err.Error(), tc.errContains) {
				t.Errorf("expected error containing %q, got: %v", tc.errContains, err)
			}

			// Verify certification.json was NOT created
			certFile := filepath.Join(goalDir, "certification.json")
			if fileExists(certFile) {
				t.Errorf("certification.json should not exist after rejection")
			}

			// Verify claim and lock were NOT removed
			if !fileExists(filepath.Join(goalDir, "claim.json")) {
				t.Errorf("claim.json should be preserved after rejection")
			}
			if !fileExists(filepath.Join(goalDir, ".claim.lock")) {
				t.Errorf(".claim.lock should be preserved after rejection")
			}
		})
	}
}

func TestCertifyPreservesStateOnFailure(t *testing.T) {
	dir, goalDir := setupTestGoalWithClaim(t, "preserve-state-test")

	// Set pre-existing certification file
	priorCert := `{"goal_slug":"preserve-state-test","verified":true,"note":"prior valid certificate"}`
	certFile := filepath.Join(goalDir, "certification.json")
	if err := os.WriteFile(certFile, []byte(priorCert), 0644); err != nil {
		t.Fatalf("write prior certification: %v", err)
	}

	// Create charter
	charterFile := filepath.Join(dir, ".sila", "charters", "preserve-state-test.json")
	_ = os.MkdirAll(filepath.Dir(charterFile), 0755)
	if err := os.WriteFile(charterFile, []byte(`{"goal_slug":"preserve-state-test"}`), 0644); err != nil {
		t.Fatalf("write charter: %v", err)
	}

	// Attempt certification with invalid evidence
	invalidEv := filepath.Join(dir, "invalid.log")
	if err := os.WriteFile(invalidEv, []byte("arbitrary plain text report"), 0644); err != nil {
		t.Fatalf("write invalid evidence: %v", err)
	}

	_, err := CertifyGoal(dir, "preserve-state-test", invalidEv, "@reviewer", "bad attempt")
	if err == nil {
		t.Fatalf("expected certification to fail")
	}

	// Verify previous certification bytes are unchanged
	data, err := os.ReadFile(certFile)
	if err != nil {
		t.Fatalf("read certification file: %v", err)
	}
	if string(data) != priorCert {
		t.Errorf("prior certificate changed: expected %q, got %q", priorCert, string(data))
	}

	// Verify claim still exists
	if !fileExists(filepath.Join(goalDir, "claim.json")) {
		t.Errorf("claim.json was unexpectedly removed")
	}
	if !fileExists(filepath.Join(goalDir, ".claim.lock")) {
		t.Errorf(".claim.lock was unexpectedly removed")
	}

	// Verify charter still exists
	if !fileExists(charterFile) {
		t.Errorf("charter was unexpectedly removed")
	}
}

func TestLegacyCertifyCompatibility(t *testing.T) {
	dir, goalDir := setupTestGoalWithClaim(t, "legacy-compat-test")

	// Valid legacy evidence with facts
	evFile := filepath.Join(dir, "valid-legacy.json")
	content := `{
		"facts": [
			{"id": "fact-1", "fact": "First requirement met", "passed": true},
			{"id": "fact-2", "fact": "Second requirement met", "verified": true}
		]
	}`
	if err := os.WriteFile(evFile, []byte(content), 0644); err != nil {
		t.Fatalf("write valid legacy evidence: %v", err)
	}

	cert, err := CertifyGoal(dir, "legacy-compat-test", evFile, "@reviewer", "legacy facts certified")
	if err != nil {
		t.Fatalf("CertifyGoal failed on valid legacy evidence: %v", err)
	}

	if !cert.Verified {
		t.Errorf("expected Verified=true")
	}
	if cert.FactsPassed != 2 || cert.FactsTotal != 2 {
		t.Errorf("expected 2/2 facts passed, got %d/%d", cert.FactsPassed, cert.FactsTotal)
	}
	if cert.EvidenceKind != "legacy_facts" {
		t.Errorf("expected EvidenceKind='legacy_facts', got %q", cert.EvidenceKind)
	}

	// Verify certification.json was written and contains evidence_kind
	data, err := os.ReadFile(filepath.Join(goalDir, "certification.json"))
	if err != nil {
		t.Fatalf("read certification.json: %v", err)
	}
	var stored CertificationRecord
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("unmarshal stored certification: %v", err)
	}
	if stored.EvidenceKind != "legacy_facts" {
		t.Errorf("stored certificate missing evidence_kind: %+v", stored)
	}

	// Also test alternative top-level "results" array
	dir2, _ := setupTestGoalWithClaim(t, "legacy-results-test")
	evFile2 := filepath.Join(dir2, "results-legacy.json")
	content2 := `{
		"results": [
			{"id": "res-1", "passed": true}
		]
	}`
	if err := os.WriteFile(evFile2, []byte(content2), 0644); err != nil {
		t.Fatalf("write results legacy evidence: %v", err)
	}

	cert2, err := CertifyGoal(dir2, "legacy-results-test", evFile2, "@reviewer", "results certified")
	if err != nil {
		t.Fatalf("CertifyGoal failed on results legacy evidence: %v", err)
	}
	if !cert2.Verified || cert2.FactsPassed != 1 || cert2.FactsTotal != 1 {
		t.Errorf("unexpected certification for results: %+v", cert2)
	}
}
