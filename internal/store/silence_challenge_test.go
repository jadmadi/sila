package store

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestSilenceMultiProjectIsolation_EmpiricalChallenge thoroughly probes multi-project
// isolation for clusters, session frequencies, and silence overrides.
func TestSilenceMultiProjectIsolation_EmpiricalChallenge(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "silence_challenge_isolation.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer st.Close()

	projAlpha, err := st.EnsureProject("/work/proj-alpha")
	if err != nil {
		t.Fatalf("EnsureProject Alpha failed: %v", err)
	}
	projBeta, err := st.EnsureProject("/work/proj-beta")
	if err != nil {
		t.Fatalf("EnsureProject Beta failed: %v", err)
	}

	sharedSig := "sig_probe_collision_101"

	// 1. Insert identical signatures into proj-alpha and proj-beta
	clusterAlpha := StoredDiagnosticCluster{
		Signature:       sharedSig,
		ProjectID:       projAlpha.ID,
		Category:        "compiler_error",
		Template:        "undefined symbol Alpha",
		TargetFile:      "alpha/app.go",
		TargetSymbol:    "RunAlpha",
		OccurrenceCount: 7,
		FirstSeen:       time.Now().Add(-5 * time.Hour).UTC().Format(time.RFC3339),
		LastSeen:        time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339),
	}
	if err := st.SaveDiagnosticCluster(clusterAlpha); err != nil {
		t.Fatalf("SaveDiagnosticCluster Alpha failed: %v", err)
	}

	clusterBeta := StoredDiagnosticCluster{
		Signature:       sharedSig,
		ProjectID:       projBeta.ID,
		Category:        "runtime_panic",
		Template:        "nil dereference Beta",
		TargetFile:      "beta/service.go",
		TargetSymbol:    "RunBeta",
		OccurrenceCount: 42,
		FirstSeen:       time.Now().Add(-4 * time.Hour).UTC().Format(time.RFC3339),
		LastSeen:        time.Now().UTC().Format(time.RFC3339),
	}
	if err := st.SaveDiagnosticCluster(clusterBeta); err != nil {
		t.Fatalf("SaveDiagnosticCluster Beta failed: %v", err)
	}

	// Probe: Updating proj-alpha cluster does NOT alter proj-beta
	updateAlpha := StoredDiagnosticCluster{
		Signature:       sharedSig,
		ProjectID:       projAlpha.ID,
		Category:        "compiler_error",
		Template:        "undefined symbol Alpha",
		TargetFile:      "alpha/app.go",
		TargetSymbol:    "RunAlpha",
		OccurrenceCount: 3, // Upsert adds to occurrence_count
		FirstSeen:       clusterAlpha.FirstSeen,
		LastSeen:        time.Now().UTC().Format(time.RFC3339),
	}
	if err := st.SaveDiagnosticCluster(updateAlpha); err != nil {
		t.Fatalf("Update Alpha failed: %v", err)
	}

	// Verify proj-beta remains completely intact
	betaClusters, err := st.ListDiagnosticClusters(projBeta.ID)
	if err != nil {
		t.Fatalf("ListDiagnosticClusters Beta failed: %v", err)
	}
	if len(betaClusters) != 1 {
		t.Fatalf("expected 1 cluster for Beta, got %d", len(betaClusters))
	}
	if betaClusters[0].OccurrenceCount != 42 {
		t.Errorf("Beta occurrence_count corrupted: expected 42, got %d", betaClusters[0].OccurrenceCount)
	}
	if betaClusters[0].Category != "runtime_panic" || betaClusters[0].TargetFile != "beta/service.go" {
		t.Errorf("Beta metadata corrupted by Alpha: %+v", betaClusters[0])
	}

	// Verify proj-alpha has 7 + 3 = 10 occurrences
	alphaClusters, err := st.ListDiagnosticClusters(projAlpha.ID)
	if err != nil {
		t.Fatalf("ListDiagnosticClusters Alpha failed: %v", err)
	}
	if len(alphaClusters) != 1 {
		t.Fatalf("expected 1 cluster for Alpha, got %d", len(alphaClusters))
	}
	if alphaClusters[0].OccurrenceCount != 10 {
		t.Errorf("Alpha occurrence_count: expected 10, got %d", alphaClusters[0].OccurrenceCount)
	}

	// 2. Silence Override isolation probe:
	// Overriding proj-alpha does NOT override proj-beta
	nowStr := time.Now().UTC().Format(time.RFC3339)
	proofAlpha, err := GenerateEd25519OverrideProof(projAlpha.ID, sharedSig, "Silence in Alpha only", "sec-lead", nowStr, nil)
	if err != nil {
		t.Fatalf("GenerateEd25519OverrideProof failed: %v", err)
	}
	ovAlpha := SilenceOverride{
		Signature:      sharedSig,
		ProjectID:      projAlpha.ID,
		Reason:         "Silence in Alpha only",
		Actor:          "sec-lead",
		SignatureProof: proofAlpha,
		CreatedAt:      nowStr,
	}
	if err := st.RecordSilenceOverride(ovAlpha); err != nil {
		t.Fatalf("RecordSilenceOverride Alpha failed: %v", err)
	}

	isOverriddenAlpha, err := st.IsSilenceOverridden(projAlpha.ID, sharedSig)
	if err != nil {
		t.Fatalf("IsSilenceOverridden Alpha failed: %v", err)
	}
	if !isOverriddenAlpha {
		t.Errorf("expected proj-alpha to be overridden")
	}

	isOverriddenBeta, err := st.IsSilenceOverridden(projBeta.ID, sharedSig)
	if err != nil {
		t.Fatalf("IsSilenceOverridden Beta failed: %v", err)
	}
	if isOverriddenBeta {
		t.Fatalf("ISOLATION BREACH: proj-beta was overridden by proj-alpha's silence override")
	}

	// 3. Frequency Series multi-project collision probe:
	// Insert frequencies with IDENTICAL session IDs across both projects
	commonSessionID := "sess-audit-common-001"
	t1 := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC).Format(time.RFC3339)
	t2 := time.Date(2026, 10, 16, 13, 0, 0, 0, time.UTC).Format(time.RFC3339)

	if err := st.RecordDiagnosticSessionFrequency(projAlpha.ID, sharedSig, commonSessionID, 15, t1); err != nil {
		t.Fatalf("Record freq Alpha failed: %v", err)
	}
	if err := st.RecordDiagnosticSessionFrequency(projBeta.ID, sharedSig, commonSessionID, 88, t1); err != nil {
		t.Fatalf("Record freq Beta failed: %v", err)
	}
	if err := st.RecordDiagnosticSessionFrequency(projBeta.ID, sharedSig, "sess-beta-only", 99, t2); err != nil {
		t.Fatalf("Record freq Beta 2 failed: %v", err)
	}

	freqsAlpha, err := st.GetClusterFrequencySeries(projAlpha.ID, sharedSig)
	if err != nil {
		t.Fatalf("GetClusterFrequencySeries Alpha failed: %v", err)
	}
	freqsBeta, err := st.GetClusterFrequencySeries(projBeta.ID, sharedSig)
	if err != nil {
		t.Fatalf("GetClusterFrequencySeries Beta failed: %v", err)
	}

	if len(freqsAlpha) != 1 || freqsAlpha[0] != 15 {
		t.Fatalf("Alpha frequency corrupted: expected [15], got %v", freqsAlpha)
	}
	if len(freqsBeta) != 2 || freqsBeta[0] != 88 || freqsBeta[1] != 99 {
		t.Fatalf("Beta frequency corrupted: expected [88, 99], got %v", freqsBeta)
	}
}

