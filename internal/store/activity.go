package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// ActivityExcerpt holds a recorded excerpt from a session (earliest request or latest reply).
type ActivityExcerpt struct {
	MessageID  int64  `json:"message_id"`
	Occurrence int    `json:"occurrence"` // 1-based occurrence index for `sila search expand`
	Role       string `json:"role"`       // "user" or "assistant"
	Timestamp  string `json:"timestamp"`  // UTC RFC3339 timestamp
	Text       string `json:"text"`       // Short excerpt (at most 160 runes)
}

// SessionActivity represents recent activity for a single (project_id, tool, session_id).
type SessionActivity struct {
	ProjectID    string           `json:"project_id"`
	Tool         string           `json:"tool"`
	SessionID    string           `json:"session_id"` // Full session ID
	LastActiveAt string           `json:"last_active_at"`
	Request      *ActivityExcerpt `json:"request,omitempty"`
	LatestReply  *ActivityExcerpt `json:"latest_reply,omitempty"`
}

// ActivityQueryOpts defines filter and limit parameters for RecentActivity.
type ActivityQueryOpts struct {
	ProjectID string
	Tool      string
	SinceTime time.Time
	Top       int
}

// ActivityResult is the synthesized activity report for a single project.
type ActivityResult struct {
	ProjectID           string            `json:"project"`
	ProjectPath         string            `json:"project_path,omitempty"`
	Since               string            `json:"since"`
	ToolFilter          string            `json:"tool_filter,omitempty"`
	Top                 int               `json:"top"`
	TotalMatching       int               `json:"total_matching"`
	Sessions            []SessionActivity `json:"sessions"`
	LegacyMessagesCount int               `json:"legacy_messages_count,omitempty"`
}

// TruncateExcerpt normalizes whitespace and shortens text to at most maxRunes with an ellipsis.
func TruncateExcerpt(s string, maxRunes int) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	if maxRunes <= 1 {
		return "…"
	}
	return string(runes[:maxRunes-1]) + "…"
}

// ShortenSessionID produces a compact display version of a session ID (display only).
func ShortenSessionID(id string) string {
	id = strings.TrimSpace(id)
	if strings.HasPrefix(id, "…") {
		return id
	}
	runes := []rune(id)
	if len(runes) <= 8 {
		return id
	}
	return "…" + string(runes[len(runes)-6:])
}

// FormatActivityTime renders a timestamp for the activity view as "01-02 15:04".
// The date is always included: a 7-day window would otherwise print two
// different days as the same "18:00".
func FormatActivityTime(srcTime string) string {
	t, err := parseActivityTime(srcTime)
	if err != nil {
		return strings.TrimSpace(srcTime)
	}
	return t.UTC().Format("01-02 15:04")
}

// parseActivityTime accepts the timestamp shapes this store writes.
func parseActivityTime(srcTime string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.000000000Z", time.RFC3339} {
		if t, err := time.Parse(layout, srcTime); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized timestamp %q", srcTime)
}

// FormatUTCRFC3339 formats an occurrence timestamp string as standard RFC3339 UTC.
func FormatUTCRFC3339(srcTime string) string {
	var t time.Time
	var err error
	if t, err = time.Parse(time.RFC3339Nano, srcTime); err != nil {
		if t, err = time.Parse("2006-01-02T15:04:05.000000000Z", srcTime); err != nil {
			if t, err = time.Parse(time.RFC3339, srcTime); err != nil {
				return srcTime
			}
		}
	}
	return t.UTC().Format(time.RFC3339)
}

