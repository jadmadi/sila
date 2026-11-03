package loaders

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"maps"
	"strings"
	"time"
)

// devinWatermark is the per-tool cursor for Devin. Nodes tracks the last
// message_nodes.row_id consumed per session (mirroring Devin's own
// rendered_max_node_id:<session>); Prompts tracks the max prompt_history
// timestamp (unix seconds) consumed. Tools tracks a separate per-session
// row_id cursor for the tool-activity layer (exec commands and file edits),
// so adding that layer later can backfill instead of being skipped by the
// already-advanced conclusion cursor.
type devinWatermark struct {
	Nodes   map[string]int64 `json:"nodes"`
	Prompts int64            `json:"prompts"`
	Tools   map[string]int64 `json:"tools,omitempty"`
}

// devinLoader reads the Devin sessions.db.
type devinLoader struct {
	path string
}

// NewDevinLoader returns a loader over the Devin CLI sessions.db.
func NewDevinLoader(path string) Loader {
	return devinLoader{path: path}
}

// Name returns the Devin loader identifier.
func (l devinLoader) Name() string { return "devin" }

// Load reads Devin CLI session messages newer than the watermark.
func (l devinLoader) Load(wm string, p *string) ([]Message, string, error) {
	if l.path == "" {
		return nil, "", errMissingDB
	}
	db, err := openReadOnly(l.path)
	if err != nil {
		return nil, "", err
	}
	defer db.Close()
	return loadDevin(db, p, wm)
}

// Count returns the number of candidate Devin session messages.
func (l devinLoader) Count(p *string) (int64, error) {
	if l.path == "" {
		return 0, errMissingDB
	}
	db, err := openReadOnly(l.path)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	return countDevin(db, p)
}

