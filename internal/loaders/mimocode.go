package loaders

import (
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// hasMiMoActorMarkers reports whether the store carries MiMoCode's actor
// classification: the message.agent_id column and the actor_registry table.
// OpenCode's store has neither, and keeps the unclassified behavior.
func hasMiMoActorMarkers(db *sql.DB) bool {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('message') WHERE name = 'agent_id'`).Scan(&n); err != nil || n == 0 {
		return false
	}
	err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'actor_registry'`).Scan(&n)
	return err == nil && n > 0
}

// loadMiMoOpenCode extracts curated messages from a MiMoCode/OpenCode-style
// store. The message.data JSON holds role/timestamps only; text content lives
// in the joined `part` table (parts of type text/reasoning). Working dir is the
// session.directory column. Returns messages (one per message id, text parts
// concatenated) and the max time_created (ms) as the new watermark.
//
// Curation rules for MiMoCode, detected by its actor markers:
//   - a message whose actor is classified `subagent` in actor_registry is
//     runtime work, not human speech, and is skipped; an unregistered actor is
//     ingested only when message.agent_id is the human-facing `main`, so the
//     marker column's `main` default keeps pre-marker history flowing;
//   - a text part the source marks `synthetic: true` is harness-injected
//     content (reminders, skill documents, rendered tool output) and is
//     dropped; a message left without parts is skipped whole.
//
// OpenCode has neither column nor table, so the rules do not apply there.
func loadMiMoOpenCode(db *sql.DB, tool string, project *string, watermark string) ([]Message, string, error) {
	actorMarkers := hasMiMoActorMarkers(db)
	q := `
		SELECT m.id, m.session_id, COALESCE(s.directory,''), m.time_created, m.data, p.data
		FROM message m
		JOIN session s ON s.id = m.session_id
		JOIN part p ON p.message_id = m.id`
	if actorMarkers {
		q += `
		LEFT JOIN actor_registry a ON a.session_id = m.session_id AND a.actor_id = m.agent_id`
	}
	q += `
		WHERE COALESCE(p.data,'') LIKE '%"text"%'`
	if actorMarkers {
		q += `
		  AND (a.mode = 'main' OR (a.mode IS NULL AND m.agent_id = 'main'))
		  AND COALESCE(CASE WHEN json_valid(p.data) THEN json_extract(p.data, '$.synthetic') END, 0) != 1`
	}
	args := []any{}
	if project != nil {
		q += ` AND s.directory LIKE ?`
		args = append(args, "%"+*project+"%")
	}
	var threshold int64
	if t, err := strconv.ParseInt(watermark, 10, 64); err == nil && t > 0 {
		threshold = t
		q += ` AND m.time_created > ?`
		args = append(args, threshold)
	}

	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, watermark, err
	}
	defer rows.Close()

	type acc struct {
		sess, cwd string
		ts        int64
		role      string
		text      string
	}
	byMsg := map[string]*acc{}
	var order []string
	maxMs := threshold
	for rows.Next() {
		var msgID, sess string
		var cwd string
		var ts int64
		var mdata, pdata string
		if err := rows.Scan(&msgID, &sess, &cwd, &ts, &mdata, &pdata); err != nil {
			continue
		}
		var m struct {
			Role string `json:"role"`
		}
		if json.Unmarshal([]byte(mdata), &m) != nil || (m.Role != "user" && m.Role != "assistant") {
			continue
		}
		var p struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal([]byte(pdata), &p) != nil || (p.Type != "text" && p.Type != "reasoning") || p.Text == "" {
			continue
		}
		a, ok := byMsg[msgID]
		if !ok {
			a = &acc{sess: sess, cwd: cwd, ts: ts, role: m.Role}
			byMsg[msgID] = a
			order = append(order, msgID)
		}
		a.text += p.Text
		if ts > a.ts {
			a.ts = ts
		}
		if a.ts > maxMs {
			maxMs = a.ts
		}
	}
	if err := rows.Err(); err != nil {
		return nil, watermark, err
	}

	msgs := make([]Message, 0, len(order))
	for _, id := range order {
		a := byMsg[id]
		msgs = append(msgs, Message{
			Tool:       tool,
			SessionID:  a.sess,
			ProjectDir: a.cwd,
			TS:         time.UnixMilli(a.ts).UTC(),
			Role:       a.role,
			Content:    a.text,
		})
	}
	newWM := watermark
	if maxMs > threshold {
		newWM = strconv.FormatInt(maxMs, 10)
	}
	if newWM == "0" {
		newWM = ""
	}
	return msgs, newWM, nil
}

