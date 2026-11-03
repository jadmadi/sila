package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// ToolEvent is one privacy-safe tool invocation record.
type ToolEvent struct {
	ID         string    `json:"id"`
	ProjectID  string    `json:"project_id"`
	SessionID  string    `json:"session_id"`
	Agent      string    `json:"agent"`
	Source     string    `json:"source"`
	ToolName   string    `json:"tool_name"`
	Category   string    `json:"category"`
	Outcome    string    `json:"outcome"`
	DurationMS int64     `json:"duration_ms"`
	GoalSlug   string    `json:"goal_slug,omitempty"`
	ArgsShape  string    `json:"args_shape,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// ToolEventFilter selects tool events for listing, counts, and reports.
type ToolEventFilter struct {
	ProjectID string
	SessionID string
	ToolName  string
	Source    string
	Agent     string
	Since     time.Time
	Until     time.Time
	Limit     int
}

// RecordToolEvent inserts one event, deduplicated by session/source/tool/second.
// The insert is bounded to 5ms and drops on timeout or a locked store.
func (s *Store) RecordToolEvent(ev ToolEvent) error {
	if s == nil || s.DB == nil {
		return nil
	}
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = time.Now().UTC()
	}
	ev.CreatedAt = ev.CreatedAt.UTC().Truncate(time.Second)
	if ev.Source == "" {
		ev.Source = "unknown"
	}
	if ev.Category == "" {
		ev.Category = "unknown"
	}
	if ev.Outcome == "" {
		ev.Outcome = "ok"
	}
	sum := sha256.Sum256([]byte(ev.SessionID + "\x00" + ev.Source + "\x00" + ev.ToolName + "\x00" + ev.CreatedAt.Format(time.RFC3339)))
	ev.ID = "tev_" + hex.EncodeToString(sum[:8])
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	_, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO tool_events
		(id, project_id, session_id, agent, source, tool_name, category, outcome, duration_ms, goal_slug, args_shape, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ev.ID, ev.ProjectID, ev.SessionID, ev.Agent, ev.Source, ev.ToolName, ev.Category,
		ev.Outcome, ev.DurationMS, ev.GoalSlug, ev.ArgsShape, ev.CreatedAt.Format(time.RFC3339))
	if err != nil {
		// Best-effort: a busy/locked store must not fail the command.
		return nil
	}
	return nil
}

// ListToolEvents returns events in deterministic created_at DESC, id order.
func (s *Store) ListToolEvents(filter ToolEventFilter) ([]ToolEvent, error) {
	where, args := toolEventWhere(filter)
	query := `SELECT id, project_id, session_id, agent, source, tool_name, category, outcome,
		duration_ms, COALESCE(goal_slug, ''), COALESCE(args_shape, ''), created_at
		FROM tool_events ` + where + ` ORDER BY created_at DESC, id LIMIT ?`
	args = append(args, toolEventLimit(filter.Limit))
	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ToolEvent
	for rows.Next() {
		var ev ToolEvent
		var created string
		if err := rows.Scan(&ev.ID, &ev.ProjectID, &ev.SessionID, &ev.Agent, &ev.Source,
			&ev.ToolName, &ev.Category, &ev.Outcome, &ev.DurationMS, &ev.GoalSlug,
			&ev.ArgsShape, &created); err != nil {
			return nil, err
		}
		ev.CreatedAt, _ = time.Parse(time.RFC3339, created)
		out = append(out, ev)
	}
	return out, rows.Err()
}

// ToolEventCounts returns calls per tool name.
func (s *Store) ToolEventCounts(filter ToolEventFilter) (map[string]int, error) {
	where, args := toolEventWhere(filter)
	rows, err := s.DB.Query(`SELECT tool_name, COUNT(*) FROM tool_events `+where+` GROUP BY tool_name ORDER BY tool_name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var name string
		var count int
		if err := rows.Scan(&name, &count); err != nil {
			return nil, err
		}
		out[name] = count
	}
	return out, rows.Err()
}

// PruneToolEvents deletes events older than the cutoff and returns the count.
func (s *Store) PruneToolEvents(olderThan time.Time) (int, error) {
	res, err := s.DB.Exec(`DELETE FROM tool_events WHERE created_at < ?`, olderThan.UTC().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func toolEventWhere(filter ToolEventFilter) (string, []any) {
	var clauses []string
	var args []any
	add := func(clause string, value any) {
		clauses = append(clauses, clause)
		args = append(args, value)
	}
	if filter.ProjectID != "" {
		add("project_id = ?", filter.ProjectID)
	}
	if filter.SessionID != "" {
		add("session_id = ?", filter.SessionID)
	}
	if filter.ToolName != "" {
		add("tool_name = ?", filter.ToolName)
	}
	if filter.Source != "" {
		add("source = ?", filter.Source)
	}
	if filter.Agent != "" {
		add("agent = ?", filter.Agent)
	}
	if !filter.Since.IsZero() {
		add("created_at >= ?", filter.Since.UTC().Format(time.RFC3339))
	}
	if !filter.Until.IsZero() {
		add("created_at <= ?", filter.Until.UTC().Format(time.RFC3339))
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return "WHERE " + strings.Join(clauses, " AND "), args
}

func toolEventLimit(limit int) int {
	if limit <= 0 {
		return 1000000
	}
	return limit
}

var shapeSecretRE = map[string]bool{
	"secret": true, "token": true, "password": true, "key": true, "auth": true, "credential": true,
}

// BuildArgsShape returns a sorted key:type list with no values. Secret-like
// keys become redacted:type. The result is capped at 64 keys and 512 bytes.
func BuildArgsShape(raw []byte) string {
	var value any
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return ""
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	keys := make([]string, 0, len(obj))
	for key := range obj {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for i, key := range keys {
		if i >= 64 {
			parts = append(parts, "truncated:bool")
			break
		}
		name := key
		lower := strings.ToLower(key)
		for secret := range shapeSecretRE {
			if strings.Contains(lower, secret) {
				name = "redacted"
				break
			}
		}
		parts = append(parts, name+":"+shapeType(obj[key]))
	}
	shape := strings.Join(parts, ",")
	for len(shape) > 512 {
		_, size := utf8.DecodeLastRuneInString(shape)
		shape = shape[:len(shape)-size]
	}
	return shape
}

func shapeType(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case float64, float32, int, int64, json.Number:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return fmt.Sprintf("%T", value)
	}
}
