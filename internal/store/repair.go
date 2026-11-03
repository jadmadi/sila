package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jadmadi/sila/internal/loaders"
)

// messageTools returns the distinct tools recorded on a message's occurrences.
func messageTools(tx *sql.Tx, id int64) ([]string, error) {
	rows, err := tx.Query(`SELECT DISTINCT tool FROM source_occurrences WHERE message_id = ? ORDER BY tool`, id)
	if err != nil {
		return nil, fmt.Errorf("read tools of message %d: %w", id, err)
	}
	defer rows.Close()
	var tools []string
	for rows.Next() {
		var tool string
		if err := rows.Scan(&tool); err != nil {
			return nil, fmt.Errorf("scan tool of message %d: %w", id, err)
		}
		tools = append(tools, tool)
	}
	return tools, rows.Err()
}

// appendSourceTools extends a survivor's source_tools JSON array with tools it
// does not list yet, the same merge ingest performs for a repeated record. A
// rewrite that merges a duplicate from another tool must not leave the tool
// attribution behind, or tool filters would miss the moved occurrence.
func appendSourceTools(tx *sql.Tx, id int64, tools []string) error {
	if len(tools) == 0 {
		return nil
	}
	var raw string
	if err := tx.QueryRow(`SELECT source_tools FROM messages WHERE id = ?`, id).Scan(&raw); err != nil {
		return fmt.Errorf("read source tools of message %d: %w", id, err)
	}
	// A value that does not parse as a tool array is replaced rather than
	// trusted: only ingest writes this column, so malformed JSON means the row
	// was tampered with, and failing the migration over one corrupt column
	// would block the store over attribution that can be rebuilt from
	// occurrences.
	var existing []string
	if err := json.Unmarshal([]byte(raw), &existing); err != nil {
		existing = nil
	}
	changed := false
	for _, tool := range tools {
		found := slices.Contains(existing, tool)
		if !found {
			existing = append(existing, tool)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	encoded, err := json.Marshal(existing)
	if err != nil {
		return fmt.Errorf("encode source tools of message %d: %w", id, err)
	}
	if _, err := tx.Exec(`UPDATE messages SET source_tools = ? WHERE id = ?`, string(encoded), id); err != nil {
		return fmt.Errorf("update source tools of message %d: %w", id, err)
	}
	return nil
}

// deleteStoredMessage removes a message and its source occurrences. The
// messages delete trigger clears its messages_fts entry.
func deleteStoredMessage(tx *sql.Tx, id int64) error {
	if _, err := tx.Exec(`DELETE FROM source_occurrences WHERE message_id = ?`, id); err != nil {
		return fmt.Errorf("delete occurrences of message %d: %w", id, err)
	}
	if _, err := tx.Exec(`DELETE FROM messages WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete message %d: %w", id, err)
	}
	return nil
}

// rewriteMessageContent replaces a stored message's content with the text the
// loader emits today. When another row already holds the new content hash, this
// row is the duplicate: its occurrences move to the surviving row and it is
// deleted. Otherwise the row is rewritten in place and messages_fts is
// maintained explicitly, because the table has insert and delete triggers but
// no update trigger.
func rewriteMessageContent(tx *sql.Tx, id int64, projectID, role, clean string) error {
	newHash := ContentHash(role, clean)
	var survivor int64
	err := tx.QueryRow(`SELECT id FROM messages WHERE project_id = ? AND content_hash = ?`,
		projectID, newHash).Scan(&survivor)
	switch {
	case err == nil && survivor != id:
		tools, err := messageTools(tx, id)
		if err != nil {
			return err
		}
		if err := appendSourceTools(tx, survivor, tools); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE source_occurrences SET message_id = ? WHERE message_id = ?`,
			survivor, id); err != nil {
			return fmt.Errorf("re-point occurrences of message %d: %w", id, err)
		}
		if _, err := tx.Exec(`DELETE FROM messages WHERE id = ?`, id); err != nil {
			return fmt.Errorf("delete duplicate message %d: %w", id, err)
		}
	case err == nil:
		// Same row, already carrying the clean hash: nothing to do.
	case err == sql.ErrNoRows:
		if _, err := tx.Exec(`UPDATE messages SET content = ?, content_hash = ? WHERE id = ?`,
			clean, newHash, id); err != nil {
			return fmt.Errorf("rewrite message %d: %w", id, err)
		}
		if _, err := tx.Exec(`DELETE FROM messages_fts WHERE rowid = ?`, id); err != nil {
			return fmt.Errorf("reindex message %d (delete): %w", id, err)
		}
		if _, err := tx.Exec(`INSERT INTO messages_fts(rowid, project_id, role, content) VALUES (?, ?, ?, ?)`,
			id, projectID, role, clean); err != nil {
			return fmt.Errorf("reindex message %d (insert): %w", id, err)
		}
	default:
		return fmt.Errorf("look up rewritten message %d: %w", id, err)
	}
	return nil
}

// repairZCodeRuntimeTurns deletes stored zcode user turns that the harness
// generated rather than the human typed.
//
// ZCode classifies every user row: `semantics.origin` is `real_user` for what
// the human typed and `agent_runtime`/`system` for injected turns (todo
// reminders, task notifications, compact summaries), which the harness also
// marks model-only and hidden. sila's loader read only `$.role`, so 72% of the
// stored zcode user rows were runtime injections. The loader now skips them,
// but the rows already stored keep a user role and win the earliest-match
// tie-break that the activity view uses, so a session whose real prompt
// predates a `--since` window can headline "The TodoWrite tool hasn't been
// used recently…". Unlike the AGY envelope there is no human text inside a
// reminder to recover, so the repair deletes rather than rewrites.
//
// Safety: a row is deleted only when its role is user, its content matches the
// tight inventory, and every occurrence is zcode. A row whose text is shared
// with another tool through content-hash dedup is another tool's data too, so
// it stays. Occurrences are deleted first and the message delete trigger clears
// its messages_fts entry.
//
// Residual risk, measured zero at repair time: the guard keys on content
// because the stored row does not keep `semantics.origin`. A real_user turn
// whose text happened to start with "The TodoWrite tool hasn" or contain
// "<task-notification>" would match; none of the 89 real_user source rows did.
func repairZCodeRuntimeTurns(tx *sql.Tx) error {
	rows, err := tx.Query(`
		SELECT m.id
		FROM messages m
		WHERE m.role = 'user'
		  AND (m.content LIKE 'The TodoWrite tool hasn%' OR m.content LIKE '%<task-notification>%')
		  AND EXISTS (
			SELECT 1 FROM source_occurrences o
			WHERE o.message_id = m.id AND o.tool = 'zcode')
		  AND NOT EXISTS (
			SELECT 1 FROM source_occurrences o
			WHERE o.message_id = m.id AND o.tool <> 'zcode')
		ORDER BY m.id`)
	if err != nil {
		return fmt.Errorf("find zcode runtime turns: %w", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("scan zcode runtime turn: %w", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, id := range ids {
		if err := deleteStoredMessage(tx, id); err != nil {
			return err
		}
	}
	return nil
}

// repairAGYUserEnvelopes rewrites stored agy user turns that still carry the
// harness envelope Antigravity records around them.
//
// The AGY loader stored the whole recorded record, so a turn read like
// "<USER_REQUEST> … </USER_REQUEST> <ADDITIONAL_METADATA> …". The loader now
// unwraps it, but a corrected loader only fixes future ingests: the wrapped
// rows already in the store keep their content hash, so a re-ingest adds a
// cleaned copy beside them instead of replacing them, and consumers that pick
// the oldest match (the activity view does) would keep showing the envelope.
// This runs once, inside the migration transaction.
//
// Content is the same text the loader would emit today, so it uses the loader's
// own unwrapper rather than a second copy of the rule.
//
// Safety: a stripped message whose content hash already exists in the same
// project is a duplicate of a cleaner row, so its occurrences are re-pointed to
// the surviving message and the wrapped row is deleted (the messages delete
// trigger clears its FTS entry). Updates maintain messages_fts explicitly,
// because the table has insert and delete triggers but no update trigger.
func repairAGYUserEnvelopes(tx *sql.Tx) error {
	rows, err := tx.Query(`
		SELECT DISTINCT m.id, m.project_id, m.role, m.content
		FROM messages m
		JOIN source_occurrences o ON o.message_id = m.id
		WHERE o.tool = 'agy'
		  AND m.role = 'user'
		  AND instr(m.content, '<USER_REQUEST>') > 0
		ORDER BY m.id`)
	if err != nil {
		return fmt.Errorf("find enveloped agy turns: %w", err)
	}
	type candidate struct {
		id        int64
		projectID string
		role      string
		content   string
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.projectID, &c.role, &c.content); err != nil {
			rows.Close()
			return fmt.Errorf("scan enveloped agy turn: %w", err)
		}
		candidates = append(candidates, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, c := range candidates {
		stripped := loaders.StripAGYUserEnvelope(c.content)
		if stripped == c.content {
			continue
		}
		if err := rewriteMessageContent(tx, c.id, c.projectID, c.role, stripped); err != nil {
			return err
		}
	}
	return nil
}

// repairAGYAssistantEnvelopes rewrites stored agy assistant turns that still
// carry the timestamp header Antigravity records at the start of every turn,
// and the pending claims copied from them.
//
// The AGY loader stored the whole recorded record, so an assistant turn read
// "Created At: …\nCompleted At: …\n<the model's output>". The loader now strips
// it, but a corrected loader only fixes future ingests: the wrapped rows
// already in the store keep their content hash, so a re-ingest adds a cleaned
// copy beside them instead of replacing them, and consumers that pick the
// oldest match (the activity view does) would keep showing the header. The
// header also reached the claim corpus, where a pending code_claim is a
// verbatim copy of the message it was taken from, so the envelope was being
// promoted as knowledge. This runs once, inside the migration transaction.
//
// Content is the same text the loader would emit today, so it uses the loader's
// own stripper rather than a second copy of the rule.
//
// Safety: matching requires an agy-sourced row and a parseable RFC3339 stamp
// after the prefix, both inside the shared stripper, so a turn a human began
// with the words "Created At:" is kept. Sourcing is read from source_tools
// rather than a join on source_occurrences, because a row whose occurrences
// were re-pointed to a surviving duplicate by an earlier repair keeps its tool
// attribution in source_tools while losing every occurrence row — a join would
// skip it and leave the header in place. A stripped message whose content hash
// already exists in the same project is a duplicate of a cleaner row, so its
// occurrences are re-pointed to the surviving message and the wrapped row is
// deleted (the messages delete trigger clears its FTS entry). Updates maintain
// messages_fts explicitly, because the table has insert and delete triggers but
// no update trigger. Claims are scoped to agy contributors and to pending
// code_claims, the states where the claim is the message text; the facts_fts
// update trigger keeps the index in step. A claim that strips to a duplicate
// of another claim in the same project is deleted rather than left as a
// content-copy, since nothing downstream merges claims by text.
func repairAGYAssistantEnvelopes(tx *sql.Tx) error {
	rows, err := tx.Query(`
		SELECT DISTINCT m.id, m.project_id, m.role, m.content
		FROM messages m
		WHERE m.source_tools LIKE '%agy%'
		  AND m.role = 'assistant'
		  AND m.content LIKE 'Created At: %'
		ORDER BY m.id`)
	if err != nil {
		return fmt.Errorf("find agy assistant envelopes: %w", err)
	}
	type candidate struct {
		id        int64
		projectID string
		role      string
		content   string
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.projectID, &c.role, &c.content); err != nil {
			rows.Close()
			return fmt.Errorf("scan agy assistant envelope: %w", err)
		}
		candidates = append(candidates, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, c := range candidates {
		stripped := loaders.StripAGYAssistantEnvelope(c.content)
		if stripped == c.content {
			continue
		}
		if err := rewriteMessageContent(tx, c.id, c.projectID, c.role, stripped); err != nil {
			return err
		}
	}

	// A pending code_claim is the message text verbatim, so the envelope it
	// carries is the same one just stripped from messages. Rewrite the claim
	// to the cleaned text; a claim left with a duplicate of another claim's
	// text in the same project is deleted, since nothing merges claims by
	// text and a duplicate would surface twice in query results.
	crows, err := tx.Query(`
		SELECT f.id, f.project_id, f.claim
		FROM facts f
		WHERE f.claim LIKE 'Created At: %'
		  AND f.kind = 'code_claim'
		  AND f.distill_status = 'pending'
		  AND EXISTS (
			SELECT 1 FROM json_each(f.tools) WHERE value = 'agy')
		ORDER BY f.id`)
	if err != nil {
		return fmt.Errorf("find enveloped agy claims: %w", err)
	}
	type claimCandidate struct {
		id        string
		projectID string
		claim     string
	}
	var claims []claimCandidate
	for crows.Next() {
		var c claimCandidate
		if err := crows.Scan(&c.id, &c.projectID, &c.claim); err != nil {
			crows.Close()
			return fmt.Errorf("scan enveloped agy claim: %w", err)
		}
		claims = append(claims, c)
	}
	crows.Close()
	if err := crows.Err(); err != nil {
		return err
	}

	for _, c := range claims {
		stripped := loaders.StripAGYAssistantEnvelope(c.claim)
		if stripped == c.claim {
			continue
		}
		var dup string
		err := tx.QueryRow(`
			SELECT f2.id FROM facts f2
			WHERE f2.project_id = ? AND f2.claim = ? AND f2.id <> ?
			LIMIT 1`, c.projectID, stripped, c.id).Scan(&dup)
		switch err {
		case nil:
			if _, err := tx.Exec(`DELETE FROM facts WHERE id = ?`, c.id); err != nil {
				return fmt.Errorf("delete duplicate claim %s: %w", c.id, err)
			}
		case sql.ErrNoRows:
			if _, err := tx.Exec(`UPDATE facts SET claim = ? WHERE id = ?`, stripped, c.id); err != nil {
				return fmt.Errorf("rewrite claim %s: %w", c.id, err)
			}
		default:
			return fmt.Errorf("check duplicate claim %s: %w", c.id, err)
		}
	}
	return nil
}

// miMoCodeChildSessions records the MiMoCode sessions the source created with
// a parent — checkpoint-writer runs — measured on the source store on
// 2026-09-17, before the loader learned to skip non-main actors. The store
// keeps neither the actor nor the parent relationship, so the repair carries
// the snapshot; a child session created after this snapshot is not matched.
var miMoCodeChildSessions = []string{
	"ses_-ffe5fc5d406a8ffex7S1z5WWL",
	"ses_-ffe5fc5ded2dfffe4HIr7C1pp",
	"ses_-ffe5fc601dc23ffeFUcu1MYKF",
	"ses_-ffe5fc61a8dc9ffe70P2jdRtU",
	"ses_-ffe5fc6bab04effepVzFb9rsT",
	"ses_-ffe5fc6bcc43effevdFUz5Jt2",
	"ses_-ffe5fc8beebe7ffepvoDfRl2I",
	"ses_-ffe5fc8c9c3fcffeyNQz113I8",
	"ses_-ffe5fc8ce8f37ffeSWtYDalTC",
	"ses_-ffe5fc8d5d503ffeORbDGPBhc",
	"ses_-ffe5fc8ded700ffedecNensxr",
	"ses_-ffe5fc8e444e5ffe6pMfQAM6V",
	"ses_-ffe5fc8e649bbffeXqMyVv5X1",
	"ses_-ffe5fcaebf1cfffe82VmGiHZR",
	"ses_-ffe5fcaf1f8b8ffe8PG0ljCnt",
	"ses_-ffe5fcaf9656dffeu9OhvAKhY",
	"ses_-ffe5fcafaf66effeAczvOPjZ1",
	"ses_-ffe5fcb07f7fbffePeIBuMZgX",
	"ses_-ffe5fcb13ea0dffe7eYusRh2o",
	"ses_-ffe5fcb1c2c7fffe4SSJ5M6Z9",
	"ses_-ffe5fcb1dab82ffeGnd55Bpm3",
	"ses_-ffe5fcb27d592ffe38J137al4",
	"ses_000a6f47dffeifSIKOwrW3O1qQ",
	"ses_000a8e888ffe2qMfYSQgYUSk9C",
	"ses_000b20910ffeqq6zO5bqFAbv0P",
	"ses_000dd7a29ffeTCCQJ1UrUo2obA",
	"ses_000de1589ffeCwpz7NI9dEgZbu",
	"ses_000f04191ffeFzhACQLBI0ME9J",
	"ses_000f5a82cffe6MaUFAOPUoDUm0",
	"ses_0036142afffeer3HF6V1ro06WE",
	"ses_003aa9e3dffe06BQWUUkd6qBbS",
	"ses_00440fa98ffeU6RyB2AN8hcnWz",
	"ses_00442ea50ffeEhi1qlKr1EZcpr",
	"ses_0045ba395ffe5b2130Wm3xEVW2",
	"ses_0045d092fffeb9Nq9f3jptYRrp",
	"ses_00479f457ffeLgbtvbd9iSFfkE",
	"ses_004808b4bffef9IKDI0uSWWTcV",
	"ses_0048bed9effeVoHulKdPIbbW24",
	"ses_004cbe1e4ffeacoIpjyopnKu0P",
	"ses_0061d3b97ffed06NTLEQauykqt",
	"ses_0061ead3effe6P14rY7spEOsoZ",
	"ses_0062af588ffeJEtWjpVMoQNN2G",
	"ses_0062dbad0ffe4bBPIjer207PCT",
	"ses_006b1f55cffej8Zs4raCGCHuoN",
	"ses_006c33cc3ffe5HDJnhgQaV67yz",
	"ses_006de46e2ffexXu7Rbkk0lVzJR",
	"ses_00710f288ffeoCdi2mpds589aF",
	"ses_009fd74cbffeWRG8RxrcOFB0KP",
	"ses_00a0048abffeUT4eefA8Ktl60P",
	"ses_00a009a35ffe2GV4QzPwQA9IaY",
	"ses_00a113b0affeaF3kx1fwYJ3PXP",
	"ses_00a13f70effea42bIF1Bq4L4Kh",
	"ses_00b4b63b9ffeg9j1yuZN8TN4BS",
	"ses_00b4d57c7ffeuopEmX6KdgZ8RA",
	"ses_00b5315b3ffe7mv05t59sX780F",
	"ses_00b6b013cffeJPtVVS1KX8bjTZ",
	"ses_00b6e4b34ffeDAZO9hnrrHib0P",
	"ses_00b788c86ffeZlJ1c54RuezHoi",
	"ses_00b7e712bffemck3cWAG0WD6vh",
	"ses_00b8e65cfffeVE0i74PbNNuzV7",
	"ses_00b91af21ffeYfi4vMIMntxv4V",
	"ses_00b9df0fdffemhDE4W2Dwv769A",
	"ses_00b9ee160ffejVusNaiLt1yIQQ",
	"ses_00bcfafbaffei843g3PL6MIzNK",
	"ses_00be3642dffe7IIG35qY0BccEg",
	"ses_00be43d50ffehrD8dV1YhlzGDh",
	"ses_00be60770ffeB4zqfWg9w2XfeW",
	"ses_00be776d9ffeiMDDIvjsGDLrrd",
	"ses_00be89a2affeE5iX5z2JpwYsgk",
	"ses_00bee1595ffeQUaUXP99cn8FFv",
	"ses_00c2ad959ffepotHWBZdHmWXE2",
	"ses_00d5afba6ffedW0fkr6OsaHF6R",
	"ses_00d621564ffezsAQPJcpK8BI05",
	"ses_00d7297aeffe7VKrWF0Tq15sEP",
	"ses_00d73adc4ffe7H12KyLl74mnRO",
	"ses_00d862318ffeBb943YU7P93Zd4",
	"ses_00d899579ffe54WF21GSsZlmmF",
	"ses_00d8b3724ffesUaZkjNj0hA3SK",
	"ses_00d8cfec1ffelVC0SOgPhv74Tr",
	"ses_036f23fb2ffeZCyeRRbMKh6tP1",
	"ses_036f36848ffeANsZOPp5dZhOpn",
	"ses_036f51232ffeXd1fpJSSzfZK5l",
	"ses_036f69aeeffemRLvtP0dnpMw1U",
	"ses_fef76ca6fffeCrgX6vMG0pXLxS",
	"ses_fefe0ec99ffePSntX2Eb0wf15i",
	"ses_fefe20decffeeUjuEo9tpt4gRB",
	"ses_fefeabbeaffeIgWnU5FUoEC8pA",
	"ses_ff03e8761ffe9l4dtqSdOFCdPT",
	"ses_ff03fb2aaffeSuy5iM7s72MR9O",
	"ses_ff6586c55ffe5e7UEtVtxAj2eI",
	"ses_ff65d4e86ffeUjwoM8yFdRDZ3e",
	"ses_ff8b2f2f8ffep1XrE9SELQ4Svc",
	"ses_ff8cb76caffeogW0iM0Eu502zw",
	"ses_ff8d53b96ffe79TbRtqNw37AJ8",
	"ses_ff93e507dffeS0VLXZgAbdjH7Y",
	"ses_ff93fc9f2ffeoP60qr8l4xK8l5",
	"ses_ff98a4776ffefo5Y4WghaWwond",
	"ses_ff9938fa6ffejfzyeI8so3u6WQ",
	"ses_ff9fdea3cffeiRsdhlGvB78Z5S",
	"ses_ff9fed4b0ffeLx6WiOomoo5Tko",
	"ses_ffa01806effe1Z1Z4WTVbE8qFH",
	"ses_ffdf086beffeUhwrMAFCHWvd1g",
	"ses_ffe3751f0ffeGegcB2sfoe5cwK",
	"ses_ffe95008cffe5m9VZK37JKqW4n",
}

// miMoCodeSyntheticMarkers are the opening strings of the harness-generated
// text parts in that same source: reminders, skill documents, actor
// notifications, rendered tool output, and plan-mode notes. Measured: none of
// them occurs in a non-synthetic text part of a root session, so they mark
// where stored user text turns into scaffolding.
var miMoCodeSyntheticMarkers = []string{
	"<system-reminder>",
	"<skill_content",
	"<actor-notification>",
	"The following tool was executed by the user",
	"Called the Read tool with the following input:",
	"Plan mode is active.",
	"Skills available in this session:",
}

// repairMiMoCodeRuntimeTurns deletes stored mimocode turns that came from
// sub-agent actors and strips the harness text the old loader concatenated into
// main-session user messages.
//
// The old loader read only `$.role`, so it ingested the checkpoint-writer
// sessions (104 at measurement, 1,234 stored messages) and the per-message
// runtime prompts and notifications the source marks `synthetic`. The loader
// now skips non-main actors and synthetic parts, but the stored rows keep their
// content hash, so a corrected re-ingest adds clean rows beside them and the
// old scaffolding keeps winning search and claiming headlines.
//
// Safety: a message is touched only when every occurrence is mimocode. Child
// sessions come from the recorded list, and the main-session boundary comes
// from markers no human text in a root session contains. A message whose text
// before the first marker is empty is deleted with its occurrences; a mixed
// message is rewritten to that text and reindexed.
//
// Residual: the clean text is trimmed at the marker, while the loader emits
// parts verbatim, so a later `--full` re-ingest of a rewritten message can add
// a whitespace variant of the same text.
func repairMiMoCodeRuntimeTurns(tx *sql.Tx, childSessions, markers []string) error {
	if len(childSessions) > 0 {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(childSessions)), ",")
		args := make([]any, 0, len(childSessions))
		for _, s := range childSessions {
			args = append(args, s)
		}
		rows, err := tx.Query(`
			SELECT DISTINCT m.id
			FROM messages m
			JOIN source_occurrences o ON o.message_id = m.id AND o.tool = 'mimocode'
			WHERE NOT EXISTS (
				SELECT 1 FROM source_occurrences x
				WHERE x.message_id = m.id AND x.tool <> 'mimocode')
			  AND NOT EXISTS (
				SELECT 1 FROM source_occurrences y
				WHERE y.message_id = m.id AND y.session_id NOT IN (`+placeholders+`))
			ORDER BY m.id`, args...)
		if err != nil {
			return fmt.Errorf("find mimocode child-session turns: %w", err)
		}
		var ids []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return fmt.Errorf("scan mimocode child-session turn: %w", err)
			}
			ids = append(ids, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, id := range ids {
			if err := deleteStoredMessage(tx, id); err != nil {
				return err
			}
		}
	}

	if len(markers) == 0 {
		return nil
	}
	conditions := make([]string, 0, len(markers))
	args := make([]any, 0, len(markers))
	for _, m := range markers {
		conditions = append(conditions, "instr(m.content, ?) > 0")
		args = append(args, m)
	}
	rows, err := tx.Query(`
		SELECT DISTINCT m.id, m.project_id, m.role, m.content
		FROM messages m
		JOIN source_occurrences o ON o.message_id = m.id AND o.tool = 'mimocode'
		WHERE m.role = 'user'
		  AND NOT EXISTS (
			SELECT 1 FROM source_occurrences x
			WHERE x.message_id = m.id AND x.tool <> 'mimocode')
		  AND (`+strings.Join(conditions, " OR ")+`)
		ORDER BY m.id`, args...)
	if err != nil {
		return fmt.Errorf("find mimocode synthetic turns: %w", err)
	}
	type candidate struct {
		id        int64
		projectID string
		role      string
		content   string
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.projectID, &c.role, &c.content); err != nil {
			rows.Close()
			return fmt.Errorf("scan mimocode synthetic turn: %w", err)
		}
		candidates = append(candidates, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, c := range candidates {
		cut := len(c.content)
		for _, marker := range markers {
			if i := strings.Index(c.content, marker); i >= 0 && i < cut {
				cut = i
			}
		}
		clean := strings.TrimSpace(c.content[:cut])
		if clean == c.content {
			continue
		}
		if clean == "" {
			if err := deleteStoredMessage(tx, c.id); err != nil {
				return err
			}
			continue
		}
		if err := rewriteMessageContent(tx, c.id, c.projectID, c.role, clean); err != nil {
			return err
		}
	}
	return nil
}

type projectEntry struct {
	id   string
	path string
}

// repairWorktreeProjects canonicalizes project registrations that were created
// for temporary or detached Git worktrees, merging their messages, occurrences,
// facts, docs, handoffs, and memos into the main repository's project row.
func repairWorktreeProjects(tx *sql.Tx) error {
	rows, err := tx.Query(`SELECT id, path FROM projects ORDER BY created_at ASC`)
	if err != nil {
		return fmt.Errorf("query projects for worktree repair: %w", err)
	}
	var allProjs []projectEntry
	for rows.Next() {
		var p projectEntry
		if err := rows.Scan(&p.id, &p.path); err != nil {
			rows.Close()
			return fmt.Errorf("scan project for worktree repair: %w", err)
		}
		allProjs = append(allProjs, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, p := range allProjs {
		canon := resolveProjectCanonicalPath(p.path, allProjs)
		if canon == p.path {
			continue
		}

		// Check if a project already exists for the canonical path
		var targetID string
		err := tx.QueryRow(`SELECT id FROM projects WHERE path = ?`, canon).Scan(&targetID)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("check canonical project %s: %w", canon, err)
		}

		if err == sql.ErrNoRows {
			// No canonical project exists yet: update this project's path and ID in place
			baseID := ProjectID(canon)
			hash := pathHash8(canon)
			targetID = baseID
			for i := 0; ; i++ {
				cand := baseID
				switch {
				case i == 0:
					cand = baseID
				case i == 1:
					cand = baseID + "-" + hash
				default:
					cand = fmt.Sprintf("%s-%s-%d", baseID, hash, i)
				}
				var owner string
				checkErr := tx.QueryRow(`SELECT path FROM projects WHERE id = ?`, cand).Scan(&owner)
				if checkErr == sql.ErrNoRows || cand == p.id {
					targetID = cand
					break
				}
				if checkErr != nil {
					return fmt.Errorf("check project id candidate: %w", checkErr)
				}
			}

			if _, err := tx.Exec(`UPDATE projects SET path = ?, id = ? WHERE path = ?`, canon, targetID, p.path); err != nil {
				return fmt.Errorf("update project %s to canonical %s: %w", p.path, canon, err)
			}
		}

		if targetID != p.id {
			if mergeErr := mergeProjectData(tx, p.id, targetID); mergeErr != nil {
				return fmt.Errorf("merge project %s into %s: %w", p.id, targetID, mergeErr)
			}
		}
		if err != sql.ErrNoRows {
			// Canonical project already exists: remove duplicate worktree row after moving its child rows
			if _, delErr := tx.Exec(`DELETE FROM projects WHERE path = ?`, p.path); delErr != nil {
				return fmt.Errorf("delete duplicate worktree project %s: %w", p.path, delErr)
			}
		}
	}

	return nil
}

func resolveProjectCanonicalPath(path string, allProjects []projectEntry) string {
	canon := CanonicalProjectPath(path)
	if canon != path {
		return canon
	}
	// Check Antigravity / Gemini worktree paths: ~/.gemini/antigravity/worktrees/<RepoName>/...
	for _, marker := range []string{"/.gemini/antigravity/worktrees/", "/.antigravity/worktrees/"} {
		if _, after, ok := strings.Cut(path, marker); ok {
			rest := after
			parts := strings.Split(rest, "/")
			if len(parts) >= 1 && parts[0] != "" {
				repoName := parts[0]
				for _, other := range allProjects {
					if other.path != path && (strings.EqualFold(other.id, repoName) || strings.EqualFold(filepath.Base(other.path), repoName)) {
						if fi, err := os.Stat(other.path); err == nil && fi.IsDir() {
							return other.path
						}
					}
				}
			}
		}
	}
	return path
}

func mergeProjectData(tx *sql.Tx, srcID, dstID string) error {
	// 1. Messages: Handle potential (dstID, content_hash) collisions
	type msgEntry struct {
		id          int64
		contentHash string
	}
	msgRows, err := tx.Query(`SELECT id, content_hash FROM messages WHERE project_id = ?`, srcID)
	if err != nil {
		return fmt.Errorf("query messages for merge: %w", err)
	}
	var msgs []msgEntry
	for msgRows.Next() {
		var m msgEntry
		if err := msgRows.Scan(&m.id, &m.contentHash); err != nil {
			msgRows.Close()
			return err
		}
		msgs = append(msgs, m)
	}
	msgRows.Close()
	if err := msgRows.Err(); err != nil {
		return err
	}

	for _, m := range msgs {
		var targetMsgID int64
		err := tx.QueryRow(`SELECT id FROM messages WHERE project_id = ? AND content_hash = ?`, dstID, m.contentHash).Scan(&targetMsgID)
		if err == nil {
			// Move occurrences to targetMsgID
			if _, err := tx.Exec(`UPDATE source_occurrences SET message_id = ? WHERE message_id = ?`, targetMsgID, m.id); err != nil {
				return fmt.Errorf("repoint occurrences: %w", err)
			}
			tools, _ := messageTools(tx, targetMsgID)
			_ = appendSourceTools(tx, targetMsgID, tools)
			_, _ = tx.Exec(`DELETE FROM messages_fts WHERE rowid = ?`, m.id)
			if _, err := tx.Exec(`DELETE FROM messages WHERE id = ?`, m.id); err != nil {
				return fmt.Errorf("delete merged message %d: %w", m.id, err)
			}
		} else if err == sql.ErrNoRows {
			// Repoint message
			if _, err := tx.Exec(`UPDATE messages SET project_id = ? WHERE id = ?`, dstID, m.id); err != nil {
				return fmt.Errorf("repoint message %d: %w", m.id, err)
			}
			_, _ = tx.Exec(`DELETE FROM messages_fts WHERE rowid = ?`, m.id)
			_, _ = tx.Exec(`INSERT INTO messages_fts(rowid, project_id, role, content) SELECT id, project_id, role, content FROM messages WHERE id = ?`, m.id)
		} else {
			return err
		}
	}

	// 2. Facts: Handle potential (dstID, claim) collisions
	type factEntry struct {
		id    string
		claim string
	}
	factRows, err := tx.Query(`SELECT id, claim FROM facts WHERE project_id = ?`, srcID)
	if err != nil {
		return fmt.Errorf("query facts for merge: %w", err)
	}
	var facts []factEntry
	for factRows.Next() {
		var f factEntry
		if err := factRows.Scan(&f.id, &f.claim); err != nil {
			factRows.Close()
			return err
		}
		facts = append(facts, f)
	}
	factRows.Close()
	if err := factRows.Err(); err != nil {
		return err
	}

	for _, f := range facts {
		var targetFactID string
		err := tx.QueryRow(`SELECT id FROM facts WHERE project_id = ? AND claim = ?`, dstID, f.claim).Scan(&targetFactID)
		if err == nil {
			_, _ = tx.Exec(`DELETE FROM facts_fts WHERE id = ?`, f.id)
			if _, err := tx.Exec(`DELETE FROM facts WHERE id = ?`, f.id); err != nil {
				return fmt.Errorf("delete merged fact %s: %w", f.id, err)
			}
		} else if err == sql.ErrNoRows {
			if _, err := tx.Exec(`UPDATE facts SET project_id = ? WHERE id = ?`, dstID, f.id); err != nil {
				return fmt.Errorf("repoint fact %s: %w", f.id, err)
			}
			_, _ = tx.Exec(`DELETE FROM facts_fts WHERE id = ?`, f.id)
			_, _ = tx.Exec(`INSERT INTO facts_fts(id, project_id, claim, repo_files, tools) SELECT id, project_id, claim, repo_files, tools FROM facts WHERE id = ?`, f.id)
		} else {
			return err
		}
	}

	// 3. Simple repoints for remaining project-scoped tables
	tables, _ := projectScopedTables(tx)
	for _, tbl := range tables {
		if tbl == "messages" || tbl == "facts" {
			continue
		}
		_, _ = tx.Exec(fmt.Sprintf(`UPDATE OR IGNORE %s SET project_id = ? WHERE project_id = ?`, tbl), dstID, srcID)
	}
	_, _ = tx.Exec(`UPDATE docs_fts SET project_id = ? WHERE project_id = ?`, dstID, srcID)
	_, _ = tx.Exec(
		`UPDATE OR IGNORE state SET key = substr(key, 1, length(key) - length(?)) || ? WHERE key = ? OR substr(key, -length(?) - 1) = ':' || ?`,
		srcID, dstID, srcID, srcID, srcID)
	_, _ = tx.Exec(`DELETE FROM state WHERE key = ? OR substr(key, -length(?) - 1) = ':' || ?`, srcID, srcID, srcID)

	return nil
}

// realDirectoryKey resolves a path to its real directory for identity checks.
// It returns "" when the path cannot be resolved, so an unresolvable or missing
// directory is never treated as a duplicate of another.
func realDirectoryKey(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return ""
	}
	return filepath.Clean(resolved)
}

// ProjectMergePair describes two registrations that resolve to the same real directory.
type ProjectMergePair struct {
	KeepID   string `json:"keep_id"`
	KeepPath string `json:"keep_path"`
	DropID   string `json:"drop_id"`
	DropPath string `json:"drop_path"`
}

// AliasDuplicatePairs groups registered projects by resolved real directory and
// returns one pair per duplicate, keeping the oldest registration.
func (s *Store) AliasDuplicatePairs() ([]ProjectMergePair, error) {
	rows, err := s.DB.Query(`SELECT id, path FROM projects ORDER BY created_at ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type entry struct {
		id   string
		path string
	}
	var entries []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.id, &e.path); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	groups := make(map[string][]entry)
	var order []string
	for _, e := range entries {
		if e.path == filepath.Join(os.Getenv("HOME"), "sila", "no-project") {
			continue
		}
		key := realDirectoryKey(e.path)
		if key == "" {
			continue
		}
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], e)
	}

	var pairs []ProjectMergePair
	for _, key := range order {
		g := groups[key]
		if len(g) < 2 {
			continue
		}
		// Keep the registration that already names the resolved real path when
		// one exists; otherwise keep the oldest.
		keep := g[0]
		for _, e := range g {
			if filepath.Clean(e.path) == key {
				keep = e
				break
			}
		}
		for _, drop := range g {
			if drop.id == keep.id {
				continue
			}
			pairs = append(pairs, ProjectMergePair{
				KeepID:   keep.id,
				KeepPath: keep.path,
				DropID:   drop.id,
				DropPath: drop.path,
			})
		}
	}
	return pairs, nil
}

// MergeProjects moves every record from dropID to keepID and removes the drop
// registration. Callers verify that the two paths name the same real directory
// (see AliasDuplicatePairs) or make the move an explicit decision.
func (s *Store) MergeProjects(keepID, dropID string) error {
	if keepID == "" || dropID == "" {
		return fmt.Errorf("keep and drop project ids are required")
	}
	if keepID == dropID {
		return nil
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM projects WHERE id IN (?,?)`, keepID, dropID).Scan(&count); err != nil {
		return err
	}
	if count != 2 {
		return fmt.Errorf("both project ids must exist (need %s and %s)", keepID, dropID)
	}
	if err := mergeProjectData(tx, dropID, keepID); err != nil {
		return fmt.Errorf("merge project %s into %s: %w", dropID, keepID, err)
	}
	if _, err := tx.Exec(`DELETE FROM projects WHERE id=?`, dropID); err != nil {
		return fmt.Errorf("delete merged project %s: %w", dropID, err)
	}
	return tx.Commit()
}

// MergeAliasDuplicates merges every pair that resolves to the same real
// directory and returns one human-readable line per merged pair.
func (s *Store) MergeAliasDuplicates() ([]string, error) {
	pairs, err := s.AliasDuplicatePairs()
	if err != nil {
		return nil, err
	}
	var merged []string
	for _, p := range pairs {
		if err := s.MergeProjects(p.KeepID, p.DropID); err != nil {
			return merged, fmt.Errorf("merge %s into %s: %w", p.DropID, p.KeepID, err)
		}
		merged = append(merged, fmt.Sprintf("Merged duplicate registration %s into %s (%s)", p.DropID, p.KeepID, p.KeepPath))
	}
	return merged, nil
}

// ProjectRename is the outcome of RenameProject. HashedFacts counts moved
// facts that carry a ledger hash: ComputeFactHash binds project_id, so those
// hashes no longer match until an explicit rebaseline, and the caller must
// surface that instead of letting the ledger look silently tampered.
type ProjectRename struct {
	Project     Project
	HashedFacts int
}

// RenameProject changes a registration's id in place, without moving data to
// another project. It is the correction for a stored id that no longer
// describes the directory — the live case: /workspace/sila/github registered as
// "example-client.com" because a stale workspace file in the directory named an
// unrelated project, so every fact written from that path landed in the wrong
// bucket. Merging is not an option there because the records belong to neither
// side of any merge; only the name is wrong.
//
// The id is the join key for every project-scoped row, so the rename moves the
// project-scoped tables, the FTS indexes that mirror them, and the state keys
// that are suffixed with the old id. Tables are discovered from the schema
// rather than listed, so a table added later cannot be silently left behind.
//
// The fact ledger is deliberately left alone: its hash chain binds project_id,
// so moved facts report as ledger mismatches until the operator authorizes a
// rebaseline. Rewriting the hashes here would erase the difference between an
// authorized rename and tampering.
func (s *Store) RenameProject(selector, newID string) (ProjectRename, error) {
	old, err := s.ResolveProject(selector)
	if err != nil {
		return ProjectRename{}, err
	}
	newID = strings.TrimSpace(newID)
	if newID == "" {
		return ProjectRename{}, fmt.Errorf("new project id is required")
	}
	if Slugify(newID) != newID {
		return ProjectRename{}, fmt.Errorf("project id %q must contain only letters, digits, '.', '_', and '-'", newID)
	}
	if strings.EqualFold(old.ID, newID) {
		return ProjectRename{Project: old}, nil
	}
	var existing string
	switch err := s.DB.QueryRow(`SELECT id FROM projects WHERE id = ?`, newID).Scan(&existing); {
	case err == nil:
		return ProjectRename{}, fmt.Errorf("project id %q is already registered to another path; merge the projects instead", newID)
	case err != sql.ErrNoRows:
		return ProjectRename{}, err
	}

	hashedFacts, err := s.HashedFactCount(old.ID)
	if err != nil {
		return ProjectRename{}, err
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return ProjectRename{}, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`PRAGMA defer_foreign_keys = ON`); err != nil {
		return ProjectRename{}, fmt.Errorf("defer foreign keys for rename %s: %w", old.ID, err)
	}

	if _, err := tx.Exec(`UPDATE projects SET id = ? WHERE id = ?`, newID, old.ID); err != nil {
		return ProjectRename{}, fmt.Errorf("rename project %s: %w", old.ID, err)
	}

	tables, err := projectScopedTables(tx)
	if err != nil {
		return ProjectRename{}, err
	}
	for _, tbl := range tables {
		if _, err := tx.Exec(fmt.Sprintf(`UPDATE %s SET project_id = ? WHERE project_id = ?`, tbl), newID, old.ID); err != nil {
			return ProjectRename{}, fmt.Errorf("move %s rows to %s: %w", tbl, newID, err)
		}
	}

	// State keys end with the old id (git:<id>, distill:<id>,
	// agents_sync:project:<id>). The suffix comparison is exact, unlike LIKE,
	// where an underscore in an id would match any character.
	if _, err := tx.Exec(
		`UPDATE OR REPLACE state SET key = substr(key, 1, length(key) - length(?)) || ? WHERE key = ? OR substr(key, -length(?) - 1) = ':' || ?`,
		old.ID, newID, old.ID, old.ID, old.ID); err != nil {
		return ProjectRename{}, fmt.Errorf("move state keys to %s: %w", newID, err)
	}

	if err := tx.Commit(); err != nil {
		return ProjectRename{}, err
	}
	return ProjectRename{
		Project:     Project{ID: newID, Path: old.Path},
		HashedFacts: hashedFacts,
	}, nil
}

// HashedFactCount returns how many of a project's facts carry a ledger hash,
// i.e. how many rows a rename would leave reporting as ledger mismatches.
func (s *Store) HashedFactCount(projectID string) (int, error) {
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM facts WHERE project_id = ? AND fact_hash != ''`, projectID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count hashed facts of %s: %w", projectID, err)
	}
	return n, nil
}

// projectScopedTables returns every base table carrying a project_id column.
// The projects registry itself is excluded: its id column is the registration
// being renamed, not a row to follow it.
func projectScopedTables(tx *sql.Tx) ([]string, error) {
	rows, err := tx.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name <> 'projects' AND sql LIKE '%project_id%' ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("discover project-scoped tables: %w", err)
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan project-scoped table: %w", err)
		}
		tables = append(tables, name)
	}
	return tables, rows.Err()
}
