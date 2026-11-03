package goals

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// TestChallenger_EmpiricalVerification_SubheaderUnderSectionHeader empirically verifies the bug fix.
func TestChallenger_EmpiricalVerification_SubheaderUnderSectionHeader(t *testing.T) {
	testCases := []struct {
		name         string
		content      string
		expectedDeps []string
		expectedInd  bool
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
			expectedInd:  false,
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
			expectedInd:  false,
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
			expectedInd:  false,
		},
		{
			name: "Asterisk subheader under Requires section",
			content: `# Goal: Asterisk Subheader

## Requires
* **Prerequisites**:
  * worker-pool
  - task-queue
`,
			expectedDeps: []string{"worker-pool", "task-queue"},
			expectedInd:  false,
		},
		{
			name: "Blocked By subheader under Dependencies section with blank lines",
			content: `# Goal: Blocked By Subheader

## Dependencies

- **Blocked By**:

  - storage-engine
  * telemetry-hub
`,
			expectedDeps: []string{"storage-engine", "telemetry-hub"},
			expectedInd:  false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, ind, deps, _, _, _ := parseGoalMetadata(tc.content)
			for _, d := range deps {
				if d == "depends-on" || d == "requires" || d == "dependencies" || d == "prerequisites" || d == "blocked-by" {
					t.Fatalf("BUG CONFIRMED: phantom dependency slug %q found in %s. Full parsed deps: %v", d, tc.name, deps)
				}
			}
			if ind != tc.expectedInd {
				t.Errorf("expected independent=%v, got %v", tc.expectedInd, ind)
			}
			if !reflect.DeepEqual(deps, tc.expectedDeps) {
				t.Errorf("expected deps %v, got %v", tc.expectedDeps, deps)
			}
		})
	}
}

// TestChallenger_NestedSubheadersWithInlineItems tests nested inline items under section headers.
func TestChallenger_NestedSubheadersWithInlineItems(t *testing.T) {
	testCases := []struct {
		name         string
		content      string
		expectedDeps []string
	}{
		{
			name: "Single inline subheader under section header",
			content: `# Goal: Inline Under Section
## Dependencies
- **Depends On**: alpha-service, beta-service, gamma-service
`,
			expectedDeps: []string{"alpha-service", "beta-service", "gamma-service"},
		},
		{
			name: "Multiple inline subheaders under same section",
			content: `# Goal: Multi Inline Under Section
## Prerequisites
- **Requires**: auth-core, db-pool
* **Blocked By**: redis-cache, event-bus
- **Dependencies**: metrics-exporter
`,
			expectedDeps: []string{"auth-core", "db-pool", "redis-cache", "event-bus", "metrics-exporter"},
		},
		{
			name: "Mixed inline subheader and multiline list under section header",
			content: `# Goal: Mixed Inline and Multiline
## Dependencies
- **Requires**: base-infra, config-loader
- extra-service-1
* extra-service-2
`,
			expectedDeps: []string{"base-infra", "config-loader", "extra-service-1", "extra-service-2"},
		},
		{
			name: "Inline subheader with markdown links and comments",
			content: `# Goal: Inline Links and Comments
## Blocked By
- **Requires**: [auth-service](../auth-service/goal.md), [db-schema](goals/db-schema/goal.md) # core deps
* **Depends On**: "cache-layer", 'msg-queue' # caching and messaging
`,
			expectedDeps: []string{"auth-service", "db-schema", "cache-layer", "msg-queue"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, ind, deps, _, _, _ := parseGoalMetadata(tc.content)
			if ind {
				t.Errorf("expected independent=false")
			}
			if !reflect.DeepEqual(deps, tc.expectedDeps) {
				t.Errorf("expected deps %v, got %v", tc.expectedDeps, deps)
			}
		})
	}
}

// TestChallenger_MixedBulletMarkers tests hyphen, asterisk, and plus markers.
func TestChallenger_MixedBulletMarkers(t *testing.T) {
	testCases := []struct {
		name         string
		content      string
		expectedDeps []string
	}{
		{
			name: "All three bullet types mixed in multiline list under section",
			content: `# Goal: All Three Bullet Types
## Dependencies
- item-hyphen
* item-asterisk
+ item-plus
- [item-hyphen-link](../item-hyphen-link/goal.md)
* [item-asterisk-link](../item-asterisk-link/goal.md)
+ [item-plus-link](../item-plus-link/goal.md)
`,
			expectedDeps: []string{
				"item-hyphen",
				"item-asterisk",
				"item-plus",
				"item-hyphen-link",
				"item-asterisk-link",
				"item-plus-link",
			},
		},
		{
			name: "Nested under subheader with mixed indentation and bullet types",
			content: `# Goal: Nested Mixed Bullets
- **Requires**:
  - dep-hyphen-indented
  * dep-asterisk-indented
  + dep-plus-indented
`,
			expectedDeps: []string{
				"dep-hyphen-indented",
				"dep-asterisk-indented",
				"dep-plus-indented",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, ind, deps, _, _, _ := parseGoalMetadata(tc.content)
			if ind {
				t.Errorf("expected independent=false")
			}
			if !reflect.DeepEqual(deps, tc.expectedDeps) {
				t.Errorf("expected deps %v, got %v", tc.expectedDeps, deps)
			}
		})
	}
}

