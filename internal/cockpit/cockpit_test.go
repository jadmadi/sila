package cockpit

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/store"
)

func TestCockpit_ModelJSONAndTOON(t *testing.T) {
	snap := &CockpitSnapshot{
		Header: HeaderInfo{
			Pulse:                 "1s pulse",
			ActiveAgentsCount:     2,
			PendingApprovalsCount: 1,
			FleetHealth:           "100% GREEN",
			Workspace:             "/test/workspace",
			SubstrateRepos:        5,
			Version:               "v0.6.0-dev",
			Timestamp:             time.Now().UTC(),
		},
		Fleet: FleetBox{
			TotalCount: 2,
			Summary:    "2 executing, 0 paused, 0 crashed",
			Agents: []AgentItem{
				{
					Agent:      "@devin",
					Harness:    "Devin",
					GoalSlug:   "auth-feature",
					ActiveTool: "run_command",
					Status:     "EXECUTING",
					Elapsed:    "2m 10s",
					Turn:       5,
				},
				{
					Agent:      "@agy",
					Harness:    "Antigravity",
					GoalSlug:   "gopls-feature",
					ActiveTool: "replace_file_content",
					Status:     "EXECUTING",
					Elapsed:    "1m 05s",
					Turn:       3,
				},
			},
		},
		Approvals: ApprovalBox{
			HasPending: true,
			Summary:    "1 approval request(s) awaiting steering",
			Pending: []PendingApprovalItem{
				{
					ID:               "app_1234",
					Agent:            "@devin",
					Action:           "rm -rf temp",
					Reason:           "cleanup test temp files",
					RiskLevel:        "medium",
					TimeoutSeconds:   300,
					RemainingSeconds: 240,
					ProgressRatio:    0.8,
				},
			},
		},
		Stream: StreamBox{
			KnowledgeRate:     "3.5 facts/min",
			VelocitySparkline: " ▂▃▅▇█",
			VelocityHistory:   []int{1, 2, 3, 5, 8, 10},
			Items: []EpistemicItem{
				{
					Time:    "12:00:00",
					Kind:    "fact",
					Agent:   "@devin",
					Content: "Sample verified fact content",
				},
			},
		},
		Concurrency: ConcurrencyBox{
			CollisionAlert: false,
			Summary:        "0 OVERLAPS (Clean isolation across 2 agent workspace(s))",
			Touches: []AgentTouchItem{
				{Agent: "@devin", Files: []string{"pkg/auth/auth.go"}},
				{Agent: "@agy", Files: []string{"pkg/gopls/server.go"}},
			},
		},
		Gates: GateBox{
			AllClean: true,
			Summary:  "All gate sentries PASS",
			Checks: []GateCheckItem{
				{Name: "Tests (-race)", Status: "PASS", Details: "clean"},
				{Name: "Code Format", Status: "PASS", Details: "gofmt clean"},
			},
		},
		Roadmap: RoadmapBox{
			TotalGoals:     10,
			ShippedGoals:   8,
			ClosedVelocity: "8/10 shipped",
			NextUnblocked:  "next-feature",
			InFlight: []InFlightGoalItem{
				{
					Slug:        "auth-feature",
					Title:       "Auth Feature",
					TasksMet:    4,
					TasksTotal:  5,
					ProgressPct: 80,
					ProgressBar: "[████████░░]",
					Status:      "In Progress",
				},
			},
		},
		Context: ContextBox{
			PrimeSummary: "unknown (no compression savings measured)",
			SavingsRatio: 0.0,
			Agents: []AgentContextItem{
				{
					Agent:                  "@devin",
					ContextTokens:          25000,
					ContextTokensFormatted: "25.0K",
					PromptTokensPerTurn:    800,
					PromptTokensFormatted:  "800",
					LimitStatus:            "within limits",
				},
			},
		},
		Ledger: LedgerBox{
			TotalBlocks:           100,
			VerifiedBlocks:        100,
			TipHash:               "abcdef0123456789abcdef0123456789",
			TipHashShort:          "abcdef0123456789…",
			Intact:                true,
			BrokenLinks:           0,
			VerificationLatencyMs: 15,
			SelfHealingAvailable:  true,
			HealthSummary:         "✔ 100% INTACT (100 blocks verified)",
		},
		Diff: DiffBox{
			TotalFiles:     2,
			TotalAdditions: 45,
			TotalDeletions: 12,
			VelocityRate:   "+45/-12 (2 files)",
			ChurnSummary:   "1 modified, 1 added",
			Items: []DiffItem{
				{File: "pkg/auth/auth.go", Additions: 40, Deletions: 10, Status: "M", Worktree: "wt-auth", Agent: "devin"},
				{File: "pkg/auth/types.go", Additions: 5, Deletions: 2, Status: "A", Worktree: "wt-auth", Agent: "devin"},
			},
		},
		Worktrees: WorktreeBox{
			ActiveCount:     1,
			TotalCapacity:   8,
			AutoPruneMerged: true,
			ActiveWorktrees: []WorktreeItem{
				{
					Name:      "wt-auth",
					Path:      "/tmp/wt-auth",
					Branch:    "feat/auth",
					Agent:     "devin",
					IsPrimary: false,
				},
			},
		},
	}

	// 1. JSON
	jsonStr, err := snap.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON failed: %v", err)
	}
	if !strings.Contains(jsonStr, `"@devin"`) || !strings.Contains(jsonStr, `"app_1234"`) {
		t.Errorf("JSON output missing expected keys:\n%s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"velocity_rate"`) {
		t.Errorf("JSON output missing diff velocity_rate:\n%s", jsonStr)
	}

	// 2. TOON
	toonStr := snap.ToTOON()
	if !strings.Contains(toonStr, "cockpit_summary:") {
		t.Errorf("TOON missing cockpit_summary:\n%s", toonStr)
	}
	if !strings.Contains(toonStr, "fleet[2]{agent,harness,goal,tool,status,elapsed,turn}:") {
		t.Errorf("TOON missing fleet table:\n%s", toonStr)
	}
	if !strings.Contains(toonStr, "approvals[1]{id,agent,action,reason,risk,remaining}:") {
		t.Errorf("TOON missing approvals table:\n%s", toonStr)
	}
	if !strings.Contains(toonStr, "diff_velocity[2]{file,status,additions,deletions,worktree}:") {
		t.Errorf("TOON missing diff_velocity table:\n%s", toonStr)
	}
}

