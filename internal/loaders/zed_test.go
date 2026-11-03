package loaders

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
	_ "modernc.org/sqlite"
)

func zedFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "threads.db")

	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	schema := `CREATE TABLE threads (
		id TEXT PRIMARY KEY,
		summary TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		data_type TEXT NOT NULL,
		data BLOB NOT NULL,
		parent_id TEXT,
		worktree_branch TEXT,
		folder_paths TEXT,
		folder_paths_order TEXT,
		created_at TEXT
	);`
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}

	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}

	// Thread 1: project-a
	t1 := zedThreadPayload{
		Title:     "Thread 1 Title",
		UpdatedAt: "2026-01-01T10:00:00Z",
		Messages: []zedRawMessage{
			{
				User: &zedTurn{
					ID:      "u1",
					Content: []zedContentPart{{Text: "How do I use Go contexts?"}},
				},
			},
			{
				Agent: &zedTurn{
					ID:      "a1",
					Content: []zedContentPart{{Text: "Pass context.Context as the first argument."}},
				},
			},
		},
	}
	b1, _ := json.Marshal(t1)
	compressed1 := encoder.EncodeAll(b1, nil)

	_, err = db.Exec(`INSERT INTO threads (id, summary, updated_at, data_type, data, folder_paths)
		VALUES (?, ?, ?, ?, ?, ?)`,
		"thread-1", "Thread 1 Title", "2026-01-01T10:00:00Z", "zstd", compressed1, "/workspace/project-a")
	if err != nil {
		t.Fatal(err)
	}

	// Thread 2: project-b
	t2 := zedThreadPayload{
		Title:     "Thread 2 Title",
		UpdatedAt: "2026-01-02T12:00:00Z",
		Messages: []zedRawMessage{
			{
				User: &zedTurn{
					ID:      "u2",
					Content: []zedContentPart{{Text: "Write a unit test."}},
				},
			},
			{
				Assistant: &zedTurn{
					ID:      "a2",
					Content: []zedContentPart{{Text: "Here is a test using testing.T."}},
				},
			},
		},
	}
	b2, _ := json.Marshal(t2)
	compressed2 := encoder.EncodeAll(b2, nil)

	_, err = db.Exec(`INSERT INTO threads (id, summary, updated_at, data_type, data, folder_paths)
		VALUES (?, ?, ?, ?, ?, ?)`,
		"thread-2", "Thread 2 Title", "2026-01-02T12:00:00Z", "zstd", compressed2, "/workspace/project-b")
	if err != nil {
		t.Fatal(err)
	}

	return dbPath
}

func TestLoadZed(t *testing.T) {
	dbPath := zedFixture(t)
	loader := NewZedLoader(dbPath)

	if loader.Name() != "zed" {
		t.Fatalf("expected loader name 'zed', got %q", loader.Name())
	}

	msgs, wm, err := loader.Load("", nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages across 2 threads, got %d", len(msgs))
	}

	if wm != "2026-01-02T12:00:00Z" {
		t.Fatalf("expected watermark 2026-01-02T12:00:00Z, got %q", wm)
	}

	// Check message content and attributes
	if msgs[0].Role != "user" || msgs[0].Content != "How do I use Go contexts?" {
		t.Errorf("unexpected first message: %+v", msgs[0])
	}
	if msgs[0].ProjectDir != "/workspace/project-a" {
		t.Errorf("expected project /workspace/project-a, got %q", msgs[0].ProjectDir)
	}
	if msgs[1].Role != "assistant" || msgs[1].Content != "Pass context.Context as the first argument." {
		t.Errorf("unexpected second message: %+v", msgs[1])
	}

	// Test Project Filter
	projA := "project-a"
	msgsA, _, err := loader.Load("", &projA)
	if err != nil {
		t.Fatalf("Load with project: %v", err)
	}
	if len(msgsA) != 2 {
		t.Fatalf("expected 2 messages for project-a, got %d", len(msgsA))
	}

	// Test Watermark progression
	msgsAfter, wmAfter, err := loader.Load("2026-01-02T00:00:00Z", nil)
	if err != nil {
		t.Fatalf("Load with watermark: %v", err)
	}
	if len(msgsAfter) != 2 {
		t.Fatalf("expected 2 messages after watermark, got %d", len(msgsAfter))
	}
	if wmAfter != "2026-01-02T12:00:00Z" {
		t.Fatalf("expected watermark 2026-01-02T12:00:00Z, got %q", wmAfter)
	}

	// Test Count
	totalCount, err := loader.Count(nil)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if totalCount != 4 {
		t.Fatalf("expected count 4, got %d", totalCount)
	}

	projCount, err := loader.Count(&projA)
	if err != nil {
		t.Fatalf("Count with project: %v", err)
	}
	if projCount != 2 {
		t.Fatalf("expected count 2 for project-a, got %d", projCount)
	}
}

func TestLoadZed_MissingDB(t *testing.T) {
	loader := NewZedLoader(filepath.Join(t.TempDir(), "nonexistent.db"))
	_, _, err := loader.Load("", nil)
	if err == nil {
		t.Fatal("expected error for nonexistent database")
	}
}

func TestLoadZed_LiveHost(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dbPath := filepath.Join(home, ".local", "share", "zed", "threads", "threads.db")
	if _, err := os.Stat(dbPath); err != nil {
		t.Skip("local Zed threads.db not present")
	}

	loader := NewZedLoader(dbPath)
	msgs, wm, err := loader.Load("", nil)
	if err != nil {
		t.Fatalf("failed to load real Zed database: %v", err)
	}
	t.Logf("loaded %d messages from live Zed database, watermark: %s", len(msgs), wm)
	if len(msgs) == 0 {
		t.Fatal("expected non-zero messages from live Zed database")
	}
}