// miMoOpenCodeLoader is the shared loader for MiMoCode and OpenCode, which
// share the message/part/session schema.
type miMoOpenCodeLoader struct {
	tool string
	path string
}

// NewMiMoCodeLoader returns a loader over the MiMoCode store.
func NewMiMoCodeLoader(path string) Loader {
	return miMoOpenCodeLoader{tool: "mimocode", path: path}
}

// NewOpenCodeLoader returns a loader over the OpenCode store.
func NewOpenCodeLoader(path string) Loader {
	return miMoOpenCodeLoader{tool: "opencode", path: path}
}

// Name returns the loader identifier.
func (l miMoOpenCodeLoader) Name() string { return l.tool }

// Load reads messages from MiMo or OpenCode SQLite database newer than the watermark.
func (l miMoOpenCodeLoader) Load(wm string, p *string) ([]Message, string, error) {
	if l.path == "" {
		return nil, "", errMissingDB
	}
	db, err := openReadOnly(l.path)
	if err != nil {
		return nil, "", err
	}
	defer db.Close()
	if l.tool == "opencode" && hasOpenCodeV2Schema(db) {
		return loadOpenCodeV2(db, p, wm)
	}
	return loadMiMoOpenCode(db, l.tool, p, wm)
}

// Count returns the number of candidate messages in the MiMo or OpenCode database.
func (l miMoOpenCodeLoader) Count(p *string) (int64, error) {
	if l.path == "" {
		return 0, errMissingDB
	}
	db, err := openReadOnly(l.path)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	if l.tool == "opencode" && hasOpenCodeV2Schema(db) {
		return countOpenCodeV2(db, p)
	}
	return countMiMoOpenCode(db, p, hasMiMoActorMarkers(db))
}

