package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
)

// TestRegisterMCPServerFailsLoudlyOnMalformedConfig covers the codex-eval F04
// finding: a malformed existing MCP config must error out, never be silently
// overwritten (that destroyed the user's other MCP registrations).
func TestRegisterMCPServerFailsLoudlyOnMalformedConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(path, []byte("{not valid json !!!"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := registerMCPServer(path); err == nil {
		t.Fatal("expected error for malformed existing MCP config, got nil")
	}
	// The malformed file must be left untouched.
	data, _ := os.ReadFile(path)
	if string(data) != "{not valid json !!!" {
		t.Fatalf("malformed config was overwritten: %q", data)
	}
}

func TestRegisterMCPServerMergesAndRejectsWrongShape(t *testing.T) {
	t.Run("merge preserves other servers", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "mcp.json")
		before := `{"mcpServers":{"other":{"command":"other-cmd"}}}`
		if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := registerMCPServer(path); err != nil {
			t.Fatalf("merge failed: %v", err)
		}
		var cfg map[string]any
		data, _ := os.ReadFile(path)
		if err := json.Unmarshal(data, &cfg); err != nil {
			t.Fatal(err)
		}
		servers := cfg["mcpServers"].(map[string]any)
		if _, ok := servers["other"]; !ok {
			t.Fatal("other MCP server entry lost during merge")
		}
		if _, ok := servers["sila"]; !ok {
			t.Fatal("sila entry missing after merge")
		}
	})

	t.Run("non-object mcpServers is an error", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "mcp.json")
		if err := os.WriteFile(path, []byte(`{"mcpServers":[1,2]}`), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := registerMCPServer(path); err == nil {
			t.Fatal("expected error for non-object mcpServers, got nil")
		}
	})
}

// TestRegisterMCPServerRejectsUnreadableConfig pins the re-eval F04 rule: a
// config we cannot read is an error, never treated as absent and overwritten.
func TestRegisterMCPServerRejectsUnreadableConfig(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: file modes are not enforced")
	}
	path := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(path, []byte(`{"mcpServers":{"other":{"command":"x"}}}`), 0o000); err != nil {
		t.Fatal(err)
	}
	err := registerMCPServer(path)
	if err == nil {
		t.Fatal("expected an error for an unreadable existing MCP config")
	}
	if !strings.Contains(err.Error(), "unreadable") {
		t.Fatalf("unexpected error: %v", err)
	}
	// The unreadable file must be left exactly as it was.
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if !strings.Contains(string(after), `"other"`) {
		t.Fatalf("unreadable config was modified: %s", after)
	}
}

// TestInstallPreviewEnumeratesEveryWrite keeps the dry-run plan honest: hooks
// and MCP registrations must be listed exactly where execution performs them.
func TestInstallPreviewEnumeratesEveryWrite(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.MkdirAll(filepath.Join(dir, ".git", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	ui := render.New(os.Stdout, true)

	plan := func() map[string]bool {
		planJSON := captureStdout(t, func() {
			runInstall(ui, "preview-test", "all", "", false, false, false, true, false, true)
		})

		// The defect was the machine-readable plan omitting writes execution does,
		// so assert the JSON payload — the human text mentioned them even before.
		var parsed struct {
			Plan []struct {
				Path   string `json:"path"`
				Action string `json:"action"`
			} `json:"plan"`
		}
		if err := json.Unmarshal([]byte(planJSON), &parsed); err != nil {
			t.Fatalf("dry-run --json did not emit a parseable plan: %v\n%s", err, planJSON)
		}
		got := map[string]bool{}
		for _, p := range parsed.Plan {
			got[p.Path] = true
		}
		return got
	}

	// A project that is not the sila source repository keeps its own git hooks;
	// the plan must not promise one.
	got := plan()
	for _, want := range []string{"AGENTS.md", ".cursor/mcp.json"} {
		if !got[want] {
			t.Fatalf("dry-run plan omits %s; plan lists %v", want, got)
		}
	}
	if got[".git/hooks/pre-commit"] {
		t.Fatalf("dry-run plan promises a git hook outside the sila source repo; plan lists %v", got)
	}

	// The sila source repository is the one place the hook is installed.
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module github.com/jadmadi/sila\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got = plan()
	if !got[".git/hooks/pre-commit"] {
		t.Fatalf("dry-run plan omits .git/hooks/pre-commit in the sila source repo; plan lists %v", got)
	}
}

func TestInstallWorkspaceShim(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	ui := render.New(os.Stdout, true)

	// Test dry-run with shim
	planJSON := captureStdout(t, func() {
		runInstall(ui, "shim-test", "all", "", true, true, true, true, false, true)
	})
	var parsed struct {
		Plan []struct {
			Path   string `json:"path"`
			Action string `json:"action"`
		} `json:"plan"`
	}
	if err := json.Unmarshal([]byte(planJSON), &parsed); err != nil {
		t.Fatalf("failed to unmarshal dry-run json: %v", err)
	}
	hasShim := false
	hasGitIgnore := false
	for _, p := range parsed.Plan {
		if p.Path == ".bin/sila" {
			hasShim = true
		}
		if p.Path == ".gitignore" {
			hasGitIgnore = true
		}
	}
	if !hasShim {
		t.Errorf("dry-run plan expected .bin/sila, got: %+v", parsed.Plan)
	}
	if !hasGitIgnore {
		t.Errorf("dry-run plan expected .gitignore, got: %+v", parsed.Plan)
	}

	// Test execution with shim
	captureStdout(t, func() {
		runInstall(ui, "shim-test", "all", "", true, true, true, false, false, false)
	})

	shimPath := filepath.Join(dir, ".bin", "sila")
	fi, err := os.Stat(shimPath)
	if err != nil {
		t.Fatalf("expected %s to exist: %v", shimPath, err)
	}
	if fi.Mode()&0o111 == 0 {
		t.Errorf("expected %s to be executable, mode: %v", shimPath, fi.Mode())
	}

	// Verify .gitignore updated
	gitIgnorePath := filepath.Join(dir, ".gitignore")
	data, err := os.ReadFile(gitIgnorePath)
	if err != nil {
		t.Fatalf("expected .gitignore to exist: %v", err)
	}
	if !strings.Contains(string(data), ".bin/") {
		t.Errorf("expected .gitignore to contain .bin/, got: %s", string(data))
	}

	// Idempotent re-run
	captureStdout(t, func() {
		runInstall(ui, "shim-test", "all", "", true, true, true, false, false, false)
	})
	// Check .gitignore doesn't duplicate entry unnecessarily
	count := strings.Count(string(data), ".bin/")
	if count != 1 {
		t.Errorf("expected exactly 1 instance of .bin/ in .gitignore, got %d", count)
	}
}
