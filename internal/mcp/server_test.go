package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jadmadi/sila/internal/assess"
	"github.com/jadmadi/sila/internal/eval"
	"github.com/jadmadi/sila/internal/gateway"
	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/store"
)

func TestMCPGoalLifecycleParity(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "lifecycle.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	for _, slug := range []string{"mcp-update-goal", "mcp-supersede-goal"} {
		if _, err := goals.CreateGoal(goals.CreateGoalParams{
			RootDir: tmp,
			Slug:    slug,
			Title:   slug,
		}); err != nil {
			t.Fatalf("CreateGoal %s: %v", slug, err)
		}
		// This fixture exercises the MCP goal lifecycle, not the memory
		// protocol; keep certification focused on update/certify/supersede.
		goalPath := filepath.Join(tmp, "goals", slug, "goal.md")
		data, err := os.ReadFile(goalPath)
		if err != nil {
			t.Fatal(err)
		}
		legacy := strings.Replace(string(data), "- **Memory Protocol**: required", "- **Memory Protocol**: legacy", 1)
		if err := os.WriteFile(goalPath, []byte(legacy), 0644); err != nil {
			t.Fatal(err)
		}
	}
	evidencePath := filepath.Join(tmp, "facts.json")
	if err := os.WriteFile(evidencePath, []byte(`{"facts":[{"id":"f1","fact":"verified","passed":true}]}`), 0644); err != nil {
		t.Fatal(err)
	}

	requests := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"update_goal","arguments":{"slug":"mcp-update-goal","title":"Updated MCP Goal","sequence":9,"project":%q}}}`, tmp),
		fmt.Sprintf(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"validate_goals","arguments":{"project":%q}}}`, tmp),
		fmt.Sprintf(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"certify_goal","arguments":{"slug":"mcp-update-goal","evidence":%q,"agent":"auditor","project":%q}}}`, evidencePath, tmp),
		fmt.Sprintf(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"supersede_goal","arguments":{"slug":"mcp-supersede-goal","superseded_by":"mcp-update-goal","agent":"tester","reason":"replaced","project":%q}}}`, tmp),
	}

	var out bytes.Buffer
	srv := NewServer(st, strings.NewReader(strings.Join(requests, "\n")+"\n"), &out)
	if err := srv.Run(); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != len(requests) {
		t.Fatalf("expected %d responses, got %d:\n%s", len(requests), len(lines), out.String())
	}

	for i, expectedID := range []float64{2, 3, 4, 5} {
		var resp JSONRPCResponse
		if err := json.Unmarshal([]byte(lines[i+1]), &resp); err != nil {
			t.Fatalf("parse response %d: %v", i+2, err)
		}
		if resp.ID != expectedID || resp.Error != nil {
			t.Fatalf("request %v failed: %+v raw=%s", expectedID, resp, lines[i+1])
		}
	}

	goalBytes, err := os.ReadFile(filepath.Join(tmp, "goals", "mcp-update-goal", "goal.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(goalBytes), "Updated MCP Goal") {
		t.Errorf("expected update_goal to persist title, got:\n%s", goalBytes)
	}
	if _, err := os.Stat(filepath.Join(tmp, "goals", "mcp-update-goal", "certification.json")); err != nil {
		t.Errorf("expected certify_goal to write certification.json: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, "goals", "mcp-supersede-goal", "superseded.json")); err != nil {
		t.Errorf("expected supersede_goal to write superseded.json: %v", err)
	}
}

func TestMain(m *testing.M) {
	tmpDir, err := os.MkdirTemp("", "sila_mcp_test_*")
	if err == nil {
		defer os.RemoveAll(tmpDir)
		_ = os.Setenv("SILA_DB", filepath.Join(tmpDir, "test.db"))
	}
	os.Exit(m.Run())
}

// TestPublicToolCount verifies that the canonical MCP tool contract publishes exactly
// PublicToolCount (75) unique, well-formed tools across the Go registry, category map,
// and the JSON-RPC tools/list protocol response.
func TestPublicToolCount(t *testing.T) {
	// 1. Verify PublicToolCount constant is exactly 75.
	const expectedCount = 76
	if PublicToolCount != expectedCount {
		t.Fatalf("expected PublicToolCount constant to be %d, got %d", expectedCount, PublicToolCount)
	}

	// 2. Verify ToolDefinitions() returns exactly PublicToolCount tool definitions.
	tools := ToolDefinitions()
	if len(tools) != PublicToolCount {
		t.Fatalf("expected len(ToolDefinitions()) == %d, got %d", PublicToolCount, len(tools))
	}

	// 3. Verify tool name uniqueness, non-emptiness, and basic schema integrity.
	seen := make(map[string]bool, len(tools))
	for _, tool := range tools {
		if strings.TrimSpace(tool.Name) == "" {
			t.Fatalf("tool definition has empty Name: %+v", tool)
		}
		if seen[tool.Name] {
			t.Errorf("duplicate tool name found in registry: %q", tool.Name)
		}
		seen[tool.Name] = true

		if strings.TrimSpace(tool.Description) == "" {
			t.Errorf("tool %q missing description", tool.Name)
		}
		if tool.InputSchema.Type != "object" {
			t.Errorf("tool %q inputSchema type must be 'object', got %q", tool.Name, tool.InputSchema.Type)
		}
		if tool.Category == "" {
			t.Errorf("tool %q has empty category", tool.Name)
		}
	}

	if len(seen) != PublicToolCount {
		t.Errorf("expected %d unique tools, got %d", PublicToolCount, len(seen))
	}

	// 4. Verify toolCategories map cardinality and consistency.
	if len(toolCategories) != PublicToolCount {
		t.Errorf("expected len(toolCategories) == %d, got %d", PublicToolCount, len(toolCategories))
	}
	for _, tool := range tools {
		cat, ok := toolCategories[tool.Name]
		if !ok {
			t.Errorf("tool %q missing from toolCategories map", tool.Name)
		} else if cat != tool.Category {
			t.Errorf("tool %q category mismatch: registry=%q, toolCategories=%q", tool.Name, tool.Category, cat)
		}
	}

	// 5. Verify presence of Project Skill Governor tools (FEAT-16 & FEAT-17).
	for _, skillTool := range []string{"skill_search", "skill_load"} {
		if !seen[skillTool] {
			t.Errorf("expected Project Skill Governor tool %q not found in registry", skillTool)
		}
	}

	// 6. Verify JSON-RPC wire protocol tools/list response matches PublicToolCount.
	reqList := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	in := strings.NewReader(reqList + "\n")
	var out bytes.Buffer
	srv := NewServer(nil, in, &out)
	if err := srv.Run(); err != nil {
		t.Fatalf("server.Run() failed on tools/list: %v", err)
	}

	var listResp JSONRPCResponse
	if err := json.Unmarshal(out.Bytes(), &listResp); err != nil {
		t.Fatalf("failed to unmarshal tools/list response: %v", err)
	}
	if listResp.Error != nil {
		t.Fatalf("tools/list returned protocol error: %+v", listResp.Error)
	}

	resMap, ok := listResp.Result.(map[string]any)
	if !ok {
		t.Fatalf("tools/list result is not an object: %+v", listResp.Result)
	}
	toolsRaw, ok := resMap["tools"].([]any)
	if !ok {
		t.Fatalf("tools/list response missing 'tools' array: %+v", resMap)
	}
	if len(toolsRaw) != PublicToolCount {
		t.Fatalf("expected tools/list protocol response to contain %d tools, got %d", PublicToolCount, len(toolsRaw))
	}
}

// TestToolCategoriesComplete keeps the category map in step with the registry
// and every published category inside the documented vocabulary.
func TestToolCategoriesComplete(t *testing.T) {
	tools := ToolDefinitions()
	valid := map[string]bool{ToolCategoryRead: true, ToolCategoryWrite: true, ToolCategoryCollab: true}
	seen := map[string]bool{}
	for _, tool := range tools {
		seen[tool.Name] = true
		if !valid[tool.Category] {
			t.Errorf("tool %s has invalid category %q", tool.Name, tool.Category)
		}
	}
	for name := range toolCategories {
		if !seen[name] {
			t.Errorf("category map has an entry for unknown tool %q", name)
		}
	}
	// Spot-check the split: reads never mutate, collab tools coordinate.
	for name, want := range map[string]string{
		"search_knowledge":  ToolCategoryRead,
		"prime_context":     ToolCategoryRead,
		"save_memo":         ToolCategoryWrite,
		"claim_goal":        ToolCategoryCollab,
		"run_gate_pipeline": ToolCategoryCollab,
	} {
		for _, tool := range tools {
			if tool.Name == name && tool.Category != want {
				t.Errorf("tool %s category = %q, want %q", name, tool.Category, want)
			}
		}
	}
}

func TestMCPServerLifecycleAndTools(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if _, err := st.EnsureProject("/repo/test-proj"); err != nil {
		t.Fatal(err)
	}
	st.UpsertFact(store.Fact{
		ID:         "fact_123",
		ProjectID:  "test-proj",
		Claim:      "Always use modernc.org/sqlite for pure-Go CGO-free builds",
		Confidence: "high",
		Verified:   false,
		RepoFiles:  []string{},
		Tools:      []string{"devin", "mimocode"},
		SessionRef: "s1",
		FirstSeen:  "2026-08-01T00:00:00Z",
		PromotedAt: "2026-08-01T00:00:00Z",
	})

	st.WriteRun("", "devin", "1", []store.LoadMessage{
		{ProjectID: "test-proj", ContentHash: "h1", TS: "2026-08-01T00:00:00Z", Role: "assistant", Content: "Raw transcript log discussing WAL mode indexing", Tool: "devin"},
	})

	// Create mock goal package in tmp
	goalPkg := filepath.Join(tmp, "goals", "mock-goal")
	_ = os.MkdirAll(goalPkg, 0755)
	_ = os.WriteFile(filepath.Join(goalPkg, "goal.md"), []byte("# Goal: Mock Goal\n## Goal Description\nMock description"), 0644)
	_ = os.WriteFile(filepath.Join(goalPkg, "plan.md"), []byte("# Plan"), 0644)
	_ = os.WriteFile(filepath.Join(goalPkg, "facts.md"), []byte("# Facts"), 0644)

	requests := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"search_knowledge","arguments":{"query":"sqlite","project":"test-proj","offset":0,"limit":5}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"list_projects"}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"get_project_facts","arguments":{"project":"test-proj","limit":5,"offset":0}}}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"get_status"}}`,
		`{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"search_messages","arguments":{"query":"indexing","tool":"devin","role":"assistant"}}}`,
		`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"reconcile_drift","arguments":{"project":"test-proj"}}}`,
		`{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"save_handoff","arguments":{"project":"test-proj","summary":"Implemented Edge Auth","open_items":["e2e test"],"decisions":["TTL=15m"],"agent":"claude-code"}}}`,
		`{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"get_latest_handoff","arguments":{"project":"test-proj"}}}`,
		`{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"save_memo","arguments":{"project":"test-proj","content":"Must run in pure-Go SQLite mode","agent":"user"}}}`,
		`{"jsonrpc":"2.0","id":13,"method":"tools/call","params":{"name":"save_doc","arguments":{"project":"test-proj","title":"SQLite FTS5 Manual","url":"https://sqlite.org/fts5.html","content":"FTS5 full text search capability"}}}`,
		`{"jsonrpc":"2.0","id":14,"method":"tools/call","params":{"name":"search_docs","arguments":{"query":"capability","project":"test-proj"}}}`,
		`{"jsonrpc":"2.0","id":15,"method":"tools/call","params":{"name":"compile_rules","arguments":{"project":"test-proj","target":"claude"}}}`,
		`{"jsonrpc":"2.0","id":16,"method":"tools/call","params":{"name":"prime_context","arguments":{"project":"test-proj","max_facts":5}}}`,
		`{"jsonrpc":"2.0","id":17,"method":"tools/call","params":{"name":"get_db_stats","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":18,"method":"tools/call","params":{"name":"vacuum_database","arguments":{"force":true}}}`,
		`{"jsonrpc":"2.0","id":19,"method":"tools/call","params":{"name":"get_message_context","arguments":{"id":1,"window":2}}}`,
		`{"jsonrpc":"2.0","id":20,"method":"tools/call","params":{"name":"omni_search","arguments":{"query":"auth","project":"test-proj"}}}`,
		`{"jsonrpc":"2.0","id":21,"method":"tools/call","params":{"name":"diagnose_system","arguments":{"repair":false}}}`,
		`{"jsonrpc":"2.0","id":22,"method":"tools/call","params":{"name":"create_goal","arguments":{"slug":"mcp-created-goal","title":"MCP Created Goal","description":"Created via MCP","independent":true,"sequence":1,"project":"` + tmp + `"}}}`,
		`{"jsonrpc":"2.0","id":23,"method":"tools/call","params":{"name":"list_goals","arguments":{"pending_only":false,"project":"` + tmp + `"}}}`,
		`{"jsonrpc":"2.0","id":24,"method":"tools/call","params":{"name":"claim_goal","arguments":{"slug":"mock-goal","agent":"claude-code","note":"started","project":"` + tmp + `"}}}`,
		`{"jsonrpc":"2.0","id":25,"method":"tools/call","params":{"name":"report_goal","arguments":{"slug":"mock-goal","note":"50% complete","project":"` + tmp + `"}}}`,
		`{"jsonrpc":"2.0","id":26,"method":"tools/call","params":{"name":"release_goal","arguments":{"slug":"mock-goal","project":"` + tmp + `"}}}`,
		`{"jsonrpc":"2.0","id":27,"method":"tools/call","params":{"name":"audit_goals","arguments":{"no_sync":true,"project":"` + tmp + `"}}}`,
		`{"jsonrpc":"2.0","id":28,"method":"tools/call","params":{"name":"record_feedback","arguments":{"content":"Sila saves 90% context!","category":"praise","project":"test-proj","agent":"devin"}}}`,
		`{"jsonrpc":"2.0","id":29,"method":"tools/call","params":{"name":"list_feedback","arguments":{"project":"test-proj","category":"praise"}}}`,
		`{"jsonrpc":"2.0","id":30,"method":"unknown_method"}`,
	}

	in := strings.NewReader(strings.Join(requests, "\n") + "\n")
	var out bytes.Buffer

	srv := NewServer(st, in, &out)
	if err := srv.Run(); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != len(requests) {
		t.Fatalf("expected %d responses, got %d:\n%s", len(requests), len(lines), out.String())
	}

	// Verify initialize
	var initResp JSONRPCResponse
	json.Unmarshal([]byte(lines[0]), &initResp)
	if initResp.ID != float64(1) || initResp.Error != nil {
		t.Fatalf("initialize failed: %+v", initResp)
	}

	// Verify tools/list
	var listResp JSONRPCResponse
	json.Unmarshal([]byte(lines[2]), &listResp)
	m := listResp.Result.(map[string]any)
	tools := m["tools"].([]any)
	if len(tools) != PublicToolCount {
		t.Fatalf("expected %d tool definitions, got %d", PublicToolCount, len(tools))
	}

	// Verify search_knowledge with partial project selector "test"
	var searchResp JSONRPCResponse
	json.Unmarshal([]byte(lines[3]), &searchResp)
	resM := searchResp.Result.(map[string]any)
	content := resM["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("expected content in search_knowledge result, got %+v", resM)
	}
	text := content[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "modernc.org/sqlite") {
		t.Fatalf("search_knowledge result does not contain expected fact: %s", text)
	}
	if resM["structuredData"] == nil {
		t.Fatalf("expected structuredData in search_knowledge response, got nil")
	}

	// Verify get_project_facts with partial project selector "test"
	var projFactsResp JSONRPCResponse
	json.Unmarshal([]byte(lines[5]), &projFactsResp)
	pfContent := projFactsResp.Result.(map[string]any)["content"].([]any)
	if len(pfContent) == 0 {
		t.Fatalf("expected content in get_project_facts result, got %+v", projFactsResp)
	}
	pfText := pfContent[0].(map[string]any)["text"].(string)
	if !strings.Contains(pfText, "modernc.org/sqlite") {
		t.Fatalf("get_project_facts result missing fact: %s", pfText)
	}

	// Verify search_messages
	var msgSearchResp JSONRPCResponse
	json.Unmarshal([]byte(lines[7]), &msgSearchResp)
	msgContent := msgSearchResp.Result.(map[string]any)["content"].([]any)
	if len(msgContent) == 0 {
		t.Fatalf("expected content in search_messages result, got %+v", msgSearchResp)
	}
	msgText := msgContent[0].(map[string]any)["text"].(string)
	if !strings.Contains(msgText, "WAL mode indexing") {
		t.Fatalf("search_messages result missing message content: %s", msgText)
	}

	// Verify reconcile_drift tool
	var reconcileResp JSONRPCResponse
	json.Unmarshal([]byte(lines[8]), &reconcileResp)
	recM := reconcileResp.Result.(map[string]any)
	recContent := recM["content"].([]any)
	if len(recContent) == 0 {
		t.Fatalf("expected content in reconcile_drift result, got %+v", reconcileResp)
	}
	if recM["structuredData"] == nil {
		t.Fatalf("expected structuredData in reconcile_drift response, got nil")
	}

	// Verify save_handoff
	var saveHandoffResp JSONRPCResponse
	json.Unmarshal([]byte(lines[9]), &saveHandoffResp)
	if saveHandoffResp.Error != nil {
		t.Fatalf("save_handoff failed: %+v", saveHandoffResp)
	}

	// Verify get_latest_handoff
	var getHandoffResp JSONRPCResponse
	json.Unmarshal([]byte(lines[10]), &getHandoffResp)
	ghContent := getHandoffResp.Result.(map[string]any)["content"].([]any)
	ghText := ghContent[0].(map[string]any)["text"].(string)
	if !strings.Contains(ghText, "Implemented Edge Auth") {
		t.Fatalf("get_latest_handoff missing summary: %s", ghText)
	}

	// Verify save_memo
	var saveMemoResp JSONRPCResponse
	json.Unmarshal([]byte(lines[11]), &saveMemoResp)
	if saveMemoResp.Error != nil {
		t.Fatalf("save_memo failed: %+v", saveMemoResp)
	}

	// Verify save_doc
	var saveDocResp JSONRPCResponse
	json.Unmarshal([]byte(lines[12]), &saveDocResp)
	if saveDocResp.Error != nil {
		t.Fatalf("save_doc failed: %+v", saveDocResp)
	}

	// Verify search_docs
	var searchDocsResp JSONRPCResponse
	json.Unmarshal([]byte(lines[13]), &searchDocsResp)
	sdContent := searchDocsResp.Result.(map[string]any)["content"].([]any)
	if len(sdContent) == 0 {
		t.Fatalf("search_docs empty: %+v", searchDocsResp)
	}

	// Verify compile_rules
	var compileRulesResp JSONRPCResponse
	json.Unmarshal([]byte(lines[14]), &compileRulesResp)
	crContent := compileRulesResp.Result.(map[string]any)["content"].([]any)
	crText := crContent[0].(map[string]any)["text"].(string)
	if !strings.Contains(crText, "CLAUDE.md") || !strings.Contains(crText, "Implemented Edge Auth") {
		t.Fatalf("compile_rules missing expected content: %s", crText)
	}

	// Verify prime_context
	var primeResp JSONRPCResponse
	json.Unmarshal([]byte(lines[15]), &primeResp)
	primeContent := primeResp.Result.(map[string]any)["content"].([]any)
	primeText := primeContent[0].(map[string]any)["text"].(string)
	if !strings.Contains(primeText, "Implemented Edge Auth") || !strings.Contains(primeText, "Must run in pure-Go SQLite mode") || !strings.Contains(primeText, "SQLite FTS5 Manual") {
		t.Fatalf("prime_context missing expected briefing details: %s", primeText)
	}

	// Verify get_db_stats
	var statsResp JSONRPCResponse
	json.Unmarshal([]byte(lines[16]), &statsResp)
	if statsResp.ID != float64(17) || statsResp.Error != nil {
		t.Fatalf("get_db_stats failed: %+v", statsResp)
	}

	// Verify vacuum_database
	var vacResp JSONRPCResponse
	json.Unmarshal([]byte(lines[17]), &vacResp)
	if vacResp.ID != float64(18) || vacResp.Error != nil {
		t.Fatalf("vacuum_database failed: %+v", vacResp)
	}

	// Verify get_message_context
	var msgCtxResp JSONRPCResponse
	json.Unmarshal([]byte(lines[18]), &msgCtxResp)
	if msgCtxResp.ID != float64(19) || msgCtxResp.Error != nil {
		t.Fatalf("get_message_context failed: %+v", msgCtxResp)
	}

	// Verify omni_search
	var omniResp JSONRPCResponse
	json.Unmarshal([]byte(lines[19]), &omniResp)
	if omniResp.ID != float64(20) || omniResp.Error != nil {
		t.Fatalf("omni_search failed: %+v", omniResp)
	}

	// Verify diagnose_system
	var diagResp JSONRPCResponse
	json.Unmarshal([]byte(lines[20]), &diagResp)
	if diagResp.ID != float64(21) || diagResp.Error != nil {
		t.Fatalf("diagnose_system failed: %+v", diagResp)
	}

	// Verify create_goal
	var createGoalResp JSONRPCResponse
	json.Unmarshal([]byte(lines[21]), &createGoalResp)
	if createGoalResp.ID != float64(22) || createGoalResp.Error != nil {
		t.Fatalf("create_goal failed: %+v", createGoalResp)
	}

	// Verify list_goals
	var listGoalsResp JSONRPCResponse
	json.Unmarshal([]byte(lines[22]), &listGoalsResp)
	if listGoalsResp.ID != float64(23) || listGoalsResp.Error != nil {
		t.Fatalf("list_goals failed: %+v", listGoalsResp)
	}

	// Verify claim_goal
	var claimGoalsResp JSONRPCResponse
	json.Unmarshal([]byte(lines[23]), &claimGoalsResp)
	if claimGoalsResp.ID != float64(24) || claimGoalsResp.Error != nil {
		t.Fatalf("claim_goal failed: %+v", claimGoalsResp)
	}

	// Verify report_goal
	var reportGoalsResp JSONRPCResponse
	json.Unmarshal([]byte(lines[24]), &reportGoalsResp)
	if reportGoalsResp.ID != float64(25) || reportGoalsResp.Error != nil {
		t.Fatalf("report_goal failed: %+v", reportGoalsResp)
	}

	// Verify release_goal
	var releaseGoalsResp JSONRPCResponse
	json.Unmarshal([]byte(lines[25]), &releaseGoalsResp)
	if releaseGoalsResp.ID != float64(26) || releaseGoalsResp.Error != nil {
		t.Fatalf("release_goal failed: %+v", releaseGoalsResp)
	}

	// Verify audit_goals
	var auditGoalsResp JSONRPCResponse
	json.Unmarshal([]byte(lines[26]), &auditGoalsResp)
	if auditGoalsResp.ID != float64(27) || auditGoalsResp.Error != nil {
		t.Fatalf("audit_goals failed: %+v", auditGoalsResp)
	}

	// Verify record_feedback
	var recordFbResp JSONRPCResponse
	json.Unmarshal([]byte(lines[27]), &recordFbResp)
	if recordFbResp.ID != float64(28) || recordFbResp.Error != nil {
		t.Fatalf("record_feedback failed: %+v", recordFbResp)
	}

	// Verify list_feedback
	var listFbResp JSONRPCResponse
	json.Unmarshal([]byte(lines[28]), &listFbResp)
	if listFbResp.ID != float64(29) || listFbResp.Error != nil {
		t.Fatalf("list_feedback failed: %+v", listFbResp)
	}

	// Verify unknown method error
	var errResp JSONRPCResponse
	json.Unmarshal([]byte(lines[29]), &errResp)
	if errResp.Error == nil || errResp.Error.Code != -32601 {
		t.Fatalf("expected method not found error, got %+v", errResp)
	}
}

func TestLiveMCPClientProtocolIntegration(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "k.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	st.EnsureProject("/workspace/my-app")
	st.UpsertFact(store.Fact{
		ID: "fact_app", ProjectID: "my-app", Claim: "Auth middleware protects /api routes",
		Confidence: "high", Verified: true, RepoFiles: []string{"middleware.ts"}, Tools: []string{"devin", "mimocode"},
		SessionRef: "s1", FirstSeen: "2026-08-01T00:00:00Z", PromotedAt: "2026-08-01T00:00:00Z",
	})
	st.Close()

	roStore, err := store.OpenRO(dbPath)
	if err != nil {
		t.Fatalf("OpenRO failed: %v", err)
	}
	defer roStore.Close()

	// Simulate full MCP client conversation: initialize -> initialized notification -> tool calls -> response inspection
	clientRequests := []string{
		`{"jsonrpc":"2.0","id":"init-1","method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"claude-code","version":"1.0.0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":"call-1","method":"tools/call","params":{"name":"search_knowledge","arguments":{"query":"middleware","project":"my-app"}}}`,
		`{"jsonrpc":"2.0","id":"call-2","method":"tools/call","params":{"name":"get_status","arguments":{}}}`,
	}

	in := strings.NewReader(strings.Join(clientRequests, "\n") + "\n")
	var out bytes.Buffer
	srv := NewServer(roStore, in, &out)
	if err := srv.Run(); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	// Expected 3 responses (notification has no response)
	if len(lines) != 3 {
		t.Fatalf("expected 3 JSON-RPC responses, got %d:\n%s", len(lines), out.String())
	}

	var initResp JSONRPCResponse
	json.Unmarshal([]byte(lines[0]), &initResp)
	if initResp.ID != "init-1" || initResp.Error != nil {
		t.Fatalf("init handshake failed: %+v", initResp)
	}

	var searchResp JSONRPCResponse
	json.Unmarshal([]byte(lines[1]), &searchResp)
	if searchResp.ID != "call-1" || searchResp.Error != nil {
		t.Fatalf("search tool call failed: %+v", searchResp)
	}
	resMap := searchResp.Result.(map[string]any)
	content := resMap["content"].([]any)
	if len(content) == 0 {
		t.Fatal("empty content in tool response")
	}

	var statusResp JSONRPCResponse
	json.Unmarshal([]byte(lines[2]), &statusResp)
	if statusResp.ID != "call-2" || statusResp.Error != nil {
		t.Fatalf("status tool call failed: %+v", statusResp)
	}
}

