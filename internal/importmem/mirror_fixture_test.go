package importmem

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func TestMirrorFixturesByHarness(t *testing.T) {
	cases := []struct {
		harness string
		fixture string
		root    string
	}{
		{"claude", "claude-decisions.md", ".claude/memory"},
		{"codex", "codex-conventions.md", ".codex/memories"},
		{"grok", "grok-failed.md", ".grok/topics"},
	}
	for _, tc := range cases {
		t.Run(tc.harness, func(t *testing.T) {
			repo := t.TempDir()
			dir := filepath.Join(repo, filepath.FromSlash(tc.root))
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join("testdata", "mirror", tc.fixture))
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, tc.fixture)
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatal(err)
			}
			home := t.TempDir()
			target, ok := ClassifyMirrorPath(home, repo, path)
			if !ok || target.Harness != tc.harness {
				t.Fatalf("classify = %+v ok=%v", target, ok)
			}
			st, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			res, err := MirrorOne(context.Background(), st, ImportOptions{ProjectID: "p", RepoRoot: repo, MirrorSession: "s1"}, target)
			if err != nil || res.Mirrored < 1 {
				t.Fatalf("mirror = %+v err=%v", res, err)
			}
		})
	}
}

func TestMirrorRedactsSecrets(t *testing.T) {
	repo := t.TempDir()
	dir := filepath.Join(repo, ".claude", "memory")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "secret.md")
	body := "## Decisions\n\n- deploy with password=hunter2 and dsn postgres://user:pass@db.local/app\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := MirrorOne(context.Background(), st, ImportOptions{ProjectID: "p", RepoRoot: repo, MirrorSession: "s1"}, MirrorTarget{Harness: "claude", Path: path}); err != nil {
		t.Fatal(err)
	}
	records, err := st.ListMemoryRecords(store.MemoryListOptions{ProjectID: "p", Limit: 10})
	if err != nil || len(records) == 0 {
		t.Fatalf("records=%d err=%v", len(records), err)
	}
	for _, rec := range records {
		text := strings.ToLower(rec.Summary + rec.EnvelopeJSON)
		if strings.Contains(text, "hunter2") || strings.Contains(text, "user:pass") {
			t.Fatalf("secret leaked: %s", rec.Summary)
		}
	}
}
