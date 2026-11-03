package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

// TestChallenge_AssessZeroWrite_ExistingDB verifies whether running assess against an
// existing database mutates the database file, its mtime, or its content.
func TestChallenge_AssessZeroWrite_ExistingDB(t *testing.T) {
	tempDir := t.TempDir()
	dbDir := filepath.Join(tempDir, ".sila")
	if err := os.MkdirAll(dbDir, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	dbPath := filepath.Join(dbDir, "sila.db")

	// 1. Create a real populated store
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open failed: %v", err)
	}
	_, err = st.SaveLesson("proj-1", "test claim", "test source", "test body", "tag1", "author", tempDir, nil)
	if err != nil {
		t.Fatalf("SaveLesson failed: %v", err)
	}
	st.Close()

	// Checkpoint WAL if any
	stCheckpoint, err := store.Open(dbPath)
	if err == nil {
		_, _ = stCheckpoint.DB.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
		stCheckpoint.Close()
	}

	// Set mode to 0644 to test permission preservation
	if err := os.Chmod(dbPath, 0644); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	// Insert a watermark record with value '0'
	stW, err := store.Open(dbPath)
	if err == nil {
		if _, err := stW.DB.Exec("INSERT INTO state (key, value, updated_at) VALUES ('watermark:test_check', '0', '2026-09-28T00:00:00Z')"); err != nil {
			t.Fatalf("insert test_check watermark: %v", err)
		}
		stW.Close()
	}

	// Wait 20ms so filesystem mtime changes if touched
	time.Sleep(25 * time.Millisecond)

	infoBefore, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("stat before: %v", err)
	}
	bytesBefore, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("read before: %v", err)
	}

	ui := render.New(nil, true)

	// 2. Run skill assess
	runSkills(ui, dbPath, true, false, skillCreateOptions{outDir: tempDir}, []string{"assess", "--json"}, "skill")

	// 3. Inspect database after assess
	infoAfter, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("stat after: %v", err)
	}
	bytesAfter, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("read after: %v", err)
	}

	var violations []string
	if infoBefore.Mode() != infoAfter.Mode() {
		violations = append(violations, "Database FileMode was mutated: before="+infoBefore.Mode().String()+" after="+infoAfter.Mode().String())
	}
	if !infoBefore.ModTime().Equal(infoAfter.ModTime()) {
		violations = append(violations, "Database ModTime was mutated: before="+infoBefore.ModTime().String()+" after="+infoAfter.ModTime().String())
	}
	if len(bytesBefore) != len(bytesAfter) {
		violations = append(violations, "Database size changed")
	} else if string(bytesBefore) != string(bytesAfter) {
		violations = append(violations, "Database content was mutated (bytes mismatch)")
	}

	// Check for stray -wal or -shm files created during assess
	walPath := dbPath + "-wal"
	shmPath := dbPath + "-shm"
	if fi, err := os.Stat(walPath); err == nil && fi.Size() > 0 {
		violations = append(violations, "Database WAL file was created or has non-zero size")
	}
	if _, err := os.Stat(shmPath); err == nil {
		violations = append(violations, "Database SHM file was created")
	}

	// Verify if watermark was deleted (data store mutation!)
	stVerify, exists, err := store.OpenInspection(dbPath)
	if err == nil && exists {
		var val string
		errRow := stVerify.DB.QueryRow("SELECT value FROM state WHERE key = 'watermark:test_check'").Scan(&val)
		if errRow != nil {
			violations = append(violations, "Database data was deleted/mutated! Row 'watermark:test_check' was wiped by assess")
		}
		stVerify.Close()
	}

	if len(violations) > 0 {
		t.Errorf("ZERO-WRITE VIOLATION on existing DB:\n%s", strings.Join(violations, "\n"))
	}
}

// TestChallenge_AssessZeroWrite_ReadOnlyFilesystem verifies assess in a read-only directory
// with a read-only database.
func TestChallenge_AssessZeroWrite_ReadOnlyFilesystem(t *testing.T) {
	tempDir := t.TempDir()
	dbDir := filepath.Join(tempDir, ".sila")
	if err := os.MkdirAll(dbDir, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	dbPath := filepath.Join(dbDir, "sila.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open failed: %v", err)
	}
	st.Close()

	// Make database and dir read-only (0444 and 0555)
	if err := os.Chmod(dbPath, 0444); err != nil {
		t.Fatalf("chmod db: %v", err)
	}
	if err := os.Chmod(dbDir, 0555); err != nil {
		t.Fatalf("chmod dbDir: %v", err)
	}
	defer os.Chmod(dbDir, 0755)

	ui := render.New(nil, true)

	// Run assess against read-only database
	runSkills(ui, dbPath, true, false, skillCreateOptions{outDir: tempDir}, []string{"assess", "--json"}, "skill")
}
