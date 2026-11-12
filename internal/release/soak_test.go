package release

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

func writeTestCert(t *testing.T, repoDir, slug string, certTime time.Time) {
	t.Helper()
	goalDir := filepath.Join(repoDir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0o755); err != nil {
		t.Fatalf("mkdir goal: %v", err)
	}
	rec := map[string]any{
		"goal_slug":    slug,
		"certified_at": certTime.UTC().Format(time.RFC3339Nano),
		"commit_sha":   "abc1234",
		"verified":     true,
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal cert: %v", err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "certification.json"), data, 0o644); err != nil {
		t.Fatalf("write cert: %v", err)
	}
}

func seedSoakStore(t *testing.T, dbPath string, toolEvents []store.ToolEvent, primeEvents []store.PrimeEvent) {
	t.Helper()
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	defer st.Close()

	for _, ev := range toolEvents {
		if err := st.RecordToolEvent(ev); err != nil {
			t.Fatalf("RecordToolEvent: %v", err)
		}
	}
	for _, pev := range primeEvents {
		if err := st.RecordPrimeEvent(pev); err != nil {
			t.Fatalf("RecordPrimeEvent: %v", err)
		}
	}
}

func TestEvaluateSoak_InProgressDuration(t *testing.T) {
	repoDir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "sila.db")
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	certTime := now.Add(-24 * time.Hour)

	writeTestCert(t, repoDir, "feature-alpha", certTime)
	seedSoakStore(t, dbPath, []store.ToolEvent{
		{
			ProjectID: "sila",
			ToolName:  "prime_context",
			Outcome:   "ok",
			CreatedAt: certTime.Add(2 * time.Hour),
		},
	}, nil)

	rep := EvaluateSoak(SoakOptions{
		RepoDir: repoDir,
		DBPath:  dbPath,
		Now:     now,
	})

	if rep.State != SoakInProgress {
		t.Fatalf("expected SoakInProgress, got %s (%s)", rep.State, rep.Summary)
	}
	if rep.CurrentDay != 2 {
		t.Errorf("expected CurrentDay 2 after 24h, got %d", rep.CurrentDay)
	}
	if rep.RemainingHours != 48.0 {
		t.Errorf("expected 48.0 remaining hours, got %.1f", rep.RemainingHours)
	}
	if rep.ActiveDays != 1 {
		t.Errorf("expected 1 active day, got %d", rep.ActiveDays)
	}
}

func TestEvaluateSoak_InProgressInsufficientActiveDays(t *testing.T) {
	repoDir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "sila.db")
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	certTime := now.Add(-80 * time.Hour)

	writeTestCert(t, repoDir, "feature-beta", certTime)
	seedSoakStore(t, dbPath, []store.ToolEvent{
		{
			ProjectID: "sila",
			ToolName:  "prime_context",
			Outcome:   "ok",
			CreatedAt: certTime.Add(2 * time.Hour),
		},
		{
			ProjectID: "sila",
			ToolName:  "run_gate_pipeline",
			Outcome:   "ok",
			CreatedAt: certTime.Add(28 * time.Hour),
		},
	}, nil)

	rep := EvaluateSoak(SoakOptions{
		RepoDir: repoDir,
		DBPath:  dbPath,
		Now:     now,
	})

	if rep.State != SoakInProgress {
		t.Fatalf("expected SoakInProgress when only 2/3 active days exist, got %s", rep.State)
	}
	if rep.ActiveDays != 2 {
		t.Errorf("expected 2 active days, got %d", rep.ActiveDays)
	}
}

