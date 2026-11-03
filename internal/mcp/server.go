package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"uuid"

	"github.com/jadmadi/sila/internal/assist"
	"github.com/jadmadi/sila/internal/config"
	"github.com/jadmadi/sila/internal/dream"
	"github.com/jadmadi/sila/internal/store"
	"github.com/jadmadi/sila/internal/version"
)

// JSONRPCRequest represents an incoming JSON-RPC 2.0 request.
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// JSONRPCResponse represents an outgoing JSON-RPC 2.0 response.
type JSONRPCResponse struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      any           `json:"id,omitempty"`
	Result  any           `json:"result,omitempty"`
	Error   *JSONRPCError `json:"error,omitempty"`
}

// JSONRPCError represents a standard JSON-RPC 2.0 error.
type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Tool describes an MCP tool definition.
type Tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Category    string      `json:"category,omitempty"`
	InputSchema InputSchema `json:"inputSchema"`
}

// Tool categories let clients filter the published surface without a
// server-side profile. `read` tools never mutate; `write` tools can mutate
// sila's store or project files; `collab` tools coordinate goals, crew, gates,
// and feedback (they write too).
const (
	ToolCategoryRead    = "read"
	ToolCategoryWrite   = "write"
	ToolCategoryCollab  = "collab"
	ToolCategoryContext = "read" // Context & orientation tools fall under read category
)

const (
	ProfileContinuity = "continuity"
	ProfileFull       = "full"
)

// InputSchema describes the JSON schema of tool arguments.
type InputSchema struct {
	Type       string                    `json:"type"`
	Properties map[string]PropertySchema `json:"properties"`
	Required   []string                  `json:"required,omitempty"`
}

// PropertySchema describes a single tool parameter.
type PropertySchema struct {
	Type        string `json:"type"`
	Description string `json:"description"`
}

// Server handles MCP protocol communication over stdio.
type Server struct {
	store                *store.Store
	in                   io.Reader
	out                  io.Writer
	mu                   sync.Mutex
	currentSentryWarning string
	upstreamMgr          *UpstreamManager
	pluginProvider       *PluginToolProvider
	profile              string
	traceArgs            bool
	lastToolError        bool
	sessionID            string
	sessionSource        string
}

// SetProfile sets the tool exposure profile (ProfileContinuity or ProfileFull).
func (s *Server) SetProfile(profile string) {
	s.profile = strings.ToLower(strings.TrimSpace(profile))
}

// Profile returns the active tool exposure profile.
func (s *Server) Profile() string {
	return s.profile
}

// SetTraceArgs enables bounded, value-free argument tracing for tools/call.
func (s *Server) SetTraceArgs(enabled bool) {
	s.traceArgs = enabled
}

// SetUpstreamManager sets the upstream MCP manager for tool multiplexing.
func (s *Server) SetUpstreamManager(mgr *UpstreamManager) {
	s.upstreamMgr = mgr
}

// UpstreamManager returns the configured upstream MCP manager.
func (s *Server) UpstreamManager() *UpstreamManager {
	return s.upstreamMgr
}

// SetPluginToolProvider sets the plugin tool provider for dynamic plugin tools.
func (s *Server) SetPluginToolProvider(p *PluginToolProvider) {
	s.pluginProvider = p
}

// PluginToolProvider returns the configured plugin tool provider.
func (s *Server) PluginToolProvider() *PluginToolProvider {
	return s.pluginProvider
}

// Run processes incoming JSON-RPC requests until EOF.
func (s *Server) Run() error {
	scanner := bufio.NewScanner(s.in)
	// Support large payloads up to 10MB
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, 10*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var req JSONRPCRequest
		if err := jsonv2.Unmarshal([]byte(line), &req); err != nil {
			s.sendError(nil, -32700, "Parse error", err.Error())
			continue
		}
		s.handleRequest(req)
	}
	return scanner.Err()
}

