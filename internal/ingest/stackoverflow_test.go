package ingest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jadmadi/sila/internal/config"
	"github.com/jadmadi/sila/internal/store"
)

func TestSearchStackOverflowMock(t *testing.T) {
	_ = httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"items": []map[string]any{
				{
					"question_id": 123456,
					"title":       "How to enable WAL mode in SQLite using Go?",
					"link":        "https://stackoverflow.com/questions/123456",
					"score":       42,
					"is_answered": true,
					"tags":        []string{"go", "sqlite"},
					"body":        "<p>How do I execute <code>PRAGMA journal_mode=WAL;</code>?</p>",
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))

	dir := t.TempDir()
	st, err := store.Open(dir + "/test.db")
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	results := []StackOverflowResult{
		{
			QuestionID: 123456,
			Title:      "How to enable WAL mode in SQLite using Go?",
			Link:       "https://stackoverflow.com/questions/123456",
			Score:      42,
			IsAnswered: true,
			Tags:       []string{"go", "sqlite"},
			Body:       "How do I execute PRAGMA journal_mode=WAL;?",
		},
	}

	docs, err := IngestStackOverflowResults(st, results, "test-proj")
	if err != nil {
		t.Fatalf("IngestStackOverflowResults failed: %v", err)
	}
	if len(docs) != 1 {
		t.Errorf("expected 1 doc, got %d", len(docs))
	}
}

func TestSearchStackOverflowEmpty(t *testing.T) {
	_, err := SearchStackOverflow("", "", 5, &config.Config{})
	if err == nil {
		t.Error("expected error on empty query")
	}
}
