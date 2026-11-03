package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initTestRepo(t *testing.T, dir string) {
	t.Helper()
	if err := exec.Command("git", "-C", dir, "init", "-b", "main").Run(); err != nil {
		t.Fatal(err)
	}
}

func TestParentRepositoryAndIgnoreDetection(t *testing.T) {
	parent := t.TempDir()
	initTestRepo(t, parent)
	if err := os.WriteFile(filepath.Join(parent, ".gitignore"), []byte("sub/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(parent, "sub")
	other := filepath.Join(parent, "other")
	for _, dir := range []string{sub, other} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if got := parentRepositoryRoot(sub); got != parent {
		t.Fatalf("parent root = %q, want %q", got, parent)
	}
	if !gitIgnoredBy(parent, sub) {
		t.Fatal("sub should be ignored by the parent")
	}
	if gitIgnoredBy(parent, other) {
		t.Fatal("other should not be ignored by the parent")
	}
}

func TestInitGitRepoHereCreatesMainAndIsIdempotent(t *testing.T) {
	parent := t.TempDir()
	initTestRepo(t, parent)
	if err := os.WriteFile(filepath.Join(parent, ".gitignore"), []byte("sub/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(parent, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := initGitRepoHere(sub); err != nil {
		t.Fatal(err)
	}
	if !repoAlreadyInitialized(sub) {
		t.Fatal(".git was not created")
	}
	out, err := exec.Command("git", "-C", sub, "symbolic-ref", "--short", "HEAD").Output()
	if err != nil || strings.TrimSpace(string(out)) != "main" {
		t.Fatalf("branch = %q, err=%v", strings.TrimSpace(string(out)), err)
	}
	if err := initGitRepoHere(sub); err != nil {
		t.Fatalf("second init should be a no-op: %v", err)
	}
}
