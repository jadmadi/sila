package store

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSyncLeaseHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_SYNC_LEASE_HELPER") != "1" {
		return
	}
	args := os.Args
	for len(args) > 0 {
		if args[0] == "--" {
			args = args[1:]
			break
		}
		args = args[1:]
	}
	if len(args) < 2 {
		os.Exit(3)
	}
	cmd := args[0]
	dbPath := args[1]

	switch cmd {
	case "hold":
		release, err := AcquireSyncLease(dbPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "hold failed: %v\n", err)
			os.Exit(1)
		}
		// Signal to parent that lock is held
		fmt.Println("LOCKED")
		_ = os.Stdout.Sync()
		// Wait for stdin EOF or close
		buf := make([]byte, 1)
		_, _ = os.Stdin.Read(buf)
		_ = release()
		fmt.Println("RELEASED")
		os.Exit(0)
	case "contend":
		release, err := AcquireSyncLease(dbPath)
		if err != nil {
			if errors.Is(err, ErrSyncBusy) {
				fmt.Println("BUSY")
				os.Exit(2)
			}
			fmt.Fprintf(os.Stderr, "contend error: %v\n", err)
			os.Exit(1)
		}
		_ = release()
		fmt.Println("ACQUIRED")
		os.Exit(0)
	default:
		os.Exit(4)
	}
}

func TestSyncLeaseMutualExclusion(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "main.db")

	// 1. Start holder subprocess
	cmdHolder := exec.Command(os.Args[0], "-test.run=^TestSyncLeaseHelperProcess$", "--", "hold", dbPath)
	cmdHolder.Env = append(os.Environ(), "GO_WANT_SYNC_LEASE_HELPER=1")
	stdinHolder, err := cmdHolder.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdoutHolder, err := cmdHolder.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}

	if err := cmdHolder.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmdHolder.Process != nil {
			_ = cmdHolder.Process.Kill()
		}
	})

	scanner := bufio.NewScanner(stdoutHolder)
	if !scanner.Scan() || scanner.Text() != "LOCKED" {
		t.Fatalf("expected LOCKED from holder, got %v", scanner.Text())
	}

	// 2. Contender on same DB must return busy within 1s (AC-001)
	start := time.Now()
	cmdContend := exec.Command(os.Args[0], "-test.run=^TestSyncLeaseHelperProcess$", "--", "contend", dbPath)
	cmdContend.Env = append(os.Environ(), "GO_WANT_SYNC_LEASE_HELPER=1")
	out, err := cmdContend.CombinedOutput()
	elapsed := time.Since(start)

	if elapsed > 1*time.Second {
		t.Errorf("contender took too long: %v (must return within 1s)", elapsed)
	}
	if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 2 {
		t.Errorf("contender expected exit code 2 (BUSY), got err: %v (output: %s)", err, string(out))
	}
	if !strings.Contains(string(out), "BUSY") {
		t.Errorf("expected BUSY output, got: %s", string(out))
	}

	// 3. Distinct store must be independent (AC-002)
	dbOther := filepath.Join(tmp, "other.db")
	cmdOther := exec.Command(os.Args[0], "-test.run=^TestSyncLeaseHelperProcess$", "--", "contend", dbOther)
	cmdOther.Env = append(os.Environ(), "GO_WANT_SYNC_LEASE_HELPER=1")
	outOther, errOther := cmdOther.CombinedOutput()
	if errOther != nil {
		t.Errorf("distinct store should not contend: %v (output: %s)", errOther, string(outOther))
	}

	// 4. Same-store symlink alias must collide and return busy (AC-002)
	symlinkDB := filepath.Join(tmp, "symlink.db")
	if err := os.Symlink(dbPath, symlinkDB); err != nil {
		t.Fatal(err)
	}
	cmdSymlink := exec.Command(os.Args[0], "-test.run=^TestSyncLeaseHelperProcess$", "--", "contend", symlinkDB)
	cmdSymlink.Env = append(os.Environ(), "GO_WANT_SYNC_LEASE_HELPER=1")
	outSymlink, errSymlink := cmdSymlink.CombinedOutput()
	if exitErr, ok := errSymlink.(*exec.ExitError); !ok || exitErr.ExitCode() != 2 {
		t.Errorf("same-store symlink alias should collide (exit 2), got: %v (output: %s)", errSymlink, string(outSymlink))
	}

	// 5. Close holder process and verify reacquisition (AC-003)
	_ = stdinHolder.Close()
	_ = cmdHolder.Wait()

	cmdAfter := exec.Command(os.Args[0], "-test.run=^TestSyncLeaseHelperProcess$", "--", "contend", dbPath)
	cmdAfter.Env = append(os.Environ(), "GO_WANT_SYNC_LEASE_HELPER=1")
	outAfter, errAfter := cmdAfter.CombinedOutput()
	if errAfter != nil {
		t.Errorf("reacquisition after holder exit failed: %v (output: %s)", errAfter, string(outAfter))
	}

	// Lock file remains on disk (AC-003)
	lockPath, err := SyncLeasePath(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lockPath); err != nil {
		t.Errorf("lock file path %s missing after release: %v", lockPath, err)
	}
}

func TestSyncLeaseIdentityAndRelease(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test_identity.db")

	// 1. Normal acquire and mutual exclusion within process
	release1, err := AcquireSyncLease(dbPath)
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}

	_, err2 := AcquireSyncLease(dbPath)
	if err2 == nil {
		t.Errorf("expected second acquire to fail with ErrSyncBusy while held")
	} else if !errors.Is(err2, ErrSyncBusy) {
		t.Errorf("expected ErrSyncBusy, got: %v", err2)
	}

	// 2. Normal release (AC-003)
	if err := release1(); err != nil {
		t.Errorf("first release failed: %v", err)
	}

	// 3. Repeated release must be safe and idempotent (AC-003)
	if err := release1(); err != nil {
		t.Errorf("repeated release returned error: %v", err)
	}

	// 4. Reacquisition succeeds (AC-003)
	release2, err := AcquireSyncLease(dbPath)
	if err != nil {
		t.Fatalf("reacquisition failed: %v", err)
	}
	_ = release2()

	// 5. Permission errors are not relabeled busy (AC-002)
	if os.Geteuid() != 0 {
		readOnlyDir := filepath.Join(tmp, "readonly_dir")
		if err := os.MkdirAll(readOnlyDir, 0555); err == nil {
			t.Cleanup(func() { _ = os.Chmod(readOnlyDir, 0755) })
			unwritableDB := filepath.Join(readOnlyDir, "store.db")
			_, errPerm := AcquireSyncLease(unwritableDB)
			if errPerm == nil {
				t.Errorf("expected error on unwritable directory")
			} else if errors.Is(errPerm, ErrSyncBusy) {
				t.Errorf("permission error was incorrectly relabeled as ErrSyncBusy: %v", errPerm)
			}
		}
	}
}
