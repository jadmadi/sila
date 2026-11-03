package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/hookconfig"
	"github.com/jadmadi/sila/internal/render"
)

func TestInstallCommandCodeTarget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".commandcode"), 0o755); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	origDir, _ := os.Getwd()
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origDir) }()

	// Seed user-owned configuration that must survive the merge.
	if err := os.MkdirAll(filepath.Join(repo, ".commandcode"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".commandcode", "settings.json"), []byte(`{"tasteLearning":false}`), 0o644); err != nil {
		t.Fatal(err)
	}
	mcpPath := filepath.Join(home, ".commandcode", "mcp.json")
	seed := `{"userKey":1,"mcpServers":{"other":{"command":"other"},"sila":{"custom":"keep"}}}`
	if err := os.WriteFile(mcpPath, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}

	ui := render.New(nil, true)
	dryOut := captureStdout(t, func() {
		runInstall(ui, "cc-proj", "command-code", "", false, false, false, true, false, true)
	})
	var dry struct {
		Plan []struct {
			Path string `json:"path"`
		} `json:"plan"`
	}
	if err := json.Unmarshal([]byte(dryOut), &dry); err != nil {
		t.Fatalf("dry-run output is not JSON: %v\n%s", err, dryOut)
	}
	paths := map[string]bool{}
	for _, p := range dry.Plan {
		paths[p.Path] = true
	}
	for _, want := range []string{".commandcode/settings.json", "AGENTS.md", "~/.commandcode/mcp.json"} {
		if !paths[want] {
			t.Errorf("dry-run plan missing %q: %v", want, paths)
		}
	}
	if strings.Contains(dryOut, "deploy targets:") {
		t.Fatalf("dry-run reported a deploy error: %s", dryOut)
	}
	if _, err := os.Stat(filepath.Join(repo, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatal("dry-run wrote AGENTS.md")
	}

	realOut := captureStdout(t, func() {
		runInstall(ui, "cc-proj", "command-code", "", false, false, false, false, false, true)
	})
	var res InstallResult
	if err := json.Unmarshal([]byte(realOut), &res); err != nil {
		t.Fatalf("install output is not JSON: %v\n%s", err, realOut)
	}
	if len(res.Errors) > 0 {
		t.Fatalf("install errors: %v", res.Errors)
	}

	if data, err := os.ReadFile(filepath.Join(repo, "AGENTS.md")); err != nil || !strings.Contains(string(data), "sila:begin") {
		t.Fatalf("AGENTS.md missing managed block: %v", err)
	}
	settings, err := os.ReadFile(filepath.Join(repo, ".commandcode", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(settings), "tasteLearning") || !strings.Contains(string(settings), "sila hook session-start") {
		t.Fatalf("settings merge lost user key or hooks: %s", settings)
	}
	mcp, err := os.ReadFile(mcpPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"userKey"`, `"other"`, `"custom": "keep"`, `"transport": "stdio"`, `"command": "sila"`} {
		if !strings.Contains(string(mcp), want) {
			t.Errorf("mcp merge missing %s: %s", want, mcp)
		}
	}

	// Idempotency: a second install must not change the two config files.
	beforeSettings := string(settings)
	beforeMCP := string(mcp)
	_ = captureStdout(t, func() {
		runInstall(ui, "cc-proj", "command-code", "", false, false, false, false, false, true)
	})
	afterSettings, _ := os.ReadFile(filepath.Join(repo, ".commandcode", "settings.json"))
	afterMCP, _ := os.ReadFile(mcpPath)
	if string(afterSettings) != beforeSettings {
		t.Fatalf("second install changed settings.json")
	}
	if string(afterMCP) != beforeMCP {
		t.Fatalf("second install changed mcp.json")
	}

	// Rollback removes only Sila's hooks and preserves the user key.
	if _, err := hookconfig.Uninstall("command-code", repo, false); err != nil {
		t.Fatal(err)
	}
	rolled, _ := os.ReadFile(filepath.Join(repo, ".commandcode", "settings.json"))
	if strings.Contains(string(rolled), "sila hook") {
		t.Fatalf("uninstall left Sila hooks behind: %s", rolled)
	}
	if !strings.Contains(string(rolled), "tasteLearning") {
		t.Fatalf("uninstall lost the user key: %s", rolled)
	}
}

func TestInstallCommandCodeNoMCPAndNoHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".commandcode"), 0o755); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	origDir, _ := os.Getwd()
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origDir) }()

	ui := render.New(nil, true)
	out := captureStdout(t, func() {
		runInstall(ui, "cc-proj", "command-code", "", true, true, false, false, false, true)
	})
	var res InstallResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("install output is not JSON: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(home, ".commandcode", "mcp.json")); !os.IsNotExist(err) {
		t.Fatal("--no-mcp still wrote the MCP config")
	}
	if _, err := os.Stat(filepath.Join(repo, ".commandcode", "settings.json")); !os.IsNotExist(err) {
		t.Fatal("--no-hooks still wrote settings.json")
	}
}

func TestInstallCommandCodeSubprocess(t *testing.T) {
	if os.Getenv("GO_WANT_INSTALL_CC_SUBPROCESS") != "1" {
		return
	}
	ui := render.New(os.Stdout, true)
	runInstall(ui, "cc-proj", "command-code", "", false, false, false, false, false, true)
	os.Exit(0)
}

func TestInstallCommandCodeMissingConfig(t *testing.T) {
	home := t.TempDir() // deliberately no ~/.commandcode
	cmd := exec.Command(os.Args[0], "-test.run=TestInstallCommandCodeSubprocess")
	cmd.Env = append(os.Environ(), "GO_WANT_INSTALL_CC_SUBPROCESS=1", "HOME="+home)
	cmd.Dir = t.TempDir()
	err := cmd.Run()
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected a non-zero exit for missing Command Code config, got %v", err)
	}
	if exitErr.ExitCode() != 1 {
		t.Fatalf("exit code = %d, want 1", exitErr.ExitCode())
	}
}
