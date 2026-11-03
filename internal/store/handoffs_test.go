package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func TestHandoffsCRUDAndSupersede(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "k.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if _, err := st.EnsureProject("/tmp/my-app"); err != nil {
		t.Fatal(err)
	}

	h1 := Handoff{
		ProjectID: "my-app",
		Agent:     "devin",
		Branch:    "feat/auth",
		Summary:   "Implemented JWT auth verification",
		OpenItems: []string{"Add token refresh", "Write unit tests"},
		Decisions: []string{"Use RS256 algorithm"},
		RepoFiles: []string{"lib/auth.ts"},
		CreatedAt: "2026-08-15T10:00:00Z",
		Status:    "open",
	}

	if err := st.InsertHandoff(h1); err != nil {
		t.Fatalf("InsertHandoff failed: %v", err)
	}

	latest, err := st.LatestHandoff("my-app")
	if err != nil {
		t.Fatalf("LatestHandoff failed: %v", err)
	}
	if latest == nil || latest.Summary != h1.Summary {
		t.Fatalf("unexpected latest handoff: %+v", latest)
	}

	// Insert second open handoff -> first handoff should become superseded
	h2 := Handoff{
		ProjectID: "my-app",
		Agent:     "claude-code",
		Branch:    "feat/auth-refresh",
		Summary:   "Added token refresh endpoint",
		OpenItems: []string{"Deploy to staging"},
		Decisions: []string{"Token TTL set to 15m"},
		CreatedAt: "2026-08-15T12:00:00Z",
		Status:    "open",
	}

	if err := st.InsertHandoff(h2); err != nil {
		t.Fatalf("InsertHandoff 2 failed: %v", err)
	}

	latest2, err := st.LatestHandoff("my-app")
	if err != nil {
		t.Fatalf("LatestHandoff 2 failed: %v", err)
	}
	if latest2 == nil || latest2.Summary != h2.Summary {
		t.Fatalf("unexpected latest2 handoff: %+v", latest2)
	}

	all, err := st.HandoffsByProject("my-app", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 handoffs, got %d", len(all))
	}
	// h1 and h2 are on different branches, so both stay open: superseding one
	// branch's handoff with another branch's erased in-progress work (F08).
	bySummary := map[string]string{}
	for _, h := range all {
		bySummary[h.Summary] = h.Status
	}
	if bySummary[h1.Summary] != "open" || bySummary[h2.Summary] != "open" {
		t.Fatalf("handoffs on different branches must both stay open: %+v", bySummary)
	}

	// A newer handoff on h2's own branch replaces h2 only.
	h3 := Handoff{
		ProjectID: "my-app",
		Agent:     "codex",
		Branch:    "feat/auth-refresh",
		Summary:   "Follow-up on token refresh",
		CreatedAt: "2026-08-15T14:00:00Z",
		Status:    "open",
	}
	if err := st.InsertHandoff(h3); err != nil {
		t.Fatalf("InsertHandoff 3 failed: %v", err)
	}
	all, err = st.HandoffsByProject("my-app", 10)
	if err != nil {
		t.Fatal(err)
	}
	bySummary = map[string]string{}
	for _, h := range all {
		bySummary[h.Summary] = h.Status
	}
	if bySummary[h2.Summary] != "superseded" {
		t.Fatalf("the replaced handoff on the same branch should be superseded: %+v", bySummary)
	}
	if bySummary[h1.Summary] != "open" || bySummary[h3.Summary] != "open" {
		t.Fatalf("other branches must be untouched: %+v", bySummary)
	}
}

func TestInsertMemo(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "k.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if _, err := st.EnsureProject("/tmp/app"); err != nil {
		t.Fatal(err)
	}

	fact, err := st.InsertMemo("app", "Always use pure-Go SQLite; never import mattn/go-sqlite3", "architect", "", nil)
	if err != nil {
		t.Fatalf("InsertMemo failed: %v", err)
	}
	if fact.Distilled != "Always use pure-Go SQLite; never import mattn/go-sqlite3" {
		t.Fatalf("unexpected distilled fact: %+v", fact)
	}
	if !fact.IsFact() {
		t.Fatalf("memo should qualify as verified fact: %+v", fact)
	}

	// Verify indexed in FTS5
	facts, err := st.SearchFactsFTS("pure-Go", "app", false, 10)
	if err != nil {
		t.Fatalf("SearchFactsFTS failed: %v", err)
	}
	if len(facts) != 1 || !facts[0].IsFact() {
		t.Fatalf("expected 1 fact in search, got %v", facts)
	}
}

