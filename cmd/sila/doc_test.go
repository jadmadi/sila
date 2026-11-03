package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestClassifyDocInput(t *testing.T) {
	tmp := t.TempDir()
	existingFile := filepath.Join(tmp, "existing.md")
	if err := os.WriteFile(existingFile, []byte("# Existing"), 0644); err != nil {
		t.Fatal(err)
	}
	existingDir := filepath.Join(tmp, "subdir")
	if err := os.Mkdir(existingDir, 0755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		input string
		want  DocInputKind
	}{
		// Existing files and dirs
		{existingFile, DocInputFileOrDir},
		{existingDir, DocInputFileOrDir},

		// Explicit URLs
		{"https://golang.org/doc", DocInputURL},
		{"http://example.com/api", DocInputURL},
		{"HTTPS://REACT.DEV", DocInputURL},

		// Bare domains
		{"golang.org", DocInputBareDomain},
		{"docs.stripe.com", DocInputBareDomain},
		{"docs.stripe.com/api/v1", DocInputBareDomain},
		{"pkg.go.dev/net/http", DocInputBareDomain},
		{"localhost:8080/docs", DocInputBareDomain},

		// Explicit paths that are missing
		{"./missing.md", DocInputExplicitPathMissing},
		{"../missing/file.txt", DocInputExplicitPathMissing},
		{"/nonexistent/doc.html", DocInputExplicitPathMissing},
		{"missing.md", DocInputExplicitPathMissing},
		{"notes.markdown", DocInputExplicitPathMissing},
		{"guide.pdf", DocInputExplicitPathMissing},
		{"path/to/missing", DocInputExplicitPathMissing},

		// Search queries and topic terms
		{"google", DocInputSearchQuery},
		{"react", DocInputSearchQuery},
		{"react hooks", DocInputSearchQuery},
		{"stripe webhook integration", DocInputSearchQuery},
		{"sqlite-fts5", DocInputSearchQuery},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := classifyDocInput(tt.input)
			if got != tt.want {
				t.Errorf("classifyDocInput(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestRunDocDashboard(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "k.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnsureProject("test-dashboard-proj"); err != nil {
		t.Fatal(err)
	}

	doc := store.Doc{
		ProjectID: "test-dashboard-proj",
		Title:     "Architecture Overview",
		URL:       "https://example.com/arch",
		Content:   "Architecture guide for test project.",
		Format:    "markdown",
	}
	if err := st.InsertDoc(doc); err != nil {
		t.Fatal(err)
	}
	st.Close()

	ui := render.New(os.Stdout, true)

	// Test Plain Dashboard
	plainOut := captureStdout(t, func() {
		runDoc(ui, dbPath, "test-dashboard-proj", "", "", "", "", "", "", false, 0, 0, 0, false, 0, "", false, false, false, "auto", []string{})
	})

	if !strings.Contains(plainOut, "sila · doc") {
		t.Fatalf("expected banner in dashboard, got: %s", plainOut)
	}
	if !strings.Contains(plainOut, "Architecture Overview") {
		t.Fatalf("expected indexed doc in dashboard, got: %s", plainOut)
	}
	if !strings.Contains(plainOut, "Commands & Usage") {
		t.Fatalf("expected commands section in dashboard, got: %s", plainOut)
	}

	// Test JSON Dashboard
	jsonOut := captureStdout(t, func() {
		runDoc(ui, dbPath, "test-dashboard-proj", "", "", "", "", "", "", false, 0, 0, 0, false, 0, "", false, true, false, "auto", []string{})
	})

	var result map[string]any
	if err := json.Unmarshal([]byte(jsonOut), &result); err != nil {
		t.Fatalf("failed to parse dashboard JSON: %v, raw:\n%s", err, jsonOut)
	}
	if result["project"] != "test-dashboard-proj" {
		t.Errorf("expected project test-dashboard-proj, got %v", result["project"])
	}
	if total, ok := result["total_docs"].(float64); !ok || total != 1 {
		t.Errorf("expected total_docs == 1, got %v", result["total_docs"])
	}
}

func TestRunDocSearchQueryRouting(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "k.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnsureProject("test-query-proj"); err != nil {
		t.Fatal(err)
	}

	doc := store.Doc{
		ProjectID: "test-query-proj",
		Title:     "React Router Documentation",
		URL:       "https://reactrouter.com",
		Content:   "React Router is a lightweight library for routing in React apps.",
		Format:    "markdown",
	}
	if err := st.InsertDoc(doc); err != nil {
		t.Fatal(err)
	}
	st.Close()

	ui := render.New(os.Stdout, true)

	// Hit search routing
	hitOut := captureStdout(t, func() {
		runDoc(ui, dbPath, "test-query-proj", "", "", "", "", "", "", false, 0, 0, 0, false, 0, "", false, false, false, "auto", []string{"react", "router"})
	})

	if !strings.Contains(hitOut, "React Router Documentation") {
		t.Fatalf("expected search match in output, got: %s", hitOut)
	}

	// Miss search routing with suggestion
	missOut := captureStdout(t, func() {
		runDoc(ui, dbPath, "test-query-proj", "", "", "", "", "", "", false, 0, 0, 0, false, 0, "", false, false, false, "auto", []string{"kubernetes"})
	})

	if !strings.Contains(missOut, "No indexed documents match") {
		t.Fatalf("expected miss message, got: %s", missOut)
	}
	if !strings.Contains(missOut, "sila doc web-search") {
		t.Fatalf("expected web-search suggestion, got: %s", missOut)
	}
}

func TestRunDocExplicitMissingPath(t *testing.T) {
	ui := render.New(os.Stdout, true)
	origExit := docExitFunc
	defer func() { docExitFunc = origExit }()

	var exitedCode int
	docExitFunc = func(code int) {
		exitedCode = code
	}

	errOut := captureStderr(t, func() {
		runDoc(ui, "nonexistent.db", "p", "", "", "", "", "", "", false, 0, 0, 0, false, 0, "", false, false, false, "auto", []string{"./nonexistent.md"})
	})

	if exitedCode != 1 {
		t.Errorf("expected exit code 1, got %d", exitedCode)
	}
	if !strings.Contains(errOut, "file or directory not found") {
		t.Fatalf("expected file or directory not found error, got: %s", errOut)
	}
	if !strings.Contains(errOut, "sila doc search") {
		t.Fatalf("expected search tip, got: %s", errOut)
	}
}

func TestRunDocIngestSubcommandAndDirectory(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "k.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	// Create synthetic Notion directory
	notionDir := filepath.Join(tmp, "wiki-export")
	if err := os.Mkdir(notionDir, 0755); err != nil {
		t.Fatal(err)
	}
	f1 := filepath.Join(notionDir, "Architecture Overview 1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d.md")
	if err := os.WriteFile(f1, []byte("# Architecture\nDetails."), 0644); err != nil {
		t.Fatal(err)
	}

	ui := render.New(os.Stdout, true)

	// Test direct directory ingestion
	dirOut := captureStdout(t, func() {
		runDoc(ui, dbPath, "test-dir-proj", "", "", "", "", "", "", false, 0, 0, 0, false, 0, "", false, false, false, "auto", []string{notionDir})
	})

	if !strings.Contains(dirOut, "Ingested and indexed 1 documents from notion export") {
		t.Fatalf("expected notion directory ingestion confirmation, got:\n%s", dirOut)
	}

	// Test explicit `sila doc ingest <path> --format=notion`
	f2 := filepath.Join(tmp, "Manual Guide 9f8e7d6c5b4a3a2b1c0d9e8f7a6b5c4d.md")
	if err := os.WriteFile(f2, []byte("# Guide\nManual content."), 0644); err != nil {
		t.Fatal(err)
	}

	ingestOut := captureStdout(t, func() {
		runDoc(ui, dbPath, "test-dir-proj", "", "", "", "", "", "", false, 0, 0, 0, false, 0, "", false, false, false, "notion", []string{"ingest", f2})
	})

	if !strings.Contains(ingestOut, "Ingested and indexed 1 documents from notion export") {
		t.Fatalf("expected ingest subcommand confirmation, got:\n%s", ingestOut)
	}
}
