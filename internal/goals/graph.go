package goals

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// DependencyGraph models the directed dependency relationships between goals.
// edges[u] = list of prerequisites (goals that u depends on / upstream).
// revEdges[v] = list of dependents (goals that depend on v / downstream).
type DependencyGraph struct {
	nodes    map[string]*Goal
	edges    map[string][]string // forward adjacency: goal -> its prerequisites
	revEdges map[string][]string // reverse adjacency: goal -> goals that depend on it
}

// BuildDependencyGraph constructs a DependencyGraph from a slice of Goal pointers.
func BuildDependencyGraph(goals []*Goal) *DependencyGraph {
	g := &DependencyGraph{
		nodes:    make(map[string]*Goal, len(goals)),
		edges:    make(map[string][]string, len(goals)),
		revEdges: make(map[string][]string, len(goals)),
	}

	for _, goal := range goals {
		if goal == nil || goal.Slug == "" {
			continue
		}
		g.nodes[goal.Slug] = goal
		g.edges[goal.Slug] = append([]string(nil), goal.DependsOn...)
	}

	for slug, deps := range g.edges {
		for _, dep := range deps {
			if dep != "" {
				g.revEdges[dep] = append(g.revEdges[dep], slug)
			}
		}
	}

	return g
}

// BuildDependencyGraphFromSlice constructs a DependencyGraph from a value slice of Goals.
func BuildDependencyGraphFromSlice(goals []Goal) *DependencyGraph {
	ptrList := make([]*Goal, len(goals))
	for i := range goals {
		ptrList[i] = &goals[i]
	}
	return BuildDependencyGraph(ptrList)
}

// ScanGoalsDependencies parses dependencies from all goal.md files without running git scans.
func ScanGoalsDependencies(rootDir string) (*DependencyGraph, error) {
	rootDir = resolveGoalsRootDir(rootDir)
	goalsDir := filepath.Join(rootDir, "goals")
	entries, err := os.ReadDir(goalsDir)
	if err != nil {
		return nil, err
	}
	var rawList []Goal
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), "_") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		slug := e.Name()
		goalPath := filepath.Join(goalsDir, slug, "goal.md")
		var dependsOn []string
		if data, err := os.ReadFile(goalPath); err == nil {
			_, _, _, dependsOn, _, _, _, _ = parseGoalMetadataWithTier(string(data))
		}
		wpPath := filepath.Join(goalsDir, slug, "work-package.json")
		if wpData, err := os.ReadFile(wpPath); err == nil {
			var wp struct {
				DependencyConditions []struct {
					GoalSlug string `json:"goal_slug"`
				} `json:"dependency_conditions"`
			}
			if err := json.Unmarshal(wpData, &wp); err == nil {
				for _, dc := range wp.DependencyConditions {
					gSlug := strings.TrimSpace(dc.GoalSlug)
					if gSlug != "" && !slices.Contains(dependsOn, gSlug) {
						dependsOn = append(dependsOn, gSlug)
					}
				}
			}
		}
		rawList = append(rawList, Goal{
			Slug:      slug,
			DependsOn: dependsOn,
		})
	}
	return BuildDependencyGraphFromSlice(rawList), nil
}

// Nodes returns the internal nodes map.
func (g *DependencyGraph) Nodes() map[string]*Goal {
	return g.nodes
}

// Edges returns the forward adjacency map (goal -> prerequisites).
func (g *DependencyGraph) Edges() map[string][]string {
	return g.edges
}

// RevEdges returns the reverse adjacency map (goal -> dependents).
func (g *DependencyGraph) RevEdges() map[string][]string {
	return g.revEdges
}

type nodeColor int

const (
	colorWhite nodeColor = 0 // Unvisited
	colorGray  nodeColor = 1 // In current DFS recursion stack
	colorBlack nodeColor = 2 // Fully explored
)

