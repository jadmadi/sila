package store

import (
	"os"
	"path/filepath"
	"testing"
)

// buildUmbrellaWorkspace mirrors the live registry false positive: a container
// directory holding a workspace file whose slug names an unrelated project
// (2026-10-02: /workspace/github carried a stale example-client.com.code-workspace
// and registered as "example-client.com"), with a distinct nested Git repository
// underneath it.
func buildUmbrellaWorkspace(t *testing.T) (umbrella, nestedRepo string) {
	t.Helper()
	root := t.TempDir()
	umbrella = filepath.Join(root, "github")
	nestedRepo = filepath.Join(umbrella, "projects", "waqftech", "example-project.com")
	if err := os.MkdirAll(filepath.Join(nestedRepo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	ws := `{"folders":[{"path":"clients/example-client.com"}],"settings":{}}`
	if err := os.WriteFile(filepath.Join(umbrella, "example-client.com.code-workspace"), []byte(ws), 0o644); err != nil {
		t.Fatal(err)
	}
	return umbrella, nestedRepo
}

// TestProjectForDirDoesNotInheritAcrossRepositoryBoundary is the regression for
// the live false positive: a distinct Git repository nested under a broadly
// registered container directory must not adopt the container project's
// identity. Repository boundaries are project boundaries; an unregistered
// repository derives its own identity instead.
func TestProjectForDirDoesNotInheritAcrossRepositoryBoundary(t *testing.T) {
	umbrella, nestedRepo := buildUmbrellaWorkspace(t)

	s, err := Open(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// The container registers with the workspace file's slug, not its dir name.
	pUmbrella, err := s.EnsureProject(umbrella)
	if err != nil {
		t.Fatal(err)
	}
	if pUmbrella.ID != "example-client.com" {
		t.Fatalf("umbrella id = %q, want example-client.com (workspace slug, mirrors the live row)", pUmbrella.ID)
	}

	if match, ok, err := s.ProjectForDir(nestedRepo); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Fatalf("nested repository resolved to project %q; a repository boundary must not inherit the umbrella's identity", match.ID)
	}

	// A plain directory under the container is still a subdirectory of the
	// registered project and keeps inheriting.
	plain := filepath.Join(umbrella, "t")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	match, ok, err := s.ProjectForDir(plain)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || match.ID != pUmbrella.ID {
		t.Fatalf("plain subdirectory match = (%+v, %v), want (%s, true)", match, ok, pUmbrella.ID)
	}
}
