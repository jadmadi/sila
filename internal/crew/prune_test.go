package crew

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

func TestPrune_LiveDetachedWatcher(t *testing.T) {
	mgr, dir := setupTestCrewManager(t)
	dbDir := filepath.Join(dir, ".sila")
	_ = os.MkdirAll(dbDir, 0755)
	dbPath := filepath.Join(dbDir, "k.db")

	// Start a simulated live watcher process (detached sleep)
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start live watcher mock: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	pid := cmd.Process.Pid
	ident := store.InspectProcess(pid)

	// Create a scoped live lock matching the live process
	lockFile := store.LiveLockPathScoped(dbPath, "test-proj")
	info := store.LiveLockInfo{
		PID:        pid,
		Project:    "test-proj",
		StartedAt:  ident.StartTime,
		Hostname:   "test-host",
		Token:      "live-token-123",
		StartTicks: ident.StartTicks,
	}
	data, _ := json.Marshal(info)
	if err := os.WriteFile(lockFile, data, 0600); err != nil {
		t.Fatalf("write lock file: %v", err)
	}

	// Run Prune
	rep, err := mgr.Prune(PruneOptions{
		DryRun:  false,
		DBPaths: []string{dbPath},
	})
	if err != nil {
		t.Fatalf("Prune failed: %v", err)
	}

	// Verify live watcher was preserved
	found := false
	for _, ev := range rep.Evaluations {
		if ev.Identifier == lockFile {
			found = true
			if ev.Action != ActionRetained {
				t.Fatalf("expected live lock retained, got %s (reason: %s)", ev.Action, ev.Reason)
			}
		}
	}
	if !found {
		t.Fatalf("lock file %s not evaluated in report", lockFile)
	}

	// Verify file still exists on disk
	if _, err := os.Stat(lockFile); os.IsNotExist(err) {
		t.Fatalf("live lock file was evicted unexpectedly")
	}

	// Verify process is still alive
	checkIdent := store.InspectProcess(pid)
	if !checkIdent.Alive {
		t.Fatalf("live watcher process was killed unexpectedly")
	}
}

func TestPrune_ReusedPID(t *testing.T) {
	mgr, dir := setupTestCrewManager(t)
	dbDir := filepath.Join(dir, ".sila")
	_ = os.MkdirAll(dbDir, 0755)
	dbPath := filepath.Join(dbDir, "k.db")

	// Start a process that represents a newly spawned unrelated program
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	pid := cmd.Process.Pid

	// Create a lock with that PID, but StartedAt 2 hours ago (reused PID scenario)
	lockFile := store.LiveLockPathScoped(dbPath, "reused-proj")
	staleInfo := store.LiveLockInfo{
		PID:        pid,
		Project:    "reused-proj",
		StartedAt:  time.Now().Add(-2 * time.Hour),
		Hostname:   "test-host",
		Token:      "old-token",
		StartTicks: 1, // Will mismatch current process start ticks
	}
	data, _ := json.Marshal(staleInfo)
	if err := os.WriteFile(lockFile, data, 0600); err != nil {
		t.Fatalf("write lock file: %v", err)
	}

	// 1. Without --force: should preserve without termination
	rep, err := mgr.Prune(PruneOptions{
		DryRun:  false,
		Force:   false,
		DBPaths: []string{dbPath},
	})
	if err != nil {
		t.Fatalf("Prune without force failed: %v", err)
	}

	for _, ev := range rep.Evaluations {
		if ev.Identifier == lockFile {
			if ev.Action != ActionRetained {
				t.Fatalf("expected lock retained when PID reused without force, got %s", ev.Action)
			}
		}
	}

	// Process must NOT be killed!
	if !store.InspectProcess(pid).Alive {
		t.Fatalf("reused PID process was killed by prune without force!")
	}
	if _, err := os.Stat(lockFile); os.IsNotExist(err) {
		t.Fatalf("lock was evicted without force")
	}

	// 2. With --force: should evict stale lock BUT NEVER terminate process
	repForce, err := mgr.Prune(PruneOptions{
		DryRun:  false,
		Force:   true,
		DBPaths: []string{dbPath},
	})
	if err != nil {
		t.Fatalf("Prune with force failed: %v", err)
	}

	foundEvicted := false
	for _, ev := range repForce.Evaluations {
		if ev.Identifier == lockFile {
			foundEvicted = true
			if ev.Action != ActionPruned {
				t.Fatalf("expected lock pruned with force, got %s", ev.Action)
			}
		}
	}
	if !foundEvicted {
		t.Fatalf("expected lock evaluation in report")
	}

	// Lock file should be removed
	if _, err := os.Stat(lockFile); !os.IsNotExist(err) {
		t.Fatalf("expected lock file removed with force, but it still exists")
	}

	// Process MUST STILL BE ALIVE!
	if !store.InspectProcess(pid).Alive {
		t.Fatalf("reused PID process was terminated by prune with force!")
	}
}

