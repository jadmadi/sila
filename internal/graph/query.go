package graph

import (
	"container/heap"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
)

const trigramGuardFrac = 0.10

// QueryOptions controls subgraph expansion.
type QueryOptions struct {
	MaxDepth    int
	TokenBudget int
}

// SubgraphResult is a scored and expanded subgraph.
type SubgraphResult struct {
	Query    string             `json:"query"`
	Seeds    []string           `json:"seeds,omitempty"`
	Terms    []string           `json:"terms,omitempty"`
	Nodes    []Node             `json:"nodes"`
	Edges    []Edge             `json:"edges"`
	Scores   map[string]float64 `json:"scores,omitempty"`
	Stale    bool               `json:"stale,omitempty"`
	GraphAge string             `json:"graph_age,omitempty"`
	BuiltAt  string             `json:"built_at,omitempty"`
}

// PathOptions controls shortest-path search.
type PathOptions struct {
	MaxHops           int
	MinPathConfidence float64
	IncludeAmbiguous  bool
	CodeOnly          bool
}

// codePathRelations are the structural relations a code-only path may follow.
var codePathRelations = map[string]bool{
	"calls": true, "contains": true, "tests": true, "implements": true,
}

// PathResult is one confidence-weighted path.
type PathResult struct {
	Nodes            []Node  `json:"nodes"`
	Edges            []Edge  `json:"edges"`
	HopCount         int     `json:"hop_count"`
	MinHopConfidence float64 `json:"min_hop_confidence"`
	PathConfidence   float64 `json:"path_confidence"`
	Stale            bool    `json:"stale,omitempty"`
	GraphAge         string  `json:"graph_age,omitempty"`
	BuiltAt          string  `json:"built_at,omitempty"`
}

// AmbiguousNodeError reports a bare query that matches several nodes.
type AmbiguousNodeError struct {
	Query      string
	Candidates []string
}

func (e *AmbiguousNodeError) Error() string {
	return fmt.Sprintf("ambiguous node %q matches: %s", e.Query, strings.Join(e.Candidates, ", "))
}

// ErrNoConfidentPath means no path met the confidence floor.
var ErrNoConfidentPath = errors.New("no path meets the confidence floor")

const querySeedLimit = 10

var relationalWords = map[string]bool{
	"call": true, "calls": true, "caller": true, "callers": true, "callee": true, "callees": true,
	"depends": true, "depend": true, "governs": true, "govern": true, "cite": true, "cites": true,
	"cited": true, "tests": true, "test": true, "implements": true, "implement": true,
	"rationale": true, "for": true,
}

