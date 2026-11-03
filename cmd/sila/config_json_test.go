package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/jadmadi/sila/internal/config"
	"github.com/jadmadi/sila/internal/render"
)

func configReportMap(t *testing.T, cfg *config.Config, reveal bool) map[string]any {
	t.Helper()
	b, err := json.Marshal(buildConfigReport(cfg, reveal))
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func hasPath(m map[string]any, path ...string) bool {
	var cur any = m
	for _, key := range path {
		next, ok := cur.(map[string]any)
		if !ok {
			return false
		}
		cur, ok = next[key]
		if !ok {
			return false
		}
	}
	return true
}

func TestBuildConfigReportParity(t *testing.T) {
	cfg := &config.Config{
		BraveAPIKey:            "brave-key-value",
		TavilyAPIKey:           "tvly-secret",
		KagiAPIKey:             "kagi-secret",
		GitHubToken:            "ghp_secret_token",
		SourcegraphToken:       "sg-secret",
		StackOverflowKey:       "so-secret",
		GoogleAPIKey:           "google-secret",
		GoogleCSECX:            "cse-cx",
		TypeSafeAPIKey:         "ts-secret",
		CloudflareAPIToken:     "cf-secret",
		CloudflareAccountID:    "acct",
		CloudflareD1DatabaseID: "db",
		AssistEnabled:          true,
		AssistProvider:         "anthropic",
		AssistScopes:           []string{"diff", "gate"},
		AssistProjects:         []string{"proj"},
		DocSites:               []string{"https://a.example", "https://b.example"},
	}
	m := configReportMap(t, cfg, false)

	// Every human text row maps to one of these paths.
	paths := [][]string{
		{"schema_version"},
		{"config_file"},
		{"config_dir"},
		{"active_provider", "name"},
		{"active_provider", "reason"},
		{"active_provider", "source"},
		{"credentials", "brave", "set"},
		{"credentials", "tavily", "set"},
		{"credentials", "kagi", "set"},
		{"credentials", "github", "set"},
		{"credentials", "sourcegraph", "set"},
		{"credentials", "stackoverflow", "set"},
		{"credentials", "google_api", "set"},
		{"credentials", "google_cse_cx", "set"},
		{"credentials", "typesafe", "set"},
		{"credentials", "cloudflare", "set"},
		{"assist", "enabled"},
		{"assist", "provider"},
		{"assist", "scopes"},
		{"assist", "projects"},
		{"cloudflare", "account_id"},
		{"cloudflare", "d1_database_id"},
		{"cloudflare", "token", "set"},
		{"doc_sites", "count"},
		{"doc_sites", "items"},
		{"github_token"}, // legacy compatibility
	}
	for _, path := range paths {
		if !hasPath(m, path...) {
			t.Errorf("missing JSON path %v", path)
		}
	}

	if got := m["doc_sites"].(map[string]any)["count"].(float64); got != 2 {
		t.Fatalf("doc_sites.count = %v, want 2", got)
	}
	provider := m["active_provider"].(map[string]any)
	if provider["name"] != "tavily" || provider["source"] != "key" {
		t.Fatalf("active provider = %#v", provider)
	}

	github := m["credentials"].(map[string]any)["github"].(map[string]any)
	if github["set"] != true {
		t.Fatal("github credential must report set=true")
	}
	if _, hasRaw := github["value"]; hasRaw {
		t.Fatal("default report must not contain a raw credential")
	}
	if github["masked"] == "" {
		t.Fatal("default report must contain a masked credential")
	}
}

func TestConfigTextAlignment(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	cfgPath := filepath.Join(home, ".config", "sila", "config.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	ui := render.New(os.Stdout, false)
	out := captureStdout(t, func() {
		runConfig(ui, false, false, nil)
	})

	re := regexp.MustCompile(`(?m)^  ([A-Za-z0-9 ]+):( +)(\S.*)$`)
	matches := re.FindAllStringSubmatch(out, -1)
	if len(matches) < 10 {
		t.Fatalf("config text has %d label rows, want at least 10:\n%s", len(matches), out)
	}
	col := -1
	for _, m := range matches {
		got := 2 + len(m[1]) + 1 + len(m[2])
		if col == -1 {
			col = got
			continue
		}
		if got != col {
			t.Fatalf("label %q value starts at column %d, want %d:\n%s", m[1], got, col, out)
		}
	}
}

func TestBuildConfigReportReveal(t *testing.T) {
	cfg := &config.Config{GitHubToken: "ghp_secret_token"}
	m := configReportMap(t, cfg, true)
	github := m["credentials"].(map[string]any)["github"].(map[string]any)
	if github["value"] != "ghp_secret_token" {
		t.Fatalf("reveal value = %#v", github["value"])
	}
	if _, hasMasked := github["masked"]; hasMasked {
		t.Fatal("reveal must not emit both raw and masked values")
	}
}
