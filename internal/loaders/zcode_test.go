package loaders

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// zcodeFixture builds a ZCode-shaped database: session → message → part, each
// payload in a JSON column. Rows mirror the real store's shapes, including the
// two traps verified against it: only assistant messages carry `path.cwd`, and
// an in-flight assistant message has no `time.completed`.
func zcodeFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "db.sqlite")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	stmts := []string{
		`CREATE TABLE session (id TEXT PRIMARY KEY, directory TEXT, path TEXT, project_id TEXT)`,
		`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, time_created INTEGER, time_updated INTEGER, data TEXT, sequence INTEGER)`,
		`CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT, session_id TEXT, time_created INTEGER, time_updated INTEGER, data TEXT, sequence INTEGER)`,
		`INSERT INTO session VALUES ('sess-1', '/work/atlas', '/work/atlas', 'proj_work-atlas')`,
		// user turn: no path.cwd (the real store omits it), text part
		`INSERT INTO message VALUES ('m1','sess-1',1700000000000,1700000000000,
			'{"role":"user","semantics":{"origin":"real_user"},"time":{"created":1700000000000}}', 0)`,
		`INSERT INTO part VALUES ('p1','m1','sess-1',1700000000000,1700000000000,
			'{"type":"text","text":"please diagnose the payment worker"}', 0)`,
		// assistant turn, completed, with cwd
		`INSERT INTO message VALUES ('m2','sess-1',1700000001000,1700000005000,
			'{"role":"assistant","time":{"created":1700000001000,"completed":1700000005000},"path":{"cwd":"/work/atlas"}}', 1)`,
		`INSERT INTO part VALUES ('p2','m2','sess-1',1700000001000,1700000001000,
			'{"type":"text","text":"the worker leaks a goroutine"}', 0)`,
		// non-text parts and reasoning must be ignored
		`INSERT INTO part VALUES ('p3','m2','sess-1',1700000001000,1700000001000,
			'{"type":"tool","tool":"Bash","state":{"input":{"command":"go test ./..."}}}', 1)`,
		`INSERT INTO part VALUES ('p4','m2','sess-1',1700000001000,1700000001000,
			'{"type":"reasoning","text":"internal monologue"}', 2)`,
		// in-flight assistant turn: no time.completed -> skipped until it finishes
		`INSERT INTO message VALUES ('m3','sess-1',1700000006000,1700000006000,
			'{"role":"assistant","time":{"created":1700000006000},"path":{"cwd":"/work/atlas"}}', 2)`,
		`INSERT INTO part VALUES ('p5','m3','sess-1',1700000006000,1700000006000,
			'{"type":"text","text":"partial answer that is still streaming"}', 0)`,
		// same-second second message in another session, to prove the boundary
		`INSERT INTO session VALUES ('sess-2', '/work/site', '/work/site', 'proj_work-site')`,
		`INSERT INTO message VALUES ('m4','sess-2',1700000010000,1700000010000,
			'{"role":"user","semantics":{"origin":"real_user"},"time":{"created":1700000010000}}', 0)`,
		`INSERT INTO part VALUES ('p6','m4','sess-2',1700000010000,1700000010000,
			'{"type":"text","text":"second session question"}', 0)`,
		// Runtime injections: the source itself marks these agent_runtime or
		// system. Every kind in the measured taxonomy appears, so a rule that
		// keys on the wrong level (kind instead of origin) fails here.
		`INSERT INTO session VALUES ('sess-synth', '/work/atlas', '/work/atlas', 'proj_work-atlas')`,
		`INSERT INTO message VALUES ('m5','sess-synth',1700000020000,1700000020000,
			'{"role":"user","semantics":{"origin":"agent_runtime","kind":"todo_reminder"},"time":{"created":1700000020000}}', 0)`,
		`INSERT INTO part VALUES ('p7','m5','sess-synth',1700000020000,1700000020000,
			'{"type":"text","text":"The TodoWrite tool hasn''t been used recently."}', 0)`,
		`INSERT INTO message VALUES ('m6','sess-synth',1700000021000,1700000021000,
			'{"role":"user","semantics":{"origin":"agent_runtime","kind":"background_notification"},"time":{"created":1700000021000}}', 0)`,
		`INSERT INTO part VALUES ('p8','m6','sess-synth',1700000021000,1700000021000,
			'{"type":"text","text":"<task-notification>background task finished</task-notification>"}', 0)`,
		`INSERT INTO message VALUES ('m7','sess-synth',1700000022000,1700000022000,
			'{"role":"user","semantics":{"origin":"agent_runtime","kind":"system_reminder"},"time":{"created":1700000022000}}', 0)`,
		`INSERT INTO part VALUES ('p9','m7','sess-synth',1700000022000,1700000022000,
			'{"type":"text","text":"<system-reminder>stay on task</system-reminder>"}', 0)`,
		`INSERT INTO message VALUES ('m8','sess-synth',1700000023000,1700000023000,
			'{"role":"user","semantics":{"origin":"agent_runtime","kind":"compact_summary"},"time":{"created":1700000023000}}', 0)`,
		`INSERT INTO part VALUES ('p10','m8','sess-synth',1700000023000,1700000023000,
			'{"type":"text","text":"summary of the conversation so far"}', 0)`,
		`INSERT INTO message VALUES ('m9','sess-synth',1700000024000,1700000024000,
			'{"role":"user","semantics":{"origin":"system"},"time":{"created":1700000024000}}', 0)`,
		`INSERT INTO part VALUES ('p11','m9','sess-synth',1700000024000,1700000024000,
			'{"type":"text","text":"system-level injected turn"}', 0)`,
		// Unknown origins fail open: a future shape of real user input must be
		// ingested rather than silently dropped by a positive allowlist.
		`INSERT INTO message VALUES ('m10','sess-synth',1700000025000,1700000025000,
			'{"role":"user","semantics":{"origin":"future_origin"},"time":{"created":1700000025000}}', 0)`,
		`INSERT INTO part VALUES ('p12','m10','sess-synth',1700000025000,1700000025000,
			'{"type":"text","text":"a future origin still counts as the user"}', 0)`,
		// Assistant turns keep their origin: the model answering through the
		// runtime is the normal case.
		`INSERT INTO message VALUES ('m11','sess-synth',1700000026000,1700000030000,
			'{"role":"assistant","semantics":{"origin":"agent_runtime"},"time":{"created":1700000026000,"completed":1700000030000},"path":{"cwd":"/work/atlas"}}', 1)`,
		`INSERT INTO part VALUES ('p13','m11','sess-synth',1700000026000,1700000026000,
			'{"type":"text","text":"runtime assistant answer"}', 0)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("fixture exec failed: %v\n%s", err, s)
		}
	}
	return path
}

func TestLoadZCode(t *testing.T) {
	path := zcodeFixture(t)

	msgs, wm, err := loadZCode(path, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 5 {
		t.Fatalf("expected 5 curated messages, got %d: %+v", len(msgs), msgs)
	}
	// The in-flight assistant turn must not be ingested as a partial message.
	for _, m := range msgs {
		if m.Content == "partial answer that is still streaming" {
			t.Fatal("in-flight assistant message was ingested before completion")
		}
		if m.Content == "internal monologue" {
			t.Fatal("reasoning text must not be ingested")
		}
		if m.Tool != "zcode" {
			t.Fatalf("tool = %q, want zcode", m.Tool)
		}
	}
	// Runtime injections the source marks agent_runtime/system are not user
	// speech, whatever their kind field says.
	for _, synthetic := range []string{
		"The TodoWrite tool hasn't been used recently.",
		"<task-notification>background task finished</task-notification>",
		"<system-reminder>stay on task</system-reminder>",
		"summary of the conversation so far",
		"system-level injected turn",
	} {
		for _, m := range msgs {
			if m.Content == synthetic {
				t.Fatalf("runtime injection ingested as user speech: %q", m.Content)
			}
		}
	}
	// Fail-open direction: an origin the curation does not know is ingested,
	// and assistant turns keep working regardless of origin.
	var sawUnknown, sawRuntimeAssistant bool
	for _, m := range msgs {
		if m.Content == "a future origin still counts as the user" {
			sawUnknown = true
		}
		if m.Content == "runtime assistant answer" && m.Role == "assistant" {
			sawRuntimeAssistant = true
		}
	}
	if !sawUnknown {
		t.Fatal("unknown origin was dropped; curation must fail open")
	}
	if !sawRuntimeAssistant {
		t.Fatal("assistant turn with agent_runtime origin was dropped")
	}

	// User turn: cwd comes from the session, since the message carries none.
	user := msgs[0]
	if user.Role != "user" || user.SessionID != "sess-1" {
		t.Fatalf("user message wrong: %+v", user)
	}
	if user.ProjectDir != "/work/atlas" {
		t.Fatalf("user turn lost its project: %q", user.ProjectDir)
	}
	if user.SourceLocation != path || user.SourceOrder != 0 {
		t.Fatalf("provenance wrong: loc=%q order=%d", user.SourceLocation, user.SourceOrder)
	}

	// Assistant turn: cwd from its own path, order = message*100 + part.
	asst := msgs[1]
	if asst.Role != "assistant" || asst.ProjectDir != "/work/atlas" {
		t.Fatalf("assistant message wrong: %+v", asst)
	}
	if asst.SourceOrder != 100 {
		t.Fatalf("assistant source order = %d, want 100", asst.SourceOrder)
	}

	// Cursor is the max time_updated of emitted messages; the real store bumps
	// it at completion.
	if wm != "1700000030000" {
		t.Fatalf("watermark = %q, want 1700000030000", wm)
	}

	// countZCode stays an upper bound: it counts the synthetic text parts the
	// loader skips, keeping assess's Total >= New invariant.
	n, err := countZCode(path, nil)
	if err != nil || n != 11 {
		t.Fatalf("countZCode = %d (err=%v), want 11 curated text parts", n, err)
	}
}

func TestLoadZCodeIncrementalAndCompletion(t *testing.T) {
	path := zcodeFixture(t)
	_, wm, err := loadZCode(path, nil, "")
	if err != nil {
		t.Fatal(err)
	}

	// An in-flight turn completing bumps time_updated past the watermark, so it
	// is picked up on the next sync rather than lost behind the cursor.
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE message SET time_updated = 1700000040000,
		data = '{"role":"assistant","time":{"created":1700000006000,"completed":1700000040000},"path":{"cwd":"/work/atlas"}}'
		WHERE id = 'm3'`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	msgs, wm2, err := loadZCode(path, nil, wm)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, m := range msgs {
		if m.Content == "partial answer that is still streaming" {
			found = true
		}
		if m.TS.Before(msgs[0].TS) {
			t.Fatalf("stale message re-emitted past the watermark: %+v", m)
		}
	}
	if !found {
		t.Fatalf("completed in-flight turn was lost: %+v", msgs)
	}
	if wm2 != "1700000040000" {
		t.Fatalf("watermark = %q, want 1700000040000", wm2)
	}

	// Nothing new: the incoming cursor is preserved rather than regressed.
	db, _ = sql.Open("sqlite", "file:"+path)
	db.Exec(`UPDATE message SET time_updated = time_updated`)
	db.Close()
	_, wm3, err := loadZCode(path, nil, wm2)
	if err != nil {
		t.Fatal(err)
	}
	if wm3 != wm2 {
		t.Fatalf("watermark regressed on an empty pass: %q -> %q", wm2, wm3)
	}
}

func TestLoadZCodeMissingStore(t *testing.T) {
	if _, _, err := loadZCode(filepath.Join(t.TempDir(), "absent.sqlite"), nil, ""); err == nil {
		t.Fatal("expected an error for a missing ZCode store")
	}
	if _, err := countZCode(filepath.Join(t.TempDir(), "absent.sqlite"), nil); err == nil {
		t.Fatal("expected an error for a missing ZCode store")
	}
	_ = os.Stat
}
