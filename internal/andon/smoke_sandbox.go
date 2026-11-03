package andon

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// DisposableSmokeSandbox manages an isolated, temporary SQLite database to run
// dry-run smoke validation without acquiring locks or mutating the shared store.
type DisposableSmokeSandbox struct {
	DB         *sql.DB
	dir        string
	sandbox    *SmokeSandbox
	invariants []InvariantFunc
}

// NewDisposableSmokeSandbox creates a temporary SQLite database and wraps it in a SmokeSandbox.
func NewDisposableSmokeSandbox(operation string) (*DisposableSmokeSandbox, error) {
	tmpDir, err := os.MkdirTemp("", "sila-smoke-sandbox-*")
	if err != nil {
		return nil, fmt.Errorf("create temporary sandbox directory: %w", err)
	}
	dbPath := filepath.Join(tmpDir, "smoke.db")
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("open disposable sandbox db: %w", err)
	}
	if _, err := db.Exec("PRAGMA journal_mode=WAL; PRAGMA synchronous=NORMAL;"); err != nil {
		_ = db.Close()
		_ = os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("init disposable sandbox db: %w", err)
	}

	sb := NewSmokeSandbox(db, operation)
	return &DisposableSmokeSandbox{
		DB:         db,
		dir:        tmpDir,
		sandbox:    sb,
		invariants: []InvariantFunc{InvariantQuickCheck},
	}, nil
}

// WithInvariants appends additional invariant validators to the disposable sandbox.
func (d *DisposableSmokeSandbox) WithInvariants(invs ...InvariantFunc) *DisposableSmokeSandbox {
	d.sandbox.WithInvariants(invs...)
	d.invariants = append(d.invariants, invs...)
	return d
}

// ExecuteDryRunOnly runs the mutation in dry-run mode against the isolated disposable database.
func (d *DisposableSmokeSandbox) ExecuteDryRunOnly(candidate func(tx *sql.Tx) error) error {
	return d.sandbox.ExecuteDryRunOnly(candidate)
}

// Close closes the disposable database handle and removes all temporary files.
func (d *DisposableSmokeSandbox) Close() error {
	var errClose error
	if d.DB != nil {
		errClose = d.DB.Close()
	}
	if d.dir != "" {
		_ = os.RemoveAll(d.dir)
	}
	return errClose
}
