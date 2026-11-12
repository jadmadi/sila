package dream

import (
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func expandStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "e.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	for i, id := range []string{"f1", "f2", "f3"} {
		if err := st.UpsertFact(store.Fact{
			ID: id, ProjectID: "p", Claim: "claim " + id, Kind: "lesson",
			FirstSeen:    []string{"2026-11-01T10:00:00Z", "2026-11-02T10:00:00Z", "2026-11-03T10:00:00Z"}[i],
			ReviewStatus: "approved", FileRefsExist: true,
		}); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

func TestRecallHitsByIDsOrderAndMissing(t *testing.T) {
	st := expandStore(t)
	hits, missing := RecallHitsByIDs(st, []string{"f3", "f1", "missing", "f3"})
	if len(hits) != 2 || hits[0].ID != "f3" || hits[1].ID != "f1" {
		t.Fatalf("hits = %+v", hits)
	}
	if len(missing) != 1 || missing[0] != "missing" {
		t.Fatalf("missing = %v", missing)
	}
	if !hits[0].Grounded || hits[0].Resolution == "" {
		t.Fatalf("expanded hit lost detail: %+v", hits[0])
	}
}

func TestRecallTimelineOrder(t *testing.T) {
	st := expandStore(t)
	hits, reason := RecallTimeline(st, "p", "f2", 1, 1)
	if reason != "" || len(hits) != 3 {
		t.Fatalf("hits=%d reason=%q", len(hits), reason)
	}
	if hits[0].ID != "f1" || hits[1].ID != "f2" || hits[2].ID != "f3" {
		t.Fatalf("order = %+v", hits)
	}
	if _, reason := RecallTimeline(st, "p", "missing", 1, 1); reason == "" {
		t.Fatal("missing anchor did not report a reason")
	}
}