func TestEvaluateSoak_NeedsDebugOnToolOrPrimeError(t *testing.T) {
	repoDir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "sila.db")
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	certTime := now.Add(-80 * time.Hour)

	writeTestCert(t, repoDir, "feature-gamma", certTime)
	seedSoakStore(t, dbPath, []store.ToolEvent{
		{
			ProjectID: "sila",
			ToolName:  "prime_context",
			Outcome:   "ok",
			CreatedAt: certTime.Add(2 * time.Hour),
		},
		{
			ProjectID: "sila",
			ToolName:  "audit_goal",
			Outcome:   "error",
			CreatedAt: certTime.Add(26 * time.Hour),
		},
		{
			ProjectID: "sila",
			ToolName:  "run_gate_pipeline",
			Outcome:   "ok",
			CreatedAt: certTime.Add(52 * time.Hour),
		},
	}, []store.PrimeEvent{
		{
			ProjectID:  "sila",
			Harness:    "claude-code",
			Status:     "failed",
			ErrorClass: "timeout",
			Reason:     "db busy",
			CreatedAt:  certTime.Add(55 * time.Hour),
		},
	})

	rep := EvaluateSoak(SoakOptions{
		RepoDir: repoDir,
		DBPath:  dbPath,
		Now:     now,
	})

	if rep.State != SoakNeedsDebug {
		t.Fatalf("expected SoakNeedsDebug on tool/prime error, got %s", rep.State)
	}
	if rep.ToolErrorCount != 1 {
		t.Errorf("expected 1 tool error, got %d", rep.ToolErrorCount)
	}
	if rep.PrimeErrorCount != 1 {
		t.Errorf("expected 1 prime error, got %d", rep.PrimeErrorCount)
	}
	if len(rep.FailedTools) != 1 || rep.FailedTools[0].ToolName != "audit_goal" {
		t.Errorf("expected audit_goal in FailedTools, got %+v", rep.FailedTools)
	}

	baseVerdict := ReleaseVerdictInfo{Code: "READY_TO_RELEASE", Verdict: "READY TO RELEASE"}
	overridden := applySoakVerdict(baseVerdict, rep)
	if overridden.Code != "SOAK_NEEDS_DEBUG" {
		t.Errorf("expected SOAK_NEEDS_DEBUG verdict override, got %s", overridden.Code)
	}
}

func TestEvaluateSoak_ReadyAfterThreeActiveDaysAnd72Hours(t *testing.T) {
	repoDir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "sila.db")
	now := time.Now().UTC().Truncate(time.Second)
	certTime := now.Add(-80 * time.Hour)

	writeTestCert(t, repoDir, "feature-delta", certTime)
	var toolEvents []store.ToolEvent
	for i := 0; i < 24; i++ {
		agent := "claude-code"
		if i%2 == 1 {
			agent = "opencode"
		}
		toolEvents = append(toolEvents, store.ToolEvent{
			ProjectID: "sila",
			ToolName:  "prime_context",
			Agent:     agent,
			Outcome:   "ok",
			CreatedAt: certTime.Add(time.Duration(i) * 3 * time.Hour),
		})
	}
	seedSoakStore(t, dbPath, toolEvents, []store.PrimeEvent{
		{
			ProjectID: "sila",
			Harness:   "claude-code",
			Status:    "ok",
			CreatedAt: certTime.Add(73 * time.Hour),
		},
	})

	rep := EvaluateSoak(SoakOptions{
		RepoDir: repoDir,
		DBPath:  dbPath,
		Now:     now,
	})

	if rep.State != SoakReady {
		t.Fatalf("expected SoakReady after 80h and 3 active days with 0 errors, got %s (%s)", rep.State, rep.Summary)
	}
	if rep.ActiveDays < 3 {
		t.Errorf("expected at least 3 active days, got %d", rep.ActiveDays)
	}

	baseVerdict := ReleaseVerdictInfo{Code: "READY_TO_RELEASE", Verdict: "READY TO RELEASE"}
	overridden := applySoakVerdict(baseVerdict, rep)
	if overridden.Code != "READY_TO_RELEASE" {
		t.Errorf("expected READY_TO_RELEASE preserved when soak is ready, got %s", overridden.Code)
	}

	chk := checkLocalSoak(repoDir, PreflightOptions{RepoDir: repoDir, DBPath: dbPath, Strict: true})
	if chk.Status != StatusPass {
		t.Errorf("expected StatusPass from checkLocalSoak in strict mode when ready, got %s (%s)", chk.Status, chk.Detail)
	}
	if !strings.Contains(chk.Detail, "3-day local soak verified") {
		t.Errorf("expected verified detail, got %s", chk.Detail)
	}
	if len(rep.FeatureLedger) == 0 {
		t.Fatalf("expected automated FeatureLedger rows in sila.db, got 0")
	}
	if rep.FeatureLedger[0].FeatureID != "feature-delta" || rep.FeatureLedger[0].Status != "ready" {
		t.Errorf("expected feature-delta status=ready in FeatureLedger, got %+v", rep.FeatureLedger[0])
	}
	if len(rep.DailyReports) == 0 {
		t.Fatalf("expected automated DailyReports row in sila.db, got 0")
	}
}

