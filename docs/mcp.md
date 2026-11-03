# Model Context Protocol (MCP) Setup Guide

`sila` includes a native Model Context Protocol (MCP) server over standard input/output (stdio), providing active AI coding assistants direct, dynamic access to unified cross-tool knowledge and raw transcripts.

The server opens sila's own knowledge store read-write because the tool set includes explicit mutations (handoffs, memos, docs, goals, feedback, and maintenance). Every external source tool database is opened read-only; sila never writes to another tool's data.

## Contract Compatibility

The executable registry is the authoritative MCP contract. Stable releases ship
`mcp-contract.json` and `release-metadata.json`; the latter records the MCP
contract version/hash and SQLite schema version. Removing a tool, removing a
field, changing a field type, or making a field required is a breaking change
and requires a documented deprecation path before release.

---

## Harness Compatibility

Real MCP proxies do not always send the exact JSON shape a server advertises. Sila
normalizes the arguments envelope before dispatch without changing the public schema:

- A JSON object is passed through.
- A string-encoded object is unwrapped **once** (double-encoded `arguments`).
- A value for a field declared `integer`, `number`, or `boolean` is coerced from a string
  when it parses as the declared type (`"limit":"3"` -> `3`, `"semantic":"true"` -> `true`).
- A value for a field declared `array` or `object` is unwrapped from a string-encoded JSON
  value.
- A field declared `string` is **never** coerced, even when it looks numeric
  (`{"query":"123"}` stays a string).
- Unknown fields are preserved; `null` values are still rejected with `-32602`.

The normalizer is schema-aware, bounded to 64 KiB and 256 top-level properties, and runs
once in `tools/call` before any handler. It is covered by the `testdata/commandcode/`
golden envelopes and the `normalizeArguments` table tests.

### Schema dump and argument tracing

```bash
sila mcp --print-schema
sila mcp --print-schema --tool=recall --json
SILA_MCP_TRACE_ARGS=1 sila mcp
sila mcp --trace-args
```

`--print-schema` emits the canonical `sila/mcp-schema/v1` payload with the tool registry
and its `schema_digest`; it does not open the store.

Argument tracing is opt-in and metadata-only. It appends one JSON line per `tools/call` to
`~/.local/share/sila/mcp-trace.jsonl` (mode `0600`, 1 MiB rotation) with the tool name,
sorted argument keys, JSON types, raw envelope shape, duration, and outcome. It never
records values, queries, paths, claims, or secrets; keys matching
`secret|token|password|key|auth|credential` are redacted. Tracing never fails a tool call.

### Version and capability handshake

`initialize` includes `capabilities.experimental.sila` with `contract_version`, `profile`,
`continuity_tool_count`, `full_tool_count`, `schema_digest`, and `commit`. `get_status`
reports the same `version`, `commit`, `profile`, tool counts, and `schema_digest`, so a
client can detect a stale server after an upgrade. `sila doctor` reports the running MCP
server count and names stale processes whose executable was replaced; it never kills a
process.

## Server Profiles

`sila mcp` defaults to a focused continuity profile (`--profile=continuity`) to
eliminate tool bloat and save prompt context tokens.

- `continuity` (default): Exposes the 36 agent-workflow tools across five groups:
  - Orientation, routing, and guidebooks (5): `salam`, `prime_context`,
    `route_workflow`, `get_status`, `get_operational_guidebook`.
  - Archaeology, transcript unroll, and documentation (7): `omni_search`,
    `search_knowledge`, `search_messages`, `get_message_context`, `search_docs`,
    `get_doc`, `recall`.
  - Active 9-verb memory writes, handoffs, and grounding (7): `save_memo`,
    `save_lesson`, `save_case`, `save_handoff`, `get_latest_handoff`,
    `get_project_facts`, `reconcile_drift`.
  - Goal lifecycle, quality gates, audits, decisions, skills, and feedback (13):
    `list_goals`, `claim_goal`, `touch_goal`, `report_goal`, `certify_goal`,
    `audit_goals`, `audit_goal`, `run_gate_pipeline`, `list_skills`,
    `skill_search`, `skill_load`, `list_decisions`, `record_feedback`.
  - Temporal, specialist, domain-learning, and symbol parity (4):
    `get_activity_digest`, `consult_personas`, `save_domain_learning`,
    `check_symbol_blast_radius`.
- `full`: Restores the complete 76-tool contract for administrative, destructive,
  external-network, and swarm-spawning tasks.

Direct `tools/call` requests are never filtered by the active profile; only
`tools/list` is. Existing automation can call any registered tool under
`--profile=continuity`.

To run the full tool suite:
```sh
sila mcp --profile=full
```

## Exposed Tools (76)

