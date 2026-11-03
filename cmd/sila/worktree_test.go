package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
)

func setupWorktreeRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmds := [][]string{
		{"init"},
		{"config", "user.email", "test@test.com"},
		{"config", "user.name", "Test"},
	}
	for _, args := range cmds {
		c := exec.Command("git", args...)
		c.Dir = dir
		if err := c.Run(); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add", "."},
		{"commit", "-m", "initial"},
	} {
		c := exec.Command("git", args...)
		c.Dir = dir
		if err := c.Run(); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	return dir
}

func captureWorktreeOutput(fn func()) string {
	f, err := os.CreateTemp("", "worktree-out-*")
	if err != nil {
		return ""
	}
	defer os.Remove(f.Name())

	origStdout := os.Stdout
	origStderr := os.Stderr
	os.Stdout = f
	os.Stderr = f

	fn()

	os.Stdout = origStdout
	os.Stderr = origStderr
	_ = f.Close()

	data, _ := os.ReadFile(f.Name())
	return string(data)
}

func TestRunWorktreeCreate(t *testing.T) {
	dir := setupWorktreeRepo(t)
	origWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	ui := render.New(os.Stdout, true)
	out := captureWorktreeOutput(func() {
		runWorktree(ui, "", "", false, false, []string{"create", "test-wt"})
	})
	if !strings.Contains(out, "Created worktree") {
		t.Errorf("expected create confirmation, got %q", out)
	}
	// Verify worktree exists.
	wtPath := filepath.Join(dir, ".git", "sila-worktrees", "test-wt")
	if _, err := os.Stat(wtPath); err != nil {
		t.Errorf("worktree dir should exist: %v", err)
	}
	// Verify SILA_CONTEXT.md was seeded.
	ctxPath := filepath.Join(wtPath, "SILA_CONTEXT.md")
	if _, err := os.Stat(ctxPath); err != nil {
		t.Errorf("SILA_CONTEXT.md should exist: %v", err)
	}
}

func TestRunWorktreeList(t *testing.T) {
	dir := setupWorktreeRepo(t)
	origWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	// First create a worktree.
	ui := render.New(os.Stdout, true)
	runWorktree(ui, "", "", false, false, []string{"create", "list-test"})

	// Now list.
	out := captureWorktreeOutput(func() {
		runWorktree(ui, "", "", false, false, []string{"list"})
	})
	if !strings.Contains(out, "worktree list") {
		t.Errorf("expected list header, got %q", out)
	}
	if !strings.Contains(out, "list-test") {
		t.Errorf("expected to find list-test worktree, got %q", out)
	}
}

func TestRunWorktreeClean(t *testing.T) {
	dir := setupWorktreeRepo(t)
	origWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	ui := render.New(os.Stdout, true)
	// Create then clean.
	runWorktree(ui, "", "", false, false, []string{"create", "clean-test"})

	out := captureWorktreeOutput(func() {
		runWorktree(ui, "", "", false, false, []string{"clean", "clean-test"})
	})
	if !strings.Contains(out, "Removed worktree") {
		t.Errorf("expected clean confirmation, got %q", out)
	}
	// Verify worktree is gone.
	wtPath := filepath.Join(dir, ".git", "sila-worktrees", "clean-test")
	if _, err := os.Stat(wtPath); err == nil {
		t.Error("worktree dir should not exist after clean")
	}
}

func TestRunWorktreeListEmpty(t *testing.T) {
	dir := setupWorktreeRepo(t)
	origWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	ui := render.New(os.Stdout, true)
	out := captureWorktreeOutput(func() {
		runWorktree(ui, "", "", false, false, []string{"list"})
	})
	// Should show primary tree at minimum.
	if !strings.Contains(out, "primary") {
		t.Errorf("expected primary tree in list, got %q", out)
	}
}

func TestRunWorktreeNoArgs(t *testing.T) {
	dir := setupWorktreeRepo(t)
	origWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	// runWorktree with no args calls os.Exit(2). We test this by running
	// the sila binary as a subprocess.
	if os.Getenv("TEST_WORKTREE_NOARGS") == "1" {
		ui := render.New(os.Stdout, true)
		runWorktree(ui, "", "", false, false, []string{})
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestRunWorktreeNoArgs")
	cmd.Env = append(os.Environ(), "TEST_WORKTREE_NOARGS=1")
	cmd.Dir = dir
	err := cmd.Run()
	if err == nil {
		t.Error("expected non-zero exit code for no args")
	}
}

func TestRunWorktreePilot(t *testing.T) {
	dir := setupWorktreeRepo(t)
	origWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	ui := render.New(os.Stdout, true)
	out := captureWorktreeOutput(func() {
		runWorktree(ui, "", "", false, false, []string{"pilot", "echo", "pilot-ok"})
	})

	if !strings.Contains(out, "PASSED") && !strings.Contains(out, "pilot-ok") {
		t.Errorf("expected pilot output with PASSED and pilot-ok, got %q", out)
	}
}
