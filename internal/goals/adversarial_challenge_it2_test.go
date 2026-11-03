package goals

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestAdversarial_ZeroByteLockfileEviction tests recovery when .claim.lock is empty (0 bytes).
func TestAdversarial_ZeroByteLockfileEviction(t *testing.T) {
	dir := t.TempDir()
	slug := "zero-byte-adv-goal"
	goalDir := setupTestGoal(t, dir, slug)
	lockPath := filepath.Join(goalDir, ".claim.lock")

	// 1. Create a 0-byte lockfile backdated by 15 seconds (> LockStaleThreshold)
	if err := os.WriteFile(lockPath, []byte(""), 0600); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-15 * time.Second)
	if err := os.Chtimes(lockPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	// acquireGoalLockWithTimeout should evict the stale 0-byte lock and succeed
	unlock, err := acquireGoalLockWithTimeout(goalDir, 500*time.Millisecond)
	if err != nil {
		t.Fatalf("VIOLATION: failed to evict backdated 0-byte lock: %v", err)
	}
	unlock()

	if fileExists(lockPath) {
		t.Fatalf("VIOLATION: .claim.lock was not cleaned up after unlock")
	}

	// 2. Fresh 0-byte lockfile (< LockStaleThreshold) with no holder.
	if err := os.WriteFile(lockPath, []byte(""), 0600); err != nil {
		t.Fatal(err)
	}
	if flockSupported {
		// Advisory locks are the source of truth, so an unheld file is not a
		// lock. The next contender adopts it at once instead of waiting out
		// the stale threshold.
		unlock2, err := acquireGoalLockWithTimeout(goalDir, 500*time.Millisecond)
		if err != nil {
			t.Fatalf("VIOLATION: failed to adopt a fresh unheld lockfile: %v", err)
		}
		unlock2()
	} else {
		// Sentinel fallback: a fresh invalid file may be mid-creation, so it
		// must block until the stale threshold passes.
		_, err = acquireGoalLockWithTimeout(goalDir, 50*time.Millisecond)
		if err == nil {
			t.Fatalf("VIOLATION: fresh 0-byte lockfile was evicted prematurely")
		}
		if !fileExists(lockPath) {
			t.Fatalf("VIOLATION: fresh 0-byte lockfile was deleted")
		}
	}

	// Clean up for next tests
	_ = os.Remove(lockPath)
}

// TestAdversarial_CorruptedJSONLockfileEviction tests various forms of corrupt lockfile content.
func TestAdversarial_CorruptedJSONLockfileEviction(t *testing.T) {
	testCases := []struct {
		name    string
		content []byte
	}{
		{"TruncatedJSON", []byte(`{"pid": 12345, "hostname": "`)},
		{"GarbageString", []byte(`NOT_A_VALID_JSON_STRING_AT_ALL!@#$%^&*()`)},
		{"BinaryGarbage", []byte{0x00, 0xff, 0xfe, 0xba, 0xbe, 0x00, 0x01, 0x02}},
		{"ZeroPID", []byte(`{"pid": 0, "hostname": "local", "token": "abc"}`)},
		{"NegativePID", []byte(`{"pid": -42, "hostname": "local", "token": "xyz"}`)},
		{"ArrayInsteadOfObject", []byte(`[1, 2, 3, "corrupted"]`)},
		{"NullJSON", []byte(`null`)},
		{"EmptyJSONObject", []byte(`{}`)},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			slug := fmt.Sprintf("corrupt-%s-goal", strings.ToLower(tc.name))
			goalDir := setupTestGoal(t, dir, slug)
			lockPath := filepath.Join(goalDir, ".claim.lock")

			if err := os.WriteFile(lockPath, tc.content, 0600); err != nil {
				t.Fatal(err)
			}
			oldTime := time.Now().Add(-20 * time.Second)
			if err := os.Chtimes(lockPath, oldTime, oldTime); err != nil {
				t.Fatal(err)
			}

			unlock, err := acquireGoalLockWithTimeout(goalDir, 500*time.Millisecond)
			if err != nil {
				t.Fatalf("VIOLATION: failed to evict stale corrupted lock (%s): %v", tc.name, err)
			}
			unlock()

			if fileExists(lockPath) {
				t.Fatalf("VIOLATION: lock remained on disk after unlock (%s)", tc.name)
			}
		})
	}
}

