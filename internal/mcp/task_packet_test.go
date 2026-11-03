package mcp

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/store"
)

func TestMCPTaskPacket(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	slug := "mcp-packet-test-goal"

	// Create source file
	src := "package mcpcore\n\ntype Service struct{}\n"
	if err := os.WriteFile(filepath.Join(tmp, "service.go"), []byte(src), 0644); err != nil {
		t.Fatalf("write service.go: %v", err)
	}
	hSrc := sha256.Sum256([]byte(src))
	serviceSHA := hex.EncodeToString(hSrc[:])

	// Create goal package
	goalDir := filepath.Join(tmp, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatalf("mkdir goalDir: %v", err)
	}

	pkg := &goals.WorkPackage{
		SchemaVersion: goals.WorkPackageSchemaVersion,
		GoalSlug:      slug,
		Revision:      1,
		Purpose:       "Verify MCP task packet exposure in list_goals",
		Constraints:   []string{"pure-Go stdlib"},
		NonGoals:      []string{"no external dependencies"},
		Tasks: []goals.TaskContract{
			{
				ID:        "T-001",
				Title:     "Build Service",
				Context:   "MCP service context",
				Why:       "Primary service layer",
				DependsOn: []string{},
				Files: goals.TaskFiles{
					Read:   []string{"service.go"},
					Edit:   []string{"service.go"},
					Create: []string{"service_output.go"},
					Forbid: []string{"research/*"},
				},
				References: []goals.Reference{
					{
						Path:    "service.go",
						Symbol:  "Service",
						Excerpt: "type Service struct{}",
						SHA256:  serviceSHA,
					},
				},
				Decisions:     []goals.Decision{},
				Steps:         []goals.Step{{ID: "S-001", Action: "Start service", Expected: "Running", OnFailure: "Halt"}},
				Interfaces:    []string{"type Service struct{}"},
				Constraints:   []string{"zero alloc"},
				Examples:      []goals.Example{},
				AcceptanceIDs: []string{"AC-001"},
				Checks: []goals.Check{
					{
						ID:               "C-001",
						Executable:       "go",
						Args:             []string{"test", "./..."},
						Cwd:              "cli",
						TimeoutSeconds:   30,
						ExpectedExitCode: 0,
						Scope:            goals.ScopeUnit,
						Assertions: []goals.Assertion{
							{Kind: goals.AssertionGoTestPass, Target: "github.com/jadmadi/sila/internal/mcp/TestMCPTaskPacket", Expected: true},
						},
						Capability: goals.CapabilityLocal,
					},
				},
				StopConditions: []string{"Panic"},
				ReturnFormat:   "sila/task-result/v1",
			},
		},
		Acceptance: []goals.AcceptanceCase{
			{
				ID:            "AC-001",
				Requirement:   "Service starts cleanly",
				Given:         "Clean state",
				Action:        "Start",
				Expected:      "Clean exit",
				RequiredScope: goals.ScopeUnit,
				Mandatory:     true,
				CheckIDs:      []string{"C-001"},
			},
		},
		DependencyConditions: []goals.DependencyCondition{},
		UnresolvedDecisions:  []string{},
	}

	pkgData, err := json.MarshalIndent(pkg, "", "  ")
	if err != nil {
		t.Fatalf("marshal work package: %v", err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "work-package.json"), pkgData, 0644); err != nil {
		t.Fatalf("write work-package.json: %v", err)
	}

	// Create goal.md so ScanGoals discovers the goal
	goalMd := fmt.Sprintf("# Goal: %s\n\n## Goal Description\n%s\n", slug, pkg.Purpose)
	if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalMd), 0644); err != nil {
		t.Fatalf("write goal.md: %v", err)
	}

	specDigest, err := goals.WorkPackageDigest(pkg)
	if err != nil {
		t.Fatalf("compute spec digest: %v", err)
	}
	review := goals.SpecReview{
		SchemaVersion:      goals.SpecReviewSchemaVersion,
		SpecDigest:         specDigest,
		Reviewer:           "mcp-reviewer",
		ReviewedAt:         time.Now().UTC().Format(time.RFC3339),
		Verdict:            goals.VerdictReady,
		Shortcuts:          []goals.ShortcutCounter{},
		UnresolvedBlockers: []string{},
	}
	if err := goals.SaveSpecReview(tmp, slug, review); err != nil {
		t.Fatalf("save spec review: %v", err)
	}

	// Register project in store
	if _, err := st.EnsureProject(tmp); err != nil {
		t.Fatalf("ensure project: %v", err)
	}

	callTool := func(toolName string, args map[string]any) (map[string]any, *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    any    `json:"data"`
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
				Data    any    `json:"data"`
			} `json:"error"`
		}
		if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode response: %v; raw: %s", err, out.String())
		}
		return resp.Result, resp.Error
	}

	// 1. Calling list_goals with both slug and task_id returns task_packet envelope
	res, rErr := callTool("list_goals", map[string]any{
		"project": tmp,
		"slug":    slug,
		"task_id": "T-001",
	})
	if rErr != nil {
		t.Fatalf("expected list_goals with task packet to succeed, got error: %+v", rErr)
	}

	sd, ok := res["structuredData"].(map[string]any)
	if !ok || sd == nil {
		t.Fatalf("expected structuredData in result envelope, got: %+v", res)
	}
	tpVal, exists := sd["task_packet"]
	if !exists || tpVal == nil {
		t.Fatalf("expected task_packet in structuredData envelope, got: %+v", sd)
	}

	tpBytes, err := json.Marshal(tpVal)
	if err != nil {
		t.Fatalf("marshal tpVal failed: %v", err)
	}
	var mcpPacket goals.TaskPacket
	if err := json.Unmarshal(tpBytes, &mcpPacket); err != nil {
		t.Fatalf("unmarshal mcpPacket failed: %v", err)
	}

	// Verify AC-007: CLI / library and MCP packets have identical digest and task contract
	directPacket, err := goals.BuildTaskPacket(tmp, slug, "T-001")
	if err != nil {
		t.Fatalf("BuildTaskPacket direct failed: %v", err)
	}
	if mcpPacket.PacketDigest != directPacket.PacketDigest {
		t.Errorf("AC-007 digest mismatch: MCP (%s) != direct (%s)", mcpPacket.PacketDigest, directPacket.PacketDigest)
	}
	if mcpPacket.Task.ID != "T-001" || mcpPacket.Task.Title != directPacket.Task.Title {
		t.Errorf("AC-007 contract mismatch: MCP (%+v) != direct (%+v)", mcpPacket.Task, directPacket.Task)
	}

	// 2. Calling list_goals without task_id returns standard goal list
	resList, rErr := callTool("list_goals", map[string]any{
		"project": tmp,
	})
	if rErr != nil {
		t.Fatalf("expected list_goals standard to succeed, got error: %+v", rErr)
	}
	if sd, ok := resList["structuredData"].(map[string]any); ok {
		if _, exists := sd["task_packet"]; exists {
			t.Errorf("standard list_goals should not return task_packet key")
		}
	}

	// 3. Calling list_goals with unknown task ID fails with code -32602
	_, rErr = callTool("list_goals", map[string]any{
		"project": tmp,
		"slug":    slug,
		"task_id": "T-999",
	})
	if rErr == nil {
		t.Fatalf("expected error for unknown task_id, got nil")
	}
	if rErr.Code != -32602 {
		t.Errorf("expected error code -32602 for unknown task_id, got %d", rErr.Code)
	}

	// 4. Calling list_goals with invalid project fails with code -32602
	_, rErr = callTool("list_goals", map[string]any{
		"project": "/nonexistent/project/path",
		"slug":    slug,
		"task_id": "T-001",
	})
	if rErr == nil {
		t.Fatalf("expected error for invalid project, got nil")
	}
	if rErr.Code != -32602 {
		t.Errorf("expected error code -32602 for invalid project, got %d", rErr.Code)
	}
}
