package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

func memoValidityFixture(t *testing.T) (string, string) {
	t.Helper()
	return memoConflictFixture(t)
}

func TestMemoValidityHidesExpired(t *testing.T) {
	dbPath, dir := memoValidityFixture(t)
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	runMemoConflict(t, dbPath, dir, "a.go", "expired directive", "--valid-until="+past)
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	facts, err := st.ListFactsByKind("memo", "", time.Time{}, 10)
	if err != nil || len(facts) != 1 {
		t.Fatalf("facts = %d err=%v", len(facts), err)
	}
	project := facts[0].ProjectID
	now := time.Now().UTC()
	read, err := st.QueryFactsFiltered(project, store.FactFilter{}, now, store.DefaultFactTTL, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Facts) != 0 || read.Hidden.Expired != 1 {
		t.Fatalf("default read = %d hidden=%+v", len(read.Facts), read.Hidden)
	}
	read, err = st.QueryFactsFiltered(project, store.FactFilter{IncludeExpired: true}, now, store.DefaultFactTTL, 0)
	if err != nil || len(read.Facts) != 1 || read.Hidden.Expired != 0 {
		t.Fatalf("include expired = %d hidden=%+v err=%v", len(read.Facts), read.Hidden, err)
	}
	if filepath.Base(dbPath) == "" {
		t.Fatal("unreachable")
	}
}

func TestExpiredMemoDoesNotDispute(t *testing.T) {
	dbPath, dir := memoValidityFixture(t)
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	runMemoConflict(t, dbPath, dir, "a.go", "expired directive", "--valid-until="+past)
	runMemoConflict(t, dbPath, dir, "a.go", "fresh directive")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	facts, err := st.ListFactsByKind("memo", "", time.Time{}, 10)
	if err != nil || len(facts) != 2 {
		t.Fatalf("facts = %d err=%v", len(facts), err)
	}
	for _, f := range facts {
		if f.DisputedBy != "" {
			t.Fatalf("%s disputed: %q", f.ID, f.DisputedBy)
		}
	}
}