func TestPrune_UnknownOwnership(t *testing.T) {
	mgr, dir := setupTestCrewManager(t)
	dbDir := filepath.Join(dir, ".sila")
	_ = os.MkdirAll(dbDir, 0755)
	dbPath := filepath.Join(dbDir, "k.db")

	// Write an invalid/corrupted lock file
	lockFile := store.LiveLockPathScoped(dbPath, "corrupt-proj")
	if err := os.WriteFile(lockFile, []byte("{bad-json-payload}"), 0600); err != nil {
		t.Fatalf("write corrupted lock: %v", err)
	}

	rep, err := mgr.Prune(PruneOptions{
		DryRun:  false,
		DBPaths: []string{dbPath},
	})
	if err != nil {
		t.Fatalf("Prune failed: %v", err)
	}

	found := false
	for _, ev := range rep.Evaluations {
		if ev.Identifier == lockFile {
			found = true
			if ev.Action != ActionRetained {
				t.Fatalf("expected corrupted lock retained safely, got %s", ev.Action)
			}
		}
	}
	if !found {
		t.Fatalf("corrupted lock file not evaluated")
	}

	// Verify file is preserved safely
	if _, err := os.Stat(lockFile); os.IsNotExist(err) {
		t.Fatalf("corrupted lock file was deleted unsafely")
	}
}

func TestPrune_ConcurrentPrune(t *testing.T) {
	_, dir := setupTestCrewManager(t)
	dbDir := filepath.Join(dir, ".sila")
	_ = os.MkdirAll(dbDir, 0755)
	dbPath := filepath.Join(dbDir, "k.db")

	// Create 5 dead locks (PID 99999999)
	for i := range 5 {
		name := filepath.Join(dbDir, filepath.Base(store.LiveLockPathScoped(dbPath, filepath.Join("dead", string(rune('a'+i))))))
		info := store.LiveLockInfo{
			PID:       99999990 + i,
			Project:   "dead",
			StartedAt: time.Now().Add(-1 * time.Hour),
			Hostname:  "test-host",
		}
		data, _ := json.Marshal(info)
		_ = os.WriteFile(name, data, 0600)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 10)

	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.PruneDeadLocks(dbPath, store.PruneDeadLocksOpts{
				DryRun: false,
				Force:  false,
			})
			if err != nil {
				errCh <- err
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("concurrent prune encountered error: %v", err)
	}
}

