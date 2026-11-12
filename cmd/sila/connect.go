package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/dream"
	"github.com/jadmadi/sila/internal/onboard"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

const (
	connectConnected   = "connected"
	connectInstalled   = "installed"
	connectUnavailable = "unavailable"
	connectTimeout     = 5 * time.Second
)

// connectTarget is one harness sila can wire and verify.
type connectTarget struct {
	Harness    string
	Display    string
	Path       string
	ExtraPaths []string
	Kind       string // mcpjson | commandcode | zed | opencode | codex
}

// connectStatus is the detection result for one harness.
type connectStatus struct {
	Harness    string `json:"harness"`
	Display    string `json:"display"`
	Status     string `json:"status"`
	ConfigPath string `json:"config_path"`
	Agent      string `json:"agent,omitempty"`
}

// verifyResult is the handshake proof for one harness.
type verifyResult struct {
	Harness    string `json:"harness"`
	OK         bool   `json:"ok"`
	Tools      int    `json:"tools"`
	Agent      string `json:"agent,omitempty"`
	Profile    string `json:"profile,omitempty"`
	DurationMS int64  `json:"duration_ms"`
	Reason     string `json:"reason,omitempty"`
}

func connectTargets(home string) []connectTarget {
	return []connectTarget{
		{Harness: "antigravity", Display: "Antigravity", Path: filepath.Join(home, ".gemini", "config", "mcp_config.json"), Kind: "mcpjson"},
		{Harness: "command-code", Display: "Command Code", Path: filepath.Join(home, ".commandcode", "mcp.json"), Kind: "commandcode"},
		{Harness: "cursor", Display: "Cursor", Path: filepath.Join(home, ".cursor", "mcp.json"), Kind: "mcpjson"},
		{Harness: "codex", Display: "Codex", Path: filepath.Join(home, ".codex", "config.toml"), Kind: "codex"},
		func() connectTarget {
			paths := openCodeConnectPaths(home)
			return connectTarget{Harness: "opencode", Display: "OpenCode", Path: paths[0], ExtraPaths: paths[1:], Kind: "opencode"}
		}(),
		{Harness: "zed", Display: "Zed", Path: filepath.Join(home, ".config", "zed", "settings.json"), Kind: "zed"},
	}
}

func resolveConnectTarget(name string) (connectTarget, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return connectTarget{}, err
	}
	want := strings.ToLower(strings.TrimSpace(name))
	for _, t := range connectTargets(home) {
		if t.Harness == want {
			return t, nil
		}
	}
	names := make([]string, 0, len(connectTargets(home)))
	for _, t := range connectTargets(home) {
		names = append(names, t.Harness)
	}
	sort.Strings(names)
	return connectTarget{}, fmt.Errorf("unknown harness %q; supported: %s", name, strings.Join(names, ", "))
}

func detectConnectStatus(t connectTarget) connectStatus {
	st := connectStatus{Harness: t.Harness, Display: t.Display, ConfigPath: t.Path, Status: connectUnavailable}
	paths := connectTargetPaths(t)
	for _, path := range paths {
		if dirExists(filepath.Dir(path)) {
			st.Status = connectInstalled
		}
	}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if agent, ok := silaAgentInConfig(t.Kind, raw); ok {
			st.Status = connectConnected
			st.Agent = agent
			break
		}
	}
	return st
}

// openCodeConnectPaths returns XDG, native, and Flathub Flatpak OpenCode config
// candidates, deduplicated.
func openCodeConnectPaths(home string) []string {
	paths := make([]string, 0, 3)
	seen := make(map[string]bool, 3)
	add := func(path string) {
		clean := filepath.Clean(path)
		if clean == "" || seen[clean] {
			return
		}
		seen[clean] = true
		paths = append(paths, clean)
	}
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		add(filepath.Join(xdg, "opencode", "opencode.json"))
	}
	add(filepath.Join(home, ".config", "opencode", "opencode.json"))
	add(filepath.Join(home, ".var", "app", "ai.opencode.opencode", "config", "opencode", "opencode.json"))
	return paths
}

func connectTargetPaths(t connectTarget) []string {
	paths := make([]string, 0, 1+len(t.ExtraPaths))
	paths = append(paths, t.Path)
	paths = append(paths, t.ExtraPaths...)
	return paths
}