func TestClaimHandoff_ConcurrencyRace(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "k.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if _, err := st.EnsureProject("/tmp/race-app"); err != nil {
		t.Fatal(err)
	}

	h := Handoff{
		ProjectID: "race-app",
		Agent:     "devin",
		Branch:    "feat/atomic-cas",
		Summary:   "Atomic CAS single winner",
		CreatedAt: "2026-08-15T10:00:00Z",
		Status:    string(HandoffPending),
	}
	h.ID = HandoffID(h.ProjectID, h.Summary, h.CreatedAt)
	if err := st.InsertHandoff(h); err != nil {
		t.Fatalf("InsertHandoff failed: %v", err)
	}

	concurrency := 20
	var successCount int32
	var alreadyClaimedCount int32
	var wg sync.WaitGroup
	startGate := make(chan struct{})

	ctx := context.Background()

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			<-startGate
			agent := fmt.Sprintf("crewmate-%d", workerID)
			sess := fmt.Sprintf("session-%d", workerID)
			_, claimErr := st.ClaimHandoff(ctx, h.ID, agent, sess)
			if claimErr == nil {
				atomic.AddInt32(&successCount, 1)
			} else if errors.Is(claimErr, ErrHandoffAlreadyClaimed) {
				atomic.AddInt32(&alreadyClaimedCount, 1)
			} else {
				t.Errorf("worker %d unexpected error: %v", workerID, claimErr)
			}
		}(i)
	}

	close(startGate)
	wg.Wait()

	if successCount != 1 {
		t.Fatalf("expected exactly 1 winner out of %d concurrent workers, got %d", concurrency, successCount)
	}
	if alreadyClaimedCount != int32(concurrency-1) {
		t.Fatalf("expected %d ErrHandoffAlreadyClaimed, got %d", concurrency-1, alreadyClaimedCount)
	}

	claimed, err := st.GetHandoff(ctx, h.ID)
	if err != nil {
		t.Fatalf("GetHandoff failed: %v", err)
	}
	if claimed.Status != string(HandoffClaimed) {
		t.Fatalf("expected status claimed, got %s", claimed.Status)
	}
	if claimed.ClaimedBy == "" || claimed.ClaimedAt == "" || claimed.SessionID == "" {
		t.Fatalf("expected claim metadata populated, got: %+v", claimed)
	}
}

func TestClaimHandoff_IdempotentRetry(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "k.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if _, err := st.EnsureProject("/tmp/idempotent-app"); err != nil {
		t.Fatal(err)
	}

	h := Handoff{
		ProjectID: "idempotent-app",
		Agent:     "devin",
		Branch:    "feat/retry",
		Summary:   "Retry handoff",
		CreatedAt: "2026-08-15T10:00:00Z",
		Status:    string(HandoffPending),
	}
	h.ID = HandoffID(h.ProjectID, h.Summary, h.CreatedAt)
	if err := st.InsertHandoff(h); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	c1, err := st.ClaimHandoff(ctx, h.ID, "antigravity", "sess-123")
	if err != nil {
		t.Fatalf("first claim failed: %v", err)
	}
	if c1.Status != string(HandoffClaimed) || c1.ClaimedBy != "antigravity" {
		t.Fatalf("unexpected claim: %+v", c1)
	}

	// Idempotent retry with exact same session ID
	c2, err := st.ClaimHandoff(ctx, h.ID, "antigravity", "sess-123")
	if err != nil {
		t.Fatalf("idempotent re-claim failed: %v", err)
	}
	if c2.ID != c1.ID {
		t.Fatalf("expected same handoff returned on retry")
	}

	// Different session ID must fail with ErrHandoffAlreadyClaimed
	_, err = st.ClaimHandoff(ctx, h.ID, "other-agent", "sess-456")
	if !errors.Is(err, ErrHandoffAlreadyClaimed) {
		t.Fatalf("expected ErrHandoffAlreadyClaimed for different session, got: %v", err)
	}
}

