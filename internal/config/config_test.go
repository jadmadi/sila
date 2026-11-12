package config

import (
	"errors"
	"os"
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

func TestRecallGatePolicyPrecedence(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	globalPath := filepath.Join(home, ".config", "sila", "config.json")
	projectPath := filepath.Join(project, ".sila", "config.json")

	if policy, err := ResolveRecallGatePolicy("", project, home, nil); err != nil || policy != RecallGateWarn {
		t.Fatalf("default policy = %q, err = %v; want warn", policy, err)
	}

	if err := os.MkdirAll(filepath.Dir(globalPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(globalPath, []byte(`{"recall_gate_policy":"enforce"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if policy, err := ResolveRecallGatePolicy("", project, home, nil); err != nil || policy != RecallGateEnforce {
		t.Fatalf("global policy = %q, err = %v; want enforce", policy, err)
	}

	if err := os.MkdirAll(filepath.Dir(projectPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectPath, []byte(`{"recall_gate_policy":"off"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if policy, err := ResolveRecallGatePolicy("", project, home, nil); err != nil || policy != RecallGateOff {
		t.Fatalf("project policy = %q, err = %v; want off", policy, err)
	}

	if policy, err := ResolveRecallGatePolicy("enforce", project, home, nil); err != nil || policy != RecallGateEnforce {
		t.Fatalf("flag policy = %q, err = %v; want enforce", policy, err)
	}

	env := map[string]string{"SILA_RECALL_GATE": "off"}
	if policy, err := ResolveRecallGatePolicy("enforce", project, home, env); err != nil || policy != RecallGateOff {
		t.Fatalf("env policy = %q, err = %v; want off", policy, err)
	}
}

func TestRecallGateInvalidPolicy(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()

	if _, err := ResolveRecallGatePolicy("loud", project, home, nil); err == nil {
		t.Fatal("expected invalid explicit flag to be rejected")
	}

	env := map[string]string{"SILA_RECALL_GATE": "loud"}
	policy, err := ResolveRecallGatePolicy("", project, home, env)
	if policy != RecallGateWarn {
		t.Fatalf("invalid env must degrade to warn, got %q", policy)
	}
	var perr *RecallGatePolicyError
	if !errors.As(err, &perr) || !perr.DiagnosticOnly() {
		t.Fatalf("expected diagnostic-only environment error, got %v", err)
	}

	projectPath := filepath.Join(project, ".sila", "config.json")
	if err := os.MkdirAll(filepath.Dir(projectPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectPath, []byte(`{"recall_gate_policy":"loud"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveRecallGatePolicy("", project, home, nil); err == nil {
		t.Fatal("expected invalid project config to be rejected")
	}

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := SetKey(cfgPath, "recall_gate_policy", "enforce"); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RecallGate != "enforce" {
		t.Fatalf("stored policy = %q", cfg.RecallGate)
	}
	if err := SetKey(cfgPath, "recall_gate_policy", "loud"); err == nil {
		t.Fatal("expected invalid config set to be rejected")
	}
}
