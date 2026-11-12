package config

import (
	"strings"
)

// KeyDoc documents a single configuration key in config.json.
type KeyDoc struct {
	Key         string   `json:"key"`
	Type        string   `json:"type"`
	Default     string   `json:"default"`
	Description string   `json:"description"`
	Security    string   `json:"security"`
	Example     string   `json:"example,omitempty"`
	ValidValues []string `json:"valid_values,omitempty"`
	IsSecret    bool     `json:"is_secret"`
}

var keyDefinitions = []KeyDoc{
	{
		Key:         "search_provider",
		Type:        "string",
		Default:     "duckduckgo",
		Description: "Web search engine provider used by sila search, web doc fetching, and grounding.",
		Security:    "Network egress occurs only when web search commands are invoked. Queries are sent over HTTPS to the selected search engine.",
		Example:     "brave",
		ValidValues: []string{"duckduckgo", "brave", "tavily", "kagi", "google"},
		IsSecret:    false,
	},
	{
		Key:         "brave_api_key",
		Type:        "string",
		Default:     "",
		Description: "API key for the Brave Search REST API (free tier: 2,000 queries/month).",
		Security:    "Secret key. Stored locally in mode 0600. Transmitted only to api.search.brave.com over TLS. Redacted in CLI output.",
		Example:     "BSA...",
		IsSecret:    true,
	},
	{
		Key:         "tavily_api_key",
		Type:        "string",
		Default:     "",
		Description: "API key for the Tavily AI Search API for developer-grounded web search.",
		Security:    "Secret key. Stored locally in mode 0600. Transmitted only to api.tavily.com over TLS. Redacted in CLI output.",
		Example:     "tvly-...",
		IsSecret:    true,
	},
	{
		Key:         "kagi_api_key",
		Type:        "string",
		Default:     "",
		Description: "API key for the Kagi Universal Search API.",
		Security:    "Secret key. Stored locally in mode 0600. Transmitted only to kagi.com/api over TLS. Redacted in CLI output.",
		Example:     "kagi_...",
		IsSecret:    true,
	},
	{
		Key:         "google_api_key",
		Type:        "string",
		Default:     "",
		Description: "API key for Google Custom Search Engine (requires google_cse_cx).",
		Security:    "Secret key. Stored locally in mode 0600. Transmitted only to www.googleapis.com over TLS. Redacted in CLI output.",
		Example:     "AIzaSy...",
		IsSecret:    true,
	},
	{
		Key:         "google_cse_cx",
		Type:        "string",
		Default:     "",
		Description: "Custom Search Engine ID (CX) paired with google_api_key.",
		Security:    "Non-secret identifier for the configured Google programmable search engine.",
		Example:     "0123456789:abcdefghijk",
		IsSecret:    false,
	},
	{
		Key:         "github_token",
		Type:        "string",
		Default:     "",
		Description: "Personal access token for GitHub API (repo facts, releases, and code search).",
		Security:    "Secret token. Stored locally in mode 0600. Transmitted only to api.github.com over TLS. Redacted in CLI output.",
		Example:     "ghp_...",
		IsSecret:    true,
	},
	{
		Key:         "sourcegraph_token",
		Type:        "string",
		Default:     "",
		Description: "Access token for Sourcegraph code search API.",
		Security:    "Secret token. Stored locally in mode 0600. Transmitted only to sourcegraph.com over TLS. Redacted in CLI output.",
		Example:     "sgp_...",
		IsSecret:    true,
	},
	{
		Key:         "stackoverflow_key",
		Type:        "string",
		Default:     "",
		Description: "Application key for Stack Exchange / Stack Overflow API (higher rate limits).",
		Security:    "Secret key. Stored locally in mode 0600. Transmitted only to api.stackexchange.com over TLS. Redacted in CLI output.",
		Example:     "key...",
		IsSecret:    true,
	},
	{
		Key:         "typesafe_api_key",
		Type:        "string",
		Default:     "",
		Description: "API key for TypeSafe semantic sentry and assistance evaluation provider.",
		Security:    "Secret key. Stored locally in mode 0600. Requires explicit assist_enabled=true and consented scope. Zero ambient calls.",
		Example:     "apikey_...",
		IsSecret:    true,
	},
	{
		Key:         "assist_provider",
		Type:        "string",
		Default:     "typesafe",
		Description: "Provider backend for AI-assisted semantic verification, recap, and dream synthesis.",
		Security:    "Selects the engine used for consented assistance scopes. Use 'offline' or 'mock' for local development.",
		Example:     "typesafe",
		ValidValues: []string{"typesafe", "mock", "offline"},
		IsSecret:    false,
	},
	{
		Key:         "assist_enabled",
		Type:        "boolean",
		Default:     "false",
		Description: "Master toggle for AI assistance. When false, zero remote assistance calls are made regardless of stored keys.",
		Security:    "Strict consent invariant. Default is false (strictly offline / zero ambient telemetry).",
		Example:     "true",
		ValidValues: []string{"true", "false"},
		IsSecret:    false,
	},
	{
		Key:         "assist_scopes",
		Type:        "array of strings",
		Default:     "[]",
		Description: "List of feature scopes permitted to use AI assistance.",
		Security:    "Granular capability boundary. Storing keys or setting assist_enabled without explicit scope grants zero egress.",
		Example:     "[\"gate\", \"recap\"]",
		ValidValues: []string{"gate", "recap", "search", "doctor", "live", "verify", "all"},
		IsSecret:    false,
	},
	{
		Key:         "assist_projects",
		Type:        "array of strings",
		Default:     "[]",
		Description: "List of project IDs permitted to use assistance. Empty list permits all projects if assist_enabled is true.",
		Security:    "Boundary fence preventing assistance calls for sensitive or unapproved repositories.",
		Example:     "[\"sila\", \"manara\"]",
		IsSecret:    false,
	},
	{
		Key:         "cloud_enabled",
		Type:        "boolean",
		Default:     "false",
		Description: "Master toggle for Sila Cloud edge synchronization via Cloudflare Worker bridge.",
		Security:    "Explicit consent gate. Default is false (strictly local SQLite knowledge store).",
		Example:     "true",
		ValidValues: []string{"true", "false"},
		IsSecret:    false,
	},
	{
		Key:         "cloud_endpoint",
		Type:        "string",
		Default:     "",
		Description: "URL of the Sila Cloud edge worker bridge endpoint.",
		Security:    "Remote destination endpoint for consented synchronization over TLS.",
		Example:     "https://edge.sila.workers.dev",
		IsSecret:    false,
	},
	{
		Key:         "cloud_token",
		Type:        "string",
		Default:     "",
		Description: "Bearer authentication token for Sila Cloud edge worker.",
		Security:    "Secret token. Stored locally in mode 0600. Sent only via Authorization header over TLS. Redacted in CLI output.",
		Example:     "sk_edge_...",
		IsSecret:    true,
	},
	{
		Key:         "cloud_scopes",
		Type:        "array of strings",
		Default:     "[]",
		Description: "List of cloud edge synchronization scopes permitted.",
		Security:    "Granular sync boundary for inbound drains and remote MCP queries.",
		Example:     "[\"sync\", \"backup\"]",
		ValidValues: []string{"sync", "backup", "mcp", "all"},
		IsSecret:    false,
	},
	{
		Key:         "cloud_projects",
		Type:        "array of strings",
		Default:     "[]",
		Description: "List of project IDs permitted to synchronize to cloud edge.",
		Security:    "Boundary fence preventing sync of confidential repositories.",
		Example:     "[\"sila\"]",
		IsSecret:    false,
	},
	{
		Key:         "doc_sites",
		Type:        "array of strings",
		Default:     "[]",
		Description: "List of documentation URL prefixes or domains registered for deep search indexing.",
		Security:    "Allowlist restricting which web domains sila is permitted to crawl or fetch.",
		Example:     "[\"https://golang.org/pkg/*\", \"https://docs.anthropic.com/*\"]",
		IsSecret:    false,
	},
	{
		Key:         "cloudflare_api_token",
		Type:        "string",
		Default:     "",
		Description: "API token for Cloudflare REST API and edge MCP integrations (D1, Workers, Telemetry).",
		Security:    "Secret token. Stored locally in mode 0600. Sent only via Authorization header to api.cloudflare.com over TLS. Redacted in CLI output.",
		Example:     "cftok_...",
		IsSecret:    true,
	},
	{
		Key:         "cloudflare_account_id",
		Type:        "string",
		Default:     "",
		Description: "Cloudflare Account ID for Workers and D1 database operations.",
		Security:    "Public identifier. Stored locally in mode 0600. Sent in API request paths over TLS.",
		Example:     "a1b2c3d4e5f6...",
		IsSecret:    false,
	},
	{
		Key:         "cloudflare_d1_database_id",
		Type:        "string",
		Default:     "",
		Description: "Default Cloudflare D1 Database ID or UUID for edge database queries.",
		Security:    "Database identifier. Stored locally in mode 0600.",
		Example:     "00000000-0000-0000-0000-000000000000",
		IsSecret:    false,
	},
	{
		Key:         "alphaxiv_api_token",
		Type:        "string",
		Default:     "",
		Description: "Bearer token for AlphaXiv MCP server integration.",
		Security:    "Secret token. Stored locally in mode 0600. Sent only via Authorization header over TLS. Redacted in CLI output.",
		Example:     "axv_...",
		IsSecret:    true,
	},
	{
		Key:         "stitch_api_key",
		Type:        "string",
		Default:     "",
		Description: "API key for Stitch Google MCP integration.",
		Security:    "Secret key. Stored locally in mode 0600. Sent only via X-Goog-Api-Key header over TLS. Redacted in CLI output.",
		Example:     "AIzaSy...",
		IsSecret:    true,
	},
	{
		Key:         "gateway_enabled",
		Type:        "boolean",
		Default:     "false",
		Description: "Master toggle for external relay notifications (Discord, Telegram, Gemini, MCP).",
		Security:    "Explicit consent gate. Default is false.",
		Example:     "true",
		ValidValues: []string{"true", "false"},
		IsSecret:    false,
	},
	{
		Key:         "gateway_surfaces",
		Type:        "array of strings",
		Default:     "[]",
		Description: "List of enabled external relay surfaces.",
		Security:    "Surface allowlist restricting notification egress.",
		Example:     "[\"discord\", \"telegram\"]",
		ValidValues: []string{"discord", "telegram", "gemini", "mcp", "all"},
		IsSecret:    false,
	},
	{
		Key:         "gateway_discord_enabled",
		Type:        "boolean",
		Default:     "false",
		Description: "Enable Discord webhook relay notifications.",
		Security:    "Explicit toggle for Discord webhook delivery.",
		Example:     "true",
		ValidValues: []string{"true", "false"},
		IsSecret:    false,
	},
	{
		Key:         "gateway_discord_webhook",
		Type:        "string",
		Default:     "",
		Description: "Discord incoming webhook URL (contains secret webhook token).",
		Security:    "Secret credential URL. Stored locally in mode 0600. Redacted in CLI output.",
		Example:     "https://discord.com/api/webhooks/...",
		IsSecret:    true,
	},
	{
		Key:         "gateway_discord_username",
		Type:        "string",
		Default:     "",
		Description: "Display username for Discord webhook notifications.",
		Security:    "Public display string.",
		Example:     "sila-bot",
		IsSecret:    false,
	},
	{
		Key:         "gateway_telegram_enabled",
		Type:        "boolean",
		Default:     "false",
		Description: "Enable Telegram Bot relay notifications.",
		Security:    "Explicit toggle for Telegram bot delivery.",
		Example:     "true",
		ValidValues: []string{"true", "false"},
		IsSecret:    false,
	},
	{
		Key:         "gateway_telegram_token",
		Type:        "string",
		Default:     "",
		Description: "Telegram Bot API token.",
		Security:    "Secret token. Stored locally in mode 0600. Redacted in CLI output.",
		Example:     "123456:ABC-DEF...",
		IsSecret:    true,
	},
	{
		Key:         "gateway_telegram_chat_id",
		Type:        "string",
		Default:     "",
		Description: "Destination Telegram chat ID for relay notifications.",
		Security:    "Chat identifier stored locally in mode 0600.",
		Example:     "-1001234567890",
		IsSecret:    false,
	},
	{
		Key:         "gateway_gemini_enabled",
		Type:        "boolean",
		Default:     "false",
		Description: "Enable Gemini task bridge relay.",
		Security:    "Explicit toggle for Gemini bridge.",
		Example:     "true",
		ValidValues: []string{"true", "false"},
		IsSecret:    false,
	},
	{
		Key:         "gateway_gemini_endpoint",
		Type:        "string",
		Default:     "",
		Description: "Endpoint URL for the Gemini task bridge.",
		Security:    "Remote HTTPS endpoint.",
		Example:     "https://gemini-bridge.example.com",
		IsSecret:    false,
	},
	{
		Key:         "gateway_gemini_token",
		Type:        "string",
		Default:     "",
		Description: "Authentication token for the Gemini task bridge.",
		Security:    "Secret token. Stored locally in mode 0600. Redacted in CLI output.",
		Example:     "gem_...",
		IsSecret:    true,
	},
	{
		Key:         "gateway_mcp_enabled",
		Type:        "boolean",
		Default:     "false",
		Description: "Enable remote MCP gateway relay.",
		Security:    "Explicit toggle for remote MCP gateway.",
		Example:     "true",
		ValidValues: []string{"true", "false"},
		IsSecret:    false,
	},
	{
		Key:         "gateway_mcp_endpoint",
		Type:        "string",
		Default:     "",
		Description: "Endpoint URL for the remote MCP gateway.",
		Security:    "Remote HTTPS endpoint.",
		Example:     "https://mcp.example.com",
		IsSecret:    false,
	},
	{
		Key:         "gateway_mcp_token",
		Type:        "string",
		Default:     "",
		Description: "Authentication token for the remote MCP gateway.",
		Security:    "Secret token. Stored locally in mode 0600. Redacted in CLI output.",
		Example:     "mcp_...",
		IsSecret:    true,
	},
	{
		Key:         "mcp_upstreams_enabled",
		Type:        "boolean",
		Default:     "false",
		Description: "Master toggle for upstream MCP server federation.",
		Security:    "Explicit toggle for upstream MCP servers.",
		Example:     "true",
		ValidValues: []string{"true", "false"},
		IsSecret:    false,
	},
	{
		Key:         "recall_gate_policy",
		Type:        "string",
		Default:     "warn",
		Description: "Pre-tool recall gate policy: off disables the gate, warn advises once per prompt, enforce denies the first mutating tool call until a recall is observed.",
		Security:    "Local behavioral nudge, not a security boundary. State is content-free and stored locally in mode 0600 under .sila/state/recall.",
		Example:     "enforce",
		ValidValues: []string{"off", "warn", "enforce"},
		IsSecret:    false,
	},
}

