package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/config"
)

func TestMalformedConfigHandling(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")

	// 1. Write malformed JSON
	malformedContent := []byte(`{ "tavily_api_key": "some-key", "broken_json": `)
	if err := os.WriteFile(cfgPath, malformedContent, 0600); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	// 2. Load must return error, not silently ignore
	cfg, err := config.Load(cfgPath)
	if err == nil {
		t.Fatalf("expected error loading malformed config, got nil cfg=%+v", cfg)
	}

	// 3. SetKey must reject modifying malformed configuration without overwriting it
	err = config.SetKey(cfgPath, "tavily_api_key", "new-key")
	if err == nil {
		t.Fatalf("expected SetKey to reject malformed config, got nil")
	}

	// Verify original file content was preserved
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("failed to read back config: %v", err)
	}
	if string(data) != string(malformedContent) {
		t.Fatalf("file content was overwritten despite malformed JSON: %q", string(data))
	}
}

func TestAbsentConfig(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "does-not-exist.json")

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("expected no error loading non-existent config, got: %v", err)
	}
	if cfg.AssistEnabled {
		t.Errorf("expected AssistEnabled to default to false")
	}
}

func TestRedactedSecrets(t *testing.T) {
	cfg := &config.Config{
		GoogleAPIKey:       "AIzaSyD1234567890",
		TavilyAPIKey:       "tvly-secretkey123",
		BraveAPIKey:        "BSA1234567890",
		KagiAPIKey:         "kagi_sec_9999",
		GitHubToken:        "ghp_1234567890abcdef",
		SourcegraphToken:   "sgp_1234567890",
		StackOverflowKey:   "so_sec_4321",
		TypeSafeAPIKey:     "jev_test_key_abcd",
		CloudflareAPIToken: "cftok_1234567890",
		AssistEnabled:      true,
		AssistScopes:       []string{"recap"},
	}

	red := cfg.Redacted()

	if red.GoogleAPIKey == cfg.GoogleAPIKey || red.GoogleAPIKey != "••••••••7890" {
		t.Errorf("expected GoogleAPIKey to be redacted, got %s", red.GoogleAPIKey)
	}
	if red.TypeSafeAPIKey == cfg.TypeSafeAPIKey || red.TypeSafeAPIKey != "••••••••abcd" {
		t.Errorf("expected TypeSafeAPIKey to be redacted, got %s", red.TypeSafeAPIKey)
	}
	if red.GitHubToken == cfg.GitHubToken || red.GitHubToken != "••••••••cdef" {
		t.Errorf("expected GitHubToken to be redacted, got %s", red.GitHubToken)
	}
	if red.CloudflareAPIToken == cfg.CloudflareAPIToken || red.CloudflareAPIToken != "••••••••7890" {
		t.Errorf("expected CloudflareAPIToken to be redacted, got %s", red.CloudflareAPIToken)
	}
	// Verify other non-secret fields remain untouched
	if !red.AssistEnabled || len(red.AssistScopes) != 1 || red.AssistScopes[0] != "recap" {
		t.Errorf("expected non-secret fields to be preserved in Redacted()")
	}
}

func TestAssistScopeAndProjectConsent(t *testing.T) {
	// Disabled by default
	cfg := &config.Config{
		TypeSafeAPIKey: "jev-live-key",
		AssistProvider: "typesafe",
		AssistEnabled:  false,
		AssistScopes:   []string{"recap", "search"},
		AssistProjects: []string{"proj-alpha"},
	}

	if cfg.HasAssistScope("recap") {
		t.Errorf("expected HasAssistScope to be false when AssistEnabled is false")
	}
	if cfg.AllowsAssistProject("proj-alpha") {
		t.Errorf("expected AllowsAssistProject to be false when AssistEnabled is false")
	}

	// Enabled with specific scopes and projects
	cfg.AssistEnabled = true
	if !cfg.HasAssistScope("recap") {
		t.Errorf("expected HasAssistScope(recap) to be true")
	}
	if !cfg.HasAssistScope("search") {
		t.Errorf("expected HasAssistScope(search) to be true")
	}
	if cfg.HasAssistScope("gate") {
		t.Errorf("expected HasAssistScope(gate) to be false (unconsented scope)")
	}
	if cfg.HasAssistScope("doctor") {
		t.Errorf("expected HasAssistScope(doctor) to be false (unconsented scope)")
	}
	if cfg.HasAssistScope("live") {
		t.Errorf("expected HasAssistScope(live) to be false (unconsented scope)")
	}

	if !cfg.AllowsAssistProject("proj-alpha") {
		t.Errorf("expected AllowsAssistProject(proj-alpha) to be true")
	}
	if cfg.AllowsAssistProject("proj-beta") {
		t.Errorf("expected AllowsAssistProject(proj-beta) to be false (unconsented project)")
	}
	if cfg.AllowsAssistProject("") {
		t.Errorf("expected AllowsAssistProject('') to be false when project allowlist is restricted")
	}

	// Unconstrained projects allow any project including empty
	cfg.AssistProjects = nil
	if !cfg.AllowsAssistProject("") {
		t.Errorf("expected AllowsAssistProject('') to be true when AssistProjects is unconstrained")
	}
	if !cfg.AllowsAssistProject("any-project") {
		t.Errorf("expected AllowsAssistProject(any-project) to be true when AssistProjects is unconstrained")
	}
}
