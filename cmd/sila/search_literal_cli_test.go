package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestSearchLiteralCLI(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "search_lit.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	doc := store.Doc{
		ID:        "doc_lit_sample",
		ProjectID: "test_proj",
		Title:     "Gobstopper Pattern",
		Content:   "Gobstopper byte-bounding pattern extracts exact strings directly.",
	}
	if err := st.InsertDoc(doc); err != nil {
		t.Fatalf("InsertDoc failed: %v", err)
	}
	st.Close()

	ui := render.New(os.Stdout, true)
	terms := []string{"literal", "doc_lit_sample", "0", "10", "doc"}

	out := captureStdout(t, func() {
		runSearch(ui, dbPath, "test_proj", "", "", time.Time{}, false, false, false, 0, 0, false, false, false, true, false, false, 10, terms)
	})

	var res map[string]any
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("unmarshal literal search result failed: %v, raw: %s", err, out)
	}

	if res["literal"] != "Gobstopper" {
		t.Fatalf("expected 'Gobstopper', got %v", res["literal"])
	}
	if res["byte_start"].(float64) != 0 || res["byte_end"].(float64) != 10 {
		t.Fatalf("unexpected bounds: start=%v, end=%v", res["byte_start"], res["byte_end"])
	}
}
