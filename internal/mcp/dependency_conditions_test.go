package mcp

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/store"
)

func setupMCPTestPredecessor(t *testing.T, dir, slug, decisionVal string) {
	t.Helper()
	goalDir := filepath.Join(dir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatalf("mkdir goalDir: %v", err)
	}

	goalMD := "# Goal: " + slug + "\n\n- Tier: roadmap\n- Shape: ship\n"
	planMD := "# Plan: " + slug + "\n\n- [x] Step 1\n"
	factsMD := "# Facts: " + slug + "\n\n- Contracts: decision.json\n"

	_ = os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalMD), 0644)
	_ = os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte(planMD), 0644)
	_ = os.WriteFile(filepath.Join(goalDir, "facts.md"), []byte(factsMD), 0644)

	wp := &goals.WorkPackage{
		SchemaVersion: goals.WorkPackageSchemaVersion,
		GoalSlug:      slug,
		Revision:      1,
		Purpose:       "Predecessor goal providing decision",
		Tasks: []goals.TaskContract{
			{
				ID:            "T-001",
				Title:         "Evaluate options",
				Context:       "Evaluation context",
				Why:           "Produce decision artifact",
				Files:         goals.TaskFiles{Create: []string{"decision.json"}},
				AcceptanceIDs: []string{"AC-001"},
				Checks: []goals.Check{
					{
						ID:               "C-001",
						Executable:       "go",
						Args:             []string{"test"},
						TimeoutSeconds:   10,
						ExpectedExitCode: 0,
						Scope:            "unit",
						Capability:       "local",
						Assertions: []goals.Assertion{
							{Kind: "go_test_pass", Target: "test", Expected: true},
						},
					},
				},
				ReturnFormat: goals.TaskResultReturnFormat,
			},
		},
		Acceptance: []goals.AcceptanceCase{
			{
				ID:            "AC-001",
				Requirement:   "Decision is made",
				Given:         "Evaluation is complete",
				Action:        "Write decision",
				Expected:      "Decision written cleanly",
				RequiredScope: "unit",
				Mandatory:     true,
				CheckIDs:      []string{"C-001"},
			},
		},
	}
	if err := goals.SaveWorkPackage(dir, slug, wp); err != nil {
		t.Fatalf("SaveWorkPackage: %v", err)
	}

	review := validTestSpecReview(wp)
	if err := goals.SaveSpecReview(dir, slug, review); err != nil {
		t.Fatalf("SaveSpecReview: %v", err)
	}

	setMCPTestPredecessorDecision(t, dir, slug, decisionVal)
}

func validTestSpecReview(wp *goals.WorkPackage) goals.SpecReview {
	digest, _ := goals.WorkPackageDigest(wp)
	return goals.SpecReview{
		SchemaVersion: goals.SpecReviewSchemaVersion,
		SpecDigest:    digest,
		Reviewer:      "strong-planner-agent",
		ReviewedAt:    time.Now().UTC().Format(time.RFC3339),
		Verdict:       goals.VerdictReady,
		Shortcuts: []goals.ShortcutCounter{
			{
				IncorrectImplementation: "implement retry by infinite loop",
				ExcludedByIDs:           []string{"T-001", "AC-001"},
				Resolution:              "T-001 caps retry attempts to 2",
			},
			{
				IncorrectImplementation: "swallow disk I/O errors silently",
				ExcludedByIDs:           []string{"T-001"},
				Resolution:              "T-001 explicitly requires returning unwrapped error",
			},
			{
				IncorrectImplementation: "bypass boundary checks with path prefix matching",
				ExcludedByIDs:           []string{"AC-001"},
				Resolution:              "AC-001 specifies filepath.Rel check against repository root",
			},
		},
		UnresolvedBlockers: []string{},
	}
}

