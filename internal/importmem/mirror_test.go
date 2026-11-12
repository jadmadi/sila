package importmem

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mirrorFixture(t *testing.T) (home, repo string) {
	t.Helper()
	home = t.TempDir()
	repo = t.TempDir()
	mustWrite := func(path string, data []byte) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(filepath.Join(home, ".claude", "projects", "proj", "memory", "note.md"), []byte("# note\n"))
	mustWrite(filepath.Join(home, ".ssh", "id_rsa"), []byte("secret"))
	mustWrite(filepath.Join(repo, ".codex", "memories", "m.md"), []byte("# codex\n"))
	mustWrite(filepath.Join(home, ".claude", "projects", "proj", "memory", "big.md"), make([]byte, MirrorMaxBytes+1))
	mustWrite(filepath.Join(home, ".claude", "projects", "proj", "memory", "note.txt"), []byte("text"))
	if err := os.Symlink(filepath.Join(home, ".ssh", "id_rsa"), filepath.Join(home, ".claude", "projects", "proj", "memory", "escape.md")); err != nil {
		t.Fatal(err)
	}
	return home, repo
}

func TestClassifyClaudeHomeMemory(t *testing.T) {
	home, repo := mirrorFixture(t)
	got, ok := ClassifyMirrorPath(home, repo, filepath.Join(home, ".claude", "projects", "proj", "memory", "note.md"))
	if !ok || got.Harness != "claude" {
		t.Fatalf("got %+v ok=%v", got, ok)
	}
	if !filepath.IsAbs(got.Path) {
		t.Fatalf("path is not absolute: %q", got.Path)
	}
}

func TestClassifyRefusesOutsidePaths(t *testing.T) {
	home, repo := mirrorFixture(t)
	for _, path := range []string{
		filepath.Join(home, ".ssh", "id_rsa"),
		filepath.Join(home, ".claude", "projects", "proj", "memory", "escape.md"),
		filepath.Join(home, ".claude", "projects", "proj", "memory", "note.txt"),
		filepath.Join(home, ".claude", "projects", "proj", "memory", "big.md"),
		filepath.Join(home, ".claude", "projects", "proj", "memory", "..", "..", "..", "sibling.md"),
	} {
		if got, ok := ClassifyMirrorPath(home, repo, path); ok {
			t.Fatalf("%s classified as %+v", path, got)
		}
	}
}

func TestClassifyRepoCodexMemory(t *testing.T) {
	home, repo := mirrorFixture(t)
	got, ok := ClassifyMirrorPath(home, repo, filepath.Join(repo, ".codex", "memories", "m.md"))
	if !ok || got.Harness != "codex" {
		t.Fatalf("got %+v ok=%v", got, ok)
	}
}

func TestClassifyMissingRootIsNotAnError(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	if _, ok := ClassifyMirrorPath(home, repo, filepath.Join(home, ".claude", "memory", "x.md")); ok {
		t.Fatal("missing root classified a file")
	}
}

func TestMirrorRootsCoverDiscoveryPatterns(t *testing.T) {
	specs := map[string]bool{}
	for _, spec := range MirrorRoots {
		specs[spec.Harness] = true
	}
	for _, want := range []string{"claude", "codex", "grok"} {
		if !specs[want] {
			t.Fatalf("missing harness %q in mirror roots", want)
		}
	}
	if strings.ToLower(MirrorPathEscape) != "mirror_path_escape" {
		t.Fatalf("unstable code %q", MirrorPathEscape)
	}
}
