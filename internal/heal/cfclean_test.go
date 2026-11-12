package heal

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func TestNeedsCFGitFilter(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Non-worker project returns false
	if NeedsCFGitFilter(tempDir) {
		t.Errorf("expected NeedsCFGitFilter=false for non-worker project")
	}

	// 2. Worker project without .gitattributes or git filter returns true
	wranglerPath := filepath.Join(tempDir, "wrangler.jsonc")
	if err := os.WriteFile(wranglerPath, []byte(`{"name": "test-worker"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if !NeedsCFGitFilter(tempDir) {
		t.Errorf("expected NeedsCFGitFilter=true for worker project lacking git filter")
	}

	// Initialize git repo in tempDir
	cmdInit := exec.Command("git", "init")
	cmdInit.Dir = tempDir
	if err := cmdInit.Run(); err != nil {
		t.Skip("git init failed; skipping git integration part")
	}

	// 3. EnsureCFGitFilter sets up filter and .gitattributes
	modified, err := EnsureCFGitFilter(tempDir)
	if err != nil {
		t.Fatalf("unexpected error running EnsureCFGitFilter: %v", err)
	}
	if !modified {
		t.Errorf("expected EnsureCFGitFilter to return modified=true on first run")
	}

	// Verify .gitattributes content
	attrData, err := os.ReadFile(filepath.Join(tempDir, ".gitattributes"))
	if err != nil {
		t.Fatalf("expected .gitattributes to exist: %v", err)
	}
	attrContent := string(attrData)
	if !strings.Contains(attrContent, "wrangler.jsonc filter=cfclean") {
		t.Errorf(".gitattributes missing wrangler.jsonc filter rule: %s", attrContent)
	}

	// Verify git config filter.cfclean.smudge
	cmdSmudge := exec.Command("git", "config", "filter.cfclean.smudge")
	cmdSmudge.Dir = tempDir
	smudgeOut, err := cmdSmudge.Output()
	if err != nil {
		t.Fatalf("expected filter.cfclean.smudge to be configured in git config: %v", err)
	}
	if strings.TrimSpace(string(smudgeOut)) != "cfclean smudge" {
		t.Errorf("filter.cfclean.smudge = %q, want 'cfclean smudge'", strings.TrimSpace(string(smudgeOut)))
	}

	// 4. Second run of NeedsCFGitFilter should now return false
	if NeedsCFGitFilter(tempDir) {
		t.Errorf("expected NeedsCFGitFilter=false after EnsureCFGitFilter")
	}

	// 5. If filter.cfclean.smudge is set to cat, NeedsCFGitFilter must return true
	cmdCat := exec.Command("git", "config", "filter.cfclean.smudge", "cat")
	cmdCat.Dir = tempDir
	if err := cmdCat.Run(); err != nil {
		t.Fatal(err)
	}
	if !NeedsCFGitFilter(tempDir) {
		t.Errorf("expected NeedsCFGitFilter=true when filter.cfclean.smudge=cat")
	}

	// 6. Running EnsureCFGitFilter again repairs smudge to cfclean smudge
	if _, err := EnsureCFGitFilter(tempDir); err != nil {
		t.Fatal(err)
	}
	if NeedsCFGitFilter(tempDir) {
		t.Errorf("expected NeedsCFGitFilter=false after repairing smudge filter")
	}
}

func TestBuildPlanIncludesCFClean(t *testing.T) {
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "heal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	repo := filepath.Join(root, "cf-repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "wrangler.toml"), []byte(`name = "cf-app"`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnsureProject(repo); err != nil {
		t.Fatal(err)
	}

	plan, err := BuildPlan(st, repo)
	if err != nil {
		t.Fatal(err)
	}

	foundCFClean := false
	for _, s := range plan.Steps {
		if s.ID == "cfclean" {
			foundCFClean = true
			if !s.Automatic {
				t.Errorf("expected cfclean step to be automatic")
			}
			if s.Command != "cfclean setup" && s.Command != "sila heal" {
				t.Errorf("unexpected cfclean command: %s", s.Command)
			}
		}
	}

	if !foundCFClean {
		t.Errorf("expected cfclean step in plan for Cloudflare Worker project")
	}
}