// QuerySubgraph scores nodes against the query and expands the best seeds.
func QuerySubgraph(g *Graph, q string, opts QueryOptions) (*SubgraphResult, error) {
	if g == nil {
		return nil, fmt.Errorf("graph is nil")
	}
	terms := queryTerms(q)
	if len(terms) == 0 {
		terms = queryFallbackTerms(q)
	}
	if len(terms) == 0 {
		return &SubgraphResult{Query: q}, nil
	}
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = 2
	}
	if opts.TokenBudget <= 0 {
		opts.TokenBudget = 4000
	}
	texts := make([]string, len(g.Nodes))
	df := map[string]int{}
	trigrams := make([][]string, len(g.Nodes))
	for i, node := range g.Nodes {
		texts[i] = strings.ToLower(node.Label + " " + node.SourceFile + " " + strings.Join(node.Findings, " "))
		seen := map[string]bool{}
		for _, tg := range textTrigrams(texts[i]) {
			if !seen[tg] {
				seen[tg] = true
				df[tg]++
			}
		}
		trigrams[i] = keysOf(seen)
	}
	total := len(g.Nodes)
	queryTrigrams := textTrigrams(strings.ToLower(q))
	allowed := map[string]bool{}
	for _, tg := range queryTrigrams {
		if df[tg] <= int(float64(total)*trigramGuardFrac) {
			allowed[tg] = true
		}
	}
	candidates := make([]int, 0, total)
	for i := range g.Nodes {
		if len(allowed) == 0 {
			candidates = append(candidates, i)
			continue
		}
		for _, tg := range trigrams[i] {
			if allowed[tg] {
				candidates = append(candidates, i)
				break
			}
		}
	}
	if len(candidates) == 0 {
		for i := range g.Nodes {
			candidates = append(candidates, i)
		}
	}

	idf := func(term string) float64 {
		count := 0
		for _, text := range texts {
			if strings.Contains(text, term) {
				count++
			}
		}
		return math.Log(1 + float64(total)/(1+float64(count)))
	}
	intent := hasIntentKeyword(q)
	scores := make(map[int]float64, len(candidates))
	for _, i := range candidates {
		node := g.Nodes[i]
		label := strings.ToLower(node.Label)
		idText := strings.ToLower(node.ID)
		file := strings.ToLower(node.SourceFile)
		findings := strings.ToLower(strings.Join(node.Findings, " "))
		sum := 0.0
		matched := 0
		for _, term := range terms {
			weight := 0.0
			if strings.Contains(label, term) {
				weight += 1.0
			}
			if strings.Contains(idText, term) {
				weight += 1.0
			}
			if strings.Contains(file, term) {
				weight += 0.8
			}
			if strings.Contains(findings, term) {
				weight += 0.6
			}
			if weight > 0 {
				matched++
				sum += idf(term) * weight
			}
		}
		if matched == 0 {
			continue
		}
		coverage := math.Pow(float64(matched)/float64(len(terms)), 2)
		score := sum * coverage
		if (node.Kind == "test" || node.Kind == "doc") && !intent {
			score *= 0.3
		}
		if qualifiedTailBonus(q, node) {
			score += 0.5
		}
		scores[i] = score
	}
	type rankedNode struct {
		index int
		score float64
	}
	kindPriority := func(node Node) int {
		switch node.Kind {
		case "func", "method":
			if strings.HasSuffix(node.SourceFile, "_test.go") || strings.HasPrefix(node.Label, "Test") || strings.HasPrefix(node.Label, "Benchmark") || strings.HasPrefix(node.Label, "Example") {
				return 1
			}
			return 3
		case "type", "interface":
			return 2
		default:
			return 1
		}
	}
	ranked := make([]rankedNode, 0, len(scores))
	for i, score := range scores {
		ranked = append(ranked, rankedNode{index: i, score: score})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		left, right := g.Nodes[ranked[i].index], g.Nodes[ranked[j].index]
		if kindPriority(left) != kindPriority(right) {
			return kindPriority(left) > kindPriority(right)
		}
		return left.ID < right.ID
	})
	seedSet := map[string]bool{}
	for _, row := range ranked {
		if len(seedSet) >= querySeedLimit {
			break
		}
		seedSet[g.Nodes[row.index].ID] = true
	}
	for _, term := range terms {
		for _, row := range ranked {
			if strings.Contains(texts[row.index], term) {
				seedSet[g.Nodes[row.index].ID] = true
				break
			}
		}
	}
	if len(seedSet) == 0 {
		best, bestScore := -1, 0.0
		for i, score := range scores {
			if score > bestScore || (best == -1 && score == bestScore) {
				best, bestScore = i, score
			}
		}
		if best >= 0 {
			seedSet[g.Nodes[best].ID] = true
		}
	}

	byID := make(map[string]Node, len(g.Nodes))
	for _, node := range g.Nodes {
		byID[node.ID] = node
	}
	adjacency := map[string][]string{}
	for _, edge := range g.Edges {
		adjacency[edge.Source] = append(adjacency[edge.Source], edge.Target)
		adjacency[edge.Target] = append(adjacency[edge.Target], edge.Source)
	}
	selected := map[string]bool{}
	queue := make([]string, 0, len(seedSet))
	for id := range seedSet {
		queue = append(queue, id)
	}
	sort.Strings(queue)
	depth := map[string]int{}
	for _, id := range queue {
		depth[id] = 0
	}
	for len(queue) > 0 && len(selected) < opts.TokenBudget {
		id := queue[0]
		queue = queue[1:]
		if selected[id] || depth[id] > opts.MaxDepth {
			continue
		}
		selected[id] = true
		neighbors := append([]string(nil), adjacency[id]...)
		sort.Strings(neighbors)
		for _, neighbor := range neighbors {
			if !selected[neighbor] && depth[neighbor] == 0 && neighbor != id {
				depth[neighbor] = depth[id] + 1
				queue = append(queue, neighbor)
			}
		}
	}
	result := &SubgraphResult{Query: q, Terms: terms, Scores: map[string]float64{}}
	for i, node := range g.Nodes {
		if selected[node.ID] {
			if score, ok := scores[i]; ok {
				result.Scores[node.ID] = round3(score)
			}
		}
	}
	for id := range seedSet {
		result.Seeds = append(result.Seeds, id)
	}
	sort.Strings(result.Seeds)
	for id := range selected {
		result.Nodes = append(result.Nodes, byID[id])
	}
	sort.Slice(result.Nodes, func(i, j int) bool { return result.Nodes[i].ID < result.Nodes[j].ID })
	for _, edge := range g.Edges {
		if selected[edge.Source] && selected[edge.Target] {
			result.Edges = append(result.Edges, edge)
		}
	}
	result.Edges = FoldEdge(result.Edges)
	if len(result.Seeds) > 20 {
		result.Seeds = result.Seeds[:20]
	}
	if len(result.Terms) > 20 {
		result.Terms = result.Terms[:20]
	}
	return result, nil
}

