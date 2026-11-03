package goals

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ============================================================================
// CHALLENGER TEST 1: Multiline Bullet List & Markdown Metadata Edge Cases
// ============================================================================

func TestChallenger_MultilineBulletList_MixedTabsAndSpaces(t *testing.T) {
	content := `# Goal: Mixed Tabs And Spaces In Bullets

## Description
Testing tab and space mixing.

- **Requires**:
	- dep-tab-1
  - dep-space-2
	  - dep-tab-space-3
  	- dep-space-tab-4

## Next Section
`
	_, _, indep, deps, _, _, _ := parseGoalMetadata(content)
	if indep {
		t.Fatalf("expected independent=false")
	}
	expected := []string{"dep-tab-1", "dep-space-2", "dep-tab-space-3", "dep-space-tab-4"}
	if !reflect.DeepEqual(deps, expected) {
		t.Fatalf("expected deps %v, got %v", expected, deps)
	}
}

func TestChallenger_MultilineBulletList_AsterisksAndHyphens(t *testing.T) {
	content := `# Goal: Mixed Bullet Styles

## Prerequisites
* auth-service # asterisk bullet with comment
- db-schema # hyphen bullet with comment
* [api-gateway](../api-gateway/goal.md)
- [metrics-collector](../../metrics-collector/goal.md#metrics)
* "redis-cache" # quoted slug
- 'event-bus' # single-quoted slug

## Execution Plan
`
	_, _, indep, deps, _, _, _ := parseGoalMetadata(content)
	if indep {
		t.Fatalf("expected independent=false")
	}
	expected := []string{
		"auth-service",
		"db-schema",
		"api-gateway",
		"metrics-collector",
		"redis-cache",
		"event-bus",
	}
	if !reflect.DeepEqual(deps, expected) {
		t.Fatalf("expected deps %v, got %v", expected, deps)
	}
}

// TestChallenger_BugReproduction_SubheaderUnderSectionHeader reproduces the bug where
// a sub-property bullet (e.g. `- **Depends On**:`) placed beneath a section header
// (e.g. `## Dependencies`) is incorrectly parsed as a dependency item `"depends-on"`.
func TestChallenger_BugReproduction_SubheaderUnderSectionHeader(t *testing.T) {
	testCases := []struct {
		name         string
		content      string
		expectedDeps []string
	}{
		{
			name: "Depends On subheader under Dependencies section",
			content: `# Goal: Empty Lines Between Bullets

## Dependencies

- **Depends On**:

  - step-alpha # first step

  - step-beta # second step with empty line before

  * step-gamma # asterisk after empty line

  - [step-delta](../step-delta/goal.md)

## Done Condition
Done when all pass.
`,
			expectedDeps: []string{"step-alpha", "step-beta", "step-gamma", "step-delta"},
		},
		{
			name: "Requires subheader under Prerequisites section",
			content: `# Goal: Requires Under Prerequisites

## Prerequisites
- **Requires**:
  - auth-service
  - user-db
`,
			expectedDeps: []string{"auth-service", "user-db"},
		},
		{
			name: "Dependencies subheader under Blocked By section",
			content: `# Goal: Dependencies Under Blocked By

## Blocked By
- **Dependencies**:
  * cache-service
  * event-bus
`,
			expectedDeps: []string{"cache-service", "event-bus"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, deps, _, _, _ := parseGoalMetadata(tc.content)
			for _, d := range deps {
				if d == "depends-on" || d == "requires" || d == "dependencies" || d == "prerequisites" || d == "blocked-by" {
					t.Errorf("BUG CONFIRMED: phantom dependency %q extracted from header line in %s. Full parsed deps: %v", d, tc.name, deps)
				}
			}
			if !reflect.DeepEqual(deps, tc.expectedDeps) {
				t.Errorf("expected deps %v, got %v", tc.expectedDeps, deps)
			}
		})
	}
}

