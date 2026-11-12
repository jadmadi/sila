package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func seedAsOfLifecycle(t *testing.T, st *Store) {
	t.Helper()
	insertFact := func(id, firstSeen, status, supersedes string) {
		t.Helper()
		if _, err := st.DB.Exec(`INSERT INTO facts(id, project_id, claim, confidence, repo_files, tools, session_ref, first_seen, promoted_at, review_status, supersedes_id)
			VALUES (?, 'g', ?, 'grounded', '[]', '[]', '', ?, ?, ?, ?)`, id, "claim "+id, firstSeen, firstSeen, status, supersedes); err != nil {
			t.Fatal(err)
		}
	}
	insertFact("f-old", "2025-12-01T00:00:00Z", "approved", "")
	insertFact("f-new", "2026-07-01T00:00:00Z", "approved", "f-old")
	insertFact("f-archived", "2025-11-01T00:00:00Z", "archived", "")
	if _, err := st.DB.Exec(`INSERT INTO memory_records(id, project_id, fact_id, kind, title, summary, digest, envelope_json, state, created_at, updated_at)
		VALUES ('m1','g','f1','memo','t','memo summary','d','{}','current','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	insertEvent := func(id, action, toState, at string) {
		t.Helper()
		if _, err := st.DB.Exec(`INSERT INTO memory_lifecycle(event_id, memory_id, project_id, action, from_state, to_state, authority, reason, actor, created_at, event_hash)
			VALUES (?, 'm1', 'g', ?, 'current', ?, 'user_request', 'other', 'test', ?, 'hash')`, id, action, toState, at); err != nil {
			t.Fatal(err)
		}
	}
	insertEvent("e1", "archive", "archived", "2026-05-01T00:00:00Z")
	insertEvent("e2", "restore", "current", "2026-07-15T00:00:00Z")
}

func TestAsOfGoldens(t *testing.T) {
	st := asOfStore(t)
	seedAsOfLifecycle(t, st)
	dates := []string{"2025-12-15", "2026-03-01", "2026-06-01", "2026-08-01"}
	results := map[string]map[string][]string{}
	for _, date := range dates {
		parsed, err := time.Parse("2006-01-02", date)
		if err != nil {
			t.Fatal(err)
		}
		facts, err := st.FactsVisibleAt("g", parsed, 50)
		if err != nil {
			t.Fatal(err)
		}
		records, err := st.MemoryRecordsVisibleAt("g", parsed, 50)
		if err != nil {
			t.Fatal(err)
		}
		factIDs := make([]string, 0, len(facts))
		for _, fact := range facts {
			factIDs = append(factIDs, fact.ID)
		}
		recordIDs := make([]string, 0, len(records))
		for _, record := range records {
			recordIDs = append(recordIDs, record.ID)
		}
		sort.Strings(factIDs)
		sort.Strings(recordIDs)
		results[date] = map[string][]string{"facts": factIDs, "memos": recordIDs}
	}
	raw, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "as_of", "goldens.json")
	if os.Getenv("SILA_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden: %v", err)
	}
	if string(raw)+"\n" != string(want) {
		t.Fatal("as-of golden drifted; run with SILA_UPDATE_GOLDEN=1")
	}
}
