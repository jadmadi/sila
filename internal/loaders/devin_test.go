package loaders

import (
	"database/sql"
	"encoding/json"
	"testing"

	_ "modernc.org/sqlite"
)

func testDevinSchema(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:devin.db?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	mustExec(t, db, `CREATE TABLE sessions (
		id TEXT PRIMARY KEY, working_directory TEXT, hidden INTEGER NOT NULL DEFAULT 0)`)
	mustExec(t, db, `CREATE TABLE message_nodes (
		row_id INTEGER PRIMARY KEY, session_id TEXT, created_at INTEGER, chat_message TEXT)`)
	mustExec(t, db, `CREATE TABLE prompt_history (
		id TEXT PRIMARY KEY, content TEXT, timestamp INTEGER, session_id TEXT, is_shell INTEGER DEFAULT 0)`)
	return db
}

func TestLoadDevin(t *testing.T) {
	db := testDevinSchema(t)
	mustExec(t, db, `INSERT INTO sessions(id,working_directory,hidden) VALUES('s1','/w/dream',0)`)
	mustExec(t, db, `INSERT INTO prompt_history(id,session_id,timestamp,content,is_shell) VALUES('p1','s1',170,'user prompt',0)`)
	// a shell one-liner is skipped (is_shell=1)
	mustExec(t, db, `INSERT INTO prompt_history(id,session_id,timestamp,content,is_shell) VALUES('p2','s1',171,'ls -la',1)`)
	// assistant conclusion
	mustExec(t, db, `INSERT INTO message_nodes(row_id,session_id,created_at,chat_message) VALUES
		(1,'s1',172, '{"role":"assistant","content":"the answer","tool_calls":null}')`)
	// pure tool chunk with no content -> skipped
	mustExec(t, db, `INSERT INTO message_nodes(row_id,session_id,created_at,chat_message) VALUES
		(2,'s1',173, '{"role":"assistant","content":"","tool_calls":[{"name":"bash"}]}')`)

	msgs, wm, err := loadDevin(db, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages (prompt + conclusion), got %d: %+v", len(msgs), msgs)
	}
	if wm == "" {
		t.Fatal("expected a non-empty watermark")
	}
}

func TestLoadDevinIncremental(t *testing.T) {
	db := testDevinSchema(t)
	mustExec(t, db, `INSERT INTO sessions(id,working_directory,hidden) VALUES('s1','/w/dream',0)`)
	mustExec(t, db, `INSERT INTO message_nodes VALUES(1,'s1',170, '{"role":"assistant","content":"one"}')`)
	mustExec(t, db, `INSERT INTO message_nodes VALUES(2,'s1',171, '{"role":"assistant","content":"two"}')`)

	msgs, wm, err := loadDevin(db, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("full pass expected 2, got %d", len(msgs))
	}

	mustExec(t, db, `INSERT INTO message_nodes VALUES(3,'s1',172, '{"role":"assistant","content":"three"}')`)
	msgs2, _, err := loadDevin(db, nil, wm)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs2) != 1 || msgs2[0].Content != "three" {
		t.Fatalf("incremental pass wrong: %+v", msgs2)
	}
}

func TestLoadDevinToolActivity(t *testing.T) {
	db := testDevinSchema(t)
	mustExec(t, db, `INSERT INTO sessions(id,working_directory,hidden) VALUES('s1','/w/dream',0)`)
	mustExec(t, db, `INSERT INTO message_nodes(row_id,session_id,created_at,chat_message) VALUES
		(1,'s1',170,'{"role":"assistant","content":"","tool_calls":[{"name":"exec","arguments":{"command":"go test ./..."}},{"name":"edit","arguments":{"file_path":"/w/dream/main.go"}}]}')`)
	// bash is not part of the curated activity layer
	mustExec(t, db, `INSERT INTO message_nodes(row_id,session_id,created_at,chat_message) VALUES
		(2,'s1',171,'{"role":"assistant","content":"","tool_calls":[{"name":"bash","arguments":{"command":"ignored"}}]}')`)
	mustExec(t, db, `INSERT INTO message_nodes(row_id,session_id,created_at,chat_message) VALUES
		(3,'s1',172,'{"role":"assistant","content":"","tool_calls":[{"name":"write","arguments":{"file_path":"/w/dream/new.go"}}]}')`)

	msgs, wm, err := loadDevin(db, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 tool-activity messages, got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != "command" || msgs[0].Content != "go test ./..." {
		t.Fatalf("bad command message: %+v", msgs[0])
	}
	if msgs[1].Role != "edit" || msgs[1].Content != "/w/dream/main.go" {
		t.Fatalf("bad edit message: %+v", msgs[1])
	}
	if msgs[2].Role != "edit" || msgs[2].Content != "/w/dream/new.go" {
		t.Fatalf("bad write message: %+v", msgs[2])
	}

	var w devinWatermark
	if err := json.Unmarshal([]byte(wm), &w); err != nil {
		t.Fatalf("watermark: %v", err)
	}
	if w.Tools["s1"] != 3 {
		t.Fatalf("expected tools cursor 3, got %d", w.Tools["s1"])
	}

	// incremental: only the new node is emitted
	mustExec(t, db, `INSERT INTO message_nodes(row_id,session_id,created_at,chat_message) VALUES
		(4,'s1',173,'{"role":"assistant","content":"","tool_calls":[{"name":"exec","arguments":{"command":"go build ./..."}}]}')`)
	msgs2, _, err := loadDevin(db, nil, wm)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs2) != 1 || msgs2[0].Role != "command" || msgs2[0].Content != "go build ./..." {
		t.Fatalf("incremental tool pass wrong: %+v", msgs2)
	}
}

