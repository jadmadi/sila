package mcp

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/jadmadi/sila/internal/config"
	"github.com/jadmadi/sila/internal/store"
)

// UpstreamStatus reports the operational and registration status of a managed upstream MCP server.
type UpstreamStatus struct {
	Name       string   `json:"name"`
	Prefix     string   `json:"prefix"`
	Type       string   `json:"type"` // "stdio" or "http"
	Enabled    bool     `json:"enabled"`
	OnDemand   bool     `json:"on_demand"`
	Running    bool     `json:"running"`
	Configured bool     `json:"configured"`
	Tools      []string `json:"tools,omitempty"`
}

// registeredTool packages an upstream tool definition with its owning server and dispatch name.
type registeredTool struct {
	tool        Tool
	serverName  string
	rawToolName string
}

// UpstreamManager manages all active upstream MCP client connections and tool routing.
type UpstreamManager struct {
	mu                sync.RWMutex
	store             *store.Store
	cfg               *config.Config
	servers           map[string]config.UpstreamMCPServer
	clients           map[string]UpstreamClient
	tools             map[string]registeredTool // exact tool name -> details
	deniedTools       map[string]string         // tool name -> rejection reason
	prefixMap         map[string]string         // prefix -> serverName
	ambiguousPrefixes map[string][]string       // prefix -> conflicting server names
	toggled           map[string]bool           // serverName -> runtime enabled state
	enabled           bool
}

// NewUpstreamManager creates an UpstreamManager from configuration.
func NewUpstreamManager(st *store.Store, cfg *config.Config) *UpstreamManager {
	if cfg == nil {
		cfg, _ = config.Load("")
	}
	servers := cfg.ResolvedUpstreamMCPServers()

	enabled := false
	if cfg != nil {
		enabled = cfg.MCPUpstreamsEnabled
	}

	mgr := &UpstreamManager{
		store:             st,
		cfg:               cfg,
		servers:           servers,
		clients:           make(map[string]UpstreamClient),
		tools:             make(map[string]registeredTool),
		deniedTools:       make(map[string]string),
		prefixMap:         make(map[string]string),
		ambiguousPrefixes: make(map[string][]string),
		toggled:           make(map[string]bool),
		enabled:           enabled,
	}

	mgr.rebuildRegistryLocked()
	return mgr
}

func (u *UpstreamManager) rebuildRegistryLocked() {
	u.tools = make(map[string]registeredTool)
	u.deniedTools = make(map[string]string)
	u.prefixMap = make(map[string]string)
	u.ambiguousPrefixes = make(map[string][]string)

	// 1. Detect ambiguous or colliding server prefixes
	prefixServers := make(map[string][]string)
	for sName, srv := range u.servers {
		pfx := srv.Prefix
		if pfx == "" {
			pfx = strings.ReplaceAll(sName, "-", "")
		}
		prefixServers[pfx] = append(prefixServers[pfx], sName)
	}

	for pfx, sNames := range prefixServers {
		if len(sNames) > 1 {
			u.ambiguousPrefixes[pfx] = sNames
			for _, sName := range sNames {
				u.deniedTools[sName+":ambiguous_prefix"] = fmt.Sprintf("prefix %q shared by servers %s", pfx, strings.Join(sNames, ", "))
			}
		} else {
			u.prefixMap[pfx] = sNames[0]
		}
	}

	// 2. Built-in management tools (status, toggle)
	u.tools["mcp_upstream_status"] = registeredTool{
		tool: Tool{
			Name:        "mcp_upstream_status",
			Description: "Inspect the status, running state, and tool capabilities of all Sila-managed upstream MCP servers (ui-craft, drizzle, cloudflare, gopls, astro, science, alphaxiv, stitch).",
			Category:    ToolCategoryRead,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"server": {Type: "string", Description: "Optional server name filter (e.g. 'alphaxiv', 'stitch', 'cloudflare')"},
				},
			},
		},
		serverName:  "",
		rawToolName: "mcp_upstream_status",
	}

	u.tools["mcp_upstream_toggle"] = registeredTool{
		tool: Tool{
			Name:        "mcp_upstream_toggle",
			Description: "Dynamically enable or disable an upstream MCP server suite (e.g. 'science', 'alphaxiv', 'stitch') during an agent session, surfacing granular tools without restarting.",
			Category:    ToolCategoryWrite,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"server":  {Type: "string", Description: "Upstream server name (e.g. 'science', 'alphaxiv', 'stitch')"},
					"enabled": {Type: "boolean", Description: "Whether to enable (true) or disable (false) the server"},
				},
				Required: []string{"server", "enabled"},
			},
		},
		serverName:  "",
		rawToolName: "mcp_upstream_toggle",
	}

	// 3. Register tools for each configured server
	for sName, srv := range u.servers {
		pfx := srv.Prefix
		if pfx == "" {
			pfx = strings.ReplaceAll(sName, "-", "")
		}

		if sNames, ambig := u.ambiguousPrefixes[pfx]; ambig {
			candidates := u.collectServerToolCandidates(sName, pfx)
			for _, cand := range candidates {
				u.deniedTools[cand.tool.Name] = fmt.Sprintf("ambiguous prefix %q shared by servers %s", pfx, strings.Join(sNames, ", "))
			}
			continue
		}

		candidates := u.collectServerToolCandidates(sName, pfx)
		for _, cand := range candidates {
			toolName := cand.tool.Name

			// Check reserved core tool name
			if IsCoreTool(toolName) {
				u.deniedTools[toolName] = "reserved_core_tool"
				continue
			}

			// Check already denied
			if _, denied := u.deniedTools[toolName]; denied {
				continue
			}

			// Check duplicate tool name collision
			if existing, exists := u.tools[toolName]; exists {
				delete(u.tools, toolName)
				u.deniedTools[toolName] = fmt.Sprintf("duplicate tool collision between servers %q and %q", existing.serverName, sName)
				continue
			}

			u.tools[toolName] = cand
		}
	}
}