func TestCockpit_RendererLayouts(t *testing.T) {
	snap := &CockpitSnapshot{
		Header: HeaderInfo{
			Pulse:                 "1s pulse",
			ActiveAgentsCount:     1,
			PendingApprovalsCount: 0,
			FleetHealth:           "100% GREEN",
			Workspace:             "/test",
			SubstrateRepos:        1,
			Version:               "v0.6.0-dev",
			Timestamp:             time.Now(),
		},
		Fleet: FleetBox{
			Summary: "1 executing, 0 paused, 0 crashed",
			Agents: []AgentItem{
				{Agent: "@agy", Harness: "Antigravity", GoalSlug: "test-goal", Status: "EXECUTING"},
			},
		},
		Approvals: ApprovalBox{
			HasPending: false,
			Summary:    "No pending approvals",
		},
		Stream: StreamBox{
			KnowledgeRate:     "1.0 facts/min",
			VelocitySparkline: " ▂▃",
		},
		Concurrency: ConcurrencyBox{
			CollisionAlert: false,
			Summary:        "0 OVERLAPS",
		},
		Gates: GateBox{
			AllClean: true,
			Summary:  "All gate sentries PASS",
			Checks: []GateCheckItem{
				{Name: "Tests (-race)", Status: "PASS", Details: "clean"},
			},
		},
		Roadmap: RoadmapBox{
			NextUnblocked: "next-step",
			InFlight: []InFlightGoalItem{
				{Slug: "test-goal", ProgressPct: 50, ProgressBar: "[█████░░░░░]", Status: "In Progress"},
			},
		},
		Context: ContextBox{
			PrimeSummary: "25% pruned",
		},
		Ledger: LedgerBox{
			TotalBlocks: 10,
			Intact:      true,
		},
		Worktrees: WorktreeBox{
			ActiveCount:   1,
			TotalCapacity: 8,
		},
	}

	// 1. Wide layout (>= 110 cols)
	outWide := RenderSnapshot(snap, 120, false)
	if !strings.Contains(outWide, "Live Agent Fleet Roster") {
		t.Errorf("wide layout missing fleet roster:\n%s", outWide)
	}
	if !strings.Contains(outWide, "Active Mutation & Diff Velocity") {
		t.Errorf("wide layout missing diff box:\n%s", outWide)
	}
	if !strings.Contains(outWide, "LEDGER: intact") {
		t.Errorf("wide layout missing ledger badge in header:\n%s", outWide)
	}

	// 2. Medium layout (80 cols)
	outMed := RenderSnapshot(snap, 80, false)
	if !strings.Contains(outMed, "Live Agent Fleet Roster") {
		t.Errorf("medium layout missing fleet roster:\n%s", outMed)
	}
	if !strings.Contains(outMed, "Active Mutation & Diff Velocity") {
		t.Errorf("medium layout missing diff box:\n%s", outMed)
	}

	// 3. Narrow layout (60 cols)
	outNarrow := RenderSnapshot(snap, 60, false)
	if !strings.Contains(outNarrow, "Live Agent Fleet Roster") {
		t.Errorf("narrow layout missing fleet roster:\n%s", outNarrow)
	}
	if !strings.Contains(outNarrow, "Active Mutation & Diff Velocity") {
		t.Errorf("narrow layout missing diff box:\n%s", outNarrow)
	}

	// Verify no raw ANSI sequences when color is false
	if strings.Contains(outWide, "\x1b[") {
		t.Errorf("expected no ANSI escape codes when color=false")
	}
}