func (s *Server) handleRequest(req JSONRPCRequest) {
	// Notifications (no ID)
	if req.ID == nil {
		return
	}

	switch req.Method {
	case "initialize":
		profile := s.Profile()
		if profile == "" {
			profile = ProfileFull
		}
		s.sendResult(req.ID, map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]any{
				"tools":   map[string]any{},
				"prompts": map[string]any{},
				"experimental": map[string]any{
					"sila": map[string]any{
						"contract_version":      1,
						"profile":               profile,
						"continuity_tool_count": len(ContinuityToolNames),
						"full_tool_count":       PublicToolCount,
						"schema_digest":         SchemaDigest(),
						"commit":                version.Commit,
						"session_id":            s.sessionID,
						"session_source":        s.sessionSource,
					},
				},
			},
			"serverInfo": map[string]any{
				"name":    "sila",
				"title":   "Sila — cross-harness continuity engine for AI coding agents",
				"version": version.Version,
			},
		})
	case "ping":
		s.sendResult(req.ID, map[string]any{})
	case "tools/list":
		tools := ToolDefinitions()
		if s.pluginProvider == nil {
			dir, _ := s.resolveTargetProjectDir("")
			s.pluginProvider = NewPluginToolProvider(dir, "")
		}
		if s.pluginProvider != nil {
			tools = append(tools, s.pluginProvider.Tools()...)
		}
		if s.upstreamMgr != nil && s.upstreamMgr.Enabled() {
			tools = append(tools, s.upstreamMgr.ListTools()...)
		}
		tools = FilterToolsByProfile(tools, s.profile)
		s.sendResult(req.ID, map[string]any{
			"tools": tools,
		})
	case "tools/call":
		s.handleToolCall(req)
	case "prompts/list":
		s.handlePromptsList(req)
	case "prompts/get":
		s.handlePromptsGet(req)
	default:
		s.sendError(req.ID, -32601, "Method not found", fmt.Sprintf("Unknown method %q", req.Method))
	}
}

func (s *Server) handlePromptsList(req JSONRPCRequest) {
	s.sendResult(req.ID, map[string]any{
		"prompts": PromptDefinitions(),
	})
}

