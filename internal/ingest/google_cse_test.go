package ingest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func TestSearchGoogleCSEWithMockServer(t *testing.T) {
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/doc1" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, "<html><head><title>FTS5 Doc</title></head><body><h1>FTS5 Tokenizer</h1><p>Content on tokenizers.</p></body></html>")
			return
		}

		resp := googleCSEResponse{
			Items: []struct {
				Title   string `json:"title"`
				Link    string `json:"link"`
				Snippet string `json:"snippet"`
			}{
				{
					Title:   "SQLite FTS5 Manual",
					Link:    ts.URL + "/doc1",
					Snippet: "Full text search with FTS5",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	// Verify IngestSearchResults
	results := []SearchResult{
		{
			Title:   "SQLite FTS5 Manual",
			Link:    ts.URL + "/doc1",
			Snippet: "Full text search with FTS5",
		},
	}

	tmpDir := t.TempDir()
	st, err := store.Open(filepath.Join(tmpDir, "cse.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	docs, err := IngestSearchResults(st, results, "test-proj")
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc ingested, got %d", len(docs))
	}
	if docs[0].Title != "FTS5 Doc" {
		t.Fatalf("expected title 'FTS5 Doc', got %q", docs[0].Title)
	}
}