`Category` is machine-readable metadata for clients that filter tools:
`read` tools never mutate, `write` tools can mutate sila's store or project
files, and `collab` tools coordinate goals, crew, gates, and feedback.
`Mutates` is the precise write behavior: `no` is read-only, `yes` writes,
and `opt-in` only writes when the caller passes the listed flag.

| Tool | Category | Description | Mutates | Parameters |
|---|---|---|---|---|
| `salam` | read | First-contact orientation briefing: 7-step working loop, live next steps, ready goals, and dual CLI/MCP runbook | no | `project` (string) |
| `get_operational_guidebook` | read | Retrieve curated cross-project operational learnings, battle-tested engineering patterns, and failure postmortems categorized by domain | no | `domain` (string), `format` (string: markdown, json, html, toon, summary), `project` (string), `search` (string, alias: `query`), `query` (string, alias: `search`) |
| `route_workflow` | read | Advisory System-1 tool chain and workflow routing: recommends optimal multi-tool Sila sequences tailored to developer objectives | no | `objective` (string, required), `current_tool` (string), `project` (string) |
| `request_approval` | collab | Synchronous human-in-the-loop steering: pauses agent execution until approved or rejected via Telegram, Discord, or CLI | yes (store state) | `prompt` (string, required), `project` (string), `risk_level` (string), `timeout_seconds` (integer) |
| `search_knowledge` | read | FTS5 search across distilled facts, claims, and architectural decisions | no | `query` (string, required), `project` (string), `tool` (string), `refs_exist_only` (boolean), `verified_only` (boolean), `facts_only` (boolean), `limit` (integer), `offset` (integer) |
| `list_projects` | read | List indexed repositories with message and claim counts | no | `query` (string), `limit` (integer) |
| `get_project_facts` | read | Get distilled facts for a project with pagination | no | `project` (string), `refs_exist_only` (boolean), `verified_only` (boolean), `facts_only` (boolean), `limit` (integer), `offset` (integer) |
| `get_status` | read | Knowledge store health and sync summary | no | none |
| `get_subagent_charter` | write | Create a signed subagent continuity charter with parent, goal, lane, paths, and handoff policy | yes (local charter file) | `parent_session_id` (string, required), `subagent_id` (string, required), `goal` (string), `lane` (string), `allow_paths` (array), `handoff_policy` (string) |
| `search_messages` | read | FTS5 search across raw conversation transcripts | no | `query` (string, required), `project` (string), `tool` (string), `role` (string), `since` (string), `limit` (integer), `offset` (integer), `two_pass` (boolean), `window` (integer), `max_tokens` (integer) |
| `reconcile_drift` | write | Re-verify referenced repository files on disk | yes (store state; no when dry_run=true) | `project` (string), `dry_run` (boolean), `semantic` (boolean) |
| `prime_context` | read | Build a concise session briefing for agent startup with bounded goal projection, envelope budgeting, and canonical representation | no | `project` (string), `max_facts` (integer), `budget` (integer), `max_tokens` (integer), `cache_pinned` (boolean), `include_markdown` (boolean), `all_goals` (boolean), `disable_codebase_map` (boolean), `codebase_map_chars` (integer) |
| `save_handoff` | write | Persist a structured session handoff (extracts candidate architectural memos) | yes | `summary` (string, required), `project` (string), `open_items` (array), `decisions` (array), `branch` (string), `agent` (string), `repo_files` (array, alias: `files`), `files` (array, alias: `repo_files`), `tool_name` (string), `client_kind` (string), `origin_entity` (string) |
| `get_latest_handoff` | read | Retrieve the active handoff for a project | no | `project` (string) |
| `claim_handoff` | write | Atomically claim an open or pending session handoff via Compare-and-Set (CAS) | yes | `handoff_id` (string, required), `agent_name` (string), `session_id` (string) |
| `pop_handoff` | write | Pop and atomically claim the newest eligible pending handoff for cwd and branch | yes | `project` (string), `branch` (string), `agent_name` (string), `session_id` (string) |
| `save_memo` | write | Record an explicit architectural directive | yes | `content` (string, required), `project` (string), `repo_files` (array, alias: `files`), `files` (array, alias: `repo_files`), `agent` (string), `tool_name` (string), `client_kind` (string), `origin_entity` (string), `decision` (string) |
| `save_lesson` | write | Record a solved problem (symptom, cause, fix, evidence) for cross-project recall | yes | `symptom` (string, required), `project` (string), `cause` (string), `fix` (string), `tags` (string), `files` (array, alias: `repo_files`), `repo_files` (array, alias: `files`), `agent` (string), `tool_name` (string), `client_kind` (string), `origin_entity` (string) |
| `save_case` | write | Record an architectural procedural case (intent, approach, invariant, evidence) | yes | `intent` (string, required), `project` (string), `approach` (string), `invariant` (string), `tags` (string), `files` (array, alias: `repo_files`), `repo_files` (array, alias: `files`), `agent` (string), `tool_name` (string), `client_kind` (string), `origin_entity` (string) |
| `save_doc` | write | Attach and index an external reference document | yes | `title` (string, required), `content` (string, required), `project` (string), `url` (string) |
| `search_docs` | read | FTS5 search across attached documentation | no | `query` (string, required), `project` (string), `limit` (integer), `offset` (integer) |
| `get_doc` | read | Retrieve a document by ID | no | `id` (string, required) |
| `search_web_docs` | write | Search live web documentation | opt-in (`auto_save`) | `query` (string, required), `project` (string), `top` (integer), `auto_save` (boolean) |
| `search_code` | write | Search public source code repositories | opt-in (`auto_save`) | `query` (string, required), `language` (string), `repo` (string), `extension` (string), `project` (string), `top` (integer), `auto_save` (boolean) |
| `get_package_doc` | write | Fetch official package docs (Go, Rust, NPM, PyPI) | opt-in (`auto_save`) | `name` (string, required), `ecosystem` (string), `project` (string), `auto_save` (boolean) |
| `go_symbol_references` | read | Locate symbol references, definitions, and usages across Go packages using gopls or native AST parser | no | `file` (string, required), `symbol` (string, required), `dir` (string), `project` (string) |
| `go_package_api` | read | Extract exported types, functions, methods, constants, and docstrings from a Go package | no | `pkg` (string, required), `include_unexported` (boolean), `project` (string) |
| `go_diagnostics` | read | Retrieve compiler errors, typecheck failures, and vet diagnostics for Go files | no | `path` (string), `tags` (string), `project` (string) |
| `cloudflare_d1_query` | write | Execute SQL query or schema inspection against a Cloudflare D1 database with parameter support and safety sentry | yes (edge database) | `query` (string, required), `database` (string), `params` (array), `project` (string) |
| `cloudflare_worker_status` | read | Inspect Cloudflare Worker deployment status, active script version, routes, compatibility date, and declared bindings | no | `script_name` (string), `project` (string) |
| `cloudflare_edge_telemetry` | read | Retrieve edge runtime telemetry, request volume, error counts, and CPU execution time percentiles (p50, p99) | no | `script_name` (string), `time_range` (string), `project` (string) |
| `search_stackoverflow` | write | Search Stack Overflow QA and error resolutions | opt-in (`auto_save`) | `query` (string, required), `tag` (string), `project` (string), `top` (integer), `auto_save` (boolean) |
| `compile_rules` | write | Compile knowledge into agent rule files | yes (files) | `project` (string), `target` (string) |
| `vacuum_database` | write | Defragment the store and optimize the query planner | yes | `force` (boolean) |
| `get_db_stats` | read | SQLite page, freelist, and fragmentation metrics | no | none |
| `get_message_context` | read | Expand surrounding conversation turns for a message ID, staying inside its recorded source session | no | `id` (integer, required), `window` (integer, max 20), `occurrence` (integer, 1-based selection among recorded source occurrences) |
| `omni_search` | read | One query across every knowledge tier: invariants, claims, candidates, transcripts, commits, docs, and session handoffs | no | `query` (string, required), `project` (string), `limit` (integer), `tool` (string), `role` (string), `since` (RFC3339 string), `semantic` (boolean) |
| `recall` | read | Find prior resolutions before web search or workarounds: deliberate lessons first, then grounded claims and memos, then transcripts | no | `query` (string, required), `project` (string), `limit` (integer), `semantic` (boolean) |
| `diagnose_system` | write | Full integrity, index, and storage health audit | opt-in (`repair`) | `repair` (boolean) |
| `list_goals` | read | List goal packages, project task acceptance status, conditional dependency blockers, or assemble a bounded task execution packet | no | `project` (string), `status` (string), `pending_only` (boolean), `tier` (string), `limit` (integer), `slug` (string), `task_id` (string) |
| `audit_goals` | collab | Audit goals against git history and sync `goals/README.md` | yes (unless `no_sync`) | `project` (string), `no_sync` (boolean) |
| `audit_goal` | collab | Run an epistemic blindfold mystery audit on a goal package to evaluate compliance and certify or reopen it | yes (receipt/dossier/status) | `slug` (string, required), `project` (string), `persona` (string), `url` (string), `binary` (string), `skip_canary` (boolean), `timeout` (string) |
| `claim_goal` | collab | Claim and lock a goal package (strictly forbids --force from overriding structured decision gates) | yes | `slug` (string, required), `project` (string), `agent` (string), `note` (string), `force` (boolean), `session` (string), `allow_paths` (array), `deny_paths` (array), `duration` (string) |
| `release_goal` | collab | Release a claimed goal lock | yes | `slug` (string, required), `project` (string) |
| `report_goal` | collab | Update an in-progress goal's status note or persist a structured task checkpoint (`sila/task-checkpoint/v1`) | yes | `slug` (string, required), `note` (string), `status` (string), `project` (string), `tool_name` (string), `client_kind` (string), `origin_entity` (string), `task_checkpoint` (object) |
| `create_goal` | collab | Scaffold a new goal package | yes | `slug` (string, required), `title` (string), `description` (string), `status` (string), `commit` (string), `depends_on` (array), `independent` (boolean), `sequence` (integer), `shape` (string), `tier` (string), `files_to_touch` (array), `invariants` (array), `tasks` (array), `work_package` (object), `spec_review` (object), `strict` (boolean), `project` (string), `overwrite` (boolean) |
| `update_goal` | collab | Update goal metadata, dependencies, files, or spec review with preflight validation | yes | `slug` (string, required), `title` (string), `description` (string), `depends_on` (array), `add_deps` (array), `remove_deps` (array), `independent` (boolean), `sequence` (integer), `files_to_touch` (array), `shape` (string), `tier` (string), `personas` (array), `add_personas` (array), `clear_personas` (boolean), `strict` (boolean), `spec_review` (object), `project` (string) |
| `certify_goal` | collab | Certify a goal from evidence or run its verification contract | yes | `slug` (string, required), `evidence` (string), `agent` (string), `note` (string), `auto_run` (boolean), `project` (string) |
| `supersede_goal` | collab | Mark a goal superseded and release its claim and charter | yes | `slug` (string, required), `superseded_by` (string), `agent` (string), `reason` (string), `project` (string) |
| `validate_goals` | read | Run deterministic goal package, dependency, lifecycle, and registry validation | no | `slug` (string), `strict` (boolean), `manifest_path` (string), `project` (string) |
| `reopen_goal` | collab | Reopen an implemented goal for remediation | yes | `slug` (string, required), `reason` (string), `agent` (string), `claim` (boolean), `project` (string) |
| `record_feedback` | collab | Persist an agent observation, tip, or issue | yes | `content` (string, required), `id` (string), `category` (string), `project` (string), `agent` (string), `fact_id` (string), `share` (boolean) |
| `get_activity_digest` | read | Summarize recent activity and time-windowed progress across sessions (equivalent to `sila standup` / `sila digest`) | no | `project` (string), `since` (string), `activity` (boolean), `tool` (string), `top` (integer) |
| `consult_personas` | collab | List, recommend, or bind specialist personas for a goal (equivalent to `sila persona` / `sila hire`) | yes | `action` (string), `domain` (string), `group` (string), `goal` (string), `personas` (array), `project` (string) |
| `save_domain_learning` | write | Persist a grounded domain guardrail learning that pairs with `get_operational_guidebook` | yes | `domain` (string, required), `title` (string, required), `symptom` (string, required), `cause` (string), `fix` (string, required), `rule` (string), `tags` (array), `files` (array), `project` (string) |
| `check_symbol_blast_radius` | read | Run an in-memory edit-check or safe-delete contract analysis for a symbol (equivalent to `sila edit-check` / `sila safe-delete`) | no | `symbol` (string, required), `mode` (string), `candidate_payload` (string), `project` (string) |
| `list_feedback` | read | List recorded feedback by status or category | no | `project` (string), `category` (string), `status` (string), `limit` (integer) |
| `resolve_feedback` | collab | Close a feedback ticket with optional goal attribution | yes | `id` (string, required), `goal` (string), `note` (string), `agent` (string) |
| `touch_goal` | collab | Renew a goal claim heartbeat lease | yes | `slug` (string, required), `project` (string), `agent` (string) |
| `compare_proposals` | read | Rank competing proposals against non-goals and invariants | no | `proposals` (array, required), `project` (string), `context_query` (string) |
| `record_preference` | write | Record a pairwise preference decision | yes | `candidate_a` (string, required), `candidate_b` (string, required), `winner` (string, required), `rationale` (string), `context_query` (string), `project` (string), `agent` (string), `decision` (string) |
| `run_gate_pipeline` | collab | Run the quality gate in a disposable Git worktree | yes (worktree) | `branch` (string), `forward_target` (string), `auto_forward` (boolean), `project` (string) |
| `run_preflight_eval` | collab | Run epistemic preflight evaluation or context ablation test suite before landing changes | yes (sandbox) | `eval_dir` (string), `case_name` (string), `tag` (string), `target_rule` (string), `harness` (string), `ablation` (boolean), `project` (string) |
| `spawn_crewmate` | collab | Spawn a crewmate in a worktree and tmux session | yes | `harness` (string, required), `goal_slug` (string, required), `base_ref` (string), `project` (string) |
| `list_crew` | read | List crewmates with zero-token liveness status | no | `project` (string) |
| `supervise_crew` | collab | Probe crewmate liveness and auto-gate completions | yes (status) | `project` (string), `sentinel` (boolean) |
| `sync_rules` | write | Project Core Hub rules and Universal Principles Pack across detected tool targets (preserves user content outside managed blocks) | yes (files) | `project` (string), `tools` (array), `include_principles` (boolean), `stack` (string), `replace` (bool, authorizes replacing user-owned files), `policy_pack` (string, opt-in policy pack) |
| `skill_search` | read | Search available agent skills across project and global libraries by keyword, technology, or task description | no | `query` (string, required), `tags` (array), `limit` (integer), `project` (string) |
| `skill_load` | read | Retrieve complete instructions, metadata, and optional resources for a specific skill from project or global catalogs | no | `slug` (string, required), `include_resources` (boolean), `include_scripts` (boolean), `project` (string) |
| `list_skills` | read | List available agent skills in compact summary form for progressive context disclosure without inlining full runbooks | no | `filter` (string), `project` (string) |
| `audit_hygiene` | read | Audit multi-toolchain disk storage, caches, and repository build residue across Go, pnpm, aube, npm, Cargo, Python (uv/pip), Bun, Zig, and mise | no | `scope` (string), `targets` (array), `heavy` (boolean), `project` (string) |
| `clean_caches` | write | Clean multi-toolchain build caches, package stores, and repository residue across Go, pnpm, aube, npm, Cargo, Python (uv/pip), Bun, Zig, and mise | yes (caches/stores; no when dry_run=true) | `scope` (string), `targets` (array), `heavy` (boolean), `dry_run` (boolean), `project` (string) |
| `emit_lifecycle_event` | write | Emit an active agent lifecycle transition event (session-start, user-prompt, pre-tool-use, post-tool-use, pre-compact, post-compaction, stop, session-end) into Sila's non-blocking spooler (<5ms latency) | yes (spool file; store state when drain=true) | `event` (string, required), `agent` (string), `session_id` (string), `cwd` (string), `project` (string), `payload` (object), `drain` (boolean) |
| `create_decision` | collab | Create an addressed epistemic decision record (D<number>) with lettered trade-off options and explicit recommendation | yes | `title` (string, required), `options` (array, required), `recommendation` (string, required), `rationale` (string), `context` (string), `goal` (string), `project` (string), `address` (string) |
| `resolve_decision` | collab | Record an in-place resolution for an open addressed decision (D<number>) | yes | `address` (string, required), `choice` (string, required), `notes` (string), `resolved_by` (string), `project` (string) |
| `list_decisions` | read | List addressed epistemic decisions (D<number>) with optional status or goal filter | no | `project` (string), `status` (string), `goal` (string), `limit` (integer) |

