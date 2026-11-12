package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDoctorOrphanScan(t *testing.T) {
	currentUID := os.Getuid()
	processes := []Proc{
		{PID: 100, PPID: 1, UID: currentUID, Exe: "/usr/local/bin/sila", Args: []string{"sila", "mcp"}},
		{PID: 101, PPID: 1, UID: currentUID + 1, Exe: "/usr/local/bin/sila", Args: []string{"sila", "mcp"}},
		{PID: 102, PPID: 100, UID: currentUID, Exe: "/usr/local/bin/sila", Args: []string{"sila", "mcp"}},
		{PID: 103, PPID: 1, UID: currentUID, Exe: "/usr/local/bin/other", Args: []string{"other", "mcp"}},
		{PID: 104, PPID: 1, UID: currentUID, Exe: "/usr/local/bin/sila", Args: []string{"sila", "daemon"}},
	}
	orphans := orphanedMCPProcesses(processes)
	if len(orphans) != 1 || orphans[0].PID != 100 {
		t.Fatalf("orphans = %+v", orphans)
	}
}

func TestDoctorStaleSocketRepair(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "stale.sock")
	if err := os.WriteFile(socket, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	report := RunMCPDoctor(MCPDoctorOptions{SocketPath: socket, Home: dir, DryRun: true, Repair: true, Processes: []Proc{}})
	if report.Healthy {
		t.Fatalf("stale socket reported healthy: %+v", report)
	}
	if _, err := os.Stat(socket); err != nil {
		t.Fatal("dry-run removed the socket")
	}
	report = RunMCPDoctor(MCPDoctorOptions{SocketPath: socket, Home: dir, Repair: true, Processes: []Proc{}})
	if _, err := os.Stat(socket); !os.IsNotExist(err) {
		t.Fatalf("socket still exists: %v", err)
	}
	if !report.Repaired {
		t.Fatalf("no repair recorded: %+v", report)
	}
}

func TestDoctorForeignProcessNeverSignalled(t *testing.T) {
	processes := []Proc{{PID: 200, PPID: 1, UID: os.Getuid() + 1, Exe: "/usr/local/bin/sila", Args: []string{"sila", "mcp"}}}
	report := RunMCPDoctor(MCPDoctorOptions{SocketPath: filepath.Join(t.TempDir(), "none.sock"), Home: t.TempDir(), Repair: true, Processes: processes})
	for _, check := range report.Checks {
		if check.Name == "orphans" && check.Status == "fail" {
			t.Fatalf("foreign process treated as orphan: %+v", report)
		}
	}
}
