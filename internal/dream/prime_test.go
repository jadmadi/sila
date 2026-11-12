package dream

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

func TestPrimeContextWithHandoffAndFacts(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "k.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if _, err := st.EnsureProject("/tmp/proj"); err != nil {
		t.Fatal(err)
	}

	// Insert a memo / fact
	_, err = st.InsertMemo("proj", "JWT auth is configured in lib/auth.ts", "architect", "", []string{"lib/auth.ts"})
	if err != nil {
		t.Fatal(err)
	}

	// Insert a handoff
	err = st.InsertHandoff(store.Handoff{
		ProjectID: "proj",
		Agent:     "claude-code",
		Branch:    "feat/tokens",
		Summary:   "Refactored token refresh mechanism",
		OpenItems: []string{"- [ ] Add unit tests for edge cases"},
		Decisions: []string{"Token TTL = 15m"},
		CreatedAt: "2026-08-15T12:00:00Z",
		Status:    "open",
	})
	if err != nil {
		t.Fatal(err)
	}

	prime, err := PrimeContext(st, "proj", "/tmp/proj", 5)
	if err != nil {
		t.Fatalf("PrimeContext failed: %v", err)
	}

	if prime.ActiveHandoff == nil || prime.ActiveHandoff.Summary != "Refactored token refresh mechanism" {
		t.Fatalf("unexpected active handoff: %+v", prime.ActiveHandoff)
	}

	if !strings.Contains(prime.Markdown, "Refactored token refresh mechanism") {
		t.Fatalf("markdown missing handoff summary: %s", prime.Markdown)
	}
	if !strings.Contains(prime.Markdown, "JWT auth is configured in lib/auth.ts") {
		t.Fatalf("markdown missing memo fact: %s", prime.Markdown)
	}
	if !strings.Contains(prime.Markdown, "Token TTL = 15m") {
		t.Fatalf("markdown missing decision: %s", prime.Markdown)
	}
}

