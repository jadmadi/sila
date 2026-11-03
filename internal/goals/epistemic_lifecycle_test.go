package goals

import (
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestAcquireMultiGoalLocks_LexicographicalOrderAndDeadlockFree(t *testing.T) {
	tmpDir := t.TempDir()

	slugs := []string{"zeta", "alpha", "gamma", "beta", "delta"}
	for _, s := range slugs {
		_ = os.MkdirAll(filepath.Join(tmpDir, "goals", s), 0755)
		_ = os.WriteFile(filepath.Join(tmpDir, "goals", s, "goal.md"), []byte("# Goal: "+s+"\n"), 0644)
	}

	// 1. Single acquire and release
	unlock, err := AcquireMultiGoalLocks(tmpDir, slugs, 2*time.Second)
	if err != nil {
		t.Fatalf("AcquireMultiGoalLocks failed: %v", err)
	}
	unlock()

	// 2. Concurrency stress test: 20 goroutines acquiring random subsets in random order
	// Without total lexicographical lock ordering, this would immediately deadlock.
	var wg sync.WaitGroup
	errCh := make(chan error, 50)

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			r := rand.New(rand.NewPCG(uint64(workerID+1), uint64(time.Now().UnixNano())))

			// Pick 2-4 random slugs in arbitrary order
			n := 2 + r.IntN(len(slugs)-1)
			shuffled := make([]string, len(slugs))
			copy(shuffled, slugs)
			r.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
			subset := shuffled[:n]

			for iter := 0; iter < 10; iter++ {
				unlock, err := AcquireMultiGoalLocks(tmpDir, subset, 5*time.Second)
				if err != nil {
					errCh <- fmt.Errorf("worker %d failed to lock %v: %w", workerID, subset, err)
					return
				}
				time.Sleep(time.Duration(1+r.IntN(5)) * time.Millisecond)
				unlock()
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrency error: %v", err)
	}
}

func TestRemediationCascadedDescendantTainting(t *testing.T) {
	tmpDir := t.TempDir()
	goalsDir := filepath.Join(tmpDir, "goals")

	// Construct DAG: A -> B -> C (B depends on A, C depends on B)
	goalA := filepath.Join(goalsDir, "service-a")
	goalB := filepath.Join(goalsDir, "service-b")
	goalC := filepath.Join(goalsDir, "service-c")

	_ = os.MkdirAll(goalA, 0755)
	_ = os.MkdirAll(goalB, 0755)
	_ = os.MkdirAll(goalC, 0755)

	_ = os.WriteFile(filepath.Join(goalA, "goal.md"), []byte("# Goal: Service A\n\n## Description\nService A\n\n- **Mode**: independent\n"), 0644)
	_ = os.WriteFile(filepath.Join(goalA, "plan.md"), []byte("# Plan\n- [x] Task A\n"), 0644)
	_ = os.WriteFile(filepath.Join(goalA, "facts.md"), []byte("# Facts\n"), 0644)

	_ = os.WriteFile(filepath.Join(goalB, "goal.md"), []byte("# Goal: Service B\n\n## Description\nService B\n\n- **Depends On**: service-a\n"), 0644)
	_ = os.WriteFile(filepath.Join(goalB, "plan.md"), []byte("# Plan\n- [x] Task B\n"), 0644)
	_ = os.WriteFile(filepath.Join(goalB, "facts.md"), []byte("# Facts\n"), 0644)

	_ = os.WriteFile(filepath.Join(goalC, "goal.md"), []byte("# Goal: Service C\n\n## Description\nService C\n\n- **Depends On**: service-b\n"), 0644)
	_ = os.WriteFile(filepath.Join(goalC, "plan.md"), []byte("# Plan\n- [x] Task C\n"), 0644)
	_ = os.WriteFile(filepath.Join(goalC, "facts.md"), []byte("# Facts\n"), 0644)

	// Simulate initial git commits verifying all 3 goals
	// We use certification records or retrospective to simulate implemented state without git
	retroA := &RetrospectiveRecord{GoalSlug: "service-a", Commit: "commit-a"}
	retroB := &RetrospectiveRecord{GoalSlug: "service-b", Commit: "commit-b"}
	retroC := &RetrospectiveRecord{GoalSlug: "service-c", Commit: "commit-c"}
	saveRetrospective(goalA, retroA)
	saveRetrospective(goalB, retroB)
	saveRetrospective(goalC, retroC)

	// Audit before reopening: all 3 should be implemented
	auditInit, err := ScanGoals(tmpDir)
	if err != nil {
		t.Fatalf("ScanGoals initial failed: %v", err)
	}
	if auditInit.ImplementedCount != 3 {
		t.Fatalf("expected 3 implemented goals, got %d", auditInit.ImplementedCount)
	}

	// Now reopen goal A for remediation
	reopenRec, _, err := ReopenGoal(tmpDir, "service-a", "tester", "found critical flaw in A", false)
	if err != nil {
		t.Fatalf("ReopenGoal failed: %v", err)
	}
	if !reopenRec.Active {
		t.Fatalf("expected active reopen record")
	}
	if reopenRec.ReopenEpoch != 1 {
		t.Errorf("ReopenEpoch = %d, want 1", reopenRec.ReopenEpoch)
	}
	if len(reopenRec.TaintedGoals) != 2 || reopenRec.TaintedGoals[0] != "service-b" || reopenRec.TaintedGoals[1] != "service-c" {
		t.Errorf("TaintedGoals = %v, want [service-b, service-c]", reopenRec.TaintedGoals)
	}

	// Audit after reopening A:
	// A: status=pending, state=remediation, Reopened=true
	// B: state=tainted, Blocked=true, blocked by prerequisite in remediation
	// C: state=tainted, Blocked=true, blocked by prerequisite in remediation
	auditAfter, err := ScanGoals(tmpDir)
	if err != nil {
		t.Fatalf("ScanGoals after reopen failed: %v", err)
	}

	goalsBySlug := make(map[string]Goal)
	for _, g := range auditAfter.Goals {
		goalsBySlug[g.Slug] = g
	}

	gA := goalsBySlug["service-a"]
	if gA.State != StateRemediation {
		t.Errorf("service-a State = %s, want %s", gA.State, StateRemediation)
	}
	if !gA.Reopened {
		t.Errorf("service-a Reopened = false, want true")
	}
	if gA.Blocked {
		t.Errorf("service-a Blocked = true, want false (ready for remediation)")
	}

	gB := goalsBySlug["service-b"]
	if gB.State != StateTainted {
		t.Errorf("service-b State = %s, want %s", gB.State, StateTainted)
	}
	if !gB.Blocked {
		t.Errorf("service-b Blocked = false, want true")
	}

	gC := goalsBySlug["service-c"]
	if gC.State != StateTainted {
		t.Errorf("service-c State = %s, want %s", gC.State, StateTainted)
	}
	if !gC.Blocked {
		t.Errorf("service-c Blocked = false, want true")
	}

	// Verify claiming tainted goals fails even with force
	_, claimErrB := ClaimGoal(tmpDir, "service-b", "agent", "testing", true)
	if claimErrB == nil {
		t.Fatalf("expected claim of tainted service-b to fail, but it succeeded")
	}

	_, claimErrC := ClaimGoal(tmpDir, "service-c", "agent", "testing", true)
	if claimErrC == nil {
		t.Fatalf("expected claim of tainted service-c to fail, but it succeeded")
	}

	// Verify claiming service-a for remediation succeeds
	claimA, claimErrA := ClaimGoal(tmpDir, "service-a", "agent", "fixing bug in A", false)
	if claimErrA != nil {
		t.Fatalf("failed to claim service-a for remediation: %v", claimErrA)
	}
	if claimA.GoalSlug != "service-a" {
		t.Errorf("claimA slug = %s, want service-a", claimA.GoalSlug)
	}
}

func saveRetrospective(goalDir string, r *RetrospectiveRecord) {
	_ = AtomicWriteFile(filepath.Join(goalDir, "retrospective.json"), []byte(fmt.Sprintf(`{"goal_slug":"%s","status":"implemented","commit":"%s"}`, r.GoalSlug, r.Commit)), 0644)
}
