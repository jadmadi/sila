package hookconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readSettings(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, data)
	}
	return root
}

func commandContains(v any, prefix string) bool {
	switch value := v.(type) {
	case map[string]any:
		if cmd, ok := value["command"].(string); ok && strings.Contains(cmd, prefix) {
			return true
		}
		for _, child := range value {
			if commandContains(child, prefix) {
				return true
			}
		}
	case []any:
		for _, child := range value {
			if commandContains(child, prefix) {
				return true
			}
		}
	}
	return false
}

func TestInstallMergeIdempotentAndUninstall(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"echo foreign"}]}]},"permissions":{"allow":["Read"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	first, err := Install("claude", dir, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Installed || first.Skipped || first.Refused {
		t.Fatalf("unexpected first install: %+v", first)
	}
	root := readSettings(t, path)
	if root["permissions"] == nil {
		t.Fatal("foreign top-level key was not preserved")
	}
	hooks := root["hooks"].(map[string]any)
	if !commandContains(hooks["SessionStart"], "echo foreign") {
		t.Fatal("foreign hook was not preserved")
	}
	if !commandContains(hooks["SessionStart"], "sila hook session-start") {
		t.Fatal("Sila SessionStart hook missing")
	}
	if !commandContains(hooks["Stop"], "sila hook stop") {
		t.Fatal("Sila Stop hook missing")
	}

	second, err := Install("claude", dir, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Skipped {
		t.Fatalf("second install is not idempotent: %+v", second)
	}

	removed, err := Uninstall("claude", dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if !removed.Installed {
		t.Fatalf("uninstall did not remove hooks: %+v", removed)
	}
	root = readSettings(t, path)
	hooks = root["hooks"].(map[string]any)
	if !commandContains(hooks["SessionStart"], "echo foreign") {
		t.Fatal("uninstall removed the foreign hook")
	}
	if commandContains(hooks, "sila hook ") {
		t.Fatal("uninstall left a Sila hook behind")
	}
}

func TestInstallCommandCodeAndDryRun(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	res, err := Install("command-code", dir, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Installed || len(res.Plan) == 0 {
		t.Fatalf("unexpected dry-run result: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, ".commandcode", "settings.json")); !os.IsNotExist(err) {
		t.Fatal("dry-run wrote a file")
	}

	res, err = Install("command-code", dir, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Installed {
		t.Fatalf("command-code install failed: %+v", res)
	}
	status := StatusFor("command-code", dir)
	if !status.Supported || !status.Installed {
		t.Fatalf("unexpected status: %+v", status)
	}
}

func TestUnsupportedHarnessAndInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	res, err := Install("unknown-harness", dir, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Refused || !strings.Contains(res.Detail, "not documented") {
		t.Fatalf("unknown-harness should be unsupported: %+v", res)
	}
	if status := StatusFor("unknown-harness", dir); status.Supported {
		t.Fatalf("unknown-harness should be unsupported: %+v", status)
	}

	path := filepath.Join(dir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{`), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err = Install("claude", dir, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Refused {
		t.Fatalf("invalid JSON should be refused: %+v", res)
	}
}

func TestInstallReplacesNonObjectHooksOnlyWithReplace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"hooks":"foreign"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := Install("claude", dir, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Refused {
		t.Fatalf("non-object hooks should be refused: %+v", res)
	}
	res, err = Install("claude", dir, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Installed {
		t.Fatalf("--replace should install: %+v", res)
	}
}

func TestUserScopeWrappers(t *testing.T) {
	home := t.TempDir()
	projectDir := t.TempDir()
	t.Setenv("HOME", home)

	res, err := InstallUser("cursor", home, false, false)
	if err != nil || !res.Installed || res.Refused {
		t.Fatalf("cursor user install failed: %+v %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".cursor", "hooks.json")); err != nil {
		t.Fatalf("cursor user config missing: %v", err)
	}

	st := StatusForUser("cursor", home)
	if !st.Installed || !st.Configured || st.Scope != "user" {
		t.Fatalf("StatusForUser cursor = %+v", st)
	}
	st = StatusFor("cursor", projectDir)
	if !st.Installed || st.Scope != "user" {
		t.Fatalf("StatusFor cursor must fall back to user scope: %+v", st)
	}

	unres, err := UninstallUser("cursor", home, false)
	if err != nil || !unres.Installed {
		t.Fatalf("cursor user uninstall failed: %+v %v", unres, err)
	}
	if st := StatusForUser("cursor", home); st.Installed {
		t.Fatalf("cursor still installed after user uninstall: %+v", st)
	}
}

func TestZCodeUserScopeWrapper(t *testing.T) {
	home := t.TempDir()
	projectDir := t.TempDir()
	t.Setenv("HOME", home)

	projectRes, err := Install("zcode", projectDir, false, false)
	if err != nil || !projectRes.Refused || !projectRes.ProjectHooksIgnored {
		t.Fatalf("zcode project install must stay refused: %+v %v", projectRes, err)
	}

	userRes, err := InstallUser("zcode", home, false, false)
	if err != nil || !userRes.Installed {
		t.Fatalf("zcode user install failed: %+v %v", userRes, err)
	}
	st := StatusFor("zcode", projectDir)
	if !st.Installed || st.Scope != "user" || !st.ProjectHooksIgnored {
		t.Fatalf("zcode user status = %+v", st)
	}

	if _, err := UninstallUser("zcode", home, false); err != nil {
		t.Fatal(err)
	}
	if st := StatusFor("zcode", projectDir); st.Installed || !st.ProjectHooksIgnored {
		t.Fatalf("zcode status after user uninstall = %+v", st)
	}
}

func TestCompactionCapabilityMatrix(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	commandCode := StatusFor("command-code", dir)
	if commandCode.CompactionSupported {
		t.Fatal("command-code must report compaction unsupported")
	}
	if !strings.Contains(commandCode.CompactionDetail, "no documented compaction event") {
		t.Fatalf("command-code detail = %q", commandCode.CompactionDetail)
	}

	claude := StatusFor("claude", dir)
	if !claude.CompactionSupported {
		t.Fatal("claude must report compaction supported")
	}
	if claude.CompactionInstalled {
		t.Fatal("claude compaction must not be reported installed before install")
	}
	if _, err := Install("claude", dir, false, false); err != nil {
		t.Fatal(err)
	}
	claude = StatusFor("claude", dir)
	if !claude.CompactionInstalled {
		t.Fatal("claude PreCompact hook was not reported installed")
	}
}
