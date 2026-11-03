package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFTS5AutoHealOnCorruption(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test_fts_heal.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer st.Close()

	// Populate messages, facts, and docs
	st.EnsureProject("/tmp/fts-heal-proj")
	_, _, err = st.WriteRun("run-1", "claude-code", "sess-1", []LoadMessage{
		{ProjectID: "fts-heal-proj", ContentHash: "hash-msg-1", TS: "2026-09-22T10:00:00Z", Role: "user", Content: "recovering corrupted index smoothly", Tool: "claude-code"},
		{ProjectID: "fts-heal-proj", ContentHash: "hash-msg-2", TS: "2026-09-22T10:01:00Z", Role: "assistant", Content: "resilience against sqlite corruption", Tool: "claude-code"},
	})
	if err != nil {
		t.Fatalf("WriteRun failed: %v", err)
	}

	fID := "fact-test-123"
	if err := st.UpsertFact(Fact{
		ID:            fID,
		ProjectID:     "fts-heal-proj",
		Claim:         "quantum resilient indexing and auto heal verification",
		Confidence:    "ungrounded",
		Corroboration: "ungrounded",
		FirstSeen:     "2026-09-22T10:00:00Z",
		ReviewStatus:  ReviewApproved,
	}); err != nil {
		t.Fatalf("UpsertFact failed: %v", err)
	}

	doc := Doc{
		ID:        "doc-heal-1",
		ProjectID: "fts-heal-proj",
		Title:     "Healing Docs Specification",
		URL:       "https://example.com/heal",
		Content:   "Self-healing full text search indexes documentation",
		Format:    "markdown",
		CreatedAt: "2026-09-22T10:00:00Z",
	}
	if err := st.InsertDoc(doc); err != nil {
		t.Fatalf("InsertDoc failed: %v", err)
	}

	// Verify normal search works first
	msgs, err := st.SearchMessagesFTS("corrupted", SearchMessagesOptions{Project: "fts-heal-proj"})
	if err != nil || len(msgs) == 0 {
		t.Fatalf("Initial SearchMessagesFTS failed: err=%v len=%d", err, len(msgs))
	}

	facts, err := st.SearchFactsFTS("quantum", "fts-heal-proj", false, 10)
	if err != nil || len(facts) == 0 {
		t.Fatalf("Initial SearchFactsFTS failed: err=%v len=%d", err, len(facts))
	}

	docs, err := st.SearchDocsFTS("healing", "fts-heal-proj", 10)
	if err != nil || len(docs) == 0 {
		t.Fatalf("Initial SearchDocsFTS failed: err=%v len=%d", err, len(docs))
	}

	// 1. Corrupt messages_fts by corrupting an index block in messages_fts_data
	if _, err := st.DB.Exec("UPDATE messages_fts_data SET block = X'ffffffffffffffffffffffffffffffffffffffffffffffff' WHERE id = 10;"); err != nil {
		t.Fatalf("Failed to corrupt messages_fts_data: %v", err)
	}

	// SearchMessagesFTS should detect corruption, auto-heal messages_fts, and return matching results!
	msgsHealed, err := st.SearchMessagesFTS("corrupted", SearchMessagesOptions{Project: "fts-heal-proj"})
	if err != nil {
		t.Fatalf("SearchMessagesFTS after corruption failed: %v", err)
	}
	if len(msgsHealed) == 0 {
		t.Fatalf("expected SearchMessagesFTS to return healed results, got 0")
	}

	// 2. Corrupt facts_fts
	if _, err := st.DB.Exec("UPDATE facts_fts_data SET block = X'ffffffffffffffffffffffffffffffffffffffffffffffff' WHERE id = 10;"); err != nil {
		t.Fatalf("Failed to corrupt facts_fts_data: %v", err)
	}

	// SearchFactsFTS should auto-heal facts_fts and return matching results!
	factsHealed, err := st.SearchFactsFTS("quantum", "fts-heal-proj", false, 10)
	if err != nil {
		t.Fatalf("SearchFactsFTS after corruption failed: %v", err)
	}
	if len(factsHealed) == 0 {
		t.Fatalf("expected SearchFactsFTS to return healed results, got 0")
	}

	// 3. Corrupt docs_fts
	if _, err := st.DB.Exec("UPDATE docs_fts_data SET block = X'ffffffffffffffffffffffffffffffffffffffffffffffff' WHERE id = 10;"); err != nil {
		t.Fatalf("Failed to corrupt docs_fts_data: %v", err)
	}

	// SearchDocsFTS should auto-heal docs_fts and return matching results!
	docsHealed, err := st.SearchDocsFTS("healing", "fts-heal-proj", 10)
	if err != nil {
		t.Fatalf("SearchDocsFTS after corruption failed: %v", err)
	}
	if len(docsHealed) == 0 {
		t.Fatalf("expected SearchDocsFTS to return healed results, got 0")
	}
}

