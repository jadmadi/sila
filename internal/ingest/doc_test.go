package ingest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func TestHTMLToMarkdownConversion(t *testing.T) {
	rawHTML := `
<!DOCTYPE html>
<html>
<head>
    <title>Modern SQLite Guide - Docs</title>
    <script>console.log("ignore");</script>
    <style>.nav { color: red; }</style>
</head>
<body>
    <header><nav><a href="/home">Home</a></nav></header>
    <h1>Modern Pure-Go SQLite</h1>
    <p>Using <code>modernc.org/sqlite</code> allows building CGO-free binaries.</p>
    <h2>Installation</h2>
    <pre><code>go get modernc.org/sqlite</code></pre>
    <ul>
        <li>Pure Go</li>
        <li>Zero C compiler needed</li>
    </ul>
    <p>Check the <a href="https://modernc.org/sqlite">documentation</a> for details.</p>
    <footer>Copyright 2026</footer>
</body>
</html>`

	title, md := HTMLToMarkdown(rawHTML)
	if title != "Modern SQLite Guide - Docs" {
		t.Fatalf("unexpected title: %q", title)
	}

	if !strings.Contains(md, "# Modern Pure-Go SQLite") {
		t.Fatalf("missing H1 in markdown: %s", md)
	}
	if !strings.Contains(md, "Using `modernc.org/sqlite` allows building CGO-free binaries.") {
		t.Fatalf("missing paragraph in markdown: %s", md)
	}
	if !strings.Contains(md, "```\ngo get modernc.org/sqlite\n```") {
		t.Fatalf("missing pre/code block in markdown: %s", md)
	}
	if !strings.Contains(md, "- Pure Go") {
		t.Fatalf("missing list item in markdown: %s", md)
	}
	if !strings.Contains(md, "[documentation](https://modernc.org/sqlite)") {
		t.Fatalf("missing link in markdown: %s", md)
	}
	if strings.Contains(md, "console.log") || strings.Contains(md, "Copyright 2026") {
		t.Fatalf("script/footer not stripped from markdown: %s", md)
	}
}

func TestIngestLocalFile(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "k.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if _, err := st.EnsureProject("/tmp/proj"); err != nil {
		t.Fatal(err)
	}

	docPath := filepath.Join(tmp, "architecture.md")
	if err := os.WriteFile(docPath, []byte("# Architecture\nPure Go SQLite architecture."), 0o644); err != nil {
		t.Fatal(err)
	}

	doc, err := IngestDoc(st, docPath, "proj", "Architecture Spec")
	if err != nil {
		t.Fatalf("IngestDoc failed: %v", err)
	}
	if doc.Title != "Architecture Spec" {
		t.Fatalf("unexpected title: %s", doc.Title)
	}
	if !strings.Contains(doc.Content, "Pure Go SQLite architecture") {
		t.Fatalf("unexpected content: %s", doc.Content)
	}
}
