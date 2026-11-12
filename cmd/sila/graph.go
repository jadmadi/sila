package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/jadmadi/sila/internal/graph"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func runGraph(ui render.UI, dbPath, project string, jsonOut, dryRun, force, ifStale bool, top int, includeAmbiguous, codeOnly bool, maxHops, maxDepth int, sliceFiles, sliceBudget string, args []string) {
	_ = project
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: sila graph <build|report|query|slice|path|affected|stats|doctor> [args]")
		os.Exit(2)
	}
	cwd, err := os.Getwd()
	if err != nil {
		graphFail(jsonOut, err)
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "build":
		if ifStale {
			if existing, loadErr := graph.LoadGraph(filepath.Join(cwd, graph.GraphPath)); loadErr == nil && existing != nil {
				fresh := graph.CheckFreshness(existing, cwd)
				if !fresh.Stale {
					if jsonOut {
						emitJSON(map[string]any{"skipped": true, "path": graph.GraphPath, "built_at": existing.BuiltAt})
						return
					}
					fmt.Fprintf(os.Stdout, "%s Graph is up to date: %s\n", ui.Green("✔"), graph.GraphPath)
					return
				}
			}
		}
		var g *graph.Graph
		cached := false
		graphPath := filepath.Join(cwd, graph.GraphPath)
		cachePath := filepath.Join(cwd, graph.BuildCachePath)
		memoryPrint := graph.MemoryFingerprint(dbPath)
		digest := ""
		if !dryRun && !force {
			if d, digestErr := graph.BuildDigest(cwd); digestErr == nil {
				digest = d
				cache, _ := graph.LoadBuildCache(cachePath)
				if cache != nil && cache.Digest == digest && cache.CodeOnly == codeOnly && cache.Memory == memoryPrint {
					sha, shaErr := graph.FileSHA256(graphPath)
					if shaErr == nil && sha != "" && sha == cache.GraphSHA {
						existing, loadErr := graph.LoadGraph(graphPath)
						if loadErr == nil && existing != nil {
							g = existing
							cached = true
						}
					}
				}
			}
		}
		if g == nil {
			st, storeErr := store.OpenRO(dbPath)
			if storeErr == nil {
				defer st.Close()
			} else {
				st = nil
			}
			built, buildErr := graph.Build(context.Background(), cwd, st, graph.BuildOptions{CodeOnly: codeOnly, AllowShrink: force})
			if buildErr != nil {
				graphFail(jsonOut, buildErr)
			}
			g = built
			if !dryRun {
				if saveErr := graph.SaveGraph(graphPath, g, graph.BuildOptions{CodeOnly: codeOnly, AllowShrink: force}); saveErr != nil {
					graphFail(jsonOut, saveErr)
				}
				if digest == "" {
					digest, _ = graph.BuildDigest(cwd)
				}
				if sha, shaErr := graph.FileSHA256(graphPath); shaErr == nil && sha != "" {
					_ = graph.SaveBuildCache(cachePath, graph.BuildCache{Version: 1, Digest: digest, CodeOnly: codeOnly, Memory: memoryPrint, GraphSHA: sha})
				}
			}
		}
		if jsonOut {
			emitJSON(map[string]any{
				"nodes": len(g.Nodes), "edges": len(g.Edges),
				"path": graph.GraphPath, "dry_run": dryRun, "cached": cached,
				"built_at": g.BuiltAt, "head": g.Head,
			})
			return
		}
		if cached {
			fmt.Fprintf(os.Stdout, "%s Graph cached: %d nodes, %d edges -> %s\n", ui.Green("✔"), len(g.Nodes), len(g.Edges), graph.GraphPath)
			return
		}
		fmt.Fprintf(os.Stdout, "%s Graph built: %d nodes, %d edges -> %s\n", ui.Green("✔"), len(g.Nodes), len(g.Edges), graph.GraphPath)
	case "report":
		g, loadErr := graphLoadOrBuild(dbPath, cwd, force)
		if loadErr != nil {
			graphFail(jsonOut, loadErr)
		}
		report := graph.AnalyzeGraph(g)
		fresh := graph.CheckFreshness(g, cwd)
		warnGraphFreshness(fresh)
		report.Stale, report.GraphAge, report.BuiltAt = fresh.Stale, fresh.GraphAge, fresh.BuiltAt
		if top > 0 {
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
		if jsonOut {
			emitJSON(report)
			return
		}
		fmt.Fprint(os.Stdout, graph.RenderMarkdownReport(report))
	case "query":
		q := strings.TrimSpace(strings.Join(rest, " "))
		if q == "" {
			fmt.Fprintln(os.Stderr, "Usage: sila graph query <terms>")
			os.Exit(2)
		}
		g, loadErr := graphLoadOrBuild(dbPath, cwd, force)
		if loadErr != nil {
			graphFail(jsonOut, loadErr)
		}
		result, queryErr := graph.QuerySubgraph(g, q, graph.QueryOptions{MaxDepth: maxDepth, TokenBudget: 4000})
		if queryErr != nil {
			graphFail(jsonOut, queryErr)
		}
		result = limitGraphQuery(g, result, top)
		fresh := graph.CheckFreshness(g, cwd)
		warnGraphFreshness(fresh)
		result.Stale, result.GraphAge, result.BuiltAt = fresh.Stale, fresh.GraphAge, fresh.BuiltAt
		if jsonOut {
			emitJSON(result)
			return
		}
		printGraphQuery(ui, result, top)
	case "path":
		if len(rest) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: sila graph path <source> <target> [--include-ambiguous] [--max-hops=N] [--code-only]")
			os.Exit(2)
		}
		g, loadErr := graphLoadOrBuild(dbPath, cwd, force)
		if loadErr != nil {
			graphFail(jsonOut, loadErr)
		}
		result, pathErr := graph.WeakestShortestPathWithOptions(g, rest[0], rest[1], graph.PathOptions{
			MaxHops: maxHops, IncludeAmbiguous: includeAmbiguous, CodeOnly: codeOnly,
		})
		if pathErr != nil {
			if errors.Is(pathErr, graph.ErrNoConfidentPath) {
				graphFail(jsonOut, pathErr)
			}
			var ambiguous *graph.AmbiguousNodeError
			if errors.As(pathErr, &ambiguous) {
				graphFail(jsonOut, pathErr)
			}
			graphFail(jsonOut, pathErr)
		}
		fresh := graph.CheckFreshness(g, cwd)
		warnGraphFreshness(fresh)
		result.Stale, result.GraphAge, result.BuiltAt = fresh.Stale, fresh.GraphAge, fresh.BuiltAt
		if jsonOut {
			emitJSON(result)
			return
		}
		fmt.Fprintf(os.Stdout, "%s Path: %d hops, confidence %.3f, weakest hop %.3f\n", ui.Green("✔"), result.HopCount, result.PathConfidence, result.MinHopConfidence)
		for _, node := range result.Nodes {
			fmt.Fprintf(os.Stdout, "  %s (%s)\n", ui.Bold(node.ID), node.Kind)
		}
	case "affected":
		if len(rest) < 1 {
			fmt.Fprintln(os.Stderr, "Usage: sila graph affected <target> [--max-depth=N] [--code-only]")
			os.Exit(2)
		}
		g, loadErr := graphLoadOrBuild(dbPath, cwd, force)
		if loadErr != nil {
			graphFail(jsonOut, loadErr)
		}
		result, affectedErr := graph.AffectedByWithOptions(g, rest[0], graph.AffectedOptions{MaxDepth: maxDepth, CodeOnly: codeOnly})
		if affectedErr != nil {
			graphFail(jsonOut, affectedErr)
		}
		if top > 0 && len(result.Impacted) > top {
			result.Impacted = result.Impacted[:top]
		}
		fresh := graph.CheckFreshness(g, cwd)
		warnGraphFreshness(fresh)
		result.Stale, result.GraphAge, result.BuiltAt = fresh.Stale, fresh.GraphAge, fresh.BuiltAt
		if jsonOut {
			emitJSON(result)
			return
		}
		fmt.Fprintf(os.Stdout, "%s Blast radius for %s: %d impacted\n", ui.Cyan("·"), ui.Bold(result.Target), len(result.Impacted))
		limit := len(result.Impacted)
		if top > 0 && top < limit {
			limit = top
		}
		for _, node := range result.Impacted[:limit] {
			via := node.ViaFile
			if node.ViaLocation != "" {
				via += ":" + node.ViaLocation
			}
			fmt.Fprintf(os.Stdout, "  depth %d  %-14s %-28s %s\n", node.Depth, node.Relation, node.ID, via)
		}
	case "slice":
		query := strings.TrimSpace(strings.Join(rest, " "))
		var files []string
		for _, file := range strings.Split(sliceFiles, ",") {
			if trimmed := strings.TrimSpace(file); trimmed != "" {
				files = append(files, trimmed)
			}
		}
		if query == "" && len(files) == 0 {
			fmt.Fprintln(os.Stderr, "Usage: sila graph slice [<terms>] [--files=a.go,b.go] [--budget=N]")
			os.Exit(2)
		}
		budget := 200
		if n, err := strconv.Atoi(strings.TrimSpace(sliceBudget)); err == nil && n > 0 {
			budget = n
		}
		g, loadErr := graphLoadOrBuild(dbPath, cwd, force)
		if loadErr != nil {
			graphFail(jsonOut, loadErr)
		}
		result, sliceErr := graph.SliceGraph(g, query, files, budget)
		if sliceErr != nil {
			graphFail(jsonOut, sliceErr)
		}
		fresh := graph.CheckFreshness(g, cwd)
		warnGraphFreshness(fresh)
		result.Stale, result.GraphAge, result.BuiltAt = fresh.Stale, fresh.GraphAge, fresh.BuiltAt
		if jsonOut {
			emitJSON(result)
			return
		}
		printGraphQuery(ui, result, top)
	case "stats":
		path := filepath.Join(cwd, graph.GraphPath)
		g, loadErr := graph.LoadGraph(path)
		if loadErr != nil {
			graphFail(jsonOut, loadErr)
		}
		stats := graph.ComputeStats(g, cwd, path)
		if jsonOut {
			emitJSON(stats)
			return
		}
		printGraphStats(ui, stats)
	case "doctor":
		path := filepath.Join(cwd, graph.GraphPath)
		g, loadErr := graph.LoadGraph(path)
		if loadErr != nil {
			graphFail(jsonOut, loadErr)
		}
		report := graph.GraphDoctor(g, cwd, path)
		if jsonOut {
			emitJSON(report)
			return
		}
		printGraphDoctor(ui, report)
	default:
		fmt.Fprintf(os.Stderr, "unknown graph subcommand %q\n", sub)
		fmt.Fprintln(os.Stderr, "Usage: sila graph <build|report|query|slice|path|affected|stats|doctor> [args]")
		os.Exit(2)
	}
}

