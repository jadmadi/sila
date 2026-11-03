package goals

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/render"
)

func TestScanGoalsAndSyncReadme(t *testing.T) {
	tempDir := t.TempDir()
	goalsDir := filepath.Join(tempDir, "goals")
	if err := os.MkdirAll(filepath.Join(goalsDir, "sample-goal"), 0755); err != nil {
		t.Fatal(err)
	}

	goalContent := `# Goal: Test Sample Goal

## Goal Description
This is a test goal package to verify scanning and auto-sync.

## Done Condition
- Verify everything works.
`
	if err := os.WriteFile(filepath.Join(goalsDir, "sample-goal", "goal.md"), []byte(goalContent), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalsDir, "sample-goal", "plan.md"), []byte("# Plan"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalsDir, "sample-goal", "facts.md"), []byte("# Facts"), 0644); err != nil {
		t.Fatal(err)
	}

	res, err := ScanGoals(tempDir)
	if err != nil {
		t.Fatalf("ScanGoals failed: %v", err)
	}

	if res.Total != 1 {
		t.Fatalf("expected 1 total goal, got %d", res.Total)
	}
	if res.PendingCount != 1 {
		t.Fatalf("expected 1 pending goal, got %d", res.PendingCount)
	}
	if res.Goals[0].Title != "Test Sample Goal" {
		t.Fatalf("expected title 'Test Sample Goal', got %q", res.Goals[0].Title)
	}

	if err := SyncReadme(tempDir, res); err != nil {
		t.Fatalf("SyncReadme failed: %v", err)
	}

	readmeData, err := os.ReadFile(filepath.Join(goalsDir, "README.md"))
	if err != nil {
		t.Fatalf("read README.md failed: %v", err)
	}
	if len(readmeData) == 0 {
		t.Fatal("README.md is empty")
	}
}