func (u *UpstreamManager) collectServerToolCandidates(sName, pfx string) []registeredTool {
	var res []registeredTool

	switch sName {
	case "ui-craft":
		for _, t := range DefaultUpstreamToolDefinitions() {
			if strings.HasPrefix(t.Name, "uicraft_") {
				res = append(res, registeredTool{
					tool:        t,
					serverName:  sName,
					rawToolName: strings.TrimPrefix(t.Name, "uicraft_"),
				})
			}
		}
	case "drizzle":
		for _, t := range DefaultUpstreamToolDefinitions() {
			if strings.HasPrefix(t.Name, "drizzle_") {
				res = append(res, registeredTool{
					tool:        t,
					serverName:  sName,
					rawToolName: strings.TrimPrefix(t.Name, "drizzle_"),
				})
			}
		}
	case "cloudflare":
		for _, t := range DefaultUpstreamToolDefinitions() {
			if strings.HasPrefix(t.Name, "cf_") {
				res = append(res, registeredTool{
					tool:        t,
					serverName:  sName,
					rawToolName: strings.TrimPrefix(t.Name, "cf_"),
				})
			}
		}
	case "gopls":
		for _, t := range DefaultUpstreamToolDefinitions() {
			if strings.HasPrefix(t.Name, "gopls_") {
				res = append(res, registeredTool{
					tool:        t,
					serverName:  sName,
					rawToolName: strings.TrimPrefix(t.Name, "gopls_"),
				})
			}
		}
	case "astro":
		for _, t := range DefaultUpstreamToolDefinitions() {
			if strings.HasPrefix(t.Name, "astro_") {
				res = append(res, registeredTool{
					tool:        t,
					serverName:  sName,
					rawToolName: strings.TrimPrefix(t.Name, "astro_"),
				})
			}
		}
	case "science":
		res = append(res, registeredTool{
			tool: Tool{
				Name:        "science_query",
				Description: "[Upstream Gateway: science] Query 211M+ scientific studies across OpenAlex by meaning or exact keywords, map research landscapes, walk citation graphs, or inspect author/institution profiles on demand. Automatically indexes retrieved paper records and abstracts into Sila's local SQLite FTS5 store.",
				Category:    ToolCategoryRead,
				InputSchema: InputSchema{
					Type: "object",
					Properties: map[string]PropertySchema{
						"action":           {Type: "string", Description: "Action: 'search', 'get_paper', 'similar', 'citation_walk', 'map_topic', 'read_topic_group', 'find_people', 'get_profile', 'raw_tool'"},
						"query":            {Type: "string", Description: "Search query for action='search'"},
						"limit":            {Type: "integer", Description: "Maximum results to return"},
						"mode":             {Type: "string", Description: "Search mode: 'auto', 'semantic', or 'keyword'"},
						"year_from":        {Type: "integer", Description: "Earliest publication year"},
						"year_to":          {Type: "integer", Description: "Latest publication year"},
						"publication_type": {Type: "string", Description: "Filter by publication type"},
						"open_access_only": {Type: "boolean", Description: "Restrict results to open-access papers"},
						"readable_only":    {Type: "boolean", Description: "Restrict results to papers with readable full text"},
						"abstracts":        {Type: "string", Description: "Abstract detail: 'short', 'full', or 'none'"},
						"ids":              {Type: "array", Description: "Paper identifiers for action='get_paper'"},
						"id":               {Type: "string", Description: "Paper, author, institution, or venue identifier"},
						"max_neighbours":   {Type: "integer", Description: "Maximum citation neighbours for action='citation_walk'"},
						"topic":            {Type: "string", Description: "Research topic for action='map_topic' or 'read_topic_group'"},
						"scope":            {Type: "string", Description: "Topic mapping scope: 'quick' or 'whole_field'"},
						"group":            {Type: "integer", Description: "Cluster group for topic actions"},
						"page":             {Type: "integer", Description: "Result page"},
						"part":             {Type: "string", Description: "Section to return: 'lists' or 'papers'"},
						"name":             {Type: "string", Description: "Author, institution, or source name for action='find_people'"},
						"kind":             {Type: "string", Description: "Entity kind: 'author', 'institution', or 'source'"},
						"list":             {Type: "string", Description: "Profile list filter for action='get_profile'"},
						"offset":           {Type: "integer", Description: "Result offset for action='get_profile'"},
						"together_with":    {Type: "array", Description: "Related entity identifiers for action='get_profile'"},
						"tool_name":        {Type: "string", Description: "Raw tool name if action='raw_tool' (e.g. 'science_search_papers')"},
						"arguments":        {Type: "object", Description: "Raw arguments map when action='raw_tool'"},
					},
					Required: []string{"action"},
				},
			},
			serverName:  sName,
			rawToolName: "science_query",
		})
		for _, t := range ScienceToolDefinitions() {
			res = append(res, registeredTool{
				tool:        t,
				serverName:  sName,
				rawToolName: strings.TrimPrefix(t.Name, "science_"),
			})
		}
	case "alphaxiv":
		res = append(res, registeredTool{
			tool: Tool{
				Name:        "alphaxiv_query",
				Description: "[Upstream Gateway: alphaxiv] Query 2.5M+ arXiv academic research papers, fetch structured paper summaries, or inspect research topics on AlphaXiv on demand. Automatically indexes retrieved paper content into Sila's local SQLite FTS5 store.",
				Category:    ToolCategoryRead,
				InputSchema: InputSchema{
					Type: "object",
					Properties: map[string]PropertySchema{
						"action":    {Type: "string", Description: "Action to perform: 'discover' (search papers by topic), 'get_paper' (retrieve paper content/summary), 'query_pdf' (ask questions about paper PDF), 'raw_tool' (invoke any raw alphaxiv tool)"},
						"keywords":  {Type: "array", Description: "Keywords for paper discovery"},
						"question":  {Type: "string", Description: "Question or topic description"},
						"url":       {Type: "string", Description: "arXiv or alphaXiv paper URL (e.g. 'https://arxiv.org/abs/2307.12307')"},
						"paper_id":  {Type: "string", Description: "arXiv paper ID (e.g. '2307.12307')"},
						"tool_name": {Type: "string", Description: "Raw tool name if action='raw_tool' (e.g. 'list_library', 'find_researchers')"},
						"arguments": {Type: "object", Description: "Raw arguments map when action='raw_tool'"},
					},
					Required: []string{"action"},
				},
			},
			serverName:  sName,
			rawToolName: "alphaxiv_query",
		})
		for _, t := range AlphaXivToolDefinitions() {
			res = append(res, registeredTool{
				tool:        t,
				serverName:  sName,
				rawToolName: strings.TrimPrefix(t.Name, "alphaxiv_"),
			})
		}
	case "stitch":
		res = append(res, registeredTool{
			tool: Tool{
				Name:        "stitch_query",
				Description: "[Upstream Gateway: Google Stitch] UI screen generation and design prototyping gateway. Generate screens from text, inspect design systems, or create variants without colliding with native project tools.",
				Category:    ToolCategoryWrite,
				InputSchema: InputSchema{
					Type: "object",
					Properties: map[string]PropertySchema{
						"action":     {Type: "string", Description: "Action to perform: 'generate_screen', 'create_design_system', 'generate_variants', 'get_screen', 'list_screens', 'list_projects', 'raw_tool'"},
						"project_id": {Type: "string", Description: "Stitch project ID"},
						"prompt":     {Type: "string", Description: "Text prompt describing screen to generate or edit"},
						"screen_id":  {Type: "string", Description: "Screen ID to retrieve or generate variants for"},
						"tool_name":  {Type: "string", Description: "Raw tool name if action='raw_tool'"},
						"arguments":  {Type: "object", Description: "Raw arguments map when action='raw_tool'"},
					},
					Required: []string{"action"},
				},
			},
			serverName:  sName,
			rawToolName: "stitch_query",
		})
		for _, t := range StitchToolDefinitions() {
			res = append(res, registeredTool{
				tool:        t,
				serverName:  sName,
				rawToolName: strings.TrimPrefix(t.Name, "stitch_"),
			})
		}
	}

	return res
}

