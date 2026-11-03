package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/loaders"
	"github.com/jadmadi/sila/internal/store"
)

func TestPathSize(t *testing.T) {
	dir := t.TempDir()

	// A file: its own size plus SQLite sidecars when present.
	db := filepath.Join(dir, "k.db")
	if err := os.WriteFile(db, make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(db+"-wal", make([]byte, 40), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := pathSize(db); got != 140 {
		t.Fatalf("file size = %d, want 140 (db + wal)", got)
	}

	// A directory: recursive sum. A symlink must not be followed, so the
	// linked-to file's size stays out.
	sub := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "a.jsonl"), make([]byte, 10), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "b.jsonl"), make([]byte, 15), 0o644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(sub, "deep")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "c.jsonl"), make([]byte, 5), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := pathSize(sub); got != 30 {
		t.Fatalf("dir size = %d, want 30", got)
	}

	link := filepath.Join(dir, "link.jsonl")
	if err := os.Symlink(sub, link); err != nil {
		t.Skip("symlinks unavailable")
	}
	// The link is sized as the entry itself (the target path string), never
	// dereferenced: its 30 bytes of content must not leak into the total.
	if got := pathSize(link); got == 30 {
		t.Fatalf("symlink was dereferenced: size = %d", got)
	}

	// Missing paths contribute nothing and never fail the report.
	if got := pathSize(filepath.Join(dir, "absent.db")); got != 0 {
		t.Fatalf("missing path size = %d, want 0", got)
	}
}

func TestBuildStorageReport(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "k.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if _, _, err := st.WriteRun("", "devin", "1", []store.LoadMessage{
		{ProjectID: "p", ContentHash: "h1", TS: "2026-09-01T10:00:00Z", Role: "user", Content: "one", Tool: "devin"},
		{ProjectID: "p", ContentHash: "h2", TS: "2026-09-01T10:01:00Z", Role: "user", Content: "two", Tool: "devin"},
	}); err != nil {
		t.Fatal(err)
	}

	// A tool whose store exists (a file with sidecars)…
	toolDB := filepath.Join(dir, "tool.sqlite")
	if err := os.WriteFile(toolDB, make([]byte, 200), 0o644); err != nil {
		t.Fatal(err)
	}
	// …and a directory-scanned tool with nested files.
	toolDir := filepath.Join(dir, "tool-sessions", "s1")
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(toolDir, "chat.jsonl"), make([]byte, 60), 0o644); err != nil {
		t.Fatal(err)
	}
	// A tool that is simply not installed resolves nowhere and is skipped.
	sources := map[string]*loaders.Source{
		"devin":    {Name: "devin", DBPath: toolDB, New: loaders.NewDevinLoader},
		"codex":    {Name: "codex", File: filepath.Join(dir, "history.jsonl"), New: loaders.NewCodexLoader},
		"mimocode": {Name: "mimocode", DBPath: filepath.Join(dir, "absent.sqlite"), New: loaders.NewMiMoCodeLoader},
	}

	rep := buildStorageReport(st, sources)

	if rep.Store.DBBytes == 0 || rep.Store.TotalBytes < rep.Store.DBBytes {
		t.Fatalf("store sizes wrong: %+v", rep.Store)
	}
	if rep.Store.Messages != 2 {
		t.Fatalf("store messages = %d, want 2", rep.Store.Messages)
	}

	if len(rep.Tools) != 1 {
		t.Fatalf("expected 1 installed tool, got %d: %+v", len(rep.Tools), rep.Tools)
	}
	tool := rep.Tools[0]
	if tool.Name != "devin" || tool.Bytes != 200 {
		t.Fatalf("devin storage = %+v, want 200 bytes", tool)
	}

	// Codex exists only as a registered path that is absent: not installed.
	if rep.ToolsInstalled != 1 || rep.ToolsKnown != 3 {
		t.Fatalf("installed=%d known=%d, want 1/3", rep.ToolsInstalled, rep.ToolsKnown)
	}
	if rep.SourceTotal != 200 {
		t.Fatalf("source total = %d, want 200", rep.SourceTotal)
	}
	if rep.CombinedTotal != rep.Store.TotalBytes+rep.SourceTotal {
		t.Fatalf("combined total inconsistent: %+v", rep)
	}
}

// TestStorageRootsCodexIncludesSessions pins the one special case: Codex's
// registered path is history.jsonl, but the loader also walks the sibling
// sessions/ tree, where most of Codex's data lives.
func TestStorageRootsCodexIncludesSessions(t *testing.T) {
	dir := t.TempDir()
	hist := filepath.Join(dir, "history.jsonl")
	if err := os.WriteFile(hist, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}

	roots := storageRoots("codex", hist)
	if len(roots) != 2 || roots[1] != filepath.Join(dir, "sessions") {
		t.Fatalf("codex roots = %v, want history + sessions", roots)
	}

	// Other tools read exactly their registered path.
	if got := storageRoots("devin", "/x/sessions.db"); len(got) != 1 || got[0] != "/x/sessions.db" {
		t.Fatalf("devin roots = %v", got)
	}

	// Zed reads threads.db plus 0-stable and 0-global db.sqlite when present.
	zedHome := t.TempDir()
	zedThreads := filepath.Join(zedHome, "threads", "threads.db")
	_ = os.MkdirAll(filepath.Join(zedHome, "threads"), 0755)
	_ = os.WriteFile(zedThreads, []byte("threads"), 0644)
	_ = os.MkdirAll(filepath.Join(zedHome, "db", "0-stable"), 0755)
	_ = os.WriteFile(filepath.Join(zedHome, "db", "0-stable", "db.sqlite"), []byte("stable"), 0644)
	_ = os.MkdirAll(filepath.Join(zedHome, "db", "0-global"), 0755)
	_ = os.WriteFile(filepath.Join(zedHome, "db", "0-global", "db.sqlite"), []byte("global"), 0644)

	zRoots := storageRoots("zed", zedThreads)
	if len(zRoots) != 3 {
		t.Fatalf("zed roots count = %d, want 3 (%v)", len(zRoots), zRoots)
	}
}

// TestStorageDisplayNamesCoverAllSources keeps the display map from drifting
// when a tool is added to the registry: every non-git source needs a label.
func TestStorageDisplayNamesCoverAllSources(t *testing.T) {
	for name := range loaders.AllSources() {
		if name == "git" {
			continue
		}
		if _, ok := storageDisplayNames[name]; !ok {
			t.Errorf("storageDisplayNames is missing %q; add it so `sila storage` labels the tool", name)
		}
	}
}