func TestScanGoalsRequiresExplicitNonDocumentationCommitEvidence(t *testing.T) {
	dir := t.TempDir()
	goalDir := filepath.Join(dir, "goals", "safe-goal")
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"goal.md", "plan.md", "facts.md"} {
		if err := os.WriteFile(filepath.Join(goalDir, name), []byte("# Goal: Safe\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	git(t, dir, "init")
	git(t, dir, "config", "user.email", "test@example.com")
	git(t, dir, "config", "user.name", "Test")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "docs: mention safe-goal")
	res, err := ScanGoals(dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Goals[0].Status == "implemented" {
		t.Fatal("documentation mention must not complete a goal")
	}
	git(t, dir, "commit", "--allow-empty", "-m", "feat: ship safeguards (goals/safe-goal/goal.md)")
	res, err = ScanGoals(dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Goals[0].Status != "implemented" || res.Goals[0].Commit == "" {
		t.Fatalf("expected explicit reachable commit evidence: %+v", res.Goals[0])
	}
}

func TestScanGoalsReportsSkippedDocsCandidates(t *testing.T) {
	dir := t.TempDir()
	goalDir := filepath.Join(dir, "goals", "notes-goal")
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"goal.md", "plan.md", "facts.md"} {
		if err := os.WriteFile(filepath.Join(goalDir, name), []byte("# Goal: Notes\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	git(t, dir, "init", "-q")
	git(t, dir, "config", "user.email", "t@t.com")
	git(t, dir, "config", "user.name", "T")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "docs: describe notes goal (goals/notes-goal/goal.md)")
	git(t, dir, "commit", "--allow-empty", "-qm", "test: verify notes goal (goals/notes-goal/goal.md)")

	res, err := ScanGoals(dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Goals[0].Status == "implemented" {
		t.Fatalf("docs/test commits must not certify a goal: %+v", res.Goals[0])
	}
	if len(res.Goals[0].SkippedCandidates) != 2 {
		t.Fatalf("expected 2 skipped candidates, got %+v", res.Goals[0].SkippedCandidates)
	}

	git(t, dir, "commit", "--allow-empty", "-qm", "feat: ship notes goal (goals/notes-goal/goal.md)")
	res, err = ScanGoals(dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Goals[0].Status != "implemented" || len(res.Goals[0].SkippedCandidates) != 0 {
		t.Fatalf("expected an implemented goal with no skipped note, got %+v", res.Goals[0])
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func TestGoalClaimReleaseAndReport(t *testing.T) {
	dir := t.TempDir()
	goalDir := filepath.Join(dir, "goals", "edge-auth")
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"goal.md", "plan.md", "facts.md"} {
		if err := os.WriteFile(filepath.Join(goalDir, name), []byte("# Goal: Edge Auth\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// 1. Initial scan: pending
	res, err := ScanGoals(dir)
	if err != nil || res.PendingCount != 1 {
		t.Fatalf("expected 1 pending goal, got %+v (err: %v)", res, err)
	}

	// 2. Claim goal for claude-code
	claim, err := ClaimGoal(dir, "edge-auth", "claude-code", "evaluating schema", false)
	if err != nil {
		t.Fatalf("ClaimGoal failed: %v", err)
	}
	if claim.Agent != "claude-code" || claim.Note != "evaluating schema" {
		t.Fatalf("unexpected claim: %+v", claim)
	}

	// 3. Scan: now claimed / in-progress
	res, err = ScanGoals(dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.ClaimedCount != 1 || res.PendingCount != 0 {
		t.Fatalf("expected 1 claimed and 0 pending, got claimed=%d pending=%d", res.ClaimedCount, res.PendingCount)
	}

	// 4. Double claim by devin should fail without force
	_, err = ClaimGoal(dir, "edge-auth", "devin", "taking over", false)
	if err == nil {
		t.Fatal("expected error on duplicate claim without force")
	}

	// 5. Double claim with force should succeed
	claim2, err := ClaimGoal(dir, "edge-auth", "devin", "taking over", true)
	if err != nil || claim2.Agent != "devin" {
		t.Fatalf("expected forced claim to succeed for devin, got %+v (err: %v)", claim2, err)
	}

	// 6. Report progress
	reported, err := ReportGoal(dir, "edge-auth", "step 2 done", "in_progress")
	if err != nil || reported.Note != "step 2 done" {
		t.Fatalf("ReportGoal failed: %+v (err: %v)", reported, err)
	}

	// 7. Sync Readme with active claim
	res, _ = ScanGoals(dir)
	if err := SyncReadme(dir, res); err != nil {
		t.Fatalf("SyncReadme failed: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "goals", "README.md"))
	if !bytes.Contains(data, []byte("In Progress (Claimed Goals)")) || !bytes.Contains(data, []byte("@devin")) {
		t.Fatalf("README.md does not contain claimed section:\n%s", string(data))
	}

	// 8. Release goal
	if err := ReleaseGoal(dir, "edge-auth"); err != nil {
		t.Fatalf("ReleaseGoal failed: %v", err)
	}

	// 9. Scan: returned to pending
	res, _ = ScanGoals(dir)
	if res.PendingCount != 1 || res.ClaimedCount != 0 {
		t.Fatalf("expected 1 pending and 0 claimed after release, got %+v", res)
	}
}

func TestCreateGoalAndDependencyResolution(t *testing.T) {
	dir := t.TempDir()

	// 1. Create Goal 1: database-migration (Independent, Sequence: 1)
	g1, err := CreateGoal(CreateGoalParams{
		RootDir:     dir,
		Slug:        "database-migration",
		Title:       "Database Migration",
		Description: "Set up core schema tables",
		Sequence:    1,
	})
	if err != nil {
		t.Fatalf("CreateGoal 1 failed: %v", err)
	}
	if !g1.Independent || g1.Slug != "database-migration" {
		t.Fatalf("unexpected goal 1: %+v", g1)
	}

	// 2. Create Goal 2: auth-service (Dependent on database-migration, Sequence: 2)
	g2, err := CreateGoal(CreateGoalParams{
		RootDir:     dir,
		Slug:        "auth-service",
		Title:       "Auth Service",
		Description: "Implement JWT login and sessions",
		DependsOn:   []string{"database-migration"},
		Sequence:    2,
	})
	if err != nil {
		t.Fatalf("CreateGoal 2 failed: %v", err)
	}
	if g2.Independent {
		t.Fatalf("expected goal 2 to be dependent: %+v", g2)
	}

	// 3. Create Goal 3: frontend-theme (Independent disjoint task, Sequence: 0)
	g3, err := CreateGoal(CreateGoalParams{
		RootDir:     dir,
		Slug:        "frontend-theme",
		Title:       "Frontend Theme",
		Description: "Design tokens and dark mode styling",
	})
	if err != nil {
		t.Fatalf("CreateGoal 3 failed: %v", err)
	}
	if !g3.Independent {
		t.Fatalf("expected goal 3 to be independent: %+v", g3)
	}

	// 4. Initial Scan:
	// - database-migration is READY (unblocked)
	// - frontend-theme is READY & INDEPENDENT
	// - auth-service is BLOCKED (database-migration is not implemented yet)
	res, err := ScanGoals(dir)
	if err != nil {
		t.Fatalf("ScanGoals failed: %v", err)
	}
	if res.Total != 3 {
		t.Fatalf("expected 3 total goals, got %d", res.Total)
	}
	if res.ReadyCount != 2 {
		t.Fatalf("expected 2 ready goals, got %d", res.ReadyCount)
	}
	if res.BlockedCount != 1 {
		t.Fatalf("expected 1 blocked goal, got %d", res.BlockedCount)
	}
	if res.IndependentCount != 2 {
		t.Fatalf("expected 2 independent goals, got %d", res.IndependentCount)
	}

	// Verify goals order: Ready Seq 1 -> Ready Seq 0 (frontend-theme) -> Blocked Seq 2 (auth-service)
	if res.Goals[0].Slug != "database-migration" || res.Goals[0].Blocked {
		t.Fatalf("expected database-migration to be first and unblocked: %+v", res.Goals[0])
	}
	if res.Goals[1].Slug != "frontend-theme" || res.Goals[1].Blocked {
		t.Fatalf("expected frontend-theme to be second (ready tier): %+v", res.Goals[1])
	}
	if res.Goals[2].Slug != "auth-service" || !res.Goals[2].Blocked {
		t.Fatalf("expected auth-service to be third and blocked by database-migration: %+v", res.Goals[2])
	}

	// 5. Simulate verifying commit for database-migration
	git(t, dir, "init")
	git(t, dir, "config", "user.email", "test@example.com")
	git(t, dir, "config", "user.name", "Test")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "feat: migrate tables (goals/database-migration/goal.md)")

	// 6. Rescan:
	// - database-migration is IMPLEMENTED
	// - auth-service is now UNBLOCKED (ReadyCount = 2, BlockedCount = 0)
	res, err = ScanGoals(dir)
	if err != nil {
		t.Fatalf("ScanGoals 2 failed: %v", err)
	}
	if res.ImplementedCount != 1 {
		t.Fatalf("expected 1 implemented goal, got %d", res.ImplementedCount)
	}
	if res.BlockedCount != 0 {
		t.Fatalf("expected 0 blocked goals after prerequisite commit, got %d", res.BlockedCount)
	}
	if res.ReadyCount != 2 {
		t.Fatalf("expected 2 ready goals, got %d", res.ReadyCount)
	}

	for _, g := range res.Goals {
		if g.Slug == "auth-service" && g.Blocked {
			t.Fatalf("auth-service should be unblocked now: %+v", g)
		}
	}

	// 7. Verify README generation includes Mode badges
	if err := SyncReadme(dir, res); err != nil {
		t.Fatalf("SyncReadme failed: %v", err)
	}
	readmeData, err := os.ReadFile(filepath.Join(dir, "goals", "README.md"))
	if err != nil {
		t.Fatalf("read README.md failed: %v", err)
	}
	readmeStr := string(readmeData)
	if !bytes.Contains(readmeData, []byte("Mode & Sequence")) {
		t.Fatalf("README.md missing 'Mode & Sequence' column header:\n%s", readmeStr)
	}
	if !bytes.Contains(readmeData, []byte("⚡ Independent")) {
		t.Fatalf("README.md missing '⚡ Independent' badge:\n%s", readmeStr)
	}
}

func TestConcurrentClaimRace(t *testing.T) {
	dir := t.TempDir()
	goalDir := filepath.Join(dir, "goals", "race-goal")
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"goal.md", "plan.md", "facts.md"} {
		if err := os.WriteFile(filepath.Join(goalDir, name), []byte("# Goal: Race Goal\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	numGoroutines := 30
	var wg sync.WaitGroup
	startCh := make(chan struct{})

	type claimResult struct {
		agent string
		claim *GoalClaim
		err   error
	}

	results := make([]claimResult, numGoroutines)

	for i := range numGoroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			agentName := fmt.Sprintf("agent-%02d", idx)
			<-startCh // Synchronize start
			claim, err := ClaimGoal(dir, "race-goal", agentName, fmt.Sprintf("claim attempt by %s", agentName), false)
			results[idx] = claimResult{
				agent: agentName,
				claim: claim,
				err:   err,
			}
		}(i)
	}

	// Release all goroutines simultaneously
	close(startCh)
	wg.Wait()

	var successes []claimResult
	var rejections []claimResult

	for _, r := range results {
		if r.err == nil && r.claim != nil {
			successes = append(successes, r)
		} else {
			rejections = append(rejections, r)
		}
	}

	if len(successes) != 1 {
		t.Fatalf("expected exactly 1 winner out of %d concurrent contenders, got %d successes", numGoroutines, len(successes))
	}
	if len(rejections) != numGoroutines-1 {
		t.Fatalf("expected %d rejections, got %d", numGoroutines-1, len(rejections))
	}

	winner := successes[0]
	for _, rej := range rejections {
		if !strings.Contains(rej.err.Error(), "already claimed by") {
			t.Errorf("expected clean domain rejection message, got: %v", rej.err)
		}
	}

	// Verify claim.json on disk
	claimData, err := os.ReadFile(filepath.Join(goalDir, "claim.json"))
	if err != nil {
		t.Fatalf("read claim.json failed: %v", err)
	}
	var onDisk GoalClaim
	if err := json.Unmarshal(claimData, &onDisk); err != nil {
		t.Fatalf("claim.json corrupted: %v", err)
	}
	if onDisk.Agent != winner.agent || onDisk.ClaimedBy != winner.agent {
		t.Fatalf("claim.json agent mismatch: expected %q, got %+v", winner.agent, onDisk)
	}

	// Verify lock file is cleanly removed
	lockPath := filepath.Join(goalDir, ".claim.lock")
	if fileExists(lockPath) {
		t.Fatalf("expected .claim.lock to be removed after all operations, but file exists")
	}
}

func TestStaleClaimDetectionAndSafeAutoReclaim(t *testing.T) {
	dir := t.TempDir()
	goalDir := filepath.Join(dir, "goals", "stale-goal")
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"goal.md", "plan.md", "facts.md"} {
		if err := os.WriteFile(filepath.Join(goalDir, name), []byte("# Goal: Stale Goal\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	currHost, _ := os.Hostname()

	// 1. Tier 1 Test: Dead PID on local host within 15m grace window should NOT be stale
	graceClaim := GoalClaim{
		GoalSlug:  "stale-goal",
		ClaimedBy: "active-cli-agent",
		Agent:     "active-cli-agent",
		ClaimedAt: time.Now().UTC().Add(-5 * time.Minute),
		UpdatedAt: time.Now().UTC().Add(-5 * time.Minute),
		Hostname:  currHost,
		PID:       9999999, // Non-existent PID
		Status:    "in_progress",
	}
	graceData, _ := json.MarshalIndent(graceClaim, "", "  ")
	if err := os.WriteFile(filepath.Join(goalDir, "claim.json"), graceData, 0644); err != nil {
		t.Fatal(err)
	}
	readGrace, err := readClaim(filepath.Join(goalDir, "claim.json"))
	if err != nil {
		t.Fatalf("readClaim failed: %v", err)
	}
	if readGrace.Stale {
		t.Fatalf("expected claim within 15m grace window NOT to be flagged stale, got: %+v", readGrace)
	}

	// Touch renewals keep claim fresh
	touchedClaim, err := TouchGoal(dir, "stale-goal", "active-cli-agent")
	if err != nil {
		t.Fatalf("TouchGoal failed: %v", err)
	}
	if touchedClaim.Stale || touchedClaim.UpdatedAt.Before(time.Now().UTC().Add(-1*time.Minute)) {
		t.Fatalf("unexpected touched claim: %+v", touchedClaim)
	}

	// Dead PID past 15m grace period should be flagged stale
	deadClaim := GoalClaim{
		GoalSlug:  "stale-goal",
		ClaimedBy: "crashed-agent",
		Agent:     "crashed-agent",
		ClaimedAt: time.Now().UTC().Add(-20 * time.Minute),
		UpdatedAt: time.Now().UTC().Add(-20 * time.Minute),
		Hostname:  currHost,
		PID:       9999999, // Non-existent PID
		Status:    "in_progress",
	}
	deadData, _ := json.MarshalIndent(deadClaim, "", "  ")
	if err := os.WriteFile(filepath.Join(goalDir, "claim.json"), deadData, 0644); err != nil {
		t.Fatal(err)
	}

	readC, err := readClaim(filepath.Join(goalDir, "claim.json"))
	if err != nil {
		t.Fatalf("readClaim failed: %v", err)
	}
	if !readC.Stale || !strings.Contains(readC.StaleReason, "9999999") {
		t.Fatalf("expected claim with dead PID past grace to be flagged stale, got: %+v", readC)
	}

	// Safe reclaim: new agent claims without --force
	newClaim, err := ClaimGoal(dir, "stale-goal", "resilient-agent", "taking over dead claim", false)
	if err != nil {
		t.Fatalf("expected safe auto-reclaim without force to succeed, got: %v", err)
	}
	if newClaim.ClaimedBy != "resilient-agent" || newClaim.Agent != "resilient-agent" {
		t.Fatalf("expected new agent in claim, got: %+v", newClaim)
	}
	if newClaim.Stale {
		t.Fatalf("expected new claim not to be stale, got: %+v", newClaim)
	}

	// 2. Tier 3 Test: Inactivity timeout (> 2 hours)
	inactiveClaim := GoalClaim{
		GoalSlug:  "stale-goal",
		ClaimedBy: "idle-agent",
		Agent:     "idle-agent",
		ClaimedAt: time.Now().UTC().Add(-3 * time.Hour),
		UpdatedAt: time.Now().UTC().Add(-3 * time.Hour),
		Hostname:  "remote-machine-xyz",
		PID:       12345,
		Status:    "in_progress",
	}
	inactiveData, _ := json.MarshalIndent(inactiveClaim, "", "  ")
	if err := os.WriteFile(filepath.Join(goalDir, "claim.json"), inactiveData, 0644); err != nil {
		t.Fatal(err)
	}

	readInactive, err := readClaim(filepath.Join(goalDir, "claim.json"))
	if err != nil {
		t.Fatalf("readClaim failed: %v", err)
	}
	if !readInactive.Stale || !strings.Contains(readInactive.StaleReason, "inactive") {
		t.Fatalf("expected claim with 3h inactivity to be flagged stale, got: %+v", readInactive)
	}

	// Safe reclaim of inactive claim without force
	reclaimed, err := ClaimGoal(dir, "stale-goal", "active-agent", "reclaiming inactive goal", false)
	if err != nil {
		t.Fatalf("expected safe auto-reclaim of inactive goal to succeed, got: %v", err)
	}
	if reclaimed.Agent != "active-agent" {
		t.Fatalf("expected active-agent, got %+v", reclaimed)
	}

	// 3. Active claim with live PID and fresh timestamp should be rejected without --force
	activeClaim := GoalClaim{
		GoalSlug:  "stale-goal",
		ClaimedBy: "live-agent",
		Agent:     "live-agent",
		ClaimedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		Hostname:  currHost,
		PID:       os.Getpid(),
		Status:    "in_progress",
	}
	activeData, _ := json.MarshalIndent(activeClaim, "", "  ")
	if err := os.WriteFile(filepath.Join(goalDir, "claim.json"), activeData, 0644); err != nil {
		t.Fatal(err)
	}

	_, err = ClaimGoal(dir, "stale-goal", "intruder-agent", "trying to steal", false)
	if err == nil {
		t.Fatalf("expected rejection when attempting to claim active goal without force")
	}

	// But succeeds with --force
	forcedClaim, err := ClaimGoal(dir, "stale-goal", "intruder-agent", "forced takeover", true)
	if err != nil || forcedClaim.Agent != "intruder-agent" {
		t.Fatalf("expected forced claim to succeed, got: %+v (err: %v)", forcedClaim, err)
	}
}

func TestReportGoalHeartbeatAndStalenessReset(t *testing.T) {
	dir := t.TempDir()
	goalDir := filepath.Join(dir, "goals", "heartbeat-goal")
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"goal.md", "plan.md", "facts.md"} {
		if err := os.WriteFile(filepath.Join(goalDir, name), []byte("# Goal: Heartbeat Goal\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Claim goal
	c1, err := ClaimGoal(dir, "heartbeat-goal", "worker-1", "starting work", false)
	if err != nil {
		t.Fatalf("ClaimGoal failed: %v", err)
	}

	time.Sleep(20 * time.Millisecond)

	// Report progress
	c2, err := ReportGoal(dir, "heartbeat-goal", "phase 2 complete", "evaluating")
	if err != nil {
		t.Fatalf("ReportGoal failed: %v", err)
	}

	if c2.Note != "phase 2 complete" || c2.Status != "evaluating" {
		t.Fatalf("unexpected reported claim: %+v", c2)
	}
	if !c2.UpdatedAt.After(c1.ClaimedAt) {
		t.Fatalf("expected UpdatedAt (%v) to be after ClaimedAt (%v)", c2.UpdatedAt, c1.ClaimedAt)
	}
	if c2.Stale {
		t.Fatalf("expected Stale to be false after heartbeat report")
	}

	// Verify on-disk persistence
	diskClaim, err := readClaim(filepath.Join(goalDir, "claim.json"))
	if err != nil {
		t.Fatalf("readClaim failed: %v", err)
	}
	if diskClaim.Note != "phase 2 complete" || diskClaim.Status != "evaluating" {
		t.Fatalf("persisted claim mismatch: %+v", diskClaim)
	}
}

func TestStaleLockEviction(t *testing.T) {
	dir := t.TempDir()
	goalDir := filepath.Join(dir, "goals", "stale-lock-goal")
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatal(err)
	}

	lockPath := filepath.Join(goalDir, ".claim.lock")
	currHost, _ := os.Hostname()

	// Write dead process lock
	staleLock := lockInfo{
		PID:       9999999,
		Hostname:  currHost,
		CreatedAt: time.Now().UTC().Add(-30 * time.Second),
		Token:     "dead-token-12345",
	}
	data, _ := json.Marshal(staleLock)
	if err := os.WriteFile(lockPath, data, 0600); err != nil {
		t.Fatal(err)
	}

	// acquireGoalLock should detect dead PID and evict lockfile
	unlock, err := acquireGoalLockWithTimeout(goalDir, 500*time.Millisecond)
	if err != nil {
		t.Fatalf("expected stale lock eviction and acquisition to succeed, got: %v", err)
	}

	// Ensure lockfile is currently held by us
	lockData, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read lockfile failed: %v", err)
	}
	var curLock lockInfo
	if err := json.Unmarshal(lockData, &curLock); err != nil || curLock.PID != os.Getpid() {
		t.Fatalf("expected lock to be held by current PID %d, got: %+v", os.Getpid(), curLock)
	}

	unlock()

	if fileExists(lockPath) {
		t.Fatalf("expected lockfile to be deleted after unlock")
	}
}

func TestAtomicWriteFileOperations(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "nested", "dir", "atomic.txt")

	// 1. Test parent directory auto-creation and permissions
	payload := []byte("hello atomic world")
	if err := AtomicWriteFile(testFile, payload, 0600); err != nil {
		t.Fatalf("AtomicWriteFile failed: %v", err)
	}

	readData, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if !bytes.Equal(readData, payload) {
		t.Fatalf("payload mismatch: expected %q, got %q", payload, readData)
	}

	fi, err := os.Stat(testFile)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	if fi.Mode().Perm() != 0600 {
		t.Fatalf("expected perm 0600, got %v", fi.Mode().Perm())
	}

	// 2. Test concurrent atomic overwrites without partial reads
	concurrentFile := filepath.Join(dir, "concurrent_atomic.txt")
	payloadA := bytes.Repeat([]byte("A"), 4096)
	payloadB := bytes.Repeat([]byte("B"), 4096)

	if err := AtomicWriteFile(concurrentFile, payloadA, 0644); err != nil {
		t.Fatal(err)
	}

	stopCh := make(chan struct{})
	var wg sync.WaitGroup

	// 4 writer goroutines
	for i := range 4 {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()
			for {
				select {
				case <-stopCh:
					return
				default:
					p := payloadA
					if writerID%2 == 0 {
						p = payloadB
					}
					_ = AtomicWriteFile(concurrentFile, p, 0644)
				}
			}
		}(i)
	}

	// 4 reader goroutines
	readErrCh := make(chan error, 10)
	for range 4 {
		wg.Go(func() {
			for {
				select {
				case <-stopCh:
					return
				default:
					data, err := os.ReadFile(concurrentFile)
					if err != nil {
						readErrCh <- fmt.Errorf("read error: %w", err)
						return
					}
					// Must be exactly 4096 bytes and either all A or all B
					if len(data) != 4096 {
						readErrCh <- fmt.Errorf("torn read: expected length 4096, got %d", len(data))
						return
					}
					if !bytes.Equal(data, payloadA) && !bytes.Equal(data, payloadB) {
						readErrCh <- fmt.Errorf("corrupted read: data neither all A nor all B")
						return
					}
				}
			}
		})
	}

	time.Sleep(100 * time.Millisecond)
	close(stopCh)
	wg.Wait()

	select {
	case err := <-readErrCh:
		t.Fatalf("concurrent atomic write/read test failed: %v", err)
	default:
	}
}

func TestCorruptedAndZeroByteLockEviction(t *testing.T) {
	t.Run("ZeroByteStaleLockEviction", func(t *testing.T) {
		dir := t.TempDir()
		goalDir := filepath.Join(dir, "goals", "zero-byte-goal")
		if err := os.MkdirAll(goalDir, 0755); err != nil {
			t.Fatal(err)
		}
		lockPath := filepath.Join(goalDir, ".claim.lock")
		if err := os.WriteFile(lockPath, []byte(""), 0600); err != nil {
			t.Fatal(err)
		}
		oldTime := time.Now().Add(-15 * time.Second)
		if err := os.Chtimes(lockPath, oldTime, oldTime); err != nil {
			t.Fatal(err)
		}

		unlock, err := acquireGoalLockWithTimeout(goalDir, 500*time.Millisecond)
		if err != nil {
			t.Fatalf("expected stale 0-byte lock eviction to succeed, got: %v", err)
		}
		unlock()

		if fileExists(lockPath) {
			t.Fatalf("expected lockfile to be removed after unlock")
		}
	})

	t.Run("CorruptedJSONStaleLockEviction", func(t *testing.T) {
		dir := t.TempDir()
		goalDir := filepath.Join(dir, "goals", "corrupted-json-goal")
		if err := os.MkdirAll(goalDir, 0755); err != nil {
			t.Fatal(err)
		}
		lockPath := filepath.Join(goalDir, ".claim.lock")
		if err := os.WriteFile(lockPath, []byte("NOT_A_VALID_JSON_STRING{{{"), 0600); err != nil {
			t.Fatal(err)
		}
		oldTime := time.Now().Add(-20 * time.Second)
		if err := os.Chtimes(lockPath, oldTime, oldTime); err != nil {
			t.Fatal(err)
		}

		unlock, err := acquireGoalLockWithTimeout(goalDir, 500*time.Millisecond)
		if err != nil {
			t.Fatalf("expected stale corrupted JSON lock eviction to succeed, got: %v", err)
		}
		unlock()

		if fileExists(lockPath) {
			t.Fatalf("expected lockfile to be removed after unlock")
		}
	})

	t.Run("InvalidPIDStaleLockEviction", func(t *testing.T) {
		dir := t.TempDir()
		goalDir := filepath.Join(dir, "goals", "invalid-pid-goal")
		if err := os.MkdirAll(goalDir, 0755); err != nil {
			t.Fatal(err)
		}
		lockPath := filepath.Join(goalDir, ".claim.lock")
		invalidLock := lockInfo{
			PID:       0,
			Hostname:  "test-host",
			CreatedAt: time.Now().UTC().Add(-20 * time.Second),
			Token:     "tok",
		}
		data, _ := json.Marshal(invalidLock)
		if err := os.WriteFile(lockPath, data, 0600); err != nil {
			t.Fatal(err)
		}
		oldTime := time.Now().Add(-20 * time.Second)
		if err := os.Chtimes(lockPath, oldTime, oldTime); err != nil {
			t.Fatal(err)
		}

		unlock, err := acquireGoalLockWithTimeout(goalDir, 500*time.Millisecond)
		if err != nil {
			t.Fatalf("expected stale invalid PID lock eviction to succeed, got: %v", err)
		}
		unlock()

		if fileExists(lockPath) {
			t.Fatalf("expected lockfile to be removed after unlock")
		}
	})

	t.Run("FreshZeroByteLockNotEvictedImmediately", func(t *testing.T) {
		dir := t.TempDir()
		goalDir := filepath.Join(dir, "goals", "fresh-zero-byte-goal")
		if err := os.MkdirAll(goalDir, 0755); err != nil {
			t.Fatal(err)
		}
		lockPath := filepath.Join(goalDir, ".claim.lock")
		// Write a 0-byte lock file with current timestamp (writer in flight)
		if err := os.WriteFile(lockPath, []byte(""), 0600); err != nil {
			t.Fatal(err)
		}

		if flockSupported {
			// With advisory locks an unheld file is not a lock: a contender
			// adopts the fresh file immediately.
			unlock, err := acquireGoalLockWithTimeout(goalDir, 500*time.Millisecond)
			if err != nil {
				t.Fatalf("expected adoption of a fresh unheld lock file, got: %v", err)
			}
			unlock()
			return
		}

		// Sentinel fallback: a fresh file may be mid-creation, so acquisition
		// must time out and leave the file on disk.
		_, err := acquireGoalLockWithTimeout(goalDir, 50*time.Millisecond)
		if err == nil {
			t.Fatalf("expected lock acquisition to time out on fresh 0-byte lock")
		}

		// Ensure the lock file is still on disk
		if !fileExists(lockPath) {
			t.Fatalf("fresh lock file should not have been deleted")
		}
	})
}

func TestMetadataParsingYamlFrontmatter(t *testing.T) {
	content := `---
title: "Custom Frontmatter Title"
description: "Frontmatter description text."
sequence: 4
mode: dependent
depends_on:
  - auth-core
  - db-schema
---

# Ignored Header Title

## Ignored Description Header
Some body description that should not override frontmatter.
`

	title, desc, indep, deps, seq, _, _ := parseGoalMetadata(content)

	if title != "Custom Frontmatter Title" {
		t.Errorf("expected title 'Custom Frontmatter Title', got %q", title)
	}
	if desc != "Frontmatter description text." {
		t.Errorf("expected desc 'Frontmatter description text.', got %q", desc)
	}
	if seq != 4 {
		t.Errorf("expected sequence 4, got %d", seq)
	}
	if indep {
		t.Errorf("expected indep to be false")
	}
	expectedDeps := []string{"auth-core", "db-schema"}
	if !reflect.DeepEqual(deps, expectedDeps) {
		t.Errorf("expected deps %v, got %v", expectedDeps, deps)
	}
}

func TestMetadataParsingMarkdownLinks(t *testing.T) {
	content := "# Goal: API Gateway\n\n" +
		"## Goal Description\nReverse proxy and routing tier.\n\n" +
		"## Dependencies & Execution Order\n" +
		"- **Depends On**: [auth-service](goals/auth-service/goal.md), [`db-migration`](../db-migration/goal.md), \"telemetry-agent\"\n" +
		"- **Sequence**: #3\n" +
		"- **Mode**: Dependent\n"

	title, desc, indep, deps, seq, _, _ := parseGoalMetadata(content)

	if title != "API Gateway" {
		t.Errorf("expected title 'API Gateway', got %q", title)
	}
	if desc != "Reverse proxy and routing tier." {
		t.Errorf("expected desc 'Reverse proxy and routing tier.', got %q", desc)
	}
	if seq != 3 {
		t.Errorf("expected sequence 3, got %d", seq)
	}
	if indep {
		t.Errorf("expected indep to be false")
	}
	expectedDeps := []string{"auth-service", "db-migration", "telemetry-agent"}
	if !reflect.DeepEqual(deps, expectedDeps) {
		t.Errorf("expected deps %v, got %v", expectedDeps, deps)
	}
}

func TestDependencyParsingProseRemainder_Issue17(t *testing.T) {
	// Hook to capture warnings
	var capturedWarnings []string
	origWarn := onDependencyWarning
	onDependencyWarning = func(raw string, remainder string) {
		capturedWarnings = append(capturedWarnings, remainder)
	}
	defer func() {
		onDependencyWarning = origWarn
	}()

	t.Run("Issue 17 reproducer - trailing prose after single dependency", func(t *testing.T) {
		capturedWarnings = nil
		content := `# Goal: Live Suite Realignment

## Goal Description
Fix live suite error swallowing.

## Dependencies & Execution Order
- Mode: Dependent (requires prerequisite goals)
- Depends On: cfclean-deploy-hydration (for the fixture path). Both must be accepted before A1.
`
		_, _, indep, deps, _, _, _, _, _, depWarnings := parseGoalMetadataFull(content)
		if indep {
			t.Errorf("expected indep to be false")
		}
		expectedDeps := []string{"cfclean-deploy-hydration"}
		if !reflect.DeepEqual(deps, expectedDeps) {
			t.Errorf("expected deps %v, got %v", expectedDeps, deps)
		}
		expectedRem := "(for the fixture path). Both must be accepted before A1."
		if len(depWarnings) == 0 || !strings.Contains(depWarnings[0], expectedRem) {
			t.Errorf("expected depWarnings to contain %q, got %v", expectedRem, depWarnings)
		}
		if len(capturedWarnings) == 0 || !strings.Contains(capturedWarnings[0], expectedRem) {
			t.Errorf("expected captured warning to contain %q, got %v", expectedRem, capturedWarnings)
		}
	})

	t.Run("Multiple comma-separated dependencies with trailing prose", func(t *testing.T) {
		capturedWarnings = nil
		content := `# Goal: Multi Dep With Prose

## Dependencies & Execution Order
- **Depends On**: cfclean-deploy-hydration, cloudflare-auth-consolidation (both required before cutover)
`
		_, _, indep, deps, _, _, _, _, _, depWarnings := parseGoalMetadataFull(content)
		if indep {
			t.Errorf("expected indep to be false")
		}
		expectedDeps := []string{"cfclean-deploy-hydration", "cloudflare-auth-consolidation"}
		if !reflect.DeepEqual(deps, expectedDeps) {
			t.Errorf("expected deps %v, got %v", expectedDeps, deps)
		}
		expectedRem := "(both required before cutover)"
		if len(depWarnings) == 0 || !strings.Contains(depWarnings[0], expectedRem) {
			t.Errorf("expected depWarnings to contain %q, got %v", expectedRem, depWarnings)
		}
	})

	t.Run("Multiline list items under Depends On with trailing prose", func(t *testing.T) {
		capturedWarnings = nil
		content := `# Goal: Multiline List With Prose

## Dependencies & Execution Order
- **Depends On**:
  - cfclean-deploy-hydration (for fixture path)
  - cloudflare-auth-consolidation
`
		_, _, indep, deps, _, _, _, _, _, depWarnings := parseGoalMetadataFull(content)
		if indep {
			t.Errorf("expected indep to be false")
		}
		expectedDeps := []string{"cfclean-deploy-hydration", "cloudflare-auth-consolidation"}
		if !reflect.DeepEqual(deps, expectedDeps) {
			t.Errorf("expected deps %v, got %v", expectedDeps, deps)
		}
		expectedRem := "(for fixture path)"
		if len(depWarnings) == 0 || !strings.Contains(depWarnings[0], expectedRem) {
			t.Errorf("expected depWarnings to contain %q, got %v", expectedRem, depWarnings)
		}
	})

	t.Run("YAML frontmatter with trailing prose", func(t *testing.T) {
		capturedWarnings = nil
		content := `---
title: Frontmatter Prose Test
depends_on: cfclean-deploy-hydration (for fixture path)
---
# Frontmatter Prose Test
`
		_, _, indep, deps, _, _, _, _, _, depWarnings := parseGoalMetadataFull(content)
		if indep {
			t.Errorf("expected indep to be false")
		}
		expectedDeps := []string{"cfclean-deploy-hydration"}
		if !reflect.DeepEqual(deps, expectedDeps) {
			t.Errorf("expected deps %v, got %v", expectedDeps, deps)
		}
		expectedRem := "(for fixture path)"
		if len(depWarnings) == 0 || !strings.Contains(depWarnings[0], expectedRem) {
			t.Errorf("expected depWarnings to contain %q, got %v", expectedRem, depWarnings)
		}
	})

	t.Run("Clean dependencies emit zero warnings", func(t *testing.T) {
		capturedWarnings = nil
		content := `# Goal: Clean Deps

- **Depends On**: [auth-service](goals/auth-service/goal.md), "cache-layer", db-schema # comment
`
		_, _, _, deps, _, _, _, _, _, depWarnings := parseGoalMetadataFull(content)
		expectedDeps := []string{"auth-service", "cache-layer", "db-schema"}
		if !reflect.DeepEqual(deps, expectedDeps) {
			t.Errorf("expected deps %v, got %v", expectedDeps, deps)
		}
		if len(depWarnings) != 0 {
			t.Errorf("expected 0 depWarnings, got %v", depWarnings)
		}
		if len(capturedWarnings) != 0 {
			t.Errorf("expected 0 capturedWarnings, got %v", capturedWarnings)
		}
	})
}

func TestDependencySlugHelpers(t *testing.T) {
	t.Run("isValidSlug", func(t *testing.T) {
		if !isValidSlug("foo-bar-123") {
			t.Errorf("expected valid slug")
		}
		if isValidSlug("Foo-Bar") {
			t.Errorf("expected invalid slug due to uppercase")
		}
	})

	t.Run("isSpecialDepValue", func(t *testing.T) {
		for _, s := range []string{"", "none", "NONE", "nil", "-", "null", "[]"} {
			if !isSpecialDepValue(s) {
				t.Errorf("expected special value for %q", s)
			}
		}
		if isSpecialDepValue("some-slug") {
			t.Errorf("unexpected special value for some-slug")
		}
	})

	t.Run("isAlphaNum", func(t *testing.T) {
		if !isAlphaNum('a') || !isAlphaNum('Z') || !isAlphaNum('9') {
			t.Errorf("expected true for alphanum")
		}
		if isAlphaNum('-') || isAlphaNum(' ') || isAlphaNum('.') {
			t.Errorf("expected false for non-alphanum")
		}
	})

	t.Run("cleanDepSlug and warnDependencyRemainder", func(t *testing.T) {
		var warned string
		orig := onDependencyWarning
		onDependencyWarning = func(raw string, remainder string) {
			warned = remainder
		}
		defer func() { onDependencyWarning = orig }()

		slug := cleanDepSlug("target-slug (some note)")
		if slug != "target-slug" {
			t.Errorf("expected target-slug, got %q", slug)
		}
		if warned != "(some note)" {
			t.Errorf("expected warned to be (some note), got %q", warned)
		}

		onDependencyWarning = nil
		warnDependencyRemainder("raw", "remainder")
	})

	t.Run("parseDependencySlugTokens edge cases", func(t *testing.T) {
		slugs, rem := parseDependencySlugTokens("")
		if len(slugs) != 0 || rem != "" {
			t.Errorf("expected empty for empty string")
		}
		slugs, rem = parseDependencySlugTokens("none")
		if len(slugs) != 0 || rem != "" {
			t.Errorf("expected empty for none")
		}
	})
}

func TestMetadataParsingHeaderVariants(t *testing.T) {
	testCases := []struct {
		name          string
		content       string
		expectedTitle string
		expectedDesc  string
		expectedDeps  []string
		expectedSeq   int
	}{
		{
			name: "Header aliases with Overview and Prerequisites",
			content: `# Core Engine

## Overview
High-performance query engine.

## Prerequisites
- **Prerequisites**: [storage-engine](goals/storage-engine/goal.md)
- **Sequence**: 1
`,
			expectedTitle: "Core Engine",
			expectedDesc:  "High-performance query engine.",
			expectedDeps:  []string{"storage-engine"},
			expectedSeq:   1,
		},
		{
			name: "Summary header with dependencies alias",
			content: `## Goal: Web UI

## Summary
Interactive dashboard.

- **Dependencies**: backend-api
- **Sequence**: #5
`,
			expectedTitle: "Web UI",
			expectedDesc:  "Interactive dashboard.",
			expectedDeps:  []string{"backend-api"},
			expectedSeq:   5,
		},
		{
			name: "Title with sequence index in title",
			content: `# #2 Background Worker

## Description
Processes async jobs.
`,
			expectedTitle: "#2 Background Worker",
			expectedDesc:  "Processes async jobs.",
			expectedSeq:   2,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			title, desc, _, deps, seq, _, _ := parseGoalMetadata(tc.content)
			if title != tc.expectedTitle {
				t.Errorf("expected title %q, got %q", tc.expectedTitle, title)
			}
			if desc != tc.expectedDesc {
				t.Errorf("expected desc %q, got %q", tc.expectedDesc, desc)
			}
			if seq != tc.expectedSeq {
				t.Errorf("expected seq %d, got %d", tc.expectedSeq, seq)
			}
			if len(tc.expectedDeps) > 0 && !reflect.DeepEqual(deps, tc.expectedDeps) {
				t.Errorf("expected deps %v, got %v", tc.expectedDeps, deps)
			}
		})
	}
}

func TestMetadataParsing_YamlFrontmatterInlineComments(t *testing.T) {
	content := `---
title: "Complex Auth Architecture" # inline title comment
description: "Handles JWT tokens and active sessions." # inline description comment
sequence: 3 # priority sequence order
mode: dependent # requires database
depends_on: # dependencies list below
  - db-schema # core database schema migration
  - redis-cache # distributed cache layer
---

# Ignored Markdown Header

## Ignored Description Header
Should not override frontmatter fields.
`

	title, desc, indep, deps, seq, _, _ := parseGoalMetadata(content)

	if title != "Complex Auth Architecture" {
		t.Errorf("expected title 'Complex Auth Architecture', got %q", title)
	}
	if desc != "Handles JWT tokens and active sessions." {
		t.Errorf("expected desc 'Handles JWT tokens and active sessions.', got %q", desc)
	}
	if seq != 3 {
		t.Errorf("expected sequence 3, got %d", seq)
	}
	if indep {
		t.Errorf("expected indep to be false")
	}
	expectedDeps := []string{"db-schema", "redis-cache"}
	if !reflect.DeepEqual(deps, expectedDeps) {
		t.Errorf("expected deps %v, got %v", expectedDeps, deps)
	}

	// Test bracketed inline comments format
	contentBracketed := `---
title: 'Single Quoted # Title' # comment after quote
sequence: #5 # comment with hash in value
depends_on: [db-schema, redis-cache] # inline array comment
---
`
	title2, _, _, deps2, seq2, _, _ := parseGoalMetadata(contentBracketed)
	if title2 != "Single Quoted # Title" {
		t.Errorf("expected title 'Single Quoted # Title', got %q", title2)
	}
	if seq2 != 5 {
		t.Errorf("expected seq 5, got %d", seq2)
	}
	if !reflect.DeepEqual(deps2, expectedDeps) {
		t.Errorf("expected deps %v, got %v", expectedDeps, deps2)
	}
}

func TestMetadataParsing_RelativeMarkdownLinks(t *testing.T) {
	content := `# Goal: Relative Link Verification

## Goal Description
Tests relative path markdown links in dependency list.

## Dependencies
- [Prerequisite 1](../auth-service/goal.md)
- [Deep Prerequisite](../../database-schema/goal.md)
- [Local Prerequisite](./redis-cache/goal.md)
- [` + "`Storage Service`" + `](goals/storage-service/goal.md)
- [URL With Fragment](../event-bus/goal.md#done)
`

	_, _, indep, deps, _, _, _ := parseGoalMetadata(content)
	if indep {
		t.Errorf("expected independent to be false")
	}
	expectedDeps := []string{
		"auth-service",
		"database-schema",
		"redis-cache",
		"storage-service",
		"event-bus",
	}
	if !reflect.DeepEqual(deps, expectedDeps) {
		t.Fatalf("expected deps %v, got %v", expectedDeps, deps)
	}
}

func TestMetadataParsing_MultilineMarkdownBulletLists(t *testing.T) {
	testCases := []struct {
		name         string
		content      string
		expectedDeps []string
	}{
		{
			name: "Requires section with multiline bullets",
			content: `# Goal: Analytics Worker

## Description
Processes telemetry streams.

- **Requires**:
  - [auth-service](../auth-service/goal.md)
  - ` + "`db-schema`" + `
  - "redis-cache"
  - metrics-collector # telemetry agent

- **Sequence**: 4
`,
			expectedDeps: []string{"auth-service", "db-schema", "redis-cache", "metrics-collector"},
		},
		{
			name: "Depends On section with multiline bullets",
			content: `# Goal: Dashboard Frontend

## Description
User interface dashboard.

- **Depends On**:
  - auth-service # user authentication
  - api-gateway # backend routing
  - web-assets

## Next Section
`,
			expectedDeps: []string{"auth-service", "api-gateway", "web-assets"},
		},
		{
			name: "Section header ## Prerequisites with multiline list",
			content: `# Goal: Payment Gateway

## Overview
Processes credit card transactions.

## Prerequisites
- [billing-db](../billing-db/goal.md)
- [fraud-detection](../fraud-detection/goal.md)
- audit-logger

## Done Condition
All tests pass.
`,
			expectedDeps: []string{"billing-db", "fraud-detection", "audit-logger"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, indep, deps, _, _, _ := parseGoalMetadata(tc.content)
			if indep {
				t.Errorf("expected independent to be false")
			}
			if !reflect.DeepEqual(deps, tc.expectedDeps) {
				t.Fatalf("expected deps %v, got %v (len %d vs %d)", tc.expectedDeps, deps, len(tc.expectedDeps), len(deps))
			}
		})
	}
}

func TestMetadataParsing_NoneAndNilHandling(t *testing.T) {
	cases := []string{
		"---\ntitle: \"Goal A\"\ndepends_on: none\n---\n# Goal A\n",
		"---\ntitle: \"Goal B\"\ndepends_on: nil\n---\n# Goal B\n",
		"---\ntitle: \"Goal C\"\ndepends_on: -\n---\n# Goal C\n",
		"# Goal D\n\n- **Depends On**: none\n",
		"# Goal E\n\n- **Prerequisites**: nil\n",
		"# Goal F\n\n## Prerequisites\n- None\n",
	}

	for i, c := range cases {
		_, _, indep, deps, _, _, _ := parseGoalMetadata(c)
		if !indep {
			t.Errorf("case %d: expected independent to be true, got false", i)
		}
		if len(deps) != 0 {
			t.Errorf("case %d: expected 0 deps, got %v", i, deps)
		}
	}
}

func TestMetadataParsing_AdvancedEdgeCases(t *testing.T) {
	// 1. Asterisk bullets, backslash paths, and deep relative links
	contentAsterisk := `# Goal: Asterisk Bullets and Windows Paths

## Blocked By
* [Core API](..\core-api\goal.md)
* [Deep Worker](../../../deep-worker/goal.md)
* bare-slug-item # inline comment on asterisk bullet

## Next Section
`
	_, _, indep1, deps1, _, _, _ := parseGoalMetadata(contentAsterisk)
	if indep1 {
		t.Errorf("expected indep to be false")
	}
	expected1 := []string{"core-api", "deep-worker", "bare-slug-item"}
	if !reflect.DeepEqual(deps1, expected1) {
		t.Errorf("expected %v, got %v", expected1, deps1)
	}

	// 2. Single-line comma-separated with trailing comment
	contentComma := `# Goal: Comma Inline

- **Depends On**: service-one, service-two, service-three # comma separated with comment
`
	_, _, indep2, deps2, _, _, _ := parseGoalMetadata(contentComma)
	if indep2 {
		t.Errorf("expected indep to be false")
	}
	expected2 := []string{"service-one", "service-two", "service-three"}
	if !reflect.DeepEqual(deps2, expected2) {
		t.Errorf("expected %v, got %v", expected2, deps2)
	}

	// 3. Escaped quotes and mixed comment quotes in frontmatter
	contentEscaped := `---
title: "Escaped \"Quotes\" Title" # comment with 'single' and "double" quotes
description: 'Single "Quote" Description' # another # comment
depends_on: [cache-layer, auth-layer] # list comment
---
# Body
`
	title3, desc3, indep3, deps3, _, _, _ := parseGoalMetadata(contentEscaped)
	if title3 != `Escaped \"Quotes\" Title` && title3 != `Escaped "Quotes" Title` {
		t.Errorf("unexpected title: %q", title3)
	}
	if desc3 != `Single "Quote" Description` {
		t.Errorf("unexpected desc: %q", desc3)
	}
	if indep3 {
		t.Errorf("expected indep to be false")
	}
	expected3 := []string{"cache-layer", "auth-layer"}
	if !reflect.DeepEqual(deps3, expected3) {
		t.Errorf("expected %v, got %v", expected3, deps3)
	}
}

func TestPrioritySortingTierOverSequence(t *testing.T) {
	dir := t.TempDir()

	// Goal 1: Implemented, Sequence: 1
	setupGoalWithMeta(t, dir, "goal-implemented-1", "Implemented 1", "", 1)
	// Goal 2: Ready Pending, Sequence: 2
	setupGoalWithMeta(t, dir, "goal-ready-2", "Ready 2", "", 2)
	// Goal 3: Ready Pending, Sequence: 0
	setupGoalWithMeta(t, dir, "goal-ready-0", "Ready 0", "", 0)
	// Goal 4: Blocked Pending, Sequence: 1 (Blocked by goal-ready-2)
	setupGoalWithMeta(t, dir, "goal-blocked-1", "Blocked 1", "goal-ready-2", 1)

	// Simulate commit for goal-implemented-1
	git(t, dir, "init")
	git(t, dir, "config", "user.email", "test@example.com")
	git(t, dir, "config", "user.name", "Test")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "feat: implement goal 1 (goals/goal-implemented-1/goal.md)")

	res, err := ScanGoals(dir)
	if err != nil {
		t.Fatalf("ScanGoals failed: %v", err)
	}

	if len(res.Goals) != 4 {
		t.Fatalf("expected 4 goals, got %d", len(res.Goals))
	}

	// Expected Tiered Order:
	// 0: goal-ready-2 (Tier 1 Ready, Seq 2)
	// 1: goal-ready-0 (Tier 1 Ready, Seq 0)
	// 2: goal-blocked-1 (Tier 3 Blocked, Seq 1)
	// 3: goal-implemented-1 (Tier 5 Implemented, Seq 1)
	expectedOrder := []string{
		"goal-ready-2",
		"goal-ready-0",
		"goal-blocked-1",
		"goal-implemented-1",
	}

	var actualOrder []string
	for _, g := range res.Goals {
		actualOrder = append(actualOrder, g.Slug)
	}

	if !reflect.DeepEqual(actualOrder, expectedOrder) {
		t.Fatalf("expected tiered priority order %v, got %v", expectedOrder, actualOrder)
	}
}

func setupGoalWithMeta(t *testing.T, dir, slug, title, dep string, seq int) {
	t.Helper()
	gDir := filepath.Join(dir, "goals", slug)
	if err := os.MkdirAll(gDir, 0755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Goal: %s\n\n", title))
	b.WriteString("## Goal Description\nDescription for " + title + "\n\n")
	if dep != "" {
		b.WriteString(fmt.Sprintf("- **Depends On**: %s\n", dep))
	} else {
		b.WriteString("- **Depends On**: none\n")
	}
	if seq > 0 {
		b.WriteString(fmt.Sprintf("- **Sequence**: %d\n", seq))
	}
	if err := os.WriteFile(filepath.Join(gDir, "goal.md"), []byte(b.String()), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gDir, "plan.md"), []byte("# Plan\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gDir, "facts.md"), []byte("# Facts\n"), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestDeepTransitiveUnblockingLinear(t *testing.T) {
	dir := t.TempDir()

	// A -> B -> C -> D
	setupGoalWithMeta(t, dir, "step-a", "Step A", "", 1)
	setupGoalWithMeta(t, dir, "step-b", "Step B", "step-a", 2)
	setupGoalWithMeta(t, dir, "step-c", "Step C", "step-b", 3)
	setupGoalWithMeta(t, dir, "step-d", "Step D", "step-c", 4)

	git(t, dir, "init")
	git(t, dir, "config", "user.email", "test@example.com")
	git(t, dir, "config", "user.name", "Test")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "chore: init repo")

	// Phase 0: All pending. A is Ready, B/C/D are Blocked.
	res0, _ := ScanGoals(dir)
	if res0.ReadyCount != 1 || res0.BlockedCount != 3 {
		t.Fatalf("Phase 0 failed: expected 1 ready, 3 blocked; got ready=%d, blocked=%d", res0.ReadyCount, res0.BlockedCount)
	}

	// Verify RootBlockers for D is step-a
	for _, g := range res0.Goals {
		if g.Slug == "step-d" {
			if !reflect.DeepEqual(g.RootBlockers, []string{"step-a"}) {
				t.Fatalf("expected RootBlockers for step-d to be ['step-a'], got %v", g.RootBlockers)
			}
		}
	}

	// Phase 1: Implement Step A
	git(t, dir, "commit", "--allow-empty", "-m", "feat: implement step a (goals/step-a/goal.md)")
	res1, _ := ScanGoals(dir)
	if res1.ImplementedCount != 1 || res1.ReadyCount != 1 || res1.BlockedCount != 2 {
		t.Fatalf("Phase 1 failed: expected 1 impl, 1 ready, 2 blocked; got impl=%d, ready=%d, blocked=%d",
			res1.ImplementedCount, res1.ReadyCount, res1.BlockedCount)
	}
	for _, g := range res1.Goals {
		if g.Slug == "step-b" && g.Blocked {
			t.Fatalf("step-b should be unblocked after step-a implementation")
		}
		if g.Slug == "step-d" {
			if !reflect.DeepEqual(g.RootBlockers, []string{"step-b"}) {
				t.Fatalf("expected RootBlockers for step-d to be ['step-b'], got %v", g.RootBlockers)
			}
		}
	}

	// Phase 2: Implement Step B
	git(t, dir, "commit", "--allow-empty", "-m", "feat: implement step b (goals/step-b/goal.md)")
	res2, _ := ScanGoals(dir)
	if res2.ImplementedCount != 2 || res2.ReadyCount != 1 || res2.BlockedCount != 1 {
		t.Fatalf("Phase 2 failed: expected 2 impl, 1 ready, 1 blocked; got impl=%d, ready=%d, blocked=%d",
			res2.ImplementedCount, res2.ReadyCount, res2.BlockedCount)
	}

	// Phase 3: Implement Step C
	git(t, dir, "commit", "--allow-empty", "-m", "feat: implement step c (goals/step-c/goal.md)")
	res3, _ := ScanGoals(dir)
	if res3.ImplementedCount != 3 || res3.ReadyCount != 1 || res3.BlockedCount != 0 {
		t.Fatalf("Phase 3 failed: expected 3 impl, 1 ready, 0 blocked; got impl=%d, ready=%d, blocked=%d",
			res3.ImplementedCount, res3.ReadyCount, res3.BlockedCount)
	}
	for _, g := range res3.Goals {
		if g.Slug == "step-d" && g.Blocked {
			t.Fatalf("step-d should be completely unblocked now")
		}
	}
}

func TestClaimBlockedGoalRejectionAndForce(t *testing.T) {
	dir := t.TempDir()

	setupGoalWithMeta(t, dir, "base-infra", "Base Infra", "", 1)
	setupGoalWithMeta(t, dir, "app-service", "App Service", "base-infra", 2)

	// 1. Claiming app-service without force should fail because base-infra is not implemented
	_, err := ClaimGoal(dir, "app-service", "agent-1", "attempting work", false)
	if err == nil {
		t.Fatal("expected error claiming blocked goal without --force")
	}
	expectedErr := `cannot claim blocked goal "app-service": unmet prerequisites: [base-infra] (use --force to override)`
	if err.Error() != expectedErr {
		t.Fatalf("expected exact error %q, got: %q", expectedErr, err.Error())
	}

	// 2. Claiming app-service with force should succeed
	claim, err := ClaimGoal(dir, "app-service", "agent-1", "forced takeover", true)
	if err != nil || claim == nil {
		t.Fatalf("expected forced claim to succeed, got %+v (err: %v)", claim, err)
	}
}

func TestClaimBlockedGoalRejectionWithRootBlockers(t *testing.T) {
	dir := t.TempDir()

	// Goal Chain: goal-c -> goal-b -> goal-a
	setupGoalWithMeta(t, dir, "goal-a", "Goal A", "", 1)
	setupGoalWithMeta(t, dir, "goal-b", "Goal B", "goal-a", 2)
	setupGoalWithMeta(t, dir, "goal-c", "Goal C", "goal-b", 3)

	// 1. Claiming goal-c without force should fail and surface direct unmet dep (goal-b) and root blocker (goal-a)
	_, err := ClaimGoal(dir, "goal-c", "agent-1", "attempting work", false)
	if err == nil {
		t.Fatal("expected error claiming blocked goal-c without --force")
	}
	expectedErr := `cannot claim blocked goal "goal-c": unmet prerequisites: [goal-b] (root blockers: [goal-a]) (use --force to override)`
	if err.Error() != expectedErr {
		t.Fatalf("expected exact error %q, got: %q", expectedErr, err.Error())
	}

	// 2. Claiming goal-c with force=true should succeed
	claim, err := ClaimGoal(dir, "goal-c", "agent-1", "forced takeover", true)
	if err != nil || claim == nil {
		t.Fatalf("expected forced claim to succeed, got %+v (err: %v)", claim, err)
	}
	if claim.GoalSlug != "goal-c" || claim.Agent != "agent-1" {
		t.Fatalf("unexpected claim details: %+v", claim)
	}
}

func TestClaimBlockedGoalRejectionCircularDependency(t *testing.T) {
	dir := t.TempDir()

	// Cycle: goal-x -> goal-y -> goal-x
	setupGoalWithMeta(t, dir, "goal-x", "Goal X", "goal-y", 1)
	setupGoalWithMeta(t, dir, "goal-y", "Goal Y", "goal-x", 2)

	// 1. Claiming goal-x without force should fail reporting the circular dependency
	_, err := ClaimGoal(dir, "goal-x", "agent-1", "attempting work", false)
	if err == nil {
		t.Fatal("expected error claiming circular blocked goal without --force")
	}
	if !strings.Contains(err.Error(), `cannot claim blocked goal "goal-x": circular dependency:`) || !strings.Contains(err.Error(), "(use --force to override)") {
		t.Fatalf("expected circular dependency rejection error, got: %v", err)
	}

	// 2. Claiming goal-x with force=true should succeed
	claim, err := ClaimGoal(dir, "goal-x", "agent-1", "forced takeover", true)
	if err != nil || claim == nil {
		t.Fatalf("expected forced claim to succeed, got %+v (err: %v)", claim, err)
	}
}

func TestCreateGoalCyclePrevention(t *testing.T) {
	dir := t.TempDir()

	// 1. Create Goal A
	_, err := CreateGoal(CreateGoalParams{
		RootDir: dir,
		Slug:    "goal-alpha",
		Title:   "Goal Alpha",
	})
	if err != nil {
		t.Fatal(err)
	}

	// 2. Create Goal B depending on A
	_, err = CreateGoal(CreateGoalParams{
		RootDir:   dir,
		Slug:      "goal-beta",
		Title:     "Goal Beta",
		DependsOn: []string{"goal-alpha"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 3. Update Goal A to depend on B (should fail due to cycle)
	_, err = CreateGoal(CreateGoalParams{
		RootDir:   dir,
		Slug:      "goal-alpha",
		Title:     "Goal Alpha",
		DependsOn: []string{"goal-beta"},
		Overwrite: true,
	})
	if err == nil {
		t.Fatal("expected error when creating circular dependency")
	}
	if !strings.Contains(err.Error(), "circular dependency") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestCycleDetectionInScanGoals(t *testing.T) {
	dir := t.TempDir()

	setupGoalWithMeta(t, dir, "cycle-a", "Cycle A", "cycle-b", 1)
	setupGoalWithMeta(t, dir, "cycle-b", "Cycle B", "cycle-a", 2)

	res, err := ScanGoals(dir)
	if err != nil {
		t.Fatalf("ScanGoals failed: %v", err)
	}

	if len(res.Cycles) == 0 {
		t.Fatal("expected cycles to be detected in ScanGoals")
	}

	for _, g := range res.Goals {
		if !g.Blocked {
			t.Errorf("expected cyclic goal %s to be blocked", g.Slug)
		}
		if !strings.Contains(g.BlockedReason, "circular dependency") {
			t.Errorf("expected circular dependency BlockedReason for %s, got %q", g.Slug, g.BlockedReason)
		}
	}
}

func TestMetadataParsing_MarkdownDependencyScannerRemediation(t *testing.T) {
	testCases := []struct {
		name         string
		content      string
		expectedDeps []string
		expectedInd  bool
		expectedSeq  int
	}{
		{
			name: "Nested multiline subheader under section header",
			content: `# Goal: Nested Multiline
## Dependencies
- **Depends On**:
  - auth-core
  - db-schema
`,
			expectedDeps: []string{"auth-core", "db-schema"},
			expectedInd:  false,
			expectedSeq:  0,
		},
		{
			name: "Nested inline subheader under section header",
			content: `# Goal: Nested Inline
## Prerequisites
- **Requires**: auth-service, db-schema, redis-cache
`,
			expectedDeps: []string{"auth-service", "db-schema", "redis-cache"},
			expectedInd:  false,
			expectedSeq:  0,
		},
		{
			name: "Multiple inline subheaders under section header with asterisk",
			content: `# Goal: Multiple Inline Subheaders
## Blocked By
* **Requires**: auth-service, db-schema
- **Blocked By**: queue-worker
`,
			expectedDeps: []string{"auth-service", "db-schema", "queue-worker"},
			expectedInd:  false,
			expectedSeq:  0,
		},
		{
			name: "Inline subheader with None or Nil values",
			content: `# Goal: None and Nil Subheaders
## Prerequisites
- **Requires**: none
- **Blocked By**: nil
- **Sequence**: 5
`,
			expectedDeps: nil,
			expectedInd:  true,
			expectedSeq:  5,
		},
		{
			name: "Thematic break termination",
			content: `# Goal: Thematic Break
## Dependencies
- dep-1
- dep-2
---
## Other Section
- item-not-a-dep
`,
			expectedDeps: []string{"dep-1", "dep-2"},
			expectedInd:  false,
			expectedSeq:  0,
		},
		{
			name: "Property bullet termination",
			content: `# Goal: Property Bullet Termination
- **Depends On**:
  - service-a
  - service-b
- **Sequence**: 8
- **Mode**: Dependent
`,
			expectedDeps: []string{"service-a", "service-b"},
			expectedInd:  false,
			expectedSeq:  8,
		},
		{
			name: "Alien header termination followed by list",
			content: `# Goal: Alien Header Termination
## Prerequisites
- worker-alpha
- worker-beta

## Success Criteria
- [ ] Task 1
- [ ] Task 2
`,
			expectedDeps: []string{"worker-alpha", "worker-beta"},
			expectedInd:  false,
			expectedSeq:  0,
		},
		{
			name: "Dependency deduplication",
			content: `# Goal: Deduplication
## Dependencies
- **Requires**: auth-service, db-schema
- auth-service
- db-schema
- cache-service
`,
			expectedDeps: []string{"auth-service", "db-schema", "cache-service"},
			expectedInd:  false,
			expectedSeq:  0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, ind, deps, seq, _, _ := parseGoalMetadata(tc.content)
			if ind != tc.expectedInd {
				t.Errorf("expected independent=%v, got %v", tc.expectedInd, ind)
			}
			if seq != tc.expectedSeq {
				t.Errorf("expected sequence=%d, got %d", tc.expectedSeq, seq)
			}
			if len(tc.expectedDeps) == 0 && len(deps) != 0 {
				t.Errorf("expected 0 deps, got %v", deps)
			} else if len(tc.expectedDeps) > 0 && !reflect.DeepEqual(deps, tc.expectedDeps) {
				t.Errorf("expected deps %v, got %v", tc.expectedDeps, deps)
			}
		})
	}
}

func TestClaimIdempotencyAndGitTrailers(t *testing.T) {
	tmp := t.TempDir()

	// Scaffold goal
	_, err := CreateGoal(CreateGoalParams{
		RootDir: tmp,
		Slug:    "idem-goal",
		Title:   "Idempotency Goal",
	})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}

	// 1. Agent @devin claims the goal
	c1, err := ClaimGoal(tmp, "idem-goal", "@devin", "initial pass", false)
	if err != nil {
		t.Fatalf("first claim failed: %v", err)
	}
	if c1.ClaimedBy != "devin" || c1.Agent != "devin" {
		t.Fatalf("expected agent devin, got %s", c1.ClaimedBy)
	}

	// 2. Same agent re-claims without force (idempotency)
	c2, err := ClaimGoal(tmp, "idem-goal", "devin", "updated pass", false)
	if err != nil {
		t.Fatalf("idempotent claim failed: %v", err)
	}
	if c2.Note != "updated pass" {
		t.Fatalf("expected updated note 'updated pass', got %s", c2.Note)
	}
	if !c2.ClaimedAt.Equal(c1.ClaimedAt) {
		t.Fatalf("expected ClaimedAt preserved across idempotent claims, got %v vs %v", c2.ClaimedAt, c1.ClaimedAt)
	}

	// 3. Different agent attempts to claim without force -> must fail
	_, err = ClaimGoal(tmp, "idem-goal", "antigravity", "taking over", false)
	if err == nil {
		t.Fatalf("expected claim by different agent to fail without force")
	}
	if !strings.Contains(err.Error(), "already claimed by \"devin\"") {
		t.Fatalf("expected error message to mention devin, got: %v", err)
	}

	// 4. Git trailer recognition
	text1 := "feat(core): implement feature\n\nGoal-Ref: idem-goal\n"
	text2 := "fix(core): resolve edge case\n\nSila-Goal: goals/idem-goal/goal.md\n"
	text3 := "perf(core): speed up loop\n\nFixes-Goal: idem-goal\n"

	for _, text := range []string{text1, text2, text3} {
		matches := trailerRE.FindAllStringSubmatch(text, -1)
		if len(matches) == 0 || len(matches[0]) < 2 || matches[0][1] != "idem-goal" {
			t.Errorf("expected trailer match for 'idem-goal' in text: %q, got: %v", text, matches)
		}
	}
}

func TestParseTaskIndices(t *testing.T) {
	// Standard numbers
	indices, err := ParseTaskIndices([]string{"1", "2", "3"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(indices, []int{1, 2, 3}) {
		t.Fatalf("expected [1 2 3], got %v", indices)
	}

	// Comma separated
	indices, err = ParseTaskIndices([]string{"1,2,4"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(indices, []int{1, 2, 4}) {
		t.Fatalf("expected [1 2 4], got %v", indices)
	}

	// Range
	indices, err = ParseTaskIndices([]string{"1-3", "5", "7-8"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(indices, []int{1, 2, 3, 5, 7, 8}) {
		t.Fatalf("expected [1 2 3 5 7 8], got %v", indices)
	}

	// Duplicates and unsorted input
	indices, err = ParseTaskIndices([]string{"3,1", "2-3"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(indices, []int{1, 2, 3}) {
		t.Fatalf("expected sorted deduplicated [1 2 3], got %v", indices)
	}

	// Invalid inputs
	if _, err := ParseTaskIndices([]string{}); err == nil {
		t.Fatal("expected error on empty input")
	}
	if _, err := ParseTaskIndices([]string{"0"}); err == nil {
		t.Fatal("expected error on 0 index")
	}
	if _, err := ParseTaskIndices([]string{"5-2"}); err == nil {
		t.Fatal("expected error on reverse range")
	}
	if _, err := ParseTaskIndices([]string{"abc"}); err == nil {
		t.Fatal("expected error on non-integer")
	}
}

func TestBatchToggleGoalTasks(t *testing.T) {
	tmp := t.TempDir()
	goalDir := filepath.Join(tmp, "goals", "batch-goal")
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte("# Batch Goal\n"), 0644); err != nil {
		t.Fatal(err)
	}
	planContent := `# Plan
- [ ] Task 1: Setup
- [ ] Task 2: Core implementation
- [ ] Task 3: Unit tests
- [ ] Task 4: Documentation
`
	if err := os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte(planContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Toggle multiple tasks: check 1, 2, 4
	tasks, err := ToggleGoalTasks(tmp, "batch-goal", []int{1, 2, 4}, true)
	if err != nil {
		t.Fatalf("ToggleGoalTasks failed: %v", err)
	}
	if len(tasks) != 3 {
		t.Fatalf("expected 3 tasks returned, got %d", len(tasks))
	}
	for _, tk := range tasks {
		if !tk.Completed {
			t.Errorf("expected task %d to be completed", tk.Index)
		}
	}

	// Verify plan.md contents
	data, _ := os.ReadFile(filepath.Join(goalDir, "plan.md"))
	sData := string(data)
	if !strings.Contains(sData, "- [x] Task 1: Setup") ||
		!strings.Contains(sData, "- [x] Task 2: Core implementation") ||
		!strings.Contains(sData, "- [ ] Task 3: Unit tests") ||
		!strings.Contains(sData, "- [x] Task 4: Documentation") {
		t.Fatalf("unexpected plan.md content after batch check: %s", sData)
	}

	// Now uncheck tasks 1 and 4
	tasks, err = ToggleGoalTasks(tmp, "batch-goal", []int{1, 4}, false)
	if err != nil {
		t.Fatalf("ToggleGoalTasks uncheck failed: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks returned on uncheck, got %d", len(tasks))
	}
	data, _ = os.ReadFile(filepath.Join(goalDir, "plan.md"))
	sData = string(data)
	if !strings.Contains(sData, "- [ ] Task 1: Setup") ||
		!strings.Contains(sData, "- [x] Task 2: Core implementation") ||
		!strings.Contains(sData, "- [ ] Task 3: Unit tests") ||
		!strings.Contains(sData, "- [ ] Task 4: Documentation") {
		t.Fatalf("unexpected plan.md content after uncheck: %s", sData)
	}
}

func TestStructuredStatusReportAndTelemetry(t *testing.T) {
	tmp := t.TempDir()
	goalDir := filepath.Join(tmp, "goals", "status-goal")
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte("# Status Goal\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte("- [ ] Task 1: In flight\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Claim
	_, err := ClaimGoal(tmp, "status-goal", "claude", "initial claim", false)
	if err != nil {
		t.Fatalf("ClaimGoal failed: %v", err)
	}

	// Report with structured status: blocked
	claim, err := ReportGoal(tmp, "status-goal", "waiting on vendor API", "blocked")
	if err != nil {
		t.Fatalf("ReportGoal failed: %v", err)
	}
	if claim.Status != "blocked" {
		t.Fatalf("expected claim status 'blocked', got %q", claim.Status)
	}
	if claim.Note != "waiting on vendor API" {
		t.Fatalf("expected note 'waiting on vendor API', got %q", claim.Note)
	}

	// Telemetry verification
	ui := render.New(os.Stdout, true)
	sum, _ := FormatProjectGoalsTelemetry(ui, tmp)
	if len(sum.ActiveWorkers) == 0 {
		t.Fatal("expected at least 1 active worker in telemetry")
	}
	worker := sum.ActiveWorkers[0]
	if !strings.Contains(worker, "BLOCKED: waiting on vendor API") {
		t.Errorf("expected worker to format BLOCKED status in telemetry, got: %s", worker)
	}

	// Sync README and verify status prefix in table
	res, err := ScanGoals(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if err := SyncReadmeWithOpts(tmp, res, SyncReadmeOptions{}); err != nil {
		t.Fatal(err)
	}
	readme, _ := os.ReadFile(filepath.Join(tmp, "goals", "README.md"))
	if !strings.Contains(string(readme), "[BLOCKED] waiting on vendor API") {
		t.Errorf("expected README to contain '[BLOCKED] waiting on vendor API', got: %s", string(readme))
	}
}

func TestReadableDecisionTelemetry(t *testing.T) {
	tmp := t.TempDir()
	ui := render.New(os.Stdout, true)

	// Goal 1: ready goal
	g1Dir := filepath.Join(tmp, "goals", "ready-goal")
	_ = os.MkdirAll(g1Dir, 0755)
	_ = os.WriteFile(filepath.Join(g1Dir, "goal.md"), []byte("# Ready Goal\n"), 0644)
	_ = os.WriteFile(filepath.Join(g1Dir, "plan.md"), []byte("- [ ] Task 1: Initialize\n"), 0644)
	_ = os.WriteFile(filepath.Join(g1Dir, "facts.md"), []byte("# Facts\n"), 0644)

	// Goal 2: active claimed goal
	g2Dir := filepath.Join(tmp, "goals", "active-goal")
	_ = os.MkdirAll(g2Dir, 0755)
	_ = os.WriteFile(filepath.Join(g2Dir, "goal.md"), []byte("# Active Goal\n"), 0644)
	_ = os.WriteFile(filepath.Join(g2Dir, "plan.md"), []byte("- [ ] Task 1: Working step\n"), 0644)
	_ = os.WriteFile(filepath.Join(g2Dir, "facts.md"), []byte("# Facts\n"), 0644)
	_, _ = ClaimGoal(tmp, "active-goal", "antigravity", "running tests", false)

	sum, _ := FormatProjectGoalsTelemetry(ui, tmp)
	if sum == nil || !sum.HasGoals {
		t.Fatal("expected telemetry to detect goals")
	}

	// 1. Verify RoadmapLine has NO active worker text (banner deduplication)
	if strings.Contains(sum.RoadmapLine, "Worker") || strings.Contains(sum.RoadmapLine, "@antigravity") {
		t.Fatalf("RoadmapLine must not duplicate worker text, got: %s", sum.RoadmapLine)
	}

	// 2. Verify RoadmapPulse
	pulse := sum.RoadmapPulse()
	if !strings.Contains(pulse, "1 active") || !strings.Contains(pulse, "1 ready") {
		t.Fatalf("expected RoadmapPulse to show active and ready counts, got: %s", pulse)
	}

	// 3. Verify ActionPulse with active worker
	action := sum.ActionPulse(ui)
	if !strings.Contains(action, "@antigravity on active-goal") {
		t.Fatalf("expected ActionPulse to report active worker, got: %s", action)
	}

	// 4. Release active goal and verify ActionPulse reports Next Up
	_ = ReleaseGoal(tmp, "active-goal")
	sum2, _ := FormatProjectGoalsTelemetry(ui, tmp)
	action2 := sum2.ActionPulse(ui)
	if !strings.Contains(action2, "Next up:") {
		t.Fatalf("expected ActionPulse to report Next up when no active workers, got: %s", action2)
	}

	// 5. Verify DecisionHint
	if sum2.NextReadyGoal == "" || sum2.DecisionHint == "" {
		t.Fatalf("expected NextReadyGoal and DecisionHint to be populated, got: %+v", sum2)
	}
}

func TestReopenGoalLifecycle(t *testing.T) {
	tmp := t.TempDir()

	// Initialize temp git repository
	runGit := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = tmp
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s failed: %v (output: %s)", strings.Join(args, " "), err, string(out))
		}
		return strings.TrimSpace(string(out))
	}

	runGit("init")
	runGit("config", "user.email", "test@example.com")
	runGit("config", "user.name", "Test Committer")

	// Create goal package
	goalDir := filepath.Join(tmp, "goals", "reopen-target")
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte("# Goal: Reopen Target\n\n- Mode: Independent\n"), 0644)
	_ = os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte("# Plan\n- [x] Task 1\n"), 0644)
	_ = os.WriteFile(filepath.Join(goalDir, "facts.md"), []byte("# Facts\n"), 0644)

	// Goal before commit is pending
	audit0, err := ScanGoals(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(audit0.Goals) != 1 || audit0.Goals[0].Status != "pending" {
		t.Fatalf("expected pending goal, got %+v", audit0.Goals)
	}

	// Cannot reopen non-implemented goal
	_, _, err = ReopenGoal(tmp, "reopen-target", "tester", "early reopen", false)
	if err == nil {
		t.Fatal("expected error reopening non-implemented goal, got nil")
	}

	// Commit initial implementation
	runGit("add", ".")
	runGit("commit", "-m", "feat: initial feature (goals/reopen-target/goal.md)")
	origCommit := runGit("rev-parse", "--short", "HEAD")

	audit1, err := ScanGoals(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if audit1.ImplementedCount != 1 || audit1.Goals[0].Status != "implemented" || audit1.Goals[0].Commit != origCommit {
		t.Fatalf("expected implemented goal in commit %s, got: %+v", origCommit, audit1)
	}

	// Reopen goal with claim=true
	rec, cl, err := ReopenGoal(tmp, "reopen-target", "remedy-agent", "critical race condition in auth", true)
	if err != nil {
		t.Fatalf("ReopenGoal failed: %v", err)
	}
	if rec == nil || !rec.Active || rec.OriginalCommit != origCommit {
		t.Fatalf("unexpected ReopenRecord: %+v", rec)
	}
	if cl == nil || cl.Agent != "remedy-agent" {
		t.Fatalf("expected claim for remedy-agent, got: %+v", cl)
	}

	// Check idempotency / double reopen guard
	_, _, err = ReopenGoal(tmp, "reopen-target", "another-agent", "duplicate reopen", false)
	if err == nil {
		t.Fatal("expected error on duplicate active reopen, got nil")
	}

	// ScanGoals after reopen: goal should revert to claimed/pending, Reopened=true, ImplementedCount=0
	audit2, err := ScanGoals(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if audit2.ImplementedCount != 0 {
		t.Fatalf("expected 0 implemented goals, got %d", audit2.ImplementedCount)
	}
	if audit2.ReopenedCount != 1 {
		t.Fatalf("expected 1 reopened goal, got %d", audit2.ReopenedCount)
	}
	g2 := audit2.Goals[0]
	if g2.Status != "claimed" || !g2.Reopened || g2.ReopenReason != "critical race condition in auth" {
		t.Fatalf("unexpected goal state after reopen: %+v", g2)
	}

	// Release claim and verify it transitions to pending ready
	if err := ReleaseGoal(tmp, "reopen-target"); err != nil {
		t.Fatalf("ReleaseGoal failed: %v", err)
	}
	audit3, err := ScanGoals(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if audit3.Goals[0].Status != "pending" || !audit3.Goals[0].Reopened {
		t.Fatalf("expected pending reopened goal, got: %+v", audit3.Goals[0])
	}

	// Commit remediation fix
	_ = os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte("# Plan\n- [x] Task 1\n- [x] Task 2 remediation\n"), 0644)
	runGit("add", ".")
	// Ensure timestamp is strictly after ReopenedAt
	time.Sleep(100 * time.Millisecond)
	runGit("commit", "-m", "fix: resolve critical race condition (goals/reopen-target/goal.md)")
	newCommit := runGit("rev-parse", "--short", "HEAD")

	// ScanGoals after remediation commit: should automatically resolve reopen and mark implemented!
	audit4, err := ScanGoals(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if audit4.ImplementedCount != 1 {
		t.Fatalf("expected 1 implemented goal after remediation, got %d", audit4.ImplementedCount)
	}
	if audit4.ReopenedCount != 0 {
		t.Fatalf("expected 0 reopened goals after resolution, got %d", audit4.ReopenedCount)
	}
	g4 := audit4.Goals[0]
	if g4.Status != "implemented" || g4.Commit != newCommit || g4.Reopened {
		t.Fatalf("expected implemented goal in new commit %s, got %+v", newCommit, g4)
	}

	// Verify on-disk reopen.json
	activeRec, err := GetActiveReopen(tmp, "reopen-target")
	if err != nil {
		t.Fatalf("GetActiveReopen error: %v", err)
	}
	if activeRec != nil {
		t.Fatalf("expected activeRec to be nil after resolution, got %+v", activeRec)
	}
}

func TestCheckReadmeSync(t *testing.T) {
	tmp := t.TempDir()
	goalDir := filepath.Join(tmp, "goals", "sample-goal")
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte("# Goal: Sample\n"), 0644); err != nil {
		t.Fatal(err)
	}

	audit, err := ScanGoals(tmp)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Missing README.md returns error
	err = CheckReadmeSync(tmp, audit)
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("expected missing README error, got: %v", err)
	}

	// 2. Generate valid README.md
	if err := SyncReadme(tmp, audit); err != nil {
		t.Fatal(err)
	}
	readmePath := filepath.Join(tmp, "goals", "README.md")
	dataBefore, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatal(err)
	}
	statBefore, _ := os.Stat(readmePath)

	// 3. In-sync check returns nil and does not mutate disk
	if err := CheckReadmeSync(tmp, audit); err != nil {
		t.Fatalf("expected in-sync check to pass, got: %v", err)
	}
	statAfter, _ := os.Stat(readmePath)
	if !statBefore.ModTime().Equal(statAfter.ModTime()) {
		t.Fatalf("CheckReadmeSync updated modtime on disk; dry-run invariant violated")
	}
	dataAfter, _ := os.ReadFile(readmePath)
	if string(dataBefore) != string(dataAfter) {
		t.Fatalf("CheckReadmeSync mutated file content on disk")
	}

	// 4. Out-of-sync returns error and does NOT overwrite file
	tampered := []byte("# Tampered README\n")
	_ = os.WriteFile(readmePath, tampered, 0644)
	err = CheckReadmeSync(tmp, audit)
	if err == nil || !strings.Contains(err.Error(), "out of sync") {
		t.Fatalf("expected out of sync error, got: %v", err)
	}

	// Invariant: dry-run must not mutate disk
	dataPersisted, _ := os.ReadFile(readmePath)
	if string(dataPersisted) != string(tampered) {
		t.Fatalf("CheckReadmeSync overwrote disk during check failure; dry-run invariant violated")
	}
}

func TestAttributeGoal(t *testing.T) {
	tmp := t.TempDir()

	// Initialize git repo in tmp
	initCmd := exec.Command("git", "init")
	initCmd.Dir = tmp
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("git init failed: %v, out: %s", err, string(out))
	}
	_ = exec.Command("git", "-C", tmp, "config", "user.email", "test@example.com").Run()
	_ = exec.Command("git", "-C", tmp, "config", "user.name", "Test User").Run()

	// Make initial commit
	dummyFile := filepath.Join(tmp, "code.go")
	_ = os.WriteFile(dummyFile, []byte("package main\n"), 0644)
	_ = exec.Command("git", "-C", tmp, "add", "code.go").Run()
	commitCmd := exec.Command("git", "-C", tmp, "commit", "-m", "feat: initial commit")
	if out, err := commitCmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit failed: %v, out: %s", err, string(out))
	}

	shaCmd := exec.Command("git", "-C", tmp, "rev-parse", "HEAD")
	shaOut, err := shaCmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD failed: %v", err)
	}
	commitSHA := strings.TrimSpace(string(shaOut))

	// Create goal
	slug := "test-attribution-goal"
	_, err = CreateGoal(CreateGoalParams{
		RootDir:     tmp,
		Slug:        slug,
		Title:       "Test Attribution Feature",
		Description: "Verify post-swarm goal attribution",
		Tasks:       []string{"Phase 1", "Phase 2"},
	})
	if err != nil {
		t.Fatalf("CreateGoal failed: %v", err)
	}

	// Claim goal
	_, err = ClaimGoal(tmp, slug, "antigravity", "working on it", false)
	if err != nil {
		t.Fatalf("ClaimGoal failed: %v", err)
	}

	// 1. Verify error on non-existent commit
	_, err = AttributeGoal(tmp, slug, "deadbeef1234567890")
	if err == nil || !strings.Contains(err.Error(), "does not exist in local git repository") {
		t.Fatalf("expected non-existent commit error, got: %v", err)
	}

	// 2. Attribute to valid commit
	goal, err := AttributeGoal(tmp, slug, commitSHA)
	if err != nil {
		t.Fatalf("AttributeGoal failed: %v", err)
	}

	if goal.Status != "implemented" {
		t.Errorf("expected goal status 'implemented', got %q", goal.Status)
	}
	if goal.Commit == "" {
		t.Errorf("expected non-empty commit SHA on attributed goal")
	}

	// 3. Verify claim lock cleared
	claimPath := filepath.Join(tmp, "goals", slug, "claim.json")
	if fileExists(claimPath) {
		t.Errorf("expected claim.json to be removed upon attribution")
	}

	// 4. Verify plan.md tasks checked
	planData, err := os.ReadFile(filepath.Join(tmp, "goals", slug, "plan.md"))
	if err != nil {
		t.Fatalf("reading plan.md: %v", err)
	}
	if strings.Contains(string(planData), "- [ ]") {
		t.Errorf("expected all plan tasks checked, got unchecked tasks: %s", string(planData))
	}

	// 5. Verify goals/README.md shows implemented
	readmeData, err := os.ReadFile(filepath.Join(tmp, "goals", "README.md"))
	if err != nil {
		t.Fatalf("reading goals/README.md: %v", err)
	}
	if !strings.Contains(string(readmeData), slug) {
		t.Errorf("goals/README.md missing goal slug: %s", string(readmeData))
	}
	if !strings.Contains(string(readmeData), "## 🟢 Implemented") {
		t.Errorf("goals/README.md missing Implemented section: %s", string(readmeData))
	}
}

func TestParseGoalMD_PlainMetadataKeys(t *testing.T) {
	// Case 1: Plain unbolded keys in Dependencies & Execution Order block
	content1 := `# Goal: Plain Metadata Test

## Goal Description
Testing unbolded metadata keys.

## Dependencies & Execution Order
- Mode: Independent (disjoint, parallelizable)
- Depends On: none
- Sequence: 5
- Shape: scout
- Tier: immediate
- Personas: architect, reviewer

## Files To Touch
- internal/goals/goals.go
- internal/goals/goals_test.go
`
	title, desc, indep, deps, seq, files, shape, tier, personas, _ := parseGoalMetadataFull(content1)
	if title != "Plain Metadata Test" {
		t.Errorf("title = %q, want Plain Metadata Test", title)
	}
	if !indep {
		t.Errorf("expected indep=true, got false")
	}
	if len(deps) != 0 {
		t.Errorf("expected 0 deps, got %v", deps)
	}
	if seq != 5 {
		t.Errorf("seq = %d, want 5", seq)
	}
	if shape != "scout" {
		t.Errorf("shape = %q, want scout", shape)
	}
	if tier != "immediate" {
		t.Errorf("tier = %q, want immediate", tier)
	}
	if len(personas) != 2 || personas[0] != "architect" || personas[1] != "reviewer" {
		t.Errorf("personas = %v, want [architect, reviewer]", personas)
	}
	if len(files) != 2 {
		t.Errorf("files = %v, want 2 files", files)
	}

	// Case 2: Plain dependent goal with inline and multiline dependencies
	content2 := `# Goal: Plain Dependent Test

## Dependencies & Execution Order
- Mode: Dependent (requires prerequisite goals)
- Depends On:
  - dep-alpha
  - dep-beta
- Status: pending
- Commit: deadbeef
`
	_, _, indep2, deps2, _, _, _, _, _, _ := parseGoalMetadataFull(content2)
	if indep2 {
		t.Errorf("expected indep2=false, got true")
	}
	expectedDeps := []string{"dep-alpha", "dep-beta"}
	if !reflect.DeepEqual(deps2, expectedDeps) {
		t.Errorf("deps = %v, want %v", deps2, expectedDeps)
	}

	// Case 3: Verify no phantom slugs from unbolded metadata under Dependencies & Execution Order
	content3 := `# Goal: Incident 15d Repro

## Dependencies & Execution Order
- Mode: Independent (disjoint, parallelizable)
- Depends On: none
`
	_, _, indep3, deps3, _, _, _, _, _, _ := parseGoalMetadataFull(content3)
	if !indep3 {
		t.Errorf("expected indep3=true, got false")
	}
	if len(deps3) != 0 {
		t.Errorf("repro failed: phantom dependency slugs extracted: %v", deps3)
	}
	_ = desc
}

func TestGoalAttribution_RequiresCheckedTasks(t *testing.T) {
	dir := t.TempDir()
	goalDir := filepath.Join(dir, "goals", "epistemic-test")
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatal(err)
	}

	goalContent := `# Goal: Epistemic Test

## Goal Description
Testing epistemic task completion gate.

## Dependencies & Execution Order
- Mode: Independent
- Depends On: none
- Tier: immediate
`
	planContentUnchecked := `# Plan: Epistemic Test

## Execution Steps
- [x] Phase 1: First completed step
- [ ] Phase 2: Second step remains incomplete
`
	factsContent := `# Facts: Epistemic Test
`

	if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalContent), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte(planContentUnchecked), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "facts.md"), []byte(factsContent), 0644); err != nil {
		t.Fatal(err)
	}

	git(t, dir, "init")
	git(t, dir, "config", "user.email", "test@example.com")
	git(t, dir, "config", "user.name", "Test")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "feat(test): initial work (goals/epistemic-test/goal.md)")

	// 1. Assert ScanGoals flags as pending because unchecked tasks remain
	res, err := ScanGoals(dir)
	if err != nil {
		t.Fatalf("ScanGoals failed: %v", err)
	}
	if len(res.Goals) != 1 {
		t.Fatalf("expected 1 goal, got %d", len(res.Goals))
	}
	g := res.Goals[0]
	if g.Status != "pending" {
		t.Fatalf("expected goal status pending due to unchecked tasks, got %q", g.Status)
	}
	if !strings.Contains(g.PendingReason, "unchecked") {
		t.Fatalf("expected pending reason to mention unchecked tasks, got %q", g.PendingReason)
	}
	if g.TasksMet != 1 || g.TasksTotal != 2 {
		t.Fatalf("expected 1/2 tasks, got %d/%d", g.TasksMet, g.TasksTotal)
	}

	// 2. ValidateEpistemicTaskAttribution warns on incomplete tasks with commit
	diags := ValidateEpistemicTaskAttribution(dir, &g)
	if len(diags) == 0 {
		t.Fatalf("expected diagnostics for unchecked tasks with attributed commit")
	}
	if diags[0].Code != DiagTaskInconsistent {
		t.Fatalf("expected code %s, got %s", DiagTaskInconsistent, diags[0].Code)
	}

	// 3. CheckEpistemicCompletion status
	status := CheckEpistemicCompletion(g.TasksMet, g.TasksTotal, g.Commit)
	if status != EpistemicPartial {
		t.Fatalf("expected EpistemicPartial, got %s", status)
	}

	// 4. Complete the second task
	planContentChecked := `# Plan: Epistemic Test

## Execution Steps
- [x] Phase 1: First completed step
- [x] Phase 2: Second step remains incomplete
`
	if err := os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte(planContentChecked), 0644); err != nil {
		t.Fatal(err)
	}

	// 5. Re-scan goals: should now transition to implemented
	res2, err := ScanGoals(dir)
	if err != nil {
		t.Fatalf("ScanGoals failed: %v", err)
	}
	g2 := res2.Goals[0]
	if g2.Status != "implemented" {
		t.Fatalf("expected goal status implemented once all tasks are checked, got %q", g2.Status)
	}
	if g2.TasksMet != 2 || g2.TasksTotal != 2 {
		t.Fatalf("expected 2/2 tasks, got %d/%d", g2.TasksMet, g2.TasksTotal)
	}

	// 6. Validate diagnostics clean once all tasks complete
	diags2 := ValidateEpistemicTaskAttribution(dir, &g2)
	if len(diags2) != 0 {
		t.Fatalf("expected 0 diagnostics once all tasks are checked, got %d", len(diags2))
	}
	status2 := CheckEpistemicCompletion(g2.TasksMet, g2.TasksTotal, g2.Commit)
	if status2 != EpistemicComplete {
		t.Fatalf("expected EpistemicComplete, got %s", status2)
	}
}

func TestGoalAttribution_ReopenedRequiresCheckedTasks(t *testing.T) {
	dir := t.TempDir()
	goalDir := filepath.Join(dir, "goals", "reopen-test")
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatal(err)
	}

	goalContent := `# Goal: Reopen Test

## Goal Description
Testing reopened goal epistemic task gate.

## Dependencies & Execution Order
- Mode: Independent
- Depends On: none
- Tier: immediate
`
	planContent := `# Plan: Reopen Test

## Execution Steps
- [x] Task 1
- [x] Task 2
`
	if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalContent), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte(planContent), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "facts.md"), []byte("# Facts"), 0644); err != nil {
		t.Fatal(err)
	}

	git(t, dir, "init")
	git(t, dir, "config", "user.email", "test@example.com")
	git(t, dir, "config", "user.name", "Test")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "feat(test): initial release (goals/reopen-test/goal.md)")

	res, err := ScanGoals(dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Goals[0].Status != "implemented" {
		t.Fatalf("expected initial implemented, got %s", res.Goals[0].Status)
	}

	// Reopen goal for remediation with new tasks
	reopenPlan := `# Plan: Reopen Test

## Execution Steps
- [x] Task 1
- [x] Task 2
- [ ] Remediation Task 3
`
	if err := os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte(reopenPlan), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReopenGoal(dir, "reopen-test", "tester", "remediation needed", false); err != nil {
		t.Fatal(err)
	}

	// Sleep slightly to ensure newer commit timestamp
	time.Sleep(10 * time.Millisecond)

	// Make an intermediate commit
	git(t, dir, "commit", "--allow-empty", "-m", "fix(test): intermediate fix (goals/reopen-test/goal.md)")

	// ScanGoals should still keep goal pending/reopened because Remediation Task 3 is unchecked
	res2, err := ScanGoals(dir)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Goals[0].Status != "pending" {
		t.Fatalf("expected status pending while remediation tasks remain unchecked, got %s", res2.Goals[0].Status)
	}
	if !res2.Goals[0].Reopened {
		t.Fatalf("expected goal to remain marked reopened")
	}

	// Now check Remediation Task 3
	resolvedPlan := `# Plan: Reopen Test

## Execution Steps
- [x] Task 1
- [x] Task 2
- [x] Remediation Task 3
`
	if err := os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte(resolvedPlan), 0644); err != nil {
		t.Fatal(err)
	}

	// Make final resolution commit
	time.Sleep(10 * time.Millisecond)
	git(t, dir, "commit", "--allow-empty", "-m", "fix(test): complete remediation (goals/reopen-test/goal.md)")

	res3, err := ScanGoals(dir)
	if err != nil {
		t.Fatal(err)
	}
	if res3.Goals[0].Status != "implemented" {
		t.Fatalf("expected status implemented after all remediation tasks checked and committed, got %s", res3.Goals[0].Status)
	}
}

func TestGoalAccounting_ImplementedGoalsCompleteTasks(t *testing.T) {
	dir := t.TempDir()

	// 1. Goal with step headings (like example-shop.com)
	g1Dir := filepath.Join(dir, "goals", "step-goal")
	if err := os.MkdirAll(g1Dir, 0755); err != nil {
		t.Fatal(err)
	}
	goal1 := "# Goal: Step Goal\n\n## Dependencies & Execution Order\n- Mode: Independent\n- Tier: immediate\n"
	plan1 := "# Plan: Step Goal\n\n### Step 1: Infra change\n### Step 2: Schema migration\n"
	if err := os.WriteFile(filepath.Join(g1Dir, "goal.md"), []byte(goal1), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(g1Dir, "plan.md"), []byte(plan1), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(g1Dir, "facts.md"), []byte("# Facts\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// 2. Goal with attribution commit phase unchecked (like athan.waqf.app)
	g2Dir := filepath.Join(dir, "goals", "attrib-goal")
	if err := os.MkdirAll(g2Dir, 0755); err != nil {
		t.Fatal(err)
	}
	goal2 := "# Goal: Attrib Goal\n\n## Dependencies & Execution Order\n- Mode: Independent\n- Tier: immediate\n"
	plan2 := "# Plan: Attrib Goal\n\n- [x] Phase 1: Core logic\n- [x] Phase 2: Tests\n- [x] Phase 3: Docs\n- [ ] Phase 4 — Attribution Commit: Commit and verify\n"
	if err := os.WriteFile(filepath.Join(g2Dir, "goal.md"), []byte(goal2), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(g2Dir, "plan.md"), []byte(plan2), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(g2Dir, "facts.md"), []byte("# Facts\n"), 0644); err != nil {
		t.Fatal(err)
	}

	git(t, dir, "init")
	git(t, dir, "config", "user.email", "test@example.com")
	git(t, dir, "config", "user.name", "Test")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "feat: implement step-goal (goals/step-goal/goal.md)")
	git(t, dir, "commit", "--allow-empty", "-m", "feat: implement attrib-goal (goals/attrib-goal/goal.md)")

	res, err := ScanGoals(dir)
	if err != nil {
		t.Fatalf("ScanGoals failed: %v", err)
	}
	if len(res.Goals) != 2 {
		t.Fatalf("expected 2 goals, got %d", len(res.Goals))
	}

	goalMap := make(map[string]Goal)
	for _, g := range res.Goals {
		if g.Status != "implemented" {
			t.Errorf("goal %s: expected status implemented, got %s", g.Slug, g.Status)
		}
		goalMap[g.Slug] = g
	}

	// Verify step-goal honest accounting (0 met out of 2 total)
	if g, ok := goalMap["step-goal"]; ok {
		if g.TasksMet != 0 || g.TasksTotal != 2 {
			t.Errorf("step-goal: expected TasksMet=0, TasksTotal=2, got %d/%d", g.TasksMet, g.TasksTotal)
		}
		view, err := BuildGoalView(dir, &g)
		if err != nil {
			t.Errorf("BuildGoalView failed for step-goal: %v", err)
		} else {
			if view.TasksMet != 0 || view.TasksTotal != 2 {
				t.Errorf("step-goal view: expected TasksMet=0, TasksTotal=2, got %d/%d", view.TasksMet, view.TasksTotal)
			}
			for _, task := range view.Tasks {
				if task.Completed {
					t.Errorf("step-goal: expected task %d to be uncompleted", task.Index)
				}
			}
		}
	} else {
		t.Errorf("missing step-goal in scan results")
	}

	// Verify attrib-goal honest accounting (3 met out of 4 total, phase 4 unchecked)
	if g, ok := goalMap["attrib-goal"]; ok {
		if g.TasksMet != 3 || g.TasksTotal != 4 {
			t.Errorf("attrib-goal: expected TasksMet=3, TasksTotal=4, got %d/%d", g.TasksMet, g.TasksTotal)
		}
		view, err := BuildGoalView(dir, &g)
		if err != nil {
			t.Errorf("BuildGoalView failed for attrib-goal: %v", err)
		} else {
			if view.TasksMet != 3 || view.TasksTotal != 4 {
				t.Errorf("attrib-goal view: expected TasksMet=3, TasksTotal=4, got %d/%d", view.TasksMet, view.TasksTotal)
			}
			if len(view.Tasks) == 4 {
				if !view.Tasks[0].Completed || !view.Tasks[1].Completed || !view.Tasks[2].Completed {
					t.Errorf("attrib-goal: expected phases 1-3 to be completed")
				}
				if view.Tasks[3].Completed {
					t.Errorf("attrib-goal: expected phase 4 to remain uncompleted")
				}
			}
		}
	} else {
		t.Errorf("missing attrib-goal in scan results")
	}

	// Honest aggregate task counts: 0 + 3 = 3 completed out of 6 total
	if res.CompletedTasks != 3 {
		t.Errorf("expected CompletedTasks (3), got %d", res.CompletedTasks)
	}
	if res.TotalTasks != 6 {
		t.Errorf("expected 6 total tasks (2 from step-goal, 4 from attrib-goal), got %d", res.TotalTasks)
	}
}

func TestCommitAttribution_ClosureSubjectValidation(t *testing.T) {
	dir := t.TempDir()
	slug := "closure-subject-goal"
	goalDir := filepath.Join(dir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatal(err)
	}
	goalMD := "# Goal: Closure Subject Test\n\n## Dependencies & Execution Order\n- Mode: Independent\n- Tier: immediate\n"
	planMD := "# Plan: Closure Subject Test\n\n- [x] Phase 1: Core logic\n- [ ] Phase 2 — Attribution Commit: Verify\n"
	if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalMD), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte(planMD), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "facts.md"), []byte("# Facts\n"), 0644); err != nil {
		t.Fatal(err)
	}

	git(t, dir, "init", "-q")
	git(t, dir, "config", "user.email", "test@example.com")
	git(t, dir, "config", "user.name", "Tester")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "initial repo setup")

	// 1. wip commit must NOT close or attribute goal
	git(t, dir, "commit", "--allow-empty", "-qm", "wip: working on feature (goals/"+slug+"/goal.md)")
	res, err := ScanGoals(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Goals) != 1 || res.Goals[0].Status == "implemented" {
		t.Fatalf("goal should NOT be implemented after wip commit, got status %s", res.Goals[0].Status)
	}

	// 2. style commit must NOT close or attribute goal
	git(t, dir, "commit", "--allow-empty", "-qm", "style: format code (goals/"+slug+"/goal.md)")
	res, err = ScanGoals(dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Goals[0].Status == "implemented" {
		t.Fatalf("goal should NOT be implemented after style commit, got status %s", res.Goals[0].Status)
	}

	// 3. untyped commit without conventional prefix must NOT close or attribute goal
	git(t, dir, "commit", "--allow-empty", "-qm", "update some code (goals/"+slug+"/goal.md)")
	res, err = ScanGoals(dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Goals[0].Status == "implemented" {
		t.Fatalf("goal should NOT be implemented after untyped commit, got status %s", res.Goals[0].Status)
	}

	// 4. conventional feat commit MUST close the goal
	git(t, dir, "commit", "--allow-empty", "-qm", "feat: implement closure subject feature (goals/"+slug+"/goal.md)")
	res, err = ScanGoals(dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Goals[0].Status != "implemented" {
		t.Fatalf("goal MUST be implemented after feat commit, got status %s", res.Goals[0].Status)
	}
}

func TestCommitAttribution_UncheckedTasksPreventImplementation(t *testing.T) {
	dir := t.TempDir()
	slug := "unchecked-tasks-goal"
	goalDir := filepath.Join(dir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatal(err)
	}
	goalMD := "# Goal: Unchecked Tasks Test\n\n## Dependencies & Execution Order\n- Mode: Independent\n- Tier: immediate\n"
	planMD := "# Plan: Unchecked Tasks Test\n\n- [ ] Phase 1: Core logic\n- [ ] Phase 2: Tests\n- [ ] Phase 3 — Attribution Commit: Verify\n"
	if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalMD), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte(planMD), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "facts.md"), []byte("# Facts\n"), 0644); err != nil {
		t.Fatal(err)
	}

	git(t, dir, "init", "-q")
	git(t, dir, "config", "user.email", "test@example.com")
	git(t, dir, "config", "user.name", "Tester")
	git(t, dir, "add", ".")

	// Commit attributing the goal while Phase 1 and 2 are unchecked
	git(t, dir, "commit", "-qm", "feat: implement feature prematurely (goals/"+slug+"/goal.md)")
	res, err := ScanGoals(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Goals) != 1 {
		t.Fatalf("expected 1 goal, got %d", len(res.Goals))
	}
	g := res.Goals[0]
	if g.Status == "implemented" {
		t.Fatalf("goal should NOT be implemented when tasks are unchecked in plan.md; got status %s", g.Status)
	}
	if g.TasksMet != 0 || g.TasksTotal != 3 {
		t.Fatalf("expected 0/3 tasks met, got %d/%d", g.TasksMet, g.TasksTotal)
	}
}

func TestGoalAttributionHonesty_GeneratedReadmeBypass(t *testing.T) {
	dir := t.TempDir()
	slug := "readme-bypass-test"
	goalDir := filepath.Join(dir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatal(err)
	}
	goalMD := "# Goal: Readme Bypass Test\n\n## Dependencies & Execution Order\n- Mode: Independent\n- Tier: immediate\n"
	planMD := "# Plan: Readme Bypass Test\n\n- [x] Step 1: Design data model\n- [ ] Step 2: Implement core algorithm\n"
	if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalMD), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte(planMD), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "facts.md"), []byte("# Facts\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a generated README that falsely lists the goal as implemented
	readmeContent := fmt.Sprintf("# Goals\n\n## 🟢 Implemented\n\n| [`%s`](%s/goal.md) | `🎯 Immediate` | `⚡ Indep` | `@agent` | Git Commit |\n", slug, slug)
	if err := os.WriteFile(filepath.Join(dir, "goals", "README.md"), []byte(readmeContent), 0644); err != nil {
		t.Fatal(err)
	}

	git(t, dir, "init", "-q")
	git(t, dir, "config", "user.email", "test@example.com")
	git(t, dir, "config", "user.name", "Tester")
	git(t, dir, "add", ".")

	// Commit with a valid feat closure commit
	git(t, dir, "commit", "-qm", "feat: implement readme bypass test (goals/"+slug+"/goal.md)")

	// 1. With generated README listing it implemented, but Step 2 unchecked:
	// Status MUST still be pending! A cached README cannot waive substantive unchecked tasks.
	res, err := ScanGoals(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Goals) != 1 {
		t.Fatalf("expected 1 goal, got %d", len(res.Goals))
	}
	g := res.Goals[0]
	if g.Status == "implemented" {
		t.Fatalf("cached README entry falsely bypassed unchecked tasks: expected pending, got %q", g.Status)
	}
	if g.Status != "pending" {
		t.Fatalf("expected status 'pending', got %q", g.Status)
	}
	if g.TasksMet != 1 || g.TasksTotal != 2 {
		t.Fatalf("expected 1/2 tasks met, got %d/%d", g.TasksMet, g.TasksTotal)
	}
	if g.Commit == "" {
		t.Fatalf("expected commit attribution hash to be preserved, got empty")
	}
	if !strings.Contains(g.PendingReason, "1/2 tasks remain unchecked in plan.md") {
		t.Fatalf("expected pending reason to mention unchecked tasks, got %q", g.PendingReason)
	}

	// 2. Remove README completely: Status must still be pending with identical accounting
	_ = os.Remove(filepath.Join(dir, "goals", "README.md"))
	resNoReadme, err := ScanGoals(dir)
	if err != nil {
		t.Fatal(err)
	}
	gNoReadme := resNoReadme.Goals[0]
	if gNoReadme.Status != "pending" || gNoReadme.TasksMet != 1 || gNoReadme.TasksTotal != 2 {
		t.Fatalf("status changed unexpectedly without README: %+v", gNoReadme)
	}

	// 3. Complete Step 2: Now all tasks are checked, status MUST transition to implemented
	completePlanMD := "# Plan: Readme Bypass Test\n\n- [x] Step 1: Design data model\n- [x] Step 2: Implement core algorithm\n"
	if err := os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte(completePlanMD), 0644); err != nil {
		t.Fatal(err)
	}
	resComplete, err := ScanGoals(dir)
	if err != nil {
		t.Fatal(err)
	}
	gComplete := resComplete.Goals[0]
	if gComplete.Status != "implemented" {
		t.Fatalf("expected status 'implemented' after all tasks checked, got %q", gComplete.Status)
	}
	if gComplete.TasksMet != 2 || gComplete.TasksTotal != 2 {
		t.Fatalf("expected 2/2 tasks met, got %d/%d", gComplete.TasksMet, gComplete.TasksTotal)
	}
}

func TestGoalAttributionHonesty_AttributionOnlyCheckboxes(t *testing.T) {
	dir := t.TempDir()
	slug := "attribution-only-test"
	goalDir := filepath.Join(dir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatal(err)
	}
	goalMD := "# Goal: Attribution Only Test\n\n## Dependencies & Execution Order\n- Mode: Independent\n- Tier: immediate\n"
	planMD := "# Plan: Attribution Only Test\n\n- [x] Phase 1: Core logic\n- [x] Phase 2: Tests\n- [ ] Phase 3 — Attribution Commit: Commit with (goals/attribution-only-test/goal.md)\n"
	if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalMD), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte(planMD), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "facts.md"), []byte("# Facts\n"), 0644); err != nil {
		t.Fatal(err)
	}

	git(t, dir, "init", "-q")
	git(t, dir, "config", "user.email", "test@example.com")
	git(t, dir, "config", "user.name", "Tester")
	git(t, dir, "add", ".")

	git(t, dir, "commit", "-qm", "feat: complete attribution only test (goals/"+slug+"/goal.md)")

	res, err := ScanGoals(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Goals) != 1 {
		t.Fatalf("expected 1 goal, got %d", len(res.Goals))
	}
	g := res.Goals[0]
	if g.Status != "implemented" {
		t.Fatalf("expected attribution-only checkbox to allow implementation; got status %q", g.Status)
	}
}

