package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestXDGPathDefaults(t *testing.T) {
	// Clear overrides
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("SILA_DB", "")

	cfgDir := ConfigDir()
	if cfgDir == "" {
		t.Fatal("ConfigDir returned empty string")
	}
	if filepath.Base(cfgDir) != "sila" {
		t.Errorf("expected ConfigDir to end in sila, got %s", cfgDir)
	}

	dataDir := DataDir()
	if dataDir == "" {
		t.Fatal("DataDir returned empty string")
	}
	if filepath.Base(dataDir) != "sila" {
		t.Errorf("expected DataDir to end in sila, got %s", dataDir)
	}

	stateDir := StateDir()
	if stateDir == "" {
		t.Fatal("StateDir returned empty string")
	}
	if filepath.Base(stateDir) != "sila" && filepath.Base(filepath.Dir(stateDir)) != "sila" {
		t.Errorf("expected StateDir to contain sila, got %s", stateDir)
	}

	cacheDir := CacheDir()
	if cacheDir == "" {
		t.Fatal("CacheDir returned empty string")
	}
	if filepath.Base(cacheDir) != "sila" {
		t.Errorf("expected CacheDir to end in sila, got %s", cacheDir)
	}

	cfgPath := ConfigPath()
	if filepath.Base(cfgPath) != "config.json" {
		t.Errorf("expected ConfigPath to end in config.json, got %s", cfgPath)
	}

	dbPath := DBPath()
	if filepath.Base(dbPath) != PrimaryDBName {
		t.Errorf("expected DBPath to end in %s, got %s", PrimaryDBName, dbPath)
	}

	primaryPath := PrimaryDBPath()
	if filepath.Base(primaryPath) != PrimaryDBName {
		t.Errorf("expected PrimaryDBPath to end in %s, got %s", PrimaryDBName, primaryPath)
	}
}

func TestDBPath(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)
	t.Setenv("SILA_DB", "")

	dataDir := filepath.Join(tmp, "sila")
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		t.Fatal(err)
	}

	// 1. Returns sila.db under DataDir
	if got := filepath.Base(DBPath()); got != PrimaryDBName {
		t.Errorf("expected %s, got %s", PrimaryDBName, got)
	}

	// 2. SILA_DB override takes top priority
	t.Setenv("SILA_DB", "/custom/override.db")
	if got := DBPath(); got != "/custom/override.db" {
		t.Errorf("expected /custom/override.db from env override, got %s", got)
	}
}

func TestXDGPathOverrides(t *testing.T) {
	tmp := t.TempDir()
	customConfig := filepath.Join(tmp, "custom-config")
	customData := filepath.Join(tmp, "custom-data")
	customState := filepath.Join(tmp, "custom-state")
	customCache := filepath.Join(tmp, "custom-cache")

	t.Setenv("XDG_CONFIG_HOME", customConfig)
	t.Setenv("XDG_DATA_HOME", customData)
	t.Setenv("XDG_STATE_HOME", customState)
	t.Setenv("XDG_CACHE_HOME", customCache)

	if got := ConfigDir(); got != filepath.Join(customConfig, "sila") {
		t.Errorf("ConfigDir override failed: got %s, want %s", got, filepath.Join(customConfig, "sila"))
	}
	if got := DataDir(); got != filepath.Join(customData, "sila") {
		t.Errorf("DataDir override failed: got %s, want %s", got, filepath.Join(customData, "sila"))
	}
	if got := StateDir(); got != filepath.Join(customState, "sila") {
		t.Errorf("StateDir override failed: got %s, want %s", got, filepath.Join(customState, "sila"))
	}
	if got := CacheDir(); got != filepath.Join(customCache, "sila") {
		t.Errorf("CacheDir override failed: got %s, want %s", got, filepath.Join(customCache, "sila"))
	}

	// Database override via SILA_DB
	customDB := filepath.Join(tmp, "custom-store.db")
	t.Setenv("SILA_DB", customDB)
	if got := DBPath(); got != customDB {
		t.Errorf("DBPath SILA_DB override failed: got %s, want %s", got, customDB)
	}
}

func TestLegacyConfigMigration(t *testing.T) {
	tmp := t.TempDir()
	customConfig := filepath.Join(tmp, "config_home")
	customData := filepath.Join(tmp, "data_home")

	t.Setenv("XDG_CONFIG_HOME", customConfig)
	t.Setenv("XDG_DATA_HOME", customData)

	// Create a legacy config at $XDG_DATA_HOME/sila/config.json
	legacyDir := filepath.Join(customData, "sila")
	if err := os.MkdirAll(legacyDir, 0700); err != nil {
		t.Fatal(err)
	}
	legacyFile := filepath.Join(legacyDir, "config.json")
	legacyContent := `{"google_api_key": "legacy-key-123", "search_provider": "tavily"}`
	if err := os.WriteFile(legacyFile, []byte(legacyContent), 0600); err != nil {
		t.Fatal(err)
	}

	primaryFile := filepath.Join(customConfig, "sila", "config.json")
	if _, err := os.Stat(primaryFile); err == nil {
		t.Fatal("primary config should not exist before migration")
	}

	// Calling Load("") should detect the legacy config and seamlessly migrate it to primary
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load failed during migration: %v", err)
	}
	if cfg.GoogleAPIKey != "legacy-key-123" {
		t.Errorf("expected legacy-key-123, got %s", cfg.GoogleAPIKey)
	}
	if cfg.SearchProvider != "tavily" {
		t.Errorf("expected tavily, got %s", cfg.SearchProvider)
	}

	// Verify the migrated primary config exists on disk
	if _, err := os.Stat(primaryFile); err != nil {
		t.Fatalf("primary config was not created during migration: %v", err)
	}

	data, err := os.ReadFile(primaryFile)
	if err != nil {
		t.Fatalf("reading primary config: %v", err)
	}
	if string(data) != legacyContent {
		t.Errorf("migrated content mismatch: got %s, want %s", string(data), legacyContent)
	}
}

