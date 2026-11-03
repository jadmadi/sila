package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestCaseCLIAddListShow(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "case_cli.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	proj, err := st.EnsureProject(tmp)
	if err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()

	ui := render.New(os.Stdout, true)

	addOut := captureStdout(t, func() {
		runCase(ui, dbPath, proj.ID, "agent", "", "single-writer WAL mode with busy timeout", "only one write transaction at a time", "sqlite,wal", "", false, true, false, []string{"SQLite connection pooling"})
	})
	var fact store.Fact
	if err := json.Unmarshal([]byte(addOut), &fact); err != nil {
		t.Fatalf("add output is not JSON: %q: %v", addOut, err)
	}
	if fact.Kind != "case" || !fact.IsCase() {
		t.Fatalf("fact kind = %q, want case", fact.Kind)
	}
	if intent, approach, invariant, tags := store.ParseCase(fact.Claim); intent != "SQLite connection pooling" || approach != "single-writer WAL mode with busy timeout" || invariant != "only one write transaction at a time" || tags != "sqlite,wal" {
		t.Fatalf("recorded content = %q", fact.Claim)
	}

	listOut := captureStdout(t, func() {
		runCase(ui, dbPath, proj.ID, "agent", "", "", "", "", "", false, true, false, []string{"list"})
	})
	var list struct {
		Count int          `json:"count"`
		Cases []store.Fact `json:"cases"`
	}
	if err := json.Unmarshal([]byte(listOut), &list); err != nil {
		t.Fatalf("list output is not JSON: %q: %v", listOut, err)
	}
	if list.Count != 1 || len(list.Cases) != 1 {
		t.Fatalf("list = %d cases, want 1", list.Count)
	}

	showOut := captureStdout(t, func() {
		runCase(ui, dbPath, proj.ID, "agent", "", "", "", "", "", false, true, false, []string{"show", fact.ID})
	})
	var shown store.Fact
	if err := json.Unmarshal([]byte(showOut), &shown); err != nil {
		t.Fatalf("show output is not JSON: %q: %v", showOut, err)
	}
	if shown.ID != fact.ID {
		t.Fatalf("show id = %q, want %q", shown.ID, fact.ID)
	}
}

func TestCaseDuplicates(t *testing.T) {
	existing := []store.Fact{{ID: "case-1", Claim: "Case: SQLite connection pooling | approach: single-writer WAL | invariant: one write at a time"}}

	exact, similar := caseDuplicates(existing, "Case: SQLite connection pooling | approach: single-writer WAL | invariant: one write at a time")
	if exact == nil || exact.ID != "case-1" {
		t.Fatalf("exact = %+v, want case-1", exact)
	}
	if len(similar) != 0 {
		t.Fatalf("similar = %d, want 0 for an exact duplicate", len(similar))
	}

	exact, similar = caseDuplicates(existing, "Case: SQLite connection pooling | approach: single-writer WAL mode with busy timeout | invariant: one write at a time")
	if exact != nil {
		t.Fatalf("exact = %+v, want nil for a near match", exact)
	}
	if len(similar) == 0 {
		t.Fatal("similar = 0, want at least one near match")
	}
}
