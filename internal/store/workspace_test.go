package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectWorkspaceProjectID(t *testing.T) {
	tmp := t.TempDir()

	// Create a mock multi-root workspace layout:
	// tmp/
	//   sila.code-workspace
	//   cli/
	//     cmd/sila/
	//   worker/
	//   research/
	wsPath := filepath.Join(tmp, "sila.code-workspace")
	wsContent := `{
  "folders": [
    {
      "name": "CLI (Go Engine)",
      "path": "cli"
    },
    {
      "name": "Worker (Cloudflare Edge)",
      "path": "worker"
    },
    {
      "name": "Docs (Public Docs)",
      "path": "docs"
    }
  ]
}`
	if err := os.WriteFile(wsPath, []byte(wsContent), 0o644); err != nil {
		t.Fatal(err)
	}

	cliDir := filepath.Join(tmp, "cli")
	subCliDir := filepath.Join(tmp, "cli", "cmd", "sila")
	workerDir := filepath.Join(tmp, "worker")
	docsDir := filepath.Join(tmp, "docs")
	for _, d := range []string{cliDir, subCliDir, workerDir, docsDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// 1. Workspace root itself
	id, ok := DetectWorkspaceProjectID(tmp)
	if !ok || id != "sila" {
		t.Errorf("DetectWorkspaceProjectID(root) = (%q, %v), want (%q, true)", id, ok, "sila")
	}
	if got := ProjectID(tmp); got != "sila" {
		t.Errorf("ProjectID(root) = %q, want %q", got, "sila")
	}

	// 2. Direct workspace folder: cli
	id, ok = DetectWorkspaceProjectID(cliDir)
	if !ok || id != "sila-cli" {
		t.Errorf("DetectWorkspaceProjectID(cli) = (%q, %v), want (%q, true)", id, ok, "sila-cli")
	}
	if got := ProjectID(cliDir); got != "sila-cli" {
		t.Errorf("ProjectID(cli) = %q, want %q", got, "sila-cli")
	}

	// 3. Nested subdirectory inside cli: cli/cmd/sila
	id, ok = DetectWorkspaceProjectID(subCliDir)
	if !ok || id != "sila-cli" {
		t.Errorf("DetectWorkspaceProjectID(subCliDir) = (%q, %v), want (%q, true)", id, ok, "sila-cli")
	}
	if got := ProjectID(subCliDir); got != "sila-cli" {
		t.Errorf("ProjectID(subCliDir) = %q, want %q", got, "sila-cli")
	}

	// 4. Other declared folders: worker & docs
	if got := ProjectID(workerDir); got != "sila-worker" {
		t.Errorf("ProjectID(worker) = %q, want %q", got, "sila-worker")
	}
	if got := ProjectID(docsDir); got != "sila-docs" {
		t.Errorf("ProjectID(docs) = %q, want %q", got, "sila-docs")
	}

	// 5. Unrelated folder outside workspace
	outsideDir := t.TempDir()
	id, ok = DetectWorkspaceProjectID(outsideDir)
	if ok || id != "" {
		t.Errorf("DetectWorkspaceProjectID(outside) = (%q, %v), want ('', false)", id, ok)
	}
}

func TestDetectWorkspaceProjectID_DotRoot(t *testing.T) {
	tmp := t.TempDir()
	wsPath := filepath.Join(tmp, "example.workspace.app.code-workspace")
	wsContent := `{"folders": [{"path": "."}]}`
	if err := os.WriteFile(wsPath, []byte(wsContent), 0o644); err != nil {
		t.Fatal(err)
	}
	subDir := filepath.Join(tmp, "apps", "web")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	id, ok := DetectWorkspaceProjectID(tmp)
	if !ok || id != "example.workspace.app" {
		t.Errorf("DetectWorkspaceProjectID(dotRoot) = (%q, %v), want (%q, true)", id, ok, "example.workspace.app")
	}
	if got := ProjectID(tmp); got != "example.workspace.app" {
		t.Errorf("ProjectID(dotRoot) = %q, want %q", got, "example.workspace.app")
	}
}

func TestDetectWorkspaceProjectID_AlreadyPrefixed(t *testing.T) {
	tmp := t.TempDir()
	wsPath := filepath.Join(tmp, "sila.code-workspace")
	wsContent := `{"folders": [{"path": "sila-cli"}]}`
	if err := os.WriteFile(wsPath, []byte(wsContent), 0o644); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(tmp, "sila-cli")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}

	id, ok := DetectWorkspaceProjectID(folder)
	if !ok || id != "sila-cli" {
		t.Errorf("DetectWorkspaceProjectID(alreadyPrefixed) = (%q, %v), want (%q, true)", id, ok, "sila-cli")
	}
}
