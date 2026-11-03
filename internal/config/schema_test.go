package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSchemaDefinitions(t *testing.T) {
	keys := AllKeys()
	if len(keys) == 0 {
		t.Fatal("expected non-empty key definitions")
	}

	for _, k := range keys {
		if k.Key == "" {
			t.Errorf("empty key name found")
		}
		if k.Type == "" {
			t.Errorf("key %s missing type", k.Key)
		}
		if k.Description == "" {
			t.Errorf("key %s missing description", k.Key)
		}
		if k.Security == "" {
			t.Errorf("key %s missing security note", k.Key)
		}
	}

	// Test ExplainKey with exact and alias matches
	doc, ok := ExplainKey("search_provider")
	if !ok || doc.Key != "search_provider" {
		t.Errorf("failed to lookup search_provider")
	}

	doc, ok = ExplainKey("brave")
	if !ok || doc.Key != "brave_api_key" {
		t.Errorf("failed to lookup brave alias")
	}

	doc, ok = ExplainKey("assist_scope")
	if !ok || doc.Key != "assist_scopes" {
		t.Errorf("failed to lookup assist_scope alias")
	}

	_, ok = ExplainKey("nonexistent_key_xyz")
	if ok {
		t.Errorf("expected failure for nonexistent key")
	}

	// Test JSONSchema validity
	js := JSONSchema()
	if js["$schema"] != "http://json-schema.org/draft-07/schema#" {
		t.Errorf("unexpected $schema: %v", js["$schema"])
	}
	props, ok := js["properties"].(map[string]any)
	if !ok || len(props) == 0 {
		t.Fatalf("expected valid properties map")
	}
	if _, ok := props["search_provider"]; !ok {
		t.Errorf("expected search_provider in JSONSchema properties")
	}

	// Verify JSON serialization of schema
	b, err := json.Marshal(js)
	if err != nil {
		t.Fatalf("failed to marshal JSONSchema: %v", err)
	}
	if len(b) < 100 {
		t.Errorf("schema unexpectedly small: %d bytes", len(b))
	}
}

func TestProjectConfigTraversalAndMerge(t *testing.T) {
	tmp := t.TempDir()

	// Setup hierarchy: tmp/repo/.git and tmp/repo/sub/dir
	repoDir := filepath.Join(tmp, "repo")
	subDir := filepath.Join(repoDir, "sub", "dir")
	if err := os.MkdirAll(subDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0700); err != nil {
		t.Fatal(err)
	}

	// Initially no .sila/config.json
	if found := FindProjectConfig(subDir); found != "" {
		t.Errorf("expected no project config, got %s", found)
	}

	// Create project config at repo root
	silaDir := filepath.Join(repoDir, ".sila")
	if err := os.MkdirAll(silaDir, 0700); err != nil {
		t.Fatal(err)
	}
	projConfigFile := filepath.Join(silaDir, "config.json")
	if err := os.WriteFile(projConfigFile, []byte(`{"search_provider":"tavily","doc_sites":["https://example.com/*"]}`), 0600); err != nil {
		t.Fatal(err)
	}

	// Traversal from subDir should find repo/.sila/config.json
	found := FindProjectConfig(subDir)
	if found != projConfigFile {
		t.Errorf("expected %s, got %s", projConfigFile, found)
	}

	// Test Merge
	base := &Config{
		SearchProvider: "duckduckgo",
		BraveAPIKey:    "base-brave-key",
		DocSites:       []string{"https://base.org/*"},
	}
	override := &Config{
		SearchProvider: "tavily",
		DocSites:       []string{"https://example.com/*", "https://base.org/*"},
	}

	Merge(base, override)

	if base.SearchProvider != "tavily" {
		t.Errorf("expected search_provider to be overridden to tavily, got %s", base.SearchProvider)
	}
	if base.BraveAPIKey != "base-brave-key" {
		t.Errorf("expected BraveAPIKey preserved, got %s", base.BraveAPIKey)
	}
	if len(base.DocSites) != 2 {
		t.Errorf("expected 2 unique doc sites after merge, got %d: %v", len(base.DocSites), base.DocSites)
	}
}

func TestInitConfig(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	// 1. Initialize global config
	globalPath, created, err := InitConfig("", false)
	if err != nil {
		t.Fatalf("InitConfig global failed: %v", err)
	}
	if !created {
		t.Fatalf("expected global config to be created")
	}
	if globalPath != filepath.Join(tmp, "sila", "config.json") {
		t.Errorf("unexpected global path: %s", globalPath)
	}

	// Calling again should report already exists without error
	_, createdAgain, err := InitConfig("", false)
	if err != nil {
		t.Fatalf("InitConfig second run failed: %v", err)
	}
	if createdAgain {
		t.Fatalf("expected created=false on subsequent run")
	}

	// 2. Initialize project config
	projDir := filepath.Join(tmp, "myproject")
	if err := os.MkdirAll(projDir, 0700); err != nil {
		t.Fatal(err)
	}
	projPath := filepath.Join(projDir, ".sila", "config.json")
	pPath, createdProj, err := InitConfig(projPath, true)
	if err != nil {
		t.Fatalf("InitConfig project failed: %v", err)
	}
	if !createdProj {
		t.Fatalf("expected project config created")
	}
	if pPath != projPath {
		t.Errorf("unexpected project path: %s", pPath)
	}

	// Verify valid json written
	data, err := os.ReadFile(pPath)
	if err != nil {
		t.Fatal(err)
	}
	var parsed Config
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to parse created project config: %v", err)
	}
	if parsed.SearchProvider != "duckduckgo" {
		t.Errorf("expected default search_provider duckduckgo, got %s", parsed.SearchProvider)
	}
}