func TestPrune_DryRun(t *testing.T) {
	mgr, dir := setupTestCrewManager(t)
	dbDir := filepath.Join(dir, ".sila")
	_ = os.MkdirAll(dbDir, 0755)
	dbPath := filepath.Join(dbDir, "k.db")

	// 1. Dead scoped lock
	deadLock := store.LiveLockPathScoped(dbPath, "dead-lock")
	staleInfo := store.LiveLockInfo{
		PID:       99999991,
		Project:   "dead-lock",
		StartedAt: time.Now().Add(-1 * time.Hour),
		Hostname:  "test-host",
	}
	data, _ := json.Marshal(staleInfo)
	_ = os.WriteFile(deadLock, data, 0600)

	// 2. Dead crewmate with clean worktree
	cm, err := mgr.Spawn("devin", "test-dryrun", "HEAD")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	cm.PID = 99999992 // dead PID
	cm.Status = StatusCompleted
	_ = mgr.SaveCrewmate(cm)

	rep, err := mgr.Prune(PruneOptions{
		DryRun:  true,
		DBPaths: []string{dbPath},
	})
	if err != nil {
		t.Fatalf("Prune dry-run: %v", err)
	}

	if !rep.DryRun {
		t.Fatalf("expected DryRun = true in report")
	}

	// Verify nothing was actually deleted
	if _, err := os.Stat(deadLock); os.IsNotExist(err) {
		t.Fatalf("dry-run deleted dead lock file")
	}
	if _, err := os.Stat(cm.Worktree); os.IsNotExist(err) {
		t.Fatalf("dry-run deleted crewmate worktree")
	}
	if _, err := mgr.Get(cm.ID); err != nil {
		t.Fatalf("dry-run deleted crewmate record")
	}
}

func TestPrune_DirtyWorktrees(t *testing.T) {
	mgr, _ := setupTestCrewManager(t)

	// Spawn a crewmate
	cm, err := mgr.Spawn("devin", "test-dirty-wt", "HEAD")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	cm.PID = 99999993 // dead PID
	cm.Status = StatusCompleted
	_ = mgr.SaveCrewmate(cm)

	// Create an uncommitted/untracked file in the worktree
	dirtyFile := filepath.Join(cm.Worktree, "untracked.txt")
	if err := os.WriteFile(dirtyFile, []byte("uncommitted user work"), 0644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}

	// Run Prune without dry-run
	rep, err := mgr.Prune(PruneOptions{DryRun: false})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}

	if rep.ManualFollowup < 1 {
		t.Fatalf("expected manual_followup count >= 1 for dirty worktree, got %d", rep.ManualFollowup)
	}

	foundDirty := false
	for _, ev := range rep.Evaluations {
		if ev.Identifier == cm.Worktree {
			foundDirty = true
			if ev.Action != ActionManualFollowup {
				t.Fatalf("expected action manual_followup for dirty worktree, got %s", ev.Action)
			}
		}
	}
	if !foundDirty {
		t.Fatalf("expected dirty worktree evaluation in report")
	}

	// Verify worktree still exists on disk!
	if _, err := os.Stat(cm.Worktree); os.IsNotExist(err) {
		t.Fatalf("dirty worktree was removed unexpectedly!")
	}
	if _, err := os.Stat(dirtyFile); os.IsNotExist(err) {
		t.Fatalf("dirty untracked file was deleted!")
	}
}

func TestPrune_CleanWorktreeRemoval(t *testing.T) {
	mgr, _ := setupTestCrewManager(t)

	// Spawn a crewmate
	cm, err := mgr.Spawn("devin", "test-clean-wt", "HEAD")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	cm.PID = 99999994 // dead PID
	cm.Status = StatusCompleted
	_ = mgr.SaveCrewmate(cm)

	// Run Prune
	rep, err := mgr.Prune(PruneOptions{DryRun: false})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}

	foundPruned := false
	for _, ev := range rep.Evaluations {
		if ev.Identifier == cm.Worktree {
			foundPruned = true
			if ev.Action != ActionPruned {
				t.Fatalf("expected clean worktree pruned, got %s (reason: %s)", ev.Action, ev.Reason)
			}
		}
	}
	if !foundPruned {
		t.Fatalf("expected clean worktree evaluation in report")
	}

	// Worktree should be removed from disk
	if _, err := os.Stat(cm.Worktree); !os.IsNotExist(err) {
		t.Fatalf("expected clean worktree removed, but it still exists")
	}

	// State file should be pruned
	if _, err := mgr.Get(cm.ID); err == nil {
		t.Fatalf("expected crewmate state file deleted, but it still exists")
	}
}

