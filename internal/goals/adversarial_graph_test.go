package goals

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// ============================================================================
// 1. CIRCULAR DEPENDENCY GRAPHS
// ============================================================================

func TestAdversarial_2NodeCycle(t *testing.T) {
	goals := []*Goal{
		{Slug: "node-x", DependsOn: []string{"node-y"}},
		{Slug: "node-y", DependsOn: []string{"node-x"}},
	}
	graph := BuildDependencyGraph(goals)

	cycles := graph.DetectCycles()
	if len(cycles) != 1 {
		t.Fatalf("expected 1 cycle, got %d: %v", len(cycles), cycles)
	}
	expected := []string{"node-x", "node-y", "node-x"}
	if !reflect.DeepEqual(cycles[0], expected) {
		t.Fatalf("expected normalized cycle %v, got %v", expected, cycles[0])
	}

	_, err := graph.TopologicalSort()
	if err == nil {
		t.Fatal("TopologicalSort must fail on 2-node cycle")
	}

	// ValidateNoCycle prevents closing the loop
	subGraph := BuildDependencyGraph([]*Goal{
		{Slug: "node-x", DependsOn: []string{"node-y"}},
		{Slug: "node-y", DependsOn: []string{}},
	})
	if err := subGraph.ValidateNoCycle("node-y", []string{"node-x"}); err == nil {
		t.Fatal("expected ValidateNoCycle to reject closing 2-node cycle")
	}
}

func TestAdversarial_3NodeCycle(t *testing.T) {
	// A depends on B, B depends on C, C depends on A
	goals := []*Goal{
		{Slug: "alpha", DependsOn: []string{"beta"}},
		{Slug: "beta", DependsOn: []string{"gamma"}},
		{Slug: "gamma", DependsOn: []string{"alpha"}},
	}
	graph := BuildDependencyGraph(goals)

	cycles := graph.DetectCycles()
	if len(cycles) != 1 {
		t.Fatalf("expected 1 cycle, got %d: %v", len(cycles), cycles)
	}
	expected := []string{"alpha", "beta", "gamma", "alpha"}
	if !reflect.DeepEqual(cycles[0], expected) {
		t.Fatalf("expected normalized cycle %v, got %v", expected, cycles[0])
	}

	_, err := graph.TopologicalSort()
	if err == nil {
		t.Fatal("TopologicalSort must fail on 3-node cycle")
	}
}

func TestAdversarial_SelfReferencingGoal(t *testing.T) {
	goals := []*Goal{
		{Slug: "ouroboros", DependsOn: []string{"ouroboros"}},
	}
	graph := BuildDependencyGraph(goals)

	cycles := graph.DetectCycles()
	if len(cycles) != 1 {
		t.Fatalf("expected 1 self-cycle, got %d: %v", len(cycles), cycles)
	}
	expected := []string{"ouroboros", "ouroboros"}
	if !reflect.DeepEqual(cycles[0], expected) {
		t.Fatalf("expected %v, got %v", expected, cycles[0])
	}

	_, err := graph.TopologicalSort()
	if err == nil {
		t.Fatal("TopologicalSort must fail on self-referencing goal")
	}

	// ValidateNoCycle on self loop
	emptyGraph := BuildDependencyGraph([]*Goal{})
	if err := emptyGraph.ValidateNoCycle("ouroboros", []string{"ouroboros"}); err == nil {
		t.Fatal("ValidateNoCycle must reject self-referencing dependency")
	}
}

func TestAdversarial_OverlappingCycles(t *testing.T) {
	// Cycle 1: A -> B -> C -> A
	// Cycle 2: B -> D -> B (overlapping at B)
	goals := []*Goal{
		{Slug: "node-a", DependsOn: []string{"node-b"}},
		{Slug: "node-b", DependsOn: []string{"node-c", "node-d"}},
		{Slug: "node-c", DependsOn: []string{"node-a"}},
		{Slug: "node-d", DependsOn: []string{"node-b"}},
		{Slug: "node-e", DependsOn: []string{"node-d"}}, // downstream dependent
	}
	graph := BuildDependencyGraph(goals)

	cycles := graph.DetectCycles()
	if len(cycles) < 2 {
		t.Fatalf("expected at least 2 cycles, got %d: %v", len(cycles), cycles)
	}

	cycleMap := make(map[string]bool)
	for _, c := range cycles {
		cycleMap[strings.Join(c, "->")] = true
	}

	// Should detect both cycles
	if !cycleMap["node-a->node-b->node-c->node-a"] {
		t.Errorf("missing cycle node-a->node-b->node-c->node-a in %v", cycles)
	}
	if !cycleMap["node-b->node-d->node-b"] {
		t.Errorf("missing cycle node-b->node-d->node-b in %v", cycles)
	}

	_, err := graph.TopologicalSort()
	if err == nil {
		t.Fatal("TopologicalSort must fail on overlapping cycles")
	}
}