func TestFTS5TimeoutEnforcement(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test_fts_timeout.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer st.Close()

	st.EnsureProject("/tmp/fts-timeout-proj")
	_, _, _ = st.WriteRun("run-t", "claude-code", "sess-t", []LoadMessage{
		{ProjectID: "fts-timeout-proj", ContentHash: "hash-t-1", TS: "2026-09-22T10:00:00Z", Role: "user", Content: "testing timeout deadline exceeded", Tool: "claude-code"},
	})

	// Set FTSQueryTimeout to an expired/zero duration
	origTimeout := FTSQueryTimeout
	defer func() { FTSQueryTimeout = origTimeout }()
	FTSQueryTimeout = -1 * time.Nanosecond

	_, err = st.SearchMessagesFTS("deadline", SearchMessagesOptions{Project: "fts-timeout-proj"})
	if err == nil {
		t.Fatalf("expected SearchMessagesFTS to fail on expired deadline, got nil")
	}
	if !strings.Contains(err.Error(), "fts5 query timed out") {
		t.Fatalf("expected error to mention 'fts5 query timed out', got %v", err)
	}

	_, err = st.SearchFactsFTS("deadline", "fts-timeout-proj", false, 10)
	if err == nil {
		t.Fatalf("expected SearchFactsFTS to fail on expired deadline, got nil")
	}
	if !strings.Contains(err.Error(), "fts5 query timed out") {
		t.Fatalf("expected error to mention 'fts5 query timed out', got %v", err)
	}

	_, err = st.SearchDocsFTS("deadline", "fts-timeout-proj", 10)
	if err == nil {
		t.Fatalf("expected SearchDocsFTS to fail on expired deadline, got nil")
	}
	if !strings.Contains(err.Error(), "fts5 query timed out") {
		t.Fatalf("expected error to mention 'fts5 query timed out', got %v", err)
	}
}

func TestFTS5DoctorIntegrityAndRepair(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test_fts_doctor.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer st.Close()

	st.EnsureProject("/tmp/fts-doc-proj")
	_, _, _ = st.WriteRun("run-doc", "claude-code", "sess-doc", []LoadMessage{
		{ProjectID: "fts-doc-proj", ContentHash: "hash-d-1", TS: "2026-09-22T10:00:00Z", Role: "user", Content: "doctor diagnostic integrity test", Tool: "claude-code"},
	})

	// Corrupt messages_fts index block
	if _, err := st.DB.Exec("UPDATE messages_fts_data SET block = X'deadbeef' WHERE id = 10;"); err != nil {
		t.Fatalf("corrupt messages_fts_data failed: %v", err)
	}

	rep, err := st.Doctor(false)
	if err != nil {
		t.Fatalf("Doctor audit failed: %v", err)
	}
	if rep.OverallStatus != StatusFail {
		t.Fatalf("expected StatusFail on missing/corrupt FTS table, got %v", rep.OverallStatus)
	}

	// Now run repair
	repRepair, err := st.Doctor(true)
	if err != nil {
		t.Fatalf("Doctor repair failed: %v", err)
	}
	if !repRepair.RepairExecuted {
		t.Fatalf("expected RepairExecuted = true")
	}

	// Post-repair audit should pass
	repPost, err := st.Doctor(false)
	if err != nil {
		t.Fatalf("Post-repair Doctor audit failed: %v", err)
	}
	if repPost.OverallStatus == StatusFail {
		t.Fatalf("expected PASS or WARN after repair, got FAIL: %+v", repPost)
	}
}

func TestRebuildFTSIndexValidation(t *testing.T) {
	tmp := t.TempDir()
	st, err := Open(filepath.Join(tmp, "test_fts_val.db"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer st.Close()

	if err := st.RebuildFTSIndex("malicious_table; SELECT 1;--"); err == nil {
		t.Fatalf("expected error on invalid table name, got nil")
	}
}

func TestRebuildFTS_InPlaceAndStoreQuickCheck(t *testing.T) {
	tmp := t.TempDir()
	st, err := Open(filepath.Join(tmp, "test_fts_quick.db"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer st.Close()

	if err := st.UpsertFact(Fact{
		ID:            "fact-qc-1",
		ProjectID:     "qc-proj",
		Claim:         "quick check auto heal test claim",
		Confidence:    "ungrounded",
		Corroboration: "ungrounded",
		FirstSeen:     "2026-09-22T10:00:00Z",
		ReviewStatus:  ReviewApproved,
	}); err != nil {
		t.Fatalf("UpsertFact failed: %v", err)
	}

	// Corrupt facts_fts_data
	if _, err := st.DB.Exec("UPDATE facts_fts_data SET block = X'ffffffffffffffff' WHERE rowid = (SELECT min(rowid) FROM facts_fts_data);"); err != nil {
		t.Fatalf("corrupt facts_fts_data: %v", err)
	}

	// 1. Verify RebuildFTS in-place helper within a transaction
	tx, err := st.DB.Begin()
	if err != nil {
		t.Fatalf("Begin tx: %v", err)
	}
	if err := RebuildFTS(tx, "facts_fts"); err != nil {
		_ = tx.Rollback()
		t.Fatalf("RebuildFTS failed: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit tx: %v", err)
	}

	// 2. Corrupt facts_fts_data again
	if _, err := st.DB.Exec("UPDATE facts_fts_data SET block = X'ffffffffffffffff' WHERE rowid = (SELECT min(rowid) FROM facts_fts_data);"); err != nil {
		t.Fatalf("corrupt facts_fts_data 2: %v", err)
	}

	// 3. Verify st.QuickCheck() auto-heals corruption and returns nil!
	if err := st.QuickCheck(); err != nil {
		t.Fatalf("expected st.QuickCheck to auto-heal FTS5 corruption, got: %v", err)
	}
}
