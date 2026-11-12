package dream

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/jadmadi/sila/internal/graph"
)

const (
	hybridGraphMaxBoost = 120.0
	hybridGraphMaxBytes = 50 * 1024 * 1024
	hybridGraphCacheMax = 4
)

type hybridGraphEntry struct {
	path string
	mod  int64
	size int64
	g    *graph.Graph
}

var hybridGraphCache struct {
	mu      sync.Mutex
	entries []hybridGraphEntry
}

// ClassifyQueryAlpha chooses the graph weight: 0.25 for structural queries,
// 0.50 for prose with context paths, and 0.80 for conceptual prose.
func ClassifyQueryAlpha(query string, ctx *RecallContext) float64 {
	if ctx != nil && len(ctx.Symbols) > 0 {
		return 0.25
	}
	if hasStructuralIntent(query) {
		return 0.25
	}
	if ctx != nil && len(ctx.Paths) > 0 {
		return 0.50
	}
	return 0.80
}

func hasStructuralIntent(query string) bool {
	lower := strings.ToLower(query)
	for _, term := range []string{"calls", "caller", "callees", "implements", "depends", "affected", "blast", "path", "imports"} {
		if strings.Contains(lower, term) {
			return true
		}
	}
	if strings.Contains(query, "::") || strings.Contains(query, "/") {
		return true
	}
	for _, suffix := range []string{".go", ".ts", ".py"} {
		if strings.Contains(lower, suffix) {
			return true
		}
	}
	for _, field := range strings.Fields(query) {
		field = strings.Trim(field, ".,;:!?()[]{}'\"")
		if strings.Contains(field, "_") || strings.Contains(field, ".") {
			return true
		}
		for i := 1; i < len(field); i++ {
			if field[i] >= 'A' && field[i] <= 'Z' && field[i-1] >= 'a' && field[i-1] <= 'z' {
				return true
			}
		}
	}
	return false
}

// ApplyHybridGraphBlend adjusts hit scores with bounded structural graph
// proximity. It returns the original slice and false when there is no usable
// graph or no hit has a graph link.
func ApplyHybridGraphBlend(hits []RecallHit, query string, ctx *RecallContext, projectDir string) ([]RecallHit, bool) {
	if len(hits) <= 1 || strings.TrimSpace(projectDir) == "" {
		return hits, false
	}
	g := loadHybridGraph(projectDir)
	if g == nil {
		return hits, false
	}
	alpha := ClassifyQueryAlpha(query, ctx)
	sub, err := graph.QuerySubgraph(g, query, graph.QueryOptions{MaxDepth: 2, TokenBudget: 1500})
	if err != nil || sub == nil {
		return hits, false
	}
	subIDs := make(map[string]bool, len(sub.Nodes))
	for _, node := range sub.Nodes {
		subIDs[node.ID] = true
	}
	subFiles := make(map[string]string, len(sub.Nodes))
	for _, node := range sub.Nodes {
		if node.SourceFile != "" {
			subFiles[filepath.ToSlash(node.SourceFile)] = node.ID
		}
	}
	incident := map[string]float64{}
	for _, edge := range g.Edges {
		if edge.Confidence == graph.ConfidenceAmbiguous {
			continue
		}
		score := edge.ConfidenceScore
		if score <= 0 {
			score = edge.Confidence.Score()
		}
		if score > incident[edge.Source] {
			incident[edge.Source] = score
		}
		if score > incident[edge.Target] {
			incident[edge.Target] = score
		}
	}
	norm := make([]float64, len(hits))
	maxNorm := 0.0
	for i := range hits {
		norm[i] = hybridGraphNorm(hits[i], ctx, g, subIDs, subFiles, incident)
		if norm[i] > maxNorm {
			maxNorm = norm[i]
		}
	}
	if maxNorm == 0 {
		return hits, false
	}
	for i := range hits {
		base, ok := recallTierBase(hits[i].Kind)
		if !ok {
			continue
		}
		withinTier := math.Max(0, hits[i].Score-base)
		blended := alpha*withinTier + (1.0-alpha)*(norm[i]*hybridGraphMaxBoost)
		ceiling := base + recallTierSpan
		if hits[i].Kind == "transcript" {
			ceiling = recallFactBase - 1.0
		}
		hits[i].Score = math.Min(base+blended, ceiling)
	}
	return hits, true
}

func hybridGraphNorm(hit RecallHit, ctx *RecallContext, g *graph.Graph, subIDs map[string]bool, subFiles map[string]string, incident map[string]float64) float64 {
	ids := []string{"lesson:" + hit.ID, "fact:" + hit.ID, "decision:" + hit.ID}
	if hit.MemoryID != "" {
		ids = append(ids, "fact:"+hit.MemoryID, "lesson:"+hit.MemoryID, "decision:"+hit.MemoryID)
	}
	for _, id := range ids {
		if subIDs[id] && incident[id] > 0 {
			return 1.0
		}
	}
	for _, repoFile := range hit.RepoFiles {
		clean := filepath.ToSlash(filepath.Clean(repoFile))
		if nodeID, ok := subFiles[clean]; ok {
			score := incident[nodeID]
			if score <= 0 {
				score = 0.6
			}
			return 0.6 * math.Min(1.0, score)
		}
	}
	if ctx != nil {
		for _, path := range ctx.Paths {
			cleanPath := filepath.ToSlash(filepath.Clean(path))
			for _, repoFile := range hit.RepoFiles {
				clean := filepath.ToSlash(filepath.Clean(repoFile))
				if clean == cleanPath || strings.HasSuffix(cleanPath, "/"+clean) || strings.HasSuffix(clean, "/"+cleanPath) {
					return 1.0
				}
			}
		}
		for _, symbol := range ctx.Symbols {
			for nodeID := range subIDs {
				node := findGraphNode(g, nodeID)
				if node.ID == "" {
					continue
				}
				if node.Label == symbol || node.ID == symbol || strings.HasSuffix(node.ID, "::"+symbol) {
					return 1.0
				}
			}
		}
	}
	return 0
}

func findGraphNode(g *graph.Graph, id string) graph.Node {
	for _, node := range g.Nodes {
		if node.ID == id {
			return node
		}
	}
	return graph.Node{}
}

func loadHybridGraph(projectDir string) *graph.Graph {
	path := filepath.Join(projectDir, graph.GraphPath)
	info, err := os.Stat(path)
	if err != nil || info.Size() <= 0 || info.Size() > hybridGraphMaxBytes {
		return nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil
	}
	mod, size := info.ModTime().UnixNano(), info.Size()
	hybridGraphCache.mu.Lock()
	for i, entry := range hybridGraphCache.entries {
		if entry.path == abs && entry.mod == mod && entry.size == size {
			entry := entry
			hybridGraphCache.entries = append(hybridGraphCache.entries[:i], hybridGraphCache.entries[i+1:]...)
			hybridGraphCache.entries = append(hybridGraphCache.entries, entry)
			hybridGraphCache.mu.Unlock()
			return entry.g
		}
	}
	hybridGraphCache.mu.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var g graph.Graph
	if err := json.Unmarshal(data, &g); err != nil {
		return nil
	}
	g.Sort()
	hybridGraphCache.mu.Lock()
	hybridGraphCache.entries = append(hybridGraphCache.entries, hybridGraphEntry{path: abs, mod: mod, size: size, g: &g})
	if len(hybridGraphCache.entries) > hybridGraphCacheMax {
		hybridGraphCache.entries = hybridGraphCache.entries[len(hybridGraphCache.entries)-hybridGraphCacheMax:]
	}
	hybridGraphCache.mu.Unlock()
	return &g
}
