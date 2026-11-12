package mcp

import (
	"strings"
	"testing"
)

func TestCompactSchemaFields(t *testing.T) {
	found := map[string]bool{}
	for _, tool := range ToolDefinitions() {
		if tool.Name != "recall" && tool.Name != "omni_search" {
			continue
		}
		prop, ok := tool.InputSchema.Properties["compact"]
		if !ok || prop.Type != "boolean" {
			t.Fatalf("%s compact = %+v", tool.Name, prop)
		}
		if !strings.Contains(prop.Description, "default false") {
			t.Fatalf("%s compact description = %q", tool.Name, prop.Description)
		}
		found[tool.Name] = true
	}
	if len(found) != 2 {
		t.Fatalf("missing tools: %v", found)
	}
}

func TestCompactArgumentValidation(t *testing.T) {
	srv, _, _ := revisionServer(t)
	response := callTool(t, srv, "recall", map[string]any{"query": "x", "compact": "yes"})
	errObj, ok := response["error"].(map[string]any)
	if !ok || errObj["code"].(float64) != -32602 {
		t.Fatalf("response = %+v", response)
	}
	dataObj, _ := errObj["data"].(map[string]any)
	if field, _ := dataObj["field"].(string); field != "compact" {
		t.Fatalf("error = %+v", errObj)
	}
}

func TestCompactEnabled(t *testing.T) {
	enabled, disabled := true, false
	if !compactEnabled(&enabled) || compactEnabled(&disabled) {
		t.Fatal("explicit compact values ignored")
	}
	if compactEnabled(nil) {
		t.Fatal("compact defaulted to true without config")
	}
}

func TestRecallExpansionMutualExclusion(t *testing.T) {
	srv, _, _ := revisionServer(t)
	response := callTool(t, srv, "recall", map[string]any{"ids": []string{"a"}, "anchor": "b"})
	errObj, ok := response["error"].(map[string]any)
	if !ok || errObj["code"].(float64) != -32602 {
		t.Fatalf("response = %+v", response)
	}
	if data, _ := errObj["data"].(string); !strings.Contains(data, "mutually exclusive") {
		if dataObj, _ := errObj["data"].(map[string]any); dataObj == nil {
			t.Fatalf("error = %+v", errObj)
		}
	}
}
