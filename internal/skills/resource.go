package skills

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ResolveResourcePath resolves a relative path (e.g. "references/creating.md", "scripts/run.sh")
// within a skill's bundle directory, enforcing jailbreak and symlink containment rules.
func ResolveResourcePath(bundleDir, relPath string) (string, error) {
	cleanRel := filepath.Clean(relPath)
	if filepath.IsAbs(cleanRel) {
		return "", fmt.Errorf("absolute resource path prohibited: %q", relPath)
	}

	// Prevent directory traversal
	if cleanRel == ".." || strings.HasPrefix(cleanRel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("resource traversal outside skill bundle: %q", relPath)
	}

	// Canonical evaluation of bundle directory
	realBundle, err := filepath.EvalSymlinks(bundleDir)
	if err != nil {
		return "", fmt.Errorf("resolve bundle directory %s: %w", bundleDir, err)
	}

	targetPath := filepath.Join(realBundle, cleanRel)

	// Verify file exists
	fi, err := os.Stat(targetPath)
	if err != nil {
		return "", fmt.Errorf("resource not found: %s (%w)", relPath, err)
	}
	if fi.IsDir() {
		return "", fmt.Errorf("resource path points to a directory, not a file: %s", relPath)
	}

	// Canonical evaluation of the target resource
	realTarget, err := filepath.EvalSymlinks(targetPath)
	if err != nil {
		return "", fmt.Errorf("resolve resource symlinks %s: %w", targetPath, err)
	}

	// Strict containment check: realTarget MUST reside within realBundle
	relToBundle, err := filepath.Rel(realBundle, realTarget)
	if err != nil || relToBundle == ".." || strings.HasPrefix(relToBundle, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("security violation: resource symlink escapes skill bundle: %s -> %s", relPath, realTarget)
	}

	return realTarget, nil
}

// ScanSkillResources populates auxiliary resources (scripts, references, assets) in a skill.
func ScanSkillResources(bundleDir string, skill *Skill) {
	subdirs := []struct {
		name     string
		hasFlag  *bool
		destList *[]string
	}{
		{"scripts", &skill.HasScripts, &skill.Scripts},
		{"references", &skill.HasRefs, &skill.References},
		{"assets", &skill.HasAssets, &skill.Assets},
	}

	for _, sub := range subdirs {
		dir := filepath.Join(bundleDir, sub.name)
		var files []string

		_ = filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
			if err != nil || fi == nil || fi.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(bundleDir, p)
			if err == nil && !strings.HasPrefix(rel, "..") {
				files = append(files, filepath.ToSlash(rel))
			}
			return nil
		})

		if len(files) > 0 {
			sort.Strings(files)
			*sub.hasFlag = true
			*sub.destList = files
		}
	}
}

// ReadResourceBounded reads a resource up to maxBytes.
func ReadResourceBounded(path string, maxBytes int64) (string, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer f.Close()

	lr := io.LimitReader(f, maxBytes+1)
	b, err := io.ReadAll(lr)
	if err != nil {
		return "", false, err
	}

	truncated := false
	if int64(len(b)) > maxBytes {
		truncated = true
		b = b[:maxBytes]
	}

	return string(b), truncated, nil
}
