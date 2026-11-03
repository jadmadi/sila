package goals_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/mcp"
	"github.com/jadmadi/sila/internal/store"
)

func setupTestGoalHelper(t *testing.T, dir, slug, title, dependsOn string, seq int) {
	t.Helper()
	goalDir := filepath.Join(dir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatal(err)
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Goal: %s\n\n", title))
	b.WriteString("## Description\nGoal description\n\n")
	b.WriteString("## Dependencies\n")
	if dependsOn != "" {
		b.WriteString(fmt.Sprintf("- **Depends On**: %s\n", dependsOn))
	} else {
		b.WriteString("- **Mode**: Independent\n")
	}
	if seq > 0 {
		b.WriteString(fmt.Sprintf("- **Sequence**: #%d\n", seq))
	}
	if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(b.String()), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte("# Plan\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "facts.md"), []byte("# Facts\n"), 0644); err != nil {
		t.Fatal(err)
	}
}

func markGitCommitHelper(t *testing.T, dir, slug string) {
	t.Helper()
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	_ = cmd.Run()
	cmd = exec.Command("git", "config", "user.email", "test@example.com")
	cmd.Dir = dir
	_ = cmd.Run()
	cmd = exec.Command("git", "config", "user.name", "Test User")
	cmd.Dir = dir
	_ = cmd.Run()

	dummyFile := filepath.Join(dir, "impl_"+slug+".txt")
	if err := os.WriteFile(dummyFile, []byte("implemented "+slug), 0644); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command("git", "add", ".")
	cmd.Dir = dir
	_ = cmd.Run()

	cmd = exec.Command("git", "commit", "-m", fmt.Sprintf("feat: implement %s (goals/%s/goal.md)", slug, slug))
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit failed: %s (err: %v)", string(out), err)
	}
}