func TestCockpit_CollectSnapshot(t *testing.T) {
	// Create temporary store and workspace
	tmpDir, err := os.MkdirTemp("", "sila-cockpit-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "sila.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	// Seed approval
	err = st.InsertApproval(&store.Approval{
		ID:             "app_test_1",
		Project:        "test-proj",
		Agent:          "devin",
		Prompt:         "make test change",
		RiskLevel:      "high",
		Status:         "pending",
		TimeoutSeconds: 300,
	})
	if err != nil {
		t.Fatalf("failed to insert approval: %v", err)
	}

	// Seed fact
	_, err = st.InsertMemo("test-proj", "Learned test invariant", "devin", tmpDir, nil)
	if err != nil {
		t.Fatalf("failed to insert memo: %v", err)
	}

	// Create goals structure
	goalsDir := filepath.Join(tmpDir, "goals", "test-goal")
	_ = os.MkdirAll(goalsDir, 0755)
	goalMd := "# Goal: Test Goal\n\n## Goal Description\nDescription for test goal.\n\n- **Mode**: Independent\n- **Depends On**: none\n"
	_ = os.WriteFile(filepath.Join(goalsDir, "goal.md"), []byte(goalMd), 0644)
	planMd := "# Plan\n- [ ] Step 1\n- [x] Step 2\n"
	_ = os.WriteFile(filepath.Join(goalsDir, "plan.md"), []byte(planMd), 0644)

	// Collect snapshot
	snap, err := CollectSnapshot(context.Background(), st, "test-proj", tmpDir)
	if err != nil {
		t.Fatalf("CollectSnapshot failed: %v", err)
	}

	if !snap.Approvals.HasPending {
		t.Errorf("expected approvals to be pending")
	}
	if len(snap.Approvals.Pending) == 0 || snap.Approvals.Pending[0].ID != "app_test_1" {
		t.Errorf("expected approval app_test_1 in snapshot, got %+v", snap.Approvals.Pending)
	}
	if len(snap.Stream.Items) == 0 {
		t.Errorf("expected stream items from seeded memo, got 0")
	}
}

func TestCockpit_ConcurrencyOverlaps(t *testing.T) {
	snap := &CockpitSnapshot{}
	audit := &goals.AuditResult{
		Goals: []goals.Goal{
			{
				Slug:         "goal-a",
				Status:       "in_progress",
				FilesToTouch: []string{"internal/store/facts.go"},
				Claim: &goals.GoalClaim{
					Agent:        "devin",
					FilesToTouch: []string{"internal/store/facts.go"},
				},
			},
			{
				Slug:         "goal-b",
				Status:       "in_progress",
				FilesToTouch: []string{"internal/store/facts.go"},
				FileConflicts: []goals.FileConflict{
					{
						GoalSlug:         "goal-b",
						ConflictingGoal:  "goal-a",
						HolderAgent:      "devin",
						OverlappingFiles: []string{"internal/store/facts.go"},
					},
				},
				Claim: &goals.GoalClaim{
					Agent:        "opencode",
					FilesToTouch: []string{"internal/store/facts.go"},
				},
			},
		},
	}

	collectGoalTelemetry(snap, audit)

	if !snap.Concurrency.CollisionAlert {
		t.Errorf("expected collision alert to be true when overlapping goals exist")
	}
	if len(snap.Concurrency.Overlaps) != 1 {
		t.Errorf("expected 1 overlap, got %d", len(snap.Concurrency.Overlaps))
	}
}

func TestCockpit_DiffTelemetry(t *testing.T) {
	// 1. Non-git or empty directory fallback
	snapEmpty := &CockpitSnapshot{}
	collectDiffTelemetry(snapEmpty, "/nonexistent/test/dir")
	if snapEmpty.Diff.TotalFiles != 0 {
		t.Errorf("expected 0 files for nonexistent dir, got %d", snapEmpty.Diff.TotalFiles)
	}
	if snapEmpty.Diff.VelocityRate != "clean (0 files)" {
		t.Errorf("expected clean velocity rate, got %q", snapEmpty.Diff.VelocityRate)
	}

	// 2. Real git repository with uncommitted mutations
	tmpDir, err := os.MkdirTemp("", "sila-diff-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	runGit := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", tmpDir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v, output: %s", args, err, string(out))
		}
	}

	runGit("init")
	runGit("config", "user.email", "test@test.com")
	runGit("config", "user.name", "Tester")

	// Commit initial file
	testFile1 := filepath.Join(tmpDir, "file1.txt")
	_ = os.WriteFile(testFile1, []byte("line 1\nline 2\n"), 0644)
	runGit("add", "file1.txt")
	runGit("commit", "-m", "initial commit")

	// Mutate file1.txt (+2 additions)
	_ = os.WriteFile(testFile1, []byte("line 1\nline 2\nline 3\nline 4\n"), 0644)

	// Create untracked file2.txt
	testFile2 := filepath.Join(tmpDir, "file2.txt")
	_ = os.WriteFile(testFile2, []byte("untracked file\n"), 0644)

	snap := &CockpitSnapshot{}
	collectDiffTelemetry(snap, tmpDir)

	if snap.Diff.TotalFiles != 2 {
		t.Fatalf("expected 2 mutated files, got %d", snap.Diff.TotalFiles)
	}
	if snap.Diff.TotalAdditions != 2 {
		t.Errorf("expected 2 additions, got %d", snap.Diff.TotalAdditions)
	}
	if !strings.Contains(snap.Diff.VelocityRate, "+2/-0") {
		t.Errorf("expected +2/-0 in velocity rate, got %q", snap.Diff.VelocityRate)
	}

	// Verify renderer output
	id := func(s string) string { return s }
	boxLines := renderDiffBox(snap, false, id, id, id, id, id, id)
	boxText := strings.Join(boxLines, "\n")
	if !strings.Contains(boxText, "file1.txt") {
		t.Errorf("diff box missing file1.txt:\n%s", boxText)
	}
	if !strings.Contains(boxText, "file2.txt") {
		t.Errorf("diff box missing file2.txt:\n%s", boxText)
	}
}