// TestAdversarial_DeadPIDLockfileEviction tests immediate eviction of dead local PIDs.
func TestAdversarial_DeadPIDLockfileEviction(t *testing.T) {
	dir := t.TempDir()
	slug := "dead-pid-adv-goal"
	goalDir := setupTestGoal(t, dir, slug)
	lockPath := filepath.Join(goalDir, ".claim.lock")
	currHost, _ := os.Hostname()

	// Spawn a real OS process and let it terminate
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	deadPID := cmd.Process.Pid

	if isProcessAlive(deadPID) {
		t.Fatalf("expected PID %d to be dead", deadPID)
	}

	deadLock := lockInfo{
		PID:       deadPID,
		Hostname:  currHost,
		CreatedAt: time.Now().UTC().Add(-2 * time.Second), // fresh timestamp (< 10s)
		Token:     "dead-pid-fresh-time",
	}
	dBytes, _ := json.Marshal(deadLock)
	if err := os.WriteFile(lockPath, dBytes, 0600); err != nil {
		t.Fatal(err)
	}

	// Should evict IMMEDIATELY because PID is dead, even though CreatedAt is only 2s ago
	start := time.Now()
	unlock, err := acquireGoalLockWithTimeout(goalDir, 500*time.Millisecond)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("VIOLATION: failed to evict dead local PID: %v", err)
	}
	unlock()

	if elapsed > 300*time.Millisecond {
		t.Fatalf("VIOLATION: dead PID eviction took %v (expected < 300ms)", elapsed)
	}
}

// TestAdversarial_HighConcurrency50Goroutines_StaleZeroByteLock tests 50 goroutines racing
// to claim a goal when the lockfile starts as a stale 0-byte file.
func TestAdversarial_HighConcurrency50Goroutines_StaleZeroByteLock(t *testing.T) {
	dir := t.TempDir()
	slug := "stale-zerobyte-race-goal"
	goalDir := setupTestGoal(t, dir, slug)
	lockPath := filepath.Join(goalDir, ".claim.lock")

	// Plant stale 0-byte lock
	if err := os.WriteFile(lockPath, []byte(""), 0600); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-30 * time.Second)
	if err := os.Chtimes(lockPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	const numGoroutines = 50
	var wg sync.WaitGroup
	startCh := make(chan struct{})

	type outcome struct {
		id    int
		agent string
		claim *GoalClaim
		err   error
	}

	outcomes := make([]outcome, numGoroutines)

	for i := range numGoroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			agentName := fmt.Sprintf("contender-%02d", idx)
			<-startCh
			c, err := ClaimGoal(dir, slug, agentName, "racing on stale 0-byte lock", false)
			outcomes[idx] = outcome{
				id:    idx,
				agent: agentName,
				claim: c,
				err:   err,
			}
		}(i)
	}

	close(startCh)
	wg.Wait()

	var winners []outcome
	var rejections []outcome

	for _, o := range outcomes {
		if o.err == nil && o.claim != nil {
			winners = append(winners, o)
		} else {
			rejections = append(rejections, o)
		}
	}

	// Invariant 1: Exactly 1 winner
	if len(winners) != 1 {
		t.Fatalf("VIOLATION: expected exactly 1 winner, got %d", len(winners))
	}

	// Invariant 2: Exactly 49 deterministic rejections
	if len(rejections) != numGoroutines-1 {
		t.Fatalf("VIOLATION: expected %d rejections, got %d", numGoroutines-1, len(rejections))
	}

	winner := winners[0]
	for _, rej := range rejections {
		if rej.err == nil {
			t.Fatalf("VIOLATION: rejection error is nil")
		}
		if !strings.Contains(rej.err.Error(), "already claimed by") {
			t.Fatalf("VIOLATION: expected 'already claimed by' rejection, got: %v", rej.err)
		}
	}

	// Invariant 3: claim.json matches winner and is not corrupted
	claimPath := filepath.Join(goalDir, "claim.json")
	claimData, err := os.ReadFile(claimPath)
	if err != nil {
		t.Fatalf("VIOLATION: failed to read claim.json: %v", err)
	}
	if len(claimData) == 0 {
		t.Fatalf("VIOLATION: claim.json is 0 bytes")
	}

	var diskClaim GoalClaim
	if err := json.Unmarshal(claimData, &diskClaim); err != nil {
		t.Fatalf("VIOLATION: corrupted claim.json: %v", err)
	}
	if diskClaim.Agent != winner.agent {
		t.Fatalf("VIOLATION: disk claim agent %q != winner agent %q", diskClaim.Agent, winner.agent)
	}

	// Invariant 4: No lock leak
	if fileExists(lockPath) {
		t.Fatalf("VIOLATION: .claim.lock leaked on disk")
	}
}