func TestGoalAttributionHonesty_ClaimWithUncheckedTasks(t *testing.T) {
	dir := t.TempDir()
	slug := "claim-unchecked-test"
	goalDir := filepath.Join(dir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatal(err)
	}
	goalMD := "# Goal: Claim Unchecked Test\n\n## Dependencies & Execution Order\n- Mode: Independent\n- Tier: immediate\n"
	planMD := "# Plan: Claim Unchecked Test\n\n- [x] Step 1: Preliminary\n- [ ] Step 2: Unfinished work\n"
	if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalMD), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte(planMD), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "facts.md"), []byte("# Facts\n"), 0644); err != nil {
		t.Fatal(err)
	}

	git(t, dir, "init", "-q")
	git(t, dir, "config", "user.email", "test@example.com")
	git(t, dir, "config", "user.name", "Tester")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "feat: partial implementation (goals/"+slug+"/goal.md)")

	// Attempt to claim goal: because tasks are unchecked, claim MUST succeed
	claim, _, err := ClaimGoalWithCharter(dir, slug, "@agent", "finishing work", "sess-1", false, ClaimOptions{})
	if err != nil {
		t.Fatalf("expected claiming goal with unchecked tasks to succeed, got error: %v", err)
	}
	if claim == nil {
		t.Fatalf("expected non-nil claim")
	}

	// Release claim
	if err := ReleaseGoal(dir, slug); err != nil {
		t.Fatal(err)
	}

	// Now check all tasks
	completePlan := "# Plan: Claim Unchecked Test\n\n- [x] Step 1: Preliminary\n- [x] Step 2: Unfinished work\n"
	if err := os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte(completePlan), 0644); err != nil {
		t.Fatal(err)
	}

	// Attempt to claim again: now that all tasks are checked, claim MUST fail with already implemented
	_, _, err = ClaimGoalWithCharter(dir, slug, "@agent", "try claim again", "sess-1", false, ClaimOptions{})
	if err == nil {
		t.Fatalf("expected claim to fail on fully completed goal, but got nil error")
	}
	if !strings.Contains(err.Error(), "already implemented") {
		t.Fatalf("expected error mentioning already implemented, got: %v", err)
	}
}

