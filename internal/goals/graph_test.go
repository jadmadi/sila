package goals

import (
	"reflect"
	"strings"
	"testing"
)

func TestDependencyGraphCycleDetection2Node(t *testing.T) {
	goals := []*Goal{
		{Slug: "goal-a", DependsOn: []string{"goal-b"}},
		{Slug: "goal-b", DependsOn: []string{"goal-a"}},
	}

	graph := BuildDependencyGraph(goals)
	cycles := graph.DetectCycles()

	if len(cycles) != 1 {
		t.Fatalf("expected 1 cycle, got %d: %v", len(cycles), cycles)
	}

	expected := []string{"goal-a", "goal-b", "goal-a"}
	if !reflect.DeepEqual(cycles[0], expected) {
		t.Fatalf("expected cycle %v, got %v", expected, cycles[0])
	}

	_, err := graph.TopologicalSort()
	if err == nil {
		t.Fatal("expected error from TopologicalSort on 2-node cyclic graph")
	}
}

func TestDependencyGraphCycleDetection3Node(t *testing.T) {
	goals := []*Goal{
		{Slug: "node-c", DependsOn: []string{"node-a"}},
		{Slug: "node-a", DependsOn: []string{"node-b"}},
		{Slug: "node-b", DependsOn: []string{"node-c"}},
	}

	graph := BuildDependencyGraph(goals)
	cycles := graph.DetectCycles()

	if len(cycles) != 1 {
		t.Fatalf("expected 1 cycle, got %d: %v", len(cycles), cycles)
	}

	expected := []string{"node-a", "node-b", "node-c", "node-a"}
	if !reflect.DeepEqual(cycles[0], expected) {
		t.Fatalf("expected cycle %v, got %v", expected, cycles[0])
	}
}

func TestDependencyGraphSelfCycle(t *testing.T) {
	goals := []*Goal{
		{Slug: "self-loop", DependsOn: []string{"self-loop"}},
	}

	graph := BuildDependencyGraph(goals)
	cycles := graph.DetectCycles()

	if len(cycles) != 1 {
		t.Fatalf("expected 1 self-cycle, got %d: %v", len(cycles), cycles)
	}

	expected := []string{"self-loop", "self-loop"}
	if !reflect.DeepEqual(cycles[0], expected) {
		t.Fatalf("expected %v, got %v", expected, cycles[0])
	}
}

func TestDependencyGraphDeepLinearChain(t *testing.T) {
	// E -> D -> C -> B -> A
	goals := []*Goal{
		{Slug: "goal-a", DependsOn: []string{}},
		{Slug: "goal-b", DependsOn: []string{"goal-a"}},
		{Slug: "goal-c", DependsOn: []string{"goal-b"}},
		{Slug: "goal-d", DependsOn: []string{"goal-c"}},
		{Slug: "goal-e", DependsOn: []string{"goal-d"}},
	}

	graph := BuildDependencyGraph(goals)

	cycles := graph.DetectCycles()
	if len(cycles) != 0 {
		t.Fatalf("expected no cycles, got %v", cycles)
	}

	order, err := graph.TopologicalSort()
	if err != nil {
		t.Fatalf("unexpected sort error: %v", err)
	}

	expectedOrder := []string{"goal-a", "goal-b", "goal-c", "goal-d", "goal-e"}
	if !reflect.DeepEqual(order, expectedOrder) {
		t.Fatalf("expected order %v, got %v", expectedOrder, order)
	}

	// Transitive prerequisites for E
	prereqsE := graph.TransitivePrerequisites("goal-e")
	expectedPrereqsE := []string{"goal-a", "goal-b", "goal-c", "goal-d"}
	if !reflect.DeepEqual(prereqsE, expectedPrereqsE) {
		t.Fatalf("expected prereqs for E %v, got %v", expectedPrereqsE, prereqsE)
	}

	// Transitive dependents for A
	depsA := graph.TransitiveDependents("goal-a")
	expectedDepsA := []string{"goal-b", "goal-c", "goal-d", "goal-e"}
	if !reflect.DeepEqual(depsA, expectedDepsA) {
		t.Fatalf("expected deps for A %v, got %v", expectedDepsA, depsA)
	}

	// Downstream impact map
	impact := graph.DownstreamImpactMap()
	if impact["goal-a"] != 4 {
		t.Errorf("expected goal-a impact 4, got %d", impact["goal-a"])
	}
	if impact["goal-b"] != 3 {
		t.Errorf("expected goal-b impact 3, got %d", impact["goal-b"])
	}
	if impact["goal-e"] != 0 {
		t.Errorf("expected goal-e impact 0, got %d", impact["goal-e"])
	}
}