// countDevin counts the assistant-conclusion rows loadDevinNodes would emit for
// a full scan, as a no-deserialization COUNT(*). The in-Go curation is partly
// content JSON (an assistant role whose flattened content is non-empty and not
// a pure tool chunk); those predicates are approximated at the JSON-extract
// level here. This keeps Count an upper bound on what a full scan emits, so
// assess reports Total >= New as required.
func countDevin(db *sql.DB, project *string) (int64, error) {
	// The full curated Load emits both assistant conclusions (message_nodes) and
	// user prompts (prompt_history). Count both as no-deserialization COUNTS so
	// Total lines up with what a full scan would emit (and stays >= New).
	q := `
		SELECT
		  (SELECT COUNT(*)
		     FROM message_nodes mn JOIN sessions s ON s.id = mn.session_id
		    WHERE s.hidden = 0 AND json_extract(mn.chat_message, '$.role') = 'assistant'
		      AND (json_extract(mn.chat_message, '$.tool_calls') IS NULL OR json_extract(mn.chat_message, '$.tool_calls') IN ('', 'null', '[]'))
		      AND json_extract(mn.chat_message, '$.content') IS NOT NULL AND json_extract(mn.chat_message, '$.content') NOT IN ('', 'null')` +
		projectPred(`mn.session_id`, project) + `)
		+
		  (SELECT COUNT(*)
		     FROM prompt_history ph JOIN sessions s ON s.id = ph.session_id
		    WHERE s.hidden = 0 AND ph.is_shell = 0 AND ph.timestamp > 0 AND ph.content != ''` +
		projectPred(`ph.session_id`, project) + `)
		+
		  (SELECT COUNT(*)
		     FROM message_nodes mn JOIN sessions s ON s.id = mn.session_id,
		          json_each(json_extract(mn.chat_message, '$.tool_calls')) je
		    WHERE s.hidden = 0
		      AND json_extract(mn.chat_message, '$.role') = 'assistant'
		      AND json_extract(je.value, '$.name') IN ('exec', 'edit', 'write')
		      AND COALESCE(json_extract(je.value, '$.arguments.command'),
		                   json_extract(je.value, '$.arguments.file_path'), '') != ''` +
		projectPred(`mn.session_id`, project) + `)`
	args := projectArgs(project)
	var n int64
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// projectPred returns an AND clause scoping a subquery by its session column to
// the project's working_directory (as a bound ? placeholder), or "" when no
// project is selected. projectArgs supplies the matching bound values.
func projectPred(sessCol string, project *string) string {
	if project == nil {
		return ""
	}
	return ` AND s.working_directory LIKE ?`
}

func projectArgs(project *string) []any {
	if project == nil {
		return nil
	}
	// One placeholder per subquery: conclusions, prompts, tool activity.
	return []any{"%" + *project + "%", "%" + *project + "%", "%" + *project + "%"}
}

// loadDevin reads curated messages: user prompts (prompt_history) + assistant
// content conclusions (message_nodes), skipping empty/structure-only rows.
// project, when set, filters sessions by working_directory substring.
func loadDevin(db *sql.DB, project *string, watermark string) ([]Message, string, error) {
	var w devinWatermark
	json.Unmarshal([]byte(watermark), &w)
	if w.Nodes == nil {
		w.Nodes = map[string]int64{}
	}

	nodesMsgs, newNodes, err := loadDevinNodes(db, project, w.Nodes)
	if err != nil {
		return nil, "", err
	}
	promptsMsgs, newPrompts, err := loadDevinPrompts(db, project, w.Prompts)
	if err != nil {
		return nodesMsgs, "", err
	}
	toolsMsgs, newTools, err := loadDevinToolActivity(db, project, w.Tools)
	if err != nil {
		return nodesMsgs, "", err
	}
	msgs := append(nodesMsgs, promptsMsgs...)
	msgs = append(msgs, toolsMsgs...)

	w.Nodes = newNodes
	w.Prompts = newPrompts
	w.Tools = newTools
	wm, _ := json.Marshal(w)
	return msgs, string(wm), nil
}

// devinVisibleSessions lists the sessions a load may read, honoring the
// project filter, and returns id -> working_directory.
//
// Selecting sessions once removes the per-row join, and — more importantly —
// lets each session be read through idx_message_nodes_session. The previous
// single global floor ("row_id > smallest per-session watermark") was defeated
// by the smallest watermark in the table: with 166 sessions whose floor ranged
// from 98 to the newest row, every sync still scanned 281,833 of 281,931 rows.
func devinVisibleSessions(db *sql.DB, project *string) (map[string]string, []string, error) {
	q := `SELECT id, COALESCE(working_directory, '') FROM sessions WHERE hidden = 0`
	args := []any{}
	if project != nil {
		q += ` AND working_directory LIKE ?`
		args = append(args, "%"+*project+"%")
	}
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	dirs := map[string]string{}
	var ids []string
	for rows.Next() {
		var id, dir string
		if err := rows.Scan(&id, &dir); err != nil {
			return nil, nil, err
		}
		dirs[id] = dir
		ids = append(ids, id)
	}
	return dirs, ids, rows.Err()
}

// loadDevinNodes reads assistant conclusions from message_nodes, skipping
// empty-content and pure tool-call chunks. seen tracks per-session max row_id.
func loadDevinNodes(db *sql.DB, project *string, seen map[string]int64) ([]Message, map[string]int64, error) {
	newSeen := map[string]int64{}
	maps.Copy(newSeen, seen)
	dirs, ids, err := devinVisibleSessions(db, project)
	if err != nil {
		return nil, nil, err
	}

	var msgs []Message
	for _, sid := range ids {
		// Read only this session's new rows, by index: a stalled session no
		// longer forces a scan of every other session's history.
		rows, err := db.Query(`SELECT row_id, created_at, chat_message FROM message_nodes
			WHERE session_id = ? AND row_id > ? ORDER BY row_id`, sid, seen[sid])
		if err != nil {
			return nil, nil, err
		}
		wd := dirs[sid]
		for rows.Next() {
			var chat string
			var rowID, createdAt int64
			if err := rows.Scan(&rowID, &createdAt, &chat); err != nil {
				continue
			}
			// Advance the cursor for every scanned row, including rows whose
			// content is filtered out below: otherwise a session of tool-call
			// chunks would keep its watermark still and be rescanned forever.
			if newSeen[sid] < rowID {
				newSeen[sid] = rowID
			}

			var m struct {
				Role      string          `json:"role"`
				Content   json.RawMessage `json:"content"`
				ToolCalls json.RawMessage `json:"tool_calls"`
			}
			if jsonv2.Unmarshal([]byte(chat), &m) != nil || m.Role != "assistant" {
				continue
			}
			c := contentText(m.Content)
			if c == "" || isToolCall(m.ToolCalls) {
				continue
			}
			msgs = append(msgs, Message{
				Tool: "devin", SessionID: sid, ProjectDir: wd,
				TS: time.Unix(createdAt, 0).UTC(), Role: "assistant", Content: c,
			})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, nil, err
		}
	}
	return msgs, newSeen, nil
}

// loadDevinPrompts reads user prompts newer than threshold (unix seconds) from
// prompt_history, skipping shell one-liners. Returns messages + new threshold.
func loadDevinPrompts(db *sql.DB, project *string, threshold int64) ([]Message, int64, error) {
	q := `
		SELECT ph.id, s.id, COALESCE(s.working_directory, ''), ph.timestamp, ph.content
		FROM prompt_history ph JOIN sessions s ON s.id = ph.session_id
		WHERE s.hidden = 0 AND ph.is_shell = 0 AND ph.timestamp > ?`
	args := []any{threshold}
	if project != nil {
		q += ` AND s.working_directory LIKE ?`
		args = append(args, "%"+*project+"%")
	}

	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, threshold, err
	}
	defer rows.Close()

	var msgs []Message
	var maxTS int64 = threshold
	for rows.Next() {
		var pid, sid, wd string
		var at int64
		var content string
		if err := rows.Scan(&pid, &sid, &wd, &at, &content); err != nil || content == "" {
			continue
		}
		if at > maxTS {
			maxTS = at
		}
		msgs = append(msgs, Message{
			Tool: "devin", SessionID: sid, ProjectDir: wd,
			TS: time.Unix(at, 0).UTC(), Role: "user", Content: content,
		})
	}
	return msgs, maxTS, rows.Err()
}