// DetectCycles finds all elementary cycles in the dependency graph using 3-color DFS traversal.
// Returns a slice of canonical cycle paths (e.g. [["a", "b", "a"]], [["a", "b", "c", "a"]]).
func (g *DependencyGraph) DetectCycles() [][]string {
	colors := make(map[string]nodeColor, len(g.nodes))
	seenCycles := make(map[string]bool)
	var cycles [][]string

	slugs := g.sortedNodes()

	var dfs func(u string, stack []string)
	dfs = func(u string, stack []string) {
		colors[u] = colorGray
		stack = append(stack, u)

		// Traverse prerequisites in deterministic order
		deps := append([]string(nil), g.edges[u]...)
		sort.Strings(deps)

		for _, v := range deps {
			// External or missing dependencies are treated as unmet prerequisites, not graph cycles
			if _, exists := g.nodes[v]; !exists {
				continue
			}

			if colors[v] == colorGray {
				// Cycle detected: extract path from v in stack up to current node and append v
				cycle := extractCyclePath(stack, v)
				if len(cycle) > 0 {
					canonical := normalizeCycle(cycle)
					key := strings.Join(canonical, "->")
					if !seenCycles[key] {
						seenCycles[key] = true
						cycles = append(cycles, canonical)
					}
				}
			} else if colors[v] == colorWhite {
				dfs(v, stack)
			}
		}

		colors[u] = colorBlack
	}

	for _, s := range slugs {
		if colors[s] == colorWhite {
			dfs(s, nil)
		}
	}

	// Sort cycles for deterministic output
	sort.Slice(cycles, func(i, j int) bool {
		return strings.Join(cycles[i], "->") < strings.Join(cycles[j], "->")
	})

	return cycles
}

// TopologicalSort returns a valid linear execution order where prerequisites precede dependents.
// If the graph contains any cycle, an error is returned.
func (g *DependencyGraph) TopologicalSort() ([]string, error) {
	cycles := g.DetectCycles()
	if len(cycles) > 0 {
		return nil, fmt.Errorf("dependency graph contains cyclical dependencies: %s", formatCycles(cycles))
	}

	// In-degree: number of prerequisite dependencies for each node that exist in g.nodes
	inDegree := make(map[string]int, len(g.nodes))
	for slug := range g.nodes {
		count := 0
		for _, dep := range g.edges[slug] {
			if _, exists := g.nodes[dep]; exists {
				count++
			}
		}
		inDegree[slug] = count
	}

	// Ready queue: nodes with 0 prerequisites
	var ready []string
	for slug, deg := range inDegree {
		if deg == 0 {
			ready = append(ready, slug)
		}
	}
	sort.Strings(ready)

	var result []string
	for len(ready) > 0 {
		// Pop first element
		curr := ready[0]
		ready = ready[1:]
		result = append(result, curr)

		// For each dependent of curr
		dependents := append([]string(nil), g.revEdges[curr]...)
		sort.Strings(dependents)

		for _, dep := range dependents {
			if _, exists := g.nodes[dep]; !exists {
				continue
			}
			inDegree[dep]--
			if inDegree[dep] == 0 {
				// Insert in sorted order
				ready = insertSorted(ready, dep)
			}
		}
	}

	if len(result) < len(g.nodes) {
		return nil, errors.New("dependency graph contains an unresolved cycle")
	}

	return result, nil
}