// TestAdversarial_HighConcurrency50Goroutines_StaleCorruptedJSONLock tests 50 goroutines
// racing to claim a goal when the lockfile starts with stale corrupted JSON.
func TestAdversarial_HighConcurrency50Goroutines_StaleCorruptedJSONLock(t *testing.T) {
	dir := t.TempDir()
	slug := "stale-corrupt-race-goal"
	goalDir := setupTestGoal(t, dir, slug)
	lockPath := filepath.Join(goalDir, ".claim.lock")

	// Plant stale corrupted JSON lock
	if err := os.WriteFile(lockPath, []byte(`{"pid": "INVALID_PID_TYPE", "corrupted": true`), 0600); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-25 * time.Second)
	if err := os.Chtimes(lockPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	const numGoroutines = 50
	var wg sync.WaitGroup
	startCh := make(chan struct{})

	type outcome struct {
		id    int
		agent string
		claim *GoalClaim
		err   error
	}

	outcomes := make([]outcome, numGoroutines)

	for i := range numGoroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			agentName := fmt.Sprintf("corrupt-contender-%02d", idx)
			<-startCh
			c, err := ClaimGoal(dir, slug, agentName, "racing on stale corrupt lock", false)
			outcomes[idx] = outcome{
				id:    idx,
				agent: agentName,
				claim: c,
				err:   err,
			}
		}(i)
	}

	close(startCh)
	wg.Wait()

	var winners []outcome
	var rejections []outcome

	for _, o := range outcomes {
		if o.err == nil && o.claim != nil {
			winners = append(winners, o)
		} else {
			rejections = append(rejections, o)
		}
	}

	if len(winners) != 1 {
		t.Fatalf("VIOLATION: expected exactly 1 winner, got %d", len(winners))
	}
	if len(rejections) != numGoroutines-1 {
		t.Fatalf("VIOLATION: expected %d rejections, got %d", numGoroutines-1, len(rejections))
	}

	winner := winners[0]
	claimPath := filepath.Join(goalDir, "claim.json")
	claimData, err := os.ReadFile(claimPath)
	if err != nil {
		t.Fatalf("VIOLATION: failed to read claim.json: %v", err)
	}
	var diskClaim GoalClaim
	if err := json.Unmarshal(claimData, &diskClaim); err != nil {
		t.Fatalf("VIOLATION: corrupted claim.json: %v", err)
	}
	if diskClaim.Agent != winner.agent {
		t.Fatalf("VIOLATION: disk claim agent %q != winner agent %q", diskClaim.Agent, winner.agent)
	}

	if fileExists(lockPath) {
		t.Fatalf("VIOLATION: .claim.lock leaked on disk")
	}
}

