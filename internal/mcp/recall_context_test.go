package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func setupRecallContextMCP(t *testing.T) (*store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "recall_context.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	proj, err := st.EnsureProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	rel := "scripts/deploy.sh"
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertLesson(proj.ID, store.FormatLesson("deploy ordering race", "shared state", "serialize deploy", ""), "agent", dir, []string{rel}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertLesson(proj.ID, store.FormatLesson("deploy ordering retry", "lock timeout", "retry deploy", ""), "agent", dir, []string{"internal/other.go"}); err != nil {
		t.Fatal(err)
	}
	return st, dir
}

func toolResultMap(t *testing.T, resp JSONRPCResponse) (map[string]any, string) {
	t.Helper()
	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result is not an object: %T", resp.Result)
	}
	structured, _ := result["structuredData"].(map[string]any)
	content, _ := result["content"].([]any)
	text := ""
	if len(content) > 0 {
		if first, ok := content[0].(map[string]any); ok {
			text, _ = first["text"].(string)
		}
	}
	return structured, text
}

func TestMCPRecallContext(t *testing.T) {
	st, dir := setupRecallContextMCP(t)
	contextJSON := fmt.Sprintf(`{"task":"deploy ordering","paths":["scripts/deploy.sh"],"branch":"main","head":"4f2a91c","symbols":["serializeDeploy"]}`)
	requests := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
		fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"recall","arguments":{"query":"deploy ordering","project":%q,"limit":5,"max_tokens":1200,"context":%s}}}`, dir, contextJSON),
	}
	responses := runCompatRequests(t, st, ProfileContinuity, requests)
	if responses[1].Error != nil {
		t.Fatalf("recall with context failed: %+v", responses[1].Error)
	}
	structured, text := toolResultMap(t, responses[1])
	pack, ok := structured["pack"].(map[string]any)
	if !ok {
		t.Fatalf("recall result has no pack: %+v", structured)
	}
	items, _ := pack["items"].([]any)
	if len(items) == 0 {
		t.Fatalf("pack is empty: %+v", pack)
	}
	if pack["ranking_version"] != "recall-pack/v1" {
		t.Fatalf("ranking_version = %v", pack["ranking_version"])
	}
	summary, _ := structured["context"].(map[string]any)
	if summary["paths_count"] != float64(1) || summary["task_present"] != true {
		t.Fatalf("context summary = %+v", summary)
	}
	if !strings.Contains(text, "untrusted, possibly stale data") {
		t.Fatalf("MCP text is missing the untrusted line: %s", text)
	}
	if strings.Contains(text, "scripts/deploy.sh") {
		t.Fatalf("MCP text echoed the context path: %s", text)
	}
}

func TestMCPRecallContextStringEncoded(t *testing.T) {
	st, dir := setupRecallContextMCP(t)
	encoded := `{\"task\":\"deploy ordering\",\"paths\":[\"scripts/deploy.sh\"]}`
	request := fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"recall","arguments":{"query":"deploy ordering","project":%q,"context":"%s"}}}`, dir, encoded)
	responses := runCompatRequests(t, st, ProfileContinuity, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
		request,
	})
	if responses[1].Error != nil {
		t.Fatalf("string-encoded context failed: %+v", responses[1].Error)
	}
	structured, _ := toolResultMap(t, responses[1])
	if _, ok := structured["pack"].(map[string]any); !ok {
		t.Fatalf("string-encoded context produced no pack: %+v", structured)
	}
}

func TestMCPRecallContextRejectsUnknown(t *testing.T) {
	st, dir := setupRecallContextMCP(t)
	requests := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
		fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"recall","arguments":{"query":"deploy","project":%q,"context":{"task":"x","bogus":true}}}}`, dir),
		fmt.Sprintf(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"recall","arguments":{"query":"deploy","project":%q,"max_tokens":9000}}}`, dir),
	}
	responses := runCompatRequests(t, st, ProfileContinuity, requests)
	if responses[1].Error == nil || responses[1].Error.Code != -32602 {
		t.Fatalf("unknown context field should be -32602, got %+v", responses[1].Error)
	}
	message, _ := responses[1].Error.Data.(string)
	if !strings.Contains(message, "bogus") {
		t.Fatalf("error does not name the offending key: %+v", responses[1].Error)
	}
	if responses[2].Error == nil || responses[2].Error.Code != -32602 {
		t.Fatalf("out-of-range max_tokens should be -32602, got %+v", responses[2].Error)
	}
}

func TestMCPRecallEmptyPack(t *testing.T) {
	t.Setenv("SILA_OOD_GUARD", "off")
	st, dir := setupRecallContextMCP(t)
	requests := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
		fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"recall","arguments":{"query":"zebra quantum telescope","project":%q,"context":{"task":"zebra quantum telescope"}}}}`, dir),
	}
	responses := runCompatRequests(t, st, ProfileContinuity, requests)
	if responses[1].Error != nil {
		t.Fatalf("empty pack must be a success: %+v", responses[1].Error)
	}
	structured, text := toolResultMap(t, responses[1])
	pack, ok := structured["pack"].(map[string]any)
	if !ok {
		t.Fatalf("empty result has no pack: %+v", structured)
	}
	if pack["empty_reason"] != "no_candidates" {
		t.Fatalf("empty_reason = %v, want no_candidates", pack["empty_reason"])
	}
	if !strings.Contains(text, "empty: no_candidates") {
		t.Fatalf("MCP text is missing the empty reason: %s", text)
	}
}

