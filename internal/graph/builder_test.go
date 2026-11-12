package graph

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func writeTestRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
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
func (s *Server) Start() error { return s.Close() }
func Use(s Service) error { return s.Close() }
`)
	write("pkg/a/a_test.go", `package a

import "testing"

func TestServer(t *testing.T) {
	s := &Server{}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
}
`)
	write("goals/test-goal/goal.md", "# Goal: Test Server Close\n\nTouch pkg/a/a.go and Server so Close is covered.\n")
	return root
}

func TestBuildCodeAndMemoryGraph(t *testing.T) {
	repoRoot := writeTestRepo(t)
	st, err := store.Open(filepath.Join(t.TempDir(), "graph.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	projectID := store.ProjectID(repoRoot)
	fact := store.Fact{
		ID: "fact_graph", ProjectID: projectID,
		Claim:     "Server.Close closes the service in pkg/a/a.go",
		RepoFiles: []string{"pkg/a/a.go"}, Verified: true, FileRefsExist: true,
	}
	if err := st.UpsertFact(fact); err != nil {
		t.Fatal(err)
	}
	lesson, err := st.InsertLesson(projectID, "Server.Close needs a nil receiver guard", "agent", repoRoot, []string{"pkg/a/a.go"})
	if err != nil {
		t.Fatal(err)
	}
	decision, err := st.CreateDecision(&store.Decision{
		ProjectID: projectID, Title: "Use Server.Close", GoalSlug: "test-goal",
		Context: "pkg/a/a.go", CreatedBy: "agent",
		Options:        []store.DecisionOption{{Letter: "A", Title: "Keep Close"}, {Letter: "B", Title: "Remove Close"}},
		Recommendation: "A",
	})
	if err != nil {
		t.Fatal(err)
	}

	first, err := Build(context.Background(), repoRoot, st, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if first.Version != 1 || len(first.Nodes) == 0 || len(first.Edges) == 0 {
		t.Fatalf("empty graph: %+v", first)
	}
	required := []string{"file:pkg/a/a.go", "goal:test-goal", "fact:" + fact.ID, "lesson:" + lesson.ID, "decision:" + decision.Address}
	for _, id := range required {
		if !hasNode(first, id) {
			t.Fatalf("missing node %s; nodes: %v", id, nodeIDs(first))
		}
	}
	for _, tier := range []ConfidenceTier{ConfidenceExtracted, ConfidenceInferred, ConfidenceAmbiguous} {
		if countTier(first, tier) == 0 {
			t.Fatalf("missing %s edge; edges: %+v", tier, first.Edges)
		}
	}
	alsoSeen := false
	for _, edge := range first.Edges {
		if len(edge.AlsoSeen) > 0 {
			alsoSeen = true
		}
	}
	if !alsoSeen {
		t.Fatal("no edge folded multi-evidence into AlsoSeen")
	}

	firstBytes, err := GraphBytes(first)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		again, err := Build(context.Background(), repoRoot, st, BuildOptions{})
		if err != nil {
			t.Fatal(err)
		}
		againBytes, err := GraphBytes(again)
		if err != nil {
			t.Fatal(err)
		}
		if string(againBytes) != string(firstBytes) {
			t.Fatalf("build %d is not byte-identical", i+2)
		}
	}
}

func TestShrinkGuardAndFoldEdge(t *testing.T) {
	folded := FoldEdge([]Edge{
		{Source: "a", Target: "b", Relation: "cites", Confidence: ConfidenceAmbiguous, ConfidenceScore: 0.3, SourceFile: "z.go"},
		{Source: "a", Target: "b", Relation: "cites", Confidence: ConfidenceExtracted, ConfidenceScore: 1.0, SourceFile: "a.go"},
		{Source: "a", Target: "b", Relation: "cites", Confidence: ConfidenceInferred, ConfidenceScore: 0.7, SourceFile: "m.go"},
	})
	if len(folded) != 1 || folded[0].Confidence != ConfidenceExtracted || len(folded[0].AlsoSeen) != 2 {
		t.Fatalf("fold = %+v", folded)
	}

	path := filepath.Join(t.TempDir(), GraphPath)
	memory := &Graph{Version: 1, Nodes: []Node{{ID: "goal:x", Kind: "goal", Modality: "memory"}}}
	if err := SaveGraph(path, memory, BuildOptions{}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	codeOnly := &Graph{Version: 1, Nodes: []Node{{ID: "file:x.go", Kind: "file", Modality: "code"}}}
	if err := SaveGraph(path, codeOnly, BuildOptions{CodeOnly: true}); err == nil {
		t.Fatal("expected the shrink guard to reject the code-only graph")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("shrink guard mutated the existing graph")
	}
	if err := SaveGraph(path, codeOnly, BuildOptions{CodeOnly: true, AllowShrink: true}); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadGraph(path)
	if err != nil || loaded == nil || loaded.HasMemoryNodes() {
		t.Fatalf("loaded = %+v, %v", loaded, err)
	}
}

func hasNode(g *Graph, id string) bool {
	for _, node := range g.Nodes {
		if node.ID == id {
			return true
		}
	}
	return false
}

func nodeIDs(g *Graph) []string {
	out := make([]string, 0, len(g.Nodes))
	for _, node := range g.Nodes {
		out = append(out, node.ID)
	}
	return out
}

func countTier(g *Graph, tier ConfidenceTier) int {
	count := 0
	for _, edge := range g.Edges {
		if edge.Confidence == tier {
			count++
		}
	}
	return count
}
