package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestListFactsByKindFiltersOrderingAndLimit(t *testing.T) {
	tmp := t.TempDir()
	st, err := Open(filepath.Join(tmp, "kind.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p, err := st.EnsureProject(tmp)
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.EnsureProject(filepath.Join(tmp, "other"))
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	seed := []Fact{
		{ID: "fact_memo_a", ProjectID: p.ID, Claim: "decision a", Kind: "", FirstSeen: now.Add(-3 * time.Hour).Format(time.RFC3339)},
		{ID: "fact_memo_b", ProjectID: p.ID, Claim: "decision b", Kind: "memo", FirstSeen: now.Add(-2 * time.Hour).Format(time.RFC3339)},
		{ID: "fact_lesson", ProjectID: p.ID, Claim: "lesson", Kind: "lesson", FirstSeen: now.Add(-1 * time.Hour).Format(time.RFC3339)},
		{ID: "fact_case", ProjectID: other.ID, Claim: "case", Kind: "case", FirstSeen: now.Format(time.RFC3339)},
	}
	for _, f := range seed {
		if err := st.UpsertFact(f); err != nil {
			t.Fatal(err)
		}
	}

	memos, err := st.ListFactsByKind("memo", p.ID, time.Time{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(memos) != 2 || memos[0].ID != "fact_memo_b" || memos[1].ID != "fact_memo_a" {
		t.Fatalf("unexpected memos: %+v", memos)
	}

	limited, err := st.ListFactsByKind("memo", p.ID, time.Time{}, 1)
	if err != nil || len(limited) != 1 || limited[0].ID != "fact_memo_b" {
		t.Fatalf("limit failed: %+v %v", limited, err)
	}

	since, err := st.ListFactsByKind("memo", p.ID, now.Add(-150*time.Minute), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(since) != 1 || since[0].ID != "fact_memo_b" {
		t.Fatalf("since failed: %+v", since)
	}

	cases, err := st.ListFactsByKind("case", "*", time.Time{}, 10)
	if err != nil || len(cases) != 1 || cases[0].ID != "fact_case" {
		t.Fatalf("global case query failed: %+v %v", cases, err)
	}

	lessons, err := st.LessonFacts(p.ID, 10)
	if err != nil || len(lessons) != 1 || lessons[0].ID != "fact_lesson" {
		t.Fatalf("lesson wrapper failed: %+v %v", lessons, err)
	}
}
