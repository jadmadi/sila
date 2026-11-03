package dream

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

// seedGoalsRepo creates a temp repository with one goal implemented by a
// feature commit, so goal scanning can verify it.
func seedGoalsRepo(t *testing.T, slug string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	goalDir := filepath.Join(dir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte("# "+slug+"\n\n- **Status**: pending\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "feat: implement "+slug+" (goals/"+slug+"/goal.md)")
	return dir
}

// TestBuildRecapFiltersImplementedGoalItems checks that the briefing stops
// showing stale work: handoff items that name an implemented goal disappear,
// and stale feedback becomes a resolve prompt instead of an open item.
func TestBuildRecapFiltersImplementedGoalItems(t *testing.T) {
	st := seedRecapStore(t)
	repo := seedGoalsRepo(t, "done-goal")
	if err := st.InsertHandoff(store.Handoff{
		ProjectID: "recap", Agent: "agy", Summary: "old baton",
		OpenItems: []string{"Execute goal done-goal"},
		CreatedAt: hoursAgoRFC(1), Status: "open",
	}); err != nil {
		t.Fatal(err)
	}
	ticket := store.Feedback{
		ProjectID: "recap", Agent: "agy", Category: "idea",
		Content: "Add a flag for goal done-goal.", Status: "open", CreatedAt: hoursAgoRFC(2),
	}
	if err := st.SaveFeedback(&ticket); err != nil {
		t.Fatal(err)
	}

	r, err := BuildRecap(st, "recap", "24h", repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range r.OpenItems {
		if contains(item, "done-goal") {
			t.Errorf("stale implemented-goal item should be dropped, got %q", item)
		}
	}
	found := false
	for _, note := range r.NeedsApproval {
		if contains(note, "feedback") && contains(note, "done-goal") {
			found = true
		}
	}
	if !found {
		t.Error("expected a resolve prompt for feedback referencing an implemented goal")
	}
}

// hoursAgoRFC renders an RFC3339 timestamp n hours ago (recap tests).
func hoursAgoRFC(n int) string {
	return time.Now().UTC().Add(-time.Duration(n) * time.Hour).Format(time.RFC3339)
}

func seedRecapStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/k.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	if _, _, err := st.WriteRun("", "devin", "", []store.LoadMessage{
		{ProjectID: "recap", ContentHash: "h1", TS: hoursAgoRFC(2), Role: "assistant", Content: "Shipped the checkout idempotency patch.", Tool: "devin"},
	}); err != nil {
		t.Fatal(err)
	}
	facts := []store.Fact{
		{
			ID: "fact_recap_inv", ProjectID: "recap",
			Claim: "Decision: payment writes are idempotent on (order_id, provider_event_id).",
			Kind:  "invariant", Confidence: "invariant", Corroboration: "invariant",
			Tools: []string{"devin"}, SessionRef: "manual_memo",
			FirstSeen: hoursAgoRFC(2), PromotedAt: hoursAgoRFC(2), DistillStatus: "skipped",
		},
		{
			ID: "fact_recap_ng", ProjectID: "recap",
			Claim: "Non-goal: no second frontend framework.",
			Kind:  "nongoal", Confidence: "invariant", Corroboration: "invariant",
			Tools: []string{"devin"}, SessionRef: "manual_memo",
			FirstSeen: hoursAgoRFC(2), PromotedAt: hoursAgoRFC(2), DistillStatus: "skipped",
		},
		{
			ID: "fact_recap_stale", ProjectID: "recap",
			Claim: "Refunds run through src/payments/refund_legacy.go pending deletion.",
			Kind:  "code_claim", Confidence: "ungrounded", Corroboration: "ungrounded",
			RepoFiles: []string{"src/payments/refund_legacy.go"},
			Tools:     []string{"mimocode"}, SessionRef: "sess_x",
			FirstSeen: hoursAgoRFC(3), PromotedAt: hoursAgoRFC(3), DistillStatus: "pending",
			// An approved (grounded-claim) fact whose file has since vanished:
			// this is what staleness exists to surface.
			ReviewStatus: store.ReviewApproved,
		},
	}
	for i := range facts {
		if err := st.UpsertFact(facts[i]); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.InsertHandoff(store.Handoff{
		ProjectID: "recap", Agent: "devin", Branch: "feat/x",
		Summary:   "baton pass",
		OpenItems: []string{"Finish refund idempotency"},
		CreatedAt: hoursAgoRFC(1), Status: "open",
	}); err != nil {
		t.Fatal(err)
	}
	tickets := []store.Feedback{
		{ProjectID: "recap", Agent: "codex", Category: "idea", Content: "Add a recap focus flag.", Status: "open", CreatedAt: hoursAgoRFC(2)},
		{ProjectID: "recap", Agent: "opencode", Category: "tip", Content: "Old tip.", Status: "closed", CreatedAt: hoursAgoRFC(30)},
	}
	for i := range tickets {
		if err := st.SaveFeedback(&tickets[i]); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

func TestBuildRecapSections(t *testing.T) {
	st := seedRecapStore(t)
	r, err := BuildRecap(st, "recap", "24h", "")
	if err != nil {
		t.Fatal(err)
	}

	if r.TotalMessages == 0 {
		t.Error("expected seeded messages in the recap window")
	}
	if len(r.Decisions) != 1 {
		t.Errorf("decisions = %d, want 1 (non-goals excluded)", len(r.Decisions))
	}
	if len(r.OpenItems) == 0 {
		t.Error("expected open items from handoff + feedback")
	}
	foundHandoff, foundFeedback := false, false
	for _, item := range r.OpenItems {
		if contains(item, "Finish refund idempotency") {
			foundHandoff = true
		}
		if contains(item, "recap focus flag") {
			foundFeedback = true
		}
	}
	if !foundHandoff {
		t.Error("handoff open item missing from recap")
	}
	if !foundFeedback {
		t.Error("open feedback missing from recap (closed feedback must be excluded)")
	}
	if len(r.NeedsApproval) == 0 {
		t.Error("expected unverified claim to require human review")
	}
}

func TestRenderRecapMarkdownSections(t *testing.T) {
	st := seedRecapStore(t)
	r, err := BuildRecap(st, "recap", "24h", "")
	if err != nil {
		t.Fatal(err)
	}
	md := RenderRecapMarkdown(r)
	for _, section := range []string{
		"# sila · standup",
		"## Decisions Recorded",
		"## Commits Shipped",
		"## Open Action Items",
		"## Needs Human Approval",
		"Decision: payment writes are idempotent",
		"- [ ] Finish refund idempotency",
	} {
		if !contains(md, section) {
			t.Errorf("standup markdown missing %q", section)
		}
	}
}

func TestBuildRecapNilStoreDegrades(t *testing.T) {
	r, err := BuildRecap(nil, "p", "24h", ".")
	if err != nil {
		t.Fatalf("nil store should degrade, not fail: %v", err)
	}
	if r.Decisions != nil || r.OpenItems != nil {
		t.Error("nil store recap should carry no store-derived sections")
	}
	if md := RenderRecapMarkdown(r); !contains(md, "# sila · standup") {
		t.Error("nil-store recap should still render a briefing")
	}
}

func TestBuildRecapGlobalMode(t *testing.T) {
	st := seedRecapStore(t)
	// Seed a second project
	if _, err := st.EnsureProject("/workspace/other-repo"); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertFact(store.Fact{
		ID: "fact_other_inv", ProjectID: "other-repo",
		Claim: "Decision: use WAL mode for other-repo.",
		Kind:  "invariant", Confidence: "invariant", Corroboration: "invariant",
		Tools: []string{"claude"}, SessionRef: "manual_memo",
		FirstSeen: hoursAgoRFC(1), PromotedAt: hoursAgoRFC(1), DistillStatus: "skipped",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertHandoff(store.Handoff{
		ID: "ho_other", ProjectID: "other-repo", Agent: "claude", Branch: "feat/wal",
		Summary: "wal handoff", OpenItems: []string{"verify wal checkpoint"},
		CreatedAt: hoursAgoRFC(1), Status: "open",
	}); err != nil {
		t.Fatal(err)
	}

	r, err := BuildRecap(st, "", "24h", "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Project != "" {
		t.Errorf("global recap should have empty project, got %q", r.Project)
	}
	if len(r.Decisions) < 2 {
		t.Errorf("expected decisions from both projects, got %d", len(r.Decisions))
	}
	foundOtherDecision := false
	for _, d := range r.Decisions {
		if contains(d.Claim, "WAL mode") && d.ProjectID == "other-repo" {
			foundOtherDecision = true
		}
	}
	if !foundOtherDecision {
		t.Error("missing other-repo decision in global standup")
	}

	foundOtherHandoff := false
	for _, oi := range r.OpenItems {
		if contains(oi, "[other-repo]") && contains(oi, "verify wal checkpoint") {
			foundOtherHandoff = true
		}
	}
	if !foundOtherHandoff {
		t.Error("missing other-repo tagged handoff item in global standup")
	}

	md := RenderRecapMarkdown(r)
	if !contains(md, "Scope: all projects") {
		t.Errorf("expected Scope: all projects in markdown, got %s", md)
	}
	if !contains(md, "[other-repo]") {
		t.Errorf("expected [other-repo] tag in global markdown, got %s", md)
	}

	toon := RenderRecapTOON(r)
	if !contains(toon, "standup_summary:") {
		t.Errorf("expected standup_summary in TOON, got %s", toon)
	}
	if !contains(toon, "scope: all") {
		t.Errorf("expected scope: all in TOON, got %s", toon)
	}
}

func TestTruncateClaim(t *testing.T) {
	short := "short claim"
	if got := truncateClaim(short, 140); got != short {
		t.Errorf("truncateClaim shortened a short claim: %q", got)
	}
	long := strings.Repeat("x", 200)
	got := truncateClaim(long, 140)
	if len([]rune(got)) != 141 || !strings.HasSuffix(got, "…") {
		t.Errorf("truncateClaim = %d runes, want 140 + ellipsis", len([]rune(got)))
	}
}

func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}
