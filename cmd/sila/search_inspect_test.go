package main

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func inspectStore(t *testing.T) (string, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "i.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.UpsertFact(store.Fact{ID: "l1", ProjectID: "p", Kind: "lesson", Claim: "### Symptom\ndatabase timeout\n### Cause\npool\n### Fix\nraise pool", FirstSeen: "2026-11-01T10:00:00Z", ReviewStatus: "approved"}); err != nil {
		t.Fatal(err)
	}
	return dbPath, "l1"
}

func TestSearchInspectTrace(t *testing.T) {
	dbPath, id := inspectStore(t)
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	entries, err := searchInspectTrace(st, "", "database timeout", true, id)
	if err != nil || len(entries) != 1 || entries[0].ID != id || entries[0].FinalScore == 0 {
		t.Fatalf("entries=%+v err=%v", entries, err)
	}
	if _, err := searchInspectTrace(st, "", "database timeout", true, "missing"); !errors.Is(err, errInspectTargetNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestSearchInspectJSONAndNoANSI(t *testing.T) {
	dbPath, _ := inspectStore(t)
	out := captureStdout(t, func() {
		runSearchInspect(render.New(nil, true), dbPath, "", true, []string{"database", "timeout", "--show-misses"})
	})
	var entries []map[string]any
	if err := json.Unmarshal([]byte(out), &entries); err != nil || len(entries) == 0 {
		t.Fatalf("json: %v %q", err, out)
	}
	for _, key := range []string{"stage", "base", "match", "project_boost", "final_score", "decision"} {
		if _, ok := entries[0][key]; !ok {
			t.Fatalf("missing %q in %+v", key, entries[0])
		}
	}
	human := captureStdout(t, func() {
		runSearchInspect(render.New(nil, true), dbPath, "", false, []string{"database", "timeout"})
	})
	if strings.Contains(human, "\x1b") {
		t.Fatal("human output contains escape bytes")
	}
}
