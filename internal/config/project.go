package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// FindProjectConfig searches dir and its parents for .sila/config.json.
// If dir is empty, the current working directory is used.
// It stops at the nearest Git boundary. Without a Git boundary only dir is searched.
func FindProjectConfig(dir string) string {
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return ""
		}
		dir = cwd
	}
	curr := filepath.Clean(dir)
	root := projectBoundary(curr)
	for {
		candidate := filepath.Join(curr, ".sila", "config.json")
		if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() {
			return candidate
		}
		// If curr has a .git directory or file (submodule/worktree), do not traverse outside repository root
		if curr == root {
			break
		}
		parent := filepath.Dir(curr)
		if parent == curr {
			break
		}
		curr = parent
	}
	return ""
}

// ProjectConfigDir finds the project directory root (by locating .git or existing .sila)
// and returns the path to its .sila directory.
func ProjectConfigDir(dir string) string {
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return ".sila"
		}
		dir = cwd
	}
	curr := filepath.Clean(dir)
	root := projectBoundary(curr)
	tempRoot := filepath.Clean(os.TempDir())
	for {
		if _, err := os.Stat(filepath.Join(curr, ".sila")); err == nil {
			return filepath.Join(curr, ".sila")
		}
		if curr == root || curr == tempRoot || curr == "/tmp" {
			return filepath.Join(curr, ".sila")
		}
		parent := filepath.Dir(curr)
		if parent == curr || parent == tempRoot || parent == "/tmp" {
			break
		}
		curr = parent
	}
	return filepath.Join(filepath.Clean(dir), ".sila")
}

// projectBoundary bounds discovery to a repository, or the selected directory
// when no repository exists. A worktree's .git file is also a boundary.
func projectBoundary(dir string) string {
	tempRoot := filepath.Clean(os.TempDir())
	for curr := dir; ; curr = filepath.Dir(curr) {
		if curr == tempRoot || curr == "/tmp" {
			return dir
		}
		if _, err := os.Stat(filepath.Join(curr, ".git")); err == nil {
			return curr
		}
		if filepath.Dir(curr) == curr {
			return dir
		}
	}
}

// Merge merges non-zero fields from override into base.
func Merge(base, override *Config) {
	if base == nil || override == nil {
		return
	}
	if override.StateBlock.WelcomeBackHours != nil {
		base.StateBlock.WelcomeBackHours = override.StateBlock.WelcomeBackHours
	}
	if override.GoogleAPIKey != "" {
		base.GoogleAPIKey = override.GoogleAPIKey
	}
	if override.GoogleCSECX != "" {
		base.GoogleCSECX = override.GoogleCSECX
	}
	if override.TavilyAPIKey != "" {
		base.TavilyAPIKey = override.TavilyAPIKey
	}
	if override.BraveAPIKey != "" {
		base.BraveAPIKey = override.BraveAPIKey
	}
	if override.KagiAPIKey != "" {
		base.KagiAPIKey = override.KagiAPIKey
	}
	if override.GitHubToken != "" {
		base.GitHubToken = override.GitHubToken
	}
	if override.SourcegraphToken != "" {
		base.SourcegraphToken = override.SourcegraphToken
	}
	if override.StackOverflowKey != "" {
		base.StackOverflowKey = override.StackOverflowKey
	}
	if override.SearchProvider != "" {
		base.SearchProvider = override.SearchProvider
	}
	if override.TypeSafeAPIKey != "" {
		base.TypeSafeAPIKey = override.TypeSafeAPIKey
	}
	if override.AssistProvider != "" {
		base.AssistProvider = override.AssistProvider
	}
	if override.AssistEnabled {
		base.AssistEnabled = override.AssistEnabled
	}
	if len(override.AssistScopes) > 0 {
		base.AssistScopes = override.AssistScopes
	}
	if len(override.AssistProjects) > 0 {
		base.AssistProjects = override.AssistProjects
	}
	if override.CloudEnabled {
		base.CloudEnabled = override.CloudEnabled
	}
	if override.CloudEndpoint != "" {
		base.CloudEndpoint = override.CloudEndpoint
	}
	if override.CloudToken != "" {
		base.CloudToken = override.CloudToken
	}
	if len(override.CloudScopes) > 0 {
		base.CloudScopes = override.CloudScopes
	}
	if len(override.CloudProjects) > 0 {
		base.CloudProjects = override.CloudProjects
	}
	if len(override.DocSites) > 0 {
		existing := make(map[string]bool)
		for _, s := range base.DocSites {
			existing[s] = true
		}
		for _, s := range override.DocSites {
			if !existing[s] {
				base.DocSites = append(base.DocSites, s)
				existing[s] = true
			}
		}
	}
	if override.MCPUpstreamsEnabled {
		base.MCPUpstreamsEnabled = override.MCPUpstreamsEnabled
	}
	if override.AlphaXivAPIToken != "" {
		base.AlphaXivAPIToken = override.AlphaXivAPIToken
	}
	if override.StitchAPIKey != "" {
		base.StitchAPIKey = override.StitchAPIKey
	}
	if len(override.MCPUpstreams) > 0 {
		if base.MCPUpstreams == nil {
			base.MCPUpstreams = make(map[string]UpstreamMCPServer)
		}
		for k, v := range override.MCPUpstreams {
			base.MCPUpstreams[k] = v
		}
	}
	if override.RecallGate != "" {
		base.RecallGate = override.RecallGate
	}
}

// InitConfig initializes a new configuration file at targetPath (or default global/project path).
// If targetPath already exists, it returns (targetPath, false, nil) without modifying the file.
// If isProject is true and targetPath is empty, it initializes in .sila/config.json.
// Otherwise it initializes in ConfigPath().
func InitConfig(targetPath string, isProject bool) (string, bool, error) {
	if targetPath == "" {
		if isProject {
			dir := ProjectConfigDir("")
			targetPath = filepath.Join(dir, "config.json")
		} else {
			targetPath = filepath.Join(ConfigDir(), "config.json")
		}
	}

	if _, err := os.Stat(targetPath); err == nil {
		return targetPath, false, nil
	}

	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", false, fmt.Errorf("creating directory %s: %w", dir, err)
	}

	// Create starter template with sensible defaults
	starter := Config{
		SearchProvider: "duckduckgo",
		AssistProvider: "typesafe",
		AssistEnabled:  false,
		AssistScopes:   []string{"gate", "recap"},
	}

	data, err := json.MarshalIndent(starter, "", "  ")
	if err != nil {
		return "", false, err
	}
	data = append(data, '\n')

	if err := os.WriteFile(targetPath, data, 0600); err != nil {
		return "", false, fmt.Errorf("writing configuration %s: %w", targetPath, err)
	}

	return targetPath, true, nil
}

// SaveProjectConfig writes or updates the project-level configuration at <dir>/.sila/config.json.
// If dir is empty, the current working directory is used.
func SaveProjectConfig(dir string, cfg *Config) error {
	pDir := ProjectConfigDir(dir)
	if err := os.MkdirAll(pDir, 0700); err != nil {
		return fmt.Errorf("creating project config dir %s: %w", pDir, err)
	}
	targetPath := filepath.Join(pDir, "config.json")
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(targetPath, data, 0600)
}
