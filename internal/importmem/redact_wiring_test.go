package importmem

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func TestMirrorRedactsDSN(t *testing.T) {
	repo := t.TempDir()
	dir := filepath.Join(repo, ".claude", "memory")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "wiring.md")
	body := "## Decisions\n\n- deploy with postgres://user:wiringpass@db.local/app\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "w.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := MirrorOne(context.Background(), st, ImportOptions{ProjectID: "p", RepoRoot: repo, MirrorSession: "s"}, MirrorTarget{Harness: "claude", Path: path}); err != nil {
		t.Fatal(err)
	}
	records, err := st.ListMemoryRecords(store.MemoryListOptions{ProjectID: "p", Limit: 10})
	if err != nil || len(records) == 0 {
		t.Fatalf("records=%d err=%v", len(records), err)
	}
	for _, rec := range records {
		if strings.Contains(rec.EnvelopeJSON, "wiringpass") || strings.Contains(rec.Summary, "wiringpass") {
			t.Fatalf("secret leaked: %q", rec.Summary)
		}
	}
}
