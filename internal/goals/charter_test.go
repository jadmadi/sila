package goals

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGoalCharter_ModelAndIntersection(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Inviolable deny path intersection violation
	_, err := CreateCharter(tempDir, "test-goal", "agent-1", "sess-1", []string{"research/notes.md"}, nil, nil, time.Hour)
	if err == nil {
		t.Fatalf("expected capability intersection error when allow path overlaps with default inviolable deny path 'research/*'")
	}
	if !strings.Contains(err.Error(), "capability intersection violation") {
		t.Fatalf("unexpected error message: %v", err)
	}

	// 2. Custom deny path intersection violation
	_, err = CreateCharter(tempDir, "test-goal", "agent-1", "sess-1", []string{"worker/routes.ts"}, []string{"worker/*"}, nil, time.Hour)
	if err == nil {
		t.Fatalf("expected capability intersection error when allow path overlaps with custom deny path 'worker/*'")
	}

	// 3. Valid capability narrowing
	charter, err := CreateCharter(tempDir, "test-goal", "@devin", "sess-123", []string{"cli/cmd/sila/main.go", "cli/internal/goals/claim.go"}, []string{"cli/docs/*"}, nil, 30*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error creating valid charter: %v", err)
	}
	if charter.Agent != "devin" {
		t.Errorf("expected agent 'devin', got %q", charter.Agent)
	}
	if charter.SessionID != "sess-123" {
		t.Errorf("expected session 'sess-123', got %q", charter.SessionID)
	}
	if len(charter.AllowPaths) != 2 {
		t.Errorf("expected 2 allow paths, got %d", len(charter.AllowPaths))
	}
	if charter.MaxDuration != 30*time.Minute {
		t.Errorf("expected 30m duration, got %v", charter.MaxDuration)
	}
	// Verify default inviolable boundaries included in deny paths
	hasResearch := false
	for _, d := range charter.DenyPaths {
		if d == "research/*" {
			hasResearch = true
			break
		}
	}
	if !hasResearch {
		t.Errorf("expected deny paths to contain default inviolable 'research/*', got %v", charter.DenyPaths)
	}
}

func TestGoalCharter_PersistenceAndLifecycle(t *testing.T) {
	tempDir := t.TempDir()

	charter, err := CreateCharter(tempDir, "persist-goal", "claude-code", "", []string{"cli/cmd/sila/*"}, nil, nil, time.Hour)
	if err != nil {
		t.Fatalf("create charter failed: %v", err)
	}

	// Save
	if err := SaveCharter(tempDir, charter); err != nil {
		t.Fatalf("save charter failed: %v", err)
	}

	// Load
	loaded, err := LoadCharter(tempDir, "persist-goal")
	if err != nil {
		t.Fatalf("load charter failed: %v", err)
	}
	if loaded.GoalSlug != "persist-goal" || loaded.Agent != "claude-code" {
		t.Fatalf("loaded charter mismatch: %+v", loaded)
	}

	// List active
	list, err := ListActiveCharters(tempDir)
	if err != nil {
		t.Fatalf("list active charters failed: %v", err)
	}
	if len(list) != 1 || list[0].GoalSlug != "persist-goal" {
		t.Fatalf("unexpected active charters list: %v", list)
	}

	// Expired charter test
	charterExpired, err := CreateCharter(tempDir, "expired-goal", "agent-old", "", []string{"cli/*"}, nil, nil, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("create expired charter failed: %v", err)
	}
	charterExpired.CreatedAt = time.Now().UTC().Add(-1 * time.Hour)
	if err := SaveCharter(tempDir, charterExpired); err != nil {
		t.Fatalf("save expired charter failed: %v", err)
	}

	activeAfter, err := ListActiveCharters(tempDir)
	if err != nil {
		t.Fatalf("list active charters failed: %v", err)
	}
	if len(activeAfter) != 1 || activeAfter[0].GoalSlug != "persist-goal" {
		t.Fatalf("expired charter should not appear in active charters, got: %d", len(activeAfter))
	}

	// Remove
	if err := RemoveCharter(tempDir, "persist-goal"); err != nil {
		t.Fatalf("remove charter failed: %v", err)
	}
	afterRemove, err := ListActiveCharters(tempDir)
	if err != nil {
		t.Fatalf("list active charters failed: %v", err)
	}
	if len(afterRemove) != 0 {
		t.Fatalf("expected 0 active charters after remove, got %d", len(afterRemove))
	}
}