func TestMCPAuditGoalsProjectResolution(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "k.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	projDir := filepath.Join(tmp, "my-registered-proj")
	p, err := st.EnsureProject(projDir)
	if err != nil {
		t.Fatal(err)
	}

	// Create a goal inside projDir
	goalPkg := filepath.Join(projDir, "goals", "registered-goal")
	if err := os.MkdirAll(goalPkg, 0755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(goalPkg, "goal.md"), []byte("# Goal: Registered Goal\n## Description\nTesting MCP resolution"), 0644)
	_ = os.WriteFile(filepath.Join(goalPkg, "plan.md"), []byte("# Plan"), 0644)
	_ = os.WriteFile(filepath.Join(goalPkg, "facts.md"), []byte("# Facts"), 0644)

	requests := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"audit_goals","arguments":{"project":"` + p.ID + `","no_sync":true}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"audit_goals","arguments":{"project":"non-existent-proj-id","no_sync":true}}}`,
	}

	in := strings.NewReader(strings.Join(requests, "\n") + "\n")
	var out bytes.Buffer
	srv := NewServer(st, in, &out)
	if err := srv.Run(); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 responses, got %d: %s", len(lines), out.String())
	}

	// Response 2: valid project ID resolution
	var auditResp JSONRPCResponse
	if err := json.Unmarshal([]byte(lines[1]), &auditResp); err != nil {
		t.Fatalf("failed to unmarshal audit response: %v", err)
	}
	if auditResp.Error != nil {
		t.Fatalf("audit_goals failed for registered project %q: %+v", p.ID, auditResp.Error)
	}
	resMap, ok := auditResp.Result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result type: %+v", auditResp.Result)
	}
	structData, ok := resMap["structuredData"].(map[string]any)
	if !ok {
		t.Fatalf("missing structuredData: %+v", resMap)
	}
	total, ok := structData["total"].(float64)
	if !ok || total != 1 {
		t.Fatalf("expected total=1 goal in resolved project, got %v", structData["total"])
	}

	// Response 3: invalid project ID should return -32602 Invalid params
	var errAuditResp JSONRPCResponse
	if err := json.Unmarshal([]byte(lines[2]), &errAuditResp); err != nil {
		t.Fatalf("failed to unmarshal error audit response: %v", err)
	}
	if errAuditResp.Error == nil || errAuditResp.Error.Code != -32602 {
		t.Fatalf("expected error code -32602 for invalid project, got %+v", errAuditResp.Error)
	}
}

func TestChallenger_All31MCPToolsAndProjectResolution(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "challenger_mcp.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Register project
	projDir := filepath.Join(tmp, "registered_proj")
	p, err := st.EnsureProject(projDir)
	if err != nil {
		t.Fatal(err)
	}

	// Create goals inside registered_proj
	goalRoot := filepath.Join(projDir, "goals", "goal-root")
	if err := os.MkdirAll(goalRoot, 0755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(goalRoot, "goal.md"), []byte("# Goal: Root Goal\n## Description\nRoot"), 0644)
	_ = os.WriteFile(filepath.Join(goalRoot, "plan.md"), []byte("# Plan"), 0644)
	_ = os.WriteFile(filepath.Join(goalRoot, "facts.md"), []byte("# Facts"), 0644)

	goalDep := filepath.Join(projDir, "goals", "goal-dep")
	if err := os.MkdirAll(goalDep, 0755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(goalDep, "goal.md"), []byte("# Goal: Dep Goal\n## Description\nDep\n## Prerequisites\n- goal-root"), 0644)
	_ = os.WriteFile(filepath.Join(goalDep, "plan.md"), []byte("# Plan"), 0644)
	_ = os.WriteFile(filepath.Join(goalDep, "facts.md"), []byte("# Facts"), 0644)

	// Ingest sample fact and doc for MCP tools
	st.UpsertFact(store.Fact{
		ID:         "fact_challenger",
		ProjectID:  p.ID,
		Claim:      "Challenger fact for testing MCP",
		Confidence: "high",
		FirstSeen:  "2026-08-31T00:00:00Z",
		PromotedAt: "2026-08-31T00:00:00Z",
	})
	_ = st.InsertDoc(store.Doc{
		ProjectID: p.ID,
		Title:     "Challenger Doc",
		URL:       "https://example.com/doc",
		Content:   "Doc content for testing",
	})

	expectedTools := []string{
		"search_knowledge",
		"list_projects",
		"get_project_facts",
		"get_status",
		"search_messages",
		"reconcile_drift",
		"prime_context",
		"save_handoff",
		"get_latest_handoff",
		"claim_handoff",
		"pop_handoff",
		"save_memo",
		"save_lesson",
		"save_case",
		"save_doc",
		"search_docs",
		"get_doc",
		"search_web_docs",
		"search_code",
		"get_package_doc",
		"search_stackoverflow",
		"compile_rules",
		"vacuum_database",
		"get_db_stats",
		"get_message_context",
		"omni_search",
		"recall",
		"diagnose_system",
		"list_goals",
		"audit_goals",
		"audit_goal",
		"claim_goal",
		"release_goal",
		"report_goal",
		"create_goal",
		"update_goal",
		"certify_goal",
		"supersede_goal",
		"validate_goals",
		"reopen_goal",
		"record_feedback",
		"list_feedback",
		"resolve_feedback",
		"touch_goal",
		"compare_proposals",
		"record_preference",
		"run_gate_pipeline",
		"run_preflight_eval",
		"spawn_crewmate",
		"list_crew",
		"supervise_crew",
		"sync_rules",
		"salam",
		"get_operational_guidebook",
		"route_workflow",
		"request_approval",
		"go_symbol_references",
		"go_package_api",
		"go_diagnostics",
		"cloudflare_d1_query",
		"cloudflare_worker_status",
		"cloudflare_edge_telemetry",
		"skill_search",
		"skill_load",
		"list_skills",
		"audit_hygiene",
		"clean_caches",
		"emit_lifecycle_event",
		"create_decision",
		"resolve_decision",
		"list_decisions",
		"get_activity_digest",
		"consult_personas",
		"save_domain_learning",
		"check_symbol_blast_radius",
		"get_subagent_charter",
	}

	if len(expectedTools) != PublicToolCount {
		t.Fatalf("expected %d tools list, got %d", PublicToolCount, len(expectedTools))
	}

	// 1. Test tools/list schema validation
	reqList := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	inList := strings.NewReader(reqList + "\n")
	var outList bytes.Buffer
	srvList := NewServer(st, inList, &outList)
	if err := srvList.Run(); err != nil {
		t.Fatal(err)
	}

	var listResp JSONRPCResponse
	if err := json.Unmarshal(outList.Bytes(), &listResp); err != nil {
		t.Fatalf("failed to unmarshal tools/list response: %v", err)
	}
	if listResp.Error != nil {
		t.Fatalf("tools/list returned error: %+v", listResp.Error)
	}

	resMap, ok := listResp.Result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result type: %+v", listResp.Result)
	}
	toolsRaw, ok := resMap["tools"].([]any)
	if !ok {
		t.Fatalf("missing tools array: %+v", resMap)
	}
	if len(toolsRaw) != PublicToolCount {
		t.Fatalf("expected exactly %d tools, got %d", PublicToolCount, len(toolsRaw))
	}

	foundToolNames := make(map[string]bool)
	for _, tr := range toolsRaw {
		tm, ok := tr.(map[string]any)
		if !ok {
			t.Fatalf("tool is not object: %+v", tr)
		}
		name, _ := tm["name"].(string)
		desc, _ := tm["description"].(string)
		if name == "" {
			t.Errorf("tool missing name: %+v", tm)
		}
		if desc == "" {
			t.Errorf("tool %q missing description", name)
		}
		schema, ok := tm["inputSchema"].(map[string]any)
		if !ok {
			t.Errorf("tool %q missing inputSchema", name)
		} else {
			if schema["type"] != "object" {
				t.Errorf("tool %q inputSchema type is not object: %v", name, schema["type"])
			}
		}
		foundToolNames[name] = true
	}

	for _, expected := range expectedTools {
		if !foundToolNames[expected] {
			t.Errorf("expected tool %q not found in tools/list", expected)
		}
	}

	// 2. Comprehensive Tool Call Sequence Testing
	calls := []string{
		`{"jsonrpc":"2.0","id":100,"method":"initialize","params":{}}`,
		// Audit goals with registered project ID
		`{"jsonrpc":"2.0","id":101,"method":"tools/call","params":{"name":"audit_goals","arguments":{"project":"` + p.ID + `","no_sync":false}}}`,
		// Audit goals with direct project path
		`{"jsonrpc":"2.0","id":102,"method":"tools/call","params":{"name":"audit_goals","arguments":{"project":"` + projDir + `","no_sync":true}}}`,
		// Audit goals with non-existent project (expect error -32602)
		`{"jsonrpc":"2.0","id":103,"method":"tools/call","params":{"name":"audit_goals","arguments":{"project":"non-existent-proj-999"}}}`,
		// List goals with project ID
		`{"jsonrpc":"2.0","id":104,"method":"tools/call","params":{"name":"list_goals","arguments":{"project":"` + p.ID + `"}}}`,
		// Claim unblocked root goal
		`{"jsonrpc":"2.0","id":105,"method":"tools/call","params":{"name":"claim_goal","arguments":{"slug":"goal-root","agent":"challenger","project":"` + p.ID + `"}}}`,
		// Report progress on root goal
		`{"jsonrpc":"2.0","id":106,"method":"tools/call","params":{"name":"report_goal","arguments":{"slug":"goal-root","note":"Root in progress","project":"` + p.ID + `"}}}`,
		// Attempt claim on blocked dependent goal without force (expect error)
		`{"jsonrpc":"2.0","id":107,"method":"tools/call","params":{"name":"claim_goal","arguments":{"slug":"goal-dep","agent":"challenger","force":false,"project":"` + p.ID + `"}}}`,
		// Claim blocked dependent goal with force (expect success)
		`{"jsonrpc":"2.0","id":108,"method":"tools/call","params":{"name":"claim_goal","arguments":{"slug":"goal-dep","agent":"challenger","force":true,"project":"` + p.ID + `"}}}`,
		// Release goals
		`{"jsonrpc":"2.0","id":109,"method":"tools/call","params":{"name":"release_goal","arguments":{"slug":"goal-root","project":"` + p.ID + `"}}}`,
		`{"jsonrpc":"2.0","id":110,"method":"tools/call","params":{"name":"release_goal","arguments":{"slug":"goal-dep","project":"` + p.ID + `"}}}`,
		// Create goal via MCP
		`{"jsonrpc":"2.0","id":111,"method":"tools/call","params":{"name":"create_goal","arguments":{"slug":"goal-created-mcp","title":"Created Via MCP","description":"Desc","independent":true,"sequence":2,"project":"` + p.ID + `"}}}`,
		// Knowledge & facts tools
		`{"jsonrpc":"2.0","id":112,"method":"tools/call","params":{"name":"search_knowledge","arguments":{"query":"Challenger","project":"` + p.ID + `"}}}`,
		`{"jsonrpc":"2.0","id":113,"method":"tools/call","params":{"name":"get_project_facts","arguments":{"project":"` + p.ID + `"}}}`,
		`{"jsonrpc":"2.0","id":114,"method":"tools/call","params":{"name":"list_projects","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":115,"method":"tools/call","params":{"name":"get_status","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":116,"method":"tools/call","params":{"name":"search_messages","arguments":{"query":"test"}}}`,
		`{"jsonrpc":"2.0","id":117,"method":"tools/call","params":{"name":"reconcile_drift","arguments":{"project":"` + p.ID + `"}}}`,
		`{"jsonrpc":"2.0","id":118,"method":"tools/call","params":{"name":"prime_context","arguments":{"project":"` + p.ID + `"}}}`,
		`{"jsonrpc":"2.0","id":119,"method":"tools/call","params":{"name":"save_handoff","arguments":{"project":"` + p.ID + `","summary":"Handoff summary","agent":"challenger"}}}`,
		`{"jsonrpc":"2.0","id":120,"method":"tools/call","params":{"name":"get_latest_handoff","arguments":{"project":"` + p.ID + `"}}}`,
		`{"jsonrpc":"2.0","id":121,"method":"tools/call","params":{"name":"save_memo","arguments":{"project":"` + p.ID + `","content":"Test memo","agent":"challenger"}}}`,
		`{"jsonrpc":"2.0","id":122,"method":"tools/call","params":{"name":"save_doc","arguments":{"project":"` + p.ID + `","title":"Doc2","url":"https://example.com/doc2","content":"Doc 2 content"}}}`,
		`{"jsonrpc":"2.0","id":123,"method":"tools/call","params":{"name":"search_docs","arguments":{"query":"content","project":"` + p.ID + `"}}}`,
		`{"jsonrpc":"2.0","id":124,"method":"tools/call","params":{"name":"compile_rules","arguments":{"project":"` + p.ID + `","target":"cursor"}}}`,
		`{"jsonrpc":"2.0","id":125,"method":"tools/call","params":{"name":"vacuum_database","arguments":{"force":true}}}`,
		`{"jsonrpc":"2.0","id":126,"method":"tools/call","params":{"name":"get_db_stats","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":127,"method":"tools/call","params":{"name":"get_message_context","arguments":{"id":1,"window":1}}}`,
		`{"jsonrpc":"2.0","id":128,"method":"tools/call","params":{"name":"omni_search","arguments":{"query":"challenger","project":"` + p.ID + `"}}}`,
		`{"jsonrpc":"2.0","id":129,"method":"tools/call","params":{"name":"diagnose_system","arguments":{"repair":false}}}`,
		`{"jsonrpc":"2.0","id":130,"method":"tools/call","params":{"name":"record_feedback","arguments":{"content":"Great tool!","category":"praise","project":"` + p.ID + `"}}}`,
		`{"jsonrpc":"2.0","id":131,"method":"tools/call","params":{"name":"list_feedback","arguments":{"project":"` + p.ID + `"}}}`,
		`{"jsonrpc":"2.0","id":132,"method":"tools/call","params":{"name":"get_doc","arguments":{"url":"https://example.com/doc"}}}`,
	}

	inCalls := strings.NewReader(strings.Join(calls, "\n") + "\n")
	var outCalls bytes.Buffer
	srvCalls := NewServer(st, inCalls, &outCalls)
	if err := srvCalls.Run(); err != nil {
		t.Fatal(err)
	}

	respLines := strings.Split(strings.TrimSpace(outCalls.String()), "\n")
	if len(respLines) != len(calls) {
		t.Fatalf("expected %d responses, got %d:\n%s", len(calls), len(respLines), outCalls.String())
	}

	// Verify specific responses
	resMapById := make(map[int]JSONRPCResponse)
	for _, l := range respLines {
		var r JSONRPCResponse
		if err := json.Unmarshal([]byte(l), &r); err != nil {
			t.Fatalf("failed to unmarshal line %q: %v", l, err)
		}
		id := int(r.ID.(float64))
		resMapById[id] = r
	}

	// 101: audit_goals with p.ID
	r101 := resMapById[101]
	if r101.Error != nil {
		t.Fatalf("call 101 audit_goals failed: %+v", r101.Error)
	}

	// 102: audit_goals with projDir
	r102 := resMapById[102]
	if r102.Error != nil {
		t.Fatalf("call 102 audit_goals failed: %+v", r102.Error)
	}

	// 103: audit_goals with non-existent project -> error -32602
	r103 := resMapById[103]
	if r103.Error == nil || r103.Error.Code != -32602 {
		t.Fatalf("expected error -32602 for non-existent project in audit_goals, got: %+v", r103.Error)
	}

	// 105: claim root goal -> success
	r105 := resMapById[105]
	if r105.Error != nil {
		t.Fatalf("call 105 claim_goal root failed: %+v", r105.Error)
	}

	// 106: report root goal -> success
	r106 := resMapById[106]
	if r106.Error != nil {
		t.Fatalf("call 106 report_goal failed: %+v", r106.Error)
	}

	// 107: claim blocked dep goal without force -> error
	r107 := resMapById[107]
	if r107.Error == nil {
		t.Fatalf("expected claim_goal on blocked goal without force to fail, got success")
	}

	// 108: claim blocked dep goal with force -> success
	r108 := resMapById[108]
	if r108.Error != nil {
		t.Fatalf("call 108 claim_goal with force failed: %+v", r108.Error)
	}
}

func TestMCPReopenGoal(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "mcp_reopen.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Initialize git repo in tmp
	runGit := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = tmp
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s failed: %v (%s)", strings.Join(args, " "), err, string(out))
		}
		return strings.TrimSpace(string(out))
	}
	runGit("init")
	runGit("config", "user.email", "mcp@test.local")
	runGit("config", "user.name", "MCP Tester")

	p, err := st.EnsureProject(tmp)
	if err != nil {
		t.Fatal(err)
	}

	// Create goal package
	goalDir := filepath.Join(tmp, "goals", "mcp-reopen")
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte("# Goal: MCP Reopen\n\n- Mode: Independent\n"), 0644)
	_ = os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte("# Plan\n- [x] Done\n"), 0644)
	_ = os.WriteFile(filepath.Join(goalDir, "facts.md"), []byte("# Facts\n"), 0644)

	runGit("add", ".")
	runGit("commit", "-m", "feat: implemented initial (goals/mcp-reopen/goal.md)")

	// Call MCP reopen_goal
	req := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"reopen_goal","arguments":{"slug":"mcp-reopen","reason":"critical regression","agent":"mcp-agent","claim":true,"project":"%s"}}}`, p.ID)
	in := strings.NewReader(req + "\n")
	var out bytes.Buffer
	srv := NewServer(st, in, &out)
	if err := srv.Run(); err != nil {
		t.Fatal(err)
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("reopen_goal failed: %+v", resp.Error)
	}

	resMap, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result type: %+v", resp.Result)
	}
	sData, ok := resMap["structuredData"].(map[string]any)
	if !ok || sData["reopened"] != true {
		t.Fatalf("unexpected reopen result: %+v", resp.Result)
	}
}

