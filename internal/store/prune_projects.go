package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DeadProject is a registered project whose directory no longer exists.
type DeadProject struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	Messages int    `json:"messages"`
	Facts    int    `json:"facts"`
}

// isTempPath reports whether path lives under a throwaway temp location. Only
// such registrations are pruned automatically: a missing real project may be on
// an unmounted drive, so it must be removed by explicit decision, not by sweep.
func isTempPath(path string) bool {
	clean := filepath.Clean(path)
	for _, prefix := range []string{"/tmp/", "/var/tmp/", "/private/tmp/", "/var/folders/", filepath.Clean(os.TempDir()) + string(filepath.Separator)} {
		if strings.HasPrefix(clean+"/", prefix) && clean != strings.TrimSuffix(prefix, "/") {
			return true
		}
	}
	return false
}

// DeadTempProjects lists registrations whose path is under a temp directory
// and no longer exists on disk.
func (s *Store) DeadTempProjects() ([]DeadProject, error) {
	stats, err := s.ProjectsWithCounts()
	if err != nil {
		return nil, err
	}
	var out []DeadProject
	for _, p := range stats {
		if p.Path == "" || !isTempPath(p.Path) {
			continue
		}
		if _, err := os.Stat(p.Path); err == nil || !os.IsNotExist(err) {
			continue
		}
		out = append(out, DeadProject{ID: p.ID, Path: p.Path, Messages: p.Messages, Facts: p.Facts})
	}
	return out, nil
}

// PruneProjects deletes every row scoped to the given project ids and the
// registrations themselves, then re-chains the fact ledger so the deletion is an
// authorized mutation and not a break. It is destructive; callers gate it.
func (s *Store) PruneProjects(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	factMu.Lock()
	defer factMu.Unlock()
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	tables, err := projectScopedTables(tx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if id == "" || id == "no-project" {
			return fmt.Errorf("refusing to prune project %q", id)
		}
		for _, tbl := range tables {
			if _, err := tx.Exec(fmt.Sprintf(`DELETE FROM %s WHERE project_id = ?`, tbl), id); err != nil {
				return fmt.Errorf("delete %s rows for %s: %w", tbl, id, err)
			}
		}
		if _, err := tx.Exec(`DELETE FROM projects WHERE id = ?`, id); err != nil {
			return fmt.Errorf("delete project %s: %w", id, err)
		}
	}
	// Occurrences of deleted messages are orphans.
	_, _ = tx.Exec(`DELETE FROM source_occurrences WHERE message_id NOT IN (SELECT id FROM messages)`)
	if err := rechainFactsFromTx(tx, 0); err != nil {
		return err
	}
	return tx.Commit()
}
