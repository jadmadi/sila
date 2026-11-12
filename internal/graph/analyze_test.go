package graph

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func plantedGraph() *Graph {
	g := &Graph{Version: 1}
	for _, prefix := range []string{"a", "b"} {
		for i := 0; i < 6; i++ {
			g.Nodes = append(g.Nodes, Node{ID: prefix + string(rune('0'+i)), Label: prefix + " node", Kind: "func", Modality: "code"})
		}
	}
	for _, prefix := range []string{"a", "b"} {
		for i := 0; i < 6; i++ {
			for j := i + 1; j < 6; j++ {
				g.Edges = append(g.Edges, Edge{
					Source: prefix + string(rune('0'+i)), Target: prefix + string(rune('0'+j)),
					Relation: "calls", Confidence: ConfidenceExtracted, ConfidenceScore: 1,
				})
			}
		}
	}
	g.Edges = append(g.Edges, Edge{Source: "a0", Target: "b0", Relation: "calls", Confidence: ConfidenceExtracted, ConfidenceScore: 1})
	return g
}

func cloneGraph(g *Graph) *Graph {
	out := &Graph{Version: g.Version}
	for _, node := range g.Nodes {
		out.Nodes = append(out.Nodes, node)
	}
	for _, edge := range g.Edges {
		out.Edges = append(out.Edges, edge)
	}
	return out
}

func TestClusterDeterminismAndConnectedRefinement(t *testing.T) {
	first := map[string]int{}
	firstQ := 0.0
	for run := 0; run < 10; run++ {
		g := cloneGraph(plantedGraph())
		communities := ClusterCommunities(g)
		if len(communities) < 2 {
			t.Fatalf("run %d produced %d communities, want two cliques", run, len(communities))
		}
		q := ModularityQ(g)
		if run == 0 {
			firstQ = q
			for _, node := range g.Nodes {
				first[node.ID] = node.Community
			}
			if q <= 0.3 {
				t.Fatalf("modularity Q = %v, want > 0.3", q)
			}
			assertConnectedCommunities(t, g, communities)
			continue
		}
		for _, node := range g.Nodes {
			if first[node.ID] != node.Community {
				t.Fatalf("run %d assignment for %s = %d, want %d", run, node.ID, node.Community, first[node.ID])
			}
		}
		if q != firstQ {
			t.Fatalf("run %d Q = %v, want %v", run, q, firstQ)
		}
	}
}

func assertConnectedCommunities(t *testing.T, g *Graph, communities []CommunitySummary) {
	t.Helper()
	adjacency := map[string][]string{}
	for _, edge := range g.Edges {
		adjacency[edge.Source] = append(adjacency[edge.Source], edge.Target)
		adjacency[edge.Target] = append(adjacency[edge.Target], edge.Source)
	}
	for _, community := range communities {
		if community.Size <= 1 {
			continue
		}
		inSet := map[string]bool{}
		for _, member := range community.Members {
			inSet[member] = true
		}
		seen := map[string]bool{}
		queue := []string{community.Members[0]}
		seen[queue[0]] = true
		for len(queue) > 0 {
			current := queue[0]
			queue = queue[1:]
			for _, neighbor := range adjacency[current] {
				if inSet[neighbor] && !seen[neighbor] {
					seen[neighbor] = true
					queue = append(queue, neighbor)
				}
			}
		}
		if len(seen) != community.Size {
			t.Fatalf("community %d is disconnected: %d of %d reachable", community.ID, len(seen), community.Size)
		}
	}
}

