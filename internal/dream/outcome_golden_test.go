package dream

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func TestOutcomeRankingGolden(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "rg.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	insert := func(hash, content, outcome string) {
		t.Helper()
		if _, err := st.DB.Exec(`INSERT INTO messages(project_id, content_hash, ts, role, content, source_tools, first_seen, outcome)
			VALUES ('p', ?, '2026-11-06T10:00:00Z', 'assistant', ?, '["claude"]', '2026-11-06T10:00:00Z', ?)`, hash, content, outcome); err != nil {
			t.Fatal(err)
		}
	}
	insert("success-1", "deployment needle success", "success")
	insert("neutral-1", "deployment needle neutral", "neutral")
	insert("corrected-1", "deployment needle corrected", "corrected")
	if _, err := st.DB.Exec(`INSERT INTO messages_fts(messages_fts) VALUES('rebuild')`); err != nil {
		t.Fatal(err)
	}
	result, err := RecallSolutions(st, "deployment needle", RecallOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, hit := range result.Hits {
		if hit.Kind == "transcript" {
			order = append(order, hit.Outcome)
		}
	}
	raw, err := json.MarshalIndent(order, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "outcome", "ranking.golden")
	if os.Getenv("SILA_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden (run with SILA_UPDATE_GOLDEN=1): %v", err)
	}
	if string(raw)+"\n" != string(want) {
		t.Fatal("ranking golden drifted; run with SILA_UPDATE_GOLDEN=1")
	}
}
