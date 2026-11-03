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

func TestMCPCreateGoalStructuredParameters(t *testing.T) {
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

	res := callTool("create_goal", map[string]any{
		"project":     tmp,
		"slug":        "mcp-structured-goal",
		"title":       "MCP Structured Goal",
		"description": "Testing structured creation via MCP",
		"invariants": []string{
			"Strict offline-first execution.",
			"Zero CGO dependencies.",
		},
		"tasks": []string{
			"Phase 1: Setup schema",
			"Phase 2: Implement logic",
			"Phase 3: Verify and ship",
		},
		"files_to_touch": []string{"internal/mcp/server.go"},
	})

	structData, ok := res["structuredData"].(map[string]any)
	if !ok {
		t.Fatalf("expected structuredData in create_goal response: %+v", res)
	}
	created, _ := structData["created"].(bool)
	if !created {
		t.Fatalf("expected created == true, got %+v", structData)
	}

	goalDir := filepath.Join(goalsDir, "mcp-structured-goal")

	factsBytes, err := os.ReadFile(filepath.Join(goalDir, "facts.md"))
	if err != nil {
		t.Fatalf("facts.md not found: %v", err)
	}
	factsContent := string(factsBytes)
	if !strings.Contains(factsContent, "Strict offline-first execution.") {
		t.Errorf("facts.md missing invariant: %s", factsContent)
	}
	if !strings.Contains(factsContent, "internal/mcp/server.go") {
		t.Errorf("facts.md missing fileToTouch: %s", factsContent)
	}

	planBytes, err := os.ReadFile(filepath.Join(goalDir, "plan.md"))
	if err != nil {
		t.Fatalf("plan.md not found: %v", err)
	}
	planContent := string(planBytes)
	if !strings.Contains(planContent, "- [ ] Phase 1: Setup schema") {
		t.Errorf("plan.md missing Phase 1: %s", planContent)
	}
	if !strings.Contains(planContent, "- [ ] Phase 3: Verify and ship") {
		t.Errorf("plan.md missing Phase 3: %s", planContent)
	}
}