func TestChallenger_DeepLinearChain(t *testing.T) {
	dir := t.TempDir()

	setupTestGoalHelper(t, dir, "goal-a", "Goal A", "", 1)
	setupTestGoalHelper(t, dir, "goal-b", "Goal B", "goal-a", 2)
	setupTestGoalHelper(t, dir, "goal-c", "Goal C", "goal-b", 3)
	setupTestGoalHelper(t, dir, "goal-d", "Goal D", "goal-c", 4)

	// Step 1: None implemented
	audit, err := goals.ScanGoals(dir)
	if err != nil {
		t.Fatalf("scan goals: %v", err)
	}
	var gD goals.Goal
	for _, g := range audit.Goals {
		if g.Slug == "goal-d" {
			gD = g
		}
	}
	if !gD.Blocked {
		t.Fatalf("goal-d should be blocked")
	}
	if !reflect.DeepEqual(gD.UnmetDeps, []string{"goal-c"}) {
		t.Fatalf("goal-d unmet deps should be [goal-c], got %v", gD.UnmetDeps)
	}
	if !reflect.DeepEqual(gD.RootBlockers, []string{"goal-a"}) {
		t.Fatalf("goal-d root blockers should be [goal-a], got %v", gD.RootBlockers)
	}

	// Attempt claim D without force -> must fail with root blocker
	_, err = goals.ClaimGoal(dir, "goal-d", "agent-x", "test note", false)
	if err == nil {
		t.Fatal("expected claim goal-d without force to fail")
	}
	expectedErr := `cannot claim blocked goal "goal-d": unmet prerequisites: [goal-c] (root blockers: [goal-a]) (use --force to override)`
	if err.Error() != expectedErr {
		t.Fatalf("unexpected error message: %q, wanted: %q", err.Error(), expectedErr)
	}

	// Attempt claim D with force=true -> must succeed atomically
	claim, err := goals.ClaimGoal(dir, "goal-d", "agent-x", "forced note", true)
	if err != nil || claim == nil {
		t.Fatalf("claiming goal-d with force should succeed: %v", err)
	}
	if claim.GoalSlug != "goal-d" || claim.Agent != "agent-x" {
		t.Fatalf("claim details mismatch: %+v", claim)
	}

	// Release goal-d
	if err := goals.ReleaseGoal(dir, "goal-d"); err != nil {
		t.Fatalf("release goal-d: %v", err)
	}

	// Step 2: Implement goal-a in git
	markGitCommitHelper(t, dir, "goal-a")
	audit, err = goals.ScanGoals(dir)
	if err != nil {
		t.Fatalf("scan goals after git: %v", err)
	}
	for _, g := range audit.Goals {
		if g.Slug == "goal-d" {
			gD = g
		}
	}
	if !gD.Blocked {
		t.Fatalf("goal-d should still be blocked")
	}
	if !reflect.DeepEqual(gD.UnmetDeps, []string{"goal-c"}) {
		t.Fatalf("goal-d unmet deps should be [goal-c], got %v", gD.UnmetDeps)
	}
	if !reflect.DeepEqual(gD.RootBlockers, []string{"goal-b"}) {
		t.Fatalf("goal-d root blockers should now be [goal-b], got %v", gD.RootBlockers)
	}

	_, err = goals.ClaimGoal(dir, "goal-d", "agent-x", "test note", false)
	if err == nil {
		t.Fatal("expected claim goal-d without force to fail")
	}
	expectedErr = `cannot claim blocked goal "goal-d": unmet prerequisites: [goal-c] (root blockers: [goal-b]) (use --force to override)`
	if err.Error() != expectedErr {
		t.Fatalf("unexpected error message: %q, wanted: %q", err.Error(), expectedErr)
	}

	// Step 3: Implement goal-b in git
	markGitCommitHelper(t, dir, "goal-b")
	audit, err = goals.ScanGoals(dir)
	if err != nil {
		t.Fatalf("scan goals: %v", err)
	}
	for _, g := range audit.Goals {
		if g.Slug == "goal-d" {
			gD = g
		}
	}
	if !gD.Blocked {
		t.Fatalf("goal-d should still be blocked")
	}
	if !reflect.DeepEqual(gD.UnmetDeps, []string{"goal-c"}) {
		t.Fatalf("goal-d unmet deps should be [goal-c]")
	}
	if !reflect.DeepEqual(gD.RootBlockers, []string{"goal-c"}) {
		t.Fatalf("goal-d root blockers should now be [goal-c]")
	}

	// When unmet == root blockers, sliceEqual is true, no redundant root blockers tag
	_, err = goals.ClaimGoal(dir, "goal-d", "agent-x", "test note", false)
	if err == nil {
		t.Fatal("expected claim goal-d without force to fail")
	}
	expectedErr = `cannot claim blocked goal "goal-d": unmet prerequisites: [goal-c] (use --force to override)`
	if err.Error() != expectedErr {
		t.Fatalf("unexpected error message: %q, wanted: %q", err.Error(), expectedErr)
	}

	// Step 4: Implement goal-c in git
	markGitCommitHelper(t, dir, "goal-c")
	audit, err = goals.ScanGoals(dir)
	if err != nil {
		t.Fatalf("scan goals: %v", err)
	}
	for _, g := range audit.Goals {
		if g.Slug == "goal-d" {
			gD = g
		}
	}
	if gD.Blocked {
		t.Fatalf("goal-d should now be unblocked (ready)")
	}
	if len(gD.UnmetDeps) != 0 {
		t.Fatalf("goal-d should have 0 unmet deps")
	}

	claim, err = goals.ClaimGoal(dir, "goal-d", "agent-x", "now unblocked", false)
	if err != nil || claim == nil {
		t.Fatalf("claiming unblocked goal-d without force should succeed: %v", err)
	}
}

func TestChallenger_DiamondGraph(t *testing.T) {
	dir := t.TempDir()

	setupTestGoalHelper(t, dir, "goal-a", "Goal A", "", 1)
	setupTestGoalHelper(t, dir, "goal-b", "Goal B", "goal-a", 2)
	setupTestGoalHelper(t, dir, "goal-c", "Goal C", "goal-a", 3)
	setupTestGoalHelper(t, dir, "goal-d", "Goal D", "goal-b, goal-c", 4)

	// When A is pending: D unmet = [goal-b, goal-c], root blockers = [goal-a]
	audit, err := goals.ScanGoals(dir)
	if err != nil {
		t.Fatalf("scan goals: %v", err)
	}
	var gD goals.Goal
	for _, g := range audit.Goals {
		if g.Slug == "goal-d" {
			gD = g
		}
	}
	sort.Strings(gD.UnmetDeps)
	if !reflect.DeepEqual(gD.UnmetDeps, []string{"goal-b", "goal-c"}) {
		t.Fatalf("unmet deps mismatch: %v", gD.UnmetDeps)
	}
	if !reflect.DeepEqual(gD.RootBlockers, []string{"goal-a"}) {
		t.Fatalf("root blockers mismatch: %v", gD.RootBlockers)
	}

	_, err = goals.ClaimGoal(dir, "goal-d", "agent-1", "", false)
	if err == nil {
		t.Fatal("claim D should fail without force")
	}
	if !strings.Contains(err.Error(), `cannot claim blocked goal "goal-d": unmet prerequisites: [goal-b goal-c] (root blockers: [goal-a])`) {
		t.Fatalf("err: %v", err)
	}

	// Implement A in git
	markGitCommitHelper(t, dir, "goal-a")
	audit, err = goals.ScanGoals(dir)
	if err != nil {
		t.Fatalf("scan goals: %v", err)
	}
	for _, g := range audit.Goals {
		if g.Slug == "goal-d" {
			gD = g
		}
	}
	sort.Strings(gD.UnmetDeps)
	sort.Strings(gD.RootBlockers)
	if !reflect.DeepEqual(gD.UnmetDeps, []string{"goal-b", "goal-c"}) {
		t.Fatalf("unmet: %v", gD.UnmetDeps)
	}
	if !reflect.DeepEqual(gD.RootBlockers, []string{"goal-b", "goal-c"}) {
		t.Fatalf("root: %v", gD.RootBlockers)
	}

	_, err = goals.ClaimGoal(dir, "goal-d", "agent-1", "", false)
	if err == nil {
		t.Fatal("claim D should fail without force")
	}
	if !strings.Contains(err.Error(), `cannot claim blocked goal "goal-d": unmet prerequisites: [goal-b goal-c] (use --force to override)`) {
		t.Fatalf("err: %v", err)
	}
}