---

## Worked Example: `omni_search`

**Request (`JSON-RPC 2.0` via stdio):**
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {
    "name": "omni_search",
    "arguments": {
      "query": "D1 sqlite migration standard",
      "project": "example.com",
      "limit": 2
    }
  }
}
```

**Response (`JSON-RPC 2.0`):**
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "content": [
      {
        "type": "text",
        "text": "{\n  \"facts\": [\n    {\n      \"project\": \"example.com\",\n      \"claim\": \"All D1 database migrations must use Drizzle ORM via wrangler d1 migrations apply <db> [--local|--remote].\",\n      \"corroboration\": \"invariant\",\n      \"verified\": true,\n      \"repo_files\": [\"db/schema.ts\", \"drizzle.config.ts\"]\n    }\n  ],\n  \"transcripts\": [\n    {\n      \"project\": \"example.com\",\n      \"role\": \"assistant\",\n      \"content\": \"Generated Drizzle migration 0004_add_reserved_slugs.sql and verified locally.\",\n      \"tool\": \"opencode\"\n    }\n  ]\n}"
      }
    ]
  }
}
```

---

## Upstream MCP Aggregator & Multiplexer

`sila` can act as an MCP gateway and aggregator: instead of configuring individual third-party MCP servers in every IDE and AI harness, assistants connect **only to Sila**. Sila transparently multiplexes upstream tools and manages child process lifecycles.

