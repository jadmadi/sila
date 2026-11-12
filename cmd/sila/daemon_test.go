package main

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func daemonRun(t *testing.T, bin string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	out, err := cmd.CombinedOutput()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("run %v: %v", args, err)
	}
	return string(out), code
}

func TestDaemonLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the CLI and forks a daemon")
	}
	bin := buildCLIForCanary(t)
	dir := t.TempDir()
	socket := filepath.Join(dir, "sila.sock")
	db := filepath.Join(dir, "s.db")
	t.Cleanup(func() { _, _ = daemonRun(t, bin, "daemon", "stop", "--socket="+socket) })

	out, code := daemonRun(t, bin, "daemon", "start", "--socket="+socket, "--db="+db)
	if code != 0 || !strings.Contains(out, "daemon started") {
		t.Fatalf("start code=%d out=%s", code, out)
	}
	statusOut, code := daemonRun(t, bin, "daemon", "status", "--socket="+socket, "--json")
	if code != 0 {
		t.Fatalf("status code=%d out=%s", code, statusOut)
	}
	var status map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(statusOut)), &status); err != nil {
		t.Fatalf("status json: %v\n%s", err, statusOut)
	}
	if pid, ok := status["pid"].(float64); !ok || pid <= 0 {
		t.Fatalf("status = %+v", status)
	}
	if status["socket"] != socket {
		t.Fatalf("socket = %v", status["socket"])
	}
	secondOut, code := daemonRun(t, bin, "daemon", "start", "--socket="+socket, "--db="+db)
	if code != 0 || !strings.Contains(secondOut, "already running") {
		t.Fatalf("second start code=%d out=%s", code, secondOut)
	}
	stopOut, code := daemonRun(t, bin, "daemon", "stop", "--socket="+socket)
	if code != 0 || !strings.Contains(stopOut, "stopped") {
		t.Fatalf("stop code=%d out=%s", code, stopOut)
	}
	finalOut, code := daemonRun(t, bin, "daemon", "status", "--socket="+socket)
	if code != 0 || !strings.Contains(finalOut, "not running") {
		t.Fatalf("final status code=%d out=%s", code, finalOut)
	}
}

func TestDaemonUsage(t *testing.T) {
	bin := buildCLIForCanary(t)
	out, code := daemonRun(t, bin, "daemon")
	if code != 2 || !strings.Contains(out, "usage: sila daemon") {
		t.Fatalf("usage code=%d out=%s", code, out)
	}
}
