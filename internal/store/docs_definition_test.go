package store

import (
	"path/filepath"
	"testing"
)

func TestSearchDocsFTSPrefersDefinitions(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "docs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	proj, err := st.EnsureProject(filepath.Join(dir, "proj"))
	if err != nil {
		t.Fatal(err)
	}
	// The prose doc is inserted first so a pure bm25 or insertion order would
	// surface it before the definition doc.
	prose := Doc{
		ID:        "doc-prose",
		ProjectID: proj.ID,
		Title:     "Tokenizer notes",
		Content:   "We talked about the tokenizer refresh and how the tokenizer refresh should work in practice.",
		CreatedAt: "2026-09-01T00:00:00Z",
	}
	def := Doc{
		ID:        "doc-def",
		ProjectID: proj.ID,
		Title:     "Tokenizer API",
		Content:   "# API\n\ntype Tokenizer struct {\n\trefresh time.Duration\n}\n\ndef reshape_tokens(tokens []string) []string:",
		CreatedAt: "2026-09-02T00:00:00Z",
	}
	for _, d := range []Doc{prose, def} {
		if err := st.InsertDoc(d); err != nil {
			t.Fatal(err)
		}
	}

	results, err := st.SearchDocsFTS("tokenizer refresh", proj.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 2 {
		t.Fatalf("results = %d, want both docs", len(results))
	}
	if results[0].ID != "doc-def" {
		t.Fatalf("first doc = %q, want the definition-shaped document first", results[0].ID)
	}
}