func TestGoalVerificationEvidenceLabels(t *testing.T) {
	dir := t.TempDir()
	goalsDir := filepath.Join(dir, "goals")
	if err := os.MkdirAll(goalsDir, 0755); err != nil {
		t.Fatal(err)
	}

	createGoalPkg := func(slug, touchFile string) {
		gDir := filepath.Join(goalsDir, slug)
		_ = os.MkdirAll(gDir, 0755)
		gContent := fmt.Sprintf("# Goal: %s\n\n- **Mode**: independent\n- **Touch**: %s\n", slug, touchFile)
		_ = os.WriteFile(filepath.Join(gDir, "goal.md"), []byte(gContent), 0644)
		pContent := "# Plan\n- [x] Step 1: Implementation\n"
		_ = os.WriteFile(filepath.Join(gDir, "plan.md"), []byte(pContent), 0644)
		_ = os.WriteFile(filepath.Join(gDir, "facts.md"), []byte("# Facts\n"), 0644)
		if touchFile != "" {
			_ = os.MkdirAll(filepath.Dir(filepath.Join(dir, touchFile)), 0755)
			_ = os.WriteFile(filepath.Join(dir, touchFile), []byte("package test\n"), 0644)
		}
	}

	createReceipt := func(slug, commit string) {
		r := AuditReceipt{
			Version:            "sila/audit-receipt/v1",
			GoalSlug:           slug,
			CommitSHA:          commit,
			SpecHash:           "spec-hash-123",
			SandboxNonce:       "sandbox-nonce-123",
			CandidateSelection: "A",
			BlindfoldVerified:  true,
			CanaryPassed:       true,
			CCDSIScore:         0.85,
			ReqCoverage:        1.0,
			ProbesExecuted:     6,
			ProbesPassed:       6,
			ProbeManifestHash:  "manifest-hash-123",
			CertifiedAt:        time.Now().UTC(),
		}
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatalf("generate ed25519 key: %v", err)
		}
		r.AuditorPubKey = base64.StdEncoding.EncodeToString(pub)
		digest := FormatReceiptDigest(&r)
		sig := ed25519.Sign(priv, digest)
		r.AuditorSignature = base64.StdEncoding.EncodeToString(sig)

		data, _ := json.MarshalIndent(r, "", "  ")
		_ = os.WriteFile(filepath.Join(goalsDir, slug, "audit-receipt.json"), data, 0644)
	}

	// 1. Goal: unverified-feat (commit attributed, tasks checked, NO receipt)
	createGoalPkg("unverified-feat", "pkg/unverified.go")

	// 2. Goal: verified-feat (commit attributed, matching receipt)
	createGoalPkg("verified-feat", "pkg/verified.go")

	// 3. Goal: stale-commit-feat (commit attributed, receipt for different commit)
	createGoalPkg("stale-commit-feat", "pkg/stale_commit.go")

	// 4. Goal: stale-dirty-feat (commit attributed, matching receipt, but working tree dirty)
	createGoalPkg("stale-dirty-feat", "pkg/stale_dirty.go")

	// 5. Goal: reopened-feat (implemented then reopened)
	createGoalPkg("reopened-feat", "pkg/reopened.go")

	git(t, dir, "init", "-q")
	git(t, dir, "config", "user.email", "test@example.com")
	git(t, dir, "config", "user.name", "Tester")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "feat: initial commit for all goals (goals/unverified-feat/goal.md) (goals/verified-feat/goal.md) (goals/stale-commit-feat/goal.md) (goals/stale-dirty-feat/goal.md) (goals/reopened-feat/goal.md)")

	// Get the commit hash
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		t.Fatalf("git rev-parse failed: %v", err)
	}
	headHash := strings.TrimSpace(string(out))

	// Attach valid matching receipt for verified-feat
	createReceipt("verified-feat", headHash)

	// Attach stale receipt (commit mismatch) for stale-commit-feat
	createReceipt("stale-commit-feat", "different001")

	// Attach matching receipt for stale-dirty-feat, but modify the touch file to make it dirty
	createReceipt("stale-dirty-feat", headHash)
	if err := os.WriteFile(filepath.Join(dir, "pkg", "stale_dirty.go"), []byte("package test // dirty edit\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Reopen reopened-feat
	if _, _, err := ReopenGoal(dir, "reopened-feat", "tester", "found edge case regression", false); err != nil {
		t.Fatalf("ReopenGoal failed: %v", err)
	}

	// Scan goals
	res, err := ScanGoals(dir)
	if err != nil {
		t.Fatalf("ScanGoals failed: %v", err)
	}

	goalBySlug := make(map[string]Goal)
	for _, g := range res.Goals {
		goalBySlug[g.Slug] = g
	}

	// Verify unverified-feat: implemented but explicitly unverified
	uv := goalBySlug["unverified-feat"]
	if uv.Status != "implemented" {
		t.Errorf("expected unverified-feat status=implemented, got %q", uv.Status)
	}
	if uv.VerificationStatus != "unverified" || uv.VerificationLabel != "unverified" {
		t.Errorf("expected unverified-feat verification=unverified, got status=%q label=%q", uv.VerificationStatus, uv.VerificationLabel)
	}
	if uv.Certified {
		t.Errorf("expected unverified-feat certified=false")
	}

	// Verify verified-feat: implemented and verified
	vf := goalBySlug["verified-feat"]
	if vf.Status != "implemented" {
		t.Errorf("expected verified-feat status=implemented, got %q", vf.Status)
	}
	if vf.VerificationStatus != "verified" || vf.VerificationLabel != "verified" {
		t.Errorf("expected verified-feat verification=verified, got status=%q label=%q", vf.VerificationStatus, vf.VerificationLabel)
	}
	if !vf.Certified {
		t.Errorf("expected verified-feat certified=true")
	}

	// Verify stale-commit-feat: implemented but stale receipt (commit mismatch)
	sc := goalBySlug["stale-commit-feat"]
	if sc.Status != "implemented" {
		t.Errorf("expected stale-commit-feat status=implemented, got %q", sc.Status)
	}
	if sc.VerificationStatus != "stale" || sc.VerificationLabel != "stale" {
		t.Errorf("expected stale-commit-feat verification=stale, got status=%q label=%q", sc.VerificationStatus, sc.VerificationLabel)
	}
	if sc.Certified {
		t.Errorf("expected stale-commit-feat certified=false")
	}

	// Verify stale-dirty-feat: implemented but stale receipt due to dirty worktree
	sd := goalBySlug["stale-dirty-feat"]
	if sd.Status != "implemented" {
		t.Errorf("expected stale-dirty-feat status=implemented, got %q", sd.Status)
	}
	if sd.VerificationStatus != "stale" || sd.VerificationLabel != "stale" {
		t.Errorf("expected stale-dirty-feat verification=stale, got status=%q label=%q", sd.VerificationStatus, sd.VerificationLabel)
	}
	if sd.Certified {
		t.Errorf("expected stale-dirty-feat certified=false")
	}

	// Verify reopened-feat: reopened
	ro := goalBySlug["reopened-feat"]
	if !ro.Reopened {
		t.Errorf("expected reopened-feat reopened=true")
	}
	if ro.VerificationStatus != "reopened" || ro.VerificationLabel != "reopened" {
		t.Errorf("expected reopened-feat verification=reopened, got status=%q label=%q", ro.VerificationStatus, ro.VerificationLabel)
	}

	// Verify AuditResult counts
	if res.ImplementedCount != 4 {
		t.Errorf("expected ImplementedCount=4, got %d", res.ImplementedCount)
	}
	if res.VerifiedCount != 1 {
		t.Errorf("expected VerifiedCount=1, got %d", res.VerifiedCount)
	}
	if res.StaleCount != 2 {
		t.Errorf("expected StaleCount=2, got %d", res.StaleCount)
	}
	if res.UnverifiedCount != 1 {
		t.Errorf("expected UnverifiedCount=1, got %d", res.UnverifiedCount)
	}

	// Verify SyncReadme output
	if err := SyncReadme(dir, res); err != nil {
		t.Fatalf("SyncReadme failed: %v", err)
	}
	readmeData, err := os.ReadFile(filepath.Join(goalsDir, "README.md"))
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	readmeStr := string(readmeData)

	// Summary check
	if !strings.Contains(readmeStr, "- 🟢 **Implemented**: 4 (1 verified 📜, 2 stale ⚠️, 1 unverified)") {
		t.Errorf("README missing truthful verification summary:\n%s", readmeStr)
	}

	// Section heading check
	if !strings.Contains(readmeStr, "## 🟢 Implemented\n\n| Goal Package | Mode | Shape | Verification | Task / Ref | Commit |") {
		t.Errorf("README missing Implemented table with Verification column:\n%s", readmeStr)
	}

	// Table entries check
	if !strings.Contains(readmeStr, "| [`verified-feat`](verified-feat/goal.md) | `⚡ Indep` | 🚢 SHIP | verified 📜 |") {
		t.Errorf("README missing verified-feat row:\n%s", readmeStr)
	}
	if !strings.Contains(readmeStr, "| [`unverified-feat`](unverified-feat/goal.md) | `⚡ Indep` | 🚢 SHIP | unverified |") {
		t.Errorf("README missing unverified-feat row:\n%s", readmeStr)
	}
	if !strings.Contains(readmeStr, "| [`stale-commit-feat`](stale-commit-feat/goal.md) | `⚡ Indep` | 🚢 SHIP | stale ⚠️ |") {
		t.Errorf("README missing stale-commit-feat row:\n%s", readmeStr)
	}
	if !strings.Contains(readmeStr, "| [`stale-dirty-feat`](stale-dirty-feat/goal.md) | `⚡ Indep` | 🚢 SHIP | stale ⚠️ |") {
		t.Errorf("README missing stale-dirty-feat row:\n%s", readmeStr)
	}

	// Criterion 3: A generated README cannot upgrade verification authority
	res2, err := ScanGoals(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range res2.Goals {
		if g.Slug == "unverified-feat" && g.VerificationStatus != "unverified" {
			t.Fatalf("generated README upgraded unverified-feat authority: got %s", g.VerificationStatus)
		}
	}
}

func TestParentCommitReceiptMatching(t *testing.T) {
	dir := t.TempDir()
	goalsDir := filepath.Join(dir, "goals")
	if err := os.MkdirAll(goalsDir, 0755); err != nil {
		t.Fatal(err)
	}

	createGoalPkg := func(slug, touchFile string) {
		pkgDir := filepath.Join(goalsDir, slug)
		_ = os.MkdirAll(pkgDir, 0755)
		goalMd := fmt.Sprintf("# Goal: %s\n\n## Dependencies\n- Mode: Independent\n- Shape: ship\n- Tier: immediate\n\n## Files to Touch\n- %s\n", slug, touchFile)
		_ = os.WriteFile(filepath.Join(pkgDir, "goal.md"), []byte(goalMd), 0644)
		_ = os.WriteFile(filepath.Join(pkgDir, "plan.md"), []byte("# Plan\n- [x] Task 1\n"), 0644)
		_ = os.WriteFile(filepath.Join(pkgDir, "facts.md"), []byte("# Facts\n"), 0644)
		_ = os.MkdirAll(filepath.Join(dir, filepath.Dir(touchFile)), 0755)
		_ = os.WriteFile(filepath.Join(dir, touchFile), []byte("package test\n"), 0644)
	}

	createGoalPkg("parent-receipt-feat", "pkg/parent_feat.go")

	git(t, dir, "init", "-q")
	git(t, dir, "config", "user.email", "test@example.com")
	git(t, dir, "config", "user.name", "Tester")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "docs: initial goal package")

	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("git rev-parse failed: %v", err)
	}
	parentHash := strings.TrimSpace(string(out))

	pubKey, privKey, _ := ed25519.GenerateKey(nil)
	receipt := &AuditReceipt{
		Version:            "sila/audit-receipt/v1",
		GoalSlug:           "parent-receipt-feat",
		CommitSHA:          parentHash,
		SpecHash:           "dummy-spec-hash",
		SandboxNonce:       "dummy-nonce",
		CandidateSelection: "A",
		BlindfoldVerified:  true,
		CanaryPassed:       true,
		CCDSIScore:         0.85,
		ReqCoverage:        1.0,
		ProbesExecuted:     6,
		ProbesPassed:       6,
		ProbeManifestHash:  "dummy-manifest-hash",
		AuditorPubKey:      base64.StdEncoding.EncodeToString(pubKey),
		CertifiedAt:        time.Now().UTC(),
	}
	digest := FormatReceiptDigest(receipt)
	receipt.AuditorSignature = base64.StdEncoding.EncodeToString(ed25519.Sign(privKey, digest))
	rBytes, _ := json.MarshalIndent(receipt, "", "  ")
	_ = os.WriteFile(filepath.Join(goalsDir, "parent-receipt-feat", "audit-receipt.json"), rBytes, 0644)

	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "feat: implement parent receipt goal [goal: parent-receipt-feat]")

	res, err := ScanGoals(dir)
	if err != nil {
		t.Fatalf("ScanGoals failed: %v", err)
	}

	var target *Goal
	for i := range res.Goals {
		if res.Goals[i].Slug == "parent-receipt-feat" {
			target = &res.Goals[i]
			break
		}
	}
	if target == nil {
		t.Fatalf("goal parent-receipt-feat not found in scan")
	}
	if target.VerificationStatus != "verified" {
		t.Errorf("expected parent-receipt-feat to be verified, got: %s", target.VerificationStatus)
	}
	if !target.Certified {
		t.Errorf("expected parent-receipt-feat certified=true")
	}
}

