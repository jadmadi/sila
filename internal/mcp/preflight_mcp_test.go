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

func TestMCPCreateGoalPreFlight(t *testing.T) {
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

	// 1. Create base goal touching "internal/mcp/server.go"
	res1, err1 := callTool("create_goal", map[string]any{
		"project":        tmp,
		"slug":           "base-feature",
		"title":          "Base Feature",
		"files_to_touch": []string{"internal/mcp/server.go"},
	})
	if err1 != nil {
		t.Fatalf("base feature creation failed: %+v", err1)
	}
	if res1 == nil {
		t.Fatal("expected result for base feature")
	}

	// 2. Create second goal touching same file in strict mode -> must fail
	_, err2 := callTool("create_goal", map[string]any{
		"project":        tmp,
		"slug":           "colliding-strict-feature",
		"title":          "Colliding Strict Feature",
		"files_to_touch": []string{"internal/mcp/server.go"},
		"strict":         true,
	})
	if err2 == nil {
		t.Fatal("expected strict mode to reject goal creation with file collision, but it succeeded")
	}

	// 3. Create second goal touching same file in non-strict mode -> succeeds with conflicts_detected
	res3, err3 := callTool("create_goal", map[string]any{
		"project":        tmp,
		"slug":           "colliding-lenient-feature",
		"title":          "Colliding Lenient Feature",
		"files_to_touch": []string{"internal/mcp/server.go"},
		"strict":         false,
	})
	if err3 != nil {
		t.Fatalf("expected non-strict mode to succeed with warnings, got: %+v", err3)
	}
	structData, ok := res3["structuredData"].(map[string]any)
	if !ok {
		t.Fatalf("expected structuredData in response: %+v", res3)
	}
	conflicts, ok := structData["conflicts_detected"].([]any)
	if !ok || len(conflicts) == 0 {
		t.Errorf("expected conflicts_detected array in structuredData, got: %+v", structData)
	}
	preflight, ok := structData["preflight"].(map[string]any)
	if !ok {
		t.Errorf("expected preflight object in structuredData, got: %+v", structData)
	} else {
		collisions, _ := preflight["collisions"].([]any)
		if len(collisions) == 0 {
			t.Errorf("expected preflight collisions, got 0")
		}
	}
}