func TestChallenger_MultilineBulletList_TrailingCommentsWithSpecialChars(t *testing.T) {
	content := `# Goal: Special Char Comments

- **Requires**:
  - service-one # comment with # multiple # hashes and [brackets] and (parens)
  - [service-two](../service-two/goal.md) # relative link with comment: "quoted text"
  - service-three # comment with 'single quotes' and \ escaped \ backslashes
  - ` + "`service-four`" + ` # backtick item with comment

- **Sequence**: 7
`
	_, _, indep, deps, seq, _, _ := parseGoalMetadata(content)
	if indep {
		t.Fatalf("expected independent=false")
	}
	if seq != 7 {
		t.Fatalf("expected sequence 7, got %d", seq)
	}
	expected := []string{"service-one", "service-two", "service-three", "service-four"}
	if !reflect.DeepEqual(deps, expected) {
		t.Fatalf("expected deps %v, got %v", expected, deps)
	}
}

func TestChallenger_MultilineBulletList_TerminationByVariousConstructs(t *testing.T) {
	testCases := []struct {
		name         string
		content      string
		expectedDeps []string
		expectedSeq  int
	}{
		{
			name: "Terminated by subsequent property bullet - **Sequence**",
			content: `# Goal: Terminate By Sequence
- **Depends On**:
  - item-1
  - item-2
- **Sequence**: 3
- **Mode**: Dependent
`,
			expectedDeps: []string{"item-1", "item-2"},
			expectedSeq:  3,
		},
		{
			name: "Terminated by unindented plain paragraph text",
			content: `# Goal: Terminate By Text
## Blocked By
- item-a
- item-b

This is a regular paragraph describing why these goals are needed.
- This is a normal list item in the description, not a dependency.
`,
			expectedDeps: []string{"item-a", "item-b"},
			expectedSeq:  0,
		},
		{
			name: "Terminated by markdown thematic break ---",
			content: `# Goal: Terminate By HR
## Requires
* dep-x
* dep-y
---
## Done Condition
All tests pass.
`,
			expectedDeps: []string{"dep-x", "dep-y"},
			expectedSeq:  0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, indep, deps, seq, _, _ := parseGoalMetadata(tc.content)
			if indep && len(tc.expectedDeps) > 0 {
				t.Fatalf("expected independent=false")
			}
			if seq != tc.expectedSeq {
				t.Fatalf("expected sequence %d, got %d", tc.expectedSeq, seq)
			}
			if !reflect.DeepEqual(deps, tc.expectedDeps) {
				t.Fatalf("expected deps %v, got %v", tc.expectedDeps, deps)
			}
		})
	}
}

func TestChallenger_Frontmatter_ComplexInlineCommentsAndLists(t *testing.T) {
	content := `---
title: "Complex # Title With Hash" # real comment after quoted title
description: "Handles users # securely" # real comment
sequence: 4 # sequence order
mode: dependent # dependent mode
depends_on: # array list
  - "db-service" # db service comment
  - 'cache-service' # cache service comment
  - [nested-bracket-slug] # bracketed slug
  - raw-slug # raw slug # with multiple # hashes
---

# Title In Body
`
	title, desc, indep, deps, seq, _, _ := parseGoalMetadata(content)
	if title != "Complex # Title With Hash" {
		t.Fatalf("expected title 'Complex # Title With Hash', got %q", title)
	}
	if desc != "Handles users # securely" {
		t.Fatalf("expected desc 'Handles users # securely', got %q", desc)
	}
	if indep {
		t.Fatalf("expected independent=false")
	}
	if seq != 4 {
		t.Fatalf("expected sequence 4, got %d", seq)
	}
	expected := []string{"db-service", "cache-service", "nested-bracket-slug", "raw-slug"}
	if !reflect.DeepEqual(deps, expected) {
		t.Fatalf("expected deps %v, got %v", expected, deps)
	}
}

// ============================================================================
// CHALLENGER TEST 2: Comprehensive Priority Sequence Ordering Across Tiers
// ============================================================================

