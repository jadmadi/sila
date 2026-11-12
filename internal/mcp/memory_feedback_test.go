package mcp

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func openFeedbackMCPStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "mcp-feedback.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// mcpRecallPack runs recall and returns the recorded retrieval id plus the
// typed memory ids it returned.
func mcpRecallPack(t *testing.T, st *store.Store, query string) (string, []string) {
	t.Helper()
	payload := fmt.Sprintf(`{"name":"recall","arguments":{"query":%q,"project":"proj","limit":20}}`, query)
	resp := runMCPMemoryRequests(t, st, payload)[0]
	if resp.Error != nil {
		t.Fatalf("recall error: %+v", resp.Error)
	}
	data := structuredResult(resp)
	pack, _ := data["pack"].(map[string]any)
	if pack == nil {
		t.Fatalf("recall returned no pack: %+v", data)
	}
	retrievalID, _ := pack["retrieval_id"].(string)
	if retrievalID == "" {
		t.Fatalf("recall returned no retrieval id: %+v", pack)
	}
	var memoryIDs []string
	items, _ := pack["items"].([]any)
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		if memoryID, _ := item["memory_id"].(string); memoryID != "" {
			memoryIDs = append(memoryIDs, memoryID)
		}
	}
	return retrievalID, memoryIDs
}

func TestMCPMemoryFeedback(t *testing.T) {
	st := openFeedbackMCPStore(t)
	memoryID := mcpSaveMemory(t, st, "MCP feedback needle", "MCP feedback needle summary")
	retrievalID, returned := mcpRecallPack(t, st, "MCP feedback needle")
	if len(returned) == 0 || returned[0] != memoryID {
		t.Fatalf("recall returned %v, want %s", returned, memoryID)
	}

	payload := fmt.Sprintf(`{"name":"memory_feedback","arguments":{"memory_id":%q,"retrieval_id":%q,"signal":"helped","project":"proj"}}`, memoryID, retrievalID)
	first := structuredResult(runMCPMemoryRequests(t, st, payload)[0])
	if created, _ := first["created"].(bool); !created {
		t.Fatalf("first feedback not created: %+v", first)
	}
	feedback, _ := first["feedback"].(map[string]any)
	if feedback["signal"] != "helped" || feedback["memory_id"] != memoryID {
		t.Fatalf("feedback payload: %+v", feedback)
	}
	if weight, _ := feedback["weight"].(float64); weight != 40 {
		t.Fatalf("feedback weight = %v, want 40", weight)
	}
	adjustment, _ := first["adjustment"].(map[string]any)
	if value, _ := adjustment["adjustment"].(float64); value != 40 {
		t.Fatalf("adjustment = %+v", adjustment)
	}

	replay := structuredResult(runMCPMemoryRequests(t, st, payload)[0])
	if created, _ := replay["created"].(bool); created {
		t.Fatalf("replay created a row: %+v", replay)
	}

	list := structuredResult(runMCPMemoryRequests(t, st, `{"name":"list_memory_feedback","arguments":{"project":"proj"}}`)[0])
	if total, _ := list["total"].(float64); total != 1 {
		t.Fatalf("list total = %v, want 1: %+v", total, list)
	}
	if accepted, _ := list["accepted"].(float64); accepted != 1 {
		t.Fatalf("list accepted = %v: %+v", accepted, list)
	}
}

func TestMCPMemoryFeedbackNotReturned(t *testing.T) {
	st := openFeedbackMCPStore(t)
	returned := mcpSaveMemory(t, st, "Returned needle", "Returned needle summary")
	hidden := mcpSaveMemory(t, st, "Hidden vane", "Hidden vane summary")
	retrievalID, ids := mcpRecallPack(t, st, "Returned needle")
	if len(ids) == 0 || ids[0] != returned {
		t.Fatalf("recall returned %v, want %s", ids, returned)
	}

	payload := fmt.Sprintf(`{"name":"memory_feedback","arguments":{"memory_id":%q,"retrieval_id":%q,"signal":"irrelevant","project":"proj"}}`, hidden, retrievalID)
	resp := runMCPMemoryRequests(t, st, payload)[0]
	if code := mcpErrorCode(resp); code != store.MemoryCodeFeedbackNotReturned {
		t.Fatalf("not-returned code = %q, want %q: %+v", code, store.MemoryCodeFeedbackNotReturned, resp)
	}

	unknown := fmt.Sprintf(`{"name":"memory_feedback","arguments":{"memory_id":%q,"retrieval_id":"ret_missing","signal":"helped","project":"proj"}}`, returned)
	resp = runMCPMemoryRequests(t, st, unknown)[0]
	if code := mcpErrorCode(resp); code != store.MemoryCodeFeedbackNotReturned {
		t.Fatalf("unknown retrieval code = %q: %+v", code, resp)
	}

	invalid := fmt.Sprintf(`{"name":"memory_feedback","arguments":{"memory_id":%q,"retrieval_id":%q,"signal":"useful","project":"proj"}}`, returned, retrievalID)
	resp = runMCPMemoryRequests(t, st, invalid)[0]
	if code := mcpErrorCode(resp); code != store.MemoryCodeFeedbackSignal {
		t.Fatalf("invalid signal code = %q: %+v", code, resp)
	}

	strict := fmt.Sprintf(`{"name":"memory_feedback","arguments":{"memory_id":%q,"retrieval_id":%q,"signal":"helped","project":"proj","extra":true}}`, returned, retrievalID)
	resp = runMCPMemoryRequests(t, st, strict)[0]
	if resp.Error == nil || resp.Error.Code != -32602 {
		t.Fatalf("unknown field was accepted: %+v", resp)
	}
}

func TestMCPMemoryFeedbackWrongRecommendsArchive(t *testing.T) {
	st := openFeedbackMCPStore(t)
	memoryID := mcpSaveMemory(t, st, "Wrong needle", "Wrong needle summary")
	retrievalID, _ := mcpRecallPack(t, st, "Wrong needle")

	payload := fmt.Sprintf(`{"name":"memory_feedback","arguments":{"memory_id":%q,"retrieval_id":%q,"signal":"wrong","project":"proj","note":"contradicted by the current tests"}}`, memoryID, retrievalID)
	out := structuredResult(runMCPMemoryRequests(t, st, payload)[0])
	if action, _ := out["recommended_action"].(string); action != "archive" {
		t.Fatalf("wrong did not recommend archive: %+v", out)
	}
	feedback, _ := out["feedback"].(map[string]any)
	if feedback["signal"] != "wrong" {
		t.Fatalf("wrong feedback payload: %+v", feedback)
	}

	memory := structuredResult(runMCPMemoryRequests(t, st, fmt.Sprintf(`{"name":"get_memory","arguments":{"id":%q,"project":"proj"}}`, memoryID))[0])
	if state, _ := memory["state"].(string); state != "current" {
		t.Fatalf("wrong feedback changed memory state to %q", state)
	}
}
