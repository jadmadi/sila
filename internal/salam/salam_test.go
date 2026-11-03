package salam

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/heal"
	"github.com/jadmadi/sila/internal/store"
)

func TestWiringFromPlan(t *testing.T) {
	cases := []struct {
		name       string
		plan       *heal.Plan
		wantState  string
		wantCmd    string
		wantMissed int
	}{
		{
			name:      "wired",
			plan:      &heal.Plan{},
			wantState: "wired",
		},
		{
			name:       "provision step means unwired",
			plan:       &heal.Plan{Steps: []heal.Step{{ID: "provision", Automatic: true, Files: []string{"AGENTS.md", ".agents/skills/sila/SKILL.md"}}}},
			wantState:  "unwired",
			wantCmd:    "sila heal",
			wantMissed: 2,
		},
		{
			name:      "maintenance steps are not wiring",
			plan:      &heal.Plan{Steps: []heal.Step{{ID: "prune", Automatic: true}, {ID: "stale", Automatic: true}}},
			wantState: "wired",
		},
		{
			name:      "nil plan is unknown",
			plan:      nil,
			wantState: "unknown",
			wantCmd:   "sila heal --dry-run",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := wiringFromPlan(tc.plan)
			if got.State != tc.wantState {
				t.Fatalf("state = %q, want %q", got.State, tc.wantState)
			}
			if got.NextCommand != tc.wantCmd {
				t.Fatalf("next_command = %q, want %q", got.NextCommand, tc.wantCmd)
			}
			if len(got.MissingPaths) != tc.wantMissed {
				t.Fatalf("missing paths = %d, want %d", len(got.MissingPaths), tc.wantMissed)
			}
		})
	}
}

func TestBuildBriefAndRender(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	dbPath := filepath.Join(tmp, "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	repoDir := filepath.Join(tmp, "my-repo")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatal(err)
	}
	p, err := st.EnsureProject(repoDir)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Create a ready goal
	indep := true
	if _, err := goals.CreateGoal(goals.CreateGoalParams{
		RootDir:     repoDir,
		Slug:        "salam-next-feature",
		Title:       "Salam Next Feature",
		Description: "A ready goal for tests",
		Independent: &indep,
	}); err != nil {
		t.Fatal(err)
	}

	// 2. Insert a handoff
	if err := st.InsertHandoff(store.Handoff{
		ProjectID: p.ID,
		Agent:     "antigravity",
		Summary:   "Last session completed foundational work",
		OpenItems: []string{"Add integration tests"},
		CreatedAt: "2026-09-22T10:00:00Z",
		Status:    "open",
	}); err != nil {
		t.Fatal(err)
	}

	brief, err := BuildBrief(st, repoDir, p.ID)
	if err != nil {
		t.Fatalf("BuildBrief failed: %v", err)
	}

	if brief.Project != p.ID {
		t.Errorf("expected project %q, got %q", p.ID, brief.Project)
	}
	if brief.RepoRoot != repoDir {
		t.Errorf("expected repoRoot %q, got %q", repoDir, brief.RepoRoot)
	}
	if len(brief.Loop) != 7 {
		t.Errorf("expected 7 loop steps, got %d", len(brief.Loop))
	}
	if len(brief.Ready) != 1 || brief.Ready[0].Slug != "salam-next-feature" {
		t.Errorf("expected ready goal 'salam-next-feature', got %+v", brief.Ready)
	}
	if brief.LastHandoff == nil || brief.LastHandoff.Summary != "Last session completed foundational work" {
		t.Errorf("expected handoff summary to match, got %+v", brief.LastHandoff)
	}

	// Verify assistance unconfigured by default
	if brief.Assistance.Status != "unconfigured" {
		t.Errorf("expected assistance status unconfigured, got %q", brief.Assistance.Status)
	}
	if !brief.Assistance.CanPrompt {
		t.Errorf("expected can_prompt true when unconfigured")
	}

	// Verify rendered markdown
	md := brief.Markdown
	if !strings.Contains(md, "# sila · salam — "+p.ID) {
		t.Errorf("markdown missing title: %s", md)
	}
	if !strings.Contains(md, "The 7-Step Working Contract") {
		t.Errorf("markdown missing working contract: %s", md)
	}
	if !strings.Contains(md, "Semantic Assistance (JEV)") {
		t.Errorf("markdown missing assistance section: %s", md)
	}
	if !strings.Contains(md, "salam-next-feature") {
		t.Errorf("markdown missing ready goal: %s", md)
	}
	if !strings.Contains(md, "claim_goal(slug=\"salam-next-feature\")") {
		t.Errorf("markdown missing MCP tool call advice: %s", md)
	}
	if !strings.Contains(md, "Subshell & Environment Guidance") {
		t.Errorf("markdown missing subshell advice: %s", md)
	}
}

