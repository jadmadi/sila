package store

import (
	"crypto/ed25519"
	"crypto/rand"
	"path/filepath"
	"testing"
	"time"
)

func TestSilenceMultiProjectIsolation(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "silence_isolation.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer s.Close()

	projA, err := s.EnsureProject("/work/project-a")
	if err != nil {
		t.Fatalf("EnsureProject A failed: %v", err)
	}
	projB, err := s.EnsureProject("/work/project-b")
	if err != nil {
		t.Fatalf("EnsureProject B failed: %v", err)
	}

	sharedSig := "sig_shared_test_12345"

	clusterA := StoredDiagnosticCluster{
		Signature:       sharedSig,
		ProjectID:       projA.ID,
		Category:        "compiler_error",
		Template:        "cannot use <*> as type string in A",
		TargetFile:      "pkg/a/main.go",
		TargetSymbol:    "RunA",
		OccurrenceCount: 10,
		FirstSeen:       time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339),
		LastSeen:        time.Now().UTC().Format(time.RFC3339),
	}
	if err := s.SaveDiagnosticCluster(clusterA); err != nil {
		t.Fatalf("SaveDiagnosticCluster A failed: %v", err)
	}

	clusterB := StoredDiagnosticCluster{
		Signature:       sharedSig,
		ProjectID:       projB.ID,
		Category:        "compiler_error",
		Template:        "cannot use <*> as type string in B",
		TargetFile:      "pkg/b/service.go",
		TargetSymbol:    "RunB",
		OccurrenceCount: 25,
		FirstSeen:       time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339),
		LastSeen:        time.Now().UTC().Format(time.RFC3339),
	}
	if err := s.SaveDiagnosticCluster(clusterB); err != nil {
		t.Fatalf("SaveDiagnosticCluster B failed: %v", err)
	}

	// Verify clusters in Project A
	clustersA, err := s.ListDiagnosticClusters(projA.ID)
	if err != nil {
		t.Fatalf("ListDiagnosticClusters A failed: %v", err)
	}
	if len(clustersA) != 1 {
		t.Fatalf("expected 1 cluster in A, got %d", len(clustersA))
	}
	if clustersA[0].OccurrenceCount != 10 || clustersA[0].TargetFile != "pkg/a/main.go" {
		t.Errorf("cluster A corrupted by B: %+v", clustersA[0])
	}

	// Verify clusters in Project B
	clustersB, err := s.ListDiagnosticClusters(projB.ID)
	if err != nil {
		t.Fatalf("ListDiagnosticClusters B failed: %v", err)
	}
	if len(clustersB) != 1 {
		t.Fatalf("expected 1 cluster in B, got %d", len(clustersB))
	}
	if clustersB[0].OccurrenceCount != 25 || clustersB[0].TargetFile != "pkg/b/service.go" {
		t.Errorf("cluster B corrupted by A: %+v", clustersB[0])
	}

	// Record override only for Project A
	proofA, err := GenerateEd25519OverrideProof(projA.ID, sharedSig, "Approved refactor for A", "alice", time.Now().UTC().Format(time.RFC3339), nil)
	if err != nil {
		t.Fatalf("GenerateEd25519OverrideProof failed: %v", err)
	}
	ovA := SilenceOverride{
		Signature:      sharedSig,
		ProjectID:      projA.ID,
		Reason:         "Approved refactor for A",
		Actor:          "alice",
		SignatureProof: proofA,
		CreatedAt:      time.Now().UTC().Format(time.RFC3339),
	}
	if err := s.RecordSilenceOverride(ovA); err != nil {
		t.Fatalf("RecordSilenceOverride A failed: %v", err)
	}

	// Check that A is overridden, but B is NOT overridden
	isOverriddenA, err := s.IsSilenceOverridden(projA.ID, sharedSig)
	if err != nil {
		t.Fatalf("IsSilenceOverridden A failed: %v", err)
	}
	if !isOverriddenA {
		t.Errorf("expected Project A to be overridden")
	}

	isOverriddenB, err := s.IsSilenceOverridden(projB.ID, sharedSig)
	if err != nil {
		t.Fatalf("IsSilenceOverridden B failed: %v", err)
	}
	if isOverriddenB {
		t.Errorf("expected Project B to NOT be overridden by Project A's override")
	}
}

