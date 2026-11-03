package goals

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
)

func TestAuditTable_InvertedVisualHierarchyAndNoiseCapping(t *testing.T) {
	tmp := t.TempDir()

	// Create 8 implemented dummy goals
	var goalsList []Goal
	for i := 1; i <= 8; i++ {
		slug := fmt.Sprintf("goal-done-%02d", i)
		goalsList = append(goalsList, Goal{
			Slug:        slug,
			Title:       fmt.Sprintf("Implemented Goal %d", i),
			Status:      "implemented",
			Independent: true,
			Commit:      fmt.Sprintf("commit-%d", i),
			Shape:       "ship",
		})
	}

	// Create 2 ready goals
	goalsList = append(goalsList, Goal{
		Slug:        "goal-ready-alpha",
		Title:       "Ready Goal Alpha",
		Status:      "pending",
		Independent: true,
		Shape:       "ship",
		LaunchCmd:   "/goal goals/goal-ready-alpha/goal.md",
	})
	goalsList = append(goalsList, Goal{
		Slug:        "goal-ready-beta",
		Title:       "Ready Goal Beta",
		Status:      "pending",
		Independent: false,
		Shape:       "scout",
		LaunchCmd:   "/goal goals/goal-ready-beta/goal.md",
	})

	audit := &AuditResult{
		Project:               "test-project",
		ProjectDir:            tmp,
		Total:                 10,
		ImplementedCount:      8,
		ReadyCount:            2,
		ReadyIndependentCount: 1,
		ReadyUnblockedCount:   1,
		PendingCount:          2,
		Goals:                 goalsList,
	}

	// 1. Default view: Should cap implemented to 5 and show truncation notice
	captureOutput := func(filter string, showAll bool) string {
		oldStdout := os.Stdout
		r, w, _ := os.Pipe()
		os.Stdout = w

		ui := render.New(w, false)
		PrintAuditTable(ui, audit, filter, showAll)

		w.Close()
		os.Stdout = oldStdout

		var buf bytes.Buffer
		io.Copy(&buf, r)
		return buf.String()
	}

	defaultOut := captureOutput("", false)

	// Check that 5 are listed and 3 are noted as earlier
	if !strings.Contains(defaultOut, "🟢 RECENTLY IMPLEMENTED (8)") {
		t.Fatalf("expected implemented header, got:\n%s", defaultOut)
	}
	if !strings.Contains(defaultOut, "... and 3 earlier implemented goals on disk.") {
		t.Errorf("expected truncation notice for 3 earlier goals, got:\n%s", defaultOut)
	}
	if !strings.Contains(defaultOut, "→ View full history: sila goals implemented (or --all, --toon, --json)") {
		t.Errorf("expected hint to view full history, got:\n%s", defaultOut)
	}
	if strings.Contains(defaultOut, "goal-done-06") {
		t.Errorf("goal-done-06 should have been capped from default output")
	}

	// Inverted Visual Hierarchy check:
	// Implemented (History) must come BEFORE Ready to Claim,
	// and Ready to Claim must come BEFORE the bottom summary matrix!
	headerIdx := strings.Index(defaultOut, "Goals Audit & Roadmap Matrix")
	implIdx := strings.Index(defaultOut, "🟢 RECENTLY IMPLEMENTED")
	readyIdx := strings.Index(defaultOut, "🟡 GOALS READY TO CLAIM & EXECUTE")

	if headerIdx == -1 || implIdx == -1 || readyIdx == -1 {
		t.Fatalf("expected header and both sections (header: %d, impl: %d, ready: %d)", headerIdx, implIdx, readyIdx)
	}
	if !(headerIdx < implIdx && implIdx < readyIdx) {
		t.Errorf("layout violation: expected header (%d) < impl (%d) < ready (%d)", headerIdx, implIdx, readyIdx)
	}
	if strings.Contains(defaultOut, "AGENT PROTOCOL") {
		t.Error("default output must not include the static agent protocol block")
	}
	if !strings.Contains(defaultOut, "Legend:") {
		t.Error("default output must include the glyph legend")
	}

	// 2. Uncapped view (--all): all 8 implemented goals shown
	allOut := captureOutput("", true)
	if strings.Contains(allOut, "... and 3 earlier goals") {
		t.Errorf("--all output should not be capped")
	}
	if !strings.Contains(allOut, "goal-done-08") {
		t.Errorf("--all should contain goal-done-08")
	}

	// 3. Implemented filter (sila goals implemented): all 8 shown, ready section omitted
	implFilterOut := captureOutput("implemented", false)
	if !strings.Contains(implFilterOut, "goal-done-08") {
		t.Errorf("implemented filter should contain all items")
	}
	if strings.Contains(implFilterOut, "🟡 GOALS READY TO CLAIM & EXECUTE") {
		t.Errorf("implemented filter should omit pending ready goals")
	}
}

