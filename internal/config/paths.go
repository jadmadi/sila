package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ConfigDir returns the XDG-compliant configuration directory for Sila:
// 1. $XDG_CONFIG_HOME/sila if XDG_CONFIG_HOME is set
// 2. os.UserConfigDir()/sila (e.g. $HOME/.config/sila on Linux, %AppData%\sila on Windows)
// 3. Fallback: $HOME/.config/sila
func ConfigDir() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "sila")
	}
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "sila")
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		home = os.Getenv("HOME")
	}
	return filepath.Join(home, ".config", "sila")
}

// DataDir returns the XDG-compliant persistent data directory for Sila:
// 1. $XDG_DATA_HOME/sila if XDG_DATA_HOME is set
// 2. %LocalAppData%\sila on Windows
// 3. Fallback: $HOME/.local/share/sila
func DataDir() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "sila")
	}
	if runtime.GOOS == "windows" {
		if localApp := os.Getenv("LOCALAPPDATA"); localApp != "" {
			return filepath.Join(localApp, "sila")
		}
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		home = os.Getenv("HOME")
	}
	return filepath.Join(home, ".local", "share", "sila")
}

// StateDir returns the XDG-compliant state and runtime lock directory for Sila:
// 1. $XDG_STATE_HOME/sila if XDG_STATE_HOME is set
// 2. %LocalAppData%\sila\state on Windows
// 3. Fallback: $HOME/.local/state/sila
func StateDir() string {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "sila")
	}
	if runtime.GOOS == "windows" {
		if localApp := os.Getenv("LOCALAPPDATA"); localApp != "" {
			return filepath.Join(localApp, "sila", "state")
		}
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		home = os.Getenv("HOME")
	}
	return filepath.Join(home, ".local", "state", "sila")
}

// CacheDir returns the XDG-compliant ephemeral cache directory for Sila:
// 1. $XDG_CACHE_HOME/sila if XDG_CACHE_HOME is set
// 2. os.UserCacheDir()/sila
// 3. Fallback: $HOME/.cache/sila
func CacheDir() string {
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "sila")
	}
	if dir, err := os.UserCacheDir(); err == nil && dir != "" {
		return filepath.Join(dir, "sila")
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		home = os.Getenv("HOME")
	}
	return filepath.Join(home, ".cache", "sila")
}

// ConfigPath returns the standard configuration file path ($XDG_CONFIG_HOME/sila/config.json).
// If the primary file does not exist but a legacy file exists at $XDG_DATA_HOME/sila/config.json,
// the legacy path is returned for backward compatibility until migrated.
func ConfigPath() string {
	primary := filepath.Join(ConfigDir(), "config.json")
	if _, err := os.Stat(primary); err == nil {
		return primary
	}
	// Check legacy location in DataDir
	legacy := filepath.Join(DataDir(), "config.json")
	if _, err := os.Stat(legacy); err == nil {
		return legacy
	}
	return primary
}

// PrimaryDBName is the standard canonical database filename.
const PrimaryDBName = "sila.db"

// DBPath returns the standard SQLite database path:
// 1. $SILA_DB override
// 2. Primary: $XDG_DATA_HOME/sila/sila.db
func DBPath() string {
	if env := os.Getenv("SILA_DB"); env != "" {
		return env
	}
	return filepath.Join(DataDir(), PrimaryDBName)
}

// PrimaryDBPath returns the canonical database path ($XDG_DATA_HOME/sila/sila.db)
// or $SILA_DB override if set.
func PrimaryDBPath() string {
	return DBPath()
}

// WorkspaceDBPath returns the workspace-local database path (.sila/sila.db)
// within the given repository or workspace root directory.
func WorkspaceDBPath(repoDir string) string {
	if repoDir == "" {
		return filepath.Join(".sila", PrimaryDBName)
	}
	return filepath.Join(repoDir, ".sila", PrimaryDBName)
}

// NeedsGitIgnoreWorkspaceStore checks whether .gitignore at repoDir needs an entry
// ignoring .sila/ or workspace database files.
func NeedsGitIgnoreWorkspaceStore(repoDir string) (bool, error) {
	if repoDir == "" {
		repoDir = "."
	}
	ignoreFile := filepath.Join(repoDir, ".gitignore")
	data, err := os.ReadFile(ignoreFile)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, fmt.Errorf("read .gitignore: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if trimmed == ".sila" || trimmed == ".sila/" || trimmed == "/.sila" || trimmed == "/.sila/" ||
			trimmed == ".sila/*" || trimmed == "/.sila/*" ||
			trimmed == ".sila/*.db*" || trimmed == "/.sila/*.db*" ||
			trimmed == ".sila/sila.db" || trimmed == "/.sila/sila.db" {
			return false, nil
		}
	}
	return true, nil
}

// EnsureGitIgnoreWorkspaceStore ensures that .sila/ is present in .gitignore at repoDir.
func EnsureGitIgnoreWorkspaceStore(repoDir string) (bool, error) {
	needs, err := NeedsGitIgnoreWorkspaceStore(repoDir)
	if err != nil || !needs {
		return false, err
	}

	if repoDir == "" {
		repoDir = "."
	}
	ignoreFile := filepath.Join(repoDir, ".gitignore")
	data, _ := os.ReadFile(ignoreFile)
	content := string(data)

	rule := "\n# Workspace-local knowledge store (managed by sila)\n.sila/\n"
	if len(content) > 0 && !strings.HasSuffix(content, "\n") {
		rule = "\n" + rule
	}

	newContent := content + rule
	if err := os.WriteFile(ignoreFile, []byte(newContent), 0o644); err != nil {
		return false, fmt.Errorf("write .gitignore: %w", err)
	}
	return true, nil
}

// NormalizePath standardizes a filesystem path by cleaning it and converting all
// path separators to forward slashes (filepath.ToSlash). On Windows, it preserves
// drive letters (e.g. "C:/path") without path mangling.
func NormalizePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	// Convert Windows backslashes to forward slashes
	p = strings.ReplaceAll(p, "\\", "/")
	clean := filepath.ToSlash(filepath.Clean(p))
	// Preserve Windows drive root: e.g. "C:/" instead of "C:" when root was intended
	if len(clean) == 2 && clean[1] == ':' && strings.HasPrefix(p, clean+"/") {
		clean += "/"
	}
	return clean
}

// SplitPath decomposes a filesystem path into its components, handling both Windows
// backslashes and Unix forward slashes safely.
func SplitPath(p string) []string {
	clean := NormalizePath(p)
	if clean == "" || clean == "." {
		return nil
	}
	vol := filepath.VolumeName(clean)
	rest := clean
	if vol != "" {
		rest = strings.TrimPrefix(rest, vol)
	}
	rest = strings.Trim(rest, "/")
	if rest == "" {
		if vol != "" {
			return []string{vol}
		}
		return nil
	}
	parts := strings.Split(rest, "/")
	if vol != "" {
		return append([]string{vol}, parts...)
	}
	return parts
}