func TestMCPResolveFeedback(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	fb := &store.Feedback{
		ProjectID: "test-proj",
		Agent:     "agent-1",
		Category:  "issue",
		Content:   "Database connection pool exhausted",
	}
	if err := st.SaveFeedback(fb); err != nil {
		t.Fatal(err)
	}

	req := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"resolve_feedback","arguments":{"id":"%s","agent":"fixer-agent","goal":"pool-tuning","note":"Increased pool size to 25"}}}`, fb.ID)
	in := strings.NewReader(req + "\n")
	var out bytes.Buffer
	srv := NewServer(st, in, &out)
	if err := srv.Run(); err != nil {
		t.Fatal(err)
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("resolve_feedback failed: %+v", resp.Error)
	}

	res, err := st.GetFeedback(fb.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "closed" || res.ResolvedGoal != "pool-tuning" || res.ResolvedBy != "fixer-agent" {
		t.Fatalf("unexpected feedback state after resolve: %+v", res)
	}
}

func TestMCPTouchGoal(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	tmp := t.TempDir()
	goalDir := filepath.Join(tmp, "goals", "touch-test")
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte("# Goal: Touch Test\n"), 0644)
	p, err := st.EnsureProject(tmp)
	if err != nil {
		t.Fatal(err)
	}

	claimPath := filepath.Join(goalDir, "claim.json")
	claimData := []byte(`{"goal_slug":"touch-test","agent":"tester","claimed_at":"2026-09-01T00:00:00Z","updated_at":"2026-09-01T00:00:00Z","status":"in_progress"}`)
	if err := os.WriteFile(claimPath, claimData, 0644); err != nil {
		t.Fatal(err)
	}

	req := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"touch_goal","arguments":{"slug":"touch-test","agent":"tester","project":"%s"}}}`, p.ID)
	in := strings.NewReader(req + "\n")
	var out bytes.Buffer
	srv := NewServer(st, in, &out)
	if err := srv.Run(); err != nil {
		t.Fatal(err)
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("touch_goal failed: %+v", resp.Error)
	}

	resMap, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result type: %+v", resp.Result)
	}
	sData, ok := resMap["structuredData"].(map[string]any)
	if !ok || sData["agent"] != "tester" {
		t.Fatalf("unexpected touch result: %+v", resp.Result)
	}
}

func TestMCPServer_CompareProposalsAndRecordPreference(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "mcp_rpm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// 1. Test record_preference tool
	reqRecord := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"record_preference","arguments":{"project":"test-proj","candidate_a":"Pure-Go SQLite","candidate_b":"CGO SQLite","winner":"A","rationale":"Zero CGO portability"}}}`
	inRecord := strings.NewReader(reqRecord + "\n")
	var outRecord bytes.Buffer
	srvRecord := NewServer(st, inRecord, &outRecord)
	if err := srvRecord.Run(); err != nil {
		t.Fatal(err)
	}

	var respRecord JSONRPCResponse
	if err := json.Unmarshal(outRecord.Bytes(), &respRecord); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if respRecord.Error != nil {
		t.Fatalf("record_preference failed: %+v", respRecord.Error)
	}

	// 2. Test compare_proposals tool
	reqCompare := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"compare_proposals","arguments":{"project":"test-proj","proposals":["CGO SQLite with mattn","Pure-Go SQLite with modernc"]}}}`
	inCompare := strings.NewReader(reqCompare + "\n")
	var outCompare bytes.Buffer
	srvCompare := NewServer(st, inCompare, &outCompare)
	if err := srvCompare.Run(); err != nil {
		t.Fatal(err)
	}

	var respCompare JSONRPCResponse
	if err := json.Unmarshal(outCompare.Bytes(), &respCompare); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if respCompare.Error != nil {
		t.Fatalf("compare_proposals failed: %+v", respCompare.Error)
	}

	cMap, ok := respCompare.Result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected compare result: %+v", respCompare.Result)
	}
	sData, ok := cMap["structuredData"].(map[string]any)
	if !ok || sData["winning_index"] == nil {
		t.Fatalf("missing winning_index in compare structuredData: %+v", cMap)
	}
}

