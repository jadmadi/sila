package goals

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/render"
)

// captureGlobalOutput runs PrintGlobalAuditTable while capturing stdout.
func captureGlobalOutput(t *testing.T, res *GlobalAuditResult, filter string) string {
	t.Helper()
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	ui := render.New(w, true)
	PrintGlobalAuditTable(ui, res, filter)
	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}

func dashboardFixture(now time.Time) *GlobalAuditResult {
	claim := func(agent string, at time.Time) *GoalClaim {
		return &GoalClaim{ClaimedBy: agent, ClaimedAt: at}
	}
	heavy := &AuditResult{
		Project:          "heavy",
		Total:            5,
		PendingCount:     3,
		ClaimedCount:     1,
		ImplementedCount: 1,
		BlockedCount:     1,
		Goals: []Goal{
			{Slug: "heavy-claimed", Title: "Claimed goal", Status: "claimed", Independent: true, Claim: claim("opencode", now.Add(-5*24*time.Hour))},
			{Slug: "heavy-blocked", Title: "Blocked goal", Status: "pending", Independent: false, Blocked: true, UnmetDeps: []string{"heavy-root"}, RootBlockers: []string{"heavy-root"}},
			{Slug: "heavy-ready-a", Title: "Ready goal alpha", Status: "pending", Independent: true},
			{Slug: "heavy-ready-b", Title: "Ready goal beta", Status: "pending", Independent: true},
			{Slug: "heavy-done", Title: "Done goal", Status: "implemented", Independent: true, Claim: claim("devin", now.Add(-9*24*time.Hour))},
		},
	}
	light := &AuditResult{
		Project:      "light",
		Total:        2,
		PendingCount: 1,
		DraftCount:   1,
		Goals: []Goal{
			{Slug: "light-ready", Title: "Light ready goal", Status: "pending", Independent: true},
			{Slug: "light-draft", Title: "Light draft", Status: "draft"},
		},
	}
	done := &AuditResult{
		Project:          "done-repo",
		Total:            1,
		ImplementedCount: 1,
		Goals: []Goal{
			{Slug: "done-goal", Title: "Done", Status: "implemented", Independent: true},
		},
	}
	return &GlobalAuditResult{
		TotalProjects:    3,
		TotalGoals:       8,
		ImplementedCount: 2,
		ClaimedCount:     1,
		PendingCount:     4,
		ReadyCount:       3,
		BlockedCount:     1,
		DraftCount:       1,
		Projects: map[string]*AuditResult{
			"heavy":     heavy,
			"light":     light,
			"done-repo": done,
		},
		Stats: computeGlobalStats(map[string]*AuditResult{
			"heavy":     heavy,
			"light":     light,
			"done-repo": done,
		}, now),
	}
}

func TestComputeGlobalStats(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	res := dashboardFixture(now)
	stats := res.Stats

	if len(stats.TopProjects) != 2 {
		t.Fatalf("top projects = %+v, want the 2 projects with pending goals", stats.TopProjects)
	}
	if stats.TopProjects[0].Project != "heavy" || stats.TopProjects[0].Pending != 3 {
		t.Errorf("top project = %+v, want heavy with 3 pending", stats.TopProjects[0])
	}
	if stats.TopProjects[1].Project != "light" {
		t.Errorf("second top project = %q, want light", stats.TopProjects[1].Project)
	}

	if len(stats.StaleClaims) != 1 {
		t.Fatalf("stale claims = %+v, want 1", stats.StaleClaims)
	}
	got := stats.StaleClaims[0]
	if got.Goal != "heavy-claimed" || got.Project != "heavy" || got.Agent != "opencode" || got.AgeDays != 5 || got.Age != "5d" {
		t.Errorf("stale claim = %+v", got)
	}

	if stats.ImplementedLockCount != 1 || len(stats.ImplementedLockProjects) != 1 || stats.ImplementedLockProjects[0] != "heavy" {
		t.Errorf("implemented locks = %d %v, want 1 [heavy]", stats.ImplementedLockCount, stats.ImplementedLockProjects)
	}

	if stats.BlockedTotal != 1 || len(stats.BlockedByRoot) != 1 {
		t.Fatalf("blocked = %d roots=%+v, want 1 goal under 1 root", stats.BlockedTotal, stats.BlockedByRoot)
	}
	if stats.BlockedByRoot[0].Root != "heavy-root" || stats.BlockedByRoot[0].Count != 1 {
		t.Errorf("root = %+v, want heavy-root x1", stats.BlockedByRoot[0])
	}

	if len(stats.Drafts) != 1 || stats.Drafts[0].Project != "light" || len(stats.Drafts[0].Goals) != 1 {
		t.Errorf("drafts = %+v, want light with 1 goal", stats.Drafts)
	}

	if len(stats.DuplicateRegistrations) != 0 {
		t.Errorf("duplicates = %+v, want none in this fixture", stats.DuplicateRegistrations)
	}
}

