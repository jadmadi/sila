package store

import (
	"strings"
	"time"
)

// PrimeEvent is one session-start prime attempt for observability.
type PrimeEvent struct {
	ID         string    `json:"id"`
	ProjectID  string    `json:"project_id"`
	SessionID  string    `json:"session_id,omitempty"`
	Harness    string    `json:"harness,omitempty"`
	Agent      string    `json:"agent,omitempty"`
	Source     string    `json:"source"`
	Status     string    `json:"status"`
	ErrorClass string    `json:"error_class,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	DurationMS int64     `json:"duration_ms,omitempty"`
	BriefBytes int       `json:"brief_bytes,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// PrimeEventFilter selects prime ledger rows.
type PrimeEventFilter struct {
	ProjectID string
	SessionID string
	Harness   string
	Status    string
	Source    string
	Since     time.Time
	Until     time.Time
	Limit     int
}

// RecordPrimeEvent inserts one attempt, deduplicated at second granularity.
func (s *Store) RecordPrimeEvent(e PrimeEvent) error {
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	e.CreatedAt = e.CreatedAt.UTC().Truncate(time.Second)
	if e.ID == "" {
		e.ID = FactID("prime-event", strings.Join([]string{
			e.SessionID, e.Harness, e.Source, e.Status, e.CreatedAt.Format(time.RFC3339),
		}, "|"))
	}
	_, err := s.DB.Exec(`INSERT OR IGNORE INTO prime_events
		(id, project_id, session_id, harness, agent, source, status, error_class, reason, duration_ms, brief_bytes, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.ProjectID, e.SessionID, e.Harness, e.Agent, e.Source, e.Status, e.ErrorClass, e.Reason,
		e.DurationMS, e.BriefBytes, e.CreatedAt.Format(time.RFC3339))
	return err
}

// ListPrimeEvents returns matching rows oldest-first.
func (s *Store) ListPrimeEvents(f PrimeEventFilter) ([]PrimeEvent, error) {
	query := `SELECT id, project_id, session_id, harness, agent, source, status, error_class, reason, duration_ms, brief_bytes, created_at FROM prime_events`
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
	if f.Harness != "" {
		where = append(where, "harness = ?")
		args = append(args, f.Harness)
	}
	if f.Status != "" {
		where = append(where, "status = ?")
		args = append(args, f.Status)
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
	query += " ORDER BY created_at ASC"
	if f.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, f.Limit)
	}
	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PrimeEvent
	for rows.Next() {
		var e PrimeEvent
		var created string
		if err := rows.Scan(&e.ID, &e.ProjectID, &e.SessionID, &e.Harness, &e.Agent, &e.Source,
			&e.Status, &e.ErrorClass, &e.Reason, &e.DurationMS, &e.BriefBytes, &created); err != nil {
			return nil, err
		}
		e.CreatedAt, _ = time.Parse(time.RFC3339, created)
		out = append(out, e)
	}
	return out, rows.Err()
}

// LatestPrimeEvent returns the most recent event for a project/harness.
func (s *Store) LatestPrimeEvent(projectID, harness string) (*PrimeEvent, error) {
	events, err := s.ListPrimeEvents(PrimeEventFilter{ProjectID: projectID, Harness: harness, Limit: 10000})
	if err != nil || len(events) == 0 {
		return nil, err
	}
	return &events[len(events)-1], nil
}

// PrunePrimeEvents deletes rows older than cutoff.
func (s *Store) PrunePrimeEvents(cutoff time.Time) (int64, error) {
	res, err := s.DB.Exec(`DELETE FROM prime_events WHERE created_at < ?`, cutoff.UTC().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