func TestResolveBaseRangeFallsBackWhenTagMissing(t *testing.T) {
	repo := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.test",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.test")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("init", "-q")
	run("add", ".")
	run("commit", "-q", "-m", "feat: one")

	published := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	ref, since := resolveBaseRange(repo, "v0.18.2", published)
	if ref != "" {
		t.Fatalf("missing tag should not be used as a ref, got %q", ref)
	}
	if since.IsZero() {
		t.Fatal("expected the published time as the fallback")
	}

	commits, impact := analyzeCommits(repo, "v0.18.2", published)
	if len(commits) == 0 {
		t.Fatal("expected commits since the published time")
	}
	if impact.MinorFeatures == 0 {
		t.Fatalf("expected a minor feature, got %+v", impact)
	}
}

func TestSoakClockFollowsCodeNotRecertification(t *testing.T) {
	repo := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	old := now.Add(-80 * time.Hour).Format(time.RFC3339)
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(env []string, args ...string) {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(), env...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(nil, "init", "-q")
	run(nil, "add", ".")
	ident := []string{
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.test",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.test",
		"GIT_AUTHOR_DATE=" + old, "GIT_COMMITTER_DATE=" + old,
	}
	run(ident, "commit", "-q", "-m", "feat: old feature")

	writeTestCert(t, repo, "late-recert", now.Add(-1*time.Hour))

	rep := EvaluateSoak(SoakOptions{
		RepoDir: repo,
		DBPath:  filepath.Join(t.TempDir(), "sila.db"),
		Now:     now,
	})
	if !strings.Contains(rep.SoakTrigger, "commit") {
		t.Fatalf("soak trigger = %q, want the feature commit", rep.SoakTrigger)
	}
	if rep.ElapsedHours < 70 {
		t.Fatalf("elapsed = %.1fh, want the 80h commit clock, not a 1h recertification clock", rep.ElapsedHours)
	}
}

func TestSoakProjectScope(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "scope.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	root := t.TempDir()
	child := filepath.Join(root, "cli")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	rootProject, err := st.EnsureProject(root)
	if err != nil {
		t.Fatal(err)
	}
	childProject, err := st.EnsureProject(child)
	if err != nil {
		t.Fatal(err)
	}
	ids := soakProjectIDs(st, root)
	if len(ids) != 1 || ids[0] != rootProject.ID {
		t.Fatalf("root ids = %v", ids)
	}
	ids = soakProjectIDs(st, child)
	got := map[string]bool{}
	for _, id := range ids {
		got[id] = true
	}
	if len(ids) != 2 || !got[childProject.ID] || !got[rootProject.ID] {
		t.Fatalf("child ids = %v", ids)
	}
	ids = soakProjectIDs(st, root)
	now := time.Now().UTC()
	_ = st.RecordToolEvent(store.ToolEvent{ProjectID: rootProject.ID, SessionID: "s1", Agent: "cli", Source: "cli", ToolName: "memo", Category: "write", Outcome: "ok", CreatedAt: now})
	_ = st.RecordToolEvent(store.ToolEvent{ProjectID: childProject.ID, SessionID: "s2", Agent: "mcp", Source: "mcp", ToolName: "memo", Category: "write", Outcome: "error", CreatedAt: now})
	events := soakToolEvents(st, ids, now.Add(-time.Hour))
	if len(events) != 1 || events[0].Outcome != "ok" {
		t.Fatalf("scoped events = %+v", events)
	}
	unscoped := soakToolEvents(st, nil, now.Add(-time.Hour))
	if len(unscoped) != 2 {
		t.Fatalf("unscoped events = %d", len(unscoped))
	}
}

func TestSoakReleaseScopeFindsNestedModule(t *testing.T) {
	root := t.TempDir()
	cli := filepath.Join(root, "sila", "cli")
	if err := os.MkdirAll(cli, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cli, "go.mod"), []byte("module github.com/jadmadi/sila\n\ngo 1.27\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := soakReleaseScope(root); got != cli {
		t.Fatalf("root scope = %q, want %q", got, cli)
	}
	if got := soakReleaseScope(cli); got != cli {
		t.Fatalf("cli scope = %q, want %q", got, cli)
	}
	if got := soakReleaseScope(t.TempDir()); got == cli {
		t.Fatalf("unexpected module scope %q", got)
	}
}

func TestHarnessIdentity(t *testing.T) {
	cases := []struct{ agent, source, want string }{
		{"cli", "cli", ""},
		{"mcp", "mcp", ""},
		{"", "cli", ""},
		{"", "mcp", ""},
		{"opencode", "mcp", "opencode"},
		{"antigravity", "cli", "antigravity"},
		{"", "codex", "codex"},
	}
	for _, tc := range cases {
		if got := harnessIdentity(tc.agent, tc.source); got != tc.want {
			t.Fatalf("harnessIdentity(%q,%q) = %q, want %q", tc.agent, tc.source, got, tc.want)
		}
	}
}

func TestLatestPublishedTag(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	root := t.TempDir()
	priv := filepath.Join(root, "sila", "cli")
	pub := filepath.Join(root, "sila", "public-export")
	for _, dir := range []string{priv, pub} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		runGit := func(args ...string) {
			cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
			cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v (%s)", args, err, out)
			}
		}
		runGit("init", "-q")
		runGit("commit", "--allow-empty", "-q", "-m", "init")
	}
	cmd := exec.Command("git", "-C", pub, "tag", "v9.9.9")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tag: %v (%s)", err, out)
	}
	if got := latestPublishedTag(priv); got != "v9.9.9" {
		t.Fatalf("tag = %q", got)
	}
	if ts := tagCommitTime(priv, "v9.9.9"); ts.IsZero() {
		t.Fatalf("expected tagCommitTime to resolve v9.9.9 from sibling public-export")
	}
}

