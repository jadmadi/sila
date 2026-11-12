package goals

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	mrand "math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
	"uuid"
)

// Default timeouts and thresholds for claim locking and staleness heuristics.
const (
	DefaultLockAcquisitionTimeout = 2 * time.Second
	LockStaleThreshold            = 10 * time.Second
	DefaultClaimInactivityTimeout = 2 * time.Hour
)

// errLockBusy reports that another process or goroutine holds the goal lock.
var errLockBusy = errors.New("goal lock is held by another process")

// GoalClaim stores active lock, assignment metadata, and staleness diagnostics for an in-progress goal.
type GoalClaim struct {
	GoalSlug            string    `json:"goal_slug,omitempty"`
	ClaimedBy           string    `json:"claimed_by,omitempty"`
	Agent               string    `json:"agent,omitempty"`
	ClaimedAt           time.Time `json:"claimed_at"`
	UpdatedAt           time.Time `json:"updated_at"`
	Hostname            string    `json:"hostname,omitempty"`
	PID                 int       `json:"pid,omitempty"`
	SessionID           string    `json:"session_id,omitempty"`
	Status              string    `json:"status,omitempty"`         // "in_progress", "evaluating", "blocked"
	Note                string    `json:"note,omitempty"`           // Live status or progress report
	FilesToTouch        []string  `json:"files_to_touch,omitempty"` // Files/directories locked under this claim
	Stale               bool      `json:"stale,omitempty"`
	StaleReason         string    `json:"stale_reason,omitempty"`
	StaleAge            string    `json:"stale_age,omitempty"`
	IterationCount      int       `json:"iteration_count,omitempty"`
	ConsecutiveFailures int       `json:"consecutive_failures,omitempty"`
	LastFailureError    string    `json:"last_failure_error,omitempty"`
	StopAtTaskID        string    `json:"stop_at_task_id,omitempty"`
}

// FileConflict reports a concurrency collision where a candidate goal touches files already claimed by an active agent.
type FileConflict struct {
	GoalSlug         string   `json:"goal_slug"`
	ConflictingGoal  string   `json:"conflicting_goal"`
	HolderAgent      string   `json:"holder_agent"`
	OverlappingFiles []string `json:"overlapping_files"`
}

// NormalizeGoalPath cleans and normalizes a file or directory path for collision checking.
func NormalizeGoalPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	// Extract markdown link target if present: [text](path)
	if m := mdLinkRE.FindStringSubmatch(p); len(m) > 2 {
		p = m[2]
	}
	// Strip URL fragments e.g. #L10
	if idx := strings.Index(p, "#"); idx != -1 {
		p = p[:idx]
	}
	p = strings.Trim(p, "\"`'[]()")
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	p = strings.ReplaceAll(p, "\\", "/")
	p = filepath.ToSlash(filepath.Clean(p))
	p = strings.TrimPrefix(p, "./")
	p = strings.TrimPrefix(p, "/")
	p = strings.TrimSuffix(p, "/")
	return p
}

// PathsOverlap returns true if p1 and p2 refer to the same file or if one directory contains the other.
func PathsOverlap(p1, p2 string) bool {
	n1 := NormalizeGoalPath(p1)
	n2 := NormalizeGoalPath(p2)
	if n1 == "" || n2 == "" {
		return false
	}
	if n1 == "." || n2 == "." {
		return true
	}
	if n1 == n2 {
		return true
	}
	if strings.HasPrefix(n2, n1+"/") {
		return true
	}
	if strings.HasPrefix(n1, n2+"/") {
		return true
	}
	return false
}

// OverlappingPaths finds all files in candidateFiles that overlap with any file in activeFiles.
func OverlappingPaths(candidateFiles, activeFiles []string) []string {
	var overlaps []string
	seen := make(map[string]bool)
	for _, c := range candidateFiles {
		cNorm := NormalizeGoalPath(c)
		if cNorm == "" {
			continue
		}
		for _, a := range activeFiles {
			aNorm := NormalizeGoalPath(a)
			if aNorm == "" {
				continue
			}
			if PathsOverlap(cNorm, aNorm) {
				if !seen[cNorm] {
					seen[cNorm] = true
					overlaps = append(overlaps, cNorm)
				}
			}
		}
	}
	return overlaps
}

