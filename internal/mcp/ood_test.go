package mcp

import (
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func seedOODFact(t *testing.T, st *store.Store, projectID string) {
	t.Helper()
	if err := st.UpsertFact(store.Fact{
		ID: "ood1", ProjectID: projectID, Kind: "invariant",
		Claim:        "alpha beta gamma deployment pipeline stays local",
		FirstSeen:    "2026-11-06T10:00:00Z",
		ReviewStatus: "approved",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRecallOutOfDomain(t *testing.T) {
	srv, st, projectID := revisionServer(t)
	seedOODFact(t, st, projectID)
	store.ResetDocumentFrequencyCache()
	response := callTool(t, srv, "recall", map[string]any{"query": "quantum flux capacitor", "project": projectID})
	text := response["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if !contains(text, "out_of_domain") {
		t.Fatalf("text = %s", text)
	}
}

func TestRecallOODDisabledByEnv(t *testing.T) {
	srv, st, projectID := revisionServer(t)
	seedOODFact(t, st, projectID)
	store.ResetDocumentFrequencyCache()
	t.Setenv("SILA_OOD_GUARD", "off")
	response := callTool(t, srv, "recall", map[string]any{"query": "quantum flux capacitor", "project": projectID})
	text := response["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if contains(text, "out_of_domain") {
		t.Fatalf("guard stayed on: %s", text)
	}
}

func contains(text, needle string) bool {
	return len(text) >= len(needle) && (text == needle || len(needle) == 0 || indexOf(text, needle) >= 0)
}

func indexOf(text, needle string) int {
	for i := 0; i+len(needle) <= len(text); i++ {
		if text[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

func TestOmniAndSearchKnowledgeOutOfDomain(t *testing.T) {
	srv, st, projectID := revisionServer(t)
	seedOODFact(t, st, projectID)
	store.ResetDocumentFrequencyCache()
	for _, tool := range []string{"omni_search", "search_knowledge"} {
		response := callTool(t, srv, tool, map[string]any{"query": "quantum flux capacitor", "project": projectID})
		text := response["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
		if !contains(text, "out_of_domain") {
			t.Fatalf("%s text = %s", tool, text)
		}
	}
}
