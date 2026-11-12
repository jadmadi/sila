package heal

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func TestBuildPlanFindsAutomaticAndJudgmentSteps(t *testing.T) {
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "heal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnsureProject(repo); err != nil {
		t.Fatal(err)
	}

	plan, err := BuildPlan(st, repo)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Project == "" {
		t.Fatal("plan must name the project")
	}
	byID := map[string]Step{}
	for _, s := range plan.Steps {
		byID[s.ID] = s
	}
	prov, ok := byID["provision"]
	if !ok || !prov.Automatic {
		t.Fatalf("provision step = %+v, want an automatic provisioning step", byID["provision"])
	}
	handoff, ok := byID["handoff"]
	if !ok || handoff.Automatic {
		t.Fatalf("handoff step = %+v, want a judgment step", byID["handoff"])
	}
	if handoff.Command == "" {
		t.Fatal("judgment step must name its command")
	}
}

func TestBuildPlanScopesAliasPairsToThisRepo(t *testing.T) {
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "heal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	real := filepath.Join(root, "repo")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "repo-alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	otherReal := filepath.Join(root, "other")
	otherAlias := filepath.Join(root, "other-alias")
	if err := os.MkdirAll(otherReal, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(otherReal, otherAlias); err != nil {
		t.Fatal(err)
	}
	for _, p := range []struct{ path, id string }{
		{real, "repo"}, {alias, "repo-alias"}, {otherReal, "other"}, {otherAlias, "other-alias"},
	} {
		if _, err := st.DB.Exec(`INSERT INTO projects (path, id, created_at) VALUES (?,?,?)`, p.path, p.id, "2026-01-01T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}

	plan, err := BuildPlan(st, real)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.AliasPairs) != 1 {
		t.Fatalf("alias pairs = %+v, want only the pair for this repository", plan.AliasPairs)
	}
	if plan.AliasPairs[0].KeepID != "repo" || plan.AliasPairs[0].DropID != "repo-alias" {
		t.Fatalf("pair = %+v, want repo ↔ repo-alias", plan.AliasPairs[0])
	}
	found := false
	for _, s := range plan.Steps {
		if s.ID == "aliases" && s.Automatic {
			found = true
		}
	}
	if !found {
		t.Fatalf("plan missing the automatic aliases step: %+v", plan.Steps)
	}
}

func TestHeal_NodeProjectStackContainment(t *testing.T) {
	repo := t.TempDir()
	pkgJSON := `{"name": "test-node", "version": "1.0.0", "scripts": {"test": "echo test"}}`
	if err := os.WriteFile(filepath.Join(repo, "package.json"), []byte(pkgJSON), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := ApplyHeal(repo)
	if err != nil {
		t.Fatalf("ApplyHeal failed: %v", err)
	}

	agentsFile := filepath.Join(repo, "AGENTS.md")
	data, err := os.ReadFile(agentsFile)
	if err != nil {
		t.Fatalf("expected AGENTS.md to be created: %v", err)
	}
	content := string(data)

	// Invariants: non-Go project must NOT contain Go maintainer mandates
	bannedPhrases := []string{
		"CGO_ENABLED=0",
		"modernc.org/sqlite",
		"Pure-Go SQLite",
		"written in Go",
		"No Python/Bash automation",
	}
	for _, phrase := range bannedPhrases {
		if strings.Contains(content, phrase) {
			t.Errorf("AGENTS.md contained Go-specific directive %q in a Node project", phrase)
		}
	}

	// Must contain Node-specific standards and Sila trigger contract
	if !strings.Contains(content, "Stack-Specific Standards (Node)") {
		t.Errorf("expected Stack-Specific Standards (Node) in AGENTS.md, got:\n%s", content)
	}
	if !strings.Contains(content, "## Sila Trigger Contract") {
		t.Errorf("expected Sila Trigger Contract in AGENTS.md")
	}
}

func TestHeal_AstroProjectStackContainment(t *testing.T) {
	repo := t.TempDir()
	pkgJSON := `{"name": "test-astro", "dependencies": {"astro": "^4.0.0"}}`
	if err := os.WriteFile(filepath.Join(repo, "package.json"), []byte(pkgJSON), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "astro.config.mjs"), []byte("export default {}"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := ApplyHeal(repo)
	if err != nil {
		t.Fatalf("ApplyHeal failed: %v", err)
	}

	agentsFile := filepath.Join(repo, "AGENTS.md")
	data, err := os.ReadFile(agentsFile)
	if err != nil {
		t.Fatalf("expected AGENTS.md to be created: %v", err)
	}
	content := string(data)

	if strings.Contains(content, "CGO_ENABLED=0") {
		t.Errorf("AGENTS.md contained CGO_ENABLED=0 in an Astro project")
	}
	if !strings.Contains(content, "Stack-Specific Standards (Astro)") {
		t.Errorf("expected Stack-Specific Standards (Astro) in AGENTS.md, got:\n%s", content)
	}
	if !strings.Contains(content, "Static-First Island Architecture") {
		t.Errorf("expected Static-First Island Architecture in Astro AGENTS.md")
	}
}

func TestHeal_NoUnrequestedGeminiFile(t *testing.T) {
	repo := t.TempDir()
	pkgJSON := `{"name": "test-clean", "version": "1.0.0"}`
	if err := os.WriteFile(filepath.Join(repo, "package.json"), []byte(pkgJSON), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := ApplyHeal(repo)
	if err != nil {
		t.Fatalf("ApplyHeal failed: %v", err)
	}

	geminiFile := filepath.Join(repo, "GEMINI.md")
	if _, err := os.Lstat(geminiFile); err == nil {
		t.Errorf("expected GEMINI.md NOT to be created in a clean repo without Gemini harness")
	}

	// When .gemini directory is present, heal should recognize the harness and manage GEMINI.md
	if err := os.MkdirAll(filepath.Join(repo, ".gemini"), 0755); err != nil {
		t.Fatal(err)
	}
	_, err = ApplyHeal(repo)
	if err != nil {
		t.Fatalf("ApplyHeal failed after adding .gemini: %v", err)
	}
	if _, err := os.Lstat(geminiFile); err != nil {
		t.Errorf("expected GEMINI.md to be created once .gemini directory exists")
	}
}

func TestHealDryRun_IncludesGitIgnoreWhenMissing(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if hasGitIgnoreHygiene(repo) {
		t.Fatal("expected hasGitIgnoreHygiene=false before .gitignore exists")
	}

	plan, err := BuildPlan(nil, repo)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	foundGitIgnore := false
	for _, s := range plan.Steps {
		for _, f := range s.Files {
			if f == ".gitignore" {
				foundGitIgnore = true
			}
		}
	}
	if !foundGitIgnore {
		t.Fatalf("expected BuildPlan to list .gitignore in step files for unprovisioned git repo, got steps=%+v", plan.Steps)
	}
	if _, err := os.Stat(filepath.Join(repo, ".gitignore")); !os.IsNotExist(err) {
		t.Fatalf("expected BuildPlan (dry-run) not to create .gitignore on disk")
	}
}

func TestInspectMCPFreshnessDoesNotRequireStoreDoctor(t *testing.T) {
	freshness, check := store.InspectMCPFreshness()
	if freshness == nil {
		t.Fatal("expected non-nil MCPFreshnessReport")
	}
	if check != nil {
		if check.Category != "Runtime" || check.Name != "Running MCP Servers" {
			t.Fatalf("unexpected check metadata: %+v", check)
		}
		if check.Status != store.StatusPass && check.Status != store.StatusWarn {
			t.Fatalf("unexpected check status: %q", check.Status)
		}
	}
}

func TestHealDryRunMatchesAppliedPathsAndLeavesCleanRepo(t *testing.T) {
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.name", "Test"},
		{"config", "user.email", "test@example.com"},
	} {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# Demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add", "README.md"},
		{"commit", "-m", "chore: initial commit"},
	} {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	// Dry run via PlanHeal (BuildPlan)
	plan, err := PlanHeal(repo)
	if err != nil {
		t.Fatalf("PlanHeal: %v", err)
	}

	// AC-F5-a: after a dry run on a clean repo, git status --porcelain is empty.
	statusOut, err := exec.Command("git", "-C", repo, "status", "--porcelain").Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(statusOut)) != "" {
		t.Fatalf("expected clean git status after dry run, got:\n%s", statusOut)
	}

	var dryRunPaths []string
	seen := map[string]bool{}
	for _, s := range plan.Steps {
		if !s.Automatic {
			continue
		}
		for _, f := range s.Files {
			clean := filepath.ToSlash(filepath.Clean(f))
			if !seen[clean] {
				seen[clean] = true
				dryRunPaths = append(dryRunPaths, clean)
			}
		}
	}
	sort.Strings(dryRunPaths)

	// Applied run
	if _, err := ApplyHeal(repo); err != nil {
		t.Fatalf("ApplyHeal: %v", err)
	}
	appliedOut, err := exec.Command("git", "-C", repo, "status", "--porcelain", "-uall").Output()
	if err != nil {
		t.Fatal(err)
	}
	var appliedPaths []string
	for _, line := range strings.Split(strings.TrimSpace(string(appliedOut)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			appliedPaths = append(appliedPaths, filepath.ToSlash(filepath.Clean(fields[len(fields)-1])))
		}
	}
	sort.Strings(appliedPaths)

	// AC-F5-b: the dry-run path list equals the paths the applied run creates.
	if !slices.Equal(dryRunPaths, appliedPaths) {
		t.Fatalf("dry-run paths %v != applied paths %v", dryRunPaths, appliedPaths)
	}
}

func TestBuildPlanSkipsOnboardingGoalWhenTrackedGoalsExist(t *testing.T) {
	repo := t.TempDir()
	existingDir := filepath.Join(repo, "goals", "existing-feature")
	if err := os.MkdirAll(existingDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(existingDir, "goal.md"), []byte("# Goal: Existing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := PlanHeal(repo)
	if err != nil {
		t.Fatalf("PlanHeal: %v", err)
	}
	for _, s := range plan.Steps {
		for _, f := range s.Files {
			if strings.Contains(f, "goals/sila-onboarding") {
				t.Fatalf("expected BuildPlan not to list goals/sila-onboarding when tracked goals exist, got %v", s.Files)
			}
		}
	}
}

func TestNeedsCFGitFilterSkipsNonGitDirWithGitattributes(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "wrangler.jsonc"), []byte(`{"name":"demo"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if !NeedsCFGitFilter(dir) {
		t.Fatal("expected NeedsCFGitFilter=true before .gitattributes exists")
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte("wrangler.jsonc filter=cfclean\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if NeedsCFGitFilter(dir) {
		t.Fatal("expected NeedsCFGitFilter=false in non-git directory once .gitattributes has filter=cfclean")
	}
}
