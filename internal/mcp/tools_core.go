package mcp

import (
	"strings"
	"sync"
)

// PublicToolCount is the documented size of the MCP contract. Tests keep this
// marker synchronized with the executable registry below and public docs.
const PublicToolCount = 76

// ContinuityToolCount is the default agent-workflow surface exposed by
// --profile=continuity.
const ContinuityToolCount = 36

// ContinuityToolNames defines the 32-tool agent-workflow surface for focused
// continuity across orientation, archaeology, memory writes, and goal/gate
// workflows. Administrative, destructive, external-network, and swarm tools
// remain behind --profile=full.
var ContinuityToolNames = map[string]bool{
	// Group 1: Orientation, Routing & Guidebooks (5)
	"salam":                     true,
	"prime_context":             true,
	"route_workflow":            true,
	"get_status":                true,
	"get_operational_guidebook": true,

	// Group 2: Archaeology, Transcript Unroll & Documentation (7)
	"omni_search":         true,
	"search_knowledge":    true,
	"search_messages":     true,
	"get_message_context": true,
	"search_docs":         true,
	"get_doc":             true,
	"recall":              true,

	// Group 3: Active 9-Verb Memory Writes, Handoffs & Grounding (7)
	"save_memo":          true,
	"save_lesson":        true,
	"save_case":          true,
	"save_handoff":       true,
	"get_latest_handoff": true,
	"get_project_facts":  true,
	"reconcile_drift":    true,

	// Group 4: Goal Lifecycle, Quality Gates, Audits, Decisions, Skills & Feedback (13)
	"list_goals":        true,
	"claim_goal":        true,
	"touch_goal":        true,
	"report_goal":       true,
	"certify_goal":      true,
	"audit_goals":       true,
	"audit_goal":        true,
	"run_gate_pipeline": true,
	"list_skills":       true,
	"skill_search":      true,
	"skill_load":        true,
	"list_decisions":    true,
	"record_feedback":   true,

	// Group 5: Temporal, Specialist & Symbol Parity (4)
	"get_activity_digest":       true,
	"consult_personas":          true,
	"save_domain_learning":      true,
	"check_symbol_blast_radius": true,
}

// FilterToolsByProfile filters tools according to the target profile.
// If profile is empty or ProfileFull, all tools are returned.
// Otherwise, only tools in ContinuityToolNames are returned.
func FilterToolsByProfile(tools []Tool, profile string) []Tool {
	if profile == "" || strings.EqualFold(profile, ProfileFull) {
		return tools
	}
	filtered := make([]Tool, 0, len(ContinuityToolNames))
	for _, t := range tools {
		if ContinuityToolNames[t.Name] {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

var (
	coreToolNamesOnce sync.Once
	coreToolNamesMap  map[string]bool
)

// IsCoreTool reports whether name is one of Sila's 63 reserved core MCP tools.
func IsCoreTool(name string) bool {
	coreToolNamesOnce.Do(func() {
		coreToolNamesMap = make(map[string]bool, PublicToolCount)
		for _, t := range ToolDefinitions() {
			coreToolNamesMap[t.Name] = true
		}
	})
	return coreToolNamesMap[name]
}

// ToolDefinitions is the canonical, executable MCP public contract.
func ToolDefinitions() []Tool {
	tools := append(coreToolDefinitions(), opsToolDefinitions()...)
	for i := range tools {
		tools[i].Category = toolCategories[tools[i].Name]
	}
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
					"query":           {Type: "string", Description: "Search terms, topic, or keyword"},
					"project":         {Type: "string", Description: "Optional project id or dir filter"},
					"tool":            {Type: "string", Description: "Optional tool filter (devin, mimocode, opencode, codex, command-code, agy)"},
					"refs_exist_only": {Type: "boolean", Description: "If true, only return claims whose referenced repo files exist on disk"},
					"verified_only":   {Type: "boolean", Description: "Deprecated alias for refs_exist_only"},
					"facts_only":      {Type: "boolean", Description: "If true, only return promoted, LLM-distilled verified facts"},
					"limit":           {Type: "integer", Description: "Maximum number of results to return (default 10)"},
					"offset":          {Type: "integer", Description: "Number of results to skip (default 0)"},
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
					"two_pass":   {Type: "boolean", Description: "Optional: if true, executes two-pass asymmetric retrieval and returns resolved conversation exchange pairs (default false)"},
					"window":     {Type: "integer", Description: "Optional surrounding turns to retrieve in two-pass mode (+/- turns, default 2)"},
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
			Description: "Retrieve a session briefing for the current project: latest handoff with open tasks, key architectural facts, candidate directives, and disk grounding. Call this ONCE at the start of a session in a known project instead of reconstructing context from files — it is the machine-readable equivalent of `sila prime`.",
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
				},
			},
		},
		{
			Name:        "save_handoff",
			Description: "Call before ending a session or handing work to another agent. Save a structured session handoff record (summary, open tasks, decisions, branch) to pass the baton cleanly to the next agent session.",
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
					"window":     {Type: "integer", Description: "Number of preceding and following turns to retrieve (default 3, max 20)"},
					"occurrence": {Type: "integer", Description: "1-based selection among multiple recorded source occurrences of a deduplicated message (default 1)"},
				},
				Required: []string{"id"},
			},
		},
		{
			Name:        "omni_search",
			Description: "Call BEFORE running raw grep, find, or git log when looking for prior research, decisions, lessons, docs, or cross-session history. One query across every knowledge tier: architectural invariants, distilled claims, raw conversation transcripts, git commits, and attached docs. The broadest recall tool for questions about past work, prior sessions, or anything an AI CLI did on this machine — use it when a targeted search returns nothing or when the right source tier is unknown.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"query":    {Type: "string", Description: "Search query or technical keywords"},
					"project":  {Type: "string", Description: "Optional project selector to scope search"},
					"limit":    {Type: "integer", Description: "Maximum results to return (default 15)"},
					"tool":     {Type: "string", Description: "Only return hits attributed to this source tool"},
					"role":     {Type: "string", Description: "Only return transcript hits with this role (user, assistant, commit)"},
					"since":    {Type: "string", Description: "RFC3339 lower bound on result timestamps"},
					"semantic": {Type: "boolean", Description: "Optional: if true, reranks candidates using TypeSafe System One assistance while preserving evidentiary tiers"},
				},
				Required: []string{"query"},
			},
		},
		{
			Name:        "recall",
			Description: "Find prior resolutions for a problem before searching the web or trying workarounds. Use it when a command fails, a package misbehaves, an API is unfamiliar, or an error message appears. Returns deliberate lessons first, then grounded claims and memos, then transcript hits, each with project, age, grounding, and an unroll command.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"query":    {Type: "string", Description: "Symptom, error message, package name, or API in the words you would search"},
					"project":  {Type: "string", Description: "Optional project selector; ranks its hits first, never filters them out"},
					"limit":    {Type: "integer", Description: "Maximum hits to return (default 5)"},
					"semantic": {Type: "boolean", Description: "Optional: rerank within tiers using TypeSafe System One assistance"},
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