// TestChallenger_ThematicBreakCombinations tests various thematic break formats.
func TestChallenger_ThematicBreakCombinations(t *testing.T) {
	breaks := []string{
		"---",
		"----",
		"----------------",
		"***",
		"****",
		"****************",
		"___",
		"____",
		"________________",
		"- - -",
		"* * *",
	}

	for _, hr := range breaks {
		t.Run("Break_"+hr, func(t *testing.T) {
			content := fmt.Sprintf(`# Goal: Thematic Break Test
## Dependencies
- dep-one
- dep-two

%s

## Execution Plan & Tasks
- [ ] Task 1: implement something
- [ ] Task 2: write unit tests
- [ ] Task 3: verify with race detector
`, hr)

			_, _, ind, deps, _, _, _ := parseGoalMetadata(content)
			if ind {
				t.Errorf("expected independent=false")
			}
			expected := []string{"dep-one", "dep-two"}
			if !reflect.DeepEqual(deps, expected) {
				t.Errorf("break %q failed to terminate dependency block: expected %v, got %v", hr, expected, deps)
			}
		})
	}
}

// TestChallenger_RealisticMultiAgentWorkflowUnblocking tests multi-agent workflow unblocking.
func TestChallenger_RealisticMultiAgentWorkflowUnblocking(t *testing.T) {
	dir := t.TempDir()

	scaffold := func(slug, title, dependsOn string, seq int) {
		gDir := filepath.Join(dir, "goals", slug)
		_ = os.MkdirAll(gDir, 0755)
		var b strings.Builder
		b.WriteString(fmt.Sprintf("# Goal: %s\n\n## Goal Description\nDescription for %s.\n\n", title, slug))
		b.WriteString("## Dependencies & Execution Order\n")
		if dependsOn != "" {
			b.WriteString(fmt.Sprintf("- **Mode**: Dependent\n- **Depends On**: %s\n", dependsOn))
		} else {
			b.WriteString("- **Mode**: Independent\n- **Depends On**: none\n")
		}
		if seq > 0 {
			b.WriteString(fmt.Sprintf("- **Sequence**: %d\n", seq))
		}
		_ = AtomicWriteFile(filepath.Join(gDir, "goal.md"), []byte(b.String()), 0644)
		_ = AtomicWriteFile(filepath.Join(gDir, "plan.md"), []byte("# Plan\n"), 0644)
		_ = AtomicWriteFile(filepath.Join(gDir, "facts.md"), []byte("# Facts\n"), 0644)
	}

	scaffold("foundation", "Foundation Architecture", "", 1)
	scaffold("data-model", "Data Model & Migrations", "foundation", 2)
	scaffold("auth-core", "Auth Core & Tokens", "foundation", 2)
	scaffold("api-gateway", "API Gateway Routes", "data-model, auth-core", 3)
	scaffold("audit-logger", "Audit Logger Subsystem", "data-model", 3)
	scaffold("e2e-suite", "End-to-End Test Suite", "api-gateway, audit-logger", 4)

	git(t, dir, "init")
	git(t, dir, "config", "user.email", "challenger@example.com")
	git(t, dir, "config", "user.name", "Challenger")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "chore: initial goals scaffolding")

	// STEP 0: Initial Audit
	audit, err := ScanGoals(dir)
	if err != nil {
		t.Fatalf("ScanGoals failed: %v", err)
	}
	if audit.Total != 6 || audit.ReadyCount != 1 || audit.BlockedCount != 5 {
		t.Fatalf("unexpected initial audit: %+v", audit)
	}

	for _, g := range audit.Goals {
		if g.Slug == "e2e-suite" {
			if !reflect.DeepEqual(g.RootBlockers, []string{"foundation"}) {
				t.Fatalf("expected e2e-suite root blocker [foundation], got %v", g.RootBlockers)
			}
		}
	}

	// STEP 1: Implement foundation
	git(t, dir, "commit", "--allow-empty", "-m", "feat: ship foundation architecture (goals/foundation/goal.md)")

	// STEP 2: Rescan after Stage 1
	audit, _ = ScanGoals(dir)
	if audit.ImplementedCount != 1 || audit.ReadyCount != 2 || audit.BlockedCount != 3 {
		t.Fatalf("unexpected audit after stage 1: %+v", audit)
	}

	// STEP 3: Implement data-model only
	git(t, dir, "commit", "--allow-empty", "-m", "feat: complete data model (goals/data-model/goal.md)")

	// STEP 4: Rescan after data-model
	audit, _ = ScanGoals(dir)
	if audit.ImplementedCount != 2 || audit.ReadyCount != 2 || audit.BlockedCount != 2 {
		// Ready: auth-core (prereq foundation is done), audit-logger (prereq data-model is done)
		// Blocked: api-gateway (needs auth-core), e2e-suite (needs api-gateway & audit-logger)
		t.Fatalf("unexpected audit after data-model: %+v", audit)
	}

	for _, g := range audit.Goals {
		if g.Slug == "audit-logger" && g.Blocked {
			t.Fatalf("audit-logger should be unblocked now")
		}
		if g.Slug == "api-gateway" {
			if !g.Blocked || !reflect.DeepEqual(g.UnmetDeps, []string{"auth-core"}) {
				t.Fatalf("api-gateway should be blocked only by auth-core: %+v", g)
			}
		}
	}

	// STEP 5: Implement auth-core
	git(t, dir, "commit", "--allow-empty", "-m", "feat: complete auth core (goals/auth-core/goal.md)")

	audit, _ = ScanGoals(dir)
	if audit.ImplementedCount != 3 || audit.ReadyCount != 2 || audit.BlockedCount != 1 {
		// Ready: api-gateway, audit-logger
		// Blocked: e2e-suite
		t.Fatalf("unexpected audit after auth-core: %+v", audit)
	}

	// STEP 6: Implement api-gateway and audit-logger
	git(t, dir, "commit", "--allow-empty", "-m", "feat: complete api gateway (goals/api-gateway/goal.md)")
	git(t, dir, "commit", "--allow-empty", "-m", "feat: complete audit logger (goals/audit-logger/goal.md)")

	audit, _ = ScanGoals(dir)
	if audit.ImplementedCount != 5 || audit.ReadyCount != 1 || audit.BlockedCount != 0 {
		t.Fatalf("unexpected audit after stage 3: %+v", audit)
	}

	// STEP 7: Implement e2e-suite
	git(t, dir, "commit", "--allow-empty", "-m", "feat: complete e2e suite (goals/e2e-suite/goal.md)")

	audit, _ = ScanGoals(dir)
	if audit.ImplementedCount != 6 || audit.ReadyCount != 0 || audit.BlockedCount != 0 {
		t.Fatalf("expected all 6 implemented, got %+v", audit)
	}
}

