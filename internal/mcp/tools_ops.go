package mcp

func opsToolDefinitions() []Tool {
	return []Tool{
		{
			Name:        "report_goal",
			Description: "Report intermediate progress or update status note for an in-progress goal.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"slug":            {Type: "string", Description: "Goal slug under goals/<slug>"},
					"note":            {Type: "string", Description: "Progress report or status update note"},
					"status":          {Type: "string", Description: "Optional execution status (e.g. in_progress, evaluating, blocked)"},
					"project":         {Type: "string", Description: "Optional project ID or repository directory"},
					"tool_name":       {Type: "string", Description: "Client or tool name (e.g. antigravity, cursor, claude-code)"},
					"client_kind":     {Type: "string", Description: "Client runtime kind (e.g. ai_agent, human, ci_job)"},
					"origin_entity":   {Type: "string", Description: "Attribution identity or organization (e.g. org:waqftech, pat:personal, system)"},
					"task_checkpoint": {Type: "object", Description: "Optional structured task checkpoint conforming to sila/task-checkpoint/v1"},
				},
				Required: []string{"slug"},
			},
		},
		{
			Name:        "create_goal",
			Description: "Create and scaffold a new goal package under goals/<slug>/ with goal.md, facts.md, and plan.md conforming to repository standards.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"slug":           {Type: "string", Description: "Kebab-case goal slug (e.g. user-roles-and-permissions)"},
					"title":          {Type: "string", Description: "Optional human-readable goal title"},
					"description":    {Type: "string", Description: "Optional summary of what this goal accomplishes"},
					"depends_on":     {Type: "array", Description: "Optional array of goal slugs that must be completed first"},
					"independent":    {Type: "boolean", Description: "Explicitly declare whether goal is disjoint/independent (default: true if no depends_on)"},
					"sequence":       {Type: "integer", Description: "Optional recommended sequence index (e.g. 1, 2, 3)"},
					"files_to_touch": {Type: "array", Description: "Optional array of file or directory paths this goal will modify (used for concurrency collision prevention)"},
					"overwrite":      {Type: "boolean", Description: "If true, overwrite existing goal files"},
					"shape":          {Type: "string", Description: "Task shape: 'ship' (code modification, default) or 'scout' (non-mutating investigation report). Scout tasks are exempt from file collision checks."},
					"tier":           {Type: "string", Description: "Optional goal tier: 'immediate', 'roadmap', 'nice-to-have', 'wont-fix' (default: 'roadmap')"},
					"invariants":     {Type: "array", Description: "Optional array of architectural invariants and constraints for facts.md"},
					"tasks":          {Type: "array", Description: "Optional array of discrete execution tasks or phases for plan.md"},
					"status":         {Type: "string", Description: "Optional goal status: 'pending' (default) or 'implemented' for retrospective goal recording"},
					"commit":         {Type: "string", Description: "Optional commit SHA when recording a retrospective goal (status='implemented')"},
					"strict":         {Type: "boolean", Description: "If true, reject goal creation if any file collisions or topological conflicts are detected"},
					"project":        {Type: "string", Description: "Optional project ID or repository directory"},
					"work_package":   {Type: "object", Description: "Optional structured work_package object conforming to sila/work-package/v1"},
					"spec_review":    {Type: "object", Description: "Optional structured spec_review object conforming to sila/spec-review/v1"},
				},
				Required: []string{"slug"},
			},
		},
		{
			Name:        "reopen_goal",
			Description: "Safely reopen an implemented goal package for remediation, clearing prior completion state and returning it to pending or claimed.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"slug":    {Type: "string", Description: "Goal slug under goals/<slug>"},
					"reason":  {Type: "string", Description: "Reason or remediation explanation"},
					"agent":   {Type: "string", Description: "Optional agent requesting reopening"},
					"claim":   {Type: "boolean", Description: "If true, immediately claim the goal for remediation"},
					"project": {Type: "string", Description: "Optional project ID or repository directory"},
				},
				Required: []string{"slug"},
			},
		},
		{
			Name:        "record_feedback",
			Description: "Submit agent observations, recommendations, issues faced, tips, friction/challenges, or praise to help continuously improve sila.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"id":       {Type: "string", Description: "Optional explicit epistemic ticket ID or custom slug (e.g. dialectic-subshell-paths or fb_dialectic-subshell-paths)"},
					"content":  {Type: "string", Description: "Feedback content, recommendation, challenge, or message"},
					"category": {Type: "string", Description: "Feedback category: idea, issue, tip, challenge, praise (default: idea)"},
					"project":  {Type: "string", Description: "Optional project ID or repository name"},
					"agent":    {Type: "string", Description: "Optional agent or tool identifier"},
					"fact_id":  {Type: "string", Description: "Optional fact ID to adjust retention salience for (helpful, not_helpful, stale, wrong)"},
					"share":    {Type: "boolean", Description: "Optional flag to anonymously share sanitized feedback with Sila maintainer via Cloudflare D1 edge"},
				},
				Required: []string{"content"},
			},
		},
		{
			Name:        "list_feedback",
			Description: "Retrieve recorded agent feedback, suggestions, issues, and tips across projects for periodic review.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"project":  {Type: "string", Description: "Optional project ID filter"},
					"category": {Type: "string", Description: "Optional category filter: idea, issue, tip, challenge, praise"},
					"status":   {Type: "string", Description: "Optional status filter: open, closed, in_progress, wontfix"},
					"limit":    {Type: "integer", Description: "Maximum records to return (default: 50)"},
				},
			},
		},
		{
			Name:        "resolve_feedback",
			Description: "Mark an agent feedback ticket as resolved and closed with optional goal attribution and resolution notes.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"id":    {Type: "string", Description: "Feedback record ID (e.g. fb_...)"},
					"agent": {Type: "string", Description: "Optional agent resolving the feedback"},
					"goal":  {Type: "string", Description: "Optional goal slug that implemented/resolved this feedback"},
					"note":  {Type: "string", Description: "Optional explanation or resolution summary"},
				},
				Required: []string{"id"},
			},
		},
		{
			Name:        "touch_goal",
			Description: "Renew the claim lease and heartbeat timestamp for an active goal without requiring long-running process persistence.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"slug":    {Type: "string", Description: "Goal slug under goals/<slug>"},
					"agent":   {Type: "string", Description: "Optional agent name renewing the lease"},
					"project": {Type: "string", Description: "Optional project ID or repository directory"},
				},
				Required: []string{"slug"},
			},
		},
		{
			Name:        "compare_proposals",
			Description: "Evaluate 2 or more competing architectural proposals or feature specs in an empirical tournament against project non-goals, invariants, and historical preferences.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"proposals":     {Type: "array", Description: "Array of proposal statements, architecture designs, or plan texts (minimum 2)"},
					"project":       {Type: "string", Description: "Optional project ID or directory name"},
					"context_query": {Type: "string", Description: "Optional background question or architectural decision topic"},
				},
				Required: []string{"proposals"},
			},
		},
		{
			Name:        "record_preference",
			Description: "Record an empirical pairwise preference decision (winner vs rejected candidate with rationale) to train and ground future proposal tournaments.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"candidate_a":   {Type: "string", Description: "First candidate proposal statement or diff"},
					"candidate_b":   {Type: "string", Description: "Second candidate proposal statement or diff"},
					"winner":        {Type: "string", Description: "Winner: 'A', 'B', or 'TIE'"},
					"rationale":     {Type: "string", Description: "Trade-off justification or architectural rationale for this choice"},
					"project":       {Type: "string", Description: "Optional project ID or directory"},
					"context_query": {Type: "string", Description: "Optional background topic or decision context"},
					"agent":         {Type: "string", Description: "Optional agent identifier"},
					"decision":      {Type: "string", Description: "Optional associated addressed decision identifier (e.g. 'D1')"},
				},
				Required: []string{"candidate_a", "candidate_b", "winner"},
			},
		},
		{
			Name:        "run_gate_pipeline",
			Description: "Execute the sila quality gate validation in an isolated disposable Git worktree, testing the specified branch against tests, changelog, binary, and documentation invariants without touching the working directory, with optional forwarding to a remote target.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"branch":         {Type: "string", Description: "Optional git branch or ref to validate (default: current HEAD/branch)"},
					"forward_target": {Type: "string", Description: "Optional remote repository target to forward push to upon successful validation (e.g. 'origin')"},
					"auto_forward":   {Type: "boolean", Description: "If true, forward push to forward_target upon 100% green gate checks (default: false)"},
					"project":        {Type: "string", Description: "Optional project ID or repository directory"},
				},
			},
		},
		{
			Name:        "run_preflight_eval",
			Description: "Run epistemic preflight evaluation or context ablation test suite before landing changes, validating against deterministic goal contracts and test suites across harnesses.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"eval_dir":    {Type: "string", Description: "Path to eval suite directory (default: ./evals or .)"},
					"case_name":   {Type: "string", Description: "Optional filter for a specific test case name or slug"},
					"tag":         {Type: "string", Description: "Optional tag filter (e.g. safety, regression)"},
					"target_rule": {Type: "string", Description: "Optional rule or context file for ablation comparison (With vs. Without)"},
					"harness":     {Type: "string", Description: "Harness runner: auto, claude, agy, mock (default: mock for instant in-process preflight, or auto)"},
					"ablation":    {Type: "boolean", Description: "If true, run ablation comparison to evaluate marginal impact and Token ROI"},
					"project":     {Type: "string", Description: "Optional project ID or repository directory (defaults to active workspace)"},
				},
			},
		},
		{
			Name:        "spawn_crewmate",
			Description: "Spawn a crewmate agent in a disposable Git worktree with a tmux session window. The crewmate is bound to a goal package and auto-seeded with sila context. Zero-token supervision via OS process probes.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"harness":   {Type: "string", Description: "AI CLI tool to spawn: devin, claude, agy, opencode, codex, mimocode"},
					"goal_slug": {Type: "string", Description: "Goal package slug the crewmate will work on"},
					"base_ref":  {Type: "string", Description: "Optional git ref to branch from (default: HEAD)"},
					"project":   {Type: "string", Description: "Optional project ID or repository directory"},
				},
				Required: []string{"harness", "goal_slug"},
			},
		},
		{
			Name:        "list_crew",
			Description: "List all active crewmates with zero-token liveness status (running, idle, completed, failed). Uses OS process probes, not LLM API calls.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"project": {Type: "string", Description: "Optional project ID or repository directory"},
				},
			},
		},
		{
			Name:        "supervise_crew",
			Description: "Run zero-token supervision pass over all crewmates, probing liveness via OS process checks and pane activity. When sentinel is enabled, executes dual-cron supervision with isolated Victory Auditor and bounded retries.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"project":  {Type: "string", Description: "Optional project ID or repository directory"},
					"sentinel": {Type: "boolean", Description: "Enable Sentinel dual-cron supervision with isolated Victory Auditor and bounded retries"},
				},
			},
		},
		{
			Name:        "sync_rules",
			Description: "Compile Core Hub agent standards, Universal Principles Pack, and project overlays, projecting updated rule files (AGENTS.md, CLAUDE.md, .cursor/rules/000-sila-core.mdc, .cursor/rules/000-sila-principles.mdc, .windsurfrules, .github/copilot-instructions.md, .devin/setup.sh) across all detected tool targets.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"project":            {Type: "string", Description: "Optional project ID or repository directory"},
					"tools":              {Type: "array", Description: "Optional list of tools to compile rules for (claude, cursor, devin, windsurf, copilot, agents, all)"},
					"include_principles": {Type: "boolean", Description: "Include Universal Principles Pack (Layered Constitution & 9-verb memory loop)"},
					"stack":              {Type: "string", Description: "Optional target stack for tailored principles (go, ts, cloudflare, python, rust, polyglot)"},
					"replace":            {Type: "boolean", Description: "Authorize replacement of user-owned files"},
					"policy_pack":        {Type: "string", Description: "Optional policy pack name (e.g. maintainer)"},
				},
			},
		},
		{
			Name:        "salam",
			Description: "First-contact orientation briefing for agents starting work in a repository: provides active workspace identity, the 7-step working loop, live next steps, ready goals, and dual CLI/MCP runbook.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"project": {Type: "string", Description: "Optional project ID or repository directory (defaults to active workspace)"},
				},
			},
		},
		{
			Name:        "get_operational_guidebook",
			Description: "Call to load Sila's domain engineering standards and guardrails across 10 domains. Retrieve curated cross-project operational learnings, battle-tested engineering patterns, and failure postmortems categorized by domain (golang, typescript, cloudflare, d1, devops, ci/cd, security, testing, a11y).",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"domain":  {Type: "string", Description: "Optional domain filter: golang, typescript, cloudflare, d1, devops, ci/cd, security, testing, a11y"},
					"format":  {Type: "string", Description: "Output format: markdown (default), json, html, toon, or summary"},
					"project": {Type: "string", Description: "Optional project ID or directory filter"},
					"search":  {Type: "string", Description: "Optional keyword search filter across titles, symptoms, causes, fixes, and rules (alias: query)"},
					"query":   {Type: "string", Description: "Optional keyword search filter across titles, symptoms, causes, fixes, and rules (alias: search)"},
				},
			},
		},
		{
			Name:        "route_workflow",
			Description: "Call when unsure which Sila command, persona, audit, or skill applies to the user's request. Advisory System-1 tool chain and workflow routing: recommends optimal multi-tool Sila sequences tailored to developer objectives (onboarding, investigation, goal execution, gate verification, or drift reconciliation).",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"objective":    {Type: "string", Description: "The developer's current objective, intent, or task description"},
					"current_tool": {Type: "string", Description: "Optional current or last executed tool name to inform chain progression"},
					"project":      {Type: "string", Description: "Optional project ID or repository directory"},
				},
				Required: []string{"objective"},
			},
		},
		{
			Name:        "request_approval",
			Description: "Pause execution and request interactive human approval via configured gateway channels (Telegram, Discord, CLI) before proceeding with high-blast-radius or sensitive operations.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"prompt":          {Type: "string", Description: "Clear explanation of the action requiring human approval"},
					"project":         {Type: "string", Description: "Optional project ID or repository directory"},
					"risk_level":      {Type: "string", Description: "Risk level: 'low', 'medium', 'high', or 'critical' (default: 'medium')"},
					"timeout_seconds": {Type: "integer", Description: "Maximum time in seconds to wait for approval before timing out (default: 300, max: 3600)"},
				},
				Required: []string{"prompt"},
			},
		},
		{
			Name:        "go_symbol_references",
			Description: "Find all usages, call sites, and declarations of a Go symbol using gopls or pure-Go AST navigation.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"file":    {Type: "string", Description: "Path to Go source file containing or referencing the symbol"},
					"symbol":  {Type: "string", Description: "Symbol or identifier name to search for (e.g. 'ApprovalCoordinator')"},
					"dir":     {Type: "string", Description: "Optional package or workspace directory to search (defaults to file's directory)"},
					"project": {Type: "string", Description: "Optional project ID or repository directory"},
				},
				Required: []string{"file", "symbol"},
			},
		},
		{
			Name:        "go_package_api",
			Description: "Inspect exported Go package types, interfaces, methods, functions, and documentation signatures using gopls or native AST parsing.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"pkg":                {Type: "string", Description: "Go package directory or import path (e.g. './internal/store')"},
					"include_unexported": {Type: "boolean", Description: "If true, also include unexported declarations (default: false)"},
					"project":            {Type: "string", Description: "Optional project ID or repository directory"},
				},
				Required: []string{"pkg"},
			},
		},
		{
			Name:        "go_diagnostics",
			Description: "Run gopls compiler and vet diagnostics across target package or directory without modifying code.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"path":    {Type: "string", Description: "Package pattern or directory path to check (default: './...')"},
					"tags":    {Type: "string", Description: "Optional Go build tags (e.g. 'integration')"},
					"project": {Type: "string", Description: "Optional project ID or repository directory"},
				},
			},
		},
		{
			Name:        "cloudflare_d1_query",
			Description: "Execute SQL query or schema inspection against a Cloudflare D1 database with parameter support and safety sentry.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"query":    {Type: "string", Description: "SQL query to execute against the D1 database (e.g. 'SELECT * FROM messages LIMIT 10')"},
					"database": {Type: "string", Description: "Optional D1 database ID or database name (defaults to configured or detected database)"},
					"params":   {Type: "array", Description: "Optional positional SQL parameters to bind to placeholders"},
					"project":  {Type: "string", Description: "Optional project ID or repository directory for local wrangler resolution"},
				},
				Required: []string{"query"},
			},
		},
		{
			Name:        "cloudflare_worker_status",
			Description: "Inspect Cloudflare Worker deployment status, active script version, routes, compatibility date, and declared bindings.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"script_name": {Type: "string", Description: "Worker script name (defaults to name detected from wrangler.jsonc or 'sila-edge-bridge')"},
					"project":     {Type: "string", Description: "Optional project ID or repository directory for local wrangler resolution"},
				},
			},
		},
		{
			Name:        "cloudflare_edge_telemetry",
			Description: "Retrieve edge runtime telemetry, request volume, error counts, and CPU execution time percentiles (p50, p99).",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"script_name": {Type: "string", Description: "Worker script name (defaults to name detected from wrangler.jsonc)"},
					"time_range":  {Type: "string", Description: "Time window for metrics aggregation: '1h', '24h', '7d' (default: '24h')"},
					"project":     {Type: "string", Description: "Optional project ID or repository directory"},
				},
			},
		},
		{
			Name:        "skill_search",
			Description: "Search available agent skills across project and global libraries by keyword, technology, or task description. Returns bounded skill summaries with source provenance without loading full instruction bodies.",
			Category:    ToolCategoryRead,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"query":   {Type: "string", Description: "Search terms, topic, skill name, technology, or task intent"},
					"tags":    {Type: "array", Description: "Optional tags or category filters to narrow search results"},
					"limit":   {Type: "integer", Description: "Maximum number of skill summaries to return (default 10, max 50)"},
					"project": {Type: "string", Description: "Optional project directory or ID (default: active workspace)"},
				},
				Required: []string{"query"},
			},
		},
		{
			Name:        "skill_load",
			Description: "Retrieve complete instructions, metadata, and optional resources (scripts, references) for a specific skill from project or global catalogs without persistent state mutation.",
			Category:    ToolCategoryRead,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"slug":              {Type: "string", Description: "Exact skill slug or identifier to load"},
					"include_resources": {Type: "boolean", Description: "If true, include contents of referenced scripts and documentation files (default false)"},
					"include_scripts":   {Type: "boolean", Description: "If true, include contents of referenced scripts and documentation files (alias for include_resources)"},
					"project":           {Type: "string", Description: "Optional project directory or ID (default: active workspace)"},
				},
				Required: []string{"slug"},
			},
		},
		{
			Name:        "list_skills",
			Description: "List available agent skills in compact summary form (slug, name, 1-sentence prompt trigger, token estimate) for progressive context disclosure without inlining full runbooks.",
			Category:    ToolCategoryRead,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"filter":  {Type: "string", Description: "Optional filter string to match against skill slugs, names, or triggers"},
					"project": {Type: "string", Description: "Optional project directory or ID (default: active workspace)"},
				},
			},
		},
		{
			Name:        "audit_hygiene",
			Description: "Audit multi-toolchain disk storage, caches, and repository build residue across Go, pnpm, aube, npm, Cargo, Python (uv/pip), Bun, Zig, and mise. Returns reclaimable bytes and cleanable items without deleting anything.",
			Category:    ToolCategoryRead,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"scope":   {Type: "string", Description: "Cleanup scope: repo (repository residue), global (user caches/package stores), or all (default)"},
					"targets": {Type: "array", Description: "Optional list of toolchain targets to inspect (e.g. ['go', 'pnpm', 'aube'])"},
					"heavy":   {Type: "boolean", Description: "If true, include heavy network-dependent caches (e.g. modcache, node_modules)"},
					"project": {Type: "string", Description: "Optional project directory or ID (default: active workspace)"},
				},
			},
		},
		{
			Name:        "clean_caches",
			Description: "Clean multi-toolchain build caches, package stores, and repository residue across Go, pnpm, aube, npm, Cargo, Python (uv/pip), Bun, Zig, and mise. Safely reclaims disk space.",
			Category:    ToolCategoryWrite,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"scope":   {Type: "string", Description: "Cleanup scope: repo (repository residue), global (user caches/package stores), or all (default)"},
					"targets": {Type: "array", Description: "Optional list of toolchain targets to clean (e.g. ['go', 'pnpm', 'aube'])"},
					"heavy":   {Type: "boolean", Description: "If true, include heavy network-dependent caches (e.g. modcache, node_modules)"},
					"dry_run": {Type: "boolean", Description: "If true, simulate cleanup without deleting any files"},
					"project": {Type: "string", Description: "Optional project directory or ID (default: active workspace)"},
				},
			},
		},
		{
			Name:        "emit_lifecycle_event",
			Description: "Emit an active agent lifecycle transition event (session-start, user-prompt, pre-tool-use, post-tool-use, pre-compact, post-compaction, stop, session-end) into Sila's non-blocking spooler (<5ms latency) for durable provenance without interactive hot-loop stalling.",
			Category:    ToolCategoryWrite,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"event":      {Type: "string", Description: "Canonical lifecycle event kind (e.g. session-start, user-prompt, pre-tool-use, post-tool-use, pre-compact, post-compaction, stop, session-end)"},
					"agent":      {Type: "string", Description: "Emitting agent or tool identifier (e.g. antigravity, claude-code, devin)"},
					"session_id": {Type: "string", Description: "Agent session or conversation identifier"},
					"cwd":        {Type: "string", Description: "Working directory context"},
					"project":    {Type: "string", Description: "Optional project ID or directory slug"},
					"payload":    {Type: "object", Description: "JSON payload containing event arguments, tool execution details, or error context"},
					"drain":      {Type: "boolean", Description: "If true, immediately drains the spool queue into the SQLite store (default: false)"},
				},
				Required: []string{"event"},
			},
		},
		{
			Name:        "create_decision",
			Description: "Create an addressed epistemic decision record (D<number>) with lettered trade-off options and explicit recommendation.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"title":          {Type: "string", Description: "Title or core question of the decision"},
					"options":        {Type: "array", Description: "List of lettered options (minimum 2). Array of strings (e.g. ['A: Option 1', 'B: Option 2']) or option objects"},
					"recommendation": {Type: "string", Description: "Explicit recommended option letter (e.g. 'A')"},
					"rationale":      {Type: "string", Description: "Rationale or justification for the recommendation"},
					"context":        {Type: "string", Description: "Architectural context or motivation"},
					"goal":           {Type: "string", Description: "Associated goal slug"},
					"project":        {Type: "string", Description: "Target project ID (defaults to cwd project)"},
					"address":        {Type: "string", Description: "Optional explicit address (e.g. 'D1') for testing or reservation"},
				},
				Required: []string{"title", "options", "recommendation"},
			},
		},
		{
			Name:        "resolve_decision",
			Description: "Record an in-place resolution for an open addressed decision (D<number>).",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"address":     {Type: "string", Description: "Decision address (e.g. 'D1')"},
					"choice":      {Type: "string", Description: "Chosen option letter (e.g. 'A') or resolution summary"},
					"notes":       {Type: "string", Description: "Resolution notes or rationale"},
					"resolved_by": {Type: "string", Description: "Name or role of resolver"},
					"project":     {Type: "string", Description: "Target project ID (defaults to cwd project)"},
				},
				Required: []string{"address", "choice"},
			},
		},
		{
			Name:        "list_decisions",
			Description: "List addressed epistemic decisions (D<number>) with optional status or goal filter.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"project": {Type: "string", Description: "Target project ID (defaults to cwd project)"},
					"status":  {Type: "string", Description: "Filter by status: open, resolved, superseded, abandoned, or all (default all)"},
					"goal":    {Type: "string", Description: "Filter by associated goal slug"},
					"limit":   {Type: "integer", Description: "Maximum decisions to return"},
				},
			},
		},
		{
			Name:        "update_goal",
			Description: "Update an existing goal's metadata, dependencies, files to touch, shape, tier, personas, or spec review with preflight validation.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"slug":           {Type: "string", Description: "Goal slug under goals/<slug>"},
					"title":          {Type: "string", Description: "New human-readable title"},
					"description":    {Type: "string", Description: "New goal description"},
					"depends_on":     {Type: "array", Description: "Replace dependency list with these goal slugs"},
					"add_deps":       {Type: "array", Description: "Add these goal slugs as dependencies"},
					"remove_deps":    {Type: "array", Description: "Remove these goal slugs from dependencies"},
					"independent":    {Type: "boolean", Description: "Explicitly mark the goal independent or dependent"},
					"sequence":       {Type: "integer", Description: "Recommended sequence index"},
					"files_to_touch": {Type: "array", Description: "Replace the goal's FilesToTouch set"},
					"shape":          {Type: "string", Description: "Task shape: ship or scout"},
					"tier":           {Type: "string", Description: "Goal tier: immediate, roadmap, nice-to-have, wont-fix"},
					"personas":       {Type: "array", Description: "Replace persona list"},
					"add_personas":   {Type: "array", Description: "Add personas"},
					"clear_personas": {Type: "boolean", Description: "Clear all personas"},
					"strict":         {Type: "boolean", Description: "Fail on preflight warnings or file collisions"},
					"spec_review":    {Type: "object", Description: "Structured spec_review object conforming to sila/spec-review/v1"},
					"project":        {Type: "string", Description: "Optional project ID or repository directory"},
				},
				Required: []string{"slug"},
			},
		},
		{
			Name:        "certify_goal",
			Description: "Certify a goal from evidence, or run its verification contract automatically with auto_run.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"slug":     {Type: "string", Description: "Goal slug under goals/<slug>"},
					"evidence": {Type: "string", Description: "Evidence file path (JSON facts or markdown review with Verdict: PASS)"},
					"agent":    {Type: "string", Description: "Auditor identity (must differ from producer/claim holder)"},
					"note":     {Type: "string", Description: "Optional certification note"},
					"auto_run": {Type: "boolean", Description: "Run the goal's verification contract before certifying"},
					"project":  {Type: "string", Description: "Optional project ID or repository directory"},
				},
				Required: []string{"slug"},
			},
		},
		{
			Name:        "supersede_goal",
			Description: "Mark a goal superseded by another goal, release, or commit, releasing its claim and charter.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"slug":          {Type: "string", Description: "Goal slug to supersede"},
					"superseded_by": {Type: "string", Description: "Superseding goal slug, release, or commit"},
					"agent":         {Type: "string", Description: "Agent recording the supersede"},
					"reason":        {Type: "string", Description: "Reason for superseding"},
					"project":       {Type: "string", Description: "Optional project ID or repository directory"},
				},
				Required: []string{"slug"},
			},
		},
		{
			Name:        "validate_goals",
			Description: "Run deterministic goal package, dependency, lifecycle, and registry validation.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"slug":          {Type: "string", Description: "Optional single goal slug to validate"},
					"strict":        {Type: "boolean", Description: "Treat warnings as validation failures"},
					"manifest_path": {Type: "string", Description: "Optional expected-state manifest path"},
					"project":       {Type: "string", Description: "Optional project ID or repository directory"},
				},
			},
		},
		{
			Name:        "get_activity_digest",
			Description: "Summarize recent activity and time-windowed progress across sessions (equivalent to sila standup / sila digest).",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"project":  {Type: "string", Description: "Optional project ID or repository directory"},
					"since":    {Type: "string", Description: "Time window: 24h, 5h, 2d, 1w, RFC3339, or YYYY-MM-DD (default 24h)"},
					"activity": {Type: "boolean", Description: "Return the source-attributed recent session activity list instead of the digest report"},
					"tool":     {Type: "string", Description: "Optional harness filter for the activity list"},
					"top":      {Type: "integer", Description: "Maximum activity rows (default 10)"},
				},
			},
		},
		{
			Name:        "consult_personas",
			Description: "List, recommend, or bind specialist personas for a goal (equivalent to sila persona / sila hire).",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"action":   {Type: "string", Description: "list (default), recommend, or bind_goal"},
					"domain":   {Type: "string", Description: "Optional domain filter"},
					"group":    {Type: "string", Description: "Optional persona group slug or number"},
					"goal":     {Type: "string", Description: "Goal slug for recommend or bind_goal"},
					"personas": {Type: "array", Description: "Persona IDs to bind to the goal"},
					"project":  {Type: "string", Description: "Optional project ID or repository directory"},
				},
			},
		},
		{
			Name:        "save_domain_learning",
			Description: "Persist a grounded domain guardrail learning that pairs with get_operational_guidebook.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"domain":  {Type: "string", Description: "Domain such as golang, cloudflare, d1, security, testing, a11y"},
					"title":   {Type: "string", Description: "Short learning title"},
					"symptom": {Type: "string", Description: "Observable symptom or failure mode"},
					"cause":   {Type: "string", Description: "Root cause, when known"},
					"fix":     {Type: "string", Description: "What fixed it"},
					"rule":    {Type: "string", Description: "Optional durable guardrail rule"},
					"tags":    {Type: "array", Description: "Optional tags"},
					"files":   {Type: "array", Description: "Optional evidence files"},
					"project": {Type: "string", Description: "Optional project ID or repository directory"},
				},
				Required: []string{"domain", "title", "symptom", "fix"},
			},
		},
		{
			Name:        "check_symbol_blast_radius",
			Description: "Run an in-memory edit-check or safe-delete contract analysis for a symbol (equivalent to sila edit-check / safe-delete).",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"symbol":            {Type: "string", Description: "Symbol name or canonical pkg::Sym identifier"},
					"mode":              {Type: "string", Description: "edit_check (default) or safe_delete"},
					"candidate_payload": {Type: "string", Description: "Candidate Go source for edit_check"},
					"project":           {Type: "string", Description: "Optional project ID or repository directory"},
				},
				Required: []string{"symbol"},
			},
		},
	}
}
