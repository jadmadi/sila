package goals

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDocumentationOnlyCommitFiltering(t *testing.T) {
	// docs, test, and goal scaffolding/registration should be ignored as documentation-only / non-closure
	for _, subject := range []string{
		"docs: update readme",
		"docs(goals): sync roadmap",
		"test: add unit test",
		"chore(goals): finalize goal package",
		"chore(goals): register goal",
		"chore: scaffold new goal",
	} {
		if !documentationOnlyCommit(subject) {
			t.Errorf("expected %q commit to be documentation-only", subject)
		}
	}

	// feat, fix, refactor, perf, and operational chore must NOT be documentation-only
	for _, subject := range []string{
		"feat: implement feature",
		"fix: resolve bug",
		"refactor: extract helper",
		"perf: optimize hot path",
		"chore: bump dependencies",
		"chore(deps): update vite",
		"chore: clean up cache",
	} {
		if documentationOnlyCommit(subject) {
			t.Errorf("expected %q commit to NOT be documentation-only", subject)
		}
	}

	// registration and scaffolding commits (docs and chore) should be ignored as documentation-only
	scaffoldCases := []string{
		"chore(goals): register goal package",
		"chore(goals): scaffold goal package",
		"chore: register new goal",
		"chore: scaffold new goal",
		"chore(goal): register new goal",
		"chore(goal): scaffold new goal",
		"chore(register): add goal definition",
		"chore(scaffold): initialize goal files",
		"chore(goals): sync goal registry for goal-x",
		"docs(goals): register new goal",
	}
	for _, sc := range scaffoldCases {
		if !documentationOnlyCommit(sc) {
			t.Errorf("expected %q to be documentation-only / scaffolding", sc)
		}
	}
}

func TestIsConventionalClosureSubject(t *testing.T) {
	valid := []string{
		"feat: add feature",
		"feat(scope): add feature",
		"feat!: breaking feature",
		"fix: resolve issue",
		"fix(api): handle nil",
		"refactor: split package",
		"perf: reduce memory",
		"chore: update dependencies",
		"chore(deps): bump vite",
		"chore!: breaking maintenance",
	}
	for _, s := range valid {
		if !isConventionalClosureSubject(s) {
			t.Errorf("expected %q to be a conventional closure subject", s)
		}
	}

	invalid := []string{
		"docs: update docs",
		"test: add tests",
		"wip: in progress",
		"ci: update pipeline",
		"style: formatting",
	}
	for _, s := range invalid {
		if isConventionalClosureSubject(s) {
			t.Errorf("expected %q to NOT be a conventional closure subject", s)
		}
	}
}

func TestBracketGoalCommitAttribution(t *testing.T) {
	dir := t.TempDir()
	slugs := []string{"bracket-feature", "bracket-doc", "bracket-registration"}
	for _, slug := range slugs {
		goalDir := filepath.Join(dir, "goals", slug)
		if err := os.MkdirAll(goalDir, 0755); err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte("# Goal: "+slug+"\n"), 0644)
		_ = os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte("# Plan\n- [x] Complete\n"), 0644)
		_ = os.WriteFile(filepath.Join(goalDir, "facts.md"), []byte("# Facts\n"), 0644)
	}

	git(t, dir, "init", "-q")
	git(t, dir, "config", "user.email", "test@test.com")
	git(t, dir, "config", "user.name", "Tester")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "feat: implement bracket-feature [goal: bracket-feature]")
	git(t, dir, "commit", "--allow-empty", "-qm", "docs: explain bracket-doc [goal: bracket-doc]")
	git(t, dir, "commit", "--allow-empty", "-qm", "chore(goals): register bracket-registration [goal: bracket-registration]")

	res, err := ScanGoals(dir)
	if err != nil {
		t.Fatal(err)
	}
	status := make(map[string]string)
	for _, g := range res.Goals {
		status[g.Slug] = g.Status
	}

	if status["bracket-feature"] != "implemented" {
		t.Errorf("expected bracket-feature implemented via [goal: ...], got %q", status["bracket-feature"])
	}
	if status["bracket-doc"] == "implemented" {
		t.Errorf("docs: commit must not close bracket-doc via [goal: ...]")
	}
	if status["bracket-registration"] == "implemented" {
		t.Errorf("chore(goals): commit must not close bracket-registration via [goal: ...]")
	}
}

