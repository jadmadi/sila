package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/store"
)

func makeTestWorkPackage(slug string) *goals.WorkPackage {
	return &goals.WorkPackage{
		SchemaVersion: goals.WorkPackageSchemaVersion,
		GoalSlug:      slug,
		Revision:      1,
		Purpose:       "Verify work package handling in CLI and MCP",
		Constraints:   []string{"Strict pure Go stdlib only", "Zero network calls"},
		NonGoals:      []string{"No external database"},
		Tasks: []goals.TaskContract{
			{
				ID:        "T-001",
				Title:     "Define interfaces",
				Context:   "Context for T-001",
				Why:       "Rationale for T-001",
				DependsOn: []string{},
				Files: goals.TaskFiles{
					Read:   []string{"README.md"},
					Edit:   []string{"README.md"},
					Create: []string{},
					Forbid: []string{".git/*"},
				},
				References: []goals.Reference{
					{
						Path:    "README.md",
						Symbol:  "Heading",
						Excerpt: "# Test Repo",
						SHA256:  "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
					},
				},
				Decisions: []goals.Decision{
					{
						ID:        "D-001",
						Chosen:    "Decision 1",
						Rationale: "Reason 1",
						Rejected:  []string{"Option B"},
					},
				},
				Steps: []goals.Step{
					{
						ID:        "S-001",
						Action:    "Run check",
						Expected:  "Pass",
						OnFailure: "Fix",
					},
				},
				Interfaces:  []string{"Interface 1"},
				Constraints: []string{"Constraint 1"},
				Examples: []goals.Example{
					{
						Input:       "input",
						Output:      "output",
						Explanation: "explanation",
					},
				},
				AcceptanceIDs: []string{"AC-001"},
				Checks: []goals.Check{
					{
						ID:               "C-001",
						Executable:       "true",
						Args:             []string{},
						Cwd:              "cli",
						TimeoutSeconds:   30,
						ExpectedExitCode: 0,
						Scope:            goals.ScopeUnit,
						Assertions: []goals.Assertion{
							{
								Kind:     goals.AssertionStdoutExact,
								Target:   "stdout",
								Expected: "ok",
							},
						},
						Capability: goals.CapabilityLocal,
					},
				},
				StopConditions: []string{"blocked"},
				ReturnFormat:   goals.TaskResultReturnFormat,
			},
			{
				ID:        "T-002",
				Title:     "Implement logic",
				Context:   "Context for T-002",
				Why:       "Rationale for T-002",
				DependsOn: []string{"T-001"},
				Files: goals.TaskFiles{
					Read:   []string{"README.md"},
					Edit:   []string{"README.md"},
					Create: []string{},
					Forbid: []string{".git/*"},
				},
				References: []goals.Reference{},
				Decisions:  []goals.Decision{},
				Steps: []goals.Step{
					{
						ID:        "S-002",
						Action:    "Run check 2",
						Expected:  "Pass",
						OnFailure: "Fix",
					},
				},
				Interfaces:  []string{"Interface 2"},
				Constraints: []string{"Constraint 2"},
				Examples: []goals.Example{
					{
						Input:       "input2",
						Output:      "output2",
						Explanation: "explanation2",
					},
				},
				AcceptanceIDs: []string{"AC-002"},
				Checks: []goals.Check{
					{
						ID:               "C-002",
						Executable:       "true",
						Args:             []string{},
						Cwd:              "cli",
						TimeoutSeconds:   30,
						ExpectedExitCode: 0,
						Scope:            goals.ScopeUnit,
						Assertions: []goals.Assertion{
							{
								Kind:     goals.AssertionStdoutExact,
								Target:   "stdout",
								Expected: "ok",
							},
						},
						Capability: goals.CapabilityLocal,
					},
				},
				StopConditions: []string{"blocked"},
				ReturnFormat:   goals.TaskResultReturnFormat,
			},
		},
		Acceptance: []goals.AcceptanceCase{
			{
				ID:            "AC-001",
				Requirement:   "Req 1",
				Given:         "Given 1",
				Action:        "Action 1",
				Expected:      "Expected 1",
				RequiredScope: goals.ScopeUnit,
				Mandatory:     true,
				CheckIDs:      []string{"C-001"},
			},
			{
				ID:            "AC-002",
				Requirement:   "Req 2",
				Given:         "Given 2",
				Action:        "Action 2",
				Expected:      "Expected 2",
				RequiredScope: goals.ScopeUnit,
				Mandatory:     true,
				CheckIDs:      []string{"C-002"},
			},
		},
		DependencyConditions: []goals.DependencyCondition{},
		UnresolvedDecisions:  []string{},
		RetiredTaskIDs:       []string{},
		RetiredAcceptanceIDs: []string{},
	}
}