func TestCertificationRecordCommitSHA(t *testing.T) {
	dir := t.TempDir()
	goalsDir := filepath.Join(dir, "goals")
	if err := os.MkdirAll(goalsDir, 0755); err != nil {
		t.Fatal(err)
	}

	createGoalPkg := func(slug, touchFile string) {
		pkgDir := filepath.Join(goalsDir, slug)
		_ = os.MkdirAll(pkgDir, 0755)
		goalMd := fmt.Sprintf("# Goal: %s\n\n## Dependencies\n- Mode: Independent\n- Shape: ship\n- Tier: immediate\n\n## Files to Touch\n- %s\n", slug, touchFile)
		_ = os.WriteFile(filepath.Join(pkgDir, "goal.md"), []byte(goalMd), 0644)
		_ = os.WriteFile(filepath.Join(pkgDir, "plan.md"), []byte("# Plan\n- [x] Task 1\n"), 0644)
		_ = os.WriteFile(filepath.Join(pkgDir, "facts.md"), []byte("# Facts\n"), 0644)
		_ = os.MkdirAll(filepath.Join(dir, filepath.Dir(touchFile)), 0755)
		_ = os.WriteFile(filepath.Join(dir, touchFile), []byte("package test\n"), 0644)
	}

	createGoalPkg("cert-commit-feat", "pkg/cert_feat.go")

	git(t, dir, "init", "-q")
	git(t, dir, "config", "user.email", "test@example.com")
	git(t, dir, "config", "user.name", "Tester")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "feat: implement cert commit feat [goal: cert-commit-feat]")

	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("git rev-parse failed: %v", err)
	}
	headHash := strings.TrimSpace(string(out))

	cert := &CertificationRecord{
		GoalSlug:     "cert-commit-feat",
		CertifiedAt:  time.Now().UTC(),
		CertifiedBy:  "tester",
		EvidenceFile: filepath.Join(goalsDir, "cert-commit-feat", "facts.md"),
		Note:         "verified from facts",
		Verified:     true,
		CommitSHA:    headHash,
	}
	cBytes, _ := json.MarshalIndent(cert, "", "  ")
	_ = os.WriteFile(filepath.Join(goalsDir, "cert-commit-feat", "certification.json"), cBytes, 0644)

	res, err := ScanGoals(dir)
	if err != nil {
		t.Fatalf("ScanGoals failed: %v", err)
	}

	var target *Goal
	for i := range res.Goals {
		if res.Goals[i].Slug == "cert-commit-feat" {
			target = &res.Goals[i]
			break
		}
	}
	if target == nil {
		t.Fatalf("goal cert-commit-feat not found in scan")
	}
	if target.VerificationStatus != "verified" {
		t.Errorf("expected cert-commit-feat to be verified, got: %s", target.VerificationStatus)
	}
	if !target.Certified {
		t.Errorf("expected cert-commit-feat certified=true")
	}
}

