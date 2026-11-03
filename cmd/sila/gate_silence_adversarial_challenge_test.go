package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

// TestGateSilenceSentryFailClosedAdversarialChallenge empirically stresses gateSilenceSentry
// against unopenable stores, corrupt files, permission denied files, dropped tables, query errors, and tampered overrides.
func TestGateSilenceSentryFailClosedAdversarialChallenge(t *testing.T) {
	t.Run("Nonexistent database path fails closed", func(t *testing.T) {
		tmp := t.TempDir()
		missingDB := filepath.Join(tmp, "subpath", "does_not_exist.db")
		t.Setenv("SILA_DB", missingDB)

		check := gateSilenceSentry(tmp)
		if check.Status != "fail" {
			t.Fatalf("expected fail status on nonexistent database, got %+v", check)
		}
		if !strings.Contains(check.Detail, "database open error") {
			t.Errorf("expected detail to mention 'database open error', got %q", check.Detail)
		}
	})

	t.Run("Corrupted SQLite file fails closed", func(t *testing.T) {
		tmp := t.TempDir()
		corruptDB := filepath.Join(tmp, "corrupt_header.db")
		if err := os.WriteFile(corruptDB, []byte("NOT_A_VALID_SQLITE_HEADER_CORRUPT"), 0644); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}
		t.Setenv("SILA_DB", corruptDB)

		check := gateSilenceSentry(tmp)
		if check.Status != "fail" {
			t.Fatalf("expected fail status on corrupt database file, got %+v", check)
		}
		if check.Detail == "" {
			t.Errorf("expected non-empty detail error on corrupt database file")
		}
	})

	t.Run("Unreadable database file permission denied fails closed", func(t *testing.T) {
		tmp := t.TempDir()
		lockedDB := filepath.Join(tmp, "locked.db")
		if err := os.WriteFile(lockedDB, []byte("dummy-data"), 0000); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}
		defer os.Chmod(lockedDB, 0644) // cleanup
		t.Setenv("SILA_DB", lockedDB)

		check := gateSilenceSentry(tmp)
		if check.Status != "fail" {
			t.Fatalf("expected fail status on unreadable database file, got %+v", check)
		}
		if !strings.Contains(check.Detail, "database open error") {
			t.Errorf("expected detail to mention 'database open error', got %q", check.Detail)
		}
	})

	t.Run("Query error on dropped diagnostic_clusters table fails closed", func(t *testing.T) {
		tmp := t.TempDir()
		dbPath := filepath.Join(tmp, "dropped_clusters.db")
		st, err := store.Open(dbPath)
		if err != nil {
			t.Fatalf("Open failed: %v", err)
		}
		if _, err := st.EnsureProject(tmp); err != nil {
			st.Close()
			t.Fatalf("EnsureProject failed: %v", err)
		}
		if _, err := st.DB.Exec("DROP TABLE diagnostic_clusters;"); err != nil {
			st.Close()
			t.Fatalf("DROP TABLE diagnostic_clusters failed: %v", err)
		}
		st.Close()

		t.Setenv("SILA_DB", dbPath)
		check := gateSilenceSentry(tmp)
		if check.Status != "fail" {
			t.Fatalf("expected fail status on dropped diagnostic_clusters table, got %+v", check)
		}
		if !strings.Contains(check.Detail, "query diagnostic clusters error") {
			t.Errorf("expected detail to mention 'query diagnostic clusters error', got %q", check.Detail)
		}
	})

	t.Run("Query error on dropped silence_overrides table fails closed", func(t *testing.T) {
		tmp := t.TempDir()
		dbPath := filepath.Join(tmp, "dropped_overrides.db")
		st, err := store.Open(dbPath)
		if err != nil {
			t.Fatalf("Open failed: %v", err)
		}
		proj, err := st.EnsureProject(tmp)
		if err != nil {
			st.Close()
			t.Fatalf("EnsureProject failed: %v", err)
		}

		cluster := store.StoredDiagnosticCluster{
			Signature:       "sig_dropped_overrides_test",
			ProjectID:       proj.ID,
			Category:        "compiler_error",
			Template:        "cannot find symbol",
			TargetFile:      "pkg/missing.go",
			TargetSymbol:    "Missing",
			OccurrenceCount: 10,
			FirstSeen:       time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339),
			LastSeen:        time.Now().UTC().Format(time.RFC3339),
		}
		if err := st.SaveDiagnosticCluster(cluster); err != nil {
			st.Close()
			t.Fatalf("SaveDiagnosticCluster failed: %v", err)
		}
		if _, err := st.DB.Exec("DROP TABLE silence_overrides;"); err != nil {
			st.Close()
			t.Fatalf("DROP TABLE silence_overrides failed: %v", err)
		}
		st.Close()

		t.Setenv("SILA_DB", dbPath)
		check := gateSilenceSentry(tmp)
		if check.Status != "fail" {
			t.Fatalf("expected fail status on dropped silence_overrides table, got %+v", check)
		}
		if !strings.Contains(check.Detail, "query silence override error") {
			t.Errorf("expected detail to mention 'query silence override error', got %q", check.Detail)
		}
	})

	t.Run("Query error on dropped diagnostic_session_frequencies table fails closed", func(t *testing.T) {
		tmp := t.TempDir()
		dbPath := filepath.Join(tmp, "dropped_frequencies.db")
		st, err := store.Open(dbPath)
		if err != nil {
			t.Fatalf("Open failed: %v", err)
		}
		proj, err := st.EnsureProject(tmp)
		if err != nil {
			st.Close()
			t.Fatalf("EnsureProject failed: %v", err)
		}

		cluster := store.StoredDiagnosticCluster{
			Signature:       "sig_dropped_frequencies_test",
			ProjectID:       proj.ID,
			Category:        "compiler_error",
			Template:        "cannot find symbol",
			TargetFile:      "pkg/missing.go",
			TargetSymbol:    "Missing",
			OccurrenceCount: 10,
			FirstSeen:       time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339),
			LastSeen:        time.Now().UTC().Format(time.RFC3339),
		}
		if err := st.SaveDiagnosticCluster(cluster); err != nil {
			st.Close()
			t.Fatalf("SaveDiagnosticCluster failed: %v", err)
		}
		if _, err := st.DB.Exec("DROP TABLE diagnostic_session_frequencies;"); err != nil {
			st.Close()
			t.Fatalf("DROP TABLE diagnostic_session_frequencies failed: %v", err)
		}
		st.Close()

		t.Setenv("SILA_DB", dbPath)
		check := gateSilenceSentry(tmp)
		if check.Status != "fail" {
			t.Fatalf("expected fail status on dropped frequencies table, got %+v", check)
		}
		if !strings.Contains(check.Detail, "query cluster frequency series error") {
			t.Errorf("expected detail to mention 'query cluster frequency series error', got %q", check.Detail)
		}
	})

	t.Run("Tampered override rejected during gate check causing gate to fail closed", func(t *testing.T) {
		tmp := t.TempDir()
		dbPath := filepath.Join(tmp, "tampered_gate.db")
		st, err := store.Open(dbPath)
		if err != nil {
			t.Fatalf("Open failed: %v", err)
		}
		proj, err := st.EnsureProject(tmp)
		if err != nil {
			st.Close()
			t.Fatalf("EnsureProject failed: %v", err)
		}

		sig := "sig_extinct_anomaly_tampered_override"
		cluster := store.StoredDiagnosticCluster{
			Signature:          sig,
			ProjectID:          proj.ID,
			Category:           "compiler_error",
			Template:           "undefined variable",
			TargetFile:         "cmd/orphan.go",
			TargetSymbol:       "OrphanSymbol",
			OccurrenceCount:    60,
			FirstSeen:          time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339),
			LastSeen:           time.Now().UTC().Format(time.RFC3339),
			SessionFrequencies: []float64{25, 25, 25, 0, 0, 0},
		}
		if err := st.SaveDiagnosticCluster(cluster); err != nil {
			st.Close()
			t.Fatalf("SaveDiagnosticCluster failed: %v", err)
		}

		// Record a tampered override (forged public key and bad signature)
		tamperedOv := store.SilenceOverride{
			Signature:      sig,
			ProjectID:      proj.ID,
			Reason:         "Malicious bypass attempt",
			Actor:          "attacker",
			SignatureProof: "ed25519:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=:BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB",
			CreatedAt:      time.Now().UTC().Format(time.RFC3339),
		}
		if err := st.RecordSilenceOverride(tamperedOv); err != nil {
			st.Close()
			t.Fatalf("RecordSilenceOverride failed: %v", err)
		}
		st.Close()

		t.Setenv("SILA_DB", dbPath)
		check := gateSilenceSentry(tmp)
		if check.Status != "fail" {
			t.Fatalf("expected fail status when override has tampered proof, got %+v", check)
		}
		if !strings.Contains(check.Detail, "detected 1 uncoupled silence anomaly") {
			t.Errorf("expected detail to identify anomaly, got %q", check.Detail)
		}
	})

	t.Run("Valid Ed25519 cryptographic override allows gate to pass", func(t *testing.T) {
		tmp := t.TempDir()
		dbPath := filepath.Join(tmp, "valid_gate.db")
		st, err := store.Open(dbPath)
		if err != nil {
			t.Fatalf("Open failed: %v", err)
		}
		proj, err := st.EnsureProject(tmp)
		if err != nil {
			st.Close()
			t.Fatalf("EnsureProject failed: %v", err)
		}

		sig := "sig_extinct_anomaly_valid_override"
		cluster := store.StoredDiagnosticCluster{
			Signature:          sig,
			ProjectID:          proj.ID,
			Category:           "compiler_error",
			Template:           "undefined variable",
			TargetFile:         "cmd/orphan.go",
			TargetSymbol:       "OrphanSymbol",
			OccurrenceCount:    60,
			FirstSeen:          time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339),
			LastSeen:           time.Now().UTC().Format(time.RFC3339),
			SessionFrequencies: []float64{25, 25, 25, 0, 0, 0},
		}
		if err := st.SaveDiagnosticCluster(cluster); err != nil {
			st.Close()
			t.Fatalf("SaveDiagnosticCluster failed: %v", err)
		}

		now := time.Now().UTC().Format(time.RFC3339)
		reason := "Approved code deprecation by maintainers"
		proof, err := store.GenerateEd25519OverrideProof(proj.ID, sig, reason, "maintainer", now, nil)
		if err != nil {
			st.Close()
			t.Fatalf("GenerateEd25519OverrideProof failed: %v", err)
		}

		validOv := store.SilenceOverride{
			Signature:      sig,
			ProjectID:      proj.ID,
			Reason:         reason,
			Actor:          "maintainer",
			SignatureProof: proof,
			CreatedAt:      now,
		}
		if err := st.RecordSilenceOverride(validOv); err != nil {
			st.Close()
			t.Fatalf("RecordSilenceOverride failed: %v", err)
		}
		st.Close()

		t.Setenv("SILA_DB", dbPath)
		check := gateSilenceSentry(tmp)
		if check.Status != "pass" {
			t.Fatalf("expected pass status with valid cryptographic override, got %+v", check)
		}
	})
}