func TestChallenger_PrioritySequenceOrdering_FullTierMatrix(t *testing.T) {
	dir := t.TempDir()

	// Helper to scaffold complete goal
	setupChallengerGoal := func(slug, title, dep string, seq int) {
		gDir := filepath.Join(dir, "goals", slug)
		_ = os.MkdirAll(gDir, 0755)
		var b strings.Builder
		b.WriteString(fmt.Sprintf("# Goal: %s\n\n## Description\n%s\n\n", title, title))
		if dep != "" {
			b.WriteString(fmt.Sprintf("- **Depends On**: %s\n", dep))
		} else {
			b.WriteString("- **Depends On**: none\n")
		}
		if seq > 0 {
			b.WriteString(fmt.Sprintf("- **Sequence**: %d\n", seq))
		}
		_ = os.WriteFile(filepath.Join(gDir, "goal.md"), []byte(b.String()), 0644)
		_ = os.WriteFile(filepath.Join(gDir, "plan.md"), []byte("# Plan\n"), 0644)
		_ = os.WriteFile(filepath.Join(gDir, "facts.md"), []byte("# Facts\n"), 0644)
	}

	// 1. Implemented goals
	setupChallengerGoal("impl-seq-1", "Implemented 1", "", 1)
	setupChallengerGoal("impl-seq-2", "Implemented 2", "", 2)

	// 2. Ready goals
	setupChallengerGoal("ready-seq-1", "Ready 1", "", 1)
	setupChallengerGoal("ready-seq-2", "Ready 2", "", 2)
	setupChallengerGoal("ready-seq-0-high-impact", "Ready High Impact", "", 0)
	setupChallengerGoal("ready-seq-0-low-impact", "Ready Low Impact", "", 0)

	// 3. Blocked goals
	// ready-seq-0-high-impact unblocks child-a and child-b
	setupChallengerGoal("child-a", "Child A", "ready-seq-0-high-impact", 0)
	setupChallengerGoal("child-b", "Child B", "ready-seq-0-high-impact", 0)
	setupChallengerGoal("blocked-seq-1-few-deps", "Blocked Few Deps", "ready-seq-1", 1)
	setupChallengerGoal("blocked-seq-1-many-deps", "Blocked Many Deps", "ready-seq-1, ready-seq-2", 1)
	setupChallengerGoal("blocked-seq-0", "Blocked Seq 0", "ready-seq-2", 0)

	// 4. Claimed goals
	setupChallengerGoal("claimed-seq-1", "Claimed 1", "", 1)
	setupChallengerGoal("claimed-seq-2", "Claimed 2", "", 2)

	// 5. Draft goal (remove facts.md)
	setupChallengerGoal("draft-seq-1", "Draft 1", "", 1)
	_ = os.Remove(filepath.Join(dir, "goals", "draft-seq-1", "facts.md"))

	// Init git and commit implemented goals
	git(t, dir, "init")
	git(t, dir, "config", "user.email", "test@example.com")
	git(t, dir, "config", "user.name", "Test")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "chore: init repo")
	git(t, dir, "commit", "--allow-empty", "-m", "feat: implement 1 (goals/impl-seq-1/goal.md)")
	git(t, dir, "commit", "--allow-empty", "-m", "feat: implement 2 (goals/impl-seq-2/goal.md)")

	// Create claim.json for claimed goals
	for _, cl := range []string{"claimed-seq-1", "claimed-seq-2"} {
		_ = AtomicWriteFile(filepath.Join(dir, "goals", cl, "claim.json"), []byte(fmt.Sprintf(`{"goal_slug":"%s","claimed_by":"agent-test","pid":%d,"claimed_at":"2026-08-31T00:00:00Z"}`, cl, os.Getpid())), 0644)
	}

	// Scan repository
	audit, err := ScanGoals(dir)
	if err != nil {
		t.Fatalf("ScanGoals failed: %v", err)
	}

	// Check metrics
	if audit.ImplementedCount != 2 {
		t.Fatalf("expected 2 implemented, got %d", audit.ImplementedCount)
	}
	if audit.ClaimedCount != 2 {
		t.Fatalf("expected 2 claimed, got %d", audit.ClaimedCount)
	}
	if audit.ReadyCount != 4 {
		t.Fatalf("expected 4 ready, got %d", audit.ReadyCount)
	}
	if audit.BlockedCount != 5 {
		t.Fatalf("expected 5 blocked (child-a, child-b, blocked-seq-1-few, blocked-seq-1-many, blocked-seq-0), got %d", audit.BlockedCount)
	}
	if audit.DraftCount != 1 {
		t.Fatalf("expected 1 draft, got %d", audit.DraftCount)
	}

	// Verify ordering rules:
	// Tier 1: Ready Pending (4 goals)
	//   1. ready-seq-1 (Seq: 1)
	//   2. ready-seq-2 (Seq: 2)
	//   3. ready-seq-0-high-impact (Seq: 0, Impact: 2)
	//   4. ready-seq-0-low-impact (Seq: 0, Impact: 0)
	// Tier 2: Claimed (2 goals)
	//   5. claimed-seq-1 (Seq: 1)
	//   6. claimed-seq-2 (Seq: 2)
	// Tier 3: Blocked Pending (5 goals)
	//   7. blocked-seq-1-few-deps (Seq: 1, 1 unmet dep)
	//   8. blocked-seq-1-many-deps (Seq: 1, 2 unmet deps)
	//   9. blocked-seq-0 (Seq: 0, 1 unmet dep)
	//   10. child-a (Seq: 0, 1 unmet dep, slug tie-break 'child-a' < 'child-b')
	//   11. child-b (Seq: 0, 1 unmet dep)
	// Tier 4: Draft (1 goal)
	//   12. draft-seq-1 (Seq: 1)
	// Tier 5: Implemented (2 goals)
	//   13. impl-seq-1 (Seq: 1)
	//   14. impl-seq-2 (Seq: 2)

	expectedOrder := []string{
		"ready-seq-1",
		"ready-seq-2",
		"ready-seq-0-high-impact",
		"ready-seq-0-low-impact",
		"claimed-seq-1",
		"claimed-seq-2",
		"blocked-seq-1-few-deps",
		"blocked-seq-1-many-deps",
		"blocked-seq-0",
		"child-a",
		"child-b",
		"draft-seq-1",
		"impl-seq-1",
		"impl-seq-2",
	}

	var actualOrder []string
	for _, g := range audit.Goals {
		actualOrder = append(actualOrder, g.Slug)
	}

	if !reflect.DeepEqual(actualOrder, expectedOrder) {
		t.Fatalf("VIOLATION: ordering does not match expected tiered priority rules:\nExpected: %v\nActual:   %v", expectedOrder, actualOrder)
	}
}

