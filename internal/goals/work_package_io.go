package goals

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	kebabCaseSlugRegex = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

	// ErrWorkPackageNotFound indicates that work-package.json is absent.
	ErrWorkPackageNotFound = os.ErrNotExist
)

// WorkPackagePath returns the canonical filesystem path for a goal's work-package.json.
func WorkPackagePath(rootDir, slug string) string {
	return filepath.Join(resolveGoalsRootDir(rootDir), "goals", slug, "work-package.json")
}

func resolveWorkPackageRootDir(rootDir string) string {
	if rootDir == "" || rootDir == "." {
		return resolveGoalsRootDir(rootDir)
	}
	cleanRoot := filepath.Clean(rootDir)
	if fi, err := os.Stat(filepath.Join(cleanRoot, "goals")); err == nil && fi.IsDir() {
		return cleanRoot
	}
	resolved := resolveGoalsRootDir(cleanRoot)
	if strings.HasPrefix(resolved, filepath.Join(cleanRoot, "internal")) {
		return cleanRoot
	}
	return resolved
}

// LoadWorkPackage reads and strictly decodes a goal's work-package.json.
// If the file is missing, it returns an error wrapping os.ErrNotExist so callers
// can distinguish legacy goals from corrupt structured packages.
func LoadWorkPackage(rootDir, slug string) (*WorkPackage, error) {
	if !kebabCaseSlugRegex.MatchString(slug) {
		return nil, fmt.Errorf("invalid goal slug %q: must be a single kebab-case component", slug)
	}

	rootDir = resolveWorkPackageRootDir(rootDir)
	pkgPath := WorkPackagePath(rootDir, slug)

	data, err := os.ReadFile(pkgPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: work-package.json not found for goal %s", ErrWorkPackageNotFound, slug)
		}
		return nil, fmt.Errorf("read work package %s: %w", pkgPath, err)
	}

	pkg, err := DecodeWorkPackage(data)
	if err != nil {
		return nil, fmt.Errorf("corrupt or invalid work package in %s: %w", pkgPath, err)
	}

	return pkg, nil
}

// SaveWorkPackage validates and persists a WorkPackage to goals/<slug>/work-package.json.
// It acquires the goal lock, enforces slug format, ensures referenced paths do not
// escape the repository root, verifies monotonic revision increments, and writes
// atomically using AtomicWriteFile.
func SaveWorkPackage(rootDir, slug string, pkg *WorkPackage) error {
	if !kebabCaseSlugRegex.MatchString(slug) {
		return fmt.Errorf("invalid goal slug %q: must be a single kebab-case component", slug)
	}
	if pkg == nil {
		return fmt.Errorf("nil work package")
	}
	if pkg.GoalSlug != slug {
		return fmt.Errorf("goal_slug mismatch: package has %q, target is %q", pkg.GoalSlug, slug)
	}

	rootDir = resolveWorkPackageRootDir(rootDir)
	goalDir := filepath.Join(rootDir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		return fmt.Errorf("create goal directory: %w", err)
	}

	unlock, err := acquireGoalLock(goalDir)
	if err != nil {
		return fmt.Errorf("acquire goal lock: %w", err)
	}
	defer unlock()

	// 1. Structural domain validation
	if err := ValidateWorkPackage(pkg); err != nil {
		return fmt.Errorf("validate work package: %w", err)
	}

	// 2. Path boundary validation against repository root
	if err := validatePackagePaths(rootDir, pkg); err != nil {
		return fmt.Errorf("path boundary validation failed: %w", err)
	}

	// 3. Revision conflict check against stored package
	pkgPath := filepath.Join(goalDir, "work-package.json")
	existingData, err := os.ReadFile(pkgPath)
	if err == nil {
		existingPkg, decErr := DecodeWorkPackage(existingData)
		if decErr != nil {
			return fmt.Errorf("stored work-package.json is corrupt: %w", decErr)
		}
		if pkg.Revision != existingPkg.Revision+1 {
			return fmt.Errorf("revision conflict: cannot save revision %d over stored revision %d (replacement requires revision %d)",
				pkg.Revision, existingPkg.Revision, existingPkg.Revision+1)
		}
	} else if os.IsNotExist(err) {
		if pkg.Revision != 1 {
			return fmt.Errorf("initial work package must have revision 1, got %d", pkg.Revision)
		}
	} else {
		return fmt.Errorf("inspect existing work package: %w", err)
	}

	// 4. Formatted serialization
	data, err := json.MarshalIndent(pkg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal work package: %w", err)
	}
	data = append(data, '\n')

	// 5. Atomic write
	if err := AtomicWriteFile(pkgPath, data, 0644); err != nil {
		return fmt.Errorf("atomic write work package: %w", err)
	}

	return nil
}

