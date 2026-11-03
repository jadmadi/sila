package ingest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func TestCleanNotionTitleAndMarkdown(t *testing.T) {
	// 1. Title stripping
	filename := "Architecture Overview 1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d.md"
	cleanTitle := CleanNotionTitle(filename)
	if cleanTitle != "Architecture Overview" {
		t.Errorf("CleanNotionTitle(%q) = %q, want 'Architecture Overview'", filename, cleanTitle)
	}

	percentEncoded := "API%20Design%20Guide%201a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d.md"
	cleanEncoded := CleanNotionTitle(percentEncoded)
	if cleanEncoded != "API Design Guide" {
		t.Errorf("CleanNotionTitle(%q) = %q, want 'API Design Guide'", percentEncoded, cleanEncoded)
	}

	// 2. Markdown link normalization
	md := `# System Architecture
See the [Storage Design](Storage%20Design%201a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d.md) and [External API](https://api.example.com).
Also refer to [Worker](Worker%20Engine%200a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d.md).`

	cleaned := CleanNotionMarkdown(md)
	if !strings.Contains(cleaned, "[Storage Design](Storage Design.md)") {
		t.Errorf("expected normalized link, got:\n%s", cleaned)
	}
	if !strings.Contains(cleaned, "[Worker](Worker Engine.md)") {
		t.Errorf("expected normalized worker link, got:\n%s", cleaned)
	}
	if !strings.Contains(cleaned, "[External API](https://api.example.com)") {
		t.Errorf("external link should remain untouched, got:\n%s", cleaned)
	}
}

func TestCleanConfluenceHTML(t *testing.T) {
	rawHTML := `<!DOCTYPE html>
<html>
<head>
<title>Database Architecture - Confluence</title>
<meta name="ajs-page-title" content="Database Architecture">
<meta name="author" content="Jane Developer">
<meta name="labels" content="database, storage, sqlite">
</head>
<body>
<div id="main-content" class="wiki-content">
<h1>Overview</h1>
<p>This is the database architecture overview.</p>
<ac:structured-macro ac:name="info">
<ac:rich-text-body><p>SQLite runs in WAL mode with pure-Go driver.</p></ac:rich-text-body>
</ac:structured-macro>
<ac:structured-macro ac:name="code">
<ac:parameter ac:name="language">go</ac:parameter>
<ac:plain-text-body><![CDATA[func Open(dbPath string) (*Store, error) {
    return &Store{}, nil
}]]></ac:plain-text-body>
</ac:structured-macro>
</div>
</body>
</html>`

	title, author, labels, md := CleanConfluenceHTML(rawHTML)
	if title != "Database Architecture" {
		t.Errorf("expected title 'Database Architecture', got %q", title)
	}
	if author != "Jane Developer" {
		t.Errorf("expected author 'Jane Developer', got %q", author)
	}
	if len(labels) != 3 || labels[0] != "database" || labels[2] != "sqlite" {
		t.Errorf("expected 3 labels, got %v", labels)
	}

	if !strings.Contains(md, "> **Author:** Jane Developer | **Labels:** database, storage, sqlite") {
		t.Errorf("expected metadata header in markdown, got:\n%s", md)
	}
	if !strings.Contains(md, "> [!INFO]") || !strings.Contains(md, "SQLite runs in WAL mode") {
		t.Errorf("expected transformed info alert, got:\n%s", md)
	}
	if !strings.Contains(md, "```go\nfunc Open") {
		t.Errorf("expected code block macro conversion, got:\n%s", md)
	}
}

