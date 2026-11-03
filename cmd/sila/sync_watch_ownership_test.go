package main

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

func TestWatchSyncPassOwnership(t *testing.T) {
	if probe := os.Getenv("SILA_WATCH_OWNERSHIP_PROBE"); probe != "" {
		os.Args = strings.Split(probe, "\x1f")
		main()
		return
	}
	if os.Getenv("SILA_TEST_WATCH_INTEGRATION") != "1" {
		t.Skip("set SILA_TEST_WATCH_INTEGRATION=1 to run the watch lease integration test")
	}

	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "watch_owner.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	_, err = st.EnsureProject(tmp)
	if err != nil {
		_ = st.Close()
		t.Fatalf("failed to ensure project: %v", err)
	}
	_ = st.Close()

	// Launch watch process with 1s interval
	args := []string{"sila", "watch", "--db=" + dbPath, "--interval=1s", "--json"}
	cmd := exec.Command(os.Args[0], "-test.run=^TestWatchSyncPassOwnership$")
	cmd.Env = append(os.Environ(), "SILA_WATCH_OWNERSHIP_PROBE="+strings.Join(args, "\x1f"))

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe failed: %v", err)
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start watcher: %v", err)
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()

	scanner := bufio.NewScanner(stdoutPipe)
	events := make(chan string, 20)
	go func() {
		for scanner.Scan() {
			events <- scanner.Text()
		}
		close(events)
	}()

	// 1. Wait for watch started event
	select {
	case line := <-events:
		if !strings.Contains(line, `"event":"started"`) {
			t.Fatalf("expected watch_started event, got: %s", line)
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("timed out waiting for watch_started")
	}

	// 2. Wait for initial pass to complete and watcher to become idle
	select {
	case line := <-events:
		if !strings.Contains(line, `"event":"status"`) {
			t.Fatalf("expected initial status event, got: %s", line)
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("timed out waiting for initial pass")
	}

	// 3. Wait for the watcher to become idle between passes, then acquire the
	// one-shot lease. The watcher may still be finishing the pass that emitted
	// the status event; polling avoids a timing race without weakening the
	// ownership assertion.
	var rel func() error
	deadline := time.Now().Add(8 * time.Second)
	for {
		rel, err = store.AcquireSyncLease(dbPath)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("watcher did not release the lease within 8s: %v", err)
		}
		time.Sleep(25 * time.Millisecond)
	}

	// 4. While one-shot sync holds lease, next watch pass must emit sync_busy
	select {
	case line := <-events:
		if !strings.Contains(line, `"event":"sync_busy"`) || !strings.Contains(line, `"code":"SYNC_BUSY"`) {
			t.Fatalf("expected sync_busy event while lease held, got: %s", line)
		}
	case <-time.After(15 * time.Second):
		_ = rel()
		t.Fatalf("timed out waiting for sync_busy event")
	}

	// 5. Release held lease; subsequent watch pass must recover and emit status
	if err := rel(); err != nil {
		t.Fatalf("failed to release lease: %v", err)
	}

	select {
	case line := <-events:
		if !strings.Contains(line, `"event":"status"`) {
			t.Fatalf("expected recovered status event, got: %s", line)
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("timed out waiting for recovered status event")
	}

	// 6. Stop watcher cleanly
	_ = cmd.Process.Kill()
	_ = cmd.Wait()

	// 7. Verify lease is cleanly unowned after process terminates
	finalRel, err := store.AcquireSyncLease(dbPath)
	if err != nil {
		t.Fatalf("expected clean reacquisition after watcher stopped, got: %v", err)
	}
	_ = finalRel()
}

func TestWatchBusyDoesNotReportSuccess(t *testing.T) {
	if probe := os.Getenv("SILA_WATCH_BUSY_PROBE"); probe != "" {
		os.Args = strings.Split(probe, "\x1f")
		main()
		return
	}
	if os.Getenv("SILA_TEST_WATCH_INTEGRATION") != "1" {
		t.Skip("set SILA_TEST_WATCH_INTEGRATION=1 to run the watch lease integration test")
	}

	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "watch_busy.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	_, err = st.EnsureProject(tmp)
	if err != nil {
		_ = st.Close()
		t.Fatalf("failed to ensure project: %v", err)
	}
	_ = st.Close()

	// 1. Hold the fixture sync lease BEFORE watcher starts its first pass
	// Note: watch mode store.Open also checks withSyncLease; so we let store initialize,
	// or we hold lease right after watch started.
	// Let's test holding the lease around iterations.
	args := []string{"sila", "watch", "--db=" + dbPath, "--interval=1s", "--json"}
	cmd := exec.Command(os.Args[0], "-test.run=^TestWatchBusyDoesNotReportSuccess$")
	cmd.Env = append(os.Environ(), "SILA_WATCH_BUSY_PROBE="+strings.Join(args, "\x1f"))

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe failed: %v", err)
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start watcher: %v", err)
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()

	scanner := bufio.NewScanner(stdoutPipe)
	events := make(chan string, 20)
	var mu sync.Mutex
	var collected []string

	go func() {
		for scanner.Scan() {
			txt := scanner.Text()
			mu.Lock()
			collected = append(collected, txt)
			mu.Unlock()
			events <- txt
		}
		close(events)
	}()

	// Read watch_started
	select {
	case line := <-events:
		if !strings.Contains(line, `"event":"started"`) {
			t.Fatalf("expected watch_started, got: %s", line)
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("timed out waiting for watch_started")
	}

	// Read initial status
	select {
	case line := <-events:
		if !strings.Contains(line, `"event":"status"`) {
			t.Fatalf("expected status, got: %s", line)
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("timed out waiting for initial status")
	}

	// Wait for the watcher pass to finish its deferred lease release after
	// flushing the status line, matching TestWatchAndOneShotContention.
	var rel func() error
	deadline := time.Now().Add(8 * time.Second)
	for {
		rel, err = store.AcquireSyncLease(dbPath)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("failed to acquire fixture lease: %v", err)
		}
		time.Sleep(25 * time.Millisecond)
	}

	// Next event MUST be sync_busy and MUST NOT be a status event
	select {
	case line := <-events:
		var parsed map[string]any
		if err := json.Unmarshal([]byte(line), &parsed); err != nil {
			_ = rel()
			t.Fatalf("failed to parse JSONL line: %v (raw: %s)", err, line)
		}
		if parsed["event"] != "sync_busy" {
			_ = rel()
			t.Fatalf("expected event=sync_busy, got: %s", line)
		}
		if parsed["code"] != "SYNC_BUSY" {
			_ = rel()
			t.Fatalf("expected code=SYNC_BUSY, got: %s", line)
		}
		if parsed["retryable"] != true {
			_ = rel()
			t.Fatalf("expected retryable=true, got: %s", line)
		}
		// Assert no success/fact counts were reported in the busy event
		if _, exists := parsed["facts_distilled"]; exists {
			_ = rel()
			t.Fatalf("busy event must not report facts_distilled: %s", line)
		}
		if _, exists := parsed["in_sync"]; exists {
			_ = rel()
			t.Fatalf("busy event must not report in_sync: %s", line)
		}
	case <-time.After(15 * time.Second):
		_ = rel()
		t.Fatalf("timed out waiting for busy iteration")
	}

	// Release lease and observe recovery
	_ = rel()

	select {
	case line := <-events:
		if !strings.Contains(line, `"event":"status"`) {
			t.Fatalf("expected recovery status event, got: %s", line)
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("timed out waiting for recovery status")
	}
}
