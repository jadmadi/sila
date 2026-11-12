package dream

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func TestVisionCompileAndTriage(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "vision_test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Seed non-goal
	_, err = AddNonGoal(st, "sila", "No cloud database dependencies (pure-Go SQLite only, no external db)", "human")
	if err != nil {
		t.Fatalf("AddNonGoal failed: %v", err)
	}

	// Seed milestone
	_, err = AddMilestone(st, "sila", "Living Architecture Decision Records (sila adr)", "human")
	if err != nil {
		t.Fatalf("AddMilestone failed: %v", err)
	}

	// Seed invariant
	_, err = st.InsertMemo("sila", "Invariant: Pure-Go SQLite: Keep CGO_ENABLED=0 with modernc.org/sqlite", "human", "", nil)
	if err != nil {
		t.Fatalf("InsertMemo failed: %v", err)
	}

	// 1. Test CompileVision
	doc, err := CompileVision(st, "sila", "")
	if err != nil {
		t.Fatalf("CompileVision failed: %v", err)
	}

	if !strings.Contains(doc, "# VISION.md — sila") {
		t.Fatalf("missing vision header in: %s", doc)
	}
	if !strings.Contains(doc, "No cloud database dependencies") {
		t.Fatalf("missing non-goal in vision doc: %s", doc)
	}
	if !strings.Contains(doc, "Living Architecture Decision Records") {
		t.Fatalf("missing milestone in vision doc: %s", doc)
	}

	// 2. Test TriageProposal: Rejected due to non-goal conflict
	triage1, err := TriageProposal(st, "sila", "", "Add PostgreSQL and CockroachDB cloud database adapter")
	if err != nil {
		t.Fatalf("TriageProposal failed: %v", err)
	}
	if triage1.Verdict != "REJECTED" {
		t.Fatalf("expected REJECTED for cloud db proposal, got %s (conflicts: %v)", triage1.Verdict, triage1.DirectConflicts)
	}
	if len(triage1.ViolatedNonGoals) == 0 {
		t.Fatalf("expected ViolatedNonGoals to be non-empty")
	}

	// 3. Test TriageProposal: Needs alignment due to invariant conflict
	triage2, err := TriageProposal(st, "sila", "", "Integrate native C library via CGO bindings")
	if err != nil {
		t.Fatalf("TriageProposal failed: %v", err)
	}
	if triage2.Verdict != "NEEDS_ALIGNMENT" && triage2.Verdict != "REJECTED" {
		t.Fatalf("expected NEEDS_ALIGNMENT or REJECTED for CGO proposal, got %s", triage2.Verdict)
	}

	// 4. Test TriageProposal: Approved clean proposal
	triage3, err := TriageProposal(st, "sila", "", "Add token usage analytics command for developer insight")
	if err != nil {
		t.Fatalf("TriageProposal failed: %v", err)
	}
	if triage3.Verdict != "APPROVED" {
		t.Fatalf("expected APPROVED for clean proposal, got %s (conflicts: %v)", triage3.Verdict, triage3.DirectConflicts)
	}
	if triage3.FitScore < 80 {
		t.Fatalf("expected high fit score, got %d", triage3.FitScore)
	}
}

func TestCompileVisionWithProjectGoals(t *testing.T) {
	projDir := t.TempDir()
	goalsDir := filepath.Join(projDir, "goals", "test-goal")
	if err := os.MkdirAll(goalsDir, 0755); err != nil {
		t.Fatal(err)
	}
	goalMd := `# Goal: Test Goal
## Description
Demonstrate vision compilation with live goal packages.
`
	if err := os.WriteFile(filepath.Join(goalsDir, "goal.md"), []byte(goalMd), 0644); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(t.TempDir(), "vision_goals.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	doc, err := CompileVision(st, "testproj", projDir)
	if err != nil {
		t.Fatalf("CompileVision failed: %v", err)
	}
	if !strings.Contains(doc, "Test Goal") {
		t.Fatalf("expected goal in upcoming horizons: %s", doc)
	}
}

func TestTriageNegatedNonGoal(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "triage_negation_test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if _, err := AddNonGoal(st, "atlas-web", "Non-goal: no raw SQL strings in route handlers; data access goes through the repository layer.", "human"); err != nil {
		t.Fatal(err)
	}
	if _, err := AddNonGoal(st, "atlas-web", "Non-goal: atlas-web never adds a second frontend framework; one SPA stack only.", "human"); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name     string
		proposal string
		want     string
	}{
		{
			name:     "raw SQL in a handler is rejected",
			proposal: "Add a raw SQL query in the checkout handler for speed",
			want:     "REJECTED",
		},
		{
			name:     "parameterized SQL in the repository is approved",
			proposal: "Use parameterized SQL through the repository layer in the checkout handler",
			want:     "APPROVED",
		},
		{
			name:     "raw SQL migration in the repository is approved",
			proposal: "Add a raw SQL migration in the repository layer",
			want:     "APPROVED",
		},
		{
			name:     "second frontend framework is rejected",
			proposal: "Add a second frontend framework alongside the SPA",
			want:     "REJECTED",
		},
		{
			name:     "unrelated proposal is approved",
			proposal: "Add token usage analytics command for developer insight",
			want:     "APPROVED",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := TriageProposal(st, "atlas-web", "", tc.proposal)
			if err != nil {
				t.Fatalf("TriageProposal: %v", err)
			}
			if res.Verdict != tc.want {
				t.Fatalf("verdict = %s, want %s (violated: %v)", res.Verdict, tc.want, res.ViolatedNonGoals)
			}
			if tc.want == "REJECTED" && len(res.ViolatedNonGoals) == 0 {
				t.Fatalf("expected ViolatedNonGoals for %q", tc.proposal)
			}
		})
	}
}
