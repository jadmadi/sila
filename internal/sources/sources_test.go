package sources

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/jadmadi/sila/internal/store"
)

// seedCodexHome creates a fake home with a codex history.jsonl holding three
// records across two distinct sessions.
func seedCodexHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	history := "" +
		`{"session_id":"s1","ts":100,"text":"hello"}` + "\n" +
		`{"session_id":"s1","ts":200,"text":"again"}` + "\n" +
		`{"session_id":"s2","ts":300,"text":"other session"}` + "\n" +
		`{"session_id":"","ts":400,"text":"no session id"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "history.jsonl"), []byte(history), 0o644); err != nil {
		t.Fatal(err)
	}
	return home
}

func findTool(m Matrix, name string) Tool {
	for _, t := range m.Tools {
		if t.Name == name {
			return t
		}
	}
	return Tool{Name: name, Status: StatusMissing}
}

func TestDiscoverMissingTools(t *testing.T) {
	m := discoverWith(t.TempDir(), nil)
	if len(m.Tools) == 0 {
		t.Fatal("expected the known-tool matrix, got none")
	}
	for _, name := range []string{"codex", "devin", "claude-code", "cursor", "agy", "zed"} {
		got := findTool(m, name)
		if got.Status != StatusMissing {
			t.Errorf("%s: status = %q, want missing", name, got.Status)
		}
		if got.Guidance == "" {
			t.Errorf("%s: missing tools should still carry guidance", name)
		}
		if len(got.Paths) != 0 {
			t.Errorf("%s: expected no detected paths, got %v", name, got.Paths)
		}
	}
	if m.Host.StoreExists {
		t.Error("no store should exist in a hermetic home")
	}
}

func TestDiscoverReadyToolCountsSessions(t *testing.T) {
	home := seedCodexHome(t)
	m := discoverWith(home, nil)
	got := findTool(m, "codex")
	if got.Status != StatusReady {
		t.Fatalf("codex status = %q, want ready", got.Status)
	}
	if got.Sessions != 2 {
		t.Errorf("codex sessions = %d, want 2 (distinct session ids)", got.Sessions)
	}
	if got.Indexed {
		t.Error("codex should not be indexed without a watermark")
	}
	if len(got.Paths) == 0 || got.Paths[0].Path != filepath.Join(home, ".codex", "history.jsonl") {
		t.Errorf("codex paths = %v, want history.jsonl hit", got.Paths)
	}
}

func TestDiscoverIndexedToolWithStore(t *testing.T) {
	home := seedCodexHome(t)
	st, err := store.Open(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.SetWatermark("", "codex", "350"); err != nil {
		t.Fatal(err)
	}

	m := discoverWith(home, st)
	got := findTool(m, "codex")
	if got.Status != StatusIndexed {
		t.Fatalf("codex status = %q, want indexed", got.Status)
	}
	if !got.Indexed {
		t.Error("codex should report indexed=true")
	}
}

// TestDiscoverDetectedNotIngestable covers tools sila finds on disk but
// deliberately does not index, and must explain why.
func TestDiscoverDetectedNotIngestable(t *testing.T) {
	home := t.TempDir()
	// Droid records user turns only — assistant output is never persisted.
	droidDir := filepath.Join(home, ".factory", "sessions", "-work-atlas")
	if err := os.MkdirAll(droidDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(droidDir, "s1.jsonl"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Grok transcripts carry no per-message timestamps.
	grokDir := filepath.Join(home, ".grok", "sessions", "sid-1")
	if err := os.MkdirAll(grokDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(grokDir, "chat_history.jsonl"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := discoverWith(home, nil)
	for _, name := range []string{"droid", "grok"} {
		got := findTool(m, name)
		if got.Status != StatusDetected {
			t.Fatalf("%s status = %q, want detected", name, got.Status)
		}
		if got.Indexed {
			t.Errorf("detected-only tool %s can never be indexed", name)
		}
		if got.Guidance == "" {
			t.Errorf("detected tool %s should explain why it is not indexed", name)
		}
	}
}

// TestDiscoverClaudeCodeIsIngestable pins the reverse: Claude Code transcripts
// are indexed, so a project with session files reports as ready.
func TestDiscoverClaudeCodeIsIngestable(t *testing.T) {
	home := t.TempDir()
	proj := filepath.Join(home, ".claude", "projects", "proj-a")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"s1.jsonl", "s2.jsonl", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(proj, f), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	m := discoverWith(home, nil)
	got := findTool(m, "claude-code")
	if got.Status == StatusDetected {
		t.Fatalf("claude-code is ingestable but reported as detected (%q)", got.Guidance)
	}
	if got.Sessions != 2 {
		t.Errorf("claude-code sessions = %d, want 2 (jsonl files only)", got.Sessions)
	}
	if strings.Contains(got.Guidance, "does not index") {
		t.Errorf("ingestable tool still claims it is not indexed: %q", got.Guidance)
	}
}

func TestDiscoverCursorWorkspaceStorages(t *testing.T) {
	home := t.TempDir()
	ws := filepath.Join(home, ".config", "Cursor", "User", "workspaceStorage")
	for _, d := range []string{"w1", "w2", "w3"} {
		if err := os.MkdirAll(filepath.Join(ws, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	m := discoverWith(home, nil)
	got := findTool(m, "cursor")
	if got.Status != StatusDetected {
		t.Fatalf("cursor status = %q, want detected", got.Status)
	}
	if got.Sessions != 3 {
		t.Errorf("cursor sessions = %d, want 3 workspaces", got.Sessions)
	}
}

func TestCountSQLiteSessionsDevinSchema(t *testing.T) {
	home := t.TempDir()
	dbPath := filepath.Join(home, ".local", "share", "devin", "cli", "sessions.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE sessions (id TEXT PRIMARY KEY, hidden INTEGER DEFAULT 0);
		INSERT INTO sessions (id, hidden) VALUES ('a', 0), ('b', 0), ('c', 1);`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	m := discoverWith(home, nil)
	got := findTool(m, "devin")
	if got.Sessions != 2 {
		t.Errorf("devin sessions = %d, want 2 (hidden excluded)", got.Sessions)
	}
	if got.Status != StatusReady {
		t.Errorf("devin status = %q, want ready", got.Status)
	}
}

