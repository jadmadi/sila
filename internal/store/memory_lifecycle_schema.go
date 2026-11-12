package store

import (
	"context"
	"database/sql"
	"fmt"
)

func init() {
	migrations = append(migrations, migration{
		version: 43,
		name:    "memory_lifecycle",
		up: func(tx *sql.Tx) error {
			stmts := []string{
				`CREATE TABLE IF NOT EXISTS memory_lifecycle (
					event_id     TEXT PRIMARY KEY,
					memory_id    TEXT NOT NULL,
					project_id   TEXT NOT NULL,
					action       TEXT NOT NULL,
					from_state   TEXT NOT NULL,
					to_state     TEXT NOT NULL,
					authority    TEXT NOT NULL,
					reason       TEXT NOT NULL,
					note         TEXT,
					actor        TEXT NOT NULL,
					created_at   TEXT NOT NULL,
					prev_hash    TEXT NOT NULL DEFAULT '',
					event_hash   TEXT NOT NULL
				);`,
				`CREATE INDEX IF NOT EXISTS idx_memory_lifecycle_memory
					ON memory_lifecycle(memory_id, created_at);`,
				`CREATE INDEX IF NOT EXISTS idx_memory_lifecycle_project
					ON memory_lifecycle(project_id, action, created_at);`,
			}
			for _, stmt := range stmts {
				if _, err := tx.Exec(stmt); err != nil {
					return fmt.Errorf("migration 43: %w", err)
				}
			}
			return nil
		},
	})
}

// MemoryDiagnostics reports structural health without exposing content or paths.
func (s *Store) MemoryDiagnostics() (MemoryDiagnostics, error) {
	var d MemoryDiagnostics
	version, err := s.SchemaVersion()
	if err != nil {
		return d, err
	}
	d.SchemaVersion = version
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM memory_records`).Scan(&d.Records); err != nil {
		return d, err
	}
	counts := []struct {
		dest  *int
		query string
	}{
		{&d.OrphanedAssertions, `SELECT COUNT(*) FROM memory_assertions a WHERE NOT EXISTS(SELECT 1 FROM memory_records r WHERE r.id = a.memory_id)`},
		{&d.OrphanedEvidence, `SELECT COUNT(*) FROM memory_evidence e WHERE NOT EXISTS(SELECT 1 FROM memory_records r WHERE r.id = e.memory_id)`},
		{&d.OrphanedRelations, `SELECT COUNT(*) FROM memory_relations x WHERE NOT EXISTS(SELECT 1 FROM memory_records r WHERE r.id = x.memory_id)`},
		{&d.BrokenRelationTargets, `SELECT COUNT(*) FROM memory_relations x WHERE NOT EXISTS(SELECT 1 FROM memory_records r WHERE r.id = x.target_memory_id)`},
		{&d.FTSMissing, `SELECT COUNT(*) FROM memory_records r WHERE NOT EXISTS(SELECT 1 FROM memory_fts f WHERE f.memory_id = r.id)`},
	}
	for _, c := range counts {
		if err := s.DB.QueryRow(c.query).Scan(c.dest); err != nil {
			return d, err
		}
	}
	rows, err := s.DB.Query(`SELECT envelope_json FROM memory_records`)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			_ = rows.Close()
			return d, err
		}
		if _, err := DecodeMemoryEnvelope([]byte(raw)); err != nil {
			d.CorruptEnvelopes++
		}
	}
	if err := rows.Close(); err != nil {
		return d, err
	}
	d.FTSHealthy = d.FTSMissing == 0
	checks := []struct {
		dest  *int
		query string
	}{
		{&d.LifecycleEvents, `SELECT COUNT(*) FROM memory_lifecycle`},
		{&d.OrphanLifecycleEvents, `SELECT COUNT(*) FROM memory_lifecycle e WHERE NOT EXISTS(SELECT 1 FROM memory_records r WHERE r.id = e.memory_id)`},
		{&d.StateIndexMismatches, `SELECT COUNT(*) FROM memory_records r WHERE r.state IN ('archived','superseded')
			AND NOT EXISTS(SELECT 1 FROM memory_lifecycle e WHERE e.memory_id = r.id AND e.to_state = r.state)`},
	}
	for _, check := range checks {
		if err := s.DB.QueryRow(check.query).Scan(check.dest); err != nil {
			return d, err
		}
	}
	ids, err := s.memoryIDsWithLifecycle()
	if err != nil {
		return d, err
	}
	for _, id := range ids {
		events, err := s.lifecycleEvents(context.Background(), id)
		if err != nil {
			return d, err
		}
		verified, _ := verifyLifecycleChain(events)
		if !verified {
			d.BrokenLifecycleChains++
		}
	}
	return d, rows.Err()
}

func (s *Store) memoryIDsWithLifecycle() ([]string, error) {
	rows, err := s.DB.Query(`SELECT DISTINCT memory_id FROM memory_lifecycle ORDER BY memory_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func verifyLifecycleChain(events []MemoryLifecycleEvent) (bool, string) {
	prev := ""
	for i := range events {
		event := events[i]
		if event.PrevHash != prev || computeLifecycleEventHash(event) != event.EventHash {
			return false, event.EventID
		}
		prev = event.EventHash
	}
	return true, ""
}
