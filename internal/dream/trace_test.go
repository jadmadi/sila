package dream

import (
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func traceStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "tr.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	lessons := []struct{ id, claim string }{
		{"l1", "### Symptom\ndatabase timeout on start\n### Cause\npool\n### Fix\nraise pool"},
		{"l2", "### Symptom\ndatabase timeout again\n### Cause\npool\n### Fix\nretry"},
		{"l3", "unrelated css bug"},
	}
	for i, lesson := range lessons {
		if err := st.UpsertFact(store.Fact{
			ID: lesson.id, ProjectID: "p", Claim: lesson.claim, Kind: "lesson",
			FirstSeen:    []string{"2026-11-01T10:00:00Z", "2026-11-02T10:00:00Z", "2026-11-03T10:00:00Z"}[i],
			ReviewStatus: "approved",
		}); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

func TestRecallTraceRecordsStages(t *testing.T) {
	st := traceStore(t)
	trace := &RecallTrace{}
	res, err := RecallSolutions(st, "database timeout", RecallOptions{Limit: 1, Trace: trace})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 1 {
		t.Fatalf("hits = %d", len(res.Hits))
	}
	var kept, below, limited int
	var keptEntry RecallTraceEntry
	for _, entry := range trace.Entries {
		switch entry.Decision {
		case "kept":
			kept++
			keptEntry = entry
		case "below-threshold":
			below++
		case "limit":
			limited++
		}
	}
	if kept != 1 || keptEntry.FinalScore != res.Hits[0].Score {
		t.Fatalf("kept=%d entry=%+v hit=%.2f", kept, keptEntry, res.Hits[0].Score)
	}
	if keptEntry.Base == 0 || keptEntry.Match <= 0 {
		t.Fatalf("stages missing: %+v", keptEntry)
	}
	if below == 0 {
		t.Fatal("no below-threshold entry for the unrelated lesson")
	}
	if limited == 0 {
		t.Fatal("no limit decision for the second matching lesson")
	}
}