// countMiMoOpenCode counts the distinct curated messages loadMiMoOpenCode would
// emit for a full scan (one row per message id with a text-bearing part and a
// user/assistant role), approximated as a COUNT(DISTINCT m.id). Grouping by
// message id keeps the count in step with the loader's message-level emission;
// the in-Go role/part-type filtering is approximated at the JSON-extract level.
// When actorMarkers is set, the same actor and synthetic-part rules as the
// loader apply, keeping assess's Total >= New invariant.
func countMiMoOpenCode(db *sql.DB, project *string, actorMarkers bool) (int64, error) {
	q := `
		SELECT COUNT(DISTINCT m.id)
		FROM message m
		JOIN session s ON s.id = m.session_id
		JOIN part p ON p.message_id = m.id`
	if actorMarkers {
		q += `
		LEFT JOIN actor_registry a ON a.session_id = m.session_id AND a.actor_id = m.agent_id`
	}
	q += `
		WHERE COALESCE(p.data, '') LIKE '%"text"%'
		  AND json_extract(m.data, '$.role') IN ('user', 'assistant')`
	if actorMarkers {
		q += `
		  AND (a.mode = 'main' OR (a.mode IS NULL AND m.agent_id = 'main'))
		  AND COALESCE(CASE WHEN json_valid(p.data) THEN json_extract(p.data, '$.synthetic') END, 0) != 1`
	}
	args := []any{}
	if project != nil {
		q += ` AND s.directory LIKE ?`
		args = append(args, "%"+*project+"%")
	}
	var n int64
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// hasOpenCodeV2Schema reports whether the store uses the session_v2 and
// session_message tables introduced by OpenCode 1.18.
func hasOpenCodeV2Schema(db *sql.DB) bool {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('session_v2','session_message')`).Scan(&n)
	return err == nil && n == 2
}

// loadOpenCodeV2 extracts curated user and assistant messages from the
// session_v2/session_message schema. It skips in-flight assistant messages
// (no completion timestamp) so a later sync emits their full text, and it
// drops reasoning and tool parts, which are agent internals rather than
// durable knowledge. The watermark only advances to the newest emitted
// message, so skipped rows are retried on the next pass.
func loadOpenCodeV2(db *sql.DB, project *string, watermark string) ([]Message, string, error) {
	q := `
		SELECT m.session_id, m.type, m.time_created, COALESCE(s.directory,''), m.data
		FROM session_message m
		JOIN session_v2 s ON s.id = m.session_id
		WHERE m.type IN ('user','assistant')`
	args := []any{}
	if project != nil {
		q += ` AND s.directory LIKE ?`
		args = append(args, "%"+*project+"%")
	}
	var threshold int64
	if t, err := strconv.ParseInt(watermark, 10, 64); err == nil && t > 0 {
		threshold = t
		q += ` AND m.time_created > ?`
		args = append(args, threshold)
	}
	q += ` ORDER BY m.time_created ASC`

	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, watermark, err
	}
	defer rows.Close()

	var msgs []Message
	maxMs := threshold
	for rows.Next() {
		var sess, typ, dir, data string
		var ts int64
		if err := rows.Scan(&sess, &typ, &ts, &dir, &data); err != nil {
			continue
		}
		text, ok := openCodeV2Text(typ, data)
		if !ok || text == "" {
			continue
		}
		msgs = append(msgs, Message{
			Tool:       "opencode",
			SessionID:  sess,
			ProjectDir: dir,
			TS:         time.UnixMilli(ts).UTC(),
			Role:       typ,
			Content:    text,
		})
		if ts > maxMs {
			maxMs = ts
		}
	}
	if err := rows.Err(); err != nil {
		return nil, watermark, err
	}
	newWM := watermark
	if maxMs > threshold {
		newWM = strconv.FormatInt(maxMs, 10)
	}
	if newWM == "0" {
		newWM = ""
	}
	return msgs, newWM, nil
}

// openCodeV2Text returns the curated text of one session_message row. Assistant
// rows must carry a completion timestamp; their text parts are joined and
// reasoning/tool parts dropped. User rows expose text directly.
func openCodeV2Text(role, data string) (string, bool) {
	var m struct {
		Text string `json:"text"`
		Time struct {
			Completed int64 `json:"completed"`
		} `json:"time"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal([]byte(data), &m) != nil {
		return "", false
	}
	if role == "assistant" {
		if m.Time.Completed == 0 {
			return "", false
		}
		var parts []string
		for _, c := range m.Content {
			if c.Type == "text" && strings.TrimSpace(c.Text) != "" {
				parts = append(parts, strings.TrimSpace(c.Text))
			}
		}
		return strings.Join(parts, "\n\n"), true
	}
	return strings.TrimSpace(m.Text), true
}

// countOpenCodeV2 counts the curated messages loadOpenCodeV2 would emit: user
// rows with text plus completed assistant rows carrying at least one text part.
func countOpenCodeV2(db *sql.DB, project *string) (int64, error) {
	q := `
		SELECT COUNT(*)
		FROM session_message m
		JOIN session_v2 s ON s.id = m.session_id
		WHERE m.type IN ('user','assistant')
		  AND (
			(m.type = 'user' AND COALESCE(json_extract(m.data,'$.text'),'') != '')
			OR (m.type = 'assistant'
				AND json_extract(m.data,'$.time.completed') IS NOT NULL
				AND EXISTS (
					SELECT 1 FROM json_each(json_extract(m.data,'$.content')) je
					WHERE json_extract(je.value,'$.type') = 'text'
					  AND COALESCE(json_extract(je.value,'$.text'),'') != ''
				))
		  )`
	args := []any{}
	if project != nil {
		q += ` AND s.directory LIKE ?`
		args = append(args, "%"+*project+"%")
	}
	var n int64
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}
