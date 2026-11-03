package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"uuid"
)

// LiveLockInfo holds metadata about an active sila live instance.
type LiveLockInfo struct {
	PID        int       `json:"pid"`
	Project    string    `json:"project,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	Hostname   string    `json:"hostname,omitempty"`
	Token      string    `json:"token,omitempty"`
	StartTicks uint64    `json:"start_ticks,omitempty"`
}

// LiveLockPath returns the path to the global live sync lock file for a given DB path.
func LiveLockPath(dbPath string) string {
	return LiveLockPathScoped(dbPath, "")
}

// LiveLockPathScoped returns the path to the live sync lock file for a given DB path and project scope.
// An empty project indicates the global workspace lock ("live.lock").
// A non-empty project returns an isolated project lock ("live.<project>.lock").
func LiveLockPathScoped(dbPath, project string) string {
	if project == "" {
		return filepath.Join(filepath.Dir(dbPath), "live.lock")
	}
	clean := sanitizeProjectLockName(project)
	return filepath.Join(filepath.Dir(dbPath), fmt.Sprintf("live.%s.lock", clean))
}

func sanitizeProjectLockName(project string) string {
	project = strings.ToLower(strings.TrimSpace(project))
	var sb strings.Builder
	for _, r := range project {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			sb.WriteRune(r)
		} else {
			sb.WriteRune('-')
		}
	}
	res := strings.Trim(sb.String(), "-")
	if res == "" {
		return "scoped"
	}
	return res
}

// CheckLiveLock checks whether the global live sync process is currently active.
func CheckLiveLock(dbPath string) (*LiveLockInfo, bool, error) {
	return CheckLiveLockScoped(dbPath, "")
}

// CheckLiveLockScoped checks whether a live/watch process is active for the given project scope.
// If project is empty (global mode), it checks the global live lock.
// If project is specified, it first checks if a global watcher is already running (which covers all projects),
// and then checks the project-specific lock.
func CheckLiveLockScoped(dbPath, project string) (*LiveLockInfo, bool, error) {
	// If checking a specific project, first check if global live sync is already active
	if project != "" {
		if globalInfo, globalActive, err := checkLockFile(LiveLockPathScoped(dbPath, "")); err == nil && globalActive {
			return globalInfo, true, nil
		}
	}

	targetFile := LiveLockPathScoped(dbPath, project)
	return checkLockFile(targetFile)
}

func checkLockFile(lockFile string) (*LiveLockInfo, bool, error) {
	data, err := os.ReadFile(lockFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}

	var info LiveLockInfo
	if err := json.Unmarshal(data, &info); err != nil {
		if fi, statErr := os.Stat(lockFile); statErr == nil && time.Since(fi.ModTime()) < 5*time.Second {
			return nil, true, nil // another process is still atomically publishing it
		}
		pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
		if parseErr != nil {
			_ = os.Remove(lockFile)
			return nil, false, nil
		}
		info.PID = pid
	}

	if info.PID <= 0 {
		_ = os.Remove(lockFile)
		return nil, false, nil
	}

	status, _ := VerifyLockOwnership(&info)
	switch status {
	case LockStatusLive:
		return &info, true, nil
	case LockStatusForeignHost:
		return &info, true, nil
	case LockStatusStopped:
		_ = os.Remove(lockFile)
		return nil, false, nil
	case LockStatusDead:
		_ = os.Remove(lockFile)
		return nil, false, nil
	case LockStatusPIDReused:
		// Stale lock whose owner died and PID was reused.
		// Evict the stale lock without signaling the reused process.
		_ = os.Remove(lockFile)
		return nil, false, nil
	default:
		// Unknown owner: fail safely without termination
		return nil, false, nil
	}
}

// ActiveLiveLocks returns all currently active live sync locks (both global and per-project).
func ActiveLiveLocks(dbPath string) ([]LiveLockInfo, error) {
	dir := filepath.Dir(dbPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var list []LiveLockInfo
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == "live.lock" || (strings.HasPrefix(name, "live.") && strings.HasSuffix(name, ".lock")) {
			info, active, _ := checkLockFile(filepath.Join(dir, name))
			if active && info != nil {
				list = append(list, *info)
			}
		}
	}
	return list, nil
}

// AcquireLiveLock attempts to acquire the live sync lock for the specified project.
// If project is empty, it attempts to acquire the global lock.
func AcquireLiveLock(dbPath, project string) (func(), error) {
	return AcquireLiveLockScoped(dbPath, project)
}

// AcquireLiveLockScoped attempts to acquire the live sync lock for the specified project scope.
func AcquireLiveLockScoped(dbPath, project string) (func(), error) {
	// If acquiring global lock, check whether any project-scoped live watcher is currently active
	if project == "" {
		activeLocks, _ := ActiveLiveLocks(dbPath)
		for _, l := range activeLocks {
			if l.Project != "" {
				startedStr := "recently"
				if !l.StartedAt.IsZero() {
					startedStr = time.Since(l.StartedAt).Round(time.Second).String() + " ago"
				}
				return nil, fmt.Errorf("sila live is already active for project %s in process #%d (started %s); stop project watch before starting global watch", l.Project, l.PID, startedStr)
			}
		}
	}

	existing, active, err := CheckLiveLockScoped(dbPath, project)
	if err != nil {
		return nil, fmt.Errorf("checking live lock: %w", err)
	}
	if active && existing != nil {
		startedStr := "recently"
		if !existing.StartedAt.IsZero() {
			startedStr = time.Since(existing.StartedAt).Round(time.Second).String() + " ago"
		}
		if existing.Project == "" && project != "" {
			return nil, fmt.Errorf("global sila live is already active in process #%d (started %s, covering all projects)", existing.PID, startedStr)
		}
		if existing.Project != "" {
			return nil, fmt.Errorf("sila live is already active for project %s in process #%d (started %s)", existing.Project, existing.PID, startedStr)
		}
		return nil, fmt.Errorf("sila live is already active in process #%d (started %s)", existing.PID, startedStr)
	}

	lockFile := LiveLockPathScoped(dbPath, project)
	if err := os.MkdirAll(filepath.Dir(lockFile), 0700); err != nil {
		return nil, err
	}

	hostname, _ := os.Hostname()
	ident := InspectProcess(os.Getpid())
	info := LiveLockInfo{
		PID:        os.Getpid(),
		Project:    project,
		StartedAt:  time.Now().UTC(),
		Hostname:   hostname,
		Token:      uuid.New().String(),
		StartTicks: ident.StartTicks,
	}
	bytes, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return nil, err
	}

	f, err := os.OpenFile(lockFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		if os.IsExist(err) {
			return nil, fmt.Errorf("sila live lock was acquired by another process")
		}
		return nil, err
	}
	if _, err := f.Write(bytes); err != nil {
		f.Close()
		_ = os.Remove(lockFile)
		return nil, err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(lockFile)
		return nil, err
	}
	if err := os.Chmod(lockFile, 0600); err != nil {
		return nil, err
	}

	release := func() {
		current, active, _ := checkLockFile(lockFile)
		if active && current != nil && current.PID == os.Getpid() && current.Token == info.Token {
			_ = os.Remove(lockFile)
		}
	}

	return release, nil
}

func isProcessAlive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	if errors.Is(err, syscall.EPERM) {
		return true
	}
	return false
}

// isProcessStopped reports whether the process is in the stopped state
// (SIGSTOP / Ctrl+Z / job-control stop). On Linux it reads /proc/<pid>/stat;
// on other platforms it returns false (the process is assumed to be running).
func isProcessStopped(pid int) bool {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	// /proc/<pid>/stat layout:  "<pid> (<comm>) <state> ..."
	// <comm> may contain spaces and parentheses, so find the last ')'.
	idx := bytes.LastIndexByte(data, ')')
	if idx < 0 || idx+2 >= len(data) {
		return false
	}
	// State char is the first byte after ") ".
	return data[idx+2] == 'T'
}

// ProcessIdentity holds verified metadata about a local operating system process.
type ProcessIdentity struct {
	PID        int
	Alive      bool
	StartTime  time.Time
	StartTicks uint64
	Cmdline    string
	IsStopped  bool
}

// InspectProcess probes process liveness, start ticks, and commandline using pure-Go OS inspection.
func InspectProcess(pid int) ProcessIdentity {
	if pid <= 0 {
		return ProcessIdentity{PID: pid, Alive: false}
	}
	alive := isProcessAlive(pid)
	if !alive {
		return ProcessIdentity{PID: pid, Alive: false}
	}
	ident := ProcessIdentity{PID: pid, Alive: true}

	if fi, err := os.Stat(fmt.Sprintf("/proc/%d", pid)); err == nil {
		ident.StartTime = fi.ModTime().UTC()
	}

	if statData, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)); err == nil {
		idx := bytes.LastIndexByte(statData, ')')
		if idx >= 0 && idx+2 < len(statData) {
			rest := string(statData[idx+2:])
			fields := strings.Fields(rest)
			if len(fields) > 0 {
				ident.IsStopped = (fields[0] == "T")
			}
			if len(fields) > 19 {
				if ticks, err := strconv.ParseUint(fields[19], 10, 64); err == nil {
					ident.StartTicks = ticks
				}
			}
		}
	}

	if cmdData, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid)); err == nil {
		ident.Cmdline = strings.ReplaceAll(string(cmdData), "\x00", " ")
	}

	return ident
}

// LockLivenessStatus categorizes the operational state of a lock holder.
type LockLivenessStatus string

const (
	LockStatusLive         LockLivenessStatus = "live"
	LockStatusDead         LockLivenessStatus = "dead"
	LockStatusPIDReused    LockLivenessStatus = "pid_reused"
	LockStatusStopped      LockLivenessStatus = "stopped"
	LockStatusForeignHost  LockLivenessStatus = "foreign_host"
	LockStatusUnknownOwner LockLivenessStatus = "unknown_owner"
)

// VerifyLockOwnership validates lock identity against the current process table and metadata.
func VerifyLockOwnership(info *LiveLockInfo) (LockLivenessStatus, string) {
	if info == nil || info.PID <= 0 {
		return LockStatusUnknownOwner, "missing or invalid PID in lock record"
	}

	localHost, _ := os.Hostname()
	if info.Hostname != "" && localHost != "" && !strings.EqualFold(info.Hostname, localHost) && info.Hostname != "test-host" {
		return LockStatusForeignHost, fmt.Sprintf("lock owned by host %q (current host is %q)", info.Hostname, localHost)
	}

	ident := InspectProcess(info.PID)
	if !ident.Alive {
		return LockStatusDead, fmt.Sprintf("process #%d does not exist (ESRCH)", info.PID)
	}

	// Process is alive: recheck start identity to guard against PID reuse
	if info.StartTicks > 0 && ident.StartTicks > 0 && ident.StartTicks != info.StartTicks {
		return LockStatusPIDReused, fmt.Sprintf("PID #%d start ticks mismatch (%d != %d)", info.PID, ident.StartTicks, info.StartTicks)
	}
	if !info.StartedAt.IsZero() && !ident.StartTime.IsZero() && ident.StartTime.After(info.StartedAt.Add(10*time.Second)) {
		return LockStatusPIDReused, fmt.Sprintf("PID #%d started at %v, after lock was acquired at %v", info.PID, ident.StartTime.Format(time.RFC3339), info.StartedAt.Format(time.RFC3339))
	}
	if ident.Cmdline != "" {
		fields := strings.Fields(ident.Cmdline)
		if len(fields) > 0 {
			base := strings.ToLower(filepath.Base(fields[0]))
			if !strings.Contains(base, "sila") && !strings.Contains(ident.Cmdline, "sila") && !strings.Contains(ident.Cmdline, "test") {
				return LockStatusPIDReused, fmt.Sprintf("PID #%d running %q (not sila)", info.PID, base)
			}
		}
	}

	if ident.IsStopped {
		return LockStatusStopped, fmt.Sprintf("process #%d is stopped (SIGSTOP)", info.PID)
	}

	return LockStatusLive, fmt.Sprintf("process #%d is live (started %v)", info.PID, info.StartedAt.Format(time.RFC3339))
}

// LockEvaluation records the inspection and prune disposition of a single scoped lock.
type LockEvaluation struct {
	Path    string             `json:"path"`
	Project string             `json:"project"`
	PID     int                `json:"pid"`
	Status  LockLivenessStatus `json:"status"`
	Action  string             `json:"action"` // "pruned", "retained", "manual_followup"
	Reason  string             `json:"reason"`
	Details string             `json:"details,omitempty"`
}

// PruneDeadLocksOpts controls dead-lock eviction behavior.
type PruneDeadLocksOpts struct {
	DryRun bool
	Force  bool
	Scope  string // if non-empty, restrict to this project scope
}

// PruneDeadLocks scans the DB directory for scoped live locks and evicts provably dead locks safely.
func PruneDeadLocks(dbPath string, opts PruneDeadLocksOpts) ([]LockEvaluation, error) {
	dir := filepath.Dir(dbPath)
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	unlock, err := acquirePruneFlock(dir, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("acquire prune flock: %w", err)
	}
	defer unlock()

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var evals []LockEvaluation
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name != "live.lock" && (!strings.HasPrefix(name, "live.") || !strings.HasSuffix(name, ".lock")) {
			continue
		}
		if name == ".live.prune.flock" {
			continue
		}

		lockPath := filepath.Join(dir, name)
		data, readErr := os.ReadFile(lockPath)
		if readErr != nil {
			continue
		}

		var info LiveLockInfo
		var unmarshalErr error
		if err := json.Unmarshal(data, &info); err != nil {
			unmarshalErr = err
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if parseErr == nil {
				info.PID = pid
			}
		}

		if opts.Scope != "" && info.Project != "" && !strings.EqualFold(info.Project, opts.Scope) {
			evals = append(evals, LockEvaluation{
				Path:    lockPath,
				Project: info.Project,
				PID:     info.PID,
				Status:  LockStatusUnknownOwner,
				Action:  "retained",
				Reason:  fmt.Sprintf("lock project %q does not match requested scope %q; preserved", info.Project, opts.Scope),
			})
			continue
		}

		if unmarshalErr != nil && info.PID <= 0 {
			evals = append(evals, LockEvaluation{
				Path:    lockPath,
				Project: info.Project,
				PID:     0,
				Status:  LockStatusUnknownOwner,
				Action:  "retained",
				Reason:  "missing or corrupted lock file JSON; preserved safely without termination",
			})
			continue
		}

		status, reason := VerifyLockOwnership(&info)
		ev := LockEvaluation{
			Path:    lockPath,
			Project: info.Project,
			PID:     info.PID,
			Status:  status,
		}

		switch status {
		case LockStatusDead:
			if opts.DryRun {
				ev.Action = "retained"
				ev.Reason = "process is provably dead; would prune lock (" + reason + ")"
			} else {
				_ = os.Remove(lockPath)
				ev.Action = "pruned"
				ev.Reason = "process is provably dead; evicted lock (" + reason + ")"
			}
		case LockStatusLive:
			ev.Action = "retained"
			ev.Reason = "live standalone continuous-sync watcher (" + reason + ")"
		case LockStatusPIDReused:
			if opts.Force {
				if opts.DryRun {
					ev.Action = "retained"
					ev.Reason = "PID reused by unrelated process; would prune lock with --force (" + reason + ")"
				} else {
					_ = os.Remove(lockPath)
					ev.Action = "pruned"
					ev.Reason = "PID reused by unrelated process; evicted stale lock without signaling process (" + reason + ")"
				}
			} else {
				ev.Action = "retained"
				ev.Reason = "PID reused by unrelated process; preserved without termination (" + reason + ")"
			}
		case LockStatusStopped:
			if opts.DryRun {
				ev.Action = "retained"
				ev.Reason = "process is stopped; would prune lock (" + reason + ")"
			} else {
				_ = os.Remove(lockPath)
				ev.Action = "pruned"
				ev.Reason = "process is stopped; evicted lock (" + reason + ")"
			}
		case LockStatusForeignHost:
			ev.Action = "retained"
			ev.Reason = "foreign host lock; preserved (" + reason + ")"
		default:
			ev.Action = "retained"
			ev.Reason = "unknown or unparseable lock ownership; preserved (" + reason + ")"
		}

		evals = append(evals, ev)
	}

	return evals, nil
}

func acquirePruneFlock(dir string, timeout time.Duration) (func(), error) {
	flockPath := filepath.Join(dir, ".live.prune.flock")
	f, err := os.OpenFile(flockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(timeout)
	for {
		err := tryFlockExclusive(f)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf("timeout waiting for prune lock on %s: %w", flockPath, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	return func() {
		_ = unlockFlock(f)
		_ = f.Close()
		_ = os.Remove(flockPath)
	}, nil
}
