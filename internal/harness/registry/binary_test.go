package registry

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeFakeSila(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "sila")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho fake-sila \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestResolveHookBinaryLegacyForTestBinary(t *testing.T) {
	home := t.TempDir()
	res, err := resolveHookBinary(home, "", "registry.test", "/usr/bin:/bin", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Resolved || res.Path != "sila" || res.Source != "legacy" {
		t.Fatalf("test-binary resolution = %+v", res)
	}
}

func TestResolveHookBinaryExplicit(t *testing.T) {
	home := t.TempDir()
	fake := writeFakeSila(t, t.TempDir())
	res, err := resolveHookBinary(home, fake, "sila", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Resolved || res.Path != fake || res.Source != "explicit" {
		t.Fatalf("explicit resolution = %+v", res)
	}
}

func TestResolveHookBinaryWrapper(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX wrapper test")
	}
	t.Chdir(t.TempDir())
	home := t.TempDir()
	res, err := resolveHookBinary(home, "", "sila", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Resolved || res.Source != "wrapper" || res.WrapperPath == "" {
		t.Fatalf("wrapper resolution = %+v", res)
	}
	info, err := os.Stat(res.WrapperPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("wrapper mode = %o", info.Mode().Perm())
	}
}

func TestHookWrapperRunsWithPathStripped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX wrapper test")
	}
	home := t.TempDir()
	wrapper, err := EnsureHookWrapper(home)
	if err != nil {
		t.Fatal(err)
	}
	fake := writeFakeSila(t, home)
	cmd := exec.Command(wrapper, "hook", "session-start")
	cmd.Env = []string{"HOME=" + home, "SILA_BIN=" + fake, "PATH=/usr/bin:/bin"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("wrapper failed: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "fake-sila hook session-start" {
		t.Fatalf("wrapper output = %q", got)
	}
}

func TestHookWrapperMissingBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX wrapper test")
	}
	t.Chdir(t.TempDir())
	home := t.TempDir()
	wrapper, err := EnsureHookWrapper(home)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(wrapper, "hook", "session-start")
	cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 127 {
		t.Fatalf("exit = %v, want 127\nstderr:\n%s", err, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("missing-binary wrapper wrote stdout: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "SILA_BIN") {
		t.Fatalf("missing diagnostic: %q", stderr.String())
	}
}

func TestCommandForInstallQuotesSpaces(t *testing.T) {
	old := BinaryResolver
	BinaryResolver = func() (BinaryResolution, error) {
		return BinaryResolution{Path: "/tmp/sila path/sila", Source: "explicit", Resolved: true}, nil
	}
	defer func() { BinaryResolver = old }()
	got := commandForInstall("sila hook session-start --json")
	if got != "'/tmp/sila path/sila' hook session-start --json" {
		t.Fatalf("quoted command = %q", got)
	}
}

func TestAbsoluteHookCommandIsSilaOwned(t *testing.T) {
	legacy := "sila hook session-start --json"
	absolute := "/opt/sila/bin/sila hook session-start --json"
	quoted := "'/path with space/sila' hook session-start --json"
	for _, cmd := range []string{legacy, absolute, quoted, "sila-hook hook session-start --json"} {
		if !isSilaHookCommand(cmd) {
			t.Fatalf("not recognized as Sila: %q", cmd)
		}
	}
	if isSilaHookCommand("/opt/other/hook hook session-start --json") {
		t.Fatal("foreign hook recognized as Sila")
	}
	if !sameHookCommand(legacy, absolute) || !sameHookCommand(legacy, quoted) {
		t.Fatal("legacy and resolved commands do not match")
	}
	if !commandContains(map[string]any{"hooks": []any{map[string]any{"command": absolute}}}, "sila hook ") {
		t.Fatal("nested resolved command not detected")
	}
}

func TestInstallUserUsesResolvedBinaryAndStatusReportsIt(t *testing.T) {
	old := BinaryResolver
	BinaryResolver = func() (BinaryResolution, error) {
		return BinaryResolution{Path: "/opt/sila/bin/sila", Source: "explicit", Resolved: true}, nil
	}
	defer func() { BinaryResolver = old }()

	home := t.TempDir()
	res, err := InstallUser("claude", home, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Refused {
		t.Fatalf("install refused: %+v", res)
	}
	data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "/opt/sila/bin/sila hook session-start --json") {
		t.Fatalf("resolved command missing:\n%s", data)
	}
	st := StatusForUser("claude", home)
	if !st.BinaryResolved || st.BinaryPath != "/opt/sila/bin/sila" || st.BinarySource != "explicit" {
		t.Fatalf("status binary fields = %+v", st)
	}
}
