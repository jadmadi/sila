package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func init() {
	migrations = append(migrations, migration{
		version: 49,
		name:    "commit_receipts",
		up: func(tx *sql.Tx) error {
			stmts := []string{
				`CREATE TABLE IF NOT EXISTS commit_receipts (
					commit_sha   TEXT NOT NULL,
					project_id   TEXT NOT NULL,
					session_id   TEXT NOT NULL DEFAULT '',
					agent        TEXT NOT NULL DEFAULT '',
					harness      TEXT NOT NULL DEFAULT '',
					branch       TEXT NOT NULL DEFAULT '',
					tree_hash    TEXT NOT NULL DEFAULT '',
					source       TEXT NOT NULL DEFAULT '',
					ancestor_pid INTEGER NOT NULL DEFAULT 0,
					created_at   TEXT NOT NULL,
					PRIMARY KEY (project_id, commit_sha)
				);`,
				`CREATE INDEX IF NOT EXISTS idx_commit_receipts_project
					ON commit_receipts(project_id, created_at DESC);`,
			}
			for _, stmt := range stmts {
				if _, err := tx.Exec(stmt); err != nil {
					return fmt.Errorf("migration 49: %w", err)
				}
			}
			return nil
		},
	})
}

// CommitReceipt binds one commit to the session that produced it.
type CommitReceipt struct {
	CommitSHA   string    `json:"commit_sha"`
	ProjectID   string    `json:"project_id"`
	SessionID   string    `json:"session_id,omitempty"`
	Agent       string    `json:"agent,omitempty"`
	Harness     string    `json:"harness,omitempty"`
	Branch      string    `json:"branch,omitempty"`
	TreeHash    string    `json:"tree_hash,omitempty"`
	Source      string    `json:"source"`
	AncestorPID int       `json:"ancestor_pid,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// SaveCommitReceipt upserts by (project_id, commit_sha).
func (s *Store) SaveCommitReceipt(r CommitReceipt) error {
	if r.CommitSHA == "" {
		return errors.New("commit receipt requires a commit sha")
	}
	if r.ProjectID == "" {
		return errors.New("commit receipt requires a project")
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now().UTC()
	}
	_, err := s.DB.Exec(`INSERT INTO commit_receipts
		(commit_sha, project_id, session_id, agent, harness, branch, tree_hash, source, ancestor_pid, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(project_id, commit_sha) DO UPDATE SET
			session_id=excluded.session_id,
			agent=excluded.agent,
			harness=excluded.harness,
			branch=excluded.branch,
			tree_hash=excluded.tree_hash,
			source=excluded.source,
			ancestor_pid=excluded.ancestor_pid,
			created_at=excluded.created_at`,
		r.CommitSHA, r.ProjectID, r.SessionID, r.Agent, r.Harness, r.Branch, r.TreeHash, r.Source, r.AncestorPID, r.CreatedAt.UTC().Format(time.RFC3339))
	return err
}

// CommitReceipt returns one receipt and whether it exists.
func (s *Store) CommitReceipt(projectID, commitSHA string) (*CommitReceipt, bool, error) {
	var r CommitReceipt
	var created string
	err := s.DB.QueryRow(`SELECT commit_sha, project_id, session_id, agent, harness, branch, tree_hash, source, ancestor_pid, created_at
		FROM commit_receipts WHERE project_id = ? AND commit_sha = ?`, projectID, commitSHA).Scan(
		&r.CommitSHA, &r.ProjectID, &r.SessionID, &r.Agent, &r.Harness, &r.Branch, &r.TreeHash, &r.Source, &r.AncestorPID, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if parsed, parseErr := time.Parse(time.RFC3339, created); parseErr == nil {
		r.CreatedAt = parsed
	}
	return &r, true, nil
}

// ListCommitReceipts returns receipts newest first, capped at 200.
func (s *Store) ListCommitReceipts(projectID string, limit int) ([]CommitReceipt, error) {
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	query := `SELECT commit_sha, project_id, session_id, agent, harness, branch, tree_hash, source, ancestor_pid, created_at
		FROM commit_receipts`
	args := []any{}
	if projectID != "" {
		query += ` WHERE project_id = ?`
		args = append(args, projectID)
	}
	query += ` ORDER BY created_at DESC, commit_sha LIMIT ?`
	args = append(args, limit)
	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CommitReceipt
	for rows.Next() {
		var r CommitReceipt
		var created string
		if err := rows.Scan(&r.CommitSHA, &r.ProjectID, &r.SessionID, &r.Agent, &r.Harness, &r.Branch, &r.TreeHash, &r.Source, &r.AncestorPID, &created); err != nil {
			return nil, err
		}
		if parsed, parseErr := time.Parse(time.RFC3339, created); parseErr == nil {
			r.CreatedAt = parsed
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
