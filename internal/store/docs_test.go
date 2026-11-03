package store

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDocsCRUDAndFTS(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "k.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if _, err := st.EnsureProject("/tmp/my-proj"); err != nil {
		t.Fatal(err)
	}

	doc := Doc{
		ProjectID: "my-proj",
		Title:     "SQLite FTS5 Architecture Guide",
		URL:       "https://sqlite.org/fts5.html",
		Content:   "FTS5 is an SQLite virtual table module that provides full-text search capability.",
		Format:    "markdown",
	}

	if err := st.InsertDoc(doc); err != nil {
		t.Fatalf("InsertDoc failed: %v", err)
	}

	docs, err := st.DocsByProject("my-proj", 10)
	if err != nil {
		t.Fatalf("DocsByProject failed: %v", err)
	}
	if len(docs) != 1 || docs[0].Title != doc.Title {
		t.Fatalf("unexpected docs: %+v", docs)
	}

	// Search via FTS5
	searchResults, err := st.SearchDocsFTS("full-text search", "my-proj", 5)
	if err != nil {
		t.Fatalf("SearchDocsFTS failed: %v", err)
	}
	if len(searchResults) != 1 || searchResults[0].ID != docs[0].ID {
		t.Fatalf("expected 1 search result, got %+v", searchResults)
	}

	// Get by ID
	single, err := st.GetDoc(docs[0].ID)
	if err != nil {
		t.Fatalf("GetDoc failed: %v", err)
	}
	if single == nil || single.URL != doc.URL {
		t.Fatalf("unexpected single doc: %+v", single)
	}

	// DocStats
	count, totalBytes, err := st.DocStats("my-proj")
	if err != nil {
		t.Fatalf("DocStats failed: %v", err)
	}
	if count != 1 || totalBytes != int64(len(doc.Content)) {
		t.Fatalf("DocStats mismatch: count=%d (want 1), bytes=%d (want %d)", count, totalBytes, len(doc.Content))
	}
	allCount, allBytes, err := st.DocStats("")
	if err != nil {
		t.Fatalf("DocStats global failed: %v", err)
	}
	if allCount != 1 || allBytes != int64(len(doc.Content)) {
		t.Fatalf("DocStats global mismatch: count=%d, bytes=%d", allCount, allBytes)
	}
}

func TestInsertDoc_ProjectScoping_And_Bounds(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "k.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// 1. Missing project_id must fail
	err = st.InsertDoc(Doc{
		Title:   "No Project",
		Content: "Valid documentation content without a project scoping identifier.",
	})
	if err == nil {
		t.Fatal("expected error when inserting doc without project_id, got nil")
	}

	// 2. Oversized content (>1MB) must fail
	hugeContent := strings.Repeat("A", MaxDocContentSize+10)
	err = st.InsertDoc(Doc{
		ProjectID: "test-proj",
		Title:     "Oversized Doc",
		Content:   hugeContent,
	})
	if err == nil {
		t.Fatal("expected error when inserting oversized doc, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds maximum allowed size") {
		t.Errorf("expected error message to mention size limit, got: %v", err)
	}
}