func TestBuildBriefAssistanceActiveAndDormant(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	dbPath := filepath.Join(tmp, "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	repoDir := filepath.Join(tmp, "my-repo")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repoDir, "init", "-b", "main").CombinedOutput(); err != nil {
		t.Fatalf("initialize isolated repository: %v: %s", err, out)
	}
	ancestorConfig := filepath.Join(tmp, ".sila", "config.json")
	if err := os.MkdirAll(filepath.Dir(ancestorConfig), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ancestorConfig, []byte(`{"assist_enabled":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := st.EnsureProject(repoDir)
	if err != nil {
		t.Fatal(err)
	}

	// Set global environment for provider
	t.Setenv("SILA_ASSIST_PROVIDER", "typesafe")
	t.Setenv("TYPESAFE_API_KEY", "ts_test_key_12345678")

	// Without project config -> dormant
	briefDormant, err := BuildBrief(st, repoDir, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if briefDormant.Assistance.Status != "dormant" {
		t.Fatalf("expected status dormant, got %q", briefDormant.Assistance.Status)
	}
	if !strings.Contains(briefDormant.Markdown, "Dormant") {
		t.Errorf("markdown should mention Dormant: %s", briefDormant.Markdown)
	}

	// Write project config enabling assistance
	silaDir := filepath.Join(repoDir, ".sila")
	if err := os.MkdirAll(silaDir, 0755); err != nil {
		t.Fatal(err)
	}
	cfgJSON := `{"assist_enabled": true, "assist_scopes": ["diff", "gate"]}`
	if err := os.WriteFile(filepath.Join(silaDir, "config.json"), []byte(cfgJSON), 0644); err != nil {
		t.Fatal(err)
	}

	briefActive, err := BuildBrief(st, repoDir, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if briefActive.Assistance.Status != "active" {
		t.Fatalf("expected status active, got %q", briefActive.Assistance.Status)
	}
	if briefActive.Assistance.CanPrompt {
		t.Errorf("expected can_prompt false when already active")
	}
	if !strings.Contains(briefActive.Markdown, "Active") {
		t.Errorf("markdown should mention Active: %s", briefActive.Markdown)
	}
}

func TestBuildBriefCustomIDDiffersFromBasename(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	dbPath := filepath.Join(tmp, "test.db")
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
	if _, err := st.DB.Exec(`INSERT INTO projects (id, path, created_at) VALUES (?, ?, datetime('now'))`, "custom-service-id", repoDir); err != nil {
		t.Fatal(err)
	}

	// 1. Without explicit selector (empty projectSel), should resolve to custom-service-id
	brief, err := BuildBrief(st, repoDir, "")
	if err != nil {
		t.Fatalf("BuildBrief failed: %v", err)
	}
	if brief.Project != "custom-service-id" {
		t.Errorf("expected brief.Project 'custom-service-id', got %q", brief.Project)
	}

	// 2. With explicit unknown selector, must fail with actionable error
	_, err = BuildBrief(st, repoDir, "nonexistent-project")
	if err == nil {
		t.Fatal("expected error for nonexistent project selector, got nil")
	}
	if !strings.Contains(err.Error(), "no registered project matches") {
		t.Errorf("expected 'no registered project matches' error, got %q", err.Error())
	}
}

func TestDetectIgnoredSubdirWarning(t *testing.T) {
	parent := t.TempDir()
	if err := exec.Command("git", "-C", parent, "init").Run(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, ".gitignore"), []byte("*\n!.gitignore\n"), 0644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(parent, "telemetry")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}

	warn := DetectIgnoredSubdirWarning(parent, sub)
	if !strings.Contains(warn, "gitignored by parent repository") {
		t.Fatalf("expected gitignored subdir warning, got %q", warn)
	}
	if !strings.Contains(warn, "sila heal --init-here") {
		t.Fatalf("warning does not offer the one-step setup: %q", warn)
	}

	// Once initialized with its own .git, warning clears
	if err := exec.Command("git", "-C", sub, "init").Run(); err != nil {
		t.Fatal(err)
	}
	if got := DetectIgnoredSubdirWarning(parent, sub); got != "" {
		t.Fatalf("expected empty warning after git init, got %q", got)
	}
}

func TestBuildGlobalBrief(t *testing.T) {
	base, err := os.MkdirTemp(".", "salam-global-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(base)
	absBase, err := filepath.Abs(base)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", filepath.Join(absBase, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(absBase, "config"))

	dbPath := filepath.Join(absBase, "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	repoA := filepath.Join(absBase, "repo-alpha")
	repoB := filepath.Join(absBase, "repo-beta")
	for _, r := range []string{repoA, repoB} {
		if err := os.MkdirAll(r, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := exec.Command("git", "-C", r, "init", "-b", "main").Run(); err != nil {
			t.Fatal(err)
		}
		if _, err := st.EnsureProject(r); err != nil {
			t.Fatal(err)
		}
	}

	indep := true
	if _, err := goals.CreateGoal(goals.CreateGoalParams{
		RootDir:     repoA,
		Slug:        "ready-one",
		Title:       "Ready One",
		Description: "Test ready goal",
		Independent: &indep,
	}); err != nil {
		t.Fatal(err)
	}

	gb, err := BuildGlobalBrief(st, true)
	if err != nil {
		t.Fatalf("BuildGlobalBrief: %v", err)
	}
	if !gb.Global || gb.Scanned != 2 {
		t.Fatalf("expected global=true scanned=2, got %+v", gb)
	}
	if gb.ReadyGoalsTotal != 1 || gb.ReposWithReady != 1 {
		t.Fatalf("expected 1 ready goal across 1 repo, got %+v", gb)
	}
	if len(gb.Repositories) != 2 || gb.Repositories[0].Brief == nil {
		t.Fatalf("expected 2 repo entries with full Brief populated, got %+v", gb.Repositories)
	}
	if gb.MissingPathCounts["goals/sila-onboarding/goal.md"] != 1 {
		t.Fatalf("expected goals/sila-onboarding/goal.md missing only in repo-beta (1), got %+v", gb.MissingPathCounts)
	}
}
