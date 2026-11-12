package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// FTSQueryTimeout is the strict per-query deadline enforced on SQLite FTS scans.
var FTSQueryTimeout = 5 * time.Second

// isFTSCorruption reports whether an error indicates corruption or internal failure in an FTS5 virtual table.
func isFTSCorruption(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "corrupt") ||
		strings.Contains(msg, "malformed") ||
		strings.Contains(msg, "vtable constructor failed") ||
		strings.Contains(msg, "fts5: ")
}

// RebuildFTSIndex rebuilds the specified FTS5 virtual table and re-synchronizes missing rows.
// Supported tables: "messages_fts", "facts_fts", "docs_fts".
func (s *Store) RebuildFTSIndex(table string) error {
	switch table {
	case "messages_fts":
		_, err := s.DB.Exec(`INSERT INTO messages_fts(messages_fts) VALUES('rebuild');`)
		if err != nil {
			_, _ = s.DB.Exec(`DELETE FROM messages_fts;`)
		}
		_, _ = s.DB.Exec(`DELETE FROM messages_fts WHERE rowid NOT IN (SELECT id FROM messages);`)
		_, err = s.DB.Exec(`INSERT INTO messages_fts(rowid, project_id, role, content)
			SELECT id, project_id, role, content FROM messages
			WHERE id NOT IN (SELECT rowid FROM messages_fts);`)
		return err
	case "facts_fts":
		_, err := s.DB.Exec(`INSERT INTO facts_fts(facts_fts) VALUES('rebuild');`)
		if err != nil {
			_, _ = s.DB.Exec(`DELETE FROM facts_fts;`)
		}
		_, _ = s.DB.Exec(`DELETE FROM facts_fts WHERE id NOT IN (SELECT id FROM facts);`)
		_, err = s.DB.Exec(`INSERT INTO facts_fts(id, project_id, claim, repo_files, tools)
			SELECT id, project_id, claim, repo_files, tools FROM facts
			WHERE id NOT IN (SELECT id FROM facts_fts);`)
		return err
	case "docs_fts":
		_, err := s.DB.Exec(`INSERT INTO docs_fts(docs_fts) VALUES('rebuild');`)
		if err != nil {
			_, _ = s.DB.Exec(`DELETE FROM docs_fts;`)
		}
		_, _ = s.DB.Exec(`DELETE FROM docs_fts WHERE id NOT IN (SELECT id FROM docs);`)
		_, err = s.DB.Exec(`INSERT INTO docs_fts(id, project_id, title, content)
			SELECT id, project_id, title, content FROM docs
			WHERE id NOT IN (SELECT id FROM docs_fts);`)
		return err
	default:
		return fmt.Errorf("invalid or untracked fts table: %s", table)
	}
}

// MessageView is a distilled-message read for the dream pass.
type MessageView struct {
	Content   string
	Role      string
	TS        string // RFC3339 UTC
	Tools     []string
	SessionID string // source session when known; empty for legacy rows
}

// Projects returns every project (path + id).
func (s *Store) Projects() ([]Project, error) {
	return s.QueryRows(context.Background(), `SELECT path, id FROM projects`, func(rows *sql.Rows) (Project, error) {
		var p Project
		err := rows.Scan(&p.Path, &p.ID)
		return p, err
	})
}

// ProjectStat is a project with its aggregated message/fact counts.
type ProjectStat struct {
	Project
	Messages int
	Facts    int
	Verified int
}

// ProjectsWithCounts returns each project with message, fact, and verified-fact
// counts, most messages first.
func (s *Store) ProjectsWithCounts() ([]ProjectStat, error) {
	return s.QueryRows(context.Background(), `
		SELECT p.path, p.id,
			(SELECT COUNT(*) FROM messages m WHERE m.project_id = p.id),
			(SELECT COUNT(*) FROM facts f WHERE f.project_id = p.id),
			(SELECT COUNT(*) FROM facts fv WHERE fv.project_id = p.id AND fv.verified = 1)
		FROM projects p
		ORDER BY (SELECT COUNT(*) FROM messages m WHERE m.project_id = p.id) DESC`,
		func(rows *sql.Rows) (ProjectStat, error) {
			var ps ProjectStat
			err := rows.Scan(&ps.Path, &ps.ID, &ps.Messages, &ps.Facts, &ps.Verified)
			return ps, err
		})
}

