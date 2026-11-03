package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveWorkspaceIdentityWrapper(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	cli := filepath.Join(root, "cli")
	if err := os.MkdirAll(filepath.Join(cli, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	goalDir := filepath.Join(cli, "goals", "demo")
	if err := os.MkdirAll(goalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte("# Goal: demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sila.code-workspace"), []byte(`{"folders":[{"path":"cli"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	id, err := ResolveWorkspaceIdentity(root)
	if err != nil {
		t.Fatalf("ResolveWorkspaceIdentity: %v", err)
	}
	if id.ProjectID != "sila-cli" || id.ActiveDir != cli {
		t.Fatalf("unexpected identity: %+v", id)
	}
}

func TestResolveWorkspaceIdentityAmbiguous(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"alpha", "beta"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		goalDir := filepath.Join(dir, "goals", "demo")
		if err := os.MkdirAll(goalDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte("# Goal: demo\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "sila.code-workspace"), []byte(`{"folders":[{"path":"alpha"},{"path":"beta"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := ResolveWorkspaceIdentity(root)
	if !errors.Is(err, ErrAmbiguousWorkspace) {
		t.Fatalf("want ErrAmbiguousWorkspace, got %v", err)
	}
}
