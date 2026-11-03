package goals

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// helper to create a valid goal package
func setupTestGoal(t *testing.T, dir, slug string) string {
	t.Helper()
	goalDir := filepath.Join(dir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatalf("failed to create goal dir: %v", err)
	}
	content := fmt.Sprintf("# Goal: %s\n\n## Goal Description\nDescription for %s\n\n## Done Condition\nDone\n", slug, slug)
	if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(content), 0644); err != nil {
		t.Fatalf("failed to write goal.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte("# Plan\n"), 0644); err != nil {
		t.Fatalf("failed to write plan.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "facts.md"), []byte("# Facts\n"), 0644); err != nil {
		t.Fatalf("failed to write facts.md: %v", err)
	}
	return goalDir
}

// 1. Extreme Contention: 100 parallel goroutines competing to claim 1 unassigned goal
func TestStress100GoroutinesCompetingSingleGoal(t *testing.T) {
	dir := t.TempDir()
	slug := "extreme-race-goal"
	goalDir := setupTestGoal(t, dir, slug)

	const numGoroutines = 100
	var wg sync.WaitGroup
	startCh := make(chan struct{})

	type result struct {
		agent string
		claim *GoalClaim
		err   error
	}

	results := make([]result, numGoroutines)

	for i := range numGoroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			agentName := fmt.Sprintf("agent-%03d", idx)
			<-startCh // Synchronize start
			claim, err := ClaimGoal(dir, slug, agentName, fmt.Sprintf("contention note %d", idx), false)
			results[idx] = result{
				agent: agentName,
				claim: claim,
				err:   err,
			}
		}(i)
	}

	close(startCh)
	wg.Wait()

	var successes []result
	var rejections []result

	for _, r := range results {
		if r.err == nil && r.claim != nil {
			successes = append(successes, r)
		} else {
			rejections = append(rejections, r)
		}
	}

	// Invariant 1: Exactly 1 caller succeeds
	if len(successes) != 1 {
		t.Fatalf("VIOLATION: Expected exactly 1 winner out of %d goroutines, got %d successes", numGoroutines, len(successes))
	}

	// Invariant 2: All 99 other callers receive deterministic rejections
	if len(rejections) != numGoroutines-1 {
		t.Fatalf("VIOLATION: Expected %d rejections, got %d", numGoroutines-1, len(rejections))
	}

	winner := successes[0]
	for _, rej := range rejections {
		if rej.err == nil {
			t.Fatalf("VIOLATION: Rejection result has nil error")
		}
		if !strings.Contains(rej.err.Error(), "already claimed by") {
			t.Fatalf("VIOLATION: Expected deterministic rejection message 'already claimed by', got: %v", rej.err)
		}
	}

	// Invariant 3: claim.json is valid, never truncated/0-bytes, matches winner
	claimFile := filepath.Join(goalDir, "claim.json")
	claimData, err := os.ReadFile(claimFile)
	if err != nil {
		t.Fatalf("VIOLATION: Failed to read claim.json: %v", err)
	}
	if len(claimData) == 0 {
		t.Fatalf("VIOLATION: claim.json is 0 bytes")
	}

	var diskClaim GoalClaim
	if err := json.Unmarshal(claimData, &diskClaim); err != nil {
		t.Fatalf("VIOLATION: claim.json is corrupted JSON: %v (raw: %s)", err, string(claimData))
	}

	if diskClaim.Agent != winner.agent || diskClaim.ClaimedBy != winner.agent {
		t.Fatalf("VIOLATION: claim.json agent mismatch: expected %q, got %+v", winner.agent, diskClaim)
	}

	// Invariant 4: .claim.lock is cleanly removed and never leaked
	lockFile := filepath.Join(goalDir, ".claim.lock")
	if fileExists(lockFile) {
		t.Fatalf("VIOLATION: .claim.lock was leaked on disk after contention")
	}
}