func TestDecisionsMCPTools(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test_decisions_mcp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if _, err := st.EnsureProject("/repo/test-dec-proj"); err != nil {
		t.Fatal(err)
	}

	// 1. Create decision D1
	reqCreate := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_decision","arguments":{"project":"test-dec-proj","title":"Database Driver","options":["A: Pure-Go SQLite (No CGO)","B: CGO SQLite (GCC required)"],"recommendation":"A","rationale":"Portable single binary","goal":"db-layer"}}}`
	inCreate := strings.NewReader(reqCreate + "\n")
	var outCreate bytes.Buffer
	srvCreate := NewServer(st, inCreate, &outCreate)
	if err := srvCreate.Run(); err != nil {
		t.Fatal(err)
	}

	var respCreate JSONRPCResponse
	if err := json.Unmarshal(outCreate.Bytes(), &respCreate); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if respCreate.Error != nil {
		t.Fatalf("create_decision failed: %+v", respCreate.Error)
	}
	cMap := respCreate.Result.(map[string]any)
	sData := cMap["structuredData"].(map[string]any)
	if sData["address"] != "D1" || sData["recommendation"] != "A" {
		t.Fatalf("unexpected decision data: %+v", sData)
	}

	// 2. List decisions
	reqList := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_decisions","arguments":{"project":"test-dec-proj"}}}`
	inList := strings.NewReader(reqList + "\n")
	var outList bytes.Buffer
	srvList := NewServer(st, inList, &outList)
	if err := srvList.Run(); err != nil {
		t.Fatal(err)
	}
	var respList JSONRPCResponse
	if err := json.Unmarshal(outList.Bytes(), &respList); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if respList.Error != nil {
		t.Fatalf("list_decisions failed: %+v", respList.Error)
	}
	lMap := respList.Result.(map[string]any)
	lData := lMap["structuredData"].([]any)
	if len(lData) != 1 {
		t.Fatalf("expected 1 decision in list, got %d", len(lData))
	}

	// 3. Resolve decision D1
	reqResolve := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"resolve_decision","arguments":{"project":"test-dec-proj","address":"D1","choice":"A","notes":"Chose pure-Go SQLite"}}}`
	inResolve := strings.NewReader(reqResolve + "\n")
	var outResolve bytes.Buffer
	srvResolve := NewServer(st, inResolve, &outResolve)
	if err := srvResolve.Run(); err != nil {
		t.Fatal(err)
	}
	var respResolve JSONRPCResponse
	if err := json.Unmarshal(outResolve.Bytes(), &respResolve); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if respResolve.Error != nil {
		t.Fatalf("resolve_decision failed: %+v", respResolve.Error)
	}
	rMap := respResolve.Result.(map[string]any)
	rData := rMap["structuredData"].(map[string]any)
	if rData["status"] != "resolved" || rData["resolution"] != "A" {
		t.Fatalf("unexpected resolved data: %+v", rData)
	}

	// 4. Record preference linked to D1
	reqPref := `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"record_preference","arguments":{"project":"test-dec-proj","candidate_a":"Pure-Go SQLite","candidate_b":"CGO SQLite","winner":"A","decision":"D1"}}}`
	inPref := strings.NewReader(reqPref + "\n")
	var outPref bytes.Buffer
	srvPref := NewServer(st, inPref, &outPref)
	if err := srvPref.Run(); err != nil {
		t.Fatal(err)
	}
	var respPref JSONRPCResponse
	if err := json.Unmarshal(outPref.Bytes(), &respPref); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if respPref.Error != nil {
		t.Fatalf("record_preference failed: %+v", respPref.Error)
	}
	pMap := respPref.Result.(map[string]any)
	pData := pMap["structuredData"].(map[string]any)
	if pData["decision_address"] != "D1" {
		t.Fatalf("expected decision_address D1, got %v", pData["decision_address"])
	}
}

func TestRunGatePipelineMCPTool(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Call run_gate_pipeline on an invalid dir
	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"run_gate_pipeline","arguments":{"project":"/nonexistent/path/for/gate/test"}}}`
	in := strings.NewReader(req + "\n")
	var out bytes.Buffer
	srv := NewServer(st, in, &out)
	if err := srv.Run(); err != nil {
		t.Fatal(err)
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if resp.Error == nil {
		t.Fatalf("expected error for nonexistent git repo, got: %+v", resp.Result)
	}
}

func TestSyncRulesMCPTool(t *testing.T) {
	tmpDir := t.TempDir()
	st, err := store.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	req := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"sync_rules","arguments":{"project":%q,"tools":["all"]}}}`, tmpDir)
	in := strings.NewReader(req + "\n")
	var out bytes.Buffer
	srv := NewServer(st, in, &out)
	if err := srv.Run(); err != nil {
		t.Fatal(err)
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
}

func TestSyncRulesWithPrinciplesMCPTool(t *testing.T) {
	tmpDir := t.TempDir()
	st, err := store.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	req := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"sync_rules","arguments":{"project":%q,"tools":["all"],"include_principles":true,"stack":"go"}}}`, tmpDir)
	in := strings.NewReader(req + "\n")
	var out bytes.Buffer
	srv := NewServer(st, in, &out)
	if err := srv.Run(); err != nil {
		t.Fatal(err)
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	// Verify AGENTS.md was written with principles block
	agentsPath := filepath.Join(tmpDir, "AGENTS.md")
	data, err := os.ReadFile(agentsPath)
	if err != nil {
		t.Fatalf("missing AGENTS.md: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "<!-- sila:begin (principles) -->") {
		t.Fatal("AGENTS.md missing principles block begin marker")
	}
	if !strings.Contains(content, "Pure-Go CGO-Free Portability") {
		t.Fatal("AGENTS.md missing Go-specific principles")
	}

	// Verify Cursor principles rule was written
	cursorPath := filepath.Join(tmpDir, ".cursor", "rules", "000-sila-principles.mdc")
	cData, err := os.ReadFile(cursorPath)
	if err != nil {
		t.Fatalf("missing 000-sila-principles.mdc: %v", err)
	}
	if !strings.Contains(string(cData), "Radical Simplicity") {
		t.Fatal("Cursor principles rule missing Radical Simplicity")
	}
}

func TestRunPreflightEvalMCPTool(t *testing.T) {
	tmpDir := t.TempDir()
	st, err := store.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	caseDir, err := eval.InitCaseTemplate(tmpDir, "preflight-test", false)
	if err != nil {
		t.Fatal(err)
	}

	// Update sila_contract.md to use command: "echo ok" for test portability
	contractFile := filepath.Join(caseDir, "graders", "sila_contract.md")
	_ = os.WriteFile(contractFile, []byte(`---
type: sila_contract
action: command
command: "echo ok"
weight: 2.0
---
`), 0644)

	outputCheck := filepath.Join(caseDir, "graders", "output_check.md")
	_ = os.WriteFile(outputCheck, []byte(`---
type: regex
target: message
match_type: contains
pattern: "Mock response"
weight: 1.0
---
`), 0644)

	// Call run_preflight_eval over JSON-RPC with explicit mock
	req := fmt.Sprintf(`{"jsonrpc":"2.0","id":99,"method":"tools/call","params":{"name":"run_preflight_eval","arguments":{"eval_dir":%q,"harness":"mock"}}}`, tmpDir)
	in := strings.NewReader(req + "\n")
	var out bytes.Buffer
	srv := NewServer(st, in, &out)
	if err := srv.Run(); err != nil {
		t.Fatal(err)
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error from run_preflight_eval: %+v", resp.Error)
	}
	resMap, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got: %T", resp.Result)
	}
	sData, ok := resMap["structuredData"].(map[string]any)
	if !ok || sData["synthetic"] != true {
		t.Errorf("expected synthetic: true in structuredData, got: %v", sData["synthetic"])
	}
	contentArr, ok := resMap["content"].([]any)
	if !ok || len(contentArr) == 0 {
		t.Fatalf("expected content array in result")
	}
	firstContent := contentArr[0].(map[string]any)
	if !strings.Contains(firstContent["text"].(string), "PASSED") {
		t.Errorf("expected text summary to contain 'PASSED', got: %s", firstContent["text"])
	}

	// Test failing evaluation in MCP reflects FAILED in text and 0 quality score in structuredData
	failCaseDir, err := eval.InitCaseTemplate(tmpDir, "mcp-fail-case", false)
	if err != nil {
		t.Fatal(err)
	}
	badGrader := filepath.Join(failCaseDir, "graders", "output_check.md")
	_ = os.WriteFile(badGrader, []byte(`---
type: regex
target: message
match_type: contains
pattern: "CANNOT_POSSIBLY_MATCH_RANDOM_TOKEN_12345"
weight: 2.0
---
`), 0644)
	reqFail := fmt.Sprintf(`{"jsonrpc":"2.0","id":991,"method":"tools/call","params":{"name":"run_preflight_eval","arguments":{"eval_dir":%q,"harness":"mock"}}}`, failCaseDir)
	inFail := strings.NewReader(reqFail + "\n")
	var outFail bytes.Buffer
	srvFail := NewServer(st, inFail, &outFail)
	if err := srvFail.Run(); err != nil {
		t.Fatal(err)
	}
	var respFail JSONRPCResponse
	if err := json.Unmarshal(outFail.Bytes(), &respFail); err != nil {
		t.Fatal(err)
	}
	resFailMap := respFail.Result.(map[string]any)
	failData := resFailMap["structuredData"].(map[string]any)
	if failData["passed"] == true {
		t.Errorf("expected passed: false in failing eval structuredData")
	}
	if failData["total_score"].(float64) != 0.0 {
		t.Errorf("expected total_score: 0.0 in failing eval structuredData, got: %v", failData["total_score"])
	}
	if failData["max_score"].(float64) <= 0.0 {
		t.Errorf("expected max_score > 0 in failing eval structuredData, got: %v", failData["max_score"])
	}
	failContentArr := resFailMap["content"].([]any)
	failFirstContent := failContentArr[0].(map[string]any)
	if !strings.Contains(failFirstContent["text"].(string), "FAILED") {
		t.Errorf("expected text summary to contain 'FAILED', got: %s", failFirstContent["text"])
	}

	// Test unknown harness runner fails in MCP
	reqUnknown := fmt.Sprintf(`{"jsonrpc":"2.0","id":100,"method":"tools/call","params":{"name":"run_preflight_eval","arguments":{"eval_dir":%q,"harness":"nonexistent_runner"}}}`, tmpDir)
	inUnknown := strings.NewReader(reqUnknown + "\n")
	var outUnknown bytes.Buffer
	srvUnknown := NewServer(st, inUnknown, &outUnknown)
	if err := srvUnknown.Run(); err != nil {
		t.Fatal(err)
	}
	var respUnknown JSONRPCResponse
	if err := json.Unmarshal(outUnknown.Bytes(), &respUnknown); err != nil {
		t.Fatal(err)
	}
	if respUnknown.Error == nil || !strings.Contains(fmt.Sprintf("%v", respUnknown.Error.Data), "unknown harness runner") {
		t.Errorf("expected unknown harness error, got: %+v", respUnknown.Error)
	}

	// Test negative timeout_seconds fails
	reqBadTimeout := fmt.Sprintf(`{"jsonrpc":"2.0","id":101,"method":"tools/call","params":{"name":"run_preflight_eval","arguments":{"eval_dir":%q,"harness":"mock","timeout_seconds":-5}}}`, tmpDir)
	inBadTimeout := strings.NewReader(reqBadTimeout + "\n")
	var outBadTimeout bytes.Buffer
	srvBadTimeout := NewServer(st, inBadTimeout, &outBadTimeout)
	if err := srvBadTimeout.Run(); err != nil {
		t.Fatal(err)
	}
	var respBadTimeout JSONRPCResponse
	if err := json.Unmarshal(outBadTimeout.Bytes(), &respBadTimeout); err != nil {
		t.Fatal(err)
	}
	if respBadTimeout.Error == nil || !strings.Contains(fmt.Sprintf("%v", respBadTimeout.Error.Data), "timeout_seconds must be >= 0") {
		t.Errorf("expected timeout_seconds validation error, got: %+v", respBadTimeout.Error)
	}

	// Test caller repository isolation via MCP
	sentinelFile := filepath.Join(tmpDir, "mcp_sentinel.txt")
	if err := os.WriteFile(sentinelFile, []byte("ORIGINAL_MCP"), 0644); err != nil {
		t.Fatal(err)
	}
	promptFile := filepath.Join(caseDir, "prompt.md")
	promptContent := fmt.Sprintf(`---
scaffold: "echo MUTATED_MCP > mcp_sentinel.txt; echo MCP_LEAK > mcp_leak.txt"
---
Write something
`)
	if err := os.WriteFile(promptFile, []byte(promptContent), 0644); err != nil {
		t.Fatal(err)
	}
	reqIso := fmt.Sprintf(`{"jsonrpc":"2.0","id":102,"method":"tools/call","params":{"name":"run_preflight_eval","arguments":{"eval_dir":%q,"project":%q,"harness":"mock"}}}`, tmpDir, tmpDir)
	inIso := strings.NewReader(reqIso + "\n")
	var outIso bytes.Buffer
	srvIso := NewServer(st, inIso, &outIso)
	if err := srvIso.Run(); err != nil {
		t.Fatal(err)
	}
	sentinelRead, err := os.ReadFile(sentinelFile)
	if err != nil || string(sentinelRead) != "ORIGINAL_MCP" {
		t.Fatalf("expected mcp_sentinel.txt to remain ORIGINAL_MCP, got %q (err: %v)", string(sentinelRead), err)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "mcp_leak.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected mcp_leak.txt to not exist in project dir")
	}
}

