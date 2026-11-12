package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentFromEntryCaseInsensitive(t *testing.T) {
	servers := map[string]any{
		"Sila": map[string]any{
			"env": map[string]any{"SILA_AGENT": "zed"},
		},
	}
	agent, ok := agentFromEntryCaseInsensitive(servers, "env")
	if !ok || agent != "zed" {
		t.Fatalf("agent = %q, ok = %v", agent, ok)
	}
}

func TestSilaAgentInConfigJSONForms(t *testing.T) {
	cases := []struct {
		name string
		kind string
		body string
		want string
	}{
		{"cursor", "mcpjson", `{"mcpServers":{"sila":{"env":{"SILA_AGENT":"cursor"}}}}`, "cursor"},
		{"zed", "zed", `{"context_servers":{"Sila":{"env":{"SILA_AGENT":"zed"}}}}`, "zed"},
		{"opencode", "opencode", `{"mcp":{"servers":{"sila":{"environment":{"SILA_AGENT":"opencode"}}}}}`, "opencode"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := silaAgentInConfig(tc.kind, []byte(tc.body))
			if !ok || got != tc.want {
				t.Fatalf("got %q ok=%v, want %q", got, ok, tc.want)
			}
		})
	}
}

func TestSilaAgentInConfigRejectsMissingAgent(t *testing.T) {
	raw := []byte(`{"mcpServers":{"sila":{"command":"sila"}}}`)
	if _, ok := silaAgentInConfig("mcpjson", raw); ok {
		t.Fatal("entry without SILA_AGENT must not count as connected")
	}
}

func TestDetectConnectStatus(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".cursor", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"mcpServers":{"sila":{"env":{"SILA_AGENT":"cursor"}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	target := connectTarget{Harness: "cursor", Display: "Cursor", Path: path, Kind: "mcpjson"}
	st := detectConnectStatus(target)
	if st.Status != connectConnected || st.Agent != "cursor" {
		t.Fatalf("status = %+v", st)
	}
	missing := detectConnectStatus(connectTarget{Harness: "zed", Display: "Zed", Path: filepath.Join(home, "nope", "settings.json"), Kind: "zed"})
	if missing.Status != connectUnavailable {
		t.Fatalf("missing status = %q", missing.Status)
	}
}

func TestWriteOpenCodeEntryPreservesForeign(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	initial := `{"mcp":{"servers":{"foreign":{"type":"local","command":["x"]}}}}`
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeOpenCodeEntry(path, "opencode"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	servers := root["mcp"].(map[string]any)["servers"].(map[string]any)
	if _, ok := servers["foreign"]; !ok {
		t.Fatal("foreign server was removed")
	}
	entry, ok := servers["sila"].(map[string]any)
	if !ok {
		t.Fatal("sila entry missing")
	}
	env := entry["environment"].(map[string]any)
	if env["SILA_AGENT"] != "opencode" {
		t.Fatalf("agent = %v", env["SILA_AGENT"])
	}
	command, _ := entry["command"].([]any)
	foundProfile := false
	for _, arg := range command {
		if arg == "--profile=graph" {
			foundProfile = true
		}
	}
	if !foundProfile {
		t.Fatalf("command = %v", command)
	}
	if err := writeOpenCodeEntry(path, "opencode"); err != nil {
		t.Fatal(err)
	}
	raw2, _ := os.ReadFile(path)
	if string(raw) != string(raw2) {
		t.Fatal("second write changed the file")
	}
}

func TestWriteCodexEntryAndRemove(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	initial := "model = \"x\"\n\n[mcp_servers.foreign]\ncommand = \"x\"\n"
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeCodexEntry(path, "codex"); err != nil {
		t.Fatal(err)
	}
	text, _ := os.ReadFile(path)
	if !strings.Contains(string(text), "[mcp_servers.sila]") || !strings.Contains(string(text), `SILA_AGENT = "codex"`) {
		t.Fatalf("codex entry missing:\n%s", text)
	}
	if err := writeCodexEntry(path, "codex"); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(path)
	if string(text) != string(again) {
		t.Fatal("second codex write changed the file")
	}
	out, changed := removeCodexSection(string(again))
	if !changed {
		t.Fatal("expected removal")
	}
	if strings.Contains(out, "mcp_servers.sila") || !strings.Contains(out, "mcp_servers.foreign") {
		t.Fatalf("removal damaged foreign content:\n%s", out)
	}
}

func TestResolveConnectTargetRejectsUnknown(t *testing.T) {
	if _, err := resolveConnectTarget("nope"); err == nil {
		t.Fatal("expected an error for an unknown harness")
	}
}

func TestOpenCodeFlatpakConnectPaths(t *testing.T) {
	home := t.TempDir()
	xdg := filepath.Join(home, "xdg")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	paths := openCodeConnectPaths(home)
	if len(paths) != 3 {
		t.Fatalf("openCodeConnectPaths = %v, want XDG, native, and Flatpak", paths)
	}
	native := filepath.Join(home, ".config", "opencode", "opencode.json")
	flatpak := filepath.Join(home, ".var", "app", "ai.opencode.opencode", "config", "opencode", "opencode.json")
	target := connectTarget{Harness: "opencode", Display: "OpenCode", Path: native, ExtraPaths: []string{flatpak}, Kind: "opencode"}
	if err := writeConnectEntry(target); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{native, flatpak} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var root map[string]any
		if err := json.Unmarshal(raw, &root); err != nil {
			t.Fatal(err)
		}
		servers := root["mcp"].(map[string]any)["servers"].(map[string]any)
		if _, ok := servers["sila"]; !ok {
			t.Fatalf("missing sila entry in %s", path)
		}
	}

	bin := filepath.Join(home, ".local", "bin", "sila")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := resolveOpenCodeSilaCommand(flatpak); got != bin {
		t.Fatalf("Flatpak command = %q, want %q", got, bin)
	}
	if got := resolveOpenCodeSilaCommand(native); got != "sila" {
		t.Fatalf("native command = %q, want sila", got)
	}

	if err := os.Remove(native); err != nil {
		t.Fatal(err)
	}
	if st := detectConnectStatus(target); st.Status != connectConnected || st.Agent != "opencode" {
		t.Fatalf("Flatpak-only status = %+v", st)
	}
	changed, err := removeConnectEntry(target)
	if err != nil || !changed {
		t.Fatalf("remove = %v, %v", changed, err)
	}
	raw, err := os.ReadFile(flatpak)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	if servers, ok := root["mcp"].(map[string]any)["servers"].(map[string]any); ok {
		if _, exists := servers["sila"]; exists {
			t.Fatal("sila entry survived remove")
		}
	}
}

func TestWriteCodexEntryUsesGraphProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := writeCodexEntry(path, "codex"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `--profile=graph`) {
		t.Fatalf("config = %s", data)
	}
}