func TestIsCommitAncestor(t *testing.T) {
	dir := t.TempDir()

	execGit := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@test.local",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@test.local",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v, out=%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}

	execGit("init")
	execGit("config", "user.name", "Test")
	execGit("config", "user.email", "test@test.local")
	execGit("config", "commit.gpgsign", "false")

	f1 := filepath.Join(dir, "f1.txt")
	_ = os.WriteFile(f1, []byte("1"), 0644)
	execGit("add", "f1.txt")
	c1 := execGit("commit", "-m", "commit 1")

	c1Hash := execGit("rev-parse", "HEAD")

	f2 := filepath.Join(dir, "f2.txt")
	_ = os.WriteFile(f2, []byte("2"), 0644)
	execGit("add", "f2.txt")
	_ = execGit("commit", "-m", "commit 2")

	c2Hash := execGit("rev-parse", "HEAD")

	if !isCommitAncestor(dir, c1Hash, c2Hash) {
		t.Errorf("expected c1 (%s) to be ancestor of c2 (%s)", c1Hash, c2Hash)
	}
	if isCommitAncestor(dir, c2Hash, c1Hash) {
		t.Errorf("did not expect c2 (%s) to be ancestor of c1 (%s)", c2Hash, c1Hash)
	}
	if isCommitAncestor(dir, "", c2Hash) {
		t.Errorf("expected empty ancestor to return false")
	}
	_ = c1
}