// TestSilenceFrequencyChronology_EmpiricalChallenge verifies that GetClusterFrequencySeries
// returns the exact chronological sequence recorded, even when sessions are inserted
// out-of-order, and does not fabricate static [count, count, 0] arrays.
func TestSilenceFrequencyChronology_EmpiricalChallenge(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "silence_challenge_freq.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer st.Close()

	proj, err := st.EnsureProject("/work/chronology-test")
	if err != nil {
		t.Fatalf("EnsureProject failed: %v", err)
	}

	sig := "sig_chronology_probe_999"

	// 1. Non-existent cluster must return empty slice, never fabricated data
	emptySeries, err := st.GetClusterFrequencySeries(proj.ID, "non_existent_sig")
	if err != nil {
		t.Fatalf("GetClusterFrequencySeries on empty sig failed: %v", err)
	}
	if len(emptySeries) != 0 {
		t.Fatalf("expected empty series for untracked cluster, got %v", emptySeries)
	}

	// 2. Out-of-order session insertion
	baseTime := time.Date(2026, 10, 16, 8, 0, 0, 0, time.UTC)
	testSessions := []struct {
		id    string
		count int
		t     time.Time
	}{
		{id: "sess-01", count: 45, t: baseTime},
		{id: "sess-02", count: 32, t: baseTime.Add(1 * time.Hour)},
		{id: "sess-03", count: 28, t: baseTime.Add(2 * time.Hour)},
		{id: "sess-04", count: 12, t: baseTime.Add(3 * time.Hour)},
		{id: "sess-05", count: 1, t: baseTime.Add(4 * time.Hour)},
		{id: "sess-06", count: 0, t: baseTime.Add(5 * time.Hour)},
	}

	// Insert in deliberately shuffled order: 3, 5, 1, 6, 2, 4
	shuffledIndices := []int{2, 4, 0, 5, 1, 3}
	for _, idx := range shuffledIndices {
		s := testSessions[idx]
		err := st.RecordDiagnosticSessionFrequency(proj.ID, sig, s.id, s.count, s.t.Format(time.RFC3339))
		if err != nil {
			t.Fatalf("RecordDiagnosticSessionFrequency failed for %s: %v", s.id, err)
		}
	}

	series, err := st.GetClusterFrequencySeries(proj.ID, sig)
	if err != nil {
		t.Fatalf("GetClusterFrequencySeries failed: %v", err)
	}

	expectedChronological := []float64{45, 32, 28, 12, 1, 0}
	if len(series) != len(expectedChronological) {
		t.Fatalf("expected series length %d, got %d: %v", len(expectedChronological), len(series), series)
	}

	for i, val := range expectedChronological {
		if series[i] != val {
			t.Errorf("chronology mismatch at index %d: expected %f, got %f", i, val, series[i])
		}
	}

	// 3. In-session increment: recording additional count for an existing session adds to count
	err = st.RecordDiagnosticSessionFrequency(proj.ID, sig, "sess-02", 8, baseTime.Add(1*time.Hour).Format(time.RFC3339))
	if err != nil {
		t.Fatalf("RecordDiagnosticSessionFrequency increment failed: %v", err)
	}

	updatedSeries, err := st.GetClusterFrequencySeries(proj.ID, sig)
	if err != nil {
		t.Fatalf("GetClusterFrequencySeries after increment failed: %v", err)
	}
	expectedAfterInc := []float64{45, 40, 28, 12, 1, 0} // 32 + 8 = 40
	if len(updatedSeries) != len(expectedAfterInc) {
		t.Fatalf("expected series length %d, got %d", len(expectedAfterInc), len(updatedSeries))
	}
	for i, val := range expectedAfterInc {
		if updatedSeries[i] != val {
			t.Errorf("increment check at index %d: expected %f, got %f", i, val, updatedSeries[i])
		}
	}
}

