package store

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync/atomic"
	"time"
)

// BusyTimeoutMS is the bounded SQLite lock wait used by writer connections.
const BusyTimeoutMS = 5000

// BusyError is returned when a write exhausts the bounded retry policy. Callers
// can detect it with errors.As and schedule their own retry; the underlying
// SQLite error remains available through Unwrap.
type BusyError struct {
	Attempts int
	Waited   time.Duration
	Err      error
}

func (e *BusyError) Error() string {
	return fmt.Sprintf("sqlite busy after %d attempts (%s); retryable: %v", e.Attempts, e.Waited.Round(time.Millisecond), e.Err)
}

func (e *BusyError) Unwrap() error   { return e.Err }
func (e *BusyError) Retryable() bool { return true }

var (
	activeWriters  atomic.Int64
	lastLockWaitMS atomic.Int64
	retryableBusy  atomic.Int64
)

// ConcurrencyMetrics is a process-local view of writer contention.
type ConcurrencyMetrics struct {
	ActiveWriters   int64 `json:"active_writers"`
	LastLockWaitMS  int64 `json:"last_lock_wait_ms"`
	RetryableErrors int64 `json:"retryable_errors"`
	BusyTimeoutMS   int   `json:"busy_timeout_ms"`
}

// CurrentConcurrencyMetrics returns the process-local writer contention view.
func CurrentConcurrencyMetrics() ConcurrencyMetrics {
	return ConcurrencyMetrics{
		ActiveWriters:   activeWriters.Load(),
		LastLockWaitMS:  lastLockWaitMS.Load(),
		RetryableErrors: retryableBusy.Load(),
		BusyTimeoutMS:   BusyTimeoutMS,
	}
}

// ErrUnknownCommit indicates that a transaction commit returned an ambiguous or
// indeterminate status that cannot be safely rolled back and replayed.
var ErrUnknownCommit = errors.New("unknown commit outcome: transaction commit status ambiguous, cannot replay")

// IsBusy returns true if the error indicates SQLite database lock contention
// where rollback and replay are safe.
func IsBusy(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrUnknownCommit) {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "unknown commit") {
		return false
	}
	return strings.Contains(msg, "sqlite_busy") ||
		strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "database table is locked") ||
		strings.Contains(msg, "resource temporarily unavailable") ||
		strings.Contains(msg, "(5)") ||
		strings.Contains(msg, "code 5") ||
		strings.Contains(msg, "code: 5")
}

// RetryConfig specifies exponential backoff retry parameters.
type RetryConfig struct {
	MaxRetries   int
	BaseDelay    time.Duration
	MaxDelay     time.Duration
	TotalTimeout time.Duration
}

// DefaultRetryConfig provides sensible defaults for concurrent SQLite operations.
var DefaultRetryConfig = RetryConfig{
	MaxRetries:   5,
	BaseDelay:    50 * time.Millisecond,
	MaxDelay:     500 * time.Millisecond,
	TotalTimeout: 5 * time.Second,
}

// WithRetry executes the provided operation with exponential backoff and jitter
// when encountering SQLite busy/lock contention errors.
func WithRetry(ctx context.Context, op func() error) error {
	return WithCustomRetry(ctx, DefaultRetryConfig, op)
}

// WithCustomRetry executes op using the provided RetryConfig.
func WithCustomRetry(ctx context.Context, cfg RetryConfig, op func() error) error {
	if ctx == nil {
		ctx = context.Background()
	}

	var cancel context.CancelFunc
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && cfg.TotalTimeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, cfg.TotalTimeout)
		defer cancel()
	}

	activeWriters.Add(1)
	defer activeWriters.Add(-1)
	var lastErr error
	var waited time.Duration
	delay := cfg.BaseDelay

	for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
		if ctx.Err() != nil {
			if lastErr != nil {
				return errors.Join(lastErr, ctx.Err())
			}
			return ctx.Err()
		}

		attemptStart := time.Now()
		err := op()
		if err == nil {
			lastLockWaitMS.Store(waited.Milliseconds())
			return nil
		}

		if !IsBusy(err) {
			return err
		}

		waited += time.Since(attemptStart)
		lastLockWaitMS.Store(waited.Milliseconds())
		retryableBusy.Add(1)
		lastErr = err
		if attempt == cfg.MaxRetries {
			break
		}

		// Calculate jitter (+/- 20%)
		jitterMax := int64(delay / 5)
		jitter := time.Duration(0)
		if jitterMax > 0 {
			if n, rErr := rand.Int(rand.Reader, big.NewInt(jitterMax*2)); rErr == nil {
				jitter = time.Duration(n.Int64() - jitterMax)
			}
		}

		sleepDuration := delay + jitter
		if sleepDuration > cfg.MaxDelay {
			sleepDuration = cfg.MaxDelay
		}

		timer := time.NewTimer(sleepDuration)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return errors.Join(lastErr, ctx.Err())
		}

		delay = delay * 3 / 2 // 1.5x multiplier
		if delay > cfg.MaxDelay {
			delay = cfg.MaxDelay
		}
	}

	if lastErr != nil {
		return &BusyError{Attempts: cfg.MaxRetries + 1, Waited: waited, Err: lastErr}
	}
	return lastErr
}
