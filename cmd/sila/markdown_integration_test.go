package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestDocGetMarkdownRendering(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	doc := store.Doc{
		ID:        "doc-arch",
		ProjectID: "test-proj",
		Title:     "Architecture Guide",
		URL:       "https://example.com/guide",
		Format:    "markdown",
		Content:   "# Header 1\n\nThis is **bold** text and `inline_code`.\n\n```go\nvar x = 10\n```",
	}
	if err := st.InsertDoc(doc); err != nil {
		t.Fatal(err)
	}

	ui := render.New(os.Stdout, true) // noColor = true (plain output)
	outText := captureStdout(t, func() {
		runDoc(ui, dbPath, "test-proj", "", "", "", "", "", "", false, 0, 0, 0, false, 0, "", false, false, false, "auto", []string{"get", doc.ID})
	})

	if !strings.Contains(outText, "HEADER 1") {
		t.Errorf("expected formatted header, got:\n%s", outText)
	}
	if !strings.Contains(outText, "bold") {
		t.Errorf("expected text content, got:\n%s", outText)
	}
	if !strings.Contains(outText, "var x = 10") {
		t.Errorf("expected code block content, got:\n%s", outText)
	}
}