func silaAgentInConfig(kind string, raw []byte) (string, bool) {
	switch kind {
	case "codex":
		return silaAgentInTOML(string(raw))
	case "opencode":
		var root map[string]any
		if json.Unmarshal(raw, &root) != nil {
			return "", false
		}
		mcp, _ := root["mcp"].(map[string]any)
		servers, _ := mcp["servers"].(map[string]any)
		return agentFromEntryCaseInsensitive(servers, "environment")
	case "zed":
		var root map[string]any
		if json.Unmarshal(raw, &root) != nil {
			return "", false
		}
		servers, _ := root["context_servers"].(map[string]any)
		return agentFromEntryCaseInsensitive(servers, "env")
	default:
		var root map[string]any
		if json.Unmarshal(raw, &root) != nil {
			return "", false
		}
		servers, _ := root["mcpServers"].(map[string]any)
		return agentFromEntryCaseInsensitive(servers, "env")
	}
}

func agentFromEntryCaseInsensitive(servers map[string]any, envKey string) (string, bool) {
	for key, entry := range servers {
		if strings.EqualFold(key, "sila") {
			return agentFromEntry(entry, envKey)
		}
	}
	return "", false
}

func agentFromEntry(entry any, envKey string) (string, bool) {
	m, ok := entry.(map[string]any)
	if !ok {
		return "", false
	}
	env, _ := m[envKey].(map[string]any)
	agent, _ := env["SILA_AGENT"].(string)
	agent = strings.TrimSpace(agent)
	return agent, agent != ""
}

func silaAgentInTOML(text string) (string, bool) {
	if !strings.Contains(text, "[mcp_servers.sila]") {
		return "", false
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "SILA_AGENT") && strings.Contains(line, "=") {
			v := strings.TrimSpace(strings.SplitN(line, "=", 2)[1])
			v = strings.Trim(v, `"'`)
			if v != "" {
				return v, true
			}
		}
	}
	return "", false
}

func configAgent(t connectTarget) string {
	raw, err := os.ReadFile(t.Path)
	if err != nil {
		return ""
	}
	agent, _ := silaAgentInConfig(t.Kind, raw)
	return agent
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func runConnect(_ render.UI, dbPath, _ string, jsonOut, globalYes bool, args []string) {
	args, jsonOut = splitConnectJSON(args, jsonOut)
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: sila connect <list|add|verify|demo|remove> [harness]")
		os.Exit(2)
	}
	switch args[0] {
	case "list":
		statuses := make([]connectStatus, 0, 6)
		home, _ := os.UserHomeDir()
		for _, t := range connectTargets(home) {
			statuses = append(statuses, detectConnectStatus(t))
		}
		if jsonOut {
			emitJSON(statuses)
			return
		}
		fmt.Printf("%-14s %-14s %-46s %s\n", "HARNESS", "STATUS", "CONFIG", "NEXT")
		for _, st := range statuses {
			next := ""
			if st.Status != connectConnected {
				next = "sila connect add " + st.Harness
			}
			fmt.Printf("%-14s %-14s %-46s %s\n", st.Harness, st.Status, st.ConfigPath, next)
		}
	case "add":
		name, rest := firstPositional(args[1:])
		if name == "" || len(rest) > 0 {
			fmt.Fprintln(os.Stderr, "usage: sila connect add <harness>")
			os.Exit(2)
		}
		runConnectAdd(name, dbPath, jsonOut)
	case "remove":
		name, rest := firstPositional(args[1:])
		yes := globalYes
		for _, a := range rest {
			switch a {
			case "--yes", "-y":
				yes = true
			default:
				fmt.Fprintf(os.Stderr, "unknown remove option %q\n", a)
				os.Exit(2)
			}
		}
		if name == "" {
			fmt.Fprintln(os.Stderr, "usage: sila connect remove <harness> [--yes]")
			os.Exit(2)
		}
		runConnectRemove(name, yes, jsonOut)
	case "verify":
		name, rest := firstPositional(args[1:])
		if name == "" || len(rest) > 0 {
			fmt.Fprintln(os.Stderr, "usage: sila connect verify <harness>")
			os.Exit(2)
		}
		runConnectVerify(name, dbPath, jsonOut)
	case "demo":
		if len(args) > 1 {
			fmt.Fprintln(os.Stderr, "usage: sila connect demo")
			os.Exit(2)
		}
		runConnectDemo(dbPath, jsonOut)
	default:
		fmt.Fprintf(os.Stderr, "unknown connect command %q\n", args[0])
		os.Exit(2)
	}
}