func TestGoalCharter_CollisionDetection(t *testing.T) {
	tempDir := t.TempDir()

	c1, err := CreateCharter(tempDir, "goal-1", "agent-1", "", []string{"cli/cmd/sila/main.go"}, nil, nil, time.Hour)
	if err != nil {
		t.Fatalf("create c1: %v", err)
	}
	if err := SaveCharter(tempDir, c1); err != nil {
		t.Fatalf("save c1: %v", err)
	}

	// Disjoint goal: no collision
	cDisjoint, err := CreateCharter(tempDir, "goal-disjoint", "agent-2", "", []string{"worker/src/index.ts"}, nil, nil, time.Hour)
	if err != nil {
		t.Fatalf("create cDisjoint: %v", err)
	}
	if err := EvaluateCharterCollisions(tempDir, cDisjoint, false); err != nil {
		t.Fatalf("expected no collision for disjoint goal, got: %v", err)
	}

	// Overlapping goal: collision detected
	cOverlap, err := CreateCharter(tempDir, "goal-overlap", "agent-3", "", []string{"cli/cmd/sila/main.go", "cli/internal/goals/view.go"}, nil, nil, time.Hour)
	if err != nil {
		t.Fatalf("create cOverlap: %v", err)
	}
	err = EvaluateCharterCollisions(tempDir, cOverlap, false)
	if err == nil {
		t.Fatalf("expected collision error for overlapping goal, got nil")
	}
	colErr, ok := err.(*CollisionError)
	if !ok {
		t.Fatalf("expected *CollisionError, got %T: %v", err, err)
	}
	if colErr.ConflictingGoal != "goal-1" || colErr.HolderAgent != "agent-1" {
		t.Fatalf("unexpected collision error fields: %+v", colErr)
	}

	// Force override: collision bypassed
	if err := EvaluateCharterCollisions(tempDir, cOverlap, true); err != nil {
		t.Fatalf("expected force=true to bypass collision, got: %v", err)
	}
}

func TestSilaOnboardingCharterAllowsGitattributes(t *testing.T) {
	root := t.TempDir()
	charter, err := CreateCharter(root, "sila-onboarding", "agent-x", "", nil, nil, nil, time.Hour)
	if err != nil {
		t.Fatalf("create charter: %v", err)
	}
	allowed := false
	for _, p := range charter.AllowPaths {
		if p == ".gitattributes" {
			allowed = true
			break
		}
	}
	if !allowed {
		t.Fatalf(".gitattributes missing from allow paths: %+v", charter.AllowPaths)
	}
	for _, f := range []string{".gitattributes", "AGENTS.md", ".gitignore"} {
		if err := ValidateStagedFilesAgainstCharter(charter, []string{f}); err != nil {
			t.Fatalf("sila-created %s rejected: %v", f, err)
		}
	}
}

