// Package workspace resolves the active project identity for a directory that
// may be a workspace root, a child repository, or a subdirectory of either.
// It is deliberately storage-free so both the store and the hook loader can use
// it without an import cycle.
package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/jadmadi/sila/internal/config"
)

// ErrAmbiguous is returned when a workspace root contains more than one child
// repository with goal packages and no single preferred child.
var ErrAmbiguous = errors.New("ambiguous workspace: multiple child repositories have goals")

// Identity is the resolved workspace identity for a directory.
type Identity struct {
	WorkspaceRoot string   `json:"workspace_root"`
	WorkspaceID   string   `json:"workspace_id"`
	ActiveDir     string   `json:"active_dir"`
	ProjectID     string   `json:"project_id"`
	Source        string   `json:"source"` // root-goals|single-child|preferred-child|repo|registry|derived
	Candidates    []string `json:"candidates,omitempty"`
	Ambiguous     bool     `json:"ambiguous"`
}

var nonSlug = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// Slugify converts an arbitrary string into a clean alphanumeric slug while
// preserving dots and underscores (project IDs such as example.workspace.app).
func Slugify(s string) string {
	return strings.Trim(nonSlug.ReplaceAllString(s, "-"), "-")
}

// ProjectID derives a stable, human-friendly id from a path. It does not
// consult the store; callers that need collision disambiguation use
// store.(*Store).ProjectID instead.
func ProjectID(path string) string {
	if wsID, ok := DetectProjectID(path); ok && wsID != "" {
		return wsID
	}
	norm := config.NormalizePath(path)
	norm = strings.TrimRight(norm, "/")
	idx := strings.LastIndex(norm, "/")
	dir := norm
	if idx >= 0 {
		dir = norm[idx+1:]
	}
	slug := Slugify(dir)
	if slug == "" {
		slug = "project"
	}
	return slug
}

// CanonicalRoot returns the nearest repository root for path. If path is not
// inside a repository, it returns the absolute path. Worktree pointer files are
// treated as repository roots; callers that need main-worktree collapse use
// store.CanonicalProjectPath.
func CanonicalRoot(path string) string {
	if strings.TrimSpace(path) == "" {
		return path
	}
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		abs = filepath.Clean(path)
	}
	cur := abs
	for {
		if _, err := os.Stat(filepath.Join(cur, ".git")); err == nil {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur || parent == "." || parent == string(filepath.Separator) {
			break
		}
		cur = parent
	}
	return abs
}

// Resolve returns the active project identity for dir.
func Resolve(dir string) (Identity, error) {
	if strings.TrimSpace(dir) == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return Identity{}, err
		}
		dir = cwd
	}
	abs, err := filepath.Abs(filepath.Clean(dir))
	if err != nil {
		abs = filepath.Clean(dir)
	}

	// A .code-workspace file is the workspace boundary. Do not canonicalize
	// above it: a workspace root may live inside a larger monorepo with its own
	// .git, and collapsing to that parent would lose the child repositories.
	root, wsID, hasWorkspace := findWorkspaceRoot(abs)
	canon := abs
	if hasWorkspace {
		if samePath(abs, root) {
			canon = root
		} else if repo := repoRootWithin(abs, root); repo != "" {
			canon = repo
		}
	} else {
		canon = CanonicalRoot(abs)
	}
	id := Identity{ActiveDir: canon}

	// 1. The canonical directory itself owns goal packages.
	if hasGoalPackages(canon) {
		id.WorkspaceRoot = canon
		id.WorkspaceID = ProjectID(canon)
		id.ProjectID = ProjectID(canon)
		id.Source = "root-goals"
		return id, nil
	}

	if !hasWorkspace {
		id.WorkspaceRoot = canon
		id.WorkspaceID = ProjectID(canon)
		id.ProjectID = ProjectID(canon)
		id.Source = "derived"
		return id, nil
	}
	id.WorkspaceRoot = root
	id.WorkspaceID = wsID

	// 2. A specific child repository keeps its own identity; never jump to a
	// sibling repository.
	if !samePath(canon, root) && isRepoRoot(canon) {
		id.ActiveDir = canon
		id.ProjectID = ProjectID(canon)
		id.Source = "repo"
		return id, nil
	}

	// 3. The workspace root (or a non-repo subdirectory of it) selects a child
	// repository that owns goals.
	candidates := childGoalRepositories(root)
	switch len(candidates) {
	case 1:
		id.ActiveDir = candidates[0]
		id.ProjectID = ProjectID(candidates[0])
		id.Source = "single-child"
		return id, nil
	case 0:
		id.ActiveDir = canon
		id.ProjectID = ProjectID(canon)
		id.Source = "derived"
		return id, nil
	default:
		preferred := []string{"cli", "core", "engine", "main", "app"}
		var hits []string
		for _, c := range candidates {
			base := strings.ToLower(filepath.Base(c))
			for _, p := range preferred {
				if base == p {
					hits = append(hits, c)
					break
				}
			}
		}
		if len(hits) == 1 {
			id.ActiveDir = hits[0]
			id.ProjectID = ProjectID(hits[0])
			id.Source = "preferred-child"
			return id, nil
		}
		id.ActiveDir = canon
		id.ProjectID = ProjectID(canon)
		id.Candidates = candidates
		id.Ambiguous = true
		return id, fmt.Errorf("%w: %s", ErrAmbiguous, strings.Join(candidates, ", "))
	}
}

