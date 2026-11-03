package ingest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jadmadi/sila/internal/config"
	"github.com/jadmadi/sila/internal/store"
)

func TestSearchGitHubCodeMock(t *testing.T) {
	mockSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"items": []map[string]any{
				{
					"name":     "sqlite.go",
					"path":     "internal/db/sqlite.go",
					"html_url": "https://github.com/example/repo/blob/main/internal/db/sqlite.go",
					"repository": map[string]any{
						"full_name": "example/repo",
						"html_url":  "https://github.com/example/repo",
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer mockSrv.Close()

	// Ingest test into temporary store
	dir := t.TempDir()
	st, err := store.Open(dir + "/test.db")
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	results := []CodeSearchResult{
		{
			Repo:     "example/repo",
			Path:     "internal/db/sqlite.go",
			HTMLURL:  "https://github.com/example/repo/blob/main/internal/db/sqlite.go",
			RawURL:   mockSrv.URL + "/raw",
			Language: "go",
		},
	}

	docs, err := IngestCodeSearchResults(st, results, "test-proj")
	if err != nil {
		t.Fatalf("IngestCodeSearchResults failed: %v", err)
	}
	if len(docs) != 1 {
		t.Errorf("expected 1 doc, got %d", len(docs))
	}
}

func TestSearchCodeEmptyQuery(t *testing.T) {
	cfg := &config.Config{}
	_, _, err := SearchCode(CodeSearchQuery{Query: ""}, cfg)
	if err == nil {
		t.Error("expected error for empty query")
	}
}

func TestSearchCodeQueryParameters(t *testing.T) {
	q := CodeSearchQuery{
		Query:     "FTS5Tokenizer",
		Language:  "go",
		Repo:      "mattn/go-sqlite3",
		Extension: ".go",
		Top:       3,
	}
	if q.Language != "go" || q.Repo != "mattn/go-sqlite3" {
		t.Errorf("unexpected query fields: %+v", q)
	}
}
