package andon

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestSeverity(t *testing.T) {
	if SeverityAdvisory.String() != "ADVISORY" {
		t.Fatalf("expected ADVISORY, got %s", SeverityAdvisory.String())
	}
	if !SeverityFatal.IsFatal() {
		t.Fatal("expected SeverityFatal.IsFatal() to be true")
	}
	if !SeverityCritical.IsCritical() {
		t.Fatal("expected SeverityCritical.IsCritical() to be true")
	}
	if SeverityAdvisory.IsCritical() {
		t.Fatal("expected SeverityAdvisory.IsCritical() to be false")
	}

	b, err := json.Marshal(SeverityCritical)
	if err != nil {
		t.Fatalf("marshal severity: %v", err)
	}
	if string(b) != `"CRITICAL"` {
		t.Fatalf("unexpected json: %s", string(b))
	}

	var s Severity
	if err := json.Unmarshal([]byte(`"FATAL"`), &s); err != nil {
		t.Fatalf("unmarshal severity: %v", err)
	}
	if s != SeverityFatal {
		t.Fatalf("expected SeverityFatal, got %v", s)
	}
}

func TestHaltErrorFormatting(t *testing.T) {
	cause := errors.New("underlying sqlite disk I/O error")
	dump := CaptureDump("store:migrate", []string{"sqlite_master readable"}, []string{"disk space > 10MB"}, map[string]any{"v": 18}, nil)
	he := &HaltError{
		Code:              CodeMigrationHalt,
		Operation:         "store:migrate",
		Severity:          SeverityFatal,
		Reason:            "Migration 19 failed integrity checks",
		Invariant:         "All schema migrations must apply cleanly without data loss",
		Remediation:       "Restore knowledge store from backup at ~/.local/share/sila/backups",
		Context:           map[string]any{"migration_target": 19},
		Dump:              dump,
		RollbacksExecuted: 2,
		RollbackErrors:    []string{"sync lock release warning"},
		Cause:             cause,
	}

	if !strings.Contains(he.Error(), CodeMigrationHalt) {
		t.Fatalf("expected code in Error(), got: %s", he.Error())
	}
	if !errors.Is(he, cause) {
		t.Fatalf("expected errors.Is to match cause")
	}

	out := he.FormatAndon(true)
	if !strings.Contains(out, "ANDON CORD HALT TRIGGERED") {
		t.Fatalf("expected banner in format, got: %s", out)
	}
	if !strings.Contains(out, "Restore knowledge store from backup") {
		t.Fatalf("expected remediation in format, got: %s", out)
	}
	if !strings.Contains(out, "Migration 19 failed integrity checks") {
		t.Fatalf("expected reason in format, got: %s", out)
	}

	extracted, ok := AsHaltError(he)
	if !ok || extracted == nil {
		t.Fatal("expected AsHaltError to succeed")
	}
}

func TestCordRollbackOrderingAndLIFO(t *testing.T) {
	cord := NewCord("test:batch_mutation")
	order := make([]string, 0, 3)

	cord.RegisterRollback("first", func() error {
		order = append(order, "first")
		return nil
	})
	cord.RegisterRollback("second", func() error {
		order = append(order, "second")
		return errors.New("non-fatal rollback notice")
	})
	cord.RegisterRollback("third", func() error {
		order = append(order, "third")
		return nil
	})

	_ = cord.CheckPrecondition("check_quota", func() error { return nil })
	_ = cord.CheckPrecondition("check_permission", func() error { return errors.New("read-only filesystem") })

	he := cord.Pull(
		CodeInvariantViolation,
		"System must be writable",
		"Permission denied on store dir",
		"Run chmod 0700 on store dir",
		SeverityCritical,
		nil,
		nil,
	)

	if he == nil {
		t.Fatal("expected HaltError from Pull")
	}
	if he.RollbacksExecuted != 3 {
		t.Fatalf("expected 3 rollbacks executed, got %d", he.RollbacksExecuted)
	}
	// LIFO check: third, second, first
	if len(order) != 3 || order[0] != "third" || order[1] != "second" || order[2] != "first" {
		t.Fatalf("expected LIFO rollback order [third, second, first], got %v", order)
	}
	if len(he.RollbackErrors) != 1 || !strings.Contains(he.RollbackErrors[0], "non-fatal rollback notice") {
		t.Fatalf("expected rollback error recorded, got: %v", he.RollbackErrors)
	}
	if len(he.Dump.FailedPreconditions) != 1 {
		t.Fatalf("expected 1 failed precondition, got %v", he.Dump.FailedPreconditions)
	}
}

func TestCordPanicGuard(t *testing.T) {
	cord := NewCord("risky:operation")
	var rolledBack bool
	cord.RegisterRollback("undo_state", func() error {
		rolledBack = true
		return nil
	})

	err := cord.Guard(func() error {
		panic("simulated fatal memory fault")
	})

	if err == nil {
		t.Fatal("expected error from panic guard")
	}
	he, ok := AsHaltError(err)
	if !ok {
		t.Fatalf("expected *HaltError, got %T: %v", err, err)
	}
	if he.Code != CodeRollbackPanic {
		t.Fatalf("expected %s, got %s", CodeRollbackPanic, he.Code)
	}
	if !rolledBack {
		t.Fatal("expected rollback to execute during panic recovery")
	}
}

