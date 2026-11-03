package release

import (
	"testing"

	"github.com/jadmadi/sila/internal/mcp"
)

func TestContractIsStableAndComplete(t *testing.T) {
	tools := Contract()
	if len(tools) != mcp.PublicToolCount {
		t.Fatalf("contract has %d tools, want %d", len(tools), mcp.PublicToolCount)
	}
	if _, err := ContractDigest(); err != nil {
		t.Fatal(err)
	}
}

func TestBreakingChanges(t *testing.T) {
	old := []mcp.Tool{{Name: "save", InputSchema: mcp.InputSchema{Properties: map[string]mcp.PropertySchema{"name": {Type: "string"}}}}}
	if got := BreakingChanges(old, []mcp.Tool{{Name: "save", InputSchema: mcp.InputSchema{Properties: map[string]mcp.PropertySchema{"name": {Type: "string"}}}}}); len(got) != 0 {
		t.Fatalf("unexpected changes: %v", got)
	}
	got := BreakingChanges(old, []mcp.Tool{{Name: "save", InputSchema: mcp.InputSchema{Properties: map[string]mcp.PropertySchema{"name": {Type: "integer"}}, Required: []string{"name"}}}})
	if len(got) != 2 {
		t.Fatalf("expected type + required breaks, got %v", got)
	}
}

func TestValidateMetadata(t *testing.T) {
	m := Metadata{Version: "v0.5.0", MCPContractVersion: MCPContractVersion, MCPContractSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SchemaVersion: 1, MinimumSchemaVersion: 1}
	if err := ValidateMetadata(m, "v0.5.0"); err != nil {
		t.Fatal(err)
	}
	m.Version = "dev+abc"
	if err := ValidateMetadata(m, ""); err == nil {
		t.Fatal("development metadata must fail")
	}
}
