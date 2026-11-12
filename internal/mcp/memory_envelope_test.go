package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func memoryMCPEnvelope(project, title, summary string) string {
	return fmt.Sprintf(`{
		"schema_version": "sila/memory/v1",
		"project_id": %q,
		"kind": "root_cause",
		"title": %q,
		"summary": %q,
		"assertions": [{
			"id": "A-001",
			"assertion_type": "descriptive",
			"predicate": "causes",
			"statement": %q,
			"subject": {"context_entity": "project", "value": %q},
			"object": {"literal": {"type": "string", "value": "fix"}},
			"confidence": 0.8,
			"importance": 4,
			"evidence_refs": ["E-001"]
		}],
		"evidence": [{"id": "E-001", "kind": "agent_initiated", "locator": {}, "summary": "MCP test."}]
	}`, project, title, summary, summary, project)
}

func runMCPMemoryRequests(t *testing.T, st *store.Store, payloads ...string) []JSONRPCResponse {
	t.Helper()
	var lines []string
	for i, payload := range payloads {
		var compact bytes.Buffer
		if err := json.Compact(&compact, []byte(payload)); err != nil {
			t.Fatalf("compact payload %d: %v", i, err)
		}
		lines = append(lines, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":%s}`, i+1, compact.String()))
	}
	var out bytes.Buffer
	srv := NewServer(st, strings.NewReader(strings.Join(lines, "\n")+"\n"), &out)
	if err := srv.Run(); err != nil {
		t.Fatal(err)
	}
	rawLines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(rawLines) != len(payloads) {
		t.Fatalf("responses = %d, want %d:\n%s", len(rawLines), len(payloads), out.String())
	}
	responses := make([]JSONRPCResponse, 0, len(rawLines))
	for _, line := range rawLines {
		var resp JSONRPCResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			t.Fatalf("parse response: %v\n%s", err, line)
		}
		responses = append(responses, resp)
	}
	return responses
}

func structuredResult(resp JSONRPCResponse) map[string]any {
	result, _ := resp.Result.(map[string]any)
	data, _ := result["structuredData"].(map[string]any)
	return data
}

func TestMCPSaveMemory(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "mcp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	envelope := memoryMCPEnvelope("proj", "MCP save", "MCP save summary")
	payload := fmt.Sprintf(`{"name":"save_memory","arguments":{"project":"proj","agent":"tester","memory":%s}}`, envelope)
	resp := runMCPMemoryRequests(t, st, payload)[0]
	if resp.Error != nil {
		t.Fatalf("save_memory error: %+v", resp.Error)
	}
	result := structuredResult(resp)
	if created, ok := result["created"].(bool); !ok || !created {
		t.Fatalf("created = %v", result["created"])
	}
	memory, _ := result["memory"].(map[string]any)
	id, _ := memory["id"].(string)
	if !strings.HasPrefix(id, "mem_") {
		t.Fatalf("memory id = %q", id)
	}
	if _, err := st.MemoryRecordByID(id); err != nil {
		t.Fatalf("record not persisted: %v", err)
	}
}

func TestMCPSaveMemoryStringEncoded(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "mcp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	envelope := memoryMCPEnvelope("proj", "MCP string", "MCP string summary")
	encoded, _ := json.Marshal(envelope)
	payload := fmt.Sprintf(`{"name":"save_memory","arguments":{"project":"proj","memory":%s}}`, encoded)
	resp := runMCPMemoryRequests(t, st, payload)[0]
	if resp.Error != nil {
		t.Fatalf("string-encoded save error: %+v", resp.Error)
	}
}

func TestMCPValidateMemory(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "mcp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	good := memoryMCPEnvelope("proj", "Valid", "Valid summary")
	bad := strings.Replace(memoryMCPEnvelope("proj", "Bad", "Bad summary"), `"confidence": 0.8`, `"confidence": 0.8, "unexpected": true`, 1)
	payloads := []string{
		fmt.Sprintf(`{"name":"validate_memory","arguments":{"project":%q,"memory":%s}}`, t.TempDir(), good),
		fmt.Sprintf(`{"name":"validate_memory","arguments":{"project":%q,"memory":%s}}`, t.TempDir(), bad),
	}
	responses := runMCPMemoryRequests(t, st, payloads...)
	first := structuredResult(responses[0])
	if valid, _ := first["valid"].(bool); !valid {
		t.Fatalf("valid envelope rejected: %+v", first)
	}
	second := structuredResult(responses[1])
	if valid, _ := second["valid"].(bool); valid {
		t.Fatal("invalid envelope accepted")
	}
	diags, _ := second["diagnostics"].([]any)
	found := false
	for _, d := range diags {
		if m, ok := d.(map[string]any); ok && m["code"] == store.MemoryCodeFieldUnknown {
			found = true
		}
	}
	if !found {
		t.Fatalf("MEMORY_FIELD_UNKNOWN missing: %+v", second["diagnostics"])
	}
}

func TestMCPMemoryUnknownField(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "mcp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	envelope := memoryMCPEnvelope("proj", "Unknown", "Unknown summary")
	payload := fmt.Sprintf(`{"name":"save_memory","arguments":{"project":"proj","surprise":true,"memory":%s}}`, envelope)
	resp := runMCPMemoryRequests(t, st, payload)[0]
	if resp.Error == nil || resp.Error.Code != -32602 {
		t.Fatalf("unknown arg response = %+v", resp)
	}
}

func TestMCPMemoryGetAndList(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "mcp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	envelope := memoryMCPEnvelope("proj", "Get list", "Get list summary")
	save := fmt.Sprintf(`{"name":"save_memory","arguments":{"project":"proj","memory":%s}}`, envelope)
	resp := runMCPMemoryRequests(t, st, save)[0]
	result := structuredResult(resp)
	memory, _ := result["memory"].(map[string]any)
	id, _ := memory["id"].(string)
	responses := runMCPMemoryRequests(t, st,
		fmt.Sprintf(`{"name":"get_memory","arguments":{"id":%q}}`, id),
		`{"name":"list_memories","arguments":{"project":"proj","include_superseded":true}}`,
	)
	got := structuredResult(responses[0])
	if got["id"] != id {
		t.Fatalf("get_memory = %+v", got)
	}
	list := structuredResult(responses[1])
	summary, _ := list["summary"].(map[string]any)
	if total, _ := summary["total"].(float64); total != 1 {
		t.Fatalf("list summary = %+v", summary)
	}
}

func TestMCPMemoryToolInventory(t *testing.T) {
	if PublicToolCount != len(ToolDefinitions()) {
		t.Fatalf("PublicToolCount = %d, registry = %d", PublicToolCount, len(ToolDefinitions()))
	}
	if len(ContinuityToolNames) != ContinuityToolCount {
		t.Fatalf("continuity names = %d, count = %d", len(ContinuityToolNames), ContinuityToolCount)
	}
	// The curated ten omit the raw memory envelope tools; they stay in the full
	// profile and must remain defined.
	for _, name := range []string{"save_memory", "get_memory"} {
		if !IsCoreTool(name) {
			t.Errorf("%s missing from the full tool registry", name)
		}
	}
	for _, name := range []string{"save_memory", "get_memory", "list_memories", "validate_memory"} {
		found := false
		for _, tool := range ToolDefinitions() {
			if tool.Name == name {
				found = true
				if tool.Category == "" {
					t.Errorf("%s has no category", name)
				}
			}
		}
		if !found {
			t.Errorf("%s missing from registry", name)
		}
	}
}
