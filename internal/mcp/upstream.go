// Package mcp upstream module boundaries and architectural ownership.
//
// Layered Architecture & Module Responsibilities:
//
// 1. Egress Policy & Privacy (upstream_privacy.go):
//   - Enforces strict data sovereignty and leak prevention across all egress paths.
//   - Redacts private tokens (GitHub, GitLab, Slack, Bearer, API keys), PEM certificates, and local home directories.
//   - Validates stdio argument vectors against shell injection (-c, -e, --eval) and inline secrets.
//   - Sanitizes tool invocation arguments; rejects sensitive code execution unless explicitly permitted.
//
// 2. Transports & Protocols (upstream_stdio.go, upstream_http.go):
//   - StdioUpstreamClient: Subprocess management, piped JSON-RPC 2.0 I/O, process reaping, and timeout handling.
//   - HTTPUpstreamClient: Remote HTTP POST JSON-RPC 2.0 with Mcp-Session-Id negotiation, SSE event streaming, and session reinitialization.
//   - Both implement the canonical UpstreamClient interface.
//
// 3. Registry & Management (upstream_registry.go):
//   - UpstreamManager: Central catalog, client caching, collision detection, and ambiguous prefix isolation.
//   - Dynamic tool registration, discovery, health status reporting, and session toggling.
//
// 4. Gateway Dispatch & Routing (upstream_gateway.go):
//   - CallTool: Dispatches tool calls to registered upstream clients.
//   - CallGateway: Maps compact unified gateway actions (alphaxiv, stitch) to underlying upstream tools.
//
// 5. Reference Caching & Auto-Ingest (upstream_cache.go):
//   - Intercepts reference documentation outputs (drizzle, cloudflare, alphaxiv) and persists them into local SQLite FTS5.
//   - Enforces bounded content limits (512KB), project scoping, and error/auth response rejection.
//
// 6. Tool Catalogues (upstream_catalog.go):
//   - Default static tool schemas for UI Craft, Drizzle ORM, Cloudflare, gopls, AlphaXiv, and Google Stitch.
//
// Dispatch Policy Invariant:
// Callers must NEVER invoke transport clients directly. All tool executions must route through
// UpstreamManager.CallTool or UpstreamManager.CallGateway, ensuring that global and per-server
// enablement checks, collision guards, argument sanitization, secret redaction, and auto-ingest
// caching are unconditionally enforced across all transport paths.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// UpstreamClient represents an active connection to an upstream Model Context Protocol server.
type UpstreamClient interface {
	Name() string
	Prefix() string
	CallTool(ctx context.Context, name string, args map[string]any) (any, error)
	Close() error
	IsRunning() bool
}

// parseDiscoveredTools parses the result of a tools/list JSON-RPC call into a slice of Tool definitions.
func parseDiscoveredTools(result any) ([]Tool, error) {
	if result == nil {
		return nil, errors.New("empty tools/list result")
	}
	resMap, ok := result.(map[string]any)
	if !ok {
		return nil, errors.New("tools/list result is not a JSON object")
	}
	rawTools, ok := resMap["tools"].([]any)
	if !ok {
		return nil, errors.New("tools/list result missing 'tools' array")
	}
	data, err := json.Marshal(rawTools)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal raw tools: %w", err)
	}
	var tools []Tool
	if err := json.Unmarshal(data, &tools); err != nil {
		return nil, fmt.Errorf("failed to unmarshal discovered tools: %w", err)
	}
	return tools, nil
}
