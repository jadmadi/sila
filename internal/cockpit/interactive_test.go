package cockpit

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

func TestInteractiveState_Quit(t *testing.T) {
	state := &interactiveState{}

	if !state.handleKey('q', nil, "", 15*time.Second) {
		t.Errorf("expected 'q' to return true (exit)")
	}
	if !state.handleKey('Q', nil, "", 15*time.Second) {
		t.Errorf("expected 'Q' to return true (exit)")
	}
	if !state.handleKey(3, nil, "", 15*time.Second) {
		t.Errorf("expected Ctrl+C (3) to return true (exit)")
	}
}

func TestInteractiveState_PauseToggle(t *testing.T) {
	state := &interactiveState{}

	// Initial state
	if state.isPaused.Load() {
		t.Fatalf("expected initial paused state to be false")
	}

	// Press space -> Pause
	exit := state.handleKey(' ', nil, "", 15*time.Second)
	if exit {
		t.Errorf("expected ' ' not to exit")
	}
	if !state.isPaused.Load() {
		t.Errorf("expected isPaused to be true after pressing space")
	}
	if state.lastStatusNotice != "PAUSED (Press Space to Resume)" {
		t.Errorf("unexpected pause notice: %q", state.lastStatusNotice)
	}

	// Press space again -> Resume
	exit = state.handleKey(' ', nil, "", 15*time.Second)
	if exit {
		t.Errorf("expected ' ' not to exit")
	}
	if state.isPaused.Load() {
		t.Errorf("expected isPaused to be false after pressing space again")
	}
	if !strings.Contains(state.lastStatusNotice, "RESUMED (Live 15s)") {
		t.Errorf("unexpected resume notice: %q", state.lastStatusNotice)
	}
}

func TestInteractiveState_ApprovalHotkeys_NoPending(t *testing.T) {
	state := &interactiveState{
		latestSnap: &CockpitSnapshot{
			Approvals: ApprovalBox{
				HasPending: false,
			},
		},
	}

	state.handleKey('a', nil, "", 15*time.Second)
	if state.lastStatusNotice != "ℹ No pending holds to approve" {
		t.Errorf("unexpected approve notice when empty: %q", state.lastStatusNotice)
	}

	state.handleKey('r', nil, "", 15*time.Second)
	if state.lastStatusNotice != "ℹ No pending holds to reject" {
		t.Errorf("unexpected reject notice when empty: %q", state.lastStatusNotice)
	}

	// Nil snapshot case
	stateNil := &interactiveState{latestSnap: nil}
	stateNil.handleKey('a', nil, "", 15*time.Second)
	if stateNil.lastStatusNotice != "ℹ No pending holds to approve" {
		t.Errorf("unexpected approve notice with nil snap: %q", stateNil.lastStatusNotice)
	}
	stateNil.handleKey('r', nil, "", 15*time.Second)
	if stateNil.lastStatusNotice != "ℹ No pending holds to reject" {
		t.Errorf("unexpected reject notice with nil snap: %q", stateNil.lastStatusNotice)
	}
}

func TestInteractiveState_ApprovalHotkeys_WithPending(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_approvals.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open test store: %v", err)
	}
	defer st.Close()

	// Insert approval 1
	appr1 := &store.Approval{
		ID:        "appr_001",
		Project:   "test-proj",
		Agent:     "@devin",
		Prompt:    "Delete test files",
		RiskLevel: "high",
		Status:    "pending",
	}
	if err := st.InsertApproval(appr1); err != nil {
		t.Fatalf("failed to insert approval: %v", err)
	}

	state := &interactiveState{
		latestSnap: &CockpitSnapshot{
			Approvals: ApprovalBox{
				HasPending: true,
				Pending: []PendingApprovalItem{
					{ID: "appr_001", Agent: "@devin", Action: "Delete test files"},
				},
			},
		},
	}

	// Test Approve [a]
	state.handleKey('a', st, "test-proj", 15*time.Second)
	if state.lastStatusNotice != "✔ Approved #appr_001" {
		t.Errorf("unexpected approve notice: %q", state.lastStatusNotice)
	}

	// Verify in database
	fetched, err := st.GetApproval("appr_001")
	if err != nil {
		t.Fatalf("failed to get approval: %v", err)
	}
	if fetched.Status != "approved" {
		t.Errorf("expected approval status 'approved', got %q", fetched.Status)
	}

	// Insert approval 2
	appr2 := &store.Approval{
		ID:        "appr_002",
		Project:   "test-proj",
		Agent:     "@devin",
		Prompt:    "Push force to main",
		RiskLevel: "critical",
		Status:    "pending",
	}
	if err := st.InsertApproval(appr2); err != nil {
		t.Fatalf("failed to insert approval 2: %v", err)
	}

	state.latestSnap.Approvals.Pending = []PendingApprovalItem{
		{ID: "appr_002", Agent: "@devin", Action: "Push force to main"},
	}

	// Test Reject [r]
	state.handleKey('r', st, "test-proj", 15*time.Second)
	if state.lastStatusNotice != "✖ Rejected #appr_002" {
		t.Errorf("unexpected reject notice: %q", state.lastStatusNotice)
	}

	fetched2, err := st.GetApproval("appr_002")
	if err != nil {
		t.Fatalf("failed to get approval 2: %v", err)
	}
	if fetched2.Status != "rejected" {
		t.Errorf("expected approval status 'rejected', got %q", fetched2.Status)
	}
}