func TestRenderGoalsTOON(t *testing.T) {
	audit := &AuditResult{
		Project:          "demo-proj",
		Total:            3,
		ImplementedCount: 1,
		ReadyCount:       1,
		BlockedCount:     1,
		Goals: []Goal{
			{
				Slug:        "goal-alpha",
				Title:       "Alpha Goal",
				Status:      "implemented",
				Independent: true,
				Shape:       "ship",
				Commit:      "abc1234",
				TasksTotal:  2,
				TasksMet:    2,
			},
			{
				Slug:        "goal-beta",
				Title:       "Beta Goal",
				Status:      "pending",
				Independent: true,
				Shape:       "scout",
				TasksTotal:  3,
				TasksMet:    1,
			},
			{
				Slug:          "goal-gamma",
				Title:         "Gamma Goal",
				Status:        "pending",
				Blocked:       true,
				BlockedReason: "requires beta",
				UnmetDeps:     []string{"goal-beta"},
				Shape:         "ship",
			},
		},
	}

	// Default TOON: summary + goals table
	defaultTOON := RenderGoalsTOON(audit, "")
	if !strings.Contains(defaultTOON, "goals_summary:") {
		t.Errorf("expected goals_summary in default TOON, got:\n%s", defaultTOON)
	}
	if !strings.Contains(defaultTOON, "goals[3]{slug,status,mode,shape,title,deps}:") {
		t.Errorf("expected goals table header in default TOON, got:\n%s", defaultTOON)
	}
	if !strings.Contains(defaultTOON, "goal-alpha,implemented,independent,ship,Alpha Goal,") {
		t.Errorf("expected goal-alpha row in TOON, got:\n%s", defaultTOON)
	}

	// Implemented TOON: implemented_goals table
	implTOON := RenderGoalsTOON(audit, "implemented")
	if !strings.Contains(implTOON, "implemented_goals[1]{slug,shape,mode,commit,tasks,title}:") {
		t.Errorf("expected implemented_goals table header, got:\n%s", implTOON)
	}
	if !strings.Contains(implTOON, "goal-alpha,ship,independent,abc1234,2/2,Alpha Goal") {
		t.Errorf("expected goal-alpha row in implemented TOON, got:\n%s", implTOON)
	}

	// Ready TOON: ready_goals table
	readyTOON := RenderGoalsTOON(audit, "ready")
	if !strings.Contains(readyTOON, "ready_goals[1]{slug,shape,mode,tasks,title}:") {
		t.Errorf("expected ready_goals table header, got:\n%s", readyTOON)
	}
	if !strings.Contains(readyTOON, "goal-beta,scout,independent,1/3,Beta Goal") {
		t.Errorf("expected goal-beta row in ready TOON, got:\n%s", readyTOON)
	}
}

