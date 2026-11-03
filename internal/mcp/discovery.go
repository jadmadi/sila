package mcp

import (
	"fmt"
	"strings"
)

// ServerCard implements the SEP-1649 MCP Server Card specification.
type ServerCard struct {
	Schema         string                 `json:"$schema,omitempty"`
	Name           string                 `json:"name"`
	Description    string                 `json:"description"`
	Version        string                 `json:"version"`
	Endpoints      map[string]string      `json:"endpoints,omitempty"`
	Capabilities   ServerCardCapabilities `json:"capabilities"`
	Authentication *ServerCardAuth        `json:"authentication,omitempty"`
	Links          map[string]string      `json:"links,omitempty"`
}

// ServerCardCapabilities describes supported MCP protocol capabilities.
type ServerCardCapabilities struct {
	Tools     bool `json:"tools"`
	Resources bool `json:"resources"`
	Prompts   bool `json:"prompts"`
	Logging   bool `json:"logging,omitempty"`
}

// ServerCardAuth describes authentication requirements for the server.
type ServerCardAuth struct {
	Type   string   `json:"type"`
	Scopes []string `json:"scopes,omitempty"`
}

// LinksetTarget represents a single target in an RFC 9727 linkset entry.
type LinksetTarget struct {
	Href string `json:"href"`
	Type string `json:"type,omitempty"`
}

// LinksetEntry defines anchor-scoped relations according to RFC 9727.
type LinksetEntry struct {
	Anchor        string          `json:"anchor"`
	ServiceDesc   []LinksetTarget `json:"service-desc,omitempty"`
	ServiceDoc    []LinksetTarget `json:"service-doc,omitempty"`
	MCPServerCard []LinksetTarget `json:"mcp-server-card,omitempty"`
}

// LinksetDocument represents the root RFC 9727 JSON linkset document.
type LinksetDocument struct {
	Linkset []LinksetEntry `json:"linkset"`
}

// DefaultServerCard returns standard SEP-1649 metadata for Sila.
func DefaultServerCard(ver string) ServerCard {
	if ver == "" {
		ver = "1.0.0"
	}
	return ServerCard{
		Schema:      "https://json-schema.org/draft/2020-12/schema",
		Name:        "sila",
		Description: "Sila Model Context Protocol (MCP) Server and Continuity Engine",
		Version:     ver,
		Endpoints: map[string]string{
			"mcp":  "/mcp",
			"rest": "/api",
		},
		Capabilities: ServerCardCapabilities{
			Tools:     true,
			Resources: true,
			Prompts:   true,
			Logging:   true,
		},
		Authentication: &ServerCardAuth{
			Type: "none",
		},
		Links: map[string]string{
			"repository":    "https://github.com/jadmadi/sila",
			"documentation": "https://github.com/jadmadi/sila",
			"llms":          "/llms.txt",
			"linkset":       "/.well-known/linkset.json",
		},
	}
}

// DefaultLinkset constructs an RFC 9727 linkset document for Sila.
func DefaultLinkset(baseURL string) LinksetDocument {
	prefix := strings.TrimRight(baseURL, "/")
	anchor := "/"
	if prefix != "" {
		anchor = prefix + "/"
	}

	return LinksetDocument{
		Linkset: []LinksetEntry{
			{
				Anchor: anchor,
				ServiceDesc: []LinksetTarget{
					{
						Href: prefix + "/openapi.json",
						Type: "application/json",
					},
				},
				ServiceDoc: []LinksetTarget{
					{
						Href: prefix + "/llms.txt",
						Type: "text/markdown",
					},
				},
				MCPServerCard: []LinksetTarget{
					{
						Href: prefix + "/.well-known/mcp/server-card.json",
						Type: "application/json",
					},
				},
			},
		},
	}
}

// DefaultLinkHeaders generates RFC 8288 Link header relations for discovery.
func DefaultLinkHeaders(baseURL string) string {
	prefix := strings.TrimRight(baseURL, "/")
	links := []string{
		fmt.Sprintf("<%s/.well-known/mcp/server-card.json>; rel=\"mcp-server-card\"; type=\"application/json\"", prefix),
		fmt.Sprintf("<%s/.well-known/linkset.json>; rel=\"linkset\"; type=\"application/linkset+json\"", prefix),
		fmt.Sprintf("<%s/llms.txt>; rel=\"service-doc\"; type=\"text/markdown\"", prefix),
	}
	return strings.Join(links, ", ")
}

// DefaultLLMsTxt generates standard llms.txt discovery markdown.
func DefaultLLMsTxt(baseURL, ver string) string {
	if ver == "" {
		ver = "1.0.0"
	}
	prefix := strings.TrimRight(baseURL, "/")
	var b strings.Builder
	b.WriteString("# Sila Continuity Engine\n\n")
	b.WriteString("> Cross-harness memory, goal attribution, and Model Context Protocol (MCP) server for autonomous AI agents.\n\n")
	b.WriteString("## System Metadata\n")
	b.WriteString(fmt.Sprintf("- Version: %s\n", ver))
	b.WriteString("- Protocol: Model Context Protocol (2024-11-05 / draft)\n")
	b.WriteString(fmt.Sprintf("- Discovery Server Card: %s/.well-known/mcp/server-card.json\n", prefix))
	b.WriteString(fmt.Sprintf("- RFC 9727 Linkset: %s/.well-known/linkset.json\n", prefix))
	b.WriteString(fmt.Sprintf("- Primary Endpoint: %s/mcp\n\n", prefix))
	b.WriteString("## Core Capabilities\n")
	b.WriteString("- prime_context: Seed full project memory, goals, lessons, and git status.\n")
	b.WriteString("- list_goals: Inspect epistemic roadmap goals and verification contracts.\n")
	b.WriteString("- save_lesson: Record learned debugging scars and preventative rules.\n")
	b.WriteString("- save_memo: Store architectural directives and decision records.\n")
	b.WriteString("- omni_search: Search hybrid vector and semantic memory entries.\n")
	return b.String()
}
