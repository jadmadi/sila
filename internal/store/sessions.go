package store

import (
	"database/sql"
	"time"
)

// AgentSession is a local, non-telemetry session record.
type AgentSession struct {
	SessionID       string     `json:"session_id"`
	ProjectID       string     `json:"project_id"`
	Harness         string     `json:"harness"`
	Agent           string     `json:"agent"`
	ParentSessionID string     `json:"parent_session_id,omitempty"`
	ActiveGoal      string     `json:"active_goal,omitempty"`
	ClaimID         string     `json:"claim_id,omitempty"`
	Cwd             string     `json:"cwd,omitempty"`
	PID             int        `json:"pid,omitempty"`
	Source          string     `json:"source"`
	Branch          string     `json:"branch,omitempty"`
	Head            string     `json:"head,omitempty"`
	Detached        bool       `json:"detached,omitempty"`
	ContextDrift    string     `json:"context_drift,omitempty"`
	ContextDriftAt  *time.Time `json:"context_drift_at,omitempty"`
	StartedAt       time.Time  `json:"started_at"`
	LastSeenAt      time.Time  `json:"last_seen_at"`
	ClosedAt        time.Time  `json:"closed_at,omitempty"`
}

func init() {
	migrations = append(migrations, migration{
		version: 39,
		name:    "agent_session_git_context",
		up: func(tx *sql.Tx) error {
			columns := []struct{ name, ddl string }{
				{"branch", "branch TEXT"},
				{"head", "head TEXT"},
				{"detached", "detached INTEGER NOT NULL DEFAULT 0"},
				{"context_drift", "context_drift TEXT"},
				{"context_drift_at", "context_drift_at TEXT"},
			}
			for _, col := range columns {
				var count int
				if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('agent_sessions') WHERE name = ?`, col.name).Scan(&count); err != nil {
					return err
				}
				if count > 0 {
					continue
				}
				if _, err := tx.Exec(`ALTER TABLE agent_sessions ADD COLUMN ` + col.ddl); err != nil {
					return err
				}
			}
			return nil
		},
	})
}

func sessionTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func sessionTimePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return sessionTime(*t)
}

func parseSessionTime(v string) time.Time {
	if v == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339, v)
	return t
}

// UpsertAgentSession records a session, refreshing last_seen_at and metadata
// while preserving its original started_at.
func (s *Store) UpsertAgentSession(sess AgentSession) error {
	now := time.Now().UTC()
	if sess.StartedAt.IsZero() {
		sess.StartedAt = now
	}
	if sess.LastSeenAt.IsZero() {
		sess.LastSeenAt = now
	}
	if sess.Source == "" {
		sess.Source = "none"
	}
	if sess.ContextDrift != "" && sess.ContextDriftAt == nil {
		now := now.UTC()
		sess.ContextDriftAt = &now
	}
	_, err := s.DB.Exec(`INSERT INTO agent_sessions
		(session_id, project_id, harness, agent, parent_session_id, active_goal, claim_id, cwd, pid, source, branch, head, detached, context_drift, context_drift_at, started_at, last_seen_at, closed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			project_id=excluded.project_id,
			harness=excluded.harness,
			agent=excluded.agent,
			parent_session_id=excluded.parent_session_id,
			active_goal=excluded.active_goal,
			claim_id=excluded.claim_id,
			cwd=excluded.cwd,
			pid=excluded.pid,
			source=excluded.source,
			branch=COALESCE(NULLIF(excluded.branch, ''), agent_sessions.branch),
			head=COALESCE(NULLIF(excluded.head, ''), agent_sessions.head),
			detached=CASE WHEN excluded.branch = '' AND excluded.head = '' THEN agent_sessions.detached ELSE excluded.detached END,
			context_drift=COALESCE(NULLIF(excluded.context_drift, ''), agent_sessions.context_drift),
			context_drift_at=COALESCE(NULLIF(excluded.context_drift_at, ''), agent_sessions.context_drift_at),
			last_seen_at=excluded.last_seen_at,
			closed_at=excluded.closed_at`,
		sess.SessionID, sess.ProjectID, sess.Harness, sess.Agent, sess.ParentSessionID,
		sess.ActiveGoal, sess.ClaimID, sess.Cwd, sess.PID, sess.Source,
		sess.Branch, sess.Head, sess.Detached, sess.ContextDrift, sessionTimePtr(sess.ContextDriftAt),
		sessionTime(sess.StartedAt), sessionTime(sess.LastSeenAt), sessionTime(sess.ClosedAt))
	return err
}

// GetAgentSession returns one session or nil when absent.
func (s *Store) GetAgentSession(sessionID string) (*AgentSession, error) {
	row := s.DB.QueryRow(`SELECT session_id, project_id, harness, agent, parent_session_id,
		active_goal, claim_id, cwd, pid, source, branch, head, detached, context_drift, context_drift_at,
		started_at, last_seen_at, closed_at
		FROM agent_sessions WHERE session_id = ?`, sessionID)
	return scanAgentSession(row)
}

type sessionScanner interface {
	Scan(dest ...any) error
}

func scanAgentSession(row sessionScanner) (*AgentSession, error) {
	var sess AgentSession
	var parent, goal, claim, cwd, source, branch, head, drift, driftAt, started, lastSeen, closed sql.NullString
	var pid, detached sql.NullInt64
	err := row.Scan(&sess.SessionID, &sess.ProjectID, &sess.Harness, &sess.Agent,
		&parent, &goal, &claim, &cwd, &pid, &source, &branch, &head, &detached, &drift, &driftAt, &started, &lastSeen, &closed)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sess.ParentSessionID = parent.String
	sess.ActiveGoal = goal.String
	sess.ClaimID = claim.String
	sess.Cwd = cwd.String
	sess.PID = int(pid.Int64)
	sess.Source = source.String
	sess.Branch = branch.String
	sess.Head = head.String
	sess.Detached = detached.Int64 != 0
	sess.ContextDrift = drift.String
	if t := parseSessionTime(driftAt.String); !t.IsZero() {
		sess.ContextDriftAt = &t
	}
	sess.StartedAt = parseSessionTime(started.String)
	sess.LastSeenAt = parseSessionTime(lastSeen.String)
	sess.ClosedAt = parseSessionTime(closed.String)
	return &sess, nil
}

// TouchAgentSession updates last_seen_at.
func (s *Store) TouchAgentSession(sessionID string, at time.Time) error {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	_, err := s.DB.Exec(`UPDATE agent_sessions SET last_seen_at = ? WHERE session_id = ?`, sessionTime(at), sessionID)
	return err
}

// CloseAgentSession marks a session closed.
func (s *Store) CloseAgentSession(sessionID string, at time.Time) error {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	_, err := s.DB.Exec(`UPDATE agent_sessions SET closed_at = ?, last_seen_at = ? WHERE session_id = ?`,
		sessionTime(at), sessionTime(at), sessionID)
	return err
}

// ListAgentSessions returns sessions for a project, newest first.
func (s *Store) ListAgentSessions(projectID string, since time.Time, limit int) ([]AgentSession, error) {
	if limit <= 0 {
		limit = 100
	}
	query := `SELECT session_id, project_id, harness, agent, parent_session_id,
		active_goal, claim_id, cwd, pid, source, branch, head, detached, context_drift, context_drift_at,
		started_at, last_seen_at, closed_at
		FROM agent_sessions`
	var args []any
	if projectID != "" {
		query += ` WHERE project_id = ?`
		args = append(args, projectID)
		if !since.IsZero() {
			query += ` AND last_seen_at >= ?`
			args = append(args, sessionTime(since))
		}
	} else if !since.IsZero() {
		query += ` WHERE last_seen_at >= ?`
		args = append(args, sessionTime(since))
	}
	query += ` ORDER BY last_seen_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AgentSession
	for rows.Next() {
		sess, err := scanAgentSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sess)
	}
	return out, rows.Err()
}

// PruneAgentSessions deletes only closed sessions older than cutoff.
func (s *Store) PruneAgentSessions(cutoff time.Time) (int64, error) {
	res, err := s.DB.Exec(`DELETE FROM agent_sessions WHERE closed_at IS NOT NULL AND closed_at != '' AND closed_at < ?`, sessionTime(cutoff))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
