package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBackupRestoresCommittedDataUnderActiveWriter inverts the codex-eval F09
// fixture: copying sila.db alone lost committed WAL data ("no such table:
// sample"), while a VACUUM INTO snapshot must contain it.
func TestBackupRestoresCommittedDataUnderActiveWriter(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "sila.db")
	s, err := Open(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Commit a table + row and keep the writer open (automatic checkpoints
	// never run, so committed data can still live in the WAL).
	if _, err := s.DB.Exec(`CREATE TABLE sample (id INTEGER PRIMARY KEY, note TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO sample(note) VALUES('committed while live')`); err != nil {
		t.Fatal(err)
	}
	// A plain file copy at this moment may be missing the table.
	copyPath := filepath.Join(dir, "naive-copy.db")
	if data, err := os.ReadFile(srcPath); err == nil {
		if err := os.WriteFile(copyPath, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	dest := filepath.Join(dir, "snapshot.db")
	if err := s.Backup(dest); err != nil {
		t.Fatalf("Backup: %v", err)
	}

	// The snapshot must be a complete, queryable database.
	restored, err := Open(dest)
	if err != nil {
		t.Fatalf("open restored snapshot: %v", err)
	}
	defer restored.Close()
	var note string
	if err := restored.DB.QueryRow(`SELECT note FROM sample`).Scan(&note); err != nil {
		t.Fatalf("snapshot missing committed table/row: %v", err)
	}
	if note != "committed while live" {
		t.Fatalf("snapshot row = %q", note)
	}

	// And the writer is still healthy afterwards.
	if _, err := s.DB.Exec(`INSERT INTO sample(note) VALUES('post-backup write')`); err != nil {
		t.Fatalf("writer broken after backup: %v", err)
	}

	if last, err := s.LastBackupTime(); err != nil || last.IsZero() {
		t.Fatalf("last_backup not recorded (err=%v)", err)
	}
}

// TestBackupRecordsMarkerFromReadOnlyHandle pins the fix for the silent
// last_backup gap: `sila backup` opens the store read-only, so the marker write
// must go through the writable fallback rather than vanishing. Before the fix
// LastBackupTime stayed zero while the command reported success.
func TestBackupRecordsMarkerFromReadOnlyHandle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sila.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`CREATE TABLE IF NOT EXISTS sample (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	ro, err := OpenRO(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()

	dest := filepath.Join(dir, "snapshot.db")
	if err := ro.Backup(dest); err != nil {
		t.Fatalf("Backup from a read-only handle: %v", err)
	}

	last, err := ro.LastBackupTime()
	if err != nil {
		t.Fatalf("LastBackupTime: %v", err)
	}
	if last.IsZero() {
		t.Fatal("last_backup marker was not recorded from a read-only handle")
	}
}

func TestBackupRefusesExistingDestination(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	dest := filepath.Join(dir, "snapshot.db")
	if err := os.WriteFile(dest, []byte("do not clobber"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = s.Backup(dest)
	if err == nil {
		t.Fatal("expected refusal to overwrite an existing backup destination")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := os.ReadFile(dest)
	if string(data) != "do not clobber" {
		t.Fatal("existing destination was modified")
	}
}

// TestOpenRejectsFutureSchema covers the codex-eval F09 finding that
// user_version=999 (a newer, unsupported schema) opened silently.
func TestOpenRejectsFutureSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "future.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	future := CurrentSchemaVersion() + 25
	if _, err := s.DB.Exec("PRAGMA user_version = " + itoa(future)); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = Open(path)
	if err == nil {
		t.Fatal("expected a future-schema open to fail")
	}
	if !strings.Contains(err.Error(), "newer than this sila build supports") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestMigrateRecordsVersionTransactionally pins that migrations leave no
// crash window: the version is committed with the schema, so a re-open sees a
// consistent (schema, user_version) pair.
func TestMigrateRecordsVersionTransactionally(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tx.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	var version, tables int
	if err := s.DB.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != CurrentSchemaVersion() {
		t.Fatalf("user_version = %d, want %d", version, CurrentSchemaVersion())
	}
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('source_occurrences','messages','facts')`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 3 {
		t.Fatalf("expected the v13 schema to be present, found %d core tables", tables)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestOpenROReadsNonWALDatabase covers the regression that made `sila doctor
// --db=<snapshot>` fail with "attempt to write a readonly database": OpenRO
// must not attempt to set journal_mode, because switching a non-WAL database
// into WAL requires a write.
func TestOpenROReadsNonWALDatabase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plain.db")

	// Plain (DELETE journal mode) database, exactly like a VACUUM INTO snapshot.
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("PRAGMA journal_mode=DELETE"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`CREATE TABLE IF NOT EXISTS sample (id INTEGER PRIMARY KEY, note TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO sample(note) VALUES('read me')`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	ro, err := OpenRO(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()

	var journal string
	if err := ro.DB.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil {
		t.Fatalf("journal_mode read failed: %v", err)
	}
	var integrity string
	if err := ro.DB.QueryRow("PRAGMA integrity_check(10)").Scan(&integrity); err != nil {
		t.Fatalf("integrity_check on a read-only handle failed: %v", err)
	}
	if integrity != "ok" {
		t.Fatalf("integrity_check = %q, want ok", integrity)
	}
	var note string
	if err := ro.DB.QueryRow(`SELECT note FROM sample`).Scan(&note); err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if note != "read me" {
		t.Fatalf("note = %q", note)
	}
}

// TestDoctorVerifiesSnapshot walks the documented recovery procedure
// end-to-end: take a snapshot, then diagnose it through the read-only path.
func TestDoctorVerifiesSnapshot(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "sila.db")
	s, err := Open(src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`CREATE TABLE IF NOT EXISTS sample (id INTEGER PRIMARY KEY, note TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO sample(note) VALUES('snapshot content')`); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(dir, "snapshot.db")
	if err := s.Backup(dest); err != nil {
		t.Fatal(err)
	}
	s.Close()

	fi, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("snapshot mode = %04o, want 0600", fi.Mode().Perm())
	}

	ro, err := OpenRO(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	rep, err := ro.Doctor(false)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range rep.Checks {
		if c.Status == StatusFail {
			t.Fatalf("snapshot verification reported a failure: %s — %s", c.Name, c.Detail)
		}
	}
}

// TestOpenROReopensReadOnlyAfterUpgrade is the re-eval F09 reproduction: a
// read-only open of an older store must migrate it (queries need current
// columns) but hand back a handle that honors the read-only contract, and must
// not leave the store version-mismatched afterwards.
func TestOpenROReopensReadOnlyAfterUpgrade(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "old.db")

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`CREATE TABLE IF NOT EXISTS sample (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	// Simulate a store written by the previous release.
	if _, err := s.DB.Exec("PRAGMA user_version = 12"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	ro, err := OpenRO(path)
	if err != nil {
		t.Fatalf("read-only open of an older store must still work: %v", err)
	}
	// The handle must refuse writes: a caller told "read-only" must not be able
	// to mutate the store through it.
	if _, werr := ro.DB.Exec(`INSERT INTO sample(id) VALUES (1)`); werr == nil {
		t.Fatal("handle returned by OpenRO accepted a write")
	}
	if err := ro.Close(); err != nil {
		t.Fatal(err)
	}

	// The upgrade happened (the read path needed the new columns), and the
	// version now matches the schema on disk.
	after, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer after.Close()
	var version int
	if err := after.DB.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != CurrentSchemaVersion() {
		t.Fatalf("store left at version %d after a read; want %d", version, CurrentSchemaVersion())
	}
}