// MessagesForDistill returns the assistant messages for a project with the
// source-tool provenance needed to score confidence, plus the source session
// id when one is recorded.
func (s *Store) MessagesForDistill(projectID string) ([]MessageView, error) {
	return s.MessagesForDistillSince(projectID, 0)
}

// MessagesForDistillSince returns the assistant messages for a project with row
// id strictly greater than afterID so incremental distillation processes only new turns.
func (s *Store) MessagesForDistillSince(projectID string, afterID int64) ([]MessageView, error) {
	return s.QueryRows(context.Background(),
		`SELECT m.content, m.role, m.ts, m.source_tools,
			COALESCE((SELECT o.session_id FROM source_occurrences o WHERE o.message_id = m.id AND o.session_id != '' ORDER BY o.id LIMIT 1), '')
		FROM messages m WHERE m.project_id=? AND m.role='assistant' AND m.id > ? ORDER BY m.ts, m.id`,
		func(rows *sql.Rows) (MessageView, error) {
			var mv MessageView
			var tools string
			if err := rows.Scan(&mv.Content, &mv.Role, &mv.TS, &tools, &mv.SessionID); err != nil {
				return mv, err
			}
			mv.Tools = unjoinJSON(tools)
			return mv, nil
		},
		projectID, afterID)
}

// StoredMessage is a message row from the store.
type StoredMessage struct {
	ID          int64
	ProjectID   string
	ContentHash string
	TS          string
	Role        string
	Content     string
	SourceTools []string
	FirstSeen   string
	Outcome     string
}

// RecentMessages returns the most recent stored messages for a project, newest first.
func (s *Store) RecentMessages(projectID string, limit int) ([]StoredMessage, error) {
	if limit <= 0 {
		limit = 20
	}
	orderClause := `ORDER BY MAX(COALESCE(first_seen, ''), ts) DESC, ts DESC, id DESC`
	query := `SELECT id, project_id, content_hash, ts, role, content, source_tools, first_seen, outcome FROM messages ` + orderClause + ` LIMIT ?`
	var args []any
	if projectID != "" {
		query = `SELECT id, project_id, content_hash, ts, role, content, source_tools, first_seen, outcome FROM messages WHERE project_id = ? ` + orderClause + ` LIMIT ?`
		args = append(args, projectID, limit)
	} else {
		args = append(args, limit)
	}

	return s.QueryRows(context.Background(), query, func(rows *sql.Rows) (StoredMessage, error) {
		var sm StoredMessage
		var tools string
		if err := rows.Scan(&sm.ID, &sm.ProjectID, &sm.ContentHash, &sm.TS, &sm.Role, &sm.Content, &tools, &sm.FirstSeen, &sm.Outcome); err != nil {
			return sm, err
		}
		sm.SourceTools = unjoinJSON(tools)
		return sm, nil
	}, args...)
}

// SearchMessagesOptions encapsulates query filters for transcript message search.
type SearchMessagesOptions struct {
	Project string
	Tool    string
	Role    string
	Since   string
	Limit   int
	Offset  int
}

