package main

import (
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func TestHandoffRevisionDecision(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	seed := store.Handoff{ID: "h1", ProjectID: "p", Agent: "a", Branch: "main", Summary: "first", Status: "open"}
	if err := st.InsertHandoff(seed); err != nil {
		t.Fatal(err)
	}
	current := store.HandoffRevision(seed)
	update := seed
	update.Summary = "second"

	rev, replayed, _, ok := handoffRevisionDecision(st, "p", "main", current, update)
	if !ok || replayed || rev == current {
		t.Fatalf("match: rev=%q replayed=%v ok=%v", rev, replayed, ok)
	}
	if _, _, gotCurrent, ok := handoffRevisionDecision(st, "p", "main", "deadbeef", update); ok || gotCurrent != current {
		t.Fatalf("stale: ok=%v current=%q", ok, gotCurrent)
	}
	if _, _, _, ok := handoffRevisionDecision(st, "p", "main", "", update); !ok {
		t.Fatal("unchecked write rejected")
	}
}
