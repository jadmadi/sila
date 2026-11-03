package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// CallTool dispatches a tool call to the appropriate upstream server.
func (u *UpstreamManager) CallTool(ctx context.Context, fullName string, args map[string]any, project string) (any, error) {
	if u == nil {
		return nil, errors.New("upstream manager is nil")
	}

	u.mu.RLock()
	enabled := u.enabled
	reg, ok := u.tools[fullName]
	denialReason := u.deniedTools[fullName]
	var ambigServers []string
	parts := strings.SplitN(fullName, "_", 2)
	if len(parts) == 2 {
		ambigServers = u.ambiguousPrefixes[parts[0]]
	}
	u.mu.RUnlock()

	// 1. Check global execution policy immediately before any acquisition or dispatch
	if !enabled {
		return nil, errors.New("upstream manager is disabled (mcp_upstreams_enabled is false)")
	}

	// 2. Reserved core tools must fail immediately
	if IsCoreTool(fullName) || denialReason == "reserved_core_tool" {
		return nil, fmt.Errorf("tool %q is a reserved core tool and cannot be dispatched upstream", fullName)
	}

	// 3. Duplicate collisions, ambiguous prefixes, or denied tools fail immediately
	if denialReason != "" {
		return nil, fmt.Errorf("upstream tool %q is denied: %s", fullName, denialReason)
	}
	if len(ambigServers) > 1 {
		return nil, fmt.Errorf("upstream tool %q has ambiguous prefix %q shared by servers %s", fullName, parts[0], strings.Join(ambigServers, ", "))
	}

	// 4. Unknown tool / unknown suffix
	if !ok {
		return nil, fmt.Errorf("unknown upstream tool %q", fullName)
	}

	// 5. Built-in management tools
	if fullName == "mcp_upstream_status" {
		return u.GetStatuses(), nil
	}
	if fullName == "mcp_upstream_toggle" {
		serverName, _ := args["server"].(string)
		enabledVal, _ := args["enabled"].(bool)
		res, err := u.ToggleServer(serverName, enabledVal)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"server":  serverName,
			"enabled": res,
			"message": fmt.Sprintf("Upstream server %q enabled=%v", serverName, res),
		}, nil
	}

	// 6. Gateways
	if fullName == "alphaxiv_query" {
		return u.CallGateway(ctx, "alphaxiv", args, project)
	}
	if fullName == "stitch_query" {
		return u.CallGateway(ctx, "stitch", args, project)
	}
	if fullName == "science_query" {
		return u.CallGateway(ctx, "science", args, project)
	}

	// 7. Check server status immediately before acquisition and dispatch
	u.mu.RLock()
	srv, ok := u.servers[reg.serverName]
	toggled := u.toggled[reg.serverName]
	u.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("upstream server %q not found in configuration", reg.serverName)
	}
	if srv.Disabled {
		return nil, fmt.Errorf("upstream server %q is disabled", reg.serverName)
	}
	if srv.OnDemand && !toggled {
		return nil, fmt.Errorf("upstream server %q is on-demand and not currently enabled (use mcp_upstream_toggle)", reg.serverName)
	}

	// 8. Egress privacy boundary check and sanitization
	sanitizedArgs, err := SanitizeEgressArgs(fullName, args)
	if err != nil {
		return nil, err
	}

	// 9. Client acquisition and dispatch
	client, err := u.getClient(reg.serverName)
	if err != nil {
		return nil, RedactEgressError(err)
	}

	result, err := client.CallTool(ctx, reg.rawToolName, sanitizedArgs)
	if err != nil {
		return nil, RedactEgressError(err)
	}

	// Auto-ingest documentation results if enabled
	u.interceptAutoIngest(reg.serverName, reg.rawToolName, sanitizedArgs, result, project)

	return result, nil
}

