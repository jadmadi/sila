package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func asOfStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "asof.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.DB.Exec(`INSERT INTO facts(id, project_id, claim, confidence, repo_files, tools, session_ref, first_seen, promoted_at, review_status)
		VALUES ('old','p','old claim','grounded','[]','[]','','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z','approved')`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`INSERT INTO facts(id, project_id, claim, confidence, repo_files, tools, session_ref, first_seen, promoted_at, review_status, supersedes_id)
		VALUES ('new','p','new claim','grounded','[]','[]','','2026-07-01T00:00:00Z','2026-07-01T00:00:00Z','approved','old')`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`INSERT INTO facts(id, project_id, claim, confidence, repo_files, tools, session_ref, first_seen, promoted_at, review_status)
		VALUES ('archived','p','archived claim','grounded','[]','[]','','2025-12-01T00:00:00Z','2025-12-01T00:00:00Z','archived')`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`INSERT INTO facts(id, project_id, claim, confidence, repo_files, tools, session_ref, first_seen, promoted_at, review_status)
		VALUES ('legacy','p','legacy claim','grounded','[]','[]','','','', 'approved')`); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestFactsVisibleAt(t *testing.T) {
	st := asOfStore(t)
	before := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	visible, err := st.FactsVisibleAt("p", before, 50)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, fact := range visible {
		ids[fact.ID] = true
	}
	if ids["old"] || ids["new"] || ids["archived"] || !ids["legacy"] {
		t.Fatalf("before ids = %v", ids)
	}
	mid := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	visible, err = st.FactsVisibleAt("p", mid, 50)
	if err != nil {
		t.Fatal(err)
	}
	ids = map[string]bool{}
	for _, fact := range visible {
		ids[fact.ID] = true
		if fact.ID == "legacy" && !fact.LegacyUnknownTimestamp {
			t.Fatal("legacy marker missing")
		}
	}
	if !ids["old"] || ids["new"] || ids["archived"] || !ids["legacy"] {
		t.Fatalf("mid ids = %v", ids)
	}
	after := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	visible, err = st.FactsVisibleAt("p", after, 50)
	if err != nil {
		t.Fatal(err)
	}
	ids = map[string]bool{}
	for _, fact := range visible {
		ids[fact.ID] = true
	}
	if ids["old"] || !ids["new"] || ids["archived"] {
		t.Fatalf("after ids = %v", ids)
	}
}

func TestFactsVisibleAtUsesIndex(t *testing.T) {
	st := asOfStore(t)
	rows, err := st.DB.Query(`EXPLAIN QUERY PLAN SELECT id FROM facts WHERE project_id = 'p' AND first_seen <= '2026-06-01T00:00:00Z'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(detail + "\n")
	}
	if !strings.Contains(plan.String(), "idx_facts_project_first_seen") {
		t.Fatalf("plan = %s", plan.String())
	}
}

func TestMemoryRecordsVisibleAt(t *testing.T) {
	st := asOfStore(t)
	if _, err := st.DB.Exec(`INSERT INTO memory_records(id, project_id, fact_id, kind, title, summary, digest, envelope_json, state, created_at, updated_at)
		VALUES ('m1','p','f1','memo','t','s','d','{}','current','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	insertEvent := func(id, action, toState, at string) {
		t.Helper()
		if _, err := st.DB.Exec(`INSERT INTO memory_lifecycle(event_id, memory_id, project_id, action, from_state, to_state, authority, reason, actor, created_at, event_hash)
			VALUES (?, 'm1', 'p', ?, 'current', ?, 'user_request', 'other', 'test', ?, 'hash')`, id, action, toState, at); err != nil {
			t.Fatal(err)
		}
	}
	insertEvent("e1", "archive", "archived", "2026-05-01T00:00:00Z")
	insertEvent("e2", "restore", "current", "2026-07-01T00:00:00Z")
	check := func(at string, want bool) {
		t.Helper()
		when, err := time.Parse(time.RFC3339, at)
		if err != nil {
			t.Fatal(err)
		}
		records, err := st.MemoryRecordsVisibleAt("p", when, 10)
		if err != nil {
			t.Fatal(err)
		}
		got := false
		for _, record := range records {
			if record.ID == "m1" {
				got = true
			}
		}
		if got != want {
			t.Fatalf("at %s = %v, want %v", at, got, want)
		}
	}
	check("2026-03-01T00:00:00Z", true)
	check("2026-06-01T00:00:00Z", false)
	check("2026-08-01T00:00:00Z", true)
}