func TestFormatInterval(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "1s"},
		{1 * time.Second, "1s"},
		{15 * time.Second, "15s"},
		{60 * time.Second, "1m"},
		{90 * time.Second, "1m 30s"},
		{10 * time.Minute, "10m"},
	}
	for _, tc := range cases {
		got := FormatInterval(tc.d)
		if got != tc.want {
			t.Errorf("FormatInterval(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestCockpit_DynamicIntervalRender(t *testing.T) {
	snap := &CockpitSnapshot{
		Header: HeaderInfo{
			Pulse:       "LIVE (15s)",
			FleetHealth: "100% GREEN",
		},
	}
	rendered := RenderSnapshot(snap, 120, false)
	if !strings.Contains(rendered, "LIVE (15s)") {
		t.Errorf("expected LIVE (15s) in rendered output:\n%s", rendered)
	}

	snap.Header.Pulse = "LIVE (1m)"
	rendered1m := RenderSnapshot(snap, 120, false)
	if !strings.Contains(rendered1m, "LIVE (1m)") {
		t.Errorf("expected LIVE (1m) in rendered output:\n%s", rendered1m)
	}

	snap.Header.FleetHealth = "PAUSED"
	snap.Header.Pulse = "PAUSED (Space to Resume)"
	renderedPaused := RenderSnapshot(snap, 120, false)
	if !strings.Contains(renderedPaused, "PAUSED (Space to Resume)") {
		t.Errorf("expected PAUSED notice in rendered output:\n%s", renderedPaused)
	}
}

func TestCockpit_RunLiveIntervalAndSync(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	var syncCalled atomic.Bool
	syncFn := func() {
		syncCalled.Store(true)
	}

	_ = RunLive(ctx, nil, "test", "", 10*time.Millisecond, syncFn)

	if !syncCalled.Load() {
		t.Errorf("expected background syncFn to be executed in RunLive")
	}
}

func TestCockpit_MultiProjectObservability(t *testing.T) {
	// 1. Create two test repositories with goals and claims
	tmpA := t.TempDir()
	goalDirA := filepath.Join(tmpA, "goals", "feat-auth")
	_ = os.MkdirAll(goalDirA, 0755)
	_ = os.WriteFile(filepath.Join(goalDirA, "goal.md"), []byte("# Goal: Auth System\n\n- Mode: Independent\n- Touch: pkg/auth/auth.go\n"), 0644)
	_ = os.WriteFile(filepath.Join(goalDirA, "plan.md"), []byte("# Plan\n- [x] Step 1\n- [ ] Step 2\n"), 0644)
	_, err := goals.ClaimGoal(tmpA, "feat-auth", "devin", "building auth", true)
	if err != nil {
		t.Fatalf("ClaimGoal tmpA failed: %v", err)
	}

	tmpB := t.TempDir()
	goalDirB := filepath.Join(tmpB, "goals", "feat-billing")
	_ = os.MkdirAll(goalDirB, 0755)
	_ = os.WriteFile(filepath.Join(goalDirB, "goal.md"), []byte("# Goal: Billing Engine\n\n- Mode: Independent\n- Touch: pkg/billing/pay.go\n"), 0644)
	_ = os.WriteFile(filepath.Join(goalDirB, "plan.md"), []byte("# Plan\n- [ ] Step 1\n"), 0644)
	_, err = goals.ClaimGoal(tmpB, "feat-billing", "claude", "building billing", true)
	if err != nil {
		t.Fatalf("ClaimGoal tmpB failed: %v", err)
	}

	// 2. Register projects in store
	dbPath := filepath.Join(t.TempDir(), "multi_proj_test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open failed: %v", err)
	}
	defer st.Close()

	if _, err := st.DB.Exec(`INSERT INTO projects (id, path, created_at) VALUES ('manara-auth', ?, datetime('now'))`, tmpA); err != nil {
		t.Fatalf("insert manara-auth failed: %v", err)
	}
	if _, err := st.DB.Exec(`INSERT INTO projects (id, path, created_at) VALUES ('manara-billing', ?, datetime('now'))`, tmpB); err != nil {
		t.Fatalf("insert manara-billing failed: %v", err)
	}

	// 3. Global workspace collection (project == "")
	snap, err := CollectSnapshot(context.Background(), st, "", "")
	if err != nil {
		t.Fatalf("CollectSnapshot failed: %v", err)
	}

	if snap.Header.SubstrateRepos != 2 {
		t.Errorf("expected 2 substrate repos, got %d", snap.Header.SubstrateRepos)
	}
	if snap.Fleet.TotalCount != 2 {
		t.Fatalf("expected 2 active fleet agents, got %d", snap.Fleet.TotalCount)
	}

	foundAuth := false
	foundBilling := false
	for _, a := range snap.Fleet.Agents {
		if a.Project == "manara-auth" && a.GoalSlug == "feat-auth" {
			foundAuth = true
		}
		if a.Project == "manara-billing" && a.GoalSlug == "feat-billing" {
			foundBilling = true
		}
	}
	if !foundAuth {
		t.Errorf("missing manara-auth agent in fleet: %+v", snap.Fleet.Agents)
	}
	if !foundBilling {
		t.Errorf("missing manara-billing agent in fleet: %+v", snap.Fleet.Agents)
	}

	if len(snap.Roadmap.InFlight) != 2 {
		t.Errorf("expected 2 in-flight goals across projects, got %d", len(snap.Roadmap.InFlight))
	}

	// 4. Verify rendered badges
	rendered := RenderSnapshot(snap, 120, false)
	if !strings.Contains(rendered, "[manara-auth]") {
		t.Errorf("rendered output missing [manara-auth] badge:\n%s", rendered)
	}
	if !strings.Contains(rendered, "[manara-billing]") {
		t.Errorf("rendered output missing [manara-billing] badge:\n%s", rendered)
	}

	// 5. Scoped project filter collection
	snapScoped, err := CollectSnapshot(context.Background(), st, "manara-auth", "/nonexistent/fallback")
	if err != nil {
		t.Fatalf("CollectSnapshot scoped failed: %v", err)
	}
	if snapScoped.Header.Workspace != tmpA {
		t.Errorf("expected workspace to resolve to %s, got %s", tmpA, snapScoped.Header.Workspace)
	}
	if snapScoped.Fleet.TotalCount != 1 {
		t.Errorf("expected 1 agent in scoped snapshot, got %d", snapScoped.Fleet.TotalCount)
	}
	if len(snapScoped.Fleet.Agents) > 0 && snapScoped.Fleet.Agents[0].Project != "manara-auth" {
		t.Errorf("expected agent project 'manara-auth', got %q", snapScoped.Fleet.Agents[0].Project)
	}
}

func TestCockpit_TelemetryEvidenceIntegrity_EmptyStore(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "empty.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	if _, err := st.EnsureProject(tmpDir); err != nil {
		t.Fatalf("EnsureProject: %v", err)
	}

	snap, err := CollectSnapshot(context.Background(), st, "", tmpDir)
	if err != nil {
		t.Fatalf("CollectSnapshot: %v", err)
	}

	// Acceptance criteria 1: empty store must display unknown token usage and savings WITHOUT an invented agent
	if len(snap.Context.Agents) != 0 {
		t.Fatalf("expected 0 agents in empty store, got %d (agents: %+v)", len(snap.Context.Agents), snap.Context.Agents)
	}
	if snap.Context.SavingsRatio != 0.0 {
		t.Errorf("expected 0.0 savings ratio on empty store, got %f", snap.Context.SavingsRatio)
	}
	if !strings.Contains(snap.Context.PrimeSummary, "unknown") {
		t.Errorf("expected PrimeSummary to contain 'unknown', got %q", snap.Context.PrimeSummary)
	}

	rendered := RenderSnapshot(snap, 120, false)
	if strings.Contains(rendered, "@antigravity") {
		t.Errorf("rendered output must NOT invent @antigravity agent when store is empty:\n%s", rendered)
	}
	if !strings.Contains(rendered, "unknown") {
		t.Errorf("rendered output must indicate unknown usage:\n%s", rendered)
	}
}

