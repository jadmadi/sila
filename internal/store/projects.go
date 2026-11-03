package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jadmadi/sila/internal/config"
)

// ProjectSelectorMatches reports whether selector identifies a project by its
// exact ID, exact registered path, or containing directory. It deliberately
// never performs substring or colliding-basename cross-repository matching:
// "sila" must not select "sila-old", and an unregistered directory must not
// adopt an unrelated registered project's data.
func ProjectSelectorMatches(p Project, selector string) bool {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return true
	}
	if strings.EqualFold(p.ID, selector) || samePath(p.Path, selector) {
		return true
	}
	if fi, err := os.Stat(selector); err == nil && fi.IsDir() {
		return samePath(p.Path, CanonicalProjectPath(selector))
	}
	return false
}

// ResolveProject resolves one unambiguous registered project. Exact IDs take
// precedence over paths and basenames so a canonical ID remains stable even
// when repositories share a directory name.
func (s *Store) ResolveProject(selector string) (Project, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return Project{}, fmt.Errorf("project selector is required")
	}
	projects, err := s.Projects()
	if err != nil {
		return Project{}, err
	}
	for _, p := range projects {
		if p.ID == selector {
			return p, nil
		}
	}
	for _, p := range projects {
		if strings.EqualFold(p.ID, selector) {
			return p, nil
		}
	}
	var matches []Project
	for _, p := range projects {
		if samePath(p.Path, selector) || strings.EqualFold(filepath.Base(filepath.Clean(p.Path)), selector) {
			matches = append(matches, p)
		}
	}
	switch len(matches) {
	case 0:
		return Project{}, fmt.Errorf("no registered project matches %q", selector)
	case 1:
		return matches[0], nil
	default:
		ids := make([]string, 0, len(matches))
		for _, p := range matches {
			ids = append(ids, p.ID)
		}
		return Project{}, fmt.Errorf("project selector %q is ambiguous; use an exact ID or path (%s)", selector, strings.Join(ids, ", "))
	}
}

// GitScope resolves the repository directory and per-repository watermark key
// for the git loader. A selector resolves to its exact registered project path
// when possible, otherwise it is treated as a directory; an empty selector
// uses the current working directory. The key is independent of the shared
// machine-wide git cursor so repositories never clobber each other's.
func (s *Store) GitScope(selector string) (dir string, watermarkKey string) {
	if strings.TrimSpace(selector) != "" {
		if p, err := s.ResolveProject(selector); err == nil {
			dir = p.Path
		} else {
			dir = selector
		}
	}
	if dir == "" {
		if wd, err := os.Getwd(); err == nil {
			dir = wd
		}
	}
	dir = CanonicalProjectPath(dir)
	return dir, "git:" + ProjectID(dir)
}