// SearchMessagesFTS performs full-text search against messages_fts with project, tool, role, since, limit, and offset support.
func (s *Store) SearchMessagesFTS(query string, opts SearchMessagesOptions) ([]StoredMessage, error) {
	target, cleanQuery := ParseTemporalAnchor(query, time.Now().UTC())
	effectiveQuery := query
	if target != nil && strings.TrimSpace(cleanQuery) != "" {
		effectiveQuery = cleanQuery
	}

	ftsQ := FormatFTS5Query(effectiveQuery)
	if ftsQ == "" {
		return nil, nil
	}
	matching, err := s.MatchingProjectIDs(opts.Project)
	if err != nil {
		return nil, err
	}
	sqlQ := `
		SELECT m.id, m.project_id, m.content_hash, m.ts, m.role, m.content, m.source_tools, m.first_seen, m.outcome
		FROM messages_fts
		JOIN messages m ON m.id = messages_fts.rowid
		WHERE messages_fts MATCH ?`
	args := []any{ftsQ}

	if len(matching) == 1 {
		sqlQ += ` AND m.project_id = ?`
		args = append(args, matching[0])
	} else if len(matching) > 1 {
		placeholders := strings.Repeat("?,", len(matching))
		placeholders = strings.TrimSuffix(placeholders, ",")
		sqlQ += ` AND m.project_id IN (` + placeholders + `)`
		for _, id := range matching {
			args = append(args, id)
		}
	} else if opts.Project != "" {
		return nil, nil
	}

	if opts.Tool != "" {
		sqlQ += ` AND m.source_tools LIKE ?`
		args = append(args, "%"+opts.Tool+"%")
	}
	if opts.Role != "" {
		sqlQ += ` AND LOWER(m.role) = LOWER(?)`
		args = append(args, opts.Role)
	}
	if opts.Since != "" {
		sqlQ += ` AND m.ts >= ?`
		args = append(args, opts.Since)
	}

	sqlQ += ` ORDER BY ((CASE WHEN LOWER(m.role) IN ('user', 'tool', 'commit') THEN 2.5 ELSE 1.0 END) * bm25(messages_fts)) ASC, m.ts DESC`
	if opts.Limit > 0 {
		sqlQ += ` LIMIT ?`
		args = append(args, opts.Limit)
		if opts.Offset > 0 {
			sqlQ += ` OFFSET ?`
			args = append(args, opts.Offset)
		}
	} else if opts.Offset > 0 {
		sqlQ += ` LIMIT -1 OFFSET ?`
		args = append(args, opts.Offset)
	}
	executeSearch := func() ([]StoredMessage, error) {
		ctx, cancel := context.WithTimeout(context.Background(), FTSQueryTimeout)
		defer cancel()

		rows, err := s.DB.QueryContext(ctx, sqlQ, args...)
		if err != nil {
			if ctx.Err() == context.DeadlineExceeded || errors.Is(err, context.DeadlineExceeded) {
				return nil, fmt.Errorf("fts5 query timed out after %v: %w", FTSQueryTimeout, context.DeadlineExceeded)
			}
			return nil, err
		}
		defer rows.Close()

		var out []StoredMessage
		for rows.Next() {
			var sm StoredMessage
			var tools string
			if err := rows.Scan(&sm.ID, &sm.ProjectID, &sm.ContentHash, &sm.TS, &sm.Role, &sm.Content, &tools, &sm.FirstSeen, &sm.Outcome); err != nil {
				return nil, err
			}
			sm.SourceTools = unjoinJSON(tools)
			out = append(out, sm)
		}
		if err := rows.Err(); err != nil {
			if ctx.Err() == context.DeadlineExceeded || errors.Is(err, context.DeadlineExceeded) {
				return nil, fmt.Errorf("fts5 query timed out after %v: %w", FTSQueryTimeout, context.DeadlineExceeded)
			}
			return nil, err
		}
		return out, nil
	}

	out, err := executeSearch()
	if err != nil && isFTSCorruption(err) {
		if healErr := s.RebuildFTSIndex("messages_fts"); healErr == nil {
			out, err = executeSearch()
		}
	}
	if err != nil {
		return nil, err
	}

	if target != nil && len(out) > 1 {
		sort.SliceStable(out, func(i, j int) bool {
			ti, oki := ParseDocTimestamp(out[i].TS)
			tj, okj := ParseDocTimestamp(out[j].TS)
			bi := CalculateTemporalBoost(ti, target)
			bj := CalculateTemporalBoost(tj, target)
			if !oki {
				bi = 0
			}
			if !okj {
				bj = 0
			}
			if math.Abs(bi-bj) > 1e-4 {
				return bi > bj
			}
			return out[i].TS > out[j].TS
		})
	}

	return out, nil
}