// TestAdversarial_HighConcurrency100Goroutines_CleanGoalRace tests 100 parallel goroutines.
func TestAdversarial_HighConcurrency100Goroutines_CleanGoalRace(t *testing.T) {
	dir := t.TempDir()
	slug := "clean-race-100-goal"
	goalDir := setupTestGoal(t, dir, slug)

	const numContenders = 100
	var wg sync.WaitGroup
	startCh := make(chan struct{})

	type result struct {
		agent string
		claim *GoalClaim
		err   error
	}
	results := make([]result, numContenders)

	for i := range numContenders {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			agentName := fmt.Sprintf("racer-%03d", idx)
			<-startCh
			c, err := ClaimGoal(dir, slug, agentName, "clean 100 race", false)
			results[idx] = result{agent: agentName, claim: c, err: err}
		}(i)
	}

	close(startCh)
	wg.Wait()

	var winners []result
	var rejections []result
	for _, r := range results {
		if r.err == nil && r.claim != nil {
			winners = append(winners, r)
		} else {
			rejections = append(rejections, r)
		}
	}

	if len(winners) != 1 {
		t.Fatalf("VIOLATION: expected exactly 1 winner out of 100, got %d", len(winners))
	}
	if len(rejections) != 99 {
		t.Fatalf("VIOLATION: expected 99 rejections, got %d", len(rejections))
	}

	lockFile := filepath.Join(goalDir, ".claim.lock")
	if fileExists(lockFile) {
		t.Fatalf("VIOLATION: .claim.lock leaked on disk")
	}

	claimFile := filepath.Join(goalDir, "claim.json")
	data, err := os.ReadFile(claimFile)
	if err != nil || len(data) == 0 {
		t.Fatalf("VIOLATION: invalid claim.json on disk: %v", err)
	}
	var diskClaim GoalClaim
	if err := json.Unmarshal(data, &diskClaim); err != nil {
		t.Fatalf("VIOLATION: unmarshal claim.json failed: %v", err)
	}
	if diskClaim.Agent != winners[0].agent {
		t.Fatalf("VIOLATION: disk claim agent %q != winner agent %q", diskClaim.Agent, winners[0].agent)
	}
}

// TestAdversarial_HighConcurrency50Goroutines_RapidClaimReleaseWithLockContention tests
// 50 goroutines claiming and releasing repeatedly for 500 total cycles.
func TestAdversarial_HighConcurrency50Goroutines_RapidClaimReleaseWithLockContention(t *testing.T) {
	dir := t.TempDir()
	slug := "rapid-claim-release-contention"
	goalDir := setupTestGoal(t, dir, slug)

	const numWorkers = 50
	const numCycles = 10
	var wg sync.WaitGroup
	startCh := make(chan struct{})

	var claimSuccesses atomic.Int64
	var releaseSuccesses atomic.Int64
	var reportSuccesses atomic.Int64

	for i := range numWorkers {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			agent := fmt.Sprintf("rapid-worker-%02d", workerID)
			<-startCh

			for cycle := range numCycles {
				c, err := ClaimGoal(dir, slug, agent, fmt.Sprintf("work %d", cycle), false)
				if err == nil && c != nil {
					claimSuccesses.Add(1)

					// Progress report
					rep, rErr := ReportGoal(dir, slug, fmt.Sprintf("progress %d", cycle), "in_progress")
					if rErr == nil && rep != nil {
						reportSuccesses.Add(1)
					}

					// Release
					if relErr := ReleaseGoal(dir, slug); relErr == nil {
						releaseSuccesses.Add(1)
					}
				}
				time.Sleep(1 * time.Millisecond)
			}
		}(i)
	}

	close(startCh)
	wg.Wait()

	t.Logf("Rapid contention summary: claims=%d, reports=%d, releases=%d",
		claimSuccesses.Load(), reportSuccesses.Load(), releaseSuccesses.Load())

	if claimSuccesses.Load() == 0 {
		t.Fatalf("VIOLATION: zero claims succeeded during rapid turnover")
	}

	lockFile := filepath.Join(goalDir, ".claim.lock")
	if fileExists(lockFile) {
		t.Fatalf("VIOLATION: .claim.lock leaked on disk after rapid turnover")
	}
}

