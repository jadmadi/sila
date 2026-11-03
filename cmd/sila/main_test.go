package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestCLIDoctorAndSearch(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "cli_test.db")

	ui := render.New(os.Stdout, true)

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := st.EnsureProject(tmp)
	_, _, _ = st.WriteRun("", "git", "1", []store.LoadMessage{
		{ProjectID: p.ID, ContentHash: "h1", TS: "2026-08-20T10:00:00Z", Role: "commit", Content: "Git Commit 1234abcd: Initial commit for repo", Tool: "git"},
		{ProjectID: p.ID, ContentHash: "h2", TS: "2026-08-20T10:05:00Z", Role: "user", Content: "How do we configure sqlite?", Tool: "devin"},
		{ProjectID: p.ID, ContentHash: "h3", TS: "2026-08-20T10:06:00Z", Role: "assistant", Content: "Use pure-Go SQLite with modernc.org/sqlite", Tool: "devin"},
	})
	_ = st.InsertDoc(store.Doc{
		ID:        "doc-cli-1",
		ProjectID: p.ID,
		Title:     "SQLite Architecture",
		Content:   "Database configuration details",
		CreatedAt: "2026-08-20T10:00:00Z",
	})
	st.Close()

	// Test Doctor
	runDoctor(ui, dbPath, p.ID, false, true, false, false, false, false, false, false)
	runDoctor(ui, dbPath, p.ID, true, true, false, false, false, false, false, false)
	runDoctor(ui, dbPath, p.ID, false, true, false, true, false, false, false, false)
	runDoctor(ui, dbPath, p.ID, false, true, false, false, true, false, false, false)
	runDoctor(ui, dbPath, p.ID, false, true, false, false, false, true, false, false)

	// Test Omni search
	runSearch(ui, dbPath, p.ID, "", "", time.Time{}, true, false, true, 3, 0, false, false, false, true, false, false, 10, []string{"sqlite"})

	// Test Claims search
	runSearch(ui, dbPath, p.ID, "", "", time.Time{}, false, false, true, 0, 0, false, false, false, true, false, false, 10, []string{"claims", "sqlite"})

	// Test Docs search
	runSearch(ui, dbPath, p.ID, "", "", time.Time{}, false, false, true, 0, 0, false, false, false, true, false, false, 10, []string{"docs", "architecture"})

	// Test Expand search
	runSearch(ui, dbPath, p.ID, "", "", time.Time{}, false, false, true, 2, 0, false, false, false, true, false, false, 10, []string{"expand", "2"})

	// Test Two-Pass Message search
	runSearch(ui, dbPath, p.ID, "", "", time.Time{}, false, false, true, 2, 0, false, false, false, true, false, false, 10, []string{"sqlite"})

	// Test Single-Pass Message search
	runSearch(ui, dbPath, p.ID, "", "", time.Time{}, false, false, false, 2, 0, false, false, false, true, false, false, 10, []string{"sqlite"})

	// Test Reconcile / Verify
	stVerify, errVerify := store.Open(dbPath)
	if errVerify == nil {
		runReconcile(ui, stVerify, p.ID, false, false, true, false)
		runReconcile(ui, stVerify, p.ID, true, false, true, false)
		stVerify.Close()
	}

	// Test Sync single run
	runSync(ui, dbPath, p.ID, false, false, false, false, true, false, "15s", time.Time{}, false, false, nil)
	runSync(ui, dbPath, p.ID, false, false, true, false, true, false, "15s", time.Time{}, false, false, nil)

	// Test Sync when live lock is active
	rel, err := store.AcquireLiveLock(dbPath, p.ID)
	if err == nil {
		runSync(ui, dbPath, p.ID, false, false, false, false, true, false, "15s", time.Time{}, false, false, nil)
		rel()
	}

	// Test Feedback recording and listing
	runFeedback(ui, dbPath, p.ID, "antigravity", "tip", "", "", "", "", true, 10, false, false, false, false, false, false, []string{"Always test with -race"})
	runFeedback(ui, dbPath, p.ID, "antigravity", "tip", "", "", "", "", true, 10, false, false, false, false, false, false, []string{"list"})
	runFeedback(ui, dbPath, p.ID, "antigravity", "tip", "", "", "", "", true, 10, true, false, false, false, false, false, []string{"open"})
	runFeedback(ui, dbPath, p.ID, "antigravity", "tip", "", "", "", "", true, 10, false, true, false, false, false, false, []string{"closed"})

	// Test Goals lifecycle: create, claim, touch, report, update, depend, ready, independent, release, pending
	runGoals(ui, dbPath, tmp, "antigravity", "", "", "", false, false, "", "CLI Goal", "Testing CLI goals", "", "", 1, true, false, false, false, false, false, false, false, true, false, false, false, false, "", "", "", false, []string{"create", "cli-goal"})
	runGoals(ui, dbPath, tmp, "antigravity", "starting goal", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, true, false, false, false, false, "", "", "", false, []string{"claim", "cli-goal"})
	runGoals(ui, dbPath, tmp, "antigravity", "", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, true, false, false, false, false, "", "", "", false, []string{"touch", "cli-goal"})
	runGoals(ui, dbPath, tmp, "antigravity", "50% done", "evaluating", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, true, false, false, false, false, "", "", "", false, []string{"report", "cli-goal"})
	runGoals(ui, dbPath, tmp, "", "", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, true, false, false, false, false, "", "dep-alpha", "", false, []string{"update", "cli-goal"})
	runGoals(ui, dbPath, tmp, "", "", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, true, false, false, false, false, "", "", "", false, []string{"depend", "cli-goal", "add", "dep-beta"})
	runGoals(ui, dbPath, tmp, "", "", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, true, false, false, false, false, "", "", "", false, []string{"depend", "cli-goal"})
	runGoals(ui, dbPath, tmp, "", "", "", "", false, false, "", "", "", "", "", 0, false, false, false, true, false, false, false, false, true, false, false, false, false, "", "", "", false, nil)
	runGoals(ui, dbPath, tmp, "", "", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, true, false, false, false, false, "", "", "", false, []string{"release", "cli-goal"})
	runGoals(ui, dbPath, tmp, "", "", "", "", false, false, "", "", "", "", "", 0, false, false, true, false, false, false, false, false, true, false, false, false, false, "", "", "", false, nil)
	runGoals(ui, dbPath, tmp, "", "", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, true, false, false, false, true, false, false, false, false, "", "", "", false, nil)
}

