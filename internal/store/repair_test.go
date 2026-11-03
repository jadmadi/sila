package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// enveloped reproduces the shape agy records around a user turn.
func enveloped(prompt string) string {
	return "<USER_REQUEST>\n" + prompt + "\n</USER_REQUEST>\n" +
		"<ADDITIONAL_METADATA> The current local time is: 2026-09-16T18:25:13+03:00. </ADDITIONAL_METADATA>\n" +
		"<USER_SETTINGS_CHANGE>changed setting `Model Selection` from None to Flash.</USER_SETTINGS_CHANGE>"
}

// rewindSchema drops user_version so the next Open re-runs migrations from
// there — how a store written by an older sila looks to a newer one.
func rewindSchema(t *testing.T, path string, version int) {
	t.Helper()
	if testing.Short() {
		t.Skip("slow schema-rewind migration test; covered by the non-race preflight")
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("PRAGMA user_version = " + strconv.Itoa(version)); err != nil {
		t.Fatal(err)
	}
}

func countRows(t *testing.T, path, query string, args ...any) int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestMigrationStripsAGYUserEnvelope covers the one-time repair: enveloped agy
// user turns are rewritten to what the user typed, the full-text index follows
// the new content, and nothing else in the store is touched.
func TestMigrationStripsAGYUserEnvelope(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repair.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	proj, err := st.EnsureProject(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	prompt := "rename the payment worker before the next release"
	_, _, err = st.WriteRun("", "agy", "1", []LoadMessage{
		{
			ProjectID: proj.ID, ContentHash: ContentHash("user", enveloped(prompt)),
			TS: "2026-09-14T10:00:00Z", Role: "user", Content: enveloped(prompt), Tool: "agy",
			SessionID: "sess-agy-1", SourceOrder: 1, SourceTime: SortableTime(mustTime(t, "2026-09-14T10:00:00Z")),
		},
		{
			ProjectID: proj.ID, ContentHash: ContentHash("assistant", "done"),
			TS: "2026-09-14T10:01:00Z", Role: "assistant", Content: "done", Tool: "agy",
			SessionID: "sess-agy-1", SourceOrder: 2, SourceTime: SortableTime(mustTime(t, "2026-09-14T10:01:00Z")),
		},
		// A different tool's turn that happens to contain the tag must survive
		// untouched: the repair is scoped to the adapter that writes envelopes.
		{
			ProjectID: proj.ID, ContentHash: ContentHash("user", enveloped("codex prompt")),
			TS: "2026-09-14T10:02:00Z", Role: "user", Content: enveloped("codex prompt"), Tool: "codex",
			SessionID: "sess-codex-1", SourceOrder: 1, SourceTime: SortableTime(mustTime(t, "2026-09-14T10:02:00Z")),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	occurrencesBefore := countRows(t, path, `SELECT COUNT(*) FROM source_occurrences`)
	st.Close()

	rewindSchema(t, path, 13)
	st2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen after rewind: %v", err)
	}
	defer st2.Close()

	stripped := "rename the payment worker before the next release"
	cleanHash := ContentHash("user", stripped)

	var id int64
	var content string
	err = st2.DB.QueryRow(
		`SELECT id, content FROM messages WHERE project_id = ? AND content_hash = ?`,
		proj.ID, cleanHash).Scan(&id, &content)
	if err != nil {
		t.Fatalf("stripped row not found by its new hash: %v", err)
	}
	if content != stripped {
		t.Errorf("content = %q, want %q", content, stripped)
	}

	// No agy turn may still carry the envelope.
	if got := countRows(t, path,
		`SELECT COUNT(*) FROM messages m JOIN source_occurrences o ON o.message_id = m.id
		 WHERE o.tool = 'agy' AND m.content LIKE '%ADDITIONAL_METADATA%'`); got != 0 {
		t.Errorf("%d agy turns still carry the envelope", got)
	}
	// The full-text index must have followed the content. messages_fts has
	// insert and delete triggers but no update trigger, so a repair that
	// forgot to maintain it would leave the envelope searchable while the row
	// itself looked clean.
	if got := countRows(t, path,
		`SELECT COUNT(*) FROM messages_fts WHERE rowid = ? AND content LIKE '%ADDITIONAL_METADATA%'`, id); got != 0 {
		t.Errorf("full-text index still holds the envelope for message %d", id)
	}
	if got := countRows(t, path,
		`SELECT COUNT(*) FROM messages_fts WHERE rowid = ? AND content LIKE '%payment worker%'`, id); got != 1 {
		t.Errorf("full-text index does not hold the stripped prompt for message %d", id)
	}
	// The prompt itself must still be findable through the public search path.
	hits, err := st2.SearchMessagesFTS("payment", SearchMessagesOptions{Project: proj.ID})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, h := range hits {
		if h.Content == stripped {
			found = true
		}
	}
	if !found {
		t.Errorf("stripped prompt is no longer searchable, hits=%d", len(hits))
	}

	// Provenance is preserved: same occurrence rows, still pointing at agy.
	if got := countRows(t, path, `SELECT COUNT(*) FROM source_occurrences`); got != occurrencesBefore {
		t.Errorf("occurrences = %d, want %d (provenance must not be lost)", got, occurrencesBefore)
	}
	if got := countRows(t, path,
		`SELECT COUNT(*) FROM source_occurrences o JOIN messages m ON m.id = o.message_id
		 WHERE o.tool = 'agy' AND m.content_hash = ?`, cleanHash); got != 1 {
		t.Errorf("agy occurrences still attached to the stripped row = %d, want 1", got)
	}

	// The other tool's enveloped turn is untouched.
	if got := countRows(t, path,
		`SELECT COUNT(*) FROM messages WHERE project_id = ? AND content_hash = ?`,
		proj.ID, ContentHash("user", enveloped("codex prompt"))); got != 1 {
		t.Errorf("codex row was modified by an agy-scoped repair")
	}
}

// TestMigrationMergesEnvelopeDuplicate covers the collision case: a wrapped row
// whose stripped text already exists as a clean row is a duplicate, so its
// provenance moves to the survivor and the wrapper is removed rather than
// leaving two rows for the same turn.
func TestMigrationMergesEnvelopeDuplicate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "merge.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	proj, err := st.EnsureProject(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	prompt := "add a regression test for the cursor fix"
	_, _, err = st.WriteRun("", "agy", "1", []LoadMessage{
		// The clean row, as a corrected loader would write it.
		{
			ProjectID: proj.ID, ContentHash: ContentHash("user", prompt),
			TS: "2026-09-14T09:00:00Z", Role: "user", Content: prompt, Tool: "agy",
			SessionID: "sess-clean", SourceOrder: 1, SourceTime: SortableTime(mustTime(t, "2026-09-14T09:00:00Z")),
		},
		// The same turn as stored before the fix.
		{
			ProjectID: proj.ID, ContentHash: ContentHash("user", enveloped(prompt)),
			TS: "2026-09-14T09:00:00Z", Role: "user", Content: enveloped(prompt), Tool: "agy",
			SessionID: "sess-old", SourceOrder: 1, SourceTime: SortableTime(mustTime(t, "2026-09-14T09:00:00Z")),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	occurrencesBefore := countRows(t, path, `SELECT COUNT(*) FROM source_occurrences`)
	messagesBefore := countRows(t, path, `SELECT COUNT(*) FROM messages`)
	st.Close()

	rewindSchema(t, path, 13)
	st2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen after rewind: %v", err)
	}
	defer st2.Close()

	if got := countRows(t, path, `SELECT COUNT(*) FROM messages`); got != messagesBefore-1 {
		t.Errorf("messages = %d, want %d (the wrapped duplicate should be removed)", got, messagesBefore-1)
	}
	if got := countRows(t, path, `SELECT COUNT(*) FROM source_occurrences`); got != occurrencesBefore {
		t.Errorf("occurrences = %d, want %d (both sessions must survive the merge)", got, occurrencesBefore)
	}
	// Both sessions now point at the single surviving row.
	if got := countRows(t, path,
		`SELECT COUNT(*) FROM source_occurrences o JOIN messages m ON m.id = o.message_id
		 WHERE m.content_hash = ?`, ContentHash("user", prompt)); got != 2 {
		t.Errorf("occurrences behind the surviving row = %d, want 2", got)
	}
	if got := countRows(t, path, `SELECT COUNT(*) FROM messages WHERE content LIKE '%USER_REQUEST%'`); got != 0 {
		t.Errorf("envelopes survived the merge")
	}
}

// TestMigrationDeletesZCodeRuntimeTurns covers the one-time repair: stored
// zcode user rows that the harness generated are deleted with their
// occurrences, while rows whose text is shared with another tool survive
// because that text is the other tool's data too.
func TestMigrationDeletesZCodeRuntimeTurns(t *testing.T) {
	const (
		todoReminder    = "The TodoWrite tool hasn't been used recently. This reminder text is unique to the injected turn."
		taskNotif       = "<task-notification>\n<task-id>exec_1</task-id>\n<output-file>/home/user/task</output-file>\n</task-notification>"
		sharedReminder  = "The TodoWrite tool hasn't been used recently. Shared reminder text recorded by two tools."
		otherToolRemind = "The TodoWrite tool hasn't been used recently. Recorded only by another tool."
		realPrompt      = "fix the payment worker"
	)
	path := filepath.Join(t.TempDir(), "zcode_repair.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	proj, err := st.EnsureProject(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	occ := func(ts string) string { return SortableTime(mustTime(t, ts)) }
	_, _, err = st.WriteRun("", "zcode", "1", []LoadMessage{
		{
			ProjectID: proj.ID, ContentHash: ContentHash("user", todoReminder),
			TS: "2026-09-14T10:00:00Z", Role: "user", Content: todoReminder, Tool: "zcode",
			SessionID: "sess-z1", SourceOrder: 1, SourceTime: occ("2026-09-14T10:00:00Z"),
		},
		{
			ProjectID: proj.ID, ContentHash: ContentHash("user", taskNotif),
			TS: "2026-09-14T10:01:00Z", Role: "user", Content: taskNotif, Tool: "zcode",
			SessionID: "sess-z1", SourceOrder: 2, SourceTime: occ("2026-09-14T10:01:00Z"),
		},
		{
			ProjectID: proj.ID, ContentHash: ContentHash("user", realPrompt),
			TS: "2026-09-14T10:02:00Z", Role: "user", Content: realPrompt, Tool: "zcode",
			SessionID: "sess-z1", SourceOrder: 3, SourceTime: occ("2026-09-14T10:02:00Z"),
		},
		{
			ProjectID: proj.ID, ContentHash: ContentHash("assistant", "done"),
			TS: "2026-09-14T10:03:00Z", Role: "assistant", Content: "done", Tool: "zcode",
			SessionID: "sess-z1", SourceOrder: 4, SourceTime: occ("2026-09-14T10:03:00Z"),
		},
		{
			ProjectID: proj.ID, ContentHash: ContentHash("user", sharedReminder),
			TS: "2026-09-14T10:04:00Z", Role: "user", Content: sharedReminder, Tool: "zcode",
			SessionID: "sess-z2", SourceOrder: 1, SourceTime: occ("2026-09-14T10:04:00Z"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// The same reminder text through another tool: content-hash dedup stores one
	// row with occurrences from both tools, so the row is not zcode's alone.
	_, _, err = st.WriteRun("", "opencode", "1", []LoadMessage{
		{
			ProjectID: proj.ID, ContentHash: ContentHash("user", sharedReminder),
			TS: "2026-09-14T11:00:00Z", Role: "user", Content: sharedReminder, Tool: "opencode",
			SessionID: "sess-o1", SourceOrder: 1, SourceTime: occ("2026-09-14T11:00:00Z"),
		},
		{
			ProjectID: proj.ID, ContentHash: ContentHash("user", otherToolRemind),
			TS: "2026-09-14T11:01:00Z", Role: "user", Content: otherToolRemind, Tool: "opencode",
			SessionID: "sess-o1", SourceOrder: 2, SourceTime: occ("2026-09-14T11:01:00Z"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	messagesBefore := countRows(t, path, `SELECT COUNT(*) FROM messages`)
	occurrencesBefore := countRows(t, path, `SELECT COUNT(*) FROM source_occurrences`)
	st.Close()

	rewindSchema(t, path, 14)
	st2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen after rewind: %v", err)
	}
	defer st2.Close()

	// Two zcode-only rows (the todo reminder and the task notification) are
	// gone; the shared row, the opencode-only row, the real prompt, and the
	// assistant turn stay.
	if got := countRows(t, path, `SELECT COUNT(*) FROM messages`); got != messagesBefore-2 {
		t.Errorf("messages = %d, want %d", got, messagesBefore-2)
	}
	if got := countRows(t, path, `SELECT COUNT(*) FROM source_occurrences`); got != occurrencesBefore-2 {
		t.Errorf("occurrences = %d, want %d", got, occurrencesBefore-2)
	}
	if got := countRows(t, path, `SELECT COUNT(*) FROM messages WHERE content = ?`, todoReminder); got != 0 {
		t.Errorf("todo reminder survived the repair")
	}
	if got := countRows(t, path, `SELECT COUNT(*) FROM messages WHERE content = ?`, taskNotif); got != 0 {
		t.Errorf("task notification survived the repair")
	}
	if got := countRows(t, path, `SELECT COUNT(*) FROM messages WHERE content = ?`, realPrompt); got != 1 {
		t.Errorf("real user prompt was deleted")
	}
	if got := countRows(t, path, `SELECT COUNT(*) FROM messages WHERE content = ?`, otherToolRemind); got != 1 {
		t.Errorf("another tool's reminder was deleted")
	}
	if got := countRows(t, path,
		`SELECT COUNT(*) FROM source_occurrences o JOIN messages m ON m.id = o.message_id
		 WHERE m.content = ?`, sharedReminder); got != 2 {
		t.Errorf("shared row occurrences = %d, want 2 (both tools must survive)", got)
	}

	// No orphaned occurrences, and the full-text index matches the table in
	// both directions: the message delete trigger must clear the deleted rows.
	if got := countRows(t, path,
		`SELECT COUNT(*) FROM source_occurrences o
		 WHERE NOT EXISTS (SELECT 1 FROM messages m WHERE m.id = o.message_id)`); got != 0 {
		t.Errorf("%d orphaned occurrences after the repair", got)
	}
	if got := countRows(t, path,
		`SELECT COUNT(*) FROM messages_fts f
		 WHERE NOT EXISTS (SELECT 1 FROM messages m WHERE m.id = f.rowid)`); got != 0 {
		t.Errorf("%d stale full-text rows after the repair", got)
	}
	if got := countRows(t, path,
		`SELECT COUNT(*) FROM messages m
		 WHERE NOT EXISTS (SELECT 1 FROM messages_fts f WHERE f.rowid = m.id)`); got != 0 {
		t.Errorf("%d messages missing from the full-text index", got)
	}
	// The deleted reminder text must not be findable through public search.
	hits, err := st2.SearchMessagesFTS("injected", SearchMessagesOptions{Project: proj.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Errorf("deleted reminder is still searchable, hits=%d", len(hits))
	}
}

// TestRepairMiMoCodeRuntimeTurns covers the mimocode cleanup: child-session
// turns disappear with their occurrences, synthetic tail text is stripped from
// main-session turns, and nothing shared with another tool is touched.
func TestRepairMiMoCodeRuntimeTurns(t *testing.T) {
	const (
		childPrompt  = "<system-reminder>\nYou are now operating in checkpoint-writer mode. Ignore the general assistant framing."
		childAsst    = "Checkpoint updated. Reconciled the A6 status change and trimmed the notes."
		mixed        = "help me deploy\n<system-reminder> Skills available in this session: ..."
		allSynthetic = "<system-reminder> You are about to finish, but these tasks are still unfinished: T1"
		realPrompt   = "please review the migration plan"
		sharedText   = "trim my goal"
		otherTool    = "<system-reminder> not sila's data"
	)
	path := filepath.Join(t.TempDir(), "mimo_repair.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	proj, err := st.EnsureProject(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	occ := func(ts string) string { return SortableTime(mustTime(t, ts)) }
	msg := func(session, role, content string, order int64, ts string) LoadMessage {
		return LoadMessage{
			ProjectID: proj.ID, ContentHash: ContentHash(role, content), TS: ts,
			Role: role, Content: content, Tool: "mimocode", SessionID: session,
			SourceOrder: order, SourceTime: occ(ts),
		}
	}
	_, _, err = st.WriteRun("", "mimocode", "1", []LoadMessage{
		msg("ses-child-1", "user", childPrompt, 1, "2026-09-14T10:00:00Z"),
		msg("ses-child-1", "assistant", childAsst, 2, "2026-09-14T10:01:00Z"),
		msg("ses-root-1", "user", mixed, 1, "2026-09-14T10:02:00Z"),
		msg("ses-root-1", "user", allSynthetic, 2, "2026-09-14T10:03:00Z"),
		msg("ses-root-1", "user", realPrompt, 3, "2026-09-14T10:04:00Z"),
		// This scaffolding wraps a text another tool already stored: the
		// rewrite must merge into that row and keep both tools attributed.
		msg("ses-root-2", "user", sharedText+"\n<system-reminder> more scaffolding", 1, "2026-09-14T10:05:00Z"),
	})
	if err != nil {
		t.Fatal(err)
	}
	// Another tool's rows, including one that contains a marker and must
	// survive the repair.
	_, _, err = st.WriteRun("", "codex", "1", []LoadMessage{
		{
			ProjectID: proj.ID, ContentHash: ContentHash("user", otherTool), TS: "2026-09-14T11:00:00Z",
			Role: "user", Content: otherTool, Tool: "codex", SessionID: "sess-codex",
			SourceOrder: 1, SourceTime: occ("2026-09-14T11:00:00Z"),
		},
		{
			ProjectID: proj.ID, ContentHash: ContentHash("user", sharedText), TS: "2026-09-14T11:01:00Z",
			Role: "user", Content: sharedText, Tool: "codex", SessionID: "sess-codex",
			SourceOrder: 2, SourceTime: occ("2026-09-14T11:01:00Z"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	messagesBefore := countRows(t, path, `SELECT COUNT(*) FROM messages`)
	occurrencesBefore := countRows(t, path, `SELECT COUNT(*) FROM source_occurrences`)

	tx, err := st.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := repairMiMoCodeRuntimeTurns(tx, []string{"ses-child-1"}, []string{"<system-reminder>"}); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	// Two child-session turns deleted, one all-synthetic turn deleted, the
	// mixed row rewritten, and the rewritten duplicate merged away (four
	// deletions); occurrences move with the merge, so only three go.
	if got := countRows(t, path, `SELECT COUNT(*) FROM messages`); got != messagesBefore-4 {
		t.Errorf("messages = %d, want %d", got, messagesBefore-4)
	}
	if got := countRows(t, path, `SELECT COUNT(*) FROM source_occurrences`); got != occurrencesBefore-3 {
		t.Errorf("occurrences = %d, want %d (the merged duplicate keeps its occurrence)", got, occurrencesBefore-3)
	}
	for _, gone := range []string{childPrompt, childAsst, allSynthetic} {
		if got := countRows(t, path, `SELECT COUNT(*) FROM messages WHERE content = ?`, gone); got != 0 {
			t.Errorf("runtime turn survived: %.60q", gone)
		}
	}
	if got := countRows(t, path, `SELECT COUNT(*) FROM messages WHERE content = ?`, realPrompt); got != 1 {
		t.Errorf("real prompt was deleted")
	}
	if got := countRows(t, path, `SELECT COUNT(*) FROM messages WHERE content = ?`, otherTool); got != 1 {
		t.Errorf("another tool's marker text was deleted")
	}
	// The mixed row reads as what the loader would emit today.
	if got := countRows(t, path, `SELECT COUNT(*) FROM messages WHERE content = ?`, "help me deploy"); got != 1 {
		t.Errorf("mixed row was not rewritten to its human text")
	}
	// The duplicate's occurrence moved to the surviving shared-text row, and
	// the survivor's tool attribution now names both tools.
	if got := countRows(t, path,
		`SELECT COUNT(*) FROM source_occurrences o JOIN messages m ON m.id = o.message_id
		 WHERE m.content = ?`, sharedText); got != 2 {
		t.Errorf("shared row occurrences = %d, want 2", got)
	}
	if got := countRows(t, path,
		`SELECT COUNT(*) FROM messages m, json_each(m.source_tools)
		 WHERE m.content = ? AND json_each.value IN ('codex','mimocode')`, sharedText); got != 2 {
		t.Errorf("merged row tool attribution = %d tools, want codex and mimocode", got)
	}

	// Consistency: no orphans, index matches the table both ways.
	if got := countRows(t, path,
		`SELECT COUNT(*) FROM source_occurrences o
		 WHERE NOT EXISTS (SELECT 1 FROM messages m WHERE m.id = o.message_id)`); got != 0 {
		t.Errorf("%d orphaned occurrences after the repair", got)
	}
	if got := countRows(t, path,
		`SELECT COUNT(*) FROM messages_fts f
		 WHERE NOT EXISTS (SELECT 1 FROM messages m WHERE m.id = f.rowid)`); got != 0 {
		t.Errorf("%d stale full-text rows after the repair", got)
	}
	if got := countRows(t, path,
		`SELECT COUNT(*) FROM messages m
		 WHERE NOT EXISTS (SELECT 1 FROM messages_fts f WHERE f.rowid = m.id)`); got != 0 {
		t.Errorf("%d messages missing from the full-text index", got)
	}
	// The stripped text is no longer searchable, the human text is.
	hits, err := st.SearchMessagesFTS("deploy", SearchMessagesOptions{Project: proj.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Content != "help me deploy" {
		t.Errorf("rewritten row not searchable as clean text: %+v", hits)
	}
	stray, err := st.SearchMessagesFTS("scaffolding", SearchMessagesOptions{Project: proj.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(stray) != 0 {
		t.Errorf("stripped scaffolding still searchable: %+v", stray)
	}
	st.Close()
}

// TestMigrationDeletesMiMoCodeRuntimeTurns drives migration 16 through the
// registry, using the recorded child-session list and marker inventory the
// migration actually ships.
func TestMigrationDeletesMiMoCodeRuntimeTurns(t *testing.T) {
	if len(miMoCodeChildSessions) == 0 {
		t.Fatal("the recorded child-session list is empty")
	}
	path := filepath.Join(t.TempDir(), "mimo_registry_repair.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	proj, err := st.EnsureProject(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	childSession := miMoCodeChildSessions[0]
	_, _, err = st.WriteRun("", "mimocode", "1", []LoadMessage{
		{
			ProjectID: proj.ID, ContentHash: ContentHash("assistant", "checkpoint narration"),
			TS: "2026-09-14T10:00:00Z", Role: "assistant", Content: "checkpoint narration", Tool: "mimocode",
			SessionID: childSession, SourceOrder: 1,
		},
		{
			ProjectID: proj.ID, ContentHash: ContentHash("user", "<system-reminder> You are about to finish"),
			TS: "2026-09-14T10:01:00Z", Role: "user", Content: "<system-reminder> You are about to finish", Tool: "mimocode",
			SessionID: "ses-root-fixture", SourceOrder: 2,
		},
		{
			ProjectID: proj.ID, ContentHash: ContentHash("user", "a real fixture prompt"),
			TS: "2026-09-14T10:02:00Z", Role: "user", Content: "a real fixture prompt", Tool: "mimocode",
			SessionID: "ses-root-fixture", SourceOrder: 3,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	rewindSchema(t, path, 15)
	st2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen after rewind: %v", err)
	}
	defer st2.Close()

	if got := countRows(t, path, `SELECT COUNT(*) FROM messages WHERE content = ?`, "checkpoint narration"); got != 0 {
		t.Errorf("recorded child-session row survived migration 16")
	}
	if got := countRows(t, path, `SELECT COUNT(*) FROM messages WHERE content LIKE '<system-reminder>%'`); got != 0 {
		t.Errorf("synthetic row survived migration 16")
	}
	if got := countRows(t, path, `SELECT COUNT(*) FROM messages WHERE content = ?`, "a real fixture prompt"); got != 1 {
		t.Errorf("real prompt was deleted by migration 16")
	}
	if got := countRows(t, path,
		`SELECT COUNT(*) FROM source_occurrences o
		 WHERE NOT EXISTS (SELECT 1 FROM messages m WHERE m.id = o.message_id)`); got != 0 {
		t.Errorf("%d orphaned occurrences after migration 16", got)
	}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

// agyAssistantEnvelope reproduces the timestamp header agy records at the start
// of every assistant turn, in the paired form that covers the large majority of
// the source store. The stamp is unique to the agy rows so a test can tell the
// rewritten row apart from another tool's row carrying the same header.
func agyAssistantEnvelope(body string) string {
	return "Created At: 2026-09-14T10:01:00+03:00\n" +
		"Completed At: 2026-09-14T10:01:00+03:00\n" + body
}

// codexEnvelopeLike gives a different tool a header of the same shape under a
// different stamp: the repair must leave it alone, and the test must tell the
// two apart.
func codexEnvelopeLike(body string) string {
	return "Created At: 2026-09-14T09:00:00+03:00\n" +
		"Completed At: 2026-09-14T09:00:00+03:00\n" + body
}

var _ = codexEnvelopeLike

// TestMigrationStripsAGYAssistantEnvelope covers migration 17: stored agy
// assistant turns carrying the timestamp header are rewritten to the model's
// actual output, the full-text index follows, and everything else is untouched.
func TestMigrationStripsAGYAssistantEnvelope(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repair.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	proj, err := st.EnsureProject(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	body := "Created file file:///repo/DISPATCH.md with the requested content."
	// A human turn that begins with the same words but carries no timestamp
	// must survive the repair untouched: the guard keys on a parseable stamp.
	humanish := "Created At: is the name of my repo, please clone it"
	_, _, err = st.WriteRun("", "agy", "1", []LoadMessage{
		{
			ProjectID: proj.ID, ContentHash: ContentHash("assistant", agyAssistantEnvelope(body)),
			TS: "2026-09-14T10:01:00Z", Role: "assistant", Content: agyAssistantEnvelope(body), Tool: "agy",
			SessionID: "sess-agy-1", SourceOrder: 1, SourceTime: SortableTime(mustTime(t, "2026-09-14T10:01:00Z")),
		},
		{
			ProjectID: proj.ID, ContentHash: ContentHash("assistant", body),
			TS: "2026-09-14T10:02:00Z", Role: "assistant", Content: body, Tool: "agy",
			SessionID: "sess-agy-1", SourceOrder: 2, SourceTime: SortableTime(mustTime(t, "2026-09-14T10:02:00Z")),
		},
		// A different tool's turn carrying the same header must survive: the
		// repair is scoped to the adapter that writes envelopes.
		{
			ProjectID: proj.ID, ContentHash: ContentHash("assistant", codexEnvelopeLike("codex output")),
			TS: "2026-09-14T10:03:00Z", Role: "assistant", Content: codexEnvelopeLike("codex output"), Tool: "codex",
			SessionID: "sess-codex-1", SourceOrder: 1, SourceTime: SortableTime(mustTime(t, "2026-09-14T10:03:00Z")),
		},
		{
			ProjectID: proj.ID, ContentHash: ContentHash("user", humanish),
			TS: "2026-09-14T10:04:00Z", Role: "user", Content: humanish, Tool: "agy",
			SessionID: "sess-agy-1", SourceOrder: 3, SourceTime: SortableTime(mustTime(t, "2026-09-14T10:04:00Z")),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	// A pending agy code_claim copied the wrapped assistant text verbatim, so
	// the migration must clean the claim corpus too, not only the messages.
	dbOpen, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dbOpen.Exec(`
		INSERT INTO facts(id, project_id, claim, confidence, repo_files, tools, session_ref, first_seen, promoted_at, kind, distill_status)
		VALUES('fact_env_1', ?, ?, 'grounded', '[]', '["agy"]', 'sess-agy-1', '2026-09-14T10:01:00Z', '2026-09-14T10:01:00Z', 'code_claim', 'pending')`,
		proj.ID, agyAssistantEnvelope(body)); err != nil {
		t.Fatal(err)
	}
	// A claim from another tool carrying the same header must survive.
	if _, err := dbOpen.Exec(`
		INSERT INTO facts(id, project_id, claim, confidence, repo_files, tools, session_ref, first_seen, promoted_at, kind, distill_status)
		VALUES('fact_env_2', ?, ?, 'grounded', '[]', '["codex"]', 'sess-codex-1', '2026-09-14T10:03:00Z', '2026-09-14T10:03:00Z', 'code_claim', 'pending')`,
		proj.ID, codexEnvelopeLike("codex output")); err != nil {
		t.Fatal(err)
	}
	dbOpen.Close()

	rewindSchema(t, path, 16)
	st2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen after rewind: %v", err)
	}
	defer st2.Close()

	// The agy claim keeps its id and now carries the model's output; the
	// codex claim is untouched.
	var claimText string
	if err := st2.DB.QueryRow(`SELECT claim FROM facts WHERE id = 'fact_env_1'`).Scan(&claimText); err != nil {
		t.Fatalf("agy claim missing: %v", err)
	}
	if claimText != body {
		t.Errorf("agy claim = %q, want the stripped body %q", claimText, body)
	}
	if got := countRows(t, path, `SELECT COUNT(*) FROM facts WHERE claim LIKE 'Created At: %'`); got != 1 {
		t.Errorf("claims still carrying an envelope = %d, want 1 (the codex claim must survive)", got)
	}

	// The wrapped turn is rewritten to the model's output, which now collides
	// by content hash with the already-clean row: occurrences are re-pointed
	// to the survivor and the wrapped row is deleted.
	if got := countRows(t, path, `SELECT COUNT(*) FROM messages WHERE content = ?`, body); got != 1 {
		t.Errorf("clean assistant rows = %d, want 1 (wrapped row merged into it)", got)
	}
	if got := countRows(t, path, `SELECT COUNT(*) FROM messages WHERE content LIKE 'Created At: 2026%'`); got != 1 {
		t.Errorf("rows still carrying an envelope = %d, want 1 (the codex row must survive)", got)
	}
	if got := countRows(t, path, `SELECT COUNT(*) FROM messages WHERE content = ?`, humanish); got != 1 {
		t.Errorf("the timestamp-less human turn was rewritten or deleted")
	}

	// No orphaned occurrences, and the full-text index matches the table in
	// both directions.
	if got := countRows(t, path,
		`SELECT COUNT(*) FROM source_occurrences o
		 WHERE NOT EXISTS (SELECT 1 FROM messages m WHERE m.id = o.message_id)`); got != 0 {
		t.Errorf("%d orphaned occurrences after migration 17", got)
	}
	if got := countRows(t, path,
		`SELECT COUNT(*) FROM messages_fts f
		 WHERE NOT EXISTS (SELECT 1 FROM messages m WHERE m.id = f.rowid)`); got != 0 {
		t.Errorf("%d stale full-text rows after migration 17", got)
	}
	if got := countRows(t, path,
		`SELECT COUNT(*) FROM messages m
		 WHERE NOT EXISTS (SELECT 1 FROM messages_fts f WHERE f.rowid = m.id)`); got != 0 {
		t.Errorf("%d messages missing from the full-text index", got)
	}
	// The agy envelope's stamp must no longer be findable, while another
	// tool's same-shaped header must still be; the body must be searchable.
	// A full RFC3339 stamp is one FTS token, so it is a precise probe.
	hits, err := st2.SearchMessagesFTS("2026-09-14T10:01:00", SearchMessagesOptions{Project: proj.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Errorf("agy envelope stamp still searchable: %d hits", len(hits))
	}
	hits, err = st2.SearchMessagesFTS("2026-09-14T09:00:00", SearchMessagesOptions{Project: proj.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Errorf("codex envelope stamp hits = %d, want 1 (the repair must not touch another tool)", len(hits))
	}
	hits, err = st2.SearchMessagesFTS("codex output", SearchMessagesOptions{Project: proj.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Errorf("codex envelope rows = %d, want 1 (the repair must not touch another tool)", len(hits))
	}
	hits, err = st2.SearchMessagesFTS("DISPATCH", SearchMessagesOptions{Project: proj.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Error("the model's output is not searchable after the rewrite")
	}
}

func TestCanonicalProjectPath(t *testing.T) {
	// Standard path
	repoDir := t.TempDir()
	if got := CanonicalProjectPath(repoDir); got != repoDir {
		t.Errorf("CanonicalProjectPath(%q) = %q, want %q", repoDir, got, repoDir)
	}

	// Pattern-based worktree
	wtPattern := filepath.Join(repoDir, ".worktrees", "feature-branch")
	if got := CanonicalProjectPath(wtPattern); got != repoDir {
		t.Errorf("CanonicalProjectPath(%q) = %q, want %q", wtPattern, got, repoDir)
	}

	// Real git worktree with .git file pointer
	wtDir := filepath.Join(repoDir, "real-worktree")
	if err := os.MkdirAll(wtDir, 0755); err != nil {
		t.Fatal(err)
	}
	gitWorktreesDir := filepath.Join(repoDir, ".git", "worktrees", "real-worktree")
	if err := os.MkdirAll(gitWorktreesDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitWorktreesDir, "commondir"), []byte("../..\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtDir, ".git"), []byte("gitdir: "+gitWorktreesDir+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if got := CanonicalProjectPath(wtDir); got != repoDir {
		t.Errorf("CanonicalProjectPath(real worktree %q) = %q, want %q", wtDir, got, repoDir)
	}

	// Subdirectory inside real git worktree
	subDir := filepath.Join(wtDir, "pkg", "sub")
	if got := CanonicalProjectPath(subDir); got != repoDir {
		t.Errorf("CanonicalProjectPath(subDir %q) = %q, want %q", subDir, got, repoDir)
	}
}

func TestMigrationCanonicalizeWorktreeProjects(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "worktrees.db")

	mainRepo := filepath.Join(tmp, "main-app")
	if err := os.MkdirAll(mainRepo, 0755); err != nil {
		t.Fatal(err)
	}
	wtPath := filepath.Join(mainRepo, ".worktrees", "test-worktree")

	// 1. Create DB with main project and worktree project
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	mainProj, err := st.EnsureProject(mainRepo)
	if err != nil {
		t.Fatal(err)
	}

	// Force insert a legacy worktree project row
	wtID := "test-worktree"
	if _, err := st.DB.Exec(`INSERT INTO projects(path, id, created_at) VALUES(?, ?, ?)`, wtPath, wtID, "2026-08-01T10:00:00Z"); err != nil {
		t.Fatal(err)
	}

	// Insert messages and facts under the worktree project ID
	_, _, err = st.WriteRun("run-wt", "claude-code", "1", []LoadMessage{
		{ProjectID: wtID, ContentHash: "hash-wt-1", TS: "2026-08-01T10:05:00Z", Role: "user", Content: "worktree prompt 1", Tool: "claude-code"},
		{ProjectID: wtID, ContentHash: "hash-wt-2", TS: "2026-08-01T10:06:00Z", Role: "assistant", Content: "worktree answer 2", Tool: "claude-code"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := st.UpsertFact(Fact{
		ID:         "fact-wt-1",
		ProjectID:  wtID,
		Claim:      "worktree factual discovery",
		Confidence: "grounded",
		Verified:   true,
		Tools:      []string{"claude-code"},
		FirstSeen:  "2026-08-01T10:05:00Z",
		PromotedAt: "2026-08-01T10:05:00Z",
	}); err != nil {
		t.Fatal(err)
	}

	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	// 2. Re-open and run repair
	st2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()

	rep, err := st2.Doctor(true)
	if err != nil {
		t.Fatalf("doctor repair failed: %v", err)
	}
	if !rep.RepairExecuted {
		t.Errorf("expected repair executed")
	}

	// 3. Verify worktree project row was removed and merged into mainProj.ID
	projs, err := st2.Projects()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range projs {
		if p.Path == wtPath || p.ID == wtID {
			t.Errorf("worktree project %s (%s) still registered after repair", p.ID, p.Path)
		}
	}

	// 4. Verify messages and facts now belong to mainProj.ID
	var msgCount int
	if err := st2.DB.QueryRow(`SELECT count(*) FROM messages WHERE project_id = ?`, mainProj.ID).Scan(&msgCount); err != nil {
		t.Fatal(err)
	}
	if msgCount != 2 {
		t.Errorf("expected 2 messages under main project, got %d", msgCount)
	}

	var factCount int
	if err := st2.DB.QueryRow(`SELECT count(*) FROM facts WHERE project_id = ?`, mainProj.ID).Scan(&factCount); err != nil {
		t.Fatal(err)
	}
	if factCount != 1 {
		t.Errorf("expected 1 fact under main project, got %d", factCount)
	}
}

// TestRenameProjectMovesAllProjectScopedData covers the registry correction:
// the registration keeps its path, every project-scoped row follows the new
// id, the full-text indexes stay in step, and state keys suffixed with the
// old id move with it.
func TestRenameProjectMovesAllProjectScopedData(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "rename.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	dir := t.TempDir()
	old, err := st.EnsureProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	if old.ID == "github" {
		t.Fatalf("test dir id collided with target id %q", old.ID)
	}
	now := time.Now().UTC().Format(time.RFC3339)

	if _, _, err := st.WriteRun("", "opencode", "wm", []LoadMessage{{
		ProjectID:   old.ID,
		ContentHash: "rename-msg",
		TS:          now,
		Role:        "user",
		Content:     "rename needle message",
		Tool:        "opencode",
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertMemo(old.ID, "rename memo", "agent", dir, nil); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertHandoff(Handoff{
		ID: "hoff_rename", ProjectID: old.ID, Agent: "agent", Summary: "rename handoff",
		CreatedAt: now, Status: "pending",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertAssertion(Assertion{
		ID: AssertionID(old.ID, "fact_x", "file", dir), ProjectID: old.ID,
		StatementID: "fact_x", StatementType: "fact", EvidenceType: "file",
		EvidenceID: dir, Confidence: "grounded", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertDoc(Doc{
		ID: DocID(old.ID, "https://example.com/rename"), ProjectID: old.ID,
		Title: "rename doc", URL: "https://example.com/rename",
		Content: "rename doc body", Format: "markdown", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`INSERT INTO epistemic_heartbeats (id, project_id, session_id, agent, invariant_rule, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		"hb_rename", old.ID, "ses_1", "agent", "rule_1", now); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"distill:" + old.ID, "agents_sync:project:" + old.ID, "git:" + old.ID} {
		if err := st.SetState(key, "1"); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetState("distill:github", "stale-orphan"); err != nil {
		t.Fatal(err)
	}

	renamed, err := st.RenameProject(old.ID, "github")
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Project.ID != "github" || renamed.Project.Path != old.Path {
		t.Fatalf("renamed = %+v, want id github at %s", renamed.Project, old.Path)
	}
	// The memo is a hashed fact and the ledger hash binds project_id, so the
	// rename must report it rather than leave the ledger looking tampered.
	if renamed.HashedFacts != 1 {
		t.Errorf("HashedFacts = %d, want 1 (the memo)", renamed.HashedFacts)
	}

	// The ledger deliberately keeps the old hash: the moved fact reports as a
	// mismatch until an operator rebaselines with a reason.
	if audit, err := st.VerifyChain(""); err != nil {
		t.Fatal(err)
	} else if audit.Intact {
		t.Error("ledger reports intact after a rename moved a hashed fact; expected a mismatch for the operator to rebaseline")
	}

	// Every project-scoped table follows the id, discovered the same way the
	// rename discovers them, so a table added later is covered here too.
	rows, err := st.DB.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name <> 'projects' AND sql LIKE '%project_id%'`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(tables) == 0 {
		t.Fatal("no project-scoped tables discovered")
	}
	for _, tbl := range tables {
		var stale, moved int
		if err := st.DB.QueryRow(`SELECT COUNT(*) FROM `+tbl+` WHERE project_id = ?`, old.ID).Scan(&stale); err != nil {
			t.Fatal(err)
		}
		if err := st.DB.QueryRow(`SELECT COUNT(*) FROM `+tbl+` WHERE project_id = ?`, "github").Scan(&moved); err != nil {
			t.Fatal(err)
		}
		if stale != 0 {
			t.Errorf("%s still holds %d rows under the old id", tbl, stale)
		}
		_ = moved
	}

	var msgs, facts int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM messages WHERE project_id = ?`, "github").Scan(&msgs); err != nil {
		t.Fatal(err)
	}
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM facts WHERE project_id = ?`, "github").Scan(&facts); err != nil {
		t.Fatal(err)
	}
	if msgs != 1 || facts != 1 {
		t.Errorf("moved rows = %d messages, %d facts; want 1 and 1", msgs, facts)
	}

	// FTS is a separate table and must have followed.
	found, err := st.SearchMessagesFTS("needle", SearchMessagesOptions{Project: "github"})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 {
		t.Errorf("FTS search under new id found %d messages, want 1", len(found))
	}
	docs, err := st.SearchDocsFTS("body", "github", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Errorf("docs FTS search under new id found %d docs, want 1", len(docs))
	}

	if v, err := st.GetState("distill:github"); err != nil || v != "1" {
		t.Errorf("distill state key did not follow rename: v=%q err=%v", v, err)
	}
	if v, err := st.GetState("agents_sync:project:github"); err != nil || v != "1" {
		t.Errorf("agents_sync state key did not follow rename: v=%q err=%v", v, err)
	}
	if n := countRows(t, st.Path, `SELECT COUNT(*) FROM projects WHERE id = ?`, old.ID); n != 0 {
		t.Errorf("old id %q still registered %d times", old.ID, n)
	}
	back, err := st.ResolveProject("github")
	if err != nil || back.Path != old.Path {
		t.Errorf("ResolveProject(github) = %+v, %v; want path %s", back, err, old.Path)
	}

	// Renaming onto a registered id is refused: that shape is a merge.
	other, err := st.EnsureProject(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.RenameProject("github", other.ID); err == nil {
		t.Error("rename onto an existing project id succeeded; want refusal")
	}
	if _, err := st.RenameProject("github", "not a slug"); err == nil {
		t.Error("rename to a non-slug id succeeded; want refusal")
	}
}
