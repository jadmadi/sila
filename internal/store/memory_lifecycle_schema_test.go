package store

import (
	"path/filepath"
	"testing"
)

func TestMigration43Additive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v42.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	rec := seedLifecycleMemory(t, st, "proj", "Survives migration", "Migration 43 keeps records")
	fact, err := st.FactByID(rec.FactID)
	if err != nil || fact == nil {
		t.Fatalf("fact before downgrade: %v", err)
	}
	if _, err := st.DB.Exec(`DROP TABLE IF EXISTS memory_lifecycle; PRAGMA user_version=42;`); err != nil {
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
	if !storeTableExists(t, st, "memory_lifecycle") {
		t.Fatal("memory_lifecycle was not created")
	}
	for _, index := range []string{"idx_memory_lifecycle_memory", "idx_memory_lifecycle_project"} {
		var name string
		if err := st.DB.QueryRow(`SELECT name FROM sqlite_master WHERE type='index' AND name=?`, index).Scan(&name); err != nil {
			t.Fatalf("missing index %s: %v", index, err)
		}
	}
	loaded, err := st.MemoryRecordByID(rec.ID)
	if err != nil || loaded.State != string(MemoryStateCurrent) || loaded.Digest != rec.Digest {
		t.Fatalf("record after migration: %+v err=%v", loaded, err)
	}
	after, err := st.FactByID(rec.FactID)
	if err != nil || after == nil || after.FactHash != fact.FactHash || after.PrevHash != fact.PrevHash {
		t.Fatalf("fact chain changed by migration")
	}
	audit, err := st.VerifyChain("")
	if err != nil || !audit.Intact {
		t.Fatalf("hash chain after migration: %+v err=%v", audit, err)
	}
}

func TestMigration43Idempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reopen43.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	rec := seedLifecycleMemory(t, st, "proj", "Reopen", "Migration 43 is idempotent")
	if _, err := st.DB.Exec(`PRAGMA user_version=42`); err != nil {
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
	if !storeTableExists(t, st, "memory_lifecycle") {
		t.Fatal("memory_lifecycle missing after reopen")
	}
	loaded, err := st.MemoryRecordByID(rec.ID)
	if err != nil || loaded.Digest != rec.Digest {
		t.Fatalf("record after reopen: %+v err=%v", loaded, err)
	}
}