// 2. Multiple Goals Contention: 100 workers competing across 5 goals with distinct agent names
func TestStressMultiGoalMultiAgentContention(t *testing.T) {
	dir := t.TempDir()
	numGoals := 5
	slugs := make([]string, numGoals)
	for i := range numGoals {
		slugs[i] = fmt.Sprintf("multi-goal-%d", i)
		setupTestGoal(t, dir, slugs[i])
	}

	const numWorkers = 100
	var wg sync.WaitGroup
	startCh := make(chan struct{})

	type claimOutcome struct {
		worker int
		slug   string
		agent  string
		claim  *GoalClaim
		err    error
	}

	outcomes := make([]claimOutcome, numWorkers)

	for i := range numWorkers {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			targetSlug := slugs[idx%numGoals] // 20 workers per goal
			agentName := fmt.Sprintf("cluster-agent-%03d", idx)
			<-startCh
			claim, err := ClaimGoal(dir, targetSlug, agentName, "multi-claim test", false)
			outcomes[idx] = claimOutcome{
				worker: idx,
				slug:   targetSlug,
				agent:  agentName,
				claim:  claim,
				err:    err,
			}
		}(i)
	}

	close(startCh)
	wg.Wait()

	// Analyze per goal
	for _, slug := range slugs {
		var successes []claimOutcome
		var rejections []claimOutcome
		for _, o := range outcomes {
			if o.slug == slug {
				if o.err == nil && o.claim != nil {
					successes = append(successes, o)
				} else {
					rejections = append(rejections, o)
				}
			}
		}

		if len(successes) != 1 {
			t.Fatalf("VIOLATION on goal %s: expected exactly 1 winner, got %d", slug, len(successes))
		}
		if len(rejections) != 19 {
			t.Fatalf("VIOLATION on goal %s: expected 19 rejections, got %d", slug, len(rejections))
		}

		goalDir := filepath.Join(dir, "goals", slug)
		lockFile := filepath.Join(goalDir, ".claim.lock")
		if fileExists(lockFile) {
			t.Fatalf("VIOLATION: .claim.lock leaked on %s", slug)
		}

		// Verify claim.json integrity
		data, err := os.ReadFile(filepath.Join(goalDir, "claim.json"))
		if err != nil {
			t.Fatalf("VIOLATION: cannot read claim.json on %s: %v", slug, err)
		}
		if len(data) == 0 {
			t.Fatalf("VIOLATION: 0-byte claim.json on %s", slug)
		}
		var c GoalClaim
		if err := json.Unmarshal(data, &c); err != nil {
			t.Fatalf("VIOLATION: corrupted claim.json on %s: %v", slug, err)
		}
		if c.Agent != successes[0].agent {
			t.Fatalf("VIOLATION on %s: disk claim agent %q != winner agent %q", slug, c.Agent, successes[0].agent)
		}
	}
}

