package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/graph"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func graphTestRepo(t *testing.T) (string, string) {
	t.Helper()
	repo := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module testmod\n\ngo 1.26\n")
	write("pkg/a/a.go", `package a

type Service interface{ Close() error }
type Server struct{}
func (s *Server) Close() error { return nil }
func Use(s Service) error { return s.Close() }
`)
	dbPath := filepath.Join(t.TempDir(), "graph-cli.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	projectID := store.ProjectID(repo)
	if err := st.UpsertFact(store.Fact{
		ID: "fact_graph", ProjectID: projectID,
		Claim: "Server.Close closes the service", RepoFiles: []string{"pkg/a/a.go"},
		Verified: true, FileRefsExist: true,
	}); err != nil {
		t.Fatal(err)
	}
	return repo, dbPath
}

func TestGraphCLISubcommandsAndJSON(t *testing.T) {
	repo, dbPath := graphTestRepo(t)
	t.Chdir(repo)
	ui := render.New(os.Stdout, true)

	out := captureStdout(t, func() {
		runGraph(ui, dbPath, "", true, false, false, false, 0, false, false, 0, 0, "", "", []string{"build"})
	})
	var built map[string]any
	if err := json.Unmarshal([]byte(out), &built); err != nil {
		t.Fatalf("build json: %v (%s)", err, out)
	}
	if built["path"] != graph.GraphPath {
		t.Fatalf("build path = %v", built["path"])
	}
	if _, err := os.Stat(filepath.Join(repo, graph.GraphPath)); err != nil {
		t.Fatalf("graph file missing: %v", err)
	}

	out = captureStdout(t, func() {
		runGraph(ui, dbPath, "", true, false, false, false, 0, false, false, 0, 0, "", "", []string{"report"})
	})
	var report graph.AuditReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("report json: %v (%s)", err, out)
	}
	if len(report.Communities) == 0 || len(report.SuggestQuestions) != 5 {
		t.Fatalf("report = %+v", report)
	}

	out = captureStdout(t, func() {
		runGraph(ui, dbPath, "", true, false, false, false, 10, false, false, 0, 0, "", "", []string{"query", "Close"})
	})
	var query graph.SubgraphResult
	if err := json.Unmarshal([]byte(out), &query); err != nil {
		t.Fatalf("query json: %v (%s)", err, out)
	}
	if len(query.Nodes) == 0 {
		t.Fatalf("query returned no nodes: %s", out)
	}

	out = captureStdout(t, func() {
		runGraph(ui, dbPath, "", true, false, false, false, 0, false, false, 0, 0, "", "", []string{"path", "file:pkg/a/a.go", "testmod/pkg/a::Use"})
	})
	var path graph.PathResult
	if err := json.Unmarshal([]byte(out), &path); err != nil {
		t.Fatalf("path json: %v (%s)", err, out)
	}
	if path.HopCount == 0 || path.PathConfidence <= 0 {
		t.Fatalf("path = %+v", path)
	}

	out = captureStdout(t, func() {
		runGraph(ui, dbPath, "", true, false, false, false, 0, false, false, 0, 0, "", "", []string{"affected", "testmod/pkg/a::Server"})
	})
	var affected graph.BlastRadiusResult
	if err := json.Unmarshal([]byte(out), &affected); err != nil {
		t.Fatalf("affected json: %v (%s)", err, out)
	}
	if len(affected.Impacted) == 0 {
		t.Fatalf("affected = %+v", affected)
	}
}

func TestGraphCLIAmbiguousPathRefusal(t *testing.T) {
	if os.Getenv("GRAPH_AMBIGUOUS_HELPER") == "1" {
		repo := os.Getenv("GRAPH_HELPER_REPO")
		include := os.Getenv("GRAPH_INCLUDE_AMBIGUOUS") == "1"
		_ = os.Chdir(repo)
		ui := render.New(os.Stdout, true)
		runGraph(ui, filepath.Join(repo, "unused.db"), "", true, false, false, false, 0, include, false, 0, 0, "", "", []string{"path", "a", "b"})
		return
	}
	repo := t.TempDir()
	path := filepath.Join(repo, graph.GraphPath)
	g := &graph.Graph{Version: 1, Nodes: []graph.Node{
		{ID: "a", Label: "a", Kind: "func", Modality: "code"},
		{ID: "b", Label: "b", Kind: "func", Modality: "code"},
	}, Edges: []graph.Edge{
		{Source: "a", Target: "b", Relation: "cites", Confidence: graph.ConfidenceAmbiguous, ConfidenceScore: 0.3},
	}}
	if err := graph.SaveGraph(path, g, graph.BuildOptions{}); err != nil {
		t.Fatal(err)
	}
	run := func(include bool) error {
		cmd := exec.Command(os.Args[0], "-test.run=TestGraphCLIAmbiguousPathRefusal")
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GRAPH_AMBIGUOUS_HELPER=1",
			"GRAPH_HELPER_REPO="+repo,
			"GRAPH_INCLUDE_AMBIGUOUS="+map[bool]string{true: "1", false: "0"}[include],
		)
		return cmd.Run()
	}
	if err := run(false); err == nil {
		t.Fatal("default path should refuse an AMBIGUOUS-only route")
	}
	if err := run(true); err != nil {
		t.Fatalf("--include-ambiguous path failed: %v", err)
	}
}