func TestCreateGoalWorkPackage(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	callTool := func(toolName string, args map[string]any) (map[string]any, *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}) {
		reqArgs, _ := json.Marshal(args)
		req := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":%q,"arguments":%s}}`+"\n", toolName, reqArgs)
		in := strings.NewReader(req)
		var out bytes.Buffer
		srv := NewServer(st, in, &out)
		if err := srv.Run(); err != nil {
			t.Fatalf("server.Run() failed: %v", err)
		}
		var resp struct {
			Result map[string]any `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode response: %v; raw: %s", err, out.String())
		}
		return resp.Result, resp.Error
	}

	t.Run("CreateWithValidWorkPackage", func(t *testing.T) {
		repoDir := t.TempDir()
		_ = os.MkdirAll(filepath.Join(repoDir, "goals"), 0755)
		_ = os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("# Test Repo\n"), 0644)

		wp := makeTestWorkPackage("mcp-pkg-feature")
		wpData, err := json.Marshal(wp)
		if err != nil {
			t.Fatal(err)
		}
		var wpMap map[string]any
		if err := json.Unmarshal(wpData, &wpMap); err != nil {
			t.Fatal(err)
		}

		res, rpcErr := callTool("create_goal", map[string]any{
			"project":      repoDir,
			"slug":         "mcp-pkg-feature",
			"work_package": wpMap,
		})
		if rpcErr != nil {
			t.Fatalf("create_goal returned error: %d %s", rpcErr.Code, rpcErr.Message)
		}
		if res == nil {
			t.Fatal("expected non-nil result from create_goal")
		}

		goalDir := filepath.Join(repoDir, "goals", "mcp-pkg-feature")

		// Verify goal.md references work-package.json
		goalMD, err := os.ReadFile(filepath.Join(goalDir, "goal.md"))
		if err != nil {
			t.Fatalf("read goal.md: %v", err)
		}
		if !strings.Contains(string(goalMD), "work-package.json") {
			t.Errorf("goal.md should reference work-package.json")
		}

		// Verify plan.md lists T-001 and T-002
		planMD, err := os.ReadFile(filepath.Join(goalDir, "plan.md"))
		if err != nil {
			t.Fatalf("read plan.md: %v", err)
		}
		planStr := string(planMD)
		if !strings.Contains(planStr, "T-001") || !strings.Contains(planStr, "T-002") {
			t.Errorf("plan.md should list T-001 and T-002, got:\n%s", planStr)
		}

		// Verify work-package.json exists, decodes cleanly, and is byte-equivalent to formatted JSON
		wpDisk, err := os.ReadFile(filepath.Join(goalDir, "work-package.json"))
		if err != nil {
			t.Fatalf("read work-package.json: %v", err)
		}
		decodedWP, err := goals.DecodeWorkPackage(wpDisk)
		if err != nil {
			t.Fatalf("decode work-package.json: %v", err)
		}
		if decodedWP.GoalSlug != "mcp-pkg-feature" || len(decodedWP.Tasks) != 2 {
			t.Errorf("unexpected decoded work package content: %+v", decodedWP)
		}

		// AC-010: Check byte equivalence against formatted JSON
		expectedBytes, err := json.MarshalIndent(decodedWP, "", "  ")
		if err != nil {
			t.Fatalf("indent encoding: %v", err)
		}
		expectedBytes = append(expectedBytes, '\n')
		if !bytes.Equal(wpDisk, expectedBytes) {
			t.Errorf("AC-010 violation: on-disk work-package.json is not byte-equivalent to canonical JSON")
		}
	})

	t.Run("CreateWithInvalidWorkPackageFailsAndNoDir", func(t *testing.T) {
		repoDir := t.TempDir()
		_ = os.MkdirAll(filepath.Join(repoDir, "goals"), 0755)

		badWP := map[string]any{
			"schema_version": "sila/work-package/v1",
			"goal_slug":      "bad-mcp-slug",
			"purpose":        "", // empty purpose violates schema
			"tasks":          []any{},
		}

		res, rpcErr := callTool("create_goal", map[string]any{
			"project":      repoDir,
			"slug":         "bad-mcp-slug",
			"work_package": badWP,
		})

		// Must yield an error (rpcErr != nil or res["isError"] == true)
		isErr := rpcErr != nil || (res != nil && res["isError"] == true)
		if !isErr {
			t.Fatalf("expected error for invalid work package, got success: %+v", res)
		}

		// AC-011: Verify no partial goal directory created
		goalDir := filepath.Join(repoDir, "goals", "bad-mcp-slug")
		if _, statErr := os.Stat(goalDir); !os.IsNotExist(statErr) {
			t.Errorf("AC-011 violation: partial goal directory %s was created on error", goalDir)
		}
	})

	t.Run("ConflictTasksAndWorkPackageFails", func(t *testing.T) {
		repoDir := t.TempDir()
		_ = os.MkdirAll(filepath.Join(repoDir, "goals"), 0755)
		_ = os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("# Test Repo\n"), 0644)

		wp := makeTestWorkPackage("mcp-pkg-feature")
		wpData, _ := json.Marshal(wp)
		var wpMap map[string]any
		_ = json.Unmarshal(wpData, &wpMap)

		res, rpcErr := callTool("create_goal", map[string]any{
			"project":      repoDir,
			"slug":         "mcp-pkg-feature",
			"work_package": wpMap,
			"tasks":        []string{"Phase 1: Conflict"},
		})

		isErr := rpcErr != nil || (res != nil && res["isError"] == true)
		if !isErr {
			t.Fatalf("expected error when both work_package and tasks provided, got: %+v", res)
		}

		goalDir := filepath.Join(repoDir, "goals", "mcp-pkg-feature")
		if _, statErr := os.Stat(goalDir); !os.IsNotExist(statErr) {
			t.Errorf("partial goal directory %s was created on conflicting parameters", goalDir)
		}
	})

	t.Run("MismatchedGoalSlugFails", func(t *testing.T) {
		repoDir := t.TempDir()
		_ = os.MkdirAll(filepath.Join(repoDir, "goals"), 0755)
		_ = os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("# Test Repo\n"), 0644)

		wp := makeTestWorkPackage("mcp-pkg-feature")
		wpData, _ := json.Marshal(wp)
		var wpMap map[string]any
		_ = json.Unmarshal(wpData, &wpMap)

		res, rpcErr := callTool("create_goal", map[string]any{
			"project":      repoDir,
			"slug":         "different-slug",
			"work_package": wpMap, // has goal_slug "mcp-pkg-feature"
		})

		isErr := rpcErr != nil || (res != nil && res["isError"] == true)
		if !isErr {
			t.Fatalf("expected error for mismatched goal_slug, got: %+v", res)
		}

		goalDir := filepath.Join(repoDir, "goals", "different-slug")
		if _, statErr := os.Stat(goalDir); !os.IsNotExist(statErr) {
			t.Errorf("partial goal directory %s was created on mismatched slug", goalDir)
		}
	})

	t.Run("LegacyTasksStillWorkAndToolCountUnchanged", func(t *testing.T) {
		repoDir := t.TempDir()
		_ = os.MkdirAll(filepath.Join(repoDir, "goals"), 0755)

		res, rpcErr := callTool("create_goal", map[string]any{
			"project": repoDir,
			"slug":    "legacy-mcp-goal",
			"tasks":   []string{"Phase 1: Do A", "Phase 2: Do B"},
		})
		if rpcErr != nil {
			t.Fatalf("expected success for legacy create_goal: %v", rpcErr)
		}
		if res == nil {
			t.Fatal("expected result for legacy goal creation")
		}

		goalDir := filepath.Join(repoDir, "goals", "legacy-mcp-goal")
		planMD, err := os.ReadFile(filepath.Join(goalDir, "plan.md"))
		if err != nil {
			t.Fatalf("read plan.md: %v", err)
		}
		if !strings.Contains(string(planMD), "Phase 1") {
			t.Errorf("plan.md should contain legacy tasks, got:\n%s", string(planMD))
		}

		// Ensure work-package.json does NOT exist
		wpPath := filepath.Join(goalDir, "work-package.json")
		if _, statErr := os.Stat(wpPath); !os.IsNotExist(statErr) {
			t.Errorf("expected no work-package.json for legacy goal, but file exists")
		}

		// AC-012: Tool count unchanged
		tools := ToolDefinitions()
		if len(tools) != PublicToolCount {
			t.Errorf("AC-012 violation: tool count changed, got %d, want %d", len(tools), PublicToolCount)
		}
	})
}