// MessageContext holds a target message with its surrounding chronological context window.
type MessageContext struct {
	TargetMessage StoredMessage   `json:"target_message"`
	Before        []StoredMessage `json:"before"`
	After         []StoredMessage `json:"after"`
	Window        int             `json:"window"`
	// Session reconstruction: when the target has a recorded source session,
	// the window stays inside that session. Legacy records without session
	// provenance report SessionAvailable=false and return no window rather
	// than presenting insertion-order neighbors as conversation turns.
	SessionID          string              `json:"session_id,omitempty"`
	SessionAvailable   bool                `json:"session_available"`
	OccurrenceCount    int                 `json:"occurrence_count,omitempty"`
	SelectedOccurrence int                 `json:"selected_occurrence,omitempty"` // 1-based
	Occurrences        []MessageOccurrence `json:"occurrences,omitempty"`
	Note               string              `json:"note,omitempty"`
}

// occPosExpr is an occurrence's position within its session. A recorded
// source_order is session-local and only comparable to another source_order;
// an occurrence without one therefore sorts *before* positioned records at the
// same timestamp and falls back to insertion id among its peers. Mixing the
// scales the other way (row id against source_order) inverts real order, e.g. a
// whole-second history prompt beside a rollout answer carrying an ordinal.
const occPosExpr = "COALESCE(NULLIF(o.source_order, 0), 0)"

// MessageOccurrence describes one recorded source position of a message.
type MessageOccurrence struct {
	RowID          int64  `json:"-"`
	Tool           string `json:"tool"`
	SessionID      string `json:"session_id,omitempty"`
	SourceLocation string `json:"source_location,omitempty"`
	SourceOrder    int64  `json:"source_order,omitempty"`
	SourceTime     string `json:"source_time,omitempty"`
}

const conversationUnavailable = "conversation reconstruction unavailable: this record has no source session provenance (ingested before occurrence tracking); insertion-order neighbors are not presented as conversation turns"

// MessageWithContext retrieves a stored message with its surrounding context.
// Deprecated: use MessageInContext, which selects among multiple occurrences.
func (s *Store) MessageWithContext(msgID int64, window int) (*MessageContext, error) {
	return s.MessageInContext(msgID, window, 0)
}

