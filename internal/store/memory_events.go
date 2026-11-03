package store

import (
	"strings"
	"time"
)

// MemoryEvent is a privacy-preserving adoption event. It never stores query,
// claim, prompt, or path content.
type MemoryEvent struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"project_id"`
	SessionID string    `json:"session_id,omitempty"`
	Agent     string    `json:"agent,omitempty"`
	Verb      string    `json:"verb"`
	Outcome   string    `json:"outcome"`
	GoalSlug  string    `json:"goal_slug,omitempty"`
	Source    string    `json:"source"`
	Matched   int       `json:"matched,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// MemoryEventFilter selects ledger rows for reporting or retention.
type MemoryEventFilter struct {
	ProjectID string
	SessionID string
	Verb      string
	Source    string
	Since     time.Time
	Until     time.Time
	Limit     int
}

// RecordMemoryEvent inserts one event with second-granularity deduplication.
// It is intentionally non-retrying: a locked store drops the event.
func (s *Store) RecordMemoryEvent(e MemoryEvent) error {
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	e.CreatedAt = e.CreatedAt.UTC().Truncate(time.Second)
	if e.ID == "" {
		e.ID = FactID("memory-event", strings.Join([]string{e.SessionID, e.Verb, e.Source, e.CreatedAt.Format(time.RFC3339)}, "|"))
	}
	_, err := s.DB.Exec(`INSERT OR IGNORE INTO memory_events
		(id, project_id, session_id, agent, verb, outcome, goal_slug, source, matched, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.ProjectID, e.SessionID, e.Agent, e.Verb, e.Outcome, e.GoalSlug, e.Source, e.Matched,
		e.CreatedAt.Format(time.RFC3339))
	return err
}

// ListMemoryEvents returns events matching the filter, oldest first.
func (s *Store) ListMemoryEvents(f MemoryEventFilter) ([]MemoryEvent, error) {
	query := `SELECT id, project_id, session_id, agent, verb, outcome, goal_slug, source, matched, created_at FROM memory_events`
	var where []string
	var args []any
	if f.ProjectID != "" {
		where = append(where, "project_id = ?")
		args = append(args, f.ProjectID)
	}
	if f.SessionID != "" {
		where = append(where, "session_id = ?")
		args = append(args, f.SessionID)
	}
	if f.Verb != "" {
		where = append(where, "verb = ?")
		args = append(args, f.Verb)
	}
	if f.Source != "" {
		where = append(where, "source = ?")
		args = append(args, f.Source)
	}
	if !f.Since.IsZero() {
		where = append(where, "created_at >= ?")
		args = append(args, f.Since.UTC().Format(time.RFC3339))
	}
	if !f.Until.IsZero() {
		where = append(where, "created_at < ?")
		args = append(args, f.Until.UTC().Format(time.RFC3339))
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY created_at ASC, rowid ASC"
	limit := f.Limit
	if limit <= 0 {
		limit = 1000
	}
	query += " LIMIT ?"
	args = append(args, limit)

	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MemoryEvent
	for rows.Next() {
		var e MemoryEvent
		var created, goal string
		if err := rows.Scan(&e.ID, &e.ProjectID, &e.SessionID, &e.Agent, &e.Verb, &e.Outcome, &goal, &e.Source, &e.Matched, &created); err != nil {
			return nil, err
		}
		e.GoalSlug = goal
		e.CreatedAt, _ = time.Parse(time.RFC3339, created)
		out = append(out, e)
	}
	return out, rows.Err()
}

// PruneMemoryEvents deletes events strictly older than cutoff.
func (s *Store) PruneMemoryEvents(cutoff time.Time) (int64, error) {
	res, err := s.DB.Exec(`DELETE FROM memory_events WHERE created_at < ?`, cutoff.UTC().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
