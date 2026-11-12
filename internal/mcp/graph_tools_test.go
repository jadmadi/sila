package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/graph"
	"github.com/jadmadi/sila/internal/store"
)

func allowGraphProjects(t *testing.T) {
	t.Setenv("SILA_GRAPH_ALLOW_ANY_PROJECT", "1")
}

func TestMCPGraphToolsAndAmbiguity(t *testing.T) {
	allowGraphProjects(t)
	repo := t.TempDir()
	st, err := store.Open(filepath.Join(t.TempDir(), "graph-mcp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	g := &graph.Graph{Version: 1, Nodes: []graph.Node{
		{ID: "pkg::Open", Label: "OpenFile", Kind: "func", Modality: "code", SourceFile: "open.go"},
		{ID: "pkg::Close", Label: "Close", Kind: "func", Modality: "code", SourceFile: "open.go"},
		{ID: "fact:x", Label: "Open closes files", Kind: "fact", Modality: "memory"},
		{ID: "pkg/a::Open", Label: "Open", Kind: "func", Modality: "code"},
		{ID: "pkg/b::Open", Label: "Open", Kind: "func", Modality: "code"},
	}, Edges: []graph.Edge{
		{Source: "pkg::Open", Target: "pkg::Close", Relation: "calls", Confidence: graph.ConfidenceExtracted, ConfidenceScore: 1, SourceFile: "open.go"},
		{Source: "fact:x", Target: "pkg::Open", Relation: "cites", Confidence: graph.ConfidenceInferred, ConfidenceScore: 0.75},
	}}
	if err := graph.SaveGraph(filepath.Join(repo, graph.GraphPath), g, graph.BuildOptions{}); err != nil {
		t.Fatal(err)
	}

	calls := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"graph_report","arguments":{"project_dir":%q}}}`, repo),
		fmt.Sprintf(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"query_graph","arguments":{"question":"Open","project_dir":%q}}}`, repo),
		fmt.Sprintf(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"shortest_path","arguments":{"source":"pkg::Open","target":"pkg::Close","project_dir":%q}}}`, repo),
		fmt.Sprintf(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"shortest_path","arguments":{"source":"Open","target":"pkg::Close","project_dir":%q}}}`, repo),
		fmt.Sprintf(`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"shortest_path","arguments":{"source":"pkg/a::Open","target":"pkg/b::Open","project_dir":%q}}}`, repo),
		fmt.Sprintf(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"shortest_path","arguments":{"source":"does-not-exist","target":"pkg::Open","project_dir":%q}}}`, repo),
	}
	var out bytes.Buffer
	if err := NewServer(st, strings.NewReader(strings.Join(calls, "\n")+"\n"), &out).Run(); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != len(calls) {
		t.Fatalf("responses = %d, want %d:\n%s", len(lines), len(calls), out.String())
	}
	resultText := func(line string) string {
		t.Helper()
		var resp JSONRPCResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if resp.Error != nil {
			t.Fatalf("rpc error: %+v", resp.Error)
		}
		content, _ := resp.Result.(map[string]any)["content"].([]any)
		if len(content) == 0 {
			t.Fatalf("empty content: %s", line)
		}
		return content[0].(map[string]any)["text"].(string)
	}

	var report graph.AuditReport
	if err := json.Unmarshal([]byte(resultText(lines[1])), &report); err != nil {
		t.Fatalf("report: %v", err)
	}
	if len(report.Communities) == 0 || len(report.SuggestQuestions) != 5 {
		t.Fatalf("report = %+v", report)
	}
	var query graph.SubgraphResult
	if err := json.Unmarshal([]byte(resultText(lines[2])), &query); err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(query.Nodes) == 0 {
		t.Fatal("query returned no nodes")
	}
	var path graph.PathResult
	if err := json.Unmarshal([]byte(resultText(lines[3])), &path); err != nil {
		t.Fatalf("path: %v", err)
	}
	if path.HopCount != 1 || path.PathConfidence < 0.6 {
		t.Fatalf("path = %+v", path)
	}
	var ambiguous map[string]any
	if err := json.Unmarshal([]byte(resultText(lines[4])), &ambiguous); err != nil {
		t.Fatalf("ambiguous: %v", err)
	}
	if ambiguous["error"] != "ambiguous_node" {
		t.Fatalf("ambiguous payload = %+v", ambiguous)
	}
	if candidates, _ := ambiguous["candidates"].([]any); len(candidates) < 2 {
		t.Fatalf("candidates = %v", ambiguous["candidates"])
	}
	var noPath map[string]any
	if err := json.Unmarshal([]byte(resultText(lines[5])), &noPath); err != nil {
		t.Fatalf("no path: %v", err)
	}
	if noPath["error"] != "no_confident_path" {
		t.Fatalf("no path payload = %+v", noPath)
	}
	var invalid JSONRPCResponse
	if err := json.Unmarshal([]byte(lines[6]), &invalid); err != nil {
		t.Fatalf("invalid node: %v", err)
	}
	if invalid.Error == nil || invalid.Error.Code != -32602 {
		t.Fatalf("invalid node response = %+v", invalid)
	}
}

func TestMCPGraphReadsDoNotWrite(t *testing.T) {
	allowGraphProjects(t)
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "pkg", "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module audit\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "pkg", "a", "a.go"), []byte("package a\n\nfunc Alpha() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "graph-read-purity.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	calls := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"query_graph","arguments":{"question":"Alpha","project_dir":%q}}}`, repo),
		fmt.Sprintf(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"graph_report","arguments":{"project_dir":%q,"rebuild":true}}}`, repo),
	}
	var out bytes.Buffer
	if err := NewServer(st, strings.NewReader(strings.Join(calls, "\n")+"\n"), &out).Run(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, graph.GraphPath)); !os.IsNotExist(err) {
		t.Fatalf("a graph read tool wrote %s: %v", graph.GraphPath, err)
	}
}

func TestMCPGraphReadReportsStale(t *testing.T) {
	allowGraphProjects(t)
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
	g, err := graph.Build(context.Background(), repo, nil, graph.BuildOptions{CodeOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.SaveGraph(filepath.Join(repo, graph.GraphPath), g, graph.BuildOptions{}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(repo, "pkg", "b.go"), []byte("package pkg\n\nfunc Beta() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "graph-stale.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	calls := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"query_graph","arguments":{"question":"Alpha","project_dir":%q}}}`, repo),
	}
	var out bytes.Buffer
	if err := NewServer(st, strings.NewReader(strings.Join(calls, "\n")+"\n"), &out).Run(); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("responses = %d: %s", len(lines), out.String())
	}
	var resp JSONRPCResponse
	if err := json.Unmarshal([]byte(lines[1]), &resp); err != nil {
		t.Fatal(err)
	}
	content, _ := resp.Result.(map[string]any)["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("empty response: %s", lines[1])
	}
	text := content[0].(map[string]any)["text"].(string)
	var query graph.SubgraphResult
	if err := json.Unmarshal([]byte(text), &query); err != nil {
		t.Fatal(err)
	}
	if !query.Stale {
		t.Fatalf("query did not report stale: %+v", query)
	}
}

