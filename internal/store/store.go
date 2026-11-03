package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/andon"
	"github.com/jadmadi/sila/internal/config"
	_ "modernc.org/sqlite"
)

type migration struct {
	version int
	name    string
	up      func(tx *sql.Tx) error
}

var migrations = []migration{
	{
		version: 1,
		name:    "core_tables_and_indexes",
		up: func(tx *sql.Tx) error {
			stmts := []string{
				`CREATE TABLE IF NOT EXISTS projects (
					path       TEXT PRIMARY KEY,
					id         TEXT NOT NULL,
					created_at TEXT NOT NULL
				);`,
				`CREATE UNIQUE INDEX IF NOT EXISTS idx_projects_id ON projects(id);`,
				`CREATE TABLE IF NOT EXISTS messages (
					id           INTEGER PRIMARY KEY,
					project_id   TEXT NOT NULL,
					content_hash TEXT NOT NULL,
					ts           TEXT NOT NULL,
					role         TEXT NOT NULL,
					content      TEXT NOT NULL,
					source_tools TEXT NOT NULL,
					first_seen   TEXT NOT NULL
				);`,
				`CREATE UNIQUE INDEX IF NOT EXISTS idx_messages_key ON messages(project_id, content_hash);`,
				`CREATE INDEX IF NOT EXISTS idx_messages_project ON messages(project_id);`,
				`CREATE INDEX IF NOT EXISTS idx_messages_near ON messages(project_id, role, ts);`,
				`CREATE TABLE IF NOT EXISTS state (
					key        TEXT PRIMARY KEY,
					value      TEXT NOT NULL,
					updated_at TEXT NOT NULL
				);`,
				`CREATE TABLE IF NOT EXISTS facts (
					id          TEXT PRIMARY KEY,
					project_id  TEXT NOT NULL,
					claim       TEXT NOT NULL,
					confidence  TEXT NOT NULL,
					verified    INTEGER NOT NULL DEFAULT 0,
					repo_files  TEXT NOT NULL,
					tools       TEXT NOT NULL,
					session_ref TEXT NOT NULL,
					first_seen  TEXT NOT NULL,
					promoted_at TEXT NOT NULL
				);`,
				`CREATE INDEX IF NOT EXISTS idx_facts_project ON facts(project_id);`,
				`CREATE TABLE IF NOT EXISTS overlaps (
					id          TEXT PRIMARY KEY,
					project_id  TEXT NOT NULL,
					msg_a       TEXT NOT NULL,
					msg_b       TEXT NOT NULL,
					tools_a     TEXT NOT NULL,
					tools_b     TEXT NOT NULL,
					similarity  REAL NOT NULL,
					first_seen  TEXT NOT NULL
				);`,
				`CREATE INDEX IF NOT EXISTS idx_overlaps_project ON overlaps(project_id);`,
			}
			for _, stmt := range stmts {
				if _, err := tx.Exec(stmt); err != nil {
					return err
				}
			}
			return nil
		},
	},
	{
		version: 2,
		name:    "fts5_virtual_tables_and_triggers",
		up: func(tx *sql.Tx) error {
			stmts := []string{
				`CREATE VIRTUAL TABLE IF NOT EXISTS facts_fts USING fts5(
					id UNINDEXED,
					project_id,
					claim,
					repo_files,
					tools,
					tokenize='unicode61'
				);`,
				`CREATE TRIGGER IF NOT EXISTS facts_ai AFTER INSERT ON facts BEGIN
					INSERT INTO facts_fts(id, project_id, claim, repo_files, tools)
					VALUES (new.id, new.project_id, new.claim, new.repo_files, new.tools);
				END;`,
				`CREATE TRIGGER IF NOT EXISTS facts_ad AFTER DELETE ON facts BEGIN
					DELETE FROM facts_fts WHERE id = old.id;
				END;`,
				`CREATE TRIGGER IF NOT EXISTS facts_au AFTER UPDATE ON facts BEGIN
					DELETE FROM facts_fts WHERE id = old.id;
					INSERT INTO facts_fts(id, project_id, claim, repo_files, tools)
					VALUES (new.id, new.project_id, new.claim, new.repo_files, new.tools);
				END;`,
				`CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
					project_id,
					role,
					content,
					tokenize='unicode61'
				);`,
				`CREATE TRIGGER IF NOT EXISTS messages_ai AFTER INSERT ON messages BEGIN
					INSERT INTO messages_fts(rowid, project_id, role, content)
					VALUES (new.id, new.project_id, new.role, new.content);
				END;`,
				`CREATE TRIGGER IF NOT EXISTS messages_ad AFTER DELETE ON messages BEGIN
					DELETE FROM messages_fts WHERE rowid = old.id;
				END;`,
				`INSERT INTO facts_fts(id, project_id, claim, repo_files, tools)
					SELECT id, project_id, claim, repo_files, tools FROM facts
					WHERE id NOT IN (SELECT id FROM facts_fts);`,
				`INSERT INTO messages_fts(rowid, project_id, role, content)
					SELECT id, project_id, role, content FROM messages
					WHERE id NOT IN (SELECT rowid FROM messages_fts);`,
			}
			for _, stmt := range stmts {
				if _, err := tx.Exec(stmt); err != nil {
					return err
				}
			}
			return nil
		},
	},
	{
		version: 3,
		name:    "file_refs_exist_column",
		up: func(tx *sql.Tx) error {
			var count int
			row := tx.QueryRow(`SELECT count(*) FROM pragma_table_info('facts') WHERE name='file_refs_exist'`)
			if err := row.Scan(&count); err != nil {
				return err
			}
			if count == 0 {
				if _, err := tx.Exec(`ALTER TABLE facts ADD COLUMN file_refs_exist INTEGER NOT NULL DEFAULT 0;`); err != nil {
					return err
				}
				if _, err := tx.Exec(`UPDATE facts SET file_refs_exist = verified;`); err != nil {
					return err
				}
			}
			return nil
		},
	},
	{
		version: 4,
		name:    "refs_checked_at_column",
		up: func(tx *sql.Tx) error {
			var count int
			row := tx.QueryRow(`SELECT count(*) FROM pragma_table_info('facts') WHERE name='refs_checked_at'`)
			if err := row.Scan(&count); err != nil {
				return err
			}
			if count == 0 {
				if _, err := tx.Exec(`ALTER TABLE facts ADD COLUMN refs_checked_at TEXT NOT NULL DEFAULT '';`); err != nil {
					return err
				}
				if _, err := tx.Exec(`UPDATE facts SET refs_checked_at = first_seen;`); err != nil {
					return err
				}
			}
			return nil
		},
	},
	{
		version: 5,
		name:    "distilled_and_status_columns",
		up: func(tx *sql.Tx) error {
			var count int
			row := tx.QueryRow(`SELECT count(*) FROM pragma_table_info('facts') WHERE name='distill_status'`)
			if err := row.Scan(&count); err != nil {
				return err
			}
			if count == 0 {
				if _, err := tx.Exec(`ALTER TABLE facts ADD COLUMN distilled TEXT NOT NULL DEFAULT '';`); err != nil {
					return err
				}
				if _, err := tx.Exec(`ALTER TABLE facts ADD COLUMN distill_status TEXT NOT NULL DEFAULT 'pending';`); err != nil {
					return err
				}
			}
			return nil
		},
	},
	{
		version: 6,
		name:    "handoffs_table",
		up: func(tx *sql.Tx) error {
			stmts := []string{
				`CREATE TABLE IF NOT EXISTS handoffs (
					id              TEXT PRIMARY KEY,
					project_id      TEXT NOT NULL,
					agent           TEXT NOT NULL,
					branch          TEXT NOT NULL DEFAULT '',
					summary         TEXT NOT NULL,
					open_items      TEXT NOT NULL DEFAULT '[]',
					decisions       TEXT NOT NULL DEFAULT '[]',
					repo_files      TEXT NOT NULL DEFAULT '[]',
					file_refs_exist INTEGER NOT NULL DEFAULT 1,
					created_at      TEXT NOT NULL,
					status          TEXT NOT NULL DEFAULT 'open'
				);`,
				`CREATE INDEX IF NOT EXISTS idx_handoffs_project ON handoffs(project_id);`,
				`CREATE INDEX IF NOT EXISTS idx_handoffs_created ON handoffs(created_at);`,
			}
			for _, stmt := range stmts {
				if _, err := tx.Exec(stmt); err != nil {
					return err
				}
			}
			return nil
		},
	},
	{
		version: 7,
		name:    "docs_and_sources_table",
		up: func(tx *sql.Tx) error {
			stmts := []string{
				`CREATE TABLE IF NOT EXISTS docs (
					id         TEXT PRIMARY KEY,
					project_id TEXT NOT NULL,
					title      TEXT NOT NULL,
					url        TEXT NOT NULL DEFAULT '',
					content    TEXT NOT NULL,
					format     TEXT NOT NULL DEFAULT 'markdown',
					created_at TEXT NOT NULL
				);`,
				`CREATE INDEX IF NOT EXISTS idx_docs_project ON docs(project_id);`,
				`CREATE INDEX IF NOT EXISTS idx_docs_created ON docs(created_at);`,
				`CREATE VIRTUAL TABLE IF NOT EXISTS docs_fts USING fts5(
					id UNINDEXED,
					project_id,
					title,
					content,
					tokenize='unicode61'
				);`,
				`CREATE TRIGGER IF NOT EXISTS docs_ai AFTER INSERT ON docs BEGIN
					INSERT INTO docs_fts(id, project_id, title, content)
					VALUES (new.id, new.project_id, new.title, new.content);
				END;`,
				`CREATE TRIGGER IF NOT EXISTS docs_ad AFTER DELETE ON docs BEGIN
					DELETE FROM docs_fts WHERE id = old.id;
				END;`,
				`CREATE TRIGGER IF NOT EXISTS docs_au AFTER UPDATE ON docs BEGIN
					DELETE FROM docs_fts WHERE id = old.id;
					INSERT INTO docs_fts(id, project_id, title, content)
					VALUES (new.id, new.project_id, new.title, new.content);
				END;`,
			}
			for _, stmt := range stmts {
				if _, err := tx.Exec(stmt); err != nil {
					return err
				}
			}
			return nil
		},
	},
	{
		version: 8,
		name:    "feedback_table",
		up: func(tx *sql.Tx) error {
			stmts := []string{
				`CREATE TABLE IF NOT EXISTS feedback (
					id         TEXT PRIMARY KEY,
					project_id TEXT NOT NULL DEFAULT '',
					agent      TEXT NOT NULL DEFAULT '',
					category   TEXT NOT NULL,
					content    TEXT NOT NULL,
					created_at TEXT NOT NULL
				);`,
				`CREATE INDEX IF NOT EXISTS idx_feedback_created ON feedback(created_at DESC);`,
				`CREATE INDEX IF NOT EXISTS idx_feedback_category ON feedback(category);`,
				`CREATE INDEX IF NOT EXISTS idx_feedback_project ON feedback(project_id);`,
			}
			for _, stmt := range stmts {
				if _, err := tx.Exec(stmt); err != nil {
					return err
				}
			}
			return nil
		},
	},
	{
		version: 9,
		name:    "feedback_status_and_resolution",
		up: func(tx *sql.Tx) error {
			var count int
			row := tx.QueryRow(`SELECT count(*) FROM pragma_table_info('feedback') WHERE name='status'`)
			if err := row.Scan(&count); err != nil {
				return err
			}
			if count == 0 {
				stmts := []string{
					`ALTER TABLE feedback ADD COLUMN status TEXT NOT NULL DEFAULT 'open';`,
					`ALTER TABLE feedback ADD COLUMN resolved_at TEXT NOT NULL DEFAULT '';`,
					`ALTER TABLE feedback ADD COLUMN resolved_by TEXT NOT NULL DEFAULT '';`,
					`ALTER TABLE feedback ADD COLUMN resolved_goal TEXT NOT NULL DEFAULT '';`,
					`ALTER TABLE feedback ADD COLUMN resolution_note TEXT NOT NULL DEFAULT '';`,
					`CREATE INDEX IF NOT EXISTS idx_feedback_status ON feedback(status);`,
				}
				for _, stmt := range stmts {
					if _, err := tx.Exec(stmt); err != nil {
						return err
					}
				}
			}
			return nil
		},
	},
	{
		version: 10,
		name:    "facts_kind_column",
		up: func(tx *sql.Tx) error {
			var count int
			row := tx.QueryRow(`SELECT count(*) FROM pragma_table_info('facts') WHERE name='kind'`)
			if err := row.Scan(&count); err != nil {
				return err
			}
			if count == 0 {
				stmts := []string{
					`ALTER TABLE facts ADD COLUMN kind TEXT NOT NULL DEFAULT '';`,
					`CREATE INDEX IF NOT EXISTS idx_facts_kind ON facts(kind);`,
				}
				for _, stmt := range stmts {
					if _, err := tx.Exec(stmt); err != nil {
						return err
					}
				}
			}
			return nil
		},
	},
	{
		version: 11,
		name:    "preferences_table",
		up: func(tx *sql.Tx) error {
			stmts := []string{
				`CREATE TABLE IF NOT EXISTS preferences (
					id            TEXT PRIMARY KEY,
					project_id    TEXT NOT NULL DEFAULT '',
					context_query TEXT NOT NULL DEFAULT '',
					candidate_a   TEXT NOT NULL,
					candidate_b   TEXT NOT NULL,
					winner        TEXT NOT NULL,
					rationale     TEXT NOT NULL DEFAULT '',
					agent         TEXT NOT NULL DEFAULT '',
					created_at    TEXT NOT NULL
				);`,
				`CREATE INDEX IF NOT EXISTS idx_preferences_project ON preferences(project_id);`,
				`CREATE INDEX IF NOT EXISTS idx_preferences_created ON preferences(created_at DESC);`,
			}
			for _, stmt := range stmts {
				if _, err := tx.Exec(stmt); err != nil {
					return err
				}
			}
			return nil
		},
	},
	{
		version: 12,
		name:    "facts_project_claim_invariant",
		up: func(tx *sql.Tx) error {
			stmts := []string{
				`DELETE FROM facts WHERE rowid NOT IN (
					SELECT min(rowid) FROM facts GROUP BY project_id, claim
				);`,
				`CREATE UNIQUE INDEX IF NOT EXISTS idx_facts_project_claim ON facts(project_id, claim);`,
			}
			for _, stmt := range stmts {
				if _, err := tx.Exec(stmt); err != nil {
					return err
				}
			}
			return nil
		},
	},
	{
		version: 13,
		name:    "source_occurrences_and_fact_authority",
		up: func(tx *sql.Tx) error {
			stmts := []string{
				// Every source occurrence of an ingested message: which tool,
				// session, and position it came from. Content dedup on messages
				// is preserved; occurrences carry the provenance instead.
				`CREATE TABLE IF NOT EXISTS source_occurrences (
					id              INTEGER PRIMARY KEY AUTOINCREMENT,
					message_id      INTEGER NOT NULL,
					tool            TEXT NOT NULL,
					session_id      TEXT NOT NULL DEFAULT '',
					source_location TEXT NOT NULL DEFAULT '',
					source_order    INTEGER NOT NULL DEFAULT 0,
					source_time     TEXT NOT NULL DEFAULT ''
				);`,
				`CREATE INDEX IF NOT EXISTS idx_occurrences_message ON source_occurrences(message_id);`,
				`CREATE INDEX IF NOT EXISTS idx_occurrences_session ON source_occurrences(session_id, source_order);`,
			}
			for _, stmt := range stmts {
				if _, err := tx.Exec(stmt); err != nil {
					return err
				}
			}
			// Authority lifecycle columns (guarded: re-entrant runs may hit a
			// schema that already carries them).
			addCol := func(table, col, ddl string) error {
				rows, err := tx.Query(`SELECT 1 FROM pragma_table_info(?) WHERE name = ?`, table, col)
				if err != nil {
					return err
				}
				exists := rows.Next()
				err = rows.Close()
				if err != nil || exists {
					return err
				}
				_, err = tx.Exec(ddl)
				return err
			}
			if err := addCol("facts", "review_status", `ALTER TABLE facts ADD COLUMN review_status TEXT NOT NULL DEFAULT 'candidate'`); err != nil {
				return err
			}
			if err := addCol("facts", "origin_role", `ALTER TABLE facts ADD COLUMN origin_role TEXT NOT NULL DEFAULT ''`); err != nil {
				return err
			}
			if err := addCol("facts", "quoted", `ALTER TABLE facts ADD COLUMN quoted INTEGER NOT NULL DEFAULT 0`); err != nil {
				return err
			}
			// Grandfather everything that is not an inferred directive:
			// explicit memos, vision objects, and ordinary grounded claims.
			if _, err := tx.Exec(`UPDATE facts SET review_status = 'approved'
					WHERE session_ref = 'manual_memo'
					   OR kind IN ('nongoal', 'milestone')
					   OR (kind != 'invariant' AND confidence != 'invariant')`); err != nil {
				return err
			}
			// Demote inferred invariants to candidates: directive-looking
			// transcript text is an attributed observation, not an
			// authorized rule.
			_, err := tx.Exec(`UPDATE facts SET review_status = 'candidate', verified = 0, file_refs_exist = 0
					WHERE session_ref != 'manual_memo'
					  AND kind NOT IN ('nongoal', 'milestone')
					  AND (
					    kind = 'invariant' OR confidence = 'invariant'
					    OR (repo_files IN ('', '[]') AND (
					         lower(claim) LIKE 'decision:%'
					      OR lower(claim) LIKE 'architecture:%'
					      OR lower(claim) LIKE 'arch:%'
					      OR lower(claim) LIKE 'rule:%'
					      OR lower(claim) LIKE 'policy:%'
					      OR lower(claim) LIKE 'guideline:%'
					      OR lower(claim) LIKE 'guidance:%'
					      OR lower(claim) LIKE 'constraint:%'
					      OR lower(claim) LIKE 'invariant:%'
					      OR lower(claim) LIKE 'tradeoff:%'
					      OR lower(claim) LIKE 'memo:%'
					      OR lower(claim) LIKE 'convention:%'
					    ))
					  )`)
			return err
		},
	},
	{
		version: 14,
		name:    "unwrap_agy_user_envelopes",
		up:      repairAGYUserEnvelopes,
	},
	{
		version: 15,
		name:    "delete_zcode_runtime_turns",
		up:      repairZCodeRuntimeTurns,
	},
	{
		version: 16,
		name:    "delete_mimocode_runtime_turns",
		up: func(tx *sql.Tx) error {
			return repairMiMoCodeRuntimeTurns(tx, miMoCodeChildSessions, miMoCodeSyntheticMarkers)
		},
	},
	{
		version: 17,
		name:    "strip_agy_assistant_envelopes",
		up:      repairAGYAssistantEnvelopes,
	},
	{
		version: 18,
		name:    "canonicalize_worktree_projects",
		up:      repairWorktreeProjects,
	},
	{
		version: 19,
		name:    "facts_hash_chain",
		up: func(tx *sql.Tx) error {
			addCol := func(table, col, ddl string) error {
				rows, err := tx.Query(`SELECT 1 FROM pragma_table_info(?) WHERE name = ?`, table, col)
				if err != nil {
					return err
				}
				exists := rows.Next()
				err = rows.Close()
				if err != nil || exists {
					return err
				}
				_, err = tx.Exec(ddl)
				return err
			}
			if err := addCol("facts", "fact_hash", `ALTER TABLE facts ADD COLUMN fact_hash TEXT NOT NULL DEFAULT ''`); err != nil {
				return err
			}
			if err := addCol("facts", "prev_hash", `ALTER TABLE facts ADD COLUMN prev_hash TEXT NOT NULL DEFAULT ''`); err != nil {
				return err
			}
			if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_facts_fact_hash ON facts(fact_hash);`); err != nil {
				return err
			}

			// Backfill existing legacy facts without hashes in deterministic rowid order
			rows, err := tx.Query(`SELECT id, project_id, claim, kind, session_ref, first_seen, origin_role, fact_hash, prev_hash FROM facts ORDER BY rowid ASC`)
			if err != nil {
				return err
			}
			type legacyFact struct {
				id, projectID, claim, kind, sessionRef, firstSeen, originRole, factHash, prevHash string
			}
			var list []legacyFact
			for rows.Next() {
				var lf legacyFact
				if err := rows.Scan(&lf.id, &lf.projectID, &lf.claim, &lf.kind, &lf.sessionRef, &lf.firstSeen, &lf.originRole, &lf.factHash, &lf.prevHash); err != nil {
					_ = rows.Close()
					return err
				}
				list = append(list, lf)
			}
			if err := rows.Close(); err != nil {
				return err
			}

			prev := GenesisHash
			for _, lf := range list {
				if lf.factHash != "" && lf.prevHash != "" {
					prev = lf.factHash
					continue
				}
				f := Fact{
					ID:         lf.id,
					ProjectID:  lf.projectID,
					Claim:      lf.claim,
					Kind:       lf.kind,
					SessionRef: lf.sessionRef,
					FirstSeen:  lf.firstSeen,
					OriginRole: lf.originRole,
				}
				hash := ComputeFactHash(f, prev)
				if _, err := tx.Exec(`UPDATE facts SET fact_hash = ?, prev_hash = ? WHERE id = ?`, hash, prev, lf.id); err != nil {
					return err
				}
				prev = hash
			}

			return nil
		},
	},
	{
		version: 20,
		name:    "approvals_table",
		up: func(tx *sql.Tx) error {
			stmts := []string{
				`CREATE TABLE IF NOT EXISTS approvals (
					id              TEXT PRIMARY KEY,
					project         TEXT NOT NULL DEFAULT '',
					agent           TEXT NOT NULL DEFAULT '',
					prompt          TEXT NOT NULL,
					risk_level      TEXT NOT NULL DEFAULT 'medium',
					metadata        TEXT NOT NULL DEFAULT '{}',
					timeout_seconds INTEGER NOT NULL DEFAULT 300,
					status          TEXT NOT NULL DEFAULT 'pending',
					decided_by      TEXT NOT NULL DEFAULT '',
					decision_reason TEXT NOT NULL DEFAULT '',
					created_at      TEXT NOT NULL,
					decided_at      TEXT NOT NULL DEFAULT ''
				);`,
				`CREATE INDEX IF NOT EXISTS idx_approvals_project ON approvals(project);`,
				`CREATE INDEX IF NOT EXISTS idx_approvals_status ON approvals(status);`,
			}
			for _, stmt := range stmts {
				if _, err := tx.Exec(stmt); err != nil {
					return err
				}
			}
			return nil
		},
	},
	{
		version: 21,
		name:    "fact_incidents_table",
		up: func(tx *sql.Tx) error {
			stmts := []string{
				`CREATE TABLE IF NOT EXISTS fact_incidents (
					id                 TEXT PRIMARY KEY,
					fact_id            TEXT NOT NULL,
					incident_type      TEXT NOT NULL,
					stored_hash        TEXT NOT NULL,
					computed_hash      TEXT NOT NULL,
					stored_prev_hash   TEXT NOT NULL,
					expected_prev_hash TEXT NOT NULL,
					claim              TEXT NOT NULL,
					details            TEXT NOT NULL DEFAULT '',
					created_at         TEXT NOT NULL
				);`,
				`CREATE INDEX IF NOT EXISTS idx_fact_incidents_fact_id ON fact_incidents(fact_id);`,
				`CREATE INDEX IF NOT EXISTS idx_fact_incidents_created_at ON fact_incidents(created_at);`,
			}
			for _, stmt := range stmts {
				if _, err := tx.Exec(stmt); err != nil {
					return err
				}
			}
			return nil
		},
	},
	{
		version: 22,
		name:    "local_personas_table",
		up: func(tx *sql.Tx) error {
			stmts := []string{
				`CREATE TABLE IF NOT EXISTS local_personas (
					id                 TEXT PRIMARY KEY,
					name               TEXT NOT NULL,
					domain             TEXT NOT NULL,
					tags               TEXT NOT NULL DEFAULT '[]',
					system_prompt      TEXT NOT NULL,
					skills_invoked     TEXT NOT NULL DEFAULT '[]',
					evaluation_rubric  TEXT NOT NULL,
					negative_lessons   TEXT NOT NULL DEFAULT '[]',
					usage_count        INTEGER NOT NULL DEFAULT 0,
					approval_rate      TEXT NOT NULL DEFAULT '100%',
					created_at         TEXT NOT NULL,
					updated_at         TEXT NOT NULL
				);`,
				`CREATE INDEX IF NOT EXISTS idx_personas_domain ON local_personas(domain);`,
			}
			for _, stmt := range stmts {
				if _, err := tx.Exec(stmt); err != nil {
					return err
				}
			}
			return nil
		},
	},
	{
		version: 23,
		name:    "handoff_claim_columns",
		up: func(tx *sql.Tx) error {
			var tblCount int
			if err := tx.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='handoffs'`).Scan(&tblCount); err != nil {
				return err
			}
			if tblCount == 0 {
				return nil
			}
			addCol := func(table, col, ddl string) error {
				var count int
				row := tx.QueryRow(fmt.Sprintf("SELECT count(*) FROM pragma_table_info('%s') WHERE name='%s'", table, col))
				if err := row.Scan(&count); err != nil {
					return err
				}
				if count > 0 {
					return nil
				}
				_, err := tx.Exec(ddl)
				return err
			}
			if err := addCol("handoffs", "claimed_by", `ALTER TABLE handoffs ADD COLUMN claimed_by TEXT NOT NULL DEFAULT ''`); err != nil {
				return err
			}
			if err := addCol("handoffs", "claimed_at", `ALTER TABLE handoffs ADD COLUMN claimed_at TEXT NOT NULL DEFAULT ''`); err != nil {
				return err
			}
			if err := addCol("handoffs", "session_id", `ALTER TABLE handoffs ADD COLUMN session_id TEXT NOT NULL DEFAULT ''`); err != nil {
				return err
			}
			if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_handoffs_status ON handoffs(status);`); err != nil {
				return err
			}
			if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_handoffs_session ON handoffs(session_id);`); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE handoffs SET status='pending' WHERE status='open';`); err != nil {
				return err
			}
			return nil
		},
	},
	{
		version: 24,
		name:    "facts_compaction_columns",
		up: func(tx *sql.Tx) error {
			addCol := func(table, col, ddl string) error {
				var count int
				row := tx.QueryRow(fmt.Sprintf("SELECT count(*) FROM pragma_table_info('%s') WHERE name='%s'", table, col))
				if err := row.Scan(&count); err != nil {
					return err
				}
				if count > 0 {
					return nil
				}
				_, err := tx.Exec(ddl)
				return err
			}
			if err := addCol("facts", "compacted_at", `ALTER TABLE facts ADD COLUMN compacted_at TEXT NOT NULL DEFAULT ''`); err != nil {
				return err
			}
			if err := addCol("facts", "supersedes_id", `ALTER TABLE facts ADD COLUMN supersedes_id TEXT NOT NULL DEFAULT ''`); err != nil {
				return err
			}
			if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_facts_compacted_at ON facts(compacted_at);`); err != nil {
				return err
			}
			return nil
		},
	},
	{
		version: 25,
		name:    "access_weighted_retention_decay",
		up: func(tx *sql.Tx) error {
			addCol := func(table, col, ddl string) error {
				var count int
				row := tx.QueryRow(fmt.Sprintf("SELECT count(*) FROM pragma_table_info('%s') WHERE name='%s'", table, col))
				if err := row.Scan(&count); err != nil {
					return err
				}
				if count > 0 {
					return nil
				}
				_, err := tx.Exec(ddl)
				return err
			}
			if err := addCol("facts", "access_count", `ALTER TABLE facts ADD COLUMN access_count INTEGER NOT NULL DEFAULT 0`); err != nil {
				return err
			}
			if err := addCol("facts", "last_accessed_at", `ALTER TABLE facts ADD COLUMN last_accessed_at TEXT NOT NULL DEFAULT ''`); err != nil {
				return err
			}
			if err := addCol("facts", "distinct_tools", `ALTER TABLE facts ADD COLUMN distinct_tools INTEGER NOT NULL DEFAULT 1`); err != nil {
				return err
			}
			if err := addCol("facts", "tier", `ALTER TABLE facts ADD COLUMN tier TEXT NOT NULL DEFAULT 'episodic'`); err != nil {
				return err
			}
			if err := addCol("facts", "salience", `ALTER TABLE facts ADD COLUMN salience REAL NOT NULL DEFAULT 1.0`); err != nil {
				return err
			}
			if err := addCol("facts", "is_stale", `ALTER TABLE facts ADD COLUMN is_stale INTEGER NOT NULL DEFAULT 0`); err != nil {
				return err
			}
			if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_facts_tier ON facts(tier);`); err != nil {
				return err
			}
			if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_facts_last_accessed ON facts(last_accessed_at);`); err != nil {
				return err
			}
			if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS fact_tool_access (
				fact_id TEXT NOT NULL,
				tool TEXT NOT NULL,
				last_accessed_at TEXT NOT NULL,
				access_count INTEGER NOT NULL DEFAULT 1,
				PRIMARY KEY (fact_id, tool)
			);`); err != nil {
				return err
			}
			if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_fact_tool_access_fact ON fact_tool_access(fact_id);`); err != nil {
				return err
			}
			if _, err := tx.Exec(`DROP TRIGGER IF EXISTS facts_au;`); err != nil {
				return err
			}
			if _, err := tx.Exec(`CREATE TRIGGER IF NOT EXISTS facts_au AFTER UPDATE OF project_id, claim, repo_files, tools ON facts BEGIN
				DELETE FROM facts_fts WHERE id = old.id;
				INSERT INTO facts_fts(id, project_id, claim, repo_files, tools)
				VALUES (new.id, new.project_id, new.claim, new.repo_files, new.tools);
			END;`); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE facts SET tier = 'semantic' WHERE kind = 'invariant' OR session_ref = 'manual_memo';`); err != nil {
				return err
			}
			return nil
		},
	},
	{
		version: 26,
		name:    "byte_bounded_literal_recovery",
		up: func(tx *sql.Tx) error {
			stmts := []string{
				`CREATE TABLE IF NOT EXISTS text_chunks (
					id          TEXT PRIMARY KEY,
					project_id  TEXT NOT NULL,
					source_type TEXT NOT NULL,
					source_id   TEXT NOT NULL,
					byte_start  INTEGER NOT NULL,
					byte_end    INTEGER NOT NULL,
					content     TEXT NOT NULL,
					hash        TEXT NOT NULL,
					created_at  TEXT NOT NULL
				);`,
				`CREATE INDEX IF NOT EXISTS idx_text_chunks_source ON text_chunks(source_type, source_id);`,
				`CREATE INDEX IF NOT EXISTS idx_text_chunks_project ON text_chunks(project_id);`,
				`CREATE INDEX IF NOT EXISTS idx_text_chunks_hash ON text_chunks(hash);`,
			}
			for _, stmt := range stmts {
				if _, err := tx.Exec(stmt); err != nil {
					return err
				}
			}
			addCol := func(table, col, ddl string) error {
				var count int
				row := tx.QueryRow(fmt.Sprintf("SELECT count(*) FROM pragma_table_info('%s') WHERE name='%s'", table, col))
				if err := row.Scan(&count); err != nil {
					return err
				}
				if count > 0 {
					return nil
				}
				_, err := tx.Exec(ddl)
				return err
			}
			if err := addCol("facts", "byte_start", `ALTER TABLE facts ADD COLUMN byte_start INTEGER NOT NULL DEFAULT -1`); err != nil {
				return err
			}
			if err := addCol("facts", "byte_end", `ALTER TABLE facts ADD COLUMN byte_end INTEGER NOT NULL DEFAULT -1`); err != nil {
				return err
			}
			if err := addCol("facts", "source_ref", `ALTER TABLE facts ADD COLUMN source_ref TEXT NOT NULL DEFAULT ''`); err != nil {
				return err
			}
			return nil
		},
	},
	{
		version: 27,
		name:    "knowledge_assertion_lineage",
		up: func(tx *sql.Tx) error {
			stmts := []string{
				`CREATE TABLE IF NOT EXISTS assertions (
					id             TEXT PRIMARY KEY,
					project_id     TEXT NOT NULL,
					statement_id   TEXT NOT NULL,
					statement_type TEXT NOT NULL,
					evidence_type  TEXT NOT NULL,
					evidence_id    TEXT NOT NULL,
					evidence_ref   TEXT NOT NULL DEFAULT '',
					confidence     TEXT NOT NULL DEFAULT 'grounded',
					verified       INTEGER NOT NULL DEFAULT 0,
					verified_at    TEXT NOT NULL DEFAULT '',
					created_at     TEXT NOT NULL
				);`,
				`CREATE INDEX IF NOT EXISTS idx_assertions_statement ON assertions(statement_id);`,
				`CREATE INDEX IF NOT EXISTS idx_assertions_evidence ON assertions(evidence_type, evidence_id);`,
				`CREATE INDEX IF NOT EXISTS idx_assertions_project ON assertions(project_id);`,
				`CREATE INDEX IF NOT EXISTS idx_assertions_verified ON assertions(verified);`,
			}
			for _, stmt := range stmts {
				if _, err := tx.Exec(stmt); err != nil {
					return err
				}
			}
			return nil
		},
	},
	{
		version: 28,
		name:    "assertion_verification_reasons",
		up: func(tx *sql.Tx) error {
			var exists int
			if err := tx.QueryRow(`SELECT count(*) FROM pragma_table_info('assertions') WHERE name='verification_error'`).Scan(&exists); err != nil {
				return err
			}
			if exists == 0 {
				if _, err := tx.Exec(`ALTER TABLE assertions ADD COLUMN verification_error TEXT NOT NULL DEFAULT ''`); err != nil {
					return err
				}
			}
			// Legacy flags were assigned from reference strings. Retain the lineage
			// and require real verification before granting evidence status again.
			_, err := tx.Exec(`UPDATE assertions SET verified=0, verified_at='', verification_error='not_reverified'`)
			return err
		},
	},
	{
		version: 29,
		name:    "epistemic_goal_isnad",
		up: func(tx *sql.Tx) error {
			stmts := []string{
				`CREATE TABLE IF NOT EXISTS goal_isnads (
					project_id              TEXT NOT NULL,
					goal_slug               TEXT NOT NULL,
					registered_by           TEXT NOT NULL,
					registered_session      TEXT NOT NULL DEFAULT '',
					registered_at           TEXT NOT NULL,
					spec_audited_by         TEXT NOT NULL DEFAULT '',
					spec_audited_session    TEXT NOT NULL DEFAULT '',
					spec_audited_at         TEXT NOT NULL DEFAULT '',
					implemented_by          TEXT NOT NULL DEFAULT '',
					implemented_session     TEXT NOT NULL DEFAULT '',
					implemented_commit      TEXT NOT NULL DEFAULT '',
					mystery_audited_by      TEXT NOT NULL DEFAULT '',
					mystery_audited_session TEXT NOT NULL DEFAULT '',
					mystery_audited_report  TEXT NOT NULL DEFAULT '',
					state                   TEXT NOT NULL DEFAULT 'draft',
					reopen_epoch            INTEGER NOT NULL DEFAULT 0,
					updated_at              TEXT NOT NULL,
					PRIMARY KEY (project_id, goal_slug)
				);`,
				`CREATE INDEX IF NOT EXISTS idx_goal_isnads_state ON goal_isnads(project_id, state);`,
				`CREATE TABLE IF NOT EXISTS goal_claims (
					id            INTEGER PRIMARY KEY AUTOINCREMENT,
					project_id    TEXT NOT NULL,
					goal_slug     TEXT NOT NULL,
					agent_id      TEXT NOT NULL,
					session_id    TEXT NOT NULL,
					pid           INTEGER NOT NULL,
					hostname      TEXT NOT NULL,
					claimed_at    TEXT NOT NULL,
					expires_at    TEXT NOT NULL,
					heartbeat_at  TEXT NOT NULL,
					phase         TEXT NOT NULL DEFAULT 'implementation',
					lease_token   TEXT NOT NULL,
					version       INTEGER NOT NULL DEFAULT 1,
					active        INTEGER NOT NULL DEFAULT 1
				);`,
				`CREATE INDEX IF NOT EXISTS idx_goal_claims_active ON goal_claims(project_id, goal_slug, active);`,
				`CREATE TABLE IF NOT EXISTS goal_remediations (
					id                INTEGER PRIMARY KEY AUTOINCREMENT,
					project_id        TEXT NOT NULL,
					goal_slug         TEXT NOT NULL,
					reopen_epoch      INTEGER NOT NULL,
					reopened_by       TEXT NOT NULL,
					reopened_session  TEXT NOT NULL DEFAULT '',
					reason            TEXT NOT NULL,
					original_commit   TEXT NOT NULL DEFAULT '',
					resolved_commit   TEXT NOT NULL DEFAULT '',
					tainted_goals     TEXT NOT NULL DEFAULT '[]',
					reopened_at       TEXT NOT NULL,
					resolved_at       TEXT NOT NULL DEFAULT '',
					active            INTEGER NOT NULL DEFAULT 1
				);`,
				`CREATE INDEX IF NOT EXISTS idx_goal_remediations_slug ON goal_remediations(project_id, goal_slug, active);`,
				`CREATE TABLE IF NOT EXISTS goal_audit_receipts (
					id                  INTEGER PRIMARY KEY AUTOINCREMENT,
					project_id          TEXT NOT NULL,
					goal_slug           TEXT NOT NULL,
					commit_sha          TEXT NOT NULL,
					spec_hash           TEXT NOT NULL,
					sandbox_nonce       TEXT NOT NULL,
					candidate_selection TEXT NOT NULL DEFAULT '',
					blindfold_verified  INTEGER NOT NULL DEFAULT 0,
					canary_passed       INTEGER NOT NULL DEFAULT 0,
					ccdsi_score         REAL NOT NULL DEFAULT 0.0,
					req_coverage        REAL NOT NULL DEFAULT 0.0,
					probes_executed     INTEGER NOT NULL DEFAULT 0,
					probes_passed       INTEGER NOT NULL DEFAULT 0,
					probe_manifest_hash TEXT NOT NULL DEFAULT '',
					auditor_pub_key     TEXT NOT NULL DEFAULT '',
					auditor_signature   TEXT NOT NULL DEFAULT '',
					certified_at        TEXT NOT NULL
				);`,
				`CREATE INDEX IF NOT EXISTS idx_goal_audit_receipts_slug ON goal_audit_receipts(project_id, goal_slug);`,
			}
			for _, stmt := range stmts {
				if _, err := tx.Exec(stmt); err != nil {
					return err
				}
			}
			return nil
		},
	},
	{
		version: 30,
		name:    "epistemic_addressed_decisions",
		up: func(tx *sql.Tx) error {
			stmts := []string{
				`CREATE TABLE IF NOT EXISTS decisions (
					id                TEXT PRIMARY KEY,
					project_id        TEXT NOT NULL,
					address           TEXT NOT NULL,
					seq_number        INTEGER NOT NULL,
					title             TEXT NOT NULL,
					context           TEXT NOT NULL DEFAULT '',
					options           TEXT NOT NULL,
					recommendation    TEXT NOT NULL,
					rationale         TEXT NOT NULL DEFAULT '',
					status            TEXT NOT NULL DEFAULT 'open',
					resolution        TEXT NOT NULL DEFAULT '',
					resolution_notes  TEXT NOT NULL DEFAULT '',
					resolved_by       TEXT NOT NULL DEFAULT '',
					resolved_at       TEXT NOT NULL DEFAULT '',
					created_by        TEXT NOT NULL DEFAULT '',
					session_id        TEXT NOT NULL DEFAULT '',
					goal_slug         TEXT NOT NULL DEFAULT '',
					created_at        TEXT NOT NULL,
					updated_at        TEXT NOT NULL
				);`,
				`CREATE UNIQUE INDEX IF NOT EXISTS idx_decisions_project_address ON decisions(project_id, address);`,
				`CREATE UNIQUE INDEX IF NOT EXISTS idx_decisions_project_seq ON decisions(project_id, seq_number);`,
				`CREATE INDEX IF NOT EXISTS idx_decisions_project_status ON decisions(project_id, status);`,
				`CREATE INDEX IF NOT EXISTS idx_decisions_project_goal ON decisions(project_id, goal_slug);`,
			}
			for _, stmt := range stmts {
				if _, err := tx.Exec(stmt); err != nil {
					return err
				}
			}
			for _, colStmt := range []struct {
				table string
				col   string
				stmt  string
			}{
				{"preferences", "decision_address", "ALTER TABLE preferences ADD COLUMN decision_address TEXT NOT NULL DEFAULT ''"},
				{"facts", "decision_address", "ALTER TABLE facts ADD COLUMN decision_address TEXT NOT NULL DEFAULT ''"},
			} {
				var count int
				row := tx.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM pragma_table_info('%s') WHERE name='%s'", colStmt.table, colStmt.col))
				if err := row.Scan(&count); err == nil && count == 0 {
					if _, err := tx.Exec(colStmt.stmt); err != nil {
						return err
					}
				}
			}
			return nil
		},
	},
	{
		version: 31,
		name:    "source_catalog_provenance",
		up: func(tx *sql.Tx) error {
			stmts := []string{
				`CREATE TABLE IF NOT EXISTS source_catalog (
					id                TEXT PRIMARY KEY,
					url               TEXT NOT NULL UNIQUE,
					title             TEXT NOT NULL,
					publisher         TEXT NOT NULL,
					license           TEXT NOT NULL,
					jurisdiction      TEXT NOT NULL DEFAULT '',
					authoritative_for TEXT NOT NULL DEFAULT '[]',
					quotable          INTEGER NOT NULL DEFAULT 0,
					verified_at       TEXT NOT NULL,
					created_at        TEXT NOT NULL
				);`,
				`CREATE INDEX IF NOT EXISTS idx_source_catalog_license ON source_catalog(license);`,
			}
			for _, stmt := range stmts {
				if _, err := tx.Exec(stmt); err != nil {
					return err
				}
			}
			return nil
		},
	},
}

// ContentHash is the dedup key over role and content, matching the
// messages(project_id, content_hash) uniqueness rule. It lives beside that
// schema rule because ingest and migrations must derive the same key.
func ContentHash(role, content string) string {
	h := sha256.Sum256([]byte(role + "\x00" + content))
	return hex.EncodeToString(h[:])
}

func migrate(db *sql.DB) error {
	var currentVersion int
	err := db.QueryRow("PRAGMA user_version").Scan(&currentVersion)
	if err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}

	// Refuse databases written by a newer sila: opening without applying the
	// newer schema's migrations would misread or corrupt its data.
	if currentVersion > len(migrations) {
		return fmt.Errorf("knowledge store schema v%d is newer than this sila build supports (v%d); upgrade sila or restore a backup made with this version", currentVersion, len(migrations))
	}

	skipSmoke := os.Getenv("SILA_SKIP_SMOKE_AB") == "1"

	for _, m := range migrations {
		if m.version <= currentVersion {
			continue
		}

		if skipSmoke {
			tx, err := db.Begin()
			if err != nil {
				return fmt.Errorf("begin migration %d (%s): %w", m.version, m.name, err)
			}
			if err := m.up(tx); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("execute migration %d (%s): %w", m.version, m.name, err)
			}
			if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", m.version)); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("set user_version to %d: %w", m.version, err)
			}
			if err := tx.Commit(); err != nil {
				return fmt.Errorf("commit migration %d (%s): %w", m.version, m.name, err)
			}
			continue
		}

		opName := fmt.Sprintf("store:migrate:v%d(%s)", m.version, m.name)
		sandbox := andon.NewSmokeSandbox(db, opName).WithInvariants(
			andon.InvariantQuickCheck,
		)

		err := sandbox.Execute(func(tx *sql.Tx) error {
			if err := m.up(tx); err != nil {
				return err
			}
			if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", m.version)); err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("migration %d (%s) failed smoke A/B validation: %w", m.version, m.name, err)
		}
	}
	return nil
}

// VerifySmokeAB executes candidate database mutations within an isolated dry-run sandbox
// and commits only after post-mutation structural invariants are confirmed intact.
func (s *Store) VerifySmokeAB(operation string, candidate func(tx *sql.Tx) error, invariants ...andon.InvariantFunc) error {
	sandbox := andon.NewSmokeSandbox(s.DB, operation)
	if len(invariants) > 0 {
		sandbox.WithInvariants(invariants...)
	}
	return sandbox.Execute(candidate)
}

// QuickCheck verifies database integrity using SQLite PRAGMA quick_check.
// If an FTS5 virtual table corruption is detected, it automatically attempts an online rebuild.
func (s *Store) QuickCheck() error {
	var result string
	if err := s.DB.QueryRow("PRAGMA quick_check(1)").Scan(&result); err != nil {
		return fmt.Errorf("quick_check failed: %w", err)
	}
	if strings.EqualFold(result, "ok") {
		return nil
	}
	if s.ReadOnly {
		return fmt.Errorf("quick_check reported corruption: %s", result)
	}

	lowerRes := strings.ToLower(result)
	if strings.Contains(lowerRes, "fts5") || strings.Contains(lowerRes, "corruption") || strings.Contains(lowerRes, "malformed") {
		for _, tbl := range []string{"facts_fts", "messages_fts", "docs_fts"} {
			if strings.Contains(lowerRes, tbl) || strings.Contains(lowerRes, "fts5") {
				_ = s.RebuildFTSIndex(tbl)
			}
		}
		var checkAfter string
		if err := s.DB.QueryRow("PRAGMA quick_check(1)").Scan(&checkAfter); err == nil && strings.EqualFold(checkAfter, "ok") {
			return nil
		}
	}

	return fmt.Errorf("quick_check reported corruption: %s", result)
}

// Project is a resolved project row.
type Project struct {
	Path string
	ID   string
}

// SortableTime formats an instant so that lexicographic order matches
// chronological order. RFC3339Nano trims trailing zeros, which makes plain
// text comparison wrong ('...48Z' sorts after '...48.5Z'); a fixed-width
// nanosecond field keeps source_occurrences ordered by (source_time, id).
func SortableTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000000000Z")
}

// LoadMessage is a message queued for insertion by the ingest orchestrator.
type LoadMessage struct {
	ProjectID   string
	ContentHash string
	TS          string // RFC3339 UTC
	Role        string
	Content     string
	Tool        string
	// Source provenance recorded in source_occurrences; zero values are
	// allowed when a loader cannot recover them.
	SessionID      string
	SourceLocation string
	SourceOrder    int64
	SourceTime     string // SortableTime; falls back to TS
}

// Store owns the normalized output sila.db.
type Store struct {
	DB       *sql.DB
	Path     string
	ReadOnly bool
}

// DefaultDBPath is the standard database location.
func DefaultDBPath() string {
	return config.DBPath()
}

// ownsDefaultDir reports whether dir is a directory sila owns (its own store
// location), where tightening permissions to 0700 is expected. An arbitrary
// --db parent chosen by the operator keeps its existing mode: chmod'ing a
// foreign directory (e.g. XDG_DATA_HOME itself) would silently restrict other
// tools' access to their own data.
func ownsDefaultDir(dir string) bool {
	if env := os.Getenv("SILA_DB"); env != "" {
		// Explicit overrides: sila owns only the file, not the parent's mode.
		return false
	}
	defaultDir := config.DataDir()
	cleanDir := filepath.Clean(dir)
	return cleanDir == filepath.Clean(defaultDir)
}

// Open opens (creating if needed) and migrates the knowledge store.
// If opening the default global store fails due to read-only filesystem or permission
// errors, and the process is inside a writable workspace, it automatically falls back
// to a workspace-local store at .sila/sila.db.
func Open(path string) (*Store, error) {
	return OpenWithFallback(path, "")
}

// openDirect opens and migrates the store directly without fallback.
func openDirect(path string) (*Store, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if ownsDefaultDir(dir) {
		if fi, err := os.Stat(dir); err == nil && fi.Mode().Perm()&0200 != 0 {
			if err := os.Chmod(dir, 0o700); err != nil {
				return nil, err
			}
		}
	}

	if f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600); err != nil {
		return nil, err
	} else {
		_ = f.Close()
	}
	if fi, err := os.Stat(path); err == nil && fi.Mode().Perm()&0200 != 0 {
		if err := os.Chmod(path, 0o600); err != nil {
			return nil, err
		}
	}
	// busy_timeout makes concurrent writers (e.g. two syncs) queue rather than
	// fail immediately with SQLITE_BUSY; journal_mode(WAL) for concurrent reads.
	// busy_timeout is armed first so the WAL switch itself can wait on a lock.
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(%d)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)", path, BusyTimeoutMS)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, _ = db.Exec(fmt.Sprintf("PRAGMA busy_timeout=%d", BusyTimeoutMS))
	_, _ = db.Exec("PRAGMA foreign_keys = ON")
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	// Self-heal any corrupted "0" or empty watermarks left from past regressions
	_, _ = db.Exec(`DELETE FROM state WHERE key LIKE 'watermark:%' AND (value = '0' OR value = '')`)

	s := &Store{DB: db, Path: path}
	if err := s.SecurePermissions(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// SecurePermissions enforces owner-only access on the database and SQLite's
// sidecar files. The store holds transcripts and API-bearing configuration.
func (s *Store) SecurePermissions() error {
	if s.ReadOnly {
		return nil
	}
	// Only enforce 0700 on directories sila owns; an arbitrary --db parent
	// keeps the operator's chosen mode.
	if ownsDefaultDir(filepath.Dir(s.Path)) {
		if fi, err := os.Stat(filepath.Dir(s.Path)); err == nil && fi.Mode().Perm() != 0o700 {
			if err := os.Chmod(filepath.Dir(s.Path), 0o700); err != nil {
				return fmt.Errorf("secure directory permissions on %s: %w", filepath.Dir(s.Path), err)
			}
		}
	}
	for _, path := range []string{s.Path, s.Path + "-wal", s.Path + "-shm", s.Path + "-journal"} {
		if fi, err := os.Stat(path); err == nil && fi.Mode().Perm() != 0o600 {
			if err := os.Chmod(path, 0o600); err != nil {
				return fmt.Errorf("secure file permissions on %s: %w", path, err)
			}
		}
	}
	return nil
}

// openReadOnlyHandle opens the SQLite file itself in read-only mode, with no
// schema inspection or migration. Callers decide whether the schema is
// readable before handing the handle to query code. If the database directory
// or media is read-only and SQLite's default WAL recovery fails with a read-only
// error, it falls back to immutable=1 to avoid lock or shared-memory writes.
func openReadOnlyHandle(path string) (*sql.DB, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return openReadOnlyHandleContext(ctx, path)
}

func openReadOnlyHandleContext(ctx context.Context, path string) (*sql.DB, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	busyTimeoutMs := BusyTimeoutMS
	if dl, ok := ctx.Deadline(); ok {
		rem := time.Until(dl)
		if rem <= 0 {
			return nil, context.DeadlineExceeded
		}
		remMs := int(rem.Milliseconds())
		if remMs < busyTimeoutMs {
			busyTimeoutMs = remMs
		}
	}

	dsn := fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(%d)", path, busyTimeoutMs)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	_, _ = db.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout=%d", busyTimeoutMs))
	var uv int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&uv); err != nil {
		if strings.Contains(err.Error(), "readonly") || strings.Contains(err.Error(), "1544") || strings.Contains(err.Error(), "unable to open") {
			_ = db.Close()
			dsnImm := fmt.Sprintf("file:%s?mode=ro&immutable=1&_pragma=busy_timeout(%d)", path, busyTimeoutMs)
			dbImm, errImm := sql.Open("sqlite", dsnImm)
			if errImm == nil {
				var uvImm int
				if errScan := dbImm.QueryRowContext(ctx, "PRAGMA user_version").Scan(&uvImm); errScan == nil {
					return dbImm, nil
				}
				_ = dbImm.Close()
			}
		}
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// OpenInspection opens the knowledge store for non-mutating inspection (e.g.
// salam, heal --dry-run). If the store file does not exist, it returns
// (nil, false, nil) without error so orientation commands can still provide
// repository guidance. If the store file exists, it opens it in read-only
// mode without chmod, migration, lock-file creation, or config writes. If the
// store schema is newer than supported or unreadable/corrupt, it returns an
// explicit error so callers do not silently treat it as empty.
func OpenInspection(path string) (*Store, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return OpenInspectionContext(ctx, path)
}

// OpenInspectionContext opens the knowledge store for non-mutating inspection using the provided context.
func OpenInspectionContext(ctx context.Context, path string) (*Store, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			if isDefaultStorePath(path) {
				if wsRoot, ok := findWorkspaceRoot(); ok {
					localDB := filepath.Join(wsRoot, ".sila", config.PrimaryDBName)
					if _, errLocal := os.Stat(localDB); errLocal == nil {
						return OpenInspectionContext(ctx, localDB)
					}
				}
			}
			return nil, false, nil
		}
		return nil, false, err
	}

	busyTimeoutMs := BusyTimeoutMS
	if dl, ok := ctx.Deadline(); ok {
		rem := time.Until(dl)
		if rem <= 0 {
			return nil, false, context.DeadlineExceeded
		}
		remMs := int(rem.Milliseconds())
		if remMs < busyTimeoutMs {
			busyTimeoutMs = remMs
		}
	}

	// If no active WAL file with pending transactions exists, open with immutable=1
	// to guarantee zero writes and prevent SQLite from spawning a -shm sidecar.
	walPath := path + "-wal"
	walInfo, errWal := os.Stat(walPath)
	if errWal != nil || walInfo.Size() == 0 {
		dsnImm := fmt.Sprintf("file:%s?mode=ro&immutable=1&_pragma=busy_timeout(%d)", path, busyTimeoutMs)
		dbImm, errImm := sql.Open("sqlite", dsnImm)
		if errImm == nil {
			var currentVersion int
			if errScan := dbImm.QueryRowContext(ctx, "PRAGMA user_version").Scan(&currentVersion); errScan == nil {
				if currentVersion > len(migrations) {
					_ = dbImm.Close()
					return nil, false, fmt.Errorf("knowledge store schema v%d is newer than this sila build supports (v%d); upgrade sila or restore a backup made with this version", currentVersion, len(migrations))
				}
				if currentVersion == 0 {
					_ = dbImm.Close()
					return nil, false, fmt.Errorf("knowledge store at %s has uninitialized schema v0; run 'sila sync' first", path)
				}
				return &Store{DB: dbImm, Path: path, ReadOnly: true}, true, nil
			}
			_ = dbImm.Close()
		}
	}

	db, err := openReadOnlyHandleContext(ctx, path)
	if err != nil {
		return nil, false, fmt.Errorf("read store: %w", err)
	}
	var currentVersion int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&currentVersion); err != nil {
		_ = db.Close()
		return nil, false, fmt.Errorf("read user_version: %w", err)
	}
	if currentVersion > len(migrations) {
		_ = db.Close()
		return nil, false, fmt.Errorf("knowledge store schema v%d is newer than this sila build supports (v%d); upgrade sila or restore a backup made with this version", currentVersion, len(migrations))
	}
	if currentVersion == 0 {
		_ = db.Close()
		return nil, false, fmt.Errorf("knowledge store at %s has uninitialized schema v0; run 'sila sync' first", path)
	}
	return &Store{DB: db, Path: path, ReadOnly: true}, true, nil
}

// isReadOnlyStore reports whether a database file or its parent directory is read-only.
func isReadOnlyStore(path string) bool {
	if fi, err := os.Stat(path); err == nil {
		if fi.Mode().Perm()&0200 == 0 {
			return true
		}
	}
	dir := filepath.Dir(path)
	if dirFi, err := os.Stat(dir); err == nil {
		if dirFi.Mode().Perm()&0200 == 0 {
			return true
		}
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		if IsReadOnlyOrPermissionError(err) {
			return true
		}
	} else {
		_ = f.Close()
	}
	return false
}

// OpenRO opens the knowledge store in read-only mode (ideal for MCP servers and query clients).
// It prevents taking write locks and ensures concurrent ingest/sync passes are never blocked.
//
// The connection deliberately does not set journal_mode: switching a database
// into WAL is a write to its header, so a read-only handle would fail every
// statement with "attempt to write a readonly database" on any store that is
// not already WAL (notably a VACUUM INTO snapshot, which is the file users are
// told to verify).
//
// A store whose schema is older than this build is upgraded through the
// writable path first: the queries in this package reference columns and
// tables added by migrations, so a read-only handle against an out-of-date
// store would fail with "no such column" instead of returning results. Only an
// out-of-date store is touched; an up-to-date store is never migrated, never
// chmod'd, and a newer schema is refused outright.
func OpenRO(path string) (*Store, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			if isDefaultStorePath(path) {
				if wsRoot, ok := findWorkspaceRoot(); ok {
					localDB := filepath.Join(wsRoot, ".sila", config.PrimaryDBName)
					if _, errLocal := os.Stat(localDB); errLocal == nil {
						return OpenRO(localDB)
					}
				}
			}
			return nil, fmt.Errorf("knowledge store not found at %s (run 'sila sync' first)", path)
		}
		return nil, err
	}

	db, err := openReadOnlyHandle(path)
	if err != nil {
		return nil, err
	}
	var currentVersion int
	if err := db.QueryRow("PRAGMA user_version").Scan(&currentVersion); err != nil {
		db.Close()
		return nil, fmt.Errorf("read user_version: %w", err)
	}
	if currentVersion > len(migrations) {
		db.Close()
		return nil, fmt.Errorf("knowledge store schema v%d is newer than this sila build supports (v%d); upgrade sila or restore a backup made with this version", currentVersion, len(migrations))
	}
	if currentVersion < len(migrations) {
		db.Close()
		// If the store is on a read-only filesystem or unwritable, do not attempt mutation.
		if isReadOnlyStore(path) {
			return nil, fmt.Errorf("knowledge store schema v%d is incompatible with this sila build (v%d); cannot migrate read-only store (run 'sila sync' with write permissions to migrate)", currentVersion, len(migrations))
		}
		upgrader, err := Open(path)
		if err != nil {
			if IsReadOnlyOrPermissionError(err) {
				return nil, fmt.Errorf("knowledge store schema v%d is incompatible with this sila build (v%d); cannot migrate read-only store: %w", currentVersion, len(migrations), err)
			}
			return nil, err
		}
		if err := upgrader.Close(); err != nil {
			return nil, err
		}
		reopened, err := openReadOnlyHandle(path)
		if err != nil {
			return nil, err
		}
		return &Store{DB: reopened, Path: path, ReadOnly: true}, nil
	}
	return &Store{DB: db, Path: path, ReadOnly: true}, nil
}

// Close closes the underlying SQLite database handle.
func (s *Store) Close() error { return s.DB.Close() }

// EnsureProject returns the project row for a normalized absolute path,
// creating it with a deduped id if absent. Ids are the dir-name slug; if that
// id is already taken by a different path, an 8-hex hash suffix is appended.
func (s *Store) EnsureProject(path string) (Project, error) {
	path = config.NormalizePath(CanonicalProjectPath(path))
	var p Project
	err := s.DB.QueryRow(`SELECT id, path FROM projects WHERE path=?`, path).Scan(&p.ID, &p.Path)
	if err == nil {
		return p, nil
	} else if err != sql.ErrNoRows {
		return p, err
	}

	// Case/slash insensitive check across existing projects
	projects, pErr := s.Projects()
	if pErr == nil {
		for _, proj := range projects {
			if samePath(proj.Path, path) {
				return proj, nil
			}
		}
	}

	base := ProjectID(path)
	hash := pathHash8(path)
	id := base
	for i := 0; ; i++ {
		cand := base
		switch {
		case i == 0:
			cand = base
		case i == 1:
			cand = base + "-" + hash
		default:
			cand = fmt.Sprintf("%s-%s-%d", base, hash, i)
		}
		var owner string
		err := s.DB.QueryRow(`SELECT path FROM projects WHERE id=?`, cand).Scan(&owner)
		if err == sql.ErrNoRows {
			id = cand
			break
		}
		if err != nil {
			return p, err
		}
		if samePath(owner, path) {
			id = cand
			break
		}
	}

	err = WithRetry(context.Background(), func() error {
		_, execErr := s.DB.Exec(`INSERT INTO projects(path,id,created_at) VALUES(?,?,?)`, path, id, time.Now().UTC().Format(time.RFC3339))
		return execErr
	})
	if err != nil {
		return p, err
	}
	return Project{Path: path, ID: id}, nil
}

// WriteRun upserts records. A record whose (project_id, content_hash) already
// exists has its tool appended to source_tools; otherwise it is new. All writes
// happen in one transaction; the watermark is committed in the same tx under
// watermarkKey(runID, tool) so it advances atomically with the inserts.
//
// It returns (newRecords, merged): newRecords = rows inserted; merged = rows
// that already existed and gained a tool (cross-tool provenance-merged). Exact
// self-duplicates (same tool again) count toward neither.
func (s *Store) WriteRun(runID string, tool string, newWatermark string, messages []LoadMessage) (newRecords, merged int, err error) {
	return s.WriteRunScoped(runID, tool, tool, newWatermark, messages)
}

// WriteRunScoped is WriteRun with a separate watermark key, so one loader can
// keep a single provenance tool name while tracking a scoped cursor. The git
// loader uses this to keep per-repository watermarks (`git:<project-id>`)
// instead of one machine-wide cursor that repositories would clobber.
func (s *Store) WriteRunScoped(runID, tool, watermarkTool string, newWatermark string, messages []LoadMessage) (newRecords, merged int, err error) {
	if watermarkTool == "" {
		watermarkTool = tool
	}
	err = WithRetry(context.Background(), func() error {
		newRecords = 0
		merged = 0
		tx, beginErr := s.DB.Begin()
		if beginErr != nil {
			return beginErr
		}
		defer tx.Rollback()

		ins, pErr := tx.Prepare(`INSERT INTO messages(project_id,content_hash,ts,role,content,source_tools,first_seen) VALUES(?,?,?,?,?,?,?)`)
		if pErr != nil {
			return pErr
		}
		defer ins.Close()

		upd, pErr := tx.Prepare(`UPDATE messages SET source_tools=? WHERE project_id=? AND content_hash=?`)
		if pErr != nil {
			return pErr
		}
		defer upd.Close()

		sel, pErr := tx.Prepare(`SELECT source_tools FROM messages WHERE project_id=? AND content_hash=?`)
		if pErr != nil {
			return pErr
		}
		defer sel.Close()

		occIns, pErr := tx.Prepare(`INSERT INTO source_occurrences(message_id,tool,session_id,source_location,source_order,source_time)
		SELECT ?,?,?,?,?,?
		WHERE NOT EXISTS (
			SELECT 1 FROM source_occurrences
			WHERE message_id=? AND tool=? AND session_id=? AND source_location=? AND source_order=? AND source_time=?
		)`)
		if pErr != nil {
			return pErr
		}
		defer occIns.Close()

		msgIDForOccurrence := func(projectID, contentHash string) (int64, error) {
			var id int64
			scanErr := tx.QueryRow(`SELECT id FROM messages WHERE project_id=? AND content_hash=?`, projectID, contentHash).Scan(&id)
			return id, scanErr
		}

		now := time.Now().UTC().Format(time.RFC3339)
		for _, m := range messages {
			var existing sql.NullString
			if scanErr := sel.QueryRow(m.ProjectID, m.ContentHash).Scan(&existing); scanErr != nil && scanErr != sql.ErrNoRows {
				return scanErr
			}
			if existing.Valid {
				var tools []string
				json.Unmarshal([]byte(existing.String), &tools)
				if !contains(tools, m.Tool) {
					tools = append(tools, m.Tool)
					b, _ := json.Marshal(tools)
					if _, execErr := upd.Exec(string(b), m.ProjectID, m.ContentHash); execErr != nil {
						return execErr
					}
					merged++
				}
			} else {
				first := []string{m.Tool}
				b, _ := json.Marshal(first)
				if _, execErr := ins.Exec(m.ProjectID, m.ContentHash, m.TS, m.Role, m.Content, string(b), now); execErr != nil {
					return execErr
				}
				newRecords++
			}
			id, idErr := msgIDForOccurrence(m.ProjectID, m.ContentHash)
			if idErr != nil {
				return idErr
			}
			srcTime := m.SourceTime
			if srcTime == "" {
				if ts, perr := time.Parse(time.RFC3339, m.TS); perr == nil {
					srcTime = SortableTime(ts)
				} else {
					srcTime = m.TS
				}
			}
			if _, execErr := occIns.Exec(id, m.Tool, m.SessionID, m.SourceLocation, m.SourceOrder, srcTime,
				id, m.Tool, m.SessionID, m.SourceLocation, m.SourceOrder, srcTime); execErr != nil {
				return execErr
			}
		}

		if newWatermark != "" && newWatermark != "0" {
			var oldWM string
			_ = tx.QueryRow(`SELECT value FROM state WHERE key=?`, watermarkKey(runID, watermarkTool)).Scan(&oldWM)
			if isWatermarkAdvancing(oldWM, newWatermark) {
				if _, execErr := tx.Exec(`INSERT INTO state(key,value,updated_at) VALUES(?,?,?)
				ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
					watermarkKey(runID, watermarkTool), newWatermark, now); execErr != nil {
					return execErr
				}
			}
		}
		return tx.Commit()
	})
	if err != nil {
		return 0, 0, err
	}
	return newRecords, merged, nil
}

