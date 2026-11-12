package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// FindMemoConflict returns another active memo with the same scope key in the
// project, or "" when there is none.
func (s *Store) FindMemoConflict(projectID, scopeKey, excludeID string, now time.Time) (string, error) {
	if scopeKey == "" {
		return "", nil
	}
	var id string
	err := s.DB.QueryRow(`SELECT id FROM facts
		WHERE project_id = ? AND (kind = 'memo' OR kind = '') AND scope_key = ? AND id != ?
		  AND COALESCE(review_status, '') != 'archived' AND COALESCE(disputed_by, '') = ''
		  AND (valid_from = '' OR valid_from <= ?) AND (valid_until = '' OR valid_until > ?)
		ORDER BY promoted_at DESC, id LIMIT 1`, projectID, scopeKey, excludeID, now.UTC().Format(time.RFC3339), now.UTC().Format(time.RFC3339)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return id, nil
}

// MarkDisputed cross-references two facts so readers can see the conflict.
func (s *Store) MarkDisputed(first, second string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`UPDATE facts SET disputed_by = ? WHERE id = ?`, second, first); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE facts SET disputed_by = ? WHERE id = ?`, first, second); err != nil {
		return err
	}
	return tx.Commit()
}

// ResolveDispute keeps winner, archives loser, and clears the winner's dispute.
func (s *Store) ResolveDispute(winner, loser string) error {
	if winner == "" || loser == "" || winner == loser {
		return fmt.Errorf("resolve needs two different fact ids")
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`UPDATE facts SET review_status = 'archived', is_stale = 1,
		stale_reason = 'superseded by ' || ?, disputed_by = '' WHERE id = ?`, winner, loser); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE facts SET disputed_by = '' WHERE id = ?`, winner); err != nil {
		return err
	}
	return tx.Commit()
}

// DisputedFactLine renders "Disputed: N (id, id)" with at most two ids, or ""
// when the project has no disputes.
func (s *Store) DisputedFactLine(projectID string) (string, error) {
	if projectID == "" {
		return "", nil
	}
	rows, err := s.DB.Query(`SELECT id FROM facts
		WHERE project_id = ? AND COALESCE(disputed_by, '') != '' AND COALESCE(review_status, '') != 'archived'
		ORDER BY id`, projectID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(ids) == 0 {
		return "", nil
	}
	line := fmt.Sprintf("Disputed: %d", len(ids))
	shown := ids
	if len(shown) > 2 {
		shown = shown[:2]
	}
	return line + " (" + strings.Join(shown, ", ") + ")", nil
}
