package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestMergeSlotTwentyWriters(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "mc.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	now := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	const writers = 20
	var wg sync.WaitGroup
	results := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, err := st.AcquireMergeSlot(ctx, "p", "agent", time.Minute, now)
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)
	success, busy := 0, 0
	for err := range results {
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrMergeSlotBusy):
			busy++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if success != 1 || busy != writers-1 {
		t.Fatalf("success=%d busy=%d", success, busy)
	}
}
