package mcp

import (
	"fmt"
	"os"
	"strings"
	"sync"
)

// PublicToolCount is the documented size of the MCP contract. Tests keep this
// marker synchronized with the executable registry below and public docs.
const PublicToolCount = 90

// ContinuityToolCount is the default agent-workflow surface exposed by
// --profile=continuity.
const ContinuityToolCount = 10

// ContinuityToolOrder is the curated ten-tool continuity surface. The order is
// part of the contract; FilterToolsByProfile and tools/list preserve it.
var ContinuityToolOrder = []string{
	"prime_context",
	"recall",
	"omni_search",
	"save_memo",
	"save_lesson",
	"save_handoff",
	"get_project_facts",
	"list_goals",
	"reconcile_drift",
	"get_status",
}

// GraphToolOrder is the continuity surface plus the three graph tools.
var GraphToolOrder = []string{
	"prime_context", "recall", "omni_search", "save_memo", "save_lesson",
	"save_handoff", "get_project_facts", "list_goals", "reconcile_drift",
	"get_status", "query_graph", "shortest_path", "graph_report",
}

// ContinuityToolNames is the membership set derived from ContinuityToolOrder.
var ContinuityToolNames = func() map[string]bool {
	names := make(map[string]bool, len(ContinuityToolOrder))
	for _, name := range ContinuityToolOrder {
		names[name] = true
	}
	return names
}()

// ContinuityTools returns the curated tools in contract order. A name that is
// missing from the definition table is an error naming it, never a silent drop.
// SILA_MCP_EXTRA_TOOLS adds known names at the end; unknown names warn on stderr
// and are ignored.
func ContinuityTools(tools []Tool) ([]Tool, error) {
	byName := make(map[string]Tool, len(tools))
	for _, tool := range tools {
		byName[tool.Name] = tool
	}
	out, err := filterToolsByOrder(tools, ContinuityToolOrder)
	if err != nil {
		return nil, err
	}
	for _, raw := range strings.Split(os.Getenv("SILA_MCP_EXTRA_TOOLS"), ",") {
		name := strings.TrimSpace(raw)
		if name == "" || ContinuityToolNames[name] {
			continue
		}
		tool, ok := byName[name]
		if !ok {
			fmt.Fprintf(os.Stderr, "sila mcp: SILA_MCP_EXTRA_TOOLS ignores unknown tool %q\n", name)
			continue
		}
		out = append(out, tool)
	}
	return out, nil
}

// FilterToolsByProfile filters tools according to the target profile.
// If profile is empty or ProfileFull, all tools are returned.
// Otherwise, only tools in ContinuityToolNames are returned.
func FilterToolsByProfile(tools []Tool, profile string) []Tool {
	if profile == "" || strings.EqualFold(profile, ProfileFull) {
		return tools
	}
	order := ContinuityToolOrder
	if strings.EqualFold(profile, ProfileGraph) {
		order = GraphToolOrder
	}
	curated, err := filterToolsByOrder(tools, order)
	if err != nil {
		return nil
	}
	return curated
}

// filterToolsByOrder returns the named tools in the requested order.
func filterToolsByOrder(tools []Tool, order []string) ([]Tool, error) {
	byName := make(map[string]Tool, len(tools))
	for _, tool := range tools {
		byName[tool.Name] = tool
	}
	out := make([]Tool, 0, len(order))
	for _, name := range order {
		tool, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("continuity tool %q is missing from the definition table", name)
		}
		out = append(out, tool)
	}
	return out, nil
}

var (
	coreToolNamesOnce sync.Once
	coreToolNamesMap  map[string]bool
)

// IsCoreTool reports whether name is one of Sila's reserved core MCP tools.
func IsCoreTool(name string) bool {
	coreToolNamesOnce.Do(func() {
		coreToolNamesMap = make(map[string]bool, PublicToolCount)
		for _, t := range ToolDefinitions() {
			coreToolNamesMap[t.Name] = true
		}
	})
	return coreToolNamesMap[name]
}

// factReadTools are the five read tools that expose verification filters.
var factReadTools = map[string]bool{
	"recall": true, "search_knowledge": true, "get_project_facts": true,
	"omni_search": true, "prime_context": true,
}

func attachFactReadFlags(tools []Tool) {
	for i := range tools {
		if !factReadTools[tools[i].Name] {
			continue
		}
		if tools[i].InputSchema.Properties == nil {
			tools[i].InputSchema.Properties = map[string]PropertySchema{}
		}
		props := tools[i].InputSchema.Properties
		for key, description := range map[string]string{
			"include_unverified": "Include unverified rows",
			"include_stale":      "Include stale rows",
			"include_disputed":   "Include disputed rows",
		} {
			if _, ok := props[key]; !ok {
				props[key] = PropertySchema{Type: "boolean", Description: description}
			}
		}
	}
}

// revisionTools maps tools to the revision field they expose.
var revisionTools = map[string]string{
	"save_handoff":       "expected_revision",
	"memory_archive":     "expected_revision",
	"memory_restore":     "expected_revision",
	"get_latest_handoff": "revision_hash",
	"get_memory":         "revision_hash",
}

func attachRevisionFields(tools []Tool) {
	for i := range tools {
		field, ok := revisionTools[tools[i].Name]
		if !ok {
			continue
		}
		if tools[i].InputSchema.Properties == nil {
			tools[i].InputSchema.Properties = map[string]PropertySchema{}
		}
		if _, exists := tools[i].InputSchema.Properties[field]; !exists {
			description := "Revision hash"
			if field == "expected_revision" {
				description = "Expected revision"
			}
			tools[i].InputSchema.Properties[field] = PropertySchema{Type: "string", Description: description}
		}
	}
}

