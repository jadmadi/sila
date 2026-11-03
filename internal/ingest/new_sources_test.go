package ingest

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/loaders"
	"github.com/jadmadi/sila/internal/store"

	_ "modernc.org/sqlite"
)

// TestNewSourcesLandWithSessionProvenance proves end-to-end that each new
// loader's messages reach `messages` and, critically, `source_occurrences` —
// session provenance is what makes `sila search expand` work, and a loader that
// forgets it fails silently (the empty state looks the same as "no context").
func TestNewSourcesLandWithSessionProvenance(t *testing.T) {
	dir := t.TempDir()

	// ZCode fixture: a session with one user and one completed assistant turn.
	zcodePath := filepath.Join(dir, "zcode.sqlite")
	zdb, err := sql.Open("sqlite", "file:"+zcodePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE session (id TEXT PRIMARY KEY, directory TEXT)`,
		`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, time_created INTEGER, time_updated INTEGER, data TEXT, sequence INTEGER)`,
		`CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT, session_id TEXT, time_created INTEGER, time_updated INTEGER, data TEXT, sequence INTEGER)`,
		`INSERT INTO session VALUES ('zc-1','/work/atlas')`,
		`INSERT INTO message VALUES ('m1','zc-1',1700000000000,1700000000000,'{"role":"user","time":{"created":1700000000000}}',0)`,
		`INSERT INTO part VALUES ('p1','m1','zc-1',1700000000000,1700000000000,'{"type":"text","text":"zcode question"}',0)`,
		`INSERT INTO message VALUES ('m2','zc-1',1700000001000,1700000002000,'{"role":"assistant","time":{"created":1700000001000,"completed":1700000002000},"path":{"cwd":"/work/atlas"}}',1)`,
		`INSERT INTO part VALUES ('p2','m2','zc-1',1700000001000,1700000001000,'{"type":"text","text":"zcode answer"}',0)`,
	} {
		if _, err := zdb.Exec(stmt); err != nil {
			t.Fatalf("zcode fixture: %v", err)
		}
	}
	zdb.Close()

	// Claude Code fixture: one transcript in a slugified project directory.
	claudeRoot := filepath.Join(dir, "claude-projects")
	claudeDir := filepath.Join(claudeRoot, "-work-atlas")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "cc-1.jsonl"), []byte(
		`{"type":"user","sessionId":"cc-1","timestamp":"2026-09-01T10:00:00.000Z","cwd":"/work/atlas","message":{"role":"user","content":"claude question"}}`+"\n"+
			`{"type":"assistant","sessionId":"cc-1","timestamp":"2026-09-01T10:00:01.000Z","cwd":"/work/atlas","message":{"role":"assistant","content":[{"type":"text","text":"claude answer"}]}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Muse fixture: a frame-wrapped user intent and an assistant commit.
	museRoot := filepath.Join(dir, "muse-sessions")
	museDir := filepath.Join(museRoot, "2026", "09", "15", "mu-1")
	if err := os.MkdirAll(museDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(museDir, "session.jsonl"), []byte(
		`{"schema_version":1,"sequence":10,"recorded_at":1700000000000000,"payload_type":"session.workspace_branch.observed","stream":{"id":"mu-1"},"payload":{"record":{"workspace_root":"/work/atlas"}}}`+"\n"+
			`{"schema_version":1,"sequence":18,"recorded_at":1700000001000000,"payload_type":"runtime.user_intent.accepted","stream":{"id":"mu-1"},"payload":{"refill_blocks":[{"text":"muse question"}]}}`+"\n"+
			`{"schema_version":1,"sequence":22,"recorded_at":1700000002000000,"payload_type":"runtime.session","stream":{"id":"mu-1"},"payload":{"event":{"kind":"assistant_message_committed","text":"muse answer"}}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mapSrc := map[string]*loaders.Source{
		"zcode":       {Name: "zcode", DBPath: zcodePath, New: loaders.NewZCodeLoader},
		"claude-code": {Name: "claude-code", DataDir: claudeRoot, New: loaders.NewClaudeCodeLoader},
		"muse":        {Name: "muse", DataDir: museRoot, New: loaders.NewMuseLoader},
	}

	st, err := store.Open(filepath.Join(dir, "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	results, err := runWithSources(st, mapSrc, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 tool results, got %d", len(results))
	}
	for _, r := range results {
		if r.Error != nil {
			t.Fatalf("tool %s failed: %v", r.Tool, r.Error)
		}
	}

	p, err := st.EnsureProject("/work/atlas")
	if err != nil {
		t.Fatal(err)
	}

	// Every curated message must be present, with a session id...
	msgs, err := st.RecentMessages(p.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 6 {
		t.Fatalf("expected 6 messages (2 per tool), got %d: %+v", len(msgs), msgs)
	}
	byContent := map[string]store.StoredMessage{}
	for _, m := range msgs {
		byContent[m.Content] = m
	}
	for _, want := range []string{"zcode question", "zcode answer", "claude question", "claude answer", "muse question", "muse answer"} {
		if _, ok := byContent[want]; !ok {
			t.Fatalf("message %q missing; have %v", want, keysOf(byContent))
		}
	}

	// ...and each one must resolve a session, so `search expand` works.
	for content, m := range byContent {
		ctx, err := st.MessageInContext(m.ID, 2, 0)
		if err != nil {
			t.Fatalf("expand %q: %v", content, err)
		}
		if !ctx.SessionAvailable {
			t.Fatalf("message %q has no session provenance: %s", content, ctx.Note)
		}
		if ctx.OccurrenceCount == 0 {
			t.Fatalf("message %q recorded no source occurrence", content)
		}
	}

	// A same-session window must contain the sibling turn, proving the session
	// id is shared across a tool's messages rather than per-message unique.
	ctx, err := st.MessageInContext(byContent["zcode answer"].ID, 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ctx.Before) == 0 || ctx.Before[len(ctx.Before)-1].Content != "zcode question" {
		t.Fatalf("session window wrong: before=%+v", ctx.Before)
	}
}

func keysOf(m map[string]store.StoredMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestNewSourcesIncrementalSyncIsStable proves a second sync over unchanged
// sources adds nothing and does not regress the cursors.
func TestNewSourcesIncrementalSyncIsStable(t *testing.T) {
	dir := t.TempDir()
	claudeRoot := filepath.Join(dir, "claude")
	claudeDir := filepath.Join(claudeRoot, "-work-site")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "s.jsonl"), []byte(
		`{"type":"user","sessionId":"s1","timestamp":"2026-09-01T10:00:00.000Z","cwd":"/work/site","message":{"role":"user","content":"only turn"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mapSrc := map[string]*loaders.Source{
		"claude-code": {Name: "claude-code", DataDir: claudeRoot, New: loaders.NewClaudeCodeLoader},
	}
	st, err := store.Open(filepath.Join(dir, "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	first, err := runWithSources(st, mapSrc, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if first[0].NewRecords != 1 {
		t.Fatalf("first sync new records = %d, want 1", first[0].NewRecords)
	}
	wmBefore, _ := st.GetWatermark("", "claude-code")

	second, err := runWithSources(st, mapSrc, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range second {
		if r.NewRecords != 0 {
			t.Fatalf("unchanged sync added %d records", r.NewRecords)
		}
	}
	wmAfter, _ := st.GetWatermark("", "claude-code")
	if wmBefore != wmAfter {
		t.Fatalf("cursor regressed: %q -> %q", wmBefore, wmAfter)
	}
}
