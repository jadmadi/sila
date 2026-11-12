package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

func revisionStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestSaveHandoffChecked(t *testing.T) {
	st := revisionStore(t)
	ctx := context.Background()
	seed := Handoff{ID: "h-first", ProjectID: "p", Agent: "a", Summary: "first", OpenItems: []string{"x"}, Status: "open"}
	if _, _, err := st.SaveHandoffChecked(ctx, seed, ""); err != nil {
		t.Fatal(err)
	}
	current, err := st.GetHandoff(ctx, seed.ID)
	if err != nil || current == nil {
		t.Fatalf("get: %v", err)
	}
	currentRev := HandoffRevision(*current)

	stale := seed
	stale.Summary = "stale write"
	if _, _, err := st.SaveHandoffChecked(ctx, stale, "deadbeef"); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale err = %v", err)
	}
	row, _ := st.GetHandoff(ctx, seed.ID)
	if row.Summary != "first" {
		t.Fatal("stale write changed the row")
	}

	update := seed
	update.Summary = "second"
	newRev, replayed, err := st.SaveHandoffChecked(ctx, update, currentRev)
	if err != nil || replayed || newRev == currentRev {
		t.Fatalf("match: rev=%q replayed=%v err=%v", newRev, replayed, err)
	}
	row, _ = st.GetHandoff(ctx, seed.ID)
	if row.Summary != "second" {
		t.Fatal("matched write did not update")
	}

	replayRev, replayed, err := st.SaveHandoffChecked(ctx, update, newRev)
	if err != nil || !replayed || replayRev != newRev {
		t.Fatalf("replay: %q %v %v", replayRev, replayed, err)
	}

	if _, _, err := st.SaveHandoffChecked(ctx, Handoff{ID: "missing", ProjectID: "p", Summary: "s"}, "abc"); !errors.Is(err, ErrHandoffNotFound) {
		t.Fatalf("missing err = %v", err)
	}
}

func TestSaveHandoffCheckedConcurrent(t *testing.T) {
	st := revisionStore(t)
	ctx := context.Background()
	seed := Handoff{ID: "h-conc", ProjectID: "p", Agent: "a", Summary: "seed", Status: "open"}
	if _, _, err := st.SaveHandoffChecked(ctx, seed, ""); err != nil {
		t.Fatal(err)
	}
	row, _ := st.GetHandoff(ctx, seed.ID)
	rev := HandoffRevision(*row)

	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			update := seed
			update.Summary = []string{"one", "two"}[n]
			_, _, err := st.SaveHandoffChecked(ctx, update, rev)
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)
	success, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrRevisionConflict):
			conflicts++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatalf("success=%d conflicts=%d", success, conflicts)
	}
}