// TransitivePrerequisites returns all direct and indirect upstream prerequisites of slug in topological order.
func (g *DependencyGraph) TransitivePrerequisites(slug string) []string {
	if _, exists := g.nodes[slug]; !exists {
		return nil
	}

	visited := make(map[string]bool)
	var ancestors []string
	queue := []string{slug}

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]

		for _, dep := range g.edges[curr] {
			if !visited[dep] {
				visited[dep] = true
				if _, exists := g.nodes[dep]; exists {
					ancestors = append(ancestors, dep)
					queue = append(queue, dep)
				}
			}
		}
	}

	if len(ancestors) == 0 {
		return nil
	}

	// Topological sort of ancestor subgraph
	ancestorSet := make(map[string]bool, len(ancestors))
	for _, a := range ancestors {
		ancestorSet[a] = true
	}

	inDegree := make(map[string]int, len(ancestors))
	for _, a := range ancestors {
		count := 0
		for _, dep := range g.edges[a] {
			if ancestorSet[dep] {
				count++
			}
		}
		inDegree[a] = count
	}

	var ready []string
	for _, a := range ancestors {
		if inDegree[a] == 0 {
			ready = append(ready, a)
		}
	}
	sort.Strings(ready)

	var sorted []string
	for len(ready) > 0 {
		curr := ready[0]
		ready = ready[1:]
		sorted = append(sorted, curr)

		for _, dep := range g.revEdges[curr] {
			if ancestorSet[dep] {
				inDegree[dep]--
				if inDegree[dep] == 0 {
					ready = insertSorted(ready, dep)
				}
			}
		}
	}

	if len(sorted) < len(ancestors) {
		// Cycle present in ancestors: fallback to alphabetical order of remaining
		for _, a := range ancestors {
			found := slices.Contains(sorted, a)
			if !found {
				sorted = append(sorted, a)
			}
		}
	}

	return sorted
}

// TransitiveDependents returns all direct and indirect downstream goals unblocked by slug in topological order.
func (g *DependencyGraph) TransitiveDependents(slug string) []string {
	if _, exists := g.nodes[slug]; !exists {
		return nil
	}

	visited := make(map[string]bool)
	var descendants []string
	queue := []string{slug}

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]

		for _, dep := range g.revEdges[curr] {
			if !visited[dep] {
				visited[dep] = true
				if _, exists := g.nodes[dep]; exists {
					descendants = append(descendants, dep)
					queue = append(queue, dep)
				}
			}
		}
	}

	if len(descendants) == 0 {
		return nil
	}

	descSet := make(map[string]bool, len(descendants))
	for _, d := range descendants {
		descSet[d] = true
	}

	inDegree := make(map[string]int, len(descendants))
	for _, d := range descendants {
		count := 0
		for _, prereq := range g.edges[d] {
			if descSet[prereq] {
				count++
			}
		}
		inDegree[d] = count
	}

	var ready []string
	for _, d := range descendants {
		if inDegree[d] == 0 {
			ready = append(ready, d)
		}
	}
	sort.Strings(ready)

	var sorted []string
	for len(ready) > 0 {
		curr := ready[0]
		ready = ready[1:]
		sorted = append(sorted, curr)

		for _, dep := range g.revEdges[curr] {
			if descSet[dep] {
				inDegree[dep]--
				if inDegree[dep] == 0 {
					ready = insertSorted(ready, dep)
				}
			}
		}
	}

	if len(sorted) < len(descendants) {
		for _, d := range descendants {
			found := slices.Contains(sorted, d)
			if !found {
				sorted = append(sorted, d)
			}
		}
	}

	return sorted
}

// RootBlockers finds the earliest un-implemented root goals currently blocking slug.
// A prerequisite is a root blocker if it is not implemented and none of its own prerequisites are un-implemented.
func (g *DependencyGraph) RootBlockers(slug string, statusMap map[string]string) []string {
	if _, exists := g.nodes[slug]; !exists {
		return nil
	}

	// 1. Collect all upstream prerequisites
	visited := make(map[string]bool)
	var allPrereqs []string
	queue := []string{slug}

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]

		for _, dep := range g.edges[curr] {
			if !visited[dep] {
				visited[dep] = true
				allPrereqs = append(allPrereqs, dep)
				if _, exists := g.nodes[dep]; exists {
					queue = append(queue, dep)
				}
			}
		}
	}

	if len(allPrereqs) == 0 {
		return nil
	}

	// 2. Identify un-implemented prerequisites (superseded goals are non-blocking)
	unimplemented := make(map[string]bool)
	for _, p := range allPrereqs {
		st := statusMap[p]
		if st != "implemented" && st != "superseded" {
			unimplemented[p] = true
		}
	}

	if len(unimplemented) == 0 {
		return nil
	}

	// 3. Find root blockers: un-implemented goals whose prerequisites are ALL implemented (or have no prerequisites)
	var rootBlockers []string
	for p := range unimplemented {
		node, exists := g.nodes[p]
		if !exists {
			// Missing prerequisite node on disk is definitely a blocker
			rootBlockers = append(rootBlockers, p)
			continue
		}

		hasUnmetPrereq := false
		for _, dep := range node.DependsOn {
			if unimplemented[dep] {
				hasUnmetPrereq = true
				break
			}
		}

		if !hasUnmetPrereq {
			rootBlockers = append(rootBlockers, p)
		}
	}

	// If cyclic dependencies exist such that every node has unmet prerequisites, return all un-implemented
	if len(rootBlockers) == 0 && len(unimplemented) > 0 {
		for p := range unimplemented {
			rootBlockers = append(rootBlockers, p)
		}
	}

	sort.Strings(rootBlockers)
	return rootBlockers
}