func TestInteractiveState_ProjectFilterCycle(t *testing.T) {
	// 1. Without store, fallback toggling
	state := &interactiveState{}
	state.handleKey('p', nil, "my-repo", 15*time.Second)
	if state.currentProject != "my-repo" {
		t.Errorf("expected currentProject = 'my-repo', got %q", state.currentProject)
	}
	if state.lastStatusNotice != "Filter: Project my-repo" {
		t.Errorf("unexpected notice: %q", state.lastStatusNotice)
	}

	state.handleKey('p', nil, "my-repo", 15*time.Second)
	if state.currentProject != "" {
		t.Errorf("expected currentProject = '', got %q", state.currentProject)
	}
	if state.lastStatusNotice != "Filter: Global Workspace (All Projects)" {
		t.Errorf("unexpected notice: %q", state.lastStatusNotice)
	}

	// 2. With store containing multiple projects
	dbPath := filepath.Join(t.TempDir(), "test_projects.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open test store: %v", err)
	}
	defer st.Close()

	if _, err := st.DB.Exec(`INSERT INTO projects (id, path, created_at) VALUES ('alpha', '/w/alpha', datetime('now'))`); err != nil {
		t.Fatalf("failed to insert alpha project: %v", err)
	}
	if _, err := st.DB.Exec(`INSERT INTO projects (id, path, created_at) VALUES ('beta', '/w/beta', datetime('now'))`); err != nil {
		t.Fatalf("failed to insert beta project: %v", err)
	}

	stState := &interactiveState{currentProject: ""}
	// First cycle: -> alpha
	stState.handleKey('p', st, "", 15*time.Second)
	if stState.currentProject != "alpha" {
		t.Errorf("expected 'alpha', got %q", stState.currentProject)
	}
	if stState.lastStatusNotice != "Filter: Project alpha" {
		t.Errorf("unexpected notice: %q", stState.lastStatusNotice)
	}

	// Second cycle: -> beta
	stState.handleKey('p', st, "", 15*time.Second)
	if stState.currentProject != "beta" {
		t.Errorf("expected 'beta', got %q", stState.currentProject)
	}
	if stState.lastStatusNotice != "Filter: Project beta" {
		t.Errorf("unexpected notice: %q", stState.lastStatusNotice)
	}

	// Third cycle: -> "" (global)
	stState.handleKey('p', st, "", 15*time.Second)
	if stState.currentProject != "" {
		t.Errorf("expected '', got %q", stState.currentProject)
	}
	if stState.lastStatusNotice != "Filter: Global Workspace (All Projects)" {
		t.Errorf("unexpected notice: %q", stState.lastStatusNotice)
	}
}

func TestInteractiveState_ProjectFilterActivePriority(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_active_p.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open test store: %v", err)
	}
	defer st.Close()

	// Insert 4 projects: 2 active, 2 idle
	mustInsert := func(id string) {
		if _, err := st.DB.Exec(`INSERT INTO projects (id, path, created_at) VALUES (?, ?, datetime('now'))`, id, "/w/"+id); err != nil {
			t.Fatalf("insert failed: %v", err)
		}
	}
	mustInsert("active-one")
	mustInsert("idle-one")
	mustInsert("active-two")
	mustInsert("idle-two")

	state := &interactiveState{
		latestSnap: &CockpitSnapshot{
			Fleet: FleetBox{
				Agents: []AgentItem{
					{Agent: "@devin", Project: "active-one", GoalSlug: "goal-1"},
				},
			},
			Roadmap: RoadmapBox{
				InFlight: []InFlightGoalItem{
					{Slug: "goal-2", Project: "active-two"},
				},
			},
		},
	}

	// Cycle 1: -> active-one
	state.handleKey('p', st, "", 15*time.Second)
	if state.currentProject != "active-one" {
		t.Errorf("expected 'active-one', got %q", state.currentProject)
	}

	// Cycle 2: -> active-two (bypasses idle projects!)
	state.handleKey('p', st, "", 15*time.Second)
	if state.currentProject != "active-two" {
		t.Errorf("expected 'active-two', got %q", state.currentProject)
	}

	// Cycle 3: -> "" (global workspace)
	state.handleKey('p', st, "", 15*time.Second)
	if state.currentProject != "" {
		t.Errorf("expected '', got %q", state.currentProject)
	}
}

func TestRunLive_DecoupledRedrawAndFastExit(t *testing.T) {
	ResetSnapshotCollectionCount()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := RunLive(ctx, nil, "", t.TempDir(), 10*time.Second, nil)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("RunLive returned unexpected error: %v", err)
	}

	// Must exit within 2 seconds
	if elapsed > 2*time.Second {
		t.Errorf("expected RunLive to exit within 2s on cancel, took %v", elapsed)
	}

	// Invocations must be bounded (initial collection only), not continuous
	invocations := GetSnapshotCollectionCount()
	if invocations > 2 {
		t.Errorf("expected at most 2 collections during 500ms, got %d", invocations)
	}
}

func TestRunLive_RepaintsReuseSnapshotsWithoutCollection(t *testing.T) {
	ResetSnapshotCollectionCount()

	// Run for 1.3 seconds so the 1-second UI ticker fires at least once
	ctx, cancel := context.WithTimeout(context.Background(), 1300*time.Millisecond)
	defer cancel()

	err := RunLive(ctx, nil, "", t.TempDir(), 10*time.Second, nil)
	if err != nil {
		t.Fatalf("RunLive returned unexpected error: %v", err)
	}

	// Initial collection is 1. If 1s ticker invoked collection, count would be >= 2.
	invocations := GetSnapshotCollectionCount()
	if invocations > 1 {
		t.Errorf("expected exactly 1 collection (initial only), repaints must reuse snapshots, got %d", invocations)
	}
}