func TestNormalizePath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"   ", ""},
		{"foo/bar/baz", "foo/bar/baz"},
		{"foo\\bar\\baz", "foo/bar/baz"},
		{"C:\\Users\\Jad\\sila", "C:/Users/Jad/sila"},
		{"C:/Users/Jad/sila", "C:/Users/Jad/sila"},
		{"/workspace/sila", "/workspace/sila"},
		{"./foo/../bar", "bar"},
		{"foo\\\\\\bar", "foo/bar"},
	}

	for _, tt := range tests {
		got := NormalizePath(tt.input)
		if got != tt.want {
			t.Errorf("NormalizePath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSplitPath(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"", nil},
		{".", nil},
		{"/", nil},
		{"foo", []string{"foo"}},
		{"foo/bar/baz", []string{"foo", "bar", "baz"}},
		{"foo\\bar\\baz", []string{"foo", "bar", "baz"}},
		{"C:\\Users\\Example", []string{"C:", "Users", "Example"}},
		{"/home/example/project", []string{"home", "example", "project"}},
	}

	for _, tt := range tests {
		got := SplitPath(tt.input)
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("SplitPath(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestWorkspaceDBPath(t *testing.T) {
	if got := WorkspaceDBPath(""); got != filepath.Join(".sila", PrimaryDBName) {
		t.Errorf("expected .sila/%s, got %s", PrimaryDBName, got)
	}

	repo := "/home/user/myrepo"
	expected := filepath.Join(repo, ".sila", PrimaryDBName)
	if got := WorkspaceDBPath(repo); got != expected {
		t.Errorf("expected %s, got %s", expected, got)
	}
}

func TestNeedsAndEnsureGitIgnoreWorkspaceStore(t *testing.T) {
	t.Run("missing gitignore needs ignore and creates it", func(t *testing.T) {
		tmp := t.TempDir()
		needs, err := NeedsGitIgnoreWorkspaceStore(tmp)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !needs {
			t.Errorf("expected needs == true for missing .gitignore")
		}

		added, err := EnsureGitIgnoreWorkspaceStore(tmp)
		if err != nil {
			t.Fatalf("EnsureGitIgnoreWorkspaceStore failed: %v", err)
		}
		if !added {
			t.Errorf("expected added == true")
		}

		content, err := os.ReadFile(filepath.Join(tmp, ".gitignore"))
		if err != nil {
			t.Fatalf("read .gitignore failed: %v", err)
		}
		if !strings.Contains(string(content), ".sila/") {
			t.Errorf("expected .gitignore to contain .sila/, got:\n%s", string(content))
		}

		// Subsequent call should be a no-op
		addedAgain, err := EnsureGitIgnoreWorkspaceStore(tmp)
		if err != nil {
			t.Fatalf("subsequent call failed: %v", err)
		}
		if addedAgain {
			t.Errorf("expected addedAgain == false on second call")
		}

		needsAfter, err := NeedsGitIgnoreWorkspaceStore(tmp)
		if err != nil {
			t.Fatalf("NeedsGitIgnoreWorkspaceStore failed: %v", err)
		}
		if needsAfter {
			t.Errorf("expected needsAfter == false after adding pattern")
		}
	})

	t.Run("existing gitignore with alternative patterns returns false", func(t *testing.T) {
		patterns := []string{
			".sila",
			".sila/",
			"/.sila/",
			".sila/*",
			"/.sila/*",
			".sila/*.db*",
			".sila/sila.db",
		}
		for _, pat := range patterns {
			tmp := t.TempDir()
			if err := os.WriteFile(filepath.Join(tmp, ".gitignore"), []byte("node_modules/\n"+pat+"\n"), 0o644); err != nil {
				t.Fatalf("write .gitignore failed: %v", err)
			}
			needs, err := NeedsGitIgnoreWorkspaceStore(tmp)
			if err != nil {
				t.Fatalf("check failed for %q: %v", pat, err)
			}
			if needs {
				t.Errorf("pattern %q in .gitignore should have satisfied check", pat)
			}
		}
	})
}

func TestIsForbiddenLocalStateDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, dir := range []string{"", ".", "/", "/tmp", "/var/tmp", home, filepath.Join(home, "Downloads"), filepath.Join(home, ".config", "sila"), filepath.Join(home, "tmp", "proj")} {
		if !IsForbiddenLocalStateDir(dir) {
			t.Errorf("expected forbidden: %q", dir)
		}
	}
	for _, dir := range []string{filepath.Join("/tmp", "proj"), "/data/proj", filepath.Join(home, "repo")} {
		if IsForbiddenLocalStateDir(dir) {
			t.Errorf("expected allowed: %q", dir)
		}
	}
	repo := filepath.Join(home, "tmp", "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if IsForbiddenLocalStateDir(repo) {
		t.Errorf("a git repository under a forbidden prefix must be allowed")
	}
}
