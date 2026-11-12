package store

import (
	"path/filepath"
	"testing"
)

func TestWriteRunLabelsOutcomes(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "ol.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	messages := []LoadMessage{
		{ProjectID: "p", ContentHash: "assistant-1", TS: "2026-11-06T10:00:00Z", Role: "assistant", Content: "done", Tool: "claude", SessionID: "s1", SourceOrder: 1},
		{ProjectID: "p", ContentHash: "user-1", TS: "2026-11-06T10:05:00Z", Role: "user", Content: "no wait, that broke it", Tool: "claude", SessionID: "s1", SourceOrder: 2},
		{ProjectID: "p", ContentHash: "assistant-2", TS: "2026-11-06T10:06:00Z", Role: "assistant", Content: "fixed", Tool: "claude", SessionID: "s1", SourceOrder: 3},
		{ProjectID: "p", ContentHash: "user-2", TS: "2026-11-06T10:07:00Z", Role: "user", Content: "Perfect, ship it", Tool: "claude", SessionID: "s1", SourceOrder: 4},
	}
	if _, _, err := st.WriteRun("run1", "claude", "", messages); err != nil {
		t.Fatal(err)
	}
	outcomes := map[string]string{}
	rows, err := st.DB.Query(`SELECT content_hash, outcome FROM messages WHERE project_id = 'p'`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var hash, outcome string
		if err := rows.Scan(&hash, &outcome); err != nil {
			t.Fatal(err)
		}
		outcomes[hash] = outcome
	}
	rows.Close()
	if outcomes["assistant-1"] != "corrected" || outcomes["assistant-2"] != "success" {
		t.Fatalf("outcomes = %+v", outcomes)
	}
	if _, _, err := st.WriteRun("run1", "claude", "", messages); err != nil {
		t.Fatal(err)
	}
	var corrected int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM messages WHERE content_hash = 'assistant-1' AND outcome = 'corrected'`).Scan(&corrected); err != nil || corrected != 1 {
		t.Fatalf("re-ingest changed labels: %d err=%v", corrected, err)
	}
}

func TestOutcomeCountsByTool(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "oc.db"))
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
	insert("h3", `["claude","codex"]`, "neutral")
	counts, err := st.OutcomeCounts("p")
	if err != nil || len(counts) != 2 {
		t.Fatalf("counts = %+v err=%v", counts, err)
	}
	if counts[0].Tool != "claude" || counts[0].Total != 3 || counts[0].Success != 1 || counts[0].Corrected != 1 || counts[0].Neutral != 1 {
		t.Fatalf("claude = %+v", counts[0])
	}
	if counts[1].Tool != "codex" || counts[1].Total != 1 {
		t.Fatalf("codex = %+v", counts[1])
	}
}