### Execution Policy & Zero-Trust Startup Defaults

To prevent unintended background processes and external transport calls, upstream MCP aggregation is **disabled by default** at startup (`mcp_upstreams_enabled: false`):

- **Enable via CLI**: Start the server with `sila mcp --upstreams`.
- **Disable via CLI**: Force disabled state with `sila mcp --no-upstreams`.
- **Enable via Environment**: Set `SILA_MCP_UPSTREAMS_ENABLED=true`.
- **Enable via Config**: Set `"mcp_upstreams_enabled": true` in `~/.config/sila/config.json` or `.sila/config.json`.

Global and per-server disable states are verified immediately before client acquisition and transport activity across both raw tools and compact gateway actions.

### Exact Tool Registry & Namespace Integrity

Upstream routing is governed by an **exact validated tool registry** rather than loose prefix or wildcard dispatch:
1. **Exact Dispatch Only**: Only registered and discovered tools dispatch to upstream subprocesses or HTTP endpoints; unknown suffixes fail immediately before transport.
2. **Core Tool Reservation**: Sila's 76 native core tools (e.g. `salam`, `search_knowledge`, `prime_context`) are strictly reserved. Attempted registrations or calls are denied with zero side effects. Native requests never touch upstream logic.
3. **Collision & Ambiguity Guard**: Duplicate tool names across servers or ambiguous shared prefixes are quarantined into a denied registry upon startup.
4. **Resurrection Safety**: Calling `mcp_upstream_toggle` or re-enabling a server rebuilds state cleanly without resurrecting quarantined or colliding tools.