func TestChallenger_MultipleRootBlockers(t *testing.T) {
	dir := t.TempDir()

	setupTestGoalHelper(t, dir, "goal-a1", "Goal A1", "", 1)
	setupTestGoalHelper(t, dir, "goal-a2", "Goal A2", "", 1)
	setupTestGoalHelper(t, dir, "goal-b", "Goal B", "goal-a1", 2)
	setupTestGoalHelper(t, dir, "goal-c", "Goal C", "goal-a2", 2)
	setupTestGoalHelper(t, dir, "goal-d", "Goal D", "goal-b, goal-c", 3)

	audit, err := goals.ScanGoals(dir)
	if err != nil {
		t.Fatalf("scan goals: %v", err)
	}
	var gD goals.Goal
	for _, g := range audit.Goals {
		if g.Slug == "goal-d" {
			gD = g
		}
	}
	sort.Strings(gD.UnmetDeps)
	sort.Strings(gD.RootBlockers)
	if !reflect.DeepEqual(gD.UnmetDeps, []string{"goal-b", "goal-c"}) {
		t.Fatalf("unmet: %v", gD.UnmetDeps)
	}
	if !reflect.DeepEqual(gD.RootBlockers, []string{"goal-a1", "goal-a2"}) {
		t.Fatalf("root: %v", gD.RootBlockers)
	}

	_, err = goals.ClaimGoal(dir, "goal-d", "agent-1", "", false)
	if err == nil {
		t.Fatal("claim D should fail")
	}
	expectedErr := `cannot claim blocked goal "goal-d": unmet prerequisites: [goal-b goal-c] (root blockers: [goal-a1 goal-a2]) (use --force to override)`
	if err.Error() != expectedErr {
		t.Fatalf("unexpected: %q", err.Error())
	}
}

func TestChallenger_MissingPrerequisite(t *testing.T) {
	dir := t.TempDir()

	setupTestGoalHelper(t, dir, "goal-z", "Goal Z", "ghost-dependency", 1)

	audit, err := goals.ScanGoals(dir)
	if err != nil {
		t.Fatalf("scan goals: %v", err)
	}
	var gZ goals.Goal
	for _, g := range audit.Goals {
		if g.Slug == "goal-z" {
			gZ = g
		}
	}
	if !gZ.Blocked {
		t.Fatalf("goal-z should be blocked")
	}
	if !reflect.DeepEqual(gZ.UnmetDeps, []string{"ghost-dependency"}) {
		t.Fatalf("unmet: %v", gZ.UnmetDeps)
	}
	if !reflect.DeepEqual(gZ.RootBlockers, []string{"ghost-dependency"}) {
		t.Fatalf("root: %v", gZ.RootBlockers)
	}

	_, err = goals.ClaimGoal(dir, "goal-z", "agent-1", "", false)
	if err == nil {
		t.Fatal("claim Z should fail")
	}
	expectedErr := `cannot claim blocked goal "goal-z": unmet prerequisites: [ghost-dependency] (use --force to override)`
	if err.Error() != expectedErr {
		t.Fatalf("unexpected: %q", err.Error())
	}

	// Claim with force
	claim, err := goals.ClaimGoal(dir, "goal-z", "agent-1", "override ghost", true)
	if err != nil || claim == nil {
		t.Fatalf("forced claim on missing dep goal should succeed: %v", err)
	}
}