func graphLoadOrBuild(dbPath, cwd string, force bool) (*graph.Graph, error) {
	path := filepath.Join(cwd, graph.GraphPath)
	g, err := graph.LoadGraph(path)
	if err != nil {
		return nil, err
	}
	if g != nil {
		return g, nil
	}
	st, err := store.OpenRO(dbPath)
	if err == nil {
		defer st.Close()
	} else {
		st = nil
	}
	g, err = graph.Build(context.Background(), cwd, st, graph.BuildOptions{AllowShrink: force})
	if err != nil {
		return nil, err
	}
	if err := graph.SaveGraph(path, g, graph.BuildOptions{AllowShrink: force}); err != nil {
		return nil, err
	}
	return g, nil
}

func printGraphQuery(ui render.UI, result *graph.SubgraphResult, top int) {
	type row struct {
		id     string
		score  float64
		kind   string
		label  string
		source string
	}
	rows := make([]row, 0, len(result.Nodes))
	for _, node := range result.Nodes {
		rows = append(rows, row{id: node.ID, score: result.Scores[node.ID], kind: node.Kind, label: node.Label, source: node.SourceFile})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].score != rows[j].score {
			return rows[i].score > rows[j].score
		}
		if queryNodeRank(rows[i].kind, rows[i].label, rows[i].source) != queryNodeRank(rows[j].kind, rows[j].label, rows[j].source) {
			return queryNodeRank(rows[i].kind, rows[i].label, rows[i].source) > queryNodeRank(rows[j].kind, rows[j].label, rows[j].source)
		}
		return rows[i].id < rows[j].id
	})
	limit := len(rows)
	if top > 0 && top < limit {
		limit = top
	}
	fmt.Fprintf(os.Stdout, "%s Graph query %q: %d nodes, %d edges\n", ui.Cyan("·"), result.Query, len(rows), len(result.Edges))
	fmt.Fprintf(os.Stdout, "  %-8s %-32s %-10s %s\n", "SCORE", "ID", "KIND", "LABEL")
	for _, row := range rows[:limit] {
		fmt.Fprintf(os.Stdout, "  %8.3f %-32s %-10s %s\n", row.score, row.id, row.kind, row.label)
	}
}

