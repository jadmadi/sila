package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func TestGateMemoryWriteSentry(t *testing.T) {
	// The pre-commit hook passes the candidate subject through SILA_COMMIT_MSG;
	// this fixture must not inherit it from the enclosing gate run.
	t.Setenv("SILA_COMMIT_MSG", "")
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	goalDir := filepath.Join(repo, "goals", "demo")
	if err := os.MkdirAll(goalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	goal := "# Goal: Demo\n\n- **Memory Protocol**: required\n\n## Files to Touch\n- cmd/sila/gate.go\n"
	if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goal), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "COMMIT_EDITMSG"), []byte("feat: demo (goals/demo/goal.md)\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(t.TempDir(), "gate-memory.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.EnsureProject(repo)
	if err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()
	t.Setenv("SILA_DB", dbPath)

	if got := gateMemoryWriteSentry(repo); got.Status != "fail" {
		t.Fatalf("missing write should fail: %+v", got)
	}

	st, err = store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertMemo(p.ID, "gate memory sentry decision", "tester", repo, []string{"cmd/sila/gate.go"}); err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()

	if got := gateMemoryWriteSentry(repo); got.Status != "pass" {
		t.Fatalf("grounded write should pass: %+v", got)
	}
}
