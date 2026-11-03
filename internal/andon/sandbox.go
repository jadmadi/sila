package andon

import (
	"database/sql"
	"fmt"
	"strings"
)

// InvariantFunc asserts that a database state satisfies a required invariant.
type InvariantFunc func(tx *sql.Tx) error

// InvariantQuickCheck verifies SQLite database structure using PRAGMA quick_check.
// If an FTS5 virtual table corruption is detected, it automatically attempts an
// online rebuild before failing.
func InvariantQuickCheck(tx *sql.Tx) error {
	var result string
	row := tx.QueryRow("PRAGMA quick_check(1)")
	if err := row.Scan(&result); err != nil {
		return fmt.Errorf("quick_check scan failed: %w", err)
	}
	if strings.EqualFold(result, "ok") {
		return nil
	}

	// Auto-heal FTS5 virtual table corruption if detected
	lowerRes := strings.ToLower(result)
	if strings.Contains(lowerRes, "fts5") || strings.Contains(lowerRes, "corruption") || strings.Contains(lowerRes, "malformed") {
		tables := []string{"facts_fts", "messages_fts", "docs_fts"}
		healed := false
		for _, tbl := range tables {
			if strings.Contains(lowerRes, tbl) || strings.Contains(lowerRes, "fts5") {
				if _, err := tx.Exec(fmt.Sprintf("INSERT INTO %s(%s) VALUES('rebuild')", tbl, tbl)); err == nil {
					healed = true
				}
			}
		}
		if healed {
			var checkAfter string
			if err := tx.QueryRow("PRAGMA quick_check(1)").Scan(&checkAfter); err == nil && strings.EqualFold(checkAfter, "ok") {
				return nil
			}
		}
	}

	return fmt.Errorf("quick_check reported corruption: %s", result)
}

// InvariantTablesExist asserts that all specified tables exist in sqlite_master.
func InvariantTablesExist(tables ...string) InvariantFunc {
	return func(tx *sql.Tx) error {
		for _, tbl := range tables {
			var count int
			err := tx.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", tbl).Scan(&count)
			if err != nil {
				return fmt.Errorf("check table %q existence: %w", tbl, err)
			}
			if count == 0 {
				return fmt.Errorf("required table %q missing from schema", tbl)
			}
		}
		return nil
	}
}

// InvariantMinRows asserts that the specified table contains at least min rows.
func InvariantMinRows(table string, min int64) InvariantFunc {
	return func(tx *sql.Tx) error {
		var count int64
		query := fmt.Sprintf("SELECT count(*) FROM %s", table)
		if err := tx.QueryRow(query).Scan(&count); err != nil {
			return fmt.Errorf("count rows in %q: %w", table, err)
		}
		if count < min {
			return fmt.Errorf("table %q row count %d is below required minimum %d", table, count, min)
		}
		return nil
	}
}

// SmokeSandbox manages dry-run smoke A/B validation against a database before live commit.
type SmokeSandbox struct {
	db         *sql.DB
	operation  string
	invariants []InvariantFunc
}

// NewSmokeSandbox creates a new SmokeSandbox for validating mutations.
func NewSmokeSandbox(db *sql.DB, operation string) *SmokeSandbox {
	return &SmokeSandbox{
		db:         db,
		operation:  operation,
		invariants: []InvariantFunc{InvariantQuickCheck},
	}
}

// WithInvariants appends additional invariant validators to the sandbox.
func (s *SmokeSandbox) WithInvariants(invs ...InvariantFunc) *SmokeSandbox {
	s.invariants = append(s.invariants, invs...)
	return s
}

// Execute runs the candidate mutation through Smoke A/B validation:
// 1. Dry-Run (Path A): Executes mutation in an isolated transaction, validates invariants, and ALWAYS rolls back.
// 2. If Path A fails: Aborts immediately, pulls the Andon cord, and leaves live state untouched.
// 3. Live Commit (Path B): Executes mutation in a live transaction, re-validates invariants, and commits.
func (s *SmokeSandbox) Execute(candidate func(tx *sql.Tx) error) (err error) {
	cord := NewCord(s.operation)

	// Step 1: Pre-flight smoke validation in dry-run transaction
	dryRunErr := cord.Guard(func() error {
		tx, beginErr := s.db.Begin()
		if beginErr != nil {
			return fmt.Errorf("begin dry-run transaction: %w", beginErr)
		}
		// Ensure dry-run transaction is ALWAYS rolled back
		defer func() { _ = tx.Rollback() }()

		if err := candidate(tx); err != nil {
			return fmt.Errorf("candidate mutation failed in dry-run: %w", err)
		}

		for idx, inv := range s.invariants {
			if err := inv(tx); err != nil {
				return fmt.Errorf("dry-run invariant #%d failed: %w", idx+1, err)
			}
		}

		return nil
	})

	if dryRunErr != nil {
		return cord.Pull(
			CodeMutationSandboxFail,
			"All state mutations must pass pre-flight smoke dry-run without invariant breaches",
			fmt.Sprintf("Smoke A/B dry-run rejected mutation during %s", s.operation),
			"Inspect the failed invariant or SQL error, fix the schema/data logic, and retry.",
			SeverityFatal,
			dryRunErr,
			map[string]any{"stage": "smoke_ab_dryrun", "operation": s.operation},
		)
	}

	// Step 2: Live transaction commit
	liveErr := cord.Guard(func() error {
		tx, beginErr := s.db.Begin()
		if beginErr != nil {
			return fmt.Errorf("begin live transaction: %w", beginErr)
		}
		cord.RegisterRollback("rollback live transaction", func() error {
			return tx.Rollback()
		})

		if err := candidate(tx); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("candidate mutation failed during live execution: %w", err)
		}

		for idx, inv := range s.invariants {
			if err := inv(tx); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("live post-mutation invariant #%d failed: %w", idx+1, err)
			}
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit live transaction: %w", err)
		}
		return nil
	})

	if liveErr != nil {
		return cord.Pull(
			CodeInvariantViolation,
			"Live state commit must preserve all database invariants and integrity checks",
			fmt.Sprintf("Live execution failed during %s", s.operation),
			"Check database locks, write permissions, and disk space before retrying.",
			SeverityCritical,
			liveErr,
			map[string]any{"stage": "live_commit", "operation": s.operation},
		)
	}

	return nil
}

// ExecuteDryRunOnly performs only Path A (dry run and invariant checks) without committing anything.
func (s *SmokeSandbox) ExecuteDryRunOnly(candidate func(tx *sql.Tx) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin dry-run transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := candidate(tx); err != nil {
		return fmt.Errorf("candidate mutation failed in dry-run: %w", err)
	}

	for idx, inv := range s.invariants {
		if err := inv(tx); err != nil {
			return fmt.Errorf("dry-run invariant #%d failed: %w", idx+1, err)
		}
	}
	return nil
}
