package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/jadmadi/sila/internal/version"
)

// ScanMCPServers counts the running `sila mcp` servers and returns every
// process with its executable and stale flag.
//
// Returns (0, nil) when the platform does not expose /proc (macOS, Windows), so
// the check is absent there rather than falsely passing.
func ScanMCPServers() (int, []MCPProcess) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, nil
	}
	total := 0
	var processes []MCPProcess
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || !IsSilaMCPServer(pid) {
			continue
		}
		total++
		exe, err := os.Readlink(filepath.Join("/proc", e.Name(), "exe"))
		stale := err == nil && strings.HasSuffix(exe, " (deleted)")
		ppid := mcpParentPID(pid)
		processes = append(processes, MCPProcess{
			PID:        pid,
			Client:     mcpParentClient(pid),
			Executable: strings.TrimSuffix(exe, " (deleted)"),
			Stale:      stale,
			Orphan:     ppid <= 1,
		})
	}
	sort.Slice(processes, func(i, j int) bool { return processes[i].PID < processes[j].PID })
	return total, processes
}

// IsSilaMCPServer reports whether the process at pid runs `sila mcp`.
func IsSilaMCPServer(pid int) bool {
	if pid <= 0 {
		return false
	}
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return false
	}
	args := strings.Split(strings.TrimRight(string(data), "\x00"), "\x00")
	return len(args) >= 2 && strings.EqualFold(filepath.Base(args[0]), "sila") && args[1] == "mcp"
}

func mcpParentPID(pid int) int {
	stat, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0
	}
	closeIdx := strings.LastIndex(string(stat), ")")
	if closeIdx < 0 {
		return 0
	}
	fields := strings.Fields(string(stat)[closeIdx+1:])
	if len(fields) < 2 {
		return 0
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0
	}
	return ppid
}

func mcpParentClient(pid int) string {
	ppid := mcpParentPID(pid)
	if ppid <= 1 {
		return ""
	}
	if data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(ppid), "cmdline")); err == nil {
		if args := strings.Split(strings.TrimRight(string(data), "\x00"), "\x00"); len(args) > 0 && args[0] != "" {
			return filepath.Base(args[0])
		}
	}
	if comm, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(ppid), "comm")); err == nil {
		return strings.TrimSpace(string(comm))
	}
	return ""
}

func procAvailable() bool {
	_, err := os.Stat("/proc")
	return err == nil
}

// InspectMCPFreshness inspects running `sila mcp` processes via /proc without
// touching SQLite or running full store diagnostics. Returns a nil CheckResult
// when /proc is unavailable on the current OS.
func InspectMCPFreshness() (*MCPFreshnessReport, *CheckResult) {
	total, processes := ScanMCPServers()
	var stale []MCPProcess
	orphans := 0
	for _, proc := range processes {
		if proc.Stale {
			stale = append(stale, proc)
		}
		if proc.Orphan {
			orphans++
		}
	}
	freshness := &MCPFreshnessReport{
		Running:          total,
		Stale:            len(stale),
		Orphans:          orphans,
		RestartRequired:  len(stale) > 0 || orphans > 0,
		InstalledVersion: version.Version,
		Processes:        processes,
	}
	if total == 0 && !procAvailable() {
		return freshness, nil
	}
	if len(stale) == 0 && orphans == 0 {
		msg := "No sila MCP servers are running."
		if total > 0 {
			msg = fmt.Sprintf("Every running sila MCP server is executing the installed binary (%s).", version.Version)
		}
		return freshness, &CheckResult{
			Category: "Runtime",
			Name:     "Running MCP Servers",
			Status:   StatusPass,
			Message:  msg,
		}
	}
	if len(stale) == 0 && orphans > 0 {
		return freshness, &CheckResult{
			Category: "Runtime",
			Name:     "Running MCP Servers",
			Status:   StatusWarn,
			Message:  fmt.Sprintf("%d orphaned sila MCP server(s) had no live parent session.", orphans),
			Detail:   "Review the PIDs; stop only sessions you recognize as finished.",
			Repair:   "Prefer one supervised server per client; orphaned stdio servers are reparented when their client exits.",
		}
	}

	counts := map[string]int{}
	pids := make([]string, 0, len(stale))
	for _, s := range stale {
		name := s.Client
		if name == "" {
			name = "unknown client"
		}
		counts[name]++
		pids = append(pids, strconv.Itoa(s.PID))
	}
	names := make([]string, 0, len(counts))
	for name, n := range counts {
		if n > 1 {
			name += " (" + strconv.Itoa(n) + ")"
		}
		names = append(names, name)
	}
	sort.Strings(names)

	verb := "servers are"
	if len(stale) == 1 {
		verb = "server is"
	}
	return freshness, &CheckResult{
		Category: "Runtime",
		Name:     "Running MCP Servers",
		Status:   StatusWarn,
		Message: fmt.Sprintf("%d of %d running sila MCP %s executing a replaced binary (installed %s; restart_required=true).",
			len(stale), total, verb, version.Version),
		Detail: "Clients to restart: " + strings.Join(names, ", ") + " (PIDs: " + strings.Join(pids, " ") + ")",
		Repair: "Restart those clients: each owns its sila MCP server process and spawns the installed binary on reconnect.",
	}
}

// AppendMCPFreshnessCheck contributes the running-server check to a doctor
// report and refreshes the summary so the result reflects it.
func AppendMCPFreshnessCheck(rep *DoctorReport) {
	freshness, check := InspectMCPFreshness()
	rep.MCP = freshness
	if check == nil {
		return
	}
	rep.AddCheck(check.Category, check.Name, check.Status, check.Message, check.Detail, check.Repair)
	rep.Finalize()
}
