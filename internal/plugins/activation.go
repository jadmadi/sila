package plugins

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ActivationConfig represents the project-scoped active plugins list.
type ActivationConfig struct {
	ActivePlugins []string `json:"active_plugins"`
}

// ActivationConfigPath returns the file path for the project's plugins.json.
func ActivationConfigPath(projectDir string) string {
	if projectDir == "" {
		projectDir = "."
	}
	return filepath.Join(projectDir, ".sila", "plugins.json")
}

// LoadActivationConfig loads the active plugin configuration for a project.
func LoadActivationConfig(projectDir string) (*ActivationConfig, error) {
	path := ActivationConfigPath(projectDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &ActivationConfig{ActivePlugins: []string{}}, nil
		}
		return nil, fmt.Errorf("read plugin activation config: %w", err)
	}
	var cfg ActivationConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse plugin activation config: %w", err)
	}
	return &cfg, nil
}

// SaveActivationConfig persists active plugin configuration for a project.
func SaveActivationConfig(projectDir string, cfg *ActivationConfig) error {
	if cfg == nil {
		cfg = &ActivationConfig{ActivePlugins: []string{}}
	}
	path := ActivationConfigPath(projectDir)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create .sila directory: %w", err)
	}

	seen := make(map[string]bool)
	var clean []string
	for _, p := range cfg.ActivePlugins {
		p = strings.TrimSpace(p)
		if p != "" && !seen[p] {
			seen[p] = true
			clean = append(clean, p)
		}
	}
	sort.Strings(clean)
	cfg.ActivePlugins = clean

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal activation config: %w", err)
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0644)
}

// IsPluginActive returns true if the plugin is activated for the project.
func IsPluginActive(projectDir, name string) bool {
	cfg, err := LoadActivationConfig(projectDir)
	if err != nil || cfg == nil {
		return false
	}
	name = strings.TrimSpace(name)
	for _, p := range cfg.ActivePlugins {
		if strings.EqualFold(p, name) {
			return true
		}
	}
	return false
}

// ActivatePlugin enables an installed plugin for a project.
func ActivatePlugin(projectDir, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("plugin name is required")
	}
	cfg, err := LoadActivationConfig(projectDir)
	if err != nil {
		return err
	}
	for _, p := range cfg.ActivePlugins {
		if strings.EqualFold(p, name) {
			return nil
		}
	}
	cfg.ActivePlugins = append(cfg.ActivePlugins, name)
	return SaveActivationConfig(projectDir, cfg)
}

// DeactivatePlugin disables an active plugin for a project.
func DeactivatePlugin(projectDir, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("plugin name is required")
	}
	cfg, err := LoadActivationConfig(projectDir)
	if err != nil {
		return err
	}
	var next []string
	found := false
	for _, p := range cfg.ActivePlugins {
		if strings.EqualFold(p, name) {
			found = true
			continue
		}
		next = append(next, p)
	}
	if !found {
		return nil
	}
	cfg.ActivePlugins = next
	return SaveActivationConfig(projectDir, cfg)
}

// ListActivePlugins returns the list of active plugin names for a project.
func ListActivePlugins(projectDir string) ([]string, error) {
	cfg, err := LoadActivationConfig(projectDir)
	if err != nil {
		return nil, err
	}
	return cfg.ActivePlugins, nil
}