func TestChallenger_Cycles(t *testing.T) {
	// 1. 2-node cycle (A -> B -> A)
	{
		dir := t.TempDir()
		setupTestGoalHelper(t, dir, "node-a", "Node A", "node-b", 1)
		setupTestGoalHelper(t, dir, "node-b", "Node B", "node-a", 2)

		_, err := goals.ClaimGoal(dir, "node-a", "agent-1", "", false)
		if err == nil {
			t.Fatal("claim node-a in 2-cycle should fail")
		}
		if !strings.Contains(err.Error(), `cannot claim blocked goal "node-a": circular dependency: node-a -> node-b -> node-a (use --force to override)`) {
			t.Fatalf("got: %v", err)
		}

		claim, err := goals.ClaimGoal(dir, "node-a", "agent-1", "override cycle", true)
		if err != nil || claim == nil {
			t.Fatalf("force claim on cycle node should succeed: %v", err)
		}
	}

	// 2. 3-node cycle (A -> B -> C -> A)
	{
		dir := t.TempDir()
		setupTestGoalHelper(t, dir, "node-a", "Node A", "node-b", 1)
		setupTestGoalHelper(t, dir, "node-b", "Node B", "node-c", 2)
		setupTestGoalHelper(t, dir, "node-c", "Node C", "node-a", 3)

		_, err := goals.ClaimGoal(dir, "node-c", "agent-1", "", false)
		if err == nil {
			t.Fatal("claim node-c in 3-cycle should fail")
		}
		if !strings.Contains(err.Error(), `cannot claim blocked goal "node-c": circular dependency: node-a -> node-b -> node-c -> node-a (use --force to override)`) {
			t.Fatalf("got: %v", err)
		}
	}

	// 3. Self cycle (A -> A)
	{
		dir := t.TempDir()
		setupTestGoalHelper(t, dir, "node-self", "Node Self", "node-self", 1)

		_, err := goals.ClaimGoal(dir, "node-self", "agent-1", "", false)
		if err == nil {
			t.Fatal("claim node-self should fail")
		}
		if !strings.Contains(err.Error(), `cannot claim blocked goal "node-self": circular dependency: node-self -> node-self (use --force to override)`) {
			t.Fatalf("got: %v", err)
		}
	}
}

func TestChallenger_DependentOnCycle(t *testing.T) {
	dir := t.TempDir()

	setupTestGoalHelper(t, dir, "node-b", "Node B", "node-c", 1)
	setupTestGoalHelper(t, dir, "node-c", "Node C", "node-b", 2)
	setupTestGoalHelper(t, dir, "node-d", "Node D", "node-c", 3)
	setupTestGoalHelper(t, dir, "node-e", "Node E", "node-d", 4)

	audit, err := goals.ScanGoals(dir)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	var gE goals.Goal
	for _, g := range audit.Goals {
		if g.Slug == "node-e" {
			gE = g
		}
	}
	if !gE.Blocked {
		t.Fatalf("node-e should be blocked")
	}
	if !reflect.DeepEqual(gE.UnmetDeps, []string{"node-d"}) {
		t.Fatalf("unmet: %v", gE.UnmetDeps)
	}

	_, err = goals.ClaimGoal(dir, "node-e", "agent-1", "", false)
	if err == nil {
		t.Fatal("claim node-e should fail")
	}
}

func TestChallenger_ConcurrentBlockedClaims(t *testing.T) {
	dir := t.TempDir()

	setupTestGoalHelper(t, dir, "dep-root", "Dep Root", "", 1)
	setupTestGoalHelper(t, dir, "dep-leaf", "Dep Leaf", "dep-root", 2)

	// Step 1: 50 concurrent agents attempting unforced claim on blocked goal
	var wg sync.WaitGroup
	var rejectedCount atomic.Int32
	var successCount atomic.Int32

	for i := range 50 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			agent := fmt.Sprintf("agent-%d", idx)
			_, err := goals.ClaimGoal(dir, "dep-leaf", agent, "trying unforced", false)
			if err != nil {
				rejectedCount.Add(1)
			} else {
				successCount.Add(1)
			}
		}(i)
	}
	wg.Wait()

	if rejectedCount.Load() != 50 || successCount.Load() != 0 {
		t.Fatalf("expected all 50 rejected, got rejected=%d, success=%d", rejectedCount.Load(), successCount.Load())
	}

	// Step 2: 50 concurrent agents attempting forced claim on blocked goal
	var forcedSuccess atomic.Int32
	var forcedConflict atomic.Int32

	for i := range 50 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			agent := fmt.Sprintf("force-agent-%d", idx)
			claim, err := goals.ClaimGoal(dir, "dep-leaf", agent, "forced concurrent", true)
			if err == nil && claim != nil {
				forcedSuccess.Add(1)
			} else {
				forcedConflict.Add(1)
			}
		}(i)
	}
	wg.Wait()

	if forcedSuccess.Load() < 1 {
		t.Fatalf("expected at least 1 forced claim to succeed")
	}

	// Verify claim.json is valid JSON on disk
	claimData, err := os.ReadFile(filepath.Join(dir, "goals", "dep-leaf", "claim.json"))
	if err != nil {
		t.Fatalf("read claim.json: %v", err)
	}
	var parsedClaim goals.GoalClaim
	if err := json.Unmarshal(claimData, &parsedClaim); err != nil {
		t.Fatalf("claim.json must be valid uncorrupted JSON: %v", err)
	}
	if parsedClaim.GoalSlug != "dep-leaf" {
		t.Fatalf("claim slug mismatch: %s", parsedClaim.GoalSlug)
	}
	if !strings.HasPrefix(parsedClaim.Agent, "force-agent-") {
		t.Fatalf("winner agent mismatch: %s", parsedClaim.Agent)
	}
}

