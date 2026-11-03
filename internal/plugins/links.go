package plugins

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/jadmadi/sila/internal/config"
)

// LinkedPlugin records metadata for a plugin linked via Sila.
type LinkedPlugin struct {
	Name     string `json:"name"`
	Target   string `json:"target"`
	LinkedAt string `json:"linked_at"`
}

// LinksRegistryPath returns the path to the linked plugins registry JSON file.
func LinksRegistryPath(dataDir string) string {
	if dataDir == "" {
		dataDir = config.DataDir()
	}
	return filepath.Join(dataDir, "plugins", "links.json")
}

// LoadLinks reads the linked plugins registry from disk.
func LoadLinks(dataDir string) (map[string]LinkedPlugin, error) {
	path := LinksRegistryPath(dataDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]LinkedPlugin), nil
		}
		return nil, err
	}

	var list []LinkedPlugin
	if err := json.Unmarshal(data, &list); err != nil {
		return make(map[string]LinkedPlugin), nil
	}

	res := make(map[string]LinkedPlugin, len(list))
	for _, lp := range list {
		res[lp.Name] = lp
	}
	return res, nil
}

// SaveLinks persists the linked plugins registry to disk.
func SaveLinks(dataDir string, links map[string]LinkedPlugin) error {
	path := LinksRegistryPath(dataDir)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	list := make([]LinkedPlugin, 0, len(links))
	for _, lp := range links {
		list = append(list, lp)
	}

	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

// RecordLink adds an entry to the links registry.
func RecordLink(dataDir, name, target string) error {
	links, err := LoadLinks(dataDir)
	if err != nil {
		links = make(map[string]LinkedPlugin)
	}

	links[name] = LinkedPlugin{
		Name:     name,
		Target:   target,
		LinkedAt: time.Now().UTC().Format(time.RFC3339),
	}

	return SaveLinks(dataDir, links)
}

// RemoveLink removes an entry from the links registry.
func RemoveLink(dataDir, name string) error {
	links, err := LoadLinks(dataDir)
	if err != nil {
		return nil
	}

	delete(links, name)
	return SaveLinks(dataDir, links)
}