func TestDependencyGraphDiamondDependencies(t *testing.T) {
	// A -> B, C -> D
	// D depends on B and C; B and C depend on A; A depends on nothing.
	goals := []*Goal{
		{Slug: "root-a", DependsOn: []string{}},
		{Slug: "branch-b", DependsOn: []string{"root-a"}},
		{Slug: "branch-c", DependsOn: []string{"root-a"}},
		{Slug: "merge-d", DependsOn: []string{"branch-b", "branch-c"}},
	}

	graph := BuildDependencyGraph(goals)

	cycles := graph.DetectCycles()
	if len(cycles) != 0 {
		t.Fatalf("expected 0 cycles, got %v", cycles)
	}

	order, err := graph.TopologicalSort()
	if err != nil {
		t.Fatalf("TopologicalSort failed: %v", err)
	}

	// root-a must precede branch-b and branch-c, which must precede merge-d
	idxMap := make(map[string]int)
	for i, s := range order {
		idxMap[s] = i
	}

	if idxMap["root-a"] >= idxMap["branch-b"] || idxMap["root-a"] >= idxMap["branch-c"] {
		t.Fatalf("root-a must precede branches: %v", order)
	}
	if idxMap["branch-b"] >= idxMap["merge-d"] || idxMap["branch-c"] >= idxMap["merge-d"] {
		t.Fatalf("branches must precede merge-d: %v", order)
	}

	// RootBlockers evaluation
	// 1. If root-a is pending: root blocker for merge-d is root-a
	statusMap := map[string]string{
		"root-a":   "pending",
		"branch-b": "pending",
		"branch-c": "pending",
		"merge-d":  "pending",
	}
	blockers := graph.RootBlockers("merge-d", statusMap)
	if !reflect.DeepEqual(blockers, []string{"root-a"}) {
		t.Fatalf("expected root blocker ['root-a'], got %v", blockers)
	}

	// 2. If root-a is implemented, branch-b and branch-c are pending:
	statusMap["root-a"] = "implemented"
	blockers2 := graph.RootBlockers("merge-d", statusMap)
	expectedBlockers2 := []string{"branch-b", "branch-c"}
	if !reflect.DeepEqual(blockers2, expectedBlockers2) {
		t.Fatalf("expected root blockers %v, got %v", expectedBlockers2, blockers2)
	}

	// 3. If root-a and branch-b are implemented, branch-c is pending:
	statusMap["branch-b"] = "implemented"
	blockers3 := graph.RootBlockers("merge-d", statusMap)
	if !reflect.DeepEqual(blockers3, []string{"branch-c"}) {
		t.Fatalf("expected root blocker ['branch-c'], got %v", blockers3)
	}

	// 4. If all prerequisites implemented:
	statusMap["branch-c"] = "implemented"
	blockers4 := graph.RootBlockers("merge-d", statusMap)
	if len(blockers4) != 0 {
		t.Fatalf("expected 0 blockers when all prerequisites implemented, got %v", blockers4)
	}
}

func TestDependencyGraphDisconnectedComponents(t *testing.T) {
	// Component 1: X -> Y
	// Component 2: P -> Q -> R
	// Component 3: Z (isolated)
	goals := []*Goal{
		{Slug: "x", DependsOn: []string{}},
		{Slug: "y", DependsOn: []string{"x"}},
		{Slug: "p", DependsOn: []string{}},
		{Slug: "q", DependsOn: []string{"p"}},
		{Slug: "r", DependsOn: []string{"q"}},
		{Slug: "z", DependsOn: []string{}},
	}

	graph := BuildDependencyGraph(goals)

	cycles := graph.DetectCycles()
	if len(cycles) != 0 {
		t.Fatalf("expected 0 cycles, got %v", cycles)
	}

	order, err := graph.TopologicalSort()
	if err != nil {
		t.Fatalf("sort failed: %v", err)
	}

	if len(order) != 6 {
		t.Fatalf("expected 6 sorted items, got %d: %v", len(order), order)
	}

	idxMap := make(map[string]int)
	for i, s := range order {
		idxMap[s] = i
	}
	if idxMap["x"] >= idxMap["y"] {
		t.Errorf("x must precede y")
	}
	if idxMap["p"] >= idxMap["q"] || idxMap["q"] >= idxMap["r"] {
		t.Errorf("p must precede q and q must precede r")
	}
}

func TestDependencyGraphValidateNoCycle(t *testing.T) {
	goals := []*Goal{
		{Slug: "auth", DependsOn: []string{"database"}},
		{Slug: "database", DependsOn: []string{}},
	}

	graph := BuildDependencyGraph(goals)

	// Valid addition: api -> auth
	if err := graph.ValidateNoCycle("api", []string{"auth"}); err != nil {
		t.Fatalf("expected valid new goal, got: %v", err)
	}

	// Invalid addition: database -> auth (creates cycle database -> auth -> database)
	err := graph.ValidateNoCycle("database", []string{"auth"})
	if err == nil {
		t.Fatal("expected cycle error when database depends on auth")
	}
	if !strings.Contains(err.Error(), "circular dependency") {
		t.Fatalf("unexpected error message: %v", err)
	}
}
