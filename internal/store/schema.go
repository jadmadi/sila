package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/ast"
)

// CurrentSchemaVersion is the schema produced by a newly opened store.
func CurrentSchemaVersion() int {
	latest := 0
	for _, m := range migrations {
		if m.version > latest {
			latest = m.version
		}
	}
	return latest
}

// MinimumSupportedSchemaVersion is the oldest on-disk schema that migration
// tests guarantee can be upgraded by a current Sila release.
func MinimumSupportedSchemaVersion() int { return 1 }

// SchemaVersion reports the database schema version of this store.
func (s *Store) SchemaVersion() (int, error) {
	var version int
	if err := s.DB.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return version, nil
}

func init() {
	migrations = append(migrations, migration{
		version: 32,
		name:    "pure_go_ripwire_capabilities",
		up: func(tx *sql.Tx) error {
			stmts := []string{
				`CREATE TABLE IF NOT EXISTS symbol_notes (
					id TEXT PRIMARY KEY,
					project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
					canonical_id TEXT NOT NULL,
					symbol_name TEXT NOT NULL,
					package_path TEXT NOT NULL,
					file_path TEXT NOT NULL,
					symbol_kind TEXT NOT NULL,
					receiver TEXT NOT NULL DEFAULT '',
					span_start_line INTEGER NOT NULL,
					span_end_line INTEGER NOT NULL,
					note_text TEXT NOT NULL,
					note_kind TEXT NOT NULL DEFAULT 'memo',
					git_sha TEXT NOT NULL,
					git_branch TEXT NOT NULL,
					committer_date TEXT NOT NULL,
					created_at TEXT NOT NULL,
					superseded_by TEXT REFERENCES symbol_notes(id),
					is_dangling INTEGER NOT NULL DEFAULT 0
				);`,
				`CREATE INDEX IF NOT EXISTS idx_symbol_notes_lookup ON symbol_notes(project_id, canonical_id);`,
				`CREATE INDEX IF NOT EXISTS idx_symbol_notes_file ON symbol_notes(project_id, file_path);`,
				`CREATE INDEX IF NOT EXISTS idx_symbol_notes_name ON symbol_notes(project_id, symbol_name);`,
				`CREATE VIRTUAL TABLE IF NOT EXISTS symbol_notes_fts USING fts5(
					canonical_id,
					symbol_name,
					note_text,
					content='symbol_notes',
					content_rowid='rowid',
					tokenize='unicode61'
				);`,
				`CREATE TRIGGER IF NOT EXISTS trg_symbol_notes_ai AFTER INSERT ON symbol_notes
				BEGIN
					INSERT INTO symbol_notes_fts(rowid, canonical_id, symbol_name, note_text)
					VALUES (new.rowid, new.canonical_id, new.symbol_name, new.note_text);
				END;`,
				`CREATE TRIGGER IF NOT EXISTS trg_symbol_notes_ad AFTER DELETE ON symbol_notes
				BEGIN
					INSERT INTO symbol_notes_fts(symbol_notes_fts, rowid, canonical_id, symbol_name, note_text)
					VALUES ('delete', old.rowid, old.canonical_id, old.symbol_name, old.note_text);
				END;`,
				`CREATE TRIGGER IF NOT EXISTS trg_symbol_notes_au AFTER UPDATE ON symbol_notes
				BEGIN
					INSERT INTO symbol_notes_fts(symbol_notes_fts, rowid, canonical_id, symbol_name, note_text)
					VALUES ('delete', old.rowid, old.canonical_id, old.symbol_name, old.note_text);
					INSERT INTO symbol_notes_fts(rowid, canonical_id, symbol_name, note_text)
					VALUES (new.rowid, new.canonical_id, new.symbol_name, new.note_text);
				END;`,
				`CREATE TABLE IF NOT EXISTS symbol_call_graph (
					id TEXT PRIMARY KEY,
					project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
					caller_canonical_id TEXT NOT NULL,
					callee_canonical_id TEXT NOT NULL,
					call_site_file TEXT NOT NULL,
					call_site_line INTEGER NOT NULL,
					call_site_col INTEGER NOT NULL,
					call_kind TEXT NOT NULL DEFAULT 'direct',
					is_test_call INTEGER NOT NULL DEFAULT 0,
					updated_at TEXT NOT NULL
				);`,
				`CREATE INDEX IF NOT EXISTS idx_call_graph_callee ON symbol_call_graph(project_id, callee_canonical_id);`,
				`CREATE INDEX IF NOT EXISTS idx_call_graph_caller ON symbol_call_graph(project_id, caller_canonical_id);`,
				`CREATE TABLE IF NOT EXISTS test_blast_matrix (
					id TEXT PRIMARY KEY,
					project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
					test_func_name TEXT NOT NULL,
					test_file_path TEXT NOT NULL,
					package_path TEXT NOT NULL,
					target_canonical_id TEXT NOT NULL,
					call_depth INTEGER NOT NULL DEFAULT 1,
					is_direct INTEGER NOT NULL DEFAULT 1,
					updated_at TEXT NOT NULL
				);`,
				`CREATE INDEX IF NOT EXISTS idx_test_blast_target ON test_blast_matrix(project_id, target_canonical_id);`,
				`CREATE TABLE IF NOT EXISTS crew_lane_plans (
					id TEXT PRIMARY KEY,
					project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
					task_goal TEXT NOT NULL,
					lane_count INTEGER NOT NULL,
					landing_strategy TEXT NOT NULL,
					created_at TEXT NOT NULL
				);`,
				`CREATE TABLE IF NOT EXISTS crew_lane_allocations (
					id TEXT PRIMARY KEY,
					plan_id TEXT NOT NULL REFERENCES crew_lane_plans(id) ON DELETE CASCADE,
					lane_index INTEGER NOT NULL,
					agent_role TEXT NOT NULL,
					assigned_packages TEXT NOT NULL,
					assigned_files TEXT NOT NULL,
					landing_order INTEGER NOT NULL,
					created_at TEXT NOT NULL
				);`,
				`CREATE INDEX IF NOT EXISTS idx_crew_allocations_plan ON crew_lane_allocations(plan_id, lane_index);`,
			}
			for _, stmt := range stmts {
				if _, err := tx.Exec(stmt); err != nil {
					return err
				}
			}
			return nil
		},
	})
	migrations = append(migrations, migration{
		version: 33,
		name:    "epistemic_heartbeats_and_negative_signals",
		up: func(tx *sql.Tx) error {
			stmts := []string{
				`CREATE TABLE IF NOT EXISTS epistemic_heartbeats (
					id TEXT PRIMARY KEY,
					project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
					session_id TEXT NOT NULL,
					agent TEXT NOT NULL,
					invariant_rule TEXT NOT NULL,
					evidence_type TEXT NOT NULL DEFAULT 'file',
					evidence_ref TEXT NOT NULL DEFAULT '',
					status TEXT NOT NULL DEFAULT 'pass',
					created_at TEXT NOT NULL
				);`,
				`CREATE INDEX IF NOT EXISTS idx_heartbeats_project_rule ON epistemic_heartbeats(project_id, invariant_rule, created_at);`,
				`CREATE INDEX IF NOT EXISTS idx_heartbeats_session ON epistemic_heartbeats(project_id, session_id);`,
				`CREATE INDEX IF NOT EXISTS idx_facts_tier_kind ON facts(project_id, tier, kind);`,
			}
			for _, stmt := range stmts {
				if _, err := tx.Exec(stmt); err != nil {
					return err
				}
			}
			return nil
		},
	})
	migrations = append(migrations, migration{
		version: 34,
		name:    "silence_sentry_diagnostic_clusters",
		up: func(tx *sql.Tx) error {
			stmts := []string{
				`CREATE TABLE IF NOT EXISTS diagnostic_clusters (
					project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
					signature TEXT NOT NULL,
					category TEXT NOT NULL,
					template TEXT NOT NULL,
					target_file TEXT NOT NULL,
					target_symbol TEXT NOT NULL,
					occurrence_count INTEGER NOT NULL DEFAULT 1,
					first_seen TEXT NOT NULL,
					last_seen TEXT NOT NULL,
					PRIMARY KEY (project_id, signature)
				);`,
				`CREATE INDEX IF NOT EXISTS idx_clusters_proj ON diagnostic_clusters(project_id, category);`,
				`CREATE TABLE IF NOT EXISTS silence_overrides (
					project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
					signature TEXT NOT NULL,
					reason TEXT NOT NULL,
					actor TEXT NOT NULL,
					signature_proof TEXT NOT NULL,
					created_at TEXT NOT NULL,
					PRIMARY KEY (project_id, signature)
				);`,
				`CREATE TABLE IF NOT EXISTS diagnostic_session_frequencies (
					project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
					signature TEXT NOT NULL,
					session_id TEXT NOT NULL,
					occurrence_count INTEGER NOT NULL DEFAULT 1,
					recorded_at TEXT NOT NULL,
					PRIMARY KEY (project_id, signature, session_id)
				);`,
				`CREATE INDEX IF NOT EXISTS idx_diag_sess_freq ON diagnostic_session_frequencies(project_id, signature, recorded_at);`,
			}
			for _, stmt := range stmts {
				if _, err := tx.Exec(stmt); err != nil {
					return err
				}
			}
			return nil
		},
	})
	migrations = append(migrations, migration{
		version: 35,
		name:    "agent_sessions",
		up: func(tx *sql.Tx) error {
			stmts := []string{
				`CREATE TABLE IF NOT EXISTS agent_sessions (
					session_id        TEXT PRIMARY KEY,
					project_id        TEXT NOT NULL,
					harness           TEXT NOT NULL,
					agent             TEXT NOT NULL,
					parent_session_id TEXT,
					active_goal       TEXT,
					claim_id          TEXT,
					cwd               TEXT,
					pid               INTEGER,
					source            TEXT NOT NULL,
					started_at        TEXT NOT NULL,
					last_seen_at      TEXT NOT NULL,
					closed_at         TEXT
				);`,
				`CREATE INDEX IF NOT EXISTS idx_agent_sessions_project ON agent_sessions(project_id, last_seen_at);`,
				`CREATE INDEX IF NOT EXISTS idx_agent_sessions_goal ON agent_sessions(active_goal, last_seen_at);`,
			}
			for _, stmt := range stmts {
				if _, err := tx.Exec(stmt); err != nil {
					return err
				}
			}
			return nil
		},
	})
	migrations = append(migrations, migration{
		version: 36,
		name:    "memory_events",
		up: func(tx *sql.Tx) error {
			stmts := []string{
				`CREATE TABLE IF NOT EXISTS memory_events (
					id          TEXT PRIMARY KEY,
					project_id  TEXT NOT NULL,
					session_id  TEXT NOT NULL,
					agent       TEXT NOT NULL,
					verb        TEXT NOT NULL,
					outcome     TEXT NOT NULL,
					goal_slug   TEXT,
					source      TEXT NOT NULL,
					matched     INTEGER NOT NULL DEFAULT 0,
					created_at  TEXT NOT NULL
				);`,
				`CREATE INDEX IF NOT EXISTS idx_memory_events_time ON memory_events(created_at);`,
				`CREATE INDEX IF NOT EXISTS idx_memory_events_verb ON memory_events(verb, created_at);`,
				`CREATE UNIQUE INDEX IF NOT EXISTS idx_memory_events_dedup ON memory_events(session_id, verb, source, created_at);`,
			}
			for _, stmt := range stmts {
				if _, err := tx.Exec(stmt); err != nil {
					return err
				}
			}
			return nil
		},
	})
	migrations = append(migrations, migration{
		version: 37,
		name:    "tool_events_adoption_ledger",
		up: func(tx *sql.Tx) error {
			stmts := []string{
				`CREATE TABLE IF NOT EXISTS tool_events (
					id          TEXT PRIMARY KEY,
					project_id  TEXT NOT NULL,
					session_id  TEXT NOT NULL,
					agent       TEXT NOT NULL,
					source      TEXT NOT NULL,
					tool_name   TEXT NOT NULL,
					category    TEXT NOT NULL,
					outcome     TEXT NOT NULL,
					duration_ms INTEGER NOT NULL DEFAULT 0,
					goal_slug   TEXT,
					args_shape  TEXT,
					created_at  TEXT NOT NULL
				);`,
				`CREATE INDEX IF NOT EXISTS idx_tool_events_time ON tool_events(created_at);`,
				`CREATE INDEX IF NOT EXISTS idx_tool_events_project ON tool_events(project_id, created_at);`,
				`CREATE INDEX IF NOT EXISTS idx_tool_events_tool ON tool_events(tool_name, created_at);`,
				`CREATE INDEX IF NOT EXISTS idx_tool_events_session ON tool_events(session_id, created_at);`,
			}
			for _, stmt := range stmts {
				if _, err := tx.Exec(stmt); err != nil {
					return err
				}
			}
			return nil
		},
	})
	migrations = append(migrations, migration{
		version: 38,
		name:    "prime_events",
		up: func(tx *sql.Tx) error {
			stmts := []string{
				`CREATE TABLE IF NOT EXISTS prime_events (
					id           TEXT PRIMARY KEY,
					project_id   TEXT NOT NULL,
					session_id   TEXT NOT NULL,
					harness      TEXT NOT NULL,
					agent        TEXT NOT NULL,
					source       TEXT NOT NULL,
					status       TEXT NOT NULL,
					error_class  TEXT,
					reason       TEXT,
					duration_ms  INTEGER NOT NULL DEFAULT 0,
					brief_bytes  INTEGER NOT NULL DEFAULT 0,
					created_at   TEXT NOT NULL
				);`,
				`CREATE INDEX IF NOT EXISTS idx_prime_events_time ON prime_events(created_at);`,
				`CREATE INDEX IF NOT EXISTS idx_prime_events_project ON prime_events(project_id, created_at);`,
				`CREATE INDEX IF NOT EXISTS idx_prime_events_harness ON prime_events(harness, created_at);`,
				`CREATE INDEX IF NOT EXISTS idx_prime_events_session ON prime_events(session_id);`,
			}
			for _, stmt := range stmts {
				if _, err := tx.Exec(stmt); err != nil {
					return err
				}
			}
			return nil
		},
	})
}