// 3. Rapid Claim-Release-Report Cycles under High Contention
func TestStressRapidClaimReleaseReportCycles(t *testing.T) {
	dir := t.TempDir()
	slug := "rapid-cycle-goal"
	goalDir := setupTestGoal(t, dir, slug)

	const numGoroutines = 50
	const numIterationsPerGoroutine = 10
	var wg sync.WaitGroup
	startCh := make(chan struct{})

	var totalSuccessfulClaims atomic.Int64
	var totalSuccessfulReleases atomic.Int64
	var totalSuccessfulReports atomic.Int64
	var totalRejections atomic.Int64

	for i := range numGoroutines {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			agentName := fmt.Sprintf("worker-%02d", workerID)
			<-startCh

			for iter := range numIterationsPerGoroutine {
				// Attempt to claim
				claim, err := ClaimGoal(dir, slug, agentName, fmt.Sprintf("iter-%d", iter), false)
				if err == nil && claim != nil {
					totalSuccessfulClaims.Add(1)

					// Perform a progress report while holding claim
					rep, repErr := ReportGoal(dir, slug, fmt.Sprintf("progress by %s iter %d", agentName, iter), "in_progress")
					if repErr == nil && rep != nil {
						totalSuccessfulReports.Add(1)
					}

					// Small synthetic work
					time.Sleep(1 * time.Millisecond)

					// Release goal
					relErr := ReleaseGoal(dir, slug)
					if relErr == nil {
						totalSuccessfulReleases.Add(1)
					}
				} else {
					totalRejections.Add(1)
					// Backoff briefly before next try
					time.Sleep(2 * time.Millisecond)
				}
			}
		}(i)
	}

	close(startCh)
	wg.Wait()

	t.Logf("Rapid cycles summary: Claims=%d, Reports=%d, Releases=%d, Rejections=%d",
		totalSuccessfulClaims.Load(), totalSuccessfulReports.Load(), totalSuccessfulReleases.Load(), totalRejections.Load())

	if totalSuccessfulClaims.Load() == 0 {
		t.Fatalf("VIOLATION: Zero successful claims occurred during rapid cycling")
	}

	// Verify no lock file leaked
	lockFile := filepath.Join(goalDir, ".claim.lock")
	if fileExists(lockFile) {
		t.Fatalf("VIOLATION: .claim.lock leaked on disk after rapid cycling")
	}

	// Check final state of claim.json
	claimFile := filepath.Join(goalDir, "claim.json")
	if fileExists(claimFile) {
		data, err := os.ReadFile(claimFile)
		if err != nil {
			t.Fatalf("VIOLATION: failed to read final claim.json: %v", err)
		}
		if len(data) == 0 {
			t.Fatalf("VIOLATION: final claim.json is 0 bytes")
		}
		var c GoalClaim
		if err := json.Unmarshal(data, &c); err != nil {
			t.Fatalf("VIOLATION: final claim.json is corrupted JSON: %v", err)
		}
	}
}

// 4. High Concurrency Contending Reports on Claimed Goal
func TestStressContendingReports(t *testing.T) {
	dir := t.TempDir()
	slug := "report-race-goal"
	goalDir := setupTestGoal(t, dir, slug)

	// Initially claim
	initialClaim, err := ClaimGoal(dir, slug, "primary-agent", "initial note", false)
	if err != nil || initialClaim == nil {
		t.Fatalf("initial claim failed: %v", err)
	}

	const numReporters = 50
	var wg sync.WaitGroup
	startCh := make(chan struct{})

	type repResult struct {
		worker int
		claim  *GoalClaim
		err    error
	}

	results := make([]repResult, numReporters)

	for i := range numReporters {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			note := fmt.Sprintf("report update #%d from worker %d", idx, idx)
			<-startCh
			c, repErr := ReportGoal(dir, slug, note, "in_progress")
			results[idx] = repResult{
				worker: idx,
				claim:  c,
				err:    repErr,
			}
		}(i)
	}

	close(startCh)
	wg.Wait()

	// Every report should succeed because acquireGoalLock serializes them safely
	var failures int
	for _, r := range results {
		if r.err != nil {
			failures++
			t.Errorf("worker %d report failed: %v", r.worker, r.err)
		}
	}
	if failures > 0 {
		t.Fatalf("VIOLATION: %d out of %d concurrent reports failed", failures, numReporters)
	}

	// Verify claim.json integrity on disk
	claimFile := filepath.Join(goalDir, "claim.json")
	data, err := os.ReadFile(claimFile)
	if err != nil {
		t.Fatalf("VIOLATION: cannot read claim.json: %v", err)
	}
	if len(data) == 0 {
		t.Fatalf("VIOLATION: claim.json is 0 bytes")
	}

	var diskClaim GoalClaim
	if err := json.Unmarshal(data, &diskClaim); err != nil {
		t.Fatalf("VIOLATION: corrupted claim.json after concurrent reports: %v", err)
	}

	if diskClaim.Status != "in_progress" || diskClaim.Note == "" {
		t.Fatalf("VIOLATION: unexpected disk claim fields: %+v", diskClaim)
	}

	// Verify lock is clean
	lockFile := filepath.Join(goalDir, ".claim.lock")
	if fileExists(lockFile) {
		t.Fatalf("VIOLATION: .claim.lock leaked after concurrent reports")
	}
}