// LimitQueryResult keeps the top-scoring nodes and their induced edges.
func LimitQueryResult(g *Graph, result *SubgraphResult, top int) *SubgraphResult {
	if result == nil || top <= 0 || len(result.Nodes) <= top {
		return result
	}
	sort.Slice(result.Nodes, func(i, j int) bool {
		left, right := result.Scores[result.Nodes[i].ID], result.Scores[result.Nodes[j].ID]
		if left != right {
			return left > right
		}
		if limitNodeRank(result.Nodes[i]) != limitNodeRank(result.Nodes[j]) {
			return limitNodeRank(result.Nodes[i]) > limitNodeRank(result.Nodes[j])
		}
		return result.Nodes[i].ID < result.Nodes[j].ID
	})
	kept := make(map[string]bool, top)
	result.Nodes = result.Nodes[:top]
	for _, node := range result.Nodes {
		kept[node.ID] = true
	}
	for id := range result.Scores {
		if !kept[id] {
			delete(result.Scores, id)
		}
	}
	result.Edges = CompleteInducedEdges(g, kept)
	return result
}

func limitNodeRank(node Node) int {
	switch node.Kind {
	case "func", "method":
		if strings.HasSuffix(node.SourceFile, "_test.go") || strings.HasPrefix(node.Label, "Test") || strings.HasPrefix(node.Label, "Benchmark") || strings.HasPrefix(node.Label, "Example") {
			return 2
		}
		return 4
	case "type", "interface":
		return 3
	default:
		return 1
	}
}

// CompleteInducedEdges keeps every edge whose two endpoints are selected.
func CompleteInducedEdges(g *Graph, selected map[string]bool) []Edge {
	if g == nil {
		return nil
	}
	var edges []Edge
	for _, edge := range g.Edges {
		if selected[edge.Source] && selected[edge.Target] {
			edges = append(edges, edge)
		}
	}
	return FoldEdge(edges)
}

func queryTerms(q string) []string {
	fields := strings.FieldsFunc(strings.ToLower(q), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.')
	})
	var out []string
	seen := map[string]bool{}
	for _, field := range fields {
		field = strings.Trim(field, ".")
		if field == "" || relationalWords[field] || seen[field] {
			continue
		}
		seen[field] = true
		out = append(out, field)
	}
	return out
}

// queryFallbackTerms keeps a bare relational word as a content term. Without
// this, a query for a method named Call is dropped before scoring.
func queryFallbackTerms(q string) []string {
	fields := strings.FieldsFunc(strings.ToLower(q), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.')
	})
	var out []string
	seen := map[string]bool{}
	for _, field := range fields {
		field = strings.Trim(field, ".")
		if field == "" || seen[field] || queryFallbackStopwords[field] {
			continue
		}
		seen[field] = true
		out = append(out, field)
	}
	return out
}

var queryFallbackStopwords = map[string]bool{
	"a": true, "an": true, "the": true, "of": true, "to": true, "in": true,
	"on": true, "for": true, "and": true, "or": true, "is": true, "it": true,
}

func textTrigrams(text string) []string {
	var out []string
	for i := 0; i+3 <= len(text); i++ {
		out = append(out, text[i:i+3])
	}
	return out
}

