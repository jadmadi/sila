package onboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readServerEnv(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	servers, _ := cfg["mcpServers"].(map[string]any)
	sila, _ := servers["sila"].(map[string]any)
	env, _ := sila["env"].(map[string]any)
	value, _ := env["SILA_AGENT"].(string)
	return value
}

func TestRegisterMCPServerAddsHarnessIdentity(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		subdir string
		agent  string
	}{
		{".cursor", "cursor"},
		{".gemini", "antigravity"},
		{".claude", "claude"},
		{".commandcode", "command-code"},
		{".zcode", "zcode"},
		{"opencode", "opencode"},
	}
	for _, tc := range cases {
		path := filepath.Join(dir, tc.subdir, "mcp.json")
		if err := RegisterMCPServer(path); err != nil {
			t.Fatalf("%s: %v", tc.subdir, err)
		}
		if got := readServerEnv(t, path); got != tc.agent {
			t.Fatalf("%s agent = %q, want %q", tc.subdir, got, tc.agent)
		}
	}
}

func TestRegisterMCPServerPreservesForeignAndIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".cursor", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := `{"mcpServers":{"other":{"command":"other","args":["serve"]}}}`
	if err := os.WriteFile(path, []byte(foreign), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RegisterMCPServer(path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"command": "other"`) {
		t.Fatalf("foreign server lost: %s", data)
	}
	first, _ := os.ReadFile(path)
	if err := RegisterMCPServer(path); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(path)
	if string(first) != string(second) {
		t.Fatal("second registration changed the file")
	}
}

func TestRegisterCodexMCPServer(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("model = \"gpt\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RegisterCodexMCPServer(path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "[mcp_servers.sila]") || !strings.Contains(string(data), `SILA_AGENT = "codex"`) {
		t.Fatalf("config = %s", data)
	}
	if !strings.Contains(string(data), `model = "gpt"`) {
		t.Fatalf("foreign key lost: %s", data)
	}
	first := string(data)
	if err := RegisterCodexMCPServer(path); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(path)
	if string(second) != first {
		t.Fatal("second codex registration changed the file")
	}
}

func TestRegisterZedMCPServerIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".config", "zed", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := RegisterZedMCPServer(path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"SILA_AGENT": "zed"`) {
		t.Fatalf("settings = %s", data)
	}
}

func TestMCPProvisionPlan(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	for _, dir := range []string{filepath.Join(cwd, ".cursor"), filepath.Join(home, ".gemini", "config"), filepath.Join(home, ".config", "zed"), filepath.Join(home, ".codex")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	plan := MCPProvisionPlan(cwd, home)
	if len(plan) != 4 {
		t.Fatalf("plan = %+v", plan)
	}
	agents := map[string]string{}
	for _, item := range plan {
		agents[item.Agent] = item.Kind
	}
	for agent, kind := range map[string]string{"cursor": "json", "antigravity": "json", "zed": "zed", "codex": "toml"} {
		if agents[agent] != kind {
			t.Fatalf("%s kind = %q", agent, agents[agent])
		}
	}
}

func readServerArgs(t *testing.T, path string) []any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	servers, _ := cfg["mcpServers"].(map[string]any)
	sila, _ := servers["sila"].(map[string]any)
	args, _ := sila["args"].([]any)
	return args
}

func TestRegisterMCPServerUsesGraphProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".cursor", "mcp.json")
	if err := RegisterMCPServer(path); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, arg := range readServerArgs(t, path) {
		if arg == "--profile=graph" {
			found = true
		}
	}
	if !found {
		t.Fatalf("args = %v", readServerArgs(t, path))
	}
}
