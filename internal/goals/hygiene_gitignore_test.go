package goals

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNeedsAndEnsureGitIgnoreClaim(t *testing.T) {
	t.Run("non git non goals repo returns false", func(t *testing.T) {
		tmp := t.TempDir()
		needs, err := NeedsGitIgnoreClaim(tmp)
		if err != nil {
			t.Fatal(err)
		}
		if needs {
			t.Errorf("expected needs == false for empty non-git dir, got true")
		}
	})

	t.Run("git repo without gitignore needs ignore", func(t *testing.T) {
		tmp := t.TempDir()
		if err := os.MkdirAll(filepath.Join(tmp, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(tmp, "goals"), 0o755); err != nil {
			t.Fatal(err)
		}
		needs, err := NeedsGitIgnoreClaim(tmp)
		if err != nil {
			t.Fatal(err)
		}
		if !needs {
			t.Errorf("expected needs == true for git repo without .gitignore")
		}

		added, err := EnsureGitIgnoreClaim(tmp)
		if err != nil {
			t.Fatal(err)
		}
		if !added {
			t.Errorf("expected added == true on first run")
		}

		content, err := os.ReadFile(filepath.Join(tmp, ".gitignore"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(content), "goals/*/claim.json") {
			t.Errorf("expected .gitignore to contain goals/*/claim.json, got:\n%s", string(content))
		}

		// Idempotency: second run must do nothing
		addedAgain, err := EnsureGitIgnoreClaim(tmp)
		if err != nil {
			t.Fatal(err)
		}
		if addedAgain {
			t.Errorf("expected addedAgain == false on second run")
		}

		needsAfter, err := NeedsGitIgnoreClaim(tmp)
		if err != nil {
			t.Fatal(err)
		}
		if needsAfter {
			t.Errorf("expected needsAfter == false once ignored")
		}
	})

	t.Run("existing gitignore with alternative pattern returns false", func(t *testing.T) {
		tmp := t.TempDir()
		if err := os.MkdirAll(filepath.Join(tmp, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(tmp, "goals"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tmp, ".gitignore"), []byte("dist/\n**/claim.json\n.sila/state/\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		needs, err := NeedsGitIgnoreClaim(tmp)
		if err != nil {
			t.Fatal(err)
		}
		if needs {
			t.Errorf("expected needs == false when **/claim.json already exists")
		}
	})
}

func TestNeedsAndEnsureGitIgnoreWorkspaceShim(t *testing.T) {
	t.Run("missing gitignore needs ignore", func(t *testing.T) {
		tmp := t.TempDir()
		needs, err := NeedsGitIgnoreWorkspaceShim(tmp)
		if err != nil {
			t.Fatal(err)
		}
		if !needs {
			t.Errorf("expected needs == true for missing .gitignore")
		}

		added, err := EnsureGitIgnoreWorkspaceShim(tmp)
		if err != nil {
			t.Fatal(err)
		}
		if !added {
			t.Errorf("expected added == true on first run")
		}

		content, err := os.ReadFile(filepath.Join(tmp, ".gitignore"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(content), ".bin/") {
			t.Errorf("expected .gitignore to contain .bin/, got:\n%s", string(content))
		}

		// Idempotency: second run must do nothing
		addedAgain, err := EnsureGitIgnoreWorkspaceShim(tmp)
		if err != nil {
			t.Fatal(err)
		}
		if addedAgain {
			t.Errorf("expected addedAgain == false on second run")
		}

		needsAfter, err := NeedsGitIgnoreWorkspaceShim(tmp)
		if err != nil {
			t.Fatal(err)
		}
		if needsAfter {
			t.Errorf("expected needsAfter == false once ignored")
		}
	})

	t.Run("existing gitignore with alternative patterns returns false", func(t *testing.T) {
		patterns := []string{".bin/", "/.bin/", ".bin", "/.bin", ".bin/*", ".bin/sila", "/.bin/sila"}
		for _, pat := range patterns {
			tmp := t.TempDir()
			if err := os.WriteFile(filepath.Join(tmp, ".gitignore"), []byte("node_modules/\n"+pat+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			needs, err := NeedsGitIgnoreWorkspaceShim(tmp)
			if err != nil {
				t.Fatal(err)
			}
			if needs {
				t.Errorf("pattern %q in .gitignore should have satisfied check", pat)
			}
		}
	})
}

func TestNeedsGitIgnoreClaimWhenStateDirMissing(t *testing.T) {
	tmp := t.TempDir()
	for _, d := range []string{".git", "goals"} {
		if err := os.MkdirAll(filepath.Join(tmp, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(tmp, ".gitignore"), []byte("goals/*/claim.json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	needs, err := NeedsGitIgnoreClaim(tmp)
	if err != nil || !needs {
		t.Fatalf("expected needs=true when .sila/state/ is not ignored: %v %v", needs, err)
	}
	added, err := EnsureGitIgnoreClaim(tmp)
	if err != nil || !added {
		t.Fatalf("expected ensure to add state rule: %v %v", added, err)
	}
	data, _ := os.ReadFile(filepath.Join(tmp, ".gitignore"))
	if strings.Count(string(data), "goals/*/claim.json") != 1 || !strings.Contains(string(data), ".sila/state/") {
		t.Fatalf("unexpected .gitignore:\n%s", data)
	}
}
