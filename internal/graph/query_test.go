package graph

import (
	"errors"
	"math"
	"testing"
)

func TestQuerySubgraphScoringAndDemotion(t *testing.T) {
	g := &Graph{Version: 1, Nodes: []Node{
		{ID: "file:store.go", Label: "store.go", Kind: "file", Modality: "code"},
		{ID: "pkg::Store.Open", Label: "Open", Kind: "func", Modality: "code", SourceFile: "store.go"},
		{ID: "test:pkg::TestOpen", Label: "TestOpen", Kind: "test", Modality: "code", SourceFile: "store_test.go"},
	}, Edges: []Edge{
		{Source: "file:store.go", Target: "pkg::Store.Open", Relation: "contains", Confidence: ConfidenceExtracted, ConfidenceScore: 1},
		{Source: "pkg::Store.Open", Target: "test:pkg::TestOpen", Relation: "tests", Confidence: ConfidenceInferred, ConfidenceScore: 0.75},
	}}
	result, err := QuerySubgraph(g, "Open", QueryOptions{MaxDepth: 2, TokenBudget: 20})
	if err != nil {
		t.Fatal(err)
	}
	if result.Scores["pkg::Store.Open"] <= result.Scores["test:pkg::TestOpen"] {
		t.Fatalf("test node was not demoted: %+v", result.Scores)
	}
	if len(result.Edges) != 2 {
		t.Fatalf("induced edges = %d, want 2: %+v", len(result.Edges), result.Edges)
	}
	if len(result.Seeds) == 0 {
		t.Fatal("no seeds")
	}
}

func chainGraph() *Graph {
	g := &Graph{Version: 1}
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		g.Nodes = append(g.Nodes, Node{ID: id, Label: id, Kind: "func", Modality: "code"})
	}
	links := [][2]string{{"a", "b"}, {"b", "c"}, {"c", "d"}, {"d", "e"}}
	for _, link := range links {
		g.Edges = append(g.Edges, Edge{Source: link[0], Target: link[1], Relation: "calls", Confidence: ConfidenceInferred, ConfidenceScore: 0.8})
	}
	return g
}

func TestWeakestShortestPathLogDijkstraFloorAndAmbiguity(t *testing.T) {
	chain := chainGraph()
	if _, err := WeakestShortestPath(chain, "a", "e", 4); !errors.Is(err, ErrNoConfidentPath) {
		t.Fatalf("four 0.80 hops should fail the 0.60 floor, got %v", err)
	}

	chain.Nodes = append(chain.Nodes, Node{ID: "x", Label: "x", Kind: "func", Modality: "code"})
	chain.Edges = append(chain.Edges,
		Edge{Source: "a", Target: "x", Relation: "calls", Confidence: ConfidenceInferred, ConfidenceScore: 0.8},
		Edge{Source: "x", Target: "e", Relation: "calls", Confidence: ConfidenceInferred, ConfidenceScore: 0.8},
	)
	path, err := WeakestShortestPath(chain, "a", "e", 4)
	if err != nil {
		t.Fatal(err)
	}
	if path.HopCount != 2 || math.Abs(path.PathConfidence-0.64) > 0.001 {
		t.Fatalf("path = %+v, want two hops at 0.64", path)
	}

	ambiguous := &Graph{Version: 1, Nodes: []Node{
		{ID: "pkg/a::Open", Label: "Open", Kind: "func", Modality: "code"},
		{ID: "pkg/b::Open", Label: "Open", Kind: "func", Modality: "code"},
	}}
	_, err = WeakestShortestPath(ambiguous, "Open", "pkg/a::Open", 2)
	var ambiguity *AmbiguousNodeError
	if !errors.As(err, &ambiguity) || len(ambiguity.Candidates) != 2 {
		t.Fatalf("ambiguous error = %v", err)
	}

	weak := &Graph{Version: 1, Nodes: []Node{
		{ID: "a", Label: "a", Kind: "func", Modality: "code"},
		{ID: "b", Label: "b", Kind: "func", Modality: "code"},
	}, Edges: []Edge{
		{Source: "a", Target: "b", Relation: "cites", Confidence: ConfidenceAmbiguous, ConfidenceScore: 0.25},
	}}
	if _, err := WeakestShortestPath(weak, "a", "b", 2); !errors.Is(err, ErrNoConfidentPath) {
		t.Fatalf("ambiguous edge should be excluded by default, got %v", err)
	}
	included, err := WeakestShortestPathWithOptions(weak, "a", "b", PathOptions{MaxHops: 2, IncludeAmbiguous: true})
	if err != nil || included.HopCount != 1 {
		t.Fatalf("IncludeAmbiguous path = %+v, %v", included, err)
	}
}

