package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// SchemaDigest returns a stable sha256: fingerprint of the canonical public
// MCP tool registry. Tools are sorted by name and each Required slice is
// sorted, so map iteration and definition order cannot change the digest.
func SchemaDigest() string {
	tools := ToolDefinitions()
	canonical := make([]Tool, len(tools))
	for i := range tools {
		canonical[i] = tools[i]
		canonical[i].InputSchema.Required = append([]string(nil), tools[i].InputSchema.Required...)
		sort.Strings(canonical[i].InputSchema.Required)
	}
	sort.Slice(canonical, func(i, j int) bool { return canonical[i].Name < canonical[j].Name })
	payload, err := json.MarshalIndent(canonical, "", "  ")
	if err != nil {
		return "sha256:unavailable"
	}
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}
