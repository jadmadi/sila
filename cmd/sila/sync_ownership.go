package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/jadmadi/sila/internal/store"
)

// syncBusyResult is emitted on stdout when sync cannot acquire the per-store lease.
type syncBusyResult struct {
	Code      string `json:"code"`
	Status    string `json:"status"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

var defaultSyncBusyResult = syncBusyResult{
	Code:      "SYNC_BUSY",
	Status:    "busy",
	Message:   "another sync is running",
	Retryable: true,
}

// watchSyncBusyEvent is emitted on stdout when a live watch iteration skips a pass due to lease contention.
type watchSyncBusyEvent struct {
	Event     string `json:"event"`
	Code      string `json:"code"`
	Retryable bool   `json:"retryable"`
}

var defaultWatchSyncBusyEvent = watchSyncBusyEvent{
	Event:     "sync_busy",
	Code:      "SYNC_BUSY",
	Retryable: true,
}

// withSyncLease wraps run in an advisory per-store sync lease. If the lease is
// already held by another process or pass, run is never invoked and ErrSyncBusy is returned.
func withSyncLease(dbPath string, run func() error) error {
	release, err := store.AcquireSyncLease(dbPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = release()
	}()
	return run()
}

// handleSyncLeaseError handles errors from sync execution at the outer CLI boundary.
// Contention results in exit code 2 and formatted busy output. Other errors exit 1.
func handleSyncLeaseError(err error, jsonOut bool) {
	if errors.Is(err, store.ErrSyncBusy) {
		if jsonOut {
			data, _ := json.Marshal(defaultSyncBusyResult)
			fmt.Fprintln(os.Stdout, string(data))
		} else {
			fmt.Fprintln(os.Stderr, "another sync is running; retry after it finishes")
		}
		os.Exit(2)
	}
	fmt.Fprintf(os.Stderr, "sila: %v\n", err)
	os.Exit(1)
}