// TestComputeGlobalStatsStaleBoundary pins the 3-day edge: exactly 3 days is
// not stale, a moment past it is.
func TestComputeGlobalStatsStaleBoundary(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	project := &AuditResult{
		Project:      "edge",
		Total:        2,
		PendingCount: 2,
		Goals: []Goal{
			{Slug: "exactly-three", Status: "claimed", Claim: &GoalClaim{ClaimedBy: "a", ClaimedAt: now.Add(-staleClaimThreshold)}},
			{Slug: "past-three", Status: "claimed", Claim: &GoalClaim{ClaimedBy: "b", ClaimedAt: now.Add(-staleClaimThreshold - time.Second)}},
		},
	}
	stats := computeGlobalStats(map[string]*AuditResult{"edge": project}, now)
	if len(stats.StaleClaims) != 1 || stats.StaleClaims[0].Goal != "past-three" {
		t.Fatalf("stale claims = %+v, want only past-three", stats.StaleClaims)
	}
}

// TestComputeGlobalStatsDuplicates detects two registrations for the same real
// directory that share slugs.
func TestComputeGlobalStatsDuplicates(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	real := filepath.Join(dir, "repo")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "repo-alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	a := &AuditResult{Project: "repo", ProjectDir: real, Total: 2, PendingCount: 2, Goals: []Goal{
		{Slug: "alpha", Status: "pending"}, {Slug: "beta", Status: "pending"},
	}}
	b := &AuditResult{Project: "repo-abcdef", ProjectDir: alias, Total: 2, PendingCount: 2, Goals: []Goal{
		{Slug: "alpha", Status: "pending"}, {Slug: "beta", Status: "pending"},
	}}
	stats := computeGlobalStats(map[string]*AuditResult{"repo": a, "repo-abcdef": b}, now)
	if len(stats.DuplicateRegistrations) != 1 {
		t.Fatalf("duplicates = %+v, want one pair", stats.DuplicateRegistrations)
	}
	d := stats.DuplicateRegistrations[0]
	if d.Project != "repo" || d.Other != "repo-abcdef" || d.Overlap != 2 {
		t.Errorf("duplicate = %+v", d)
	}
	if len(stats.LookalikeRegistrations) != 0 {
		t.Errorf("lookalikes = %+v, want none for the same real directory", stats.LookalikeRegistrations)
	}
}

func TestPrintGlobalAuditTableCompactShape(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	out := captureGlobalOutput(t, dashboardFixture(now), "")

	// One line per goal: the slug and title share a line, and the per-goal
	// Claim:/Launch: boilerplate is gone.
	if !strings.Contains(out, "▶ heavy-ready-a — Ready goal alpha ⚡") {
		t.Errorf("pending goal line missing, got:\n%s", out)
	}
	if strings.Count(out, "Claim:") != 1 {
		t.Errorf("the claim pattern must be stated once, found %d:\n%s", strings.Count(out, "Claim:"), out)
	}
	if !strings.Contains(out, "sila goals claim <slug> --agent=<name>") {
		t.Errorf("footer must state the claim pattern, got:\n%s", out)
	}
	if !strings.Contains(out, "/goal goals/<slug>/goal.md") {
		t.Errorf("footer must state the launch pattern, got:\n%s", out)
	}

	// Claimed goals carry holder and age on their line.
	var claimedLine string
	for line := range strings.SplitSeq(out, "\n") {
		if strings.Contains(line, "🔒 heavy-claimed") {
			claimedLine = line
		}
	}
	if !strings.Contains(claimedLine, "🔒 heavy-claimed — Claimed goal ⚡ @opencode ") {
		t.Errorf("claimed goal line missing holder: %q", claimedLine)
	}
	if !strings.HasSuffix(strings.TrimSpace(claimedLine), "d") {
		t.Errorf("claimed goal line missing day-granularity age: %q", claimedLine)
	}
	// Blocked goals name the root.
	if !strings.Contains(out, "⛔") || !strings.Contains(out, "blocked by heavy-root") {
		t.Errorf("blocked goal line missing root, got:\n%s", out)
	}

	// One-line project headers, pending work first.
	heavyIdx := strings.Index(out, "📁 heavy")
	lightIdx := strings.Index(out, "📁 light")
	doneIdx := strings.Index(out, "fully implemented")
	if heavyIdx == -1 || lightIdx == -1 || doneIdx == -1 {
		t.Fatalf("project lines missing (heavy=%d light=%d done=%d):\n%s", heavyIdx, lightIdx, doneIdx, out)
	}
	if !(heavyIdx < lightIdx && lightIdx < doneIdx) {
		t.Errorf("expected heavy before light before the collapse footer, got %d %d %d", heavyIdx, lightIdx, doneIdx)
	}
	if !strings.Contains(out, "fully implemented (1): done-repo") {
		t.Errorf("zero-pending repository must collapse to the footer, got:\n%s", out)
	}
	if strings.Contains(out, "📁 done-repo") {
		t.Errorf("a finished repository must not print a section, got:\n%s", out)
	}

	// The closing statistics block.
	for _, want := range []string{
		"📊 Where the work is",
		"⚠️  Needs attention",
		"Stale live claims",
		"Locks on implemented goals",
		"Blocked work",
		"sila goals release <slug>",
		"sila goals release --stale --global --dry-run",
		"sila goals prune --claims --global --dry-run",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("statistics block missing %q, got:\n%s", want, out)
		}
	}
}

