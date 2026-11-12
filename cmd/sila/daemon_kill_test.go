package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/daemon"
)

func TestDaemonKillRecovers(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the CLI and kills a daemon")
	}
	bin := buildCLIForCanary(t)
	dir := t.TempDir()
	socket := filepath.Join(dir, "sila.sock")
	db := filepath.Join(dir, "s.db")
	t.Cleanup(func() { _, _ = daemonRun(t, bin, "daemon", "stop", "--socket="+socket) })

	if out, code := daemonRun(t, bin, "daemon", "start", "--socket="+socket, "--db="+db); code != 0 {
		t.Fatalf("start code=%d out=%s", code, out)
	}
	statusOut, _ := daemonRun(t, bin, "daemon", "status", "--socket="+socket, "--json")
	var status map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(statusOut)), &status); err != nil {
		t.Fatal(err)
	}
	pid := int(status["pid"].(float64))
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if out, code := daemonRun(t, bin, "daemon", "start", "--socket="+socket, "--db="+db); code != 0 {
		t.Fatalf("restart code=%d out=%s", code, out)
	}
	client, err := daemon.DialWithRetry(socket, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := client.Call(ctx, "status", nil); err != nil {
		t.Fatal(err)
	}
}

func TestDaemonInFlightAfterKillReturns(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the CLI and kills a daemon")
	}
	bin := buildCLIForCanary(t)
	dir := t.TempDir()
	socket := filepath.Join(dir, "sila.sock")
	db := filepath.Join(dir, "s.db")
	t.Cleanup(func() { _, _ = daemonRun(t, bin, "daemon", "stop", "--socket="+socket) })
	if out, code := daemonRun(t, bin, "daemon", "start", "--socket="+socket, "--db="+db); code != 0 {
		t.Fatalf("start code=%d out=%s", code, out)
	}
	statusOut, _ := daemonRun(t, bin, "daemon", "status", "--socket="+socket, "--json")
	var status map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(statusOut)), &status); err != nil {
		t.Fatal(err)
	}
	client, err := daemon.Dial(socket, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	process, _ := os.FindProcess(int(status["pid"].(float64)))
	_ = process.Signal(syscall.SIGKILL)
	time.Sleep(100 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if _, err := client.Call(ctx, "status", nil); err == nil {
		t.Fatal("call after kill succeeded")
	}
}