func (s *Server) handlePromptsGet(req JSONRPCRequest) {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return
		}
	}
	if params.Name == "" {
		s.sendError(req.ID, -32602, "Invalid params", "prompt name is required")
		return
	}

	projectStr := ""
	if pVal, ok := params.Arguments["project"].(string); ok {
		projectStr = pVal
	}
	projectID, _ := s.resolveTargetProject(projectStr)

	switch params.Name {
	case "cache_pinned_session":
		userTask := ""
		if tVal, ok := params.Arguments["user_task"].(string); ok {
			userTask = tVal
		}
		prime, _ := dream.BuildPrimeBrief(s.store, projectID, "", 10, 20000)
		var facts []store.Fact
		var handoff *store.Handoff
		var runbookStr string
		goalSlug := ""
		goalTitle := ""
		if prime != nil {
			facts = prime.TopFacts
			handoff = prime.ActiveHandoff
			if prime.ActiveGoal != nil {
				goalSlug = prime.ActiveGoal.Slug
				goalTitle = prime.ActiveGoal.Title
			}
			if prime.Runbook != nil {
				var rb strings.Builder
				for _, e := range prime.Runbook.Entries {
					rb.WriteString(fmt.Sprintf("• **%s**: `%s`\n", e.Phase, e.Command))
				}
				runbookStr = rb.String()
			}
		}
		prompt := BuildCachePinnedPrompt(CachePromptOptions{
			ProjectID:          projectID,
			IncludeTools:       true,
			RepoRunbook:        runbookStr,
			Facts:              facts,
			ActiveGoalSlug:     goalSlug,
			ActiveGoalTitle:    goalTitle,
			ActiveHandoff:      handoff,
			UserPrompt:         userTask,
			InsertCacheMarkers: true,
		})
		s.sendResult(req.ID, GetPromptResult{
			Description: fmt.Sprintf("Cache-pinned session prompt for project %s (estimated cache hit: %.1f%%)", projectID, prompt.EstimatedHitPct),
			Messages: []PromptMessage{
				{
					Role: "user",
					Content: PromptContent{
						Type: "text",
						Text: prompt.FullPrompt,
					},
				},
			},
		})

	case "cache_pinned_goal":
		slug := ""
		if sVal, ok := params.Arguments["slug"].(string); ok {
			slug = sVal
		}
		if slug == "" {
			s.sendError(req.ID, -32602, "Invalid params", "slug argument is required for cache_pinned_goal")
			return
		}
		prime, _ := dream.BuildPrimeBrief(s.store, projectID, "", 10, 20000)
		var facts []store.Fact
		var handoff *store.Handoff
		var runbookStr string
		if prime != nil {
			facts = prime.TopFacts
			handoff = prime.ActiveHandoff
			if prime.Runbook != nil {
				var rb strings.Builder
				for _, e := range prime.Runbook.Entries {
					rb.WriteString(fmt.Sprintf("• **%s**: `%s`\n", e.Phase, e.Command))
				}
				runbookStr = rb.String()
			}
		}
		prompt := BuildCachePinnedPrompt(CachePromptOptions{
			ProjectID:          projectID,
			IncludeTools:       true,
			RepoRunbook:        runbookStr,
			Facts:              facts,
			ActiveGoalSlug:     slug,
			ActiveGoalTitle:    fmt.Sprintf("Goal %s", slug),
			ActiveHandoff:      handoff,
			InsertCacheMarkers: true,
		})
		s.sendResult(req.ID, GetPromptResult{
			Description: fmt.Sprintf("Cache-pinned goal prompt for %s (estimated cache hit: %.1f%%)", slug, prompt.EstimatedHitPct),
			Messages: []PromptMessage{
				{
					Role: "user",
					Content: PromptContent{
						Type: "text",
						Text: prompt.FullPrompt,
					},
				},
			},
		})

	default:
		s.sendError(req.ID, -32602, "Invalid params", fmt.Sprintf("Unknown prompt %q", params.Name))
	}
}

type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// rejectNullArguments reports every argument whose value is JSON null, whether
// at the top level or as an array element. The advertised schemas declare no
// nullable field, and a null that decodes to a zero value would silently widen
// the request (an unscoped search) instead of failing.
func rejectNullArguments(args json.RawMessage) error {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(args, &obj); err != nil {
		return nil // shape errors are reported by the handler's own decode
	}
	var bad []string
	for key, raw := range obj {
		if isJSONNull(raw) {
			bad = append(bad, key)
			continue
		}
		var arr []json.RawMessage
		if json.Unmarshal(raw, &arr) == nil {
			for i, el := range arr {
				if isJSONNull(el) {
					bad = append(bad, fmt.Sprintf("%s[%d]", key, i))
				}
			}
		}
	}
	if len(bad) == 0 {
		return nil
	}
	sort.Strings(bad)
	return fmt.Errorf("null is not a valid value for %s; omit the field to leave it unset", strings.Join(bad, ", "))
}

func isJSONNull(raw json.RawMessage) bool {
	return strings.TrimSpace(string(raw)) == "null"
}

