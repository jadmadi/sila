package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/mcp"
)

func TestRunMCPSchemaSingleTool(t *testing.T) {
	var code int
	out := captureStdout(t, func() {
		code = runMCPSchema("recall", true)
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	var payload struct {
		Schema       string     `json:"schema"`
		SchemaDigest string     `json:"schema_digest"`
		Tools        []mcp.Tool `json:"tools"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if payload.Schema != "sila/mcp-schema/v1" || !strings.HasPrefix(payload.SchemaDigest, "sha256:") {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	if len(payload.Tools) != 1 || payload.Tools[0].Name != "recall" {
		t.Fatalf("unexpected tools: %+v", payload.Tools)
	}
}

func TestRunMCPSchemaUnknownTool(t *testing.T) {
	var code int
	_ = captureStdout(t, func() {
		code = runMCPSchema("not_a_tool", true)
	})
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}

func TestRunMCPSchemaAllTools(t *testing.T) {
	var code int
	out := captureStdout(t, func() {
		code = runMCPSchema("", true)
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	var payload struct {
		Tools []mcp.Tool `json:"tools"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(payload.Tools) != len(mcp.ToolDefinitions()) {
		t.Fatalf("got %d tools, want %d", len(payload.Tools), len(mcp.ToolDefinitions()))
	}
}
