package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jadmadi/sila/internal/graph"
	"github.com/jadmadi/sila/internal/store"
)

var graphBuildTimeout = 30 * time.Second

const (
	graphDefaultMaxNodes  = 200
	graphDefaultMaxBytes  = 64 * 1024
	graphReportDefaultTop = 10
)

const graphCacheMax = 4

type graphCacheEntry struct {
	key string
	g   *graph.Graph
}

var graphFileCache struct {
	mu      sync.Mutex
	entries []graphCacheEntry
}

type graphBuildCall struct {
	done chan struct{}
	g    *graph.Graph
	err  error
}

var graphInflight = struct {
	mu    sync.Mutex
	calls map[string]*graphBuildCall
}{calls: map[string]*graphBuildCall{}}

var buildGraphForTool = func(ctx context.Context, abs string, st *store.Store) (*graph.Graph, error) {
	return graph.Build(ctx, abs, st, graph.BuildOptions{})
}

var analyzeGraphForTool = graph.AnalyzeGraph

type graphReportEntry struct {
	key    string
	report *graph.AuditReport
}

var graphReportCache struct {
	mu      sync.Mutex
	entries []graphReportEntry
}

const graphReportCacheMax = 4

func graphCacheGet(key string) *graph.Graph {
	graphFileCache.mu.Lock()
	defer graphFileCache.mu.Unlock()
	for i, entry := range graphFileCache.entries {
		if entry.key != key {
			continue
		}
		graphFileCache.entries = append(graphFileCache.entries[:i], graphFileCache.entries[i+1:]...)
		graphFileCache.entries = append(graphFileCache.entries, entry)
		return entry.g
	}
	return nil
}

func graphCachePut(key string, g *graph.Graph) {
	if g == nil {
		return
	}
	graphFileCache.mu.Lock()
	defer graphFileCache.mu.Unlock()
	for i, entry := range graphFileCache.entries {
		if entry.key == key {
			graphFileCache.entries = append(graphFileCache.entries[:i], graphFileCache.entries[i+1:]...)
			break
		}
	}
	graphFileCache.entries = append(graphFileCache.entries, graphCacheEntry{key: key, g: g})
	if len(graphFileCache.entries) > graphCacheMax {
		graphFileCache.entries = graphFileCache.entries[len(graphFileCache.entries)-graphCacheMax:]
	}
}

func graphReportCacheGet(key string) *graph.AuditReport {
	graphReportCache.mu.Lock()
	defer graphReportCache.mu.Unlock()
	for i, entry := range graphReportCache.entries {
		if entry.key != key {
			continue
		}
		graphReportCache.entries = append(graphReportCache.entries[:i], graphReportCache.entries[i+1:]...)
		graphReportCache.entries = append(graphReportCache.entries, entry)
		return entry.report
	}
	return nil
}

func graphReportCachePut(key string, report *graph.AuditReport) {
	if report == nil {
		return
	}
	graphReportCache.mu.Lock()
	defer graphReportCache.mu.Unlock()
	for i, entry := range graphReportCache.entries {
		if entry.key == key {
			graphReportCache.entries = append(graphReportCache.entries[:i], graphReportCache.entries[i+1:]...)
			break
		}
	}
	graphReportCache.entries = append(graphReportCache.entries, graphReportEntry{key: key, report: report})
	if len(graphReportCache.entries) > graphReportCacheMax {
		graphReportCache.entries = graphReportCache.entries[len(graphReportCache.entries)-graphReportCacheMax:]
	}
}

func graphReportCacheDelete(key string) {
	graphReportCache.mu.Lock()
	defer graphReportCache.mu.Unlock()
	for i, entry := range graphReportCache.entries {
		if entry.key == key {
			graphReportCache.entries = append(graphReportCache.entries[:i], graphReportCache.entries[i+1:]...)
			return
		}
	}
}

