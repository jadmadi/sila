package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestMigration44Additive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v43.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	env := testMemoryEnvelope("proj", "Survives feedback migration", "Migration 44 keeps records")
	rec, _, err := st.SaveMemoryEnvelope(context.Background(), "proj", env, Provenance{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`DROP TABLE IF EXISTS memory_retrieval_items;
		DROP TABLE IF EXISTS memory_retrievals;
		DROP TABLE IF EXISTS memory_feedback;
		PRAGMA user_version=43;`); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	version, err := st.SchemaVersion()
	if err != nil || version != CurrentSchemaVersion() {
		t.Fatalf("version=%d err=%v", version, err)
	}
	for _, table := range []string{"memory_retrievals", "memory_retrieval_items", "memory_feedback"} {
		if !storeTableExists(t, st, table) {
			t.Fatalf("%s was not created", table)
		}
	}
	for _, index := range []string{
		"idx_memory_retrievals_project", "idx_memory_retrieval_items_memory",
		"idx_memory_feedback_unique", "idx_memory_feedback_memory",
	} {
		var name string
		if err := st.DB.QueryRow(`SELECT name FROM sqlite_master WHERE type='index' AND name=?`, index).Scan(&name); err != nil {
			t.Fatalf("missing index %s: %v", index, err)
		}
	}
	loaded, err := st.MemoryRecordByID(rec.ID)
	if err != nil || loaded.Digest != rec.Digest || loaded.State != string(MemoryStateCurrent) {
		t.Fatalf("record after migration: %+v err=%v", loaded, err)
	}
	report, err := st.Doctor(false)
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	found := false
	for _, check := range report.Checks {
		if check.Name == "Memory Relevance Feedback" {
			found = true
			if check.Status != StatusPass {
				t.Fatalf("feedback health check = %s: %s", check.Status, check.Message)
			}
		}
	}
	if !found {
		t.Fatal("doctor does not report memory relevance feedback health")
	}
}

func TestMigration44Idempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reopen44.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	env := testMemoryEnvelope("proj", "Reopen", "Migration 44 is idempotent")
	rec, _, err := st.SaveMemoryEnvelope(context.Background(), "proj", env, Provenance{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`PRAGMA user_version=43`); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if !storeTableExists(t, st, "memory_feedback") {
		t.Fatal("memory_feedback missing after reopen")
	}
	loaded, err := st.MemoryRecordByID(rec.ID)
	if err != nil || loaded.Digest != rec.Digest {
		t.Fatalf("record after reopen: %+v err=%v", loaded, err)
	}
	// A second reopen must not re-run destructive DDL or change rows.
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if version, err := st.SchemaVersion(); err != nil || version != CurrentSchemaVersion() {
		t.Fatalf("second reopen version=%d err=%v", version, err)
	}
}

func TestMigration44FromV41(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v41.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	env := testMemoryEnvelope("proj", "From v41", "Migration 44 upgrades a v41 store")
	rec, _, err := st.SaveMemoryEnvelope(context.Background(), "proj", env, Provenance{})
	if err != nil {
		t.Fatal(err)
	}
	// v41 predates both the lifecycle ledger (43) and this goal's tables (44).
	if _, err := st.DB.Exec(`DROP TABLE IF EXISTS memory_lifecycle;
		DROP TABLE IF EXISTS memory_retrieval_items;
		DROP TABLE IF EXISTS memory_retrievals;
		DROP TABLE IF EXISTS memory_feedback;
		PRAGMA user_version=41;`); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if version, err := st.SchemaVersion(); err != nil || version != CurrentSchemaVersion() {
		t.Fatalf("version=%d err=%v", version, err)
	}
	for _, table := range []string{"memory_lifecycle", "memory_retrievals", "memory_retrieval_items", "memory_feedback"} {
		if !storeTableExists(t, st, table) {
			t.Fatalf("%s missing after v41 upgrade", table)
		}
	}
	loaded, err := st.MemoryRecordByID(rec.ID)
	if err != nil || loaded.Digest != rec.Digest {
		t.Fatalf("record after v41 upgrade: %+v err=%v", loaded, err)
	}
}
