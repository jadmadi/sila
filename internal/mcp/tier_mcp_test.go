package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func TestMCPCreateAndListGoalsTier(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	goalsDir := filepath.Join(tmp, "goals")
	if err := os.MkdirAll(goalsDir, 0755); err != nil {
		t.Fatal(err)
	}

	callTool := func(toolName string, args map[string]any) map[string]any {
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
		if resp.Error != nil {
			t.Fatalf("tool call returned error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp.Result
	}

	// 1. Create goal with tier: immediate
	resCreateImm := callTool("create_goal", map[string]any{
		"project": tmp,
		"slug":    "imm-goal",
		"title":   "Immediate Goal",
		"tier":    "immediate",
	})
	sdImm, ok := resCreateImm["structuredData"].(map[string]any)
	if !ok {
		t.Fatalf("expected structuredData in create_goal response: %+v", resCreateImm)
	}
	goalImm, ok := sdImm["goal"].(map[string]any)
	if !ok || goalImm["tier"] != "immediate" {
		t.Fatalf("expected goal.tier == 'immediate', got: %+v", goalImm)
	}

	// 2. Create goal with default tier (omitted)
	resCreateDef := callTool("create_goal", map[string]any{
		"project": tmp,
		"slug":    "def-goal",
		"title":   "Default Goal",
	})
	sdDef := resCreateDef["structuredData"].(map[string]any)
	goalDef := sdDef["goal"].(map[string]any)
	if goalDef["tier"] != "roadmap" {
		t.Fatalf("expected goal.tier == 'roadmap' when omitted, got: %+v", goalDef)
	}

	// 3. Create goal with tier: nice-to-have
	callTool("create_goal", map[string]any{
		"project": tmp,
		"slug":    "nth-goal",
		"title":   "Nice to have Goal",
		"tier":    "nice-to-have",
	})

	// 4. List goals filtered by tier: immediate
	resListImm := callTool("list_goals", map[string]any{
		"project": tmp,
		"tier":    "immediate",
	})
	goalsImmList, ok := resListImm["structuredData"].([]any)
	if !ok {
		t.Fatalf("expected array of goals in list_goals, got: %+v", resListImm)
	}
	if len(goalsImmList) != 1 {
		t.Fatalf("expected exactly 1 immediate goal, got %d", len(goalsImmList))
	}
	g0 := goalsImmList[0].(map[string]any)
	if g0["slug"] != "imm-goal" || g0["tier"] != "immediate" {
		t.Fatalf("unexpected goal in immediate filter: %+v", g0)
	}

	// 5. List goals filtered by tier alias: tier-3 (nice-to-have)
	resListNth := callTool("list_goals", map[string]any{
		"project": tmp,
		"tier":    "tier-3",
	})
	goalsNthList := resListNth["structuredData"].([]any)
	if len(goalsNthList) != 1 {
		t.Fatalf("expected exactly 1 nice-to-have goal, got %d", len(goalsNthList))
	}
	gNth := goalsNthList[0].(map[string]any)
	if gNth["slug"] != "nth-goal" {
		t.Fatalf("unexpected goal in nice-to-have filter: %+v", gNth)
	}

	// 6. List all goals (no tier filter)
	resListAll := callTool("list_goals", map[string]any{
		"project": tmp,
	})
	goalsAllList := resListAll["structuredData"].([]any)
	if len(goalsAllList) != 3 {
		t.Fatalf("expected 3 total goals, got %d", len(goalsAllList))
	}

	// Verify goal.md contains Tier line for imm-goal
	goalMDBytes, err := os.ReadFile(filepath.Join(goalsDir, "imm-goal", "goal.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(goalMDBytes), "- **Tier**: immediate") {
		t.Errorf("expected goal.md to contain '- **Tier**: immediate', got:\n%s", string(goalMDBytes))
	}
}
