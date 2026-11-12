package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func buildCLIForCanary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "sila-canary")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build cli: %v\n%s", err, out)
	}
	return bin
}

func TestTrustCanaryNeverRuns(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the CLI and a canary script")
	}
	repo, _ := trustRepo(t, "other@example.com", "AGENTS.md")
	bin := buildCLIForCanary(t)

	canaryDir := t.TempDir()
	marker := filepath.Join(canaryDir, "canary-ran")
	fakeGo := filepath.Join(canaryDir, "go")
	script := "#!/bin/sh\ntouch " + marker + "\nexit 0\n"
	if err := os.WriteFile(fakeGo, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	run := func(args ...string) (string, int) {
		cmd := exec.Command(bin, args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "PATH="+canaryDir+":"+os.Getenv("PATH"))
		out, err := cmd.CombinedOutput()
		code := 0
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else if err != nil {
			t.Fatalf("run %v: %v", args, err)
		}
		return string(out), code
	}

	out, code := run("gate", "--pre-commit")
	if code != 2 || !strings.Contains(out, "UNTRUSTED_COMMIT") {
		t.Fatalf("refusal code=%d out=%s", code, out)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("canary script ran before the trust refusal")
	}

	out, code = run("gate", "--show-config")
	if code != 0 || !strings.Contains(out, "sensitive:") {
		t.Fatalf("show-config code=%d out=%s", code, out)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("show-config executed a repository script")
	}
}