// ToolDefinitions is the canonical, executable MCP public contract.
func ToolDefinitions() []Tool {
	tools := append(coreToolDefinitions(), opsToolDefinitions()...)
	tools = append(tools, graphToolDefinitions()...)
	for i := range tools {
		tools[i].Category = toolCategories[tools[i].Name]
	}
	attachFactReadFlags(tools)
	attachRevisionFields(tools)
	attachCompactFields(tools)
	attachAsOfFields(tools)
	return tools
}

// toolCategories maps every published tool to its category. A test keeps this
// map in step with the registry, so a new tool cannot ship uncategorized.
var toolCategories = map[string]string{
	"salam":                     ToolCategoryRead,
	"get_operational_guidebook": ToolCategoryRead,
	"route_workflow":            ToolCategoryContext,
	"request_approval":          ToolCategoryCollab,
	"get_activity_digest":       ToolCategoryRead,
	"consult_personas":          ToolCategoryCollab,
	"save_domain_learning":      ToolCategoryWrite,
	"check_symbol_blast_radius": ToolCategoryRead,
	"go_symbol_references":      ToolCategoryRead,
	"go_package_api":            ToolCategoryRead,
	"go_diagnostics":            ToolCategoryRead,
	"cloudflare_d1_query":       ToolCategoryWrite,
	"cloudflare_worker_status":  ToolCategoryRead,
	"cloudflare_edge_telemetry": ToolCategoryRead,
	"search_knowledge":          ToolCategoryRead,
	"query_graph":               ToolCategoryRead,
	"shortest_path":             ToolCategoryRead,
	"graph_report":              ToolCategoryRead,
	"list_projects":             ToolCategoryRead,
	"get_project_facts":         ToolCategoryRead,
	"get_status":                ToolCategoryRead,
	"get_subagent_charter":      ToolCategoryWrite,
	"search_messages":           ToolCategoryRead,
	"reconcile_drift":           ToolCategoryWrite,
	"prime_context":             ToolCategoryRead,
	"save_handoff":              ToolCategoryWrite,
	"get_latest_handoff":        ToolCategoryRead,
	"claim_handoff":             ToolCategoryWrite,
	"pop_handoff":               ToolCategoryWrite,
	"save_memo":                 ToolCategoryWrite,
	"save_lesson":               ToolCategoryWrite,
	"save_case":                 ToolCategoryWrite,
	"save_memory":               ToolCategoryWrite,
	"get_memory":                ToolCategoryRead,
	"memory_archive":            ToolCategoryWrite,
	"memory_restore":            ToolCategoryWrite,
	"list_archived_memories":    ToolCategoryRead,
	"memory_history":            ToolCategoryRead,
	"memory_feedback":           ToolCategoryCollab,
	"list_memory_feedback":      ToolCategoryRead,
	"list_memories":             ToolCategoryRead,
	"validate_memory":           ToolCategoryRead,
	"save_doc":                  ToolCategoryWrite,
	"search_docs":               ToolCategoryRead,
	"get_doc":                   ToolCategoryRead,
	"search_web_docs":           ToolCategoryWrite,
	"search_code":               ToolCategoryWrite,
	"get_package_doc":           ToolCategoryWrite,
	"search_stackoverflow":      ToolCategoryWrite,
	"compile_rules":             ToolCategoryWrite,
	"vacuum_database":           ToolCategoryWrite,
	"get_db_stats":              ToolCategoryRead,
	"get_message_context":       ToolCategoryRead,
	"omni_search":               ToolCategoryRead,
	"recall":                    ToolCategoryRead,
	"diagnose_system":           ToolCategoryWrite,
	"list_goals":                ToolCategoryRead,
	"manage_merge_slot":         ToolCategoryWrite,
	"audit_goals":               ToolCategoryCollab,
	"audit_goal":                ToolCategoryCollab,
	"claim_goal":                ToolCategoryCollab,
	"release_goal":              ToolCategoryCollab,
	"report_goal":               ToolCategoryCollab,
	"create_goal":               ToolCategoryCollab,
	"update_goal":               ToolCategoryCollab,
	"certify_goal":              ToolCategoryCollab,
	"supersede_goal":            ToolCategoryCollab,
	"validate_goals":            ToolCategoryRead,
	"reopen_goal":               ToolCategoryCollab,
	"record_feedback":           ToolCategoryCollab,
	"list_feedback":             ToolCategoryRead,
	"resolve_feedback":          ToolCategoryCollab,
	"touch_goal":                ToolCategoryCollab,
	"compare_proposals":         ToolCategoryRead,
	"record_preference":         ToolCategoryWrite,
	"run_gate_pipeline":         ToolCategoryCollab,
	"run_preflight_eval":        ToolCategoryCollab,
	"spawn_crewmate":            ToolCategoryCollab,
	"list_crew":                 ToolCategoryRead,
	"supervise_crew":            ToolCategoryCollab,
	"sync_rules":                ToolCategoryWrite,
	"skill_search":              ToolCategoryRead,
	"skill_load":                ToolCategoryRead,
	"list_skills":               ToolCategoryRead,
	"audit_hygiene":             ToolCategoryRead,
	"clean_caches":              ToolCategoryWrite,
	"emit_lifecycle_event":      ToolCategoryWrite,
	"create_decision":           ToolCategoryCollab,
	"resolve_decision":          ToolCategoryCollab,
	"list_decisions":            ToolCategoryRead,
}