// RecentActivity retrieves bounded, source-attributed session history across tools
// for one project. It runs strictly read-only and queries indexed history.
func (s *Store) RecentActivity(opts ActivityQueryOpts) (*ActivityResult, error) {
	projID := strings.TrimSpace(opts.ProjectID)
	if projID == "" {
		return nil, fmt.Errorf("project ID is required for activity query")
	}

	sinceTime := opts.SinceTime
	if sinceTime.IsZero() {
		sinceTime = time.Now().UTC().Add(-24 * time.Hour)
	}
	sinceSortable := SortableTime(sinceTime)
	sinceRFC := sinceTime.UTC().Format(time.RFC3339)

	top := opts.Top
	if top <= 0 {
		top = 10
	}
	if top > 100 {
		top = 100
	}

	var whereClauses []string
	var whereArgs []any

	whereClauses = append(whereClauses, "m.project_id = ?")
	whereArgs = append(whereArgs, projID)

	whereClauses = append(whereClauses, "o.session_id != ''")
	whereClauses = append(whereClauses, "m.role IN ('user', 'assistant')")
	whereClauses = append(whereClauses, "o.source_time >= ?")
	whereArgs = append(whereArgs, sinceSortable)

	if opts.Tool != "" && opts.Tool != "all" {
		whereClauses = append(whereClauses, "o.tool = ?")
		whereArgs = append(whereArgs, opts.Tool)
	}

	whereSQL := strings.Join(whereClauses, " AND ")

	// 1. Total matching sessions in the window
	countQuery := `SELECT COUNT(*) FROM (
		SELECT 1
		FROM source_occurrences o
		JOIN messages m ON m.id = o.message_id
		WHERE ` + whereSQL + `
		GROUP BY o.tool, o.session_id
	)`
	var totalMatching int
	if err := s.DB.QueryRow(countQuery, whereArgs...).Scan(&totalMatching); err != nil {
		return nil, fmt.Errorf("count activity sessions: %w", err)
	}

	// 2. Query the newest top session keys
	groupQuery := `SELECT o.tool, o.session_id, MAX(o.source_time) AS latest_time
		FROM source_occurrences o
		JOIN messages m ON m.id = o.message_id
		WHERE ` + whereSQL + `
		GROUP BY o.tool, o.session_id
		ORDER BY latest_time DESC, o.tool ASC, o.session_id ASC
		LIMIT ?`
	groupArgs := append(whereArgs, top)

	rows, err := s.DB.Query(groupQuery, groupArgs...)
	if err != nil {
		return nil, fmt.Errorf("query activity sessions: %w", err)
	}
	defer rows.Close()

	type sessionKey struct {
		tool       string
		sessionID  string
		latestTime string
	}
	var keys []sessionKey
	for rows.Next() {
		var k sessionKey
		if err := rows.Scan(&k.tool, &k.sessionID, &k.latestTime); err != nil {
			return nil, fmt.Errorf("scan activity session: %w", err)
		}
		keys = append(keys, k)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Prepare queries for earliest user request and latest assistant reply
	stmtUser, err := s.DB.Prepare(`
		SELECT m.id, o.id, o.source_time, m.content
		FROM source_occurrences o
		JOIN messages m ON m.id = o.message_id
		WHERE m.project_id = ?
		  AND o.tool = ?
		  AND o.session_id = ?
		  AND m.role = 'user'
		  AND o.source_time >= ?
		ORDER BY o.source_time ASC, o.source_order ASC, o.id ASC
		LIMIT 1`)
	if err != nil {
		return nil, fmt.Errorf("prepare user excerpt: %w", err)
	}
	defer stmtUser.Close()

	stmtAsst, err := s.DB.Prepare(`
		SELECT m.id, o.id, o.source_time, m.content
		FROM source_occurrences o
		JOIN messages m ON m.id = o.message_id
		WHERE m.project_id = ?
		  AND o.tool = ?
		  AND o.session_id = ?
		  AND m.role = 'assistant'
		  AND o.source_time >= ?
		ORDER BY o.source_time DESC, o.source_order DESC, o.id DESC
		LIMIT 1`)
	if err != nil {
		return nil, fmt.Errorf("prepare assistant excerpt: %w", err)
	}
	defer stmtAsst.Close()

	stmtOccCount, err := s.DB.Prepare(`
		SELECT COUNT(*) FROM source_occurrences
		WHERE message_id = ? AND id <= ?`)
	if err != nil {
		return nil, fmt.Errorf("prepare occ count: %w", err)
	}
	defer stmtOccCount.Close()

	sessions := make([]SessionActivity, 0, len(keys))
	for _, k := range keys {
		sa := SessionActivity{
			ProjectID:    projID,
			Tool:         k.tool,
			SessionID:    k.sessionID,
			LastActiveAt: FormatUTCRFC3339(k.latestTime),
		}

		// Earliest user request in window
		var uMsgID, uOccID int64
		var uTime, uContent string
		err := stmtUser.QueryRow(projID, k.tool, k.sessionID, sinceSortable).Scan(&uMsgID, &uOccID, &uTime, &uContent)
		if err == nil {
			var occIdx int
			_ = stmtOccCount.QueryRow(uMsgID, uOccID).Scan(&occIdx)
			if occIdx < 1 {
				occIdx = 1
			}
			text := TruncateExcerpt(uContent, 160)
			if text == "" {
				text = "(empty message)"
			}
			sa.Request = &ActivityExcerpt{
				MessageID:  uMsgID,
				Occurrence: occIdx,
				Role:       "user",
				Timestamp:  FormatUTCRFC3339(uTime),
				Text:       text,
			}
		} else if err != sql.ErrNoRows {
			return nil, fmt.Errorf("query user request: %w", err)
		}

		// Latest assistant reply in window
		var aMsgID, aOccID int64
		var aTime, aContent string
		err = stmtAsst.QueryRow(projID, k.tool, k.sessionID, sinceSortable).Scan(&aMsgID, &aOccID, &aTime, &aContent)
		if err == nil {
			var occIdx int
			_ = stmtOccCount.QueryRow(aMsgID, aOccID).Scan(&occIdx)
			if occIdx < 1 {
				occIdx = 1
			}
			text := TruncateExcerpt(aContent, 160)
			if text == "" {
				text = "(empty message)"
			}
			sa.LatestReply = &ActivityExcerpt{
				MessageID:  aMsgID,
				Occurrence: occIdx,
				Role:       "assistant",
				Timestamp:  FormatUTCRFC3339(aTime),
				Text:       text,
			}
		} else if err != sql.ErrNoRows {
			return nil, fmt.Errorf("query assistant reply: %w", err)
		}

		sessions = append(sessions, sa)
	}

	// 3. Count legacy messages lacking session provenance in the window
	legacyCount := 0
	legOccQ := `SELECT COUNT(*) FROM source_occurrences o
		JOIN messages m ON m.id = o.message_id
		WHERE m.project_id = ? AND o.source_time >= ? AND o.session_id = '' AND m.role IN ('user', 'assistant')`
	legOccArgs := []any{projID, sinceSortable}
	if opts.Tool != "" && opts.Tool != "all" {
		legOccQ += " AND o.tool = ?"
		legOccArgs = append(legOccArgs, opts.Tool)
	}
	var count1 int
	if err := s.DB.QueryRow(legOccQ, legOccArgs...).Scan(&count1); err == nil {
		legacyCount += count1
	}

	legMsgQ := `SELECT COUNT(*) FROM messages m
		WHERE m.project_id = ? AND m.role IN ('user', 'assistant') AND m.ts >= ?
		  AND NOT EXISTS (SELECT 1 FROM source_occurrences o WHERE o.message_id = m.id)`
	legMsgArgs := []any{projID, sinceRFC}
	var count2 int
	if err := s.DB.QueryRow(legMsgQ, legMsgArgs...).Scan(&count2); err == nil {
		legacyCount += count2
	}

	var projPath string
	if p, err := s.ResolveProject(projID); err == nil {
		projPath = p.Path
	}

	return &ActivityResult{
		ProjectID:           projID,
		ProjectPath:         projPath,
		Since:               sinceRFC,
		ToolFilter:          opts.Tool,
		Top:                 top,
		TotalMatching:       totalMatching,
		Sessions:            sessions,
		LegacyMessagesCount: legacyCount,
	}, nil
}
