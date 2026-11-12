package git

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func newHookTestManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Skipf("git init unavailable: %v %s", err, out)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".git", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	mgr, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	return mgr
}

func TestInstallPostCommitHook(t *testing.T) {
	mgr := newHookTestManager(t)
	path, err := mgr.InstallPostCommitHook()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v", info.Mode().Perm())
	}
	status := mgr.ManagedHookStatus("post-commit")
	if !status.Installed || !status.Current || !status.Owned {
		t.Fatalf("status = %+v", status)
	}
	first, _ := os.ReadFile(path)
	if _, err := mgr.InstallPostCommitHook(); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(path)
	if string(first) != string(second) {
		t.Fatal("second install changed the hook")
	}
}

func TestPostCommitPreservesForeignHook(t *testing.T) {
	mgr := newHookTestManager(t)
	path := filepath.Join(mgr.RepoRoot(), ".git", "hooks", "post-commit")
	foreign := "#!/bin/sh\necho foreign\n"
	if err := os.WriteFile(path, []byte(foreign), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.InstallPostCommitHook(); !errors.Is(err, ErrForeignHook) {
		t.Fatalf("err = %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != foreign {
		t.Fatal("foreign hook changed")
	}
	status := mgr.ManagedHookStatus("post-commit")
	if !status.Installed || status.Owned {
		t.Fatalf("status = %+v", status)
	}
}

func TestHookStatusMissing(t *testing.T) {
	mgr := newHookTestManager(t)
	status := mgr.ManagedHookStatus("post-commit")
	if status.Installed || status.Current || status.Owned {
		t.Fatalf("status = %+v", status)
	}
}