func TestGraphAffectedCodeOnly(t *testing.T) {
	repo, dbPath := graphTestRepo(t)
	t.Chdir(repo)
	ui := render.New(os.Stdout, true)
	captureStdout(t, func() {
		runGraph(ui, dbPath, "", true, false, false, false, 0, false, false, 0, 0, "", "", []string{"build"})
	})
	runAffected := func(codeOnly bool) graph.BlastRadiusResult {
		out := captureStdout(t, func() {
			runGraph(ui, dbPath, "", true, false, false, false, 0, false, codeOnly, 0, 0, "", "", []string{"affected", "testmod/pkg/a::Server"})
		})
		var result graph.BlastRadiusResult
		if err := json.Unmarshal([]byte(out), &result); err != nil {
			t.Fatalf("affected json: %v (%s)", err, out)
		}
		return result
	}
	has := func(result graph.BlastRadiusResult, id string) bool {
		for _, node := range result.Impacted {
			if node.ID == id {
				return true
			}
		}
		return false
	}
	full := runAffected(false)
	code := runAffected(true)
	if !has(full, "fact:fact_graph") {
		t.Fatalf("full affected result is missing the fact: %+v", full.Impacted)
	}
	if has(code, "fact:fact_graph") {
		t.Fatalf("code-only affected result leaked the fact: %+v", code.Impacted)
	}
	if !has(code, "testmod/pkg/a::Use") {
		t.Fatalf("code-only affected result is missing Use: %+v", code.Impacted)
	}
}

func TestGraphFreshnessAndIfStale(t *testing.T) {
	repo, dbPath := graphTestRepo(t)
	t.Chdir(repo)
	ui := render.New(os.Stdout, true)
	captureStdout(t, func() {
		runGraph(ui, dbPath, "", true, false, false, false, 0, false, false, 0, 0, "", "", []string{"build"})
	})
	if err := os.WriteFile(filepath.Join(repo, "pkg", "a", "new.go"), []byte("package a\n\nfunc Newer() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		runGraph(ui, dbPath, "", true, false, false, false, 0, false, false, 0, 0, "", "", []string{"query", "Newer"})
	})
	var query graph.SubgraphResult
	if err := json.Unmarshal([]byte(out), &query); err != nil {
		t.Fatalf("query json: %v", err)
	}
	if !query.Stale {
		t.Fatalf("query = %+v, want stale", query)
	}
	out = captureStdout(t, func() {
		runGraph(ui, dbPath, "", true, false, false, true, 0, false, false, 0, 0, "", "", []string{"build"})
	})
	var built map[string]any
	if err := json.Unmarshal([]byte(out), &built); err != nil {
		t.Fatalf("build json: %v", err)
	}
	if built["skipped"] == true {
		t.Fatalf("stale build was skipped: %v", built)
	}
	out = captureStdout(t, func() {
		runGraph(ui, dbPath, "", true, false, false, true, 0, false, false, 0, 0, "", "", []string{"build"})
	})
	var skipped map[string]any
	if err := json.Unmarshal([]byte(out), &skipped); err != nil {
		t.Fatalf("skip json: %v", err)
	}
	if skipped["skipped"] != true {
		t.Fatalf("fresh build was not skipped: %v", skipped)
	}
}

func TestGraphPathCodeOnly(t *testing.T) {
	repo := t.TempDir()
	g := &graph.Graph{Version: 1, Nodes: []graph.Node{
		{ID: "a", Label: "a", Kind: "func", Modality: "code"},
		{ID: "b", Label: "b", Kind: "func", Modality: "code"},
		{ID: "fact:f", Label: "f", Kind: "fact", Modality: "memory"},
	}, Edges: []graph.Edge{
		{Source: "a", Target: "b", Relation: "calls", Confidence: graph.ConfidenceExtracted, ConfidenceScore: 1},
		{Source: "a", Target: "fact:f", Relation: "governs", Confidence: graph.ConfidenceExtracted, ConfidenceScore: 1},
	}}
	if err := graph.SaveGraph(filepath.Join(repo, graph.GraphPath), g, graph.BuildOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runGraph(ui, filepath.Join(repo, "unused.db"), "", true, false, false, false, 0, false, true, 0, 0, "", "", []string{"path", "a", "b"})
	})
	var path graph.PathResult
	if err := json.Unmarshal([]byte(out), &path); err != nil {
		t.Fatalf("path json: %v (%s)", err, out)
	}
	if path.HopCount != 1 {
		t.Fatalf("path = %+v", path)
	}
}

