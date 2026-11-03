package main

import (
	"errors"
	"os"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

// isStoreInitContention reports whether a failed first-run initialization lost
// a race with another process or goroutine. The andon smoke sandbox wraps the
// underlying SQLite busy error, so the sentinel alone is not enough.
func isStoreInitContention(err error) bool {
	if err == nil {
		return false
	}
	if store.IsBusy(err) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "failed smoke a/b validation") &&
		(strings.Contains(msg, "database is locked") || strings.Contains(msg, "sqlite_busy"))
}

// openReadStore opens the knowledge store for a state-reporting command.
//
// A missing store is initialized empty so first contact never dead-ends on an
// undiscoverable `sila sync`. The store is created through the writable open
// path (migrations, permissions), closed, then reopened read-only so callers
// keep the no-write contract of a read command.
//
// Concurrent first runs retry: two processes can race the same migration, and
// SQLite reports the loser as busy. The retry is bounded and only applies to
// lock contention.
//
// The second return value reports whether this call created the store, which
// lets callers distinguish a pre-existing store from a first-contact
// initialization.
func openReadStore(dbPath string) (*store.Store, bool, error) {
	deadline := time.Now().Add(30 * time.Second)
	delay := 50 * time.Millisecond
	for {
		st, created, err := openReadStoreOnce(dbPath)
		if err == nil {
			return st, created, nil
		}
		if !isStoreInitContention(err) || time.Now().After(deadline) {
			return nil, false, err
		}
		time.Sleep(delay)
		if delay < time.Second {
			delay *= 2
		}
	}
}

func openReadStoreOnce(dbPath string) (*store.Store, bool, error) {
	if _, err := os.Stat(dbPath); err == nil {
		st, openErr := store.OpenRO(dbPath)
		return st, false, openErr
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}

	created, err := store.Open(dbPath)
	if err != nil {
		return nil, false, err
	}
	actualPath := created.Path
	if err := created.Close(); err != nil {
		return nil, false, err
	}
	st, err := store.OpenRO(actualPath)
	return st, true, err
}