func setMCPTestPredecessorDecision(t *testing.T, dir, slug, decisionVal string) {
	t.Helper()
	goalDir := filepath.Join(dir, "goals", slug)

	decObj := map[string]any{
		"decision":    decisionVal,
		"reviewed_by": "@architect",
	}
	decBytes, _ := json.MarshalIndent(decObj, "", "  ")
	decPath := filepath.Join(goalDir, "decision.json")
	if err := os.WriteFile(decPath, decBytes, 0644); err != nil {
		t.Fatalf("write decision.json: %v", err)
	}

	decSum := sha256.Sum256(decBytes)
	decHex := hex.EncodeToString(decSum[:])

	evDir := filepath.Join(goalDir, "evidence", "T-001")
	if err := os.MkdirAll(evDir, 0755); err != nil {
		t.Fatalf("mkdir evDir: %v", err)
	}
	exit0 := 0
	ev := &goals.TaskEvidence{
		SchemaVersion: goals.TaskEvidenceSchemaVersion,
		AttemptID:     "att-1",
		GoalSlug:      slug,
		TaskID:        "T-001",
		CaseID:        "AC-001",
		CheckID:       "C-001",
		Outcome:       "pass",
		Origin:        "runner",
		Executable:    "go",
		ExitCode:      &exit0,
		Artifacts: []goals.EvidenceArtifact{
			{
				Path:   "decision.json",
				SHA256: decHex,
			},
		},
		StartedAt: time.Now().UTC().Add(-time.Minute),
		EndedAt:   time.Now().UTC(),
	}
	evBytes, _ := json.MarshalIndent(ev, "", "  ")
	receiptPath := filepath.Join(evDir, "receipt.json")
	if err := os.WriteFile(receiptPath, evBytes, 0644); err != nil {
		t.Fatalf("write receipt.json: %v", err)
	}

	wp, _ := goals.LoadWorkPackage(dir, slug)
	specDigest, _ := goals.WorkPackageDigest(wp)

	taskAcc := &goals.AcceptanceResult{
		SchemaVersion:  goals.AcceptanceResultSchemaVersion,
		GoalSlug:       slug,
		TaskID:         "T-001",
		Accepted:       true,
		EvidenceStatus: goals.EvidenceStatusAccepted,
		SpecDigest:     specDigest,
		ReceiptPaths:   []string{filepath.Join("evidence", "T-001", "receipt.json")},
		ReviewedAt:     time.Now().UTC().Format(time.RFC3339),
	}
	if err := goals.SaveTaskAcceptance(dir, slug, "T-001", taskAcc, nil, nil); err != nil {
		t.Fatalf("SaveTaskAcceptance: %v", err)
	}

	goalAcc := &goals.AcceptanceResult{
		SchemaVersion:  goals.AcceptanceResultSchemaVersion,
		GoalSlug:       slug,
		Accepted:       true,
		EvidenceStatus: goals.EvidenceStatusAccepted,
		SpecDigest:     specDigest,
		ReceiptPaths:   []string{filepath.Join("evidence", "T-001", "receipt.json")},
		ReviewedAt:     time.Now().UTC().Format(time.RFC3339),
	}
	if err := goals.SaveGoalAcceptance(dir, slug, goalAcc); err != nil {
		t.Fatalf("SaveGoalAcceptance: %v", err)
	}
}