// CanonicalProjectPath resolves any path to its canonical root repository directory.
// If dir is inside a Git worktree (e.g. .worktrees/<name>, .git/worktrees/<name>,
// or .git is a gitdir pointer file), it returns the main repository root directory on disk.
func CanonicalProjectPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return path
	}
	p := config.NormalizePath(path)

	// Resolve symlinked path aliases (for example /home/user/x -> /mnt/x) so the
	// same real directory cannot register as two projects.
	if resolved, err := filepath.EvalSymlinks(filepath.FromSlash(p)); err == nil {
		p = config.NormalizePath(resolved)
	}

	// 1. Walk up from p checking for .git (file for worktree, dir for repo root)
	cur := filepath.FromSlash(p)
	for {
		gitPath := filepath.Join(cur, ".git")
		if fi, err := os.Stat(gitPath); err == nil {
			if !fi.IsDir() {
				// .git is a worktree pointer file
				if data, err := os.ReadFile(gitPath); err == nil {
					line := strings.TrimSpace(string(data))
					if after, ok := strings.CutPrefix(line, "gitdir:"); ok {
						gitdir := strings.TrimSpace(after)
						if !filepath.IsAbs(gitdir) {
							gitdir = filepath.Join(cur, gitdir)
						}
						gitdir = config.NormalizePath(gitdir)

						// Check if commondir exists inside gitdir
						commonDirFile := filepath.Join(filepath.FromSlash(gitdir), "commondir")
						if cdData, err := os.ReadFile(commonDirFile); err == nil {
							cd := strings.TrimSpace(string(cdData))
							mainGit := config.NormalizePath(filepath.Join(filepath.FromSlash(gitdir), cd))
							mainRepo := config.NormalizePath(filepath.Dir(filepath.FromSlash(mainGit)))
							if fiMain, err := os.Stat(filepath.FromSlash(mainRepo)); err == nil && fiMain.IsDir() {
								return mainRepo
							}
						}

						// Fallback: look for worktree directory markers in gitdir path
						for _, marker := range []string{"/.git/worktrees/", "/.git/sila-worktrees/", "/.git/worktree/"} {
							if idx := strings.Index(gitdir, marker); idx != -1 {
								mainRepo := config.NormalizePath(gitdir[:idx])
								if fiMain, err := os.Stat(filepath.FromSlash(mainRepo)); err == nil && fiMain.IsDir() {
									return mainRepo
								}
							}
						}
					}
				}
			}

			// If cur has a .git directory, check if p is inside a worktree subfolder within cur
			for _, marker := range []string{"/.worktrees/", "/.worktree/", "/.git/sila-worktrees/", "/.git/worktrees/", "/sila-worktrees/"} {
				if idx := strings.Index(p, marker); idx != -1 {
					candidate := config.NormalizePath(p[:idx])
					if fiCandidate, err := os.Stat(filepath.FromSlash(candidate)); err == nil && fiCandidate.IsDir() {
						return candidate
					}
				}
			}
			// A subdirectory inside a Git repository collapses to the repository
			// root unless an intermediate directory owns a .code-workspace file.
			curNorm := config.NormalizePath(cur)
			if curNorm != p && !hasWorkspaceBoundaryBetween(filepath.FromSlash(p), cur) {
				return curNorm
			}
			break
		}

		parent := filepath.Dir(cur)
		if parent == cur || parent == "." || parent == "/" || parent == "\\" {
			break
		}
		cur = parent
	}

	// 2. Pattern-based fallback (handles worktrees whose directories or .git files were already deleted from disk)
	for _, marker := range []string{"/.worktrees/", "/.worktree/", "/.git/sila-worktrees/", "/.git/worktrees/", "/sila-worktrees/"} {
		if idx := strings.Index(p, marker); idx != -1 {
			candidate := config.NormalizePath(p[:idx])
			if fiCandidate, err := os.Stat(filepath.FromSlash(candidate)); err == nil && fiCandidate.IsDir() {
				return candidate
			}
		}
	}

	return p
}

func hasWorkspaceBoundaryBetween(start, stop string) bool {
	cur := filepath.Clean(start)
	stopClean := filepath.Clean(stop)
	for cur != stopClean {
		entries, err := os.ReadDir(cur)
		if err == nil {
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".code-workspace") {
					return true
				}
			}
		}
		parent := filepath.Dir(cur)
		if parent == cur || parent == "." || parent == string(filepath.Separator) {
			break
		}
		cur = parent
	}
	return false
}

// ProjectForDir detects whether the given directory (or current working directory)
// belongs to a registered project (either exact path match, or within the project root).
// Returns (Project, true, nil) if found, or (Project{}, false, nil) if not in any registered project.
func (s *Store) ProjectForDir(dir string) (Project, bool, error) {
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			return Project{}, false, err
		}
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		absDir = filepath.Clean(dir)
	}

	canonDir := CanonicalProjectPath(absDir)

	projects, err := s.Projects()
	if err != nil {
		return Project{}, false, err
	}

	// 1. First pass: exact path match (checking canonical path first, then absDir)
	for _, p := range projects {
		if samePath(p.Path, canonDir) || samePath(p.Path, absDir) {
			return p, true, nil
		}
	}

	// 2. Second pass: CWD is a subdirectory inside a registered project
	// (a workspace or container directory). A directory that is the root of
	// its own Git repository is not a subdirectory: it is a separate
	// repository with its own identity, so it must not borrow a registered
	// ancestor's. Without this boundary a container registration that spans
	// many repositories silently absorbs every unregistered repository under
	// it — the live case: /workspace/sila/github registered as a stale workspace slug
	// while a distinct repository lived at projects/waqftech/MutmainKalp.com.
	if !isRepositoryRoot(canonDir) {
		var bestMatch Project
		bestLen := 0
		for _, p := range projects {
			pAbs, err := filepath.Abs(p.Path)
			if err != nil {
				pAbs = filepath.Clean(p.Path)
			}
			relCanon, errCanon := filepath.Rel(pAbs, canonDir)
			if errCanon == nil {
				relSlash := filepath.ToSlash(relCanon)
				if relSlash != ".." && !strings.HasPrefix(relSlash, "../") && !strings.HasPrefix(relSlash, "/") {
					if len(pAbs) > bestLen {
						bestLen = len(pAbs)
						bestMatch = p
					}
				}
			}
			rel, errRel := filepath.Rel(pAbs, absDir)
			if errRel == nil {
				relSlash := filepath.ToSlash(rel)
				if relSlash != ".." && !strings.HasPrefix(relSlash, "../") && !strings.HasPrefix(relSlash, "/") {
					if len(pAbs) > bestLen {
						bestLen = len(pAbs)
						bestMatch = p
					}
				}
			}
		}
		if bestLen > 0 {
			return bestMatch, true, nil
		}
	}

	return Project{}, false, nil
}