func TestPrune_UserWindowsInTmux(t *testing.T) {
	if !tmuxAvailable() {
		t.Skip("tmux not available")
	}

	mgr, _ := setupTestCrewManager(t)
	session := "test-crew-user-wins"

	// Create a test tmux session with a user window
	cmd := exec.Command("tmux", "new-session", "-d", "-s", session, "-n", "user-shell")
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to create tmux test session: %v", err)
	}
	defer func() {
		_ = exec.Command("tmux", "kill-session", "-t", session).Run()
	}()

	// Attempt cleanup: since 'user-shell' is not a tracked crewmate, session must be preserved!
	mgr.cleanupTmuxSessionIfIdle(session)

	// Verify session still exists
	checkCmd := exec.Command("tmux", "has-session", "-t", session)
	if err := checkCmd.Run(); err != nil {
		t.Fatalf("tmux session containing user window was killed unexpectedly!")
	}
}

func TestPrune_PreservesUserModifiedContext(t *testing.T) {
	mgr, _ := setupTestCrewManager(t)

	// Spawn a crewmate
	cm, err := mgr.Spawn("devin", "test-user-modified-ctx", "HEAD")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	cm.PID = 99999995 // dead PID
	cm.Status = StatusCompleted
	if err := mgr.SaveCrewmate(cm); err != nil {
		t.Fatalf("SaveCrewmate: %v", err)
	}

	// Append custom user notes to SILA_CONTEXT.md
	ctxPath := filepath.Join(cm.Worktree, "SILA_CONTEXT.md")
	userNotes := "\n## User Notes\nImportant user notes that must survive pruning.\n"
	f, err := os.OpenFile(ctxPath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	if _, err := f.WriteString(userNotes); err != nil {
		_ = f.Close()
		t.Fatalf("WriteString: %v", err)
	}
	_ = f.Close()

	// Run Prune without dry-run
	rep, err := mgr.Prune(PruneOptions{DryRun: false})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}

	// Verify report marked worktree for manual followup
	foundWT := false
	for _, ev := range rep.Evaluations {
		if ev.Identifier == cm.Worktree {
			foundWT = true
			if ev.Action != ActionManualFollowup {
				t.Fatalf("expected worktree action manual_followup, got %s (reason: %s)", ev.Action, ev.Reason)
			}
		}
	}
	if !foundWT {
		t.Fatalf("expected worktree evaluation in report")
	}

	// Verify worktree and SILA_CONTEXT.md still exist on disk
	if _, err := os.Stat(cm.Worktree); os.IsNotExist(err) {
		t.Fatalf("worktree was deleted unexpectedly")
	}
	data, err := os.ReadFile(ctxPath)
	if err != nil {
		t.Fatalf("failed to read context file: %v", err)
	}
	if !strings.Contains(string(data), "Important user notes that must survive pruning.") {
		t.Fatalf("user notes were lost or corrupted from context file")
	}

	// Verify crewmate record is retained
	if _, err := mgr.Get(cm.ID); err != nil {
		t.Fatalf("crewmate state record was deleted unexpectedly: %v", err)
	}
}

func TestPrune_PreservesUserContext_DirectWorktree(t *testing.T) {
	// Directly reproduces cli/docs/evaluations/2026-10-08-prune-context-probe.go.txt
	root := t.TempDir()
	git := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %s: %v", args, out, err)
		}
	}
	git("init", "-q")
	git("-c", "user.name=Evaluation", "-c", "user.email=evaluation@example.invalid", "commit", "--allow-empty", "-qm", "fixture")
	wt := filepath.Join(root, ".git", "sila-worktrees", "crew-probe")
	git("worktree", "add", "--detach", wt, "HEAD")
	target := filepath.Join(wt, "SILA_CONTEXT.md")
	userContent := []byte("User notes added after the generated context. Preserve these.\n")
	if err := os.WriteFile(target, userContent, 0600); err != nil {
		t.Fatalf("write context: %v", err)
	}

	mgr, err := NewCrewManager(root)
	if err != nil {
		t.Fatalf("NewCrewManager: %v", err)
	}
	if err := mgr.SaveCrewmate(&Crewmate{ID: "probe", Harness: "codex", Worktree: wt, Status: StatusCompleted}); err != nil {
		t.Fatalf("SaveCrewmate: %v", err)
	}

	report, err := mgr.Prune(PruneOptions{DBPaths: []string{filepath.Join(root, "absent.db")}})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}

	_, statErr := os.Stat(target)
	userContextDeleted := os.IsNotExist(statErr)
	if userContextDeleted {
		t.Fatalf("expected user context preserved, but it was deleted!")
	}
	if report.PrunedCount > 0 {
		t.Fatalf("expected 0 pruned resources for dirty worktree, got %d", report.PrunedCount)
	}
}