func TestPrintGlobalAuditTableFilters(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	res := dashboardFixture(now)

	ready := captureGlobalOutput(t, res, "ready")
	if strings.Contains(ready, "▶ heavy-blocked") {
		t.Errorf("ready filter must hide blocked goals from the project section:\n%s", ready)
	}
	if !strings.Contains(ready, "heavy-ready-a") {
		t.Errorf("ready filter must keep unblocked goals:\n%s", ready)
	}

	claimed := captureGlobalOutput(t, res, "claimed")
	if !strings.Contains(claimed, "🔒 heavy-claimed") {
		t.Errorf("claimed filter must show claimed goals:\n%s", claimed)
	}
	if strings.Contains(claimed, "▶ heavy-ready-a") {
		t.Errorf("claimed filter must hide pending goals:\n%s", claimed)
	}

	blocked := captureGlobalOutput(t, res, "blocked")
	if !strings.Contains(blocked, "▶ heavy-blocked") || strings.Contains(blocked, "▶ heavy-ready-a") {
		t.Errorf("blocked filter selection wrong:\n%s", blocked)
	}
}

// TestProjectScopedViewUnchanged keeps the per-project rendering free of the
// global dashboard's compact shape and statistics.
func TestProjectScopedViewUnchanged(t *testing.T) {
	audit := &AuditResult{
		Project:      "solo",
		Total:        1,
		PendingCount: 1,
		ReadyCount:   1,
		Goals:        []Goal{{Slug: "solo-goal", Title: "Solo goal", Status: "pending", Independent: true}},
	}
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

	if strings.Contains(out, "📊 Where the work is") || strings.Contains(out, "fully implemented (") {
		t.Errorf("project-scoped view must not render the global dashboard:\n%s", out)
	}
	if !strings.Contains(out, "sila goals claim solo-goal --agent=<name>") {
		t.Errorf("project-scoped view still prints the per-goal claim command:\n%s", out)
	}
}

// TestGlobalJSONAddsOnlyStats keeps the machine contract additive.
func TestGlobalJSONAddsOnlyStats(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	res := dashboardFixture(now)
	data, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"total_projects", "total_goals", "implemented_count", "claimed_count",
		"pending_count", "ready_count", "blocked_count", "contested_count",
		"independent_count", "draft_count", "projects",
	} {
		if _, ok := payload[key]; !ok {
			t.Errorf("existing JSON field %q disappeared", key)
		}
	}
	stats, ok := payload["stats"].(map[string]any)
	if !ok {
		t.Fatalf("stats missing from JSON payload: %v", payload["stats"])
	}
	for _, key := range []string{
		"top_projects", "stale_claims", "implemented_lock_count",
		"implemented_lock_projects", "blocked_total", "blocked_by_root",
		"duplicate_registrations", "drafts",
	} {
		if _, ok := stats[key]; !ok {
			t.Errorf("stats JSON field %q missing", key)
		}
	}
}
