package release

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveGitRepoDir(t *testing.T) {
	// From within cli/
	cliDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	if filepath.Base(cliDir) != "cli" {
		t.Skipf("workspace layout requires the cli directory name, got %s", filepath.Base(cliDir))
	}
	found := ResolveGitRepoDir(cliDir)
	if !filepath.IsAbs(found) {
		t.Errorf("expected absolute path, got %s", found)
	}
	if filepath.Base(found) != "cli" {
		t.Errorf("expected repo dir to end with cli, got %s", found)
	}

	// From parent workspace root
	parentDir := filepath.Dir(cliDir)
	foundParent := ResolveGitRepoDir(parentDir)
	if filepath.Base(foundParent) != "cli" {
		t.Errorf("expected parent workspace root to discover cli, got %s", foundParent)
	}
}
