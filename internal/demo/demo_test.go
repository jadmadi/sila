package demo

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/store"
)

func TestCreateSeedsSandbox(t *testing.T) {
	sb, st, err := Create()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	defer sb.Cleanup()

	// Store exists on disk inside the sandbox root.
	if _, err := os.Stat(sb.DBPath); err != nil {
		t.Fatalf("demo store missing: %v", err)
	}

	msgs, factsN, verified, projects, err := st.Counts()
	if err != nil {
		t.Fatal(err)
	}
	if msgs < 15 {
		t.Errorf("expected a rich transcript dataset, got %d messages", msgs)
	}
	if factsN := factsN; factsN < 6 {
		t.Errorf("expected at least 6 seeded claims, got %d", factsN)
	}
	if verified < 2 {
		t.Errorf("expected at least 2 grounded claims, got %d", verified)
	}
	if projects != 1 {
		t.Errorf("expected exactly 1 demo project, got %d", projects)
	}

	// FTS search over the seeded transcripts.
	hits, err := st.SearchMessagesFTS("webhook", store.SearchMessagesOptions{Project: ProjectName, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Error("expected FTS hits for 'webhook' in the demo transcripts")
	}

	// Facts cover all three vision kinds.
	vf, err := st.VisionFacts(ProjectName)
	if err != nil {
		t.Fatal(err)
	}
	var nonGoals, milestones, invariants int
	for _, f := range vf {
		switch {
		case f.IsNonGoal():
			nonGoals++
		case f.IsMilestone():
			milestones++
		case f.IsInvariant():
			invariants++
		}
	}
	if invariants == 0 || nonGoals == 0 || milestones == 0 {
		t.Errorf("expected invariants, non-goals, and milestones; got %d/%d/%d", invariants, nonGoals, milestones)
	}

	// Feedback tickets in multiple states.
	items, err := st.QueryFeedback(store.FeedbackFilter{ProjectID: ProjectName, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) < 4 {
		t.Errorf("expected at least 4 feedback tickets, got %d", len(items))
	}

	// One open handoff.
	ho, err := st.HandoffsSince(ProjectName, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ho) == 0 {
		t.Error("expected at least one handoff record")
	}
}

func TestCreateSeedsProjectFiles(t *testing.T) {
	sb, st, err := Create()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	defer sb.Cleanup()

	for _, p := range []string{
		filepath.Join("goals", "payment-webhooks", "goal.md"),
		filepath.Join("goals", "payment-webhooks", "claim.json"),
		filepath.Join("goals", "search-filters", "plan.md"),
		filepath.Join("goals", "billing-migrations", "facts.md"),
		filepath.Join("goals", "dark-mode-toggle", "goal.md"),
		filepath.Join("goals", "README.md"),
		filepath.Join("docs", "adr", "ADR-0001-idempotent-payment-writes.md"),
		filepath.Join("docs", "adr", "ADR-0002-standard-api-envelope.md"),
		filepath.Join("docs", "deploy.md"),
		filepath.Join("src", "payments", "checkout.go"),
		filepath.Join("src", "payments", "refunds.go"),
		"AGENTS.md",
		".gitignore",
	} {
		if _, err := os.Stat(filepath.Join(sb.Dir, p)); err != nil {
			t.Errorf("demo project file missing: %s (%v)", p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(sb.Dir, ".sila")); err != nil {
		t.Errorf("demo .sila directory missing: %v", err)
	}
	// The stale claim must stay ungrounded, so the legacy file must not exist.
	if _, err := os.Stat(filepath.Join(sb.Dir, "src", "payments", "refund_legacy.go")); !os.IsNotExist(err) {
		t.Errorf("refund_legacy.go must be absent, stat err: %v", err)
	}

	// The goal scanner reads the seeded board with all four statuses.
	audit, err := goals.ScanGoals(sb.Dir)
	if err != nil {
		t.Fatal(err)
	}
	status := map[string]string{}
	for _, g := range audit.Goals {
		status[g.Slug] = g.Status
	}
	want := map[string]string{
		"payment-webhooks":   "claimed",
		"search-filters":     "pending",
		"billing-migrations": "pending",
		"dark-mode-toggle":   "implemented",
	}
	for slug, s := range want {
		if status[slug] != s {
			t.Errorf("goal %s status = %q, want %q", slug, status[slug], s)
		}
	}
}

func TestCreateSeedsGroundedAndStaleFiles(t *testing.T) {
	sb, st, err := Create()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	defer sb.Cleanup()

	res, err := st.ReconcileFactDrift(ProjectName, sb.Dir)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.NewlyStale != 0 {
		t.Errorf("NewlyStale = %d, want 0 (grounded files must exist on disk)", res.NewlyStale)
	}

	facts, err := st.FactsByProject(ProjectName)
	if err != nil {
		t.Fatal(err)
	}
	var stale []string
	for _, f := range facts {
		if f.Kind == "code_claim" && !f.FileRefsExist {
			stale = append(stale, f.ID)
		}
	}
	if len(stale) != 1 || stale[0] != "fact_demo_stale_refund" {
		t.Errorf("stale code claims = %v, want [fact_demo_stale_refund]", stale)
	}
}

func TestCreateSeedsLessonCaseAndIntactChain(t *testing.T) {
	sb, st, err := Create()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	defer sb.Cleanup()

	lessons, err := st.ListFactsByKind("lesson", ProjectName, time.Time{}, 50)
	if err != nil {
		t.Fatal(err)
	}
	cases, err := st.ListFactsByKind("case", ProjectName, time.Time{}, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(lessons) < 1 {
		t.Errorf("seeded lessons = %d, want >= 1", len(lessons))
	}
	if len(cases) < 1 {
		t.Errorf("seeded cases = %d, want >= 1", len(cases))
	}
	for _, f := range append(append([]store.Fact{}, lessons...), cases...) {
		if !f.FileRefsExist {
			t.Errorf("%s %q is not grounded on disk", f.Kind, f.ID)
		}
	}

	chain, err := st.VerifyChain(ProjectName)
	if err != nil {
		t.Fatalf("verify chain: %v", err)
	}
	if !chain.Intact {
		t.Errorf("hash chain is broken: violations=%v", chain.Violations)
	}
	if len(chain.Violations) != 0 {
		t.Errorf("violations = %v, want none", chain.Violations)
	}
}

func TestCleanupRemovesSandbox(t *testing.T) {
	sb, st, err := Create()
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	sb.Cleanup()

	if _, err := os.Stat(sb.Root); !os.IsNotExist(err) {
		t.Errorf("sandbox root still present after cleanup (stat err: %v)", err)
	}
	// Double cleanup is safe.
	sb.Cleanup()
}
