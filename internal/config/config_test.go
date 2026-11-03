package config

import (
	"path/filepath"
	"testing"
)

func TestConfigCRUD(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GoogleAPIKey != "" || len(cfg.DocSites) != 0 {
		t.Fatalf("expected empty config, got %+v", cfg)
	}

	if err := SetKey(cfgPath, "google_api_key", "secret123"); err != nil {
		t.Fatal(err)
	}
	if err := SetKey(cfgPath, "google_cse_cx", "cx456"); err != nil {
		t.Fatal(err)
	}

	if err := AddDocSite(cfgPath, "https://docs.anthropic.com/*"); err != nil {
		t.Fatal(err)
	}
	if err := AddDocSite(cfgPath, "https://sqlite.org/*"); err != nil {
		t.Fatal(err)
	}
	// duplicate addition should be idempotent
	if err := AddDocSite(cfgPath, "https://sqlite.org/*"); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.GoogleAPIKey != "secret123" {
		t.Fatalf("expected secret123, got %s", loaded.GoogleAPIKey)
	}
	if loaded.GoogleCSECX != "cx456" {
		t.Fatalf("expected cx456, got %s", loaded.GoogleCSECX)
	}
	if len(loaded.DocSites) != 2 {
		t.Fatalf("expected 2 doc sites, got %d", len(loaded.DocSites))
	}

	// Remove doc site
	if err := RemoveDocSite(cfgPath, "https://sqlite.org/*"); err != nil {
		t.Fatal(err)
	}
	loaded2, _ := Load(cfgPath)
	if len(loaded2.DocSites) != 1 || loaded2.DocSites[0] != "https://docs.anthropic.com/*" {
		t.Fatalf("expected 1 doc site remaining, got %+v", loaded2.DocSites)
	}
}

func TestResolvedUpstreamMCPServers_AuthSecurity(t *testing.T) {
	cfg := &Config{
		AlphaXivAPIToken: "mock-alpha-token-xyz",
		StitchAPIKey:     "mock-stitch-key-abc",
	}

	servers := cfg.ResolvedUpstreamMCPServers()

	// 1. AlphaXiv must use ServerURL and Headers/AuthToken, never npx argv with --header
	ax, ok := servers["alphaxiv"]
	if !ok {
		t.Fatal("expected alphaxiv server to be resolved")
	}
	if ax.ServerURL != "https://api.alphaxiv.org/mcp/v1" {
		t.Fatalf("expected alphaxiv ServerURL https://api.alphaxiv.org/mcp/v1, got %q", ax.ServerURL)
	}
	if ax.Headers["Authorization"] != "Bearer mock-alpha-token-xyz" {
		t.Fatalf("expected Authorization header to contain token, got %q", ax.Headers["Authorization"])
	}
	if ax.AuthToken != "mock-alpha-token-xyz" {
		t.Fatalf("expected AuthToken to match config, got %q", ax.AuthToken)
	}
	if ax.Command != "" {
		t.Fatalf("expected empty Command for HTTP upstream, got %q", ax.Command)
	}
	if len(ax.Args) != 0 {
		t.Fatalf("expected empty Args for HTTP upstream, got %v", ax.Args)
	}

	// 2. Stitch must use ServerURL and Headers, never npx argv with --header
	st, ok := servers["stitch"]
	if !ok {
		t.Fatal("expected stitch server to be resolved")
	}
	if st.ServerURL != "https://stitch.googleapis.com/mcp" {
		t.Fatalf("expected stitch ServerURL https://stitch.googleapis.com/mcp, got %q", st.ServerURL)
	}
	if st.Headers["X-Goog-Api-Key"] != "mock-stitch-key-abc" {
		t.Fatalf("expected X-Goog-Api-Key header to contain key, got %q", st.Headers["X-Goog-Api-Key"])
	}
	if st.Command != "" {
		t.Fatalf("expected empty Command for HTTP upstream, got %q", st.Command)
	}
	if len(st.Args) != 0 {
		t.Fatalf("expected empty Args for HTTP upstream, got %v", st.Args)
	}
}
