package goals

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func writeGoalPackage(t *testing.T, root, slug string, files map[string]string) {
	t.Helper()
	dir := filepath.Join(root, "goals", slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestScanGoalsDraftAccuracy(t *testing.T) {
	root := t.TempDir()
	writeGoalPackage(t, root, "upper-plan", map[string]string{
		"goal.md":  "# Goal: Upper\n\n## Goal Description\nCase variant package.\n",
		"PLAN.md":  "- [ ] one\n- [x] two\n",
		"FACTS.md": "facts\n",
	})
	writeGoalPackage(t, root, "partial", map[string]string{
		"goal.md": "# Goal: Partial\n\n## Goal Description\nOnly a goal file.\n",
	})
	if err := os.MkdirAll(filepath.Join(root, "goals", "_program"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "goals", "_program", "contracts.md"), []byte("scaffold"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := ScanGoals(root)
	if err != nil {
		t.Fatal(err)
	}

	bySlug := map[string]Goal{}
	for _, g := range res.Goals {
		bySlug[g.Slug] = g
	}
	if _, ok := bySlug["_program"]; ok {
		t.Fatal("underscore scaffolding directory must not be scanned as a goal")
	}
	upper, ok := bySlug["upper-plan"]
	if !ok {
		t.Fatal("uppercase PLAN.md goal missing from the scan")
	}
	if upper.Status == "draft" {
		t.Fatalf("uppercase PLAN.md/FACTS.md counted as a draft: %+v", upper)
	}
	if !upper.HasPlanMd || !upper.HasFactsMd {
		t.Fatalf("uppercase files not recognized: plan=%t facts=%t", upper.HasPlanMd, upper.HasFactsMd)
	}
	if upper.TasksTotal != 2 {
		t.Fatalf("uppercase PLAN.md tasks = %d, want 2", upper.TasksTotal)
	}
	partial, ok := bySlug["partial"]
	if !ok || partial.Status != "draft" {
		t.Fatalf("partial package status = %+v, want draft", partial)
	}
	if len(partial.MissingFiles) != 2 || partial.MissingFiles[0] != "plan.md" || partial.MissingFiles[1] != "facts.md" {
		t.Fatalf("missing files = %v, want [plan.md facts.md]", partial.MissingFiles)
	}
}

func TestScanAllProjectsGoalsDedupesAliasRegistrations(t *testing.T) {
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "stats.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	real := filepath.Join(root, "repo")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	writeGoalPackage(t, real, "alpha", map[string]string{
		"goal.md":  "# Goal: Alpha\n\n## Goal Description\nOne goal.\n",
		"plan.md":  "- [ ] step\n",
		"facts.md": "facts\n",
	})
	alias := filepath.Join(root, "repo-alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`INSERT INTO projects (path, id, created_at) VALUES (?,?,?)`, real, "repo", "2026-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`INSERT INTO projects (path, id, created_at) VALUES (?,?,?)`, alias, "repo-alias", "2026-01-02T00:00:00Z"); err != nil {
		t.Fatal(err)
	}

	res, err := ScanAllProjectsGoals(st)
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalGoals != 1 {
		t.Fatalf("total goals = %d, want 1 counted once", res.TotalGoals)
	}
	if res.TotalProjects != 1 {
		t.Fatalf("total projects = %d, want 1 counted once", res.TotalProjects)
	}
	if len(res.Stats.DuplicateRegistrations) != 1 {
		t.Fatalf("duplicates = %+v, want the alias pair reported", res.Stats.DuplicateRegistrations)
	}
	if res.Stats.DuplicateRegistrations[0].Overlap != 1 {
		t.Fatalf("overlap = %d, want 1 shared goal", res.Stats.DuplicateRegistrations[0].Overlap)
	}
}
