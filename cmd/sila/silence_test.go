package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestSilenceCLIBasic(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "silence_test.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	proj, err := st.EnsureProject(tmp)
	if err != nil {
		st.Close()
		t.Fatal(err)
	}

	cluster := store.StoredDiagnosticCluster{
		Signature:          "sig-test-1234",
		ProjectID:          proj.ID,
		Category:           "compiler_error",
		Template:           "cannot use <*> as type string",
		TargetFile:         "main.go",
		TargetSymbol:       "RunServer",
		OccurrenceCount:    42,
		FirstSeen:          time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339),
		LastSeen:           time.Now().UTC().Format(time.RFC3339),
		SessionFrequencies: []float64{15, 15, 12},
	}
	if err := st.SaveDiagnosticCluster(cluster); err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()

	ui := render.New(os.Stdout, true)

	// Test status command with JSON output
	out := captureStdout(t, func() {
		runSilence(ui, dbPath, proj.ID, true, false, []string{"status"})
	})

	var statusRes struct {
		ProjectID string `json:"project_id"`
		Clusters  int    `json:"clusters"`
	}
	if err := json.Unmarshal([]byte(out), &statusRes); err != nil {
		t.Fatalf("unmarshal status JSON: %v, out=%s", err, out)
	}
	if statusRes.Clusters != 1 {
		t.Errorf("clusters = %d, want 1", statusRes.Clusters)
	}

	// Test list command with JSON output
	listOut := captureStdout(t, func() {
		runSilence(ui, dbPath, proj.ID, true, false, []string{"list"})
	})
	var listRes []store.StoredDiagnosticCluster
	if err := json.Unmarshal([]byte(listOut), &listRes); err != nil {
		t.Fatalf("unmarshal list JSON: %v, out=%s", err, listOut)
	}
	if len(listRes) != 1 || listRes[0].Signature != "sig-test-1234" {
		t.Errorf("unexpected list output: %+v", listRes)
	}

	// Test override command with JSON output
	overrideOut := captureStdout(t, func() {
		runSilence(ui, dbPath, proj.ID, true, false, []string{"override", "sig-test-1234", "--reason=Intentional refactor"})
	})
	var ovRes store.SilenceOverride
	if err := json.Unmarshal([]byte(overrideOut), &ovRes); err != nil {
		t.Fatalf("unmarshal override JSON: %v, out=%s", err, overrideOut)
	}
	if ovRes.Signature != "sig-test-1234" {
		t.Errorf("override signature = %q, want sig-test-1234", ovRes.Signature)
	}
	if !strings.HasPrefix(ovRes.SignatureProof, "ed25519:") {
		t.Errorf("override signature_proof = %q, want ed25519:... proof", ovRes.SignatureProof)
	}

	// Verify cryptographic verification in store
	stVerify, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer stVerify.Close()

	isOverridden, err := stVerify.IsSilenceOverridden(proj.ID, "sig-test-1234")
	if err != nil {
		t.Fatalf("IsSilenceOverridden failed: %v", err)
	}
	if !isOverridden {
		t.Errorf("expected valid cryptographic override to verify successfully")
	}
}

func TestGateSilenceSentryClean(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "clean.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnsureProject(tmp); err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()

	t.Setenv("SILA_DB", dbPath)
	check := gateSilenceSentry(tmp)
	if check.Status != "pass" {
		t.Errorf("expected pass for clean temp dir, got %+v", check)
	}
}

