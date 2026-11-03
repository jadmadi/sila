package release

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestClassifyCommit(t *testing.T) {
	cases := []struct {
		subject    string
		body       string
		wantCat    ChangelogCategory
		wantSlug   string
		wantPrefix string
		wantScope  string
	}{
		{
			subject: "feat: add new command for ADR management (goals/living-adr-engine/goal.md)",
			body:    "",
			wantCat: CatAdded, wantSlug: "living-adr-engine", wantPrefix: "feat",
		},
		{
			subject: "fix(adr): ground doctor against project root (goals/living-adr-engine/goal.md)",
			body:    "",
			wantCat: CatFixed, wantSlug: "living-adr-engine", wantPrefix: "fix", wantScope: "adr",
		},
		{
			subject: "perf: optimize SQLite query planner",
			body:    "",
			wantCat: CatChanged, wantPrefix: "perf",
		},
		{
			subject: "refactor: extract rendering helpers",
			body:    "",
			wantCat: CatChanged, wantPrefix: "refactor",
		},
		{
			subject: "docs(goals): sync roadmap matrix",
			body:    "",
			wantCat: CatChanged, wantPrefix: "docs", wantScope: "goals",
		},
		{
			subject: "chore: update dependencies",
			body:    "",
			wantCat: CatChanged, wantPrefix: "chore",
		},
		{
			subject: "Random commit without prefix",
			body:    "Body with (goals/some-slug/goal.md) reference",
			wantCat: CatChanged, wantSlug: "some-slug",
		},
	}
	for _, c := range cases {
		got := classifyCommit("abc1234", c.subject, c.body)
		if got.Category != c.wantCat {
			t.Errorf("classifyCommit(%q).Category = %s, want %s", c.subject, got.Category, c.wantCat)
		}
		if c.wantSlug != "" && got.GoalSlug != c.wantSlug {
			t.Errorf("classifyCommit(%q).GoalSlug = %q, want %q", c.subject, got.GoalSlug, c.wantSlug)
		}
		if c.wantPrefix != "" && got.Prefix != c.wantPrefix {
			t.Errorf("classifyCommit(%q).Prefix = %q, want %q", c.subject, got.Prefix, c.wantPrefix)
		}
		if c.wantScope != "" && got.Scope != c.wantScope {
			t.Errorf("classifyCommit(%q).Scope = %q, want %q", c.subject, got.Scope, c.wantScope)
		}
	}
}

func TestCategorize(t *testing.T) {
	cases := map[string]ChangelogCategory{
		"feat":     CatAdded,
		"fix":      CatFixed,
		"perf":     CatChanged,
		"refactor": CatChanged,
		"docs":     CatChanged,
		"chore":    CatChanged,
		"revert":   CatDeprecated,
		"security": CatSecurity,
		"unknown":  CatChanged,
	}
	for prefix, want := range cases {
		if got := categorize(prefix); got != want {
			t.Errorf("categorize(%q) = %s, want %s", prefix, got, want)
		}
	}
}

func TestParseGitLog(t *testing.T) {
	// Simulate git log output with \x1e record separator and \x1f field separator.
	raw := "abc1234\x1ffeat: add ADR engine (goals/living-adr-engine/goal.md)\x1f\n\x1edeff567\x1ffix: resolve drift in doctor\x1f\n\x1e"
	commits := parseGitLog([]byte(raw))
	if len(commits) != 2 {
		t.Fatalf("parseGitLog = %d commits, want 2", len(commits))
	}
	if commits[0].Hash != "abc1234" {
		t.Errorf("first hash = %s, want abc1234", commits[0].Hash)
	}
	if commits[0].Prefix != "feat" {
		t.Errorf("first prefix = %s, want feat", commits[0].Prefix)
	}
	if commits[0].GoalSlug != "living-adr-engine" {
		t.Errorf("first slug = %s, want living-adr-engine", commits[0].GoalSlug)
	}
	if commits[1].Prefix != "fix" {
		t.Errorf("second prefix = %s, want fix", commits[1].Prefix)
	}
}