### Default Bundled Upstreams (5)

| Server | Prefix | Protocol / Command | Capabilities |
| :--- | :--- | :--- | :--- |
| **`ui-craft`** | `uicraft_*` | stdio: `npx -y ui-craft-mcp@0.9.0` | 8 deterministic frontend quality tools (`uicraft_check_anti_slop`, `uicraft_tokens_lint`, `uicraft_score_ui`, `uicraft_acceptance_bar`, etc.) |
| **`cloudflare`** | `cf_*` | HTTP: `https://mcp.cloudflare.com/mcp` | Cloudflare Code Mode (`cf_search`, `cf_execute`) for dynamic API interactions and infrastructure management |
| **`drizzle`** | `drizzle_*` | stdio: `npx -y drizzle-docs-mcp` | 450+ pages of Drizzle ORM documentation search (`drizzle_search_docs`, `drizzle_fetch_page`, `drizzle_list_topics`) |
| **`gopls`** | `gopls_*` | stdio: `go run golang.org/x/tools/gopls@latest mcp` | Go language server diagnostics (`gopls_diagnostics`) and workspace inspections (`gopls_workspace`) |
| **`astro`** | `astro_*` | HTTP: `https://mcp.docs.astro.build/mcp` | Official Astro framework documentation search (`astro_search_astro_docs`) |