func validatePackagePaths(rootDir string, pkg *WorkPackage) error {
	cleanRoot, err := filepath.Abs(rootDir)
	if err != nil {
		return fmt.Errorf("resolve rootDir abs: %w", err)
	}
	if evalRoot, err := filepath.EvalSymlinks(cleanRoot); err == nil {
		cleanRoot = evalRoot
	}

	for _, t := range pkg.Tasks {
		for _, p := range t.Files.Read {
			if err := checkContainedPath(cleanRoot, p, false, false); err != nil {
				return fmt.Errorf("task %s read path %w", t.ID, err)
			}
		}
		for _, p := range t.Files.Edit {
			if err := checkContainedPath(cleanRoot, p, false, false); err != nil {
				return fmt.Errorf("task %s edit path %w", t.ID, err)
			}
		}
		for _, p := range t.Files.Create {
			if err := checkContainedPath(cleanRoot, p, false, true); err != nil {
				return fmt.Errorf("task %s create path %w", t.ID, err)
			}
		}
		for _, p := range t.Files.Forbid {
			if err := checkContainedPath(cleanRoot, p, false, false); err != nil {
				return fmt.Errorf("task %s forbid path %w", t.ID, err)
			}
		}
		for _, r := range t.References {
			if err := checkContainedPath(cleanRoot, r.Path, false, false); err != nil {
				return fmt.Errorf("task %s reference path %w", t.ID, err)
			}
		}
	}
	return nil
}

func checkContainedPath(cleanRoot, relPath string, mustExist, checkParentForNew bool) error {
	cleanRel := filepath.Clean(relPath)
	if filepath.IsAbs(cleanRel) {
		return fmt.Errorf("%q: absolute paths are forbidden", relPath)
	}
	if cleanRel == "." || cleanRel == ".." || strings.HasPrefix(cleanRel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%q: path escapes repository root", relPath)
	}

	fullPath := filepath.Join(cleanRoot, cleanRel)

	if mustExist {
		evalPath, err := filepath.EvalSymlinks(fullPath)
		if err != nil {
			return fmt.Errorf("%q: unable to evaluate symlinks: %w", relPath, err)
		}
		rel, err := filepath.Rel(cleanRoot, evalPath)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("%q: symlink target escapes repository root", relPath)
		}
	} else if checkParentForNew {
		parentDir := filepath.Dir(fullPath)
		evalParent, err := filepath.EvalSymlinks(parentDir)
		if err != nil {
			return fmt.Errorf("%q: parent directory does not exist: %w", relPath, err)
		}
		rel, err := filepath.Rel(cleanRoot, evalParent)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("%q: parent directory escapes repository root", relPath)
		}
	} else {
		// Walk up from fullPath to verify that any existing ancestor or leaf does not escape repository root
		curr := fullPath
		for {
			if _, err := os.Lstat(curr); err == nil {
				evalPath, err := filepath.EvalSymlinks(curr)
				if err != nil {
					return fmt.Errorf("%q: invalid symlink at %q: %w", relPath, curr, err)
				}
				rel, err := filepath.Rel(cleanRoot, evalPath)
				if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					return fmt.Errorf("%q: symlink target escapes repository root", relPath)
				}
				break
			}
			parent := filepath.Dir(curr)
			if parent == curr || len(parent) < len(cleanRoot) {
				break
			}
			curr = parent
		}
	}
	return nil
}

// IsWorkPackageNotFound reports whether err indicates a missing work-package.json.
func IsWorkPackageNotFound(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, ErrWorkPackageNotFound)
}