// TestSearchEmptyStateNamesTheScopeItSearched pins the fix for an empty result
// set that printed "in project " with no name when no project filter was given,
// reading as if a filter had been applied. It also pins the hint that a
// multi-term query requires every term, which is what actually caused the
// zero-result surprise the message was covering for.
func TestSearchEmptyStateNamesTheScopeItSearched(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "scope_test.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := st.EnsureProject(tmp)
	_, _, _ = st.WriteRun("", "devin", "1", []store.LoadMessage{
		{ProjectID: p.ID, ContentHash: "scope-1", TS: "2026-08-20T10:00:00Z", Role: "user", Content: "alpha beta gamma", Tool: "devin"},
	})
	st.Close()

	ui := render.New(os.Stdout, true)
	run := func(project string, terms ...string) string {
		return captureStdout(t, func() {
			runSearch(ui, dbPath, project, "", "", time.Time{}, false, false, false, 3, 0, false, false, false, false, false, false, 10, terms)
		})
	}

	unscoped := run("", "nonexistentterm")
	if !strings.Contains(unscoped, "across all projects") {
		t.Errorf("unscoped empty state must name every project, got: %q", unscoped)
	}
	if strings.Contains(unscoped, "in project \n") {
		t.Errorf("unscoped empty state printed a blank project name: %q", unscoped)
	}

	scoped := run(p.ID, "nonexistentterm")
	if !strings.Contains(scoped, "in project "+p.ID) {
		t.Errorf("scoped empty state must name the project %q, got: %q", p.ID, scoped)
	}

	multi := run("", "nonexistentterm", "alsomissing")
	if !strings.Contains(multi, "every term must appear") {
		t.Errorf("multi-term empty state must explain the all-terms requirement, got: %q", multi)
	}
	if single := run("", "nonexistentterm"); strings.Contains(single, "every term must appear") {
		t.Errorf("single-term empty state must not mention the all-terms requirement: %q", single)
	}
}

