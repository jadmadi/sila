package loaders

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestLoadMiMoOpenCode(t *testing.T) {
	db := mustSchema(t)
	mustExec(t, db, `INSERT INTO session(id,directory) VALUES('s1','/workspace/dream')`)
	mustExec(t, db, `INSERT INTO message(id,session_id,data,time_created) VALUES
		('m1','s1','{"role":"user"}',1700000000000)`)
	mustExec(t, db, `INSERT INTO part(id,message_id,session_id,data) VALUES
		('p1','m1','s1','{"type":"text","text":"hello"}')`)

	msgs, wm, err := loadMiMoOpenCode(db, "mimocode-test", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Content != "hello" || msgs[0].Role != "user" || msgs[0].ProjectDir != "/workspace/dream" {
		t.Fatalf("bad message: %+v", msgs[0])
	}
	if wm != "1700000000000" {
		t.Fatalf("watermark = %q", wm)
	}
}

func TestLoadMiMoOpenCodeJoinsPartTextAndSkipsToolchunks(t *testing.T) {
	db := mustSchema(t)
	mustExec(t, db, `INSERT INTO session(id,directory) VALUES('s1','/workspace/dream')`)
	// one assistant message made of multiple text parts -> concatenated
	mustExec(t, db, `INSERT INTO message(id,session_id,data,time_created) VALUES('m1','s1','{"role":"assistant"}',1700000000001)`)
	mustExec(t, db, `INSERT INTO part(id,message_id,session_id,data) VALUES
		('p1','m1','s1','{"type":"reasoning","text":"think"}'),
		('p2','m1','s1','{"type":"text","text":" answer"}'),
		('p3','m1','s1','{"type":"step-start"}')`)
	// empty-content assistant message -> skipped
	mustExec(t, db, `INSERT INTO message(id,session_id,data,time_created) VALUES('m2','s1','{"role":"assistant"}',1700000000002)`)
	mustExec(t, db, `INSERT INTO part(id,message_id,session_id,data) VALUES('p4','m2','s1','{"type":"tool","tool_call":"bash"}')`)

	msgs, _, err := loadMiMoOpenCode(db, "mimocode-test", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Content != "think answer" {
		t.Fatalf("expected concatenated parts, got %q", msgs[0].Content)
	}
}

func TestLoadMiMoOpenCodeIncremental(t *testing.T) {
	db := mustSchema(t)
	mustExec(t, db, `INSERT INTO session(id,directory) VALUES('s1','/w/dream')`)
	mustExec(t, db, `INSERT INTO message(id,session_id,data,time_created) VALUES('m1','s1','{"role":"assistant"}',1000)`)
	mustExec(t, db, `INSERT INTO part(id,message_id,session_id,data) VALUES('p1','m1','s1','{"type":"text","text":"one"}')`)

	_, wm, err := loadMiMoOpenCode(db, "mimocode-test", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	// add a newer message, re-run with watermark -> only it is emitted
	mustExec(t, db, `INSERT INTO message(id,session_id,data,time_created) VALUES('m2','s1','{"role":"assistant"}',2000)`)
	mustExec(t, db, `INSERT INTO part(id,message_id,session_id,data) VALUES('p2','m2','s1','{"type":"text","text":"two"}')`)
	msgs, _, err := loadMiMoOpenCode(db, "mimocode-test", nil, wm)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Content != "two" {
		t.Fatalf("incremental pass wrong: %+v", msgs)
	}
}

func TestLoadMiMoOpenCodePreservesWatermarkWhenEmpty(t *testing.T) {
	db := mustSchema(t)
	msgs, wm, err := loadMiMoOpenCode(db, "mimocode-test", nil, "1700000000000")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Fatalf("expected 0 messages, got %d", len(msgs))
	}
	if wm != "1700000000000" {
		t.Fatalf("expected watermark to be preserved as '1700000000000', got %q", wm)
	}
}

func TestLoadMiMoOpenCodeScopedDoesNotRegressWatermark(t *testing.T) {
	db := mustSchema(t)
	mustExec(t, db, `INSERT INTO session(id,directory) VALUES('s1','/workspace/other')`)
	mustExec(t, db, `INSERT INTO message(id,session_id,data,time_created) VALUES('m1','s1','{"role":"user"}',1700000000000)`)
	mustExec(t, db, `INSERT INTO part(id,message_id,session_id,data) VALUES('p1','m1','s1','{"type":"text","text":"hello"}')`)

	proj := "prompts"
	msgs, wm, err := loadMiMoOpenCode(db, "mimocode-test", &proj, "1700000000000")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Fatalf("expected 0 messages, got %d", len(msgs))
	}
	if wm != "1700000000000" {
		t.Fatalf("expected watermark to be preserved as '1700000000000', got %q", wm)
	}
}

func TestLoadOpenCodeV2CuratedText(t *testing.T) {
	db := mustV2Schema(t)
	mustExec(t, db, `INSERT INTO session_v2(id,directory) VALUES('s1','/workspace/v2-app')`)
	mustExec(t, db, `INSERT INTO session_message(id,session_id,type,time_created,time_updated,data) VALUES
		('m1','s1','user',1000,1000,'{"text":"hello","time":{"created":1000}}')`)
	mustExec(t, db, `INSERT INTO session_message(id,session_id,type,time_created,time_updated,data) VALUES
		('m2','s1','assistant',2000,2500,'{"time":{"created":2000,"completed":2500},"content":[{"type":"reasoning","text":"think"},{"type":"text","text":"answer one"},{"type":"tool","tool":"bash"},{"type":"text","text":"answer two"}]}')`)
	// in-flight assistant without completion -> skipped this pass
	mustExec(t, db, `INSERT INTO session_message(id,session_id,type,time_created,time_updated,data) VALUES
		('m3','s1','assistant',3000,3000,'{"time":{"created":3000},"content":[{"type":"text","text":"partial"}]}')`)

	msgs, wm, err := loadOpenCodeV2(db, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].Content != "hello" || msgs[0].Role != "user" || msgs[0].ProjectDir != "/workspace/v2-app" {
		t.Fatalf("bad user message: %+v", msgs[0])
	}
	if msgs[1].Content != "answer one\n\nanswer two" || msgs[1].Role != "assistant" {
		t.Fatalf("expected joined text parts without reasoning/tool, got %q", msgs[1].Content)
	}
	// watermark stops at the newest emitted row, so m3 is retried next pass
	if wm != "2000" {
		t.Fatalf("watermark = %q, want 2000", wm)
	}

	// completing m3 makes the next pass emit it
	mustExec(t, db, `UPDATE session_message SET data='{"time":{"created":3000,"completed":3500},"content":[{"type":"text","text":"completed late"}]}' WHERE id='m3'`)
	msgs, wm, err = loadOpenCodeV2(db, nil, wm)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Content != "completed late" {
		t.Fatalf("expected completed late message, got %+v", msgs)
	}
	if wm != "3000" {
		t.Fatalf("watermark = %q, want 3000", wm)
	}
}

func TestLoadOpenCodeV2ProjectScoped(t *testing.T) {
	db := mustV2Schema(t)
	mustExec(t, db, `INSERT INTO session_v2(id,directory) VALUES('s1','/workspace/v2-app'),('s2','/workspace/other-app')`)
	mustExec(t, db, `INSERT INTO session_message(id,session_id,type,time_created,time_updated,data) VALUES
		('m1','s1','user',1000,1000,'{"text":"mine","time":{"created":1000}}'),
		('m2','s2','user',1000,1000,'{"text":"theirs","time":{"created":1000}}')`)

	proj := "v2-app"
	msgs, _, err := loadOpenCodeV2(db, &proj, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Content != "mine" {
		t.Fatalf("expected only the scoped session, got %+v", msgs)
	}
}

func TestCountOpenCodeV2(t *testing.T) {
	db := mustV2Schema(t)
	mustExec(t, db, `INSERT INTO session_v2(id,directory) VALUES('s1','/workspace/v2-app')`)
	mustExec(t, db, `INSERT INTO session_message(id,session_id,type,time_created,time_updated,data) VALUES
		('m1','s1','user',1000,1000,'{"text":"hello","time":{"created":1000}}'),
		('m2','s1','assistant',2000,2500,'{"time":{"created":2000,"completed":2500},"content":[{"type":"text","text":"answer"}]}'),
		('m3','s1','assistant',3000,3000,'{"time":{"created":3000},"content":[{"type":"text","text":"partial"}]}'),
		('m4','s1','system',4000,4000,'{"text":"ignored"}')`)

	n, err := countOpenCodeV2(db, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("expected 2 curated messages, got %d", n)
	}
}

func mustV2Schema(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:memo_v2.db?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	mustExec(t, db, `CREATE TABLE session_v2 (id TEXT PRIMARY KEY, directory TEXT, title TEXT, time_created INTEGER, time_updated INTEGER)`)
	mustExec(t, db, `CREATE TABLE session_message (id TEXT PRIMARY KEY, session_id TEXT, type TEXT, seq INTEGER, time_created INTEGER, time_updated INTEGER, data TEXT)`)
	return db
}

// TestLoadMiMoCodeActorMarkers pins the MiMoCode curation: sub-agent actors
// are runtime work and are skipped, harness-generated synthetic parts are
// dropped, and an unregistered actor fails open only for the `main` default.
func TestLoadMiMoCodeActorMarkers(t *testing.T) {
	db := mustMiMoMarkerSchema(t)
	mustExec(t, db, `INSERT INTO session(id,directory,parent_id) VALUES
		('root1','/work/atlas',''),
		('root2','/work/atlas',''),
		('child1','/work/atlas','root1')`)
	mustExec(t, db, `INSERT INTO actor_registry(session_id,actor_id,mode) VALUES
		('root1','main','main'),
		('child1','checkpoint-writer-1','subagent'),
		('root1','general-1','subagent')`)

	insert := func(id, session, agent, role, text string, ts int64) {
		t.Helper()
		mustExec(t, db, `INSERT INTO message(id,session_id,agent_id,data,time_created) VALUES(?,?,?,?,?)`,
			id, session, agent, `{"role":"`+role+`"}`, ts)
		mustExec(t, db, `INSERT INTO part(id,message_id,session_id,data) VALUES(?,?,?,?)`,
			"p-"+id, id, session, `{"type":"text","text":`+jsonString(text)+`}`)
	}

	insert("m-main", "root1", "main", "user", "real human prompt", 1000)
	insert("m-unregistered", "root2", "main", "user", "unregistered main prompt", 2000)
	insert("m-sub", "root1", "general-1", "user", "You are juror 2 of 3, knock this down", 3000)
	insert("m-mystery", "root2", "mystery-1", "user", "unregistered sub-agent prompt", 4000)
	insert("m-child", "child1", "checkpoint-writer-1", "user", "<system-reminder> mode prompt", 5000)
	insert("m-asst-sub", "root1", "general-1", "assistant", "sub-agent narration", 6000)

	// A mixed message keeps its human part and drops the synthetic reminder.
	mustExec(t, db, `INSERT INTO message(id,session_id,agent_id,data,time_created) VALUES('m-mixed','root1','main','{"role":"user"}',7000)`)
	mustExec(t, db, `INSERT INTO part(id,message_id,session_id,data) VALUES
		('p-mixed-1','m-mixed','root1','{"type":"text","text":"help me deploy"}'),
		('p-mixed-2','m-mixed','root1','{"type":"text","text":"<system-reminder> Skills available","synthetic":true}')`)
	// An all-synthetic message disappears entirely.
	mustExec(t, db, `INSERT INTO message(id,session_id,agent_id,data,time_created) VALUES('m-synth','root1','main','{"role":"user"}',8000)`)
	mustExec(t, db, `INSERT INTO part(id,message_id,session_id,data) VALUES
		('p-synth-1','m-synth','root1','{"type":"text","text":"<system-reminder> You are about to finish","synthetic":true}')`)
	// Reasoning still joins with text for an ingested message.
	mustExec(t, db, `INSERT INTO message(id,session_id,agent_id,data,time_created) VALUES('m-reason','root1','main','{"role":"assistant"}',9000)`)
	mustExec(t, db, `INSERT INTO part(id,message_id,session_id,data) VALUES
		('p-reason-1','m-reason','root1','{"type":"reasoning","text":"think "}'),
		('p-reason-2','m-reason','root1','{"type":"text","text":"answer"}')`)

	msgs, _, err := loadMiMoOpenCode(db, "mimocode-test", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, m := range msgs {
		got[m.Content] = m.Role
	}
	want := map[string]string{
		"real human prompt":        "user",
		"unregistered main prompt": "user",
		"help me deploy":           "user",
		"think answer":             "assistant",
	}
	if len(got) != len(want) {
		t.Fatalf("curated messages = %v, want %v", got, want)
	}
	for content, role := range want {
		if got[content] != role {
			t.Errorf("missing curated message %q (%s); got %v", content, role, got)
		}
	}

	n, err := countMiMoOpenCode(db, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if n != int64(len(want)) {
		t.Errorf("countMiMoOpenCode = %d, want %d", n, len(want))
	}
}

// TestLoadMiMoCodeToleratesMalformedPart pins the json_valid guard: one part
// row whose data is not valid JSON must not fail the whole sync.
func TestLoadMiMoCodeToleratesMalformedPart(t *testing.T) {
	db := mustMiMoMarkerSchema(t)
	mustExec(t, db, `INSERT INTO session(id,directory,parent_id) VALUES('root1','/work/atlas','')`)
	mustExec(t, db, `INSERT INTO actor_registry(session_id,actor_id,mode) VALUES('root1','main','main')`)
	mustExec(t, db, `INSERT INTO message(id,session_id,agent_id,data,time_created) VALUES
		('m-good','root1','main','{"role":"user"}',1000),
		('m-bad','root1','main','{"role":"user"}',2000)`)
	mustExec(t, db, `INSERT INTO part(id,message_id,session_id,data) VALUES
		('p-good','m-good','root1','{"type":"text","text":"kept"}'),
		('p-bad','m-bad','root1','{"type":"text","text":')`)

	msgs, _, err := loadMiMoOpenCode(db, "mimocode-test", nil, "")
	if err != nil {
		t.Fatalf("a malformed part must not fail the load: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Content != "kept" {
		t.Fatalf("malformed part changed the curated set: %+v", msgs)
	}
	if _, err := countMiMoOpenCode(db, nil, true); err != nil {
		t.Fatalf("a malformed part must not fail the count: %v", err)
	}
}

// TestLoadOpenCodeHasNoActorMarkers keeps the shared loader honest: a store
// without MiMoCode's columns keeps ingesting exactly what it did before.
func TestLoadOpenCodeHasNoActorMarkers(t *testing.T) {
	db := mustSchema(t)
	mustExec(t, db, `INSERT INTO session(id,directory) VALUES('s1','/workspace/oc')`)
	mustExec(t, db, `INSERT INTO message(id,session_id,data,time_created) VALUES('m1','s1','{"role":"user"}',1000)`)
	mustExec(t, db, `INSERT INTO part(id,message_id,session_id,data) VALUES
		('p1','m1','s1','{"type":"text","text":"synthetic-looking but unmarked","synthetic":true}')`)

	if hasMiMoActorMarkers(db) {
		t.Fatal("a store without agent_id/actor_registry must not report actor markers")
	}
	msgs, _, err := loadMiMoOpenCode(db, "opencode", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Content != "synthetic-looking but unmarked" {
		t.Fatalf("unmarked store behavior changed: %+v", msgs)
	}
}

func mustMiMoMarkerSchema(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:mimo_markers.db?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	mustExec(t, db, `CREATE TABLE session (id TEXT PRIMARY KEY, directory TEXT, parent_id TEXT)`)
	mustExec(t, db, `CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, agent_id TEXT NOT NULL DEFAULT 'main', data TEXT, time_created INTEGER)`)
	mustExec(t, db, `CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT, session_id TEXT, data TEXT)`)
	mustExec(t, db, `CREATE TABLE actor_registry (session_id TEXT NOT NULL, actor_id TEXT NOT NULL, mode TEXT NOT NULL, PRIMARY KEY (session_id, actor_id))`)
	return db
}

// jsonString escapes a Go string for embedding in a JSON document.
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func mustSchema(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:memo.db?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	mustExec(t, db, `CREATE TABLE session (id TEXT PRIMARY KEY, directory TEXT)`)
	mustExec(t, db, `CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, data TEXT, time_created INTEGER)`)
	mustExec(t, db, `CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT, session_id TEXT, data TEXT)`)
	return db
}

func seedOpenCodeStore(t *testing.T, path, text string, ts int64) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	queries := []string{
		`CREATE TABLE session (id TEXT PRIMARY KEY, directory TEXT)`,
		`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, time_created INTEGER, data TEXT)`,
		`CREATE TABLE part (message_id TEXT, data TEXT)`,
		`INSERT INTO session (id, directory) VALUES ('s1', '/work/proj')`,
		fmt.Sprintf(`INSERT INTO message (id, session_id, time_created, data) VALUES ('m1', 's1', %d, '{"role":"user"}')`, ts),
		fmt.Sprintf(`INSERT INTO part (message_id, data) VALUES ('m1', '{"type":"text","text":"%s"}')`, text),
	}
	for _, query := range queries {
		if _, err := db.Exec(query); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
}

func TestOpenCodeFlatpakXDGAndWALDiscovery(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	native := filepath.Join(home, ".local", "share", "opencode", "opencode.db")
	flatpak := filepath.Join(home, ".var", "app", "ai.opencode.opencode", "data", "opencode", "opencode.db")
	xdgDB := filepath.Join(xdg, "opencode", "opencode.db")
	seedOpenCodeStore(t, native, "native", 1000)
	seedOpenCodeStore(t, flatpak, "flatpak", 2000)
	seedOpenCodeStore(t, xdgDB, "xdg", 3000)
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", xdg)

	paths := OpenCodeDBPaths(home)
	if len(paths) != 3 {
		t.Fatalf("OpenCodeDBPaths = %v, want native, Flatpak, and XDG", paths)
	}

	wal := flatpak + "-wal"
	if err := os.WriteFile(wal, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(wal, future, future); err != nil {
		t.Fatal(err)
	}
	paths = OpenCodeDBPaths(home)
	if len(paths) == 0 || paths[0] != flatpak {
		t.Fatalf("WAL mtime did not win: %v", paths)
	}

	loader := NewOpenCodeLoader("")
	msgs, _, err := loader.Load("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Fatalf("Load returned %d messages, want 3", len(msgs))
	}
	count, err := loader.Count(nil)
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("Count = %d, want 3", count)
	}
}