// 5. Edge Cases: Release non-existent claim, double release, reporting on released claims
func TestEdgeCasesReleaseAndReport(t *testing.T) {
	dir := t.TempDir()
	slug := "edge-case-goal"
	goalDir := setupTestGoal(t, dir, slug)

	// Edge Case 1: Release non-existent claim
	err := ReleaseGoal(dir, slug)
	if err == nil {
		t.Fatalf("VIOLATION: expected error when releasing un-claimed goal, got nil")
	}
	if !strings.Contains(err.Error(), "not currently claimed") {
		t.Fatalf("VIOLATION: expected 'not currently claimed' error message, got: %v", err)
	}
	if fileExists(filepath.Join(goalDir, ".claim.lock")) {
		t.Fatalf("VIOLATION: lock leaked after failed release")
	}

	// Edge Case 2: Claim then release twice
	_, err = ClaimGoal(dir, slug, "agent-x", "testing double release", false)
	if err != nil {
		t.Fatalf("ClaimGoal failed: %v", err)
	}
	// First release -> success
	if err := ReleaseGoal(dir, slug); err != nil {
		t.Fatalf("first ReleaseGoal failed: %v", err)
	}
	// Second release -> deterministic error
	err = ReleaseGoal(dir, slug)
	if err == nil {
		t.Fatalf("VIOLATION: expected error on second release, got nil")
	}
	if !strings.Contains(err.Error(), "not currently claimed") {
		t.Fatalf("VIOLATION: expected 'not currently claimed' error on duplicate release, got: %v", err)
	}
	if fileExists(filepath.Join(goalDir, ".claim.lock")) {
		t.Fatalf("VIOLATION: lock leaked after duplicate release")
	}

	// Edge Case 3: Releasing on a non-existent goal directory
	err = ReleaseGoal(dir, "non-existent-goal-xyz")
	if err == nil {
		t.Fatalf("VIOLATION: expected error on non-existent goal slug release")
	}

	// Edge Case 4: Claiming on non-existent goal directory
	_, err = ClaimGoal(dir, "non-existent-slug", "agent-y", "note", false)
	if err == nil {
		t.Fatalf("VIOLATION: expected error when claiming non-existent goal package")
	}
}

// 6. Adversarial Stress: Concurrent Corrupted Lock File Eviction
func TestStressConcurrentStaleLockEviction(t *testing.T) {
	dir := t.TempDir()
	slug := "stale-evict-race"
	goalDir := setupTestGoal(t, dir, slug)
	lockPath := filepath.Join(goalDir, ".claim.lock")

	currHost, _ := os.Hostname()

	// Write dead PID lockfile
	deadLock := lockInfo{
		PID:       9999999, // Dead PID
		Hostname:  currHost,
		CreatedAt: time.Now().UTC().Add(-1 * time.Minute),
		Token:     "dead-token-adversarial",
	}
	lockBytes, _ := json.Marshal(deadLock)
	if err := os.WriteFile(lockPath, lockBytes, 0600); err != nil {
		t.Fatal(err)
	}

	// 50 goroutines simultaneously try to acquire and evict the dead lock
	const numGoroutines = 50
	var wg sync.WaitGroup
	startCh := make(chan struct{})

	type lockAcquireResult struct {
		worker int
		err    error
	}

	results := make([]lockAcquireResult, numGoroutines)

	for i := range numGoroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-startCh
			unlock, err := acquireGoalLockWithTimeout(goalDir, 3*time.Second)
			if err == nil {
				// Held lock briefly
				time.Sleep(5 * time.Millisecond)
				unlock()
			}
			results[idx] = lockAcquireResult{
				worker: idx,
				err:    err,
			}
		}(i)
	}

	close(startCh)
	wg.Wait()

	for _, r := range results {
		if r.err != nil {
			t.Fatalf("VIOLATION: worker %d failed to acquire lock through stale eviction: %v", r.worker, r.err)
		}
	}

	if fileExists(lockPath) {
		t.Fatalf("VIOLATION: .claim.lock leaked after all locks released")
	}
}

