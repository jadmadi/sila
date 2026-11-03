package registry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/harness/pluginbridge"
)

// writeHookConfig seeds a harness config file with raw JSON content.
func writeHookConfig(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// readHookConfig unmarshals a hook config file into a generic JSON object.
func readHookConfig(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return root
}

func TestInstallTrustAndRefusalRules(t *testing.T) {
	// Keep status resolution hermetic: no user-scope hooks from the host.
	t.Setenv("HOME", t.TempDir())

	// 1. ZCode project hooks refused with ProjectHooksIgnored=true
	dir := t.TempDir()
	zres, err := Install("zcode", dir, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !zres.Refused || !zres.ProjectHooksIgnored {
		t.Fatalf("zcode project install must be refused with ProjectHooksIgnored=true: %+v", zres)
	}
	zst := StatusFor("zcode", dir)
	if !zst.ProjectHooksIgnored || zst.Installed {
		t.Fatalf("zcode status must report ProjectHooksIgnored=true and Installed=false: %+v", zst)
	}

	// ZCode user install succeeds into ~/.zcode/cli/config.json
	home := t.TempDir()
	zuser, err := InstallUser("zcode", home, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !zuser.Installed || zuser.Refused {
		t.Fatalf("zcode user install failed: %+v", zuser)
	}
	if _, err := os.Stat(filepath.Join(home, ".zcode", "cli", "config.json")); err != nil {
		t.Fatalf("zcode user config not written: %v", err)
	}

	// 2. Trust-required harnesses (Codex, Codewhale, Muse) write config but report Installed=false until trusted
	for _, h := range []string{"codex", "codewhale", "muse"} {
		pdir := t.TempDir()
		res, err := Install(h, pdir, false, false)
		if err != nil {
			t.Fatalf("%s install error: %v", h, err)
		}
		if !res.Configured || !res.TrustRequired || res.Trusted || res.Installed {
			t.Fatalf("%s before trust must have Configured=true, Trusted=false, Installed=false: %+v", h, res)
		}
		st := StatusFor(h, pdir)
		if !st.Configured || st.Trusted || st.Installed {
			t.Fatalf("%s status before trust must have Configured=true, Trusted=false, Installed=false: %+v", h, st)
		}

		// Approve trust -> now Installed=true and Trusted=true
		if err := ApproveTrust(h, pdir); err != nil {
			t.Fatalf("%s approve trust: %v", h, err)
		}
		stAfter := StatusFor(h, pdir)
		if !stAfter.Trusted || !stAfter.Installed {
			t.Fatalf("%s status after trust approval must be Trusted=true, Installed=true: %+v", h, stAfter)
		}

		// Uninstall preserves foreign content and removes Sila hooks
		unres, err := Uninstall(h, pdir, false)
		if err != nil || !unres.Installed {
			t.Fatalf("%s uninstall failed: %+v %v", h, unres, err)
		}
	}

	// 3. Codewhale TOML foreign preservation and digest invalidation on tamper
	cwDir := t.TempDir()
	cwPath := filepath.Join(cwDir, ".codewhale", "hooks.toml")
	if err := os.MkdirAll(filepath.Dir(cwPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cwPath, []byte("# foreign comment\n[[hooks]]\nevent = \"custom\"\ncommand = \"echo hi\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Install("codewhale", cwDir, false, false); err != nil {
		t.Fatal(err)
	}
	if err := ApproveTrust("codewhale", cwDir); err != nil {
		t.Fatal(err)
	}
	if st := StatusFor("codewhale", cwDir); !st.Installed {
		t.Fatalf("codewhale should be installed after digest approval: %+v", st)
	}
	// Tamper with hooks.toml -> digest mismatch -> Installed=false
	raw, _ := os.ReadFile(cwPath)
	_ = os.WriteFile(cwPath, append(raw, []byte("\n# tampered\n")...), 0o600)
	if st := StatusFor("codewhale", cwDir); st.Trusted || st.Installed {
		t.Fatalf("codewhale should lose trust after file modification: %+v", st)
	}
	// Uninstall preserves foreign hook
	if _, err := Uninstall("codewhale", cwDir, false); err != nil {
		t.Fatal(err)
	}
	afterUninstall, _ := os.ReadFile(cwPath)
	if !strings.Contains(string(afterUninstall), "echo hi") || strings.Contains(string(afterUninstall), "sila hook") {
		t.Fatalf("codewhale uninstall did not preserve foreign or left sila hook:\n%s", afterUninstall)
	}

	// 4. Full-block / no-trust-required shell harnesses (claude, cursor, devin, command-code, antigravity)
	for _, h := range []string{"claude", "cursor", "devin", "command-code", "antigravity"} {
		pdir := t.TempDir()
		res, err := Install(h, pdir, false, false)
		if err != nil || !res.Installed {
			t.Fatalf("%s install failed: %+v %v", h, res, err)
		}
		// Second install is idempotent (Skipped=true)
		res2, err := Install(h, pdir, false, false)
		if err != nil || !res2.Skipped {
			t.Fatalf("%s second install not idempotent: %+v %v", h, res2, err)
		}
		st := StatusFor(h, pdir)
		if !st.Supported || !st.Installed || !st.Trusted || st.DocsURL == "" || len(st.Capabilities) == 0 {
			t.Fatalf("%s status incomplete: %+v", h, st)
		}
	}
}

func TestInstall_ClaudeNestedDialect(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude", "settings.json")
	writeHookConfig(t, path, `{
  "model": "opus",
  "hooks": {
    "SessionStart": [
      {"hooks": [{"type": "command", "command": "herdr hook session-start"}]}
    ]
  }
}`)

	res, err := Install("claude", dir, false, false)
	if err != nil || !res.Installed || res.Refused {
		t.Fatalf("claude install failed: %+v %v", res, err)
	}
	root := readHookConfig(t, path)
	if root["model"] != "opus" {
		t.Fatalf("claude install dropped foreign top-level key: %#v", root)
	}
	hooks, ok := root["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("claude hooks must be an object: %#v", root["hooks"])
	}
	sessionStart := toSlice(hooks["SessionStart"])
	if len(sessionStart) != 2 {
		t.Fatalf("claude SessionStart must keep foreign and add sila: %#v", sessionStart)
	}
	foreignKept := false
	for _, raw := range sessionStart {
		if commandContains(raw, "herdr hook session-start") {
			foreignKept = true
		}
	}
	if !foreignKept {
		t.Fatalf("claude foreign SessionStart entry lost: %#v", sessionStart)
	}
	silaEntry := toSlice(hooks["SessionStart"])[1].(map[string]any)
	nested := toSlice(silaEntry["hooks"])
	if len(nested) != 1 {
		t.Fatalf("claude must write a nested hooks wrapper: %#v", silaEntry)
	}
	if cmd, _ := nested[0].(map[string]any)["command"].(string); cmd != "sila hook session-start --json" {
		t.Fatalf("claude session-start command = %q", cmd)
	}
	preToolUse := toSlice(hooks["PreToolUse"])
	foundMatcher := false
	for _, raw := range preToolUse {
		entry := raw.(map[string]any)
		if entry["matcher"] != "Bash" {
			continue
		}
		foundMatcher = commandContains(entry["hooks"], "sila hook pre-tool-use --json")
	}
	if !foundMatcher {
		t.Fatalf("claude PreToolUse must be matcher-grouped with Bash: %#v", preToolUse)
	}

	if st := StatusFor("claude", dir); !st.Installed || !st.Configured {
		t.Fatalf("claude status must be installed: %+v", st)
	}
	if _, err := Uninstall("claude", dir, false); err != nil {
		t.Fatal(err)
	}
	after := readHookConfig(t, path)
	raw, _ := json.Marshal(after)
	if strings.Contains(string(raw), "sila hook") {
		t.Fatalf("claude uninstall left sila hooks: %s", raw)
	}
	if !strings.Contains(string(raw), "herdr hook session-start") || after["model"] != "opus" {
		t.Fatalf("claude uninstall dropped foreign content: %s", raw)
	}
}

func TestInstall_CursorFlatDialect(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, ".cursor", "hooks.json")
	writeHookConfig(t, path, `{
  "version": 1,
  "hooks": {
    "sessionStart": [
      {"command": "orca-status cursor sessionStart", "timeout": 5},
      {"command": "herdr hook session-start", "timeout": 5}
    ],
    "stop": [
      {"hooks": [{"type": "command", "command": "sila hook stop --json"}]}
    ]
  }
}`)

	res, err := Install("cursor", dir, false, false)
	if err != nil || !res.Installed || res.Refused {
		t.Fatalf("cursor install failed: %+v %v", res, err)
	}
	root := readHookConfig(t, path)
	if version, ok := root["version"].(float64); !ok || version != 1 {
		t.Fatalf("cursor hooks.json must keep version 1: %#v", root["version"])
	}
	hooks, ok := root["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("cursor hooks must be an object: %#v", root["hooks"])
	}
	sp, _ := Lookup("cursor")
	for _, ev := range sp.Events {
		entries := toSlice(hooks[ev.NativeName])
		found := false
		for _, raw := range entries {
			entry := raw.(map[string]any)
			if _, nested := entry["hooks"]; nested {
				t.Fatalf("cursor %s entry must not use a nested hooks wrapper: %#v", ev.NativeName, entry)
			}
			cmd, _ := entry["command"].(string)
			if !strings.Contains(cmd, ev.Command) {
				continue
			}
			found = true
			if timeout, ok := entry["timeout"].(float64); !ok || timeout != 10 {
				t.Fatalf("cursor %s handler must carry timeout 10: %#v", ev.NativeName, entry)
			}
		}
		if !found {
			t.Fatalf("cursor %s missing flat Sila handler: %#v", ev.NativeName, entries)
		}
	}
	sessionStart := toSlice(hooks["sessionStart"])
	if len(sessionStart) != 3 {
		t.Fatalf("cursor sessionStart must keep two foreign handlers and add one Sila handler: %#v", sessionStart)
	}
	for _, want := range []string{"orca-status cursor sessionStart", "herdr hook session-start"} {
		kept := false
		for _, raw := range sessionStart {
			if entry, ok := raw.(map[string]any); ok {
				if cmd, _ := entry["command"].(string); cmd == want {
					kept = true
				}
			}
		}
		if !kept {
			t.Fatalf("cursor foreign handler %q lost: %#v", want, sessionStart)
		}
	}

	if st := StatusFor("cursor", dir); !st.Installed || !st.Configured {
		t.Fatalf("cursor status must be installed: %+v", st)
	}
	res2, err := Install("cursor", dir, false, false)
	if err != nil || !res2.Skipped {
		t.Fatalf("cursor second install must be idempotent: %+v %v", res2, err)
	}

	if _, err := Uninstall("cursor", dir, false); err != nil {
		t.Fatal(err)
	}
	after := readHookConfig(t, path)
	if version, ok := after["version"].(float64); !ok || version != 1 {
		t.Fatalf("cursor uninstall must preserve version 1: %#v", after["version"])
	}
	afterBytes, _ := json.Marshal(after)
	if strings.Contains(string(afterBytes), "sila hook") {
		t.Fatalf("cursor uninstall left Sila hooks: %s", afterBytes)
	}
	afterHooks := after["hooks"].(map[string]any)
	if remaining := toSlice(afterHooks["sessionStart"]); len(remaining) != 2 {
		t.Fatalf("cursor uninstall must keep both foreign handlers: %#v", remaining)
	}
	if _, exists := afterHooks["stop"]; exists {
		t.Fatalf("cursor uninstall must delete the emptied stop event: %#v", afterHooks)
	}
}

func TestStatusCursorRejectsLegacyNestedDialect(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, ".cursor", "hooks.json")
	writeHookConfig(t, path, `{
  "hooks": {
    "sessionStart": [{"hooks": [{"type": "command", "command": "sila hook session-start --json"}]}],
    "preToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "sila hook pre-tool-use --json"}]}]
  }
}`)
	if st := StatusFor("cursor", dir); st.Installed || st.Configured {
		t.Fatalf("cursor legacy nested Sila entries must not count as installed: %+v", st)
	}

	if _, err := Install("cursor", dir, false, false); err != nil {
		t.Fatal(err)
	}
	root := readHookConfig(t, path)
	hooks := root["hooks"].(map[string]any)
	for _, native := range []string{"sessionStart", "preToolUse"} {
		for _, raw := range toSlice(hooks[native]) {
			if _, nested := raw.(map[string]any)["hooks"]; nested {
				t.Fatalf("cursor install did not heal legacy nested entry for %s: %#v", native, raw)
			}
		}
	}
	if st := StatusFor("cursor", dir); !st.Installed {
		t.Fatalf("cursor status must be installed after healing: %+v", st)
	}
}

func TestInstall_AntigravityNamedDialect(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, ".agents", "hooks.json")
	seed := `{
  "herdr": {
    "enabled": true,
    "SessionStart": [
      {"type": "command", "command": "/opt/herdr hook session-start", "timeout": 5}
    ],
    "PreToolUse": [
      {"matcher": "*", "hooks": [{"type": "command", "command": "/opt/herdr hook pre-tool-use", "timeout": 5}]}
    ]
  },
  "orca-status": {
    "enabled": true,
    "PreInvocation": [
      {"type": "command", "command": "orca-status antigravity PreInvocation", "timeout": 5}
    ]
  },
  "sila": {
    "enabled": true,
    "SessionStart": [
      {"type": "command", "command": "echo keep-me", "timeout": 5}
    ]
  },
  "hooks": {
    "SessionStart": [
      {"hooks": [{"type": "command", "command": "sila hook session-start --json"}]}
    ]
  }
}`
	writeHookConfig(t, path, seed)
	var seedRoot map[string]any
	if err := json.Unmarshal([]byte(seed), &seedRoot); err != nil {
		t.Fatal(err)
	}

	res, err := Install("antigravity", dir, false, false)
	if err != nil || !res.Installed || res.Refused {
		t.Fatalf("antigravity install failed: %+v %v", res, err)
	}
	root := readHookConfig(t, path)
	if _, exists := root["hooks"]; exists {
		t.Fatalf("antigravity install must remove legacy top-level hooks: %#v", root["hooks"])
	}
	for _, sibling := range []string{"herdr", "orca-status"} {
		if !reflect.DeepEqual(root[sibling], seedRoot[sibling]) {
			t.Fatalf("antigravity install mutated foreign group %q:\ngot  %#v\nwant %#v", sibling, root[sibling], seedRoot[sibling])
		}
	}
	group, ok := root["sila"].(map[string]any)
	if !ok {
		t.Fatalf("antigravity config must contain a named sila group: %#v", root)
	}
	if enabled, ok := group["enabled"].(bool); !ok || !enabled {
		t.Fatalf("antigravity sila group must be enabled: %#v", group)
	}
	sp, _ := Lookup("antigravity")
	for _, ev := range sp.Events {
		entries := toSlice(group[ev.NativeName])
		if antigravityEventUsesMatcherGroup(ev.NativeName) {
			found := false
			for _, raw := range entries {
				entry, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				if _, hasMatcher := entry["matcher"]; !hasMatcher {
					t.Fatalf("antigravity %s entry must be matcher-grouped: %#v", ev.NativeName, entry)
				}
				for _, rawHandler := range toSlice(entry["hooks"]) {
					handler := rawHandler.(map[string]any)
					cmd, _ := handler["command"].(string)
					if !strings.Contains(cmd, ev.Command) {
						continue
					}
					found = true
					if timeout, ok := handler["timeout"].(float64); !ok || timeout != 10 {
						t.Fatalf("antigravity %s handler must carry timeout 10: %#v", ev.NativeName, handler)
					}
				}
			}
			if !found {
				t.Fatalf("antigravity %s missing matcher-grouped Sila handler: %#v", ev.NativeName, entries)
			}
			continue
		}
		found := false
		for _, raw := range entries {
			handler := raw.(map[string]any)
			if _, nested := handler["hooks"]; nested {
				t.Fatalf("antigravity %s must stay a flat handler array: %#v", ev.NativeName, handler)
			}
			cmd, _ := handler["command"].(string)
			if !strings.Contains(cmd, ev.Command) {
				continue
			}
			found = true
			if timeout, ok := handler["timeout"].(float64); !ok || timeout != 10 {
				t.Fatalf("antigravity %s handler must carry timeout 10: %#v", ev.NativeName, handler)
			}
		}
		if !found {
			t.Fatalf("antigravity %s missing flat Sila handler: %#v", ev.NativeName, entries)
		}
	}
	sessionStart := toSlice(group["SessionStart"])
	if len(sessionStart) != 2 || !commandContains(sessionStart, "echo keep-me") {
		t.Fatalf("antigravity install must preserve foreign sila-group handlers: %#v", sessionStart)
	}

	if st := StatusFor("antigravity", dir); !st.Installed || !st.Configured {
		t.Fatalf("antigravity status must be installed: %+v", st)
	}
	res2, err := Install("antigravity", dir, false, false)
	if err != nil || !res2.Skipped {
		t.Fatalf("antigravity second install must be idempotent: %+v %v", res2, err)
	}

	if _, err := Uninstall("antigravity", dir, false); err != nil {
		t.Fatal(err)
	}
	after := readHookConfig(t, path)
	for _, sibling := range []string{"herdr", "orca-status"} {
		if !reflect.DeepEqual(after[sibling], seedRoot[sibling]) {
			t.Fatalf("antigravity uninstall mutated foreign group %q:\ngot  %#v\nwant %#v", sibling, after[sibling], seedRoot[sibling])
		}
	}
	afterGroup, ok := after["sila"].(map[string]any)
	if !ok {
		t.Fatalf("antigravity uninstall must keep the sila group while foreign handlers remain: %#v", after)
	}
	afterBytes, _ := json.Marshal(after)
	if strings.Contains(string(afterBytes), "sila hook") {
		t.Fatalf("antigravity uninstall left Sila hooks: %s", afterBytes)
	}
	if remaining := toSlice(afterGroup["SessionStart"]); len(remaining) != 1 || !commandContains(remaining, "echo keep-me") {
		t.Fatalf("antigravity uninstall must preserve foreign sila-group handlers: %#v", remaining)
	}
}

func TestStatusAntigravityRejectsWrongDialect(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	legacyDir := t.TempDir()
	writeHookConfig(t, filepath.Join(legacyDir, ".agents", "hooks.json"), `{
  "hooks": {
    "SessionStart": [{"hooks": [{"type": "command", "command": "sila hook session-start --json"}]}]
  }
}`)
	if st := StatusFor("antigravity", legacyDir); st.Installed || st.Configured {
		t.Fatalf("antigravity legacy top-level hooks must not count as installed: %+v", st)
	}

	disabledDir := t.TempDir()
	writeHookConfig(t, filepath.Join(disabledDir, ".agents", "hooks.json"), `{
  "sila": {
    "enabled": false,
    "SessionStart": [{"type": "command", "command": "sila hook session-start --json"}]
  }
}`)
	st := StatusFor("antigravity", disabledDir)
	if st.Installed || st.Configured {
		t.Fatalf("disabled antigravity sila group must not count as installed: %+v", st)
	}
	if !strings.Contains(st.Detail, "disabled") {
		t.Fatalf("disabled antigravity status detail = %q, want disabled notice", st.Detail)
	}
}

func TestUserScopeLifecycleRoundTrip(t *testing.T) {
	cases := []struct{ harness, userRel string }{
		{"claude", ".claude/settings.json"},
		{"cursor", ".cursor/hooks.json"},
		{"antigravity", ".gemini/config/hooks.json"},
		{"command-code", ".commandcode/settings.json"},
		{"zcode", ".zcode/cli/config.json"},
		{"opencode", ".config/opencode/plugins/" + pluginbridge.BridgeFilename},
	}
	for _, tc := range cases {
		t.Run(tc.harness, func(t *testing.T) {
			home := t.TempDir()
			projectDir := t.TempDir()
			t.Setenv("HOME", home)

			res, err := InstallUser(tc.harness, home, false, false)
			if err != nil || !res.Installed || res.Refused {
				t.Fatalf("%s InstallUser failed: %+v %v", tc.harness, res, err)
			}
			userPath := filepath.Join(home, filepath.FromSlash(tc.userRel))
			if _, err := os.Stat(userPath); err != nil {
				t.Fatalf("%s user config missing at %s: %v", tc.harness, userPath, err)
			}
			entries, err := os.ReadDir(projectDir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("%s user install polluted the project dir: %v", tc.harness, entries)
			}

			userSt := StatusForUser(tc.harness, home)
			if !userSt.Installed || !userSt.Configured || userSt.Scope != "user" || userSt.Path != userPath {
				t.Fatalf("%s StatusForUser = %+v, want installed user scope at %s", tc.harness, userSt, userPath)
			}
			st := StatusFor(tc.harness, projectDir)
			if !st.Installed || st.Scope != "user" || st.Path != userPath {
				t.Fatalf("%s StatusFor user fallback = %+v, want installed user scope at %s", tc.harness, st, userPath)
			}
			if tc.harness == "zcode" && !st.ProjectHooksIgnored {
				t.Fatalf("zcode user status must preserve ProjectHooksIgnored: %+v", st)
			}

			unres, err := UninstallUser(tc.harness, home, false)
			if err != nil || !unres.Installed {
				t.Fatalf("%s UninstallUser failed: %+v %v", tc.harness, unres, err)
			}
			if st := StatusForUser(tc.harness, home); st.Installed || st.Configured {
				t.Fatalf("%s still installed at user scope after uninstall: %+v", tc.harness, st)
			}
			if st := StatusFor(tc.harness, projectDir); st.Installed {
				t.Fatalf("%s StatusFor still installed after user uninstall: %+v", tc.harness, st)
			}
		})
	}
}

func TestStatusProjectScopeTakesPrecedenceOverUser(t *testing.T) {
	home := t.TempDir()
	projectDir := t.TempDir()
	t.Setenv("HOME", home)

	if res, err := InstallUser("cursor", home, false, false); err != nil || !res.Installed {
		t.Fatalf("user install failed: %+v %v", res, err)
	}
	if res, err := Install("cursor", projectDir, false, false); err != nil || !res.Installed {
		t.Fatalf("project install failed: %+v %v", res, err)
	}
	st := StatusFor("cursor", projectDir)
	if st.Scope != "project" || st.Path != filepath.Join(projectDir, ".cursor", "hooks.json") {
		t.Fatalf("project scope must take precedence: %+v", st)
	}
}

func TestZCodeUserScopeLifecycle(t *testing.T) {
	home := t.TempDir()
	projectDir := t.TempDir()
	t.Setenv("HOME", home)

	res, err := Install("zcode", projectDir, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Refused || !res.ProjectHooksIgnored {
		t.Fatalf("zcode project install must stay refused: %+v", res)
	}
	if entries, err := os.ReadDir(projectDir); err != nil || len(entries) != 0 {
		t.Fatalf("zcode project install wrote files: %v %v", entries, err)
	}

	if _, err := InstallUser("zcode", home, false, false); err != nil {
		t.Fatal(err)
	}
	st := StatusFor("zcode", projectDir)
	if !st.Installed || st.Scope != "user" || !st.ProjectHooksIgnored {
		t.Fatalf("zcode status must resolve user scope: %+v", st)
	}
	if st.Path != filepath.Join(home, ".zcode", "cli", "config.json") {
		t.Fatalf("zcode user path = %q", st.Path)
	}

	if unres, err := Uninstall("zcode", projectDir, false); err != nil || !unres.Refused {
		t.Fatalf("zcode project uninstall must stay refused: %+v %v", unres, err)
	}
	if _, err := UninstallUser("zcode", home, false); err != nil {
		t.Fatal(err)
	}
	st = StatusFor("zcode", projectDir)
	if st.Installed || !st.ProjectHooksIgnored {
		t.Fatalf("zcode status after user uninstall = %+v", st)
	}
	if !strings.Contains(st.Detail, "--user") {
		t.Fatalf("zcode uninstalled detail should point at --user: %q", st.Detail)
	}
}

func TestUserScopePreservesForeignHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cursorPath := filepath.Join(home, ".cursor", "hooks.json")
	writeHookConfig(t, cursorPath, `{
  "version": 1,
  "hooks": {
    "sessionStart": [{"command": "orca-status cursor sessionStart", "timeout": 5}]
  }
}`)
	if _, err := InstallUser("cursor", home, false, false); err != nil {
		t.Fatal(err)
	}
	root := readHookConfig(t, cursorPath)
	hooks := root["hooks"].(map[string]any)
	if !commandContains(hooks["sessionStart"], "orca-status") || !commandContains(hooks["sessionStart"], "sila hook ") {
		t.Fatalf("cursor user install lost foreign or Sila handler: %#v", hooks["sessionStart"])
	}
	if _, err := UninstallUser("cursor", home, false); err != nil {
		t.Fatal(err)
	}
	root = readHookConfig(t, cursorPath)
	hooks = root["hooks"].(map[string]any)
	if !commandContains(hooks["sessionStart"], "orca-status") {
		t.Fatalf("cursor user uninstall removed foreign handler: %#v", hooks["sessionStart"])
	}
	raw, _ := json.Marshal(root)
	if strings.Contains(string(raw), "sila hook") {
		t.Fatalf("cursor user uninstall left Sila hooks: %s", raw)
	}

	agyPath := filepath.Join(home, ".gemini", "config", "hooks.json")
	seed := `{"herdr":{"enabled":true,"SessionStart":[{"type":"command","command":"herdr hook session-start","timeout":5}]}}`
	writeHookConfig(t, agyPath, seed)
	if _, err := InstallUser("antigravity", home, false, false); err != nil {
		t.Fatal(err)
	}
	if _, err := UninstallUser("antigravity", home, false); err != nil {
		t.Fatal(err)
	}
	root = readHookConfig(t, agyPath)
	var want map[string]any
	if err := json.Unmarshal([]byte(seed), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(root, want) {
		t.Fatalf("antigravity user uninstall mutated foreign config:\ngot  %#v\nwant %#v", root, want)
	}
}

func TestInstallUserCodewhaleTOML(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".codewhale", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# foreign\n[[hooks]]\nevent = \"custom\"\ncommand = \"echo hi\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := InstallUser("codewhale", home, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Configured || !res.TrustRequired || res.Installed {
		t.Fatalf("codewhale user install must be configured but untrusted: %+v", res)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "sila:begin hooks") || !strings.Contains(string(data), "echo hi") {
		t.Fatalf("codewhale user TOML not written correctly:\n%s", data)
	}
	st := StatusForUser("codewhale", home)
	if !st.Configured || st.Installed || st.Scope != "user" {
		t.Fatalf("codewhale user status = %+v", st)
	}

	if _, err := UninstallUser("codewhale", home, false); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "sila hook") || !strings.Contains(string(data), "echo hi") {
		t.Fatalf("codewhale user uninstall did not preserve foreign content:\n%s", data)
	}
}
