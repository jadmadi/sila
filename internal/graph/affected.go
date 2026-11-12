package graph

import (
	"fmt"
	"sort"
	"strings"
)

// BlastRadiusResult is the reverse-reachability result for one target.
type BlastRadiusResult struct {
	Target   string         `json:"target"`
	Depth    int            `json:"depth"`
	Impacted []ImpactedNode `json:"impacted"`
	Stale    bool           `json:"stale,omitempty"`
	GraphAge string         `json:"graph_age,omitempty"`
	BuiltAt  string         `json:"built_at,omitempty"`
}

// ImpactedNode is one caller, test, goal, decision, or memory record affected
// by a change to the target.
type ImpactedNode struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Kind        string `json:"kind"`
	Relation    string `json:"relation"`
	Depth       int    `json:"depth"`
	ViaFile     string `json:"via_file,omitempty"`
	ViaLocation string `json:"via_location,omitempty"`
}

var blastRelations = map[string]bool{
	"calls": true, "implements": true, "tests": true,
	"governs": true, "rationale_for": true, "cites": true,
}

// AffectedOptions controls blast-radius traversal.
type AffectedOptions struct {
	MaxDepth int
	CodeOnly bool
}

// AffectedBy walks incoming graph edges from the target and reports the blast
// radius. Depth-0 type members are included before the reverse BFS.
func AffectedBy(g *Graph, target string, maxDepth int) (*BlastRadiusResult, error) {
	return AffectedByWithOptions(g, target, AffectedOptions{MaxDepth: maxDepth})
}

// AffectedByWithOptions is AffectedBy with an optional code-only filter.
func AffectedByWithOptions(g *Graph, target string, opts AffectedOptions) (*BlastRadiusResult, error) {
	if g == nil {
		return nil, fmt.Errorf("graph is nil")
	}
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = 3
	}
	root, err := resolveNode(g, target)
	if err != nil {
		return nil, err
	}
	if opts.CodeOnly && root.Modality == "memory" {
		return nil, fmt.Errorf("target %q is a memory node; --code-only needs a code target", target)
	}
	result := &BlastRadiusResult{Target: root.ID, Depth: opts.MaxDepth}
	byID := make(map[string]Node, len(g.Nodes))
	for _, node := range g.Nodes {
		byID[node.ID] = node
	}
	seen := map[string]bool{root.ID: true}
	frontier := []string{root.ID}
	if root.Kind == "type" || root.Kind == "interface" {
		for _, node := range g.Nodes {
			if strings.HasPrefix(node.ID, root.ID+".") && !seen[node.ID] {
				seen[node.ID] = true
				frontier = append(frontier, node.ID)
				result.Impacted = append(result.Impacted, ImpactedNode{
					ID: node.ID, Label: node.Label, Kind: node.Kind, Relation: "contains",
					Depth: 0, ViaFile: root.SourceFile,
				})
			}
		}
	}
	for depth := 1; depth <= opts.MaxDepth && len(frontier) > 0; depth++ {
		next := make([]string, 0)
		for _, current := range frontier {
			edges := make([]Edge, 0)
			for _, edge := range g.Edges {
				if edge.Target == current && blastRelations[edge.Relation] {
					edges = append(edges, edge)
				}
			}
			sort.Slice(edges, func(i, j int) bool {
				if edges[i].Source != edges[j].Source {
					return edges[i].Source < edges[j].Source
				}
				return edges[i].Relation < edges[j].Relation
			})
			for _, edge := range edges {
				if seen[edge.Source] {
					continue
				}
				node, ok := byID[edge.Source]
				if !ok {
					continue
				}
				if opts.CodeOnly && node.Modality == "memory" {
					continue
				}
				seen[edge.Source] = true
				result.Impacted = append(result.Impacted, ImpactedNode{
					ID: node.ID, Label: node.Label, Kind: node.Kind, Relation: edge.Relation,
					Depth: depth, ViaFile: edge.SourceFile, ViaLocation: edge.SourceLocation,
				})
				next = append(next, edge.Source)
			}
		}
		frontier = next
	}
	sort.Slice(result.Impacted, func(i, j int) bool {
		if result.Impacted[i].Depth != result.Impacted[j].Depth {
			return result.Impacted[i].Depth < result.Impacted[j].Depth
		}
		return result.Impacted[i].ID < result.Impacted[j].ID
	})
	if result.Impacted == nil {
		result.Impacted = []ImpactedNode{}
	}
	return result, nil
}