### Automatic Knowledge Caching & Reference Provenance

When documentation or search queries are executed through upstream tools (e.g. `drizzle_fetch_page` or `drizzle_search_docs`), Sila intercepts the content and automatically persists it into the local SQLite FTS5 knowledge store (`sila.db`). Subsequent queries can recall that knowledge offline via `sila recall` or `search_docs` without network round-trips.

1. **Reference Filtering & Strict Error Gate**:
   Only successful, reference-bearing tool executions are eligible for automatic knowledge ingestion. Authentication failures (HTTP 401/403, invalid token responses), tool execution errors (`isError: true`, explicit error payloads), malformed output, content under 50 characters, and non-reference actions (e.g. `drizzle_list_topics`, `cf_execute`, `alphaxiv_discover_papers`) create zero documents or FTS rows.
2. **Canonical Pages vs. Query Snapshots (Distinct Source Identity)**:
   - **Canonical Pages**: Ingested from canonical documentation or paper fetch actions (`drizzle_fetch_page`, `alphaxiv_get_paper`) with actual page or arXiv URLs (`https://orm.drizzle.team/docs/...`, `https://alphaxiv.org/abs/...`).
   - **Query Snapshots**: Ingested from technical searches (`drizzle_search_docs`, `cf_search`, `alphaxiv_query_pdf`). When no actual page URL is supplied, the document URL is kept empty (`""`) rather than inventing a generic overview URL. Identity is derived deterministically from the query title (`DocID(projectID, title)`), ensuring distinct searches remain independently retrievable without colliding or overwriting one another. Repeated identical queries update their cached snapshot in-place with the latest retrieval timestamp.
3. **Provenance, Project Scoping & Bounded Sizes**:
   All cached references are strictly scoped to the active project ID. Result sizes are bounded (512KB limit for auto-ingested content, 1MB max document limit in SQLite store). If cache persistence encounters a store failure, an explicit error/warning is logged without claiming cached success, and the upstream provider result is returned intact to the caller.


### Custom Upstream Configuration