func TestCockpit_TelemetryEvidenceIntegrity_RenamedSession(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "renamed.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	proj, err := st.EnsureProject(tmpDir)
	if err != nil {
		t.Fatalf("EnsureProject: %v", err)
	}

	content := strings.Repeat("A", 1200) // 1200 characters = 300 tokens
	t1 := time.Now().UTC().Add(-5 * time.Minute)

	// Write session A with short session ID
	_, _, err = st.WriteRun("", "devin", "sess-1", []store.LoadMessage{
		{ProjectID: proj.ID, ContentHash: "hash-1", TS: t1.Format(time.RFC3339), Role: "user", Content: content, Tool: "devin", SessionID: "sess-1", SourceOrder: 1, SourceTime: store.SortableTime(t1)},
	})
	if err != nil {
		t.Fatalf("WriteRun: %v", err)
	}

	snapA, err := CollectSnapshot(context.Background(), st, proj.ID, tmpDir)
	if err != nil {
		t.Fatalf("CollectSnapshot A: %v", err)
	}
	if len(snapA.Context.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(snapA.Context.Agents))
	}
	tokA := snapA.Context.Agents[0].ContextTokens

	// Write session B with identical content but much longer session ID
	_, _, err = st.WriteRun("", "devin", "sess-very-long-renamed-identifier-999999", []store.LoadMessage{
		{ProjectID: proj.ID, ContentHash: "hash-2", TS: t1.Add(time.Minute).Format(time.RFC3339), Role: "user", Content: content, Tool: "devin", SessionID: "sess-very-long-renamed-identifier-999999", SourceOrder: 1, SourceTime: store.SortableTime(t1.Add(time.Minute))},
	})
	if err != nil {
		t.Fatalf("WriteRun B: %v", err)
	}

	snapB, err := CollectSnapshot(context.Background(), st, proj.ID, tmpDir)
	if err != nil {
		t.Fatalf("CollectSnapshot B: %v", err)
	}
	if len(snapB.Context.Agents) < 2 {
		t.Fatalf("expected at least 2 agents/sessions, got %d", len(snapB.Context.Agents))
	}

	// Renaming / changing session ID length must NOT change token counts for identical content!
	tokB := snapB.Context.Agents[0].ContextTokens
	if tokA != tokB {
		t.Errorf("token count changed based on session ID! tokA=%d, tokB=%d", tokA, tokB)
	}
	expectedTok := store.EstimateTokens(len(content))
	if tokA != expectedTok {
		t.Errorf("expected %d tokens, got %d", expectedTok, tokA)
	}
}