func TestParseFrontmatter(t *testing.T) {
	raw := `---
title: "Authentication & Authorization Guide"
description: "How user tokens and roles are validated."
tags: ["auth", "security", "jwt"]
author: "Security Team"
---

# Authentication

All requests require a bearer token.
`

	fm := ParseFrontmatter(raw)
	if fm.Title != "Authentication & Authorization Guide" {
		t.Errorf("expected title parsed from frontmatter, got %q", fm.Title)
	}
	if fm.Description != "How user tokens and roles are validated." {
		t.Errorf("expected description parsed from frontmatter, got %q", fm.Description)
	}
	if len(fm.Tags) != 3 || fm.Tags[0] != "auth" {
		t.Errorf("expected tags parsed, got %v", fm.Tags)
	}
	if !strings.Contains(fm.Body, "# Authentication") {
		t.Errorf("expected body without frontmatter, got:\n%s", fm.Body)
	}
}

func TestIngestExportDirectory(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "k.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if _, err := st.EnsureProject("test-export-proj"); err != nil {
		t.Fatal(err)
	}

	// 1. Synthetic Notion Export directory
	notionDir := filepath.Join(tmp, "notion-export")
	if err := os.Mkdir(notionDir, 0755); err != nil {
		t.Fatal(err)
	}
	notionFile1 := filepath.Join(notionDir, "API Specification 1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d.md")
	notionContent1 := "# API\nRefer to [Data Model](Data%20Model%209f8e7d6c5b4a3a2b1c0d9e8f7a6b5c4d.md)."
	if err := os.WriteFile(notionFile1, []byte(notionContent1), 0644); err != nil {
		t.Fatal(err)
	}
	notionFile2 := filepath.Join(notionDir, "Data Model 9f8e7d6c5b4a3a2b1c0d9e8f7a6b5c4d.md")
	notionContent2 := "# Data Model\nStores all models."
	if err := os.WriteFile(notionFile2, []byte(notionContent2), 0644); err != nil {
		t.Fatal(err)
	}

	res, err := IngestExport(st, notionDir, IngestExportOptions{
		Format:    FormatAuto,
		ProjectID: "test-export-proj",
	})
	if err != nil {
		t.Fatalf("IngestExport failed on Notion dir: %v", err)
	}

	if res.DetectedFormat != FormatNotion {
		t.Errorf("expected detected format 'notion', got %q", res.DetectedFormat)
	}
	if res.TotalFiles != 2 {
		t.Errorf("expected 2 files ingested, got %d", res.TotalFiles)
	}

	// Verify documents in store have cleaned titles and links
	docs, err := st.DocsByProject("test-export-proj", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 docs in store, got %d", len(docs))
	}

	var foundAPI bool
	for _, d := range docs {
		if d.Title == "API Specification" {
			foundAPI = true
			if !strings.Contains(d.Content, "[Data Model](Data Model.md)") {
				t.Errorf("expected normalized link in stored doc, got:\n%s", d.Content)
			}
		}
	}
	if !foundAPI {
		t.Errorf("expected 'API Specification' title in stored docs")
	}

	// 2. Synthetic GitBook directory
	gitbookDir := filepath.Join(tmp, "gitbook-export")
	if err := os.Mkdir(gitbookDir, 0755); err != nil {
		t.Fatal(err)
	}
	summaryFile := filepath.Join(gitbookDir, "SUMMARY.md")
	if err := os.WriteFile(summaryFile, []byte("# Summary\n* [Intro](intro.md)"), 0644); err != nil {
		t.Fatal(err)
	}
	introFile := filepath.Join(gitbookDir, "intro.md")
	introContent := `---
title: "Welcome to Documentation"
description: "Introduction and Getting Started"
---
# Welcome
Here is the welcome body.`
	if err := os.WriteFile(introFile, []byte(introContent), 0644); err != nil {
		t.Fatal(err)
	}

	resGB, err := IngestExport(st, gitbookDir, IngestExportOptions{
		Format:    FormatAuto,
		ProjectID: "test-export-proj",
	})
	if err != nil {
		t.Fatalf("IngestExport failed on GitBook dir: %v", err)
	}
	if resGB.DetectedFormat != FormatGitBook {
		t.Errorf("expected detected format 'gitbook', got %q", resGB.DetectedFormat)
	}
}