// loadDevinToolActivity extracts executed commands (exec) and file
// modifications (edit/write) from assistant tool-call nodes. seen tracks the
// max message_nodes.row_id consumed per session for this layer; it is separate
// from the conclusion cursor so the tool history can be backfilled on stores
// that were already fully ingested before this layer existed. Command and file
// paths become searchable messages; tool results stay out of the store.
func loadDevinToolActivity(db *sql.DB, project *string, seen map[string]int64) ([]Message, map[string]int64, error) {
	newSeen := map[string]int64{}
	maps.Copy(newSeen, seen)
	dirs, ids, err := devinVisibleSessions(db, project)
	if err != nil {
		return nil, nil, err
	}

	var msgs []Message
	for _, sid := range ids {
		// Per-session read, as in loadDevinNodes: the tool-activity pass shares
		// the same cursor and the same index.
		rows, err := db.Query(`SELECT row_id, created_at, chat_message FROM message_nodes
			WHERE session_id = ? AND row_id > ? ORDER BY row_id`, sid, seen[sid])
		if err != nil {
			return nil, nil, err
		}
		wd := dirs[sid]
		for rows.Next() {
			var chat string
			var rowID, createdAt int64
			if err := rows.Scan(&rowID, &createdAt, &chat); err != nil {
				continue
			}
			if newSeen[sid] < rowID {
				newSeen[sid] = rowID
			}
			var m struct {
				Role      string `json:"role"`
				ToolCalls []struct {
					Name      string `json:"name"`
					Arguments struct {
						Command  string `json:"command"`
						FilePath string `json:"file_path"`
					} `json:"arguments"`
				} `json:"tool_calls"`
			}
			if jsonv2.Unmarshal([]byte(chat), &m) != nil || m.Role != "assistant" {
				continue
			}
			ts := time.Unix(createdAt, 0).UTC()
			for _, tc := range m.ToolCalls {
				switch tc.Name {
				case "exec":
					if cmd := strings.TrimSpace(tc.Arguments.Command); cmd != "" {
						msgs = append(msgs, Message{
							Tool: "devin", SessionID: sid, ProjectDir: wd,
							TS: ts, Role: "command", Content: cmd,
						})
					}
				case "edit", "write":
					if p := strings.TrimSpace(tc.Arguments.FilePath); p != "" {
						msgs = append(msgs, Message{
							Tool: "devin", SessionID: sid, ProjectDir: wd,
							TS: ts, Role: "edit", Content: p,
						})
					}
				}
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, nil, err
		}
	}
	return msgs, newSeen, nil
}

// contentText flattens Devin content that may be a string or an array of
// {type,text} blocks (e.g. text / reasoning) into plain text.
func contentText(r json.RawMessage) string {
	if len(r) == 0 || string(r) == "null" {
		return ""
	}
	dec := jsontext.NewDecoder(bytes.NewReader(r))
	tok, err := dec.ReadToken()
	if err == nil && tok.Kind() == '"' {
		return tok.String()
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if jsonv2.Unmarshal(r, &parts) != nil {
		return ""
	}
	var out strings.Builder
	for _, p := range parts {
		if p.Text != "" {
			out.WriteString(p.Text)
		}
	}
	return out.String()
}

func isToolCall(r json.RawMessage) bool {
	return len(r) > 0 && string(r) != "null" && string(r) != "[]"
}