// IsRepoOrWorkspaceDir reports whether dir is a valid repository or multi-root
// workspace root rather than a bare user directory (/home/user, /tmp, ~/Downloads, etc.).
func IsRepoOrWorkspaceDir(dir string) bool {
	if dir == "" {
		return false
	}
	clean := filepath.Clean(filepath.FromSlash(dir))
	if clean == "/" || clean == "." || clean == "/tmp" || clean == "/var/tmp" {
		return false
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if clean == filepath.Clean(home) {
			return false
		}
	}
	fi, err := os.Stat(clean)
	if err != nil || !fi.IsDir() {
		return false
	}
	for _, marker := range []string{".git", "AGENTS.md", "CLAUDE.md", "GEMINI.md", "goals", ".sila"} {
		if _, err := os.Stat(filepath.Join(clean, marker)); err == nil {
			return true
		}
	}
	if entries, err := os.ReadDir(clean); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".code-workspace") {
				return true
			}
		}
	}
	return false
}

// IsTempDirPath reports whether dir is inside /tmp or /var/tmp.
func IsTempDirPath(dir string) bool {
	clean := filepath.Clean(filepath.FromSlash(dir))
	return clean == "/tmp" || clean == "/var/tmp" ||
		strings.HasPrefix(clean, "/tmp/") || strings.HasPrefix(clean, "/var/tmp/")
}

// isRepositoryRoot reports whether dir is the working-tree root of its own Git
// repository: a .git directory, or a worktree pointer file. CanonicalProjectPath
// already collapses paths inside a repository to that repository's root, so a
// canonical directory carrying .git marks a repository boundary.
func isRepositoryRoot(dir string) bool {
	if dir == "" {
		return false
	}
	fi, err := os.Stat(filepath.Join(filepath.FromSlash(dir), ".git"))
	return err == nil && (fi.IsDir() || fi.Mode().IsRegular())
}

