package registry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAntigravitySurfacesDetected(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".gemini", "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".config", "Antigravity"), 0o755); err != nil {
		t.Fatal(err)
	}
	surfaces := AntigravitySurfaces(home)
	byName := map[string]HookSurface{}
	for _, s := range surfaces {
		byName[s.Name] = s
	}
	if !byName["cli"].Detected || !byName["app"].Detected {
		t.Fatalf("surfaces = %+v", surfaces)
	}
	if byName["ide"].Detected {
		t.Fatalf("ide should not be detected: %+v", byName["ide"])
	}
}

func TestInstallUserSurfaceAllPreservesForeignHooks(t *testing.T) {
	home := t.TempDir()
	for _, dir := range []string{
		filepath.Join(home, ".gemini", "config"),
		filepath.Join(home, ".config", "Antigravity"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	appPath := filepath.Join(home, ".config", "Antigravity", "hooks.json")
	if err := os.WriteFile(appPath, []byte(`{"herdr":{"SessionStart":[{"command":"herdr"}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := InstallUserSurface("antigravity", home, "all", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Refused {
		t.Fatalf("install refused: %+v", res)
	}
	for _, path := range []string{
		filepath.Join(home, ".gemini", "config", "hooks.json"),
		appPath,
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if !strings.Contains(string(data), `"sila"`) || !strings.Contains(string(data), "SessionStart") {
			t.Fatalf("%s missing Sila group:\n%s", path, data)
		}
	}
	appData, _ := os.ReadFile(appPath)
	if !strings.Contains(string(appData), "herdr") {
		t.Fatalf("foreign hook removed:\n%s", appData)
	}
	st := StatusForUser("antigravity", home)
	installed := 0
	for _, s := range st.Surfaces {
		if s.Installed && s.SessionStart {
			installed++
		}
	}
	if installed < 2 {
		t.Fatalf("surface status = %+v", st.Surfaces)
	}
}

func TestInstallUserSurfaceSingleAndUndetected(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".config", "Antigravity"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallUserSurface("antigravity", home, "app", false, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".gemini", "config", "hooks.json")); !os.IsNotExist(err) {
		t.Fatalf("CLI surface should not be written: %v", err)
	}
	res, err := InstallUserSurface("antigravity", home, "ide", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Refused || !strings.Contains(res.Detail, "not detected") {
		t.Fatalf("undetected surface = %+v", res)
	}
	res, err = InstallUserSurface("antigravity", home, "unknown", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Refused || !strings.Contains(res.Detail, "unknown") {
		t.Fatalf("unknown surface = %+v", res)
	}
}

func TestTrustApprovalReceipt(t *testing.T) {
	home := t.TempDir()
	if TrustApproved("codex", home) {
		t.Fatal("trust approved before receipt")
	}
	if err := ApproveTrust("codex", home); err != nil {
		t.Fatal(err)
	}
	if !TrustApproved("codex", home) {
		t.Fatal("trust receipt not recognized")
	}
	if TrustApproved("codewhale", home) {
		t.Fatal("trust receipt leaked across harnesses")
	}
}