func TestAdversarial_Large50NodeCyclicNetwork(t *testing.T) {
	// 50-node ring: node-00 -> node-01 -> node-02 -> ... -> node-49 -> node-00
	numNodes := 50
	goals := make([]*Goal, numNodes)
	for i := range numNodes {
		slug := fmt.Sprintf("node-%02d", i)
		nextSlug := fmt.Sprintf("node-%02d", (i+1)%numNodes)
		goals[i] = &Goal{
			Slug:      slug,
			DependsOn: []string{nextSlug},
		}
	}

	graph := BuildDependencyGraph(goals)
	cycles := graph.DetectCycles()

	if len(cycles) != 1 {
		t.Fatalf("expected 1 large cycle across 50 nodes, got %d: %v", len(cycles), cycles)
	}

	cycle := cycles[0]
	if len(cycle) != 51 { // 50 nodes + closing node
		t.Fatalf("expected cycle length 51, got %d", len(cycle))
	}
	if cycle[0] != "node-00" || cycle[50] != "node-00" {
		t.Fatalf("expected normalized cycle starting and ending at node-00, got %s -> %s", cycle[0], cycle[50])
	}

	_, err := graph.TopologicalSort()
	if err == nil {
		t.Fatal("TopologicalSort must fail on 50-node cyclic network")
	}
}