// SymbolNote represents a durable fact, lesson, or invariant attached to an AST symbol.
type SymbolNote struct {
	ID            string `json:"id"`
	ProjectID     string `json:"project_id"`
	CanonicalID   string `json:"canonical_id"`
	SymbolName    string `json:"symbol_name"`
	PackagePath   string `json:"package_path"`
	FilePath      string `json:"file_path"`
	SymbolKind    string `json:"symbol_kind"`
	Receiver      string `json:"receiver,omitempty"`
	SpanStartLine int    `json:"span_start_line"`
	SpanEndLine   int    `json:"span_end_line"`
	NoteText      string `json:"note_text"`
	NoteKind      string `json:"note_kind"` // memo | lesson | invariant
	GitSHA        string `json:"git_sha"`
	GitBranch     string `json:"git_branch"`
	CommitterDate string `json:"committer_date"`
	CreatedAt     string `json:"created_at"`
	SupersededBy  string `json:"superseded_by,omitempty"`
	IsDangling    bool   `json:"is_dangling"`
}

// InsertSymbolNote persists a symbol note into SQLite and the FTS5 index.
func (s *Store) InsertSymbolNote(note *SymbolNote) error {
	if note.ID == "" {
		note.ID = "sn_" + NewV7UUID()
	}
	if note.CreatedAt == "" {
		note.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if note.NoteKind == "" {
		note.NoteKind = "memo"
	}

	if note.ProjectID != "" && !s.ReadOnly {
		var exists int
		_ = s.DB.QueryRow(`SELECT 1 FROM projects WHERE id = ?`, note.ProjectID).Scan(&exists)
		if exists == 0 {
			_, _ = s.DB.Exec(`INSERT OR IGNORE INTO projects (path, id, created_at) VALUES (?, ?, ?)`, note.ProjectID, note.ProjectID, time.Now().UTC().Format(time.RFC3339))
		}
	}

	query := `INSERT OR REPLACE INTO symbol_notes (
		id, project_id, canonical_id, symbol_name, package_path, file_path,
		symbol_kind, receiver, span_start_line, span_end_line, note_text,
		note_kind, git_sha, git_branch, committer_date, created_at,
		superseded_by, is_dangling
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err := s.DB.Exec(
		query,
		note.ID,
		note.ProjectID,
		note.CanonicalID,
		note.SymbolName,
		note.PackagePath,
		note.FilePath,
		note.SymbolKind,
		note.Receiver,
		note.SpanStartLine,
		note.SpanEndLine,
		note.NoteText,
		note.NoteKind,
		note.GitSHA,
		note.GitBranch,
		note.CommitterDate,
		note.CreatedAt,
		sqlNullString(note.SupersededBy),
		boolInt(note.IsDangling),
	)
	return err
}

// ListSymbolNotes returns symbol notes for a project, optionally filtered by a target symbol.
func (s *Store) ListSymbolNotes(projectID, target string) ([]SymbolNote, error) {
	rows, err := s.DB.Query(`
		SELECT id, project_id, canonical_id, symbol_name, package_path, file_path,
		       symbol_kind, receiver, span_start_line, span_end_line, note_text,
		       note_kind, git_sha, git_branch, committer_date, created_at,
		       COALESCE(superseded_by, ''), is_dangling
		FROM symbol_notes
		WHERE (? = '' OR project_id = ?)
		ORDER BY created_at DESC
	`, projectID, projectID)
	if err != nil {
		return nil, fmt.Errorf("list symbol notes: %w", err)
	}
	defer rows.Close()

	var notes []SymbolNote
	for rows.Next() {
		var n SymbolNote
		var isDanglingInt int
		if err := rows.Scan(
			&n.ID, &n.ProjectID, &n.CanonicalID, &n.SymbolName, &n.PackagePath, &n.FilePath,
			&n.SymbolKind, &n.Receiver, &n.SpanStartLine, &n.SpanEndLine, &n.NoteText,
			&n.NoteKind, &n.GitSHA, &n.GitBranch, &n.CommitterDate, &n.CreatedAt,
			&n.SupersededBy, &isDanglingInt,
		); err != nil {
			return nil, err
		}
		n.IsDangling = isDanglingInt == 1
		if target == "" || n.CanonicalID == target || n.SymbolName == target || ast.MatchSymbolTarget(n.CanonicalID, target) {
			notes = append(notes, n)
		}
	}
	return notes, rows.Err()
}

// GetSymbolNotesByFile returns all symbol notes associated with a specific file path.
func (s *Store) GetSymbolNotesByFile(projectID, filePath string) ([]SymbolNote, error) {
	cleanPath := strings.TrimPrefix(filePath, "./")
	rows, err := s.DB.Query(`
		SELECT id, project_id, canonical_id, symbol_name, package_path, file_path,
		       symbol_kind, receiver, span_start_line, span_end_line, note_text,
		       note_kind, git_sha, git_branch, committer_date, created_at,
		       COALESCE(superseded_by, ''), is_dangling
		FROM symbol_notes
		WHERE (? = '' OR project_id = ?) AND (file_path = ? OR file_path LIKE ?)
		ORDER BY span_start_line ASC
	`, projectID, projectID, cleanPath, "%"+cleanPath)
	if err != nil {
		return nil, fmt.Errorf("get symbol notes by file: %w", err)
	}
	defer rows.Close()

	var notes []SymbolNote
	for rows.Next() {
		var n SymbolNote
		var isDanglingInt int
		if err := rows.Scan(
			&n.ID, &n.ProjectID, &n.CanonicalID, &n.SymbolName, &n.PackagePath, &n.FilePath,
			&n.SymbolKind, &n.Receiver, &n.SpanStartLine, &n.SpanEndLine, &n.NoteText,
			&n.NoteKind, &n.GitSHA, &n.GitBranch, &n.CommitterDate, &n.CreatedAt,
			&n.SupersededBy, &isDanglingInt,
		); err != nil {
			return nil, err
		}
		n.IsDangling = isDanglingInt == 1
		notes = append(notes, n)
	}
	return notes, rows.Err()
}

// SearchSymbolNotesFTS searches symbol notes using FTS5 with LIKE fallback.
func (s *Store) SearchSymbolNotesFTS(projectID, query string, limit int) ([]SymbolNote, error) {
	if limit <= 0 {
		limit = 10
	}

	sanitized := FormatFTS5Query(query)
	var rows *sql.Rows
	var err error

	if sanitized != "" {
		rows, err = s.DB.Query(`
			SELECT n.id, n.project_id, n.canonical_id, n.symbol_name, n.package_path, n.file_path,
			       n.symbol_kind, n.receiver, n.span_start_line, n.span_end_line, n.note_text,
			       n.note_kind, n.git_sha, n.git_branch, n.committer_date, n.created_at,
			       COALESCE(n.superseded_by, ''), n.is_dangling
			FROM symbol_notes n
			JOIN symbol_notes_fts f ON n.rowid = f.rowid
			WHERE (? = '' OR n.project_id = ?) AND symbol_notes_fts MATCH ?
			ORDER BY rank
			LIMIT ?
		`, projectID, projectID, sanitized, limit)
	}

	if err != nil || rows == nil {
		likePat := "%" + query + "%"
		rows, err = s.DB.Query(`
			SELECT id, project_id, canonical_id, symbol_name, package_path, file_path,
			       symbol_kind, receiver, span_start_line, span_end_line, note_text,
			       note_kind, git_sha, git_branch, committer_date, created_at,
			       COALESCE(superseded_by, ''), is_dangling
			FROM symbol_notes
			WHERE (? = '' OR project_id = ?) AND (note_text LIKE ? OR canonical_id LIKE ? OR symbol_name LIKE ?)
			ORDER BY created_at DESC
			LIMIT ?
		`, projectID, projectID, likePat, likePat, likePat, limit)
		if err != nil {
			return nil, fmt.Errorf("search symbol notes: %w", err)
		}
	}
	defer rows.Close()

	var notes []SymbolNote
	for rows.Next() {
		var n SymbolNote
		var isDanglingInt int
		if err := rows.Scan(
			&n.ID, &n.ProjectID, &n.CanonicalID, &n.SymbolName, &n.PackagePath, &n.FilePath,
			&n.SymbolKind, &n.Receiver, &n.SpanStartLine, &n.SpanEndLine, &n.NoteText,
			&n.NoteKind, &n.GitSHA, &n.GitBranch, &n.CommitterDate, &n.CreatedAt,
			&n.SupersededBy, &isDanglingInt,
		); err != nil {
			return nil, err
		}
		n.IsDangling = isDanglingInt == 1
		notes = append(notes, n)
	}
	return notes, rows.Err()
}

// ExportSymbolNotes returns all active symbol notes for project JSON serialization.
func (s *Store) ExportSymbolNotes(projectID string) ([]SymbolNote, error) {
	return s.ListSymbolNotes(projectID, "")
}

// ImportSymbolNotes inserts or updates a list of symbol notes into SQLite.
func (s *Store) ImportSymbolNotes(projectID string, notes []SymbolNote) (int, error) {
	count := 0
	for _, n := range notes {
		n.ProjectID = projectID
		if err := s.InsertSymbolNote(&n); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func sqlNullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func init() {
	migrations = append(migrations, migration{
		version: 53,
		name:    "facts_visibility_scope",
		up: func(tx *sql.Tx) error {
			return ensureFactVisibilityColumn(tx)
		},
	})
}

// ensureFactVisibilityColumn adds the facts.visibility scope column when it is
// missing. The pragma guard keeps the migration idempotent for databases that
// were migrated out of order.
func ensureFactVisibilityColumn(tx *sql.Tx) error {
	var count int
	if err := tx.QueryRow(`SELECT count(*) FROM pragma_table_info('facts') WHERE name='visibility'`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	if _, err := tx.Exec(`ALTER TABLE facts ADD COLUMN visibility TEXT NOT NULL DEFAULT 'project'`); err != nil {
		return fmt.Errorf("migration 53 add visibility: %w", err)
	}
	return nil
}