func TestGraphBuildCodeOnly(t *testing.T) {
	repo, dbPath := graphTestRepo(t)
	t.Chdir(repo)
	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runGraph(ui, dbPath, "", true, false, true, false, 0, false, true, 0, 0, "", "", []string{"build"})
	})
	var built map[string]any
	if err := json.Unmarshal([]byte(out), &built); err != nil {
		t.Fatalf("build json: %v (%s)", err, out)
	}
	g, err := graph.LoadGraph(filepath.Join(repo, graph.GraphPath))
	if err != nil || g == nil {
		t.Fatalf("load graph: %v", err)
	}
	for _, node := range g.Nodes {
		if node.Modality == "memory" {
			t.Fatalf("code-only build kept memory node %s", node.ID)
		}
	}
}

func TestGraphStatsAndDoctor(t *testing.T) {
	repo, dbPath := graphTestRepo(t)
	t.Chdir(repo)
	ui := render.New(os.Stdout, true)
	captureStdout(t, func() {
		runGraph(ui, dbPath, "", true, false, false, false, 0, false, false, 0, 0, "", "", []string{"build"})
	})
	out := captureStdout(t, func() {
		runGraph(ui, dbPath, "", true, false, false, false, 0, false, false, 0, 0, "", "", []string{"stats"})
	})
	var stats graph.Stats
	if err := json.Unmarshal([]byte(out), &stats); err != nil {
		t.Fatalf("stats json: %v (%s)", err, out)
	}
	if stats.Nodes == 0 || stats.Missing {
		t.Fatalf("stats = %+v", stats)
	}
	out = captureStdout(t, func() {
		runGraph(ui, dbPath, "", true, false, false, false, 0, false, false, 0, 0, "", "", []string{"doctor"})
	})
	var report graph.DoctorReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("doctor json: %v (%s)", err, out)
	}
	if report.Status == "" || len(report.Checks) == 0 {
		t.Fatalf("doctor = %+v", report)
	}
	empty := t.TempDir()
	t.Chdir(empty)
	out = captureStdout(t, func() {
		runGraph(ui, filepath.Join(empty, "unused.db"), "", true, false, false, false, 0, false, false, 0, 0, "", "", []string{"stats"})
	})
	var missing graph.Stats
	if err := json.Unmarshal([]byte(out), &missing); err != nil {
		t.Fatalf("missing stats json: %v (%s)", err, out)
	}
	if !missing.Missing {
		t.Fatalf("missing stats = %+v", missing)
	}
	if _, err := os.Stat(filepath.Join(empty, graph.GraphPath)); !os.IsNotExist(err) {
		t.Fatalf("stats wrote a graph file: %v", err)
	}
}

func TestGraphSlice(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	g := &graph.Graph{Version: 1, Nodes: []graph.Node{
		{ID: "file:pkg/a.go", Label: "a.go", Kind: "file", Modality: "code", SourceFile: "pkg/a.go"},
		{ID: "pkg::Alpha", Label: "Alpha", Kind: "func", Modality: "code", SourceFile: "pkg/a.go"},
	}, Edges: []graph.Edge{
		{Source: "file:pkg/a.go", Target: "pkg::Alpha", Relation: "contains", Confidence: graph.ConfidenceExtracted, ConfidenceScore: 1},
	}}
	if err := graph.SaveGraph(filepath.Join(repo, graph.GraphPath), g, graph.BuildOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runGraph(ui, filepath.Join(repo, "unused.db"), "", true, false, false, false, 0, false, false, 0, 0, "pkg/a.go", "10", []string{"slice"})
	})
	var result graph.SubgraphResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("slice json: %v (%s)", err, out)
	}
	if len(result.Nodes) != 2 {
		t.Fatalf("slice = %+v", result)
	}
}

func TestGraphBuildCache(t *testing.T) {
	repo, dbPath := graphTestRepo(t)
	t.Chdir(repo)
	ui := render.New(os.Stdout, true)
	build := func() map[string]any {
		t.Helper()
		out := captureStdout(t, func() {
			runGraph(ui, dbPath, "", true, false, false, false, 0, false, false, 0, 0, "", "", []string{"build"})
		})
		var result map[string]any
		if err := json.Unmarshal([]byte(out), &result); err != nil {
			t.Fatalf("build json: %v (%s)", err, out)
		}
		return result
	}
	first := build()
	if cached, _ := first["cached"].(bool); cached {
		t.Fatalf("first build unexpectedly cached: %+v", first)
	}
	second := build()
	if cached, _ := second["cached"].(bool); !cached {
		t.Fatalf("second build was not cached: %+v", second)
	}
	goFile := ""
	_ = filepath.WalkDir(repo, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".go") && goFile == "" {
			goFile = path
		}
		return nil
	})
	if goFile == "" {
		t.Fatal("no Go source to edit")
	}
	original, err := os.ReadFile(goFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(goFile, append(original, []byte("\n// cache invalidation\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	third := build()
	if cached, _ := third["cached"].(bool); cached {
		t.Fatalf("changed source was cached: %+v", third)
	}
}