func hasGoalPackages(dir string) bool {
	if dir == "" {
		return false
	}
	entries, err := os.ReadDir(filepath.Join(dir, "goals"))
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, "goals", e.Name(), "goal.md")); err == nil {
			return true
		}
	}
	return false
}

// repoRootWithin returns the nearest repository root between dir and boundary,
// inclusive, without leaving the workspace boundary.
func repoRootWithin(dir, boundary string) string {
	cur := dir
	for {
		if isRepoRoot(cur) {
			return cur
		}
		if samePath(cur, boundary) {
			return ""
		}
		parent := filepath.Dir(cur)
		if parent == cur || !(samePath(parent, boundary) || isSubdir(parent, boundary)) {
			return ""
		}
		cur = parent
	}
}

func childGoalRepositories(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		switch e.Name() {
		case "node_modules", "dist", "vendor", "build", "target":
			continue
		}
		child := filepath.Join(root, e.Name())
		if !isRepoRoot(child) || !hasGoalPackages(child) {
			continue
		}
		abs, err := filepath.Abs(child)
		if err != nil {
			abs = child
		}
		out = append(out, abs)
	}
	sort.Strings(out)
	return out
}

func findWorkspaceRoot(dir string) (root, wsID string, ok bool) {
	cur := dir
	for {
		entries, err := os.ReadDir(cur)
		if err == nil {
			var files []string
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".code-workspace") {
					files = append(files, e.Name())
				}
			}
			sort.Strings(files)
			if len(files) > 0 {
				base := strings.TrimSuffix(files[0], ".code-workspace")
				slug := Slugify(base)
				if slug == "" {
					slug = "workspace"
				}
				return cur, slug, true
			}
		}
		parent := filepath.Dir(cur)
		if parent == cur || parent == "." || parent == string(filepath.Separator) {
			return "", "", false
		}
		cur = parent
	}
}

// DetectProjectID inspects parent and ancestor directories for a
// *.code-workspace file and derives a workspace-prefixed project ID.
func DetectProjectID(path string) (string, bool) {
	if strings.TrimSpace(path) == "" {
		return "", false
	}
	absPath, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		absPath = filepath.Clean(path)
	}
	if fi, err := os.Stat(absPath); err == nil && !fi.IsDir() {
		absPath = filepath.Dir(absPath)
	}

	cur := absPath
	for hops := 0; hops < 4; hops++ {
		entries, err := os.ReadDir(cur)
		if err == nil {
			for _, entry := range entries {
				if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".code-workspace") {
					continue
				}
				if projID, ok := matchCodeWorkspaceFile(filepath.Join(cur, entry.Name()), cur, absPath); ok {
					return projID, true
				}
			}
		}
		parent := filepath.Dir(cur)
		if parent == cur || parent == "." || parent == string(filepath.Separator) {
			break
		}
		cur = parent
	}
	return "", false
}

func matchCodeWorkspaceFile(wsFilePath, wsDir, targetPath string) (string, bool) {
	data, err := os.ReadFile(wsFilePath)
	if err != nil {
		return "", false
	}
	var ws struct {
		Folders []struct {
			Path string `json:"path"`
		} `json:"folders"`
	}
	if err := json.Unmarshal(data, &ws); err != nil || len(ws.Folders) == 0 {
		return "", false
	}
	wsBaseName := strings.TrimSuffix(filepath.Base(wsFilePath), ".code-workspace")
	wsSlug := Slugify(wsBaseName)
	if wsSlug == "" {
		wsSlug = "workspace"
	}
	if samePath(targetPath, wsDir) {
		return wsSlug, true
	}
	for _, f := range ws.Folders {
		folderPath := f.Path
		if !filepath.IsAbs(folderPath) {
			folderPath = filepath.Clean(filepath.Join(wsDir, folderPath))
		}
		if samePath(folderPath, wsDir) {
			continue
		}
		if samePath(folderPath, targetPath) || isSubdir(targetPath, folderPath) {
			folderSlug := Slugify(filepath.Base(folderPath))
			if folderSlug == "" {
				folderSlug = "sub"
			}
			if strings.HasPrefix(folderSlug, wsSlug+"-") {
				return folderSlug, true
			}
			if folderSlug == wsSlug {
				return wsSlug, true
			}
			return fmt.Sprintf("%s-%s", wsSlug, folderSlug), true
		}
	}
	return "", false
}

func isRepoRoot(dir string) bool {
	fi, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil && (fi.IsDir() || fi.Mode().IsRegular())
}

func isSubdir(sub, parent string) bool {
	rel, err := filepath.Rel(parent, sub)
	if err != nil {
		return false
	}
	return rel != "." && !strings.HasPrefix(rel, "..") && !strings.HasPrefix(rel, "/")
}

func samePath(a, b string) bool {
	absA, errA := filepath.Abs(filepath.FromSlash(config.NormalizePath(a)))
	absB, errB := filepath.Abs(filepath.FromSlash(config.NormalizePath(b)))
	if errA != nil || errB != nil {
		return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	return strings.EqualFold(filepath.Clean(absA), filepath.Clean(absB))
}