func TestAffectedByBlastRadius(t *testing.T) {
	g := &Graph{Version: 1, Nodes: []Node{
		{ID: "pkg::Server", Label: "Server", Kind: "type", Modality: "code", SourceFile: "server.go"},
		{ID: "pkg::Server.Close", Label: "Close", Kind: "method", Modality: "code", SourceFile: "server.go"},
		{ID: "pkg::Use", Label: "Use", Kind: "func", Modality: "code", SourceFile: "use.go"},
		{ID: "goal:x", Label: "Server goal", Kind: "goal", Modality: "memory"},
		{ID: "test:pkg::TestServer", Label: "TestServer", Kind: "test", Modality: "code"},
	}, Edges: []Edge{
		{Source: "pkg::Use", Target: "pkg::Server.Close", Relation: "calls", Confidence: ConfidenceExtracted, ConfidenceScore: 1, SourceFile: "use.go", SourceLocation: "10"},
		{Source: "test:pkg::TestServer", Target: "pkg::Server.Close", Relation: "tests", Confidence: ConfidenceInferred, ConfidenceScore: 0.75},
		{Source: "goal:x", Target: "pkg::Server", Relation: "governs", Confidence: ConfidenceInferred, ConfidenceScore: 0.7},
	}}
	result, err := AffectedBy(g, "pkg::Server", 2)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]ImpactedNode{}
	for _, node := range result.Impacted {
		found[node.ID] = node
	}
	if node, ok := found["pkg::Server.Close"]; !ok || node.Depth != 0 || node.Relation != "contains" {
		t.Fatalf("member expansion missing: %+v", result.Impacted)
	}
	if node, ok := found["pkg::Use"]; !ok || node.Depth != 1 || node.ViaFile != "use.go" || node.ViaLocation != "10" {
		t.Fatalf("caller attribution missing: %+v", result.Impacted)
	}
	if _, ok := found["goal:x"]; !ok {
		t.Fatalf("goal not impacted: %+v", result.Impacted)
	}
}

func TestQuerySubgraphTopSeedsAndSeedList(t *testing.T) {
	g := &Graph{Version: 1, Nodes: []Node{
		{ID: "file:store.go", Label: "store.go", Kind: "file", Modality: "code"},
		{ID: "pkg::Store.Open", Label: "Open", Kind: "func", Modality: "code", SourceFile: "store.go"},
		{ID: "pkg::Store.Close", Label: "Close", Kind: "func", Modality: "code", SourceFile: "store.go"},
		{ID: "fact:f1", Label: "Open store note", Kind: "fact", Modality: "memory"},
	}, Edges: []Edge{
		{Source: "file:store.go", Target: "pkg::Store.Open", Relation: "contains", Confidence: ConfidenceExtracted, ConfidenceScore: 1},
	}}
	result, err := QuerySubgraph(g, "Open", QueryOptions{MaxDepth: 1, TokenBudget: 20})
	if err != nil {
		t.Fatal(err)
	}
	hasNode := func(id string) bool {
		for _, node := range result.Nodes {
			if node.ID == id {
				return true
			}
		}
		return false
	}
	hasSeed := func(id string) bool {
		for _, seed := range result.Seeds {
			if seed == id {
				return true
			}
		}
		return false
	}
	if !hasSeed("pkg::Store.Open") {
		t.Fatalf("symbol was not seeded: %v", result.Seeds)
	}
	if !hasSeed("fact:f1") {
		t.Fatalf("matching fact was not seeded: %v", result.Seeds)
	}
	if !hasNode("file:store.go") {
		t.Fatalf("expansion did not include the containing file: %+v", result.Nodes)
	}
	if hasSeed("file:store.go") {
		t.Fatalf("a non-matching file was reported as a seed: %v", result.Seeds)
	}
	if len(result.Seeds) > 20 {
		t.Fatalf("seed list was not capped: %d", len(result.Seeds))
	}
}