func TestBuildReport(t *testing.T) {
	commits := []CommitEntry{
		{Hash: "a1", Prefix: "feat", Category: CatAdded, Description: "Add ADR engine"},
		{Hash: "b2", Prefix: "fix", Category: CatFixed, Description: "Fix drift"},
		{Hash: "c3", Prefix: "feat", Category: CatAdded, Description: "Add digest"},
	}
	r := BuildReport(commits, "v0.9.0..HEAD")
	if len(r.Groups[CatAdded]) != 2 {
		t.Errorf("Added group = %d, want 2", len(r.Groups[CatAdded]))
	}
	if len(r.Groups[CatFixed]) != 1 {
		t.Errorf("Fixed group = %d, want 1", len(r.Groups[CatFixed]))
	}
	// Sorted by hash within group.
	if r.Groups[CatAdded][0].Hash != "a1" {
		t.Errorf("first Added entry should be a1, got %s", r.Groups[CatAdded][0].Hash)
	}
}

func TestRenderChangelog(t *testing.T) {
	r := &ChangelogReport{
		TagRange: "unreleased",
		Groups: map[ChangelogCategory][]CommitEntry{
			CatAdded: {
				{Hash: "abc1234", Description: "Add ADR engine", GoalSlug: "living-adr-engine"},
			},
			CatFixed: {
				{Hash: "def5678", Description: "Fix drift in doctor"},
			},
		},
	}
	out := RenderChangelog(r)
	if !strings.Contains(out, "### Added") {
		t.Errorf("missing Added header")
	}
	if !strings.Contains(out, "### Fixed") {
		t.Errorf("missing Fixed header")
	}
	if !strings.Contains(out, "Add ADR engine") {
		t.Errorf("missing description")
	}
	if !strings.Contains(out, "(goals/living-adr-engine)") {
		t.Errorf("missing goal attribution")
	}
	if !strings.Contains(out, "`abc1234`") {
		t.Errorf("missing commit hash")
	}
}

func TestRenderChangelogEmpty(t *testing.T) {
	r := &ChangelogReport{Groups: map[ChangelogCategory][]CommitEntry{}}
	out := RenderChangelog(r)
	if out != "" {
		t.Errorf("empty report should render empty string, got %q", out)
	}
}

func TestFindUnreleasedSection(t *testing.T) {
	content := `# Changelog

## [Unreleased]

### Added
- Something new

### Fixed
- Something fixed

## [0.9.0] - 2026-01-01

### Added
- Old feature
`
	start, end := FindUnreleasedSection(content)
	if start < 0 {
		t.Fatal("FindUnreleasedSection: section not found")
	}
	section := content[start:end]
	if !strings.Contains(section, "### Added") {
		t.Errorf("section should contain Added, got:\n%s", section)
	}
	if strings.Contains(section, "## [0.9.0]") {
		t.Errorf("section should not include the next version header")
	}
}

func TestFindUnreleasedSectionNotFound(t *testing.T) {
	start, end := FindUnreleasedSection("no changelog here")
	if start != -1 || end != -1 {
		t.Errorf("expected -1, -1 for missing section, got %d, %d", start, end)
	}
}

func TestWriteUnreleasedIdempotent(t *testing.T) {
	existing := `# Changelog

## [Unreleased]

### Added
- Existing entry (` + "`abc1234`" + `)

## [0.9.0] - 2026-01-01

### Added
- Old feature
`
	// New content includes a duplicate hash and a new one.
	newContent := `### Added
- New entry (` + "`def5678`" + `)

### Fixed
- Bug fix (` + "`ghi9012`" + `)
`
	updated := WriteUnreleased(existing, newContent)
	// Should contain the new entry.
	if !strings.Contains(updated, "def5678") {
		t.Errorf("updated should contain new hash def5678")
	}
	if !strings.Contains(updated, "ghi9012") {
		t.Errorf("updated should contain new hash ghi9012")
	}
	// Should not duplicate the existing entry.
	count := strings.Count(updated, "abc1234")
	if count != 1 {
		t.Errorf("abc1234 should appear exactly once, got %d", count)
	}
	// Should still have the old version section.
	if !strings.Contains(updated, "## [0.9.0]") {
		t.Errorf("updated should preserve old version section")
	}
}

func TestWriteUnreleasedNoDuplicates(t *testing.T) {
	existing := `## [Unreleased]

### Added
- Entry A (` + "`aaa1111`" + `)
- Entry B (` + "`bbb2222`" + `)
`
	newContent := `### Added
- Entry A (` + "`aaa1111`" + `)
- Entry C (` + "`ccc3333`" + `)
`
	updated := WriteUnreleased(existing, newContent)
	// aaa1111 should appear once (not duplicated).
	if strings.Count(updated, "aaa1111") != 1 {
		t.Errorf("aaa1111 should appear once, got %d", strings.Count(updated, "aaa1111"))
	}
	// ccc3333 should be added.
	if !strings.Contains(updated, "ccc3333") {
		t.Errorf("ccc3333 should be added")
	}
}

func TestWriteChangelogToFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CHANGELOG.md")
	existing := "# Changelog\n\n## [Unreleased]\n\n### Added\n- Old (`old1234`)\n\n## [0.1.0]\n"
	if err := os.WriteFile(path, []byte(existing), 0644); err != nil {
		t.Fatal(err)
	}
	newContent := "### Fixed\n- New fix (`new5678`)\n"
	updated, err := WriteChangelogToFile(path, newContent)
	if err != nil {
		t.Fatalf("WriteChangelogToFile: %v", err)
	}
	if !strings.Contains(updated, "new5678") {
		t.Errorf("file should contain new entry")
	}
	// Verify file was actually written.
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "new5678") {
		t.Errorf("file on disk should contain new entry")
	}
}

func TestGenerateChangelogFiltersDocsGoals(t *testing.T) {
	// This test verifies the filtering logic by calling GenerateChangelog
	// in a temp git repo. We'll create commits and check the output.
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@test.com")
	runGit(t, dir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "feat: add feature (goals/test-goal/goal.md)")
	runGit(t, dir, "commit", "--allow-empty", "-m", "docs(goals): sync roadmap")
	runGit(t, dir, "commit", "--allow-empty", "-m", "fix: resolve bug (goals/test-goal/goal.md)")

	content, report, err := GenerateChangelog(dir, "")
	if err != nil {
		t.Fatalf("GenerateChangelog: %v", err)
	}
	// docs(goals) should be filtered out.
	if strings.Contains(content, "sync roadmap") {
		t.Errorf("docs(goals) commits should be filtered out, got:\n%s", content)
	}
	// feat and fix should be present.
	if !strings.Contains(content, "### Added") {
		t.Errorf("should have Added category")
	}
	if !strings.Contains(content, "### Fixed") {
		t.Errorf("should have Fixed category")
	}
	// Should have 2 commits (feat + fix, docs filtered).
	if len(report.Commits) != 2 {
		t.Errorf("report should have 2 commits (docs filtered), got %d", len(report.Commits))
	}
}

func TestScanCommitsRealGit(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@test.com")
	runGit(t, dir, "config", "user.name", "Test")
	runGit(t, dir, "commit", "--allow-empty", "-m", "feat: first feature (goals/alpha/goal.md)")
	runGit(t, dir, "commit", "--allow-empty", "-m", "fix: second fix")

	commits, err := ScanCommits(dir, "")
	if err != nil {
		t.Fatalf("ScanCommits: %v", err)
	}
	if len(commits) != 2 {
		t.Fatalf("expected 2 commits, got %d", len(commits))
	}
	// Newest first in git log.
	if commits[0].Prefix != "fix" {
		t.Errorf("first commit prefix = %s, want fix (newest first)", commits[0].Prefix)
	}
	if commits[1].GoalSlug != "alpha" {
		t.Errorf("second commit slug = %s, want alpha", commits[1].GoalSlug)
	}
}

// runGit runs a git command in dir, failing the test on error.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
}

func TestClassifyGoalMarkers(t *testing.T) {
	tests := []struct {
		name, subject, body string
		wantSlug, wantDesc  string
	}{
		{"path form", "feat: add x (goals/alpha/goal.md)", "", "alpha", "add x"},
		{"tag form", "feat: add x [goal: alpha]", "", "alpha", "add x"},
		{"both forms once", "feat(a): add x [goal: alpha] (goals/alpha/goal.md)", "", "alpha", "add x"},
		{"trailer in body", "feat: add x", "details\n\nGoal: alpha", "alpha", "add x"},
		{"no goal", "fix: plain", "", "", "plain"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := classifyCommit("abc1234", tt.subject, tt.body)
			if c.GoalSlug != tt.wantSlug {
				t.Errorf("GoalSlug = %q, want %q", c.GoalSlug, tt.wantSlug)
			}
			if c.Description != tt.wantDesc {
				t.Errorf("Description = %q, want %q", c.Description, tt.wantDesc)
			}
		})
	}
}