// CheckStale performs multi-tier heuristic staleness evaluation on a claim.
// Returns (isStale, reason, age).
func (c *GoalClaim) CheckStale() (bool, string, time.Duration) {
	if c == nil {
		return false, "", 0
	}

	tRef := c.UpdatedAt
	if tRef.IsZero() {
		tRef = c.ClaimedAt
	}

	var age time.Duration
	if !tRef.IsZero() {
		age = time.Since(tRef).Round(time.Second)
		c.StaleAge = age.String()
	}

	currHost, _ := os.Hostname()
	isLocalHost := c.Hostname == "" || c.Hostname == currHost

	// Tier 1: Local PID liveness with a 15-minute claim grace period.
	// If the recorded process has exited, allow a grace window (15m) from the last
	// heartbeat/claim update before declaring the claim dead/stale. This avoids
	// false-positives when CLI commands exit immediately after claim/report.
	const DeadPIDGracePeriod = 15 * time.Minute
	if isLocalHost && c.PID > 0 {
		if !isProcessAlive(c.PID) {
			if !tRef.IsZero() && time.Since(tRef) > DeadPIDGracePeriod {
				c.Stale = true
				c.StaleReason = fmt.Sprintf("process %d is no longer running", c.PID)
				return true, c.StaleReason, age
			}
		} else if isProcessStopped(c.PID) {
			// Tier 2: Stopped / Paused process
			c.Stale = true
			c.StaleReason = fmt.Sprintf("process %d is stopped/paused", c.PID)
			return true, c.StaleReason, age
		}
	}

	// Tier 3: Inactivity timeout (> 2 hours without heartbeat update)
	if !tRef.IsZero() && time.Since(tRef) > DefaultClaimInactivityTimeout {
		c.Stale = true
		c.StaleReason = fmt.Sprintf("inactive for >%s", time.Since(tRef).Round(time.Minute))
		return true, c.StaleReason, age
	}

	c.Stale = false
	c.StaleReason = ""
	return false, "", age
}