func TestChallenger_MCPBlockedClaims(t *testing.T) {
	dir := t.TempDir()

	setupTestGoalHelper(t, dir, "mcp-root", "MCP Root", "", 1)
	setupTestGoalHelper(t, dir, "mcp-leaf", "MCP Leaf", "mcp-root", 2)
	setupTestGoalHelper(t, dir, "mcp-cycle-a", "Cycle A", "mcp-cycle-b", 1)
	setupTestGoalHelper(t, dir, "mcp-cycle-b", "Cycle B", "mcp-cycle-a", 2)

	st, err := store.Open(filepath.Join(dir, "sila.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	requests := []string{
		// 1. claim_goal without force on blocked goal
		fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"claim_goal","arguments":{"project":%q,"slug":"mcp-leaf","agent":"mcp-agent","force":false}}}`, dir),
		// 2. claim_goal with force on blocked goal
		fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"claim_goal","arguments":{"project":%q,"slug":"mcp-leaf","agent":"mcp-agent","force":true}}}`, dir),
		// 3. claim_goal without force on cycle goal
		fmt.Sprintf(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"claim_goal","arguments":{"project":%q,"slug":"mcp-cycle-a","agent":"mcp-agent","force":false}}}`, dir),
	}

	in := strings.NewReader(strings.Join(requests, "\n") + "\n")
	var out bytes.Buffer

	srv := mcp.NewServer(st, in, &out)
	if err := srv.Run(); err != nil {
		t.Fatalf("mcp server run: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != len(requests) {
		t.Fatalf("expected %d responses, got %d:\n%s", len(requests), len(lines), out.String())
	}

	// 1. Check response 1 (blocked claim error)
	var resp1 struct {
		ID    int `json:"id"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Data    string `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &resp1); err != nil {
		t.Fatalf("unmarshal resp1: %v", err)
	}
	if resp1.Error == nil {
		t.Fatalf("expected error on unforced blocked claim in MCP, got: %s", lines[0])
	}
	if resp1.Error.Code != -32603 {
		t.Fatalf("expected code -32603, got %d", resp1.Error.Code)
	}
	if !strings.Contains(resp1.Error.Data, `cannot claim blocked goal "mcp-leaf": unmet prerequisites: [mcp-root]`) {
		t.Fatalf("unexpected error data: %s", resp1.Error.Data)
	}

	// 2. Check response 2 (forced claim success)
	var resp2 struct {
		ID     int            `json:"id"`
		Result map[string]any `json:"result"`
		Error  any            `json:"error"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &resp2); err != nil {
		t.Fatalf("unmarshal resp2: %v", err)
	}
	if resp2.Error != nil {
		t.Fatalf("expected no error on forced claim in MCP, got: %+v", resp2.Error)
	}
	if resp2.Result == nil {
		t.Fatalf("expected result on forced claim in MCP, got nil")
	}

	// 3. Check response 3 (cycle claim error)
	var resp3 struct {
		ID    int `json:"id"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Data    string `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(lines[2]), &resp3); err != nil {
		t.Fatalf("unmarshal resp3: %v", err)
	}
	if resp3.Error == nil {
		t.Fatalf("expected error on cycle claim in MCP, got: %s", lines[2])
	}
	if !strings.Contains(resp3.Error.Data, `circular dependency: mcp-cycle-a -> mcp-cycle-b -> mcp-cycle-a`) {
		t.Fatalf("unexpected error data for cycle: %s", resp3.Error.Data)
	}
}