// SetClient injects an UpstreamClient for a configured server (useful for testing and recording transports).
func (u *UpstreamManager) SetClient(serverName string, client UpstreamClient) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if client == nil {
		delete(u.clients, serverName)
	} else {
		u.clients[serverName] = client
	}
}

// RegisterServerTools dynamically registers tools for a server and validates them against core and duplicate constraints.
func (u *UpstreamManager) RegisterServerTools(serverName string, tools []Tool) {
	u.mu.Lock()
	defer u.mu.Unlock()

	srv, ok := u.servers[serverName]
	if !ok {
		return
	}
	pfx := srv.Prefix
	if pfx == "" {
		pfx = strings.ReplaceAll(serverName, "-", "")
	}

	for _, t := range tools {
		toolName := t.Name
		if IsCoreTool(toolName) {
			u.deniedTools[toolName] = "reserved_core_tool"
			continue
		}
		if _, isDenied := u.deniedTools[toolName]; isDenied {
			continue
		}
		if sNames, ambig := u.ambiguousPrefixes[pfx]; ambig {
			u.deniedTools[toolName] = fmt.Sprintf("ambiguous prefix %q shared by servers %s", pfx, strings.Join(sNames, ", "))
			continue
		}
		if existing, exists := u.tools[toolName]; exists && existing.serverName != serverName {
			delete(u.tools, toolName)
			u.deniedTools[toolName] = fmt.Sprintf("duplicate tool collision between servers %q and %q", existing.serverName, serverName)
			continue
		}
		raw := strings.TrimPrefix(toolName, pfx+"_")
		u.tools[toolName] = registeredTool{
			tool:        t,
			serverName:  serverName,
			rawToolName: raw,
		}
	}
}

