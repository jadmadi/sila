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
	//   example.code-workspace
	//   cli/
	//     cmd/sila/
	//   worker/
	//   research/
	wsPath := filepath.Join(tmp, "example.code-workspace")
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
      "name": "Research (Private Docs)",
      "path": "research"
    }
  ]
}`
	if err := os.WriteFile(wsPath, []byte(wsContent), 0o644); err != nil {
		t.Fatal(err)
	}

	cliDir := filepath.Join(tmp, "cli")
	subCliDir := filepath.Join(tmp, "cli", "cmd", "example")
	workerDir := filepath.Join(tmp, "worker")
	researchDir := filepath.Join(tmp, "research")
	for _, d := range []string{cliDir, subCliDir, workerDir, researchDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// 1. Workspace root itself
	id, ok := DetectWorkspaceProjectID(tmp)
	if !ok || id != "example" {
		t.Errorf("DetectWorkspaceProjectID(root) = (%q, %v), want (%q, true)", id, ok, "example")
	}
	if got := ProjectID(tmp); got != "example" {
		t.Errorf("ProjectID(root) = %q, want %q", got, "example")
	}

	// 2. Direct workspace folder: cli
	id, ok = DetectWorkspaceProjectID(cliDir)
	if !ok || id != "example-cli" {
		t.Errorf("DetectWorkspaceProjectID(cli) = (%q, %v), want (%q, true)", id, ok, "example-cli")
	}
	if got := ProjectID(cliDir); got != "example-cli" {
		t.Errorf("ProjectID(cli) = %q, want %q", got, "example-cli")
	}

	// 3. Nested subdirectory inside cli: cli/cmd/sila
	id, ok = DetectWorkspaceProjectID(subCliDir)
	if !ok || id != "example-cli" {
		t.Errorf("DetectWorkspaceProjectID(subCliDir) = (%q, %v), want (%q, true)", id, ok, "example-cli")
	}
	if got := ProjectID(subCliDir); got != "example-cli" {
		t.Errorf("ProjectID(subCliDir) = %q, want %q", got, "example-cli")
	}

	// 4. Other declared folders: worker & research
	if got := ProjectID(workerDir); got != "example-worker" {
		t.Errorf("ProjectID(worker) = %q, want %q", got, "example-worker")
	}
	if got := ProjectID(researchDir); got != "example-research" {
		t.Errorf("ProjectID(research) = %q, want %q", got, "example-research")
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
	wsPath := filepath.Join(tmp, "example-app.code-workspace")
	wsContent := `{"folders": [{"path": "."}]}`
	if err := os.WriteFile(wsPath, []byte(wsContent), 0o644); err != nil {
		t.Fatal(err)
	}
	subDir := filepath.Join(tmp, "apps", "web")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	id, ok := DetectWorkspaceProjectID(tmp)
	if !ok || id != "example-app" {
		t.Errorf("DetectWorkspaceProjectID(dotRoot) = (%q, %v), want (%q, true)", id, ok, "example-app")
	}
	if got := ProjectID(tmp); got != "example-app" {
		t.Errorf("ProjectID(dotRoot) = %q, want %q", got, "example-app")
	}
}

func TestDetectWorkspaceProjectID_AlreadyPrefixed(t *testing.T) {
	tmp := t.TempDir()
	wsPath := filepath.Join(tmp, "example.code-workspace")
	wsContent := `{"folders": [{"path": "example-cli"}]}`
	if err := os.WriteFile(wsPath, []byte(wsContent), 0o644); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(tmp, "example-cli")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}

	id, ok := DetectWorkspaceProjectID(folder)
	if !ok || id != "example-cli" {
		t.Errorf("DetectWorkspaceProjectID(alreadyPrefixed) = (%q, %v), want (%q, true)", id, ok, "example-cli")
	}
}