func keysOf(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func hasIntentKeyword(q string) bool {
	lower := strings.ToLower(q)
	for _, keyword := range []string{"test", "spec", "doc", "readme", "goal", "decision"} {
		if strings.Contains(lower, keyword) {
			return true
		}
	}
	return false
}

func qualifiedTailBonus(q string, node Node) bool {
	for _, field := range strings.Fields(q) {
		if idx := strings.LastIndex(field, "."); idx >= 0 && idx+1 < len(field) {
			tail := strings.ToLower(field[idx+1:])
			if tail != "" && (strings.EqualFold(node.Label, tail) || strings.HasSuffix(node.ID, "."+tail) || strings.HasSuffix(node.ID, "::"+tail)) {
				return true
			}
		}
	}
	return false
}

// WeakestShortestPath keeps the original four-argument path API.
func WeakestShortestPath(g *Graph, srcQuery, dstQuery string, maxHops int) (*PathResult, error) {
	return WeakestShortestPathWithOptions(g, srcQuery, dstQuery, PathOptions{MaxHops: maxHops})
}

// WeakestShortestPathWithOptions finds the most reliable path within the
// confidence floor using additive negative-log-confidence costs.
func WeakestShortestPathWithOptions(g *Graph, srcQuery, dstQuery string, opts PathOptions) (*PathResult, error) {
	if g == nil {
		return nil, fmt.Errorf("graph is nil")
	}
	if opts.MaxHops <= 0 {
		opts.MaxHops = 6
	}
	if opts.MinPathConfidence <= 0 {
		if opts.IncludeAmbiguous {
			opts.MinPathConfidence = 0
		} else {
			opts.MinPathConfidence = 0.60
		}
	}
	src, err := resolveNode(g, srcQuery)
	if err != nil {
		return nil, err
	}
	dst, err := resolveNode(g, dstQuery)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]Node, len(g.Nodes))
	for _, node := range g.Nodes {
		byID[node.ID] = node
	}
	type graphHop struct {
		to   string
		edge Edge
	}
	adjacency := make(map[string][]graphHop, len(g.Nodes))
	for _, edge := range g.Edges {
		if edge.Confidence == ConfidenceAmbiguous && !opts.IncludeAmbiguous {
			continue
		}
		if opts.CodeOnly {
			source, sourceOK := byID[edge.Source]
			target, targetOK := byID[edge.Target]
			if !sourceOK || !targetOK || source.Modality == "memory" || target.Modality == "memory" || !codePathRelations[edge.Relation] {
				continue
			}
		}
		adjacency[edge.Source] = append(adjacency[edge.Source], graphHop{to: edge.Target, edge: edge})
		adjacency[edge.Target] = append(adjacency[edge.Target], graphHop{to: edge.Source, edge: edge})
	}
	best := map[string]pathState{}
	start := pathState{node: src.ID, cost: 0, hops: 0, minC: 1, nodes: []string{src.ID}}
	queue := &pathQueue{start}
	heap.Init(queue)
	for queue.Len() > 0 {
		state := heap.Pop(queue).(pathState)
		if state.hops > opts.MaxHops {
			continue
		}
		if prev, ok := best[state.node]; ok && state.cost > prev.cost+1e-12 {
			continue
		}
		best[state.node] = state
		if state.node == dst.ID && state.hops > 0 {
			break
		}
		for _, hop := range adjacency[state.node] {
			edge := hop.edge
			neighbor := hop.to
			c := edge.ConfidenceScore
			if c <= 0 {
				switch edge.Confidence {
				case ConfidenceExtracted:
					c = 1.0
				case ConfidenceInferred:
					c = 0.75
				case ConfidenceAmbiguous:
					c = 0.25
				}
			}
			c = math.Max(1e-6, math.Min(1.0, c))
			next := pathState{
				node: neighbor, cost: state.cost - math.Log(c), hops: state.hops + 1,
				minC:  math.Min(state.minC, c),
				nodes: append(append([]string(nil), state.nodes...), neighbor),
				edges: append(append([]Edge(nil), state.edges...), edge),
			}
			if next.hops > opts.MaxHops {
				continue
			}
			if prev, ok := best[neighbor]; ok {
				if next.cost > prev.cost+1e-9 {
					continue
				}
				if math.Abs(next.cost-prev.cost) <= 1e-9 {
					if next.hops > prev.hops || (next.hops == prev.hops && next.minC < prev.minC-1e-12) {
						continue
					}
				}
			}
			heap.Push(queue, next)
		}
	}
	final, ok := best[dst.ID]
	if !ok || final.hops == 0 {
		return nil, ErrNoConfidentPath
	}
	pathConfidence := math.Exp(-final.cost)
	if pathConfidence < opts.MinPathConfidence {
		return nil, ErrNoConfidentPath
	}
	result := &PathResult{HopCount: final.hops, MinHopConfidence: final.minC, PathConfidence: round3(pathConfidence)}
	for _, id := range final.nodes {
		result.Nodes = append(result.Nodes, byID[id])
	}
	result.Edges = FoldEdge(final.edges)
	return result, nil
}

