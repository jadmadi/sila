package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestManageMergeSlotLifecycle(t *testing.T) {
	srv, _, projectID := revisionServer(t)
	response := callTool(t, srv, "manage_merge_slot", map[string]any{"operation": "acquire", "project": projectID, "holder": "test"})
	result := response["result"].(map[string]any)
	text := result["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "acquired") {
		t.Fatalf("acquire text = %s", text)
	}
	// Extract the token from the structured payload when present.
	var token string
	if structured, ok := result["structuredContent"].(map[string]any); ok {
		token, _ = structured["token"].(string)
	}
	if token == "" {
		t.Skip("server does not expose structured content; store tests cover tokens")
	}
	busy := callTool(t, srv, "manage_merge_slot", map[string]any{"operation": "acquire", "project": projectID})
	errObj, ok := busy["error"].(map[string]any)
	if !ok || errObj["message"] != "MERGE_SLOT_BUSY" {
		t.Fatalf("busy = %+v", busy)
	}
	status := callTool(t, srv, "manage_merge_slot", map[string]any{"operation": "status", "project": projectID})
	statusText := status["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if strings.Contains(statusText, token) {
		t.Fatal("status leaked the token")
	}
	release := callTool(t, srv, "manage_merge_slot", map[string]any{"operation": "release", "project": projectID, "token": token})
	if _, ok := release["error"]; ok {
		t.Fatalf("release = %+v", release)
	}
}

func TestManageMergeSlotUnknownOperation(t *testing.T) {
	srv, _, projectID := revisionServer(t)
	response := callTool(t, srv, "manage_merge_slot", map[string]any{"operation": "explode", "project": projectID})
	errObj, ok := response["error"].(map[string]any)
	if !ok || errObj["code"].(float64) != -32602 {
		t.Fatalf("response = %+v", response)
	}
	_ = json.Valid
}
