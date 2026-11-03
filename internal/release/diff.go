package release

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/jadmadi/sila/internal/mcp"
)

// BreakingChanges compares a prior contract to the current registry. It flags
// removed tools, removed fields, changed field types, and newly required fields.
func BreakingChanges(previous, current []mcp.Tool) []string {
	cur := make(map[string]mcp.Tool, len(current))
	for _, tool := range current {
		cur[tool.Name] = tool
	}
	var changes []string
	for _, old := range previous {
		newTool, ok := cur[old.Name]
		if !ok {
			changes = append(changes, "removed MCP tool "+old.Name)
			continue
		}
		oldRequired, newRequired := requiredSet(old), requiredSet(newTool)
		for name, oldProp := range old.InputSchema.Properties {
			newProp, ok := newTool.InputSchema.Properties[name]
			if !ok {
				changes = append(changes, fmt.Sprintf("removed field %s.%s", old.Name, name))
				continue
			}
			if oldProp.Type != newProp.Type {
				changes = append(changes, fmt.Sprintf("changed field type %s.%s: %s to %s", old.Name, name, oldProp.Type, newProp.Type))
			}
		}
		for name := range newRequired {
			if !oldRequired[name] {
				changes = append(changes, fmt.Sprintf("made field required %s.%s", old.Name, name))
			}
		}
	}
	return changes
}

func requiredSet(tool mcp.Tool) map[string]bool {
	set := make(map[string]bool, len(tool.InputSchema.Required))
	for _, name := range tool.InputSchema.Required {
		set[name] = true
	}
	return set
}

// ReadContract loads and decodes an MCP tool contract from the given JSON file.
func ReadContract(path string) ([]mcp.Tool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var tools []mcp.Tool
	if err := json.Unmarshal(b, &tools); err != nil {
		return nil, fmt.Errorf("decode MCP contract: %w", err)
	}
	return tools, nil
}