// ResolveScope resolves a project by explicit selector if provided, or from the
// given directory (falling back to current working directory).
//
// For explicit selectors:
// It looks up exact project ID, registered path, or registered directory basename.
// If the selector is ambiguous or unknown, an actionable error is returned;
// no fuzzy cross-repository substring selection or silent fallback is performed.
// A bare derived project ID is accepted with an explicit dir only when that dir
// exists and resolves to the same identity, which covers first-contact callers
// that pass the directory before the project is registered.
//
// For empty selectors (current directory):
// It resolves the directory against existing canonical project registrations
// (exact path or containing repository directory) *before* falling back to
// deriving a basename ID.
func (s *Store) ResolveScope(selector, dir string) (Project, error) {
	selector = strings.TrimSpace(selector)
	if selector != "" {
		if s == nil {
			return Project{ID: ProjectID(selector), Path: selector}, nil
		}
		// Explicit selector: must resolve to an exact registered project.
		p, err := s.ResolveProject(selector)
		if err == nil {
			return p, nil
		}
		// If selector is an existing directory on disk, check if it falls inside a registered project,
		// or resolve its scoped directory identity with collision disambiguation.
		if fi, statErr := os.Stat(selector); statErr == nil && fi.IsDir() {
			if proj, ok, pErr := s.ProjectForDir(selector); pErr == nil && ok {
				return proj, nil
			}
			return s.ResolveScope("", selector)
		}
		// A derived project ID may arrive with the resolved directory before the
		// project is registered (MCP resolves a project path, derives the
		// basename, then passes both). Accept it only when dir exists and
		// resolves to the same identity; unknown slugs still error.
		if strings.TrimSpace(dir) != "" {
			if fi, statErr := os.Stat(dir); statErr == nil && fi.IsDir() {
				if scoped, scopeErr := s.ResolveScope("", dir); scopeErr == nil &&
					strings.EqualFold(scoped.ID, selector) {
					return scoped, nil
				}
			}
		}
		return Project{}, err
	}

	// Current directory resolution
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return Project{ID: "default", Path: ""}, nil
		}
		dir = cwd
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		absDir = filepath.Clean(dir)
	}
	canonDir := CanonicalProjectPath(absDir)

	if s != nil {
		if p, ok, err := s.ProjectForDir(canonDir); err == nil && ok {
			return p, nil
		}
		if !samePath(canonDir, absDir) {
			if p, ok, err := s.ProjectForDir(absDir); err == nil && ok {
				return p, nil
			}
		}
	}

	derivedID := ProjectID(canonDir)
	if derivedID == "" || derivedID == "." || derivedID == "/" {
		derivedID = ProjectID(absDir)
	}
	if derivedID == "" || derivedID == "." || derivedID == "/" {
		derivedID = "default"
	}

	if s != nil {
		var owner string
		err := s.DB.QueryRow(`SELECT path FROM projects WHERE id=?`, derivedID).Scan(&owner)
		if err == nil && !samePath(owner, canonDir) && !samePath(owner, absDir) {
			// Sibling or colliding basename collision: another registered project owns this derivedID at a different path.
			// Disambiguate using repository remote hash or path hash to isolate memory.
			if remote := DetectGitRemoteURL(canonDir); remote != "" {
				derivedID = fmt.Sprintf("%s-%s", derivedID, RemoteHash(remote))
			} else {
				derivedID = fmt.Sprintf("%s-%s", derivedID, pathHash8(canonDir))
			}
		} else if err == sql.ErrNoRows {
			// Check if facts or handoffs already exist under derivedID from an unlinked/colliding directory
			var recordCount int
			_ = s.DB.QueryRow(`SELECT COUNT(*) FROM facts WHERE project_id=?`, derivedID).Scan(&recordCount)
			if recordCount == 0 {
				_ = s.DB.QueryRow(`SELECT COUNT(*) FROM handoffs WHERE project_id=?`, derivedID).Scan(&recordCount)
			}
			if recordCount > 0 {
				// derivedID already has un-owned or prior facts; do not contaminate this new/unregistered path
				if remote := DetectGitRemoteURL(canonDir); remote != "" {
					derivedID = fmt.Sprintf("%s-%s", derivedID, RemoteHash(remote))
				} else {
					derivedID = fmt.Sprintf("%s-%s", derivedID, pathHash8(canonDir))
				}
			}
		}
	}

	return Project{
		ID:   derivedID,
		Path: canonDir,
	}, nil
}

// ProjectID returns a collision-safe, deterministic project ID for the given path
// within the context of the store. If the path matches a registered project,
// its registered ID is returned. Otherwise, if the path shares a directory basename
// with an existing registered project or recorded memory at another location,
// it returns a path-hash disambiguated ID.
func (s *Store) ProjectID(path string) string {
	if s == nil {
		return ProjectID(path)
	}
	p, err := s.ResolveScope("", path)
	if err == nil && p.ID != "" {
		return p.ID
	}
	return ProjectID(path)
}

func samePath(a, b string) bool {
	normA := config.NormalizePath(a)
	normB := config.NormalizePath(b)
	if strings.EqualFold(normA, normB) {
		return true
	}
	absA, errA := filepath.Abs(filepath.FromSlash(normA))
	absB, errB := filepath.Abs(filepath.FromSlash(normB))
	if errA == nil && errB == nil {
		if strings.EqualFold(config.NormalizePath(absA), config.NormalizePath(absB)) {
			return true
		}
	}
	return false
}