// MessageInContext retrieves a stored message along with N preceding and N
// following messages from the same source session. When deduplicated content
// has several recorded occurrences, occurrence (1-based) selects which one to
// expand; 0 picks the first. Windows never cross sessions: an unrelated
// message from another session can no longer appear as a conversation turn.
func (s *Store) MessageInContext(msgID int64, window int, occurrence int) (*MessageContext, error) {
	if window <= 0 {
		window = 3
	}
	if window > 20 {
		window = 20
	}

	var target StoredMessage
	var tools string
	err := s.DB.QueryRow(`
		SELECT id, project_id, content_hash, ts, role, content, source_tools, first_seen, outcome
		FROM messages
		WHERE id = ?`, msgID).Scan(
		&target.ID, &target.ProjectID, &target.ContentHash, &target.TS,
		&target.Role, &target.Content, &tools, &target.FirstSeen, &target.Outcome)
	if err != nil {
		return nil, err
	}
	target.SourceTools = unjoinJSON(tools)

	ctx := &MessageContext{TargetMessage: target, Window: window}

	rowsOcc, err := s.DB.Query(`
		SELECT id, tool, session_id, source_location, source_order, source_time
		FROM source_occurrences
		WHERE message_id = ?
		ORDER BY id`, msgID)
	if err != nil {
		return nil, err
	}
	var occs []MessageOccurrence
	for rowsOcc.Next() {
		var o MessageOccurrence
		if err := rowsOcc.Scan(&o.RowID, &o.Tool, &o.SessionID, &o.SourceLocation, &o.SourceOrder, &o.SourceTime); err != nil {
			rowsOcc.Close()
			return nil, err
		}
		occs = append(occs, o)
	}
	rowsOcc.Close()
	if err := rowsOcc.Err(); err != nil {
		return nil, err
	}
	ctx.OccurrenceCount = len(occs)
	ctx.Occurrences = occs
	if len(occs) == 0 {
		ctx.Note = conversationUnavailable
		return ctx, nil
	}
	if occurrence < 0 || occurrence > len(occs) {
		occurrence = 1
	}
	if occurrence == 0 {
		occurrence = 1
	}
	sel := occs[occurrence-1]
	ctx.SelectedOccurrence = occurrence
	if sel.SessionID == "" {
		// Recorded occurrence but unknown session: same unavailable contract.
		ctx.Note = conversationUnavailable
		return ctx, nil
	}
	ctx.SessionID = sel.SessionID
	ctx.SessionAvailable = true

	scanMsg := func(rows *sql.Rows) (StoredMessage, error) {
		var sm StoredMessage
		var t string
		if err := rows.Scan(&sm.ID, &sm.ProjectID, &sm.ContentHash, &sm.TS, &sm.Role, &sm.Content, &t, &sm.FirstSeen); err != nil {
			return sm, err
		}
		sm.SourceTools = unjoinJSON(t)
		return sm, nil
	}

	// Reconstruct the session by source position: the recorded source_order
	// when the source provides one (non-zero), otherwise the occurrence row id
	// as the explicit insertion-order fallback. The window is scoped to the
	// selected occurrence's tool — identical session-id strings in two tools
	// are separate namespaces — and excludes only the selected occurrence row,
	// so a repetition of the target's own text at a later position is a real
	// turn.
	// The target's own position uses the same rule as occPosExpr so the
	// predicate compares like with like: 0 means "no recorded position", not
	// "position zero", and it sorts before positioned records at that time.
	selPos := sel.SourceOrder
	posCmp := func(direction string) string {
		return fmt.Sprintf("(o.source_time %s ? OR (o.source_time = ? AND (%s %s ? OR (%s = ? AND o.id %s ?))))",
			direction, occPosExpr, direction, occPosExpr, direction)
	}
	windowSelect := func(predicate, order string) string {
		return fmt.Sprintf(`SELECT m.id, m.project_id, m.content_hash, m.ts, m.role, m.content, m.source_tools, m.first_seen
		FROM source_occurrences o
		JOIN messages m ON m.id = o.message_id
		WHERE o.session_id = ? AND m.project_id = ? AND o.tool = ? AND o.id != ?
		  AND %s
		ORDER BY %s
		LIMIT ?`, predicate, order)
	}

	// Before: positions strictly earlier, most recent first, then reversed
	// into chronological order.
	rowsBefore, err := s.DB.Query(
		windowSelect(posCmp("<"), "o.source_time DESC, "+occPosExpr+" DESC, o.id DESC"),
		sel.SessionID, target.ProjectID, sel.Tool, sel.RowID,
		sel.SourceTime, sel.SourceTime, selPos, selPos, sel.RowID, window)
	if err != nil {
		return nil, err
	}
	defer rowsBefore.Close()
	var beforeRev []StoredMessage
	for rowsBefore.Next() {
		sm, err := scanMsg(rowsBefore)
		if err != nil {
			return nil, err
		}
		beforeRev = append(beforeRev, sm)
	}
	if err := rowsBefore.Err(); err != nil {
		return nil, err
	}
	before := make([]StoredMessage, len(beforeRev))
	for i, sm := range beforeRev {
		before[len(beforeRev)-1-i] = sm
	}
	ctx.Before = before

	rowsAfter, err := s.DB.Query(
		windowSelect(posCmp(">"), "o.source_time ASC, "+occPosExpr+" ASC, o.id ASC"),
		sel.SessionID, target.ProjectID, sel.Tool, sel.RowID,
		sel.SourceTime, sel.SourceTime, selPos, selPos, sel.RowID, window)
	if err != nil {
		return nil, err
	}
	defer rowsAfter.Close()
	for rowsAfter.Next() {
		sm, err := scanMsg(rowsAfter)
		if err != nil {
			return nil, err
		}
		ctx.After = append(ctx.After, sm)
	}
	return ctx, rowsAfter.Err()
}

