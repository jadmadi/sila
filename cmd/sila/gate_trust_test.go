package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
)

func trustRepo(t *testing.T, authorEmail, file string) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	dir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_EMAIL="+authorEmail, "GIT_AUTHOR_NAME=Other",
			"GIT_COMMITTER_EMAIL="+authorEmail, "GIT_COMMITTER_NAME=Other")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	run("init", "-q")
	run("config", "user.email", "self@example.com")
	run("config", "user.name", "Self")
	path := filepath.Join(dir, file)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("-c", "user.email="+authorEmail, "-c", "user.name=Other", "commit", "-q", "-m", "change "+file)
	sha := strings.TrimSpace(run("rev-parse", "HEAD"))
	return dir, sha
}

func TestTrustCheckRefusesUntrustedSensitiveCommit(t *testing.T) {
	dir, sha := trustRepo(t, "other@example.com", "AGENTS.md")
	t.Chdir(dir)
	gateTrust = gateTrustOptions{Range: "HEAD"}
	t.Cleanup(func() { gateTrust = gateTrustOptions{} })
	check, refusal := trustCheck(dir)
	if check.Status != "fail" || !strings.Contains(refusal, "UNTRUSTED_COMMIT") {
		t.Fatalf("check=%+v refusal=%q", check, refusal)
	}
	if !strings.Contains(refusal, sha) || !strings.Contains(refusal, "--trust-target") {
		t.Fatalf("refusal lacks the accept command: %q", refusal)
	}
	gateTrust = gateTrustOptions{Range: "HEAD", Target: sha}
	check, refusal = trustCheck(dir)
	if check.Status != "pass" {
		t.Fatalf("trusted check=%+v refusal=%q", check, refusal)
	}
}

func TestTrustCheckAllowsSelfAndNonSensitive(t *testing.T) {
	dir, _ := trustRepo(t, "self@example.com", "AGENTS.md")
	t.Chdir(dir)
	gateTrust = gateTrustOptions{Range: "HEAD"}
	t.Cleanup(func() { gateTrust = gateTrustOptions{} })
	if check, refusal := trustCheck(dir); check.Status != "pass" {
		t.Fatalf("self check=%+v refusal=%q", check, refusal)
	}

	dir, _ = trustRepo(t, "other@example.com", "README.md")
	t.Chdir(dir)
	if check, refusal := trustCheck(dir); check.Status != "pass" {
		t.Fatalf("non-sensitive check=%+v refusal=%q", check, refusal)
	}
}

func TestTrustTargetValidation(t *testing.T) {
	if isFullCommitSHA("deadbeef") || isFullCommitSHA(strings.Repeat("g", 40)) {
		t.Fatal("short or non-hex target accepted")
	}
	if !isFullCommitSHA(strings.Repeat("a", 40)) {
		t.Fatal("full SHA rejected")
	}
}

func TestShowTrustConfig(t *testing.T) {
	dir, _ := trustRepo(t, "self@example.com", "README.md")
	t.Chdir(dir)
	gateTrust = gateTrustOptions{Range: "HEAD", Target: strings.Repeat("a", 40)}
	t.Cleanup(func() { gateTrust = gateTrustOptions{} })
	out := captureStdout(t, func() { showGateTrustConfig(render.New(os.Stdout, true)) })
	for _, want := range []string{"identity: self@example.com", "targets:", "sensitive:", "AGENTS.md", ".github/workflows/**"} {
		if !strings.Contains(out, want) {
			t.Fatalf("show-config missing %q: %s", want, out)
		}
	}
}