func TestPrune_PreservesUserContext_OrphanWorktree(t *testing.T) {
	mgr, dir := setupTestCrewManager(t)

	// Create an orphan worktree without any tracked crewmate
	wtDir := mgr.wtMgr.WorktreeDir()
	_ = os.MkdirAll(wtDir, 0755)
	orphanName := "crew-orphan-probe"
	orphanPath := filepath.Join(wtDir, orphanName)

	git := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %s: %v", args, out, err)
		}
	}
	git("worktree", "add", "--detach", orphanPath, "HEAD")

	// Write custom user notes into SILA_CONTEXT.md
	target := filepath.Join(orphanPath, "SILA_CONTEXT.md")
	userNotes := "Orphan custom notes that should not be pruned.\n"
	if err := os.WriteFile(target, []byte(userNotes), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	rep, err := mgr.Prune(PruneOptions{DryRun: false})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}

	foundOrphan := false
	for _, ev := range rep.Evaluations {
		if ev.Identifier == orphanPath {
			foundOrphan = true
			if ev.Action != ActionManualFollowup {
				t.Fatalf("expected orphan worktree action manual_followup, got %s (reason: %s)", ev.Action, ev.Reason)
			}
		}
	}
	if !foundOrphan {
		t.Fatalf("expected orphan worktree evaluation in report")
	}

	// Verify file and worktree still exist
	if _, err := os.Stat(target); os.IsNotExist(err) {
		t.Fatalf("orphan user context was deleted unexpectedly")
	}
	if _, err := os.Stat(orphanPath); os.IsNotExist(err) {
		t.Fatalf("orphan worktree was deleted unexpectedly")
	}
}

func TestPrune_CleanGeneratedContextIsPruned(t *testing.T) {
	mgr, _ := setupTestCrewManager(t)

	// Spawn a crewmate and leave SILA_CONTEXT.md untouched
	cm, err := mgr.Spawn("devin", "test-clean-ctx-pruned", "HEAD")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	cm.PID = 99999996 // dead PID
	cm.Status = StatusCompleted
	if err := mgr.SaveCrewmate(cm); err != nil {
		t.Fatalf("SaveCrewmate: %v", err)
	}

	// Context file was generated and untouched
	ctxPath := filepath.Join(cm.Worktree, "SILA_CONTEXT.md")
	if _, err := os.Stat(ctxPath); err != nil {
		t.Fatalf("expected seeded context file: %v", err)
	}

	// Run Prune without dry-run
	rep, err := mgr.Prune(PruneOptions{DryRun: false})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}

	foundPruned := false
	for _, ev := range rep.Evaluations {
		if ev.Identifier == cm.Worktree {
			foundPruned = true
			if ev.Action != ActionPruned {
				t.Fatalf("expected clean worktree pruned, got %s (reason: %s)", ev.Action, ev.Reason)
			}
		}
	}
	if !foundPruned {
		t.Fatalf("expected worktree evaluation in report")
	}

	// Worktree should be gone
	if _, err := os.Stat(cm.Worktree); !os.IsNotExist(err) {
		t.Fatalf("expected clean worktree removed, but it still exists")
	}
}
