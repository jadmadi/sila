package pluginbridge

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenCodeInstallIdempotentAndUninstallPreservesForeign(t *testing.T) {
	dir := t.TempDir()
	foreignPath := filepath.Join(dir, ".opencode", "plugins", "foreign-plugin.js")
	if err := os.MkdirAll(filepath.Dir(foreignPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(foreignPath, []byte("// foreign plugin\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Dry run first
	dryRes, err := Install("opencode", dir, "v1.0.0", true)
	if err != nil {
		t.Fatalf("dry-run install failed: %v", err)
	}
	if !dryRes.Installed || len(dryRes.Plan) == 0 {
		t.Fatalf("expected dry-run plan, got %+v", dryRes)
	}
	if ok, _ := IsInstalled("opencode", dir); ok {
		t.Fatalf("dry-run should not write bridge file")
	}

	// Real install
	res1, err := Install("opencode", dir, "v1.0.0", false)
	if err != nil {
		t.Fatalf("install failed: %v", err)
	}
	if !res1.Installed || res1.Skipped || res1.Refused {
		t.Fatalf("unexpected first install result: %+v", res1)
	}

	bridgePath := filepath.Join(dir, ".opencode", "plugins", BridgeFilename)
	info, err := os.Stat(bridgePath)
	if err != nil {
		t.Fatalf("stat bridge file: %v", err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("expected mode 0644, got %o", info.Mode().Perm())
	}

	// Second install is idempotent no-op
	res2, err := Install("opencode", dir, "v1.0.0", false)
	if err != nil {
		t.Fatalf("second install failed: %v", err)
	}
	if !res2.Installed || !res2.Skipped {
		t.Fatalf("expected second install to be skipped (idempotent), got %+v", res2)
	}

	// Uninstall removes Sila bridge and preserves foreign plugin
	unRes, err := Uninstall("opencode", dir, false)
	if err != nil {
		t.Fatalf("uninstall failed: %v", err)
	}
	if !unRes.Installed {
		t.Fatalf("expected uninstall to report removed bridge, got %+v", unRes)
	}
	if _, err := os.Stat(bridgePath); !os.IsNotExist(err) {
		t.Fatalf("expected bridge file removed, stat err: %v", err)
	}
	if _, err := os.Stat(foreignPath); err != nil {
		t.Fatalf("expected foreign plugin preserved, stat err: %v", err)
	}
}

func TestMiMoCodeInstallVerificationAndForeignPreservation(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SILA_MIMOCODE_PLUGIN_VERIFIED", "")

	// Without mimocode.json or env verification, install must refuse with unsupported
	refused, err := Install("mimocode", dir, "v1.0.0", false)
	if err != nil {
		t.Fatalf("unexpected error on unverified mimocode: %v", err)
	}
	if !refused.Refused || !strings.Contains(refused.Detail, "unsupported") {
		t.Fatalf("expected refused unsupported result, got %+v", refused)
	}

	// Create mimocode.json with a foreign plugin in the "plugin" array
	cfgPath := filepath.Join(dir, "mimocode.json")
	initialCfg := `{"plugin": ["foreign-mimo-plugin"], "theme": "light"}` + "\n"
	if err := os.WriteFile(cfgPath, []byte(initialCfg), 0o644); err != nil {
		t.Fatal(err)
	}

	// Now install succeeds and appends Sila bridge once
	res1, err := Install("mimocode", dir, "v1.0.0", false)
	if err != nil {
		t.Fatalf("mimocode install with plugin array failed: %v", err)
	}
	if !res1.Installed || res1.Refused {
		t.Fatalf("expected mimocode install success, got %+v", res1)
	}

	// Second install is idempotent and does not duplicate entry in mimocode.json
	res2, err := Install("mimocode", dir, "v1.0.0", false)
	if err != nil {
		t.Fatalf("second mimocode install failed: %v", err)
	}
	if !res2.Skipped {
		t.Fatalf("expected second mimocode install to be skipped, got %+v", res2)
	}
	cfgBytes, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(cfgBytes), BridgeFilename) != 1 {
		t.Fatalf("expected exactly 1 bridge entry in mimocode.json, got:\n%s", string(cfgBytes))
	}

	// Uninstall removes Sila entry and preserves foreign-mimo-plugin + theme
	unRes, err := Uninstall("mimocode", dir, false)
	if err != nil {
		t.Fatalf("mimocode uninstall failed: %v", err)
	}
	if !unRes.Installed {
		t.Fatalf("expected mimocode uninstall to report removal, got %+v", unRes)
	}
	cfgAfter, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cfgAfter), BridgeFilename) {
		t.Fatalf("expected bridge removed from mimocode.json, got:\n%s", string(cfgAfter))
	}
	if !strings.Contains(string(cfgAfter), "foreign-mimo-plugin") || !strings.Contains(string(cfgAfter), `"theme": "light"`) {
		t.Fatalf("expected foreign plugin and settings preserved in mimocode.json, got:\n%s", string(cfgAfter))
	}
}

func TestUserScopeOpenCodeBridgeRoundTrip(t *testing.T) {
	home := t.TempDir()
	foreignPath := filepath.Join(home, ".config", "opencode", "plugins", "foreign-plugin.js")
	if err := os.MkdirAll(filepath.Dir(foreignPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(foreignPath, []byte("// foreign plugin\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if ok, _ := IsInstalledUser("opencode", home); ok {
		t.Fatal("user bridge must not report installed before install")
	}
	res, err := InstallUser("opencode", home, "v1.0.0", false)
	if err != nil {
		t.Fatalf("user install failed: %v", err)
	}
	if !res.Installed || res.Refused {
		t.Fatalf("unexpected user install result: %+v", res)
	}
	bridgePath := filepath.Join(home, ".config", "opencode", "plugins", BridgeFilename)
	info, err := os.Stat(bridgePath)
	if err != nil {
		t.Fatalf("stat user bridge: %v", err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("expected mode 0644, got %o", info.Mode().Perm())
	}
	ok, path := IsInstalledUser("opencode", home)
	if !ok || path != bridgePath {
		t.Fatalf("IsInstalledUser = %v, %q; want true, %q", ok, path, bridgePath)
	}

	second, err := InstallUser("opencode", home, "v1.0.0", false)
	if err != nil || !second.Skipped {
		t.Fatalf("second user install must be idempotent: %+v %v", second, err)
	}

	unRes, err := UninstallUser("opencode", home, false)
	if err != nil || !unRes.Installed {
		t.Fatalf("user uninstall failed: %+v %v", unRes, err)
	}
	if _, err := os.Stat(bridgePath); !os.IsNotExist(err) {
		t.Fatalf("expected user bridge removed, stat err: %v", err)
	}
	if _, err := os.Stat(foreignPath); err != nil {
		t.Fatalf("expected foreign plugin preserved: %v", err)
	}
	if ok, _ := IsInstalledUser("opencode", home); ok {
		t.Fatal("user bridge must report uninstalled after removal")
	}
}

func TestUserScopeMiMoCodeBridgeRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SILA_MIMOCODE_PLUGIN_VERIFIED", "")
	cfgPath := filepath.Join(home, ".mimocode", "mimocode.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(`{"plugin":["foreign-mimo-plugin"],"theme":"light"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := InstallUser("mimocode", home, "v1.0.0", false)
	if err != nil || !res.Installed || res.Refused {
		t.Fatalf("mimocode user install failed: %+v %v", res, err)
	}
	bridgePath := filepath.Join(home, ".mimocode", "plugins", BridgeFilename)
	if _, err := os.Stat(bridgePath); err != nil {
		t.Fatalf("mimocode user bridge missing: %v", err)
	}
	cfg, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(cfg), BridgeFilename) != 1 || !strings.Contains(string(cfg), "./plugins/"+BridgeFilename) {
		t.Fatalf("mimocode user config must reference the bridge once:\n%s", cfg)
	}
	if !strings.Contains(string(cfg), "foreign-mimo-plugin") || !strings.Contains(string(cfg), `"theme": "light"`) {
		t.Fatalf("mimocode user install dropped foreign config:\n%s", cfg)
	}

	second, err := InstallUser("mimocode", home, "v1.0.0", false)
	if err != nil || !second.Skipped {
		t.Fatalf("second mimocode user install must be idempotent: %+v %v", second, err)
	}
	ok, path := IsInstalledUser("mimocode", home)
	if !ok || path != bridgePath {
		t.Fatalf("IsInstalledUser mimocode = %v, %q; want true, %q", ok, path, bridgePath)
	}

	unRes, err := UninstallUser("mimocode", home, false)
	if err != nil || !unRes.Installed {
		t.Fatalf("mimocode user uninstall failed: %+v %v", unRes, err)
	}
	if _, err := os.Stat(bridgePath); !os.IsNotExist(err) {
		t.Fatalf("expected mimocode user bridge removed: %v", err)
	}
	cfg, err = os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cfg), BridgeFilename) {
		t.Fatalf("mimocode user config still references the bridge:\n%s", cfg)
	}
	if !strings.Contains(string(cfg), "foreign-mimo-plugin") || !strings.Contains(string(cfg), `"theme": "light"`) {
		t.Fatalf("mimocode user uninstall dropped foreign config:\n%s", cfg)
	}
}

func TestUserScopeRefusesMissingHome(t *testing.T) {
	if res, err := InstallUser("opencode", "", "v1", false); err != nil || !res.Refused {
		t.Fatalf("expected refusal for empty home: %+v %v", res, err)
	}
	if res, err := UninstallUser("opencode", "", false); err != nil || !res.Refused {
		t.Fatalf("expected refusal for empty home: %+v %v", res, err)
	}
	if ok, path := IsInstalledUser("opencode", ""); ok || path != "" {
		t.Fatalf("expected no user install for empty home, got %v %q", ok, path)
	}
}

func TestJSFixtureHarnessEndToEnd(t *testing.T) {
	nodeBin, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH; skipping JS fixture harness test")
	}

	cases := []struct {
		harness string
		runner  string
		want    string
	}{
		{harness: "opencode", runner: "runner-v2.mjs", want: "BRIDGE_V2_RUNNER_OK"},
		{harness: "mimocode", runner: "runner.mjs", want: "BRIDGE_RUNNER_OK"},
	}
	for _, tc := range cases {
		t.Run(tc.harness, func(t *testing.T) {
			tmpDir := t.TempDir()
			src, err := Render(tc.harness, "v1.0.0-test")
			if err != nil {
				t.Fatal(err)
			}
			bridgePath := filepath.Join(tmpDir, "sila-bridge.mjs")
			if err := os.WriteFile(bridgePath, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}

			fakeSilaSrc, err := os.ReadFile(filepath.Join("testdata", "fake-sila.mjs"))
			if err != nil {
				t.Fatal(err)
			}
			fakeSilaPath := filepath.Join(tmpDir, "fake-sila.mjs")
			if err := os.WriteFile(fakeSilaPath, fakeSilaSrc, 0o755); err != nil {
				t.Fatal(err)
			}

			logPath := filepath.Join(tmpDir, "sila-stdin.jsonl")
			runnerPath := filepath.Join("testdata", tc.runner)

			cmd := exec.Command(nodeBin, runnerPath, bridgePath, fakeSilaPath, logPath)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("JS fixture runner failed: %v\nOutput:\n%s", err, string(out))
			}
			if !strings.Contains(string(out), tc.want) {
				t.Fatalf("expected %s in output, got:\n%s", tc.want, string(out))
			}
		})
	}
}

func TestUserScopeOpenCodeFlatpakTargets(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	nativePath := filepath.Join(".config", "opencode", "plugins", BridgeFilename)
	flatpakPath := filepath.Join(".var", "app", "ai.opencode.opencode", "config", "opencode", "plugins", BridgeFilename)

	for _, tc := range []struct {
		name  string
		dirs  []string
		paths []string
	}{
		{"flatpak-only", []string{filepath.Dir(flatpakPath)}, []string{flatpakPath}},
		{"dual", []string{filepath.Dir(nativePath), filepath.Dir(flatpakPath)}, []string{nativePath, flatpakPath}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			for _, dir := range tc.dirs {
				if err := os.MkdirAll(filepath.Join(home, dir), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			res, err := InstallUser("opencode", home, "v1.0.0", false)
			if err != nil || !res.Installed {
				t.Fatalf("install = %+v, %v", res, err)
			}
			for _, path := range tc.paths {
				if _, err := os.Stat(filepath.Join(home, path)); err != nil {
					t.Fatalf("expected bridge at %s: %v", path, err)
				}
			}
			ok, installedPath := IsInstalledUser("opencode", home)
			if !ok || installedPath != filepath.Join(home, nativePath) {
				t.Fatalf("IsInstalledUser = %v, %q", ok, installedPath)
			}
			if _, err := UninstallUser("opencode", home, false); err != nil {
				t.Fatal(err)
			}
			for _, path := range tc.paths {
				if _, err := os.Stat(filepath.Join(home, path)); !os.IsNotExist(err) {
					t.Fatalf("expected %s removed, stat err %v", path, err)
				}
			}
		})
	}
}

func TestResolveSilaBinFallback(t *testing.T) {
	nodeBin, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH; skipping JS fallback test")
	}
	src, err := Render("opencode", "v1.0.0-test")
	if err != nil {
		t.Fatal(err)
	}
	tmpDir := t.TempDir()
	bridgePath := filepath.Join(tmpDir, "sila-bridge.mjs")
	if err := os.WriteFile(bridgePath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	fallback := filepath.Join(home, ".local", "bin", "sila")
	if err := os.MkdirAll(filepath.Dir(fallback), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fallback, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	script := `import { resolveSilaBin } from "file://` + filepath.ToSlash(bridgePath) + `"; console.log(resolveSilaBin());`
	cmd := exec.Command(nodeBin, "--input-type=module", "-e", script)
	cmd.Env = []string{"PATH=/usr/bin", "HOME=" + home, "SILA_HOOK_BIN="}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node failed: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != fallback {
		t.Fatalf("resolveSilaBin = %q, want %q", got, fallback)
	}
}
