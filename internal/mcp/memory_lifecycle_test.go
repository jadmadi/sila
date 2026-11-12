package mcp

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func mcpToolData(resp JSONRPCResponse) any {
	result, _ := resp.Result.(map[string]any)
	return result["structuredData"]
}

func mcpSaveMemory(t *testing.T, st *store.Store, title, summary string) string {
	t.Helper()
	envelope := memoryMCPEnvelope("proj", title, summary)
	payload := fmt.Sprintf(`{"name":"save_memory","arguments":{"project":"proj","memory":%s}}`, envelope)
	resp := runMCPMemoryRequests(t, st, payload)[0]
	if resp.Error != nil {
		t.Fatalf("save_memory error: %+v", resp.Error)
	}
	data, _ := mcpToolData(resp).(map[string]any)
	memory, _ := data["memory"].(map[string]any)
	id, _ := memory["id"].(string)
	if id == "" {
		t.Fatalf("save_memory returned no id: %+v", data)
	}
	return id
}

func mcpErrorCode(resp JSONRPCResponse) string {
	if resp.Error == nil {
		return ""
	}
	data, _ := resp.Error.Data.(map[string]any)
	code, _ := data["code"].(string)
	return code
}

func TestMCPMemoryArchive(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "mcp-lifecycle.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	id := mcpSaveMemory(t, st, "MCP archive needle", "MCP archive needle summary")
	payload := fmt.Sprintf(`{"name":"memory_archive","arguments":{"id":%q,"authority":"user_request","reason":"outdated","note":"old fact"}}`, id)
	resp := runMCPMemoryRequests(t, st, payload)[0]
	if resp.Error != nil {
		t.Fatalf("memory_archive error: %+v", resp.Error)
	}
	data, _ := mcpToolData(resp).(map[string]any)
	if data["state"] != "archived" || data["reason"] != "outdated" {
		t.Fatalf("archive result: %+v", data)
	}
	if archiveID, _ := data["archive_id"].(string); archiveID == "" {
		t.Fatalf("archive id missing: %+v", data)
	}

	responses := runMCPMemoryRequests(t, st,
		`{"name":"search_knowledge","arguments":{"query":"archive needle","project":"proj"}}`,
		`{"name":"search_knowledge","arguments":{"query":"archive needle","project":"proj","include_archived":true}}`,
	)
	excluded, _ := mcpToolData(responses[0]).([]any)
	if len(excluded) != 0 {
		t.Fatalf("archived memory returned by search_knowledge: %+v", excluded)
	}
	included, _ := mcpToolData(responses[1]).([]any)
	if len(included) != 1 {
		t.Fatalf("include_archived search returned %d facts: %+v", len(included), included)
	}

	list := structuredResult(runMCPMemoryRequests(t, st, `{"name":"list_archived_memories","arguments":{"project":"proj"}}`)[0])
	summary, _ := list["summary"].(map[string]any)
	if archived, _ := summary["archived"].(float64); archived != 1 {
		t.Fatalf("archive list summary: %+v", summary)
	}
}

func TestMCPMemoryRestore(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "mcp-restore.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	id := mcpSaveMemory(t, st, "MCP restore needle", "MCP restore needle summary")
	archive := fmt.Sprintf(`{"name":"memory_archive","arguments":{"id":%q,"authority":"user_request","reason":"outdated"}}`, id)
	archived := structuredResult(runMCPMemoryRequests(t, st, archive)[0])
	archiveID, _ := archived["archive_id"].(string)
	if archiveID == "" {
		t.Fatalf("archive id missing: %+v", archived)
	}
	restore := fmt.Sprintf(`{"name":"memory_restore","arguments":{"id":%q,"authority":"user_request"}}`, archiveID)
	first := structuredResult(runMCPMemoryRequests(t, st, restore)[0])
	if restored, _ := first["restored"].(bool); !restored {
		t.Fatalf("restore by archive id: %+v", first)
	}
	second := structuredResult(runMCPMemoryRequests(t, st, restore)[0])
	if restored, _ := second["restored"].(bool); restored {
		t.Fatalf("second restore was not idempotent: %+v", second)
	}
}