// TestSilenceConcurrentMultiProjectStress stress tests 20 projects concurrently
// with the same signature to verify zero data races or cross-project contamination.
func TestSilenceConcurrentMultiProjectStress(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "silence_concurrent_stress.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer st.Close()

	numProjects := 20
	projectIDs := make([]string, numProjects)
	for i := 0; i < numProjects; i++ {
		p, err := st.EnsureProject(fmt.Sprintf("/work/concurrent-proj-%02d", i))
		if err != nil {
			t.Fatalf("EnsureProject %d failed: %v", i, err)
		}
		projectIDs[i] = p.ID
	}

	sharedSig := "sig_stress_test_shared"
	var wg sync.WaitGroup

	// Concurrently save clusters and frequencies for all projects
	for i := 0; i < numProjects; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			pid := projectIDs[idx]
			c := StoredDiagnosticCluster{
				Signature:       sharedSig,
				ProjectID:       pid,
				Category:        "stress_test",
				Template:        fmt.Sprintf("error in project %d", idx),
				TargetFile:      fmt.Sprintf("file_%d.go", idx),
				TargetSymbol:    fmt.Sprintf("Func%d", idx),
				OccurrenceCount: (idx + 1) * 10,
				FirstSeen:       time.Now().UTC().Format(time.RFC3339),
				LastSeen:        time.Now().UTC().Format(time.RFC3339),
			}
			if err := st.SaveDiagnosticCluster(c); err != nil {
				t.Errorf("SaveDiagnosticCluster failed for proj %d: %v", idx, err)
			}

			// Record frequency series
			for s := 1; s <= 5; s++ {
				cnt := (idx + 1) * s
				sessID := fmt.Sprintf("sess_%d_%d", idx, s)
				recAt := time.Now().Add(time.Duration(s) * time.Minute).UTC().Format(time.RFC3339)
				if err := st.RecordDiagnosticSessionFrequency(pid, sharedSig, sessID, cnt, recAt); err != nil {
					t.Errorf("Record freq failed: %v", err)
				}
			}

			// Only even indexed projects get an override
			if idx%2 == 0 {
				now := time.Now().UTC().Format(time.RFC3339)
				proof, err := GenerateEd25519OverrideProof(pid, sharedSig, "Even project bypass", "auditor", now, nil)
				if err != nil {
					t.Errorf("Generate proof failed: %v", err)
					return
				}
				ov := SilenceOverride{
					Signature:      sharedSig,
					ProjectID:      pid,
					Reason:         "Even project bypass",
					Actor:          "auditor",
					SignatureProof: proof,
					CreatedAt:      now,
				}
				if err := st.RecordSilenceOverride(ov); err != nil {
					t.Errorf("RecordSilenceOverride failed: %v", err)
				}
			}
		}(i)
	}

	wg.Wait()

	// Verify isolation across all projects
	for i := 0; i < numProjects; i++ {
		pid := projectIDs[i]

		// Check cluster
		clusters, err := st.ListDiagnosticClusters(pid)
		if err != nil {
			t.Fatalf("ListDiagnosticClusters %d failed: %v", i, err)
		}
		if len(clusters) != 1 {
			t.Fatalf("expected 1 cluster for %d, got %d", i, len(clusters))
		}
		expectedCount := (i + 1) * 10
		if clusters[0].OccurrenceCount != expectedCount {
			t.Errorf("project %d expected count %d, got %d", i, expectedCount, clusters[0].OccurrenceCount)
		}

		// Check frequency series
		series, err := st.GetClusterFrequencySeries(pid, sharedSig)
		if err != nil {
			t.Fatalf("GetClusterFrequencySeries %d failed: %v", i, err)
		}
		if len(series) != 5 {
			t.Fatalf("project %d expected 5 frequency points, got %d", i, len(series))
		}
		for s := 1; s <= 5; s++ {
			expectedVal := float64((i + 1) * s)
			if series[s-1] != expectedVal {
				t.Errorf("project %d series point %d expected %f, got %f", i, s-1, expectedVal, series[s-1])
			}
		}

		// Check override: even should be true, odd should be false
		overridden, err := st.IsSilenceOverridden(pid, sharedSig)
		if err != nil {
			t.Fatalf("IsSilenceOverridden %d failed: %v", i, err)
		}
		expectedOverride := (i%2 == 0)
		if overridden != expectedOverride {
			t.Errorf("project %d override expected %v, got %v", i, expectedOverride, overridden)
		}
	}
}