func TestClassifyMultiScopePrefix(t *testing.T) {
	c := classifyCommit("abc1234", "feat(cmd,mcp): separate routing from handlers", "")
	if c.Prefix != "feat" || c.Scope != "cmd,mcp" {
		t.Errorf("Prefix/Scope = %q/%q, want feat/cmd,mcp", c.Prefix, c.Scope)
	}
	if c.Description != "separate routing from handlers" || strings.Contains(c.Description, "feat") {
		t.Errorf("prefix leaked into description: %q", c.Description)
	}
	if c.Category != CatAdded {
		t.Errorf("Category = %q, want Added", c.Category)
	}
}

func TestParseGitLogReadsDate(t *testing.T) {
	raw := "abc1234\x1ffeat: one\x1fbody text\x1f2026-10-04T06:29:42+03:00\x1e"
	commits := parseGitLog([]byte(raw))
	if len(commits) != 1 {
		t.Fatalf("got %d commits, want 1", len(commits))
	}
	if commits[0].Date != "2026-10-04T06:29:42+03:00" || commits[0].Body != "body text" {
		t.Errorf("unexpected date/body: %+v", commits[0])
	}
}

func TestBuildReportOrdersNewestFirstThenHash(t *testing.T) {
	commits := []CommitEntry{
		{Hash: "ccc", Date: "2026-10-01T10:00:00Z", Category: CatAdded},
		{Hash: "bbb", Date: "2026-10-03T10:00:00Z", Category: CatAdded},
		{Hash: "aaa", Date: "2026-10-03T10:00:00Z", Category: CatAdded},
		{Hash: "ddd", Date: "", Category: CatAdded},
	}
	r := BuildReport(commits, "")
	var got []string
	for _, c := range r.Groups[CatAdded] {
		got = append(got, c.Hash)
	}
	if want := "aaa bbb ccc ddd"; strings.Join(got, " ") != want {
		t.Errorf("group order = %v, want %s", got, want)
	}
	if r.Total != 4 || r.GroupCounts[CatAdded] != 4 {
		t.Errorf("Total/GroupCounts = %d/%d, want 4/4", r.Total, r.GroupCounts[CatAdded])
	}
}

func TestRenderChangelogKeepsLongDescriptionsAndShowsGoalOnce(t *testing.T) {
	long := strings.Repeat("word ", 60) + "end"
	c := classifyCommit("abc1234", "feat: "+long+" (goals/alpha/goal.md)", "")
	out := RenderChangelog(BuildReport([]CommitEntry{c}, ""))
	if strings.Contains(out, "…") {
		t.Errorf("description must not be truncated: %q", out)
	}
	if !strings.Contains(out, strings.TrimSpace(long)) {
		t.Errorf("full description missing: %q", out)
	}
	if n := strings.Count(out, "goals/alpha"); n != 1 {
		t.Errorf("goal shown %d times, want 1: %q", n, out)
	}
	if !strings.Contains(out, "(`abc1234`)") {
		t.Errorf("backticked hash is required for --write idempotency: %q", out)
	}
}

func TestChangelogReportJSONKeys(t *testing.T) {
	c := classifyCommit("abc1234", "feat(adr): add x [goal: alpha]", "")
	c.Date = "2026-10-04T06:29:42+03:00"
	b, err := json.Marshal(BuildReport([]CommitEntry{c}, "v1..HEAD"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, key := range []string{`"tag_range":"v1..HEAD"`, `"total":1`, `"groups":{"Added":1}`, `"goal_slug":"alpha"`, `"hash":"abc1234"`, `"date":"2026-10-04T06:29:42+03:00"`} {
		if !strings.Contains(s, key) {
			t.Errorf("JSON missing %s: %s", key, s)
		}
	}
	if strings.Contains(s, `"Commits"`) || strings.Contains(s, `"Groups"`) {
		t.Errorf("JSON must not carry PascalCase keys: %s", s)
	}
}

func TestRenderChangelogTOON(t *testing.T) {
	a := classifyCommit("aaa1111", "feat(adr): add x, y [goal: alpha]", "")
	b := classifyCommit("bbb2222", "fix: resolve bug", "")
	out := RenderChangelogTOON(BuildReport([]CommitEntry{a, b}, "v1..HEAD"))
	for _, want := range []string{
		"changelog:\n",
		"  total: 2\n",
		"commits[2]{hash,category,scope,goal,description}:\n",
		"  aaa1111,Added,adr,alpha,\"add x, y\"\n",
		"  bbb2222,Fixed,\"\",\"\",resolve bug\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("TOON missing %q:\n%s", want, out)
		}
	}
	if RenderChangelogTOON(nil) != "" {
		t.Error("nil report should render empty")
	}
}