func TestMCPMemoryHistory(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "mcp-history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	id := mcpSaveMemory(t, st, "MCP history needle", "MCP history needle summary")
	archive := fmt.Sprintf(`{"name":"memory_archive","arguments":{"id":%q,"authority":"agent_verified","reason":"wrong"}}`, id)
	runMCPMemoryRequests(t, st, archive)
	historyData := structuredResult(runMCPMemoryRequests(t, st, fmt.Sprintf(`{"name":"memory_history","arguments":{"id":%q,"verify":true}}`, id))[0])
	if verified, _ := historyData["verified"].(bool); !verified {
		t.Fatalf("history not verified: %+v", historyData)
	}
	events, _ := historyData["events"].([]any)
	if len(events) != 1 {
		t.Fatalf("history events = %d: %+v", len(events), events)
	}
	if _, err := st.DB.Exec(`UPDATE memory_lifecycle SET note='tampered' WHERE memory_id=?`, id); err != nil {
		t.Fatal(err)
	}
	tampered := runMCPMemoryRequests(t, st, fmt.Sprintf(`{"name":"memory_history","arguments":{"id":%q,"verify":true}}`, id))[0]
	if code := mcpErrorCode(tampered); code != store.MemoryCodeChainBroken {
		t.Fatalf("tampered history code = %q resp=%+v", code, tampered)
	}
}

func TestMCPArchiveSensitiveRedaction(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "mcp-sensitive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	id := mcpSaveMemory(t, st, "Sensitive title needle", "Sensitive summary needle")
	archive := fmt.Sprintf(`{"name":"memory_archive","arguments":{"id":%q,"authority":"sensitive","reason":"sensitive","note":"secret note"}}`, id)
	resp := runMCPMemoryRequests(t, st, archive)[0]
	if resp.Error != nil {
		t.Fatalf("sensitive archive error: %+v", resp.Error)
	}
	data, _ := mcpToolData(resp).(map[string]any)
	if data["title"] != "[redacted]" {
		t.Fatalf("sensitive archive echoed title: %+v", data)
	}
	if note, ok := data["note"]; ok && note != "" {
		t.Fatalf("sensitive archive echoed note: %+v", data)
	}
	list := structuredResult(runMCPMemoryRequests(t, st, `{"name":"list_archived_memories","arguments":{"project":"proj"}}`)[0])
	records, _ := list["records"].([]any)
	if len(records) != 1 {
		t.Fatalf("sensitive list records: %+v", records)
	}
	record, _ := records[0].(map[string]any)
	if record["title"] != "[redacted]" {
		t.Fatalf("sensitive list echoed title: %+v", record)
	}
	if note, ok := record["note"]; ok && note != "" {
		t.Fatalf("sensitive list echoed note: %+v", record)
	}
	historyData := structuredResult(runMCPMemoryRequests(t, st, fmt.Sprintf(`{"name":"memory_history","arguments":{"id":%q}}`, id))[0])
	events, _ := historyData["events"].([]any)
	if len(events) != 1 {
		t.Fatalf("sensitive history events: %+v", events)
	}
	event, _ := events[0].(map[string]any)
	if note, ok := event["note"]; ok && note != "" {
		t.Fatalf("sensitive history echoed note: %+v", event)
	}
	restore := fmt.Sprintf(`{"name":"memory_restore","arguments":{"id":%q}}`, id)
	refused := runMCPMemoryRequests(t, st, restore)[0]
	if code := mcpErrorCode(refused); code != store.MemoryCodeSensitiveRestore {
		t.Fatalf("agent sensitive restore code = %q resp=%+v", code, refused)
	}
	claimedUser := fmt.Sprintf(`{"name":"memory_restore","arguments":{"id":%q,"authority":"user_request"}}`, id)
	refused = runMCPMemoryRequests(t, st, claimedUser)[0]
	if code := mcpErrorCode(refused); code != store.MemoryCodeSensitiveRestore {
		t.Fatalf("MCP user-authority sensitive restore code = %q resp=%+v", code, refused)
	}
}

func TestMCPMemoryArchiveStrictArgs(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "mcp-strict.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	id := mcpSaveMemory(t, st, "Strict args", "Strict args summary")
	payload := fmt.Sprintf(`{"name":"memory_archive","arguments":{"id":%q,"authority":"user_request","reason":"wrong","surprise":true}}`, id)
	resp := runMCPMemoryRequests(t, st, payload)[0]
	if resp.Error == nil || resp.Error.Code != -32602 {
		t.Fatalf("unknown arg response = %+v", resp)
	}
	if _, err := json.Marshal(resp); err != nil {
		t.Fatalf("marshal response: %v", err)
	}
}