func TestGoalCharter_ValidateStagedFiles(t *testing.T) {
	tempDir := t.TempDir()

	charter, err := CreateCharter(tempDir, "test-gate-goal", "agent-x", "", []string{"cli/cmd/sila/*", "cli/internal/goals/*"}, []string{"research/*", "worker/*"}, nil, time.Hour)
	if err != nil {
		t.Fatalf("create charter: %v", err)
	}

	// 1. Staged clean files within allow_paths
	cleanFiles := []string{
		"cli/cmd/sila/main.go",
		"cli/internal/goals/charter.go",
	}
	if err := ValidateStagedFilesAgainstCharter(charter, cleanFiles); err != nil {
		t.Fatalf("expected clean validation, got: %v", err)
	}

	// 2. Deny path violation
	deniedFiles := []string{
		"cli/cmd/sila/main.go",
		"research/secret_memo.md",
	}
	err = ValidateStagedFilesAgainstCharter(charter, deniedFiles)
	if err == nil {
		t.Fatalf("expected error for denied path, got nil")
	}
	if !strings.Contains(err.Error(), "denied path") || !strings.Contains(err.Error(), "research/secret_memo.md") {
		t.Fatalf("unexpected error message: %v", err)
	}

	// 3. Outside allow_paths violation
	outsideFiles := []string{
		"docs/unrelated.md",
	}
	err = ValidateStagedFilesAgainstCharter(charter, outsideFiles)
	if err == nil {
		t.Fatalf("expected error for outside allow_paths, got nil")
	}
	if !strings.Contains(err.Error(), "outside authorized allow_paths") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestGoalCharter_ConcurrencyStress(t *testing.T) {
	tempDir := t.TempDir()
	goalsDir := filepath.Join(tempDir, "goals")
	if err := os.MkdirAll(goalsDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Setup 20 goal packages on disk
	const agentCount = 20
	for i := 1; i <= agentCount; i++ {
		slug := fmt.Sprintf("stress-goal-%02d", i)
		gDir := filepath.Join(goalsDir, slug)
		if err := os.MkdirAll(gDir, 0755); err != nil {
			t.Fatal(err)
		}
		// All 20 goals intentionally target the exact same shared file to force collisions
		goalContent := fmt.Sprintf("# Goal: %s\n\n## Tasks To Touch\n- `cli/internal/shared/hot_path.go`\n", slug)
		if err := os.WriteFile(filepath.Join(gDir, "goal.md"), []byte(goalContent), 0644); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	var successCount atomic.Int32
	var collisionCount atomic.Int32

	startSignal := make(chan struct{})

	for i := 1; i <= agentCount; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-startSignal

			slug := fmt.Sprintf("stress-goal-%02d", idx)
			agent := fmt.Sprintf("worker-%02d", idx)

			opts := ClaimOptions{
				AllowPaths: []string{"cli/internal/shared/hot_path.go"},
			}
			_, _, err := ClaimGoalWithCharter(tempDir, slug, agent, "stress testing", "", false, opts)
			if err == nil {
				successCount.Add(1)
			} else {
				if strings.Contains(err.Error(), "collision") || strings.Contains(err.Error(), "conflict") {
					collisionCount.Add(1)
				}
			}
		}(i)
	}

	// Unleash all 20 agents concurrently
	close(startSignal)
	wg.Wait()

	// Exactly 1 agent must succeed, and 19 must fail closed with collision errors
	if successCount.Load() != 1 {
		t.Fatalf("expected exactly 1 successful claim without --force, got %d", successCount.Load())
	}
	if collisionCount.Load() != agentCount-1 {
		t.Fatalf("expected %d collision rejections, got %d", agentCount-1, collisionCount.Load())
	}

	// Verify active charters list has exactly 1 entry
	activeList, err := ListActiveCharters(tempDir)
	if err != nil {
		t.Fatalf("list active charters failed: %v", err)
	}
	if len(activeList) != 1 {
		t.Fatalf("expected 1 active charter, got %d", len(activeList))
	}
}

func TestExtractFactsContractPaths(t *testing.T) {
	factsContent := `# Facts: Sila Component Engine

## Architectural Invariants & Constraints
- Immutable log

## File & Interface Contracts
- Relevant files:
  - ` + "`" + `internal/goals/charter.go` + "`" + `
  - ` + "`" + `internal/goals/charter_test.go` + "`" + `
- Loaders: ` + "`" + `internal/loaders/zcode.go` + "`" + `, ` + "`" + `internal/loaders/claude.go` + "`" + `
- Helper function ` + "`" + `NewLoader(path) Loader` + "`" + ` is exported

## Accepted Facts
- Valid
`

	paths := ExtractFactsContractPaths(factsContent)
	expected := []string{
		"internal/goals/charter.go",
		"internal/goals/charter_test.go",
		"internal/loaders/zcode.go",
		"internal/loaders/claude.go",
	}

	for _, exp := range expected {
		found := false
		for _, p := range paths {
			if p == exp {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected path %q in extracted paths, got %v", exp, paths)
		}
	}

	// Non-path tokens like "NewLoader(path) Loader" should be filtered out
	for _, p := range paths {
		if strings.Contains(p, "NewLoader") {
			t.Errorf("unexpected non-path token extracted: %q", p)
		}
	}
}

func TestCreateCharter_AutoSeedFromFactsContracts(t *testing.T) {
	tempDir := t.TempDir()
	goalDir := filepath.Join(tempDir, "goals", "autoseed-test")
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatalf("create goal dir: %v", err)
	}

	factsMD := `# Facts: Test Autoseed

## File & Interface Contracts
- ` + "`" + `cmd/sila/main.go` + "`" + `
- ` + "`" + `internal/goals/charter.go` + "`" + `
`
	if err := os.WriteFile(filepath.Join(goalDir, "facts.md"), []byte(factsMD), 0644); err != nil {
		t.Fatalf("write facts.md: %v", err)
	}

	// Create charter with nil allow_paths
	charter, err := CreateCharter(tempDir, "autoseed-test", "agent", "", nil, nil, nil, 0)
	if err != nil {
		t.Fatalf("CreateCharter failed: %v", err)
	}

	expectedPaths := []string{
		"cmd/sila/main.go",
		"internal/goals/charter.go",
		"goals/autoseed-test/*",
	}

	for _, exp := range expectedPaths {
		found := false
		for _, a := range charter.AllowPaths {
			if a == exp {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected charter to allow %q, got %v", exp, charter.AllowPaths)
		}
	}

	// Test graceful fallback when facts.md has no explicit file contracts
	fallbackDir := filepath.Join(tempDir, "goals", "fallback-test")
	_ = os.MkdirAll(fallbackDir, 0755)
	_ = os.WriteFile(filepath.Join(fallbackDir, "facts.md"), []byte("# Facts\nNo file contracts here.\n"), 0644)

	fallbackCharter, err := CreateCharter(tempDir, "fallback-test", "agent", "", nil, nil, nil, 0)
	if err != nil {
		t.Fatalf("CreateCharter fallback failed: %v", err)
	}
	if len(fallbackCharter.AllowPaths) != 1 || fallbackCharter.AllowPaths[0] != "goals/fallback-test/*" {
		t.Errorf("expected fallback charter to only allow goals/fallback-test/*, got %v", fallbackCharter.AllowPaths)
	}
}
