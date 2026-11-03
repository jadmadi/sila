package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestIsBusy(t *testing.T) {
	cases := []struct {
		err  error
		busy bool
	}{
		{nil, false},
		{errors.New("table not found"), false},
		{errors.New("database is locked"), true},
		{errors.New("database is locked (5) (SQLITE_BUSY)"), true},
		{errors.New("sqlite_busy: cannot acquire lock"), true},
		{errors.New("resource temporarily unavailable"), true},
		{errors.New("sqlite error code 5"), true},
	}

	for _, tc := range cases {
		got := IsBusy(tc.err)
		if got != tc.busy {
			t.Errorf("IsBusy(%v) = %v; want %v", tc.err, got, tc.busy)
		}
	}
}

func TestWithRetrySuccessAfterBusy(t *testing.T) {
	ctx := context.Background()
	attempts := 0

	err := WithRetry(ctx, func() error {
		attempts++
		if attempts < 3 {
			return errors.New("database is locked (5) (SQLITE_BUSY)")
		}
		return nil
	})

	if err != nil {
		t.Fatalf("expected WithRetry to succeed, got %v", err)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
}

func TestWithRetryNonBusyErrorFailsImmediately(t *testing.T) {
	ctx := context.Background()
	attempts := 0
	expectedErr := errors.New("syntax error")

	err := WithRetry(ctx, func() error {
		attempts++
		return expectedErr
	})

	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected error %v, got %v", expectedErr, err)
	}
	if attempts != 1 {
		t.Fatalf("expected non-busy error to fail immediately on attempt 1, got %d", attempts)
	}
}

func TestWithRetryContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err := WithRetry(ctx, func() error {
		return errors.New("database is locked")
	})

	if err == nil {
		t.Fatal("expected canceled context error, got nil")
	}
}

func TestConcurrentWritersWithRetry(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	var wg sync.WaitGroup
	var successCount int64
	numWorkers := 10
	iterations := 20

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		workerID := i
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				claim := fmt.Sprintf("Worker %d Claim %d", workerID, j)
				fact := Fact{
					ProjectID:  "test-concurrency",
					Claim:      claim,
					Distilled:  claim,
					Confidence: "corroborated",
				}
				err := WithRetry(context.Background(), func() error {
					return st.UpsertFact(fact)
				})
				if err != nil {
					t.Errorf("worker %d failed upsert: %v", workerID, err)
					return
				}
				atomic.AddInt64(&successCount, 1)
			}
		}()
	}

	wg.Wait()
	expectedTotal := int64(numWorkers * iterations)
	if successCount != expectedTotal {
		t.Fatalf("expected %d successful upserts, got %d", expectedTotal, successCount)
	}
}

func TestWithRetry_BoundedTotalTimeout(t *testing.T) {
	start := time.Now()
	cfg := RetryConfig{
		MaxRetries:   50,
		BaseDelay:    50 * time.Millisecond,
		MaxDelay:     100 * time.Millisecond,
		TotalTimeout: 200 * time.Millisecond,
	}

	err := WithCustomRetry(context.Background(), cfg, func() error {
		return errors.New("database is locked (5) (SQLITE_BUSY)")
	})

	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error on persistent contention, got nil")
	}
	if !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("expected deadline exceeded error, got: %v", err)
	}
	// Verify total duration stayed bounded close to TotalTimeout
	if elapsed > 1*time.Second {
		t.Fatalf("retry took too long (%v), expected bounded to ~200ms", elapsed)
	}
}

func TestWithRetry_UnknownCommitFailsImmediately(t *testing.T) {
	attempts := 0
	err := WithRetry(context.Background(), func() error {
		attempts++
		return ErrUnknownCommit
	})

	if !errors.Is(err, ErrUnknownCommit) {
		t.Fatalf("expected ErrUnknownCommit, got: %v", err)
	}
	if attempts != 1 {
		t.Fatalf("expected exactly 1 attempt without replay, got %d", attempts)
	}

	attemptsStr := 0
	errStr := WithRetry(context.Background(), func() error {
		attemptsStr++
		return errors.New("unknown commit outcome occurred")
	})
	if errStr == nil || !strings.Contains(errStr.Error(), "unknown commit") {
		t.Fatalf("expected unknown commit error, got: %v", errStr)
	}
	if attemptsStr != 1 {
		t.Fatalf("expected exactly 1 attempt for unknown commit string, got %d", attemptsStr)
	}
}

func TestWithRetry_LockReleaseEventualSuccess(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "lock_release.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	lockHoldDuration := 150 * time.Millisecond
	attempts := 0

	// Simulate lock held and released after lockHoldDuration
	start := time.Now()
	err = WithRetry(context.Background(), func() error {
		attempts++
		if time.Since(start) < lockHoldDuration {
			return errors.New("database is locked (5) (SQLITE_BUSY)")
		}
		return st.SetState("test_key", "test_val")
	})

	if err != nil {
		t.Fatalf("expected eventual success after lock release, got: %v", err)
	}
	if attempts < 2 {
		t.Fatalf("expected multiple attempts during lock hold, got %d", attempts)
	}

	val, err := st.GetState("test_key")
	if err != nil || val != "test_val" {
		t.Fatalf("expected state test_val, got %s (err %v)", val, err)
	}
}

func TestWithRetry_NoDuplicateFactsOrWatermarks(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "dedup.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	numWorkers := 5
	iterations := 10
	var wg sync.WaitGroup

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				// All workers update the EXACT same fact and watermark
				fact := Fact{
					ProjectID:  "dedup-proj",
					Claim:      "deterministic-unique-claim",
					Distilled:  fmt.Sprintf("Distilled content %d", j),
					Confidence: "proven",
				}
				if err := WithRetry(context.Background(), func() error {
					return st.UpsertFact(fact)
				}); err != nil {
					t.Errorf("UpsertFact failed: %v", err)
				}

				wmVal := fmt.Sprintf("%04d", j)
				if err := WithRetry(context.Background(), func() error {
					return st.SetWatermark("run-dedup", "test-tool", wmVal)
				}); err != nil {
					t.Errorf("SetWatermark failed: %v", err)
				}
			}
		}()
	}

	wg.Wait()

	// Verify exact count of facts with claim="deterministic-unique-claim" is 1 (zero duplicates)
	var factCount int
	err = st.DB.QueryRow(`SELECT COUNT(*) FROM facts WHERE project_id = ? AND claim = ?`, "dedup-proj", "deterministic-unique-claim").Scan(&factCount)
	if err != nil {
		t.Fatal(err)
	}
	if factCount != 1 {
		t.Fatalf("expected exactly 1 fact (zero duplicates), got %d", factCount)
	}

	// Verify watermark exists and is the maximum monotonic value
	wm, err := st.GetState(watermarkKey("run-dedup", "test-tool"))
	if err != nil {
		t.Fatal(err)
	}
	expectedWM := fmt.Sprintf("%04d", iterations-1)
	if wm != expectedWM {
		t.Fatalf("expected watermark %s, got %s", expectedWM, wm)
	}
}
