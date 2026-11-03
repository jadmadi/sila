package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeGoal(t *testing.T, dir, slug string) {
	t.Helper()
	goalDir := filepath.Join(dir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0o755); err != nil {
		t.Fatalf("mkdir goal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte("# Goal: "+slug+"\n"), 0o644); err != nil {
		t.Fatalf("write goal: %v", err)
	}
}

func initRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
}

func writeWorkspace(t *testing.T, root, name string, folders ...string) {
	t.Helper()
	body := `{"folders":[`
	for i, f := range folders {
		if i > 0 {
			body += ","
		}
		body += `{"path":"` + f + `"}`
	}
	body += `]}`
	if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write workspace: %v", err)
	}
}

func TestResolveSingleChild(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	writeWorkspace(t, root, "sila.code-workspace", "cli")
	cli := filepath.Join(root, "cli")
	initRepo(t, cli)
	writeGoal(t, cli, "demo")

	id, err := Resolve(root)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if id.ProjectID != "sila-cli" || id.ActiveDir != cli || id.Source != "single-child" {
		t.Fatalf("unexpected identity: %+v", id)
	}
	if id.WorkspaceID != "sila" || id.Ambiguous {
		t.Fatalf("unexpected workspace fields: %+v", id)
	}
}

func TestResolvePreferredChild(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	writeWorkspace(t, root, "sila.code-workspace", "alpha", "cli")
	for _, name := range []string{"alpha", "cli"} {
		dir := filepath.Join(root, name)
		initRepo(t, dir)
		writeGoal(t, dir, "demo")
	}

	id, err := Resolve(root)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if id.ProjectID != "sila-cli" || id.Source != "preferred-child" {
		t.Fatalf("unexpected identity: %+v", id)
	}
}

func TestResolveAmbiguous(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	writeWorkspace(t, root, "sila.code-workspace", "alpha", "beta")
	for _, name := range []string{"alpha", "beta"} {
		dir := filepath.Join(root, name)
		initRepo(t, dir)
		writeGoal(t, dir, "demo")
	}

	id, err := Resolve(root)
	if !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("want ErrAmbiguous, got %v", err)
	}
	if !id.Ambiguous || len(id.Candidates) != 2 {
		t.Fatalf("unexpected ambiguity fields: %+v", id)
	}
	if id.Candidates[0] >= id.Candidates[1] {
		t.Fatalf("candidates not sorted: %v", id.Candidates)
	}
}

func TestResolveRootGoals(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	writeWorkspace(t, root, "sila.code-workspace", "cli")
	writeGoal(t, root, "root-demo")

	id, err := Resolve(root)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if id.ProjectID != "sila" || id.Source != "root-goals" {
		t.Fatalf("unexpected identity: %+v", id)
	}
}

func TestResolveSpecificChildDoesNotJumpToSibling(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	writeWorkspace(t, root, "sila.code-workspace", "cli", "worker")
	cli := filepath.Join(root, "cli")
	initRepo(t, cli)
	writeGoal(t, cli, "demo")
	worker := filepath.Join(root, "worker")
	initRepo(t, worker)

	id, err := Resolve(worker)
	if err != nil {
		t.Fatalf("Resolve(worker): %v", err)
	}
	if id.ActiveDir != worker || id.Source != "repo" {
		t.Fatalf("worker should not resolve to a sibling: %+v", id)
	}
}

func TestResolveSubdirectoryCollapsesToRepo(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	writeWorkspace(t, root, "sila.code-workspace", "cli")
	cli := filepath.Join(root, "cli")
	initRepo(t, cli)
	writeGoal(t, cli, "demo")
	sub := filepath.Join(cli, "internal", "store")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	id, err := Resolve(sub)
	if err != nil {
		t.Fatalf("Resolve(sub): %v", err)
	}
	if id.ActiveDir != cli || id.ProjectID != "sila-cli" {
		t.Fatalf("subdirectory did not collapse to repo: %+v", id)
	}
}

func TestResolveNoWorkspaceDerivesDir(t *testing.T) {
	root := t.TempDir()
	id, err := Resolve(root)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if id.Source != "derived" || id.ProjectID == "" {
		t.Fatalf("unexpected identity: %+v", id)
	}
}