func TestAdversarial_5Disconnected10NodeCycles(t *testing.T) {
	// 5 independent disconnected 10-node cycles in one graph (50 nodes total)
	numClusters := 5
	nodesPerCluster := 10
	var goals []*Goal

	for c := range numClusters {
		for i := range nodesPerCluster {
			slug := fmt.Sprintf("cluster%d-node%d", c, i)
			nextSlug := fmt.Sprintf("cluster%d-node%d", c, (i+1)%nodesPerCluster)
			goals = append(goals, &Goal{
				Slug:      slug,
				DependsOn: []string{nextSlug},
			})
		}
	}

	graph := BuildDependencyGraph(goals)
	cycles := graph.DetectCycles()

	if len(cycles) != numClusters {
		t.Fatalf("expected %d cycles, got %d: %v", numClusters, len(cycles), cycles)
	}

	for c := range numClusters {
		expectedFirst := fmt.Sprintf("cluster%d-node0", c)
		found := false
		for _, cyc := range cycles {
			if cyc[0] == expectedFirst && cyc[len(cyc)-1] == expectedFirst && len(cyc) == nodesPerCluster+1 {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected cycle for cluster %d starting with %s, but not found in %v", c, expectedFirst, cycles)
		}
	}
}

// ============================================================================
// 2. DEEP TRANSITIVE DEPENDENCY CHAINS (20+ Levels Deep)
// ============================================================================

func TestAdversarial_DeepTransitiveChain20Levels(t *testing.T) {
	// Level 19 depends on Level 18 ... Level 1 depends on Level 0 (root)
	// chain: level-19 -> level-18 -> ... -> level-00
	depth := 20
	goals := make([]*Goal, depth)
	for i := range depth {
		slug := fmt.Sprintf("level-%02d", i)
		var deps []string
		if i > 0 {
			deps = []string{fmt.Sprintf("level-%02d", i-1)}
		}
		goals[i] = &Goal{
			Slug:      slug,
			DependsOn: deps,
		}
	}

	graph := BuildDependencyGraph(goals)

	// 1. Cycle detection: 0 cycles
	cycles := graph.DetectCycles()
	if len(cycles) != 0 {
		t.Fatalf("expected 0 cycles in linear chain, got %v", cycles)
	}

	// 2. Topological sort: must be level-00, level-01, ..., level-19
	order, err := graph.TopologicalSort()
	if err != nil {
		t.Fatalf("TopologicalSort failed: %v", err)
	}
	if len(order) != depth {
		t.Fatalf("expected %d elements, got %d", depth, len(order))
	}
	for i := range depth {
		expectedSlug := fmt.Sprintf("level-%02d", i)
		if order[i] != expectedSlug {
			t.Errorf("order[%d] expected %s, got %s", i, expectedSlug, order[i])
		}
	}

	// 3. Transitive prerequisites for deepest node (level-19)
	prereqs19 := graph.TransitivePrerequisites("level-19")
	if len(prereqs19) != 19 {
		t.Fatalf("expected 19 transitive prerequisites for level-19, got %d: %v", len(prereqs19), prereqs19)
	}
	for i := range 19 {
		expectedSlug := fmt.Sprintf("level-%02d", i)
		if prereqs19[i] != expectedSlug {
			t.Errorf("prereqs19[%d] expected %s, got %s", i, expectedSlug, prereqs19[i])
		}
	}

	// 4. Transitive dependents for root node (level-00)
	deps0 := graph.TransitiveDependents("level-00")
	if len(deps0) != 19 {
		t.Fatalf("expected 19 transitive dependents for level-00, got %d: %v", len(deps0), deps0)
	}
	for i := range 19 {
		expectedSlug := fmt.Sprintf("level-%02d", i+1)
		if deps0[i] != expectedSlug {
			t.Errorf("deps0[%d] expected %s, got %s", i, expectedSlug, deps0[i])
		}
	}

	// 5. Downstream impact map
	impact := graph.DownstreamImpactMap()
	for i := range depth {
		slug := fmt.Sprintf("level-%02d", i)
		expectedImpact := (depth - 1) - i
		if impact[slug] != expectedImpact {
			t.Errorf("impact[%s] expected %d, got %d", slug, expectedImpact, impact[slug])
		}
	}

	// 6. Step-by-step unblocking and Root Blockers simulation
	statusMap := make(map[string]string)
	for i := range depth {
		statusMap[fmt.Sprintf("level-%02d", i)] = "pending"
	}

	// Initially, root blocker for all level-k (k > 0) is level-00
	for k := 1; k < depth; k++ {
		target := fmt.Sprintf("level-%02d", k)
		rb := graph.RootBlockers(target, statusMap)
		if !reflect.DeepEqual(rb, []string{"level-00"}) {
			t.Fatalf("initially root blocker for %s expected ['level-00'], got %v", target, rb)
		}
	}

	// Progressively implement level-00 up to level-17
	for k := 0; k < depth-2; k++ {
		statusMap[fmt.Sprintf("level-%02d", k)] = "implemented"

		// Next unblocked / root blocker for level-19 should be level-(k+1)
		nextRoot := fmt.Sprintf("level-%02d", k+1)
		rb := graph.RootBlockers("level-19", statusMap)
		if !reflect.DeepEqual(rb, []string{nextRoot}) {
			t.Fatalf("after implementing level-%02d, expected root blocker for level-19 to be [%s], got %v", k, nextRoot, rb)
		}
	}

	// Implement level-18: level-19 has no root blockers left (fully unblocked)
	statusMap["level-18"] = "implemented"
	rbFinal := graph.RootBlockers("level-19", statusMap)
	if len(rbFinal) != 0 {
		t.Fatalf("expected 0 root blockers for level-19 after all prerequisites implemented, got %v", rbFinal)
	}
}

// ============================================================================
// 3. DIAMOND GRAPHS & MULTI-LATTICE TOPOLOGIES
// ============================================================================

func TestAdversarial_DiamondGraphUnblocking(t *testing.T) {
	// Standard Diamond:
	//       A
	//      / \
	//     B   C
	//      \ /
	//       D
	// (D depends on B and C; B and C depend on A; A depends on nothing)
	goals := []*Goal{
		{Slug: "diamond-a", DependsOn: []string{}},
		{Slug: "diamond-b", DependsOn: []string{"diamond-a"}},
		{Slug: "diamond-c", DependsOn: []string{"diamond-a"}},
		{Slug: "diamond-d", DependsOn: []string{"diamond-b", "diamond-c"}},
	}
	graph := BuildDependencyGraph(goals)

	cycles := graph.DetectCycles()
	if len(cycles) != 0 {
		t.Fatalf("diamond graph should have 0 cycles, got %v", cycles)
	}

	// Downstream impact:
	// A unblocks B, C, D -> impact 3
	// B unblocks D -> impact 1
	// C unblocks D -> impact 1
	// D unblocks 0 -> impact 0
	impact := graph.DownstreamImpactMap()
	if impact["diamond-a"] != 3 {
		t.Errorf("expected diamond-a impact 3, got %d", impact["diamond-a"])
	}
	if impact["diamond-b"] != 1 {
		t.Errorf("expected diamond-b impact 1, got %d", impact["diamond-b"])
	}
	if impact["diamond-c"] != 1 {
		t.Errorf("expected diamond-c impact 1, got %d", impact["diamond-c"])
	}
	if impact["diamond-d"] != 0 {
		t.Errorf("expected diamond-d impact 0, got %d", impact["diamond-d"])
	}

	// State 1: All pending
	statusMap := map[string]string{
		"diamond-a": "pending",
		"diamond-b": "pending",
		"diamond-c": "pending",
		"diamond-d": "pending",
	}
	rbD := graph.RootBlockers("diamond-d", statusMap)
	if !reflect.DeepEqual(rbD, []string{"diamond-a"}) {
		t.Fatalf("State 1: expected root blocker ['diamond-a'], got %v", rbD)
	}

	// State 2: A implemented, B and C pending
	// Both B and C must be root blockers for D
	statusMap["diamond-a"] = "implemented"
	rbD2 := graph.RootBlockers("diamond-d", statusMap)
	expected2 := []string{"diamond-b", "diamond-c"}
	if !reflect.DeepEqual(rbD2, expected2) {
		t.Fatalf("State 2: expected root blockers %v, got %v", expected2, rbD2)
	}

	// State 3: A and B implemented, C pending -> D is still blocked by C
	statusMap["diamond-b"] = "implemented"
	rbD3 := graph.RootBlockers("diamond-d", statusMap)
	if !reflect.DeepEqual(rbD3, []string{"diamond-c"}) {
		t.Fatalf("State 3: expected root blocker ['diamond-c'], got %v", rbD3)
	}

	// State 4: A and C implemented, B pending -> D is still blocked by B
	statusMap["diamond-b"] = "pending"
	statusMap["diamond-c"] = "implemented"
	rbD4 := graph.RootBlockers("diamond-d", statusMap)
	if !reflect.DeepEqual(rbD4, []string{"diamond-b"}) {
		t.Fatalf("State 4: expected root blocker ['diamond-b'], got %v", rbD4)
	}

	// State 5: A, B, and C all implemented -> D is unblocked (0 root blockers)
	statusMap["diamond-b"] = "implemented"
	statusMap["diamond-c"] = "implemented"
	rbD5 := graph.RootBlockers("diamond-d", statusMap)
	if len(rbD5) != 0 {
		t.Fatalf("State 5: expected 0 root blockers for D, got %v", rbD5)
	}
}

func TestAdversarial_ChainedDoubleDiamond(t *testing.T) {
	// Double diamond / hourglass:
	//       A
	//      / \
	//     B   C
	//      \ /
	//       D
	//      / \
	//     E   F
	//      \ /
	//       G
	goals := []*Goal{
		{Slug: "node-a", DependsOn: []string{}},
		{Slug: "node-b", DependsOn: []string{"node-a"}},
		{Slug: "node-c", DependsOn: []string{"node-a"}},
		{Slug: "node-d", DependsOn: []string{"node-b", "node-c"}},
		{Slug: "node-e", DependsOn: []string{"node-d"}},
		{Slug: "node-f", DependsOn: []string{"node-d"}},
		{Slug: "node-g", DependsOn: []string{"node-e", "node-f"}},
	}
	graph := BuildDependencyGraph(goals)

	cycles := graph.DetectCycles()
	if len(cycles) != 0 {
		t.Fatalf("expected 0 cycles in double diamond, got %v", cycles)
	}

	// Impact of A should be all 6 other nodes
	impact := graph.DownstreamImpactMap()
	if impact["node-a"] != 6 {
		t.Errorf("expected node-a impact 6, got %d", impact["node-a"])
	}
	if impact["node-d"] != 3 { // E, F, G
		t.Errorf("expected node-d impact 3, got %d", impact["node-d"])
	}

	// Transitive prerequisites of G should be A, B, C, D, E, F
	prereqsG := graph.TransitivePrerequisites("node-g")
	if len(prereqsG) != 6 {
		t.Fatalf("expected 6 prerequisites for G, got %d: %v", len(prereqsG), prereqsG)
	}

	// Topological sort validity
	order, err := graph.TopologicalSort()
	if err != nil {
		t.Fatalf("TopologicalSort failed: %v", err)
	}
	idx := make(map[string]int)
	for i, s := range order {
		idx[s] = i
	}
	if !(idx["node-a"] < idx["node-b"] && idx["node-a"] < idx["node-c"]) {
		t.Errorf("node-a must precede node-b and node-c: %v", order)
	}
	if !(idx["node-b"] < idx["node-d"] && idx["node-c"] < idx["node-d"]) {
		t.Errorf("node-b and node-c must precede node-d: %v", order)
	}
	if !(idx["node-d"] < idx["node-e"] && idx["node-d"] < idx["node-f"]) {
		t.Errorf("node-d must precede node-e and node-f: %v", order)
	}
	if !(idx["node-e"] < idx["node-g"] && idx["node-f"] < idx["node-g"]) {
		t.Errorf("node-e and node-f must precede node-g: %v", order)
	}
}

// ============================================================================
// 4. EDGE CASES & ADVERSARIAL STRESS
// ============================================================================

func TestAdversarial_MissingAndExternalPrerequisites(t *testing.T) {
	// node-a depends on "external-lib" and "non-existent" which are not goal nodes
	goals := []*Goal{
		{Slug: "node-a", DependsOn: []string{"external-lib", "non-existent"}},
		{Slug: "node-b", DependsOn: []string{"node-a"}},
	}
	graph := BuildDependencyGraph(goals)

	// External deps should not trigger cycle detection
	cycles := graph.DetectCycles()
	if len(cycles) != 0 {
		t.Fatalf("expected 0 cycles with missing prerequisites, got %v", cycles)
	}

	// TopologicalSort should succeed for nodes in graph
	order, err := graph.TopologicalSort()
	if err != nil {
		t.Fatalf("TopologicalSort failed: %v", err)
	}
	expected := []string{"node-a", "node-b"}
	if !reflect.DeepEqual(order, expected) {
		t.Fatalf("expected order %v, got %v", expected, order)
	}

	// RootBlockers: missing dependencies are recognized as blockers
	statusMap := map[string]string{
		"node-a": "pending",
		"node-b": "pending",
	}
	rbA := graph.RootBlockers("node-a", statusMap)
	sort.Strings(rbA)
	expectedBlockers := []string{"external-lib", "non-existent"}
	if !reflect.DeepEqual(rbA, expectedBlockers) {
		t.Fatalf("expected missing dependencies as root blockers for node-a, got %v", rbA)
	}
}

func TestAdversarial_EmptyAndSingleNodeGraphs(t *testing.T) {
	// Empty graph
	emptyGraph := BuildDependencyGraph(nil)
	if len(emptyGraph.DetectCycles()) != 0 {
		t.Fatal("empty graph must have 0 cycles")
	}
	order, err := emptyGraph.TopologicalSort()
	if err != nil || len(order) != 0 {
		t.Fatalf("empty graph sort failed: %v, %v", order, err)
	}
	if len(emptyGraph.TransitivePrerequisites("any")) != 0 {
		t.Fatal("expected nil/empty prereqs")
	}
	if len(emptyGraph.TransitiveDependents("any")) != 0 {
		t.Fatal("expected nil/empty dependents")
	}
	if len(emptyGraph.RootBlockers("any", nil)) != 0 {
		t.Fatal("expected nil/empty root blockers")
	}

	// Single node
	singleGraph := BuildDependencyGraph([]*Goal{{Slug: "lonely"}})
	if len(singleGraph.DetectCycles()) != 0 {
		t.Fatal("single node graph must have 0 cycles")
	}
	orderSingle, err := singleGraph.TopologicalSort()
	if err != nil || !reflect.DeepEqual(orderSingle, []string{"lonely"}) {
		t.Fatalf("single node sort failed: %v, %v", orderSingle, err)
	}
}

func TestAdversarial_WideTree100Nodes(t *testing.T) {
	// Root node with 100 parallel leaf dependents
	numLeaves := 100
	goals := []*Goal{
		{Slug: "root-hub", DependsOn: []string{}},
	}
	for i := range numLeaves {
		goals = append(goals, &Goal{
			Slug:      fmt.Sprintf("leaf-%03d", i),
			DependsOn: []string{"root-hub"},
		})
	}

	graph := BuildDependencyGraph(goals)

	cycles := graph.DetectCycles()
	if len(cycles) != 0 {
		t.Fatalf("expected 0 cycles in wide tree, got %v", cycles)
	}

	impact := graph.DownstreamImpactMap()
	if impact["root-hub"] != numLeaves {
		t.Fatalf("expected root-hub to unblock %d leaves, got %d", numLeaves, impact["root-hub"])
	}

	order, err := graph.TopologicalSort()
	if err != nil {
		t.Fatalf("TopologicalSort failed on wide tree: %v", err)
	}
	if order[0] != "root-hub" {
		t.Fatalf("root-hub must be first in topological order, got %s", order[0])
	}
}