func TestQuerySubgraphRelationalOnlyFallback(t *testing.T) {
	g := &Graph{Version: 1, Nodes: []Node{
		{ID: "pkg::fake.Call", Label: "Call", Kind: "method", Modality: "code"},
		{ID: "pkg::Before", Label: "Before", Kind: "func", Modality: "code"},
	}}
	result, err := QuerySubgraph(g, "Call", QueryOptions{MaxDepth: 1, TokenBudget: 10})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, node := range result.Nodes {
		if node.ID == "pkg::fake.Call" {
			found = true
		}
	}
	if !found {
		t.Fatalf("a bare Call query returned nothing useful: %+v", result.Nodes)
	}
	empty, err := QuerySubgraph(g, "for", QueryOptions{MaxDepth: 1, TokenBudget: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Nodes) != 0 {
		t.Fatalf("a stopword fallback matched nodes: %+v", empty.Nodes)
	}
}

func TestQuerySubgraphLabelAndIDWeightsAreSeparate(t *testing.T) {
	g := &Graph{Version: 1, Nodes: []Node{
		{ID: "pkg::relay", Label: "relay", Kind: "func", Modality: "code"},
		{ID: "pkg/relay::helper", Label: "helper", Kind: "func", Modality: "code"},
	}}
	result, err := QuerySubgraph(g, "relay", QueryOptions{MaxDepth: 1, TokenBudget: 10})
	if err != nil {
		t.Fatal(err)
	}
	labelScore := result.Scores["pkg::relay"]
	pathScore := result.Scores["pkg/relay::helper"]
	if labelScore <= pathScore {
		t.Fatalf("label match %v did not outrank ID-path match %v", labelScore, pathScore)
	}
}

func TestLimitQueryResult(t *testing.T) {
	g := &Graph{Version: 1}
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		g.Nodes = append(g.Nodes, Node{ID: id, Label: id, Kind: "func", Modality: "code"})
	}
	g.Edges = []Edge{
		{Source: "a", Target: "b", Relation: "calls", Confidence: ConfidenceExtracted, ConfidenceScore: 1},
		{Source: "b", Target: "c", Relation: "calls", Confidence: ConfidenceExtracted, ConfidenceScore: 1},
		{Source: "c", Target: "d", Relation: "calls", Confidence: ConfidenceExtracted, ConfidenceScore: 1},
	}
	result := &SubgraphResult{
		Query:  "node",
		Nodes:  append([]Node(nil), g.Nodes...),
		Edges:  append([]Edge(nil), g.Edges...),
		Scores: map[string]float64{"a": 5, "b": 4, "c": 3, "d": 2, "e": 1},
	}
	limited := LimitQueryResult(g, result, 3)
	if len(limited.Nodes) != 3 {
		t.Fatalf("nodes = %d", len(limited.Nodes))
	}
	if len(limited.Edges) != 2 {
		t.Fatalf("edges = %+v", limited.Edges)
	}
	if _, ok := limited.Scores["d"]; ok {
		t.Fatalf("trimmed score leaked: %+v", limited.Scores)
	}
}

func TestWeakestShortestPathCodeOnly(t *testing.T) {
	g := &Graph{Version: 1, Nodes: []Node{
		{ID: "a", Label: "a", Kind: "func", Modality: "code"},
		{ID: "b", Label: "b", Kind: "func", Modality: "code"},
		{ID: "fact:f", Label: "f", Kind: "fact", Modality: "memory"},
	}, Edges: []Edge{
		{Source: "a", Target: "fact:f", Relation: "governs", Confidence: ConfidenceExtracted, ConfidenceScore: 1},
		{Source: "fact:f", Target: "b", Relation: "cites", Confidence: ConfidenceInferred, ConfidenceScore: 1},
	}}
	path, err := WeakestShortestPathWithOptions(g, "a", "b", PathOptions{MaxHops: 3})
	if err != nil {
		t.Fatalf("default path: %v", err)
	}
	if path.HopCount != 2 {
		t.Fatalf("default hop count = %d", path.HopCount)
	}
	if _, err := WeakestShortestPathWithOptions(g, "a", "b", PathOptions{MaxHops: 3, CodeOnly: true}); !errors.Is(err, ErrNoConfidentPath) {
		t.Fatalf("code-only memory path error = %v", err)
	}
	g.Edges = append(g.Edges, Edge{Source: "a", Target: "b", Relation: "calls", Confidence: ConfidenceExtracted, ConfidenceScore: 1})
	path, err = WeakestShortestPathWithOptions(g, "a", "b", PathOptions{MaxHops: 3, CodeOnly: true})
	if err != nil {
		t.Fatalf("code-only direct path: %v", err)
	}
	if path.HopCount != 1 {
		t.Fatalf("code-only hop count = %d", path.HopCount)
	}
}

func TestSliceGraph(t *testing.T) {
	g := &Graph{Version: 1, Nodes: []Node{
		{ID: "file:pkg/a.go", Label: "a.go", Kind: "file", Modality: "code", SourceFile: "pkg/a.go"},
		{ID: "pkg::Alpha", Label: "Alpha", Kind: "func", Modality: "code", SourceFile: "pkg/a.go"},
		{ID: "pkg::Beta", Label: "Beta", Kind: "func", Modality: "code", SourceFile: "pkg/b.go"},
	}, Edges: []Edge{
		{Source: "file:pkg/a.go", Target: "pkg::Alpha", Relation: "contains", Confidence: ConfidenceExtracted, ConfidenceScore: 1},
	}}
	result, err := SliceGraph(g, "", []string{"pkg/a.go"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Nodes) != 2 || len(result.Edges) != 1 {
		t.Fatalf("slice = %+v", result)
	}
	if _, err := SliceGraph(g, "", nil, 10); !errors.Is(err, ErrNoSliceInput) {
		t.Fatalf("empty slice error = %v", err)
	}
	result, err = SliceGraph(g, "Alpha", nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Nodes) > 1 {
		t.Fatalf("budget slice = %+v", result)
	}
}