// ============================================================================
// CHALLENGER TEST 3: High-Concurrency Stress Testing On Graph & Topological Sort
// ============================================================================

func TestChallenger_ConcurrentGraphOperationsStress(t *testing.T) {
	// Build a complex 50-node DAG with diamonds, trees, and isolated components
	numNodes := 50
	var goals []*Goal
	for i := range numNodes {
		slug := fmt.Sprintf("node-%02d", i)
		var deps []string
		// Connect nodes deterministically in DAG (edges only to strictly smaller indices)
		if i > 0 && i%2 == 0 {
			deps = append(deps, fmt.Sprintf("node-%02d", i-1))
		}
		if i > 2 && i%3 == 0 {
			deps = append(deps, fmt.Sprintf("node-%02d", i-2))
		}
		if i > 5 && i%5 == 0 {
			deps = append(deps, fmt.Sprintf("node-%02d", i/2))
		}
		goals = append(goals, &Goal{
			Slug:      slug,
			DependsOn: deps,
		})
	}

	// Reference canonical graph
	canonicalGraph := BuildDependencyGraph(goals)
	canonicalOrder, err := canonicalGraph.TopologicalSort()
	if err != nil {
		t.Fatalf("canonical topological sort failed: %v", err)
	}
	if len(canonicalOrder) != numNodes {
		t.Fatalf("expected %d sorted nodes, got %d", numNodes, len(canonicalOrder))
	}

	// Concurrently invoke graph operations across 100 parallel goroutines for 1 second
	const workers = 100
	var wg sync.WaitGroup
	var opCount atomic.Int64
	var errCount atomic.Int64
	stopCh := make(chan struct{})

	// Status map for RootBlockers
	statusMap := make(map[string]string, numNodes)
	for i := range numNodes {
		if i < 10 {
			statusMap[fmt.Sprintf("node-%02d", i)] = "implemented"
		} else {
			statusMap[fmt.Sprintf("node-%02d", i)] = "pending"
		}
	}

	for w := range workers {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(workerID * 1000)))

			for {
				select {
				case <-stopCh:
					return
				default:
					// 1. Build graph from slice copy
					localGoals := make([]*Goal, len(goals))
					copy(localGoals, goals)
					g := BuildDependencyGraph(localGoals)

					// 2. Perform various operations
					op := rng.Intn(6)
					switch op {
					case 0:
						order, err := g.TopologicalSort()
						if err != nil || len(order) != numNodes {
							errCount.Add(1)
						}
						// Verify topological validity: for every node, its deps appear before it
						idxMap := make(map[string]int, len(order))
						for idx, s := range order {
							idxMap[s] = idx
						}
						for _, goal := range goals {
							nodeIdx := idxMap[goal.Slug]
							for _, dep := range goal.DependsOn {
								if depIdx, exists := idxMap[dep]; exists {
									if depIdx >= nodeIdx {
										errCount.Add(1)
									}
								}
							}
						}
					case 1:
						cycles := g.DetectCycles()
						if len(cycles) != 0 {
							errCount.Add(1)
						}
					case 2:
						target := fmt.Sprintf("node-%02d", rng.Intn(numNodes))
						prereqs := g.TransitivePrerequisites(target)
						_ = prereqs
					case 3:
						target := fmt.Sprintf("node-%02d", rng.Intn(numNodes))
						dependents := g.TransitiveDependents(target)
						_ = dependents
					case 4:
						target := fmt.Sprintf("node-%02d", rng.Intn(numNodes))
						blockers := g.RootBlockers(target, statusMap)
						_ = blockers
					case 5:
						impact := g.DownstreamImpactMap()
						if len(impact) != numNodes {
							errCount.Add(1)
						}
					}
					opCount.Add(1)
				}
			}
		}(w)
	}

	time.Sleep(1 * time.Second)
	close(stopCh)
	wg.Wait()

	if errCount.Load() > 0 {
		t.Fatalf("VIOLATION: encountered %d errors during concurrent graph operations", errCount.Load())
	}
	if opCount.Load() < 500 {
		t.Fatalf("VIOLATION: expected at least 500 concurrent operations, got %d", opCount.Load())
	}
	t.Logf("Concurrent Graph Stress Test passed: executed %d operations across %d goroutines with 0 race/logic errors", opCount.Load(), workers)
}