func TestOpenMCPStoreSupportsPublicWriteTools(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "mcp.db")
	st, err := openMCPStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.SaveFeedback(&store.Feedback{Content: "MCP write contract", Category: "tip"}); err != nil {
		t.Fatalf("MCP store must support write tools: %v", err)
	}
}

func TestGateCleanSkippedTestsPolicy(t *testing.T) {
	skipped := []gateCheck{{Name: "tests", Status: "skip"}}
	if !computeGateVerdict(skipped, false, false).Clean {
		t.Fatal("a skipped test run is advisory by default")
	}
	if computeGateVerdict(skipped, true, false).Clean {
		t.Fatal("--require-tests must promote a skipped test run to a blocker")
	}
	if !gateClean([]gateCheck{{Name: "tests", Status: "pass"}}) {
		t.Fatal("all passing checks should be clean")
	}
}

func TestScopedGoalsCLICommands(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "scoped_cli_test.db")

	ui := render.New(os.Stdout, true)

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.EnsureProject(tmp)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	// 1. Scoped create: sila goals <project> create scoped-goal
	runGoals(ui, dbPath, "", "antigravity", "", "", "", false, false, "", "Scoped Goal", "Testing scoped goals", "", "", 1, true, false, false, false, false, false, false, false, true, false, false, false, false, "", "", "", false, []string{p.ID, "create", "scoped-goal"})

	// 2. Scoped claim: sila goals <project> claim scoped-goal
	runGoals(ui, dbPath, "", "antigravity", "claiming scoped", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, true, false, false, false, false, "", "", "", false, []string{p.ID, "claim", "scoped-goal"})

	// 3. Scoped report: sila goals <project> report scoped-goal "Scoped note 1"
	runGoals(ui, dbPath, "", "antigravity", "scoped note 1", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, true, false, false, false, false, "", "", "", false, []string{p.ID, "report", "scoped-goal"})

	// 4. Scoped status alias: sila goals <project> status scoped-goal "Scoped note 2"
	runGoals(ui, dbPath, "", "antigravity", "scoped note 2", "blocked", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, true, false, false, false, false, "", "", "", false, []string{p.ID, "status", "scoped-goal"})

	// 5. Scoped release: sila goals <project> release scoped-goal
	runGoals(ui, dbPath, "", "", "", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, true, false, false, false, false, "", "", "", false, []string{p.ID, "release", "scoped-goal"})

	// 6. Scoped filters
	runGoals(ui, dbPath, "", "", "", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, true, false, false, false, false, "", "", "", false, []string{p.ID, "ready"})
	runGoals(ui, dbPath, "", "", "", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, true, false, false, false, false, "", "", "", false, []string{p.ID, "pending"})
	runGoals(ui, dbPath, "", "", "", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, true, false, false, false, false, "", "", "", false, []string{p.ID, "independent"})
	runGoals(ui, dbPath, "", "", "", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, true, false, false, false, false, "", "", "", false, []string{p.ID, "blocked"})
	runGoals(ui, dbPath, "", "", "", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, true, false, false, false, false, "", "", "", false, []string{p.ID, "claimed"})
	runGoals(ui, dbPath, "", "", "", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, true, false, false, false, false, "", "", "", false, []string{p.ID, "contested"})
	runGoals(ui, dbPath, "", "", "", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, true, false, false, false, false, "", "", "", false, []string{p.ID, "conflicts"})
}

