package release

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ResolveGitRepoDir discovers the git repository root across single- and multi-repo workspaces.
//  1. If startDir is inside a git repo, returns git rev-parse --show-toplevel.
//  2. If startDir is a multi-repo workspace root (e.g. /sila containing cli/, worker/),
//     discovers child git repositories (preferring "cli", "core", "engine", "main", "app").
//  3. If neither succeeds, climbs parent directories looking for .git.
func ResolveGitRepoDir(startDir string) string {
	if startDir == "" {
		startDir, _ = os.Getwd()
	}

	// 1. Direct rev-parse from startDir
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = startDir
	if out, err := cmd.Output(); err == nil {
		top := strings.TrimSpace(string(out))
		if top != "" {
			return top
		}
	}

	// 2. Downward multi-repo discovery: check standard child repos
	for _, sub := range []string{"cli", "core", "engine", "main", "app"} {
		cand := filepath.Join(startDir, sub)
		cmd := exec.Command("git", "rev-parse", "--show-toplevel")
		cmd.Dir = cand
		if out, err := cmd.Output(); err == nil {
			top := strings.TrimSpace(string(out))
			if top != "" {
				return top
			}
		}
	}

	// 3. Upward climb: check ancestors for .git
	curr := startDir
	for {
		if fi, err := os.Stat(filepath.Join(curr, ".git")); err == nil {
			if fi.IsDir() || fi.Mode().IsRegular() {
				return curr
			}
		}
		parent := filepath.Dir(curr)
		if parent == curr {
			break
		}
		curr = parent
	}

	return startDir
}
