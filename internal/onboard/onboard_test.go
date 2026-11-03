package onboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func TestProbeInventory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".brain"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "brain.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "TODO.md"), []byte("- [ ] ship the thing\n- [x] done thing\n- [ ] second thing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("# Rules\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	goalDir := filepath.Join(root, "goals", "one-goal")
	if err := os.MkdirAll(goalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"goal.md":  "# Goal: One\n\n## Goal Description\nD\n",
		"plan.md":  "- [ ] step\n",
		"facts.md": "facts\n",
	} {
		if err := os.WriteFile(filepath.Join(goalDir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "goals", "_program"), 0o755); err != nil {
		t.Fatal(err)
	}

	rep, err := Probe(root)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	detailByKind := map[string]string{}
	for _, s := range rep.Surfaces {
		kinds = append(kinds, s.Kind)
		detailByKind[s.Kind] = s.Detail
	}
	for _, want := range []string{"brain", "tasks", "goals", "scaffold", "instructions"} {
		found := false
		for _, k := range kinds {
			if k == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("inventory missing %q: %+v", want, rep.Surfaces)
		}
	}
	if !strings.Contains(detailByKind["tasks"], "2 open") {
		t.Fatalf("task detail = %q, want 2 open checkboxes", detailByKind["tasks"])
	}
	if rep.Unmanaged < 3 {
		t.Fatalf("unmanaged = %d, want brain + tasks + instructions at least", rep.Unmanaged)
	}
}

func TestCheckReportsCriticalDuplicate(t *testing.T) {
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "check.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	proj, err := st.EnsureProject(repo)
	if err != nil {
		t.Fatal(err)
	}

	rep := Check(st, repo, proj.ID, true)
	if !rep.Ready {
		t.Fatalf("clean repo should be ready: %+v", rep.Items)
	}

	// Simulate a pre-fix duplicate registration through a symlink alias.
	alias := filepath.Join(root, "repo-alias")
	if err := os.Symlink(repo, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`INSERT INTO projects (path, id, created_at) VALUES (?,?,?)`, alias, "repo-alias-legacy", "2026-01-02T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	dup := Check(st, repo, proj.ID, true)
	if dup.Ready {
		t.Fatal("duplicate registration must fail the check")
	}
	foundCritical := false
	for _, item := range dup.Items {
		if item.Name == "Canonical project id" && item.Status == "fail" && item.Critical {
			foundCritical = true
			if !strings.Contains(item.Fix, "sila doctor --repair") {
				t.Fatalf("duplicate fix = %q, want doctor --repair", item.Fix)
			}
		}
	}
	if !foundCritical {
		t.Fatalf("no critical duplicate item: %+v", dup.Items)
	}
}

func TestScaffoldAndConvert(t *testing.T) {
	root := t.TempDir()
	dir, err := ScaffoldGoalPackage(root)
	if err != nil || dir == "" {
		t.Fatalf("scaffold: dir=%q err=%v", dir, err)
	}
	if again, err := ScaffoldGoalPackage(root); err != nil || again != "" {
		t.Fatalf("second scaffold must be a no-op, got dir=%q err=%v", again, err)
	}
	for _, name := range []string{"goal.md", "plan.md", "facts.md"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("scaffolded package missing %s: %v", name, err)
		}
	}

	if err := os.WriteFile(filepath.Join(root, "TODO.md"), []byte("- [ ] alpha\n- [ ] beta\n- [x] done\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	proposal, err := ProposeTaskConversion(root, "TODO.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(proposal.Tasks) != 2 {
		t.Fatalf("tasks = %v, want 2 open", proposal.Tasks)
	}
	written, err := WriteConvertedGoal(root, proposal)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := os.ReadFile(filepath.Join(written, "plan.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(plan), "- [ ] alpha") || !strings.Contains(string(plan), "- [ ] beta") {
		t.Fatalf("converted plan missing tasks:\n%s", plan)
	}
	facts, _ := os.ReadFile(filepath.Join(written, "facts.md"))
	if !strings.Contains(string(facts), "TODO.md") {
		t.Fatalf("converted facts must name the source file:\n%s", facts)
	}
}

// sila installs its Go discipline hook only in its own source repository; any
// other project keeps its own hooks and must not be nagged about one.
func TestCheckGitHookPolicy(t *testing.T) {
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "hooks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	project := filepath.Join(root, "project")
	if err := os.MkdirAll(filepath.Join(project, ".git", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnsureProject(project); err != nil {
		t.Fatal(err)
	}

	rep := Check(st, project, "", false)
	var status string
	for _, item := range rep.Items {
		if item.Name == "Git hook" {
			status = item.Status
		}
	}
	if status != "pass" {
		t.Fatalf("non-sila repo without a hook: Git hook = %q, want pass (project-managed)", status)
	}

	if err := os.WriteFile(filepath.Join(project, "go.mod"), []byte("module github.com/jadmadi/sila\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep = Check(st, project, "", false)
	for _, item := range rep.Items {
		if item.Name == "Git hook" {
			status = item.Status
		}
	}
	if status != "warn" {
		t.Fatalf("sila source repo without a hook: Git hook = %q, want warn", status)
	}
}

func TestDetectProjectStack_PolyglotNoGo(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "pyproject.toml"), []byte("[project]\nname=\"demo\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Cargo.toml"), []byte("[package]\nname=\"demo\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	info := DetectProjectStack(root)
	if info.Primary != "polyglot-no-go" {
		t.Errorf("expected Primary == 'polyglot-no-go', got %q", info.Primary)
	}
}

func TestRegisterZedMCPServerToleratesJSONC(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	content := `// Zed settings
{
  "theme": "dark",
  "context_servers": {
    "foreign": {"command": "foreign-mcp",},
  },
}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RegisterZedMCPServer(path); err != nil {
		t.Fatalf("JSONC with trailing commas must parse: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("rewritten settings must be valid JSON: %v\n%s", err, data)
	}
	if root["theme"] != "dark" {
		t.Fatalf("theme lost: %#v", root)
	}
	servers, ok := root["context_servers"].(map[string]any)
	if !ok || servers["foreign"] == nil || servers["sila"] == nil {
		t.Fatalf("context_servers not merged: %#v", root["context_servers"])
	}
	if err := RegisterZedMCPServer(path); err != nil {
		t.Fatalf("second call must be idempotent: %v", err)
	}
}

func TestProvision_DoesNotCreateCursorMCPServerUnprompted(t *testing.T) {
	root := t.TempDir()
	res := Provision(root, "test-clean")
	if len(res.Errors) > 0 {
		t.Fatalf("unexpected errors from Provision: %v", res.Errors)
	}

	for _, cfg := range res.MCPConfigs {
		if strings.Contains(cfg, ".cursor") {
			t.Errorf("res.MCPConfigs unexpectedly contains cursor: %v", res.MCPConfigs)
		}
	}

	for _, unprompted := range []string{".cursor", "CLAUDE.md", "GEMINI.md", ".devin"} {
		p := filepath.Join(root, unprompted)
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("unprompted path %q should not exist in clean repo: %v", unprompted, err)
		}
	}
}

func TestProvision_RegistersCursorMCPServerWhenDirectoryExists(t *testing.T) {
	root := t.TempDir()
	cursorDir := filepath.Join(root, ".cursor")
	if err := os.MkdirAll(cursorDir, 0755); err != nil {
		t.Fatal(err)
	}

	res := Provision(root, "test-cursor")
	if len(res.Errors) > 0 {
		t.Fatalf("unexpected errors from Provision: %v", res.Errors)
	}

	foundCursorMCP := false
	for _, cfg := range res.MCPConfigs {
		if cfg == ".cursor/mcp.json" {
			foundCursorMCP = true
		}
	}
	if !foundCursorMCP {
		t.Errorf("expected .cursor/mcp.json in res.MCPConfigs, got: %v", res.MCPConfigs)
	}

	mcpPath := filepath.Join(cursorDir, "mcp.json")
	if _, err := os.Stat(mcpPath); err != nil {
		t.Errorf("expected %s to exist: %v", mcpPath, err)
	}
}

func TestScaffoldGoalPackage_GitVsNonGitRoot(t *testing.T) {
	// Non-git root: condition 5 omitted
	nonGitRoot := t.TempDir()
	dir1, err := ScaffoldGoalPackage(nonGitRoot)
	if err != nil || dir1 == "" {
		t.Fatalf("ScaffoldGoalPackage non-git: %v", err)
	}
	goalContent1, err := os.ReadFile(filepath.Join(dir1, "goal.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(goalContent1), "5. Goal closed via commit containing") {
		t.Errorf("expected Done Condition 5 omitted for non-git root, got:\n%s", string(goalContent1))
	}
	planContent1, err := os.ReadFile(filepath.Join(dir1, "plan.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(planContent1), "- Commit and run `sila goals`.") {
		t.Errorf("expected plan not to demand git commit for non-git root, got:\n%s", string(planContent1))
	}

	// Git root: condition 5 included
	gitRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(gitRoot, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	dir2, err := ScaffoldGoalPackage(gitRoot)
	if err != nil || dir2 == "" {
		t.Fatalf("ScaffoldGoalPackage git: %v", err)
	}
	goalContent2, err := os.ReadFile(filepath.Join(dir2, "goal.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(goalContent2), "5. Goal closed via commit containing `(goals/sila-onboarding/goal.md)`.") {
		t.Errorf("expected Done Condition 5 included for git root, got:\n%s", string(goalContent2))
	}
	if !strings.Contains(string(goalContent2), "- **Memory Protocol**: required") {
		t.Errorf("expected Memory Protocol: required in scaffolded goal.md, got:\n%s", string(goalContent2))
	}
	planContent2, err := os.ReadFile(filepath.Join(dir2, "plan.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(planContent2), "- Commit and run `sila goals`.") {
		t.Errorf("expected plan to include git commit for git root, got:\n%s", string(planContent2))
	}
}

func TestProvision_NonGitRootNote(t *testing.T) {
	root := t.TempDir()
	res := Provision(root, "test-non-git")
	foundNote := false
	for _, n := range res.Notes {
		if strings.Contains(n, "non-git workspace root detected") {
			foundNote = true
			break
		}
	}
	if !foundNote {
		t.Errorf("expected non-git note in res.Notes, got: %v", res.Notes)
	}
}

func TestScaffoldGoalPackage_SkipsWhenTrackedGoalsOrMarkerExist(t *testing.T) {
	rootWithGoal := t.TempDir()
	existingDir := filepath.Join(rootWithGoal, "goals", "existing-feature")
	if err := os.MkdirAll(existingDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(existingDir, "goal.md"), []byte("# Goal: Existing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir, err := ScaffoldGoalPackage(rootWithGoal)
	if err != nil {
		t.Fatalf("ScaffoldGoalPackage: %v", err)
	}
	if dir != "" {
		t.Fatalf("expected empty dir when tracked goal already exists, got %q", dir)
	}
	if _, err := os.Stat(filepath.Join(rootWithGoal, "goals", "sila-onboarding")); !os.IsNotExist(err) {
		t.Fatalf("expected sila-onboarding not to be created when tracked goal exists")
	}

	rootNoGoals := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootNoGoals, ".sila-no-goals"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	dir2, err := ScaffoldGoalPackage(rootNoGoals)
	if err != nil || dir2 != "" {
		t.Fatalf("expected skip when .sila-no-goals exists, got dir=%q err=%v", dir2, err)
	}
}

func TestCheckUserScopeSkillFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	userSkill := filepath.Join(home, ".agents", "skills", "sila", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(userSkill), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userSkill, []byte("# Sila Skill\nsila standup\nsila case\nsila persona\nsila edit-check\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	rep := Check(nil, repo, "repo", false)
	foundSkillPass := false
	for _, item := range rep.Items {
		if item.Name == "Sila skill" && item.Status == "pass" && strings.Contains(item.Detail, "~/.agents/skills/sila/SKILL.md") {
			foundSkillPass = true
		}
	}
	if !foundSkillPass {
		t.Fatalf("expected Sila skill to pass via user-scope fallback, got items: %+v", rep.Items)
	}
}