func setupMCPTestSuccessor(t *testing.T, dir, slug, predSlug string) {
	t.Helper()
	goalDir := filepath.Join(dir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatalf("mkdir goalDir: %v", err)
	}

	goalMD := "# Goal: " + slug + "\n\n- Tier: roadmap\n- Shape: ship\n- Depends On: " + predSlug + "\n"
	planMD := "# Plan: " + slug + "\n\n- [ ] Step 1\n"
	factsMD := "# Facts: " + slug + "\n\n- Contracts: release.go\n"

	_ = os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalMD), 0644)
	_ = os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte(planMD), 0644)
	_ = os.WriteFile(filepath.Join(goalDir, "facts.md"), []byte(factsMD), 0644)

	wp := &goals.WorkPackage{
		SchemaVersion: goals.WorkPackageSchemaVersion,
		GoalSlug:      slug,
		Revision:      1,
		Purpose:       "Successor release goal requiring proceed",
		Tasks: []goals.TaskContract{
			{
				ID:            "T-001",
				Title:         "Ship release",
				Context:       "Ship context",
				Why:           "Fulfill release charter",
				Files:         goals.TaskFiles{Create: []string{"release.go"}},
				AcceptanceIDs: []string{"AC-001"},
				Checks: []goals.Check{
					{
						ID:               "C-001",
						Executable:       "go",
						Args:             []string{"test"},
						TimeoutSeconds:   10,
						ExpectedExitCode: 0,
						Scope:            "unit",
						Capability:       "local",
						Assertions: []goals.Assertion{
							{Kind: "go_test_pass", Target: "test", Expected: true},
						},
					},
				},
				ReturnFormat: goals.TaskResultReturnFormat,
			},
		},
		Acceptance: []goals.AcceptanceCase{
			{
				ID:            "AC-001",
				Requirement:   "Release builds",
				Given:         "Code is written",
				Action:        "Build binary",
				Expected:      "Build passes cleanly",
				RequiredScope: "unit",
				Mandatory:     true,
				CheckIDs:      []string{"C-001"},
			},
		},
		DependencyConditions: []goals.DependencyCondition{
			{
				Kind:     goals.DepConditionDecisionEquals,
				GoalSlug: predSlug,
				Artifact: "decision.json",
				Pointer:  "/decision",
				Equals:   "proceed",
			},
			{
				Kind:     goals.DepConditionAcceptedGoal,
				GoalSlug: predSlug,
			},
		},
	}
	if err := goals.SaveWorkPackage(dir, slug, wp); err != nil {
		t.Fatalf("SaveWorkPackage: %v", err)
	}

	review := validTestSpecReview(wp)
	if err := goals.SaveSpecReview(dir, slug, review); err != nil {
		t.Fatalf("SaveSpecReview: %v", err)
	}
}

