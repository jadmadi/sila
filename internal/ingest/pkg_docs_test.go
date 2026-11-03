package ingest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func TestDetectEcosystem(t *testing.T) {
	if eco := detectEcosystem("github.com/jadmadi/sila"); eco != "go" {
		t.Errorf("expected go, got %s", eco)
	}
	if eco := detectEcosystem("@modelcontextprotocol/sdk"); eco != "npm" {
		t.Errorf("expected npm, got %s", eco)
	}
	if eco := detectEcosystem("requests"); eco != "auto" {
		t.Errorf("expected auto, got %s", eco)
	}
}

func TestIngestPackageDoc(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir + "/test.db")
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	doc := &PackageDoc{
		Name:      "zombiezen/go-sqlite",
		Ecosystem: "go",
		Version:   "v0.13.0",
		Summary:   "Go SQLite driver using CGo-free WebAssembly",
		DocURL:    "https://pkg.go.dev/zombiezen.com/go/sqlite",
		Content:   "# Package Doc Content",
	}

	saved, err := IngestPackageDoc(st, doc, "my-proj")
	if err != nil {
		t.Fatalf("IngestPackageDoc failed: %v", err)
	}
	if saved == nil || saved.ID == "" {
		t.Errorf("expected valid saved doc, got %+v", saved)
	}
}

func TestFetchNPMMock(t *testing.T) {
	_ = httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"name":        "zod",
			"description": "TypeScript-first schema validation",
			"readme":      "# Zod Documentation",
			"license":     "MIT",
			"dist-tags": map[string]string{
				"latest": "3.23.8",
			},
			"repository": map[string]string{
				"url": "https://github.com/colinhacks/zod",
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
}
