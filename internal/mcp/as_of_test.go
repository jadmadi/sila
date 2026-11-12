package mcp

import (
	"strings"
	"testing"
	"time"
)

func seedAsOfData(t *testing.T, srv *Server, projectID string) {
	t.Helper()
	created := "2026-01-01T00:00:00Z"
	if _, err := srv.store.DB.Exec(`INSERT INTO facts(id, project_id, claim, confidence, repo_files, tools, session_ref, first_seen, promoted_at, review_status)
		VALUES ('asof-old', ?, 'historical claim', 'grounded', '[]', '[]', '', ?, ?, 'approved')`, projectID, created, created); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.DB.Exec(`INSERT INTO memory_records(id, project_id, fact_id, kind, title, summary, digest, envelope_json, state, created_at, updated_at)
		VALUES ('asof-memo', ?, 'f', 'memo', 't', 'historical memo', 'd', '{}', 'current', ?, ?)`, projectID, created, created); err != nil {
		t.Fatal(err)
	}
}

func TestRecallAsOf(t *testing.T) {
	srv, _, projectID := revisionServer(t)
	seedAsOfData(t, srv, projectID)
	response := callTool(t, srv, "recall", map[string]any{"query": "historical", "project": projectID, "as_of": "2026-03-01"})
	if _, ok := response["error"]; ok {
		t.Fatalf("recall error = %+v", response)
	}
	text := response["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "Historical recall") || !strings.Contains(text, "as of 2026-03-01T00:00:00Z") {
		t.Fatalf("text = %s", text)
	}
}

func TestAsOfRejectsGarbage(t *testing.T) {
	srv, _, projectID := revisionServer(t)
	response := callTool(t, srv, "recall", map[string]any{"query": "x", "project": projectID, "as_of": "not-a-date"})
	errObj, ok := response["error"].(map[string]any)
	if !ok || errObj["code"].(float64) != -32602 {
		t.Fatalf("response = %+v", response)
	}
	data, _ := errObj["data"].(string)
	if !strings.Contains(data, "as_of must be") {
		t.Fatalf("data = %v", errObj["data"])
	}
}

func TestFactsAsOfSurfaces(t *testing.T) {
	srv, _, projectID := revisionServer(t)
	seedAsOfData(t, srv, projectID)
	for _, tool := range []string{"search_knowledge", "get_project_facts"} {
		response := callTool(t, srv, tool, map[string]any{"query": "historical", "project": projectID, "as_of": "2026-03-01"})
		text := response["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
		if !strings.Contains(text, `"historical": true`) {
			t.Fatalf("%s text = %s", tool, text)
		}
	}
	_ = time.Now
}