// splitConnectJSON removes a trailing --json from subcommand arguments and
// folds it into the global flag, because the global parser stops at the first
// positional argument.
func splitConnectJSON(args []string, jsonOut bool) ([]string, bool) {
	out := make([]string, 0, len(args))
	for _, a := range args {
		switch a {
		case "--json", "--json=true":
			jsonOut = true
		case "--json=false":
			jsonOut = false
		default:
			out = append(out, a)
		}
	}
	return out, jsonOut
}

func firstPositional(args []string) (string, []string) {
	if len(args) == 0 {
		return "", nil
	}
	return args[0], args[1:]
}

func runConnectAdd(name, dbPath string, jsonOut bool) {
	t, err := resolveConnectTarget(name)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sila connect:", err)
		os.Exit(2)
	}
	if err := backupConnectTarget(t); err != nil {
		fmt.Fprintf(os.Stderr, "sila connect: backup %s: %v\n", t.Path, err)
		os.Exit(1)
	}
	if err := writeConnectEntry(t); err != nil {
		fmt.Fprintf(os.Stderr, "sila connect: write %s: %v\n", t.Path, err)
		os.Exit(1)
	}
	res := verifyHarness(t.Harness, dbPath)
	if jsonOut {
		emitJSON(res)
		if !res.OK {
			os.Exit(1)
		}
		return
	}
	fmt.Printf("connected %s (%s)\n", t.Harness, t.Path)
	if !res.OK {
		fmt.Printf("FAIL %s: %s\n", res.Harness, res.Reason)
		os.Exit(1)
	}
	fmt.Printf("PASS %s %d tools in %dms; restart %s to load the server\n", res.Harness, res.Tools, res.DurationMS, t.Display)
}

func writeConnectEntry(t connectTarget) error {
	switch t.Kind {
	case "mcpjson":
		return onboard.RegisterMCPServer(t.Path)
	case "commandcode":
		return onboard.RegisterCommandCodeMCPServer(t.Path)
	case "zed":
		return onboard.RegisterZedMCPServer(t.Path)
	case "opencode":
		for _, path := range connectTargetPaths(t) {
			if err := writeOpenCodeEntry(path, t.Harness); err != nil {
				return err
			}
		}
		return nil
	case "codex":
		return writeCodexEntry(t.Path, t.Harness)
	default:
		return fmt.Errorf("unsupported config kind %q", t.Kind)
	}
}

// resolveOpenCodeSilaCommand uses the absolute ~/.local/bin/sila path for
// Flatpak configs, where the sandbox PATH may not include the user binary.
func resolveOpenCodeSilaCommand(configPath string) string {
	normalized := filepath.ToSlash(configPath)
	if !strings.Contains(normalized, "/.var/app/ai.opencode.opencode/") {
		return "sila"
	}
	home := ""
	if idx := strings.Index(normalized, "/.var/app/ai.opencode.opencode/"); idx > 0 {
		home = filepath.FromSlash(normalized[:idx])
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	bin := filepath.Join(home, ".local", "bin", "sila")
	if _, err := os.Stat(bin); err == nil {
		return bin
	}
	return "sila"
}

func backupConnectTarget(t connectTarget) error {
	for _, path := range connectTargetPaths(t) {
		if err := backupConfig(path); err != nil {
			return err
		}
	}
	return nil
}

func backupConfig(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return os.WriteFile(path+".sila.bak", raw, 0o600)
}

func writeOpenCodeEntry(path, agent string) error {
	var root map[string]any
	if raw, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(raw, &root); err != nil {
			return fmt.Errorf("existing config is not valid JSON: %w", err)
		}
	}
	if root == nil {
		root = map[string]any{}
	}
	mcp, _ := root["mcp"].(map[string]any)
	if mcp == nil {
		mcp = map[string]any{}
		root["mcp"] = mcp
	}
	servers, _ := mcp["servers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
		mcp["servers"] = servers
	}
	servers["sila"] = map[string]any{
		"type":        "local",
		"command":     []string{resolveOpenCodeSilaCommand(path), "mcp", "--profile=graph"},
		"environment": map[string]any{"SILA_AGENT": agent},
	}
	return writeJSONPreservingMode(path, root)
}

func writeCodexEntry(path, agent string) error {
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	text := string(raw)
	if strings.Contains(text, "[mcp_servers.sila]") {
		return nil
	}
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	text += fmt.Sprintf(`[mcp_servers.sila]
command = "sila"
args = ["mcp", "--profile=graph"]

[mcp_servers.sila.env]
SILA_AGENT = %q
`, agent)
	mode := os.FileMode(0o600)
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	}
	return os.WriteFile(path, []byte(text), mode)
}

