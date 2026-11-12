package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func memoConflictFixture(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"a.go", "b.go", "c.go", "d.go"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("package x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(t.TempDir(), "m.db"), dir
}

func runMemoConflict(t *testing.T, dbPath, dir, files string, args ...string) {
	t.Helper()
	ui := render.New(os.Stdout, true)
	captureStdout(t, func() {
		runMemo(ui, dbPath, dir, "test-agent", files, "", memoOptions{}, args)
	})
}

func memoRows(t *testing.T, dbPath string) []store.Fact {
	t.Helper()
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	facts, err := st.ListFactsByKind("memo", "", time.Time{}, 50)
	if err != nil {
		t.Fatal(err)
	}
	return facts
}

func TestMemoConflictMarksBoth(t *testing.T) {
	dbPath, dir := memoConflictFixture(t)
	runMemoConflict(t, dbPath, dir, "a.go,b.go", "first directive")
	runMemoConflict(t, dbPath, dir, "b.go,a.go", "second directive")
	facts := memoRows(t, dbPath)
	if len(facts) != 2 {
		t.Fatalf("memos = %d", len(facts))
	}
	ids := map[string]string{}
	for _, f := range facts {
		ids[f.ID] = f.DisputedBy
	}
	for id, disputed := range ids {
		if disputed == "" || disputed == id {
			t.Fatalf("%s disputed_by = %q", id, disputed)
		}
		if _, ok := ids[disputed]; !ok {
			t.Fatalf("%s points to unknown %q", id, disputed)
		}
	}
}

func TestMemoDifferentFilesStayClean(t *testing.T) {
	dbPath, dir := memoConflictFixture(t)
	runMemoConflict(t, dbPath, dir, "a.go", "first directive")
	runMemoConflict(t, dbPath, dir, "c.go", "second directive")
	for _, f := range memoRows(t, dbPath) {
		if f.DisputedBy != "" {
			t.Fatalf("%s disputed unexpectedly: %q", f.ID, f.DisputedBy)
		}
	}
}

func TestMemoNoFilesNeverDisputes(t *testing.T) {
	dbPath, dir := memoConflictFixture(t)
	runMemoConflict(t, dbPath, dir, "", "first directive")
	runMemoConflict(t, dbPath, dir, "", "second directive")
	for _, f := range memoRows(t, dbPath) {
		if f.DisputedBy != "" {
			t.Fatalf("%s disputed without files", f.ID)
		}
	}
}

func TestMemoResolveArchivesLoser(t *testing.T) {
	dbPath, dir := memoConflictFixture(t)
	runMemoConflict(t, dbPath, dir, "a.go", "first directive")
	runMemoConflict(t, dbPath, dir, "a.go", "second directive")
	facts := memoRows(t, dbPath)
	if len(facts) != 2 {
		t.Fatalf("memos = %d", len(facts))
	}
	winner, loser := facts[0].ID, facts[1].ID
	runMemoConflict(t, dbPath, dir, "", "resolve", winner, "--supersede="+loser)
	after := memoRows(t, dbPath)
	if len(after) != 1 || after[0].ID != winner || after[0].DisputedBy != "" {
		t.Fatalf("after resolve = %+v", after)
	}
}

func TestMemoSupersedesSkipsDispute(t *testing.T) {
	dbPath, dir := memoConflictFixture(t)
	runMemoConflict(t, dbPath, dir, "a.go", "first directive")
	first := memoRows(t, dbPath)[0]
	runMemoConflict(t, dbPath, dir, "a.go", "replacement directive", "--supersedes="+first.ID)
	facts := memoRows(t, dbPath)
	for _, f := range facts {
		if f.DisputedBy != "" {
			t.Fatalf("%s disputed despite supersedes", f.ID)
		}
	}
	if len(facts) != 1 {
		t.Fatalf("active memos = %d, want the replacement only", len(facts))
	}
}

func TestPrimeShowsDisputedLine(t *testing.T) {
	dbPath, dir := memoConflictFixture(t)
	runMemoConflict(t, dbPath, dir, "a.go", "first directive")
	runMemoConflict(t, dbPath, dir, "a.go", "second directive")
	out := captureStdout(t, func() {
		runPrime(render.New(os.Stdout, true), dbPath, dir, false, false, 5, "", "--no-map")
	})
	if !strings.Contains(out, "Disputed: 2") {
		t.Fatalf("prime output lacks the disputed line: %s", out)
	}
}