// clampLimit enforces non-negative numeric arguments with an upper bound so a
// single request cannot stream an unbounded slice of the store to a client.
func clampLimit(n, def, max int) int {
	if n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

// resolveTargetProject resolves a project parameter to an unambiguous project ID or active workspace identity.
// If raw is non-empty, it uses fuzzy alias matching before falling back.
// If raw is empty, it automatically detects the caller's active working directory or git root.
func (s *Server) handleToolCall(req JSONRPCRequest) {
	var params toolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.sendError(req.ID, -32602, "Invalid params", err.Error())
		return
	}
	start := time.Now()
	// A tool call with no arguments member is valid JSON-RPC; treat it as an
	// empty object so every handler reports missing *fields* consistently
	// instead of one handler seeing "unexpected end of JSON input".
	if len(params.Arguments) == 0 {
		params.Arguments = json.RawMessage("{}")
	}
	originalArgs := params.Arguments
	normalized, err := NormalizeArguments(params.Name, params.Arguments)
	if err != nil {
		if s.traceArgs || os.Getenv("SILA_MCP_TRACE_ARGS") == "1" {
			s.traceToolCall(params.Name, originalArgs, originalArgs, start, "error")
		}
		if argErr, ok := err.(*ArgError); ok {
			s.sendError(req.ID, -32602, "Invalid params", map[string]any{
				"field":    argErr.Field,
				"expected": argErr.Expected,
				"got":      argErr.Got,
			})
			return
		}
		s.sendError(req.ID, -32602, "Invalid params", err.Error())
		return
	}
	params.Arguments = normalized
	defer s.recordToolEvent(params.Name, params.Arguments, start)
	s.mu.Lock()
	s.lastToolError = false
	s.mu.Unlock()
	if s.traceArgs || os.Getenv("SILA_MCP_TRACE_ARGS") == "1" {
		defer func() {
			s.mu.Lock()
			outcome := "ok"
			if s.lastToolError {
				outcome = "error"
			}
			s.mu.Unlock()
			s.traceToolCall(params.Name, originalArgs, params.Arguments, start, outcome)
		}()
	}
	// JSON null decodes into a Go zero value, so a null selector used to mean
	// "no filter" — a null project silently widened a search to every project.
	if err := rejectNullArguments(params.Arguments); err != nil {
		s.sendError(req.ID, -32602, "Invalid params", err.Error())
		return
	}

	if assist.IsHighBlastRadiusTool(params.Name) {
		var projArg struct {
			Project string `json:"project"`
		}
		_ = json.Unmarshal(params.Arguments, &projArg)
		targetProj := projArg.Project
		if targetProj == "" {
			targetProj, _ = s.resolveTargetProject("")
		} else if resolved, err := s.resolveTargetProject(targetProj); err == nil {
			targetProj = resolved
		}
		audit := assist.AuditMCPCall(context.Background(), params.Name, params.Arguments, targetProj, nil)
		if audit.AdvisoryWarning != "" {
			s.mu.Lock()
			s.currentSentryWarning = "⚠️ [Sentry Advisory] " + audit.AdvisoryWarning
			s.mu.Unlock()
		}
	}
	defer func() {
		s.mu.Lock()
		s.currentSentryWarning = ""
		s.mu.Unlock()
	}()

	if s.handleGoalsTool(req, params.Name, params.Arguments) {
		return
	}
	if s.handleProjectTool(req, params.Name, params.Arguments) {
		return
	}
	if s.handleKnowledgeTool(req, params.Name, params.Arguments) {
		return
	}
	if s.handleCollabTool(req, params.Name, params.Arguments) {
		return
	}
	if s.handleSpecialistTool(req, params.Name, params.Arguments) {
		return
	}
	if s.handleOpsTool(req, params.Name, params.Arguments) {
		return
	}
	if s.handlePluginTool(req, params.Name, params.Arguments) {
		return
	}

	if s.upstreamMgr == nil {
		cfg, _ := config.Load("")
		s.upstreamMgr = NewUpstreamManager(s.store, cfg)
	}
	if s.upstreamMgr != nil {
		if state, registered := s.upstreamMgr.ToolRuntimeState(params.Name); registered && state.Upstream != "" && !state.Runnable() {
			data := ClassifyUpstreamFailure(params.Name, nil, state)
			s.sendError(req.ID, -32603, FormatUpstreamErrorMessage(params.Name, data), data)
			return
		}
	}
	if s.upstreamMgr != nil && s.upstreamMgr.HasTool(params.Name) {
		var args map[string]any
		if len(params.Arguments) > 0 {
			_ = json.Unmarshal(params.Arguments, &args)
		}
		targetProj, _ := s.resolveTargetProject("")
		callCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		res, err := s.upstreamMgr.CallTool(callCtx, params.Name, args, targetProj)
		if err != nil {
			state, _ := s.upstreamMgr.ToolRuntimeState(params.Name)
			data := ClassifyUpstreamFailure(params.Name, err, state)
			s.sendError(req.ID, -32603, FormatUpstreamErrorMessage(params.Name, data), data)
			return
		}
		s.sendResult(req.ID, res)
		return
	}
	s.sendError(req.ID, -32601, "Tool not found", fmt.Sprintf("Unknown tool %q", params.Name))
}

