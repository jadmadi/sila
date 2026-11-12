package store

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

// TestUpsertFactParallelWritersNoBusy simulates 20 independent processes
// writing a memo at the same time. Each goroutine opens its own handle on the
// same database, which is the shape that produced SQLITE_BUSY before writer
// transactions began with BEGIN IMMEDIATE.
func TestUpsertFactParallelWritersNoBusy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "parallel_memo.db")
	setup, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := setup.EnsureProject(path); err != nil {
		setup.Close()
		t.Fatal(err)
	}
	setup.Close()

	const writers = 20
	start := make(chan struct{})
	errs := make(chan error, writers)
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
			if _, err := st.InsertMemo("parallel", fmt.Sprintf("parallel memo %d", i), "test", "", nil); err != nil {
				errs <- fmt.Errorf("writer %d: %w", i, err)
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
}