// AllKeys returns documentation for all configuration keys.
func AllKeys() []KeyDoc {
	res := make([]KeyDoc, len(keyDefinitions))
	copy(res, keyDefinitions)
	return res
}

// ExplainKey looks up documentation for a specific key (case-insensitive, supporting aliases).
func ExplainKey(key string) (KeyDoc, bool) {
	k := strings.ToLower(strings.TrimSpace(key))
	switch k {
	case "tavily":
		k = "tavily_api_key"
	case "brave":
		k = "brave_api_key"
	case "kagi":
		k = "kagi_api_key"
	case "github", "gh_token":
		k = "github_token"
	case "sourcegraph", "src_token":
		k = "sourcegraph_token"
	case "stackoverflow", "so_key":
		k = "stackoverflow_key"
	case "google", "api_key":
		k = "google_api_key"
	case "cx", "cse_cx":
		k = "google_cse_cx"
	case "provider":
		k = "search_provider"
	case "typesafe", "jev_api_key":
		k = "typesafe_api_key"
	case "assistance_provider":
		k = "assist_provider"
	case "assistance_enabled":
		k = "assist_enabled"
	case "assistance_scopes", "assist_scope", "assistance_scope":
		k = "assist_scopes"
	case "assistance_projects", "assist_project", "assistance_project":
		k = "assist_projects"
	case "cloud_url":
		k = "cloud_endpoint"
	case "cloud_auth_token", "edge_token", "edge_auth_token", "edge.token":
		k = "cloud_token"
	case "cloud_scope":
		k = "cloud_scopes"
	case "cloud_project":
		k = "cloud_projects"
	case "cloudflare_token", "cf_token":
		k = "cloudflare_api_token"
	case "cloudflare_account", "cf_account":
		k = "cloudflare_account_id"
	case "cloudflare_d1_database", "cloudflare_d1_id", "cf_d1_id":
		k = "cloudflare_d1_database_id"
	case "alphaxiv_token", "alphaxiv":
		k = "alphaxiv_api_token"
	case "stitch_key", "stitch":
		k = "stitch_api_key"
	case "gateway_surface":
		k = "gateway_surfaces"
	case "gateway_discord_url":
		k = "gateway_discord_webhook"
	case "gateway_telegram_bot_token":
		k = "gateway_telegram_token"
	case "gateway_telegram_chat":
		k = "gateway_telegram_chat_id"
	case "gateway_gemini_url":
		k = "gateway_gemini_endpoint"
	case "gateway_gemini_auth_token":
		k = "gateway_gemini_token"
	case "gateway_mcp_url":
		k = "gateway_mcp_endpoint"
	case "gateway_mcp_auth_token":
		k = "gateway_mcp_token"
	case "upstreams_enabled", "mcp_upstream_enabled":
		k = "mcp_upstreams_enabled"
	case "recall_gate", "recall-policy":
		k = "recall_gate_policy"
	}

	for _, d := range keyDefinitions {
		if d.Key == k {
			return d, true
		}
	}
	return KeyDoc{}, false
}