func (s *Server) sendToolResult(id any, rawData any, text string) {
	s.mu.Lock()
	if s.currentSentryWarning != "" {
		text = s.currentSentryWarning + "\n\n" + text
		s.currentSentryWarning = ""
	}
	s.mu.Unlock()
	s.sendResult(id, map[string]any{
		"content": []map[string]any{
			{
				"type": "text",
				"text": text,
			},
		},
		"structuredData": rawData,
	})
}

func (s *Server) sendToolError(id any, rawData any, text string) {
	s.mu.Lock()
	s.lastToolError = true
	if s.currentSentryWarning != "" {
		text = s.currentSentryWarning + "\n\n" + text
		s.currentSentryWarning = ""
	}
	s.mu.Unlock()
	s.sendResult(id, map[string]any{
		"content": []map[string]any{
			{
				"type": "text",
				"text": text,
			},
		},
		"isError":        true,
		"structuredData": rawData,
	})
}

var mcpJSONV2Options = jsonv2.WithMarshalers(jsonv2.MarshalFunc(func(d time.Duration) ([]byte, error) {
	return []byte(strconv.FormatInt(int64(d), 10)), nil
}))

func (s *Server) sendResult(id any, result any) {
	res := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	if err := jsonv2.MarshalWrite(s.out, res, mcpJSONV2Options); err != nil {
		b, _ := json.Marshal(res)
		s.out.Write(b)
	}
	_, _ = s.out.Write([]byte("\n"))
}

func (s *Server) sendError(id any, code int, msg string, data any) {
	s.mu.Lock()
	s.lastToolError = true
	s.mu.Unlock()
	res := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &JSONRPCError{
			Code:    code,
			Message: msg,
			Data:    data,
		},
	}
	if err := jsonv2.MarshalWrite(s.out, res, mcpJSONV2Options); err != nil {
		b, _ := json.Marshal(res)
		s.out.Write(b)
	}
	_, _ = s.out.Write([]byte("\n"))
}

// mergeUniqueStrings merges multiple string slices preserving order and removing empty or duplicate values.
func mergeUniqueStrings(slices ...[]string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, s := range slices {
		for _, v := range s {
			v = strings.TrimSpace(v)
			if v != "" && !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
	}
	return out
}

// GenerateSessionID returns a cryptographically secure random session identifier using the native Go 1.27 uuid package.
func GenerateSessionID() string {
	return uuid.New().String()
}

func (s *Server) handlePluginTool(req JSONRPCRequest, name string, rawArgs json.RawMessage) bool {
	if !strings.HasPrefix(name, "plugin_") {
		return false
	}
	if s.pluginProvider == nil {
		dir, _ := s.resolveTargetProjectDir("")
		s.pluginProvider = NewPluginToolProvider(dir, "")
	}
	if s.pluginProvider == nil || !s.pluginProvider.HasTool(name) {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	res, err := s.pluginProvider.Call(ctx, name, rawArgs)
	if err != nil {
		s.sendToolError(req.ID, map[string]any{"error": err.Error()}, fmt.Sprintf("Plugin execution error: %v", err))
		return true
	}
	if res.IsError {
		s.sendToolError(req.ID, res.StructuredData, res.Text)
		return true
	}
	s.sendToolResult(req.ID, res.StructuredData, res.Text)
	return true
}
