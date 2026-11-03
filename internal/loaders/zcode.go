package loaders

import (
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// zcodeTextPart mirrors the JSON stored in part.data for a rendered text part.
type zcodeTextPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
	Time struct {
		Start int64 `json:"start"`
		End   int64 `json:"end"`
	} `json:"time"`
}

// zcodeMessageJSON is the JSON in message.data: role plus the working
// directory the turn ran in. Semantics carries the source's own classification
// of who produced the turn.
type zcodeMessageJSON struct {
	Role      string `json:"role"`
	Semantics struct {
		Origin string `json:"origin"`
	} `json:"semantics"`
	Time struct {
		Created   int64 `json:"created"`
		Completed int64 `json:"completed"`
	} `json:"time"`
	Path struct {
		Cwd  string `json:"cwd"`
		Root string `json:"root"`
	} `json:"path"`
}

// zcodeSyntheticUserOrigin reports whether the source itself classifies a user
// row as runtime output rather than something the human typed. The list is
// negative on purpose: an unknown origin is ingested, so a future shape of
// real user input is never silently dropped.
func zcodeSyntheticUserOrigin(origin string) bool {
	switch origin {
	case "agent_runtime", "system":
		return true
	}
	return false
}

// Only assistant messages carry `path.cwd`, so the session's own directory is
// selected as the fallback that gives user turns their project.
const zcodeCuratedQuery = `
	SELECT m.session_id,
	       m.sequence,
	       p.sequence,
	       m.time_created,
	       m.time_updated,
	       m.data,
	       p.data,
	       COALESCE(s.directory, '')
	FROM part p
	JOIN message m ON p.message_id = m.id
	LEFT JOIN session s ON s.id = m.session_id
	WHERE m.time_updated >= ?
	ORDER BY m.session_id, m.sequence, p.sequence`

// NewZCodeLoader returns a loader over ZCode's SQLite store.
func NewZCodeLoader(path string) Loader {
	return funcLoader{
		name: "zcode",
		path: path,
		fn: func(p string, project *string, wm string) ([]Message, string, error) {
			return loadZCode(p, project, wm)
		},
		count: countZCode,
	}
}

// loadZCode streams curated ZCode text parts. ZCode is a fork of the OpenCode
// layout: `session` → `message` → `part`, with each row's payload in a JSON
// column.
//
// Curation rules, chosen for the same reasons as the OpenCode loader:
//   - only `part.data.type == "text"` with non-empty text becomes a message
//     (tool output, step markers, and reasoning are dropped);
//   - assistant turns are emitted only once `message.data.time.completed` is
//     set, so a turn captured mid-stream is never ingested as a partial
//     message — it is simply skipped until it completes;
//   - the cursor is the maximum `message.time_updated`, not `time_created`,
//     because ZCode bumps `time_updated` when a streaming turn completes.
//     That is what lets a skipped in-flight turn be picked up on a later sync
//     instead of being lost behind the watermark;
//   - the comparison is inclusive (`>=`): re-emitting the record sitting on the
//     boundary is free because exact content dedup absorbs it downstream;
//   - a user row whose `semantics.origin` is `agent_runtime` or `system` is
//     runtime output, not user speech: ZCode injects todo reminders, task
//     notifications, and compact summaries into the session and marks them
//     model-only/hidden. They are skipped, because a headline that reads "The
//     TodoWrite tool hasn't been used recently…" is not a request. Assistant
//     rows keep the origin untouched — the model speaking through the runtime
//     is the normal case for assistant turns.
func loadZCode(path string, project *string, watermark string) ([]Message, string, error) {
	db, err := openReadOnly(path)
	if err != nil {
		return nil, watermark, err
	}
	defer db.Close()

	threshold, _ := strconv.ParseInt(watermark, 10, 64)
	rows, err := db.Query(zcodeCuratedQuery, threshold)
	if err != nil {
		return nil, watermark, err
	}
	defer rows.Close()

	var msgs []Message
	var maxUpdated int64 = threshold
	for rows.Next() {
		var sessionID string
		var msgSeq, partSeq sql.NullInt64
		var created, updated int64
		var msgData, partData, sessionDir string
		if err := rows.Scan(&sessionID, &msgSeq, &partSeq, &created, &updated, &msgData, &partData, &sessionDir); err != nil {
			return nil, watermark, err
		}

		var part zcodeTextPart
		if json.Unmarshal([]byte(partData), &part) != nil || part.Type != "text" {
			continue
		}
		text := strings.TrimSpace(part.Text)
		if text == "" {
			continue
		}

		var meta zcodeMessageJSON
		if json.Unmarshal([]byte(msgData), &meta) != nil {
			continue
		}
		if meta.Role != "user" && meta.Role != "assistant" {
			continue
		}
		if meta.Role == "user" && zcodeSyntheticUserOrigin(meta.Semantics.Origin) {
			continue // runtime injection (todo reminder, notification, summary)
		}
		if meta.Role == "assistant" && meta.Time.Completed == 0 {
			continue // still streaming: retried on a later sync
		}

		ts := created
		if meta.Time.Created > 0 {
			ts = meta.Time.Created
		}
		if updated > maxUpdated {
			maxUpdated = updated
		}

		msgs = append(msgs, Message{
			Tool:           "zcode",
			SessionID:      sessionID,
			ProjectDir:     firstNonEmptyString(meta.Path.Cwd, meta.Path.Root, sessionDir),
			TS:             time.UnixMilli(ts).UTC(),
			Role:           meta.Role,
			Content:        text,
			SourceLocation: path,
			// message.sequence reaches the hundreds and part.sequence the tens,
			// so this stays monotonic within a session.
			SourceOrder: msgSeq.Int64*100 + partSeq.Int64,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, watermark, err
	}

	newWM := watermark
	if maxUpdated > threshold {
		newWM = strconv.FormatInt(maxUpdated, 10)
	}
	if newWM == "0" {
		newWM = ""
	}
	return msgs, newWM, nil
}

// countZCode counts curated text parts without deserializing their content.
// It is an upper bound on what a full scan emits, keeping assess's
// Total >= New invariant.
func countZCode(path string, _ *string) (int64, error) {
	db, err := openReadOnly(path)
	if err != nil {
		return 0, err
	}
	defer db.Close()

	var n int64
	err = db.QueryRow(`
		SELECT COUNT(*)
		FROM part p
		JOIN message m ON p.message_id = m.id
		WHERE json_extract(p.data, '$.type') = 'text'
		  AND COALESCE(json_extract(p.data, '$.text'), '') <> ''
		  AND json_extract(m.data, '$.role') IN ('user', 'assistant')`).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}