func TestContainsSlugProse(t *testing.T) {
	cases := []struct {
		text string
		slug string
		want bool
	}{
		{"docs(spec): finalize markdown-for-agents", "markdown-for-agents", true},
		{"finalize markdown-for-agents in prose", "markdown-for-agents", true},
		{"markdown-for-agents-extended is different", "markdown-for-agents", false},
		{"prefix-markdown-for-agents is different", "markdown-for-agents", false},
		{"mentions 'markdown-for-agents'", "markdown-for-agents", true},
		{"(markdown-for-agents)", "markdown-for-agents", true},
		{"unrelated commit message", "markdown-for-agents", false},
		{"", "markdown-for-agents", false},
		{"some text", "", false},
	}

	for _, c := range cases {
		got := containsSlugProse(c.text, c.slug)
		if got != c.want {
			t.Errorf("containsSlugProse(%q, %q) = %v; want %v", c.text, c.slug, got, c.want)
		}
	}
}

func TestScanGoalsNearMissDetection(t *testing.T) {
	dir := t.TempDir()
	goalDir := filepath.Join(dir, "goals", "my-feature")
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"goal.md", "plan.md", "facts.md"} {
		if err := os.WriteFile(filepath.Join(goalDir, name), []byte("# Goal: My Feature\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	git(t, dir, "init", "-q")
	git(t, dir, "config", "user.email", "test@test.com")
	git(t, dir, "config", "user.name", "Tester")
	git(t, dir, "add", ".")
	// Commit with prose mention of my-feature, but no trailer or goal.md path
	git(t, dir, "commit", "-qm", "docs(spec): draft spec for my-feature in progress")

	res, err := ScanGoals(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Goals) != 1 {
		t.Fatalf("expected 1 goal, got %d", len(res.Goals))
	}
	g := res.Goals[0]
	if g.Status == "implemented" {
		t.Fatalf("goal should not be implemented from near-miss commit")
	}
	if len(g.NearMisses) == 0 {
		t.Fatalf("expected at least 1 near-miss candidate, got 0")
	}

	// Now commit with chore(goals) and trailer to certify
	git(t, dir, "commit", "--allow-empty", "-qm", "chore(goals): finalize my-feature package\n\nFixes-Goal: my-feature")
	res, err = ScanGoals(dir)
	if err != nil {
		t.Fatal(err)
	}
	g = res.Goals[0]
	if g.Status != "implemented" {
		t.Fatalf("goal should be implemented by chore(goals) commit, got status %q", g.Status)
	}
	if len(g.NearMisses) != 0 {
		t.Fatalf("expected near misses to be cleared on implemented goal, got %+v", g.NearMisses)
	}
}

func TestExplicitTrailerOverridesDocumentationOnly(t *testing.T) {
	dir := t.TempDir()
	for _, slug := range []string{"doc-goal", "test-goal", "plain-doc-goal"} {
		goalDir := filepath.Join(dir, "goals", slug)
		if err := os.MkdirAll(goalDir, 0755); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"goal.md", "plan.md", "facts.md"} {
			if err := os.WriteFile(filepath.Join(goalDir, name), []byte("# Goal: "+slug+"\n"), 0644); err != nil {
				t.Fatal(err)
			}
		}
	}

	git(t, dir, "init", "-q")
	git(t, dir, "config", "user.email", "test@test.com")
	git(t, dir, "config", "user.name", "Tester")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "initial commit")

	// 1. Commit with docs: subject and path reference (no explicit trailer) -> MUST be skipped
	git(t, dir, "commit", "--allow-empty", "-qm", "docs: update guide (goals/plain-doc-goal/goal.md)")

	// 2. Commit with docs: subject and Fixes-Goal: trailer -> MUST be honored
	git(t, dir, "commit", "--allow-empty", "-qm", "docs(guide): write documentation\n\nFixes-Goal: doc-goal")

	// 3. Commit with test: subject and Sila-Goal: trailer -> MUST be honored
	git(t, dir, "commit", "--allow-empty", "-qm", "test(unit): add unit tests\n\nSila-Goal: test-goal")

	res, err := ScanGoals(dir)
	if err != nil {
		t.Fatal(err)
	}

	goalMap := make(map[string]Goal)
	for _, g := range res.Goals {
		goalMap[g.Slug] = g
	}

	// plain-doc-goal must NOT be implemented
	if g, ok := goalMap["plain-doc-goal"]; !ok || g.Status == "implemented" {
		t.Errorf("plain-doc-goal should NOT be implemented (was doc-only without trailer), got status: %v", g.Status)
	}

	// doc-goal MUST be implemented due to Fixes-Goal trailer
	if g, ok := goalMap["doc-goal"]; !ok || g.Status != "implemented" {
		t.Errorf("doc-goal should be implemented via Fixes-Goal trailer, got status: %v", g.Status)
	}

	// test-goal MUST be implemented due to Sila-Goal trailer
	if g, ok := goalMap["test-goal"]; !ok || g.Status != "implemented" {
		t.Errorf("test-goal should be implemented via Sila-Goal trailer, got status: %v", g.Status)
	}
}