func coreToolDefinitions() []Tool {
	return []Tool{
		{
			Name:        "search_knowledge",
			Description: "Search distilled knowledge, claims, and architectural decisions accumulated from past AI CLI sessions (Devin, OpenCode, Codex, AGY, ZCode, Claude Code, and more). PREFER THIS over reading files or git log when a question is about what was previously decided, built, or discovered — especially across different tools or before this repository existed. Returns attributed claims with file references.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"query":            {Type: "string", Description: "Search terms, topic, or keyword"},
					"project":          {Type: "string", Description: "Optional project id or dir filter"},
					"tool":             {Type: "string", Description: "Optional tool filter (devin, mimocode, opencode, codex, command-code, agy)"},
					"refs_exist_only":  {Type: "boolean", Description: "If true, only return claims whose referenced repo files exist on disk"},
					"verified_only":    {Type: "boolean", Description: "Deprecated alias for refs_exist_only"},
					"facts_only":       {Type: "boolean", Description: "If true, only return promoted, LLM-distilled verified facts"},
					"limit":            {Type: "integer", Description: "Maximum number of results to return (default 10)"},
					"offset":           {Type: "integer", Description: "Number of results to skip (default 0)"},
					"include_archived": {Type: "boolean", Description: "Include archived and superseded memories (default false)"},
				},
				Required: []string{"query"},
			},
		},
		{
			Name:        "list_projects",
			Description: "List indexed project repositories with message, claim, and verified fact counts, with optional substring query and limit.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"query": {Type: "string", Description: "Optional search query to filter by project path or ID"},
					"limit": {Type: "integer", Description: "Maximum number of projects to return (default 20, max 200)"},
				},
			},
		},
		{
			Name:        "get_project_facts",
			Description: "Retrieve all distilled claims/facts for a specific project with pagination support.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"project":         {Type: "string", Description: "Optional project ID or alias (default: active workspace)"},
					"refs_exist_only": {Type: "boolean", Description: "Filter by claims whose referenced files exist on disk"},
					"verified_only":   {Type: "boolean", Description: "Deprecated alias for refs_exist_only"},
					"facts_only":      {Type: "boolean", Description: "Filter by promoted, LLM-distilled facts only"},
					"limit":           {Type: "integer", Description: "Maximum number of results to return (default all)"},
					"offset":          {Type: "integer", Description: "Number of results to skip (default 0)"},
				},
			},
		},
		{
			Name:        "get_status",
			Description: "Get knowledge store health statistics and summary counts.",
			InputSchema: InputSchema{
				Type:       "object",
				Properties: map[string]PropertySchema{},
			},
		},
		{
			Name:        "get_subagent_charter",
			Description: "Create and sign a local subagent continuity charter carrying the parent session, goal, lane, allow paths, and handoff policy.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"parent_session_id": {Type: "string", Description: "Parent session that owns continuity"},
					"subagent_id":       {Type: "string", Description: "Child agent identifier"},
					"goal":              {Type: "string", Description: "Goal slug bound to the parent claim"},
					"lane":              {Type: "string", Description: "Optional lane label"},
					"allow_paths":       {Type: "array", Description: "Repo-relative paths the child may write"},
					"handoff_policy":    {Type: "string", Description: "parent-only (default) or subagent-allowed"},
				},
				Required: []string{"parent_session_id", "subagent_id"},
			},
		},
		{
			Name:        "search_messages",
			Description: "Search the verbatim conversation transcripts of every indexed AI CLI tool on this machine. Use when you need the actual words exchanged in a past session — decisions in context, exact error messages, shell commands that were run — rather than distilled summaries of them.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"query":      {Type: "string", Description: "Search terms or phrase"},
					"project":    {Type: "string", Description: "Optional project id or dir filter"},
					"tool":       {Type: "string", Description: "Optional tool filter (devin, mimocode, opencode, codex, etc.)"},
					"role":       {Type: "string", Description: "Optional message role filter (user, assistant)"},
					"since":      {Type: "string", Description: "Optional timestamp filter in RFC3339 format"},
					"limit":      {Type: "integer", Description: "Maximum number of results to return (default 10)"},
					"offset":     {Type: "integer", Description: "Number of results to skip (default 0)"},
					"two_pass":   {Type: "boolean", Description: "Two-pass asymmetric retrieval (default false)"},
					"window":     {Type: "integer", Description: "Turns in two-pass mode (default 2)"},
					"max_tokens": {Type: "integer", Description: "Optional maximum token budget per unrolled turn (default 800)"},
				},
				Required: []string{"query"},
			},
		},
		{
			Name:        "reconcile_drift",
			Description: "Audit filesystem drift by re-verifying whether referenced repository files exist on disk for stored claims.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"project":  {Type: "string", Description: "Optional project ID or directory slug (empty to reconcile all projects)"},
					"dry_run":  {Type: "boolean", Description: "Optional simulation mode; if true, audits drift without writing updates to SQLite (default: false)"},
					"semantic": {Type: "boolean", Description: "Optional System One semantic verification; evaluates whether referenced code excerpts substantiate or contradict stored claims (default: false)"},
				},
			},
		},
		{
			Name:        "prime_context",
			Description: "Retrieve a session briefing: latest handoff, key facts, directives, and disk grounding. Call this ONCE at the start of a session in a known project.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"project":              {Type: "string", Description: "Optional project ID or directory slug"},
					"max_facts":            {Type: "integer", Description: "Maximum number of key facts to include (default 5)"},
					"budget":               {Type: "integer", Description: "Optional target context token budget for declarative compression and token pruning (e.g. 8000, 16000)"},
					"max_tokens":           {Type: "integer", Description: "Optional target context token budget for deterministic multi-pressure degradation (default: 20000)"},
					"cache_pinned":         {Type: "boolean", Description: "Optional Gobstopper cache-aware prompt pinning; segregates static system rules/tools at the top and dynamic handoff variables at the bottom (default: false)"},
					"include_markdown":     {Type: "boolean", Description: "Optional inclusion of full rendered markdown text within the structured JSON result (default: false; text is returned in content[0].text)"},
					"all_goals":            {Type: "boolean", Description: "Optional inclusion of all completed roadmap goals instead of aggregate count + bounded recent goals (default: false)"},
					"disable_codebase_map": {Type: "boolean", Description: "Optional flag to disable automatic codebase symbol digest generation (default: false)"},
					"codebase_map_chars":   {Type: "integer", Description: "Optional maximum character budget for codebase symbol digest (default: 2000)"},
					"context":              {Type: "object", Description: "Optional task context object ({task, paths[], branch, head, symbols[]}); when present, the briefing includes a bounded task-scoped recall pack placed before the codebase map."},
					"include_archived":     {Type: "boolean", Description: "Include archived and superseded memories in the briefing (default false)"},
					"graph":                {Type: "boolean", Description: "Append a bounded graph summary (default false)"},
				},
			},
		},
		{
			Name:        "save_handoff",
			Description: "Call before ending a session. Save a structured handoff (summary, open tasks, decisions, branch) to pass the baton.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"project":       {Type: "string", Description: "Optional project ID or directory name (default: active workspace)"},
					"summary":       {Type: "string", Description: "Summary of work completed and current state"},
					"open_items":    {Type: "array", Description: "List of open tasks and pending items"},
					"decisions":     {Type: "array", Description: "Key decisions or constraints established"},
					"branch":        {Type: "string", Description: "Active git branch name"},
					"agent":         {Type: "string", Description: "Agent or tool identifier (e.g. claude-code, devin)"},
					"repo_files":    {Type: "array", Description: "Key repository files touched or referenced (alias: files)"},
					"files":         {Type: "array", Description: "Key repository files touched or referenced (alias: repo_files)"},
					"tool_name":     {Type: "string", Description: "Client or tool name (e.g. antigravity, cursor, claude-code)"},
					"client_kind":   {Type: "string", Description: "Client runtime kind (e.g. ai_agent, human, ci_job)"},
					"origin_entity": {Type: "string", Description: "Attribution identity or organization (e.g. org:waqftech, pat:personal, system)"},
				},
				Required: []string{"summary"},
			},
		},
		{
			Name:        "get_latest_handoff",
			Description: "Retrieve the most recent active session handoff for a project.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"project": {Type: "string", Description: "Optional project ID or directory slug (default: active workspace)"},
				},
			},
		},
		{
			Name:        "claim_handoff",
			Description: "Atomically claim an open or pending session handoff via Compare-and-Set (CAS), ensuring parallel agents and subagent swarms do not duplicate work.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"handoff_id": {Type: "string", Description: "The ID of the handoff to claim (e.g. hoff_123456)."},
					"agent_name": {Type: "string", Description: "Name or role of the claiming agent (defaults to caller identity)."},
					"session_id": {Type: "string", Description: "Unique session ID for idempotent retry and claim tracking."},
				},
				Required: []string{"handoff_id"},
			},
		},
		{
			Name:        "pop_handoff",
			Description: "Pop and atomically claim the newest eligible pending handoff for the current working directory and branch.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"project":    {Type: "string", Description: "Optional project ID or directory slug (default: active workspace)"},
					"branch":     {Type: "string", Description: "Optional git branch to scope the handoff baton to."},
					"agent_name": {Type: "string", Description: "Name or role of the claiming agent."},
					"session_id": {Type: "string", Description: "Unique session ID."},
				},
			},
		},
		{
			Name:        "save_memo",
			Description: "Record an explicit architectural directive, decision, or project memo directly into the verified knowledge store. Call immediately when an architectural decision, invariant, or rejected alternative is established.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"project":       {Type: "string", Description: "Optional project ID or directory name (default: active workspace)"},
					"content":       {Type: "string", Description: "Architectural decision or memo content"},
					"agent":         {Type: "string", Description: "Agent or author identifier (default: user)"},
					"repo_files":    {Type: "array", Description: "Referenced repository files (alias: files)"},
					"files":         {Type: "array", Description: "Referenced repository files (alias: repo_files)"},
					"tool_name":     {Type: "string", Description: "Client or tool name (e.g. antigravity, cursor, claude-code)"},
					"client_kind":   {Type: "string", Description: "Client runtime kind (e.g. ai_agent, human, ci_job)"},
					"origin_entity": {Type: "string", Description: "Attribution identity or organization (e.g. org:waqftech, pat:personal, system)"},
					"decision":      {Type: "string", Description: "Optional associated addressed decision identifier (e.g. 'D1')"},
				},
				Required: []string{"content"},
			},
		},
		{
			Name:        "save_lesson",
			Description: "Record a solved problem as a structured lesson the next session or project can recall: symptom, cause, fix, and evidence files. Call this the moment a fix is proven — after debugging, after resolving a build or dependency failure, or after a tooling workaround.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"project":       {Type: "string", Description: "Optional project ID or directory name (default: active workspace)"},
					"symptom":       {Type: "string", Description: "What went wrong, in the words someone would search for"},
					"cause":         {Type: "string", Description: "Root cause, when known"},
					"fix":           {Type: "string", Description: "What actually fixed it"},
					"tags":          {Type: "string", Description: "Comma-separated tags"},
					"files":         {Type: "array", Description: "Evidence files that show the fix (alias: repo_files)"},
					"repo_files":    {Type: "array", Description: "Evidence files that show the fix (alias: files)"},
					"agent":         {Type: "string", Description: "Agent or author identifier (default: user)"},
					"tool_name":     {Type: "string", Description: "Client or tool name (e.g. antigravity, cursor, claude-code)"},
					"client_kind":   {Type: "string", Description: "Client runtime kind (e.g. ai_agent, human, ci_job)"},
					"origin_entity": {Type: "string", Description: "Attribution identity or organization (e.g. org:waqftech, pat:personal, system)"},
				},
				Required: []string{"symptom"},
			},
		},
		{
			Name:        "save_case",
			Description: "Call when introducing a reusable architectural pattern, module seam, or structural design. Record an architectural procedural case: intent, structural approach, key invariants/trade-offs, and evidence files. Call this when establishing an architectural precedent, choosing a design pattern, or making a non-error structural decision.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"project":       {Type: "string", Description: "Optional project ID or directory name (default: active workspace)"},
					"intent":        {Type: "string", Description: "What architectural or design goal was addressed"},
					"approach":      {Type: "string", Description: "The chosen pattern, library, or structural approach"},
					"invariant":     {Type: "string", Description: "Key non-obvious constraint, race hazard, or trade-off"},
					"tags":          {Type: "string", Description: "Comma-separated tags"},
					"files":         {Type: "array", Description: "Evidence files that show the implementation (alias: repo_files)"},
					"repo_files":    {Type: "array", Description: "Evidence files that show the implementation (alias: files)"},
					"agent":         {Type: "string", Description: "Agent or author identifier (default: user)"},
					"tool_name":     {Type: "string", Description: "Client or tool name (e.g. antigravity, cursor, claude-code)"},
					"client_kind":   {Type: "string", Description: "Client runtime kind (e.g. ai_agent, human, ci_job)"},
					"origin_entity": {Type: "string", Description: "Attribution identity or organization (e.g. org:waqftech, pat:personal, system)"},
				},
				Required: []string{"intent"},
			},
		},
		{
			Name:        "save_memory",
			Description: "Persist a strict sila/memory/v1 envelope with typed assertions, evidence locators, relations and idempotency. Use when a decision needs machine-checkable structure beyond a prose memo. Unknown envelope fields are rejected with stable MEMORY_* codes.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"memory":        {Type: "object", Description: "sila/memory/v1 envelope object (a JSON-encoded object string is also accepted)"},
					"project":       {Type: "string", Description: "Optional project ID or directory name (default: active workspace)"},
					"agent":         {Type: "string", Description: "Agent or author identifier (default: user)"},
					"tool_name":     {Type: "string", Description: "Client or tool name (e.g. antigravity, cursor, claude-code)"},
					"client_kind":   {Type: "string", Description: "Client runtime kind (e.g. ai_agent, human, ci_job)"},
					"origin_entity": {Type: "string", Description: "Attribution identity or organization (e.g. org:waqftech, pat:personal, system)"},
				},
				Required: []string{"memory"},
			},
		},
		{
			Name:        "get_memory",
			Description: "Read one typed sila/memory/v1 record by id, including its envelope, authority label, grounding state and supersession status.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"id":      {Type: "string", Description: "Memory record id (mem_...)"},
					"project": {Type: "string", Description: "Optional project ID or directory name"},
				},
				Required: []string{"id"},
			},
		},
		{
			Name:        "list_memories",
			Description: "List typed memory records with kind, recency and supersession filters, plus grounded/authority counts. Superseded records are excluded unless include_superseded is true.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"project":            {Type: "string", Description: "Optional project ID or directory name"},
					"kind":               {Type: "string", Description: "Optional memory kind filter"},
					"since":              {Type: "string", Description: "Optional RFC3339 timestamp or duration (e.g. 24h)"},
					"limit":              {Type: "integer", Description: "Maximum records (default 50, max 500)"},
					"include_superseded": {Type: "boolean", Description: "Include records superseded by a relation"},
				},
			},
		},
		{
			Name:        "validate_memory",
			Description: "Validate an envelope without writing; never mutates the store",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"memory":  {Type: "object", Description: "sila/memory/v1 envelope object (a JSON-encoded object string is also accepted)"},
					"project": {Type: "string", Description: "Optional project ID or directory name used for evidence checks"},
				},
				Required: []string{"memory"},
			},
		},
		{
			Name:        "memory_archive",
			Description: "Archive a current memory with an explicit reason and authority. Archived memories leave recall, prime and default search but stay readable, auditable and restorable. Archive never deletes content; hard delete is refused.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"id":           {Type: "string", Description: "Memory record id (mem_...)"},
					"project":      {Type: "string", Description: "Optional project ID or directory name"},
					"authority":    {Type: "string", Description: "Required authority: user_request, agent_verified, sensitive"},
					"reason":       {Type: "string", Description: "Required reason: wrong, outdated, duplicate, sensitive, superseded, user_requested, other"},
					"note":         {Type: "string", Description: "Optional bounded note (max 500 characters); required for reason other"},
					"duplicate_of": {Type: "string", Description: "Required target memory id when reason is duplicate"},
					"actor":        {Type: "string", Description: "Actor identifier (default agent)"},
				},
				Required: []string{"id", "authority", "reason"},
			},
		},
		{
			Name:        "memory_restore",
			Description: "Restore an archived memory to current by memory id or archive event id. Restoring an already-current memory is an idempotent no-op. Sensitive archives are user-restorable only; agent-verified archives expire after 24 hours for agents; superseded records cannot be restored.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"id":        {Type: "string", Description: "Memory id or archive event id"},
					"project":   {Type: "string", Description: "Optional project ID or directory name"},
					"authority": {Type: "string", Description: "Restore authority: user_request (default) or agent_verified"},
					"actor":     {Type: "string", Description: "Actor identifier (default user)"},
				},
				Required: []string{"id"},
			},
		},
		{
			Name:        "list_archived_memories",
			Description: "List archived and superseded memories with state, reason, authority, actor, restore deadline and restorable flag, plus aggregate counts and hash-chain health. Sensitive archive titles and notes are redacted.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"project": {Type: "string", Description: "Optional project ID or directory name"},
				},
			},
		},
		{
			Name:        "memory_history",
			Description: "Return the append-only, hash-linked lifecycle chain for one memory and optionally verify it. A tampered chain reports the first broken event with MEMORY_CHAIN_BROKEN.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"id":      {Type: "string", Description: "Memory record id (mem_...)"},
					"project": {Type: "string", Description: "Optional project ID or directory name"},
					"verify":  {Type: "boolean", Description: "Verify hashes and chain (default true)"},
				},
				Required: []string{"id"},
			},
		},
		{
			Name:        "memory_feedback",
			Description: "Report whether a memory returned by a specific recall pack helped, was verified, mattered less, was irrelevant, or was wrong. Requires the pack retrieval_id and the memory_id it returned; later signals from the same agent supersede the earlier one. Feedback only adjusts recall ranking inside the memory's authority tier and never edits, archives, or deletes the memory.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"memory_id":    {Type: "string", Description: "Memory record id returned by the retrieval (mem_...)"},
					"retrieval_id": {Type: "string", Description: "Retrieval id reported by recall or prime_context (ret_...)"},
					"signal":       {Type: "string", Description: "Relevance signal: helped, verified, less_important, irrelevant, wrong"},
					"note":         {Type: "string", Description: "Optional bounded note (max 500 characters); secrets and absolute paths are rejected"},
					"agent":        {Type: "string", Description: "Agent identifier (default agent)"},
					"project":      {Type: "string", Description: "Optional project ID or directory name"},
				},
				Required: []string{"memory_id", "retrieval_id", "signal"},
			},
		},
		{
			Name:        "list_memory_feedback",
			Description: "List memory relevance feedback with signal counts, accepted versus superseded rows, bound versus unbound rows, and the most adjusted memories. Archived and superseded history is included; notes are local only.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"project":   {Type: "string", Description: "Optional project ID or directory name"},
					"memory_id": {Type: "string", Description: "Optional memory record id filter"},
					"signal":    {Type: "string", Description: "Optional signal filter: helped, verified, less_important, irrelevant, wrong"},
					"since":     {Type: "string", Description: "Optional RFC3339 timestamp or duration (e.g. 7d)"},
					"limit":     {Type: "integer", Description: "Maximum rows (default 50, max 500)"},
				},
			},
		},
		{
			Name:        "save_doc",
			Description: "Save or attach an external reference document, API guide, or markdown documentation to a project.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"project": {Type: "string", Description: "Optional project ID or directory name (default: active workspace)"},
					"title":   {Type: "string", Description: "Document title or headline"},
					"url":     {Type: "string", Description: "Optional source URL or file path"},
					"content": {Type: "string", Description: "Markdown or text content of the document"},
				},
				Required: []string{"title", "content"},
			},
		},
		{
			Name:        "search_docs",
			Description: "Call BEFORE web search or scaffolding integrations to query indexed specifications and local documentation. Search attached external reference documents and API guides for a project using SQLite FTS5 full-text search.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"query":   {Type: "string", Description: "Search terms or phrase"},
					"project": {Type: "string", Description: "Optional project ID or directory slug"},
					"limit":   {Type: "integer", Description: "Maximum number of results (default 10)"},
					"offset":  {Type: "integer", Description: "Number of results to skip (default 0)"},
				},
				Required: []string{"query"},
			},
		},
		{
			Name:        "get_doc",
			Description: "Retrieve the full text and metadata of a specific attached document by ID.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"id": {Type: "string", Description: "Document ID (e.g. doc_abc123)"},
				},
				Required: []string{"id"},
			},
		},
		{
			Name:        "search_web_docs",
			Description: "Search live online documentation using Google Custom Search and optionally auto-ingest resulting documentation pages into the project store.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"query":     {Type: "string", Description: "Search terms or phrase"},
					"project":   {Type: "string", Description: "Project ID to attach documents to"},
					"top":       {Type: "integer", Description: "Maximum number of search results (default 5)"},
					"auto_save": {Type: "boolean", Description: "If true, fetches and saves full Markdown content for top results"},
				},
				Required: []string{"query"},
			},
		},
		{
			Name:        "search_code",
			Description: "Search open-source code across public repositories on GitHub and optionally auto-ingest resulting code files into the project store.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"query":     {Type: "string", Description: "Code search terms or syntax (e.g. 'sqlite fts5 tokenizer')"},
					"language":  {Type: "string", Description: "Programming language filter (e.g. 'go', 'rust', 'ts', 'py')"},
					"repo":      {Type: "string", Description: "Filter by owner/repo (e.g. 'mattn/go-sqlite3')"},
					"extension": {Type: "string", Description: "Filter by file extension (e.g. 'go', 'rs')"},
					"project":   {Type: "string", Description: "Project ID to attach code docs to"},
					"top":       {Type: "integer", Description: "Maximum number of search results (default 5)"},
					"auto_save": {Type: "boolean", Description: "If true, fetches full raw source files and saves into project memory"},
				},
				Required: []string{"query"},
			},
		},
		{
			Name:        "get_package_doc",
			Description: "Fetch official package and registry documentation for Go (pkg.go.dev), Rust (crates.io/docs.rs), NPM (npmjs.org), or Python (pypi.org) and optionally auto-ingest into project memory.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"name":      {Type: "string", Description: "Package or module name (e.g. 'zod', 'tokio', 'modernc.org/sqlite', 'requests')"},
					"ecosystem": {Type: "string", Description: "Registry ecosystem: auto (default), go, rust, npm, python"},
					"project":   {Type: "string", Description: "Project ID to attach doc to"},
					"auto_save": {Type: "boolean", Description: "If true, saves package doc into project knowledge store"},
				},
				Required: []string{"name"},
			},
		},
		{
			Name:        "search_stackoverflow",
			Description: "Search developer QA, troubleshooting threads, and error resolutions on Stack Overflow and optionally auto-ingest solutions into project memory.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"query":     {Type: "string", Description: "Error message or technical question"},
					"tag":       {Type: "string", Description: "Optional tag filter (e.g. 'sqlite', 'go', 'react')"},
					"project":   {Type: "string", Description: "Project ID to attach solutions to"},
					"top":       {Type: "integer", Description: "Maximum results to return (default 5)"},
					"auto_save": {Type: "boolean", Description: "If true, saves troubleshooting thread into project knowledge store"},
				},
				Required: []string{"query"},
			},
		},
		{
			Name:        "compile_rules",
			Description: "Compile the latest project handoff, architectural memos, distilled facts, file-referenced claims (labelled as claims, never as facts), and reference docs into agent rule files (CLAUDE.md, .cursorrules, AGENTS.md, .windsurfrules).",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"project": {Type: "string", Description: "Optional project ID or directory slug (default: active workspace)"},
					"target":  {Type: "string", Description: "Target format: claude (default), cursor, windsurf, agents"},
				},
			},
		},
		{
			Name:        "vacuum_database",
			Description: "Run SQLite VACUUM and optimize query planner statistics on the knowledge store, defragmenting storage and reclaiming disk space.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"force": {Type: "boolean", Description: "If true, forces a full vacuum even if freelist fragmentation is low"},
				},
			},
		},
		{
			Name:        "get_db_stats",
			Description: "Get detailed SQLite database storage statistics, including file size, page count, freelist pages, and fragmentation percentage.",
			InputSchema: InputSchema{
				Type:       "object",
				Properties: map[string]PropertySchema{},
			},
		},
		{
			Name:        "get_message_context",
			Description: "Expand the verbatim conversation around a specific message ID, staying within its recorded source session. Use after search or omni_search to unroll the turns surrounding a hit — the before/after context that explains WHY something was done. Returns a note instead of misleading neighbors when session provenance is unavailable.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"id":         {Type: "integer", Description: "Message ID to expand"},
					"window":     {Type: "integer", Description: "Turns around the hit (default 3, max 20)"},
					"occurrence": {Type: "integer", Description: "1-based selection among multiple recorded source occurrences of a deduplicated message (default 1)"},
				},
				Required: []string{"id"},
			},
		},
		{
			Name:        "omni_search",
			Description: "Call BEFORE running raw grep, find, or git log. Search every knowledge tier in one query: invariants, claims, transcripts, commits, docs, and handoffs, looking for prior research, decisions, or cross-session history.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"query":            {Type: "string", Description: "Search query or technical keywords"},
					"project":          {Type: "string", Description: "Optional project selector to scope search"},
					"limit":            {Type: "integer", Description: "Maximum results to return (default 15)"},
					"tool":             {Type: "string", Description: "Only return hits attributed to this source tool"},
					"role":             {Type: "string", Description: "Only return transcript hits with this role (user, assistant, commit)"},
					"since":            {Type: "string", Description: "RFC3339 lower bound on result timestamps"},
					"semantic":         {Type: "boolean", Description: "Optional: if true, reranks candidates using TypeSafe System One assistance while preserving evidentiary tiers"},
					"include_archived": {Type: "boolean", Description: "Include archived and superseded memories (default false)"},
				},
				Required: []string{"query"},
			},
		},
		{
			Name:        "recall",
			Description: "Find prior resolutions for a problem before searching the web or trying workarounds. Use it when a command fails, a package misbehaves, or an error appears. Returns lessons first, then grounded claims and memos, then transcript hits with grounding and an unroll command.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"query":            {Type: "string", Description: "Symptom, error message, package name, or API in the words you would search"},
					"project":          {Type: "string", Description: "Optional project selector; ranks its hits first, never filters them out"},
					"limit":            {Type: "integer", Description: "Maximum hits to return (default 5, max 50)"},
					"semantic":         {Type: "boolean", Description: "Optional: rerank within tiers using TypeSafe System One assistance"},
					"max_tokens":       {Type: "integer", Description: "Estimated token budget for the recall pack (256-8192, default 1200)"},
					"context":          {Type: "object", Description: "Optional task context object: {task, paths[], branch, head, symbols[]}. A JSON-encoded object string is also accepted. Unknown fields are rejected."},
					"include_archived": {Type: "boolean", Description: "Include archived and superseded memories (default false)"},
				},
				Required: []string{"query"},
			},
		},
		{
			Name:        "diagnose_system",
			Description: "Run end-to-end system diagnostic health audit on SQLite DB integrity, FTS5 virtual indexes, project path existence on disk, and storage permissions, with optional automatic repair.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"repair": {Type: "boolean", Description: "If true, automatically rebuilds FTS5 indexes and optimizes query planner"},
				},
			},
		},
		{
			Name:        "list_goals",
			Description: "List goal packages in the project's goals/ directory, including titles, descriptions, implementation statuses, and /goal launch commands.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"project":      {Type: "string", Description: "Optional project ID or workspace root directory"},
					"status":       {Type: "string", Description: "Filter goals by status: 'pending', 'claimed' (or 'active'), 'implemented' (or 'done'), 'ready', 'blocked', or 'all'"},
					"pending_only": {Type: "boolean", Description: "Deprecated alias for status='pending'"},
					"tier":         {Type: "string", Description: "Filter goals by tier: 'immediate', 'roadmap', 'nice-to-have', 'wont-fix'"},
					"limit":        {Type: "integer", Description: "Optional maximum number of goals to return"},
					"slug":         {Type: "string", Description: "Optional goal slug under goals/<slug>; required together with task_id to inspect a specific task packet"},
					"task_id":      {Type: "string", Description: "Optional stable task ID (e.g. T-001); required together with slug to return a TaskPacket"},
				},
			},
		},
		{
			Name:        "audit_goals",
			Description: "Audit goals against recent git commits, detecting implemented goals with commit SHAs, and optionally auto-syncing goals/README.md.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"project": {Type: "string", Description: "Optional project ID or workspace root directory"},
					"no_sync": {Type: "boolean", Description: "If true, skip auto-updating goals/README.md"},
				},
			},
		},
		{
			Name:        "audit_goal",
			Description: "Run an epistemic blindfold mystery audit on a goal package to evaluate compliance and certify or reopen it.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"slug":        {Type: "string", Description: "Goal slug under goals/<slug>"},
					"project":     {Type: "string", Description: "Optional project ID or repository directory"},
					"persona":     {Type: "string", Description: "Optional foundation persona ID (defaults to mystery-auditor)"},
					"url":         {Type: "string", Description: "Optional target URL for web service probing"},
					"binary":      {Type: "string", Description: "Optional target CLI binary path"},
					"skip_canary": {Type: "boolean", Description: "If true, skip negative mutant canary testing"},
					"timeout":     {Type: "string", Description: "Optional timeout duration (e.g. 5m, 10m)"},
				},
				Required: []string{"slug"},
			},
		},
		{
			Name:        "claim_goal",
			Description: "Claim and lock a goal package for an agent to prevent other agents from taking the same task.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"slug":        {Type: "string", Description: "Goal slug under goals/<slug>"},
					"agent":       {Type: "string", Description: "Optional agent identifier (e.g. claude-code, devin, antigravity)"},
					"note":        {Type: "string", Description: "Optional initial status or execution note"},
					"force":       {Type: "boolean", Description: "If true, override an existing active claim"},
					"project":     {Type: "string", Description: "Optional project ID or repository directory"},
					"session":     {Type: "string", Description: "Optional session identifier to bind (UUID format recommended)"},
					"allow_paths": {Type: "array", Description: "Optional canonical repo-relative paths authorized for mutation"},
					"deny_paths":  {Type: "array", Description: "Optional inviolable boundary paths"},
					"duration":    {Type: "string", Description: "Optional auto-release lease duration e.g. 2h"},
				},
				Required: []string{"slug"},
			},
		},
		{
			Name:        "release_goal",
			Description: "Release a claimed goal lock, returning the goal to the ready-to-execute pending pool.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"slug":    {Type: "string", Description: "Goal slug under goals/<slug>"},
					"project": {Type: "string", Description: "Optional project ID or repository directory"},
				},
				Required: []string{"slug"},
			},
		},
	}
}

// compactTools lists tools that accept a compact projection.
var compactTools = map[string]bool{"recall": true, "omni_search": true}

func attachCompactFields(tools []Tool) {
	for i := range tools {
		if !compactTools[tools[i].Name] {
			continue
		}
		if tools[i].InputSchema.Properties == nil {
			tools[i].InputSchema.Properties = map[string]PropertySchema{}
		}
		if _, exists := tools[i].InputSchema.Properties["compact"]; !exists {
			tools[i].InputSchema.Properties["compact"] = PropertySchema{Type: "boolean", Description: "Compact, default false"}
		}
	}
}

// asOfTools lists tools that accept point-in-time reads.
var asOfTools = map[string]bool{"recall": true, "search_knowledge": true, "get_project_facts": true}

func attachAsOfFields(tools []Tool) {
	for i := range tools {
		if !asOfTools[tools[i].Name] {
			continue
		}
		if tools[i].InputSchema.Properties == nil {
			tools[i].InputSchema.Properties = map[string]PropertySchema{}
		}
		if _, exists := tools[i].InputSchema.Properties["as_of"]; !exists {
			tools[i].InputSchema.Properties["as_of"] = PropertySchema{Type: "string", Description: "As-of timestamp"}
		}
	}
}