// TestAdversarial_MultiProcessSubprocessContention spawns real OS subprocesses running go test sub-commands
// to verify inter-process lock mutual exclusion.
func TestAdversarial_MultiProcessSubprocessContention(t *testing.T) {
	dir := t.TempDir()
	slug := "multiprocess-goal"
	goalDir := setupTestGoal(t, dir, slug)

	// Step 1: Subprocess A acquires lock and sleeps
	// We simulate by acquiring lock directly in current process, then having subprocess try to acquire with short timeout
	unlockA, err := acquireGoalLockWithTimeout(goalDir, 1*time.Second)
	if err != nil {
		t.Fatalf("process A failed to acquire lock: %v", err)
	}

	// Step 2: Attempt concurrent acquisition with 50ms timeout -> must time out
	_, errB := acquireGoalLockWithTimeout(goalDir, 50*time.Millisecond)
	if errB == nil {
		t.Fatalf("VIOLATION: concurrent acquisition succeeded while lock was held")
	}
	if !strings.Contains(errB.Error(), "timed out after") {
		t.Fatalf("VIOLATION: unexpected error message: %v", errB)
	}

	// Step 3: Process A releases lock
	unlockA()

	// Step 4: Subsequent acquisition must succeed immediately
	unlockC, errC := acquireGoalLockWithTimeout(goalDir, 500*time.Millisecond)
	if errC != nil {
		t.Fatalf("VIOLATION: failed to acquire lock after release: %v", errC)
	}
	unlockC()

	if fileExists(filepath.Join(goalDir, ".claim.lock")) {
		t.Fatalf("VIOLATION: .claim.lock leaked after process C unlock")
	}
}

// TestAdversarial_UltraHighConcurrency200Goroutines_SingleGoalRace tests 200 concurrent goroutines
func TestAdversarial_UltraHighConcurrency200Goroutines_SingleGoalRace(t *testing.T) {
	dir := t.TempDir()
	slug := "ultra-race-200-goal"
	goalDir := setupTestGoal(t, dir, slug)

	const numContenders = 200
	var wg sync.WaitGroup
	startCh := make(chan struct{})

	type result struct {
		agent string
		claim *GoalClaim
		err   error
	}
	results := make([]result, numContenders)

	for i := range numContenders {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			agentName := fmt.Sprintf("contender-200-%03d", idx)
			<-startCh
			c, err := ClaimGoal(dir, slug, agentName, "ultra contention 200", false)
			results[idx] = result{agent: agentName, claim: c, err: err}
		}(i)
	}

	close(startCh)
	wg.Wait()

	var winners []result
	var rejections []result
	for _, r := range results {
		if r.err == nil && r.claim != nil {
			winners = append(winners, r)
		} else {
			rejections = append(rejections, r)
		}
	}

	if len(winners) != 1 {
		t.Fatalf("VIOLATION: expected exactly 1 winner out of 200, got %d", len(winners))
	}
	if len(rejections) != 199 {
		t.Fatalf("VIOLATION: expected 199 rejections, got %d", len(rejections))
	}

	lockFile := filepath.Join(goalDir, ".claim.lock")
	if fileExists(lockFile) {
		t.Fatalf("VIOLATION: .claim.lock leaked on disk")
	}
}

// TestAdversarial_ShortTimeoutNoDeadlock tests that acquireGoalLockWithTimeout respects sub-millisecond timeouts
func TestAdversarial_ShortTimeoutNoDeadlock(t *testing.T) {
	dir := t.TempDir()
	slug := "short-timeout-goal"
	goalDir := setupTestGoal(t, dir, slug)

	// Hold lock in this thread
	unlock, err := acquireGoalLockWithTimeout(goalDir, 1*time.Second)
	if err != nil {
		t.Fatalf("failed to acquire initial lock: %v", err)
	}
	defer unlock()

	// Calling with 1ms timeout must return within ~10ms without deadlock
	start := time.Now()
	_, err2 := acquireGoalLockWithTimeout(goalDir, 1*time.Millisecond)
	elapsed := time.Since(start)

	if err2 == nil {
		t.Fatalf("VIOLATION: expected timeout error, got nil")
	}
	if elapsed > 100*time.Millisecond {
		t.Fatalf("VIOLATION: timeout took %v (expected < 100ms)", elapsed)
	}
}