Define custom upstreams globally in `~/.config/sila/config.json` or per-project in `.sila/config.json`:

```json
{
  "mcp_upstreams_enabled": true,
  "mcp_upstreams": {
    "my-service": {
      "command": "node",
      "args": ["./path/to/server.js"],
      "prefix": "custom",
      "auto_ingest": true
    }
  }
}
```

### On-Demand Specialized Upstreams & Compact Gateways

Specialized upstreams that require credentials or target specific domains (such as academic arXiv research or UI screen generation) are managed **on demand** to protect the agent context window from schema bloat:

| Server | Type | Gateway Facade | Capabilities & Persistence |
| :--- | :--- | :--- | :--- |
| **`alphaxiv`** | Academic Research | `alphaxiv_query` | Query 2.5M+ arXiv academic research papers, fetch structured paper summaries, and ask questions against paper PDFs. **Automatically ingests paper text and abstracts into `sila.db` (SQLite FTS5)** for permanent offline search. |
| **`stitch`** | UI Screen Gen | `stitch_query` | Google Stitch screen generation from text, UI variants exploration, design system synthesis, and screen inspection without tool namespace collisions. |

#### Dynamic Runtime Management Tools:
- **`mcp_upstream_status`**: Inspects running state, configuration, and capabilities of all managed upstreams. Reflects both global `mcp_upstreams_enabled` and per-server enabled state.
- **`mcp_upstream_toggle`**: Enables or disables an upstream suite dynamically (e.g. `server: "alphaxiv"`, `enabled: true`), surfacing granular tools (`alphaxiv_*`, `stitch_*`) on the fly.
- **`alphaxiv_query`**: Compact gateway facade supporting actions: `"discover"`, `"get_paper"`, `"query_pdf"`, and `"raw_tool"`. Validates arguments before acquiring connections.
- **`stitch_query`**: Compact gateway facade supporting actions: `"generate_screen"`, `"generate_variants"`, `"variants"`, `"get_screen"`, `"list_screens"`, `"list_projects"`, `"create_design_system"`, and `"raw_tool"`. Validates arguments before acquiring connections.

### Negotiated HTTP Transport & Dynamic Schema Discovery (MCP 2024-11-05)

Remote HTTP upstreams communicate via the Model Context Protocol Streamable HTTP transport specification (revision `2024-11-05`):

1. **Protocol Negotiation & Session Handshake**:
   Before dispatching `tools/call` or `tools/list`, `HTTPUpstreamClient` initiates an `initialize` JSON-RPC handshake, validates the returned `protocolVersion`, registers server capabilities, and acknowledges with `notifications/initialized`. If the server issues an `Mcp-Session-Id` header, it is captured and transmitted on all subsequent requests. First-flight calls under high concurrency share a single synchronized handshake without duplicate negotiations.
2. **Dual-Format Decoding (`application/json` & `text/event-stream`)**:
   Clients negotiate `Accept: application/json, text/event-stream`. Responses formatted as Server-Sent Events (SSE) or standard JSON are decoded with strict frame bounds (10MB body limit). Response IDs are strictly validated against request IDs; mismatched response IDs, malformed event frames, and explicit `event: error` payloads trigger actionable errors. Stalled streams observe context deadlines without leaking goroutines.
3. **Session Expiry & Transparent Re-initialization**:
   When a remote endpoint returns HTTP 404 (or session expired error conditions), the client clears the stale session and automatically re-executes the initialize handshake before retrying the call once. Concurrent requests encountering expiration coordinate so only one handshake executes.
4. **Dynamic Schema Discovery & Namespace Protection**:
   Remote tools can be inspected dynamically via `tools/list` (`DiscoverTools` / `DiscoverServerTools`). Discovered tools are namespaced with the server's prefix (`<prefix>_<tool>`), validated against Sila's 63 reserved core tools, and integrated into the active dispatch table. Failed discovery reports explicit errors rather than registering phantom or partial schemas.
5. **Auto-Ingest Error Gate**:
   Tool results marked with `isError: true` are returned faithfully to the caller but are strictly excluded from automated SQLite knowledge ingestion (`sila.db`), preventing transient errors from polluting persistent documentation search.

### Outbound Egress Privacy & Credential Protection

Every tool call dispatched to an upstream server (over HTTP, stdio, compact gateways, or raw tools) is governed by Sila's audited outbound privacy boundary:

1. **Audited Egress Policy Before I/O**:
   Tool call arguments pass through `SanitizeEgressArgs` before client acquisition or transport I/O. Nested maps, arrays, and synthetic paths/tokens exercise the identical privacy boundary.