// TestLeadingFlagGuard pins the contract that a flag placed before the
// subcommand is rejected instead of silently dropped. Before the guard,
// `sila --db=/missing.db status` exited 0 after running against the live
// store, so the test asserts the command never ran and stderr names the flag
// and the canonical order.
func TestLeadingFlagGuard(t *testing.T) {
	if probe := os.Getenv("SILA_LEADING_GUARD_PROBE"); probe != "" {
		os.Args = append([]string{"sila"}, strings.Split(probe, "\x1f")...)
		main()
		return
	}

	cases := []struct {
		name           string
		args           string // \x1f-joined tokens after "sila"
		flag           string
		wantSuggestion bool
	}{
		{"single-token-db", "--db=/tmp/definitely-not-here.db\x1fstatus", "--db=/tmp/definitely-not-here.db", true},
		{"json", "--json\x1fstatus", "--json", true},
		{"project", "--project=elsewhere\x1fstatus", "--project=elsewhere", true},
		// The two-token form's first non-flag token is the flag's value, so the
		// rejected message must not fabricate "try: sila /tmp/x --db".
		{"two-token-db", "--db\x1f/tmp/x\x1fstatus", "--db", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=TestLeadingFlagGuard")
			cmd.Env = append(os.Environ(), "SILA_LEADING_GUARD_PROBE="+tc.args)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 2 {
				t.Fatalf("expected exit 2, got %v (stderr: %q)", err, stderr.String())
			}
			if !strings.Contains(stderr.String(), tc.flag) {
				t.Errorf("stderr must name the flag %q, got %q", tc.flag, stderr.String())
			}
			if got := strings.Contains(stderr.String(), "(try:"); got != tc.wantSuggestion {
				t.Errorf("suggestion presence = %v, want %v (stderr: %q)", got, tc.wantSuggestion, stderr.String())
			}
			if tc.wantSuggestion && !strings.Contains(stderr.String(), "sila status "+tc.flag) {
				t.Errorf("stderr must show the canonical order, got %q", stderr.String())
			}
			if stdout.Len() != 0 {
				t.Errorf("the command must not run; stdout: %q", stdout.String())
			}
		})
	}
}

// TestAllowedLeadingFlags keeps the guard's allowlist honest: the flags that
// were legal before the guard keep working, including the -lp alias.
func TestAllowedLeadingFlags(t *testing.T) {
	if probe := os.Getenv("SILA_LEADING_ALLOWED_PROBE"); probe != "" {
		os.Args = strings.Split(probe, "\x1f")
		main()
		return
	}

	cases := []struct {
		name string
		args []string
	}{
		{"version", []string{"sila", "--version"}},
		{"version-short", []string{"sila", "-V"}},
		{"help", []string{"sila", "--help"}},
		{"no-color-version", []string{"sila", "--no-color", "--version"}},
		{"list-projects-alias", []string{"sila", "-lp", "--help"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=TestAllowedLeadingFlags")
			cmd.Env = append(os.Environ(), "SILA_LEADING_ALLOWED_PROBE="+strings.Join(tc.args, "\x1f"))
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("expected exit 0, got %v (stderr: %q)", err, stderr.String())
			}
			if stdout.Len() == 0 && stderr.Len() == 0 {
				t.Error("expected the allowed flag path to report something")
			}
		})
	}
}

func TestLeadingVerboseShorthandIsRejected(t *testing.T) {
	if probe := os.Getenv("SILA_LEADING_VERBOSE_PROBE"); probe != "" {
		os.Args = strings.Split(probe, "\x1f")
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestLeadingVerboseShorthandIsRejected")
	cmd.Env = append(os.Environ(), "SILA_LEADING_VERBOSE_PROBE=sila\x1f-v")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatal("sila -v must exit non-zero")
	}
	if stdout.Len() != 0 {
		t.Fatalf("sila -v wrote to stdout: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "-v means verbose") || !strings.Contains(stderr.String(), "--version") {
		t.Fatalf("missing actionable hint: %q", stderr.String())
	}
}

func TestChallenger_ScopedGoalsCLIErrorAndBlockerHandling(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "scoped_err_cli_test.db")

	ui := render.New(os.Stdout, true)

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.EnsureProject(tmp)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	// Create root and dependent goals
	runGoals(ui, dbPath, "", "antigravity", "", "", "", false, false, "", "Root Goal", "Root", "", "", 1, true, false, false, false, false, false, false, false, true, false, false, false, false, "", "", "", false, []string{p.ID, "create", "cli-root"})
	runGoals(ui, dbPath, "", "antigravity", "", "", "", false, false, "", "Dep Goal", "Dep", "cli-root", "", 2, false, false, false, false, false, false, false, false, true, false, false, false, false, "", "", "", false, []string{p.ID, "create", "cli-dep"})

	// Claim root
	runGoals(ui, dbPath, "", "antigravity", "starting root", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, true, false, false, false, false, "", "", "", false, []string{p.ID, "claim", "cli-root"})

	// Report with multiple trailing args: sila goals <project> report cli-root note part 1 note part 2
	runGoals(ui, dbPath, "", "antigravity", "", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, true, false, false, false, false, "", "", "", false, []string{p.ID, "report", "cli-root", "note", "part", "1", "note", "part", "2"})

	// Attempt claim on blocked dep goal with force=true -> should succeed
	runGoals(ui, dbPath, "", "antigravity", "force claiming", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, true, true, false, false, false, false, "", "", "", false, []string{p.ID, "claim", "cli-dep"})

	// Clean release
	runGoals(ui, dbPath, "", "", "", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, true, false, false, false, false, "", "", "", false, []string{p.ID, "release", "cli-root"})
	runGoals(ui, dbPath, "", "", "", "", "", false, false, "", "", "", "", "", 0, false, false, false, false, false, false, false, false, true, false, false, false, false, "", "", "", false, []string{p.ID, "release", "cli-dep"})
}

