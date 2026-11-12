package git

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallPrePushHook(t *testing.T) {
	dir := setupRepo(t)
	mgr, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	hookPath, err := mgr.InstallPrePushHook()
	if err != nil {
		t.Fatalf("InstallPrePushHook: %v", err)
	}

	data, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "sila gate") {
		t.Fatalf("expected hook to contain 'sila gate', got: %s", string(data))
	}
}

// A project pre-push hook that sila did not write must survive an install.
func TestInstallPrePushHookLeavesForeignHookAlone(t *testing.T) {
	dir := setupRepo(t)
	hooksDir := filepath.Join(dir, ".git", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	hookPath := filepath.Join(hooksDir, "pre-push")
	if err := os.WriteFile(hookPath, []byte("#!/bin/sh\necho project-push\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	mgr, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.InstallPrePushHook(); !errors.Is(err, ErrForeignHook) {
		t.Fatalf("InstallPrePushHook = %v, want ErrForeignHook", err)
	}
	data, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "project-push") {
		t.Fatalf("foreign pre-push hook was overwritten: %s", string(data))
	}
}

func TestInstallPreCommitHook(t *testing.T) {
	dir := setupRepo(t)
	mgr, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	hookPath, err := mgr.InstallPreCommitHook()
	if err != nil {
		t.Fatalf("InstallPreCommitHook: %v", err)
	}

	data, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "sila gate") {
		t.Fatalf("expected hook to contain 'sila gate', got: %s", string(data))
	}
	if !strings.Contains(string(data), "git rev-parse --show-toplevel") {
		t.Fatalf("expected hook to resolve repo root via 'git rev-parse --show-toplevel', got: %s", string(data))
	}
	if !strings.Contains(string(data), `cd "$REPO_ROOT"`) {
		t.Fatalf("expected hook to switch directory via 'cd \"$REPO_ROOT\"', got: %s", string(data))
	}
}

// A project hook that sila did not write must survive an install attempt.
func TestInstallPreCommitHookLeavesForeignHookAlone(t *testing.T) {
	dir := setupRepo(t)
	hooksDir := filepath.Join(dir, ".git", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	hookPath := filepath.Join(hooksDir, "pre-commit")
	if err := os.WriteFile(hookPath, []byte("#!/bin/sh\necho project-hook\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	mgr, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.InstallPreCommitHook(); !errors.Is(err, ErrForeignHook) {
		t.Fatalf("InstallPreCommitHook = %v, want ErrForeignHook", err)
	}
	data, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "project-hook") {
		t.Fatalf("foreign hook was overwritten: %s", string(data))
	}
}

func TestInstallGateRemote(t *testing.T) {
	dir := setupRepo(t)
	mgr, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	remote, err := mgr.InstallGateRemote("test-gate")
	if err != nil {
		t.Fatalf("InstallGateRemote: %v", err)
	}
	if remote != "test-gate" {
		t.Fatalf("expected remote name 'test-gate', got %s", remote)
	}

	// Idempotent call
	remote2, err := mgr.InstallGateRemote("test-gate")
	if err != nil {
		t.Fatalf("second InstallGateRemote: %v", err)
	}
	if remote2 != "test-gate" {
		t.Fatalf("expected remote name 'test-gate', got %s", remote2)
	}
}

func TestRunGatePipelineSandbox(t *testing.T) {
	if _, err := exec.LookPath("sila"); err == nil {
		t.Skip("a sila binary is on PATH; the empty-repo fail-closed case requires no installed binary")
	}
	dir := setupRepo(t)
	mgr, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// In this mock repo without sila command or code, gate will fail closed
	res, err := mgr.RunGatePipeline("HEAD", "origin", false)
	if err != nil {
		t.Fatalf("RunGatePipeline crashed: %v", err)
	}

	if res.Clean {
		t.Fatalf("expected gate checks to fail in an uninitialized empty repo")
	}

	// Verify worktree was cleaned up
	if _, err := os.Stat(filepath.Join(mgr.WorktreeDir(), res.Worktree)); err == nil {
		t.Fatalf("expected gate worktree to be cleaned up")
	}
}