// TestPrimeContextUsesSelectedProjectPath is the re-eval F08 probe: priming an
// explicitly selected project while running in a different checkout attached the
// caller's Git root, branch, runbook, and context files to the selected
// project's memory.
func TestPrimeContextUsesSelectedProjectPath(t *testing.T) {
	tmp := t.TempDir()

	// Two repositories: "atlas-web" (the selected project) and "sila" (the cwd).
	atlas := filepath.Join(tmp, "atlas-web")
	silaDir := filepath.Join(tmp, "sila")
	for _, d := range []string{atlas, silaDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	initGitRepo(t, atlas, "atlas-branch")
	initGitRepo(t, silaDir, "sila-branch")
	// Distinct runbooks, so which one is picked is visible in the result.
	writeRunbook(t, atlas, "go test -v ./atlas/...")
	writeRunbook(t, silaDir, "./build.sh")

	st, err := store.Open(filepath.Join(tmp, "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.EnsureProject(atlas); err != nil {
		t.Fatal(err)
	}

	// The caller works in sila's checkout but asks for atlas-web's memory.
	t.Chdir(silaDir)
	summary, err := PrimeContext(st, "atlas-web", silaDir, 5)
	if err != nil {
		t.Fatal(err)
	}
	if summary.ProjectDir != atlas {
		t.Fatalf("prime resolved project dir %q, want %q", summary.ProjectDir, atlas)
	}
	if summary.Git.RootPath != atlas {
		t.Fatalf("prime attached Git root %q, want %q", summary.Git.RootPath, atlas)
	}
	if summary.Git.Branch != "atlas-branch" {
		t.Fatalf("prime attached branch %q, want atlas-branch", summary.Git.Branch)
	}
	if summary.Runbook == nil {
		t.Fatal("prime did not find the selected project's runbook")
	}
	found := false
	for _, e := range summary.Runbook.Entries {
		if strings.Contains(e.Command, "atlas") {
			found = true
		}
		if strings.Contains(e.Command, "build.sh") {
			t.Fatalf("prime used the caller's runbook: %+v", summary.Runbook)
		}
	}
	if !found {
		t.Fatalf("prime did not load the selected project's runbook: %+v", summary.Runbook)
	}
}

// initGitRepo makes a minimal repository with one commit on the named branch.
func initGitRepo(t *testing.T, dir, branch string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-q", "-b", branch},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git unavailable or failing (%v): %s", err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "add", "-A")
	cmd.Dir = dir
	_ = cmd.Run()
	commit := exec.Command("git", "commit", "-qm", "init")
	commit.Dir = dir
	if out, err := commit.CombinedOutput(); err != nil {
		t.Skipf("git commit failed (%v): %s", err, out)
	}
}

// writeRunbook documents a command in AGENTS.md, which is where DetectRunbook
// looks for explicit commands.
func writeRunbook(t *testing.T, dir, command string) {
	t.Helper()
	body := "# AGENTS.md\n\n## Commands\n\n```bash\n" + command + "\n```\n"
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestPrimeMarkdownLabelsClaimAuthority pins the relabeling: a distilled fact
// or explicit invariant is shown under "Key Architectural Facts & Decisions",
// while a claim that merely references a real file is shown under a separate,
// honestly-named section. Before this split, raw conversational text reached
// prime labeled as verified architecture.
func TestPrimeMarkdownLabelsClaimAuthority(t *testing.T) {
	realFile := "/repo/lib/auth.ts"

	genuineFact := store.Fact{
		ID: "f1", ProjectID: "p", Claim: "raw observation",
		Distilled: "JWT auth lives in lib/auth.ts", Kind: "invariant",
		Confidence: "invariant", Corroboration: "invariant",
		ReviewStatus: store.ReviewApproved, DistillStatus: "distilled",
		RepoFiles: []string{realFile},
	}
	conversational := store.Fact{
		ID: "c1", ProjectID: "p", Claim: "Both records are now in place. Here is the action list.",
		RepoFiles: []string{realFile}, FileRefsExist: true,
		ReviewStatus: store.ReviewApproved, DistillStatus: "pending",
	}
	// A memo whose file is not on disk: authority, but IsFact() is false, so
	// only IsManualMemo() keeps it out of the claims section.
	ungroundedMemo := store.Fact{
		ID: "m1", ProjectID: "p", Claim: "Decision: ship pure-Go SQLite only",
		SessionRef: "manual_memo", ReviewStatus: store.ReviewApproved,
		DistillStatus: "distilled", Distilled: "Ship pure-Go SQLite only.",
		Confidence: "corroborated", Corroboration: "corroborated",
	}

	t.Run("claims only", func(t *testing.T) {
		md := FormatPrimeMarkdown(&PrimeSummary{
			ProjectID:   "p",
			TopFacts:    []store.Fact{conversational},
			TotalClaims: 1,
		})
		if !strings.Contains(md, "**File-Referenced Claims**") {
			t.Fatalf("claims-only briefing must name the claims section honestly:\n%s", md)
		}
		if strings.Contains(md, "Key Architectural Facts") {
			t.Fatalf("claims-only briefing must not claim to present facts:\n%s", md)
		}
	})

	t.Run("ungrounded memo stays an authority fact", func(t *testing.T) {
		md := FormatPrimeMarkdown(&PrimeSummary{
			ProjectID: "p",
			TopFacts:  []store.Fact{ungroundedMemo},
		})
		if !strings.Contains(md, "**Key Architectural Facts & Decisions**") {
			t.Fatalf("a memo a person wrote must reach the facts section:\n%s", md)
		}
		if strings.Contains(md, "File-Referenced Claims") {
			t.Fatalf("a memo is not a session claim:\n%s", md)
		}
		if !strings.Contains(md, "Ship pure-Go SQLite only.") {
			t.Fatalf("distilled memo text must be shown:\n%s", md)
		}
	})

	t.Run("facts only", func(t *testing.T) {
		md := FormatPrimeMarkdown(&PrimeSummary{
			ProjectID: "p",
			TopFacts:  []store.Fact{genuineFact},
		})
		if !strings.Contains(md, "**Key Architectural Facts & Decisions**") {
			t.Fatalf("fact briefing missing its section:\n%s", md)
		}
		if strings.Contains(md, "File-Referenced Claims") {
			t.Fatalf("fact-only briefing must not add an empty claims section:\n%s", md)
		}
		if !strings.Contains(md, "JWT auth lives in lib/auth.ts") {
			t.Fatalf("distilled text must be shown, not the raw claim:\n%s", md)
		}
	})

	t.Run("both tiers keep facts above claims", func(t *testing.T) {
		md := FormatPrimeMarkdown(&PrimeSummary{
			ProjectID: "p",
			TopFacts:  []store.Fact{genuineFact, conversational},
		})
		factIdx := strings.Index(md, "Key Architectural Facts")
		claimIdx := strings.Index(md, "File-Referenced Claims")
		if factIdx < 0 || claimIdx < 0 {
			t.Fatalf("mixed briefing must render both sections:\n%s", md)
		}
		if factIdx > claimIdx {
			t.Fatal("facts section must precede the claims section")
		}
	})
}

func TestBuildPrimeBriefWithCompression(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "k.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if _, err := st.EnsureProject("/tmp/proj"); err != nil {
		t.Fatal(err)
	}

	// Insert memo (must be preserved)
	_, err = st.InsertMemo("proj", "Core architecture invariant: pure Go CGO_ENABLED=0", "arch", "", []string{"main.go"})
	if err != nil {
		t.Fatal(err)
	}

	realFile := filepath.Join(tmp, "main.go")
	if err := os.WriteFile(realFile, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Insert several file-referenced claims with boilerplate
	for i := 1; i <= 8; i++ {
		err := st.UpsertFact(store.Fact{
			ID:            fmt.Sprintf("claim-%d", i),
			ProjectID:     "proj",
			Claim:         fmt.Sprintf("Observed claim %d: array data with license boilerplate\n// SPDX-License-Identifier: MIT\n// Copyright 2026 Test\nvar data = []string{\"item1\", \"item2\"}", i),
			FileRefsExist: true,
			RepoFiles:     []string{realFile},
			ReviewStatus:  store.ReviewApproved,
			DistillStatus: "pending",
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	// Briefing with tight budget (e.g. 50 tokens) to trigger pruning
	summary, err := BuildPrimeBrief(st, "proj", "/tmp/proj", 8, 50)
	if err != nil {
		t.Fatalf("BuildPrimeBrief failed: %v", err)
	}

	if summary.Compression == nil {
		t.Fatal("expected compression result to be populated")
	}
	if summary.Compression.PrunedTokens <= 0 {
		t.Errorf("expected pruned tokens > 0, got %d", summary.Compression.PrunedTokens)
	}
	// Verify critical memo is preserved
	if !strings.Contains(summary.Markdown, "Core architecture invariant: pure Go CGO_ENABLED=0") {
		t.Errorf("critical invariant memo must never be pruned")
	}
	if !strings.Contains(summary.Markdown, "Context Compression") {
		t.Errorf("expected Context Compression note in markdown output")
	}
}

func TestBuildPrimeBriefCustomIDDiffersFromBasename(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "k.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	repoDir := filepath.Join(tmp, "local-repo")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Registered ID differs from directory basename "local-repo"
	if _, err := st.DB.Exec(`INSERT INTO projects (id, path, created_at) VALUES (?, ?, datetime('now'))`, "custom-proj-id", repoDir); err != nil {
		t.Fatal(err)
	}

	// Insert fact and handoff under custom-proj-id
	if _, err := st.InsertMemo("custom-proj-id", "Critical security policy in place", "security", repoDir, nil); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertHandoff(store.Handoff{
		ProjectID: "custom-proj-id",
		Agent:     "antigravity",
		Summary:   "Continuity handoff for custom-proj-id",
		Status:    "open",
		CreatedAt: "2026-09-24T12:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}

	// 1. Without explicit selector (empty projectSel), should resolve to custom-proj-id and retrieve its memory
	summary, err := BuildPrimeBrief(st, "", repoDir, 5, 0)
	if err != nil {
		t.Fatalf("BuildPrimeBrief failed: %v", err)
	}
	if summary.ProjectID != "custom-proj-id" {
		t.Fatalf("expected summary.ProjectID 'custom-proj-id', got %q", summary.ProjectID)
	}
	if summary.ActiveHandoff == nil || summary.ActiveHandoff.Summary != "Continuity handoff for custom-proj-id" {
		t.Fatalf("expected handoff for custom-proj-id, got: %+v", summary.ActiveHandoff)
	}
	if len(summary.TopFacts) == 0 || !strings.Contains(summary.TopFacts[0].Claim, "Critical security policy in place") {
		t.Fatalf("expected fact for custom-proj-id, got: %+v", summary.TopFacts)
	}

	// 2. With unknown explicit selector, must fail with actionable error
	_, err = BuildPrimeBrief(st, "nonexistent-proj", repoDir, 5, 0)
	if err == nil {
		t.Fatal("expected error for nonexistent project selector, got nil")
	}
	if !strings.Contains(err.Error(), "no registered project matches") {
		t.Errorf("expected 'no registered project matches' error, got %q", err.Error())
	}
}

func TestPrimeContext_HandoffSecurityFencing(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "k.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	repoDir := filepath.Join(tmp, "fenced-repo")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnsureProject(repoDir); err != nil {
		t.Fatal(err)
	}

	// Create a real file in the project
	realFile := "config.json"
	if err := os.WriteFile(filepath.Join(repoDir, realFile), []byte(`{"env":"prod"}`), 0644); err != nil {
		t.Fatal(err)
	}

	// 1. Handoff with valid file reference
	hValid := store.Handoff{
		ProjectID: "fenced-repo",
		Agent:     "other-agent",
		Summary:   "Handoff with valid file",
		RepoFiles: []string{realFile},
		Status:    string(store.HandoffPending),
	}
	if err := st.InsertHandoff(hValid); err != nil {
		t.Fatal(err)
	}

	summary, err := BuildPrimeBrief(st, "fenced-repo", repoDir, 5, 0)
	if err != nil {
		t.Fatalf("BuildPrimeBrief failed: %v", err)
	}
	if summary.ActiveHandoff == nil {
		t.Fatal("expected active handoff")
	}
	if !summary.ActiveHandoff.FileRefsExist {
		t.Errorf("expected FileRefsExist=true for valid file, got false")
	}
	if !strings.Contains(summary.Markdown, "Inbound Handoff Baton (Untrusted Cross-Agent Context)") {
		t.Errorf("expected security notice banner in markdown output")
	}
	if !strings.Contains(summary.Markdown, "verified on disk") {
		t.Errorf("expected 'verified on disk' note for valid file refs")
	}

	// 2. Handoff with nonexistent / traversing file reference
	hInvalid := store.Handoff{
		ProjectID: "fenced-repo",
		Agent:     "untrusted-agent",
		Summary:   "Handoff with bad file",
		RepoFiles: []string{"missing.txt", "../escape.txt"},
		Status:    string(store.HandoffPending),
	}
	if err := st.InsertHandoff(hInvalid); err != nil {
		t.Fatal(err)
	}

	summary2, err := BuildPrimeBrief(st, "fenced-repo", repoDir, 5, 0)
	if err != nil {
		t.Fatalf("BuildPrimeBrief failed: %v", err)
	}
	if summary2.ActiveHandoff == nil {
		t.Fatal("expected active handoff")
	}
	if summary2.ActiveHandoff.FileRefsExist {
		t.Errorf("expected FileRefsExist=false for missing/escaping file, got true")
	}
	if !strings.Contains(summary2.Markdown, "unverified or missing on disk") {
		t.Errorf("expected 'unverified or missing on disk' note for bad file refs")
	}
}

func TestPrimeSerializedBudgetAndProjections(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "k.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	repoDir := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnsureProject(repoDir); err != nil {
		t.Fatal(err)
	}
	projID := store.ProjectID(repoDir)

	// 1. Create synthetic goals directory with 250 completed goals and 1 active goal
	goalsDir := filepath.Join(repoDir, "goals")
	if err := os.MkdirAll(goalsDir, 0755); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 250; i++ {
		slug := fmt.Sprintf("completed-goal-%03d", i)
		gDir := filepath.Join(goalsDir, slug)
		if err := os.MkdirAll(gDir, 0755); err != nil {
			t.Fatal(err)
		}
		content := fmt.Sprintf("# Goal: Completed Goal %03d\n\n- **Status**: implemented\n- **Commit**: %012x\n", i, i)
		if err := os.WriteFile(filepath.Join(gDir, "goal.md"), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		retContent := fmt.Sprintf(`{"goal_slug":"%s","status":"implemented","commit":"%012x"}`, slug, i)
		if err := os.WriteFile(filepath.Join(gDir, "retrospective.json"), []byte(retContent), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// 1 active claimed goal
	activeSlug := "active-remediation-goal"
	actDir := filepath.Join(goalsDir, activeSlug)
	if err := os.MkdirAll(actDir, 0755); err != nil {
		t.Fatal(err)
	}
	actGoalContent := "# Goal: Active Remediation Goal\n\n- **Status**: in-progress\n"
	if err := os.WriteFile(filepath.Join(actDir, "goal.md"), []byte(actGoalContent), 0644); err != nil {
		t.Fatal(err)
	}
	claimContent := `{"goal_slug":"active-remediation-goal","agent":"antigravity","claimed_by":"antigravity"}`
	if err := os.WriteFile(filepath.Join(actDir, "claim.json"), []byte(claimContent), 0644); err != nil {
		t.Fatal(err)
	}
	actContractContent := `{"command":"go test -race ./...","timeout_seconds":60}`
	if err := os.WriteFile(filepath.Join(actDir, "contract.json"), []byte(actContractContent), 0644); err != nil {
		t.Fatal(err)
	}

	// 2. Populate facts, lessons, docs, and large handoff
	for i := 1; i <= 5; i++ {
		_, err = st.InsertMemo(projID, fmt.Sprintf("Architectural Decision %d: pure Go SQLite engine", i), "architect", "", []string{"cmd/sila/main.go"})
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 5; i++ {
		_, err = st.InsertMemoWithKind(projID, fmt.Sprintf("Grounding Fact %d: isolated test runners prevent flakiness", i), "architect", "", []string{"cmd/sila/main.go"}, "fact")
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 3; i++ {
		_, err = st.InsertLesson(projID, fmt.Sprintf("Build failed with exit code 127 in step %d: PATH isolation in sandbox", i), "antigravity", "", []string{"cmd/sila/main.go"})
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 4; i++ {
		err = st.InsertDoc(store.Doc{
			ProjectID: projID,
			Title:     fmt.Sprintf("Doc Specification %d", i),
			Content:   fmt.Sprintf("Detailed engineering spec content for module %d with extensive documentation.", i),
			URL:       fmt.Sprintf("https://example.com/spec-%d", i),
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	// Large handoff
	openItems := make([]string, 12)
	for i := range openItems {
		openItems[i] = fmt.Sprintf("Task %d: verify isolated container sandbox", i+1)
	}
	decisions := make([]string, 6)
	for i := range decisions {
		decisions[i] = fmt.Sprintf("Design decision %d: Gobstopper cache-pinned architecture", i+1)
	}
	err = st.InsertHandoff(store.Handoff{
		ProjectID: projID,
		Agent:     "antigravity",
		Branch:    "fix/remediation",
		Summary:   "Comprehensive remediation pass across 17 packages, achieving full verification contracts and deterministic test isolation.",
		OpenItems: openItems,
		Decisions: decisions,
		RepoFiles: []string{"cmd/sila/main.go", "internal/dream/prime.go"},
		CreatedAt: "2026-10-02T12:00:00Z",
		Status:    "open",
	})
	if err != nil {
		t.Fatal(err)
	}

	// 3. Test Condition 1 & 2: Default Priming (aggregate count, bounded goals, canonical representation)
	defaultBrief, err := BuildPrimeBriefWithOptions(st, PrimeOptions{
		ProjectDir:      repoDir,
		ProjectSel:      projID,
		MaxFacts:        5,
		IncludeMarkdown: false,
	})
	if err != nil {
		t.Fatalf("BuildPrimeBriefWithOptions failed: %v", err)
	}

	if defaultBrief.CompletedGoalsCount != 250 {
		t.Errorf("expected CompletedGoalsCount=250, got %d", defaultBrief.CompletedGoalsCount)
	}
	if len(defaultBrief.CompletedGoals) != 5 {
		t.Errorf("expected len(CompletedGoals)=5 (bounded default), got %d", len(defaultBrief.CompletedGoals))
	}
	if defaultBrief.CompletedGoalsRoute != "sila goals list --status=implemented" {
		t.Errorf("unexpected CompletedGoalsRoute: %q", defaultBrief.CompletedGoalsRoute)
	}
	if defaultBrief.Markdown != "" {
		t.Errorf("expected Markdown to be omitted (empty) by default in structured summary, got len=%d", len(defaultBrief.Markdown))
	}

	// Verify JSON output omits "markdown" and includes bounded goals & route
	defaultJSONBytes, err := json.Marshal(defaultBrief)
	if err != nil {
		t.Fatal(err)
	}
	defaultJSONStr := string(defaultJSONBytes)
	if strings.Contains(defaultJSONStr, `"markdown":`) {
		t.Errorf("default JSON payload should NOT contain duplicated markdown field")
	}
	if !strings.Contains(defaultJSONStr, `"completed_goals_count":250`) {
		t.Errorf("default JSON payload missing completed_goals_count:250")
	}
	if !strings.Contains(defaultJSONStr, `"completed_goals_route":"sila goals list --status=implemented"`) {
		t.Errorf("default JSON payload missing completed_goals_route")
	}

	// Verify formatted markdown displays aggregate count and route
	renderedMD := FormatPrimeMarkdown(defaultBrief)
	if !strings.Contains(renderedMD, "• ... and 245 earlier completed goals (run `sila goals list --status=implemented` to view all).") {
		t.Errorf("markdown missing earlier completed goals note with route; got:\n%s", renderedMD)
	}

	// 4. Test Opt-in: AllGoals and IncludeMarkdown
	optInBrief, err := BuildPrimeBriefWithOptions(st, PrimeOptions{
		ProjectDir:      repoDir,
		ProjectSel:      projID,
		MaxFacts:        5,
		AllGoals:        true,
		IncludeMarkdown: true,
	})
	if err != nil {
		t.Fatalf("opt-in build failed: %v", err)
	}
	if len(optInBrief.CompletedGoals) != 250 {
		t.Errorf("expected len(CompletedGoals)=250 with AllGoals=true, got %d", len(optInBrief.CompletedGoals))
	}
	if optInBrief.Markdown == "" {
		t.Errorf("expected Markdown to be populated with IncludeMarkdown=true")
	}
	optInJSONBytes, _ := json.Marshal(optInBrief)
	if !strings.Contains(string(optInJSONBytes), `"markdown":`) {
		t.Errorf("opt-in JSON payload should contain markdown field")
	}
	if len(optInJSONBytes) <= len(defaultJSONBytes)*2 {
		t.Errorf("opt-in JSON (%d bytes) should be significantly larger than bounded default (%d bytes)", len(optInJSONBytes), len(defaultJSONBytes))
	}

	// 5. Test Condition 3: Estimator documentation and Budget-Too-Small
	if defaultBrief.BudgetMetrics == nil {
		t.Fatal("expected BudgetMetrics to be populated")
	}
	if defaultBrief.BudgetMetrics.Estimator != EstimatorDocumentation {
		t.Errorf("expected Estimator %q, got %q", EstimatorDocumentation, defaultBrief.BudgetMetrics.Estimator)
	}
	if defaultBrief.BudgetMetrics.ReservedMetadataTokens <= 0 {
		t.Errorf("expected ReservedMetadataTokens > 0, got %d", defaultBrief.BudgetMetrics.ReservedMetadataTokens)
	}

	// Budget too small
	tooSmallBrief, err := BuildPrimeBriefWithOptions(st, PrimeOptions{
		ProjectDir: repoDir,
		ProjectSel: projID,
		MaxTokens:  50,
	})
	if err != nil {
		t.Fatalf("unexpected error on too small budget: %v", err)
	}
	if tooSmallBrief.Error == "" || !strings.Contains(tooSmallBrief.Error, "budget_too_small") {
		t.Errorf("expected budget_too_small error, got %q", tooSmallBrief.Error)
	}
	if tooSmallBrief.BudgetMetrics == nil || !tooSmallBrief.BudgetMetrics.BudgetTooSmall {
		t.Errorf("expected BudgetMetrics.BudgetTooSmall=true")
	}
	if tooSmallBrief.BudgetMetrics.FitsBudget {
		t.Errorf("expected FitsBudget=false for too small budget")
	}

	// 6. Test Condition 4: Budget sizing (--max-tokens / --top alters size and fits budget)
	largeBudgetBrief, err := BuildPrimeBriefWithOptions(st, PrimeOptions{
		ProjectDir: repoDir,
		ProjectSel: projID,
		MaxFacts:   5,
		MaxTokens:  3000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !largeBudgetBrief.BudgetMetrics.FitsBudget {
		t.Errorf("expected FitsBudget=true for 3000 tokens budget")
	}

	smallBudgetBrief, err := BuildPrimeBriefWithOptions(st, PrimeOptions{
		ProjectDir: repoDir,
		ProjectSel: projID,
		MaxFacts:   5,
		MaxTokens:  600,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !smallBudgetBrief.BudgetMetrics.FitsBudget {
		t.Errorf("expected FitsBudget=true for 600 tokens budget (after progressive pruning), got %d tokens", smallBudgetBrief.BudgetMetrics.SerializedTotalTokens)
	}
	if smallBudgetBrief.BudgetMetrics.SerializedTotalTokens > 600 {
		t.Errorf("small budget payload (%d tokens) exceeds max_tokens 600", smallBudgetBrief.BudgetMetrics.SerializedTotalTokens)
	}
	if smallBudgetBrief.BudgetMetrics.SerializedTotalTokens >= largeBudgetBrief.BudgetMetrics.SerializedTotalTokens {
		t.Errorf("small budget tokens (%d) should be smaller than large budget (%d)", smallBudgetBrief.BudgetMetrics.SerializedTotalTokens, largeBudgetBrief.BudgetMetrics.SerializedTotalTokens)
	}

	// Top facts altering
	top2Brief, err := BuildPrimeBriefWithOptions(st, PrimeOptions{
		ProjectDir: repoDir,
		ProjectSel: projID,
		MaxFacts:   2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(top2Brief.TopFacts) != 2 {
		t.Errorf("expected 2 TopFacts, got %d", len(top2Brief.TopFacts))
	}
	top8Brief, err := BuildPrimeBriefWithOptions(st, PrimeOptions{
		ProjectDir: repoDir,
		ProjectSel: projID,
		MaxFacts:   8,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(top8Brief.TopFacts) != 8 {
		t.Errorf("expected 8 TopFacts, got %d", len(top8Brief.TopFacts))
	}

	t.Logf("=== Synthetic Warm-Read & Serialized Payload Measurements ===")
	t.Logf("Default bounded payload (5 goals, no markdown): %d bytes, %d tokens", len(defaultJSONBytes), defaultBrief.BudgetMetrics.SerializedTotalTokens)
	t.Logf("Opt-in unconstrained payload (250 goals, full markdown): %d bytes, %d tokens", len(optInJSONBytes), optInBrief.BudgetMetrics.SerializedTotalTokens)
	t.Logf("Bounded vs unconstrained payload reduction: %.1f%% reduction", (1.0-float64(len(defaultJSONBytes))/float64(len(optInJSONBytes)))*100.0)
	t.Logf("3000-token budget payload: %d tokens", largeBudgetBrief.BudgetMetrics.SerializedTotalTokens)
	t.Logf("600-token budget pruned payload: %d tokens (FitsBudget=%t)", smallBudgetBrief.BudgetMetrics.SerializedTotalTokens, smallBudgetBrief.BudgetMetrics.FitsBudget)
	t.Logf("Top-2 facts payload: %d tokens vs Top-8 facts: %d tokens", top2Brief.BudgetMetrics.SerializedTotalTokens, top8Brief.BudgetMetrics.SerializedTotalTokens)
}

func TestPrimeIncludesCasesAndPersonas(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "prime_cases.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	proj, err := st.EnsureProject(tmp)
	if err != nil {
		t.Fatal(err)
	}

	goalDir := filepath.Join(tmp, "goals", "prime-cases-demo")
	if err := os.MkdirAll(goalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	goalMD := "# Goal: Prime Cases Demo\n\n## Dependencies & Execution Order\n- **Mode**: Independent\n- **Personas**: security-sentinel\n\n## Files to Touch\n- internal/dream/prime.go\n"
	if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalMD), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "facts.md"), []byte("# Facts\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte("# Plan\n\n- [ ] Phase 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	claim := `{"goal_slug":"prime-cases-demo","agent":"tester","claimed_at":"` + time.Now().UTC().Format(time.RFC3339) + `","status":"in_progress"}`
	if err := os.WriteFile(filepath.Join(goalDir, "claim.json"), []byte(claim), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := st.SaveCase(proj.ID, "Expand MCP continuity profile", "Include all 9-verb and Tier-0 tools in ContinuityToolNames", "ContinuityToolCount == 32", "mcp", "agent", tmp, nil); err != nil {
		t.Fatal(err)
	}

	brief, err := BuildPrimeBriefWithOptions(st, PrimeOptions{
		ProjectDir:      tmp,
		ProjectSel:      proj.ID,
		MaxFacts:        5,
		IncludeMarkdown: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(brief.Cases) == 0 {
		t.Fatalf("expected Cases in prime summary, got none (markdown): %s", brief.Markdown)
	}
	md := FormatPrimeMarkdown(brief)
	if !strings.Contains(md, "**Architectural Cases (procedural patterns)**:") {
		t.Errorf("markdown missing architectural cases section:\n%s", md)
	}
	if !strings.Contains(md, "Expand MCP continuity profile") || !strings.Contains(md, "ContinuityToolCount == 32") {
		t.Errorf("markdown missing case intent/invariant:\n%s", md)
	}
	if !strings.Contains(md, "Hired Squad:") || !strings.Contains(md, "security-sentinel") {
		t.Errorf("markdown missing hired squad:\n%s", md)
	}
	if !strings.Contains(md, "Working Contract (9-Verb Memory Loop)") || !strings.Contains(md, "Capability Triggers") {
		t.Errorf("markdown missing full capability contract:\n%s", md)
	}
	for _, want := range []string{"sila search \"<query>\" --omni", "sila standup", "sila guide <domain>", "sila edit-check <symbol>"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing capability trigger %q", want)
		}
	}

	raw, err := json.Marshal(brief)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"cases":`) {
		t.Errorf("JSON summary missing cases field: %s", raw)
	}
}

func TestPrimeShedsCasesUnderTightBudget(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "prime_shed.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	proj, err := st.EnsureProject(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveCase(proj.ID, "Very long architectural case intent used to force the token shedding cascade to prune it before lessons", strings.Repeat("approach detail ", 30), strings.Repeat("invariant detail ", 30), "budget", "agent", tmp, nil); err != nil {
		t.Fatal(err)
	}

	unbounded, err := BuildPrimeBriefWithOptions(st, PrimeOptions{ProjectDir: tmp, ProjectSel: proj.ID, MaxFacts: 5})
	if err != nil {
		t.Fatal(err)
	}
	if unbounded.BudgetMetrics == nil {
		t.Fatal("missing budget metrics")
	}
	tight := unbounded.BudgetMetrics.MinimumRequiredTokens + 40
	brief, err := BuildPrimeBriefWithOptions(st, PrimeOptions{ProjectDir: tmp, ProjectSel: proj.ID, MaxFacts: 5, MaxTokens: tight})
	if err != nil {
		t.Fatal(err)
	}
	if brief.BudgetMetrics == nil || !brief.BudgetMetrics.FitsBudget {
		t.Fatalf("tight budget %d did not fit: %+v", tight, brief.BudgetMetrics)
	}
	if brief.Error != "" {
		t.Fatalf("tight budget reported error: %s", brief.Error)
	}
	if brief.Cases != nil && len(brief.Cases) > 0 && brief.BudgetMetrics.SerializedTotalTokens > tight {
		t.Fatalf("cases survived an over-budget payload: %d > %d", brief.BudgetMetrics.SerializedTotalTokens, tight)
	}

	if unbounded.BudgetMetrics.MinimumRequiredTokens < 350 {
		brief350, err := BuildPrimeBriefWithOptions(st, PrimeOptions{ProjectDir: tmp, ProjectSel: proj.ID, MaxFacts: 5, MaxTokens: 350})
		if err != nil {
			t.Fatal(err)
		}
		if brief350.BudgetMetrics == nil || !brief350.BudgetMetrics.FitsBudget {
			t.Fatalf("MaxTokens=350 did not fit: %+v", brief350.BudgetMetrics)
		}
	}
}

func TestPrimeHandoffExistingDirAndStaleBehindHEAD(t *testing.T) {
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "fs-relay")
	if err := os.MkdirAll(filepath.Join(repo, ".sila"), 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repo, "main")

	st, err := store.Open(filepath.Join(tmp, "prime_handoff.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	proj, err := st.EnsureProject(repo)
	if err != nil {
		t.Fatal(err)
	}

	if err := st.InsertHandoff(store.Handoff{
		ProjectID: proj.ID,
		Agent:     "sila-hook",
		Branch:    "main",
		Summary:   "sila-hook session handoff · branch main · HEAD e9291de · 1 changed file(s) · goal none",
		RepoFiles: []string{".sila/"},
		CreatedAt: "2026-11-02T10:58:00Z",
		Status:    "open",
	}); err != nil {
		t.Fatal(err)
	}

	brief, err := PrimeContext(st, proj.ID, repo, 5)
	if err != nil {
		t.Fatalf("PrimeContext: %v", err)
	}
	if brief.ActiveHandoff == nil {
		t.Fatal("expected active handoff")
	}
	// AC-F6-a: a handoff whose referenced path (.sila/) exists is not listed as missing.
	if !brief.ActiveHandoff.FileRefsExist {
		t.Fatalf("expected FileRefsExist=true when .sila/ exists on disk")
	}
	if strings.Contains(brief.Markdown, "unverified or missing on disk") {
		t.Fatalf("expected existing .sila/ not to be listed as missing:\n%s", brief.Markdown)
	}
	// AC-F6-b: a handoff behind HEAD carries an explicit stale marker.
	if !brief.ActiveHandoff.Stale || !strings.Contains(brief.Markdown, "[STALE:") {
		t.Fatalf("expected handoff behind HEAD to carry [STALE: ...] marker, got:\n%s", brief.Markdown)
	}
}

func setupPrimeRecallPack(t *testing.T) (*store.Store, string, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "prime_pack.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	proj, err := st.EnsureProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	rel := "scripts/deploy.sh"
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertLesson(proj.ID, store.FormatLesson("deploy ordering race", "shared state", "serialize deploy", ""), "agent", dir, []string{rel}); err != nil {
		t.Fatal(err)
	}
	return st, proj.ID, dir
}

func TestPrimeIncludesRecallPack(t *testing.T) {
	st, projectID, dir := setupPrimeRecallPack(t)
	brief, err := BuildPrimeBriefWithOptions(st, PrimeOptions{
		ProjectSel:         projectID,
		ProjectDir:         dir,
		MaxFacts:           5,
		IncludeMarkdown:    true,
		DisableCodebaseMap: true,
		Context:            &RecallContext{Task: "deploy ordering", Paths: []string{"scripts/deploy.sh"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if brief.RecallPack == nil {
		t.Fatal("prime brief has no recall pack")
	}
	if len(brief.RecallPack.Items) == 0 {
		t.Fatalf("recall pack is empty: %+v", brief.RecallPack)
	}
	if !strings.Contains(brief.Markdown, "Context Recall Pack") {
		t.Fatalf("briefing markdown is missing the pack section:\n%s", brief.Markdown)
	}
	if !strings.Contains(brief.Markdown, "untrusted, possibly stale data") {
		t.Fatalf("briefing markdown is missing the untrusted line:\n%s", brief.Markdown)
	}
}

func TestPrimePackBudgetDropOrder(t *testing.T) {
	st, projectID, dir := setupPrimeRecallPack(t)
	ctx := &RecallContext{Task: "deploy ordering", Paths: []string{"scripts/deploy.sh"}}

	generous, err := BuildPrimeBriefWithOptions(st, PrimeOptions{
		ProjectSel: projectID, ProjectDir: dir, MaxFacts: 5, MaxTokens: 8000,
		IncludeMarkdown: true, DisableCodebaseMap: true, Context: ctx,
	})
	if err != nil {
		t.Fatal(err)
	}
	if generous.RecallPack == nil {
		t.Fatal("generous budget dropped the pack")
	}
	packIdx := strings.Index(generous.Markdown, "Context Recall Pack")
	groundIdx := strings.Index(generous.Markdown, "**Disk Grounding**")
	if packIdx < 0 || groundIdx < 0 || packIdx > groundIdx {
		t.Fatalf("pack is not placed before disk grounding:\n%s", generous.Markdown)
	}

	for _, budget := range []int{700, 800, 900, 1000, 1200, 1600} {
		baseline, err := BuildPrimeBriefWithOptions(st, PrimeOptions{
			ProjectSel: projectID, ProjectDir: dir, MaxFacts: 5, MaxTokens: budget,
			IncludeMarkdown: true, DisableCodebaseMap: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		brief, err := BuildPrimeBriefWithOptions(st, PrimeOptions{
			ProjectSel: projectID, ProjectDir: dir, MaxFacts: 5, MaxTokens: budget,
			IncludeMarkdown: true, DisableCodebaseMap: true, Context: ctx,
		})
		if err != nil {
			t.Fatal(err)
		}
		if brief.BudgetMetrics == nil {
			t.Fatalf("budget %d produced no metrics", budget)
		}
		if baseline.BudgetMetrics == nil || !baseline.BudgetMetrics.FitsBudget {
			continue // pre-existing content already exceeds this budget
		}
		if !brief.BudgetMetrics.FitsBudget {
			t.Fatalf("budget %d was exceeded by a pack: %+v", budget, brief.BudgetMetrics)
		}
		if brief.BudgetMetrics.SerializedTotalTokens > budget {
			t.Fatalf("budget %d: serialized tokens = %d", budget, brief.BudgetMetrics.SerializedTotalTokens)
		}
	}
}

func TestPrimeExcludesArchived(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "prime-archive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	proj, err := st.EnsureProject(tmp)
	if err != nil {
		t.Fatal(err)
	}
	rec := archivedMemoryFixture(t, st, proj.ID, "Narwhal prime needle", "Narwhal prime needle fact")
	prime, err := BuildPrimeBriefWithOptions(st, PrimeOptions{ProjectSel: proj.ID, ProjectDir: tmp, MaxFacts: 5, IncludeMarkdown: true})
	if err != nil {
		t.Fatal(err)
	}
	if !primeContainsFact(prime.TopFacts, rec.FactID) {
		t.Fatalf("prime before archive did not include the fact: %+v", prime.TopFacts)
	}
	if _, err := st.ArchiveMemory(context.Background(), proj.ID, rec.ID, store.ArchiveAuthorityUserRequest, store.ArchiveReasonOutdated, "", "user", ""); err != nil {
		t.Fatal(err)
	}
	prime, err = BuildPrimeBriefWithOptions(st, PrimeOptions{ProjectSel: proj.ID, ProjectDir: tmp, MaxFacts: 5, IncludeMarkdown: true})
	if err != nil {
		t.Fatal(err)
	}
	if primeContainsFact(prime.TopFacts, rec.FactID) {
		t.Fatalf("prime included archived fact: %+v", prime.TopFacts)
	}
	prime, err = BuildPrimeBriefWithOptions(st, PrimeOptions{ProjectSel: proj.ID, ProjectDir: tmp, MaxFacts: 5, IncludeMarkdown: true, IncludeArchived: true})
	if err != nil {
		t.Fatal(err)
	}
	if !primeContainsFact(prime.TopFacts, rec.FactID) {
		t.Fatalf("include-archived prime did not include the fact: %+v", prime.TopFacts)
	}
}

func primeContainsFact(facts []store.Fact, id string) bool {
	for _, fact := range facts {
		if fact.ID == id {
			return true
		}
	}
	return false
}
