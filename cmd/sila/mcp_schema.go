package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/jadmadi/sila/internal/mcp"
)

// runMCPSchema prints the canonical MCP tool registry, optionally limited to
// one tool. It does not open the store or start a server.
func runMCPSchema(toolName, profile string, jsonOut bool) int {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		profile = mcp.ProfileContinuity
	}
	tools := mcp.FilterToolsByProfile(mcp.ToolDefinitions(), profile)
	if !strings.EqualFold(profile, mcp.ProfileContinuity) || toolName != "" {
		sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	}
	if toolName != "" {
		filtered := tools[:0]
		for _, tool := range tools {
			if tool.Name == toolName {
				filtered = append(filtered, tool)
			}
		}
		if len(filtered) == 0 {
			fmt.Fprintf(os.Stderr, "sila mcp: unknown tool %q\n", toolName)
			return 2
		}
		tools = filtered
	}

	payload := struct {
		Schema       string     `json:"schema"`
		SchemaDigest string     `json:"schema_digest"`
		Tools        []mcp.Tool `json:"tools"`
	}{
		Schema:       "sila/mcp-schema/v1",
		SchemaDigest: mcp.SchemaDigest(),
		Tools:        tools,
	}
	if jsonOut {
		emitJSON(payload)
		return 0
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila mcp: %v\n", err)
		return 1
	}
	fmt.Fprintln(os.Stdout, string(data))
	return 0
}
