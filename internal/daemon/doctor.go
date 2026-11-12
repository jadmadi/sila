package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

// Proc is one process table entry.
type Proc struct {
	PID  int
	PPID int
	UID  int
	Exe  string
	Args []string
}

// MCPDoctorOptions configures the MCP-aware doctor.
type MCPDoctorOptions struct {
	SocketPath string
	Home       string
	DryRun     bool
	Repair     bool
	Processes  []Proc
}

// MCPDoctorCheck is one health check result.
type MCPDoctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// MCPDoctorReport is the MCP doctor result.
type MCPDoctorReport struct {
	Checks   []MCPDoctorCheck `json:"checks"`
	Healthy  bool             `json:"healthy"`
	Repaired bool             `json:"repaired"`
	Actions  []string         `json:"actions,omitempty"`
}

func (r *MCPDoctorReport) add(name, status, detail string) {
	r.Checks = append(r.Checks, MCPDoctorCheck{Name: name, Status: status, Detail: detail})
	if status == "fail" {
		r.Healthy = false
	}
}

// RunMCPDoctor checks the daemon, socket, orphans, and schema cache.
func RunMCPDoctor(opts MCPDoctorOptions) *MCPDoctorReport {
	report := &MCPDoctorReport{Healthy: true}
	socket := opts.SocketPath
	if socket == "" {
		socket = DefaultSocketPath()
	}
	live := false
	if client, err := Dial(socket, 500*time.Millisecond); err == nil {
		live = true
		if raw, callErr := client.Call(contextBackground(), "status", nil); callErr == nil {
			report.add("daemon", "pass", "daemon reachable: "+string(raw))
		} else {
			report.add("daemon", "warn", callErr.Error())
		}
		_ = client.Close()
	} else {
		report.add("daemon", "fail", "daemon not running; fallback mode: in-process MCP (SILA_DAEMON=off)")
	}

	if _, err := os.Stat(socket); err != nil {
		report.add("socket", "pass", "no socket present")
	} else if live {
		report.add("socket", "pass", "socket is live")
	} else {
		report.add("socket", "fail", "stale socket at "+socket)
		if opts.Repair {
			if client, recheck := Dial(socket, 300*time.Millisecond); recheck == nil {
				_ = client.Close()
				report.add("socket", "pass", "daemon appeared during repair; socket kept")
			} else if opts.DryRun {
				report.Actions = append(report.Actions, "would remove stale socket "+socket)
			} else if err := os.Remove(socket); err != nil {
				report.add("socket", "fail", "remove socket: "+err.Error())
			} else {
				report.Actions = append(report.Actions, "removed stale socket "+socket)
				report.Repaired = true
			}
		}
	}

	processes := opts.Processes
	if processes == nil {
		processes = ScanProcesses()
	}
	orphans := orphanedMCPProcesses(processes)
	if len(orphans) == 0 {
		report.add("orphans", "pass", "no orphaned sila mcp processes")
	} else {
		pids := make([]string, 0, len(orphans))
		for _, proc := range orphans {
			pids = append(pids, strconv.Itoa(proc.PID))
		}
		report.add("orphans", "fail", "orphaned sila mcp processes: "+strings.Join(pids, ", "))
		if opts.Repair {
			for _, proc := range orphans {
				if opts.DryRun {
					report.Actions = append(report.Actions, fmt.Sprintf("would terminate pid %d", proc.PID))
					continue
				}
				if err := syscall.Kill(proc.PID, syscall.SIGTERM); err != nil {
					report.add("orphans", "fail", fmt.Sprintf("signal pid %d: %v", proc.PID, err))
					continue
				}
				if waitForExit(proc.PID, 2*time.Second) {
					report.Actions = append(report.Actions, fmt.Sprintf("terminated pid %d", proc.PID))
					report.Repaired = true
				} else {
					report.add("orphans", "fail", fmt.Sprintf("pid %d ignored SIGTERM; not escalated", proc.PID))
				}
			}
		}
	}

	cache := store.CheckMCPClientCache(opts.Home, store.DefaultContinuityToolCount)
	status := "pass"
	if string(cache.Status) == "fail" || string(cache.Status) == "warn" {
		status = "warn"
	}
	report.add("schema_cache", status, cache.Message)
	return report
}

func contextBackground() context.Context { return context.Background() }

func orphanedMCPProcesses(processes []Proc) []Proc {
	known := map[int]bool{}
	currentUID := os.Getuid()
	for _, proc := range processes {
		known[proc.PID] = true
	}
	var orphans []Proc
	for _, proc := range processes {
		if filepath.Base(proc.Exe) != "sila" || proc.UID != currentUID {
			continue
		}
		hasMCP := false
		for _, arg := range proc.Args[1:] {
			if arg == "mcp" {
				hasMCP = true
				break
			}
		}
		if !hasMCP {
			continue
		}
		if proc.PPID == 1 || !known[proc.PPID] || proc.PPID == 0 {
			orphans = append(orphans, proc)
		}
	}
	return orphans
}

func waitForExit(pid int, budget time.Duration) bool {
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return true
		}
		time.Sleep(25 * time.Millisecond)
	}
	return syscall.Kill(pid, 0) != nil
}

// ScanProcesses reads the process table on Linux; other platforms yield none.
func ScanProcesses() []Proc {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var processes []Proc
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		proc := Proc{PID: pid}
		statusData, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "status"))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(statusData), "\n") {
			if strings.HasPrefix(line, "PPid:") {
				fmt.Sscanf(line, "PPid: %d", &proc.PPID)
			}
			if strings.HasPrefix(line, "Uid:") {
				fields := strings.Fields(line)
				if len(fields) > 1 {
					proc.UID, _ = strconv.Atoi(fields[1])
				}
			}
		}
		cmdline, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		if err != nil || len(cmdline) == 0 {
			continue
		}
		proc.Args = strings.Split(strings.TrimRight(string(cmdline), "\x00"), "\x00")
		if len(proc.Args) > 0 {
			proc.Exe = proc.Args[0]
		}
		processes = append(processes, proc)
	}
	return processes
}