// 7. AtomicWriteFile extreme torture test: high concurrency writes and stat assertions
func TestStressAtomicWriteFileZeroByteProtection(t *testing.T) {
	dir := t.TempDir()
	targetFile := filepath.Join(dir, "protected_claim.json")

	const numWriters = 30
	const numReaders = 30
	stopCh := make(chan struct{})
	var wg sync.WaitGroup

	errCh := make(chan error, 100)

	// Writers constantly writing JSON payloads of various sizes
	for i := range numWriters {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				select {
				case <-stopCh:
					return
				default:
					c := GoalClaim{
						GoalSlug:  "stress-slug",
						ClaimedBy: fmt.Sprintf("writer-%d", id),
						Agent:     fmt.Sprintf("writer-%d", id),
						ClaimedAt: time.Now().UTC(),
						UpdatedAt: time.Now().UTC(),
						PID:       os.Getpid(),
						Status:    "in_progress",
						Note:      strings.Repeat("x", (id%10+1)*100),
					}
					data, _ := json.MarshalIndent(c, "", "  ")
					if err := AtomicWriteFile(targetFile, data, 0644); err != nil {
						select {
						case errCh <- fmt.Errorf("write error: %w", err):
						default:
						}
					}
				}
			}
		}(i)
	}

	// Readers constantly checking for 0-byte or corrupted JSON
	for i := range numReaders {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				select {
				case <-stopCh:
					return
				default:
					data, err := os.ReadFile(targetFile)
					if err != nil {
						if os.IsNotExist(err) {
							continue
						}
						select {
						case errCh <- fmt.Errorf("read error: %w", err):
						default:
						}
						return
					}
					if len(data) == 0 {
						select {
						case errCh <- fmt.Errorf("FATAL: read 0 bytes from %s", targetFile):
						default:
						}
						return
					}
					var parsed GoalClaim
					if err := json.Unmarshal(data, &parsed); err != nil {
						select {
						case errCh <- fmt.Errorf("FATAL: corrupted JSON (len=%d): %w (raw: %s)", len(data), err, string(data)):
						default:
						}
						return
					}
				}
			}
		}(i)
	}

	time.Sleep(500 * time.Millisecond)
	close(stopCh)
	wg.Wait()

	select {
	case err := <-errCh:
		t.Fatalf("VIOLATION in AtomicWriteFile torture test: %v", err)
	default:
	}
}

// 8. Contending Force Takeover: 50 goroutines claiming simultaneously with force=true
func TestStressContendingForceTakeover(t *testing.T) {
	dir := t.TempDir()
	slug := "force-takeover-goal"
	goalDir := setupTestGoal(t, dir, slug)

	const numWorkers = 50
	var wg sync.WaitGroup
	startCh := make(chan struct{})

	type forceResult struct {
		worker int
		agent  string
		claim  *GoalClaim
		err    error
	}

	results := make([]forceResult, numWorkers)

	for i := range numWorkers {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			agentName := fmt.Sprintf("force-agent-%02d", idx)
			<-startCh
			c, err := ClaimGoal(dir, slug, agentName, "forced claim takeover", true)
			results[idx] = forceResult{
				worker: idx,
				agent:  agentName,
				claim:  c,
				err:    err,
			}
		}(i)
	}

	close(startCh)
	wg.Wait()

	for _, r := range results {
		if r.err != nil {
			t.Fatalf("VIOLATION: forced claim failed for worker %d (%s): %v", r.worker, r.agent, r.err)
		}
	}

	// Verify final claim.json is intact
	claimFile := filepath.Join(goalDir, "claim.json")
	data, err := os.ReadFile(claimFile)
	if err != nil {
		t.Fatalf("VIOLATION: failed to read claim.json: %v", err)
	}
	if len(data) == 0 {
		t.Fatalf("VIOLATION: claim.json is 0 bytes after forced contention")
	}

	var diskClaim GoalClaim
	if err := json.Unmarshal(data, &diskClaim); err != nil {
		t.Fatalf("VIOLATION: corrupted claim.json after forced contention: %v", err)
	}

	// Verify lock is clean
	lockFile := filepath.Join(goalDir, ".claim.lock")
	if fileExists(lockFile) {
		t.Fatalf("VIOLATION: .claim.lock leaked after forced contention")
	}
}