func TestMCPGraphOutputBudgets(t *testing.T) {
	allowGraphProjects(t)
	repo := t.TempDir()
	g := &graph.Graph{Version: 1}
	for i := 0; i < 30; i++ {
		id := fmt.Sprintf("pkg::Node%02d", i)
		g.Nodes = append(g.Nodes, graph.Node{ID: id, Label: fmt.Sprintf("Node%02d", i), Kind: "func", Modality: "code"})
	}
	for group := 0; group < 2; group++ {
		base := group * 3
		for i := base; i < base+3; i++ {
			for j := i + 1; j < base+3; j++ {
				g.Edges = append(g.Edges, graph.Edge{Source: fmt.Sprintf("pkg::Node%02d", i), Target: fmt.Sprintf("pkg::Node%02d", j), Relation: "calls", Confidence: graph.ConfidenceExtracted, ConfidenceScore: 1})
			}
		}
	}
	if err := graph.SaveGraph(filepath.Join(repo, graph.GraphPath), g, graph.BuildOptions{}); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "graph-budget.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	calls := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"query_graph","arguments":{"question":"Node","project_dir":%q,"max_nodes":3,"max_bytes":4096}}}`, repo),
		fmt.Sprintf(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"graph_report","arguments":{"project_dir":%q,"top":2}}}`, repo),
		fmt.Sprintf(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"graph_report","arguments":{"project_dir":%q,"full":true}}}`, repo),
	}
	var out bytes.Buffer
	if err := NewServer(st, strings.NewReader(strings.Join(calls, "\n")+"\n"), &out).Run(); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("responses = %d: %s", len(lines), out.String())
	}
	text := func(line string) string {
		t.Helper()
		var resp JSONRPCResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			t.Fatal(err)
		}
		content, _ := resp.Result.(map[string]any)["content"].([]any)
		if len(content) == 0 {
			t.Fatalf("empty response: %s", line)
		}
		return content[0].(map[string]any)["text"].(string)
	}
	queryText := text(lines[1])
	var query graph.SubgraphResult
	if err := json.Unmarshal([]byte(queryText), &query); err != nil {
		t.Fatal(err)
	}
	if len(query.Nodes) > 3 {
		t.Fatalf("nodes = %d", len(query.Nodes))
	}
	if len(queryText) > 4096 {
		t.Fatalf("query bytes = %d", len(queryText))
	}
	var summary graph.AuditReport
	if err := json.Unmarshal([]byte(text(lines[2])), &summary); err != nil {
		t.Fatal(err)
	}
	if len(summary.Communities) > 2 {
		t.Fatalf("summary communities = %d", len(summary.Communities))
	}
	var full graph.AuditReport
	if err := json.Unmarshal([]byte(text(lines[3])), &full); err != nil {
		t.Fatal(err)
	}
	if len(full.Communities) <= len(summary.Communities) {
		t.Fatalf("full communities = %d, summary = %d", len(full.Communities), len(summary.Communities))
	}
}