func TestMCPDriftPreviewAndHandoffMemoSuggestions(t *testing.T) {
	tmpDir := t.TempDir()
	st, err := store.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	p, err := st.EnsureProject(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	// Insert message with invariant rule marker
	st.WriteRun("", "claude", "1", []store.LoadMessage{
		{
			ProjectID:   p.ID,
			Tool:        "claude",
			Role:        "assistant",
			Content:     "Rule: always use pure-Go SQLite drivers without CGO",
			ContentHash: "h-mcp-1",
			TS:          "2026-09-14T12:00:00Z",
		},
	})

	// 1. Reconcile drift with dry_run
	reqDrift := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"reconcile_drift","arguments":{"project":%q,"dry_run":true}}}`, p.ID)
	// 2. Save handoff (should detect candidate memo)
	reqHandoff := fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"save_handoff","arguments":{"project":%q,"summary":"Completed session work"}}}`, p.ID)
	// 3. Prime context (should include context budget)
	reqPrime := fmt.Sprintf(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"prime_context","arguments":{"project":%q}}}`, p.ID)

	payload := strings.Join([]string{reqDrift, reqHandoff, reqPrime}, "\n") + "\n"
	var out bytes.Buffer
	srv := NewServer(st, strings.NewReader(payload), &out)
	if err := srv.Run(); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 responses, got %d", len(lines))
	}

	// Verify drift preview
	var respDrift JSONRPCResponse
	if err := json.Unmarshal([]byte(lines[0]), &respDrift); err != nil {
		t.Fatal(err)
	}
	if respDrift.Error != nil {
		t.Fatalf("drift preview failed: %+v", respDrift.Error)
	}
	driftMap := respDrift.Result.(map[string]any)
	driftContent := driftMap["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(driftContent, "[PREVIEW / DRY-RUN]") {
		t.Errorf("expected [PREVIEW / DRY-RUN] in text, got %q", driftContent)
	}
	driftData := driftMap["structuredData"].(map[string]any)
	if driftData["dry_run"] != true {
		t.Errorf("expected dry_run=true in structuredData, got %v", driftData["dry_run"])
	}

	// Verify save handoff with suggested memos
	var respHandoff JSONRPCResponse
	if err := json.Unmarshal([]byte(lines[1]), &respHandoff); err != nil {
		t.Fatal(err)
	}
	if respHandoff.Error != nil {
		t.Fatalf("save_handoff failed: %+v", respHandoff.Error)
	}
	hMap := respHandoff.Result.(map[string]any)
	hContent := hMap["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(hContent, "Candidate Memos Detected") {
		t.Errorf("expected Candidate Memos Detected in handoff output, got %q", hContent)
	}
	hData := hMap["structuredData"].(map[string]any)
	suggested, ok := hData["suggested_memos"].([]any)
	if !ok || len(suggested) == 0 {
		t.Errorf("expected suggested_memos in structuredData, got %v", hData["suggested_memos"])
	}

	// Verify prime_context includes context budget
	var respPrime JSONRPCResponse
	if err := json.Unmarshal([]byte(lines[2]), &respPrime); err != nil {
		t.Fatal(err)
	}
	if respPrime.Error != nil {
		t.Fatalf("prime_context failed: %+v", respPrime.Error)
	}
	pMap := respPrime.Result.(map[string]any)
	pContent := pMap["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(pContent, "Context Surface") {
		t.Errorf("expected Context Surface in prime briefing, got %q", pContent)
	}
	pData := pMap["structuredData"].(map[string]any)
	if pData["context_budget"] == nil {
		t.Errorf("expected context_budget in structuredData, got nil")
	}
}

// TestMCPArgumentValidationAndBounds covers the codex-eval F05 probe: mistyped
// arguments must be rejected instead of silently degrading scope, and numeric
// bounds must clamp instead of streaming unbounded results.
func TestMCPArgumentValidationAndBounds(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if _, err := st.EnsureProject("/repo/test-proj"); err != nil {
		t.Fatal(err)
	}
	st.UpsertFact(store.Fact{
		ID: "fact_v1", ProjectID: "test-proj",
		Claim:      "unique validation marker claim about teapot protocols",
		Confidence: "high", Tools: []string{"devin"},
		SessionRef: "s1", FirstSeen: "2026-08-01T00:00:00Z",
	})
	st.WriteRun("", "devin", "1", []store.LoadMessage{
		{ProjectID: "test-proj", ContentHash: "h1", TS: "2026-08-01T00:00:00Z", Role: "assistant", Content: "message one", Tool: "devin"},
	})

	requests := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		// F05 probe: numeric project must be a params error, not an unscoped search
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search_knowledge","arguments":{"query":"teapot","project":17}}}`,
		// Unparseable arguments
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search_messages","arguments":{"query":"x","limit":"lots"}}}`,
		// Out-of-bounds limit is clamped, served without error
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"search_knowledge","arguments":{"query":"teapot","limit":100000}}}`,
		// Out-of-bounds window is clamped to 20
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"get_message_context","arguments":{"id":1,"window":999}}}`,
	}
	in := strings.NewReader(strings.Join(requests, "\n") + "\n")
	var out bytes.Buffer
	srv := NewServer(st, in, &out)
	if err := srv.Run(); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != len(requests) {
		t.Fatalf("expected %d responses, got %d: %s", len(requests), len(lines), out.String())
	}

	byID := map[float64]JSONRPCResponse{}
	for _, ln := range lines {
		var r JSONRPCResponse
		if err := json.Unmarshal([]byte(ln), &r); err != nil {
			t.Fatalf("bad response line: %v (%s)", err, ln)
		}
		idNum, ok := r.ID.(float64)
		if !ok {
			t.Fatalf("non-numeric id in response: %s", ln)
		}
		byID[idNum] = r
	}

	resp := byID[2]
	if resp.Error == nil || resp.Error.Code != -32602 {
		t.Fatalf("numeric project must be rejected as invalid params, got %+v", resp)
	}

	resp = byID[3]
	if resp.Error == nil || resp.Error.Code != -32602 {
		t.Fatalf("mistyped limit must be rejected as invalid params, got %+v", resp)
	}

	resp = byID[4]
	if resp.Error != nil {
		t.Fatalf("oversized-but-typed limit must clamp, not error: %+v", resp.Error)
	}

	resp = byID[5]
	if resp.Error != nil {
		t.Fatalf("oversized window must clamp, not error: %+v", resp.Error)
	}
	pMap := resp.Result.(map[string]any)
	sd := pMap["structuredData"].(map[string]any)
	if w, ok := sd["window"].(float64); !ok || w != 20 {
		t.Fatalf("window not clamped to 20: %+v", sd["window"])
	}
}

// TestMCPRejectsNullArguments is the re-eval F05 probe: a null selector decoded
// to a zero value and silently became "no filter", so {"project": null}
// returned an unscoped search instead of failing.
func TestMCPRejectsNullArguments(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.EnsureProject("/repo/null-probe"); err != nil {
		t.Fatal(err)
	}

	requests := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search_knowledge","arguments":{"query":"teapots","project":null}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search_messages","arguments":{"query":"teapots","tool":null}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"sync_rules","arguments":{"tools":["all",null]}}}`,
		// A genuinely absent selector is still fine.
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"search_knowledge","arguments":{"query":"teapots"}}}`,
	}
	in := strings.NewReader(strings.Join(requests, "\n") + "\n")
	var out bytes.Buffer
	if err := NewServer(st, in, &out).Run(); err != nil {
		t.Fatal(err)
	}

	byID := map[float64]JSONRPCResponse{}
	for line := range strings.SplitSeq(strings.TrimSpace(out.String()), "\n") {
		var r JSONRPCResponse
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("bad response line: %v (%s)", err, line)
		}
		if id, ok := r.ID.(float64); ok {
			byID[id] = r
		}
	}

	for _, id := range []float64{2, 3, 4} {
		r, ok := byID[id]
		if !ok {
			t.Fatalf("no response for request %v", id)
		}
		if r.Error == nil || r.Error.Code != -32602 {
			t.Fatalf("null selector must be rejected as invalid params: %+v", r)
		}
		if !strings.Contains(fmt.Sprint(r.Error.Data), "null is not a valid value") {
			t.Fatalf("error should name the null field: %+v", r.Error)
		}
	}
	if r := byID[5]; r.Error != nil {
		t.Fatalf("an omitted selector must still work: %+v", r.Error)
	}
}

func TestMCPReconcileDriftSemantic(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "mcp_drift.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	projDir := filepath.Join(tmp, "proj")
	if err := os.MkdirAll(projDir, 0755); err != nil {
		t.Fatal(err)
	}
	p, err := st.EnsureProject(projDir)
	if err != nil {
		t.Fatal(err)
	}

	req := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"reconcile_drift","arguments":{"project":%q,"semantic":true,"dry_run":true}}}`, p.ID)
	var out bytes.Buffer
	srv := NewServer(st, strings.NewReader(req+"\n"), &out)
	if err := srv.Run(); err != nil {
		t.Fatal(err)
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	resMap := resp.Result.(map[string]any)
	sData := resMap["structuredData"].(map[string]any)
	if sData["semantic"] != true {
		t.Errorf("expected semantic=true in structuredData, got %v", sData["semantic"])
	}
}

func TestClaimGoalWithSessionAndNativeUUID(t *testing.T) {
	// Verify GenerateSessionID generates valid UUID
	sessID := GenerateSessionID()
	if sessID == "" {
		t.Fatal("GenerateSessionID() returned empty")
	}
	if _, err := store.ParseUUID(sessID); err != nil {
		t.Fatalf("GenerateSessionID() returned invalid UUID %q: %v", sessID, err)
	}

	tempDir := t.TempDir()
	st, err := store.Open(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	goalDir := filepath.Join(tempDir, "goals", "test-uuid-goal")
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatal(err)
	}
	goalMD := `# Goal: Test UUID Goal
## Goal Description
Test goal for UUID session claim.
## Dependencies & Execution Order
- **Mode**: Independent
- **Shape**: ship
- **Files To Touch**: file.go
`
	if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalMD), 0644); err != nil {
		t.Fatal(err)
	}

	req := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"claim_goal","arguments":{"slug":"test-uuid-goal","agent":"@devin","project":%q,"session":%q}}}`, tempDir, sessID)
	var out bytes.Buffer
	srv := NewServer(st, strings.NewReader(req+"\n"), &out)
	if err := srv.Run(); err != nil {
		t.Fatal(err)
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	resMap, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result type: %+v", resp.Result)
	}
	sData, ok := resMap["structuredData"].(map[string]any)
	if !ok {
		t.Fatalf("missing structuredData: %+v", resMap)
	}
	claimMap, ok := sData["claim"].(map[string]any)
	if !ok {
		t.Fatalf("missing claim in structuredData: %+v", sData)
	}
	if claimMap["session_id"] != sessID {
		t.Errorf("session_id = %v, want %q", claimMap["session_id"], sessID)
	}
}

func TestMCPServerGoroutineLeak(t *testing.T) {
	assess.AssertNoGoroutineLeaks(t)

	tempDir := t.TempDir()
	st, err := store.Open(filepath.Join(tempDir, "leak_test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	req := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`
	var out bytes.Buffer
	srv := NewServer(st, strings.NewReader(req+"\n"), &out)
	if err := srv.Run(); err != nil {
		t.Fatal(err)
	}
}

func TestMCPServerHTTPTransportWithSynctest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tempDir := t.TempDir()
		st, err := store.Open(filepath.Join(tempDir, "http_mcp_test.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()

		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			srv := NewServer(st, r.Body, w)
			_ = srv.Run()
		})

		ts := httptest.NewTestServer(t, handler)
		client := ts.Client()

		reqBody := `{"jsonrpc":"2.0","id":42,"method":"tools/list","params":{}}`
		resp, err := client.Post(ts.URL+"/mcp", "application/json", strings.NewReader(reqBody))
		if err != nil {
			t.Fatalf("client.Post: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
		}

		var rpcResp JSONRPCResponse
		if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
			t.Fatalf("decode rpc response: %v", err)
		}
		if rpcResp.Error != nil {
			t.Fatalf("unexpected error: %+v", rpcResp.Error)
		}
		resultMap, ok := rpcResp.Result.(map[string]any)
		if !ok {
			t.Fatalf("unexpected result type: %+v", rpcResp.Result)
		}
		tools, ok := resultMap["tools"].([]any)
		if !ok || len(tools) == 0 {
			t.Fatalf("expected tools in response, got %+v", resultMap)
		}
	})
}

func TestMCP_GetOperationalGuidebook(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// 1. Markdown format
	reqMD := `{"jsonrpc":"2.0","id":101,"method":"tools/call","params":{"name":"get_operational_guidebook","arguments":{"format":"markdown"}}}`
	inMD := strings.NewReader(reqMD + "\n")
	var outMD bytes.Buffer
	srvMD := NewServer(st, inMD, &outMD)
	if err := srvMD.Run(); err != nil {
		t.Fatal(err)
	}

	var respMD JSONRPCResponse
	if err := json.Unmarshal(outMD.Bytes(), &respMD); err != nil {
		t.Fatalf("unmarshal resp: %v", err)
	}
	if respMD.Error != nil {
		t.Fatalf("unexpected error: %+v", respMD.Error)
	}
	resMap, ok := respMD.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected result map, got: %+v", respMD.Result)
	}
	content, ok := resMap["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("expected content in result, got: %+v", resMap)
	}
	textItem := content[0].(map[string]any)
	textStr := textItem["text"].(string)
	if !strings.Contains(textStr, "Sila Developer Operational Guidebook") {
		t.Errorf("expected guidebook header in markdown output, got: %s", textStr)
	}

	// 2. Domain filter
	reqDomain := `{"jsonrpc":"2.0","id":102,"method":"tools/call","params":{"name":"get_operational_guidebook","arguments":{"domain":"security"}}}`
	inDomain := strings.NewReader(reqDomain + "\n")
	var outDomain bytes.Buffer
	srvDomain := NewServer(st, inDomain, &outDomain)
	if err := srvDomain.Run(); err != nil {
		t.Fatal(err)
	}

	var respDomain JSONRPCResponse
	if err := json.Unmarshal(outDomain.Bytes(), &respDomain); err != nil {
		t.Fatalf("unmarshal resp: %v", err)
	}
	if respDomain.Error != nil {
		t.Fatalf("unexpected error: %+v", respDomain.Error)
	}

	// 3. Search query filter
	reqSearch := `{"jsonrpc":"2.0","id":103,"method":"tools/call","params":{"name":"get_operational_guidebook","arguments":{"search":"gofmt","format":"summary"}}}`
	inSearch := strings.NewReader(reqSearch + "\n")
	var outSearch bytes.Buffer
	srvSearch := NewServer(st, inSearch, &outSearch)
	if err := srvSearch.Run(); err != nil {
		t.Fatal(err)
	}
	var respSearch JSONRPCResponse
	if err := json.Unmarshal(outSearch.Bytes(), &respSearch); err != nil {
		t.Fatalf("unmarshal resp: %v", err)
	}
	if respSearch.Error != nil {
		t.Fatalf("unexpected error: %+v", respSearch.Error)
	}
	resSearch := respSearch.Result.(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(resSearch, "Sila Operational Guidebook:") || !strings.Contains(resSearch, "Golang Engineering Disciplines") {
		t.Errorf("expected gofmt search match in summary, got: %s", resSearch)
	}

	// 4. HTML format
	reqHTML := `{"jsonrpc":"2.0","id":104,"method":"tools/call","params":{"name":"get_operational_guidebook","arguments":{"format":"html"}}}`
	inHTML := strings.NewReader(reqHTML + "\n")
	var outHTML bytes.Buffer
	srvHTML := NewServer(st, inHTML, &outHTML)
	if err := srvHTML.Run(); err != nil {
		t.Fatal(err)
	}
	var respHTML JSONRPCResponse
	if err := json.Unmarshal(outHTML.Bytes(), &respHTML); err != nil {
		t.Fatalf("unmarshal resp: %v", err)
	}
	resHTML := respHTML.Result.(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(resHTML, "<!DOCTYPE html>") {
		t.Errorf("expected HTML doctype, got: %s", resHTML)
	}

	// 5. TOON format
	reqTOON := `{"jsonrpc":"2.0","id":105,"method":"tools/call","params":{"name":"get_operational_guidebook","arguments":{"format":"toon"}}}`
	inTOON := strings.NewReader(reqTOON + "\n")
	var outTOON bytes.Buffer
	srvTOON := NewServer(st, inTOON, &outTOON)
	if err := srvTOON.Run(); err != nil {
		t.Fatal(err)
	}
	var respTOON JSONRPCResponse
	if err := json.Unmarshal(outTOON.Bytes(), &respTOON); err != nil {
		t.Fatalf("unmarshal resp: %v", err)
	}
	resTOON := respTOON.Result.(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(resTOON, "guidebook_summary:") {
		t.Errorf("expected TOON output, got: %s", resTOON)
	}
}

func TestMCP_RouteWorkflow(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "route_mcp_test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// 1. Valid objective routing to investigation
	reqInv := `{"jsonrpc":"2.0","id":201,"method":"tools/call","params":{"name":"route_workflow","arguments":{"objective":"debug why test suite is failing with sqlite lock"}}}`
	inInv := strings.NewReader(reqInv + "\n")
	var outInv bytes.Buffer
	srvInv := NewServer(st, inInv, &outInv)
	if err := srvInv.Run(); err != nil {
		t.Fatal(err)
	}

	var respInv JSONRPCResponse
	if err := json.Unmarshal(outInv.Bytes(), &respInv); err != nil {
		t.Fatalf("unmarshal resp: %v", err)
	}
	if respInv.Error != nil {
		t.Fatalf("unexpected error: %+v", respInv.Error)
	}
	resMap, ok := respInv.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected result map, got: %+v", respInv.Result)
	}
	content, ok := resMap["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("expected content, got: %+v", resMap)
	}
	textItem := content[0].(map[string]any)
	textStr := textItem["text"].(string)
	if !strings.Contains(textStr, "Recommended Workflow: investigation") {
		t.Errorf("expected investigation workflow in text, got: %s", textStr)
	}
	if !strings.Contains(textStr, "search_knowledge") {
		t.Errorf("expected search_knowledge in tool sequence, got: %s", textStr)
	}
	if !strings.Contains(textStr, "Equivalent Sila CLI Commands:") {
		t.Errorf("expected CLI command section, got: %s", textStr)
	}
	if !strings.Contains(textStr, `sila search "<query>" --omni`) {
		t.Errorf("expected omni-search CLI equivalent, got: %s", textStr)
	}

	// 2. Missing objective returns invalid params error
	reqErr := `{"jsonrpc":"2.0","id":202,"method":"tools/call","params":{"name":"route_workflow","arguments":{}}}`
	inErr := strings.NewReader(reqErr + "\n")
	var outErr bytes.Buffer
	srvErr := NewServer(st, inErr, &outErr)
	if err := srvErr.Run(); err != nil {
		t.Fatal(err)
	}

	var respErr JSONRPCResponse
	if err := json.Unmarshal(outErr.Bytes(), &respErr); err != nil {
		t.Fatalf("unmarshal resp: %v", err)
	}
	if respErr.Error == nil || respErr.Error.Code != -32602 {
		t.Errorf("expected invalid params error (-32602), got: %+v", respErr.Error)
	}
}

func TestMCP_SemanticOperationSentry(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "sentry_mcp_test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// 1. High-blast-radius tool call with destructive SQL payload
	// Execution MUST succeed (fail-open) and output MUST prepend sentry advisory warning.
	reqDestructive := `{"jsonrpc":"2.0","id":301,"method":"tools/call","params":{"name":"save_memo","arguments":{"content":"Beware of executing DROP TABLE users; in production"}}}`
	inDestructive := strings.NewReader(reqDestructive + "\n")
	var outDestructive bytes.Buffer
	srv := NewServer(st, inDestructive, &outDestructive)
	if err := srv.Run(); err != nil {
		t.Fatal(err)
	}

	var respDestructive JSONRPCResponse
	if err := json.Unmarshal(outDestructive.Bytes(), &respDestructive); err != nil {
		t.Fatalf("unmarshal resp: %v", err)
	}
	if respDestructive.Error != nil {
		t.Fatalf("unexpected error (sentry must fail-open): %+v", respDestructive.Error)
	}
	resMap, ok := respDestructive.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected result map, got: %+v", respDestructive.Result)
	}
	content, ok := resMap["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("expected content, got: %+v", resMap)
	}
	textItem := content[0].(map[string]any)
	textStr := textItem["text"].(string)
	if !strings.Contains(textStr, "⚠️ [Sentry Advisory]") {
		t.Errorf("expected Sentry Advisory in output text, got: %s", textStr)
	}
	if !strings.Contains(textStr, "destructive database schema change or table wipe detected") {
		t.Errorf("expected destructive schema warning, got: %s", textStr)
	}

	// 2. High-blast-radius tool call with credential exposure payload
	reqSecret := `{"jsonrpc":"2.0","id":302,"method":"tools/call","params":{"name":"save_memo","arguments":{"content":"Set API_KEY=ghp_1234567890abcdef1234567890abcdef1234"}}}`
	inSecret := strings.NewReader(reqSecret + "\n")
	var outSecret bytes.Buffer
	srvSecret := NewServer(st, inSecret, &outSecret)
	if err := srvSecret.Run(); err != nil {
		t.Fatal(err)
	}

	var respSecret JSONRPCResponse
	if err := json.Unmarshal(outSecret.Bytes(), &respSecret); err != nil {
		t.Fatalf("unmarshal resp: %v", err)
	}
	if respSecret.Error != nil {
		t.Fatalf("unexpected error (sentry must fail-open): %+v", respSecret.Error)
	}
	resMapSecret := respSecret.Result.(map[string]any)
	contentSecret := resMapSecret["content"].([]any)
	textSecret := contentSecret[0].(map[string]any)["text"].(string)
	if !strings.Contains(textSecret, "⚠️ [Sentry Advisory]") {
		t.Errorf("expected Sentry Advisory for token, got: %s", textSecret)
	}
	if !strings.Contains(textSecret, "potential credential exposure detected") {
		t.Errorf("expected credential warning, got: %s", textSecret)
	}

	// 3. Clean mutating tool call does NOT receive sentry warning
	reqClean := `{"jsonrpc":"2.0","id":303,"method":"tools/call","params":{"name":"save_memo","arguments":{"content":"Standard architectural design notes."}}}`
	inClean := strings.NewReader(reqClean + "\n")
	var outClean bytes.Buffer
	srvClean := NewServer(st, inClean, &outClean)
	if err := srvClean.Run(); err != nil {
		t.Fatal(err)
	}

	var respClean JSONRPCResponse
	if err := json.Unmarshal(outClean.Bytes(), &respClean); err != nil {
		t.Fatalf("unmarshal resp: %v", err)
	}
	if respClean.Error != nil {
		t.Fatalf("unexpected error: %+v", respClean.Error)
	}
	resMapClean := respClean.Result.(map[string]any)
	contentClean := resMapClean["content"].([]any)
	textClean := contentClean[0].(map[string]any)["text"].(string)
	if strings.Contains(textClean, "⚠️ [Sentry Advisory]") {
		t.Errorf("expected NO sentry advisory for clean payload, got: %s", textClean)
	}

	// 4. Low-risk tool call (search_knowledge) does NOT trigger sentry
	reqRead := `{"jsonrpc":"2.0","id":304,"method":"tools/call","params":{"name":"search_knowledge","arguments":{"query":"test"}}}`
	inRead := strings.NewReader(reqRead + "\n")
	var outRead bytes.Buffer
	srvRead := NewServer(st, inRead, &outRead)
	if err := srvRead.Run(); err != nil {
		t.Fatal(err)
	}

	var respRead JSONRPCResponse
	if err := json.Unmarshal(outRead.Bytes(), &respRead); err != nil {
		t.Fatalf("unmarshal resp: %v", err)
	}
	if respRead.Error != nil {
		t.Fatalf("unexpected error: %+v", respRead.Error)
	}
	resMapRead := respRead.Result.(map[string]any)
	contentRead := resMapRead["content"].([]any)
	textRead := contentRead[0].(map[string]any)["text"].(string)
	if strings.Contains(textRead, "⚠️ [Sentry Advisory]") {
		t.Errorf("expected NO sentry advisory for read-only tool, got: %s", textRead)
	}
}

