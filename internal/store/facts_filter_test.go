package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func factFilterFixture(t *testing.T) (*Store, time.Time) {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "ff.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	recent := now.Add(-2 * 24 * time.Hour).Format(time.RFC3339)
	insert := func(id string, fileRefs, stale int, disputed, review, checkedAt, validUntil, supersedes string) {
		t.Helper()
		_, err := st.DB.Exec(`INSERT INTO facts (id, project_id, claim, confidence, verified, file_refs_exist, repo_files, tools,
			session_ref, first_seen, promoted_at, refs_checked_at, fact_hash, prev_hash, is_stale, disputed_by, review_status,
			supersedes_id, valid_until, kind)
			VALUES (?, 'p', ?, 'grounded', 0, ?, '["a.go"]', '[]', 's', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'code_claim')`,
			id, "claim-"+id, fileRefs, recent, recent, checkedAt, "hash-"+id, GenesisHash, stale, disputed, review, supersedes, validUntil)
		if err != nil {
			t.Fatal(err)
		}
	}
	insert("v1", 1, 0, "", "approved", recent, "", "")
	insert("v2", 1, 0, "", "approved", recent, "", "")
	insert("v3", 1, 0, "", "approved", recent, "", "")
	insert("s1", 1, 1, "", "approved", recent, "", "")
	insert("s2", 1, 0, "", "approved", now.Add(-60*24*time.Hour).Format(time.RFC3339), "", "")
	insert("u1", 0, 0, "", "approved", recent, "", "")
	insert("u2", 0, 0, "", "approved", recent, "", "")
	insert("d1", 1, 0, "agent-b", "approved", recent, "", "")
	insert("e1", 1, 0, "", "approved", recent, now.Add(-24*time.Hour).Format(time.RFC3339), "")
	insert("a1", 1, 0, "", "archived", recent, "", "")
	return st, now
}

func TestQueryFactsFilteredDefaultsToVerified(t *testing.T) {
	st, now := factFilterFixture(t)
	read, err := st.QueryFactsFiltered("p", FactFilter{}, now, 30*24*time.Hour, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Facts) != 3 {
		t.Fatalf("visible = %d: %+v", len(read.Facts), read.Hidden)
	}
	if read.Hidden.Stale != 2 || read.Hidden.Unverified != 2 || read.Hidden.Disputed != 1 || read.Hidden.Expired != 1 {
		t.Fatalf("hidden = %+v", read.Hidden)
	}
	read, err = st.QueryFactsFiltered("p", FactFilter{IncludeStale: true}, now, 30*24*time.Hour, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Facts) != 5 || read.Hidden.Stale != 0 {
		t.Fatalf("include stale: facts=%d hidden=%+v", len(read.Facts), read.Hidden)
	}
	read, err = st.QueryFactsFiltered("p", FactFilter{IncludeExpired: true}, now, 30*24*time.Hour, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Facts) != 4 || read.Hidden.Expired != 0 {
		t.Fatalf("include expired: facts=%d hidden=%+v", len(read.Facts), read.Hidden)
	}
}

func TestQueryFactsFilteredExcludesSuperseded(t *testing.T) {
	st, now := factFilterFixture(t)
	recent := now.Add(-time.Hour).Format(time.RFC3339)
	if _, err := st.DB.Exec(`INSERT INTO facts (id, project_id, claim, confidence, file_refs_exist, repo_files, tools, session_ref,
		first_seen, promoted_at, refs_checked_at, fact_hash, prev_hash, review_status, supersedes_id)
		VALUES ('v9','p','claim-v9','grounded',1,'["a.go"]','[]','s',?,?,?,?,?,'approved','v1')`, recent, recent, recent, "hash-v9", GenesisHash); err != nil {
		t.Fatal(err)
	}
	read, err := st.QueryFactsFiltered("p", FactFilter{}, now, 30*24*time.Hour, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, fact := range read.Facts {
		if fact.ID == "v1" {
			t.Fatal("superseded row visible")
		}
	}
}

func TestReconcileFlipsStaleAndBack(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	dir := t.TempDir()
	if _, err := st.ResolveScope(dir, dir); err != nil {
		t.Fatal(err)
	}
	project := ProjectID(dir)
	now := time.Now().UTC()
	recent := now.Format(time.RFC3339)
	if _, err := st.DB.Exec(`INSERT INTO facts (id, project_id, claim, confidence, verified, file_refs_exist, repo_files, tools,
		session_ref, first_seen, promoted_at, refs_checked_at, fact_hash, prev_hash, kind)
		VALUES ('f1', ?, 'claim-f1', 'grounded', 1, 1, '["a.go"]', '[]', 's', ?, ?, ?, 'hash-f1', ?, 'code_claim')`,
		project, recent, recent, recent, GenesisHash); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "a.go")
	if err := os.WriteFile(file, []byte("package a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ReconcileFactDrift(project, dir); err != nil {
		t.Fatal(err)
	}
	read, err := st.QueryFactsFiltered(project, FactFilter{}, now, 30*24*time.Hour, 0)
	if err != nil || len(read.Facts) != 1 {
		t.Fatalf("after create: facts=%d err=%v", len(read.Facts), err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ReconcileFactDrift(project, dir); err != nil {
		t.Fatal(err)
	}
	read, err = st.QueryFactsFiltered(project, FactFilter{}, now.Add(time.Minute), 30*24*time.Hour, 0)
	if err != nil || len(read.Facts) != 0 || read.Hidden.Stale != 1 {
		t.Fatalf("after remove: facts=%d hidden=%+v err=%v", len(read.Facts), read.Hidden, err)
	}
	var reason string
	_ = st.DB.QueryRow(`SELECT stale_reason FROM facts WHERE id='f1'`).Scan(&reason)
	if reason != "referenced files missing" {
		t.Fatalf("reason = %q", reason)
	}
	if err := os.WriteFile(file, []byte("package a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ReconcileFactDrift(project, dir); err != nil {
		t.Fatal(err)
	}
	read, err = st.QueryFactsFiltered(project, FactFilter{}, now.Add(2*time.Minute), 30*24*time.Hour, 0)
	if err != nil || len(read.Facts) != 1 {
		t.Fatalf("after restore: facts=%d err=%v", len(read.Facts), err)
	}
	_ = st.DB.QueryRow(`SELECT stale_reason FROM facts WHERE id='f1'`).Scan(&reason)
	if reason != "" {
		t.Fatalf("reason after restore = %q", reason)
	}
}
