package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorMCPNoDaemonExitsOne(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the CLI")
	}
	bin := buildCLIForCanary(t)
	socket := filepath.Join(t.TempDir(), "absent.sock")
	cmd := exec.Command(bin, "doctor", "--mcp", "--json")
	cmd.Env = append(os.Environ(), "SILA_DAEMON_SOCKET="+socket)
	raw, err := cmd.CombinedOutput()
	out := string(raw)
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	if code != 1 {
		t.Fatalf("doctor --mcp code=%d out=%s", code, out)
	}
	if !strings.Contains(out, "fallback mode") {
		t.Fatalf("fallback missing: %s", out)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &payload); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if _, ok := payload["mcp"]; !ok {
		t.Fatalf("mcp report missing: %+v", payload)
	}
}