func TestSmokeSandboxSuccess(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	sandbox := NewSmokeSandbox(db, "test:create_table").WithInvariants(
		InvariantQuickCheck,
		InvariantTablesExist("test_items"),
		InvariantMinRows("test_items", 2),
	)

	// Mutation that creates table and inserts 2 rows
	err = sandbox.Execute(func(tx *sql.Tx) error {
		if _, err := tx.Exec("CREATE TABLE IF NOT EXISTS test_items (id INT PRIMARY KEY, name TEXT)"); err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO test_items VALUES (1, 'apple'), (2, 'banana')"); err != nil {
			return err
		}
		return nil
	})

	if err != nil {
		t.Fatalf("expected SmokeSandbox to succeed, got: %v", err)
	}

	// Verify table exists in live database
	var count int
	if err := db.QueryRow("SELECT count(*) FROM test_items").Scan(&count); err != nil {
		t.Fatalf("query live table: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 rows in live table, got %d", count)
	}
}

func TestSmokeSandboxDryRunAbortOnInvariantFailure(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	// Initial table with 1 item
	_, _ = db.Exec("CREATE TABLE test_data (id INT PRIMARY KEY, val TEXT); INSERT INTO test_data VALUES (1, 'orig')")

	sandbox := NewSmokeSandbox(db, "test:broken_mutation").WithInvariants(
		InvariantMinRows("test_data", 5), // requires 5 rows, but mutation only adds 1 (total 2)
	)

	err = sandbox.Execute(func(tx *sql.Tx) error {
		_, err := tx.Exec("INSERT INTO test_data VALUES (2, 'added')")
		return err
	})

	if err == nil {
		t.Fatal("expected SmokeSandbox to fail due to invariant failure")
	}

	he, ok := AsHaltError(err)
	if !ok {
		t.Fatalf("expected *HaltError, got %T: %v", err, err)
	}
	if he.Code != CodeMutationSandboxFail {
		t.Fatalf("expected CodeMutationSandboxFail, got %s", he.Code)
	}

	// Invariant breached during dry-run: Live DB must be completely untouched! Row count must still be 1!
	var count int
	if err := db.QueryRow("SELECT count(*) FROM test_data").Scan(&count); err != nil {
		t.Fatalf("query count: %v", err)
	}
	if count != 1 {
		t.Fatalf("live database was corrupted! expected 1 row, found %d", count)
	}
}

func TestSmokeSandboxExecuteDryRunOnly(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	sandbox := NewSmokeSandbox(db, "test:dry_run_only").WithInvariants(
		InvariantTablesExist("ephemeral"),
	)

	err = sandbox.ExecuteDryRunOnly(func(tx *sql.Tx) error {
		_, err := tx.Exec("CREATE TABLE ephemeral (id INT)")
		return err
	})

	if err != nil {
		t.Fatalf("expected dry run only to succeed: %v", err)
	}

	// Verify ephemeral does NOT exist in live DB
	var count int
	_ = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='ephemeral'").Scan(&count)
	if count != 0 {
		t.Fatalf("expected table not to exist after ExecuteDryRunOnly, got count %d", count)
	}
}

func TestUserVersionRollback(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	_, err = db.Exec("PRAGMA user_version = 5")
	if err != nil {
		t.Fatalf("set initial user_version: %v", err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	_, err = tx.Exec("PRAGMA user_version = 6")
	if err != nil {
		t.Fatalf("set tx user_version: %v", err)
	}
	_ = tx.Rollback()

	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("scan version: %v", err)
	}
	if version != 5 {
		t.Fatalf("expected user_version 5 after rollback, got %d", version)
	}
}

func TestInvariantQuickCheck_FTS5AutoHeal(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`
		CREATE TABLE facts (id TEXT PRIMARY KEY, project_id TEXT, claim TEXT);
		CREATE VIRTUAL TABLE facts_fts USING fts5(id, project_id, claim, content='facts', content_rowid='rowid');
		INSERT INTO facts VALUES ('fact-1', 'proj-1', 'claim one');
		INSERT INTO facts VALUES ('fact-2', 'proj-1', 'claim two');
		INSERT INTO facts_fts(facts_fts) VALUES('rebuild');
	`); err != nil {
		t.Fatalf("create fts table: %v", err)
	}

	// Corrupt facts_fts shadow table
	if _, err := db.Exec("UPDATE facts_fts_data SET block = X'ffffffffffffffff' WHERE rowid = (SELECT min(rowid) FROM facts_fts_data);"); err != nil {
		t.Fatalf("corrupt facts_fts_data: %v", err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := InvariantQuickCheck(tx); err != nil {
		t.Fatalf("expected InvariantQuickCheck to auto-heal FTS5 corruption, got: %v", err)
	}
}

func TestDisposableSmokeSandbox(t *testing.T) {
	sandbox, err := NewDisposableSmokeSandbox("test:disposable")
	if err != nil {
		t.Fatalf("failed to create disposable smoke sandbox: %v", err)
	}

	err = sandbox.ExecuteDryRunOnly(func(tx *sql.Tx) error {
		if _, err := tx.Exec("CREATE TEMPORARY TABLE _smoke_test (id INT PRIMARY KEY, name TEXT)"); err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO _smoke_test VALUES (1, 'active')"); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected dry-run to succeed on disposable sandbox, got: %v", err)
	}

	tempDir := sandbox.dir
	if err := sandbox.Close(); err != nil {
		t.Fatalf("failed to close disposable sandbox: %v", err)
	}

	// Verify temp directory was removed on close
	if _, err := os.Stat(tempDir); !os.IsNotExist(err) {
		t.Fatalf("expected temp dir %s to be removed, but stat returned: %v", tempDir, err)
	}
}