// TestChallenger_StressRandomDAGResolution tests random DAG generation and topological sorting.
func TestChallenger_StressRandomDAGResolution(t *testing.T) {
	const (
		numNodes = 80
		trials   = 10
	)

	for trial := range trials {
		rng := rand.New(rand.NewSource(int64(trial * 1337)))
		var goals []*Goal

		for i := range numNodes {
			slug := fmt.Sprintf("node-%02d", i)
			var deps []string
			if i > 0 {
				numDeps := rng.Intn(4)
				for range numDeps {
					depIdx := rng.Intn(i)
					depSlug := fmt.Sprintf("node-%02d", depIdx)
					found := slices.Contains(deps, depSlug)
					if !found {
						deps = append(deps, depSlug)
					}
				}
			}
			goals = append(goals, &Goal{
				Slug:      slug,
				DependsOn: deps,
			})
		}

		g := BuildDependencyGraph(goals)
		cycles := g.DetectCycles()
		if len(cycles) > 0 {
			t.Fatalf("Trial %d: unexpected cycle detected in acyclic DAG: %v", trial, cycles)
		}

		order, err := g.TopologicalSort()
		if err != nil || len(order) != numNodes {
			t.Fatalf("Trial %d: TopologicalSort failed: %v", trial, err)
		}

		posMap := make(map[string]int, numNodes)
		for idx, slug := range order {
			posMap[slug] = idx
		}
		for _, goal := range goals {
			nodePos := posMap[goal.Slug]
			for _, dep := range goal.DependsOn {
				depPos := posMap[dep]
				if depPos >= nodePos {
					t.Fatalf("Trial %d: topological invariant violated: %s (pos %d) after %s (pos %d)",
						trial, dep, depPos, goal.Slug, nodePos)
				}
			}
		}
	}
}