func TestGateSilenceSentryFailClosed(t *testing.T) {
	tmp := t.TempDir()

	// 1. Missing / unopenable database must fail closed
	nonexistentDB := filepath.Join(tmp, "missing_subdir", "nonexistent.db")
	t.Setenv("SILA_DB", nonexistentDB)

	check := gateSilenceSentry(tmp)
	if check.Status != "fail" {
		t.Fatalf("expected fail status on missing database, got %+v", check)
	}
	if !strings.Contains(check.Detail, "database open error") {
		t.Errorf("expected detail to mention database open error, got %q", check.Detail)
	}

	// 2. Corrupt database must fail closed
	corruptDB := filepath.Join(tmp, "corrupt.db")
	if err := os.WriteFile(corruptDB, []byte("NOT A SQLITE DATABASE FILE"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SILA_DB", corruptDB)

	checkCorrupt := gateSilenceSentry(tmp)
	if checkCorrupt.Status != "fail" {
		t.Fatalf("expected fail status on corrupt database, got %+v", checkCorrupt)
	}
}

func TestGateSilenceSentryAnomalyAndOverrideLifecycle(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "lifecycle.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	proj, err := st.EnsureProject(tmp)
	if err != nil {
		st.Close()
		t.Fatal(err)
	}

	sig := "sig-extinct-flag"
	cluster := store.StoredDiagnosticCluster{
		Signature:          sig,
		ProjectID:          proj.ID,
		Category:           "compiler_error",
		Template:           "cannot find symbol <*> in <*>",
		TargetFile:         "pkg/nonexistent_file.go",
		TargetSymbol:       "MissingSymbol",
		OccurrenceCount:    60,
		FirstSeen:          time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339),
		LastSeen:           time.Now().UTC().Format(time.RFC3339),
		SessionFrequencies: []float64{20, 20, 20, 0, 0, 0},
	}
	if err := st.SaveDiagnosticCluster(cluster); err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()

	t.Setenv("SILA_DB", dbPath)

	// Step 1: Without override, gate detects uncoupled silence anomaly and fails closed
	checkAnomaly := gateSilenceSentry(tmp)
	if checkAnomaly.Status != "fail" {
		t.Fatalf("expected fail status for uncoupled silence anomaly, got %+v", checkAnomaly)
	}
	if !strings.Contains(checkAnomaly.Detail, "detected 1 uncoupled silence anomaly") {
		t.Errorf("expected detail to mention anomaly, got %q", checkAnomaly.Detail)
	}

	// Step 2: Record tampered override (invalid signature proof) -> gate still fails closed
	stMut, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	badOv := store.SilenceOverride{
		Signature:      sig,
		ProjectID:      proj.ID,
		Reason:         "Bypass without valid signature",
		Actor:          "attacker",
		SignatureProof: "ed25519:bad_pub:bad_sig",
		CreatedAt:      time.Now().UTC().Format(time.RFC3339),
	}
	if err := stMut.RecordSilenceOverride(badOv); err != nil {
		stMut.Close()
		t.Fatal(err)
	}
	stMut.Close()

	checkTampered := gateSilenceSentry(tmp)
	if checkTampered.Status != "fail" {
		t.Fatalf("expected fail status when override has tampered proof, got %+v", checkTampered)
	}

	// Step 3: Record valid cryptographic Ed25519 override -> gate passes cleanly
	now := time.Now().UTC().Format(time.RFC3339)
	reason := "Authorized intentional removal verified by architect"
	proof, err := store.GenerateEd25519OverrideProof(proj.ID, sig, reason, "architect", now, nil)
	if err != nil {
		t.Fatal(err)
	}

	stValid, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	validOv := store.SilenceOverride{
		Signature:      sig,
		ProjectID:      proj.ID,
		Reason:         reason,
		Actor:          "architect",
		SignatureProof: proof,
		CreatedAt:      now,
	}
	if err := stValid.RecordSilenceOverride(validOv); err != nil {
		stValid.Close()
		t.Fatal(err)
	}
	stValid.Close()

	checkPass := gateSilenceSentry(tmp)
	if checkPass.Status != "pass" {
		t.Fatalf("expected pass status after valid cryptographic override, got %+v", checkPass)
	}
}

func TestIngestProjectDiagnostics_MinesAndClustersMessages(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "ingest_diag.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	proj, err := st.EnsureProject(tmp)
	if err != nil {
		t.Fatal(err)
	}

	// Insert messages across two sessions into SQLite store
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := st.DB.Exec(`
		INSERT INTO messages (project_id, content_hash, ts, role, content, source_tools, first_seen)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, proj.ID, "hash1", now, "tool", "main.go:15:4: undefined: CalculateFoo", "[]", now)
	if err != nil {
		t.Fatal(err)
	}
	msgID, _ := res.LastInsertId()

	_, err = st.DB.Exec(`
		INSERT INTO source_occurrences (message_id, tool, session_id, source_location, source_order, source_time)
		VALUES (?, ?, ?, ?, ?, ?)
	`, msgID, "bash", "sess_alpha", "terminal", 1, now)
	if err != nil {
		t.Fatal(err)
	}

	// Run diagnostic ingestion
	if err := ingestProjectDiagnostics(st, proj.ID, tmp); err != nil {
		t.Fatalf("ingestProjectDiagnostics failed: %v", err)
	}

	// Verify that the cluster was saved
	clusters, err := st.ListDiagnosticClusters(proj.ID)
	if err != nil {
		t.Fatalf("ListDiagnosticClusters failed: %v", err)
	}
	if len(clusters) == 0 {
		t.Fatal("expected at least 1 diagnostic cluster to be mined from messages")
	}

	found := false
	for _, c := range clusters {
		if c.Category == "compiler_error" && c.TargetFile == "main.go" {
			found = true
			if c.TargetSymbol != "CalculateFoo" {
				t.Errorf("expected TargetSymbol 'CalculateFoo', got %q", c.TargetSymbol)
			}
		}
	}
	if !found {
		t.Fatalf("expected compiler_error cluster for main.go, got %+v", clusters)
	}
}