func graphFail(jsonOut bool, err error) {
	if jsonOut {
		emitErrorEnvelope("GRAPH_ERROR", err.Error(), "sila graph build")
	} else {
		fmt.Fprintf(os.Stderr, "sila graph: %v\n", err)
	}
	os.Exit(1)
}

func limitGraphQuery(g *graph.Graph, result *graph.SubgraphResult, top int) *graph.SubgraphResult {
	if result == nil || top <= 0 || len(result.Nodes) <= top {
		return result
	}
	sort.Slice(result.Nodes, func(i, j int) bool {
		left, right := result.Scores[result.Nodes[i].ID], result.Scores[result.Nodes[j].ID]
		if left != right {
			return left > right
		}
		if queryNodeRank(result.Nodes[i].Kind, result.Nodes[i].Label, result.Nodes[i].SourceFile) != queryNodeRank(result.Nodes[j].Kind, result.Nodes[j].Label, result.Nodes[j].SourceFile) {
			return queryNodeRank(result.Nodes[i].Kind, result.Nodes[i].Label, result.Nodes[i].SourceFile) > queryNodeRank(result.Nodes[j].Kind, result.Nodes[j].Label, result.Nodes[j].SourceFile)
		}
		return result.Nodes[i].ID < result.Nodes[j].ID
	})
	kept := make(map[string]bool, top)
	result.Nodes = result.Nodes[:top]
	for _, node := range result.Nodes {
		kept[node.ID] = true
	}
	result.Edges = graph.CompleteInducedEdges(g, kept)
	return result
}