func TestCLIPrimeWithBudgetAndCompression(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "prime_compress_test.db")

	ui := render.New(os.Stdout, true)

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.EnsureProject(tmp)
	if err != nil {
		t.Fatal(err)
	}

	realFile := filepath.Join(tmp, "main.go")
	if err := os.WriteFile(realFile, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}

	for i := 1; i <= 6; i++ {
		_ = st.UpsertFact(store.Fact{
			ID:            filepath.Base(tmp) + fmt.Sprintf("-claim-%d", i),
			ProjectID:     p.ID,
			Claim:         fmt.Sprintf("Observed claim %d with verbose license boilerplate // Copyright 2026 Foo SPDX-License-Identifier: MIT in %s", i, realFile),
			FileRefsExist: true,
			RepoFiles:     []string{realFile},
			ReviewStatus:  store.ReviewApproved,
			DistillStatus: "pending",
		})
	}
	st.Close()

	// 1. Test runPrime with budget in human markdown mode
	runPrime(ui, dbPath, p.ID, false, false, 5, "50")

	// 2. Test runPrime with budget in JSON mode
	runPrime(ui, dbPath, p.ID, true, false, 5, "50")

	// 3. Test runPrime with budget in TOON mode
	runPrime(ui, dbPath, p.ID, false, true, 5, "50")
}

func TestRunPrimeMultiPressure(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "k.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.EnsureProject(tmp)
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.InsertMemo(p.ID, "Core invariant: deterministic compaction", "arch", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	ui := render.New(os.Stdout, true)

	// 1. Test runPrime with --pressure=watch
	runPrime(ui, dbPath, p.ID, false, false, 5, "", "1000", "watch")

	// 2. Test runPrime with --pressure=critical
	runPrime(ui, dbPath, p.ID, false, false, 5, "", "500", "critical")

	// 3. Test runPrime with JSON and max-tokens
	runPrime(ui, dbPath, p.ID, true, false, 5, "", "20000", "auto")

	// 4. Test runPrime with TOON and forced pressure
	runPrime(ui, dbPath, p.ID, false, true, 5, "", "1000", "heavy")
}

// TestEntrypointBoundary750Lines guards the architectural boundary invariant that
// CLI routing entrypoints and MCP server files stay at or below the 750-line ceiling.
func TestEntrypointBoundary750Lines(t *testing.T) {
	files := []string{
		"main.go",
		"flags.go",
		"dispatch.go",
		filepath.Join("..", "..", "internal", "mcp", "server.go"),
	}
	for _, rel := range files {
		content, err := os.ReadFile(rel)
		if err != nil {
			t.Fatalf("failed to read %s: %v", rel, err)
		}
		lines := bytes.Count(content, []byte("\n"))
		if len(content) > 0 && content[len(content)-1] != '\n' {
			lines++
		}
		if lines > 750 {
			t.Errorf("file %s has %d lines, exceeding the 750-line ceiling", rel, lines)
		}
	}
}