func TestCockpit_TelemetryEvidenceIntegrity_LedgerErrorUnavailable(t *testing.T) {
	snap := &CockpitSnapshot{}
	// Simulate an unverified ledger error state
	snap.Ledger.Status = "unavailable"
	snap.Ledger.Error = "disk I/O error reading blocks"
	snap.Ledger.Intact = false
	snap.Ledger.HealthSummary = "▲ UNVERIFIED: disk I/O error reading blocks"

	computeFleetHealth(snap)

	// Acceptance criteria 2: ledger-verification error produces unavailable/error, NEVER intact!
	if snap.Ledger.Intact {
		t.Errorf("expected Ledger.Intact to be false on error")
	}
	if snap.Header.FleetHealth == "100% GREEN" {
		t.Errorf("FleetHealth must not be 100%% GREEN on ledger error, got %q", snap.Header.FleetHealth)
	}
	if snap.Header.FleetHealth != "LEDGER UNVERIFIED" {
		t.Errorf("expected FleetHealth 'LEDGER UNVERIFIED', got %q", snap.Header.FleetHealth)
	}

	rendered := RenderSnapshot(snap, 120, false)
	if strings.Contains(rendered, "100% INTACT") {
		t.Errorf("rendered output must NOT say 100%% INTACT on error:\n%s", rendered)
	}
	if !strings.Contains(rendered, "ERROR: disk I/O error reading blocks") {
		t.Errorf("rendered output must display verification error:\n%s", rendered)
	}

	id := func(s string) string { return s }
	boxLines := renderLedgerBox(snap, false, id, id, id, id, id, id)
	boxText := strings.Join(boxLines, "\n")
	if !strings.Contains(boxText, "ERROR: disk I/O error reading blocks") {
		t.Errorf("renderLedgerBox must display verification error:\n%s", boxText)
	}
}
