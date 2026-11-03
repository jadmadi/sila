package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestConcurrentWritersAndReadersNoBusy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent.db")
	setup, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	setup.Close()

	const writers = 10
	const readers = 10
	start := make(chan struct{})
	errs := make(chan error, writers+readers)
	var wg sync.WaitGroup

	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			st, err := Open(path)
			if err != nil {
				errs <- fmt.Errorf("writer %d open: %w", i, err)
				return
			}
			defer st.Close()
			<-start
			if _, err := st.InsertMemo("concurrency", fmt.Sprintf("writer-%d unique directive", i), "test", "", nil); err != nil {
				errs <- fmt.Errorf("writer %d: %w", i, err)
			}
		}(i)
	}
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			st, err := OpenRO(path)
			if err != nil {
				errs <- fmt.Errorf("reader %d open: %w", i, err)
				return
			}
			defer st.Close()
			<-start
			for j := 0; j < 10; j++ {
				if _, _, _, _, err := st.Counts(); err != nil {
					errs <- fmt.Errorf("reader %d: %w", i, err)
					return
				}
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	st, err := OpenRO(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, facts, _, _, err := st.Counts()
	if err != nil {
		t.Fatal(err)
	}
	if facts != writers {
		t.Fatalf("facts = %d, want %d", facts, writers)
	}
	health, err := st.Health()
	if err != nil {
		t.Fatal(err)
	}
	if health.ActiveWriters != 0 {
		t.Fatalf("active writers after completion = %d", health.ActiveWriters)
	}
}

func TestBusyErrorIsStructuredRetryable(t *testing.T) {
	cfg := RetryConfig{MaxRetries: 1, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond, TotalTimeout: time.Second}
	err := WithCustomRetry(context.Background(), cfg, func() error {
		return errors.New("database is locked (5) (SQLITE_BUSY)")
	})
	var busy *BusyError
	if !errors.As(err, &busy) {
		t.Fatalf("expected BusyError, got %v", err)
	}
	if !busy.Retryable() || busy.Attempts != 2 {
		t.Fatalf("unexpected BusyError: %+v", busy)
	}
}