func resetGraphCaches() {
	graphFileCache.mu.Lock()
	graphFileCache.entries = nil
	graphFileCache.mu.Unlock()
	graphReportCache.mu.Lock()
	graphReportCache.entries = nil
	graphReportCache.mu.Unlock()
	graphInflight.mu.Lock()
	graphInflight.calls = map[string]*graphBuildCall{}
	graphInflight.mu.Unlock()
}

func TestMCPGraphCacheLRU(t *testing.T) {
	allowGraphProjects(t)
	resetGraphCaches()
	defer resetGraphCaches()
	st, err := store.Open(filepath.Join(t.TempDir(), "graph-lru.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv := NewServer(st, strings.NewReader(""), &bytes.Buffer{})
	repos := make([]string, 0, 5)
	for i := 0; i < 5; i++ {
		repo := t.TempDir()
		g := &graph.Graph{Version: 1, Nodes: []graph.Node{{ID: fmt.Sprintf("n%d", i), Label: fmt.Sprintf("n%d", i), Kind: "func", Modality: "code"}}}
		if err := graph.SaveGraph(filepath.Join(repo, graph.GraphPath), g, graph.BuildOptions{}); err != nil {
			t.Fatal(err)
		}
		repos = append(repos, repo)
		if _, err := srv.loadGraphForTool(repo, false); err != nil {
			t.Fatal(err)
		}
	}
	graphFileCache.mu.Lock()
	if len(graphFileCache.entries) != graphCacheMax {
		t.Fatalf("cache entries = %d, want %d", len(graphFileCache.entries), graphCacheMax)
	}
	graphFileCache.mu.Unlock()
	if _, err := srv.loadGraphForTool(repos[0], false); err != nil {
		t.Fatal(err)
	}
	graphFileCache.mu.Lock()
	if len(graphFileCache.entries) != graphCacheMax {
		t.Fatalf("cache entries after reload = %d", len(graphFileCache.entries))
	}
	graphFileCache.mu.Unlock()
}

func TestMCPGraphConcurrentFirstRun(t *testing.T) {
	allowGraphProjects(t)
	resetGraphCaches()
	defer resetGraphCaches()
	st, err := store.Open(filepath.Join(t.TempDir(), "graph-flight.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv := NewServer(st, strings.NewReader(""), &bytes.Buffer{})
	repo := t.TempDir()
	oldBuild := buildGraphForTool
	defer func() { buildGraphForTool = oldBuild }()
	var count int32
	buildGraphForTool = func(ctx context.Context, abs string, st *store.Store) (*graph.Graph, error) {
		atomic.AddInt32(&count, 1)
		time.Sleep(25 * time.Millisecond)
		return &graph.Graph{Version: 1}, nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := srv.loadGraphForTool(repo, false); err != nil {
				t.Errorf("load: %v", err)
			}
		}()
	}
	wg.Wait()
	if got := atomic.LoadInt32(&count); got != 1 {
		t.Fatalf("builds = %d, want 1", got)
	}
}

func TestMCPGraphReportAnalysisCache(t *testing.T) {
	allowGraphProjects(t)
	resetGraphCaches()
	defer resetGraphCaches()
	repo := t.TempDir()
	g := &graph.Graph{Version: 1, Nodes: []graph.Node{
		{ID: "pkg::A", Label: "A", Kind: "func", Modality: "code"},
		{ID: "pkg::B", Label: "B", Kind: "func", Modality: "code"},
	}}
	if err := graph.SaveGraph(filepath.Join(repo, graph.GraphPath), g, graph.BuildOptions{}); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "graph-report-cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	oldAnalyze := analyzeGraphForTool
	defer func() { analyzeGraphForTool = oldAnalyze }()
	realAnalyze := graph.AnalyzeGraph
	var count int32
	analyzeGraphForTool = func(g *graph.Graph) *graph.AuditReport {
		atomic.AddInt32(&count, 1)
		return realAnalyze(g)
	}
	calls := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"graph_report","arguments":{"project_dir":%q}}}`, repo),
		fmt.Sprintf(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"graph_report","arguments":{"project_dir":%q}}}`, repo),
		fmt.Sprintf(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"graph_report","arguments":{"project_dir":%q,"rebuild":true}}}`, repo),
	}
	var out bytes.Buffer
	if err := NewServer(st, strings.NewReader(strings.Join(calls, "\n")+"\n"), &out).Run(); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&count); got != 2 {
		t.Fatalf("analyze calls = %d, want 2 after rebuild", got)
	}
}

func TestMCPGraphShortestPathCodeOnly(t *testing.T) {
	allowGraphProjects(t)
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
	st, err := store.Open(filepath.Join(t.TempDir(), "graph-code-only.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	calls := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"shortest_path","arguments":{"source":"a","target":"b","code_only":true,"project_dir":%q}}}`, repo),
	}
	var out bytes.Buffer
	if err := NewServer(st, strings.NewReader(strings.Join(calls, "\n")+"\n"), &out).Run(); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("responses = %d: %s", len(lines), out.String())
	}
	var resp JSONRPCResponse
	if err := json.Unmarshal([]byte(lines[1]), &resp); err != nil {
		t.Fatal(err)
	}
	content, _ := resp.Result.(map[string]any)["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("empty response: %s", lines[1])
	}
	var path graph.PathResult
	if err := json.Unmarshal([]byte(content[0].(map[string]any)["text"].(string)), &path); err != nil {
		t.Fatal(err)
	}
	if path.HopCount != 1 {
		t.Fatalf("path = %+v", path)
	}
}