// AtomicWriteFile safely writes data to filename using a temporary file in the
// same directory, followed by fsync, close, and atomic os.Rename.
func AtomicWriteFile(filename string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(filename)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create directory for %s: %w", filename, err)
	}

	base := filepath.Base(filename)
	tmpFile, err := os.CreateTemp(dir, fmt.Sprintf(".%s-*.tmp", base))
	if err != nil {
		return fmt.Errorf("create temp file for %s: %w", filename, err)
	}
	tmpName := tmpFile.Name()
	success := false
	defer func() {
		if !success {
			_ = tmpFile.Close()
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmpFile.Chmod(perm); err != nil {
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if _, err := tmpFile.Write(data); err != nil {
		return fmt.Errorf("write data to temp file: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, filename); err != nil {
		return fmt.Errorf("atomic rename temp file to %s: %w", filename, err)
	}
	success = true
	return nil
}

// lockInfo holds metadata about an active goal claim lock file.
type lockInfo struct {
	PID       int       `json:"pid"`
	Hostname  string    `json:"hostname,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	Token     string    `json:"token,omitempty"`
}

// acquireGoalLock acquires mutual exclusion for a specific goal directory
// using a sentinel `.claim.lock` file with retry backoff and stale lock eviction.
func acquireGoalLock(goalDir string) (func(), error) {
	return acquireGoalLockWithTimeout(goalDir, DefaultLockAcquisitionTimeout)
}

// acquireGoalLockWithTimeout attempts to acquire the lock within the specified
// timeout. It uses an advisory flock where the platform supports it, so a
// crashed holder cannot leave a lock behind and two contenders can never both
// win. Platforms without flock fall back to an O_EXCL sentinel file with
// stale-lock eviction.
func acquireGoalLockWithTimeout(goalDir string, timeout time.Duration) (func(), error) {
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		return nil, fmt.Errorf("create goal dir %s: %w", goalDir, err)
	}
	if flockSupported {
		return acquireFlockLock(goalDir, timeout)
	}
	return acquireSentinelLock(goalDir, timeout)
}

// AcquireMultiGoalLocks acquires mutual exclusion locks on multiple goals
// strictly in lexicographical order by goal slug (L(u) <_L L(v) <=> slug(u) <_lex slug(v))
// to guarantee zero deadlock across concurrent multi-goal operations.
func AcquireMultiGoalLocks(rootDir string, slugs []string, timeout time.Duration) (func(), error) {
	if len(slugs) == 0 {
		return func() {}, nil
	}

	seen := make(map[string]bool)
	var uniqueSlugs []string
	for _, s := range slugs {
		s = filepath.Clean(strings.TrimSpace(s))
		if s != "" && !seen[s] {
			seen[s] = true
			uniqueSlugs = append(uniqueSlugs, s)
		}
	}

	sort.Strings(uniqueSlugs)

	var unlocks []func()
	cleanup := func() {
		for i := len(unlocks) - 1; i >= 0; i-- {
			if unlocks[i] != nil {
				unlocks[i]()
			}
		}
	}

	rootDir = resolveGoalsRootDir(rootDir)
	for _, slug := range uniqueSlugs {
		goalDir := filepath.Join(rootDir, "goals", slug)
		unlock, err := acquireGoalLockWithTimeout(goalDir, timeout)
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("acquire goal lock for %s: %w", slug, err)
		}
		unlocks = append(unlocks, unlock)
	}

	return cleanup, nil
}

func acquireFlockLock(goalDir string, timeout time.Duration) (func(), error) {
	lockFile := filepath.Join(goalDir, ".claim.lock")
	unlock, _, err := acquireFlockFile(lockFile, timeout)
	return unlock, err
}

// acquireFlockFile serializes contenders with a non-blocking advisory flock on
// lockFile. The kernel releases the flock when the holder exits, so stale
// eviction is unnecessary. The file still carries holder metadata for
// diagnostics. After taking the flock it verifies the locked inode is still
// the one at the path, because a previous holder removes the file on release.
func acquireFlockFile(lockFile string, timeout time.Duration) (func(), string, error) {
	infoBytes, token, err := newLockInfo()
	if err != nil {
		return nil, "", err
	}

	deadline := time.Now().Add(timeout)
	for {
		f, err := os.OpenFile(lockFile, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return nil, "", fmt.Errorf("open lock file %s: %w", lockFile, err)
		}
		if err := tryLockFile(f); err != nil {
			_ = f.Close()
			if !errors.Is(err, errLockBusy) {
				return nil, "", fmt.Errorf("lock %s: %w", lockFile, err)
			}
		} else {
			fi, ferr := f.Stat()
			pi, perr := os.Stat(lockFile)
			if ferr == nil && perr == nil && os.SameFile(fi, pi) {
				if _, err := f.Seek(0, 0); err == nil {
					_ = f.Truncate(0)
					if _, werr := f.Write(infoBytes); werr != nil {
						unlockFile(f)
						_ = f.Close()
						return nil, "", werr
					}
					_ = f.Sync()
				}
				unlock := func() {
					data, readErr := os.ReadFile(lockFile)
					if readErr == nil {
						var current lockInfo
						if json.Unmarshal(data, &current) == nil {
							if current.Token == token && current.PID == os.Getpid() {
								_ = os.Remove(lockFile)
							}
						} else {
							_ = os.Remove(lockFile)
						}
					}
					unlockFile(f)
					_ = f.Close()
				}
				return unlock, token, nil
			}
			// The path was replaced while we waited, so our flock is on a dead
			// inode. Drop it and race for the live file.
			unlockFile(f)
			_ = f.Close()
		}

		if time.Now().After(deadline) {
			return nil, "", fmt.Errorf("acquire lock on %s timed out after %v", filepath.Base(lockFile), timeout)
		}
		time.Sleep(time.Duration(5+mrand.IntN(20)) * time.Millisecond)
	}
}

// newLockInfo builds the holder metadata and ownership token written into
// .claim.lock.
func newLockInfo() ([]byte, string, error) {
	hostname, _ := os.Hostname()
	token := uuid.New().String()
	info := lockInfo{PID: os.Getpid(), Hostname: hostname, CreatedAt: time.Now().UTC(), Token: token}
	b, err := json.Marshal(info)
	if err != nil {
		return nil, "", err
	}
	return b, token, nil
}

// acquireSentinelLock is the portable fallback: it creates .claim.lock with
// O_EXCL and evicts a lock whose holder died or whose age passed the
// threshold. Its check-then-remove eviction can race under high contention,
// which is why it is only used on platforms without advisory locks.
func acquireSentinelLock(goalDir string, timeout time.Duration) (func(), error) {
	lockFile := filepath.Join(goalDir, ".claim.lock")
	hostname, _ := os.Hostname()
	token := uuid.New().String()

	info := lockInfo{
		PID:       os.Getpid(),
		Hostname:  hostname,
		CreatedAt: time.Now().UTC(),
		Token:     token,
	}
	infoBytes, err := json.Marshal(info)
	if err != nil {
		return nil, err
	}

	deadline := time.Now().Add(timeout)
	var lastHolderPID int
	var lastHolderHost string

	for {
		f, err := os.OpenFile(lockFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err == nil {
			// Successfully created lock file
			_, writeErr := f.Write(infoBytes)
			_ = f.Sync()
			_ = f.Close()
			if writeErr != nil {
				_ = os.Remove(lockFile)
				return nil, writeErr
			}

			unlock := func() {
				data, readErr := os.ReadFile(lockFile)
				if readErr == nil {
					var current lockInfo
					if json.Unmarshal(data, &current) == nil {
						if current.Token == token && current.PID == os.Getpid() {
							_ = os.Remove(lockFile)
						}
					} else {
						_ = os.Remove(lockFile)
					}
				}
			}
			return unlock, nil
		}

		if !os.IsExist(err) {
			return nil, fmt.Errorf("open lock file %s: %w", lockFile, err)
		}

		// Lock exists: check if stale
		data, readErr := os.ReadFile(lockFile)
		if readErr == nil {
			var existing lockInfo
			if json.Unmarshal(data, &existing) == nil && existing.PID > 0 {
				lastHolderPID = existing.PID
				lastHolderHost = existing.Hostname

				isLocal := existing.Hostname == "" || existing.Hostname == hostname
				if isLocal && existing.PID != os.Getpid() {
					if !isProcessAlive(existing.PID) || isProcessStopped(existing.PID) {
						// Dead or stopped process left lockfile behind: safely evict
						evictStaleLock(lockFile, existing.Token)
						continue
					}
				}

				if !existing.CreatedAt.IsZero() && time.Since(existing.CreatedAt) > LockStaleThreshold {
					evictStaleLock(lockFile, existing.Token)
					continue
				}
			} else {
				// Corrupted, unparseable, 0-byte, or invalid lockfile: check modification time
				if fi, statErr := os.Stat(lockFile); statErr == nil && time.Since(fi.ModTime()) > LockStaleThreshold {
					if latestData, err := os.ReadFile(lockFile); err == nil {
						var validLock lockInfo
						if json.Unmarshal(latestData, &validLock) == nil && validLock.PID > 0 && time.Since(validLock.CreatedAt) < LockStaleThreshold {
							continue // Lock was already replaced with a fresh valid lock!
						}
					}
					_ = os.Remove(lockFile)
					continue
				}
			}
		} else if os.IsNotExist(readErr) {
			// Lock file disappeared in between; retry immediately
			continue
		} else {
			// Unreadable or corrupted lockfile; check modification time
			if fi, statErr := os.Stat(lockFile); statErr == nil && time.Since(fi.ModTime()) > LockStaleThreshold {
				if latestData, err := os.ReadFile(lockFile); err == nil {
					var validLock lockInfo
					if json.Unmarshal(latestData, &validLock) == nil && validLock.PID > 0 && time.Since(validLock.CreatedAt) < LockStaleThreshold {
						continue // Lock was already replaced with a fresh valid lock!
					}
				}
				_ = os.Remove(lockFile)
				continue
			}
		}

		if time.Now().After(deadline) {
			if lastHolderPID > 0 {
				return nil, fmt.Errorf("acquire goal lock on %s timed out after %v (held by PID %d on %s)", filepath.Base(goalDir), timeout, lastHolderPID, lastHolderHost)
			}
			return nil, fmt.Errorf("acquire goal lock on %s timed out after %v", filepath.Base(goalDir), timeout)
		}

		// Jittered backoff between 5ms and 25ms
		sleepDur := time.Duration(5+mrand.IntN(20)) * time.Millisecond
		time.Sleep(sleepDur)
	}
}

func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
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

func isProcessStopped(pid int) bool {
	if pid <= 0 {
		return false
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	idx := bytes.LastIndexByte(data, ')')
	if idx < 0 || idx+2 >= len(data) {
		return false
	}
	state := data[idx+2]
	return state == 'T' || state == 't' || state == 'Z'
}

func evictStaleLock(lockFile, expectedToken string) {
	data, err := os.ReadFile(lockFile)
	if err != nil {
		return
	}
	var cur lockInfo
	if err := json.Unmarshal(data, &cur); err == nil {
		if expectedToken != "" && cur.Token != expectedToken {
			return // Lock was already replaced by another contender
		}
		_ = os.Remove(lockFile)
	} else {
		// 0-byte or corrupted lock: only evict if older than threshold
		if fi, statErr := os.Stat(lockFile); statErr == nil && time.Since(fi.ModTime()) > LockStaleThreshold {
			_ = os.Remove(lockFile)
		}
	}
}

// SessionBoundGoal returns the slug of the goal whose claim carries the given
// session id, or an empty string when no goal is bound to it.
func SessionBoundGoal(rootDir, session string) string {
	session = strings.TrimSpace(session)
	if session == "" {
		return ""
	}
	rootDir = resolveGoalsRootDir(rootDir)
	entries, err := os.ReadDir(filepath.Join(rootDir, "goals"))
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		claimPath := filepath.Join(rootDir, "goals", e.Name(), "claim.json")
		if !fileExists(claimPath) {
			continue
		}
		claim, err := readClaim(claimPath)
		if err != nil || claim == nil {
			continue
		}
		if claim.SessionID == session {
			return e.Name()
		}
	}
	return ""
}

// clearSessionBinding removes the session binding from a goal's claim while
// leaving the claim itself in place.
func clearSessionBinding(rootDir, slug string) error {
	rootDir = resolveGoalsRootDir(rootDir)
	goalDir := filepath.Join(rootDir, "goals", slug)
	claimPath := filepath.Join(goalDir, "claim.json")
	unlock, err := acquireGoalLock(goalDir)
	if err != nil {
		return fmt.Errorf("acquire goal lock: %w", err)
	}
	defer unlock()

	claim, err := readClaim(claimPath)
	if err != nil {
		return fmt.Errorf("read claim %s: %w", claimPath, err)
	}
	if claim.SessionID == "" {
		return nil
	}
	claim.SessionID = ""
	claim.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(claim, "", "  ")
	if err != nil {
		return err
	}
	return AtomicWriteFile(claimPath, data, 0644)
}

// FocusGoal deterministically binds an agent session to a goal and returns the
// resulting claim. It claims the goal when free, refreshes the binding when the
// same agent already holds it, and refuses when another agent holds it. A
// session already bound to a different goal requires force, which clears the
// old binding before moving it.
func FocusGoal(rootDir, slug, agent, note, session string, force bool) (*GoalClaim, error) {
	session = strings.TrimSpace(session)
	if session == "" {
		return nil, fmt.Errorf("session id is required (pass --session=<id>)")
	}
	slug = filepath.Clean(slug)
	rootDir = resolveGoalsRootDir(rootDir)
	if !fileExists(filepath.Join(rootDir, "goals", slug, "goal.md")) {
		return nil, fmt.Errorf("goal package %q does not exist in goals/%s", slug, slug)
	}
	if bound := SessionBoundGoal(rootDir, session); bound != "" && bound != slug {
		if !force {
			return nil, fmt.Errorf("session %q is already focused on goal %q (use --force to move the binding)", session, bound)
		}
		if err := clearSessionBinding(rootDir, bound); err != nil {
			return nil, fmt.Errorf("clear session binding on %q: %w", bound, err)
		}
	}
	if strings.TrimSpace(agent) == "" {
		claimPath := filepath.Join(rootDir, "goals", slug, "claim.json")
		if existing, err := readClaim(claimPath); err == nil && existing != nil && !existing.Stale {
			if existing.SessionID == "" || existing.SessionID == session {
				if existing.ClaimedBy != "" {
					agent = existing.ClaimedBy
				} else if existing.Agent != "" {
					agent = existing.Agent
				}
			}
		}
	}
	cl, _, err := claimGoal(rootDir, slug, agent, note, session, force)
	return cl, err
}

// RecordClaimIteration records a test or progress attempt against an active claim.
// It enforces the Autonomous Sprint Circuit Breaker:
// If IterationCount exceeds contract.MaxRounds (default: 5) or if the same compiler/test error
// occurs 3 consecutive times, it trips the circuit breaker, marks the claim as
// "halted_circuit_breaker", releases the claim lock (.claim.lock), and returns tripped=true.
func RecordClaimIteration(rootDir, slug string, passed bool, failureReason string) (*GoalClaim, bool, error) {
	if slug == "" {
		return nil, false, fmt.Errorf("missing goal slug")
	}
	cleanRoot := resolveGoalsRootDir(rootDir)
	goalDir := filepath.Join(cleanRoot, "goals", slug)
	claimPath := filepath.Join(goalDir, "claim.json")

	data, err := os.ReadFile(claimPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, fmt.Errorf("no active claim found for goal %q", slug)
		}
		return nil, false, fmt.Errorf("read claim.json: %w", err)
	}

	var claim GoalClaim
	if err := json.Unmarshal(data, &claim); err != nil {
		return nil, false, fmt.Errorf("parse claim.json: %w", err)
	}

	claim.IterationCount++
	claim.UpdatedAt = time.Now().UTC()

	maxRounds := DefaultContractMaxRounds
	if c, cErr := LoadContract(cleanRoot, slug); cErr == nil && c != nil && c.MaxRounds > 0 {
		maxRounds = c.MaxRounds
	}

	tripped := false
	if passed {
		claim.ConsecutiveFailures = 0
		claim.LastFailureError = ""
	} else {
		trimmedFail := strings.TrimSpace(failureReason)
		if trimmedFail != "" && claim.LastFailureError == trimmedFail {
			claim.ConsecutiveFailures++
		} else {
			claim.ConsecutiveFailures = 1
			claim.LastFailureError = trimmedFail
		}

		if claim.IterationCount >= maxRounds {
			tripped = true
			claim.Status = "halted_circuit_breaker"
			claim.Stale = true
			claim.StaleReason = fmt.Sprintf("circuit breaker tripped: exceeded maximum iterations (%d/%d)", claim.IterationCount, maxRounds)
		} else if claim.ConsecutiveFailures >= 3 {
			tripped = true
			claim.Status = "halted_circuit_breaker"
			claim.Stale = true
			claim.StaleReason = fmt.Sprintf("circuit breaker tripped: repeated identical failure 3 times: %s", claim.LastFailureError)
		}
	}

	claimBytes, mErr := json.MarshalIndent(&claim, "", "  ")
	if mErr != nil {
		return nil, false, fmt.Errorf("marshal claim.json: %w", mErr)
	}
	if err := AtomicWriteFile(claimPath, claimBytes, 0644); err != nil {
		return nil, false, fmt.Errorf("write claim.json: %w", err)
	}

	if tripped {
		// Automatically release advisory claim lock and charter to prevent runaway agent loops
		lockPath := filepath.Join(goalDir, ".claim.lock")
		_ = os.Remove(lockPath)
		_ = RemoveCharter(cleanRoot, slug)
	}

	return &claim, tripped, nil
}

// SetGoalStopAtTask records or clears the stop-at-task checkpoint lease on the
// active claim. An empty stopAtTaskID clears the lease.
func SetGoalStopAtTask(rootDir, slug, stopAtTaskID string) (*GoalClaim, error) {
	if strings.TrimSpace(slug) == "" {
		return nil, fmt.Errorf("missing goal slug")
	}
	cleanRoot := resolveGoalsRootDir(rootDir)
	claim, err := LoadGoalClaim(cleanRoot, slug)
	if err != nil {
		return nil, err
	}
	if claim == nil {
		return nil, fmt.Errorf("goal %q has no active claim", slug)
	}
	claim.StopAtTaskID = strings.TrimSpace(stopAtTaskID)
	claim.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(claim, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal claim.json: %w", err)
	}
	claimPath := filepath.Join(cleanRoot, "goals", slug, "claim.json")
	if err := AtomicWriteFile(claimPath, data, 0644); err != nil {
		return nil, fmt.Errorf("write claim.json: %w", err)
	}
	return claim, nil
}