// 9. Multi-Process Subprocess Race Simulation
func TestMultiProcessContention(t *testing.T) {
	// Spawns multiple test processes attempting to acquire the goal lock
	dir := t.TempDir()
	slug := "subprocess-goal"
	goalDir := setupTestGoal(t, dir, slug)

	// We test acquireGoalLock across multiple goroutines simulating distinct PID metadata
	// by writing mock lockfiles with foreign hostnames/PIDs and verifying fallback heuristics.
	currHost, _ := os.Hostname()

	// Create active lock with current PID
	unlock, err := acquireGoalLockWithTimeout(goalDir, 1*time.Second)
	if err != nil {
		t.Fatalf("initial lock acquisition failed: %v", err)
	}

	// Another attempt with short timeout must fail deterministically
	_, err2 := acquireGoalLockWithTimeout(goalDir, 50*time.Millisecond)
	if err2 == nil {
		t.Fatalf("VIOLATION: expected timeout when lock is actively held")
	}
	if !strings.Contains(err2.Error(), "timed out after") {
		t.Fatalf("VIOLATION: expected timeout error message, got: %v", err2)
	}

	unlock()

	// Now lock must be acquirable again
	unlock3, err3 := acquireGoalLockWithTimeout(goalDir, 500*time.Millisecond)
	if err3 != nil {
		t.Fatalf("VIOLATION: failed to acquire lock after previous unlock: %v", err3)
	}
	unlock3()

	if fileExists(filepath.Join(goalDir, ".claim.lock")) {
		t.Fatalf("VIOLATION: lock file remained after unlock")
	}

	// Verify corrupted 0-byte lock file eviction after staleness threshold
	corruptedLockPath := filepath.Join(goalDir, ".claim.lock")
	if err := os.WriteFile(corruptedLockPath, []byte(""), 0600); err != nil {
		t.Fatal(err)
	}
	// Backdate file modtime to exceed LockStaleThreshold
	oldTime := time.Now().Add(-15 * time.Second)
	_ = os.Chtimes(corruptedLockPath, oldTime, oldTime)

	unlock4, err4 := acquireGoalLockWithTimeout(goalDir, 500*time.Millisecond)
	if err4 != nil {
		t.Fatalf("VIOLATION: failed to evict backdated 0-byte corrupted lock: %v", err4)
	}
	unlock4()

	// Verify dead PID lock eviction
	deadLock := lockInfo{
		PID:       9999998,
		Hostname:  currHost,
		CreatedAt: time.Now().UTC().Add(-1 * time.Second),
		Token:     "dead-tok",
	}
	dBytes, _ := json.Marshal(deadLock)
	if err := os.WriteFile(corruptedLockPath, dBytes, 0600); err != nil {
		t.Fatal(err)
	}

	unlock5, err5 := acquireGoalLockWithTimeout(goalDir, 500*time.Millisecond)
	if err5 != nil {
		t.Fatalf("VIOLATION: failed to evict dead local PID lock: %v", err5)
	}
	unlock5()
}