// ExpandSessionExchange reconstructs the conversation exchange surrounding anchorMessageID
// within sessionID for a chronological turn window of +/- window turns.
func (s *Store) ExpandSessionExchange(ctx context.Context, sessionID string, anchorMessageID string, window int) (*MessageContext, error) {
	if window <= 0 {
		window = 2
	}
	if window > 20 {
		window = 20
	}
	cleanID := strings.TrimPrefix(anchorMessageID, "msg-")
	msgID, err := strconv.ParseInt(cleanID, 10, 64)
	if err != nil || msgID <= 0 {
		return nil, fmt.Errorf("invalid anchor message id %q: %w", anchorMessageID, err)
	}

	if ctx == nil {
		ctx = context.Background()
	}

	if sessionID != "" {
		rows, err := s.DB.QueryContext(ctx, `
			SELECT id, session_id
			FROM source_occurrences
			WHERE message_id = ?
			ORDER BY id`, msgID)
		if err == nil {
			var occIdx int
			var idx int
			for rows.Next() {
				idx++
				var rowID int64
				var sess string
				if err := rows.Scan(&rowID, &sess); err == nil && sess == sessionID && occIdx == 0 {
					occIdx = idx
				}
			}
			rows.Close()
			if occIdx > 0 {
				return s.MessageInContext(msgID, window, occIdx)
			}
		}
	}

	return s.MessageInContext(msgID, window, 0)
}

// ResolveProjectFuzzy resolves a project using exact match first, then fuzzy alias matching
// against project IDs, registered paths, and directory basenames. For core orientation and
// memory commands (salam, prime, handoff, memo), use ResolveScope to enforce strict
// canonical identity without cross-repository fuzzy fallback.
func (s *Store) ResolveProjectFuzzy(name string) (*Project, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("project name or alias is required")
	}

	// 1. Try exact resolution first
	if p, err := s.ResolveProject(name); err == nil {
		return &p, nil
	}

	// 2. Fetch all registered projects
	projects, err := s.Projects()
	if err != nil {
		return nil, err
	}

	nameLower := strings.ToLower(name)

	// 3. Try prefix matches on ID or directory basename
	var prefixMatches []Project
	for _, p := range projects {
		idLower := strings.ToLower(p.ID)
		baseLower := strings.ToLower(filepath.Base(p.Path))
		if strings.HasPrefix(idLower, nameLower) || strings.HasPrefix(baseLower, nameLower) {
			prefixMatches = append(prefixMatches, p)
		}
	}
	if len(prefixMatches) == 1 {
		return &prefixMatches[0], nil
	}
	if len(prefixMatches) > 1 {
		ids := make([]string, 0, len(prefixMatches))
		for _, p := range prefixMatches {
			ids = append(ids, p.ID)
		}
		return nil, fmt.Errorf("project alias %q is ambiguous; matches: %s", name, strings.Join(ids, ", "))
	}

	// 4. Try substring matches on ID, directory basename, or full Path
	var substrMatches []Project
	for _, p := range projects {
		idLower := strings.ToLower(p.ID)
		baseLower := strings.ToLower(filepath.Base(p.Path))
		pathLower := strings.ToLower(p.Path)
		if strings.Contains(idLower, nameLower) || strings.Contains(baseLower, nameLower) || strings.Contains(pathLower, nameLower) {
			substrMatches = append(substrMatches, p)
		}
	}
	if len(substrMatches) == 1 {
		return &substrMatches[0], nil
	}
	if len(substrMatches) > 1 {
		ids := make([]string, 0, len(substrMatches))
		for _, p := range substrMatches {
			ids = append(ids, p.ID)
		}
		return nil, fmt.Errorf("project alias %q is ambiguous; matches: %s", name, strings.Join(ids, ", "))
	}

	return nil, fmt.Errorf("no registered project matches %q", name)
}