func TestMCPGraphProjectScope(t *testing.T) {
	resetGraphCaches()
	defer resetGraphCaches()
	repo := t.TempDir()
	g := &graph.Graph{Version: 1, Nodes: []graph.Node{{ID: "a", Label: "a", Kind: "func", Modality: "code"}}}
	if err := graph.SaveGraph(filepath.Join(repo, graph.GraphPath), g, graph.BuildOptions{}); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "graph-scope.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv := NewServer(st, strings.NewReader(""), &bytes.Buffer{})
	t.Setenv("SILA_GRAPH_ALLOW_ANY_PROJECT", "0")
	if _, err := srv.loadGraphForTool(repo, false); err == nil {
		t.Fatal("outside project was accepted without opt-in")
	}
	t.Setenv("SILA_GRAPH_ALLOW_ANY_PROJECT", "1")
	if _, err := srv.loadGraphForTool(repo, false); err != nil {
		t.Fatalf("opt-in load: %v", err)
	}
}

func TestMCPGraphBuildTimeout(t *testing.T) {
	resetGraphCaches()
	defer resetGraphCaches()
	t.Setenv("SILA_GRAPH_ALLOW_ANY_PROJECT", "1")
	repo := t.TempDir()
	st, err := store.Open(filepath.Join(t.TempDir(), "graph-timeout.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv := NewServer(st, strings.NewReader(""), &bytes.Buffer{})
	oldTimeout, oldBuild := graphBuildTimeout, buildGraphForTool
	defer func() {
		graphBuildTimeout = oldTimeout
		buildGraphForTool = oldBuild
	}()
	graphBuildTimeout = 20 * time.Millisecond
	buildGraphForTool = func(ctx context.Context, abs string, st *store.Store) (*graph.Graph, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if _, err := srv.loadGraphForTool(repo, false); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
}

func TestGraphBriefSection(t *testing.T) {
	repo := t.TempDir()
	g := &graph.Graph{Version: 1, Nodes: []graph.Node{
		{ID: "a", Label: "a", Kind: "func", Modality: "code"},
		{ID: "b", Label: "b", Kind: "func", Modality: "code"},
	}, Edges: []graph.Edge{
		{Source: "a", Target: "b", Relation: "calls", Confidence: graph.ConfidenceExtracted, ConfidenceScore: 1},
	}}
	if err := graph.SaveGraph(filepath.Join(repo, graph.GraphPath), g, graph.BuildOptions{}); err != nil {
		t.Fatal(err)
	}
	section := graphBriefSection(repo)
	if !strings.Contains(section, "## Graph") || !strings.Contains(section, "2 nodes") {
		t.Fatalf("section = %q", section)
	}
	if _, err := os.Stat(filepath.Join(repo, graph.GraphPath)); err != nil {
		t.Fatal(err)
	}
	if graphBriefSection(t.TempDir()) != "" {
		t.Fatal("absent graph should produce no section")
	}
}

func TestMCPGraphFilesSlice(t *testing.T) {
	allowGraphProjects(t)
	repo := t.TempDir()
	g := &graph.Graph{Version: 1, Nodes: []graph.Node{
		{ID: "file:pkg/a.go", Label: "a.go", Kind: "file", Modality: "code", SourceFile: "pkg/a.go"},
		{ID: "pkg::Alpha", Label: "Alpha", Kind: "func", Modality: "code", SourceFile: "pkg/a.go"},
	}, Edges: []graph.Edge{
		{Source: "file:pkg/a.go", Target: "pkg::Alpha", Relation: "contains", Confidence: graph.ConfidenceExtracted, ConfidenceScore: 1},
	}}
	if err := graph.SaveGraph(filepath.Join(repo, graph.GraphPath), g, graph.BuildOptions{}); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "graph-files-slice.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	calls := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"query_graph","arguments":{"files":["pkg/a.go"],"project_dir":%q}}}`, repo),
	}
	var out bytes.Buffer
	if err := NewServer(st, strings.NewReader(strings.Join(calls, "\n")+"\n"), &out).Run(); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("responses = %d: %s", len(lines), out.String())
	}
	var resp JSONRPCResponse
	if err := json.Unmarshal([]byte(lines[1]), &resp); err != nil {
		t.Fatal(err)
	}
	content, _ := resp.Result.(map[string]any)["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("empty response: %s", lines[1])
	}
	var result graph.SubgraphResult
	if err := json.Unmarshal([]byte(content[0].(map[string]any)["text"].(string)), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Nodes) != 2 {
		t.Fatalf("slice = %+v", result)
	}
}