2. **Safe Scrub for Non-Executable Parameters**:
   For non-executable parameters (prompts, questions, descriptions, search queries, URLs), local user home paths (e.g. `/home/user`, `/Users/user`) are normalized to `~`, and sensitive credentials (API keys, GitHub tokens, Bearer tokens, private keys) are scrubbed to standard redaction placeholders (`[REDACTED_API_KEY]`, `[REDACTED_GH_TOKEN]`, `[REDACTED_PRIVATE_KEY]`).
3. **Fail-Closed Deny & Explicit Opt-In for Executable Content**:
   Silently rewriting code or scripts would corrupt ASTs, syntax, and cryptographic signatures. When an executable argument (`code`, `script`, `command`, `sql`, `source`, `source_code`, `raw_code`, `patch`, `diff`) contains sensitive credentials or local home paths without explicit caller consent, the call is **denied immediately with zero transport side effects** (`0` subprocess executions or HTTP calls). Callers may explicitly permit the payload intact by passing `allow_sensitive=true` or `opt_in_privacy=true`.
4. **Auth Secrets Kept Out of `argv`**:
   Child-process configurations forbid secret-bearing `--header` flags in command-line arguments (`argv`), preventing credential leakage via `ps aux` or `/proc`. AlphaXiv and Stitch communicate over direct HTTP transport with transport headers (`Authorization`, `X-Goog-Api-Key`). Subprocess adapters receive credentials strictly via scoped environment variables (`env`).
5. **Credential Rotation & Diagnostics Redaction**:
   Active HTTP clients support live credential rotation (`UpdateCredentials`) without dropping connections. All outbound error diagnostics, HTTP failure bodies, and cached docs (`interceptAutoIngest`) are sanitized before persistence or return.

### Serialized Stdio Lifecycle, Single Reaper & Deadline Propagation

Subprocess MCP servers communicating over standard input/output are managed with strict lifecycle guarantees:

1. **Three-State Lifecycle Machine**:
   Subprocess clients transition through explicit states (`closed` -> `starting` -> `ready` -> `closed`). Concurrent first-flight callers share a single startup handshake; callers cannot dispatch `tools/call` early before `initialize` finishes. If startup fails, all concurrent waiters receive the bounded error and the child is cleanly reaped.
2. **Single Reaper Guarantee**:
   Every started child process has exactly one owning reaper goroutine executing `cmd.Wait()` across all exit paths (normal EOF exit, early exit, handshake failure, deadline timeout, server disable via `ToggleServer`, or repeated `Close()`). Completed `os.ProcessState` is safely preserved without race conditions or duplicate wait calls.
3. **Explicit Deadlines & Context Propagation**:
   All upstream calls propagate bounded contexts with default 30-second deadlines from the MCP server dispatch layer (`handleToolCall`). A hanging or silent child cannot stall the gateway connection indefinitely. Upon context deadline expiry, the silent child is terminated, reaped, and cleaned up without leaking waiters in `pending`.
4. **Noise-Tolerant Scanning**:
   `readLoop` safely ignores malformed, non-JSON output, and debug stderr/stdout noise without crashing or misrouting JSON-RPC messages.

---

## Client Configuration

### 1. Claude Code CLI

Run the following command in your terminal:
```sh
claude mcp add sila -- sila mcp
```

Or place the shipped [`.mcp.json`](../.mcp.json) in your project root.

### 2. Claude Desktop

Add `sila` to your `claude_desktop_config.json` (`~/.config/Claude/claude_desktop_config.json` on Linux, `~/Library/Application Support/Claude/claude_desktop_config.json` on macOS):

```json
{
  "mcpServers": {
    "sila": {
      "command": "sila",
      "args": ["mcp"]
    }
  }
}
```

### 3. Cursor & Antigravity IDE

Configure MCP server in your editor settings with:
- **Server Name:** `sila`
- **Command:** `sila`
- **Args:** `["mcp"]`

### 4. OpenCode

Add the server to `opencode.json` in the project root, or to the global `~/.config/opencode/opencode.json`:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "sila": {
      "type": "local",
      "command": ["sila", "mcp"],
      "enabled": true
    }
  }
}
```

OpenCode expects the executable and its arguments in one `command` array, and it reads `AGENTS.md` natively, so no separate rule projection is needed. Control the surface with:

- Set `"enabled": false` to keep the server configured but off for a project.
- Set top-level `"tools": {"sila*": false}` to hide every sila tool, then re-enable the server for one agent with `"agent": {"<name>": {"tools": {"sila*": true}}}`.

Session lifecycle is not a first-class OpenCode setting: `sila hook` is a generic CLI hook. A local plugin can subscribe to the `session.idle` event and call `sila hook session-end --auto-sync` if automatic store sync is wanted; sila does not ship that plugin by default.