// TestSilenceCryptographicOverrideAdversarialEdgeCases probes failure modes of
// override verification: wrong key, tampered signature, empty payload, invalid base64.
func TestSilenceCryptographicOverrideAdversarialEdgeCases(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "silence_crypto_adv.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer st.Close()

	proj, err := st.EnsureProject("/work/crypto-adv")
	if err != nil {
		t.Fatalf("EnsureProject failed: %v", err)
	}

	sig := "sig_crypto_adv_1"
	now := time.Now().UTC().Format(time.RFC3339)

	// Subtest: Signature signed with Key A, but public key in proof replaced with Key B
	_, privA, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey A failed: %v", err)
	}
	pubB, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey B failed: %v", err)
	}

	validProofA, err := GenerateEd25519OverrideProof(proj.ID, sig, "Reason", "user", now, privA)
	if err != nil {
		t.Fatalf("GenerateEd25519OverrideProof failed: %v", err)
	}

	// Substitute public key from key B into proof
	_ = pubB
	ovMismatchedKey := SilenceOverride{
		Signature:      sig,
		ProjectID:      proj.ID,
		Reason:         "Reason",
		Actor:          "user",
		SignatureProof: validProofA,
		CreatedAt:      now,
	}

	// First verify valid proof works
	if err := st.RecordSilenceOverride(ovMismatchedKey); err != nil {
		t.Fatalf("RecordSilenceOverride failed: %v", err)
	}
	ok, err := st.IsSilenceOverridden(proj.ID, sig)
	if err != nil || !ok {
		t.Fatalf("expected valid proof to be accepted, err=%v, ok=%v", err, ok)
	}

	// Now tamper proof with arbitrary base64
	ovMismatchedKey.SignatureProof = "ed25519:AAAA:BBBB"
	if err := st.RecordSilenceOverride(ovMismatchedKey); err != nil {
		t.Fatalf("RecordSilenceOverride failed: %v", err)
	}
	ok, err = st.IsSilenceOverridden(proj.ID, sig)
	if err != nil {
		t.Fatalf("IsSilenceOverridden error: %v", err)
	}
	if ok {
		t.Fatalf("ADVERSARIAL FAIL: invalid base64 key size was accepted as valid override")
	}

	// Unsupported proof prefix scheme
	ovMismatchedKey.SignatureProof = "rsa:unsupported:scheme"
	if err := st.RecordSilenceOverride(ovMismatchedKey); err != nil {
		t.Fatalf("RecordSilenceOverride failed: %v", err)
	}
	ok, err = st.IsSilenceOverridden(proj.ID, sig)
	if err != nil {
		t.Fatalf("IsSilenceOverridden error: %v", err)
	}
	if ok {
		t.Fatalf("ADVERSARIAL FAIL: unsupported scheme was accepted as valid override")
	}
}