func TestCountDevinIncludesToolActivity(t *testing.T) {
	db := testDevinSchema(t)
	mustExec(t, db, `INSERT INTO sessions(id,working_directory,hidden) VALUES('s1','/w/dream',0)`)
	mustExec(t, db, `INSERT INTO message_nodes(row_id,session_id,created_at,chat_message) VALUES
		(1,'s1',170,'{"role":"assistant","content":"","tool_calls":[{"name":"exec","arguments":{"command":"go test ./..."}}]}'),
		(2,'s1',171,'{"role":"assistant","content":"","tool_calls":[{"name":"edit","arguments":{"file_path":"/w/dream/main.go"}}]}')`)

	n, err := countDevin(db, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("expected 2 tool-activity messages in count, got %d", n)
	}
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}

func TestDevinPromptsIncremental(t *testing.T) {
	db := testDevinSchema(t)
	mustExec(t, db, `INSERT INTO sessions(id,working_directory,hidden) VALUES('s1','/w/dream',0)`)
	mustExec(t, db, `INSERT INTO prompt_history(id,session_id,timestamp,content,is_shell) VALUES('p1','s1',100,'old',0)`)
	msgs, mx, err := loadDevinPrompts(db, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || mx != 100 {
		t.Fatalf("full prompt pass wrong: %d msgs, mx=%d", len(msgs), mx)
	}
	msgs2, _, err := loadDevinPrompts(db, nil, mx)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs2) != 0 {
		t.Fatalf("expected no re-sent prompts, got %d", len(msgs2))
	}
}

// TestLoadDevinPerSessionCursorIsIndependent pins the rewrite of the nodes and
// tool-activity cursors: each session advances on its own, so a session whose
// only remaining nodes are tool calls neither re-emits another session's
// history nor keeps a low floor that forces a full rescan of the table.
//
// The cursor must advance past rows that are filtered out by content; otherwise
// that session is rescanned forever and — under the previous single global
// floor — so is every other session's history (281,833 of 281,931 rows on the
// maintainer's real store, 12.5s per sync).
func TestLoadDevinPerSessionCursorIsIndependent(t *testing.T) {
	db := testDevinSchema(t)
	// Session A: one assistant conclusion, consumed first.
	mustExec(t, db, `INSERT INTO sessions (id, working_directory, hidden) VALUES ('sessA', '/work/a', 0)`)
	mustExec(t, db, `INSERT INTO message_nodes (row_id, session_id, created_at, chat_message)
		VALUES (1, 'sessA', 1700000000, '{"role":"assistant","content":"conclusion A"}')`)
	// Session B: a conclusion, then a run of nodes that are pure tool calls.
	mustExec(t, db, `INSERT INTO sessions (id, working_directory, hidden) VALUES ('sessB', '/work/b', 0)`)
	mustExec(t, db, `INSERT INTO message_nodes (row_id, session_id, created_at, chat_message)
		VALUES (2, 'sessB', 1700000001, '{"role":"assistant","content":"conclusion B"}')`)
	for row := 3; row <= 7; row++ {
		mustExec(t, db, `INSERT INTO message_nodes (row_id, session_id, created_at, chat_message)
			VALUES (?, 'sessB', 1700000002, '{"role":"assistant","content":"","tool_calls":[{"name":"exec","arguments":{"command":"ls"}}]}')`, row)
	}

	// First pass: both sessions are new, so both conclusions are emitted and
	// session B's cursor advances past its tool-call rows.
	msgs, seen, err := loadDevinNodes(db, nil, map[string]int64{})
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("first pass emitted %d messages, want 2: %+v", len(msgs), msgs)
	}
	if seen["sessB"] != 7 {
		t.Fatalf("session B cursor = %d, want 7 (it must advance past filtered rows)", seen["sessB"])
	}

	// Second pass with those cursors: nothing is new, in either session.
	msgs2, seen2, err := loadDevinNodes(db, nil, seen)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs2) != 0 {
		t.Fatalf("second pass re-emitted %d messages: %+v", len(msgs2), msgs2)
	}
	if seen2["sessA"] != 1 || seen2["sessB"] != 7 {
		t.Fatalf("cursors regressed: %+v", seen2)
	}

	// A new conclusion in the stalled session is picked up on its own, without
	// disturbing the other session's cursor.
	mustExec(t, db, `INSERT INTO message_nodes (row_id, session_id, created_at, chat_message)
		VALUES (8, 'sessB', 1700000003, '{"role":"assistant","content":"conclusion B2"}')`)
	msgs3, seen3, err := loadDevinNodes(db, nil, seen2)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs3) != 1 || msgs3[0].Content != "conclusion B2" {
		t.Fatalf("new conclusion not picked up: %+v", msgs3)
	}
	if seen3["sessA"] != 1 {
		t.Fatalf("session A cursor moved without new rows: %d", seen3["sessA"])
	}
}