// isWatermarkAdvancing returns true if newWM is a non-empty, non-zero cursor
// that advances or equals oldWM monotonically.
func isWatermarkAdvancing(oldWM, newWM string) bool {
	if newWM == "" || newWM == "0" {
		return false
	}
	if oldWM == "" || oldWM == "0" {
		return true
	}
	// Try integer comparison (e.g. milliseconds or seconds unix timestamps)
	if oldInt, err1 := strconv.ParseInt(oldWM, 10, 64); err1 == nil {
		if newInt, err2 := strconv.ParseInt(newWM, 10, 64); err2 == nil {
			return newInt >= oldInt
		}
	}
	// Try float comparison
	if oldFloat, err1 := strconv.ParseFloat(oldWM, 64); err1 == nil {
		if newFloat, err2 := strconv.ParseFloat(newWM, 64); err2 == nil {
			return newFloat >= oldFloat
		}
	}
	// Try RFC3339 time comparison
	if oldT, err1 := time.Parse(time.RFC3339, oldWM); err1 == nil {
		if newT, err2 := time.Parse(time.RFC3339, newWM); err2 == nil {
			return !newT.Before(oldT)
		}
	}
	return true
}

// GetWatermark returns the stored cursor for a tool, or "" if none.
func (s *Store) GetWatermark(runID, tool string) (string, error) {
	var v string
	err := s.DB.QueryRow(`SELECT value FROM state WHERE key=?`, watermarkKey(runID, tool)).Scan(&v)
	if err == sql.ErrNoRows || v == "0" {
		return "", nil
	}
	return v, err
}