// executeMCPCall executes a single MCP JSON-RPC tool call and returns the parsed result.
func executeMCPCall(t *testing.T, st *store.Store, toolName string, args map[string]any) (any, *JSONRPCError) {
	t.Helper()
	reqArgs, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal tool args: %v", err)
	}
	req := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":%q,"arguments":%s}}`+"\n", toolName, reqArgs)
	in := strings.NewReader(req)
	var out bytes.Buffer
	srv := NewServer(st, in, &out)
	if err := srv.Run(); err != nil {
		t.Fatalf("server run error: %v", err)
	}

	var resp struct {
		JSONRPC string `json:"jsonrpc"`
		ID      any    `json:"id"`
		Result  *struct {
			StructuredData any `json:"structuredData"`
		} `json:"result,omitempty"`
		Error *JSONRPCError `json:"error,omitempty"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("decode MCP response: %v, raw: %s", err, out.String())
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	if resp.Result != nil {
		return resp.Result.StructuredData, nil
	}
	return nil, nil
}

// TestConditionalDependencyMCP exercises AC-010, AC-011, and AC-012 through the MCP interface.
func TestConditionalDependencyMCP(t *testing.T) {
	tmp := t.TempDir()

	// Initialize git repo in tmp
	cmd := exec.Command("git", "init")
	cmd.Dir = tmp
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init failed: %v, out: %s", err, string(out))
	}

	st, err := store.Open(filepath.Join(tmp, "test_mcp.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	if _, err := st.EnsureProject(tmp); err != nil {
		t.Fatalf("EnsureProject: %v", err)
	}

	// 1. Setup assessment goal with decision = "revise"
	setupMCPTestPredecessor(t, tmp, "assessment", "revise")

	// 2. Setup release goal requiring decision = "proceed"
	setupMCPTestSuccessor(t, tmp, "release", "assessment")

	// ------------------------------------------------------------------------
	// Scenario 1 (AC-010): Decision mismatch (observed=revise, required=proceed)
	// ------------------------------------------------------------------------

	// Call list_goals(status="blocked")
	resData, rpcErr := executeMCPCall(t, st, "list_goals", map[string]any{
		"project": tmp,
		"status":  "blocked",
	})
	if rpcErr != nil {
		t.Fatalf("list_goals blocked failed: %+v", rpcErr)
	}
	resBytes, _ := json.Marshal(resData)
	var blockedGoals []struct {
		Slug     string                    `json:"slug"`
		Blocked  bool                      `json:"blocked"`
		Blockers []goals.DependencyBlocker `json:"blockers"`
	}
	if err := json.Unmarshal(resBytes, &blockedGoals); err != nil {
		t.Fatalf("unmarshal list_goals blocked: %v, raw: %s", err, string(resBytes))
	}

	var releaseBlocked *goals.DependencyBlocker
	for _, g := range blockedGoals {
		if g.Slug == "release" {
			if !g.Blocked {
				t.Errorf("release should be marked blocked")
			}
			for i := range g.Blockers {
				if g.Blockers[i].Code == goals.DiagDependencyDecisionMismatch {
					releaseBlocked = &g.Blockers[i]
					break
				}
			}
		}
	}
	if releaseBlocked == nil {
		t.Fatalf("AC-010: release goal missing decision mismatch blocker in MCP list_goals: %s", string(resBytes))
	}
	if releaseBlocked.GoalSlug != "release" || releaseBlocked.SourceGoal != "assessment" {
		t.Errorf("AC-010: unexpected blocker slugs: %+v", releaseBlocked)
	}
	if releaseBlocked.Required != "proceed" || releaseBlocked.Observed != "revise" {
		t.Errorf("AC-010: unexpected values: required=%v observed=%v", releaseBlocked.Required, releaseBlocked.Observed)
	}

	// Call list_goals(status="ready"): release must not appear
	resReady, rpcErr := executeMCPCall(t, st, "list_goals", map[string]any{
		"project": tmp,
		"status":  "ready",
	})
	if rpcErr != nil {
		t.Fatalf("list_goals ready failed: %+v", rpcErr)
	}
	readyBytes, _ := json.Marshal(resReady)
	if strings.Contains(string(readyReadyBytes(readyBytes)), "release") {
		t.Errorf("release must not appear in ready goals")
	}

	// Call claim_goal(slug="release") without force: must fail with structured blocker data
	_, claimErr := executeMCPCall(t, st, "claim_goal", map[string]any{
		"project": tmp,
		"slug":    "release",
		"agent":   "@agent",
		"note":    "test attempt",
	})
	if claimErr == nil {
		t.Fatalf("AC-010: expected claim_goal to fail on blocked goal")
	}
	errDataBytes, _ := json.Marshal(claimErr.Data)
	if !strings.Contains(string(errDataBytes), "DEPENDENCY_DECISION_MISMATCH") {
		t.Errorf("AC-010: claim_goal error data should contain DEPENDENCY_DECISION_MISMATCH: %s", string(errDataBytes))
	}

	// Call claim_goal(slug="release") with force=true: must still fail
	_, forcedErr := executeMCPCall(t, st, "claim_goal", map[string]any{
		"project": tmp,
		"slug":    "release",
		"agent":   "@agent",
		"force":   true,
	})
	if forcedErr == nil {
		t.Fatalf("expected forced claim_goal on structured decision gate to fail")
	}

	// ------------------------------------------------------------------------
	// Scenario 2 (AC-011): Missing artifact reported distinctly from revise
	// ------------------------------------------------------------------------
	decPath := filepath.Join(tmp, "goals", "assessment", "decision.json")
	if err := os.Remove(decPath); err != nil {
		t.Fatalf("remove decision.json: %v", err)
	}

	resMissing, rpcErr := executeMCPCall(t, st, "list_goals", map[string]any{
		"project": tmp,
		"status":  "blocked",
	})
	if rpcErr != nil {
		t.Fatalf("list_goals failed: %+v", rpcErr)
	}
	missBytes, _ := json.Marshal(resMissing)
	var missGoals []struct {
		Slug     string                    `json:"slug"`
		Blockers []goals.DependencyBlocker `json:"blockers"`
	}
	if err := json.Unmarshal(missBytes, &missGoals); err != nil {
		t.Fatalf("unmarshal missGoals: %v", err)
	}
	var artMissingBlocker *goals.DependencyBlocker
	for _, g := range missGoals {
		if g.Slug == "release" {
			for i := range g.Blockers {
				if g.Blockers[i].Code == goals.DiagDependencyArtifactNotFound {
					artMissingBlocker = &g.Blockers[i]
					break
				}
			}
		}
	}
	if artMissingBlocker == nil {
		t.Fatalf("AC-011: expected DEPENDENCY_ARTIFACT_NOT_FOUND blocker: %s", string(missBytes))
	}
	if artMissingBlocker.Artifact != "decision.json" {
		t.Errorf("AC-011: expected artifact decision.json, got: %s", artMissingBlocker.Artifact)
	}

	// ------------------------------------------------------------------------
	// Scenario 3 (AC-012): Decision is "stop" (successor blocked, pred not failed)
	// ------------------------------------------------------------------------
	setMCPTestPredecessorDecision(t, tmp, "assessment", "stop")

	resStop, rpcErr := executeMCPCall(t, st, "list_goals", map[string]any{
		"project": tmp,
		"status":  "blocked",
	})
	if rpcErr != nil {
		t.Fatalf("list_goals failed: %+v", rpcErr)
	}
	stopBytes, _ := json.Marshal(resStop)
	var stopGoals []struct {
		Slug     string                    `json:"slug"`
		Blockers []goals.DependencyBlocker `json:"blockers"`
	}
	_ = json.Unmarshal(stopBytes, &stopGoals)
	var stopBlocker *goals.DependencyBlocker
	for _, g := range stopGoals {
		if g.Slug == "release" {
			for i := range g.Blockers {
				if g.Blockers[i].Code == goals.DiagDependencyDecisionMismatch {
					stopBlocker = &g.Blockers[i]
					break
				}
			}
		}
	}
	if stopBlocker == nil {
		t.Fatalf("AC-012: expected decision mismatch blocker for stop decision: %s", string(stopBytes))
	}
	if stopBlocker.Required != "proceed" || stopBlocker.Observed != "stop" {
		t.Errorf("AC-012: expected required=proceed observed=stop, got: required=%v observed=%v", stopBlocker.Required, stopBlocker.Observed)
	}

	// Verify predecessor assessment is accepted/implemented in list_goals
	resAll, rpcErr := executeMCPCall(t, st, "list_goals", map[string]any{
		"project": tmp,
	})
	if rpcErr != nil {
		t.Fatalf("list_goals all failed: %+v", rpcErr)
	}
	allBytes, _ := json.Marshal(resAll)
	var allGoals []struct {
		Slug   string `json:"slug"`
		Status string `json:"status"`
	}
	_ = json.Unmarshal(allBytes, &allGoals)
	for _, g := range allGoals {
		if g.Slug == "assessment" && g.Status != "implemented" {
			t.Errorf("AC-012: assessment status should remain implemented, got: %s", g.Status)
		}
	}

	// ------------------------------------------------------------------------
	// Scenario 4: Decision is "proceed" -> release unblocks and claim succeeds
	// ------------------------------------------------------------------------
	setMCPTestPredecessorDecision(t, tmp, "assessment", "proceed")

	resReadyAfter, rpcErr := executeMCPCall(t, st, "list_goals", map[string]any{
		"project": tmp,
		"status":  "ready",
	})
	if rpcErr != nil {
		t.Fatalf("list_goals ready after proceed failed: %+v", rpcErr)
	}
	readyAfterBytes, _ := json.Marshal(resReadyAfter)
	if !strings.Contains(string(readyAfterBytes), "release") {
		t.Errorf("release must appear in ready goals after proceed: %s", string(readyAfterBytes))
	}

	claimSuccess, claimErr2 := executeMCPCall(t, st, "claim_goal", map[string]any{
		"project": tmp,
		"slug":    "release",
		"agent":   "@agent",
		"note":    "proceeding with release",
	})
	if claimErr2 != nil {
		t.Fatalf("claim_goal should succeed after decision updated to proceed: %+v", claimErr2)
	}
	claimSuccBytes, _ := json.Marshal(claimSuccess)
	if !strings.Contains(string(claimSuccBytes), "claimed") {
		t.Errorf("expected claim confirmation: %s", string(claimSuccBytes))
	}
}

func readyReadyBytes(b []byte) []byte {
	return b
}