func queryNodeRank(kind, label, source string) int {
	switch kind {
	case "func", "method":
		if strings.HasSuffix(source, "_test.go") || strings.HasPrefix(label, "Test") || strings.HasPrefix(label, "Benchmark") || strings.HasPrefix(label, "Example") {
			return 2
		}
		return 4
	case "type", "interface":
		return 3
	case "test":
		return 1
	default:
		return 1
	}
}

func warnGraphFreshness(f graph.Freshness) {
	if !f.Stale {
		return
	}
	age := f.GraphAge
	if age == "" {
		age = "unknown age"
	}
	fmt.Fprintf(os.Stderr, "warning: graph is stale (%s); run sila graph build\n", age)
}

func printGraphStats(ui render.UI, stats graph.Stats) {
	if stats.Missing {
		fmt.Fprintf(os.Stdout, "%s graph is not built: %s; run sila graph build\n", ui.Cyan("·"), stats.Path)
		return
	}
	fmt.Fprintf(os.Stdout, "%s Graph %s: %d nodes, %d edges, %d bytes\n", ui.Cyan("·"), stats.Path, stats.Nodes, stats.Edges, stats.SizeBytes)
	fmt.Fprintf(os.Stdout, "  files %d/%d, orphans %d, stale %t, built %s\n", stats.Files, stats.RepoFiles, stats.Orphans, stats.Stale, stats.BuiltAt)
	for _, kind := range sortedStatKeys(stats.NodesByKind) {
		fmt.Fprintf(os.Stdout, "  node %-12s %d\n", kind, stats.NodesByKind[kind])
	}
	for _, relation := range sortedStatKeys(stats.EdgesByRelation) {
		fmt.Fprintf(os.Stdout, "  edge %-12s %d\n", relation, stats.EdgesByRelation[relation])
	}
}

func printGraphDoctor(ui render.UI, report graph.DoctorReport) {
	fmt.Fprintf(os.Stdout, "%s Graph doctor: %s\n", ui.Cyan("·"), report.Status)
	for _, check := range report.Checks {
		label := "PASS"
		if check.Status == "warn" {
			label = "WARN"
		}
		fmt.Fprintf(os.Stdout, "  %-4s %-12s %s", label, check.Name, check.Detail)
		if check.Fix != "" {
			fmt.Fprintf(os.Stdout, " (fix: %s)", check.Fix)
		}
		fmt.Fprintln(os.Stdout)
	}
}

func sortedStatKeys(values map[string]int) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
