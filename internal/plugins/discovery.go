package plugins

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// InstalledBinary records metadata about a binary found in the plugin bin directory.
type InstalledBinary struct {
	Name       string
	Path       string
	IsSymlink  bool
	TargetLink string
}

// ResolveBinDir returns the target directory for installed Sila plugins.
func ResolveBinDir() string {
	if dir := os.Getenv("SILA_PLUGINS_BIN_DIR"); dir != "" {
		return dir
	}
	if dir := os.Getenv("BINDIR"); dir != "" {
		return dir
	}
	home := os.Getenv("HOME")
	if home == "" {
		return filepath.Join(".", ".bin")
	}
	return filepath.Join(home, ".local", "bin")
}

// ResolveSrcDir locates the sila-plugins source repository.
func ResolveSrcDir() string {
	if dir := os.Getenv("SILA_PLUGINS_SRC_DIR"); dir != "" {
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			return dir
		}
	}

	candidates := []string{
		"../sila-plugins",
		"./sila-plugins",
		"../../sila-plugins",
	}

	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates,
			filepath.Join(cwd, "sila-plugins"),
			filepath.Join(cwd, "..", "sila-plugins"),
			filepath.Join(cwd, "..", "..", "sila-plugins"),
		)
	}

	for _, c := range candidates {
		abs, err := filepath.Abs(c)
		if err != nil {
			continue
		}
		if fi, err := os.Stat(abs); err == nil && fi.IsDir() {
			// Verify it looks like sila-plugins (contains cmd/ or tools-list.md or go.mod)
			if _, err := os.Stat(filepath.Join(abs, "cmd")); err == nil {
				return abs
			}
		}
	}

	return ""
}

// ReadCatalogDescriptions parses tools-list.md to extract plugin summaries.
func ReadCatalogDescriptions(srcDir string) map[string]string {
	res := make(map[string]string)
	if srcDir == "" {
		return res
	}

	path := filepath.Join(srcDir, "tools-list.md")
	f, err := os.Open(path)
	if err != nil {
		return res
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// Match: - `<name>`: <description>
		if strings.HasPrefix(line, "- `") {
			parts := strings.SplitN(line, "`:", 2)
			if len(parts) == 2 {
				name := strings.TrimPrefix(parts[0], "- `")
				desc := strings.TrimSpace(parts[1])
				// Clean up trailing details if long
				if idx := strings.Index(desc, "usage:"); idx > 0 {
					desc = strings.TrimSpace(desc[:idx])
				}
				desc = strings.TrimSuffix(desc, ".")
				res[name] = desc
			}
		}
	}
	return res
}

// QueryPluginMetadata invokes `<binPath> help --json` to fetch structured metadata.
func QueryPluginMetadata(ctx context.Context, binPath string) (*PluginMetadata, error) {
	tctx, cancel := context.WithTimeout(ctx, 600*time.Millisecond)
	defer cancel()

	cmd := exec.CommandContext(tctx, binPath, "help", "--json")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("plugin %s help --json failed: %w", filepath.Base(binPath), err)
	}

	var meta PluginMetadata
	if err := json.Unmarshal(out, &meta); err != nil {
		return nil, fmt.Errorf("plugin %s emitted invalid JSON schema: %w", filepath.Base(binPath), err)
	}

	return &meta, nil
}

// ScanInstalled scans binDir for executables and symlinks.
func ScanInstalled(binDir string) (map[string]InstalledBinary, error) {
	res := make(map[string]InstalledBinary)
	entries, err := os.ReadDir(binDir)
	if err != nil {
		if os.IsNotExist(err) {
			return res, nil
		}
		return nil, err
	}

	for _, entry := range entries {
		name := entry.Name()
		fullPath := filepath.Join(binDir, name)

		fi, err := os.Lstat(fullPath)
		if err != nil {
			continue
		}

		isSym := fi.Mode()&os.ModeSymlink != 0
		target := ""
		if isSym {
			if t, err := os.Readlink(fullPath); err == nil {
				target = t
			}
		} else if fi.Mode()&0111 == 0 {
			// Not executable
			continue
		}

		res[name] = InstalledBinary{
			Name:       name,
			Path:       fullPath,
			IsSymlink:  isSym,
			TargetLink: target,
		}
	}

	return res, nil
}

// ScanSourcePlugins returns directory names under `srcDir/cmd/`.
func ScanSourcePlugins(srcDir string) ([]string, error) {
	var names []string
	if srcDir == "" {
		return names, nil
	}

	cmdDir := filepath.Join(srcDir, "cmd")
	entries, err := os.ReadDir(cmdDir)
	if err != nil {
		if os.IsNotExist(err) {
			return names, nil
		}
		return nil, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		// Verify there is at least one .go file
		subEntries, err := os.ReadDir(filepath.Join(cmdDir, name))
		if err != nil {
			continue
		}
		hasGo := false
		for _, se := range subEntries {
			if !se.IsDir() && strings.HasSuffix(se.Name(), ".go") {
				hasGo = true
				break
			}
		}
		if hasGo {
			names = append(names, name)
		}
	}

	return names, nil
}
