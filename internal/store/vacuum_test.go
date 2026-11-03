package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStoreStatsAndVacuum(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_vacuum.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Initial stats
	stats, err := st.Stats()
	if err != nil {
		t.Fatalf("Stats() failed: %v", err)
	}
	if stats.PageCount == 0 {
		t.Fatalf("expected non-zero page count, got %d", stats.PageCount)
	}
	if stats.Path != dbPath {
		t.Fatalf("expected path %q, got %q", dbPath, stats.Path)
	}

	// Insert many messages to allocate pages
	var msgs []LoadMessage
	for i := range 200 {
		msgs = append(msgs, LoadMessage{
			ProjectID:   "test-proj",
			ContentHash: fmtHash(i),
			TS:          time.Now().UTC().Format(time.RFC3339),
			Role:        "assistant",
			Content:     "Some lengthy test content for allocating SQLite pages across tables and FTS indexes.",
			Tool:        "devin",
		})
	}
	if _, _, err := st.WriteRun("", "devin", "", msgs); err != nil {
		t.Fatal(err)
	}

	// Delete half the messages to create free pages in SQLite freelist
	if _, err := st.DB.Exec("DELETE FROM messages WHERE id % 2 = 0"); err != nil {
		t.Fatal(err)
	}

	// Rebuild FTS
	_, _ = st.DB.Exec("INSERT INTO messages_fts(messages_fts) VALUES('rebuild')")

	// Run vacuum
	vacRes, err := st.Vacuum()
	if err != nil {
		t.Fatalf("Vacuum() failed: %v", err)
	}
	if !vacRes.Optimized {
		t.Fatalf("expected Optimized = true")
	}
	if vacRes.After.FreelistCount != 0 {
		t.Fatalf("expected 0 freelist pages after vacuum, got %d", vacRes.After.FreelistCount)
	}

	// Check last vacuum time
	lastTime, err := st.LastVacuumTime()
	if err != nil {
		t.Fatalf("LastVacuumTime() failed: %v", err)
	}
	if lastTime.IsZero() {
		t.Fatalf("expected non-zero last vacuum time")
	}
}

func TestAutoVacuumIfNeeded(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_autovacuum.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Initial state: nothing to vacuum
	vac, res, err := st.AutoVacuumIfNeeded(100, 24*time.Hour)
	if err != nil {
		t.Fatalf("AutoVacuumIfNeeded() failed: %v", err)
	}
	if vac || res != nil {
		t.Fatalf("expected no vacuum needed, got vac=%v, res=%+v", vac, res)
	}

	// Force vacuum once
	if _, err := st.Vacuum(); err != nil {
		t.Fatal(err)
	}

	// Immediately calling AutoVacuum with minInterval=24h should return false
	vac, res, err = st.AutoVacuumIfNeeded(1, 24*time.Hour)
	if err != nil {
		t.Fatalf("AutoVacuumIfNeeded() failed: %v", err)
	}
	if vac || res != nil {
		t.Fatalf("expected minInterval gating to prevent vacuum, got vac=%v", vac)
	}
}

func fmtHash(i int) string {
	return FactID("test", string(rune('a'+i%26)))
}