func TestScaffoldAndRegistrationCommitsDoNotCloseGoal(t *testing.T) {
	dir := t.TempDir()
	slug := "test-scaffold-defense"
	goalDir := filepath.Join(dir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"goal.md", "plan.md", "facts.md"} {
		if err := os.WriteFile(filepath.Join(goalDir, name), []byte("# Goal: "+slug+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	git(t, dir, "init", "-q")
	git(t, dir, "config", "user.email", "test@test.com")
	git(t, dir, "config", "user.name", "Tester")
	git(t, dir, "add", ".")

	// 1. docs registration commit with goal.md path reference - must NOT close goal
	git(t, dir, "commit", "-qm", "docs(goals): register "+slug+" (goals/"+slug+"/goal.md)")
	res, err := ScanGoals(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Goals) != 1 || res.Goals[0].Status == "implemented" {
		t.Fatalf("goal should NOT be implemented after docs registration commit, got status: %v", res.Goals[0].Status)
	}

	// 2. chore(goals) registration commit with goal.md path reference - must NOT close goal
	git(t, dir, "commit", "--allow-empty", "-qm", "chore(goals): register "+slug+" (goals/"+slug+"/goal.md)")
	res, err = ScanGoals(dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Goals[0].Status == "implemented" {
		t.Fatalf("goal should NOT be implemented after chore(goals) registration commit, got status: %v", res.Goals[0].Status)
	}

	// 3. chore scaffolding commit with goal.md path reference - must NOT close goal
	git(t, dir, "commit", "--allow-empty", "-qm", "chore: scaffold "+slug+" (goals/"+slug+"/goal.md)")
	res, err = ScanGoals(dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Goals[0].Status == "implemented" {
		t.Fatalf("goal should NOT be implemented after chore scaffold commit, got status: %v", res.Goals[0].Status)
	}

	// 3b. general operational chore commit with goal.md path reference - MUST close goal
	git(t, dir, "commit", "--allow-empty", "-qm", "chore(cleanup): update dependencies for "+slug+" (goals/"+slug+"/goal.md)")
	res, err = ScanGoals(dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Goals[0].Status != "implemented" {
		t.Fatalf("goal MUST be implemented after operational chore commit, got status: %v", res.Goals[0].Status)
	}
}
