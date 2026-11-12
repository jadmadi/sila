package dream

import (
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func TestOutcomeWeightValues(t *testing.T) {
	if OutcomeWeight("success") != 1.0 || OutcomeWeight("corrected") != 0.2 || OutcomeWeight("neutral") != 0.6 || OutcomeWeight("") != 0.6 {
		t.Fatalf("weights = %v %v %v %v", OutcomeWeight("success"), OutcomeWeight("corrected"), OutcomeWeight("neutral"), OutcomeWeight(""))
	}
}

func TestCorrectedTurnRanksBelowNeutral(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "ow.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	insert := func(hash, role, content, outcome string) {
		t.Helper()
		if _, err := st.DB.Exec(`INSERT INTO messages(project_id, content_hash, ts, role, content, source_tools, first_seen, outcome)
			VALUES ('p', ?, '2026-11-06T10:00:00Z', ?, ?, '["claude"]', '2026-11-06T10:00:00Z', ?)`, hash, role, content, outcome); err != nil {
			t.Fatal(err)
		}
	}
	insert("success-1", "assistant", "deployment needle resolved", "success")
	insert("neutral-1", "assistant", "deployment needle neutral", "neutral")
	insert("corrected-1", "assistant", "deployment needle corrected", "corrected")
	if _, err := st.DB.Exec(`INSERT INTO messages_fts(messages_fts) VALUES('rebuild')`); err != nil {
		t.Fatal(err)
	}
	result, err := RecallSolutions(st, "deployment needle", RecallOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	scores := map[string]float64{}
	for _, hit := range result.Hits {
		if hit.Kind == "transcript" {
			scores[hit.Outcome] = hit.Score
		}
	}
	if scores["success"] <= scores["neutral"] || scores["neutral"] <= scores["corrected"] {
		t.Fatalf("scores = %+v", scores)
	}
}
