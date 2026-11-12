package store

import (
	"encoding/json"
	"strings"
	"time"
)

// ReportSession is one agent session that overlaps the report window.
type ReportSession struct {
	SessionID  string
	ProjectID  string
	Harness    string
	Agent      string
	StartedAt  string
	LastSeenAt string
}

// ReportEvent is a prime or recall event in the window.
type ReportEvent struct {
	SessionID string
	Agent     string
	Harness   string
	ToolName  string
	CreatedAt string
}

// ReportToolCount is the number of messages ingested from one tool label.
type ReportToolCount struct {
	Tool     string
	Messages int64
}

// ReportKnowledgeDelta counts knowledge written in the window.
type ReportKnowledgeDelta struct {
	Memos       int64
	Lessons     int64
	Handoffs    int64
	GoalsClosed int64
}

// ReportHealthCounts is the store-side health summary.
type ReportHealthCounts struct {
	StaleFacts         int64
	DisputedDirectives int64
	ExpiredMemos       int64
	BrokenChains       int64
}

// ReportHandoff is one open handoff.
type ReportHandoff struct {
	ID        string
	Agent     string
	Summary   string
	OpenItems int
	CreatedAt string
}

func reportBound(since, until time.Time) (string, string) {
	from := "0000-01-01T00:00:00Z"
	if !since.IsZero() {
		from = since.UTC().Format(time.RFC3339)
	}
	to := "9999-12-31T23:59:59Z"
	if !until.IsZero() {
		to = until.UTC().Format(time.RFC3339)
	}
	return from, to
}

