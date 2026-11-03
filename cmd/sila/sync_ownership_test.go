package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestSyncOwnershipBeforeStoreOpen(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	_ = st.Close()

	t.Run("HeldLeaseBlocksCallback", func(t *testing.T) {
		rel, err := store.AcquireSyncLease(dbPath)
		if err != nil {
			t.Fatalf("failed to acquire fixture lease: %v", err)
		}
		defer rel()

		called := false
		err = withSyncLease(dbPath, func() error {
			called = true
			return nil
		})
		if !errors.Is(err, store.ErrSyncBusy) {
			t.Fatalf("expected ErrSyncBusy, got: %v", err)
		}
		if called {
			t.Fatalf("callback must never be called when lease is busy")
		}
	})

	t.Run("FreeLeaseExecutesCallbackAndReleases", func(t *testing.T) {
		called := false
		err := withSyncLease(dbPath, func() error {
			called = true
			return nil
		})
		if err != nil {
			t.Fatalf("expected nil error, got: %v", err)
		}
		if !called {
			t.Fatalf("expected callback to be executed")
		}

		rel, err := store.AcquireSyncLease(dbPath)
		if err != nil {
			t.Fatalf("expected lease to be released, but reacquisition failed: %v", err)
		}
		_ = rel()
	})

	t.Run("CallbackErrorPropagatedAndLeaseReleased", func(t *testing.T) {
		customErr := errors.New("failed inside callback")
		err := withSyncLease(dbPath, func() error {
			return customErr
		})
		if !errors.Is(err, customErr) {
			t.Fatalf("expected customErr, got: %v", err)
		}

		rel, err := store.AcquireSyncLease(dbPath)
		if err != nil {
			t.Fatalf("expected lease to be released after callback error: %v", err)
		}
		_ = rel()
	})

	t.Run("DryRunDoesNotAcquireLease", func(t *testing.T) {
		dryTmp := t.TempDir()
		dryDB := filepath.Join(dryTmp, "dry.db")
		dst, err := store.Open(dryDB)
		if err != nil {
			t.Fatalf("failed to open dry store: %v", err)
		}
		_ = dst.Close()

		flockPath := dryDB + ".sync.flock"
		if _, err := os.Stat(flockPath); err == nil {
			t.Fatalf("flock file %s should not exist prior to dry-run", flockPath)
		}

		roSt, err := store.OpenRO(dryDB)
		if err != nil {
			t.Fatalf("failed to open ro store: %v", err)
		}
		defer roSt.Close()

		ui := render.New(os.Stdout, true)
		if err := runSingleSyncCore(ui, roSt, "", true, false, false, false, time.Time{}); err != nil {
			t.Fatalf("unexpected dry-run error: %v", err)
		}

		if _, err := os.Stat(flockPath); err == nil {
			t.Fatalf("dry-run must not create or touch flock file %s", flockPath)
		}
	})
}

