package mcp

import (
	"encoding/json"
	"testing"
)

const continuitySchemaBudget = 12288

func TestContinuityProfileOrderAndBudget(t *testing.T) {
	tools := FilterToolsByProfile(ToolDefinitions(), ProfileContinuity)
	if len(tools) != ContinuityToolCount {
		t.Fatalf("continuity tools = %d", len(tools))
	}
	for i, name := range ContinuityToolOrder {
		if tools[i].Name != name {
			t.Fatalf("order[%d] = %q, want %q", i, tools[i].Name, name)
		}
	}
	encoded, err := json.Marshal(map[string]any{"tools": tools})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("continuity tools/list payload = %d bytes (budget %d)", len(encoded), continuitySchemaBudget)
	if len(encoded) > continuitySchemaBudget {
		t.Fatalf("continuity schema is %d bytes, budget %d", len(encoded), continuitySchemaBudget)
	}
}

func TestContinuityMissingDefinitionIsAnError(t *testing.T) {
	defs := ToolDefinitions()
	var filtered []Tool
	for _, def := range defs {
		if def.Name == "get_status" {
			continue
		}
		filtered = append(filtered, def)
	}
	if _, err := ContinuityTools(filtered); err == nil {
		t.Fatal("missing definition did not error")
	}
}

func TestFullProfileKeepsEveryTool(t *testing.T) {
	if got := len(FilterToolsByProfile(ToolDefinitions(), ProfileFull)); got != PublicToolCount {
		t.Fatalf("full profile = %d, want %d", got, PublicToolCount)
	}
}

func TestGraphProfileExposesGraphTools(t *testing.T) {
	tools := FilterToolsByProfile(ToolDefinitions(), ProfileGraph)
	if len(tools) != len(GraphToolOrder) {
		t.Fatalf("graph tools = %d, want %d", len(tools), len(GraphToolOrder))
	}
	for i, name := range GraphToolOrder {
		if tools[i].Name != name {
			t.Fatalf("graph order[%d] = %q, want %q", i, tools[i].Name, name)
		}
	}
	for _, name := range []string{"query_graph", "shortest_path", "graph_report"} {
		found := false
		for _, tool := range tools {
			if tool.Name == name {
				found = true
			}
		}
		if !found {
			t.Fatalf("graph profile is missing %q", name)
		}
	}
}
