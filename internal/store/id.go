package store

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/config"
	"github.com/jadmadi/sila/internal/workspace"
)

// Slugify converts an arbitrary string into a clean alphanumeric slug.
func Slugify(s string) string {
	return workspace.Slugify(s)
}

// ProjectID derives a stable, human-friendly id from a normalized absolute
// path: if part of a multi-root .code-workspace, it derives a workspace-prefixed
// slug (e.g. "sila-cli", "sila-worker"); otherwise it uses the slugified final
// dir name, suffixed with a short path hash ONLY when a different path maps to
// the same dir name (resolved by the store).
func ProjectID(path string) string {
	return workspace.ProjectID(path)
}

// pathHash8 returns an 8-hex-char fingerprint of a normalized path, used to
// disambiguate same-name dirs at different absolute paths.
func pathHash8(path string) string {
	norm := config.NormalizePath(path)
	sum := sha256.Sum256([]byte(norm))
	return hex.EncodeToString(sum[:4])
}

// PathHash8 returns an 8-hex-char fingerprint of a normalized path, used to
// disambiguate same-name dirs at different absolute paths.
func PathHash8(path string) string {
	return pathHash8(path)
}

// RemoteHash returns a short 8-hex-char fingerprint of a repository remote URL.
func RemoteHash(remoteURL string) string {
	clean := strings.TrimSpace(strings.ToLower(remoteURL))
	clean = strings.TrimSuffix(clean, ".git")
	clean = strings.TrimRight(clean, "/")
	sum := sha256.Sum256([]byte(clean))
	return hex.EncodeToString(sum[:4])
}

// DetectGitRemoteURL attempts to find the remote origin URL for a repository path.
func DetectGitRemoteURL(repoPath string) string {
	if strings.TrimSpace(repoPath) == "" {
		return ""
	}
	canon := CanonicalProjectPath(repoPath)
	if canon == "" {
		canon = repoPath
	}
	canon = filepath.FromSlash(config.NormalizePath(canon))

	gitPath := filepath.Join(canon, ".git")
	configPath := filepath.Join(gitPath, "config")
	if fi, err := os.Stat(gitPath); err == nil && !fi.IsDir() {
		// Worktree pointer file: gitdir: ...
		if data, err := os.ReadFile(gitPath); err == nil {
			line := strings.TrimSpace(string(data))
			if after, ok := strings.CutPrefix(line, "gitdir:"); ok {
				gitdir := strings.TrimSpace(after)
				if !filepath.IsAbs(gitdir) {
					gitdir = filepath.Join(canon, gitdir)
				}
				configPath = filepath.Join(gitdir, "config")
				commonPath := filepath.Join(gitdir, "commondir")
				if cdData, err := os.ReadFile(commonPath); err == nil {
					cd := strings.TrimSpace(string(cdData))
					if !filepath.IsAbs(cd) {
						cd = filepath.Join(gitdir, cd)
					}
					configPath = filepath.Join(cd, "config")
				}
			}
		}
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return ""
	}
	return parseGitConfigRemoteURL(string(data))
}

func parseGitConfigRemoteURL(configContent string) string {
	lines := strings.Split(configContent, "\n")
	inOriginRemote := false
	var fallbackURL string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			lower := strings.ToLower(trimmed)
			inOriginRemote = strings.Contains(lower, `remote "origin"`) ||
				strings.Contains(lower, `remote 'origin'`)
			continue
		}
		if strings.HasPrefix(trimmed, "url") {
			parts := strings.SplitN(trimmed, "=", 2)
			if len(parts) == 2 {
				val := strings.TrimSpace(parts[1])
				if inOriginRemote {
					return val
				}
				if fallbackURL == "" {
					fallbackURL = val
				}
			}
		}
	}
	return fallbackURL
}

// FactID is deterministic over (project_id, claim).
func FactID(projectID, claim string) string {
	sum := sha256.Sum256([]byte(projectID + "\x00" + claim))
	return "fact_" + hex.EncodeToString(sum[:4])
}

// CausalFactID generates an HLC-based fact identifier: "fact_<hlc>".
func CausalFactID(nodeID string) string {
	if nodeID == "" {
		return "fact_" + GenerateHLC()
	}
	return "fact_" + NewHLC(nodeID).Tick(time.Now())
}

// CausalHandoffID generates an HLC-based handoff identifier: "hoff_<hlc>".
func CausalHandoffID(nodeID string) string {
	if nodeID == "" {
		return "hoff_" + GenerateHLC()
	}
	return "hoff_" + NewHLC(nodeID).Tick(time.Now())
}

// CausalMemoID generates an HLC-based memo identifier: "memo_<hlc>".
func CausalMemoID(nodeID string) string {
	if nodeID == "" {
		return "memo_" + GenerateHLC()
	}
	return "memo_" + NewHLC(nodeID).Tick(time.Now())
}

// CausalEventID generates an HLC-based event identifier with a custom prefix: "<prefix>_<hlc>".
func CausalEventID(prefix, nodeID string) string {
	if prefix == "" {
		prefix = "evt"
	}
	if nodeID == "" {
		return prefix + "_" + GenerateHLC()
	}
	return prefix + "_" + NewHLC(nodeID).Tick(time.Now())
}