// DiscoverServerTools queries a managed upstream server for exposed tools, registers them under the server prefix, and returns them.
func (u *UpstreamManager) DiscoverServerTools(ctx context.Context, serverName string) ([]Tool, error) {
	if u == nil {
		return nil, errors.New("upstream manager is nil")
	}

	client, err := u.getClient(serverName)
	if err != nil {
		return nil, fmt.Errorf("failed to get client for server %q: %w", serverName, err)
	}

	discoverer, ok := client.(interface {
		DiscoverTools(ctx context.Context) ([]Tool, error)
	})
	if !ok {
		return nil, fmt.Errorf("server %q does not support dynamic tool discovery", serverName)
	}

	tools, err := discoverer.DiscoverTools(ctx)
	if err != nil {
		return nil, fmt.Errorf("tool discovery failed for server %q: %w", serverName, err)
	}

	u.mu.RLock()
	srv, ok := u.servers[serverName]
	pfx := ""
	if ok {
		pfx = srv.Prefix
	}
	if pfx == "" {
		pfx = strings.ReplaceAll(serverName, "-", "")
	}
	u.mu.RUnlock()

	namespaced := make([]Tool, len(tools))
	for i, t := range tools {
		if !strings.HasPrefix(t.Name, pfx+"_") {
			t.Name = pfx + "_" + t.Name
		}
		namespaced[i] = t
	}

	u.RegisterServerTools(serverName, namespaced)
	return namespaced, nil
}