func TestMCP_RequestApproval(t *testing.T) {
	tempHome := t.TempDir()
	dbPath := filepath.Join(tempHome, "test_mcp_approvals.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	coord := gateway.GetDefaultCoordinator(st, nil)

	// 1. Successful approval
	go func() {
		for i := 0; i < 50; i++ {
			time.Sleep(50 * time.Millisecond)
			pending, err := st.ListPendingApprovals("")
			if err == nil && len(pending) > 0 {
				_, _ = coord.Resolve(pending[0].ID, true, "test-reviewer", "Action approved")
				return
			}
		}
	}()

	reqCall := `{"jsonrpc":"2.0","id":401,"method":"tools/call","params":{"name":"request_approval","arguments":{"project":"test-project","agent":"test-agent","prompt":"Apply production migration schema","risk_level":"high","timeout_seconds":5}}}`
	inCall := strings.NewReader(reqCall + "\n")
	var outCall bytes.Buffer
	srvCall := NewServer(st, inCall, &outCall)
	if err := srvCall.Run(); err != nil {
		t.Fatal(err)
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal(outCall.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal resp: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	resMap := resp.Result.(map[string]any)
	content := resMap["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)

	sData, ok := resMap["structuredData"].(map[string]any)
	if !ok {
		t.Fatalf("missing structuredData: %+v", resMap)
	}
	if sData["approved"] != true {
		t.Fatalf("expected approved=true, got: %v", sData["approved"])
	}
	if sData["status"] != "approved" {
		t.Fatalf("expected status=approved, got: %v", sData["status"])
	}
	if sData["decided_by"] != "test-reviewer" {
		t.Fatalf("expected decided_by=test-reviewer, got: %v", sData["decided_by"])
	}
	if !strings.Contains(text, "Approval GRANTED") {
		t.Fatalf("expected summary to contain 'Approval GRANTED', got: %s", text)
	}

	// 2. Rejected approval
	go func() {
		for i := 0; i < 50; i++ {
			time.Sleep(50 * time.Millisecond)
			pending, err := st.ListPendingApprovals("")
			if err == nil && len(pending) > 0 {
				_, _ = coord.Resolve(pending[0].ID, false, "security-bot", "Action rejected: security risk")
				return
			}
		}
	}()

	reqReject := `{"jsonrpc":"2.0","id":402,"method":"tools/call","params":{"name":"request_approval","arguments":{"project":"test-project","prompt":"Delete user database","risk_level":"critical","timeout_seconds":5}}}`
	inReject := strings.NewReader(reqReject + "\n")
	var outReject bytes.Buffer
	srvReject := NewServer(st, inReject, &outReject)
	if err := srvReject.Run(); err != nil {
		t.Fatal(err)
	}

	var respRej JSONRPCResponse
	if err := json.Unmarshal(outReject.Bytes(), &respRej); err != nil {
		t.Fatalf("unmarshal resp: %v", err)
	}
	if respRej.Error != nil {
		t.Fatalf("unexpected error: %+v", respRej.Error)
	}
	resMapRej := respRej.Result.(map[string]any)
	contentRej := resMapRej["content"].([]any)
	textRej := contentRej[0].(map[string]any)["text"].(string)

	sDataRej, ok := resMapRej["structuredData"].(map[string]any)
	if !ok {
		t.Fatalf("missing structuredData: %+v", resMapRej)
	}
	if sDataRej["approved"] != false {
		t.Fatalf("expected approved=false, got: %v", sDataRej["approved"])
	}
	if sDataRej["status"] != "rejected" {
		t.Fatalf("expected status=rejected, got: %v", sDataRej["status"])
	}
	if !strings.Contains(textRej, "Approval REJECTED") {
		t.Fatalf("expected summary to contain 'Approval REJECTED', got: %s", textRej)
	}

	// 3. Timeout
	reqTimeout := `{"jsonrpc":"2.0","id":403,"method":"tools/call","params":{"name":"request_approval","arguments":{"prompt":"Fast timeout request","timeout_seconds":1}}}`
	inTimeout := strings.NewReader(reqTimeout + "\n")
	var outTimeout bytes.Buffer
	srvTimeout := NewServer(st, inTimeout, &outTimeout)
	if err := srvTimeout.Run(); err != nil {
		t.Fatal(err)
	}

	var respTimeout JSONRPCResponse
	if err := json.Unmarshal(outTimeout.Bytes(), &respTimeout); err != nil {
		t.Fatalf("unmarshal resp: %v", err)
	}
	if respTimeout.Error != nil {
		t.Fatalf("unexpected error: %+v", respTimeout.Error)
	}
	resMapTimeout := respTimeout.Result.(map[string]any)
	contentTimeout := resMapTimeout["content"].([]any)
	textTimeout := contentTimeout[0].(map[string]any)["text"].(string)

	sDataTimeout, ok := resMapTimeout["structuredData"].(map[string]any)
	if !ok {
		t.Fatalf("missing structuredData: %+v", resMapTimeout)
	}
	if sDataTimeout["approved"] != false {
		t.Fatalf("expected approved=false on timeout, got: %v", sDataTimeout["approved"])
	}
	if sDataTimeout["status"] != "timed_out" {
		t.Fatalf("expected status=timed_out, got: %v", sDataTimeout["status"])
	}
	if !strings.Contains(textTimeout, "TIMED OUT") {
		t.Fatalf("expected summary to contain 'TIMED OUT', got: %s", textTimeout)
	}
}

func TestMCP_GoplsTools(t *testing.T) {
	tempHome := t.TempDir()
	dbPath := filepath.Join(tempHome, "test_mcp_gopls.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	cwd, _ := os.Getwd()
	serverGoPath := filepath.Join(cwd, "server.go")

	// 1. go_symbol_references
	reqSym := fmt.Sprintf(`{"jsonrpc":"2.0","id":501,"method":"tools/call","params":{"name":"go_symbol_references","arguments":{"file":%q,"symbol":"PublicToolCount"}}}`, serverGoPath)
	inSym := strings.NewReader(reqSym + "\n")
	var outSym bytes.Buffer
	srvSym := NewServer(st, inSym, &outSym)
	if err := srvSym.Run(); err != nil {
		t.Fatal(err)
	}

	var respSym JSONRPCResponse
	if err := json.Unmarshal(outSym.Bytes(), &respSym); err != nil {
		t.Fatalf("unmarshal resp: %v", err)
	}
	if respSym.Error != nil {
		t.Fatalf("unexpected error: %+v", respSym.Error)
	}
	resMapSym := respSym.Result.(map[string]any)
	sDataSym := resMapSym["structuredData"].(map[string]any)
	if sDataSym["symbol"] != "PublicToolCount" {
		t.Errorf("expected symbol PublicToolCount, got %v", sDataSym["symbol"])
	}
	refs := sDataSym["references"].([]any)
	if len(refs) == 0 {
		t.Errorf("expected references to PublicToolCount, got 0")
	}

	// 2. go_package_api
	reqAPI := fmt.Sprintf(`{"jsonrpc":"2.0","id":502,"method":"tools/call","params":{"name":"go_package_api","arguments":{"pkg":%q}}}`, cwd)
	inAPI := strings.NewReader(reqAPI + "\n")
	var outAPI bytes.Buffer
	srvAPI := NewServer(st, inAPI, &outAPI)
	if err := srvAPI.Run(); err != nil {
		t.Fatal(err)
	}

	var respAPI JSONRPCResponse
	if err := json.Unmarshal(outAPI.Bytes(), &respAPI); err != nil {
		t.Fatalf("unmarshal resp: %v", err)
	}
	if respAPI.Error != nil {
		t.Fatalf("unexpected error: %+v", respAPI.Error)
	}
	resMapAPI := respAPI.Result.(map[string]any)
	sDataAPI := resMapAPI["structuredData"].(map[string]any)
	if sDataAPI["package"] != "mcp" {
		t.Errorf("expected package 'mcp', got %v", sDataAPI["package"])
	}
	typesAPI := sDataAPI["types"].([]any)
	if len(typesAPI) == 0 {
		t.Errorf("expected types in package API, got 0")
	}

	// 3. go_diagnostics
	reqDiag := `{"jsonrpc":"2.0","id":503,"method":"tools/call","params":{"name":"go_diagnostics","arguments":{"path":"."}}}`
	inDiag := strings.NewReader(reqDiag + "\n")
	var outDiag bytes.Buffer
	srvDiag := NewServer(st, inDiag, &outDiag)
	if err := srvDiag.Run(); err != nil {
		t.Fatal(err)
	}

	var respDiag JSONRPCResponse
	if err := json.Unmarshal(outDiag.Bytes(), &respDiag); err != nil {
		t.Fatalf("unmarshal resp: %v", err)
	}
	if respDiag.Error != nil {
		t.Fatalf("unexpected error: %+v", respDiag.Error)
	}
	resMapDiag := respDiag.Result.(map[string]any)
	sDataDiag := resMapDiag["structuredData"].(map[string]any)
	if sDataDiag["provider"] == "" {
		t.Errorf("expected non-empty diagnostics provider")
	}
}

func TestMCP_CloudflareTools(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// 1. cloudflare_d1_query (offline / local schema inspection)
	reqD1 := `{"jsonrpc":"2.0","id":601,"method":"tools/call","params":{"name":"cloudflare_d1_query","arguments":{"query":"SELECT name FROM sqlite_master WHERE type='table'","project":"."}}}`
	inD1 := strings.NewReader(reqD1 + "\n")
	var outD1 bytes.Buffer
	srvD1 := NewServer(st, inD1, &outD1)
	if err := srvD1.Run(); err != nil {
		t.Fatal(err)
	}

	var respD1 JSONRPCResponse
	if err := json.Unmarshal(outD1.Bytes(), &respD1); err != nil {
		t.Fatalf("unmarshal resp: %v", err)
	}
	if respD1.Error != nil {
		t.Fatalf("unexpected error: %+v", respD1.Error)
	}
	resMapD1 := respD1.Result.(map[string]any)
	sDataD1 := resMapD1["structuredData"].(map[string]any)
	if sDataD1["provider"] == "" {
		t.Errorf("expected non-empty D1 provider")
	}

	// 2. cloudflare_worker_status
	reqStatus := `{"jsonrpc":"2.0","id":602,"method":"tools/call","params":{"name":"cloudflare_worker_status","arguments":{"project":"."}}}`
	inStatus := strings.NewReader(reqStatus + "\n")
	var outStatus bytes.Buffer
	srvStatus := NewServer(st, inStatus, &outStatus)
	if err := srvStatus.Run(); err != nil {
		t.Fatal(err)
	}

	var respStatus JSONRPCResponse
	if err := json.Unmarshal(outStatus.Bytes(), &respStatus); err != nil {
		t.Fatalf("unmarshal resp: %v", err)
	}
	if respStatus.Error != nil {
		t.Fatalf("unexpected error: %+v", respStatus.Error)
	}
	resMapStatus := respStatus.Result.(map[string]any)
	sDataStatus := resMapStatus["structuredData"].(map[string]any)
	if sDataStatus["script_name"] == "" {
		t.Errorf("expected non-empty script_name")
	}

	// 3. cloudflare_edge_telemetry
	reqTelem := `{"jsonrpc":"2.0","id":603,"method":"tools/call","params":{"name":"cloudflare_edge_telemetry","arguments":{"time_range":"24h","project":"."}}}`
	inTelem := strings.NewReader(reqTelem + "\n")
	var outTelem bytes.Buffer
	srvTelem := NewServer(st, inTelem, &outTelem)
	if err := srvTelem.Run(); err != nil {
		t.Fatal(err)
	}

	var respTelem JSONRPCResponse
	if err := json.Unmarshal(outTelem.Bytes(), &respTelem); err != nil {
		t.Fatalf("unmarshal resp: %v", err)
	}
	if respTelem.Error != nil {
		t.Fatalf("unexpected error: %+v", respTelem.Error)
	}
	resMapTelem := respTelem.Result.(map[string]any)
	sDataTelem := resMapTelem["structuredData"].(map[string]any)
	if sDataTelem["provider"] == "" {
		t.Errorf("expected non-empty telemetry provider")
	}
}

func TestMCP_SaveCaseAndRecall(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "mcp_case.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	p, err := st.EnsureProject(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "handler.go"), []byte("package handler"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 1. Call save_case via MCP
	reqCase := fmt.Sprintf(`{"jsonrpc":"2.0","id":701,"method":"tools/call","params":{"name":"save_case","arguments":{"project":%q,"intent":"Graceful shutdown timeout","approach":"context.WithTimeout in SIGTERM listener","invariant":"database pool closed after active queries finish","files":["handler.go"],"tags":"shutdown,concurrency"}}}`, p.ID)
	inCase := strings.NewReader(reqCase + "\n")
	var outCase bytes.Buffer
	srvCase := NewServer(st, inCase, &outCase)
	if err := srvCase.Run(); err != nil {
		t.Fatal(err)
	}

	var respCase JSONRPCResponse
	if err := json.Unmarshal(outCase.Bytes(), &respCase); err != nil {
		t.Fatalf("unmarshal resp: %v", err)
	}
	if respCase.Error != nil {
		t.Fatalf("unexpected error: %+v", respCase.Error)
	}

	// 2. Call recall via MCP and verify categorized results
	reqRecall := fmt.Sprintf(`{"jsonrpc":"2.0","id":702,"method":"tools/call","params":{"name":"recall","arguments":{"project":%q,"query":"Graceful shutdown timeout"}}}`, p.ID)
	inRecall := strings.NewReader(reqRecall + "\n")
	var outRecall bytes.Buffer
	srvRecall := NewServer(st, inRecall, &outRecall)
	if err := srvRecall.Run(); err != nil {
		t.Fatal(err)
	}

	var respRecall JSONRPCResponse
	if err := json.Unmarshal(outRecall.Bytes(), &respRecall); err != nil {
		t.Fatalf("unmarshal recall resp: %v", err)
	}
	if respRecall.Error != nil {
		t.Fatalf("unexpected recall error: %+v", respRecall.Error)
	}
	resMap := respRecall.Result.(map[string]any)
	sData := resMap["structuredData"].(map[string]any)
	hits := sData["hits"].([]any)
	if len(hits) == 0 {
		t.Fatal("expected recall hits, got 0")
	}
	cases := sData["cases"].([]any)
	if len(cases) == 0 {
		t.Fatal("expected structuredData.cases to be populated, got 0")
	}
}

func TestEmitLifecycleEventTool(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "mcp_hook.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	req := `{"jsonrpc":"2.0","id":801,"method":"tools/call","params":{"name":"emit_lifecycle_event","arguments":{"event":"post-tool-use","agent":"test-agent","session_id":"s-mcp-99","payload":{"tool":"read_file","status":"ok"},"drain":true}}}`
	in := strings.NewReader(req + "\n")
	var out bytes.Buffer
	srv := NewServer(st, in, &out)
	if err := srv.Run(); err != nil {
		t.Fatal(err)
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal resp: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	resMap, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result type: %+v", resp.Result)
	}
	sData, ok := resMap["structuredData"].(map[string]any)
	if !ok {
		t.Fatalf("missing structuredData: %+v", resMap)
	}
	if sData["status"] != "spooled" {
		t.Errorf("expected status 'spooled', got %v", sData["status"])
	}
	if sData["event"] != "post-tool-use" {
		t.Errorf("expected event 'post-tool-use', got %v", sData["event"])
	}
	if sData["drained"] != true {
		t.Errorf("expected drained true, got %v", sData["drained"])
	}
}

func TestClaimAndPopHandoffTools(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if _, err := st.EnsureProject(tmp); err != nil {
		t.Fatal(err)
	}

	h := store.Handoff{
		ProjectID: "mcp-app",
		Agent:     "producer",
		Branch:    "feat/mcp-baton",
		Summary:   "Handoff for MCP testing",
		OpenItems: []string{"Task 1", "Task 2"},
		CreatedAt: "2026-08-15T10:00:00Z",
		Status:    string(store.HandoffPending),
	}
	h.ID = store.HandoffID(h.ProjectID, h.Summary, h.CreatedAt)
	if err := st.InsertHandoff(h); err != nil {
		t.Fatal(err)
	}

	// 1. Test claim_handoff
	reqClaim := fmt.Sprintf(`{"jsonrpc":"2.0","id":901,"method":"tools/call","params":{"name":"claim_handoff","arguments":{"handoff_id":%q,"agent_name":"consumer-agent","session_id":"s-mcp-claim-1"}}}`, h.ID)
	in := strings.NewReader(reqClaim + "\n")
	var out bytes.Buffer
	srv := NewServer(st, in, &out)
	if err := srv.Run(); err != nil {
		t.Fatal(err)
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal resp: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	resMap, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result: %+v", resp.Result)
	}
	sData, ok := resMap["structuredData"].(map[string]any)
	if !ok {
		t.Fatalf("missing structuredData: %+v", resMap)
	}
	if sData["status"] != string(store.HandoffClaimed) {
		t.Errorf("expected status 'claimed', got %v", sData["status"])
	}
	if sData["claimed_by"] != "consumer-agent" {
		t.Errorf("expected claimed_by 'consumer-agent', got %v", sData["claimed_by"])
	}

	// 2. Re-claiming with different session must fail
	reqClaimFail := fmt.Sprintf(`{"jsonrpc":"2.0","id":902,"method":"tools/call","params":{"name":"claim_handoff","arguments":{"handoff_id":%q,"agent_name":"intruder","session_id":"s-mcp-diff"}}}`, h.ID)
	in = strings.NewReader(reqClaimFail + "\n")
	out.Reset()
	srv = NewServer(st, in, &out)
	_ = srv.Run()
	var failResp JSONRPCResponse
	_ = json.Unmarshal(out.Bytes(), &failResp)
	if failResp.Error == nil {
		t.Fatal("expected error claiming already claimed handoff")
	}

	// 3. Test pop_handoff
	hPop := store.Handoff{
		ProjectID: "mcp-app",
		Agent:     "producer-2",
		Branch:    "feat/pop-branch",
		Summary:   "Handoff to be popped",
		CreatedAt: "2026-08-16T10:00:00Z",
		Status:    string(store.HandoffPending),
	}
	hPop.ID = store.HandoffID(hPop.ProjectID, hPop.Summary, hPop.CreatedAt)
	if err := st.InsertHandoff(hPop); err != nil {
		t.Fatal(err)
	}

	reqPop := `{"jsonrpc":"2.0","id":903,"method":"tools/call","params":{"name":"pop_handoff","arguments":{"project":"mcp-app","branch":"feat/pop-branch","agent_name":"pop-agent","session_id":"s-pop-1"}}}`
	in = strings.NewReader(reqPop + "\n")
	out.Reset()
	srv = NewServer(st, in, &out)
	if err := srv.Run(); err != nil {
		t.Fatal(err)
	}
	var popResp JSONRPCResponse
	if err := json.Unmarshal(out.Bytes(), &popResp); err != nil {
		t.Fatal(err)
	}
	if popResp.Error != nil {
		t.Fatalf("unexpected pop error: %+v", popResp.Error)
	}
	popMap := popResp.Result.(map[string]any)
	popStruct := popMap["structuredData"].(map[string]any)
	if popStruct["id"] != hPop.ID {
		t.Errorf("expected popped handoff %s, got %v", hPop.ID, popStruct["id"])
	}
	if popStruct["status"] != string(store.HandoffClaimed) {
		t.Errorf("expected status 'claimed', got %v", popStruct["status"])
	}
}

func TestAgentProvenanceMetadataSchema(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "provenance_test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// 1. save_memo with provenance metadata
	reqMemo := `{"jsonrpc":"2.0","id":1001,"method":"tools/call","params":{"name":"save_memo","arguments":{"project":"prov-proj","content":"Use logical CSS properties exclusively","tool_name":"antigravity","client_kind":"ai_agent","origin_entity":"org:waqftech"}}}`
	in := strings.NewReader(reqMemo + "\n")
	var out bytes.Buffer
	srv := NewServer(st, in, &out)
	if err := srv.Run(); err != nil {
		t.Fatal(err)
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected save_memo error: %+v", resp.Error)
	}
	resMap := resp.Result.(map[string]any)
	structData := resMap["structuredData"].(map[string]any)
	if structData["tool_name"] != "antigravity" {
		t.Errorf("expected tool_name 'antigravity', got %v", structData["tool_name"])
	}
	if structData["client_kind"] != "ai_agent" {
		t.Errorf("expected client_kind 'ai_agent', got %v", structData["client_kind"])
	}
	if structData["origin_entity"] != "org:waqftech" {
		t.Errorf("expected origin_entity 'org:waqftech', got %v", structData["origin_entity"])
	}

	// 2. Verify store persistence and retrieval
	facts, err := st.FactsByProject("prov-proj")
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact, got %d", len(facts))
	}
	if facts[0].ToolName != "antigravity" || facts[0].ClientKind != "ai_agent" || facts[0].OriginEntity != "org:waqftech" {
		t.Errorf("persisted fact provenance mismatch: %+v", facts[0].Provenance())
	}

	// 3. Provenance isolation between personal PAT and organization
	reqPATMemo := `{"jsonrpc":"2.0","id":1002,"method":"tools/call","params":{"name":"save_memo","arguments":{"project":"prov-proj","content":"Personal exploratory note","tool_name":"curl","client_kind":"human","origin_entity":"pat:jadmadi"}}}`
	in = strings.NewReader(reqPATMemo + "\n")
	out.Reset()
	srv = NewServer(st, in, &out)
	if err := srv.Run(); err != nil {
		t.Fatal(err)
	}

	allFacts, err := st.FactsByProject("prov-proj")
	if err != nil {
		t.Fatal(err)
	}
	if len(allFacts) != 2 {
		t.Fatalf("expected 2 facts, got %d", len(allFacts))
	}
	// Check distinct origin entities preserved
	var origins []string
	for _, f := range allFacts {
		origins = append(origins, f.OriginEntity)
	}
	sort.Strings(origins)
	if origins[0] != "org:waqftech" || origins[1] != "pat:jadmadi" {
		t.Errorf("expected origins [org:waqftech, pat:jadmadi], got %v", origins)
	}

	// 4. save_handoff with provenance
	reqHandoff := `{"jsonrpc":"2.0","id":1003,"method":"tools/call","params":{"name":"save_handoff","arguments":{"project":"prov-proj","summary":"Baton passed with provenance","tool_name":"cursor","client_kind":"ai_agent","origin_entity":"org:waqftech"}}}`
	in = strings.NewReader(reqHandoff + "\n")
	out.Reset()
	srv = NewServer(st, in, &out)
	if err := srv.Run(); err != nil {
		t.Fatal(err)
	}
	h, err := st.LatestHandoff("prov-proj")
	if err != nil {
		t.Fatal(err)
	}
	if h == nil || h.ToolName != "cursor" || h.OriginEntity != "org:waqftech" {
		t.Errorf("handoff provenance mismatch: %+v", h)
	}
}

func TestServer_ResolveTargetProject_ScopeIsolation(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "mcp_scope.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	dirA := filepath.Join(t.TempDir(), "clientA", "portal")
	dirB := filepath.Join(t.TempDir(), "clientB", "portal")
	if err := os.MkdirAll(dirA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dirB, 0o755); err != nil {
		t.Fatal(err)
	}

	// Register dirA with ID "portal"
	if _, err := st.DB.Exec(`INSERT INTO projects (id, path, created_at) VALUES (?, ?, datetime('now'))`, "portal", dirA); err != nil {
		t.Fatal(err)
	}

	srv := NewServer(st, strings.NewReader(""), &bytes.Buffer{})

	// Resolving dirA must return "portal"
	pA, err := srv.resolveTargetProject(dirA)
	if err != nil {
		t.Fatalf("resolveTargetProject(dirA) failed: %v", err)
	}
	if pA != "portal" {
		t.Errorf("expected dirA to resolve to 'portal', got %q", pA)
	}

	// Resolving dirB (unregistered with same basename) must NOT return "portal"
	pB, err := srv.resolveTargetProject(dirB)
	if err != nil {
		t.Fatalf("resolveTargetProject(dirB) failed: %v", err)
	}
	if pB == "portal" {
		t.Fatalf("dirB must not collide with registered 'portal' owned by dirA")
	}
	if !strings.HasPrefix(pB, "portal-") {
		t.Errorf("expected pB to be disambiguated with 'portal-' prefix, got %q", pB)
	}

	// resolveTargetProjectDir must return clean canonical paths
	pathA, err := srv.resolveTargetProjectDir(dirA)
	if err != nil {
		t.Fatalf("resolveTargetProjectDir(dirA) failed: %v", err)
	}
	if pathA != store.CanonicalProjectPath(dirA) {
		t.Errorf("expected %q, got %q", dirA, pathA)
	}
}

func TestSaveHandoff_AutoDetectsGitBranch(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	dir := t.TempDir()
	cmdInit := exec.Command("git", "init")
	cmdInit.Dir = dir
	if err := cmdInit.Run(); err != nil {
		t.Fatalf("git init failed: %v", err)
	}
	_ = exec.Command("git", "-C", dir, "config", "user.name", "Test").Run()
	_ = exec.Command("git", "-C", dir, "config", "user.email", "test@example.com").Run()
	if err := exec.Command("git", "-C", dir, "commit", "--allow-empty", "-m", "init").Run(); err != nil {
		t.Fatalf("git commit failed: %v", err)
	}
	cmdCheckout := exec.Command("git", "-C", dir, "checkout", "-b", "feature/my-branch")
	if err := cmdCheckout.Run(); err != nil {
		t.Fatalf("git checkout -b failed: %v", err)
	}

	p, err := st.EnsureProject(dir)
	if err != nil {
		t.Fatalf("EnsureProject failed: %v", err)
	}

	reqJSON := fmt.Sprintf(`{"jsonrpc":"2.0","id":101,"method":"tools/call","params":{"name":"save_handoff","arguments":{"project":%q,"summary":"Session work on branch"}}}`, p.ID)

	in := strings.NewReader(reqJSON + "\n")
	var out bytes.Buffer
	srv := NewServer(st, in, &out)
	if err := srv.Run(); err != nil {
		t.Fatalf("server Run failed: %v", err)
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response failed: %v, raw: %s", err, out.String())
	}
	if resp.Error != nil {
		t.Fatalf("expected success, got error: %+v", resp.Error)
	}

	h, err := st.LatestHandoff(p.ID)
	if err != nil {
		t.Fatalf("LatestHandoff failed: %v", err)
	}
	if h == nil {
		t.Fatalf("expected handoff to be inserted")
	}
	if h.Branch != "feature/my-branch" {
		t.Errorf("expected branch 'feature/my-branch', got %q", h.Branch)
	}
}

func TestServer_ProfileFiltering(t *testing.T) {
	// 1. Test continuity profile (exposing the 36-tool agent-workflow surface)
	reqList := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	inContinuity := strings.NewReader(reqList + "\n")
	var outContinuity bytes.Buffer
	srvContinuity := NewServer(nil, inContinuity, &outContinuity)
	srvContinuity.SetProfile(ProfileContinuity)
	if err := srvContinuity.Run(); err != nil {
		t.Fatalf("server.Run() failed: %v", err)
	}

	var respContinuity JSONRPCResponse
	if err := json.Unmarshal(outContinuity.Bytes(), &respContinuity); err != nil {
		t.Fatalf("failed to unmarshal tools/list response: %v", err)
	}
	resMap, ok := respContinuity.Result.(map[string]any)
	if !ok {
		t.Fatalf("tools/list result is not an object: %+v", respContinuity.Result)
	}
	toolsRaw, ok := resMap["tools"].([]any)
	if !ok {
		t.Fatalf("missing tools array: %+v", resMap)
	}
	if ContinuityToolCount != 36 || len(ContinuityToolNames) != ContinuityToolCount {
		t.Fatalf("continuity surface mismatch: count=%d names=%d", ContinuityToolCount, len(ContinuityToolNames))
	}
	if len(toolsRaw) != ContinuityToolCount {
		t.Fatalf("expected continuity profile to return %d tools, got %d", ContinuityToolCount, len(toolsRaw))
	}
	expectedContinuity := FilterToolsByProfile(ToolDefinitions(), ProfileContinuity)
	if len(toolsRaw) != len(expectedContinuity) {
		t.Fatalf("expected %d canonical continuity tools, got %d", len(expectedContinuity), len(toolsRaw))
	}
	for i, raw := range toolsRaw {
		toolMap, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("tool %d is not an object", i)
		}
		if got, want := toolMap["name"], expectedContinuity[i].Name; got != want {
			t.Fatalf("continuity order[%d] = %v, want %q", i, got, want)
		}
	}
	for _, raw := range toolsRaw {
		toolMap, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name, _ := toolMap["name"].(string)
		if !ContinuityToolNames[name] {
			t.Errorf("unexpected tool %q exposed in continuity profile", name)
		}
	}

	// 2. Test full profile (restoring complete 75 tools)
	inFull := strings.NewReader(reqList + "\n")
	var outFull bytes.Buffer
	srvFull := NewServer(nil, inFull, &outFull)
	srvFull.SetProfile(ProfileFull)
	if err := srvFull.Run(); err != nil {
		t.Fatalf("server.Run() failed: %v", err)
	}

	var respFull JSONRPCResponse
	if err := json.Unmarshal(outFull.Bytes(), &respFull); err != nil {
		t.Fatalf("failed to unmarshal tools/list response: %v", err)
	}
	resMapFull, ok := respFull.Result.(map[string]any)
	if !ok {
		t.Fatalf("tools/list result is not an object: %+v", respFull.Result)
	}
	toolsRawFull, ok := resMapFull["tools"].([]any)
	if !ok {
		t.Fatalf("missing tools array: %+v", resMapFull)
	}
	if len(toolsRawFull) != PublicToolCount {
		t.Fatalf("expected full profile to return %d tools, got %d", PublicToolCount, len(toolsRawFull))
	}
}

func TestContinuityToolNames_AllExistAndCoverContracts(t *testing.T) {
	defs := ToolDefinitions()
	byName := make(map[string]Tool, len(defs))
	for _, def := range defs {
		byName[def.Name] = def
	}
	if len(ContinuityToolNames) != ContinuityToolCount {
		t.Fatalf("len(ContinuityToolNames)=%d, want %d", len(ContinuityToolNames), ContinuityToolCount)
	}
	for name := range ContinuityToolNames {
		if _, ok := byName[name]; !ok {
			t.Errorf("continuity tool %q is not registered", name)
		}
	}
	required := []string{
		"salam", "prime_context", "list_goals", "run_gate_pipeline",
		"reconcile_drift", "claim_goal", "recall", "save_lesson", "save_memo",
		"audit_goals", "save_handoff", "record_feedback", "get_status",
		"search_knowledge", "get_message_context", "touch_goal", "report_goal",
	}
	for _, name := range required {
		if !ContinuityToolNames[name] {
			t.Errorf("required continuity tool %q is missing", name)
		}
	}
	excluded := []string{"vacuum_database", "clean_caches", "supersede_goal", "reopen_goal", "cloudflare_d1_query", "search_web_docs", "spawn_crewmate"}
	for _, name := range excluded {
		if ContinuityToolNames[name] {
			t.Errorf("administrative/destructive tool %q leaked into continuity", name)
		}
	}
	triggers := map[string]string{
		"omni_search":               "Call BEFORE running raw grep, find, or git log",
		"search_docs":               "Call BEFORE web search or scaffolding integrations",
		"save_case":                 "Call when introducing a reusable architectural pattern",
		"get_operational_guidebook": "Call to load Sila's domain engineering standards",
		"route_workflow":            "Call when unsure which Sila command",
	}
	for name, phrase := range triggers {
		def, ok := byName[name]
		if !ok {
			t.Errorf("missing trigger tool %q", name)
			continue
		}
		if !strings.Contains(def.Description, phrase) {
			t.Errorf("%s description missing %q: %s", name, phrase, def.Description)
		}
	}

	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "continuity.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	req := `{"jsonrpc":"2.0","id":77,"method":"tools/call","params":{"name":"get_db_stats","arguments":{}}}`
	var out bytes.Buffer
	srv := NewServer(st, strings.NewReader(req+"\n"), &out)
	srv.SetProfile(ProfileContinuity)
	if err := srv.Run(); err != nil {
		t.Fatal(err)
	}
	var resp JSONRPCResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("direct tools/call outside continuity failed: %+v", resp.Error)
	}
}

func TestMCPSessionIdentity(t *testing.T) {
	t.Setenv("SILA_SESSION_ID", "sess-test")
	st, err := store.Open(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	req := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_status","arguments":{}}}` + "\n"
	var out bytes.Buffer
	srv := NewServer(st, strings.NewReader(req), &out)
	if err := srv.Run(); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("responses: %s", out.String())
	}
	var initResp JSONRPCResponse
	if err := json.Unmarshal([]byte(lines[0]), &initResp); err != nil {
		t.Fatal(err)
	}
	initResult, _ := initResp.Result.(map[string]any)
	caps, _ := initResult["capabilities"].(map[string]any)
	experimental, _ := caps["experimental"].(map[string]any)
	sila, _ := experimental["sila"].(map[string]any)
	if sila["session_id"] != "sess-test" || sila["session_source"] != "env" {
		t.Fatalf("initialize session identity: %+v", sila)
	}
	var statusResp JSONRPCResponse
	if err := json.Unmarshal([]byte(lines[1]), &statusResp); err != nil {
		t.Fatal(err)
	}
	statusResult, _ := statusResp.Result.(map[string]any)
	content, _ := statusResult["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("get_status result: %+v", statusResult)
	}
	text, _ := content[0].(map[string]any)
	if !strings.Contains(fmt.Sprint(text["text"]), `"session_id": "sess-test"`) {
		t.Fatalf("get_status did not expose the env session: %v", text["text"])
	}

	t.Setenv("SILA_SESSION_ID", "")
	generated := NewServer(st, strings.NewReader(""), io.Discard)
	if generated.sessionID == "" || generated.sessionSource != "mcp-process" {
		t.Fatalf("generated session identity: %+v", generated)
	}
}

func TestPrimeContext_SameBasenameDirectoryIsolation(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	dirA := filepath.Join(tmp, "a", "widget")
	dirB := filepath.Join(tmp, "b", "widget")
	if err := os.MkdirAll(dirA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dirB, 0o755); err != nil {
		t.Fatal(err)
	}

	// 1. Record memo in dirA
	_, err = st.InsertMemo("widget", "Secret architectural directive for widget A", "agent", dirA, nil)
	if err != nil {
		t.Fatalf("InsertMemo in dirA failed: %v", err)
	}

	// 2. Call prime_context explicitly scoping to dirB
	reqJSON := fmt.Sprintf(`{"jsonrpc":"2.0","id":201,"method":"tools/call","params":{"name":"prime_context","arguments":{"project":%q}}}`, dirB)
	in := strings.NewReader(reqJSON + "\n")
	var out bytes.Buffer
	srv := NewServer(st, in, &out)
	if err := srv.Run(); err != nil {
		t.Fatalf("server Run failed: %v", err)
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response failed: %v, raw: %s", err, out.String())
	}
	if resp.Error != nil {
		t.Fatalf("expected success, got error: %+v", resp.Error)
	}

	rawResult, _ := json.Marshal(resp.Result)
	resultStr := string(rawResult)
	if strings.Contains(resultStr, "Secret architectural directive for widget A") {
		t.Fatalf("prime_context for dirB leaked dirA's memory directive: %s", resultStr)
	}
}

// TestCommandCodeBridgeContractMarker pins the public schema digest and the
// continuity surface so a server change cannot silently drift away from the
// Command Code fixture contract. Update contract.json only with the schema.
func TestCommandCodeBridgeContractMarker(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "commandcode", "contract.json"))
	if err != nil {
		t.Fatal(err)
	}
	var marker struct {
		Schema              string `json:"schema"`
		SchemaDigest        string `json:"schema_digest"`
		ToolCount           int    `json:"tool_count"`
		ContinuityToolCount int    `json:"continuity_tool_count"`
	}
	if err := json.Unmarshal(data, &marker); err != nil {
		t.Fatal(err)
	}
	if marker.Schema != "sila/mcp-schema/v1" {
		t.Fatalf("marker schema = %q", marker.Schema)
	}
	if marker.SchemaDigest != SchemaDigest() {
		t.Fatalf("marker schema_digest = %q, live = %q; update the fixture deliberately", marker.SchemaDigest, SchemaDigest())
	}
	if marker.ToolCount != PublicToolCount {
		t.Fatalf("marker tool_count = %d, live = %d", marker.ToolCount, PublicToolCount)
	}
	if marker.ContinuityToolCount != ContinuityToolCount {
		t.Fatalf("marker continuity_tool_count = %d, live = %d", marker.ContinuityToolCount, ContinuityToolCount)
	}

	wanted := map[string]bool{
		"recall": true, "prime_context": true, "save_memo": true, "save_lesson": true, "save_handoff": true,
	}
	files, err := filepath.Glob(filepath.Join("testdata", "commandcode", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var fixture struct {
			Request struct {
				Method string `json:"method"`
				Params struct {
					Name      string         `json:"name"`
					Arguments map[string]any `json:"arguments"`
				} `json:"params"`
			} `json:"request"`
		}
		if err := json.Unmarshal(raw, &fixture); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		if fixture.Request.Method != "tools/call" {
			continue
		}
		if fixture.Request.Params.Name == "" || fixture.Request.Params.Arguments == nil {
			t.Fatalf("%s: tools/call fixture must carry params.name and params.arguments", file)
		}
		delete(wanted, fixture.Request.Params.Name)
	}
	if len(wanted) > 0 {
		t.Fatalf("missing Command Code fixtures for continuity tools: %v", wanted)
	}
}

// TestMCP_SpecialistToolParity exercises the four specialist tools added for
// CLI/MCP parity: temporal digest, persona consultation, domain learning, and
// symbol blast-radius analysis.
func TestMCP_SpecialistToolParity(t *testing.T) {
	repo := t.TempDir()
	st, err := store.Open(filepath.Join(t.TempDir(), "specialist.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.EnsureProject(repo); err != nil {
		t.Fatal(err)
	}

	requests := []string{
		fmt.Sprintf(`{"jsonrpc":"2.0","id":601,"method":"tools/call","params":{"name":"get_activity_digest","arguments":{"project":%q}}}`, repo),
		`{"jsonrpc":"2.0","id":602,"method":"tools/call","params":{"name":"consult_personas","arguments":{"action":"list"}}}`,
		fmt.Sprintf(`{"jsonrpc":"2.0","id":603,"method":"tools/call","params":{"name":"save_domain_learning","arguments":{"project":%q,"domain":"golang","title":"Specialist parity","symptom":"missing tool","fix":"add handler"}}}`, repo),
		fmt.Sprintf(`{"jsonrpc":"2.0","id":604,"method":"tools/call","params":{"name":"check_symbol_blast_radius","arguments":{"project":%q,"symbol":"NoSuchSymbol","mode":"safe_delete"}}}`, repo),
	}
	var out bytes.Buffer
	srv := NewServer(st, strings.NewReader(strings.Join(requests, "\n")+"\n"), &out)
	if err := srv.Run(); err != nil {
		t.Fatal(err)
	}
	responses := parseJSONRPCResponses(t, out.Bytes())
	if len(responses) != 4 {
		t.Fatalf("expected 4 responses, got %d: %s", len(responses), out.String())
	}
	for i, resp := range responses {
		if resp.Error != nil {
			t.Fatalf("request %d failed: %+v", i+1, resp.Error)
		}
		resMap, ok := resp.Result.(map[string]any)
		if !ok {
			t.Fatalf("request %d result is not an object: %T", i+1, resp.Result)
		}
		if _, ok := resMap["structuredData"]; !ok {
			t.Fatalf("request %d has no structuredData: %+v", i+1, resMap)
		}
	}

	status := responses[3].Result.(map[string]any)["structuredData"].(map[string]any)
	if status["status"] != "safe-to-delete" {
		t.Fatalf("safe-delete status = %v", status["status"])
	}
}

// TestMCP_CheckSymbolBlastRadiusContractChange verifies an in-memory breaking
// change is reported without touching the file on disk.
func TestMCP_CheckSymbolBlastRadiusContractChange(t *testing.T) {
	repo := t.TempDir()
	pkgDir := filepath.Join(repo, "internal", "store")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	storeSrc := `package store

type Store struct{}

func Open(path string) (*Store, error) {
	return &Store{}, nil
}
`
	if err := os.WriteFile(filepath.Join(pkgDir, "store.go"), []byte(storeSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	cmdDir := filepath.Join(repo, "cmd", "app")
	if err := os.MkdirAll(cmdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	appSrc := `package main

import "internal/store"

func main() {
	st, _ := store.Open("local.db")
	_ = st
}

func initStore() {
	st2, _ := store.Open("test.db")
	_ = st2
}
`
	if err := os.WriteFile(filepath.Join(cmdDir, "main.go"), []byte(appSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(filepath.Join(t.TempDir(), "blast.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.EnsureProject(repo); err != nil {
		t.Fatal(err)
	}

	candidate := "package store\n\ntype Store struct{}\n\nfunc Open(path string, timeout int) (*Store, error) {\n\treturn &Store{}, nil\n}\n"
	candidateJSON, _ := json.Marshal(candidate)
	req := fmt.Sprintf(`{"jsonrpc":"2.0","id":610,"method":"tools/call","params":{"name":"check_symbol_blast_radius","arguments":{"project":%q,"symbol":"internal/store::Open","mode":"edit_check","candidate_payload":%s}}}`, repo, string(candidateJSON))
	var out bytes.Buffer
	srv := NewServer(st, strings.NewReader(req+"\n"), &out)
	if err := srv.Run(); err != nil {
		t.Fatal(err)
	}
	responses := parseJSONRPCResponses(t, out.Bytes())
	if len(responses) != 1 || responses[0].Error != nil {
		t.Fatalf("unexpected response: %+v", responses)
	}
	data := responses[0].Result.(map[string]any)["structuredData"].(map[string]any)
	if data["status"] != "contract-change" {
		t.Fatalf("status = %v, want contract-change: %+v", data["status"], data)
	}
	if incompatible, _ := data["incompatible_count"].(float64); incompatible <= 0 {
		t.Fatalf("incompatible_count = %v, want > 0", data["incompatible_count"])
	}
	if exitCode, _ := data["exit_code"].(float64); exitCode != 1 {
		t.Fatalf("exit_code = %v, want 1", data["exit_code"])
	}
	after, err := os.ReadFile(filepath.Join(pkgDir, "store.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != storeSrc {
		t.Fatal("edit_check modified the file on disk")
	}
}

// parseJSONRPCResponses decodes one JSON document per newline.
func parseJSONRPCResponses(t *testing.T, raw []byte) []JSONRPCResponse {
	t.Helper()
	var out []JSONRPCResponse
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var resp JSONRPCResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			t.Fatalf("invalid JSON-RPC line %q: %v", line, err)
		}
		out = append(out, resp)
	}
	return out
}