func TestCreateGoalShapeTemplatesAndDefaultScaffoldPlan(t *testing.T) {
	dir := t.TempDir()

	// 1. Create scout goal without explicit tasks
	scoutGoal, err := CreateGoal(CreateGoalParams{
		RootDir:     dir,
		Slug:        "audit-architecture",
		Title:       "Audit Architecture",
		Description: "Perform architectural reconnaissance and produce report",
		Shape:       "scout",
	})
	if err != nil {
		t.Fatalf("CreateGoal scout failed: %v", err)
	}
	if scoutGoal.Shape != "scout" {
		t.Errorf("expected Shape=scout, got %q", scoutGoal.Shape)
	}

	scoutPlanPath := filepath.Join(dir, "goals", "audit-architecture", "plan.md")
	scoutPlanBytes, err := os.ReadFile(scoutPlanPath)
	if err != nil {
		t.Fatalf("read scout plan.md failed: %v", err)
	}
	scoutPlan := string(scoutPlanBytes)
	if !strings.Contains(scoutPlan, "Discovery & Reconnaissance") {
		t.Errorf("scout plan missing Phase 1 Discovery: %s", scoutPlan)
	}
	if !strings.Contains(scoutPlan, "Analysis & Evidence Gathering") {
		t.Errorf("scout plan missing Phase 2 Analysis: %s", scoutPlan)
	}
	if !strings.Contains(scoutPlan, "Documentation & Evidence") {
		t.Errorf("scout plan missing Phase 3 Documentation: %s", scoutPlan)
	}
	if !strings.Contains(scoutPlan, "`chore:`") {
		t.Errorf("scout plan missing chore in Phase 4: %s", scoutPlan)
	}
	if !isDefaultScaffoldPlan(scoutPlan) {
		t.Errorf("expected isDefaultScaffoldPlan=true for scout default plan")
	}

	// 2. Create ship goal without explicit tasks
	shipGoal, err := CreateGoal(CreateGoalParams{
		RootDir:     dir,
		Slug:        "implement-feature",
		Title:       "Implement Feature",
		Description: "Build core feature implementation",
		Shape:       "ship",
	})
	if err != nil {
		t.Fatalf("CreateGoal ship failed: %v", err)
	}
	if shipGoal.Shape != "ship" {
		t.Errorf("expected Shape=ship, got %q", shipGoal.Shape)
	}

	shipPlanPath := filepath.Join(dir, "goals", "implement-feature", "plan.md")
	shipPlanBytes, err := os.ReadFile(shipPlanPath)
	if err != nil {
		t.Fatalf("read ship plan.md failed: %v", err)
	}
	shipPlan := string(shipPlanBytes)
	if !strings.Contains(shipPlan, "Contracts & Schemas") {
		t.Errorf("ship plan missing Phase 1 Contracts: %s", shipPlan)
	}
	if !strings.Contains(shipPlan, "Core Implementation") {
		t.Errorf("ship plan missing Phase 2 Implementation: %s", shipPlan)
	}
	if !strings.Contains(shipPlan, "Verification & Evidence") {
		t.Errorf("ship plan missing Phase 3 Verification: %s", shipPlan)
	}
	if !strings.Contains(shipPlan, "`chore:`") {
		t.Errorf("ship plan missing chore in Phase 4: %s", shipPlan)
	}
	if !isDefaultScaffoldPlan(shipPlan) {
		t.Errorf("expected isDefaultScaffoldPlan=true for ship default plan")
	}
}