// TestAuditTableShowsSkippedCandidateNote keeps the "why didn't it close" hint
// visible for pending goals with documentation-only candidate commits.
func TestAuditTableShowsSkippedCandidateNote(t *testing.T) {
	audit := &AuditResult{Goals: []Goal{{
		Slug: "notes-goal", Title: "Notes Goal", Status: "pending", Independent: true,
		SkippedCandidates: []string{"abc1234", "def5678"},
	}}}

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	ui := render.New(w, true)
	PrintAuditTable(ui, audit, "")
	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	io.Copy(&buf, r)
	out := buf.String()

	if !strings.Contains(out, "skipped as docs/test") {
		t.Fatalf("expected skipped-candidate note, got:\n%s", out)
	}
	if !strings.Contains(out, "abc1234") || !strings.Contains(out, "def5678") {
		t.Fatalf("expected candidate hashes in note, got:\n%s", out)
	}
}

func TestAuditTableShowsSuperseded(t *testing.T) {
	audit := &AuditResult{
		Total:            2,
		ImplementedCount: 1,
		SupersededCount:  1,
		Goals: []Goal{
			{Slug: "done-goal", Title: "Done Goal", Status: "implemented", Independent: true},
			{Slug: "old-goal", Title: "Old Goal", Status: "superseded", Independent: true},
		},
	}

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	ui := render.New(w, false)
	PrintAuditTable(ui, audit, "")
	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	io.Copy(&buf, r)
	out := buf.String()

	if !strings.Contains(out, "Superseded:") || !strings.Contains(out, "1") {
		t.Fatalf("expected superseded count in audit table, got:\n%s", out)
	}
}

func TestGoalProgressBarsAreTenCells(t *testing.T) {
	ui := render.New(os.Stdout, true)
	cases := []struct {
		met   int
		human string
		ascii string
	}{
		{0, "[░░░░░░░░░░]", "[----------]"},
		{5, "[█████░░░░░]", "[#####-----]"},
		{10, "[██████████]", "[##########]"},
	}
	for _, tc := range cases {
		if got := renderProgressBar(ui, tc.met, 10, GoalProgressBarWidth); got != tc.human {
			t.Errorf("human bar %d/10 = %q, want %q", tc.met, got, tc.human)
		}
		if got := renderProgressBarASCII(tc.met, 10, GoalProgressBarWidth); got != tc.ascii {
			t.Errorf("ascii bar %d/10 = %q, want %q", tc.met, got, tc.ascii)
		}
	}
}

func TestAuditTableASCIIModeIsPlainAndLegible(t *testing.T) {
	res := &AuditResult{
		Project:             "proj",
		Total:               2,
		ReadyCount:          1,
		ReadyUnblockedCount: 1,
		ImplementedCount:    1,
		VerifiedCount:       1,
		TotalTasks:          2,
		CompletedTasks:      1,
		Goals: []Goal{
			{Slug: "ready-goal", Status: "pending", Independent: false, Tier: "roadmap", TasksMet: 1, TasksTotal: 2, Title: "Ready"},
			{Slug: "done-goal", Status: "implemented", Independent: true, Tier: "immediate", VerificationStatus: "verified", Title: "Done"},
		},
	}
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	PrintAuditTable(render.New(w, true), res, "", false, true)
	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	io.Copy(&buf, r)
	out := buf.String()

	for _, r := range out {
		if r > 127 {
			t.Fatalf("ASCII mode contains non-ASCII rune %q:\n%s", r, out)
		}
	}
	if strings.Contains(out, "AGENT PROTOCOL") {
		t.Fatal("ASCII mode must not include the agent protocol block")
	}
	if !strings.Contains(out, "Legend: ship") {
		t.Fatalf("ASCII legend missing:\n%s", out)
	}
	if !strings.Contains(out, "[#####-----]") {
		t.Fatalf("ASCII progress bar missing:\n%s", out)
	}
}

func TestGoalStateLabels(t *testing.T) {
	for _, state := range []GoalState{StateDraft, StateSpecAudited, StateClaimed, StatePendingAudit, StateImplemented, StateRemediation, StateTainted, StateSuperseded} {
		if label := state.Label(); label == "" || label == string(state) {
			t.Errorf("state %q label = %q", state, label)
		}
	}
	if StateDraft.Label() == StateImplemented.Label() {
		t.Fatal("draft and implemented labels must differ")
	}
}