// SetWatermark stores a cursor for a tool, enforcing monotonic non-regression.
func (s *Store) SetWatermark(runID, tool, value string) error {
	if value == "" || value == "0" {
		return nil
	}
	return WithRetry(context.Background(), func() error {
		var oldWM string
		_ = s.DB.QueryRow(`SELECT value FROM state WHERE key=?`, watermarkKey(runID, tool)).Scan(&oldWM)
		if !isWatermarkAdvancing(oldWM, value) {
			return nil
		}
		_, err := s.DB.Exec(`INSERT INTO state(key,value,updated_at) VALUES(?,?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
			watermarkKey(runID, tool), value, time.Now().UTC().Format(time.RFC3339))
		return err
	})
}

// GetState retrieves a key's string value from the state table.
func (s *Store) GetState(key string) (string, error) {
	var val string
	err := s.DB.QueryRow(`SELECT value FROM state WHERE key=?`, key).Scan(&val)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return val, err
}

// SetState upserts a key-value pair in the state table with current UTC timestamp.
func (s *Store) SetState(key, value string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	return WithRetry(context.Background(), func() error {
		_, err := s.DB.Exec(`INSERT INTO state(key, value, updated_at) VALUES(?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`, key, value, now)
		return err
	})
}

func watermarkKey(runID, tool string) string {
	if runID == "" {
		return "watermark:" + tool
	}
	return "watermark:" + runID + ":" + tool
}

func contains(s []string, v string) bool {
	return slices.Contains(s, v)
}