func TestSilenceRealSessionFrequencyHistory(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "silence_freq.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer s.Close()

	proj, err := s.EnsureProject("/work/freq-project")
	if err != nil {
		t.Fatalf("EnsureProject failed: %v", err)
	}

	sig := "sig_freq_test_999"

	// Record chronological session occurrences: 15 -> 18 -> 14 -> 2
	baseTime := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
	sessions := []struct {
		id    string
		count int
		t     time.Time
	}{
		{id: "sess-01", count: 15, t: baseTime},
		{id: "sess-02", count: 18, t: baseTime.Add(1 * time.Hour)},
		{id: "sess-03", count: 14, t: baseTime.Add(2 * time.Hour)},
		{id: "sess-04", count: 2, t: baseTime.Add(3 * time.Hour)},
	}

	for _, sess := range sessions {
		err := s.RecordDiagnosticSessionFrequency(proj.ID, sig, sess.id, sess.count, sess.t.Format(time.RFC3339))
		if err != nil {
			t.Fatalf("RecordDiagnosticSessionFrequency failed for %s: %v", sess.id, err)
		}
	}

	// Query chronological frequency series
	freqs, err := s.GetClusterFrequencySeries(proj.ID, sig)
	if err != nil {
		t.Fatalf("GetClusterFrequencySeries failed: %v", err)
	}

	expected := []float64{15, 18, 14, 2}
	if len(freqs) != len(expected) {
		t.Fatalf("expected %d frequency entries, got %d: %v", len(expected), len(freqs), freqs)
	}
	for i, v := range expected {
		if freqs[i] != v {
			t.Errorf("at index %d: expected %f, got %f", i, v, freqs[i])
		}
	}

	// Saving a cluster with SessionFrequencies populates frequency series
	sigCluster := "sig_from_cluster"
	c := StoredDiagnosticCluster{
		Signature:          sigCluster,
		ProjectID:          proj.ID,
		Category:           "runtime_panic",
		Template:           "nil pointer dereference",
		TargetFile:         "server.go",
		TargetSymbol:       "Handle",
		OccurrenceCount:    40,
		FirstSeen:          baseTime.Format(time.RFC3339),
		LastSeen:           baseTime.Add(4 * time.Hour).Format(time.RFC3339),
		SessionFrequencies: []float64{20, 20, 0},
	}
	if err := s.SaveDiagnosticCluster(c); err != nil {
		t.Fatalf("SaveDiagnosticCluster with SessionFrequencies failed: %v", err)
	}

	clusterFreqs, err := s.GetClusterFrequencySeries(proj.ID, sigCluster)
	if err != nil {
		t.Fatalf("GetClusterFrequencySeries for cluster failed: %v", err)
	}
	if len(clusterFreqs) != 3 {
		t.Fatalf("expected 3 frequencies for cluster, got %d: %v", len(clusterFreqs), clusterFreqs)
	}
	if clusterFreqs[0] != 20 || clusterFreqs[1] != 20 || clusterFreqs[2] != 0 {
		t.Errorf("unexpected cluster freqs: %v", clusterFreqs)
	}
}