// DownstreamImpactMap computes how many downstream goals each goal unblocks.
func (g *DependencyGraph) DownstreamImpactMap() map[string]int {
	impact := make(map[string]int, len(g.nodes))
	for slug := range g.nodes {
		impact[slug] = len(g.TransitiveDependents(slug))
	}
	return impact
}

// ValidateNoCycle verifies that adding or updating newSlug with dependsOn will not introduce a cycle.
func (g *DependencyGraph) ValidateNoCycle(newSlug string, dependsOn []string) error {
	// Create temporary goal list with newSlug
	var tempGoals []*Goal
	found := false
	for _, node := range g.nodes {
		if node.Slug == newSlug {
			found = true
			copyGoal := *node
			copyGoal.DependsOn = append([]string(nil), dependsOn...)
			tempGoals = append(tempGoals, &copyGoal)
		} else {
			tempGoals = append(tempGoals, node)
		}
	}
	if !found {
		tempGoals = append(tempGoals, &Goal{
			Slug:      newSlug,
			DependsOn: append([]string(nil), dependsOn...),
		})
	}

	tempGraph := BuildDependencyGraph(tempGoals)
	cycles := tempGraph.DetectCycles()
	if len(cycles) > 0 {
		// Find a cycle involving newSlug if possible
		var relevantCycle []string
		for _, c := range cycles {
			if slices.Contains(c, newSlug) {
				relevantCycle = c
			}
			if len(relevantCycle) > 0 {
				break
			}
		}
		if len(relevantCycle) == 0 {
			relevantCycle = cycles[0]
		}
		return fmt.Errorf("cannot create/update goal %q: introduces circular dependency: %s", newSlug, strings.Join(relevantCycle, " -> "))
	}

	return nil
}

func (g *DependencyGraph) sortedNodes() []string {
	slugs := make([]string, 0, len(g.nodes))
	for s := range g.nodes {
		slugs = append(slugs, s)
	}
	sort.Strings(slugs)
	return slugs
}

func extractCyclePath(stack []string, target string) []string {
	for i, node := range stack {
		if node == target {
			cycle := append([]string(nil), stack[i:]...)
			return append(cycle, target)
		}
	}
	return nil
}

func normalizeCycle(cycle []string) []string {
	if len(cycle) <= 1 {
		return cycle
	}
	loop := cycle[:len(cycle)-1]
	if len(loop) <= 1 {
		return cycle
	}

	minIdx := 0
	minVal := loop[0]
	for i := 1; i < len(loop); i++ {
		if loop[i] < minVal {
			minVal = loop[i]
			minIdx = i
		}
	}

	rotated := make([]string, len(loop))
	for i := range loop {
		rotated[i] = loop[(minIdx+i)%len(loop)]
	}
	return append(rotated, rotated[0])
}

func insertSorted(slice []string, s string) []string {
	idx := sort.SearchStrings(slice, s)
	slice = append(slice, "")
	copy(slice[idx+1:], slice[idx:])
	slice[idx] = s
	return slice
}

func formatCycles(cycles [][]string) string {
	var parts []string
	for _, c := range cycles {
		parts = append(parts, strings.Join(c, " -> "))
	}
	return strings.Join(parts, "; ")
}