func TestMCPPrimeContextIncludesPack(t *testing.T) {
	st, dir := setupRecallContextMCP(t)
	request := fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"prime_context","arguments":{"project":%q,"max_tokens":6000,"disable_codebase_map":true,"context":{"task":"deploy ordering","paths":["scripts/deploy.sh"]}}}}`, dir)
	responses := runCompatRequests(t, st, ProfileContinuity, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
		request,
	})
	if responses[1].Error != nil {
		t.Fatalf("prime_context with context failed: %+v", responses[1].Error)
	}
	structured, text := toolResultMap(t, responses[1])
	pack, ok := structured["recall_pack"].(map[string]any)
	if !ok {
		t.Fatalf("prime_context result has no recall_pack: %+v", structured)
	}
	items, _ := pack["items"].([]any)
	if len(items) == 0 {
		t.Fatalf("prime_context recall_pack is empty: %+v", pack)
	}
	if !strings.Contains(text, "Context Recall Pack") {
		t.Fatalf("prime_context text is missing the pack section: %s", text)
	}
	metrics, _ := structured["budget_metrics"].(map[string]any)
	if metrics["fits_budget"] != true {
		t.Fatalf("prime_context exceeded its budget: %+v", metrics)
	}
}

func TestMCPRecallContextJSONShape(t *testing.T) {
	// Unknown top-level fields stay tolerated by the existing normalizer; only
	// the nested context object is strict.
	st, dir := setupRecallContextMCP(t)
	requests := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
		fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"recall","arguments":{"query":"deploy","project":%q,"context":"not-an-object"}}}`, dir),
	}
	responses := runCompatRequests(t, st, ProfileContinuity, requests)
	if responses[1].Error == nil || responses[1].Error.Code != -32602 {
		t.Fatalf("non-object context should be -32602, got %+v", responses[1].Error)
	}
}

func TestRecallToolSchemaDeclaresContext(t *testing.T) {
	var recallTool Tool
	for _, tool := range ToolDefinitions() {
		if tool.Name == "recall" {
			recallTool = tool
		}
	}
	if recallTool.Name != "recall" {
		t.Fatal("recall tool is not registered")
	}
	if recallTool.InputSchema.Properties["context"].Type != "object" {
		t.Fatalf("recall context property = %+v", recallTool.InputSchema.Properties["context"])
	}
	if recallTool.InputSchema.Properties["max_tokens"].Type != "integer" {
		t.Fatalf("recall max_tokens property = %+v", recallTool.InputSchema.Properties["max_tokens"])
	}
	raw, _ := json.Marshal(recallTool.InputSchema)
	if !strings.Contains(string(raw), `"context"`) {
		t.Fatalf("recall schema does not declare context: %s", raw)
	}
}