func TestChallenger_ConcurrentCyclicGraphDetectionStress(t *testing.T) {
	// Concurrently test cycle detection on dynamically generated cyclic graphs
	const workers = 50
	var wg sync.WaitGroup
	var cycleDetections atomic.Int64
	var falseNegatives atomic.Int64
	stopCh := make(chan struct{})

	for w := range workers {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(workerID * 777)))

			for {
				select {
				case <-stopCh:
					return
				default:
					// Construct a ring of size N (3 to 10)
					ringSize := 3 + rng.Intn(8)
					var ringGoals []*Goal
					for i := range ringSize {
						slug := fmt.Sprintf("ring-%d-%d", workerID, i)
						prev := fmt.Sprintf("ring-%d-%d", workerID, (i+ringSize-1)%ringSize)
						ringGoals = append(ringGoals, &Goal{
							Slug:      slug,
							DependsOn: []string{prev},
						})
					}

					g := BuildDependencyGraph(ringGoals)
					cycles := g.DetectCycles()
					if len(cycles) == 0 {
						falseNegatives.Add(1)
					}

					_, err := g.TopologicalSort()
					if err == nil {
						falseNegatives.Add(1)
					}

					cycleDetections.Add(1)
				}
			}
		}(w)
	}

	time.Sleep(1 * time.Second)
	close(stopCh)
	wg.Wait()

	if falseNegatives.Load() > 0 {
		t.Fatalf("VIOLATION: %d false negatives in concurrent cycle detection", falseNegatives.Load())
	}
	t.Logf("Concurrent Cycle Detection Stress Test passed: %d cycles verified across %d goroutines", cycleDetections.Load(), workers)
}