// CallGateway routes a compact gateway action to the underlying upstream server.
func (u *UpstreamManager) CallGateway(ctx context.Context, gateway string, args map[string]any, project string) (any, error) {
	if u == nil {
		return nil, errors.New("upstream manager is nil")
	}

	u.mu.RLock()
	enabled := u.enabled
	srv, ok := u.servers[gateway]
	u.mu.RUnlock()

	// 1. Check global execution policy immediately before any acquisition or dispatch
	if !enabled {
		return nil, errors.New("upstream manager is disabled (mcp_upstreams_enabled is false)")
	}

	if !ok {
		switch gateway {
		case "alphaxiv":
			return nil, errors.New("alphaxiv is not configured. Set your token via 'sila config set alphaxiv_api_token <token>' or ALPHAXIV_API_TOKEN environment variable")
		case "stitch":
			return nil, errors.New("stitch is not configured. Set your API key via 'sila config set stitch_api_key <key>' or STITCH_API_KEY environment variable")
		default:
			return nil, fmt.Errorf("gateway %q is not configured", gateway)
		}
	}

	// 2. Check per-server disable state immediately before acquisition
	if srv.Disabled {
		return nil, fmt.Errorf("upstream server %q is disabled", gateway)
	}

	// 3. Validate arguments and action BEFORE acquiring client or touching transport
	action, _ := args["action"].(string)
	if action == "" {
		return nil, errors.New("missing required parameter 'action'")
	}

	var upstreamTool string
	upstreamArgs := make(map[string]any)

	switch gateway {
	case "alphaxiv":
		switch action {
		case "discover", "search":
			upstreamTool = "discover_papers"
			if kw, ok := args["keywords"]; ok {
				upstreamArgs["keywords"] = kw
			} else if q, ok := args["question"].(string); ok {
				words := strings.Fields(q)
				if len(words) > 5 {
					words = words[:5]
				}
				upstreamArgs["keywords"] = words
			}
			if q, ok := args["question"]; ok {
				upstreamArgs["question"] = q
			}
			upstreamArgs["difficulty"] = 5
			if diff, ok := args["difficulty"]; ok {
				upstreamArgs["difficulty"] = diff
			}
			if p, ok := args["prioritize"]; ok {
				upstreamArgs["prioritize"] = p
			}
		case "get_paper", "read":
			upstreamTool = "get_paper_content"
			urlStr, _ := args["url"].(string)
			if urlStr == "" {
				if paperID, ok := args["paper_id"].(string); ok && paperID != "" {
					urlStr = "https://arxiv.org/abs/" + paperID
				}
			}
			if urlStr == "" {
				return nil, errors.New("alphaxiv get_paper requires 'url' or 'paper_id'")
			}
			upstreamArgs["url"] = urlStr
			if ft, ok := args["full_text"]; ok {
				upstreamArgs["fullText"] = ft
			}
		case "query_pdf", "ask":
			upstreamTool = "answer_pdf_queries"
			urlStr, _ := args["url"].(string)
			if urlStr == "" {
				if paperID, ok := args["paper_id"].(string); ok && paperID != "" {
					urlStr = "https://arxiv.org/abs/" + paperID
				}
			}
			if urlStr == "" {
				return nil, errors.New("alphaxiv query_pdf requires 'url' or 'paper_id'")
			}
			upstreamArgs["url"] = urlStr
			q, _ := args["question"].(string)
			if q == "" {
				return nil, errors.New("alphaxiv query_pdf requires 'question'")
			}
			upstreamArgs["queries"] = []string{q}
		case "raw_tool":
			toolName, _ := args["tool_name"].(string)
			if toolName == "" {
				return nil, errors.New("raw_tool action requires 'tool_name'")
			}
			if IsCoreTool(toolName) {
				return nil, fmt.Errorf("tool %q is a reserved core tool", toolName)
			}
			upstreamTool = toolName
			if rawArgs, ok := args["arguments"].(map[string]any); ok {
				upstreamArgs = rawArgs
			}
		default:
			return nil, fmt.Errorf("unknown alphaxiv action %q (supported: discover, get_paper, query_pdf, raw_tool)", action)
		}

	case "stitch":
		switch action {
		case "generate_screen":
			upstreamTool = "generate_screen_from_text"
			upstreamArgs["projectId"] = args["project_id"]
			upstreamArgs["prompt"] = args["prompt"]
		case "generate_variants", "variants":
			upstreamTool = "generate_variants"
			upstreamArgs["projectId"] = args["project_id"]
			upstreamArgs["prompt"] = args["prompt"]
			if screenID, ok := args["screen_id"].(string); ok && screenID != "" {
				upstreamArgs["selectedScreenIds"] = []string{screenID}
			}
		case "get_screen":
			upstreamTool = "get_screen"
			upstreamArgs["projectId"] = args["project_id"]
			upstreamArgs["screenId"] = args["screen_id"]
		case "list_screens":
			upstreamTool = "list_screens"
			upstreamArgs["projectId"] = args["project_id"]
		case "list_projects":
			upstreamTool = "list_projects"
		case "create_design_system":
			upstreamTool = "create_design_system"
			if rawArgs, ok := args["arguments"].(map[string]any); ok {
				upstreamArgs = rawArgs
			}
		case "raw_tool":
			toolName, _ := args["tool_name"].(string)
			if toolName == "" {
				return nil, errors.New("raw_tool action requires 'tool_name'")
			}
			if IsCoreTool(toolName) {
				return nil, fmt.Errorf("tool %q is a reserved core tool", toolName)
			}
			upstreamTool = toolName
			if rawArgs, ok := args["arguments"].(map[string]any); ok {
				upstreamArgs = rawArgs
			}
		default:
			return nil, fmt.Errorf("unknown stitch action %q (supported: generate_screen, generate_variants, get_screen, list_screens, list_projects, create_design_system, raw_tool)", action)
		}

	case "science":
		switch action {
		case "search":
			q, _ := args["query"].(string)
			if strings.TrimSpace(q) == "" {
				return nil, errors.New("science search requires 'query'")
			}
			upstreamTool = "search_papers"
			upstreamArgs["query"] = q
			copyScienceArgs(upstreamArgs, args, "limit", "mode", "year_from", "year_to", "publication_type", "open_access_only", "readable_only", "abstracts")
		case "get_paper":
			ids := scienceStringSlice(args["ids"])
			if len(ids) == 0 {
				return nil, errors.New("science get_paper requires 'ids'")
			}
			upstreamTool = "get_papers"
			upstreamArgs["ids"] = ids
		case "similar":
			id, _ := args["id"].(string)
			if strings.TrimSpace(id) == "" {
				return nil, errors.New("science similar requires 'id'")
			}
			upstreamTool = "find_similar_papers"
			upstreamArgs["id"] = id
			copyScienceArgs(upstreamArgs, args, "limit", "abstracts")
		case "citation_walk":
			id, _ := args["id"].(string)
			if strings.TrimSpace(id) == "" {
				return nil, errors.New("science citation_walk requires 'id'")
			}
			upstreamTool = "citation_walk"
			upstreamArgs["id"] = id
			copyScienceArgs(upstreamArgs, args, "max_neighbours", "limit")
		case "map_topic":
			topic, _ := args["topic"].(string)
			if strings.TrimSpace(topic) == "" {
				return nil, errors.New("science map_topic requires 'topic'")
			}
			upstreamTool = "map_topic"
			upstreamArgs["topic"] = topic
			copyScienceArgs(upstreamArgs, args, "scope", "group", "page")
		case "read_topic_group":
			topic, _ := args["topic"].(string)
			if strings.TrimSpace(topic) == "" {
				return nil, errors.New("science read_topic_group requires 'topic'")
			}
			if _, ok := args["group"]; !ok {
				return nil, errors.New("science read_topic_group requires 'group'")
			}
			upstreamTool = "read_topic_group"
			upstreamArgs["topic"] = topic
			copyScienceArgs(upstreamArgs, args, "group", "part", "page")
		case "find_people":
			name, _ := args["name"].(string)
			if strings.TrimSpace(name) == "" {
				return nil, errors.New("science find_people requires 'name'")
			}
			upstreamTool = "find_people"
			upstreamArgs["name"] = name
			copyScienceArgs(upstreamArgs, args, "kind", "limit")
		case "get_profile":
			id, _ := args["id"].(string)
			if strings.TrimSpace(id) == "" {
				return nil, errors.New("science get_profile requires 'id'")
			}
			upstreamTool = "get_profile"
			upstreamArgs["id"] = id
			copyScienceArgs(upstreamArgs, args, "group", "list", "offset", "together_with")
		case "raw_tool":
			toolName, _ := args["tool_name"].(string)
			toolName = strings.TrimPrefix(toolName, "science_")
			if toolName == "" {
				return nil, errors.New("raw_tool action requires 'tool_name'")
			}
			if IsCoreTool(toolName) {
				return nil, fmt.Errorf("tool %q is a reserved core tool", toolName)
			}
			upstreamTool = toolName
			if rawArgs, ok := args["arguments"].(map[string]any); ok {
				upstreamArgs = rawArgs
			}
		default:
			return nil, fmt.Errorf("unknown science action %q (supported: search, get_paper, similar, citation_walk, map_topic, read_topic_group, find_people, get_profile, raw_tool)", action)
		}
	}

	// Propagate privacy opt-in from outer arguments if supplied
	if opt, ok := args["allow_sensitive"].(bool); ok && opt {
		upstreamArgs["allow_sensitive"] = true
	} else if opt, ok := args["opt_in_privacy"].(bool); ok && opt {
		upstreamArgs["opt_in_privacy"] = true
	}

	sanitizedArgs, err := SanitizeEgressArgs(upstreamTool, upstreamArgs)
	if err != nil {
		return nil, err
	}

	client, err := u.getClient(gateway)
	if err != nil {
		return nil, RedactEgressError(fmt.Errorf("failed to initialize %s upstream: %w", gateway, err))
	}

	result, err := client.CallTool(ctx, upstreamTool, sanitizedArgs)
	if err != nil {
		if gateway == "science" {
			return nil, wrapScienceRateLimitError(RedactEgressError(err))
		}
		return nil, RedactEgressError(err)
	}

	if srv.AutoIngest {
		u.interceptAutoIngest(gateway, upstreamTool, sanitizedArgs, result, project)
	}

	return result, nil
}

// copyScienceArgs copies only explicitly supported optional arguments into the
// upstream request, preventing arbitrary pass-through.
func copyScienceArgs(dst, src map[string]any, keys ...string) {
	for _, key := range keys {
		if v, ok := src[key]; ok {
			dst[key] = v
		}
	}
}

// scienceStringSlice normalizes MCP JSON arrays and direct string slices.
func scienceStringSlice(v any) []string {
	switch vals := v.(type) {
	case []string:
		return vals
	case []any:
		out := make([]string, 0, len(vals))
		for _, item := range vals {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		if strings.TrimSpace(vals) != "" {
			return []string{vals}
		}
	}
	return nil
}

// wrapScienceRateLimitError adds actionable retry guidance for the upstream's
// 30 requests/minute per-IP budget (map_topic counts as 5 requests).
func wrapScienceRateLimitError(err error) error {
	if err == nil {
		return nil
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "429") || strings.Contains(msg, "rate limit") || strings.Contains(msg, "too many requests") {
		return fmt.Errorf("%w (ScienceMadeReadable allows 30 requests/minute per IP; map_topic counts as 5 — retry after a short delay)", err)
	}
	return err
}