// Enabled reports whether upstream aggregation is active.
func (u *UpstreamManager) Enabled() bool {
	if u == nil {
		return false
	}
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.enabled
}

// SetEnabled toggles upstream aggregation.
func (u *UpstreamManager) SetEnabled(enabled bool) {
	if u == nil {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.enabled = enabled
}

// Close gracefully terminates all child client connections.
func (u *UpstreamManager) Close() error {
	if u == nil {
		return nil
	}
	u.mu.Lock()
	defer u.mu.Unlock()

	var firstErr error
	for _, client := range u.clients {
		if err := client.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	u.clients = make(map[string]UpstreamClient)
	return firstErr
}

// GetStatuses returns health, runtime, and capability status for all configured upstream servers.
func (u *UpstreamManager) GetStatuses() []UpstreamStatus {
	if u == nil {
		return nil
	}
	u.mu.RLock()
	defer u.mu.RUnlock()

	statuses := make([]UpstreamStatus, 0, len(u.servers))
	var names []string
	for name := range u.servers {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		srv := u.servers[name]
		prefix := srv.Prefix
		if prefix == "" {
			prefix = strings.ReplaceAll(name, "-", "")
		}
		typ := "stdio"
		if srv.ServerURL != "" {
			typ = "http"
		}
		running := false
		if client, ok := u.clients[name]; ok {
			running = client.IsRunning()
		}

		enabled := u.enabled && !srv.Disabled
		if srv.OnDemand {
			enabled = u.enabled && !srv.Disabled && u.toggled[name]
		}

		tools := u.getToolNamesForServerLocked(name)

		statuses = append(statuses, UpstreamStatus{
			Name:       name,
			Prefix:     prefix,
			Type:       typ,
			Enabled:    enabled,
			OnDemand:   srv.OnDemand,
			Running:    running,
			Configured: true,
			Tools:      tools,
		})
	}
	return statuses
}

func (u *UpstreamManager) getToolNamesForServerLocked(serverName string) []string {
	var toolNames []string
	for toolName, reg := range u.tools {
		if reg.serverName == serverName {
			toolNames = append(toolNames, toolName)
		}
	}
	sort.Strings(toolNames)
	return toolNames
}

func (u *UpstreamManager) getToolNamesForServer(name string) []string {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.getToolNamesForServerLocked(name)
}

// ToggleServer dynamically enables or disables an upstream server for the session.
func (u *UpstreamManager) ToggleServer(name string, enabled bool) (bool, error) {
	if u == nil {
		return false, errors.New("upstream manager is nil")
	}
	u.mu.Lock()
	defer u.mu.Unlock()

	srv, ok := u.servers[name]
	if !ok {
		return false, fmt.Errorf("upstream server %q not found in configuration", name)
	}

	u.toggled[name] = enabled
	if !enabled {
		if client, ok := u.clients[name]; ok {
			_ = client.Close()
			delete(u.clients, name)
		}
		if !srv.OnDemand {
			srv.Disabled = true
			u.servers[name] = srv
		}
	} else {
		if !srv.OnDemand {
			srv.Disabled = false
			u.servers[name] = srv
		}
	}

	// Rebuild registry; denied tools stay denied ("Re-enabling does not resurrect a stale denied registry")
	u.rebuildRegistryLocked()

	return enabled, nil
}

// isUpstreamGatewayTool reports whether name is a compact gateway tool that
// remains visible before its on-demand server suite is toggled on.
func isUpstreamGatewayTool(name string) bool {
	switch name {
	case "alphaxiv_query", "stitch_query", "science_query":
		return true
	default:
		return false
	}
}

// ListTools returns the registered upstream tool definitions.
func (u *UpstreamManager) ListTools() []Tool {
	if u == nil {
		return nil
	}
	u.mu.RLock()
	defer u.mu.RUnlock()

	if !u.enabled {
		return nil
	}

	var names []string
	for name := range u.tools {
		names = append(names, name)
	}
	sort.Strings(names)

	var res []Tool
	for _, name := range names {
		reg := u.tools[name]
		if reg.serverName == "" {
			res = append(res, reg.tool)
			continue
		}
		srv, ok := u.servers[reg.serverName]
		if !ok || srv.Disabled {
			continue
		}
		if srv.OnDemand {
			isGateway := isUpstreamGatewayTool(name)
			if isGateway {
				res = append(res, reg.tool)
			} else if u.toggled[reg.serverName] {
				res = append(res, reg.tool)
			}
			continue
		}
		res = append(res, reg.tool)
	}
	return res
}

// HasTool reports whether a tool name belongs to a managed upstream.
func (u *UpstreamManager) HasTool(name string) bool {
	if u == nil {
		return false
	}
	u.mu.RLock()
	defer u.mu.RUnlock()

	if !u.enabled {
		return false
	}
	if IsCoreTool(name) {
		return false
	}
	if _, denied := u.deniedTools[name]; denied {
		return false
	}
	parts := strings.SplitN(name, "_", 2)
	if len(parts) == 2 && len(u.ambiguousPrefixes[parts[0]]) > 1 {
		return false
	}
	reg, ok := u.tools[name]
	if !ok {
		return false
	}
	if reg.serverName == "" {
		return true
	}
	srv, ok := u.servers[reg.serverName]
	if !ok || srv.Disabled {
		return false
	}
	if srv.OnDemand {
		isGateway := isUpstreamGatewayTool(name)
		if isGateway {
			return true
		}
		return u.toggled[reg.serverName]
	}
	return true
}

// getClient retrieves or lazily establishes an upstream client.
func (u *UpstreamManager) getClient(serverName string) (UpstreamClient, error) {
	u.mu.Lock()
	defer u.mu.Unlock()

	if !u.enabled {
		return nil, errors.New("upstream manager is disabled (mcp_upstreams_enabled is false)")
	}

	srv, ok := u.servers[serverName]
	if !ok {
		return nil, fmt.Errorf("upstream server %q not found in configuration", serverName)
	}

	if srv.Disabled {
		return nil, fmt.Errorf("upstream server %q is disabled", serverName)
	}

	if client, ok := u.clients[serverName]; ok {
		if httpCli, ok := client.(*HTTPUpstreamClient); ok {
			// Refresh credentials from srv or cfg if updated
			authToken := srv.AuthToken
			if authToken == "" && serverName == "cloudflare" && u.cfg != nil {
				authToken = u.cfg.CloudflareAPIToken
			}
			httpCli.UpdateCredentials(authToken, srv.Headers)
		}
		return client, nil
	}

	prefix := srv.Prefix
	if prefix == "" {
		prefix = strings.ReplaceAll(serverName, "-", "")
	}

	var client UpstreamClient
	if srv.ServerURL != "" {
		// Remote HTTP endpoint
		authToken := srv.AuthToken
		if authToken == "" && serverName == "cloudflare" && u.cfg != nil {
			authToken = u.cfg.CloudflareAPIToken
		}
		client = NewHTTPUpstreamClient(serverName, prefix, srv.ServerURL, srv.Headers, authToken, srv.AutoIngest)
	} else {
		// Stdio subprocess
		if err := ValidateStdioArgs(srv.Args); err != nil {
			return nil, fmt.Errorf("invalid stdio configuration for %s: %w", serverName, err)
		}
		client = NewStdioUpstreamClient(serverName, prefix, srv.Command, srv.Args, srv.Env, srv.AutoIngest)
	}

	u.clients[serverName] = client
	return client, nil
}

// UpdateCredentials updates credentials for a configured upstream server and active client.
func (u *UpstreamManager) UpdateCredentials(serverName string, authToken string, headers map[string]string) {
	u.mu.Lock()
	defer u.mu.Unlock()

	if srv, ok := u.servers[serverName]; ok {
		srv.AuthToken = authToken
		srv.Headers = headers
		u.servers[serverName] = srv
	}
	if client, ok := u.clients[serverName]; ok {
		if httpCli, ok := client.(*HTTPUpstreamClient); ok {
			httpCli.UpdateCredentials(authToken, headers)
		}
	}
}
