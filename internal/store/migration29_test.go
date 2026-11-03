package store

import (
	"path/filepath"
	"testing"
)

func TestMigration29_EpistemicGoalTables(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_m29.db")

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer s.Close()

	// Verify schema user_version is at least 29
	var uv int
	if err := s.DB.QueryRow("PRAGMA user_version").Scan(&uv); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if uv < 29 {
		t.Fatalf("user_version = %d, want at least 29", uv)
	}

	// Verify goal_isnads table exists and accepts records
	_, err = s.DB.Exec(`
		INSERT INTO goal_isnads (
			project_id, goal_slug, registered_by, registered_session, registered_at,
			spec_audited_by, state, reopen_epoch, updated_at
		) VALUES (
			'proj-1', 'goal-auth', '@antigravity', 'sess-123', '2026-10-03T12:00:00Z',
			'@spec-auditor', 'spec_audited', 0, '2026-10-03T12:00:00Z'
		)
	`)
	if err != nil {
		t.Fatalf("insert goal_isnads failed: %v", err)
	}

	// Verify goal_claims table exists
	_, err = s.DB.Exec(`
		INSERT INTO goal_claims (
			project_id, goal_slug, agent_id, session_id, pid, hostname,
			claimed_at, expires_at, heartbeat_at, lease_token
		) VALUES (
			'proj-1', 'goal-auth', '@agent', 'sess-123', 1234, 'host-1',
			'2026-10-03T12:00:00Z', '2026-10-03T12:05:00Z', '2026-10-03T12:00:00Z', 'token-abc'
		)
	`)
	if err != nil {
		t.Fatalf("insert goal_claims failed: %v", err)
	}

	// Verify goal_remediations table exists
	_, err = s.DB.Exec(`
		INSERT INTO goal_remediations (
			project_id, goal_slug, reopen_epoch, reopened_by, reason,
			tainted_goals, reopened_at
		) VALUES (
			'proj-1', 'goal-auth', 1, '@auditor', 'failing probe',
			'["downstream-1"]', '2026-10-03T12:00:00Z'
		)
	`)
	if err != nil {
		t.Fatalf("insert goal_remediations failed: %v", err)
	}

	// Verify goal_audit_receipts table exists
	_, err = s.DB.Exec(`
		INSERT INTO goal_audit_receipts (
			project_id, goal_slug, commit_sha, spec_hash, sandbox_nonce,
			blindfold_verified, canary_passed, ccdsi_score, req_coverage,
			probes_executed, probes_passed, certified_at
		) VALUES (
			'proj-1', 'goal-auth', 'abcdef1', 'hash-123', 'nonce-456',
			1, 1, 0.85, 0.90,
			10, 10, '2026-10-03T12:00:00Z'
		)
	`)
	if err != nil {
		t.Fatalf("insert goal_audit_receipts failed: %v", err)
	}

	// Verify PRAGMA foreign_keys is ON
	var fk int
	if err := s.DB.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatalf("read foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Errorf("PRAGMA foreign_keys = %d, want 1", fk)
	}
}