func TestToolMessageCountsAttribution(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, _, err := st.WriteRun("", "devin", "9", []store.LoadMessage{
		{ProjectID: "p", ContentHash: "h1", TS: "2026-08-01T00:00:00Z", Role: "user", Content: "x", Tool: "devin"},
	}); err != nil {
		t.Fatal(err)
	}
	// Merge a second tool into the same message, then add a solo message.
	if _, _, err := st.WriteRun("", "mimocode", "9", []store.LoadMessage{
		{ProjectID: "p", ContentHash: "h1", TS: "2026-08-01T00:00:00Z", Role: "user", Content: "x", Tool: "mimocode"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.WriteRun("", "codex", "9", []store.LoadMessage{
		{ProjectID: "p", ContentHash: "h2", TS: "2026-08-01T00:00:00Z", Role: "assistant", Content: "y", Tool: "codex"},
	}); err != nil {
		t.Fatal(err)
	}

	counts, err := st.ToolMessageCounts()
	if err != nil {
		t.Fatal(err)
	}
	if counts["devin"] != 1 || counts["mimocode"] != 1 {
		t.Errorf("co-authored message should count once per tool: %v", counts)
	}
	if counts["codex"] != 1 {
		t.Errorf("codex = %d, want 1: %v", counts["codex"], counts)
	}
	if n := counts["nonexistent"]; n != 0 {
		t.Errorf("unknown tool should be absent, got %d", n)
	}
}

func TestSortedToolsStableOrder(t *testing.T) {
	m := discoverWith(t.TempDir(), nil)
	tools := m.SortedTools()
	if len(tools) != len(m.Tools) {
		t.Fatalf("SortedTools length = %d, want %d", len(tools), len(m.Tools))
	}
	for i := 1; i < len(tools); i++ {
		if tools[i-1].Display > tools[i].Display {
			t.Fatalf("SortedTools not ordered at %d: %q > %q", i, tools[i-1].Display, tools[i].Display)
		}
	}
}

func TestCountHelpersTolerateMissingPaths(t *testing.T) {
	if got := countFilesWithSuffix(filepath.Join(t.TempDir(), "nope"), ".json"); got != 0 {
		t.Errorf("countFilesWithSuffix on missing dir = %d, want 0", got)
	}
	if got := countNamedFilesRecursive("", "x.jsonl"); got != 0 {
		t.Errorf("countNamedFilesRecursive on empty root = %d, want 0", got)
	}
	if got := countDistinctJSONLField(filepath.Join(t.TempDir(), "nope.jsonl"), "session_id"); got != 0 {
		t.Errorf("countDistinctJSONLField on missing file = %d, want 0", got)
	}
	if got := countSQLiteSessions("", "SELECT 1"); got != 0 {
		t.Errorf("countSQLiteSessions on empty path = %d, want 0", got)
	}
}

func TestDiscoverZedSessions(t *testing.T) {
	home := t.TempDir()
	threadsDir := filepath.Join(home, ".local", "share", "zed", "threads")
	if err := os.MkdirAll(threadsDir, 0755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(threadsDir, "threads.db")
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE threads (id TEXT PRIMARY KEY); INSERT INTO threads VALUES ('t1'), ('t2'), ('t3');`); err != nil {
		t.Fatal(err)
	}

	m := discoverWith(home, nil)
	got := findTool(m, "zed")
	if got.Status != StatusReady {
		t.Fatalf("zed status = %q, want ready", got.Status)
	}
	if got.Sessions != 3 {
		t.Errorf("zed sessions = %d, want 3", got.Sessions)
	}
}
