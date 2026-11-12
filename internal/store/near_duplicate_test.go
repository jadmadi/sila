package store

import (
	"path/filepath"
	"strings"
	"testing"
)

func guardStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "nd.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ResetDocumentFrequencyCache()
	return st
}

func TestNearDuplicateVetoVersionDiff(t *testing.T) {
	st := guardStore(t)
	base := "### Symptom\ndeployment fails on release v0.17\n### Cause\nimage tag\n### Fix\npin tag"
	next := strings.Replace(base, "v0.17", "v0.18", 1)
	if err := st.UpsertFact(Fact{ID: "l1", ProjectID: "p", Claim: base, Kind: "lesson", FirstSeen: "2026-11-06T10:00:00Z", ReviewStatus: "approved"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertFact(Fact{ID: "l2", ProjectID: "p", Claim: next, Kind: "lesson", FirstSeen: "2026-11-06T11:00:00Z", ReviewStatus: "approved"}); err != nil {
		t.Fatal(err)
	}
	var lessons int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM facts WHERE project_id = 'p' AND kind = 'lesson'`).Scan(&lessons); err != nil {
		t.Fatal(err)
	}
	if lessons != 2 {
		t.Fatalf("version-different lessons merged: %d", lessons)
	}
}

func TestNearDuplicateMerge(t *testing.T) {
	st := guardStore(t)
	for i := 0; i < 10; i++ {
		claim := "alpha beta gamma delta common word" + string(rune('a'+i))
		if err := st.UpsertFact(Fact{ProjectID: "p", Claim: claim, Kind: "fact", FirstSeen: "2026-11-06T09:00:00Z", ReviewStatus: "approved"}); err != nil {
			t.Fatal(err)
		}
	}
	first := "deployment pipeline alpha beta gamma delta epsilon zeta eta theta"
	second := "deployment pipeline beta gamma delta epsilon zeta eta theta"
	if err := st.UpsertFact(Fact{ProjectID: "p", Claim: first, Kind: "memo", FirstSeen: "2026-11-06T10:00:00Z", ReviewStatus: "approved", RepoFiles: []string{"a.go"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertFact(Fact{ProjectID: "p", Claim: second, Kind: "memo", FirstSeen: "2026-11-06T11:00:00Z", ReviewStatus: "approved", RepoFiles: []string{"b.go"}}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM facts WHERE project_id = 'p' AND kind = 'memo'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("near-duplicate memo not merged: %d rows", count)
	}
	var repos string
	if err := st.DB.QueryRow(`SELECT repo_files FROM facts WHERE project_id = 'p' AND kind = 'memo'`).Scan(&repos); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(repos, "a.go") || !strings.Contains(repos, "b.go") {
		t.Fatalf("merged evidence = %s", repos)
	}
}

func TestExactDigestStillIdempotent(t *testing.T) {
	st := guardStore(t)
	fact := Fact{ID: "m1", ProjectID: "p", Claim: "exact claim for idempotency", Kind: "memo", FirstSeen: "2026-11-06T10:00:00Z", ReviewStatus: "approved"}
	if err := st.UpsertFact(fact); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertFact(fact); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM facts WHERE project_id = 'p'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("count = %d", count)
	}
}

func TestPrecisionCountersInToolEvents(t *testing.T) {
	st := guardStore(t)
	base := "### Symptom\ndeployment release pipeline image tag pin rollout staging production canary verify metric alert trace span budget quota limit version v9.1\n### Cause\nimage\n### Fix\npin"
	next := strings.Replace(base, "v9.1", "v9.2", 1)
	if err := st.UpsertFact(Fact{ProjectID: "c", Claim: base, Kind: "lesson", FirstSeen: "2026-11-06T10:00:00Z", ReviewStatus: "approved"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertFact(Fact{ProjectID: "c", Claim: next, Kind: "lesson", FirstSeen: "2026-11-06T11:00:00Z", ReviewStatus: "approved"}); err != nil {
		t.Fatal(err)
	}
	var veto int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM tool_events WHERE project_id = 'c' AND tool_name = 'idf_veto'`).Scan(&veto); err != nil {
		t.Fatal(err)
	}
	if veto == 0 {
		t.Fatal("idf_veto counter missing")
	}
}