func TestSilenceCryptographicOverrideVerification(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "silence_crypto.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer s.Close()

	proj, err := s.EnsureProject("/work/crypto-proj")
	if err != nil {
		t.Fatalf("EnsureProject failed: %v", err)
	}

	sig := "sig_crypto_test_001"
	reason := "Intentional flag removal verified by team lead"
	actor := "lead_auditor"
	now := time.Now().UTC().Format(time.RFC3339)

	// 1. Valid Ed25519 proof accepted
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}

	proofEd25519, err := GenerateEd25519OverrideProof(proj.ID, sig, reason, actor, now, priv)
	if err != nil {
		t.Fatalf("GenerateEd25519OverrideProof failed: %v", err)
	}
	_ = pub

	ovValid := SilenceOverride{
		Signature:      sig,
		ProjectID:      proj.ID,
		Reason:         reason,
		Actor:          actor,
		SignatureProof: proofEd25519,
		CreatedAt:      now,
	}
	if err := s.RecordSilenceOverride(ovValid); err != nil {
		t.Fatalf("RecordSilenceOverride failed: %v", err)
	}

	overridden, err := s.IsSilenceOverridden(proj.ID, sig)
	if err != nil {
		t.Fatalf("IsSilenceOverridden failed: %v", err)
	}
	if !overridden {
		t.Errorf("expected valid Ed25519 override to be accepted")
	}

	// 2. Tampered reason rejected
	ovTamperedReason := ovValid
	ovTamperedReason.Reason = "Sneaky bypass without approval"
	if err := s.RecordSilenceOverride(ovTamperedReason); err != nil {
		t.Fatalf("RecordSilenceOverride tampered failed: %v", err)
	}
	overridden, err = s.IsSilenceOverridden(proj.ID, sig)
	if err != nil {
		t.Fatalf("IsSilenceOverridden query failed: %v", err)
	}
	if overridden {
		t.Errorf("expected tampered reason to be rejected by cryptographic verification")
	}

	// 3. Empty signature proof rejected
	ovEmptyProof := ovValid
	ovEmptyProof.SignatureProof = ""
	if err := s.RecordSilenceOverride(ovEmptyProof); err != nil {
		t.Fatalf("RecordSilenceOverride empty failed: %v", err)
	}
	overridden, err = s.IsSilenceOverridden(proj.ID, sig)
	if err != nil {
		t.Fatalf("IsSilenceOverridden query failed: %v", err)
	}
	if overridden {
		t.Errorf("expected empty signature proof to be rejected")
	}

	// 4. Garbage / tampered proof string rejected
	ovGarbageProof := ovValid
	ovGarbageProof.SignatureProof = "ed25519:not_a_valid_proof:corrupted"
	if err := s.RecordSilenceOverride(ovGarbageProof); err != nil {
		t.Fatalf("RecordSilenceOverride garbage failed: %v", err)
	}
	overridden, err = s.IsSilenceOverridden(proj.ID, sig)
	if err != nil {
		t.Fatalf("IsSilenceOverridden query failed: %v", err)
	}
	if overridden {
		t.Errorf("expected garbage signature proof to be rejected")
	}

	// 5. Valid HMAC proof accepted
	secret := []byte("super-secret-hmac-key-for-test-12345")
	t.Setenv("SILA_OVERRIDE_SECRET", string(secret))

	proofHMAC, err := GenerateHMACOverrideProof(proj.ID, sig, reason, actor, now, secret)
	if err != nil {
		t.Fatalf("GenerateHMACOverrideProof failed: %v", err)
	}
	ovHMAC := SilenceOverride{
		Signature:      sig,
		ProjectID:      proj.ID,
		Reason:         reason,
		Actor:          actor,
		SignatureProof: proofHMAC,
		CreatedAt:      now,
	}
	if err := s.RecordSilenceOverride(ovHMAC); err != nil {
		t.Fatalf("RecordSilenceOverride HMAC failed: %v", err)
	}
	overridden, err = s.IsSilenceOverridden(proj.ID, sig)
	if err != nil {
		t.Fatalf("IsSilenceOverridden query failed: %v", err)
	}
	if !overridden {
		t.Errorf("expected valid HMAC override to be accepted")
	}

	// 6. Tampered HMAC rejected
	t.Setenv("SILA_OVERRIDE_SECRET", "different-secret-invalid")
	overridden, err = s.IsSilenceOverridden(proj.ID, sig)
	if err != nil {
		t.Fatalf("IsSilenceOverridden query failed: %v", err)
	}
	if overridden {
		t.Errorf("expected HMAC with wrong secret to be rejected")
	}
}