func writeJSONPreservingMode(path string, root map[string]any) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	b, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	return os.WriteFile(path, append(b, '\n'), mode)
}

func runConnectRemove(name string, yes, jsonOut bool) {
	t, err := resolveConnectTarget(name)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sila connect:", err)
		os.Exit(2)
	}
	if !yes {
		fmt.Fprintf(os.Stderr, "refusing to remove without --yes: sila connect remove %s --yes\n", t.Harness)
		os.Exit(2)
	}
	if err := backupConnectTarget(t); err != nil {
		fmt.Fprintf(os.Stderr, "sila connect: backup: %v\n", err)
		os.Exit(1)
	}
	changed, err := removeConnectEntry(t)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila connect: remove: %v\n", err)
		os.Exit(1)
	}
	if jsonOut {
		emitJSON(map[string]any{"harness": t.Harness, "removed": changed})
		return
	}
	if changed {
		fmt.Printf("removed %s entry from %s\n", t.Harness, t.Path)
	} else {
		fmt.Printf("no sila entry found for %s\n", t.Harness)
	}
}

func removeConnectEntry(t connectTarget) (bool, error) {
	if t.Kind == "opencode" {
		changed := false
		for _, path := range connectTargetPaths(t) {
			removed, err := removeOpenCodeEntryAt(path)
			if err != nil {
				return false, err
			}
			changed = changed || removed
		}
		return changed, nil
	}
	switch t.Kind {
	case "codex":
		raw, err := os.ReadFile(t.Path)
		if err != nil {
			return false, err
		}
		out, changed := removeCodexSection(string(raw))
		if !changed {
			return false, nil
		}
		return true, os.WriteFile(t.Path, []byte(out), 0o600)
	default:
		raw, err := os.ReadFile(t.Path)
		if err != nil {
			return false, err
		}
		var root map[string]any
		if err := json.Unmarshal(raw, &root); err != nil {
			return false, err
		}
		changed := false
		switch t.Kind {
		case "zed":
			if servers, ok := root["context_servers"].(map[string]any); ok {
				if key, exists := findSilaKey(servers); exists {
					delete(servers, key)
					changed = true
				}
			}
		case "opencode":
			if mcp, ok := root["mcp"].(map[string]any); ok {
				if servers, ok := mcp["servers"].(map[string]any); ok {
					if key, exists := findSilaKey(servers); exists {
						delete(servers, key)
						changed = true
					}
				}
			}
		default:
			if servers, ok := root["mcpServers"].(map[string]any); ok {
				if key, exists := findSilaKey(servers); exists {
					delete(servers, key)
					changed = true
				}
			}
		}
		if !changed {
			return false, nil
		}
		return true, writeJSONPreservingMode(t.Path, root)
	}
}

func findSilaKey(servers map[string]any) (string, bool) {
	for key := range servers {
		if strings.EqualFold(key, "sila") {
			return key, true
		}
	}
	return "", false
}

func removeCodexSection(text string) (string, bool) {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	skipping := false
	changed := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			skipping = trimmed == "[mcp_servers.sila]" || trimmed == "[mcp_servers.sila.env]"
			if skipping {
				changed = true
				continue
			}
		}
		if skipping {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n"), changed
}

func runConnectVerify(name, dbPath string, jsonOut bool) {
	t, err := resolveConnectTarget(name)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sila connect:", err)
		os.Exit(2)
	}
	if agent := configAgent(t); agent == "" {
		fmt.Fprintf(os.Stderr, "FAIL %s: no sila entry with SILA_AGENT; run sila connect add %s\n", t.Harness, t.Harness)
		os.Exit(1)
	} else if agent != t.Harness {
		fmt.Fprintf(os.Stderr, "FAIL %s: config SILA_AGENT=%s, want %s\n", t.Harness, agent, t.Harness)
		os.Exit(1)
	}
	res := verifyHarness(t.Harness, dbPath)
	if jsonOut {
		emitJSON(res)
		if !res.OK {
			os.Exit(1)
		}
		return
	}
	if res.OK {
		fmt.Printf("PASS %s %d tools in %dms\n", res.Harness, res.Tools, res.DurationMS)
		return
	}
	fmt.Printf("FAIL %s: %s\n", res.Harness, res.Reason)
	os.Exit(1)
}