func TestEvaluateSoak_PerFeatureIndependentTimers(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	repo := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "sila.db")
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)

	runGit := func(commitTime time.Time, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		env := append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.test",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.test",
		)
		if !commitTime.IsZero() {
			iso := commitTime.UTC().Format(time.RFC3339)
			env = append(env, "GIT_AUTHOR_DATE="+iso, "GIT_COMMITTER_DATE="+iso)
		}
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	runGit(time.Time{}, "init", "-q")

	// Commit 1: 80h ago (already soaked >= 72h across 4 active days)
	t1 := now.Add(-80 * time.Hour)
	if err := os.WriteFile(filepath.Join(repo, "alpha.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t1, "add", ".")
	runGit(t1, "commit", "-q", "-m", "feat(core): add alpha engine")

	// Commit 2: 6h ago (new feature with its own independent timer; must not reset alpha)
	t2 := now.Add(-6 * time.Hour)
	if err := os.WriteFile(filepath.Join(repo, "beta.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t2, "add", ".")
	runGit(t2, "commit", "-q", "-m", "feat(core): add beta engine")

	// Commit 3: 1h ago touching only goals/ paperwork (must not create a feature timer)
	t3 := now.Add(-1 * time.Hour)
	goalDir := filepath.Join(repo, "goals", "new-goal")
	if err := os.MkdirAll(goalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte("# Goal: New Goal\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t3, "add", ".")
	runGit(t3, "commit", "-q", "-m", "feat(goals): register new-goal")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	proj, err := st.EnsureProject(repo)
	if err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	var events []store.ToolEvent
	for i := 0; i < 24; i++ {
		agent := "antigravity"
		if i%2 == 1 {
			agent = "commandcode"
		}
		events = append(events, store.ToolEvent{
			ProjectID: proj.ID,
			ToolName:  "recall",
			Agent:     agent,
			Outcome:   "ok",
			CreatedAt: t1.Add(time.Duration(i) * 3 * time.Hour),
		})
	}
	// Error in an unrelated project must not mark repo features as needs_debug.
	events = append(events, store.ToolEvent{
		ProjectID: "unrelated-project",
		ToolName:  "audit_goal",
		Agent:     "antigravity",
		Outcome:   "error",
		CreatedAt: t1.Add(10 * time.Hour),
	})
	seedSoakStore(t, dbPath, events, nil)

	rep := EvaluateSoak(SoakOptions{
		RepoDir: repo,
		DBPath:  dbPath,
		Now:     now,
	})

	if rep.State != SoakReady {
		t.Fatalf("expected SoakReady when alpha is ready and beta is soaking on its own timer, got %s (%s)", rep.State, rep.Summary)
	}
	if rep.ReadyFeatures != 1 {
		t.Fatalf("ReadyFeatures = %d, want 1", rep.ReadyFeatures)
	}
	if rep.SoakingFeatures != 1 {
		t.Fatalf("SoakingFeatures = %d, want 1", rep.SoakingFeatures)
	}
	if rep.DebugFeatures != 0 {
		t.Fatalf("DebugFeatures = %d, want 0", rep.DebugFeatures)
	}
	if rep.ReadyThroughCommit == "" {
		t.Fatalf("expected non-empty ReadyThroughCommit for ready alpha feature")
	}
	if len(rep.FeatureLedger) != 2 {
		t.Fatalf("len(FeatureLedger) = %d, want 2 (alpha and beta; goals-only commit excluded)", len(rep.FeatureLedger))
	}
	// FeatureLedger is sorted newest-first: [0] is beta (soaking, ~6h), [1] is alpha (ready, ~80h).
	if rep.FeatureLedger[0].Status != "soaking" || rep.FeatureLedger[0].ElapsedHours > 10 {
		t.Fatalf("expected newest feature beta to be soaking (~6h), got %+v", rep.FeatureLedger[0])
	}
	if rep.FeatureLedger[1].Status != "ready" || rep.FeatureLedger[1].ElapsedHours < 79 {
		t.Fatalf("expected older feature alpha to remain ready (~80h), got %+v", rep.FeatureLedger[1])
	}
	if got := formatFeatureTrigger(rep.FeatureLedger[0]); !strings.Contains(got, "feat(core): add beta engine") {
		t.Fatalf("formatFeatureTrigger = %q", got)
	}
	if got := nonEmptyCommit("abc12345"); got != "abc12345" {
		t.Fatalf("nonEmptyCommit = %q", got)
	}
	if !isNonRuntimeCommit([]string{"goals/foo/goal.md", "docs/reference.md"}) || isNonRuntimeCommit([]string{"goals/foo/goal.md", "internal/release/soak.go"}) {
		t.Fatal("isNonRuntimeCommit classification failed")
	}
	if !isNonRuntimePath("goals/README.md") || isNonRuntimePath("cmd/sila/soak.go") {
		t.Fatal("isNonRuntimePath classification failed")
	}
	if !hasGitCommits(repo) || commitTimeInRepo(repo, "HEAD").IsZero() {
		t.Fatal("expected hasGitCommits and commitTimeInRepo to succeed on test repo")
	}
	filtered := filterFeatureLedger(rep.FeatureLedger, []string{proj.ID}, time.Time{})
	if len(filtered) != 2 {
		t.Fatalf("filterFeatureLedger = %d, want 2", len(filtered))
	}
}