// IsSecret reports whether a configuration key (or any of its aliases) holds a confidential credential.
func IsSecret(key string) bool {
	if doc, ok := ExplainKey(key); ok {
		return doc.IsSecret
	}
	lower := strings.ToLower(strings.TrimSpace(key))
	return strings.Contains(lower, "token") ||
		strings.Contains(lower, "secret") ||
		strings.Contains(lower, "api_key") ||
		strings.Contains(lower, "apikey") ||
		strings.Contains(lower, "password") ||
		strings.Contains(lower, "webhook")
}

// SecretKeys returns the sorted canonical names of all secret configuration keys.
func SecretKeys() []string {
	var out []string
	for _, d := range keyDefinitions {
		if d.IsSecret {
			out = append(out, d.Key)
		}
	}
	return out
}

// NonSecretKeys returns the sorted canonical names of all non-secret configuration keys.
func NonSecretKeys() []string {
	var out []string
	for _, d := range keyDefinitions {
		if !d.IsSecret {
			out = append(out, d.Key)
		}
	}
	return out
}

// JSONSchema returns the complete Draft-07 JSON Schema for config.json.
func JSONSchema() map[string]any {
	properties := make(map[string]any)
	for _, k := range keyDefinitions {
		prop := map[string]any{
			"description": k.Description,
		}
		switch k.Type {
		case "string":
			prop["type"] = "string"
			if k.Default != "" {
				prop["default"] = k.Default
			}
			if len(k.ValidValues) > 0 {
				prop["enum"] = k.ValidValues
			}
		case "boolean":
			prop["type"] = "boolean"
			prop["default"] = k.Default == "true"
		case "array of strings":
			prop["type"] = "array"
			items := map[string]any{"type": "string"}
			if len(k.ValidValues) > 0 {
				items["enum"] = k.ValidValues
			}
			prop["items"] = items
		}
		properties[k.Key] = prop
	}

	return map[string]any{
		"$schema":              "http://json-schema.org/draft-07/schema#",
		"title":                "SilaConfig",
		"description":          "Persistent configuration schema for Sila (صِلَة)",
		"type":                 "object",
		"additionalProperties": false,
		"properties":           properties,
	}
}
