package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func workspaceFixture(t *testing.T, names ...string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"folders":[`
	for i, name := range names {
		if i > 0 {
			body += ","
		}
		body += `{"path":"` + name + `"}`
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
	body += `]}`
	if err := os.WriteFile(filepath.Join(root, "sila.code-workspace"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestRunWorkspaceStatusJSON(t *testing.T) {
	root := workspaceFixture(t, "cli")
	ui := render.New(os.Stdout, true)
	var code int
	out := captureStdout(t, func() {
		code = runWorkspace(ui, root, true, []string{"status"})
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	var id store.WorkspaceIdentity
	if err := json.Unmarshal([]byte(out), &id); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if id.ProjectID != "sila-cli" || id.ActiveDir != filepath.Join(root, "cli") {
		t.Fatalf("unexpected identity: %+v", id)
	}
}

func TestRunWorkspaceStatusAmbiguous(t *testing.T) {
	root := workspaceFixture(t, "alpha", "beta")
	ui := render.New(os.Stdout, true)
	var code int
	out := captureStdout(t, func() {
		code = runWorkspace(ui, root, true, []string{"status"})
	})
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	var id store.WorkspaceIdentity
	if err := json.Unmarshal([]byte(out), &id); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if !id.Ambiguous || len(id.Candidates) != 2 {
		t.Fatalf("unexpected ambiguity: %+v", id)
	}
}

func TestRunWorkspaceExplicitProject(t *testing.T) {
	root := workspaceFixture(t, "cli")
	ui := render.New(os.Stdout, true)
	var code int
	out := captureStdout(t, func() {
		code = runWorkspace(ui, filepath.Join(root, "cli"), true, []string{"status"})
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	var id store.WorkspaceIdentity
	if err := json.Unmarshal([]byte(out), &id); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if id.ProjectID != "sila-cli" || id.Source != "root-goals" {
		t.Fatalf("explicit project did not win: %+v", id)
	}
}

func TestRunWorkspaceUsage(t *testing.T) {
	ui := render.New(os.Stdout, true)
	var code int
	_ = captureStdout(t, func() {
		code = runWorkspace(ui, "", true, nil)
	})
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}