type pathState struct {
	node  string
	cost  float64
	hops  int
	minC  float64
	nodes []string
	edges []Edge
}

type pathQueue []pathState

func (q pathQueue) Len() int      { return len(q) }
func (q pathQueue) Swap(i, j int) { q[i], q[j] = q[j], q[i] }
func (q pathQueue) Less(i, j int) bool {
	if math.Abs(q[i].cost-q[j].cost) > 1e-9 {
		return q[i].cost < q[j].cost
	}
	if q[i].hops != q[j].hops {
		return q[i].hops < q[j].hops
	}
	if math.Abs(q[i].minC-q[j].minC) > 1e-12 {
		return q[i].minC > q[j].minC
	}
	return strings.Join(q[i].nodes, "\x00") < strings.Join(q[j].nodes, "\x00")
}
func (q *pathQueue) Push(x any) { *q = append(*q, x.(pathState)) }
func (q *pathQueue) Pop() any {
	old := *q
	n := len(old)
	item := old[n-1]
	*q = old[:n-1]
	return item
}

func resolveNode(g *Graph, query string) (Node, error) {
	trimmed := strings.TrimSpace(query)
	for _, node := range g.Nodes {
		if node.ID == trimmed {
			return node, nil
		}
	}
	var matches []Node
	for _, node := range g.Nodes {
		if strings.EqualFold(node.Label, trimmed) || strings.HasSuffix(node.ID, "::"+trimmed) || strings.HasSuffix(node.ID, "."+trimmed) {
			matches = append(matches, node)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		sort.Slice(matches, func(i, j int) bool { return matches[i].ID < matches[j].ID })
		ids := make([]string, 0, len(matches))
		for _, node := range matches {
			ids = append(ids, node.ID)
		}
		return Node{}, &AmbiguousNodeError{Query: query, Candidates: ids}
	}
	return Node{}, fmt.Errorf("no node matches %q", query)
}

// ErrNoSliceInput reports a slice call without a query or file.
var ErrNoSliceInput = errors.New("graph slice needs a query or file")

// SliceGraph returns a bounded neighbourhood for a query and/or files.
func SliceGraph(g *Graph, query string, files []string, budget int) (*SubgraphResult, error) {
	if g == nil {
		return nil, fmt.Errorf("graph is nil")
	}
	query = strings.TrimSpace(query)
	if query == "" && len(files) == 0 {
		return nil, ErrNoSliceInput
	}
	if budget <= 0 {
		budget = 200
	}
	result := &SubgraphResult{Query: query, Scores: map[string]float64{}}
	if query != "" {
		queried, err := QuerySubgraph(g, query, QueryOptions{MaxDepth: 2, TokenBudget: budget})
		if err != nil {
			return nil, err
		}
		result = queried
	}
	selected := make(map[string]bool, len(result.Nodes))
	for _, node := range result.Nodes {
		selected[node.ID] = true
	}
	byID := make(map[string]Node, len(g.Nodes))
	for _, node := range g.Nodes {
		byID[node.ID] = node
	}
	add := func(node Node, score float64) bool {
		if selected[node.ID] || len(result.Nodes) >= budget {
			return false
		}
		selected[node.ID] = true
		result.Nodes = append(result.Nodes, node)
		result.Scores[node.ID] = score
		return true
	}
	for _, file := range files {
		clean := filepath.ToSlash(filepath.Clean(strings.TrimSpace(file)))
		if clean == "" || clean == "." {
			continue
		}
		for _, node := range g.Nodes {
			if node.Kind != "file" {
				continue
			}
			idPath := strings.TrimPrefix(node.ID, "file:")
			source := filepath.ToSlash(filepath.Clean(node.SourceFile))
			if idPath != clean && source != clean && !strings.HasSuffix(source, "/"+clean) && !strings.HasSuffix(clean, "/"+source) {
				continue
			}
			add(node, 1.0)
			for _, edge := range g.Edges {
				if edge.Source != node.ID || edge.Relation != "contains" {
					continue
				}
				if child, ok := byID[edge.Target]; ok {
					add(child, 1.0)
				}
			}
		}
	}
	result = LimitQueryResult(g, result, budget)
	result.Edges = CompleteInducedEdges(g, selected)
	sort.Strings(result.Seeds)
	if len(result.Seeds) > 20 {
		result.Seeds = result.Seeds[:20]
	}
	return result, nil
}
