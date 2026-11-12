package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

func TestSaveHandoffCheckedTwentyWriters(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	seed := Handoff{ID: "h-race", ProjectID: "p", Agent: "a", Summary: "seed", Status: "open"}
	if _, _, err := st.SaveHandoffChecked(ctx, seed, ""); err != nil {
		t.Fatal(err)
	}
	row, err := st.GetHandoff(ctx, seed.ID)
	if err != nil || row == nil {
		t.Fatalf("seed read: %v", err)
	}
	rev := HandoffRevision(*row)
	const writers = 20
	var wg sync.WaitGroup
	results := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			update := seed
			update.Summary = fmt.Sprintf("writer-%02d", n)
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
	if success != 1 || conflicts != writers-1 {
		t.Fatalf("success=%d conflicts=%d", success, conflicts)
	}
}