func TestChallenger_ComplexGraphTopologies(t *testing.T) {
	// 1. Star Topology: 1 Central Hub, 30 Spokes depending on Hub
	var starGoals []*Goal
	starGoals = append(starGoals, &Goal{Slug: "hub", DependsOn: nil})
	for i := 1; i <= 30; i++ {
		starGoals = append(starGoals, &Goal{
			Slug:      fmt.Sprintf("spoke-%02d", i),
			DependsOn: []string{"hub"},
		})
	}
	gStar := BuildDependencyGraph(starGoals)
	if len(gStar.DetectCycles()) != 0 {
		t.Fatal("star topology should have 0 cycles")
	}
	starOrder, err := gStar.TopologicalSort()
	if err != nil || len(starOrder) != 31 {
		t.Fatalf("star topological sort failed: %v", err)
	}
	if starOrder[0] != "hub" {
		t.Fatalf("hub must be first in star topology: %v", starOrder)
	}
	if gStar.DownstreamImpactMap()["hub"] != 30 {
		t.Fatalf("hub downstream impact should be 30, got %d", gStar.DownstreamImpactMap()["hub"])
	}

	// 2. Inverted Star Topology: 30 Spokes prerequisites of 1 Sink
	var invStarGoals []*Goal
	var spokeSlugs []string
	for i := 1; i <= 30; i++ {
		slug := fmt.Sprintf("source-%02d", i)
		spokeSlugs = append(spokeSlugs, slug)
		invStarGoals = append(invStarGoals, &Goal{Slug: slug, DependsOn: nil})
	}
	invStarGoals = append(invStarGoals, &Goal{Slug: "sink", DependsOn: spokeSlugs})
	gInv := BuildDependencyGraph(invStarGoals)
	if len(gInv.DetectCycles()) != 0 {
		t.Fatal("inverted star topology should have 0 cycles")
	}
	invOrder, err := gInv.TopologicalSort()
	if err != nil || len(invOrder) != 31 {
		t.Fatalf("inverted star sort failed: %v", err)
	}
	if invOrder[30] != "sink" {
		t.Fatalf("sink must be last in inverted star: %v", invOrder)
	}
	if len(gInv.TransitivePrerequisites("sink")) != 30 {
		t.Fatalf("sink should have 30 transitive prerequisites, got %d", len(gInv.TransitivePrerequisites("sink")))
	}

	// 3. Double Diamond Alternating Chain:
	// A -> (B, C) -> D -> (E, F) -> G
	diamondGoals := []*Goal{
		{Slug: "node-a", DependsOn: nil},
		{Slug: "node-b", DependsOn: []string{"node-a"}},
		{Slug: "node-c", DependsOn: []string{"node-a"}},
		{Slug: "node-d", DependsOn: []string{"node-b", "node-c"}},
		{Slug: "node-e", DependsOn: []string{"node-d"}},
		{Slug: "node-f", DependsOn: []string{"node-d"}},
		{Slug: "node-g", DependsOn: []string{"node-e", "node-f"}},
	}
	gDiamond := BuildDependencyGraph(diamondGoals)
	if len(gDiamond.DetectCycles()) != 0 {
		t.Fatal("double diamond should have 0 cycles")
	}
	dOrder, err := gDiamond.TopologicalSort()
	if err != nil || len(dOrder) != 7 {
		t.Fatalf("double diamond sort failed: %v", err)
	}
	idxMap := make(map[string]int)
	for i, s := range dOrder {
		idxMap[s] = i
	}
	if idxMap["node-a"] >= idxMap["node-b"] || idxMap["node-a"] >= idxMap["node-c"] {
		t.Fatal("node-a must precede node-b and node-c")
	}
	if idxMap["node-b"] >= idxMap["node-d"] || idxMap["node-c"] >= idxMap["node-d"] {
		t.Fatal("node-b and node-c must precede node-d")
	}
	if idxMap["node-d"] >= idxMap["node-e"] || idxMap["node-d"] >= idxMap["node-f"] {
		t.Fatal("node-d must precede node-e and node-f")
	}
	if idxMap["node-e"] >= idxMap["node-g"] || idxMap["node-f"] >= idxMap["node-g"] {
		t.Fatal("node-e and node-f must precede node-g")
	}

	impactDiamond := gDiamond.DownstreamImpactMap()
	if impactDiamond["node-a"] != 6 {
		t.Fatalf("expected node-a impact 6, got %d", impactDiamond["node-a"])
	}
	if impactDiamond["node-d"] != 3 {
		t.Fatalf("expected node-d impact 3, got %d", impactDiamond["node-d"])
	}
	if impactDiamond["node-g"] != 0 {
		t.Fatalf("expected node-g impact 0, got %d", impactDiamond["node-g"])
	}
}
