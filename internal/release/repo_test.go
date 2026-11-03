package release

import (
	"path/filepath"
	"testing"
)

func TestResolveGitRepoDir(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	// From a package subdirectory, the resolver should find the repository root.
	found := ResolveGitRepoDir(filepath.Join(repoRoot, "internal", "release"))
	if !filepath.IsAbs(found) {
		t.Errorf("expected absolute path, got %s", found)
	}
	if filepath.Clean(found) != filepath.Clean(repoRoot) {
		t.Errorf("expected repo root %s, got %s", repoRoot, found)
	}

	// A direct call from the repository root must resolve to itself.
	if got := ResolveGitRepoDir(repoRoot); filepath.Clean(got) != filepath.Clean(repoRoot) {
		t.Errorf("expected repo root from root call, got %s", got)
	}
}