// verifyHarness performs a real MCP handshake against this binary.
func verifyHarness(harness, dbPath string) verifyResult {
	start := time.Now()
	res := verifyResult{Harness: harness}
	binary, err := os.Executable()
	if err != nil || binary == "" {
		binary = "sila"
	}
	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, "mcp")
	env := append(os.Environ(), "SILA_AGENT="+harness)
	if dbPath != "" {
		env = append(env, "SILA_DB="+dbPath)
	}
	cmd.Env = env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		res.Reason = err.Error()
		return res
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		res.Reason = err.Error()
		return res
	}
	if err := cmd.Start(); err != nil {
		res.Reason = "cannot start sila mcp: " + err.Error()
		return res
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	requests := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"sila-connect","version":"1"}}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
`
	go func() {
		_, _ = stdin.Write([]byte(requests))
	}()

	type reply struct {
		ID     int             `json:"id"`
		Result json.RawMessage `json:"result"`
	}
	lines := make(chan string, 4)
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()

	var initResult struct {
		ServerInfo struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
		Capabilities struct {
			Experimental struct {
				Sila struct {
					Profile             string `json:"profile"`
					ContinuityToolCount int    `json:"continuity_tool_count"`
				} `json:"sila"`
			} `json:"experimental"`
		} `json:"capabilities"`
	}
	var toolCount int
	seen := map[int]bool{}
	timeout := time.After(connectTimeout)
	for len(seen) < 2 {
		select {
		case <-timeout:
			res.Reason = "handshake timed out after 5s; check the sila binary path and retry"
			res.DurationMS = time.Since(start).Milliseconds()
			return res
		case line, ok := <-lines:
			if !ok {
				res.Reason = "server closed the connection before answering"
				res.DurationMS = time.Since(start).Milliseconds()
				return res
			}
			var r reply
			if json.Unmarshal([]byte(line), &r) != nil {
				continue
			}
			seen[r.ID] = true
			switch r.ID {
			case 1:
				if err := json.Unmarshal(r.Result, &initResult); err != nil {
					res.Reason = "invalid initialize result"
					return res
				}
			case 2:
				var tl struct {
					Tools []json.RawMessage `json:"tools"`
				}
				if err := json.Unmarshal(r.Result, &tl); err != nil {
					res.Reason = "invalid tools/list result"
					return res
				}
				toolCount = len(tl.Tools)
			}
		}
	}
	res.DurationMS = time.Since(start).Milliseconds()
	res.Tools = toolCount
	res.Profile = initResult.Capabilities.Experimental.Sila.Profile
	expected := initResult.Capabilities.Experimental.Sila.ContinuityToolCount
	switch {
	case initResult.ServerInfo.Name != "sila":
		res.Reason = fmt.Sprintf("server name %q, want sila", initResult.ServerInfo.Name)
	case res.Profile != "continuity":
		res.Reason = fmt.Sprintf("profile %q, want continuity", res.Profile)
	case expected > 0 && toolCount != expected:
		res.Reason = fmt.Sprintf("%d tools, want %d", toolCount, expected)
	case toolCount < 10:
		res.Reason = fmt.Sprintf("%d tools, want at least 10", toolCount)
	default:
		res.OK = true
	}
	return res
}

func runConnectDemo(dbPath string, jsonOut bool) {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "sila connect:", err)
		os.Exit(1)
	}
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sila connect: open store:", err)
		os.Exit(1)
	}
	defer st.Close()
	projectID := store.ProjectID(cwd)
	if _, err := st.EnsureProject(cwd); err != nil {
		fmt.Fprintf(os.Stderr, "sila connect: register project: %v\n", err)
		os.Exit(1)
	}
	brief, err := dream.BuildPrimeBrief(st, "", cwd, 10, 20000)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila connect: build prime: %v\n", err)
		os.Exit(1)
	}
	if brief == nil || brief.ActiveHandoff == nil {
		fmt.Fprintln(os.Stderr, `no handoff yet: run sila handoff "<summary>" first`)
		os.Exit(3)
	}
	payload := dream.FormatPrimeMarkdown(brief)
	if jsonOut {
		emitJSON(map[string]any{"demo": true, "project": projectID, "payload": payload})
		return
	}
	fmt.Println("demo payload: what the next harness receives from prime_context")
	fmt.Println(strings.Repeat("-", 72))
	fmt.Print(payload)
}

func removeOpenCodeEntryAt(path string) (bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return false, err
	}
	mcp, _ := root["mcp"].(map[string]any)
	servers, _ := mcp["servers"].(map[string]any)
	key, exists := findSilaKey(servers)
	if !exists {
		return false, nil
	}
	delete(servers, key)
	return true, writeJSONPreservingMode(path, root)
}