func (s *Server) handleGraphTool(req JSONRPCRequest, name string, rawArgs json.RawMessage) bool {
	switch name {
	case "query_graph":
		var args struct {
			Question    string   `json:"question"`
			ProjectDir  string   `json:"project_dir"`
			MaxDepth    int      `json:"max_depth"`
			TokenBudget int      `json:"token_budget"`
			MaxNodes    int      `json:"max_nodes"`
			MaxBytes    int      `json:"max_bytes"`
			Files       []string `json:"files"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		if args.Question == "" && len(args.Files) == 0 {
			s.sendError(req.ID, -32602, "Invalid params", "question or files is required")
			return true
		}
		g, err := s.loadGraphForTool(args.ProjectDir, false)
		if err != nil {
			s.sendError(req.ID, -32603, "Internal error", err.Error())
			return true
		}
		maxNodes := args.MaxNodes
		if maxNodes <= 0 {
			maxNodes = graphDefaultMaxNodes
		}
		if args.TokenBudget > 0 {
			maxNodes = args.TokenBudget
		}
		maxBytes := args.MaxBytes
		if maxBytes <= 0 {
			maxBytes = graphDefaultMaxBytes
		}
		result, err := graph.SliceGraph(g, args.Question, args.Files, maxNodes)
		if err != nil {
			s.sendError(req.ID, -32603, "Internal error", err.Error())
			return true
		}
		fresh := graph.CheckFreshness(g, graphProjectDir(args.ProjectDir))
		result.Stale, result.GraphAge, result.BuiltAt = fresh.Stale, fresh.GraphAge, fresh.BuiltAt
		result = graph.LimitQueryResult(g, result, maxNodes)
		data, _ := json.Marshal(result)
		for len(data) > maxBytes && len(result.Nodes) > 1 {
			next := len(result.Nodes) * 3 / 4
			if next >= len(result.Nodes) {
				next = len(result.Nodes) - 1
			}
			if next < 1 {
				next = 1
			}
			result = graph.LimitQueryResult(g, result, next)
			data, _ = json.Marshal(result)
		}
		s.sendToolResult(req.ID, result, string(data))
		return true

	case "shortest_path":
		var args struct {
			Source     string `json:"source"`
			Target     string `json:"target"`
			ProjectDir string `json:"project_dir"`
			MaxHops    int    `json:"max_hops"`
			CodeOnly   bool   `json:"code_only"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		if args.Source == "" || args.Target == "" {
			s.sendError(req.ID, -32602, "Invalid params", "source and target are required")
			return true
		}
		g, err := s.loadGraphForTool(args.ProjectDir, false)
		if err != nil {
			s.sendError(req.ID, -32603, "Internal error", err.Error())
			return true
		}
		result, err := graph.WeakestShortestPathWithOptions(g, args.Source, args.Target, graph.PathOptions{MaxHops: args.MaxHops, CodeOnly: args.CodeOnly})
		if err != nil {
			var ambiguous *graph.AmbiguousNodeError
			if errors.As(err, &ambiguous) {
				payload := map[string]any{
					"ok": false, "error": "ambiguous_node",
					"query": ambiguous.Query, "candidates": ambiguous.Candidates,
				}
				data, _ := json.Marshal(payload)
				s.sendToolResult(req.ID, payload, string(data))
				return true
			}
			if errors.Is(err, graph.ErrNoConfidentPath) {
				payload := map[string]any{
					"ok": false, "error": "no_confident_path",
					"source": args.Source, "target": args.Target,
					"message": err.Error(),
				}
				data, _ := json.Marshal(payload)
				s.sendToolResult(req.ID, payload, string(data))
				return true
			}
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return true
		}
		fresh := graph.CheckFreshness(g, graphProjectDir(args.ProjectDir))
		result.Stale, result.GraphAge, result.BuiltAt = fresh.Stale, fresh.GraphAge, fresh.BuiltAt
		formatted, _ := json.MarshalIndent(result, "", "  ")
		s.sendToolResult(req.ID, result, string(formatted))
		return true

	case "graph_report":
		var args struct {
			ProjectDir string `json:"project_dir"`
			Rebuild    bool   `json:"rebuild"`
			Top        int    `json:"top"`
			Full       bool   `json:"full"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		g, err := s.loadGraphForTool(args.ProjectDir, args.Rebuild)
		if err != nil {
			s.sendError(req.ID, -32603, "Internal error", err.Error())
			return true
		}
		_, _, reportKey := graphCacheTarget(args.ProjectDir)
		if args.Rebuild {
			graphReportCacheDelete(reportKey)
		}
		report := graphReportCacheGet(reportKey)
		if report == nil {
			report = analyzeGraphForTool(g)
			graphReportCachePut(reportKey, report)
		}
		copied := *report
		report = &copied
		fresh := graph.CheckFreshness(g, graphProjectDir(args.ProjectDir))
		report.Stale, report.GraphAge, report.BuiltAt = fresh.Stale, fresh.GraphAge, fresh.BuiltAt
		if !args.Full {
			top := args.Top
			if top <= 0 {
				top = graphReportDefaultTop
			}
			limitGraphReport(report, top)
			data, _ := json.Marshal(report)
			for len(data) > graphDefaultMaxBytes && top > 1 {
				top /= 2
				limitGraphReport(report, top)
				data, _ = json.Marshal(report)
			}
			s.sendToolResult(req.ID, report, string(data))
			return true
		}
		data, _ := json.Marshal(report)
		s.sendToolResult(req.ID, report, string(data))
		return true
	}
	return false
}

func (s *Server) loadGraphForTool(projectDir string, rebuild bool) (*graph.Graph, error) {
	abs, path, key := graphCacheTarget(projectDir)
	if !graphProjectAllowed(abs) {
		return nil, fmt.Errorf("project_dir %q is outside the current repository; set SILA_GRAPH_ALLOW_ANY_PROJECT=1 to allow it", abs)
	}
	if !rebuild {
		if cached := graphCacheGet(key); cached != nil {
			return cached, nil
		}
	}
	flightKey := key
	if rebuild {
		flightKey += ":rebuild"
	}
	graphInflight.mu.Lock()
	if call, ok := graphInflight.calls[flightKey]; ok {
		graphInflight.mu.Unlock()
		<-call.done
		return call.g, call.err
	}
	call := &graphBuildCall{done: make(chan struct{})}
	graphInflight.calls[flightKey] = call
	graphInflight.mu.Unlock()
	g, err := s.loadOrBuildGraph(abs, path, rebuild)
	if err == nil {
		graphCachePut(key, g)
	}
	graphInflight.mu.Lock()
	call.g, call.err = g, err
	delete(graphInflight.calls, flightKey)
	close(call.done)
	graphInflight.mu.Unlock()
	return g, err
}

func (s *Server) loadOrBuildGraph(abs, path string, rebuild bool) (*graph.Graph, error) {
	if info, statErr := os.Stat(path); statErr == nil && info.Size() > graph.MaxGraphBytes() {
		return nil, fmt.Errorf("graph %s exceeds the %d-byte cap; raise SILA_GRAPH_MAX_BYTES", path, graph.MaxGraphBytes())
	}
	if !rebuild {
		g, err := graph.LoadGraph(path)
		if err != nil {
			return nil, err
		}
		if g != nil {
			return g, nil
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), graphBuildTimeout)
	defer cancel()
	return buildGraphForTool(ctx, abs, s.store)
}

func graphProjectAllowed(abs string) bool {
	if os.Getenv("SILA_GRAPH_ALLOW_ANY_PROJECT") == "1" {
		return true
	}
	cwd, err := os.Getwd()
	if err != nil {
		return false
	}
	cwdAbs, err := filepath.Abs(cwd)
	if err != nil {
		return false
	}
	return filepath.Clean(cwdAbs) == filepath.Clean(abs)
}

func graphCacheTarget(projectDir string) (abs, path, key string) {
	abs = graphProjectDir(projectDir)
	path = filepath.Join(abs, graph.GraphPath)
	key = path + ":memory"
	if info, err := os.Stat(path); err == nil {
		key = fmt.Sprintf("%s:%d:%d", path, info.ModTime().UnixNano(), info.Size())
	}
	return abs, path, key
}

func graphProjectDir(projectDir string) string {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	if projectDir == "" {
		projectDir = cwd
	}
	abs, err := filepath.Abs(projectDir)
	if err != nil {
		return projectDir
	}
	return abs
}

func limitGraphReport(report *graph.AuditReport, top int) {
	if top <= 0 {
		return
	}
	if len(report.Communities) > top {
		report.Communities = report.Communities[:top]
	}
	if len(report.GodNodes) > top {
		report.GodNodes = report.GodNodes[:top]
	}
	if len(report.SurprisingConnections) > top {
		report.SurprisingConnections = report.SurprisingConnections[:top]
	}
}