func TestSyncBusyCLIJSON(t *testing.T) {
	if probe := os.Getenv("SILA_SYNC_CLI_PROBE"); probe != "" {
		os.Args = strings.Split(probe, "\x1f")
		main()
		return
	}

	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "cli_test.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	_, err = st.EnsureProject(tmp)
	if err != nil {
		_ = st.Close()
		t.Fatalf("failed to ensure project: %v", err)
	}
	_ = st.Close()

	dbInfoBefore, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}

	// 1. Hold the fixture sync lease
	releaseFixture, err := store.AcquireSyncLease(dbPath)
	if err != nil {
		t.Fatalf("failed to acquire fixture lease: %v", err)
	}

	// Helper to run CLI subprocess
	runCLI := func(args []string) (stdout, stderr string, exitCode int) {
		cmd := exec.Command(os.Args[0], "-test.run=^TestSyncBusyCLIJSON$")
		cmd.Env = append(os.Environ(), "SILA_SYNC_CLI_PROBE="+strings.Join(args, "\x1f"))
		var outBuf, errBuf bytes.Buffer
		cmd.Stdout = &outBuf
		cmd.Stderr = &errBuf
		runErr := cmd.Run()
		exit := 0
		if runErr != nil {
			var exitErr *exec.ExitError
			if errors.As(runErr, &exitErr) {
				exit = exitErr.ExitCode()
			} else {
				t.Fatalf("failed to run CLI command: %v", runErr)
			}
		}
		return outBuf.String(), errBuf.String(), exit
	}

	// 2. sila sync --db=... --json on held lease -> exit 2, exact JSON
	t.Run("JSONContentionExit2", func(t *testing.T) {
		stdout, stderr, exit := runCLI([]string{"sila", "sync", "--db=" + dbPath, "--json"})
		if exit != 2 {
			t.Fatalf("expected exit 2, got %d. stderr: %s, stdout: %s", exit, stderr, stdout)
		}
		if stderr != "" {
			t.Fatalf("expected empty stderr, got: %s", stderr)
		}

		var payload syncBusyResult
		if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
			t.Fatalf("failed to unmarshal JSON output: %v, raw stdout: %q", err, stdout)
		}
		if payload.Code != "SYNC_BUSY" || payload.Status != "busy" || payload.Message != "another sync is running" || !payload.Retryable {
			t.Fatalf("unexpected busy payload: %+v", payload)
		}

		// Verify raw string format has no extra banner
		wantRaw := "{\"code\":\"SYNC_BUSY\",\"status\":\"busy\",\"message\":\"another sync is running\",\"retryable\":true}\n"
		if stdout != wantRaw {
			t.Fatalf("raw stdout mismatch:\ngot:  %q\nwant: %q", stdout, wantRaw)
		}

		// Verify database file was not modified
		infoAfter, err := os.Stat(dbPath)
		if err != nil {
			t.Fatalf("stat after failed: %v", err)
		}
		if infoAfter.ModTime() != dbInfoBefore.ModTime() || infoAfter.Size() != dbInfoBefore.Size() {
			t.Fatalf("database file was modified during busy contention")
		}

		// Verify no WAL or SHM files were created
		if _, err := os.Stat(dbPath + "-wal"); err == nil {
			t.Fatalf("WAL file created during busy contention")
		}
		if _, err := os.Stat(dbPath + "-shm"); err == nil {
			t.Fatalf("SHM file created during busy contention")
		}
	})

	// 3. --force cannot defeat the sync lease -> exit 2, SYNC_BUSY
	t.Run("ForceCannotDefeatLease", func(t *testing.T) {
		stdout, stderr, exit := runCLI([]string{"sila", "sync", "--db=" + dbPath, "--force", "--json"})
		if exit != 2 {
			t.Fatalf("expected exit 2 with --force, got %d. stderr: %s, stdout: %s", exit, stderr, stdout)
		}
		var payload syncBusyResult
		if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
			t.Fatalf("failed to unmarshal JSON output: %v", err)
		}
		if payload.Code != "SYNC_BUSY" {
			t.Fatalf("expected SYNC_BUSY, got: %s", payload.Code)
		}
	})

	// 4. Human mode contention -> exit 2, message to stderr, empty stdout
	t.Run("HumanContentionExit2", func(t *testing.T) {
		stdout, stderr, exit := runCLI([]string{"sila", "sync", "--db=" + dbPath})
		if exit != 2 {
			t.Fatalf("expected exit 2 in human mode, got %d. stderr: %s", exit, stderr)
		}
		if stdout != "" {
			t.Fatalf("expected empty stdout in human mode contention, got: %s", stdout)
		}
		if !strings.Contains(stderr, "another sync is running; retry after it finishes") {
			t.Fatalf("expected retry message on stderr, got: %s", stderr)
		}
	})

	// 5. Dry-run while lease is held -> exit 0, dry-run succeeds
	t.Run("DryRunWithHeldLease", func(t *testing.T) {
		stdout, stderr, exit := runCLI([]string{"sila", "sync", "--db=" + dbPath, "--dry-run", "--json"})
		if exit != 0 {
			t.Fatalf("expected exit 0 for dry-run, got %d. stderr: %s", exit, stderr)
		}
		if !strings.Contains(stdout, `"dry_run":true`) && !strings.Contains(stdout, `"dry_run": true`) {
			t.Fatalf("expected dry_run:true in json, got: %s", stdout)
		}
	})

	// 6. Release fixture lease
	if err := releaseFixture(); err != nil {
		t.Fatalf("failed to release fixture lease: %v", err)
	}

	// 7. Normal sync succeeds after lease release -> exit 0
	t.Run("NormalSyncAfterRelease", func(t *testing.T) {
		stdout, stderr, exit := runCLI([]string{"sila", "sync", "--db=" + dbPath, "--json"})
		if exit != 0 {
			t.Fatalf("expected exit 0 after lease release, got %d. stderr: %s, stdout: %s", exit, stderr, stdout)
		}
		if !strings.Contains(stdout, "facts_distilled") {
			t.Fatalf("expected successful sync JSON output, got: %s", stdout)
		}
	})

	// 8. Other lock errors exit 1 (e.g. permission denied)
	t.Run("OtherLockErrorsExit1", func(t *testing.T) {
		roDir := filepath.Join(tmp, "ro_dir")
		if err := os.MkdirAll(roDir, 0755); err != nil {
			t.Fatalf("failed to create dir: %v", err)
		}
		if err := os.Chmod(roDir, 0500); err != nil {
			t.Fatalf("failed to chmod dir: %v", err)
		}
		defer func() {
			_ = os.Chmod(roDir, 0755)
		}()

		badDB := filepath.Join(roDir, "forbidden.db")
		_, stderr, exit := runCLI([]string{"sila", "sync", "--db=" + badDB, "--json"})
		if exit != 1 {
			t.Fatalf("expected exit 1 for lock/db error, got %d. stderr: %s", exit, stderr)
		}
		if !strings.Contains(stderr, "sila:") {
			t.Fatalf("expected sila error on stderr, got: %s", stderr)
		}
	})
}
