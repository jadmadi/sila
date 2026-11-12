//go:build !race

package importmem

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

func TestMirror100KiBUnderBudget(t *testing.T) {
	repo := t.TempDir()
	dir := filepath.Join(repo, ".claude", "memory")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "big.md")
	body := "## Decisions\n\n- " + strings.Repeat("x", 100*1024) + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	start := time.Now()
	res, err := MirrorOne(context.Background(), st, ImportOptions{ProjectID: "p", RepoRoot: repo, MirrorSession: "s1"}, MirrorTarget{Harness: "claude", Path: path})
	elapsed := time.Since(start)
	if err != nil || res.Mirrored < 1 {
		t.Fatalf("mirror = %+v err=%v", res, err)
	}
	t.Logf("mirror of 100 KiB took %s", elapsed)
	if elapsed > MirrorBudget {
		t.Fatalf("mirror took %s, budget %s", elapsed, MirrorBudget)
	}
}