func TestCreateGoalClosureChecklistScaffolding(t *testing.T) {
	dir := t.TempDir()
	slug := "closure-checklist"

	_, err := CreateGoal(CreateGoalParams{
		RootDir:     dir,
		Slug:        slug,
		Title:       "Closure Checklist",
		Description: "Exercise closure scaffolding",
		Shape:       "ship",
	})
	if err != nil {
		t.Fatalf("CreateGoal failed: %v", err)
	}

	planBytes, err := os.ReadFile(filepath.Join(dir, "goals", slug, "plan.md"))
	if err != nil {
		t.Fatalf("read plan.md: %v", err)
	}
	plan := string(planBytes)
	for _, want := range []string{
		"Verdict: PASS",
		"sila goals certify " + slug,
		"Attribution & Registry Closure",
		"unverified_count: 0",
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("plan.md missing %q:\n%s", want, plan)
		}
	}
	if !isDefaultScaffoldPlan(plan) {
		t.Errorf("expected updated default plan to be recognized as scaffold")
	}

	goalBytes, err := os.ReadFile(filepath.Join(dir, "goals", slug, "goal.md"))
	if err != nil {
		t.Fatalf("read goal.md: %v", err)
	}
	goalMd := string(goalBytes)
	for _, want := range []string{
		"`- [x]`",
		"(goals/" + slug + "/goal.md)",
		"Verdict: PASS",
		"unverified_count: 0",
	} {
		if !strings.Contains(goalMd, want) {
			t.Errorf("goal.md Done Condition missing %q:\n%s", want, goalMd)
		}
	}

	legacy := `# Plan: legacy

- [ ] **Phase 1 — Contracts & Schemas**: Define models.
- [ ] **Phase 2 — Core Implementation**: Implement logic.
- [ ] **Phase 3 — Verification & Tests**: Write tests.
- [ ] **Phase 4 — Attribution Commit**: Commit and run sila goals.
`
	if !isDefaultScaffoldPlan(legacy) {
		t.Errorf("expected legacy default scaffold plan to remain recognized")
	}
}

func TestReadmePreamblePreservation(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateGoal(CreateGoalParams{
		RootDir: dir,
		Slug:    "preamble-goal",
		Title:   "Preamble Goal",
	}); err != nil {
		t.Fatalf("CreateGoal failed: %v", err)
	}

	readmePath := filepath.Join(dir, "goals", "README.md")
	customBanner := "> [!IMPORTANT]\n> Feature freeze active: only security fixes allowed.\n\n# Goals Status & Pragmatic Execution Roadmap\n"
	if err := os.WriteFile(readmePath, []byte(customBanner), 0644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}

	res, err := ScanGoals(dir)
	if err != nil {
		t.Fatalf("ScanGoals failed: %v", err)
	}
	if err := SyncReadme(dir, res); err != nil {
		t.Fatalf("SyncReadme failed: %v", err)
	}
	updated, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	if !strings.HasPrefix(string(updated), "> [!IMPORTANT]\n> Feature freeze active: only security fixes allowed.\n\n# Goals Status & Pragmatic Execution Roadmap") {
		t.Fatalf("expected custom preamble to be preserved at top of goals/README.md, got:\n%s", string(updated))
	}
	if err := CheckReadmeSync(dir, res); err != nil {
		t.Fatalf("CheckReadmeSync failed with custom preamble: %v", err)
	}
}

func TestScanGoalsCertReceiptCommitSHA(t *testing.T) {
	dir := t.TempDir()
	slug := "private-untracked-goal"
	goalDir := filepath.Join(dir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte("# Goal: Private Untracked Goal\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte("# Plan\n- [x] Done\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "facts.md"), []byte("# Facts\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cert := &CertificationRecord{
		GoalSlug:     slug,
		CertifiedAt:  time.Now().UTC(),
		CertifiedBy:  "reviewer",
		EvidenceFile: "goals/private-untracked-goal/evidence.md",
		Verified:     true,
		CommitSHA:    "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4",
	}
	cBytes, _ := json.MarshalIndent(cert, "", "  ")
	if err := os.WriteFile(filepath.Join(goalDir, "certification.json"), cBytes, 0644); err != nil {
		t.Fatal(err)
	}

	res, err := ScanGoals(dir)
	if err != nil {
		t.Fatalf("ScanGoals failed: %v", err)
	}
	if len(res.Goals) != 1 {
		t.Fatalf("expected 1 goal, got %d", len(res.Goals))
	}
	if res.Goals[0].Commit != "a1b2c3d" {
		t.Fatalf("expected Commit 'a1b2c3d' from certRec.CommitSHA, got %q", res.Goals[0].Commit)
	}
}

func TestScanGoalsRecertWithOlderAuditReceiptAndRetroDep(t *testing.T) {
	dir := t.TempDir()
	runGit := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Tester", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Tester", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, string(out))
		}
	}
	runGit("init", "-b", "main")

	depDir := filepath.Join(dir, "goals", "audit-app-identity")
	targetDir := filepath.Join(dir, "goals", "audit-release-trust-config")
	for _, gd := range []string{depDir, targetDir} {
		if err := os.MkdirAll(gd, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(gd, "plan.md"), []byte("# Plan\n- [x] Done\n"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(gd, "facts.md"), []byte("# Facts\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(depDir, "goal.md"), []byte("# Goal: App Identity\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(depDir, "retrospective.json"), []byte(`{"goal_slug":"audit-app-identity","status":"implemented","commit":"1111111"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "goal.md"), []byte("# Goal: Release Trust\n\n## Dependencies & Execution Order\n- **Depends On**: audit-app-identity\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "audit-receipt.json"), []byte(`{"version":"sila/audit-receipt/v1","goal_slug":"audit-release-trust-config","commit_sha":"0000000000000000000000000000000000000000"}`), 0644); err != nil {
		t.Fatal(err)
	}
	runGit("add", ".")
	runGit("commit", "-m", "fix: initial implementation [goal: audit-release-trust-config]")

	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	implSHA := strings.TrimSpace(string(out))

	cert := &CertificationRecord{
		GoalSlug:     "audit-release-trust-config",
		CertifiedAt:  time.Now().UTC(),
		CertifiedBy:  "reviewer",
		EvidenceFile: "goals/audit-release-trust-config/evidence.md",
		Verified:     true,
		CommitSHA:    implSHA,
	}
	cBytes, _ := json.MarshalIndent(cert, "", "  ")
	if err := os.WriteFile(filepath.Join(targetDir, "certification.json"), cBytes, 0644); err != nil {
		t.Fatal(err)
	}
	runGit("add", ".")
	runGit("commit", "-m", "chore: re-certify release trust [goal: audit-release-trust-config]")

	res, err := ScanGoals(dir)
	if err != nil {
		t.Fatalf("ScanGoals failed: %v", err)
	}
	var gotTarget *Goal
	for i := range res.Goals {
		if res.Goals[i].Slug == "audit-release-trust-config" {
			gotTarget = &res.Goals[i]
		}
	}
	if gotTarget == nil || gotTarget.VerificationStatus != "verified" {
		t.Fatalf("expected audit-release-trust-config to be verified despite older audit-receipt.json, got %+v", gotTarget)
	}
	blockers, err := GoalDependencyBlockers(dir, "audit-release-trust-config")
	if err != nil || len(blockers) != 0 {
		t.Fatalf("expected 0 dependency blockers when prerequisite has retrospective.json, got %v (err=%v)", blockers, err)
	}
}
