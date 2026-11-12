package store

import (
	"database/sql"
	"fmt"
)

func init() {
	migrations = append(migrations, migration{
		version: 44,
		name:    "memory_relevance_feedback",
		up: func(tx *sql.Tx) error {
			stmts := []string{
				`CREATE TABLE IF NOT EXISTS memory_retrievals (
					id          TEXT PRIMARY KEY,
					project_id  TEXT NOT NULL,
					agent       TEXT NOT NULL,
					session_ref TEXT,
					query_hash  TEXT NOT NULL,
					item_count  INTEGER NOT NULL,
					created_at  TEXT NOT NULL,
					expires_at  TEXT NOT NULL
				);`,
				`CREATE INDEX IF NOT EXISTS idx_memory_retrievals_project
					ON memory_retrievals(project_id, created_at);`,

				`CREATE TABLE IF NOT EXISTS memory_retrieval_items (
					retrieval_id TEXT NOT NULL,
					memory_id    TEXT NOT NULL,
					rank         INTEGER NOT NULL,
					kind         TEXT NOT NULL,
					score        REAL NOT NULL,
					PRIMARY KEY (retrieval_id, memory_id)
				);`,
				`CREATE INDEX IF NOT EXISTS idx_memory_retrieval_items_memory
					ON memory_retrieval_items(memory_id);`,

				`CREATE TABLE IF NOT EXISTS memory_feedback (
					id              TEXT PRIMARY KEY,
					project_id      TEXT NOT NULL,
					memory_id       TEXT NOT NULL,
					retrieval_id    TEXT,
					signal          TEXT NOT NULL,
					weight          REAL NOT NULL,
					unbound         INTEGER NOT NULL DEFAULT 0,
					note            TEXT,
					agent           TEXT NOT NULL,
					session_ref     TEXT,
					created_at      TEXT NOT NULL,
					expires_at      TEXT NOT NULL,
					superseded_by   TEXT,
					metadata_json   TEXT NOT NULL DEFAULT '{}'
				);`,
				`CREATE UNIQUE INDEX IF NOT EXISTS idx_memory_feedback_unique
					ON memory_feedback(retrieval_id, memory_id, agent)
					WHERE superseded_by IS NULL;`,
				`CREATE INDEX IF NOT EXISTS idx_memory_feedback_memory
					ON memory_feedback(memory_id, created_at);`,
			}
			for _, stmt := range stmts {
				if _, err := tx.Exec(stmt); err != nil {
					return fmt.Errorf("migration 44: %w", err)
				}
			}
			return nil
		},
	})
}
