package store

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
)

func TestLiveLockLifecycle(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "k.db")

	// Initially no lock
	info, active, err := CheckLiveLockScoped(dbPath, "test-project")
	if err != nil {
		t.Fatal(err)
	}
	if active || info != nil {
		t.Fatalf("expected inactive lock, got active=%v info=%+v", active, info)
	}

	// Acquire lock
	release1, err := AcquireLiveLockScoped(dbPath, "test-project")
	if err != nil {
		t.Fatalf("failed to acquire live lock: %v", err)
	}

	// Verify active
	info, active, err = CheckLiveLockScoped(dbPath, "test-project")
	if err != nil {
		t.Fatal(err)
	}
	if !active || info == nil || info.PID != os.Getpid() {
		t.Fatalf("expected active lock with pid %d, got %+v", os.Getpid(), info)
	}

	// Attempting second acquire for same project should fail
	_, err = AcquireLiveLockScoped(dbPath, "test-project")
	if err == nil {
		t.Fatal("expected error on duplicate acquire for same project, got nil")
	}

	// Acquiring lock for a DIFFERENT project succeeds (isolated locks)
	release2, err := AcquireLiveLockScoped(dbPath, "another-project")
	if err != nil {
		t.Fatalf("expected isolated project lock to succeed, got %v", err)
	}

	// Release both locks
	release1()
	release2()

	// Verify inactive
	info, active, err = CheckLiveLockScoped(dbPath, "test-project")
	if err != nil {
		t.Fatal(err)
	}
	if active || info != nil {
		t.Fatalf("expected inactive lock after release, got active=%v info=%+v", active, info)
	}
}

func TestGlobalLiveLockInteraction(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "k.db")

	// 1. Acquire global lock
	releaseGlobal, err := AcquireLiveLockScoped(dbPath, "")
	if err != nil {
		t.Fatalf("acquire global lock: %v", err)
	}

	// Project lock should be blocked by active global lock
	_, err = AcquireLiveLockScoped(dbPath, "proj-a")
	if err == nil {
		t.Fatal("expected project acquire to fail when global lock is active")
	}

	releaseGlobal()

	// 2. Acquire project lock
	releaseProj, err := AcquireLiveLockScoped(dbPath, "proj-a")
	if err != nil {
		t.Fatalf("acquire project lock: %v", err)
	}

	// Global lock acquire should be blocked while project lock is active
	_, err = AcquireLiveLockScoped(dbPath, "")
	if err == nil {
		t.Fatal("expected global acquire to fail when project lock is active")
	}

	releaseProj()

	// Now global lock can be acquired
	rel, err := AcquireLiveLockScoped(dbPath, "")
	if err != nil {
		t.Fatalf("acquire global lock after project release: %v", err)
	}
	rel()
}

func TestAcquireLiveLockIsAtomic(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "k.db")
	start := make(chan struct{})
	result := make(chan func(), 12)
	for range 12 {
		go func() {
			<-start
			release, err := AcquireLiveLock(dbPath, "p")
			if err == nil {
				result <- release
			} else {
				result <- nil
			}
		}()
	}
	close(start)
	var successes int
	var releases []func()
	for range 12 {
		if release := <-result; release != nil {
			successes++
			releases = append(releases, release)
		}
	}
	for _, release := range releases {
		release()
	}
	if successes != 1 {
		t.Fatalf("atomic acquisition allowed %d winners, want 1", successes)
	}
}

func TestStaleLiveLockCleanup(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "k.db")
	lockFile := LiveLockPath(dbPath)

	// Write lock with a dead PID (e.g. 999999)
	staleInfo := LiveLockInfo{
		PID:       99999999,
		Project:   "dead-proj",
		StartedAt: time.Now().Add(-1 * time.Hour),
		Hostname:  "test-host",
	}
	data, _ := json.Marshal(staleInfo)
	_ = os.MkdirAll(filepath.Dir(lockFile), 0700)
	_ = os.WriteFile(lockFile, data, 0600)

	// CheckLiveLock should detect stale lock, clean it up, and return active=false
	info, active, err := CheckLiveLock(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if active || info != nil {
		t.Fatalf("expected inactive after stale cleanup, got active=%v info=%+v", active, info)
	}

	// Should be able to acquire immediately
	release, err := AcquireLiveLock(dbPath, "new-proj")
	if err != nil {
		t.Fatalf("failed to acquire lock after stale cleanup: %v", err)
	}
	defer release()
}

// TestStoppedLiveLockCleanup verifies that a lock held by a stopped process
// (e.g. Ctrl+Z'd) is treated as not active and cleaned up, so a new sila
// watch instance can take over. Linux-only: uses /proc to detect stop state.
func TestStoppedLiveLockCleanup(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("stopped-process detection is Linux-only (/proc)")
	}

	// Spawn a child process we can stop with SIGSTOP.
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start child process: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		cmd.Wait()
	}()

	pid := cmd.Process.Pid
	if err := cmd.Process.Signal(syscall.SIGSTOP); err != nil {
		t.Fatalf("failed to stop child process: %v", err)
	}

	// Give the kernel a moment to update /proc/<pid>/stat.
	time.Sleep(100 * time.Millisecond)

	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "k.db")
	lockFile := LiveLockPath(dbPath)

	stoppedInfo := LiveLockInfo{
		PID:       pid,
		Project:   "stopped-proj",
		StartedAt: time.Now().Add(-1 * time.Minute),
		Hostname:  "test-host",
	}
	data, _ := json.Marshal(stoppedInfo)
	_ = os.MkdirAll(filepath.Dir(lockFile), 0700)
	_ = os.WriteFile(lockFile, data, 0600)

	// CheckLiveLock should detect the stopped process, clean up, and return
	// active=false — even though the process is technically alive.
	info, active, err := CheckLiveLock(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if active || info != nil {
		t.Fatalf("expected inactive after stopped-process cleanup, got active=%v info=%+v", active, info)
	}

	// Lock file should have been removed.
	if _, err := os.Stat(lockFile); !os.IsNotExist(err) {
		t.Fatalf("expected lock file removed, but it still exists")
	}

	// Should be able to acquire immediately.
	release, err := AcquireLiveLock(dbPath, "new-proj")
	if err != nil {
		t.Fatalf("failed to acquire lock after stopped cleanup: %v", err)
	}
	defer release()
}
