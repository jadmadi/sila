package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestRecallCLIJSON(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "recall_cli.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	proj, err := st.EnsureProject(tmp)
	if err != nil {
		st.Close()
		t.Fatal(err)
	}
	if _, err := st.InsertLesson(proj.ID, store.FormatLesson("xlsx export hangs", "shared strings", "stream rows", "xlsx"), "agent", tmp, nil); err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runRecall(ui, dbPath, proj.ID, 5, false, true, false, []string{"xlsx", "export", "hangs"})
	})

	var res struct {
		Query string `json:"query"`
		Total int    `json:"total"`
		Hits  []struct {
			Kind      string `json:"kind"`
			Grounded  bool   `json:"grounded"`
			SourceRef string `json:"source_ref"`
		} `json:"hits"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("recall output is not JSON: %q: %v", out, err)
	}
	if res.Total != 1 || len(res.Hits) != 1 {
		t.Fatalf("recall total = %d, hits = %d, want 1", res.Total, len(res.Hits))
	}
	if res.Hits[0].Kind != "lesson" {
		t.Fatalf("hit kind = %q, want lesson", res.Hits[0].Kind)
	}
}
