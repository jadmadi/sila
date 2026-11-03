package assess

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/loaders"
	"github.com/jadmadi/sila/internal/store"
)

// initAssessGitRepo creates a temp repository with one commit.
func initAssessGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "a.txt")
	run("commit", "-qm", "feat: initial work")
	return dir
}

// TestAssessGitScopedResolvesRepo keeps `sila assess git --project=<id>` from
// regressing to the old n/a result: git resolves selectors to a repository and
// reports its pending commits.
func TestAssessGitScopedResolvesRepo(t *testing.T) {
	repo := initAssessGitRepo(t)
	st, err := store.Open(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p, err := st.EnsureProject(repo)
	if err != nil {
		t.Fatal(err)
	}

	srcs := map[string]*loaders.Source{
		"git": {Name: "git", New: loaders.NewGitLoader},
	}
	reports, err := runWithSources(st, srcs, Options{ProjectSel: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 1 {
		t.Fatalf("expected 1 report, got %d", len(reports))
	}
	r := reports[0]
	if r.NotScoped {
		t.Fatalf("git must be scoped under --project, got %+v", r)
	}
	if r.Total != 1 || r.New != 1 {
		t.Fatalf("expected 1 total / 1 pending commit, got %+v", r)
	}
}

func codexSource(t *testing.T) (string, map[string]*loaders.Source) {
	t.Helper()
	dir := t.TempDir()
	f := filepath.Join(dir, "hist.jsonl")
	os.WriteFile(f, []byte(
		"{\"session_id\":\"a\",\"ts\":100,\"text\":\"one\"}\n"+
			"{\"session_id\":\"a\",\"ts\":200,\"text\":\"two\"}\n"+
			"{\"session_id\":\"a\",\"ts\":300,\"text\":\"three\"}\n"), 0o644)
	srcs := map[string]*loaders.Source{
		"codex": {Name: "codex", File: f, New: loaders.NewCodexLoader},
		"devin": {Name: "devin", DataDir: filepath.Join(dir, "absent"), New: loaders.NewDevinLoader},
	}
	return f, srcs
}

func TestAssessTotalAndNewNoStore(t *testing.T) {
	_, srcs := codexSource(t)
	reports, err := runWithSources(nil, srcs, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 2 {
		t.Fatalf("expected 2 reports, got %d", len(reports))
	}

	var codex *Report
	for i := range reports {
		if reports[i].Tool == "codex" {
			codex = &reports[i]
		}
	}
	if codex == nil {
		t.Fatal("missing codex report")
	}
	if !codex.Exists || codex.Total != 3 || codex.New != 3 {
		t.Fatalf("no-store codex report wrong: %+v", codex)
	}
	if codex.HasWatermark {
		t.Fatal("no store => should not report a watermark")
	}
}

func TestAssessNewRespectsWatermark(t *testing.T) {
	_, srcs := codexSource(t)
	st, err := store.Open(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	// simulate an ingest that consumed records up to ts=200
	if err := st.SetWatermark("", "codex", "200"); err != nil {
		t.Fatal(err)
	}

	reports, err := runWithSources(st, srcs, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var codex *Report
	for i := range reports {
		if reports[i].Tool == "codex" {
			codex = &reports[i]
		}
	}
	if codex == nil {
		t.Fatal("missing codex report")
	}
	if !codex.HasWatermark || codex.Total != 3 || codex.New != 2 {
		// The equal-timestamp boundary record is re-emitted by design (the
		// cursor tolerates same-second appends); content dedup absorbs it at
		// ingest time, so the assess "new" count overstates by the boundary.
		t.Fatalf("watermark-aware codex report wrong: %+v", codex)
	}
}

func TestAssessProjectScopingMarksUnscoped(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "hist.jsonl")
	os.WriteFile(f, []byte("{\"session_id\":\"a\",\"ts\":1,\"text\":\"x\"}\n"), 0o644)
	srcs := map[string]*loaders.Source{
		"codex":    {Name: "codex", File: f, New: loaders.NewCodexLoader},
		"mimocode": {Name: "mimocode", DataDir: filepath.Join(dir, "absent"), New: loaders.NewMiMoCodeLoader},
	}
	reports, err := runWithSources(nil, srcs, Options{ProjectSel: "someproj"})
	if err != nil {
		t.Fatal(err)
	}
	byTool := map[string]Report{}
	for _, r := range reports {
		byTool[r.Tool] = r
	}
	// codex has no working dir -> NotScoped
	if !byTool["codex"].NotScoped {
		t.Fatalf("codex should be NotScoped under a project scope")
	}
	// mimocode is a scoped tool; it's just absent here, not NotScoped
	if byTool["mimocode"].NotScoped {
		t.Fatalf("mimocode should be project-scoped, not NotScoped")
	}
}

func TestAssessMissingSource(t *testing.T) {
	_, srcs := codexSource(t)
	reports, err := runWithSources(nil, srcs, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var devin *Report
	for i := range reports {
		if reports[i].Tool == "devin" {
			devin = &reports[i]
		}
	}
	if devin == nil {
		t.Fatal("missing devin report")
	}
	if devin.Exists || devin.Error != nil {
		t.Fatalf("absent source should be exists=false with no error: %+v", devin)
	}
}
