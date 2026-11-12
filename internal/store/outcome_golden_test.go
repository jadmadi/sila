package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestOutcomeStatsGolden(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "og.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	insert := func(hash, tools, outcome string) {
		t.Helper()
		if _, err := st.DB.Exec(`INSERT INTO messages(project_id, content_hash, ts, role, content, source_tools, first_seen, outcome)
			VALUES ('p', ?, '2026-11-06T10:00:00Z', 'assistant', 'x', ?, '2026-11-06T10:00:00Z', ?)`, hash, tools, outcome); err != nil {
			t.Fatal(err)
		}
	}
	insert("h1", `["claude"]`, "success")
	insert("h2", `["claude"]`, "corrected")
	insert("h3", `["codex"]`, "neutral")
	counts, err := st.OutcomeCounts("p")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.MarshalIndent(counts, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "outcome", "stats.golden")
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
		t.Fatal("stats golden drifted; run with SILA_UPDATE_GOLDEN=1")
	}
}