// ReportSessions returns sessions whose activity overlaps the window.
func (s *Store) ReportSessions(projectID string, since, until time.Time) ([]ReportSession, error) {
	from, to := reportBound(since, until)
	query := `SELECT session_id, project_id, harness, agent, started_at, last_seen_at
		FROM agent_sessions WHERE last_seen_at >= ? AND started_at <= ?`
	args := []any{from, to}
	if projectID != "" {
		query += ` AND project_id = ?`
		args = append(args, projectID)
	}
	query += ` ORDER BY started_at`
	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReportSession
	for rows.Next() {
		var item ReportSession
		if err := rows.Scan(&item.SessionID, &item.ProjectID, &item.Harness, &item.Agent, &item.StartedAt, &item.LastSeenAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// ReportPrimeRecallEvents returns prime and recall events joined to their
// session harness.
func (s *Store) ReportPrimeRecallEvents(projectID string, since, until time.Time) ([]ReportEvent, error) {
	from, to := reportBound(since, until)
	query := `SELECT e.session_id, e.agent, COALESCE(s.harness, ''), e.tool_name, e.created_at
		FROM tool_events e LEFT JOIN agent_sessions s ON s.session_id = e.session_id
		WHERE e.created_at >= ? AND e.created_at <= ? AND e.tool_name IN ('prime','prime_context','recall')`
	args := []any{from, to}
	if projectID != "" {
		query += ` AND e.project_id = ?`
		args = append(args, projectID)
	}
	query += ` ORDER BY e.created_at`
	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReportEvent
	for rows.Next() {
		var item ReportEvent
		if err := rows.Scan(&item.SessionID, &item.Agent, &item.Harness, &item.ToolName, &item.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// ReportMessageCounts groups ingested messages by tool label.
func (s *Store) ReportMessageCounts(projectID string, since, until time.Time) ([]ReportToolCount, error) {
	from, to := reportBound(since, until)
	query := `SELECT source_tools, COUNT(*) FROM messages WHERE ts >= ? AND ts <= ?`
	args := []any{from, to}
	if projectID != "" {
		query += ` AND project_id = ?`
		args = append(args, projectID)
	}
	query += ` GROUP BY source_tools`
	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int64{}
	for rows.Next() {
		var label string
		var count int64
		if err := rows.Scan(&label, &count); err != nil {
			return nil, err
		}
		var labels []string
		if err := json.Unmarshal([]byte(label), &labels); err != nil {
			labels = strings.Split(label, ",")
		}
		for _, tool := range labels {
			tool = strings.Trim(strings.TrimSpace(tool), `"[]`)
			if tool != "" {
				counts[tool] += count
			}
		}
	}
	out := make([]ReportToolCount, 0, len(counts))
	for tool, count := range counts {
		out = append(out, ReportToolCount{Tool: tool, Messages: count})
	}
	return out, rows.Err()
}

// ReportKnowledgeDelta counts memos, lessons, handoffs, and closed goals in the
// window.
func (s *Store) ReportKnowledgeDelta(projectID string, since, until time.Time) (ReportKnowledgeDelta, error) {
	from, to := reportBound(since, until)
	var delta ReportKnowledgeDelta
	count := func(query string, args ...any) (int64, error) {
		var n int64
		row := s.DB.QueryRow(query, args...)
		if err := row.Scan(&n); err != nil {
			return 0, err
		}
		return n, nil
	}
	memQuery := `SELECT COUNT(*) FROM memory_records WHERE created_at >= ? AND created_at <= ? AND kind = ?`
	memArgs := func(kind string) []any {
		args := []any{from, to, kind}
		if projectID != "" {
			args = append(args, projectID)
		}
		return args
	}
	if projectID != "" {
		memQuery += ` AND project_id = ?`
	}
	var err error
	delta.Memos, err = count(memQuery, memArgs("memo")...)
	if err != nil {
		return delta, err
	}
	delta.Lessons, err = count(memQuery, memArgs("lesson")...)
	if err != nil {
		return delta, err
	}
	handoffQuery := `SELECT COUNT(*) FROM handoffs WHERE created_at >= ? AND created_at <= ?`
	handoffArgs := []any{from, to}
	if projectID != "" {
		handoffQuery += ` AND project_id = ?`
		handoffArgs = append(handoffArgs, projectID)
	}
	if delta.Handoffs, err = count(handoffQuery, handoffArgs...); err != nil {
		return delta, err
	}
	goalQuery := `SELECT COUNT(*) FROM goal_audit_receipts WHERE certified_at >= ? AND certified_at <= ?`
	goalArgs := []any{from, to}
	if projectID != "" {
		goalQuery += ` AND project_id = ?`
		goalArgs = append(goalArgs, projectID)
	}
	delta.GoalsClosed, err = count(goalQuery, goalArgs...)
	return delta, err
}

// ReportHealthCounts counts stale facts and returns zeros for signals the store
// does not track yet.
func (s *Store) ReportHealthCounts(projectID string) (ReportHealthCounts, error) {
	query := `SELECT COUNT(*) FROM facts WHERE verified = 0 AND repo_files NOT IN ('', '[]')`
	args := []any{}
	if projectID != "" {
		query += ` AND project_id = ?`
		args = append(args, projectID)
	}
	var out ReportHealthCounts
	if err := s.DB.QueryRow(query, args...).Scan(&out.StaleFacts); err != nil {
		return out, err
	}
	return out, nil
}

// ReportOpenHandoffs returns the newest open handoffs with their open-item count.
func (s *Store) ReportOpenHandoffs(projectID string, limit int) ([]ReportHandoff, error) {
	if limit <= 0 {
		limit = 3
	}
	query := `SELECT id, agent, summary, open_items, created_at FROM handoffs WHERE status = 'open'`
	args := []any{}
	if projectID != "" {
		query += ` AND project_id = ?`
		args = append(args, projectID)
	}
	query += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReportHandoff
	for rows.Next() {
		var item ReportHandoff
		var openJSON string
		if err := rows.Scan(&item.ID, &item.Agent, &item.Summary, &openJSON, &item.CreatedAt); err != nil {
			return nil, err
		}
		var items []string
		_ = json.Unmarshal([]byte(openJSON), &items)
		item.OpenItems = len(items)
		out = append(out, item)
	}
	return out, rows.Err()
}

// ReportActiveSessionIDs returns distinct sessions with at least one event in
// the window, used as the coverage denominator.
func (s *Store) ReportActiveSessionIDs(projectID string, since, until time.Time) (map[string]bool, error) {
	from, to := reportBound(since, until)
	query := `SELECT DISTINCT session_id FROM tool_events WHERE created_at >= ? AND created_at <= ?`
	args := []any{from, to}
	if projectID != "" {
		query += ` AND project_id = ?`
		args = append(args, projectID)
	}
	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}