func TestPopEligibleHandoff_BranchIsolation(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "k.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if _, err := st.EnsureProject("/tmp/branch-app"); err != nil {
		t.Fatal(err)
	}

	hA := Handoff{
		ProjectID: "branch-app",
		Agent:     "agent-a",
		Branch:    "feat/branch-a",
		Summary:   "Work on branch A",
		CreatedAt: "2026-08-15T10:00:00Z",
		Status:    string(HandoffPending),
	}
	hA.ID = HandoffID(hA.ProjectID, hA.Summary, hA.CreatedAt)
	hB := Handoff{
		ProjectID: "branch-app",
		Agent:     "agent-b",
		Branch:    "feat/branch-b",
		Summary:   "Work on branch B",
		CreatedAt: "2026-08-15T10:00:00Z",
		Status:    string(HandoffPending),
	}
	hB.ID = HandoffID(hB.ProjectID, hB.Summary, hB.CreatedAt)
	if err := st.InsertHandoff(hA); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertHandoff(hB); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	// Pop for branch A
	poppedA, err := st.PopEligibleHandoff(ctx, "branch-app", "feat/branch-a", "", "consumer-a", "sess-a")
	if err != nil {
		t.Fatalf("pop A failed: %v", err)
	}
	if poppedA == nil || poppedA.Summary != "Work on branch A" {
		t.Fatalf("unexpected popped A: %+v", poppedA)
	}
	if poppedA.Status != string(HandoffClaimed) {
		t.Fatalf("expected popped A claimed, got: %s", poppedA.Status)
	}

	// Verify branch B is still pending and untouched
	hBFromStore, err := st.GetHandoff(ctx, hB.ID)
	if err != nil {
		t.Fatalf("get B failed: %v", err)
	}
	if !hBFromStore.IsPending() {
		t.Fatalf("branch B handoff must remain pending, got: %s", hBFromStore.Status)
	}

	// Pop for branch B
	poppedB, err := st.PopEligibleHandoff(ctx, "branch-app", "feat/branch-b", "", "consumer-b", "sess-b")
	if err != nil {
		t.Fatalf("pop B failed: %v", err)
	}
	if poppedB == nil || poppedB.Summary != "Work on branch B" {
		t.Fatalf("unexpected popped B: %+v", poppedB)
	}
}

func TestPopEligibleHandoff_AutoSupersede(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "k.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if _, err := st.EnsureProject("/tmp/auto-app"); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	// 1. Older automated handoff
	hOldAuto := Handoff{
		ProjectID: "auto-app",
		Agent:     "auto",
		Branch:    "main",
		Summary:   "Old automated snapshot",
		CreatedAt: "2026-08-01T10:00:00Z",
		Status:    string(HandoffPending),
	}
	hOldAuto.ID = HandoffID(hOldAuto.ProjectID, hOldAuto.Summary, hOldAuto.CreatedAt)

	// 2. Manual handoff
	hManual := Handoff{
		ProjectID: "auto-app",
		Agent:     "human",
		Branch:    "main",
		Summary:   "Important human instructions",
		CreatedAt: "2026-08-02T10:00:00Z",
		Status:    string(HandoffPending),
	}
	hManual.ID = HandoffID(hManual.ProjectID, hManual.Summary, hManual.CreatedAt)

	// 3. Newest handoff
	hNew := Handoff{
		ProjectID: "auto-app",
		Agent:     "antigravity",
		Branch:    "main",
		Summary:   "Latest active work",
		CreatedAt: "2026-08-03T10:00:00Z",
		Status:    string(HandoffPending),
	}
	hNew.ID = HandoffID(hNew.ProjectID, hNew.Summary, hNew.CreatedAt)

	if err := st.InsertHandoff(hOldAuto); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertHandoff(hManual); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertHandoff(hNew); err != nil {
		t.Fatal(err)
	}

	popped, err := st.PopEligibleHandoff(ctx, "auto-app", "main", "", "consumer", "sess-pop")
	if err != nil {
		t.Fatalf("PopEligibleHandoff failed: %v", err)
	}
	if popped == nil || popped.Summary != "Latest active work" {
		t.Fatalf("expected newest handoff popped, got: %+v", popped)
	}

	// Verify old automated handoff was superseded
	oldAuto, err := st.GetHandoff(ctx, hOldAuto.ID)
	if err != nil {
		t.Fatal(err)
	}
	if oldAuto.Status != string(HandoffSuperseded) {
		t.Fatalf("expected old automated handoff superseded, got: %s", oldAuto.Status)
	}

	// Verify manual handoff was preserved (not superseded)
	manual, err := st.GetHandoff(ctx, hManual.ID)
	if err != nil {
		t.Fatal(err)
	}
	if manual.Status != string(HandoffPending) {
		t.Fatalf("manual handoff should stay pending, got: %s", manual.Status)
	}
}
