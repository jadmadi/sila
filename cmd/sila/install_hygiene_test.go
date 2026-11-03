package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/onboard"
	"github.com/jadmadi/sila/internal/render"
)

func TestShouldProvisionCursorMCP(t *testing.T) {
	dir := t.TempDir()
	if !shouldProvisionCursorMCP(dir, false, "all") {
		t.Fatal("expected default tools='all' to provision Cursor MCP")
	}
	if !shouldProvisionCursorMCP(dir, true, "cursor") {
		t.Fatal("expected explicit cursor to provision Cursor MCP")
	}
	if shouldProvisionCursorMCP(dir, false, "claude") {
		t.Fatal("expected --tools=claude in repo without .cursor to skip Cursor MCP")
	}
	if err := os.MkdirAll(filepath.Join(dir, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !shouldProvisionCursorMCP(dir, false, "claude") {
		t.Fatal("expected existing .cursor dir to provision Cursor MCP")
	}
}

func TestShouldProvisionProjectCommandCodeHooks(t *testing.T) {
	dir := t.TempDir()
	if shouldProvisionProjectCommandCodeHooks(dir, false, true, "command-code") {
		t.Fatal("expected false when Command Code is not detected on host")
	}
	if shouldProvisionProjectCommandCodeHooks(dir, true, false, "all") {
		t.Fatal("expected false when ~/.commandcode exists on host but <repo>/.commandcode does not exist and --tools=all")
	}
	if !shouldProvisionProjectCommandCodeHooks(dir, true, true, "command-code") {
		t.Fatal("expected true when --tools=command-code is explicitly passed")
	}
	if err := os.MkdirAll(filepath.Join(dir, ".commandcode"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !shouldProvisionProjectCommandCodeHooks(dir, true, false, "all") {
		t.Fatal("expected true when <repo>/.commandcode exists in workspace")
	}
}

func TestInstall_FreshRepo_ZeroCursorWarningAndNoCommandCodeDirt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".commandcode"), 0o755); err != nil {
		t.Fatal(err)
	}

	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)

	ui := render.New(os.Stdout, true)
	_ = captureStdout(t, func() {
		runInstall(ui, "fresh-repo", "all", "", false, false, false, false, false, true)
	})

	// 1. Project-local .commandcode/settings.json must NOT be created in a repo without .commandcode/
	if _, err := os.Stat(filepath.Join(repo, ".commandcode", "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("expected <repo>/.commandcode/settings.json NOT to be created during default sila install, got err=%v", err)
	}
	// User-scope ~/.commandcode/mcp.json SHOULD still be registered.
	if _, err := os.Stat(filepath.Join(home, ".commandcode", "mcp.json")); err != nil {
		t.Fatalf("expected ~/.commandcode/mcp.json to be registered: %v", err)
	}
	// User-scope ~/.commandcode/settings.json SHOULD receive lifecycle hooks.
	if _, err := os.Stat(filepath.Join(home, ".commandcode", "settings.json")); err != nil {
		t.Fatalf("expected ~/.commandcode/settings.json to receive user hooks: %v", err)
	}

	// 2. Both .cursor/mcp.json and .cursor/rules/000-sila-core.mdc must exist so onboard check has 0 rule warnings.
	if _, err := os.Stat(filepath.Join(repo, ".cursor", "mcp.json")); err != nil {
		t.Fatalf("expected .cursor/mcp.json to exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".cursor", "rules", "000-sila-core.mdc")); err != nil {
		t.Fatalf("expected .cursor/rules/000-sila-core.mdc to exist: %v", err)
	}

	rep := onboard.Check(nil, repo, "fresh-repo", false)
	for _, item := range rep.Items {
		if item.Name == "Agent rules" && item.Status != "pass" {
			t.Fatalf("expected Agent rules status='pass' after sila install, got status=%q detail=%q", item.Status, item.Detail)
		}
	}
}

func jsonContainsCommand(v any, prefix string) bool {
	switch value := v.(type) {
	case map[string]any:
		if cmd, ok := value["command"].(string); ok && strings.Contains(cmd, prefix) {
			return true
		}
		for _, child := range value {
			if jsonContainsCommand(child, prefix) {
				return true
			}
		}
	case []any:
		for _, child := range value {
			if jsonContainsCommand(child, prefix) {
				return true
			}
		}
	}
	return false
}

func TestDetectedUserHookHarnesses(t *testing.T) {
	if got := detectedUserHookHarnesses(""); got != nil {
		t.Fatalf("empty home must return nil, got %v", got)
	}
	home := t.TempDir()
	if got := detectedUserHookHarnesses(home); len(got) != 0 {
		t.Fatalf("empty home tree must return no harnesses, got %v", got)
	}
	for _, rel := range []string{".claude", ".cursor", ".gemini/config", ".commandcode", ".zcode/cli", ".config/opencode/plugins"} {
		if err := os.MkdirAll(filepath.Join(home, filepath.FromSlash(rel)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"claude", "cursor", "antigravity", "command-code", "zcode", "opencode"}
	if got := detectedUserHookHarnesses(home); !reflect.DeepEqual(got, want) {
		t.Fatalf("detected harnesses = %v, want %v", got, want)
	}
	// A regular file named like a host dir must not count as detection.
	oddHome := t.TempDir()
	if err := os.WriteFile(filepath.Join(oddHome, ".claude"), []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := detectedUserHookHarnesses(oddHome); len(got) != 0 {
		t.Fatalf("regular files must not count as detected host dirs, got %v", got)
	}
}

func TestInstall_DetectedHostHarnessesReceiveUserHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, rel := range []string{".gemini/config", ".cursor", ".claude", ".commandcode"} {
		if err := os.MkdirAll(filepath.Join(home, filepath.FromSlash(rel)), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)

	ui := render.New(os.Stdout, true)
	_ = captureStdout(t, func() {
		runInstall(ui, "detected-hooks", "all", "", false, false, false, false, false, true)
	})

	readJSON := func(path string) map[string]any {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var root map[string]any
		if err := json.Unmarshal(data, &root); err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		return root
	}

	// Antigravity: named sila group with dialect-native events.
	agy := readJSON(filepath.Join(home, ".gemini", "config", "hooks.json"))
	agyGroup, ok := agy["sila"].(map[string]any)
	if !ok {
		t.Fatalf("antigravity user hooks missing named sila group: %#v", agy)
	}
	if enabled, ok := agyGroup["enabled"].(bool); !ok || !enabled {
		t.Fatalf("antigravity sila group must be enabled: %#v", agyGroup)
	}
	if !jsonContainsCommand(agyGroup["SessionStart"], "sila hook session-start") {
		t.Fatalf("antigravity SessionStart missing Sila handler: %#v", agyGroup["SessionStart"])
	}
	if !jsonContainsCommand(agyGroup["PreToolUse"], "sila hook pre-tool-use") {
		t.Fatalf("antigravity PreToolUse missing Sila handler: %#v", agyGroup["PreToolUse"])
	}

	// Cursor: flat handlers with version 1.
	cursor := readJSON(filepath.Join(home, ".cursor", "hooks.json"))
	if version, ok := cursor["version"].(float64); !ok || version != 1 {
		t.Fatalf("cursor user hooks must carry version 1: %#v", cursor["version"])
	}
	cursorHooks := cursor["hooks"].(map[string]any)
	for _, raw := range cursorHooks["sessionStart"].([]any) {
		if _, nested := raw.(map[string]any)["hooks"]; nested {
			t.Fatalf("cursor user hooks must be flat: %#v", raw)
		}
	}
	if !jsonContainsCommand(cursorHooks["sessionStart"], "sila hook session-start") {
		t.Fatalf("cursor sessionStart missing Sila handler: %#v", cursorHooks["sessionStart"])
	}

	// Claude: nested matcher-group dialect.
	claude := readJSON(filepath.Join(home, ".claude", "settings.json"))
	claudeHooks := claude["hooks"].(map[string]any)
	if !jsonContainsCommand(claudeHooks["SessionStart"], "sila hook session-start") {
		t.Fatalf("claude user hooks missing Sila SessionStart: %#v", claudeHooks["SessionStart"])
	}

	// Command Code: nested dialect at user scope.
	cc := readJSON(filepath.Join(home, ".commandcode", "settings.json"))
	if !jsonContainsCommand(cc["hooks"], "sila hook session-start") {
		t.Fatalf("command-code user hooks missing Sila SessionStart: %#v", cc["hooks"])
	}

	// Zero project-tree pollution from user-scope hook provisioning.
	for _, rel := range []string{".claude", ".commandcode", ".agents/hooks.json"} {
		if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Fatalf("default install polluted the repo with %s (err=%v)", rel, err)
		}
	}
}

func TestInstall_NoHooksWritesNoHookFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, rel := range []string{".gemini/config", ".cursor", ".claude", ".commandcode"} {
		if err := os.MkdirAll(filepath.Join(home, filepath.FromSlash(rel)), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)

	ui := render.New(os.Stdout, true)
	_ = captureStdout(t, func() {
		runInstall(ui, "no-hooks", "all", "", true, false, false, false, false, true)
	})

	for _, rel := range []string{
		".gemini/config/hooks.json",
		".cursor/hooks.json",
		".claude/settings.json",
		".commandcode/settings.json",
	} {
		if _, err := os.Stat(filepath.Join(home, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Fatalf("--no-hooks wrote user hook file %s (err=%v)", rel, err)
		}
	}
	for _, rel := range []string{".claude", ".commandcode", ".agents/hooks.json"} {
		if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Fatalf("--no-hooks wrote project hook path %s (err=%v)", rel, err)
		}
	}
}