func TestGodNodesExcludesContainers(t *testing.T) {
	g := &Graph{Version: 1}
	g.Nodes = append(g.Nodes, Node{ID: "file:big.go", Label: "big.go", Kind: "file", Modality: "code"})
	g.Nodes = append(g.Nodes, Node{ID: "func:hot", Label: "hot", Kind: "func", Modality: "code"})
	for i := 0; i < 10; i++ {
		id := "func:leaf" + string(rune('0'+i))
		g.Nodes = append(g.Nodes, Node{ID: id, Label: "leaf", Kind: "func", Modality: "code"})
		g.Edges = append(g.Edges, Edge{Source: "file:big.go", Target: id, Relation: "contains", Confidence: ConfidenceExtracted, ConfidenceScore: 1})
		if i < 5 {
			g.Edges = append(g.Edges, Edge{Source: "func:hot", Target: id, Relation: "calls", Confidence: ConfidenceExtracted, ConfidenceScore: 1})
		}
	}
	report := AnalyzeGraph(g)
	if len(report.GodNodes) == 0 {
		t.Fatal("no god nodes")
	}
	for _, node := range report.GodNodes {
		if node.Kind == "file" || node.Kind == "package" {
			t.Fatalf("container %s leaked into god nodes", node.ID)
		}
	}
	found := false
	for _, node := range report.GodNodes {
		if node.ID == "func:hot" {
			found = true
		}
	}
	if !found {
		t.Fatalf("hot function missing from god nodes: %+v", report.GodNodes)
	}
}

func TestSurprisingConnectionsAndSuggestQuestions(t *testing.T) {
	g := cloneGraph(plantedGraph())
	g.Nodes = append(g.Nodes, Node{ID: "fact:x", Label: "Memory fact", Kind: "fact", Modality: "memory"})
	g.Edges = append(g.Edges,
		Edge{Source: "fact:x", Target: "a0", Relation: "cites", Confidence: ConfidenceInferred, ConfidenceScore: 0.75},
		Edge{Source: "b0", Target: "a1", Relation: "cites", Confidence: ConfidenceAmbiguous, ConfidenceScore: 0.3},
	)
	report := AnalyzeGraph(g)
	if len(report.SurprisingConnections) == 0 {
		t.Fatal("no surprising connections")
	}
	top := report.SurprisingConnections[0]
	if top.Score < 4 {
		t.Fatalf("top connection score = %v, want at least 4: %+v", top.Score, top)
	}
	categories := map[string]bool{}
	for _, question := range report.SuggestQuestions {
		categories[question.Category] = true
	}
	want := []string{"ambiguous_edge", "bridge_node", "verify_inferred", "isolated_cluster", "low_cohesion"}
	for _, category := range want {
		if !categories[category] {
			t.Fatalf("missing question category %s: %+v", category, report.SuggestQuestions)
		}
	}
	if report.ModularityQ <= 0 {
		t.Fatalf("modularity Q = %v", report.ModularityQ)
	}
	md := RenderMarkdownReport(report)
	for _, section := range []string{"## Graph Audit", "### Communities", "### God nodes", "### Surprising connections", "### Suggested questions"} {
		if !contains(md, section) {
			t.Fatalf("report missing %q", section)
		}
	}
}

func contains(haystack, needle string) bool {
	return indexOfString(haystack, needle) >= 0
}

func indexOfString(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

func TestGraphStatsAndDoctor(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module audit\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "pkg", "a.go"), []byte("package pkg\n\nfunc Alpha() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g, err := Build(context.Background(), repo, nil, BuildOptions{CodeOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo, GraphPath)
	if err := SaveGraph(path, g, BuildOptions{}); err != nil {
		t.Fatal(err)
	}
	stats := ComputeStats(g, repo, path)
	if stats.Nodes == 0 || stats.Files == 0 || stats.RepoFiles == 0 || stats.Missing {
		t.Fatalf("stats = %+v", stats)
	}
	if stats.Stale {
		t.Fatalf("fresh graph is stale: %+v", stats)
	}
	report := GraphDoctor(g, repo, path)
	if report.Status != "ok" {
		t.Fatalf("doctor = %+v", report)
	}
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(repo, "pkg", "b.go"), []byte("package pkg\n\nfunc Beta() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stale := ComputeStats(g, repo, path)
	if !stale.Stale {
		t.Fatalf("source change did not report stale: %+v", stale)
	}
	report = GraphDoctor(g, repo, path)
	if report.Status != "warn" {
		t.Fatalf("stale doctor = %+v", report)
	}
	missing := ComputeStats(nil, repo, path)
	if !missing.Missing {
		t.Fatalf("missing stats = %+v", missing)
	}
	missingReport := GraphDoctor(nil, repo, path)
	if missingReport.Status != "warn" || len(missingReport.Checks) == 0 || missingReport.Checks[0].Fix == "" {
		t.Fatalf("missing doctor = %+v", missingReport)
	}
}
