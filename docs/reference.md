# sila reference

Detailed feature guide, design rationale, and the complete CLI command reference.
New users should start with [README.md](../README.md); agents working on this
repository should read [AGENTS.md](../AGENTS.md) and [docs/architecture.md](architecture.md).

Contents:

- Live session briefing example
- What is sila
- The problem: every tool has its own harness and DB
- Why sila: comparison vs alternatives
- Core capabilities (1-26)
- Adoption trigger-adherence evaluation (`sila eval adoption`)
- Agent coordination subsystem (optional)
- Security, privacy and data recovery
- CLI command reference

## Assertion lineage policy

`sila search lineage <statement-id|terms> --json` returns the fact and every linked
assertion, including failed references. `sila search assertions --json` lists project
assertions; `--refs-exist` restricts this list to verified assertion sources. Text output
and assertion TOON tables include failure reasons. Stored confidence is separate from
source availability and does not certify the claim's truth or authority.

An assertion is verified only when its statement belongs to its registered project and:

- A file resolves to a readable regular file inside the project's canonical root;
  absolute paths are allowed only within that root, and symlink escapes fail.
- A document ID (optionally `doc:` prefixed) resolves to a document in that same project.
- A numeric message ID (optionally `msg:` or `message:` prefixed) resolves to a stored
  message in that same project.
- A 7-64 character hexadecimal Git ID resolves to a commit in that exact repository,
  reachable from HEAD or any repository ref. Object presence or a reflog alone is insufficient.
  Git checks ignore `GIT_*` environment overrides and use a five-second deadline.

Optional evidence spans must use `bytes:start-end` with a nonnegative start, an exclusive
end greater than start, and an end within the source's current byte length. Unsupported
spans and evidence types, including `test_run`, stay unverified. JSON includes a stable
`verification_error` reason for failures; `verified_at` is populated only on success.

`sila verify [<project>]` (also `sila dream reconcile`) rechecks all assertion types,
including manually linked and legacy references. Deleted sources, shortened spans and
commits that become unreachable lose their verification timestamp. Normal reconciliation
JSON adds `assertions_checked` and `assertions_verified`; `--dry-run` computes these counts
without writing assertion statuses. Reconciliation does not repair the fact hash ledger.

Schema migration 28 preserves assertion IDs, references and creation history while
invalidating legacy verification flags with `not_reverified`. Rechecking grants status
only after resolution succeeds. Existing stores upgrade when opened by this build,
including the documented older-schema read path. Use one current build for CLI and MCP
processes after upgrading; an older process must not continue writing legacy flags.

## First Contact and Store Bootstrap

State-reporting commands must work on a machine that has never run Sila.
When the knowledge store file does not exist, they initialize an empty store
through the normal migration path, close the writable handle, and reopen it
read-only. No command in this set exits `1` solely because the store is absent.

Covered commands: `prime`, `prime --json`, `prime status`, `prime --verify`,
`recall`, `search`, `doctor` (without `--repair`), `digest`, `recap`, `standup`,
`goals`, `lesson list`, and `case list`.

- `sila prime` prints an empty briefing and exits `0`.
- `sila prime --json` adds `initialized`: `false` when this invocation created
  the store, `true` when a store already existed.
- `sila recall`, `sila search`, `sila doctor`, and `sila digest` return empty
  results instead of a missing-store error.
- Mutating commands keep the explicit initialization path.

Concurrent first runs are safe: the loser of a migration race is retried on
SQLite lock contention, bounded to 30 seconds, and then opens the winner's
store. The store is created with `0600` permissions under the resolved XDG data
directory.

`sila salam` reports wiring truthfully from the same heal plan used by
`sila heal --dry-run`:

```json
{
  "connected": false,
  "wiring": {
    "state": "unwired",
    "next_command": "sila heal",
    "missing_paths": ["AGENTS.md", ".agents/skills/sila/SKILL.md"]
  }
}
```

`wiring.state` is `wired`, `unwired`, or `unknown`. The greeting never says
"sila-connected" unless the state is `wired`; `unknown` asks the operator to run
`sila heal --dry-run`. `sila salam --global` (`-g`, with optional `--full` and
`--json`) and `sila heal --global` (`-g`, with optional `--dry-run` and `--json`)
scan all valid registered repositories on disk (filtering out `/`, `$HOME`, `/tmp`,
and non-repository directories via `store.IsRepoOrWorkspaceDir` and collapsing
subdirectories via `store.CanonicalProjectPath`) and aggregate fleet wiring,
auto-heal steps, ready goals, and active claims. `onboard.Check` accepts
user-scope `~/.agents/skills/sila/SKILL.md` as a fallback when a project-local
skill file is absent. `sila heal --dry-run` lists every repository path
that `sila heal` will create or update, including
`goals/sila-onboarding/{goal.md,plan.md,facts.md}` (scaffolded with
`- **Memory Protocol**: required` only when the repository does not already track
a goal package or `.sila-no-goals`) and `.gitignore`. When `sila gate` fails
`memory-write`, the diagnostic states that none of the staged files is referenced
by a memory write in the window and lists any unmatched window files.
`sila goals focus <slug> --session=<id>` inherits the existing active claim
holder when `--agent` is omitted, and `sila feedback` auto-resolves registered
repository `cwd`s while emitting `null` for `project_id`/`project` outside
registered repositories.

## Prime Canary

`sila prime canary` is a hermetic end-to-end check for session-start priming:

```bash
sila prime canary --json [--harness=<name>] [--keep] [--strict] [--timeout=60s]
```

It creates a temp `HOME`/XDG/DB and a temp git repository, then runs
`sila hook session-start` for each harness (`claude-code`, `cursor`,
`command-code`, `zcode`, `antigravity`) with the harness's native envelope. A
case passes when the hook exits 0, stdout is non-empty (and valid JSON when it
looks like JSON), the marker reports `status=ok`, `prime_status=ok`,
`checks.prime=true`, `brief_bytes>0`, and the ledger has a `session_start` row.

A failure-injection case sets `SILA_PRIME_FAIL=1` and asserts
`prime_status=failed`, `checks.prime=false`, and default exit 0; `--strict` or
`SILA_HOOK_STRICT=1` must exit 3. Exit codes: `0` all cases pass, `1` a case
fails, `2` invalid flags, `3` timeout. `--keep` preserves and prints the temp
root. The full gate runs a single-harness canary when `sila` is on PATH; CI runs
the matrix in `docs/prime-canary-local.yml` on Linux, macOS, and Windows; the repository forbids CI workflow files (Zero CI Sprawl), so this is a local/release step.

## First-Contact Budget

`sila salam` defaults to a state-first briefing: wiring truth, heal state, up
to three ready goals, memory/handoff status, and one next command. The 7-step
loop, memory protocol, routing table, claims, and harness details are behind
`sila salam --full`. Default JSON is a bounded state object (no `loop` or
`markdown`); `--full` emits the complete brief.

`sila prime` defaults to project state and omits the repeated nine-verb
contract; `sila prime --full` keeps the complete contract. Human output is
wrapped to `--width` (or `COLUMNS`, clamped to 40-200; default 100). Budgets
are enforced by tests: salam ≤12 lines / ≤2 KB, compact salam JSON ≤4 KB,
prime ≤40 lines / ≤300 words at the default width.

## CLI Output Polish

- `sila version` is an alias for `sila --version`.
- `sila list` suggests `sila goals list` or `sila skill list` instead of a
  fuzzy neighbor.
- Human counts use English inflection (`1 match`, `2 matches`, `entries`,
  `fixes`).
- `sila config` aligns every label through one computed column.
- `sila sync` prints one summary line when harness sources are absent
  (`N harness sources not found`); `--verbose` lists each source, and
  `--json` exposes `sources_missing` plus the existing counts.

## Terminal Color Contract

Color resolution is centralized in `internal/render` and follows this order
(strongest first):

1. `--no-color` / `--plain` → disabled.
2. `NO_COLOR` present and non-empty → disabled (an empty value is ignored).
3. `SILA_FORCE_COLOR=1` or `CLICOLOR_FORCE=1` → enabled.
4. `CLICOLOR=0` → disabled.
5. `TERM=dumb` → disabled.
6. stdout is not a TTY → disabled.

Depth is negotiated from the terminal: `COLORTERM=truecolor|24bit` selects
24-bit, `TERM=*256color` selects 256-color, and everything else uses the
16-color palette. `38;2` is never emitted without a truecolor signal.

Theme selection: `SILA_THEME=light|dark`, then `COLORFGBG` background detection
(`7`, `15`, `231` are light), then dark. The light palette uses WCAG AA
foregrounds on white (`#B91C1C`, `#047857`, `#B45309`, `#4338CA`, `#6B7280`,
`#92400E`).

Piped stdout, JSON, and TOON output contain zero escape bytes. `--no-color` and
`--plain` override force-enable variables.

## Config JSON Contract

`sila config --json` emits a versioned report (`schema_version: 1`) that covers
every row in the human view:

- `config_file`, `config_dir`, `project_config`;
- `active_provider: {name, reason, source}` where source is `explicit`, `key`,
  or `default`;
- `credentials`: one entry per provider with `{set, masked}` by default, or
  `{set, value}` with `--reveal`;
- `assist: {enabled, provider, scopes, projects}`;
- `cloudflare: {account_id, d1_database_id, token}`;
- `doc_sites: {count, items}`.

Legacy top-level keys (`github_token`, `gateway_*`) remain for one release.
Default output never contains a raw credential; `--reveal` is explicit and
emits `value` instead of `masked`, never both. New text rows must add a JSON
path in the same change; the parity test fails otherwise.

## Error Contract

A user-facing failure is one cause line plus one runnable fix:

```text
✖ sila search expand: no message with id 999
  fix: find ids with: sila search "<query>"
```

Default human stderr never contains SQLite/driver strings (`sql:`,
`SQLITE_BUSY`), Go `flag` boilerplate (`invalid value ... parse error`), andon
sentinels (`ERR_ANDON_*`), or a full usage block. Bad flag values render as
`sila <cmd>: --<flag> expects <type>, got "<value>"` and exit `2`.

`search expand` and `doc` also emit machine-readable errors on stdout with
`--json`:

```json
{ "error": { "code": "message_not_found", "message": "no message with id 999", "fix": "find ids with: sila search \"<query>\"" } }
```

Set `SILA_DEBUG=1` to see the raw cause for diagnosis. The escape hatch is
redacted and never enables a stack trace by default.

## Live Session Briefing (`sila prime`)

```
$ sila prime
### 🧠 Project Memory (sila · project: sila)

**Active Session Handoff** (by antigravity on main):
- Summary: Implemented Hub & Spoke modular AGENTS architecture, goals lifecycle, and self-update
- Tasks & Open Items:
  - [x] Refactor domain sub-files into AGENTS.cloudflare.md & AGENTS.ui.md
  - [ ] Add live telemetry dashboard in Web Cockpit

**Verified Repo Runbook**:
• **Test**: `go test -race ./...`
• **Build**: `./build.sh`

**Key Architectural Facts & Decisions**:
• Pure-Go SQLite engine using modernc.org/sqlite with CGO_ENABLED=0 (internal/store/store.go)
• Read-only access to all source tool databases with PRAGMA mmap_size=256MB
• Enforce aubx or pnpm exec over npx for all CLI executions across repositories

**Attached Reference Docs & Sources**:
• Cloudflare Agents SDK & Stateful Actors (https://developers.cloudflare.com/agents/)
• Modern SQLite FTS5 Full-Text Search (https://sqlite.org/fts5.html)

Disk Grounding: 83/83 claims verified with existing files on disk.
```

`sila prime --json` (MCP `prime_context`) also emits `cases`: procedural
architectural patterns recorded with `sila case`, parsed via `store.ParseCase`.
The markdown renders them under `**Architectural Cases (procedural patterns)**`
and prints the full 9-verb working contract plus capability triggers. When the
active goal carries `Personas`, it renders `Hired Squad:`. Cases participate in
the `--max-tokens` shedding cascade and are dropped before lessons.

The briefing separates what it knows from what it merely observed. The block above
shows distilled facts and explicit decisions; a claim that only *references* a real
file is not one, so it renders under a separately named heading instead:

```
**File-Referenced Claims** (observed in sessions, not distilled — verify before trusting):
• Committed as 550ff22 with all gate checks green (tests, format, changelog, binary, readme). (cmd/sila/main.go, internal/store/facts.go) [zcode]
```

Distillation is opt-in (`sila dream --llm=stdio`), because it is the one step that
calls out to a model. Until it runs, the store holds claims — grounded, searchable,
and honestly labeled — rather than facts.

## Workspace Identity and `sila workspace status`

### Adoption Trigger-Adherence Evaluation (`sila eval adoption`)

`sila eval adoption` evaluates the 12 canonical Sila trigger moments in [`evals/adoption/cases.json`](../evals/adoption/cases.json) against [`evals/adoption/schema.json`](../evals/adoption/schema.json) and emits a versioned `sila/adoption-eval/v1` report:

- `--offline` (default): deterministic, model-free, network-free verification of compiled rulebook triggers, `SKILL.md`, `salam`/`prime` briefing lines, active MCP profile exposure (`continuity` vs `full`), and the deterministic `ScriptedAgent`.
- `--live`: opt-in live harness evaluation backed by `tool_events`, with `--harness=<name>`, `--cases=<path>`, `--out=<dir>`, `--dry-run`, and `--json`. Missing harness or ledger data yields `unknown` (`denominator: 0`), never a fabricated pass.
- Complements `sila memory report` / `sila doctor --adoption` (`sila/adoption-funnel/v1`), which summarize passive 7-day session tool-call funnels from local usage. See [`docs/adoption-eval.md`](./adoption-eval.md).


Sila resolves one active project identity for `prime`, `salam`, `goals`, MCP tools, and
lifecycle hooks. A directory can be a workspace root, a child repository, or a
subdirectory of either; the resolver applies this precedence:

1. **Explicit `--project=<id|path>`** always wins. No fuzzy matching is used.
2. **The directory itself owns goals** (`goals/*/goal.md`) -> that directory is the
   project.
3. **A `.code-workspace` boundary is honored.** Sila never walks above the workspace root
   into a parent monorepo.
4. **A specific child repository keeps its identity.** Running in `worker/` resolves
   `worker`, even when a sibling `cli/` owns goals. It never jumps to a sibling.
5. **The workspace root selects a child** when exactly one child repository owns goals.
6. **Multiple children** select a single preferred child (`cli`, `core`, `engine`, `main`,
   `app`) only when exactly one matches. Otherwise the workspace is **ambiguous**: the
   command exits 2 with sorted candidates and one recovery command per candidate.
7. **No workspace and no goals** -> the current repository or directory is used as-is.

```bash
sila workspace status
sila workspace status --json
sila workspace status --json | jq -r .project_id
```

JSON contract (`sila/workspace-identity/v1`):

```json
{
  "workspace_root": "/path/to/sila",
  "workspace_id": "sila",
  "active_dir": "/path/to/sila/cli",
  "project_id": "sila-cli",
  "source": "preferred-child",
  "candidates": [],
  "ambiguous": false
}
```

`source` is one of `root-goals`, `single-child`, `preferred-child`, `repo`, `registry`,
or `derived`. An ambiguous workspace returns `"ambiguous": true`, the candidate paths, and
exit code 2. The command never writes to the store or changes a project registration.

## MCP Profiles

`sila mcp` defaults to `--profile=continuity`, a 36-tool agent-workflow surface
grouped into orientation/routing/guidebooks (5), archaeology/transcripts/docs
(7), active 9-verb memory writes/handoffs/grounding (7), and goal lifecycle,
quality gates, audits, decisions, skills, and feedback (13). The administrative,
destructive, external-network, and swarm tools remain behind
`--profile=full`, which exposes all 75 registered tools.

Only `tools/list` is filtered. Direct `tools/call` requests for any registered
tool continue to execute under `--profile=continuity`, so existing CLI bridges
and automation do not break.

### Upstream error attribution

When a tool mapped to a managed upstream fails, the JSON-RPC error keeps the
`-32603` code and adds structured `data`:

```json
{
  "code": "upstream_unreachable",
  "upstream": "cloudflare",
  "state": {
    "manager": "enabled",
    "upstream": "cloudflare",
    "configured": true,
    "server_enabled": true,
    "on_demand": false,
    "toggled": false,
    "running": false
  },
  "message": "dial tcp 127.0.0.1:8787: connect: connection refused",
  "remediation": "start or restart \"cloudflare\", then run `mcp_upstream_status`"
}
```

| `code` | Cause | Remediation |
|---|---|---|
| `upstream_disabled` | manager off, server disabled, or on-demand not toggled | `sila mcp --upstreams` or `mcp_upstream_toggle` |
| `upstream_unconfigured` | no configured server for the tool | configure `mcp_upstreams`, restart MCP |
| `upstream_unreachable` | connection refused/reset, DNS, broken pipe | start/restart the upstream, `mcp_upstream_status` |
| `upstream_timeout` | deadline exceeded or net timeout | narrower request, `mcp_upstream_status` |
| `upstream_tool_error` | upstream JSON-RPC error; keeps `upstream_code` | check arguments, `mcp_upstream_status` |
| `upstream_protocol_error` | unclassified transport/protocol failure | `mcp_upstream_status`; restart if persistent |

The human `message` names the tool and upstream and stays on one line. Secrets,
home paths, and URL query strings are redacted before either field is emitted.
Unknown tools still return `-32601`.

### `route_workflow` taxonomy

`route_workflow` classifies an objective into one of 11 categories and returns
both `recommended_tools` (MCP) and `cli_commands` (CLI equivalents). The
heuristic path scores categories in a fixed priority order, so equal keyword
scores never depend on Go map iteration.

| Category | MCP tools | CLI equivalents |
|---|---|---|
| `onboarding` | `salam`, `prime_context`, `get_status`, `list_goals` | `sila salam`, `sila prime`, `sila status`, `sila goals ready` |
| `investigation` | `recall`, `omni_search`, `search_knowledge`, `search_docs`, `get_message_context` | `sila recall "<query>"`, `sila search "<query>" --omni`, `sila search expand <session_id>`, `sila doc search <query>` |
| `goal_execution` | `claim_goal`, `prime_context`, `touch_goal`, `report_goal`, `save_lesson` | `sila goals claim <slug>`, `sila prime`, `sila goals touch <slug>`, `sila lesson "<symptom>" --cause=... --fix=...` |
| `gate_and_verification` | `get_project_facts`, `run_gate_pipeline`, `reconcile_drift`, `audit_goals`, `save_handoff` | `sila facts verify`, `sila gate --pre-commit`, `sila goals audit <slug>`, `sila goals`, `sila handoff "<summary>" --open="..."` |
| `instruction_and_drift` | `reconcile_drift`, `sync_rules`, `search_docs`, `compile_rules` | `sila heal`, `sila agents sync`, `sila doc search <query>` |
| `temporal_activity` | `get_status`, `get_activity_digest`, `omni_search`, `get_message_context` | `sila standup "last 24h"`, `sila digest --activity --since=24h`, `sila recap --since=7d` |
| `architecture_and_decisions` | `save_memo`, `save_case`, `create_decision`, `compare_proposals`, `record_preference` | `sila memo "<directive>" --files=...`, `sila case "<intent>" --approach="..." --invariant="..."`, `sila decision "<title>"`, `sila vision` |
| `persona_and_audit` | `audit_goal`, `audit_goals`, `consult_personas`, `run_gate_pipeline` | `sila persona list`, `sila hire --goal=<slug>`, `sila hire assess`, `sila hire audit-goal <slug>`, `sila audit` |
| `skill_and_domain_learning` | `get_operational_guidebook`, `save_domain_learning`, `list_skills`, `skill_search`, `skill_load` | `sila guide <domain>`, `sila learn add --domain=... --title=... --symptom=... --fix=...`, `sila learn distil --save`, `sila skill search <query>`, `sila skill load <name>` |
| `symbol_and_blast_radius` | `go_symbol_references`, `go_package_api`, `go_diagnostics`, `check_symbol_blast_radius`, `save_memo` | `sila edit-check <symbol>`, `sila safe-delete <symbol>`, `sila note "<warning>" --symbol=<pkg::Sym>`, `sila recall --symbol=<pkg::Sym>` |
| `general` | `prime_context`, `omni_search`, `list_goals` | `sila prime`, `sila search "<query>" --omni`, `sila goals ready` |

## MCP Harness Compatibility

The MCP server accepts the argument envelopes real harnesses send without weakening the
public schema:

- string-encoded objects are unwrapped once;
- string-encoded integers, numbers, and booleans are coerced only when the tool schema
  declares that type;
- string-encoded arrays and objects are unwrapped;
- string fields are never coerced;
- unknown fields are preserved and `null` values remain errors.

`sila mcp --print-schema [--tool=<name>] [--json]` emits the canonical
`sila/mcp-schema/v1` registry with `schema_digest`. `SILA_MCP_TRACE_ARGS=1` or
`--trace-args` appends metadata-only records to `~/.local/share/sila/mcp-trace.jsonl`
(argument keys and JSON types, never values). The `initialize` result exposes
`capabilities.experimental.sila` with the contract version, profile, tool counts,
schema digest, and commit; `get_status` mirrors them. `sila doctor` reports running MCP
servers whose executable was replaced and recommends a client restart without killing
the process.

### MCP continuity surface and client cache

`store.DefaultContinuityToolCount` (36) mirrors `mcp.ContinuityToolCount`, and
`TestTier0AndTriggerMCPToolsExistInContinuityProfile` fails if a tool named in
`AGENTS.md` or the compiled trigger/prime guidance is excluded from the
continuity profile. `sila doctor` adds an **MCP Continuity Surface & Client
Cache** check: it counts the `.json` schema files under
`~/.gemini/antigravity/mcp/sila/` and warns with a restart/`sila install`
repair when fewer than 36 are cached. A missing cache directory (CI or
non-Antigravity harness) passes without creating anything. See
[Command Code Integration](command-code.md) for the preflight gate, bridge envelope, pinned
fixtures, and the CLI fallback.

## Session Memory Bridge (`sila hook`)

`harness-session-memory-bridge` turns Sila from an opt-in tool into a session
lifecycle contract.

### Session start

```bash
sila hook session-start
sila hook session-start --budget=512 --json
```

The start brief carries the project, branch, HEAD, active goal, latest handoff,
open items, and the six-line memory contract under
`sila/memory-contract/v1`. The default budget is **2,048 bytes**; `--budget`
accepts 512–8,192. Optional sections are dropped in a fixed order (next pointer,
open items, handoff summary); a line is never cut mid-rune. A missing store
degrades to the static contract with `memory_unavailable: true` and never fails
the session.

### Session end

```bash
sila hook session-end
sila hook session-end --dry-run --json
sila hook session-end --no-auto-handoff
```

Session end guarantees a handoff. If none exists in the current session window,
Sila writes one marked `agent: "sila-hook"` and `tool_name: "sila-hook"` with
the branch, HEAD, changed files (bounded to 50), prior open items, the active
goal, and dependent pending goals. Repeated calls are idempotent. Sila never
supersedes a pending handoff owned by another agent; it reports the existing
handoff instead. `--no-auto-handoff` exits 2 with the recovery command.
`--dry-run` prints the plan without writing.

### Failure-time recall

A `post-tool-use` event with a non-zero `exit_code` emits at most three prior
resolutions and the exact `sila recall "<query>"` command. The nudge is bounded
to 512 bytes and never blocks or retries the failed command.

### Post-compaction re-anchor

`pre-compact` / `precompact` events are silent correlation points. A
`post-compaction` / `postcompact` / `compact` event renders a bounded anchor:

```text
Sila re-anchor · sila-cli · main · 832d8bb
goal: <slug|none> · session: <id|none> · handoff: <id|none>
verbs: prime recall memo lesson case facts gate handoff sync
contract: sila/memory-contract/v1
before grep/web: sila recall "<symptom>" or sila search "<query>" --omni
next: sila goals ready
```

- Budget: 512 bytes by default, clamped to 256–2048 via `--budget`.
- Drop order when over budget: `next`, the handoff token, the
  `before grep/web` line, branch/HEAD, then goal/session values. The project,
  the nine verbs, and the contract version are never dropped. Output is
  UTF-8-safe, newline-terminated, and `bytes` equals `len(text)`.
- JSON mode emits `sila/post-compaction/v1` with the brief fields plus `text`.
- Dedupe key: `sha256(session + NUL + head + NUL + event + NUL + sequence)`.
  The marker lives at `.sila/state/reanchor/<session>.json` (0600, parent
  0700); the same key within 5 seconds emits once, a new key emits again.
  Marker failures are warnings, never session failures.
- A missing store degrades to the static contract with
  `memory_unavailable:true`.
- Harness matrix: Claude Code installs `PreCompact` and uses the documented
  `SessionStart` `source=compact` fallback to emit the anchor. Command Code has
  no compaction event and is reported `compaction_supported:false`; wrappers
  can still call `sila hook --event=post-compaction`. Cursor, Gemini, and Codex
  are unsupported. `sila hook status --json` reports
  `compaction_supported` / `compaction_installed` per harness.

### Harness hook installation

```bash
sila hook install --harness=claude --dry-run --json
sila hook install --harness=command-code
sila hook install --harness=antigravity --user
sila hook uninstall --harness=claude
sila hook uninstall --harness=antigravity --user
sila hook status --json
```

Scope: `--user`, `--global`, `-g`, and `--scope=user` write the harness's first
declared `UserPaths` entry under `$HOME` (for example `~/.cursor/hooks.json`,
`~/.gemini/config/hooks.json`, `~/.codex/hooks.json`, or
`~/.config/opencode/plugins/sila-bridge.js`) instead of creating project-local
`.claude/`, `.cursor/`, `.codex/`, or `.agents/` files. `--scope=project`
forces project scope. `sila hook status` inspects project scope first and falls
back to user scope, reporting `"scope": "user"` and the resolved path when only
global hooks are active. ZCode ignores project hooks, so `sila hook install
--harness=zcode` defaults to user scope (`~/.zcode/cli/config.json`) while
`Install`/`Uninstall` at project scope remain refused with
`project_hooks_ignored: true`.

Supported in this build via the declarative adapter registry (`internal/harness/registry`):
- Native shell-hook harnesses: Claude Code (`.claude/settings.json`), Cursor (`.cursor/hooks.json`, stop is continue-only), Codex (`.codex/hooks.json`, requires project trust review), Devin (`.devin/hooks.v1.json`), Command Code (`.commandcode/settings.json`, skips hooks in plan mode), Codewhale (`.codewhale/hooks.toml`, requires workspace trust + SHA-256 digest approval), Antigravity (`.agents/hooks.json`), ZCode (`~/.zcode/cli/config.json`; project hooks are refused because ZCode ignores them), and Muse (`.muse/hooks.json`, requires project trust; output blocking returns `unsupported` until verified).
- Plugin-only harnesses: OpenCode (`.opencode/plugins/sila-bridge.js`, mode `0644`) and MiMoCode (`.mimocode/plugins/sila-bridge.js` + `mimocode.json` `"plugin"` entry; refused with `unsupported` and the docs URL unless `mimocode.json` already declares a `"plugin": []` array or `SILA_MIMOCODE_PLUGIN_VERIFIED=1`). Both are generated from the single plain-JS template (`internal/harness/pluginbridge`), report `plugin_bridge` separately from `shell_hook` in `sila hook status --json`, throw only on `tool.execute.before` deny, never fake a hard stop block on `session.idle`, fail open with one warning when `sila` is missing from `PATH`, and fall back to manual `sila hook --event=<phase> --json` + `sila gate --pre-commit`.
- Commit hook: Git (`pre-commit` / `pre-push` via `sila gate`).

Prime attempts are recorded in `prime_events` with status, error class, duration, source, session, harness, and brief bytes. `sila prime status --json` reports the last attempt and 24h counts per harness; `sila prime --verify [--session=<id>]` exits 0 only for a successful event. `sila hook status --json` adds `last_prime` and `prime_counts_24h` to each harness, and `sila hook status --strict` / `sila prime status --strict` fail an installed session-start harness without a successful event. The ledger is bounded and prunable via `PrunePrimeEvents`.

Antigravity exposes multiple app-data surfaces: CLI (`~/.gemini/config/hooks.json`), app (`~/.config/Antigravity/hooks.json`; macOS `~/Library/Application Support/Antigravity/hooks.json`; Windows `%APPDATA%/Antigravity/hooks.json`), and IDE (`~/.config/antigravity-ide/hooks.json`). `sila hook install --harness=antigravity --user --surface=all|app|cli|ide` installs every detected surface; `sila hook status --json` reports a `surfaces` array and a top-level `coverage` summary; `sila hook status --strict` fails a detected-but-unwired surface. Trust-required harnesses require explicit approval: `sila hook install --harness=codex --user --trust` writes `~/.sila/harness-trust/codex.json`; without `--trust` the install refuses, and `--tools=all` never auto-installs trust-required harnesses. Devin is supported by the native registry (`~/.config/devin/hooks.v1.json`) and is reported supported/not installed; MiMoCode installs its plugin bridge only when the host declares it.

Installed hook commands resolve the Sila binary at install time instead of relying on the harness inheriting `~/.local/bin`: `SILA_BIN`, the running `sila`, `PATH`, `~/.local/bin/sila`, workspace `./.bin/sila`/`./sila`, or a portable wrapper at `~/.local/share/sila/hooks/sila-hook`. `sila hook status --json` reports `binary_path`, `binary_source`, `wrapper_path`, and `binary_resolved`; `sila hook status --strict` exits 1 when an installed harness has no resolved binary. The wrapper retries the same order at runtime and exits 127 with a bounded stderr diagnostic when nothing is found, so sandboxes that omit `~/.local/bin` from PATH fail visibly instead of silently starting an unprimed session.

Install is merge-preserving and idempotent: foreign hooks and user keys are preserved, a second install is a no-op, and uninstall removes only Sila-owned entries. Trust-required harnesses are never reported `installed: true` before trust approval. Unknown harnesses are reported as `unsupported` with the manual `sila hook session-start --json` / `sila hook stop --json` wiring; Sila does not invent a config path. `sila hook install` without `--harness` retains the existing git pre-commit behavior. See [`docs/harness-capability-matrix.md`](harness-capability-matrix.md) for the complete matrix.

## Harness Goal Contract (`sila goals contract`)

The harness goal contract is the core invariant that a session is bound to a Sila
goal (or an explicit waiver) and has claim, plan, evidence, grounding, handoff,
and memory debt checked before it finishes. It is harness-neutral: adapters
translate lifecycle events; they do not implement policy.

### Signal and decision

Hooks and chokepoints produce a `sila/harness-signal/v1` signal:

```json
{
  "schema_version": "sila/harness-signal/v1",
  "harness": "claude",
  "session_id": "sess_123",
  "parent_session_id": "",
  "project_id": "sila-cli",
  "cwd": "/repo",
  "phase": "stop",
  "sila_goal_slug": "core-harness-goal-contract",
  "claim_id": "",
  "timestamp": "2026-10-22T12:00:00Z"
}
```

The result is `sila/harness-goal-contract/v1` with `decision` one of `allow`,
`warn`, `deny`, or `unsupported`, a stable `reason_code`, `checks`, `recovery`,
and `receipt_path`.

### Binding and policy

```bash
sila goals contract link --session=<id> --goal=<slug> [--harness=] [--expires=24h]
sila goals contract unlink --session=<id> [--harness=]
sila goals contract status [--session=<id>] [--json]
sila goals contract check --session=<id> --goal=<slug> [--phase=stop|session-end|certify] [--json]
sila goals contract waive --session=<id> --reason="..." [--check=handoff,facts] [--expires=168h]
```

Bindings live at `.sila/state/harness-goals/<harness>-<session>.json` (0600);
waivers live at `.sila/state/goals/waivers/<harness>-<session>.json` and never
exceed seven days. If no binding exists, an active claim owned by the session is
used as an implicit `source: "claim"` binding. A native harness goal (Codex
persisted goals, ZCode Goal Mode, Muse goal observer, Command Code `/goal`) must
be linked explicitly; Sila does not create a second goal store.

Policy resolves project > user > compiled default (`warn`), then
`SILA_GOAL_CONTRACT=off|warn|enforce`. `off` is silent. In `enforce`, one-session
`SILA_GOAL_CONTRACT_BYPASS=1` allows the operation with a visible warning and a
`bypassed: true` receipt.

### Checks

| Check | Failure reason |
|---|---|
| session/project | `SIGNAL_INVALID` |
| binding | `GOAL_UNBOUND`, `GOAL_BINDING_EXPIRED`, `GOAL_MISMATCH` |
| claim | `CLAIM_MISSING`, `CLAIM_MISMATCH`, `CLAIM_DUPLICATE` |
| plan tasks | `TASKS_INCOMPLETE` |
| evidence | `EVIDENCE_MISSING`, `EVIDENCE_INVALID` |
| facts chain | `FACTS_DIRTY` |
| handoff | `HANDOFF_MISSING` |
| memory write | `MEMORY_WRITE_MISSING` |
| store unavailable | `STORE_BUSY` (warn) |

At `stop`/`turn-end`/`handoff`/`certify`, a missing handoff or memory write is a
warning because the session is still open. At `session-end` and in
`sila gate`, it is a failure. Adoption metrics are advisory only.

### Enforcement points

- `sila hook` at `Stop`/`turn-end` returns the harness's blocking response in
  `enforce`; Cursor is continue-only and is never reported as a hard block.
- `sila gate --pre-commit` and `--full` run the check engine without any hook.
- `sila goals certify` refuses a failing contract unless `--waiver="..."` is
  supplied.
- `sila handoff` records a receipt and appends failed checks to the handoff open
  items.
- `sila goals contract check` is the manual probe.

Receipts are `sila/harness-goal-receipt/v1` at
`.sila/state/goal-contract/<harness>-<session>.json`, mode 0600, append-only
archived under `receipts/`. They contain no tool arguments, prompts, file paths,
transcripts, or secrets.

### Harness facts

`docs/harness-capability-matrix.md` is the per-harness source of truth. Nine
harnesses expose shell hooks; OpenCode and MiMoCode use one shared plugin bridge;
Git uses `sila gate`. Known limits are explicit: Cursor stop is continue-only,
ZCode ignores project hooks, Command Code skips hooks in plan mode, and
Command Code/Antigravity/Codewhale/ZCode have no compaction event.

## Sila Skill Provisioning

`sila install`, `sila onboard`, and `sila heal` provision the canonical Sila skill from an
embedded markdown asset:

- repository: `.agents/skills/sila/SKILL.md`
- global: `~/.agents/skills/sila/SKILL.md`
- discovery symlinks: `~/.gemini/config/skills/sila` and `~/.claude/skills/sila`, created
  only when the harness skill parent directory already exists

The skill carries a quoted YAML description and operational triggers for the 9-verb
continuity loop (`prime`, `recall`, `memo`, `lesson`, `case`, `facts verify`, `gate`,
`handoff`, `sync`), pre-grep archaeology (`sila search --omni`, `sila search expand`,
`sila doc search`), temporal activity (`sila standup`, `sila digest`, `sila recap`),
specialist workflows (`sila guide`, `sila learn`, `sila persona`, `sila hire`, `sila audit`,
`sila skill`), and symbol blast radius (`sila edit-check`, `sila safe-delete`, `sila note`).

`sila onboard check` reports a `Sila skill` item and warns when the file is missing or
stale (missing `sila standup`, `sila case`, `sila persona`, or `sila edit-check`). `sila
heal` adds the skill file to the automatic provision step. A foreign non-Sila skill at a
harness discovery path is never overwritten.

```bash
sila install --dry-run --json   # plan includes .agents/skills/sila/SKILL.md
sila install                    # writes repo + global skill
sila onboard check --strict     # Sila skill item
sila heal --dry-run             # provision step includes the skill when missing
```

## Heal in a Gitignored Subproject

When the current directory has no `.git` and the parent repository ignores it,
`sila heal` refuses to provision there and tells you to initialize it. The
one-step path is explicit and non-interactive:

```bash
sila heal --init-here   # git init -b main here, then the normal heal flow
```

Preconditions: no `.git` in the current directory, a parent repository is
detected, and `git check-ignore` confirms the directory is ignored. If the
directory is already a repository, heal prints "already a repository" and runs
normally. If it is not a gitignored subdirectory, heal exits 2 with the parent
path and the command to run there. `--init-here` cannot be combined with
`--project` or `--dry-run`; it never prompts, never edits the parent
`.gitignore`, and never stages anything. The warning without the flag is
unchanged apart from offering this command.

## Memory Protocol in the Goal Contract

New goals created by `sila goals create` carry `- **Memory Protocol**: required`
and a `## Memory Plan`. Existing goals without the marker are `legacy`: pending
legacy goals produce a warning, and `sila goals validate --strict-memory`
promotes that warning to an error.

Required goals embed a canonical block under `## Memory Writes`:

```json
{
  "schema": "sila/memory-writes/v1",
  "goal_slug": "example-goal",
  "project": "sila-cli",
  "prime": {"ran": true, "command": "sila prime"},
  "recall": [{"query": "error or design question", "matches": 2}],
  "memos": [{"id": "fact_...", "text": "..."}],
  "lessons": [{"id": "fact_...", "symptom": "..."}],
  "cases": [],
  "facts_verify": {"exit_code": 0},
  "handoff": {"id": "hoff_..."},
  "waivers": [{"verb": "case", "reason": "no reusable pattern introduced"}]
}
```

`sila goals certify` parses the block, rejects unknown fields and duplicate
blocks, and validates:

- `prime.ran` (or a `prime` waiver);
- at least one recall query (or a `recall` waiver);
- at least one memo, lesson, or case (or a write waiver);
- `facts_verify.exit_code == 0` (or a `facts` waiver);
- a handoff ID that resolves in the store and is no older than 24 hours (or a
  `handoff` waiver);
- every memo/lesson/case fact ID resolves in the knowledge store.

A missing block fails with `MEMORY_BLOCK_MISSING`; a missing store fails closed
with `MEMORY_STORE_UNAVAILABLE`. Legacy goals certify as before and report
`legacy_skipped`. Waivers live in the evidence block, are reviewed with it, and
must carry a non-empty reason.

## Memory Verb Command Matrix

The four memory verbs share one add/list/show grammar. `memo` is the only one that
previously treated every argument as content; `memo list` is now a read-only
lister and never writes a fact whose claim is `"list"`.

| Verb | Add | List | Show |
|---|---|---|---|
| `memo` | `sila memo "text"` · `sila memo add "text"` · `sila memo --text="text"` | `sila memo list [--since=] [--limit=N] [--claim=<text>] [--json\|--toon]` | `sila memo show <fact-id> [--json]` |
| `note` | `sila note add <symbol> "text" --kind=invariant` | `sila note list` | `sila note list --symbol=<symbol>` |
| `lesson` | `sila lesson "symptom" --cause= --fix=` | `sila lesson list [--since=] [--json\|--toon]` | `sila lesson show <fact-id>` |
| `case` | `sila case "intent" --approach= --invariant=` | `sila case list [--since=] [--json\|--toon]` | `sila case show <fact-id>` |

Memo writes register the project before inserting, so
`sila memo "<text>" && sila memo list` shows the row in the same directory
without `-p`. `--claim=<text>` is the canonical filter; `--claim-text=<text>`
remains as a deprecated alias. `goals reopen --claim` keeps its boolean
meaning because the memo scope owns its own `--claim` value.

Reserved first tokens for `memo` are `add`, `list`, `ls`, `show`, `get`, `delete`,
`rm`, `prune`, and `help`. A reserved word with no valid action exits 2 and writes
nothing. `sila memo "list"` is intentionally interpreted as the list action; use
`sila memo add "list"` or `sila memo --text="list"` for the literal text. Plain
`sila memo "decision text"` remains the shorthand add form. Unknown single-word
first tokens for `lesson` and `case` exit 2 instead of falling through to a write.

`memo list`, `lesson list`, and `case list` use `Store.ListFactsByKind` with
`first_seen DESC, id DESC` ordering; `--project=*` spans every registered
project. `sila memo list --claim=list` (or the deprecated `--claim-text=list`)
is the read-only diagnostic for the two historical junk records
(`fact_40c0cff6`, `fact_cb8f7597`); deletion is out of scope.

## Gate Verdicts and Attribution

`sila gate` computes one verdict and uses it for text, JSON, TOON, and the exit
code. A dirty run always names at least one blocker; `clean` is exactly
equivalent to an empty `blockers` list.

| Status | Default | With `--strict` | With `--require-tests` |
| --- | --- | --- | --- |
| `pass` | clean | clean | clean |
| `warn` (or a `warning` field) | warning row | warning row | warning row |
| `fail` | blocker | blocker | blocker |
| `partial` | blocker | blocker | blocker |
| `unavailable` | advisory | blocker | advisory |
| `skip` | advisory | advisory | `tests` becomes a blocker |

Sentry checks (`sentry`, `arch-sentry`, `rtl-sentry`, `cf-worker-sentry`,
`cfclean`, `reviewer-veto`) are fail-open: only `fail` blocks. A skipped test
run is advisory by default because a repository may have nothing to test; pass
`--require-tests` to promote it.

Text output prints each blocker as `✖ <check>: <cause>` followed by a `fix:`
line. JSON adds `blockers`, `warnings`, `advisory`, `skipped`, and `exit_code`
alongside the existing `clean` and `checks`. Exit codes: `0` no blockers, `1` at
least one blocker, `2` usage.

## Gate Memory-Write Sentry

The pre-commit and full gates include a deterministic `memory-write` check for
goal-attributed closure commits (`feat|fix|refactor|perf|chore` with
`(goals/<slug>/goal.md)` or `[goal: <slug>]`). Docs/test commits,
`chore(goals)` registration commits, unattributed commits, and repositories
without `goals/` return `skip`.

For a scoped commit, the sentry looks for a recent memo, lesson, case, or
operational learning whose `repo_files` intersects the goal's Files to Touch or
the staged files, whose claim contains the goal slug, or whose provenance
matches `SILA_ACTIVE_GOAL`/`SILA_SESSION_ID`. The window starts at the goal's
claim time, else the earliest matching commit, else 48 hours ago. The query is
bounded to 500 facts and fails if it exceeds 200 ms.

A checked-in `.sila/memory-write-waiver.json` can document an exception:

```json
{
  "schema": "sila/memory-write-waiver/v1",
  "goal": "gate-memory-write-sentry",
  "reason": "pure refactor with no durable decision; verified by tests",
  "approved_by": "jad",
  "expires_commit": "<sha>"
}
```

The waiver must name the attributed goal, carry a reason of at least 20
characters, and expire at HEAD or a descendant. An expired waiver fails with
`MEMORY_WAIVER_EXPIRED`; malformed or wrong-goal waivers fail with
`MEMORY_WAIVER_INVALID`. A missing store returns `skip`; an unreadable store
fails closed with `MEMORY_STORE_UNAVAILABLE`. There is no second bypass
environment variable.

## Generic Stop Memory Gate

`sila hook stop` and `sila hook session-end` evaluate `sila/stop-gate/v1` after the
session bridge. The policy is `warn` by default:

- `off` returns `skip` with no output;
- `warn` returns `warn`, prints one bounded line, exits 0;
- `enforce` returns `block` with missing items and recovery commands, exits 2;
- a degraded store returns `degraded`, prints a warning, exits 3.

A `stop` event is evaluated only when the session mutated code or is goal-bound.
`session-end` is evaluated for a claimed goal or a mutating change. Read-only and
documentation-only sessions, non-Sila repositories, `stop_hook_active: true`, and
a third retry return `skip`/`warn` and exit 0. The retry counter lives in
`.sila/state/stop/<session>.json` and is capped at three.

A mutating session needs one grounded memo, lesson, or case; `session-end` also
needs a handoff (the #279 bridge can create it first). A checked-in waiver is
session-scoped and short-lived:

```json
{
  "schema": "sila/stop-waiver/v1",
  "session_id": "cc-123",
  "goal": "generic-stop-memory-gate",
  "reason": "read-only investigation; no durable decision",
  "approved_by": "jad",
  "expires_at": "2026-10-22T00:00:00Z"
}
```

The reason must be at least 20 characters, `expires_at` must be within 24 hours,
and the session must match (or be `"*"`). An expired waiver is
`MEMORY_WAIVER_EXPIRED`; a wrong session is `MEMORY_WAIVER_SESSION_MISMATCH`;
malformed or short-reason waivers are `MEMORY_WAIVER_INVALID`.

`SILA_STOP_GATE=off` is the only one-session bypass. It appends a mode `0600`
record to `.sila/state/stop/bypass.jsonl` and prints a visible warning. The gate is
not a git hook and never retroactively audits old sessions.

Harness matrix: Claude Code and Command Code install a `Stop` hook; Command Code
has no documented `SessionEnd`, so do not invent one. Cursor, Gemini, and Codex
remain manual/unsupported until their stop events are documented. See
[`docs/command-code.md`](command-code.md) for the exact snippet.

## Atomic Per-Project Rule Sync

`sila agents sync` plans every target with writes disabled, then commits a
project only when the plan has zero errors. The plan includes `AGENTS.md`,
`CLAUDE.md`, `GEMINI.md`, Cursor MDC projections, and `.devin/setup.sh`.
`planTargets` exposes `Writes`, `Errors`, and `Atomic`; `commitSyncPlan` writes
only an atomic plan.

- A merged `AGENTS.md` over 24,000 bytes is a preflight error; zero targets are
  written for that project.
- Regular files are written through a same-directory temporary file and rename
  with mode preservation; symlinks use temporary-link + rename with a
  rollback-protected fallback.
- The commit snapshots every touched path (bounded to 8 MiB) and restores it in
  reverse order if a later write fails. A failed atomic project reports
  `atomic=false`, `rolled_back=true`, and `failure_stage=plan|commit|rollback`
  with no partial `files_written`.
- `--all` isolates projects; `multi-sync` adds `skipped`, `atomic_failures`, and
  `files_written` counts while preserving `succeeded`/`failed`.
- `--dry-run` returns the same plan and writes nothing; a second unchanged sync
  writes zero files.

## Memory Adoption Observability

Memory adoption is measured locally in `memory_events` (migration 36):
`project_id`, `session_id`, `agent`, `verb`, `outcome`, `goal_slug`, `source`,
`matched`, and `created_at`. It never stores query, claim, prompt, or path
content; events are excluded from the facts hash chain and retained 90 days.
Recording is best-effort and never retries a locked store.

`sila memory report [--since=7d] [--project=<id>] [--json]` emits
`sila/memory-adoption/v1` with per-verb outcomes, session prime/recall/handoff
rates (empty session IDs are excluded and counted as `unattributed_events`),
implemented-goal memory compliance, and gate pass/fail/waiver/skip counts.
Zero denominators produce 0.0 with an explicit denominator. `sila memory prune
--older-than=90d --yes` deletes old events only.

`sila doctor --memory` is advisory and never changes the doctor exit code:
WARN at >=5 sessions when prime <80%, handoff <70%, or write compliance <90%;
FAIL at >=20 sessions when any rate is exactly 0. `sila stats` adds one line:
`memory: prime N/M · writes N/M · handoff N/M`.

## Goal Claim Charters

`sila goals claim` derives `allow_paths` from the goal's `files_to_touch` plus
the goal package directory. For `sila-onboarding`, the defaults also authorize
`AGENTS.md`, `.gitignore`, `.gitattributes`, `goals/*`, `tests/*`,
`.cursor/rules/*`, and `.agents/skills/*`, because `sila onboard` and
`sila heal` may create them. `.gitattributes` covers the cfclean filter that
`sila heal` writes, so the onboarding goal can stage sila-created files without
a charter deadlock.

## Subagent Continuity Charters

A parent agent can grant a child a signed, local `sila/subagent-charter/v1`
capability instead of creating a second claim:

```bash
sila subagent charter --parent-session=p1 --subagent=s1 --goal=<slug> \
  --lane=lane-a --paths=cmd/sila/hook.go --handoff=parent-only --json
sila subagent verify --charter=.sila/state/charters/s1.json --path=cmd/sila/hook.go
sila subagent status --charter=.sila/state/charters/s1.json --json
```

The charter carries parent session, child id, project, goal, lane,
`allow_paths`, `deny_paths` (always including `research/*` and `.git/*`),
required memory verbs, a 24h expiry, and an HMAC-SHA256 signature using the
local `.sila/state/charter.key` (0600, created on first use). `verify` rejects
tampering (`CHARTER_SIGNATURE`), expiry (`CHARTER_EXPIRED`), malformed input
(`CHARTER_INVALID`), and paths outside the lane (`CHARTER_PATH_DENIED`).

With `--charter=<file>` or `SILA_CHARTER`, `sila memo`, `sila lesson`, and
`sila case` record `session_ref = parent_session_id` and
`origin_role = subagent:<id>`; every referenced path is checked against the
lane before the write. A child never creates a second goal claim. The default
handoff policy is `parent-only`: a child `sila handoff` returns
`written:false` and the parent command instead of writing a duplicate. The
parent remains the canonical handoff owner.

On harnesses that emit `SubagentStart`, the hook also returns a bounded
`sila/subagent-brief/v1` object: parent session, child id, project, active
goal, claim id, `allow_paths`/`deny_paths`, open items, memory verbs, and the
handoff policy (default under 1 KB). A `prime` event with `source=subagent`,
the child session, status, and brief bytes is recorded in the ledger. Missing
IDs or a missing parent return a structured `unsupported` result; the parent
marker is never modified.

`sila hook` normalizes `subagent-start`/`task-start` and
`subagent-stop`/`task-stop`; with `parent_session_id` and `subagent_id` it
creates/closes a child `agent_sessions` row, and otherwise returns
`unsupported` plus the manual charter command. Command Code has no documented
subagent event; use the manual flow. The full MCP profile exposes
`get_subagent_charter` (the continuity profile is unchanged).

## Tool-Call Adoption Ledger

`tool_events` (migration 32) records one privacy-safe row per CLI, MCP, or hook
invocation: `project_id`, `session_id`, `agent`, `source`, `tool_name`,
`category`, `outcome`, `duration_ms`, `goal_slug`, `args_shape`, `created_at`.
It never stores argument values, queries, claims, paths, cwd, transcripts, or
secrets. `args_shape` is a sorted `key:type` list (secret-like keys become
`redacted`), capped at 64 keys and 512 bytes. Events are excluded from
`sila facts verify` and the hash chain.

`SILA_TOOL_LEDGER=off|summary|full` controls recording: `off` records nothing,
`summary` (default) records metadata only, `full` adds `args_shape`. Inserts are
best-effort with a 5 ms bound and drop on a locked store. Duplicate
`(session, source, tool, second)` rows are suppressed. Retention is 90 days;
`sila adoption prune --older-than=90d --yes` deletes older rows manually.

`sila adoption report [--since=7d] [--project=<id>] [--harness=<name>] [--json]`
emits `sila/adoption-funnel/v1`: session funnel (prime, recall, writes, handoff,
gate, omni_search, search_docs, skill_load, edit_check), explicit-denominator
rates, per-tool calls/sessions/harnesses/last-used/category, coverage against
the exposed continuity tools, and `unattributed_events` for empty session ids.
Zero denominators are `0.0` with `denominator: 0`.

`tool_events` owns invocation counts; `memory_events` owns memory-verb semantics
and grounding. They are joined by session/time for reports and are never
double-counted in a rate. `sila doctor --adoption` is advisory: PASS/WARN/FAIL
at the documented 5/20-session boundaries and never changes the doctor exit
code. `sila stats` adds:
`adoption: prime N/M · writes N/M · handoff N/M · tools U/E`.

## Knowledge Hygiene Cadence

Stale file references are claims whose cited files no longer exist. The
maintenance loop is bounded and non-destructive:

```bash
sila facts hygiene --dry-run      # classify: moved, deleted, generated, wrong
sila facts hygiene --yes          # re-point moved files; archive the rest
sila facts verify                 # hash chain must stay 100% intact
sila dream query --stale          # only archived exceptions remain
sila status --json                # stale_claims / stale_facts must be 0
```

- **Repair** re-points a missing path only when exactly one filesystem
  candidate matches (same parent first, then a unique basename) and writes the
  verified path back to `repo_files`.
- **Archive** sets the non-hashed `is_stale` marker on deleted, generated, or
  ambiguous references. The fact, its claim, and its hash are preserved; the
  classification and reason stay in the hygiene report.
- **Cadence**: run the dry-run weekly and before every release. An active
  stale count above 50 triggers a hygiene pass; above 500 triggers a dedicated
  repair goal. `sila doctor` ("Claim Grounding & Staleness") is the health
  check, and `sila dream query --stale` must show only `[archived]` rows.
- **Retention**: archived claims are never deleted. They remain queryable by ID
  and are excluded from the active stale count.

## Prune Authority Boundary

Distillation reconciles derived claims; it never deletes explicit memory.
`PruneFacts` preserves any `Fact` for which `IsAuthorityBearing()` is true:

- explicit memos from the CLI (`session_ref=manual_memo`), MCP (`kind=memo`), or
  legacy writes (`kind=""`, `origin_role=user`);
- lessons and cases (`kind=lesson|case`, or the canonical claim prefix);
- non-goals and milestones;
- approved invariants (`kind=invariant`, or invariant confidence/corroboration).

Candidate rows and derived claims (`kind=code_claim`) remain eligible for
pruning. The boundary is evaluated once in Go and the SQL only deletes the
resulting IDs, so the two cannot drift. Pruning rechains the fact ledger and
cleans FTS. A forced `sila sync --full` must leave `memo list`, `lesson list`,
and `case list` counts unchanged.

The same `IsExplicitMemo` predicate powers the `sila onboard check` "Recorded
directives" probe, so CLI memos, MCP memos, and approved invariants all count;
candidates warn.

## Facts Ledger Integrity

`sila facts verify --explain` classifies every ledger violation and reports the
first divergence. `--json` adds the `sila/facts-verify/v1` fields while keeping
the legacy `total_blocks`, `verified_blocks`, `legacy_blocks`, `genesis_hash`,
`tip_hash`, `intact`, `violations`, `incidents`, and `duration_ms` fields.

Violation classes: `broken_prev_link`, `digest_mismatch`, `missing_hash`,
`genesis_mismatch`, `duplicate_hash`, and `order_gap` (a broken link across a
rowid discontinuity). A violation after the first root is tagged `cascade`;
`summary.roots + summary.cascades` equals the classified total.
`first_divergence` is the earliest non-cascade block. `--limit=N` caps the human
list only; summary counts remain complete. Exit codes: 0 intact/empty, 1
violations, 2 usage errors.

Recovery is explicit and backup-first:

- The ledger is append-only at the writer level: `claim`, `kind`, and
  `origin_role` cannot change on an existing fact (`store.ErrFactImmutable`).
  Corrections append a new fact with `supersedes_id` and mark the old fact
  `is_stale`; they never delete or rehash the old row. Cross-project
  re-attribution and distillation consolidation follow the same rule.

- `sila facts repair --dry-run` shows legacy blocks; `--yes` executes. Without
  either it exits 2. It initializes unhashed legacy rows only and preserves
  mismatch incidents.
- `sila facts rebaseline --reason="..." --yes` rehashes the ledger after an
  authorized change and records the reason/prior tip as an incident. A reason is
  required.
- Both refuse to mutate without a readable backup unless `--allow-no-backup` is
  explicit. The default backup is `<store>.backup-<UTC>.db`, created with
  `VACUUM INTO` and verified with `VerifyChain` before the live store changes.
- A second run with no changes is a no-op. No automatic repair runs in `doctor`,
  `sync`, or `install`.
- Decision tree: intact -> no action; one root plus cascades -> backup then
  rebaseline with a reason; multiple independent roots -> investigate before any
  recovery.

## Store Concurrency & Recovery

Every writer connection uses a **5,000 ms busy timeout** and WAL mode. Write
helpers run through a bounded retry policy: 5 retries, 50 ms base delay, 500 ms
cap, total timeout 5 s, and ±20% jitter. Exhausted contention returns a typed
`store.BusyError` (`Retryable() == true`) that unwraps to the SQLite error, so
callers never see a bare `SQLITE_BUSY` string.

`sila store health` reports:

- store and WAL size;
- schema version and `quick_check`;
- configured busy timeout;
- active writer operations, last lock wait, and retryable event count.

`sila doctor --mcp` reports running `sila mcp` processes with PID, client,
executable, stale flag, and `orphans` (process reparented to PID 1). Sila does
not kill processes automatically. The supported model is one supervised stdio
server per client; stale servers are restarted by restarting their client. A
stale server is not an orphan, and an orphan is reported separately.

Recovery for a locked or unhealthy store:

1. Run `sila store health` and `sila store verify`.
2. Stop extra writers (`sila sync`, `sila goals stats live`) and restart the
   owning client for stale MCP servers.
3. Take `sila backup` before any repair.
4. Run `sila vacuum` only when no writer is active; it checkpoints the WAL.
5. Restore the backup if integrity fails. Never delete `sila.db` as a shortcut.

The concurrency regression test runs 10 writers and 10 readers against a
temporary store and requires zero `SQLITE_BUSY` failures.

## Memory & Continuity Protocol

The managed rulebook carries a compact checklist (at most 12 lines, 900 bytes)
plus the situation-routing lines; the complete `TriggerContract` stays at or
below 16 lines and 1,400 bytes. The shared sources are
`internal/agents.MemoryProtocolChecklist` / `MemoryProtocolExpanded` and
`TriggerRoutingChecklist` / `TriggerRoutingExpanded`; `sila salam` prints both
expanded lists and `docs/reference.md` is the durable reference.

| Verb | Trigger | Command | Artifact | Enforcement |
|---|---|---|---|---|
| prime | task start | `sila prime` | briefing + ready goals | session-start hook |
| recall | stuck / before web search | `sila recall "<symptom>"` | prior resolutions | failure nudge |
| memo | decision | `sila memo "<directive>"` | grounded directive | goal memory contract |
| lesson | solved problem | `sila lesson "<symptom>" --cause=... --fix=...` | grounded lesson | goal memory contract |
| case | pattern choice | `sila case "<intent>" --approach=... --invariant=...` | case record | goal memory contract |
| facts verify | before closure | `sila facts verify` | file/ledger evidence | gate |
| gate | before commit | `sila gate` | quality receipt | pre-commit hook |
| handoff | session end | `sila handoff "<summary>" --open=...` | handoff record | stop gate |
| sync | after changes | `sila sync` | cross-harness continuity | session-end hook |

The compact checklist is generated from one shared list, keeps the managed
markers/metadata byte-stable, and never replaces user content outside the
managed block.

### Situation-to-Command Routing

Run the matching Sila command before a generic fallback (`grep`, `find`,
`git log`, web search, or an ad-hoc script). `CanonicalMemoryLoop` carries the
same matrix as "Specialist Capability Routing (Mandatory Pre-Fallback Checks)".

| Situation | CLI | MCP |
|---|---|---|
| Prior research, specs, or "why did we choose X?" | `sila search "<query>" --omni`, `sila search expand <id>`, `sila doc search "<query>"` | `omni_search`, `get_message_context`, `search_docs` |
| Temporal progress ("what did we work on in the last 24h?") | `sila standup last 24h`, `sila digest --since=24h`, `sila recap` | — |
| Domain rules and codebase learning capture | `sila guide [domain]`, `sila learn` | `get_operational_guidebook` |
| Adversarial review and security/compliance audits | `sila persona list`, `sila hire --for-goal=<slug>`, `sila audit` | `audit_goal` |
| Reusable skills and workflow discovery | `sila skill list`, `sila skill create` | `list_skills`, `skill_search`, `skill_load`, `route_workflow` |
| Symbol blast-radius and epistemic decisions | `sila edit-check --symbol=<sym>`, `sila safe-delete --symbol=<sym>`, `sila note --symbol=<sym>`, `sila decision`, `sila vision triage` | `create_decision`, `list_decisions`, `compare_proposals` |

## Rule Revision Visibility

`sila agents check` reports the managed block revision and three hashes:

- `managed_revision` / `managed_hash`: the on-disk owned block fingerprint;
- `hub_managed_hash`: the fingerprint compiled from the current hub rules;
- `current_hash`: the whole-file fingerprint (changes when user content
  outside the block changes).

`hash_match` compares only the managed body, so a user-only edit keeps
`hash_match=true` while `current_hash` changes. `principles_hash_match` and
`principles_present` report the optional principles block. `target_statuses`
covers AGENTS, CLAUDE, GEMINI, Cursor, and Devin with presence/managed/hash
state.

`drift_kind` is the primary reason, chosen by priority: `error` >
`missing_target` > `principles_drift` > `managed_hash_drift` > `legacy_block` >
`foreign_target` > `in_sync`; `drift_kinds` retains all reasons. A legacy block
with no fingerprint reports `legacy_block` and recommends `sila agents sync`.
`check --all` aggregates `in_sync`, `hash_drift`, `legacy`, `missing_target`,
`principles_drift`, and `failed`, lists only drifted projects, and exits 1 on
any drift or failure.

Sync receipts carry the run-level and per-project `managed_revision` /
`managed_hash`; per-project receipts also record `drift_kind`, `written`, and
`skipped`. Old receipts without these fields load unchanged.

## Session Identity

Sila attaches a local session identifier to memory writes, handoffs, hooks, and
MCP calls. The identifier is never uploaded and is not derived from user
content.

Resolution precedence (`internal/session.ResolveIdentity`):

1. `--session` (explicit flag);
2. `SILA_SESSION_ID`;
3. `<project>/.sila/state/sessions/current` (JSON with `session_id`,
   `project_id`, `harness`, `written_at`);
4. `$XDG_STATE_HOME/sila/sessions/current-<project>.json` or
   `~/.local/state/sila/sessions/current-<project>.json`;
5. none.

A current pointer is valid only when its `project_id` matches the resolved
project and its `written_at` is within 24 hours; stale, mismatched, or malformed
pointers are ignored. `sila session start` writes the project pointer
atomically with mode `0600` and a `0700` directory. `sila session env` prints the
shell exports (`sh`, `fish`, `powershell`).

The local `agent_sessions` registry stores session ID, project, harness, agent,
parent, active goal, claim, cwd, PID, source, branch, HEAD, detached state,
context drift, and timestamps. It is excluded from the facts hash chain, never
uploaded, and retained for 90 days; `sila session prune --older-than=90d --yes`
deletes only closed sessions.

### Resume context integrity

Each session owns a branch/HEAD snapshot recorded at start and refreshed only
for that session ID. On resume the stored snapshot wins: if the current cwd is
on another branch, Sila records `context_drift` (`main@abc -> feature@def`) but
keeps the session's own branch, so a parallel sibling cannot change it. If the
snapshot is missing (legacy) or its commit no longer exists (rewrite/worktree),
the current cwd is re-resolved and the drift is recorded. Detached HEAD stores
the commit with `detached: true`. Subagent sessions snapshot their own cwd and
never update the parent row. `sila session status --json` exposes the fields;
the session-start brief reports the stored branch/HEAD and
`context_source` (`session`, `resolved`, or `unknown`).

Goal binding on `session start --goal=<slug>` never auto-claims. It binds only
when the goal is already claimed by the same session/agent, prints the
`sila goals claim ...` command for an unclaimed goal, and refuses a goal claimed
by another session. A session can bind one goal; moving it requires `--force`.

### Goal Audit, Producer Independence, Memory Grace Window & Subdirectory Scope

- **Deliverable-Coupled Mystery Audit Probes (`sila goals audit`)**:
  `ExtractStrippedContract` parses executable verification commands from
  `## Verification Commands`, `## Verification Plan`, and `## Acceptance Criteria`
  in `goal.md` (and sibling `plan.md`) and synthesizes `CLI-DELIV-001`..`003`
  probes inside the blindfold sandbox while maintaining `CCDSIScore >= 0.40`.
  Host-local absolute paths are scrubbed from `certification.json`,
  `defect-dossier.json`, and `remediation.md`.
- **Session-Bound Producer Independence (`sila goals certify`)**:
  `CertifyGoalWithOptions` checks `claim.SessionID` and `isnad.ImplementedSession`
  via `enforceProducerSessionIndependence` so spoofing a different `--agent`
  within the same producer session is rejected (`session bleed detected`), while
  distinct reviewer sessions succeed.
- **Pre-Claim Memory Grace Window & Mid-Session Handoff Deferral**:
  `memoryWindowStart` applies a 2-hour pre-claim grace window
  (`PreClaimMemoryGraceWindow = 2h`) before `claim.json` `claimed_at` so memos,
  lessons, and cases recorded earlier in the same session satisfy the memory gate.
  Mid-session goal evidence may set `"handoff":{"id":"session-end"}` or
  `"handoff":{"deferred":true}` when `sila handoff` will be recorded at session end.
- **Subdirectory Project Scope & Custom Roadmap Preambles**:
  `sila session`, `sila lesson`, `sila case`, `sila memo`, and `sila handoff`
  resolve a unified canonical `project_id` across subdirectories via
  `goals.ResolveProjectDir` and `st.ResolveScope`. `sila goals` preserves any
  custom user preamble placed above `# Goals Status & Pragmatic Execution Roadmap`
  in `goals/README.md`, and attributes `certRec.CommitSHA` when `goals/` is
  gitignored/private.

## CLI Dispatch & Flag Contract

The root parser owns global flags (`--json`, `--project`, `--db`, `--no-color`,
`--verbose`, `--help`, `--version`) and leaves subcommand-owned flags in place.
`reorderFlagsForSub` recognizes ownership such as `decision -o/--option/-r/
--recommend` even when the root FlagSet registers the same spelling for another
command.

- Unknown top-level command: stderr only, `sila: unknown command "<input>"`, an
  optional `did you mean "<candidate>"?`, then `run 'sila --help' for usage.`;
  exit 2; never the full help dump.
- Explicit help: every `sila <command> --help` writes help to stdout and exits 0
  without executing the command.
- Error usage: missing/invalid arguments write to stderr and exit 2.
- Version/verbose: `-V` or `--version` prints the version; `-v` is verbose only.
  A leading `sila -v` without a subcommand exits 2 with a hint to use `-V`.
- Decision create accepts all three forms:
  `sila decision create "T" -o "A: x" -o "B: y" -r A`,
  `sila decision -o "A: x" -o "B: y" -r A create "T"`, and
  `sila decision create --options="A: x;B: y" --recommend=A "T"`.

### Help Tiers & Canonical Inventory

- `sila --help` and `sila help` print the core loop (10 commands, at most 40
  lines) to stdout and exit 0.
- `sila help all` (or `sila --help --all`) prints the full inventory, one row
  per canonical command, to stdout and exit 0.
- `sila help <command>` is equivalent to `sila <command> --help`.
- `sila help <unknown>` writes a short error to stderr and exits 2.
- Visibility tiers are `core` (default), `standard`, and `advanced` (shown in
  `help all`). Aliases never get their own row; they are annotated on the
  canonical line, e.g. `standup (deprecated alias: recap)` and
  `skill (alias: skills, skill-create)`.
- Help is plain ASCII, contains no ANSI even when color is enabled, uses stable
  column widths per section, and never opens the store or runs a command.
- CLI command counts are separate from the MCP tool inventory; `help all` is
  not an MCP tool listing.

### Conversational CLI (CCLI) Precedence, Opt-Out & Transparency

Canonical commands and aliases are always available, deterministic, and preferred
for scripts and automation. Conversational CLI phrases (`internal/ccli`) provide
an ergonomic natural-language layer governed by strict precedence and opt-out rules:

- **Canonical-First Precedence**: In automatic dispatch (`RewriteAuto`), if the
  first non-flag token resolves to a canonical command or alias and
  `internal/cli.CanonicalAccepts(argv)` accepts the path, the argv is never
  rewritten (even if a phrase production also matches). If a canonical command
  path is invalid (e.g., unknown subcommand), automatic rewrite is allowed only
  for a `phrase-first` production with a recorded rejection witness; otherwise
  the invocation remains canonical.
- **Explicit Phrase Routing (`sila do`)**: `sila do <phrase...> [flags] [-- <positional>...]`
  bypasses canonical-first precedence and routes both direct and overlapping
  (`phrase-only-via-do`) phrases (such as `sila do "sync rules"` or
  `sila do "release goal legacy-backlog" --yes`). It preserves quoted
  single-argument slot values, extracts flags in lenient mode, never invokes a
  shell or `eval`, and is blocked only by `--no-ccli` (never by `SILA_CCLI=off`
  or `SILA_CCLI_DENYLIST`).
- **Opt-Out & Denylist Precedence** (`--no-ccli` > `SILA_CCLI=off` > `SILA_CCLI_DENYLIST` > `auto`):
  - `--no-ccli`: disables automatic phrase rewriting and blocks `sila do` for
    the current invocation; `sila ccli explain` and `sila ccli list` remain
    available.
  - `SILA_CCLI=off|on`: `off` (also `0`, `false`, `no`, `disable`,
    case-insensitive) disables automatic phrase rewriting while keeping
    `sila do` active; `on` (`1`, `true`, `yes`, `enable`) is the default.
    Unknown values warn once to stderr and behave as `on`.
  - `SILA_CCLI_DENYLIST`: comma-separated literal tokens or phrase prefixes
    matched case-insensitively on token boundaries **after flag extraction**.
    Matching phrases are not auto-rewritten (`DENYLISTED`).
- **Transparency Commands (`sila ccli`)**:
  - `sila ccli explain <phrase...>`: side-effect-free inspection of tokens,
    matched production, action, safety tier, target requirement, canonical argv,
    precedence, safety action (`none`/`preview`/`confirm`/`refuse`), exit code,
    and decision (`ROUTE`, `CANONICAL`, `DISABLED`, `DENYLISTED`, `NO_MATCH`,
    `CLARIFY`). Supports human, `--json`, `--json-envelope`, `--toon`, and
    `--porcelain`.
  - `sila ccli rewrite <phrase...>`: prints only the rewritten canonical argv
    without executing; exits 0 on match, 2 on unknown phrase, and 3 (with empty
    stdout) on secret or forbidden phrases.
  - `sila ccli list [--domain=...] [--tier=...]`: lists catalogued CCLI
    productions with optional domain and safety-tier filters.
- **Secret & Forbidden Phrase Redaction**: Any token bound to a sensitive slot,
  matching a secret prefix/keyword, or belonging to a `forbidden_phrase` (such
  as `set config <secret-key> to <value>`) is redacted as `<redacted:N chars>`
  across human, JSON, TOON, and stderr output, with `canonical: null` and exit
  code 3.
- **Safety Tiers, Central Dry-Run & Confirmation (`internal/ccli/guardrails.go`)**:
  - **Tier 1 (`QUERY`)**: read-only, never prompts. Uses default targets only
    when the production declares `permitted-default`; `required` targets exit 2
    when missing.
  - **Tier 2 (`MUTATION`)**: obeys `target_policy` (`required`,
    `permitted-default`, `implicit` with stderr scope announcement) and never
    prompts unless `confirmation_policy: optional` is declared.
  - **Tier 3 (`DESTRUCTIVE`)**: requires confirmation before dispatch. Proceeds
    without prompting when user-supplied `--yes`/`-y`/`--force`/`-f` is passed,
    or on single-target productions when `--confirm=<target>` matches the exact
    target (directional productions reject `--confirm` with exit 2 and require
    `--yes`/`--force`). Production `static_flags` (such as `--force` on
    `force vacuum database`) are never treated as user consent. Interactive
    terminals (`StdinIsTTY && StderrIsTTY`) prompt on stderr and accept only
    `yes`/`y`; declining, EOF, non-TTY without bypass, `--no-input`, or machine
    output modes (`--json`, `--json-envelope`, `--toon`, `--porcelain`) exit 3.
    Ctrl-C (`\x03`) during a prompt exits `130` (OS signal footnote outside the
    normative 0/1/2/3/4/5 CCLI table).
  - **Central Dry-Run**: `--dry-run`, `--dry-run=true`, `--preview`, and `-n`
    print the `sila · ccli dry-run` preview on stderr, emit the machine payload
    on stdout in `--json`/`--json-envelope`/`--toon`/`--porcelain` modes, and
    exit 0 without calling the canonical handler (`--dry-run=false` does not
    preview).
  - **CCLI Exit-Code & JSON Error Contract**: `0` success/dry-run, `1`
    canonical check failure only when `preserve_exit: 1` is declared in catalog
    metadata, `2` usage/syntax/ambiguity/missing target, `3` policy/safety
    refusal, `4` runtime/host execution failure (default mapping from host exit
    1), and `5` recovered panic/internal fault. JSON error envelopes use
    `CCLI_POLICY_REFUSAL`, `CCLI_SECRET`, `CCLI_CONFIRMATION_REQUIRED`,
    `CCLI_RUNTIME`, and `CCLI_INTERNAL`.

## Specialist Workflow Contract

The persona, audit, skill, guide, and learn surfaces are a workflow, not a
laundry list. Use them at these trigger moments and keep the evidence:

| Trigger | Commands | Output | Evidence |
|---|---|---|---|
| Need a specialist lens | `sila persona list`, `sila persona get <id>` | persona stance, domain, rubric | record the chosen persona ID and why |
| Group review or goal synthesis | `sila audit web <url\|file> --personas=<id>`, `sila audit squad` | `.sila/reports/<report-id>.json` | `sila reports list` / `sila reports show <id>` |
| Skill token bloat or drift | `sila skill assess`, `sila skill preview` (zero writes), `sila skill apply --skills=<ids>` | assessment JSON + planned diff | keep the before/after assessment; apply only after preview |
| New bespoke skill | `sila skill create <topic> --prompt-only` or `--interactive` | `.agents/skills/<slug>/SKILL.md` | assessment before and after |
| Domain standards | `sila guide <domain>` (`--format=markdown`/`html`) | compiled guidebook | link the exported artifact |
| Recurring fix or guardrail | `sila learn add --domain=... --title=... --symptom=... --fix=... --rule=...`; `sila learn distil --save` | grounded OperationalLearning fact | `sila learn list <domain>` |

Rules: `sila hire` is reserved for group audits and goal synthesis, not routine
drafting; audit output must be stored under `.sila/reports/` and linked, not
pasted into chat; `skill apply` is preceded by `preview` or `assess`; and
`guide`/`learn` output must cite real records. The MCP equivalents are
`consult_personas`, `audit_goal`, `skill_search`/`skill_load`,
`get_operational_guidebook`, and `save_domain_learning`.

## Human Output Contract

Human output is assembled by `internal/render/output.go`:

- **Glyphs**: `✔` success, `✖` failure, `▲` warning, `•` detail, `→` hint,
  `ℹ` info. Color never changes the glyph.
- **Cards**: mutating commands emit one `✔ <Verb> <Object> <ID>` line followed
  by aligned `Label: value` fields; empty lists render `none`.
- **Headers**: `sila · cmd · scope — title` (all four documented forms are
  produced by `RenderHeader`); a header never starts with an emoji.
- **Errors**: `✖ <what failed>` plus `  fix: <command>`. Commands do not prefix
  errors with `sila <cmd> error:`.
- **Projects**: `DisplayProject` renders `<friendly> (<id>)` when both differ,
  otherwise the single available value, so the same project looks identical in
  every card.
- **Paths**: `DisplayPath` renders repository-relative paths inside the repo,
  `~/...` under the home directory, and absolute paths outside. JSON keeps
  absolute paths for compatibility.

## Machine-Readable Output Contract

Sila exposes four distinct machine modes. Only one may be active per invocation.

| Mode | stdout | Exit code on failure |
|---|---|---|
| `--json` | One JSON document in the command's legacy success shape; errors use the error envelope | 1 (check/operational) or 2 (usage) |
| `--json-envelope` | The versioned `sila/json/v1` envelope for both success and error | 1 or 2 |
| `--toon` | Compact TOON; read-only for goals | 1 or 2 |
| `--porcelain` | Stable tab-separated records, one per line, no headers or glyphs; implies `--quiet` | 1 or 2 |

`--json` + `--toon`, `--json-envelope` + `--toon`, and any `--porcelain`
combination with `--json`/`--json-envelope`/`--toon` exit 2 before dispatch.

### Envelope schema

```json
{
  "schema": "sila/json/v1",
  "ok": true,
  "data": {},
  "warnings": [],
  "error": null,
  "mutations": []
}
```

Errors always return `ok: false`, `data: null`, and a stable
SCREAMING_SNAKE_CASE code with an optional fix:

```json
{
  "schema": "sila/json/v1",
  "ok": false,
  "data": null,
  "warnings": [],
  "error": {"code": "DECISION_NOT_FOUND", "message": "decision \"D99\" not found", "fix": "sila decision list"},
  "mutations": []
}
```

Collections are never `null`: list-shaped data, `warnings`, and `mutations`
encode as `[]`. `--json` keeps each command's legacy success shape for
backward compatibility; the envelope is opt-in, except that error paths always
use it.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | Success, no-op, or an explicit advisory skip (`--advisory`) |
| 1 | Check or operational failure (readiness not ready, not found, packet blocked, onboard failure) |
| 2 | Usage error (missing argument, unknown flag, conflicting output modes) |

No mode exits 0 while reporting `ok:false` or `ready:false`. `sila onboard
check` fails with exit 1 by default; `--advisory` restores exit 0 and marks the
JSON output with `"advisory": true`.

### Goals matrix

Human `sila goals` prints the shared header first, then the actionable sections,
then the summary, then the glyph legend. It announces its side effect before
printing:

```text
ℹ goals/README.md will be reconciled after this report (use --no-sync to preview only)
```

`--no-sync` suppresses both the notice and the write. Machine modes stay
read-only unless `--sync` is explicit. Progress bars use one width
(`GoalProgressBarWidth = 10`): `█`/`░` in human mode and `#`/`-` under
`--ascii`. `--ascii` (alias `--plain`) replaces every decorative glyph with a
stable tag (`[ship]`, `[roadmap]`, `[immediate]`, `[indep]`, `[dep]`,
`[claimed]`, `[ready]`) and is fully ASCII; `--no-color` is not the same as
`--ascii`.

The legend is:
`Legend: 🚢 ship · 🗺️ roadmap · 🎯 immediate · ⚡ independent · 🔗 dependent · 🔒 claimed · ▶ ready`.

`status` is the lifecycle (`pending`, `claimed`, `implemented`, `superseded`);
`state` is the epistemic stage (`draft`, `spec_audited`, `claimed`,
`pending_audit`, `implemented`, `remediation`, `tainted`, `superseded`).
`goals --json` exposes `state_label` with a human explanation. The default view
does not print the static agent protocol; use `sila salam` or
`sila goals --help` for the working loop. `sila goals tasks <slug>` uses
`sila · goals · tasks — <slug> (<met>/<total> completed)`.

### Agents sync scope and flags

| Flag | Default | Behavior | Exit impact |
|---|---|---|---|
| `--all`, `-a` | false | Sync/check every registered project (canonical scope) | 1 if any project fails |
| `--global`, `-g` | false | Alias for `--all`; passing both is idempotent | Same as `--all` |
| `--changed-only` | implicit | Write only targets whose content/action changed | 0 for skips |
| `--force` | false | Rewrite canonical AGENTS.md/Cursor/devin targets, normalize modes, repair wrong CLAUDE.md/GEMINI.md symlinks; still skips foreign files without `--replace` | 1 on failure |
| `--replace` | false | Authorize replacing a foreign user-owned file | Unchanged |
| `--dry-run` | false | Plan only; wins over `--force`; nothing is written | 0 unless the plan has errors |

`--force` and `--changed-only` are mutually exclusive (exit 2). `--force`
never implies `--replace` and never edits user content outside the owned block.
A second default sync writes nothing and reports the skipped count. Human and
JSON output both report `written`, `skipped`, `failed`, `changed_only`,
`forced`, and per-target `skip_reason`. Exit codes are `0` success/skip, `1`
project failure, `2` flag validation. `--all --global` is redundant; `--all`
alone is canonical.

### Read-only machine reads

`sila goals --json`, `--json-envelope`, `--toon`, and `--porcelain` do not
mutate `goals/README.md` and do not clear closed claims. Add `--sync` to opt in:

```bash
sila goals --json --sync
```

The success envelope then carries the changed paths in `mutations`
(for example `["goals/README.md"]`). Human `sila goals` keeps its historical
auto-sync unless `--no-sync` is passed. `--quiet` suppresses hints and success
decoration but never suppresses errors or exit codes.

### Porcelain records

| Command | Record |
|---|---|
| `sila goals --porcelain` | `slug<TAB>status<TAB>tier<TAB>blocked<TAB>tasks_met/total<TAB>title` |
| `sila search --porcelain` | `id<TAB>project<TAB>tools<TAB>role<TAB>time<TAB>content` (two-pass: `session<TAB>turn<TAB>score<TAB>user_prompt<TAB>assistant_fix`) |
| `sila handoff --porcelain` | `id<TAB>status<TAB>agent<TAB>branch<TAB>summary`; `none<TAB>project` when no handoff exists |
| `sila handoff list --porcelain` | `id<TAB>status<TAB>agent<TAB>branch<TAB>summary` |
| `sila decision list --porcelain` | `ADDR<TAB>STATUS<TAB>REC<TAB>CHOICE<TAB>TITLE` |
| `sila onboard check --porcelain` | `item<TAB>status<TAB>critical<TAB>fix`; global: `project<TAB>ready<TAB>warn<TAB>fail` |

`--porcelain` output is the stable scripted alternative to JSON: no ANSI, no
headers, no blank lines, and tabs as the only field separator.

### Onboard check contract

`sila onboard check` exits 1 whenever a critical item fails, in both human and
JSON modes. `--advisory` restores exit 0 and marks the report `advisory:true`;
`--strict` remains accepted and is equivalent to the default. `--json` emits
the `sila/json/v1` envelope with `ok:false` and
`error.code:"ONBOARDING_INCOMPLETE"` on failure, keeping the report under
`data` (`project`, `ready`, `pass`, `warn`, `fail`, `advisory`, `items`).
Critical rows carry `*`, with the legend
`* critical - must be resolved before onboarding is complete` below the table.
The header falls back to the repository basename when no project id is
resolved; `--global` uses `global (N projects)`.

## What is `sila`?

**Sila helps you carry lessons between harnesses, then helps you improve the instructions that guide their next attempt.**

The local core works without a model account or API key. Experimental assistance
supports recap, live activity, search, diff review, semantic drift and instruction
previews. All ten remediation findings (F01–F10) have been
remediated in runtime code (see [Architecture & Design Decisions](architecture.md)). Hosted utility decisions remain deferred until authorized
hosted evaluations exist. You review the evidence and adopt instruction changes yourself.

**`sila`** (*Arabic: صِلَة — a connection*) is the cross-harness continuity engine for AI coding agents: one local store for what they learned, what was decided, and what is being worked on. It reads the session logs of ten AI CLI tools plus git, distills them into claims verified against files on disk, and serves them back to any agent as context, compiled rules, and tracked goals.

When switching between Devin, Claude Code, OpenCode, Antigravity, or IDE agents, each session normally suffers from **context amnesia**:
- You re-explain architectural decisions and project constraints.
- Previous agents' debugging discoveries and solutions are trapped in disjointed local database logs.
- Outdated docs or missing context leads to hallucinations and wasted tokens.

`sila` bridges this gap:
1. **Reads (read-only)** raw session logs from 10 AI CLI tools across 35 active repositories.
2. **Deduplicates and ground-truths** claims against actual repository files on disk (`file_refs_exist`).
3. **Distills** noise into atomic codebase facts and architectural memos indexed in SQLite FTS5.
4. **Primes** agents in sub-milliseconds at session boot (`sila prime` / MCP `prime_context`).
5. **Audits & tracks goals** across `goals/` packages and git commits with Plannotator review hooks (`sila goals`).
6. **Tracks** tactical session handoffs, active tasks, and git branch state (`sila handoff`).
7. **Crawls & attaches** external API docs, package specs, and web documentation (`sila doc`).
8. **Compiles** modular Hub & Spoke agent rules (`AGENTS.latest.md`, `CLAUDE.md`, `.cursorrules`).
9. **Guarantees local quality gates** with zero CI workflow dependency (`sila doctor`, `sila gate`).

### Source curation rules

Sources classify their own records, and sila obeys that classification rather
than guessing from content. Curation fails open: an unknown shape is ingested,
and only records the source itself marks synthetic are skipped.

- **ZCode**: user rows whose `semantics.origin` is `agent_runtime` or `system`
  are runtime injections (todo reminders, task notifications, compact
  summaries) and are not ingested; `real_user` and unknown origins are.
- **MiMoCode**: `message.agent_id` plus `actor_registry.mode` classify each
  actor; turns from spawned (sub-agent) actors are not ingested, and neither
  are text parts the source marks `synthetic` (reminders, skill documents,
  rendered tool output). An unregistered actor is ingested only when
  `agent_id` is the human-facing `main`.
- **Claude Code**: `isMeta` context, `last-prompt` echoes, and synthetic
  API-error notices are skipped.
- **AGY**: user turns are stored as the user wrote them, with the harness
  envelope removed.

The per-tool source paths live in the
[support matrix](../README.md#source-support-matrix).

### 🏛️ Hierarchical Memory Architecture (Zero Vectors, Zero Embeddings)

Sila structures project knowledge into four semantic tiers backed by a single local SQLite database (`sila.db`) with FTS5:

| Tier | Intent & Purpose | Sila Primitives | How It's Used |
| :--- | :--- | :--- | :--- |
| **Hot (Active State)** | What you are working on *right now* | `sila handoff`, `sila goals` | Prevents agents from losing context or repeating work mid-task |
| **Warm (Canonical Invariants)** | What must *never* be broken | `sila memo`, `sila adr` | Canonical constraints, verified runbook commands, and architectural stances |
| **Cold (Chronological Journal)** | What happened in the codebase | `sila sync`, `sila search` | Append-only multi-tool conversation transcripts, tool outputs, and git commits |
| **Reference (Knowledge Lookup)** | External documentation and specifications | `sila doc` | Official framework docs, package READMEs, and crawled API references |

*(Independent benchmarks like LongMemEval Oracle demonstrate that structured, file-based SQLite FTS5 memory achieves 95.6% retrieval accuracy—outperforming complex vector databases without cloud costs, embedding latency, or fuzzy hallucinations).*

---

## The Problem: Every Tool Has Its Own Harness & DB

Today's developer uses multiple specialized AI agents: **Claude Code** for fast terminal loops, **Devin** for long-running overnight refactors, **Antigravity (AGY)** for pair-programming, and **OpenCode / MiMoCode** for focused sub-tasks.

Every single one of them has its own private harness and database:
- **Devin** keeps runs in `.devin/sessions.db`.
- **Claude Code** stores transcripts in `~/.claude/projects/`.
- **MiMoCode** records notes in private `MEMORY.md`.
- **Antigravity / AGY** maintains isolated context databases.

### What happens without `sila`?
- **Total Amnesia Across Tools**: When Devin spends 45 minutes fixing a gnarly race condition on Tuesday, Claude Code and AGY have zero knowledge of it on Wednesday. They repeat the same failed experiments.
- **Lost Baton Passes**: Moving work from Devin to Claude Code means manually copy-pasting terminal logs and explaining what was done and what remains open.
- **Accumulated Hallucinations**: Private agent databases remember files that have since been deleted or refactored on disk, causing agents to attempt edits on phantom code.
- **Concurrent Stomping**: Launching two autonomous agents simultaneously results in file collisions and merge conflicts because neither harness knows what the other is touching.

### What `sila` adds to every harness:
1. **Unbroken Memory Hub**: Automatically extracts architectural invariants, memos, and verified facts from all tools into a single, unified local SQLite store (`sila.db`).
2. **Standardized Baton Passing (`sila handoff`)**: Every agent reads and updates the same structured handoff record (summary, open task checkboxes, decisions made, checked-out git branch).
3. **Physical Disk Grounding (`refs∃`)**: Sila verifies every claim against the physical filesystem. Claims referencing deleted or moved files are flagged stale (`file_refs_exist = 0`).
4. **Multi-Agent Locking & Collision Defense (`sila goals claim --touch`)**: Atomic claim locks prevent agents from stomping on the same files concurrently.
5. **Architectural Arbiter (Non-Goals & RPM)**: Autonomous tournament scoring (`sila vision triage --compare`) checks candidate plans against explicit project non-goals before code is written.

---

## Why `sila`? (Comparison vs Alternatives)

| Feature | `sila (صِلَة)` | Mem0 | Letta (MemGPT) | Cursor / IDE Memory | Individual Tool Harness (Devin, AGY, Claude) |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **Primary Focus** | **Cross-harness memory, rules, and goals for AI coding agents** | General LLM chat memory | Agent operating system / state | Single-IDE editor chat history | Single-tool internal execution |
| **Multi-Tool Ingestion** | **10 AI CLI tools + Git** (MiMoCode, OpenCode, Devin, Codex, Command Code, Codewhale, AGY, ZCode, Claude Code, Muse — see the [support matrix](../README.md#source-support-matrix)) | ❌ API-only (Manual push) | ❌ Agent-internal only | ❌ Editor-siloed only | ❌ Isolated to that one tool |
| **Baton Passing** | **Structured handoffs with task checkboxes** | ❌ None | ❌ None | ❌ None | ❌ None (cannot hand off) |
| **Storage & Privacy** | **100% Local SQLite** (`~/.local/share/sila/`) | Cloud SaaS / Vector DB | Postgres / Local server | Proprietary Cloud Index | Siloed private local/cloud DB |
| **Filesystem Grounding** | **Automatic (`refs∃` on disk verification)** | ❌ No disk validation | ❌ No disk validation | ⚠️ Partial open tabs only | ❌ Trusts past agent generation |
| **Context Priming Latency** | **Sub-millisecond (`< 5ms`)** | ~200-500ms (Cloud API) | ~100-300ms (Server call) | Editor-managed | Built-in context load |
| **MCP Native Support** | **41 JSON-RPC Tools** | ⚠️ Limited wrapper | ⚠️ Agent framework | ❌ Client only | ❌ Server client only |
| **Goals & Roadmap Tracking**| **Scaffolding, Dependencies, Claim Locks, Plannotator** | ❌ None | ❌ None | ❌ None | ❌ None |
| **Multi-Agent Collision Prevention** | **Atomic Claim Locks & Touch File Registry** | ❌ None | ❌ None | ❌ None | ❌ None (collisions likely) |
| **Rules Compiler** | **Exports `AGENTS.md`, `CLAUDE.md`, `.cursorrules`** | ❌ None | ❌ None | ❌ None | ❌ None |
| **Zero-CGO Dependency** | **100% Pure Go (`modernc.org/sqlite`)** | Python / Node.js | Python server | Closed-source binary | Varies by vendor |

### Empirically Measured in Sandboxed Battle Tests

To verify these capabilities under realistic pressure, `sila` includes a reproducible, automated battle-test suite (`internal/assess/battletest_test.go`) simulating real-world agent handoffs (e.g. Devin → Claude Code) in isolated sandboxes:

| Metric | Without `sila` (Isolated Harnesses) | With `sila` (Sandboxed Benchmark) | Measurable Impact |
| :--- | :---: | :---: | :--- |
| **Cross-Agent Baton Passing** | ❌ Lost (0% context transfer) | **100% Retained** | Full task checklists & branch state transferred across tools |
| **Context Priming Latency** | ~200-500ms (Remote/LLM summary) | **7.2ms** (`< 10ms`) | Instant local SQLite injection into prompt turn 0 |
| **Non-Goal Guardrail Defense** | ❌ Violated (Postgres built despite constraint) | **100% Rejected (RPM)** | Competing proposals scored against project invariants before coding |
| **Trial Verification Latency** | Direct repo mutation / dirty branch | **10.5ms** | Disposable Git worktree sandbox executed & auto-cleaned |

---

## Core Capabilities

### 1. Instant Session Context Priming (`sila prime`)
Sub-millisecond briefing for agent startup. Delivers active handoffs, task checkboxes, hard architectural memos, distilled facts and file-referenced claims (each under its own heading), and attached docs straight to your agent or terminal prompt.

```bash
sila prime
```

### 2. Goals & Roadmap Audit with Plannotator (`sila goals`)
Pure-Go goals lifecycle and audit engine that scaffolds standardized goal packages, models prerequisite dependencies (`Depends On` & `Mode: Independent ⚡ / Dependent 🔗`), manages multi-agent claiming & lock files (`claim.json`), matches verifying git commits (`git log` SHA attribution), auto-syncs `goals/README.md`, and triggers the locally installed [Plannotator](https://github.com/backnotprop/plannotator) CLI for visual interactive plan reviews.

Goals support **dual task shapes**:
- **🚢 Ship** (default): Code modification tasks that produce commits. Subject to file collision checks — two ship tasks touching the same files cannot be claimed simultaneously.
- **🔭 Scout**: Non-mutating investigation/audit tasks that produce report artifacts. Exempt from file collision checks — multiple scout tasks can read the same files concurrently without blocking ship tasks.

```bash
# Scaffold a new ship goal (default) with goal.md, facts.md, and plan.md
sila goals create user-roles --title="User Roles & Permissions" --depends-on="database-migration" --touch="internal/auth/,cmd/api/" --seq=2

# Scaffold a scout goal (non-mutating investigation, exempt from collision checks)
sila goals create audit-auth --title="Audit Auth Architecture" --shape=scout --touch="internal/auth/"

# Scaffold a goal from a typed work package contract (sila/work-package/v1)
sila goals create user-roles --work-package=contracts/user-roles.json --json
```

Goals support typed task execution contracts conforming to schema
`sila/work-package/v1`. Specifying `--work-package=<path>` validates the
complete contract, writes `goals/<slug>/work-package.json` under advisory lock,
and populates `plan.md` with self-contained task identifiers (such as T-001).
Structured tasks require verification receipts and reject plain checkbox toggles.

```bash
# Check goal task contract readiness (coverage, symbols, scope limits, spec review)
sila goals readiness user-roles
sila goals readiness user-roles --review=contracts/user-roles-review.json
sila goals readiness user-roles --json

# Output self-contained task execution packet (Markdown or JSON)
sila goals packet user-roles T-001
sila goals packet user-roles T-001 --json

# Execute declared checks and persist candidate-bound task evidence
sila goals evidence user-roles T-001 --run
sila goals evidence user-roles T-001 --run --json

# Import externally produced task evidence receipts
sila goals evidence user-roles T-001 --import=receipt.json
sila goals evidence user-roles T-001 --import=receipt.json --json

# Inspect recorded evidence attempts
sila goals evidence user-roles T-001
sila goals evidence user-roles T-001 --json

# Evaluate and record task acceptance
sila goals accept user-roles T-001 --selection=selection.json --review=review.json
sila goals accept user-roles T-001 --selection=selection.json --review=review.json --json

# Checkpoint in-progress or blocked task state
sila goals checkpoint user-roles T-001 --from=checkpoint.json
sila goals checkpoint user-roles T-001 --from=checkpoint.json --json

# Resume task execution from verified checkpoint
sila goals resume user-roles T-001
sila goals resume user-roles T-001 --json

# Apply explicit, versioned contract amendment
sila goals amend user-roles --from=amendment.json --package=work-package-v2.json
sila goals amend user-roles --from=amendment.json --package=work-package-v2.json --json
```

Task readiness gates enforce that goals with a work package satisfy all
prerequisites before code modifications begin: 100% acceptance criteria coverage,
referenced Go AST symbol existence, file scope limits, acyclic task dependency
graphs, and valid spec reviews (`sila/spec-review/v1`). `sila goals claim`
blocks claims for unready work packages unless overridden with `--force`.

Task execution packets (`sila/task-packet/v1`) package all necessary context
for an individual task: purpose, task contract, losslessly rendered acceptance
criteria, global constraints, non-goals, and cryptographic source pins for
read, edit, and reference files. Pre-dispatch validation detects spec drift,
source drift, and missing predecessor artifacts without modifying the packet.

Candidate-bound task execution evidence (`sila/task-evidence/v1`) records
immutable verification receipts for each declared acceptance case check,
binding results to the exact candidate commit and Git tree content digest.
Checks execute locally with process isolation and timeout guards, supporting
go test -json, exact stdout, and RFC 6901 JSON pointer assertions.
Imported receipts are strictly validated, prevented from escaping goal paths,
and forced to `origin=imported` with attempt collision rejection.

Task acceptance gates (`sila/acceptance-result/v1`) evaluate explicit receipt
selections and reviewer attestations against mandatory acceptance cases.
Both direct certification and auto-run certification on structured work packages
require verified acceptance for all mandatory task criteria, closing legacy evidence
bypasses. Validated acceptance results unblock dependent task packets and bind
predecessor candidate, spec, and artifact digests.

Typed conditional dependencies gate successor goals on cryptographically verified
predecessor artifacts and scalar decisions (`accepted_goal`, `accepted_task`,
`artifact_digest`, `decision_equals`). Active conditions are evaluated on every read,
propagating invalidations and reopen cascades to dependent tasks. Structured decision
gates strictly forbid `--force` overrides, ensuring an assessment ending in revise or
stop never unlocks a release goal. Machine-readable blockers are surfaced in JSON
and CLI views (`sila goals view <slug> --json`, `sila goals blocked --json`).

Task checkpoints (`sila/task-checkpoint/v1`) persist execution progress across
blockers and interrupted sessions with monotonic sequence progression and append-only
attempt tracking. Repeated failure signatures automatically trip circuit breakers
to prevent unassisted churn. Resuming a task reconstructs continuation packets,
verifies specification and candidate drift, and selects the next uncompleted step.
Contract amendments (`sila/task-amendment/v1`) allow operators to alter work packages
with atomic revision progression, underdeclared impact checks, and retired ID retention.

```bash

# Inspect full goal specifications, execution plans, invariants, and claim state
sila goal view <identifier>          # resolve by 1-based index (e.g. 1) or slug (exact/prefix)
sila goals view <identifier> --json  # machine-readable full epistemic goal specification
sila goals show <identifier> --toon  # token-optimized TOON output for agents

# View all implemented goals without capping (or inspect full history)
sila goals implemented               # (alias: sila goals done)
sila goals --all                     # show full uncapped history alongside active goals
sila goals --toon                    # token-optimized TOON summary and tabular output

# Goal counts: a sidebar-sized line, or a self-refreshing view (replaces `watch -n 20 "sila goals stats"`)
sila goals stats --line              # Goals: Ready 2 · WIP 0 · Blocked 1 · Done 232 (add --global for all projects)
sila goals stats live                # redraw the line every 20s until Ctrl-C (--interval=5s, minimum 1s)
sila goals stats live --full         # redraw the full stats view instead of one line
sila goals implemented --toon        # TOON table of all implemented goals (ideal for agents)
sila goals implemented --json        # machine-readable JSON list of implemented goals

# Filter only unblocked, conflict-free goals ready to execute right now
sila goals ready

# Filter disjoint / independent parallelizable goals for worker swarms
sila goals independent

# Filter blocked goals waiting on prerequisite tasks
sila goals blocked

# Filter contested goals with file collisions against currently claimed goals
sila goals contested

# Inspect active file concurrency collisions across goals
sila goals conflicts [slug]

# Claim & lock a goal package (enforcing file disjointness; use --force to override)
sila goals claim user-roles --agent=claude-code --note="evaluating schema"

# Update live execution progress note & structured status (in_progress, blocked, evaluating, done)
sila goals report user-roles --status=evaluating "Verifying authorization schema"
sila goals status user-roles blocked "Waiting on upstream auth PR"

# Release a claimed goal back to the pending queue
sila goals release user-roles

# Release stale claims (dead or inactive processes) with a preview first
sila goals release --stale --older-than=72h --dry-run
sila goals release --stale --global

# Remove claim records left on goals that already closed
sila goals prune --claims --dry-run
sila goals prune --claims --global

# Reopen an implemented goal package for remediation (clears completion status, supports --claim)
sila goals reopen user-roles --reason="critical regression in auth" [--claim]

# Inspect sub-task checkboxes and execution plan progress
sila goals tasks user-roles

# Mark sub-task steps complete or pending (supports individual indices, lists, and ranges)
# Works with checkbox (- [ ]) and Plannotator numbered (1. 2. 3.) plan steps
sila goals task user-roles check 1 2 3
sila goals task user-roles check 1-5
sila goals task user-roles uncheck 2

# Execute atomic two-stage goal commit protocol:
# Stage 1: Verifies goals/README.md is unstaged, commits implementation with attribution
# Optional: Runs ./build.sh rebuild to refresh embedded commit SHA
# Stage 2: Auto-reconciles goal status and commits goals/README.md in a separate docs(goals) commit
sila goals commit user-roles -m "feat(auth): implement role-based access"
sila goals commit user-roles -m "feat(auth): implement role-based access" --no-build

# Audit roadmap with automatic git staging or commit
sila goals --stage
sila goals --commit="docs(goals): sync roadmap progress"

# Filter only claimed / in-progress goals
sila goals claimed

# Active-goal binding for dashboards and sidebars (alias of claimed): one call
# returns each claimed goal with its claim block. `claim.claimed_by` and
# `claim.claimed_at` identify the holder; `tasks_total` / `tasks_met` track plan
# progress. `sila goals --json` carries the same per-goal fields for a full audit.
sila goals active --json

# Deterministic session-to-goal binding: record which goal this agent session
# works on (claims the goal when free, refreshes the binding when the same agent
# already holds it). One session binds to one goal per repository.
sila goals focus user-roles --session="$OPENCODE_SESSION_ID" --agent=opencode

# Read the binding back: exactly the goals whose claim carries that session,
# never a transcript heuristic. Unknown sessions return count 0.
sila goals active --session="$OPENCODE_SESSION_ID" --json

# View pending goals across ALL repositories in your workspace
sila goals global --ready

# The global view is compact by design: one line per non-implemented goal,
# projects ordered by pending work (alphabetical tiebreak), finished
# repositories collapsed into a footer, and a closing "Needs attention" block
# with one section per finding. Each section states what was found (repos,
# goals, ages), one sentence of why it exists, and the exact commands for a
# human and an agent. Counts are distinct: alias duplicate registrations are
# merged before aggregation and reported with the merge command, and lookalike
# repositories that merely share goal slugs are reported separately as
# not-mergeable. Machine consumers read the same detail from the additive
# `stats` object in `sila goals --global --json`; existing JSON field names and
# shapes are unchanged.

# Audit goals for a specific repository by name or directory
sila goals example.com
sila goals example-app ready

# Update goal dependencies or attributes with cycle validation & auto-sync
sila goals update <slug> --add-dep=auth-service
sila goals update <slug> --remove-dep=old-dep
sila goals update <slug> --depends-on=dep1,dep2
sila goals update <slug> --independent

# Manage dependencies directly
sila goals depend <slug>                        # view current dependencies
sila goals depend <slug> add <dep>              # add dependency
sila goals depend <slug> remove <dep>           # remove dependency
sila goals depend <slug> set <dep1,dep2>        # replace dependencies
sila goals depend <slug> clear                  # clear dependencies (mark independent)

# Attribute completed goal to a git commit
sila goals attribute <slug> --commit=<hash>      # attribute to specific commit
sila goals attribute <slug> -c=<hash>            # short flag form
sila goals attribute <slug> <hash>               # positional commit argument

# Re-attribute complete goals after a history rewrite (report-first)
sila goals reconcile --tree                     # dry-run report
sila goals reconcile --tree --apply             # write receipts, not certification
sila goals reconcile --tree --apply --allow-dirty
sila goals reconcile --tree --json              # {candidates, applied}

# Supersede a goal absorbed by another goal or commit
sila goals supersede <slug> --by=<other-goal> [--reason="absorbed in v2 architecture"]

# Certify a goal from automated test or verification evidence
sila goals certify <slug> --evidence=test-results.json [--note="all unit tests passing"]

# Import Plannotator JSON package (interview.json, facts.meta.json, plan.md)
sila goals import <slug> [--from=.plannotator/my-plan/]

# Open visual plan review in Plannotator
sila goals review <slug>

# Validate goal packages, dependencies, lifecycles, and registry synchronization
sila goals validate                            # validate all goal packages in repository
sila goals validate <slug>                     # validate a specific goal package
sila goals validate --json                     # machine-readable diagnostic report with error/warning counts
sila goals validate --strict                   # fail (exit 1) on warnings as well as errors
sila goals validate --manifest=<path.json>     # assert expected status, sequence, tier, and task counts
```

### Tree reconciliation after a history rewrite

`sila goals reconcile --tree` proposes a goal only when its package has
`goal.md`, `plan.md`, and `facts.md`, every plan checkbox is checked, every
`files_to_touch` entry exists in the current work tree (globs are expanded),
and the goal is pending or implemented-unverified. Verified goals, claimed
goals, superseded goals, and goals with a current `tree-attribution.json` are
never proposed.

`--apply` requires a clean work tree (or `--allow-dirty`), writes
`goals/<slug>/retrospective.json` with status `implemented` and
`goals/<slug>/tree-attribution.json` (version, HEAD, tree hash, matched files,
`attributed_by: tree-reconcile`), then resyncs `goals/README.md`. It never
writes `certification.json`, never sets `verification_status=verified`, and
never changes the normal commit matcher. `--dry-run` wins over `--apply`. JSON
output is `{candidates: [{slug,status,matched_files,missing_files,plan_checked,
plan_total,head,action}], applied: [...]}`; missing files and unchecked plans
are excluded, not reported as candidates.

## Repository Goal Templates

`sila goals create` supports custom templates to customize `goal.md`, `facts.md`, and
`plan.md` outputs.

Sila discovers templates from:
- `.sila/templates/goals/`
- `.sila/templates/`
- `.github/sila/templates/`
- `templates/goals/`
- Custom directory passed via `--template-dir=<path>`

Template filenames can use the target file name or `.tmpl` suffixes:
`goal.md`, `goal.md.tmpl`, or `goal.tmpl`.

## Goal Template Variables

Templates execute through Go text/template with `GoalTemplateData`. The table below
lists every exported variable:

| Variable        | Type     | Description                                               |
|:----------------|:---------|:----------------------------------------------------------|
| `Slug`          | string   | Normalized goal slug identifier                           |
| `Title`         | string   | Human-readable title of the goal                          |
| `Description`   | string   | Brief description or summary                              |
| `Mode`          | string   | Mode label: Independent or Dependent                      |
| `IsIndependent` | bool     | True when goal has no prerequisites                       |
| `DependsOn`     | []string | Slice of prerequisite goal slugs for range loops          |
| `DependsOnStr`  | string   | Comma-separated prerequisite slugs, or "none"             |
| `Sequence`      | int      | Execution order sequence index                            |
| `Shape`         | string   | Task shape: ship (default) or scout                       |
| `Tier`          | string   | Roadmap tier: immediate, roadmap, nice-to-have, wont-fix   |
| `Personas`      | []string | Slice of assigned persona slugs for range loops           |
| `PersonasStr`   | string   | Comma-separated persona slugs                             |
| `Status`        | string   | Lifecycle status: pending or implemented                  |
| `Commit`        | string   | Verifying git commit hash if closed                       |
| `FilesToTouch`  | []string | Slice of declared file paths to touch                     |
| `Invariants`    | []string | Slice of architectural invariants                         |
| `Tasks`         | []string | Slice of plan task descriptions                           |
| `Date`          | string   | Scaffolding date in YYYY-MM-DD format                     |
| `Year`          | int      | Scaffolding UTC year (such as 2026)                       |
| `Month`         | int      | Scaffolding UTC month (1 to 12)                           |
| `Day`           | int      | Scaffolding UTC day of month (1 to 31)                    |

## Dual Slice and String Formats

Distinguish between slice variables and pre-formatted string counterparts:

- Slice variables: Use slice fields (`DependsOn`, `Personas`, `FilesToTouch`,
  `Invariants`, `Tasks`) with `{{range}}` loops to render custom bullet lists.
- Pre-joined strings: Use string fields (`DependsOnStr`, `PersonasStr`) for scalar
  embedding in YAML or markdown metadata blocks without writing range loops.

Printing a slice variable directly (such as `{{.DependsOn}}`) renders raw Go slice
brackets (`[dep-a dep-b]`). Always use `{{range .DependsOn}}` or `{{.DependsOnStr}}`.

## Template Functions

Sila provides four built-in helper functions:

- `lower`: converts text to lower case (`{{lower .Shape}}`)
- `upper`: converts text to upper case (`{{upper .Slug}}`)
- `trim`: strips leading and trailing whitespace (`{{trim .Description}}`)
- `join`: joins slice elements with a separator (`{{join .FilesToTouch ", "}}`)

## Template Examples

Custom `goal.md` template (`.sila/templates/goals/goal.md`):

```markdown
# Goal: {{.Title}}

## Goal Description
{{.Description}}

## Dependencies & Execution Order
- Mode: {{.Mode}}
- Depends On: {{.DependsOnStr}}
- Sequence: {{.Sequence}}
- Shape: {{.Shape}}
- Tier: {{.Tier}}

## Files to Touch
{{- range .FilesToTouch}}
- {{.}}
{{- else}}
- none
{{- end}}
```

Custom `facts.md` template (`.sila/templates/goals/facts.md`):

```markdown
# Facts: {{.Title}}

## Invariants
{{- range .Invariants}}
- {{.}}
{{- else}}
- No explicit invariants declared.
{{- end}}
```

Custom `plan.md` template (`.sila/templates/goals/plan.md`):

```markdown
# Plan: {{.Title}}

## Execution Steps
{{- range .Tasks}}
- [ ] {{.}}
{{- else}}
- [ ] Phase 1: Implement core functionality.
{{- end}}
```

### 3. Tactical Session Handoffs (`sila handoff`)
Pass the baton cleanly between agent sessions. Captures summary descriptions, open tasks (`- [ ]`), completed items (`- [x]`), architectural decisions, author attribution, and auto-detected git branches.

```bash
# Record an active handoff
sila handoff "Refactored JWT auth refresh" \
  --open="Add e2e integration tests, update swagger spec" \
  --decisions="Token TTL set to 15m; refresh rotation enabled"

# View the latest handoff for the current project
sila handoff

# Machine-readable sessions: --json always emits one JSON object. A project
# with no handoff emits the same key set with "status": "none".
sila handoff --json
```

### 4. Explicit Architectural Directives and Solution Lessons (`sila memo`, `sila lesson`)
Record hard constraints that every future agent must know, and record solved problems in the shape the next session can recall. Memos are directives; lessons are fixes. Both are authority records, both are indexed in SQLite FTS5, and both surface in `prime` context and rule files.

```bash
sila memo "Always use pure-Go SQLite; never introduce CGO dependencies."

# Record a solved problem: symptom, cause, fix, evidence
sila lesson "xlsx export hangs on large sheets" \
  --cause="shared strings held in memory" \
  --fix="stream rows with ExcelJS" \
  --files=scripts/export.ts --tags=xlsx,export

# Read lessons back (grounding shown per record)
sila lesson list --since=30d
sila lesson show <id>
sila lesson list --json

# Distill lessons proactively from recent session transcripts
sila lesson distill [--since=24h]
sila lesson distill --save
```

A lesson is a deliberate record, like a memo: it keeps authority even when its evidence files are gone, but grounding is always shown (`grounded` or `evidence files missing`). Writing a lesson names up to three similar existing lessons first, and refuses an exact duplicate unless `--force` is passed. `sila recall` ranks lessons above claims and transcripts.

### 5. Agent Experience Feedback & Continuous Learning (`sila feedback`)
Provides a closed-loop learning and ticket lifecycle system for AI agents and developers to log real-time observations, feature recommendations, challenges, tips, and praise. Records are tracked with statuses (`open`, `in_progress`, `closed`, `wontfix`), resolution attribution, and indexed in `sila.db` for verifiable continuous improvement.

```bash
# Log a recommendation or feature suggestion
sila feedback "Consider adding an auto-reconnect hook for MCP" --type=idea

# Log feedback with an explicit mnemonic ID (or let Sila auto-derive epistemic grammar)
sila feedback "Dialectic subshell paths guidance" --id=dialectic-subshell-paths --type=idea

# Log an issue or friction encountered
sila feedback "Remote crawler failed on large sitemaps" --type=issue

# Share feedback anonymously with Sila maintainer via Cloudflare D1
sila feedback "Context window estimation should account for tool schemas" --type=idea --share

# Share an existing local feedback record to Cloudflare D1 edge
sila feedback share fb_12345

# Drain edge feedback items into local store (maintainer only)
sila feedback drain --token="<SILA_EDGE_AUTH_TOKEN>"

# Submit feedback upstream directly to GitHub Issues (with sanitization & confirmation)
sila feedback "Sync stalls when parsing malformed JSONL in Codex" --type=issue --issue

# Submit an existing local feedback item upstream to GitHub Issues
sila feedback submit fb_12345 [--yes]

# Resolve feedback with goal attribution
sila feedback resolve fb_12345 --goal=auto-reconnect --agent=@antigravity --note="Implemented auto-reconnection in MCP"

# Reopen a previously closed feedback item
sila feedback reopen fb_12345

# View open or closed feedback only (subcommands & flags)
sila feedback open
sila feedback closed
sila feedback --open
sila feedback --closed
sila feedback list --status=open
sila feedback list --status=closed

# View feedback in JSON format for automated agent review
sila feedback open --json
```

Upstream submissions via `--share` or `--issue` run through automated pre-flight sanitization (stripping user home directories, absolute paths, and secrets/tokens):
- Cloudflare D1 Edge (`--share` / `sila feedback share`): Posts anonymous sanitized feedback to `https://sila.jadmadi.net/api/feedback` without requiring GitHub credentials or write permissions.
- Upstream GitHub Issues (`--issue` / `sila feedback submit`): Uses a 3-tier zero-failure fallback:
  1. `gh issue create` if the GitHub CLI is installed and authenticated.
  2. GitHub REST API if `GITHUB_TOKEN` is present in the environment.
  3. Universal pre-filled Web URL fallback opening in the system browser or printing a clickable terminal link.



### 6. Recursive Documentation Site Crawler (`sila doc --crawl`)
Pure-Go cascading crawler that auto-discovers `/llms.txt`, `/sitemap.xml`, or falls back to host/path-prefix bounded BFS crawling to ingest complete documentation trees into local FTS5 search.

```bash
# Ingest single page or local file
sila doc AGENTS.md --title="Project Guidelines"

# Crawl an entire documentation tree (bounded concurrency, normalized Markdown)
sila doc https://docs.anthropic.com/en/docs/ --crawl --depth=2 --max-pages=30

# Full-text search attached documents
sila doc search "prompt caching"
```

### 6. Multi-Provider Web Documentation Search (`sila doc sites`, `doc web-search`)
Search developer documentation across your curated sites or the global web, preview snippets, and auto-ingest top results directly into `sila.db`.

`sila` supports multiple modern search backends with automatic failover:

| Provider | Keys Needed | Free Tier | Recommendation |
| :--- | :---: | :--- | :--- |
| **Brave Search** | **1 Key** | **2,000 searches / mo free** | ⭐ **Recommended (Fastest setup & generous quota)** |
| **Tavily Search** | **1 Key** | **1,000 searches / mo free** | 🤖 **Best for AI Agents (Pre-extracted clean Markdown)** |
| **Google Custom Search** | **2 Keys** (`key`+`cx`) | **100 searches / day free** | 🌐 Google programmable search engine |

#### Quick Setup:
```bash
# Option 1: Brave Search (Recommended)
sila config set brave_api_key "BSA..."

# Option 2: Tavily Search (Best for AI)
sila config set tavily_api_key "tvly-..."

# Option 3: Google Custom Search
sila config set google_api_key "AIzaSy..."
sila config set google_cse_cx "0123456789..."
```

```bash
# Search and automatically ingest top matching pages into project memory
sila doc web-search "sqlite fts5 unicode61 tokenizer" --auto-ingest
```

### 7. Official Package & Registry Documentation (`sila doc pkg`)
Fetch official documentation, versions, and READMEs directly from package registries:

```bash
# Go package documentation (pkg.go.dev)
sila doc pkg modernc.org/sqlite --auto-ingest

# NPM package & TypeScript typings (registry.npmjs.org)
sila doc pkg zod --auto-ingest

# Rust Crate metadata & docs.rs guides (crates.io)
sila doc pkg tokio --eco=rust --auto-ingest

# Python Package & PyPI documentation (pypi.org)
sila doc pkg requests --eco=python --auto-ingest
```

### 8. Developer Problem Solving & Error Resolvers (`sila doc so`)
Search Stack Overflow QA threads, bug workarounds, and error resolutions with accepted answer badges:

```bash
# Search for error messages or troubleshooting guides
sila doc so "sqlite wal mode checkpoint on background thread" --auto-ingest
```

### 9. Open-Source Code Search (`sila doc code-search`)
Search across millions of public GitHub & Sourcegraph repositories to discover real-world library usage patterns, syntax examples, and battle-tested implementations:

```bash
# Search for real-world usage of a Go package
sila doc code-search "modernc.org/sqlite PRAGMA" --lang=go --auto-ingest
```

### 10. Multi-Tool Agent SOP Enforcement & Hub-and-Spoke Compiler (`sila install` & `sila agents`)
Enforces standard operating procedures (SOPs) across heterogeneous AI coding agents (Claude Code, Cursor, Devin, OpenCode, MiMoCode, Codex, Antigravity) through a defense-in-depth architecture:
- **`sila install`**: One-shot multi-tool provisioner. Compiles and deploys canonical rules (`AGENTS.md`, symlinked `CLAUDE.md`, Cursor Rules 2.0 `.cursor/rules/000-sila-core.mdc`, `.devin/setup.sh`), installs the Git pre-commit quality gate (`sila gate install --hook` adds the pre-push pipeline), registers MCP server configurations atomically, and provisions Command Code (`.commandcode/settings.json` lifecycle hooks plus `~/.commandcode/mcp.json`) when `--tools=command-code` is selected or the config directory is detected under `all`. With the default `--tools=all`, it also provisions **user-scope** lifecycle hooks (`hookconfig.InstallUser`) for every detected host harness directory (`~/.claude`, `~/.cursor`, `~/.gemini/config`, `~/.commandcode`, `~/.zcode/cli`, `~/.config/opencode/plugins`) so Sila lifecycle events are active across all workspaces without creating project-local `.claude/`, `.commandcode/`, `.codex/`, or `.agents/hooks.json` files. Trust-required harnesses (`codex`, `codewhale`, `muse`) are never auto-provisioned; request them explicitly with `--tools=<harness>` or `sila hook install --harness=<harness> --user`. `--no-hooks` skips both the git gate and user-scope hook provisioning. Explicit `--tools=zcode` installs user-scope `~/.zcode/cli/config.json` (ZCode ignores project hooks).
- **`sila agents check`**: Continuous SOP drift auditor. Checks all target AI tool rules against `agents/AGENTS.latest.md` using SHA-256 content hashes to detect drift, missing overlays, or orphaned tool configs. Exits 1 on drift so it can gate CI/pre-commit pipelines.
- **`sila agents sync`**: Compiles Core Hub standards with domain overlays (`AGENTS.<domain>.md`) and project overlays (`AGENTS.<project>.md`), projecting updated rules across all detected AI tools.
- **`sila agents status`** (alias: `sila agents last`): Inspects the provenance receipt of the last rule sync, showing timestamp, relative age, Core Hub commit hash, target repository count, files written, and error status.
- **`sila agents review`**: Incident-attributed instruction review. Synthesizes minimal unified diff proposals for project or global instruction files (`AGENTS.md`, `CLAUDE.md`) derived from concrete failure incidents recorded across agent transcripts. Operates in preview-only mode with guaranteed zero writes to instruction files.
- **`sila hook`**: Unified lifecycle execution engine (`session-start`, `session-end`, `stop`, `pre-commit`, `pre-push`, `install`).

```bash
# Provision rules, git quality gates, and MCP configs in one command
sila install

# Audit project tool rules for drift or missing files (current or all workspaces)
sila agents check
sila agents check --all

# Recompile and sync hub standards to all tool targets (current or all workspaces)
sila agents sync
sila agents sync --all --dry-run
sila agents sync --all

# Inspect agent rule sync provenance receipt and fleet freshness
sila agents status
sila agents last --json

# Preview incident-grounded instruction update diffs (zero writes)
sila agents review AGENTS.md --scope=project

# Export universal rule files individually (or preview updates via --review)
sila export rules --target=claude   --out=CLAUDE.md
sila export rules --target=cursor   --out=.cursorrules
sila export rules --target=windsurf --out=.windsurfrules
sila export rules --target=agents   --out=AGENTS.md
sila export rules --review
```

#### Managed block fingerprint (`sila:meta`)

Sila owns exactly one managed block per `AGENTS.md`; everything outside the markers is user content and is preserved verbatim:

```markdown
<!-- sila:begin (managed by sila; content inside is regenerated by `sila install` / `sila agents sync`) -->
<!-- sila:meta rev=1 scope=core hash=9ce683aca0ba1844 -->
...compiled core rules...
<!-- sila:end -->
```

- `rev` is the managed-block schema revision (currently `1`); it is not the sila release version.
- `scope=core` identifies the core hub block. The principles block (`<!-- sila:begin (principles) -->`) is separate and is not fingerprinted here.
- `hash` is the first 16 lowercase hex characters of SHA-256 over the managed body. Canonicalization is explicit: CRLF and bare CR become LF, exactly one trailing newline is removed, the leading project H1 (`# AGENTS.md — <project>`) is excluded, and the begin/metadata/end marker lines are never hashed. Leading whitespace and trailing spaces on content lines are preserved, so any compiled rule change changes the hash while identical rule sets fingerprint identically across projects.
- The metadata line is sila-owned content: it is stripped from preserved user content and never treated as user text.
- **Skip decision:** when the on-disk block metadata has the same revision and hash and the merged bytes are otherwise identical, `sila agents sync` reports `managed hash match (rev=1)` and writes nothing. The short-circuit is scoped to the core block: CLAUDE/GEMINI symlinks, Cursor rules, the principles block, and Devin setup repairs still run. `--dry-run` computes the same decision and hash without writing.
- **Migration:** a legacy block with no valid metadata (`legacy_block: true`) is rewritten once with a fresh fingerprint; the next sync is a no-op. Malformed metadata and unknown scopes are treated as legacy, never as user content.
- **JSON surfaces:** `sila agents check --json` adds `managed_hash`, `hub_managed_hash`, `managed_revision`, `hash_match`, and `legacy_block` (the legacy `core_hub_hash`/`current_hash` fields remain). Sync plans add `managed_hash`, `existing_managed_hash`, `managed_revision`, and `hash_match`; sync results and receipts add `managed_hash`, `managed_revision`, `hash_skips`, and `skipped_files`. Receipts written before fingerprints existed load with those fields absent, and `sila agents status` prints `not recorded (pre-fingerprint receipt)`.

### 11. Autonomous Fact Distillation & Disk Grounding (`sila dream`)
Transforms raw conversation transcripts into crisp, verifiable codebase facts. Scans the local filesystem to verify referenced file paths (`refs∃`) and flags stale claims when files are deleted or moved.

```bash
# Search grounded codebase facts via SQLite FTS5
sila dream query "D1 sqlite migrations" --refs-exist

# Distill pending raw claims into atomic facts via LLM
sila dream --llm=stdio

# Reconcile disk grounding without re-distilling
sila dream reconcile
```

### 12. Local-First Quality Gates (`sila gate` & `sila doctor`)
- `sila doctor [--repair] [--skills] [--covenant] [--edge]`: Validates SQLite page integrity, FTS5 index sync, directory permissions, disk claim staleness, the always-load context surface, running MCP server binary freshness, audits agent skills for bloat, verifies adherence to the Digital Data Sovereignty Covenant, and checks Cloudflare Workers test suites for cross-test storage leakage.
- `sila doctor --covenant [--json] [--toon]`: Audits adherence to the four developer rights of the Digital Data Sovereignty Covenant (local custody, epistemic non-destruction, cognitive portability, and privacy boundaries).
- `sila doctor --edge [--json] [--toon]`: Audits Cloudflare Workers test suites (`@cloudflare/vitest-pool-workers`) for cross-test state leakage, unisolated D1/KV mutations, missing per-test seeding, and Vitest miniflare binding parity.
- `sila gate [--full]`: Verifies complete engineering discipline (full 25-package race detector, statement coverage threshold >= 50%, Go report card Grade A+, exported symbol doc coverage, binary freshness, and scar tissue) before pushing or in CI.
- `sila gate --pre-commit` (alias `--quick`, `--fast`): Fast-path quality gate (<3s target latency) for local git commits; runs changed-package tests, format checks, changelog freshness, and secret sentry while deferring heavy race/SLSA verification.
- `sila gate --scar-tissue [--json] [--toon]`: Audits git diff against historical architectural memos, incident patterns, and AGENTS.md prohibitions to halt regressions.
- `sila gate install [--hook] [--remote]`: Installs native Git `.git/hooks/pre-push` quality gate hook and/or local Git proxy remote (`sila-gate`).
- `sila gate pipeline [--branch=] [--forward=]`: Runs quality gate in an isolated, disposable Git worktree without touching developer's working directory, optionally forwarding push to target remote upon 100% green verification.
- `sila gate run-remote [--branch=] [--forward=]`: Executes gate in isolated worktree and automatically forwards push to remote (`origin`).

```bash
# Audit database health and auto-repair index drift
sila doctor --repair

# Audit registered agent skills and MCP tools for dormancy and prompt token waste
sila doctor --skills

# Verify adherence to the Digital Data Sovereignty Covenant
sila doctor --covenant

# Audit Cloudflare Workers test suites for cross-test state leakage and unisolated storage
sila doctor --edge

# Verify local pre-commit engineering discipline and scar tissue in working tree
sila gate

# Audit git diff standalone against architectural scar tissue and outlawed patterns
sila gate --scar-tissue

# Install pre-push validation hook running in disposable worktree
sila gate install --hook

# Install local git remote proxy
sila gate install --remote

# Run validation pipeline in disposable worktree
sila gate pipeline --branch=main

# Push via proxy remote (validates in worktree, forwards to origin)
git push sila-gate main
```

### 13. Database Maintenance & Self-Vacuuming (`sila vacuum`)
Defragment SQLite storage, reclaim unused freelist pages, checkpoint WAL logs, and run SQLite query planner statistics optimization (`PRAGMA optimize`). `sila` also automatically self-vacuums during `sila sync` when dead pages accumulate.

```bash
# Analyze database fragmentation and potential space savings
sila vacuum --stats

# Defragment database and reclaim free space
sila vacuum
```

### 14. Consistent Snapshots (`sila backup`)
Write a consistent snapshot of the knowledge store with SQLite's `VACUUM INTO`. The snapshot includes every committed transaction even while `sila sync` is writing, which a plain file copy cannot guarantee (committed data may still sit in the WAL sidecar). Destinations are immutable: the command refuses to overwrite an existing file.

```bash
# Timestamped snapshot next to the store
sila backup

# Explicit destination
sila backup --out=~/sila-backup-2026-09-15.db

# Verify the snapshot before relying on it
sila doctor --db=~/sila-backup-2026-09-15.db
```

### 15. Exposing the Cockpit (`--expose`)
By default the cockpit binds loopback and only this machine can reach it. `--expose` authorizes a non-loopback bind so other devices can open it, and it changes the trust contract in two ways worth stating plainly:

* **Reads become network-reachable.** The launch token guards mutations only. Anyone who can reach the port can read transcripts, claims, and handoffs through the read endpoints. Expose a store you are willing to serve, not one holding sensitive history.
* **Mutations require the token** printed at startup (also included in the URL the cockpit opens, which seeds a `SameSite=Strict` launch cookie).

Host, origin scheme/host/port, and JSON media type are validated on every request, so a page on another origin cannot drive the API.

### 16. Storage Footprint (`sila storage`)
Show how much disk space sila's knowledge store and each source tool's data take, with totals. Read-only: opens the store read-only and never touches the source tools.

```bash
sila storage          # table: store files, per-tool sizes, totals
sila storage --json   # machine-readable report
```

Store sizes include the WAL/SHM sidecars, which carry committed state. Tool sizes cover exactly what each loader reads (for Codex that includes the sibling `sessions/` rollout tree). Git is excluded — its storage is every repository itself. Trimming sila means `sila vacuum` for its own store; source data belongs to the tools.

### 17. Self-Update (`sila update`)
Keep `sila` current with a single command. Rebuilds cleanly from source or downloads the latest standalone binary from the release tag:

```bash
sila update
```

### 18. Living Technical Taste & Opinions (`sila opinions`)
Compile collective codebase decisions, verified architectural directives, and hard-won stances into a clean, durable `OPINIONS.md` file (or export via `sila export opinions`). Follows the single-source-of-truth pattern to align human developers and autonomous agents on project conventions:

```bash
# List distilled architectural opinions and stances
sila opinions list --toon

# Add an opinion stance directly
sila opinions add --category=architecture --stance="Pure-Go SQLite: Keep CGO_ENABLED=0 with modernc.org/sqlite"

# Compile and write OPINIONS.md to repository root
sila opinions compile --out=OPINIONS.md
```

Frozen surface: no new features. If it stays unused, the next major release deprecates it.

### 19. TOON Format & AXI Ergonomics (`--toon`)
Adopt [AXI (Agent eXperience Interface)](https://github.com/kunchenguid/kun) principles and [TOON (Token-Oriented Object Notation)](https://toonformat.dev) across all agent-facing commands (`sila search`, `sila prime`, `sila dream query`, `sila credits`, `sila opinions`). `--toon` delivers ultra-dense, token-optimized output (30-60% savings compared to JSON) with explicit headers, clean delimiters, and zero JSON quoting overhead:

```bash
# Prime context in TOON format
sila prime --toon

# Search codebase facts in token-optimized format
sila search "wal mode" --toon
```

### 20. Tool Attribution & Credits (`sila credits`)
Inspect formal attributions, inspirations, and upstream research (Kun Chen's `kun`, `axi`, `backpass`, `firstmate`, `treehouse`, `TOON`, `modernc.org/sqlite`, and the AI agent ecosystem). Filter entries by query argument:

```bash
sila credits
sila credits backpass
sila credits --toon
```

Frozen surface: no new features. If it stays unused, the next major release deprecates the query mode.

### 21. Strategic Vision Engine & Tournament Proposal Triage (`sila vision`)
Compile a living `VISION.md` that synthesizes closed roadmap goals, delivered capabilities, active milestones, and explicit **negative constraints (non-goals)** to prevent agent scope creep. Run autonomous proposal triage or multi-candidate tournament comparisons before writing code:

```bash
# Evaluate a proposal against project non-goals and invariants
sila vision triage "Add external Postgres cloud sync adapter" --toon

# Run round-robin tournament comparison across competing architecture proposals (RPM)
sila vision triage --compare "Candidate A: Pure-Go SQLite" "Candidate B: CGO Postgres"

# Add an explicit negative constraint (what we will NOT build)
sila vision add-nongoal "No cloud database dependencies (100% pure-Go SQLite only)"

# Compile living VISION.md to repository root
sila vision compile --out=VISION.md
```

### 22. Disposable Worktrees & Sandboxed Pilots (`sila worktree`)
Manage isolated Git worktrees for agent sessions and run time-bounded sandboxed pilot trials:

```bash
# Create an isolated worktree for an agent session
sila worktree create agent-auth-session

# Run a 30s sandboxed execution trial in a disposable worktree (auto-cleaned)
sila worktree pilot go test -run TestSanity

# List active sila-managed worktrees
sila worktree list --toon
```

### 23. First Mate Crew Orchestrator (`sila crew`)
Spawn and supervise multi-agent crews in visible tmux sessions with disposable Git worktrees. Each crewmate runs in an isolated worktree on branch `sila/crew-<id>`, auto-seeded with `SILA_CONTEXT.md`. Zero-token supervision uses OS process probes (`kill -0`) and tmux pane activity — no LLM API calls. Auto-gated completion runs `sila gate` in the worktree before reporting done.

```bash
# Spawn a crewmate (devin, claude, agy, opencode, codex, mimocode)
sila crew spawn devin firstmate-crew-orchestration

# List active crewmates with liveness status
sila crew list --toon

# Check detailed status of one crewmate
sila crew status devin-123456

# Run a zero-token supervision pass (probes liveness, auto-gates completed tasks)
sila crew supervise

# Terminate a crewmate and clean up its worktree
sila crew kill devin-123456
```

### 24. Versioned Releases and Compatibility

Stable Sila releases use annotated Git tags in `vMAJOR.MINOR.PATCH` form. A
patch fixes behavior without changing the CLI or MCP contract; a minor release
adds backward-compatible commands, flags, tools, or optional fields; a breaking
change requires deprecation and the next breaking release. Untagged builds are
development builds and intentionally do not report a stable version.

Every release publishes uncompressed platform binaries, `checksums.txt`, MCP
contract metadata, and the SQLite schema version. Before tagging, run:

```bash
scripts/release.sh v0.5.0 --dry-run
scripts/release.sh v0.5.0
```

The script creates the annotated tag, builds assets for all
platforms, and writes release metadata locally; it pushes nothing unless
`--push` or `--publish` is explicit. Read the complete
[release procedure](releases.md) before publishing.

### 25. Local Web Cockpit & Decision Interface (`sila ui` / `sila web`)

Transform Sila's multi-agent SQLite knowledge store into an interactive, local decision cockpit embedded directly within the single standalone binary. Serves a modern, dark-themed Single Page Application over loopback (`127.0.0.1:7452`) with zero external runtime dependencies (no Node/NPM or external CDN requests):

- **Knowledge Healing**: Inspect ungrounded claims (`file_refs_exist=0`), examine missing disk references, trigger on-disk file verification, and promote verified claims to durable facts.
- **Feedback Board**: Closed-loop ticket triage (`open`, `in_progress`, `closed`, `wontfix`) with resolution attribution and goal linkage.
- **ADR Studio**: Browse and verify living MADR decision records, check disk-drift health, and promote store memos into formal ADR files.
- **Strategic Vision Triage**: Evaluate candidate proposals against project non-goals and milestones with automated RPM tournament scoring.
- **Swarm Radar**: Real-time goals dependency matrix, task checklists, and live tmux/crewmate process liveness.
- **Omni-Search Explorer**: Full-text transcript search across 10 AI CLI tools with multi-turn conversation turn unrolling (`window=N`).
- **Live SSE Telemetry**: Server-Sent Events (`/api/events`) pushing real-time store updates and watcher heartbeats.

```bash
# Launch decision cockpit and auto-open browser on default port 7452
sila ui

# Alias with custom port and headless/no-open mode for remote or CI environments
sila web --port=8080 --no-open
```

### 26. Host Tool Source Auto-Discovery (`sila sources`)

Inspect the local host OS environment and discover which AI coding tools are installed, where their session/log data lives, how many sessions are discoverable, and what to do next. Read-only — never writes to another tool's data:

- **Discovery matrix**: probes candidate paths for every known tool (Claude Code, Cursor, MiMoCode, OpenCode, Devin, Codex, Command Code, Codewhale, Antigravity, Git).
- **Status classification**: `indexed` (sila ingests it, watermark exists), `ready` (data found, never ingested), `detected` (installed but not yet ingestable), `missing`.
- **Session counts**: best-effort distinct-session counts straight from each tool's own store (SQLite COUNT, JSONL distinct session ids, transcript walks) plus store message attribution.
- **Actionable guidance**: one concrete next step per tool (`sila sync`, `sila status`, or why a tool is not indexable yet).

```bash
# Human-readable discovery matrix with per-tool guidance
sila sources

# Machine-readable matrix for agents and scripts
sila sources --json

# Show every detected log path with size and modification time
sila sources --verbose
```

### 27. Ephemeral Demo Playground (`sila demo` / `sila ui --demo`)

Launch the Web Cockpit against an ephemeral, pre-populated sandbox knowledge store — no real data, no setup, no leftovers. The sandbox ships a realistic multi-agent team dataset for a fictional project (`atlas-web`):

- **Cross-agent transcripts**: a payment-incident triage thread across Devin, Claude Code, Codex, OpenCode, and MiMoCode with cross-tool corroboration merges, plus search/roadmap and infra/ops sessions.
- **Claims & guardrails**: architectural invariants, explicit non-goals, a milestone, grounded code claims (file-verified), and a stale claim for the Knowledge Healing flow.
- **Contested goals**: a live goal board with a claimed goal (active agent lock), a ready goal, a blocked goal (unmet dependency), and an implemented goal (git-attributed).
- **ADRs & feedback**: two MADR decision records and a feedback board spanning open/in-progress/closed tickets.
- **Ephemeral by construction**: the sandbox store and project tree live in a temp directory and are deleted when the cockpit shuts down.

```bash
# Launch the demo playground and auto-open the browser
sila demo

# Machine-readable launch (CI / scripting)
sila demo --json --no-open

# Equivalent form via the cockpit command
sila ui --demo

# Query the live demo store from the CLI while the playground runs
sila search payment --db=/tmp/sila-demo-*/sila.db
```

### 28. Human Standup & Multi-Agent Activity (`sila standup`, `sila digest`)

Sila provides distinct, purpose-built semantics for humans and AI agents:

1. **Human Morning Standup (`sila standup`)**: Human-first daily standup briefing detailing architectural decisions recorded, commits shipped, open action items (handoff checklists + unresolved feedback), and items needing human approval (unverified claims referencing missing files, claimed goals in progress).
   - Supports natural human lookback windows: `sila standup last 5h`, `sila standup last 2d`, `sila standup last 1w`, `sila standup last 30m`, or `sila standup 5h`, `2d`, `30m`.
   - Supports global workspace view: `sila standup last 5h --global` (or `-g`) aggregates activity, decisions, and shipped commits across all registered projects in the workspace, with source project attribution (`[sila]`, `[atlas-web]`).
2. **Recent Session Activity (`sila digest --activity`)**: compact, source-attributed session history across tools (Codex, OpenCode, Muse, Devin, Claude Code, etc.) without requiring a search term or manually written handoff.
3. **Agent Multi-Agent Standup (`sila digest`)**: 1-page briefing for AI agents of active sessions by tool, invariants, closed goals, open handoff items, and stale claims (`--toon` for token-efficient TOON format, `--json` for automation).

```bash
# Human morning standup for current project (default: last 24h)
sila standup

# Natural duration windows
sila standup last 5h
sila standup last 2d
sila standup last 1w
sila standup last 30m
sila standup 5h

# Global workspace standup across all projects
sila standup last 5h --global
sila standup -g last 2d

# Recent session activity for agents or inspection (single project)
sila digest --activity
sila digest --activity --project=sila --since=7d
sila digest --activity --project=sila --tool=opencode --top=5
sila digest --activity --json
sila digest --activity --toon

# Agent-facing executive standup
sila digest
sila digest --since=7d --project=atlas-web
```

Each entry in `--activity` mode shows:
- A date-stamped UTC time (`MM-DD HH:MM`, so a multi-day window is never ambiguous), the contributing tool, and a shortened session ID.
- Earliest user request in the window (at most 160 Unicode characters), with its own stamp.
- Latest assistant reply in the window (at most 160 Unicode characters), with its own stamp — the two stamps are printed together because the earliest request and the latest reply can be hours apart and are not one exchange.
- Long excerpts wrap to the terminal width under the text column, so a continuation line cannot read as another entry.
- Direct drill-down inspection command (`sila search expand <id> --occurrence=<n>`).
- Strict single-project scope (`--global` is rejected) and read-only indexed history execution.

### 29. Interactive Quick Launcher (bare `sila`)

Running bare `sila` in an interactive terminal shows a 4-option quick launcher instead of a wall of help text — press a number to launch, `q` to quit. Piped/CI invocations keep the classic usage + exit 2:

```bash
# bare invocation in a TTY
sila
#   1  Launch Web Cockpit   (sila ui)
#   2  View Roadmap Matrix  (sila goals)
#   3  Search Transcripts   (prompts for terms, then sila search <terms>)
#   4  Run System Doctor    (sila doctor)
```

### 30. Smoke A/B Verification and Andon Cord Halts (`internal/andon`, `sila store`, `sila gate smoke`)

Sila incorporates Toyota Production System (TPS) style **Andon cord** protection and **Smoke A/B pre-flight validation** before any high-stakes or irreversible state mutations (such as schema migrations, bulk fact updates, or vacuum operations).

1. **Path A (Smoke Dry-Run Sandbox)**:
   - Sila opens an isolated dry-run transaction.
   - The candidate mutation is executed inside this sandbox.
   - Post-mutation invariants (`PRAGMA quick_check`, table schemas, foreign key integrity) are strictly verified.
   - The dry-run transaction is **always rolled back cleanly**, guaranteeing zero side-effects on the live database.
2. **Andon Cord Pull (Instant Abort & Recovery)**:
   - If Path A detects any invariant violation, SQL failure, or panic:
     - The Andon cord is pulled immediately (`andon.Cord.Pull`).
     - Any registered rollback callbacks execute in LIFO order.
     - A structured `HaltError` is emitted with an emergency diagnostic banner, code (e.g. `ERR_ANDON_MIGRATION_HALT`), and actionable remediation guidance.
     - Sila exits cleanly (exit code 70) without corrupting the production database.
3. **Path B (Live Commit)**:
   - Only when Path A passes 100% of invariants does Sila proceed to execute the mutation in a live transaction and commit.

```bash
# Verify knowledge store metrics, schema version, and integrity
sila store status

# Execute pre-flight Smoke A/B dry-run sandbox check
sila store smoke

# Run deep PRAGMA integrity_check and foreign key validation
sila store verify

# Run smoke A/B check inside the gate pipeline
sila gate smoke
```

### 31. Protected Literals and Constant Hygiene Guard (`internal/lint`, `sila gate constants`)

To prevent silent behavioral drift and duplicate sentinel strings during autonomous agent refactoring, Sila includes a native Go AST analyzer (`internal/lint`) that audits codebase hygiene.

- **Sentinel Error Hygiene**: Detects bare `errors.New("...")` calls repeated across files, proposing typed package constants (e.g. `ErrRecordNotFound`).
- **Domain Literal Factoring**: Detects repeated magic strings used in domain switches, status codes, and JSON-RPC method routing.
- **Intelligent Filtering**: Automatically exempts test files (`*_test.go`), struct field tags (`json:`, `yaml:`, `db:`), package import paths, SQL statements (`SELECT`, `CREATE`), printf format verbs (`%s`, `%d`), and UI decorative lines.
- **High Performance**: Scans 200+ Go source files in under 100 milliseconds using standard library AST tools with zero external dependencies.

```bash
# Audit repository for bare magic strings and candidate constants
sila gate constants

# Machine-readable JSON output
sila gate constants --json
```

## Agent Coordination Subsystem (optional)

Goals, crew, worktree, and the gate pipeline form one optional subsystem for
teams that want agents to coordinate on a shared roadmap. `sila goals` is the
entry point:

- `sila goals` tracks the roadmap, claim locks, file collisions, dependencies,
  and commit evidence (section 2).
- `sila worktree` creates disposable Git sandboxes for isolated runs
  (section 19).
- `sila crew` spawns harnesses in those sandboxes with zero-token supervision
  (section 20), and `sila gate pipeline` validates a branch in a disposable
  worktree before it is pushed (section 12).

The subsystem is optional. The core loop (`sync`, `search`, `prime`, `handoff`,
`memo`, `doc`) works without it. New coordination features extend `sila goals`
instead of adding more top-level commands.

### 32. Universal Multi-Toolchain Clean & Disk Hygiene Engine (`sila clean`)

Modern multi-repo and polyglot workspaces accumulate gigabytes of build artifacts, test dumps, compiler caches, and package stores across heterogeneous toolchains. `sila clean` provides a safe, pluggable, multi-toolchain disk hygiene engine across:
- **Systems & Compilers**: Go (`GOCACHE`, `testcache`, `coverage.out`, `*.test`, `cpu.pprof`), Rust/Cargo (`target/`, registry cache), Zig (`zig-cache/`, `~/.cache/zig`).
- **Package Managers & Runtimes**: pnpm (`pnpm store prune`, `.pnpm-store`), aube (`aube store prune`), npm (`npm cache clean`, `_cacache`), Python uv/pip (`uv cache clean`, `pip cache purge`, `__pycache__`), Bun (`bun pm cache rm`), mise (`mise cache`).

```bash
# Interactive preview with confirmation prompt across all detected toolchains
sila clean

# Dry-run inspection (zero disk writes)
sila clean -n

# Non-interactive cleanup execution
sila clean -y

# Clean specific toolchains
sila clean go
sila clean pnpm aube

# Scope cleanup
sila clean --repo     # Scope cleanup to repository-local artifacts (coverage, test binaries)
sila clean --global   # Scope cleanup to global user caches and package stores
sila clean --heavy    # Include network-dependent caches (modcache, node_modules)

# Machine-readable output
sila clean --json
sila clean --toon
```

Integrated with `sila doctor` to warn whenever toolchain caches exceed 2 GB, and exposed via MCP tools `audit_hygiene` and `clean_caches`.

## Security, Privacy & Data Recovery

### 1. Local-First Privacy
* The core loop (`sync`, `search`, `prime`, `handoff`, `memo`, `doc`) operates **entirely offline and on-device**; no telemetry or background analytics.
* Explicit exceptions, always user-initiated: `sila doc web-search`, `code-search`, `pkg`, and `so` query external documentation services when invoked, and an MCP client receives retrieved context according to its own provider's data policy.
* No transmission of code or conversation logs to external servers outside those explicit commands.

### 2. File & Key Permissions
* **API Credentials**: Stored locally in `~/.local/share/sila/config.json` with strict `0600` (user read/write only) file permissions.
* **Knowledge Store**: Located in `~/.local/share/sila/` with strict `0700` private directory and `0600` database/SQLite-sidecar permissions.

### 3. Read-Only Source Protection
* `sila` opens every external source tool database with `mode=ro` and `PRAGMA mmap_size=268435456` (256 MB).
* **Guaranteed non-mutation**: `sila` never alters, locks, or writes to any source tool's internal database files (including Devin, MiMo, Codex, OpenCode, and ZCode).

### 4. Database Integrity & Backup Recovery
* **Crash-Resilience**: Utilizes SQLite Write-Ahead Logging (WAL) and pure-Go SQLite for atomic transactions.
* **Self-Healing Diagnostics**: Run `sila doctor --repair` at any time to automatically rebuild corrupted FTS5 virtual tables and optimize page allocations.
* **Backups**: Never copy `sila.db` alone. A live store keeps committed transactions in its WAL sidecar (`sila.db-wal`), so a bare `cp` can produce a snapshot that is missing committed data or will not open. Take a consistent snapshot instead:
  ```bash
  # Consistent snapshot via VACUUM INTO (safe while sila sync is running)
  sila backup --out=~/sila-backup-$(date +%F).db

  # Verify the snapshot before trusting it
  sila doctor --db=~/sila-backup-$(date +%F).db
  ```
  Restore by stopping any running `sila` process, moving the current store aside, and copying the verified snapshot back to `sila.db`.
* **Schema lifecycle**: `sila` refuses to open a store whose schema is newer than the running build, and applies migrations transactionally so a crash cannot leave the schema and its recorded version out of step. Query commands read through a read-only handle, so they never change data. One exception: a store whose schema is behind the running build is upgraded first (the queries need the current columns), which also applies the store file permissions — back up before running an older store if that matters.

### 5. Pluggable Gateways & Outbound Redaction
* External relay gateways (`sila gateway`) are **strictly disabled by default**; zero background connections, listeners, or outgoing webhooks run unless explicitly enabled and configured via `sila config`.
* All outbound notification payloads automatically undergo secret scrubbing (`gateway.Redact`), redacting bearer tokens, API keys, private key blocks, passwords, and local home directory paths (`/home/... -> ~`).

### 6. Cryptographic Hash-Chained Fact Certification Ledger
* **Immutable Audit Trail**: SQLite `facts` table schema extended with `fact_hash` and `prev_hash` columns (schema migration 19). Every committed fact, lesson, and architectural decision forms a cryptographically linked SHA-256 chain originating from the 64-zero `GenesisHash`.
* **Deterministic Canonical Hashing**: Digests are computed across `prev_hash`, `id`, `project_id`, `claim`, `kind`, `session_ref`, `first_seen`, and `origin_role` separated by null bytes (`\x00`).
* **Tamper-Evident Detection**: Any direct SQLite modifications to fact claims, row deletions, or arbitrary injections break the hash chain and are instantly detected.
* **Integrity Audits**: Run `sila facts verify` or inspect via `sila doctor` ("Fact Hash Chain Integrity").

### 7. Operational Learning Kinds & Rule Harmonization
`sila guide`, `sila learn`, and the `guidebook` MCP tool render four distinct record kinds. A case is not a lesson: the renderer never fabricates failure-mode text for it.

| Kind | Terminal labels | Markdown / HTML headings | JSON / TOON fields |
| :--- | :--- | :--- | :--- |
| `lesson` | Symptom, Cause, Fix, Rule | Symptom & Failure Mode, Root Cause, Pivotal Fix, Guardrail Rule & Invariant | `symptom`, `root_cause`, `pivotal_fix`, `guardrail_rule` |
| `case` | Intent, Approach, Invariant | Intent, Approach, Invariant | `kind`, `intent`, `approach`, `invariant` |
| `memo` | Directive | Directive | `kind`, `directive` |
| `decision` | Decision | Decision | `kind`, `directive` |

* **Display-only normalization**: rendering selects labels and harmonizes rules at output time; stored facts and their hash-chained claims are never rewritten. Legacy lesson-shaped records (no `kind`) default to `kind=lesson`.
* **Rule harmonization** (`HarmonizeRule`) is deterministic: trim; capitalize a rule that already starts with a documented imperative verb; remove a redundant `Ensure ` before another imperative; otherwise frame the text as a property with `Ensure `. The documented verbs are `ensure, enforce, always, never, do not, avoid, verify, use, enable, disable, prefer, run, set, add, remove, keep, validate, configure, require, return, wrap`. Examples: `enable WAL and busy_timeout` → `Enable WAL and busy_timeout`; `explicit dependencies` → `Ensure explicit dependencies`; `Ensure enable WAL mode` → `Enable WAL mode`. Output never contains `Ensure Ensure` or `Ensure <imperative>`.
* **Search parity**: `sila guide` search and `sila learn list` match case fields (`intent`, `approach`, `invariant`) and directives in addition to lesson fields.

## CLI Command Reference

### Argument order

Write flags after the command: `sila <command> [flags] [positional]`. A flag
placed before the command is rejected with exit code 2 and a message naming
the flag, because a silently dropped `--db` would run the command against the
live store instead of the store you named. The only flags accepted before the
command are `--version`, `--help`, `--no-color`, and the
`-lp`/`--list-projects` alias.

### 🚀 Daily Agent & Developer Workflows

| Command | Usage | Description |
| :--- | :--- | :--- |
| **`prime`** | `sila prime [--project=] [--top=N] [--budget=8k]` | Output concise session-start context briefing with declarative token compression and always-load budgeting |
| **`goals`** | `sila goals [global\|pending\|ready\|contested\|implemented\|conflicts\|validate] [--all] [--toon\|--json\|--strict] \| view <slug\|N> \| commit <slug> \| review [<slug>]` | Audit goals roadmap matrix, validate packages & DAG cycles, inspect goal specifications, execute two-stage commits, enforce file disjointness & trigger Plannotator |
| **`goal`** | `sila goal [view\|show] <slug\|index> [--json\|--toon]` | Inspect full goal package specification, checklist state, invariants, and claim status by slug or 1-based roadmap index |
| **`search`** | `sila search <query> [--omni] [--semantic] \| claims <query> \| docs <query>` | Full-text search across transcripts, claims, docs, or omni-search with optional System One semantic reranking; equal-score ties prefer fresher evidence (14-day frecency half-life), and definition-shaped documents rank above prose |
| **`recall`** | `sila recall "<symptom\|error\|package>" [--project=] [--top=N] [--semantic]` | Find prior cross-project resolutions before web search or trial and error: lessons first, then grounded claims, then transcripts |
| **`search expand`** | `sila search expand <id> [--window=N] [--occurrence=N]` | Expand verbatim conversation context turns around message, within its recorded source session |
| **`handoff`** | `sila handoff [<summary>] [--open=] [--decisions=]` | View or record structured session handoff |
| **`memo`** | `sila memo <directive> [--project=]` | Record architectural decision into store |
| **`facts`** | `sila facts [verify\|show <id>\|list] [--project=] [--json]` | Audit and verify cryptographic SHA-256 hash-chained fact ledger |
| **`lesson`** | `sila lesson "<symptom>" [--cause=] [--fix=] [--files=] \| list \| show <id> \| distill` | Record, read, or distill structured solution lessons with grounding (solved-before knowledge) |
| **`case`** | `sila case "<intent>" [--approach=] [--invariant=] [--files=] \| list \| show <id>` | Record and read architectural procedural cases (proactive design memory) |
| **`guide`** | `sila guide [domain] [--format=terminal\|markdown\|html\|json\|toon] [--out=path] [--project=]` | Compile and export categorized cross-project developer operational guidebook across 10 engineering domains |
| **`learn`** | `sila learn [list\|add\|export] [--domain=] [--title=] [--symptom=] [--cause=] [--fix=] [--rule=]` | Record, list, and export distilled operational learnings and durable guardrail invariant rules |
| **`sync`** | `sila sync [--project=] [-v|--verbose] [--global] [--dry-run|--preview] [--cloud]` | Ingest from all tools + distill claims with live progress, preview simulation, and optional edge cloud pass |
| **`watch`** / **`live`** | `sila watch [project] [--global] [--compact] [--interval=15s]` | Continuous live 9-box agent mission control cockpit and background sync loop; displays real-time fleet roster, approval steering desk, epistemic stream, concurrency sentry, gate health, roadmap DAG, context tokens, active mutation & diff velocity, and sandbox worktree pool (with cryptographic fact ledger health integrated into the live pulse header; pass `--compact` for legacy 1-2 line ticker) |
| **`stats`** | `sila stats [project] [--plain] [--json] [--toon]` | One-shot snapshot of the 9-box agent mission control cockpit across fleet, approvals, knowledge stream, concurrency, gates, roadmap, context, diff velocity, and worktrees |
| **`doc`** | `sila doc <url\|file> [--crawl] \| serve [--port=N] \| list \| get <id>` | Ingest, crawl, serve, or attach external documentation |
| **`doc search`** | `sila doc search <query> [--global]` | Full-text search attached documentation |
| **`doc web-search`**| `sila doc web-search <query> [--auto-ingest]` | Search live web via Brave/Tavily/Kagi/Google |
| **`doc code-search`**| `sila doc code-search <query> [--lang=] [--auto-ingest]` | Search open-source code across public GitHub & Sourcegraph |
| **`doc pkg`** | `sila doc pkg <name> [--eco=auto\|go\|rust\|npm\|py]` | Fetch official package docs & READMEs |
| **`doc so`** | `sila doc so <query> [--tag=tag] [--auto-ingest]` | Search Stack Overflow QA & error resolutions |
| **`export rules`** | `sila export rules [--target=claude\|cursor\|agents]` | Compile project memory into rule files |
| **`opinions`** | `sila opinions [compile\|add\|list] [--category=] [--out=] [--toon]` | Living technical taste ledger & OPINIONS.md compilation (frozen) |
| **`adr`** | `sila adr new <title> \| list \| compile \| doctor \| from-memo <id>` | Author, compile, and ground living Architecture Decision Records (MADR) with disk-drift detection |
| **`standup`** | `sila standup [last <n>h/d/w/m] [-g\|--global] [--project=<id>] [--since=<dur>] [--toon\|--json]` | Human morning standup: decisions recorded, commits shipped, open items, approvals needed; natural windows (`last 5h`, `last 2d`); `--global` (`-g`) for workspace-wide view |
| **`digest`** | `sila digest [--activity] [--since=24h] [--project=<id>] [--tool=<tool>] [--top=10] [--human] [--toon\|--json]` | Multi-agent activity digest & session ledger for AI coding agents; with `--activity`, shows compact source-attributed session history |
| **`benchmark`** | `sila benchmark [--project=<id>] [--min-sessions=<n>] [--toon\|--json]` | Cross-agent empirical scorecard: grounding rate, hallucination ratio, goal attribution (frozen) |
| **`changelog`** | `sila changelog [--unreleased] [--write] [--tag-range=<range>] [--json \| --toon]` | Synthesize effect-first release notes from git history into Keep a Changelog format; `--json` and `--toon` print machine output and never write files |
| **`worktree`** | `sila worktree create <name> \| list \| clean <name>\|--all \| run <name> <cmd> \| pilot <cmd>` | Disposable Git worktree pool & agent sandbox with context seeding and gate validation |
| **`crew`** | `sila crew spawn <harness> <goal> \| list \| status <id> \| kill <id> \| supervise` | First Mate crew orchestrator: multi-agent tmux sessions with zero-token supervision |
| **`vision`** | `sila vision [compile\|triage\|add-nongoal\|add-milestone\|list] [--toon]` | Strategic North Star, VISION.md compiler & autonomous proposal triage |
| **`approve`** | `sila approve [<id>] [--reject] [--by=...] [--reason=...] [--pending] [--json]` | Human-in-the-loop approval steering for autonomous AI agents via CLI, Telegram, or Discord |
| **`feedback`** | `sila feedback [<msg>] [--share] [--issue] \| share <id> \| drain [--token=] \| submit <id> \| resolve <id> \| reopen <id> \| list [--status=]` | Closed-loop ticket ledger for agent observations, suggestions, Cloudflare D1 edge sharing, upstream GitHub issue dispatch & resolutions |
| **`install`** | `sila install [--tools=] [--no-hooks] [--no-mcp] [--dry-run]` | Provision agent rules, git quality gates, and tool configs in one command |
| **`agents`** | `sila agents [check\|sync\|status\|compile\|list\|review] [--all] [--dry-run] [--target=] [--scope=]` | Hub-and-spoke agent SOP rule compiler, drift auditor, sync engine, provenance ledger, and previewable instruction reviewer |
| **`onboard`** | `sila onboard [--dry-run]` · `sila onboard check [--strict] [--global]` · `sila onboard defer <surface> [--reason=...]` · `sila onboard --convert-tasks --from=<file> [--review --yes]` | Inventory legacy task systems (brain, markdown lists, scaffolds), defer reviewed legacy task surfaces, provision rules/hooks/MCP/plugin, and verify sila participation with fix commands |
| **`heal`** | `sila heal [--dry-run] [--json]` | Bring one repository current in a single command: merge its duplicate registrations, provision rules/hooks/MCP/plugin/goal, auto-configure cfclean filter in Cloudflare repositories, prune closed-goal claims, release stale claims, and list the judgment steps only an agent can finish. Each agent heals the repo it works in |
| **`salam`** | `sila salam [--json] [--yes\|--assist]` (aliases: `hi`, `hello`, `merhaba`) | Agent front door: prints the seven-step working loop with live heal findings, detected harnesses, assistance (JEV) tier state, ready goals, active claims, last handoff, and an interactive 10s onboarding handshake |
| **`hook`** | `sila hook [session-start\|session-end\|stop\|pre-commit\|pre-push\|install]` | Execute agent lifecycle hooks and git quality gates |
| **`upgrade`** / **`update`** | `sila upgrade` / `sila update` (`--check`, `--force`, `--source`, `--json`) | Self-upgrade sila binary via canonical Cloudflare R2 distribution (`https://sila.jadmadi.net/sila/`) with SHA-256 verification and atomic swap |
| **`release`** | `sila release [preflight\|check\|snapshot\|metadata] [--allow-dirty] [--offline] [--strict] [--json\|--toon]` | Software Quality Scorecard & Release Readiness Matrix, MCP contract baseline & release metadata |
| **`credits`** | `sila credits [<query>] [--toon\|--json]` | Display project credits, research inspirations, and AXI attribution |
| **`persona`** | `sila persona [list\|get] [--group=ID]` | Modular persona registry & edge vault across 31 specialist personas |
| **`hire`** | `sila hire group <id> \| <1,4,6,3,8> [url]` | Autonomous multi-persona hiring, group audits, and goal package synthesis |
| **`reports`** | `sila reports [list\|show <id>]` | Browse, inspect, and trace persona evaluation reports in .sila/reports/ |
| **`skill`** / **`skills`** | `sila skill preview \| apply \| remove \| assess \| list \| show \| create` | Project skill governance, filtering, assessment & bespoke skill authoring |
| **`skill-create`** | `sila skill-create <topic...> [--prompt-only\|-i]` | Interactive tailored project skill creator (alias: sila skill create) |
| **`plugins`** / **`plugin`** | `sila plugins [list\|init\|install\|uninstall\|link\|unlink\|lint\|activate\|deactivate]` | Manage standalone Go plugins, project MCP activation, and contributor standards verification |
| **`unslop`** | `sila unslop [paths...] [--check] [--fix] [--diff] [--json]` | Plain English communication hygiene cleaner; scrubs sycophancy, buzzwords, excessive bold, and em dashes |
| **`ui`** / **`web`** | `sila ui [--port=7452] [--host=127.0.0.1] [--no-open]` | Interactive local web cockpit for decision-making, knowledge healing & live telemetry |

### 🛠️ Administration, Ingestion & Quality Gates

| Command | Usage | Description |
| :--- | :--- | :--- |
| **`sync --full`** | `sila sync --full [--global]` | Ignore stored cursors and re-import all history; backfills source provenance for records ingested before occurrence tracking |
| **`ui`** | `sila ui [--host=] [--expose]` | Local cockpit; a non-loopback bind requires `--expose` and then gates mutations behind the printed launch token |
| **`install --replace`** | `sila install [--replace] [--policy-pack=<name>]` | Merge rules into a sila-owned block; `--replace` is required to adopt existing user-owned files |
| **`gate`** | `sila gate [install|pipeline|scar-tissue|doc-coverage|report-card|coverage|smoke|constants] [--pre-commit] [--full] [--scar-tissue] [--doc-coverage] [--report-card] [--coverage] [--json] [--toon]` | Verify engineering discipline (fast pre-commit vs full pre-push), Cloudflare cfclean credential sanitization, audit scar tissue, report card, test coverage, smoke A/B sandbox, or constant hygiene |
| **`doctor`** | `sila doctor [--repair] [--skills] [--covenant] [--edge] [--debt] [--json] [--toon]` | End-to-end SQLite integrity, index sync, skill bloat, Digital Data Sovereignty Covenant, tracked debt comments, and Cloudflare Workers test sandbox audit |
| **`reportcard`** | `sila reportcard [--json] [--toon]` | Builtin local Go Report Card analyzer (gofmt, govet, gocyclo, ineffassign, misspell) |
| **`coverage`** | `sila coverage [--strict] [--profile=path] [--json] [--toon]` | Evaluate test statement coverage profile against quality thresholds |
| **`eval`** | `sila eval run|ablate|init|synthesize [options]` | Universal epistemic evaluation, multi-harness runner & context ablation; enforces zero quality credit on failed executions while retaining denominator integrity |
| **`vacuum`** | `sila vacuum [--stats] [--force]` | Defragment store, reclaim unused pages & optimize planner |
| **`backup`** | `sila backup [--out=<path>]` | Write a consistent `VACUUM INTO` snapshot (safe under active writers) |
| **`storage`** | `sila storage [--json]` | Show disk footprint: sila's store plus each source tool's data |
| **`clean`** | `sila clean [targets...] [--repo\|--global\|--all] [--heavy] [-n] [-y] [--json\|--toon]` | Universal multi-toolchain disk hygiene & cache pruning across Go, pnpm, aube, npm, Cargo, Python, Bun, Zig, mise |
| **`store`** | `sila store [status\|smoke\|verify\|migrate] [--json]` | Inspect store metrics, verify integrity invariants & run Smoke A/B sandbox |
| **`facts`** | `sila facts [verify\|show <id>\|list] [--project=] [--json]` | Audit and verify cryptographic SHA-256 hash-chained fact ledger |
| **`dream query`** | `sila dream query <term> [--refs-exist] [--facts-only]` | Ranked FTS5 search over stored claims |
| **`dream reconcile`**| `sila dream reconcile [<project>] [--dry-run] [--semantic]` | Re-verify referenced files on disk without re-distilling; optional System One semantic audit |
| **`verify`** | `sila verify [<project>] [--dry-run] [--semantic] [--json]` | Re-verify referenced files on disk and detect stale claims with optional semantic audit (alias: dream reconcile) |
| **`dream`** | `sila dream [<project>] [--dry-run] [--llm=stdio]` | Distill claims and atomic facts from messages |
| **`ingest`** | `sila ingest [<tool>\|git\|all]` | Incremental ingestion with watermarks & content hashing |
| **`assess`** | `sila assess [<tool>\|all]` | Dry-run preview of what ingest would pull in |
| **`projects`** | `sila projects` (or `sila -lp`) · `sila projects merge <keep> <drop> [--dry-run] [--force]` · `sila projects rename <project> <new-id> [--dry-run]` | Catalog all indexed repositories and claim counts; merge path-alias duplicate registrations or correct a registration whose id no longer describes its directory |
| **`overlaps`** | `sila overlaps [--project=<id>]` | List cross-tool overlaps and co-occurrence |
| **`status`** | `sila status [--scan]` | Sync health dashboard and stale claim detection |
| **`cloud`** | `sila cloud status [--project=] \| pull [--limit=N] [--project=] \| push [--project=] [--json] [--toon]` | D1 edge bridge: probe connectivity, drain pending conversation mailbox, and publish verified facts/memos/goals to edge replica |
| **`gateway`** | `sila gateway status \| test [<surface>] \| send --title="..." --body="..." [--surface=] [--level=] [--json] [--toon]` | Pluggable external relay: modular adapters for Discord, Telegram, Gemini, and MCP |
| **`sources`** | `sila sources [--json] [--verbose]` | Discover installed AI tools on this host: log paths, session counts, setup guidance |
| **`demo`** | `sila demo [--port=7452] [--no-open]` (also `sila ui --demo`) | Ephemeral demo playground: pre-populated sandbox store for the Web Cockpit |
| **`recap`** | `sila recap [--since=24h] [--project=<id>] [--json]` | Deprecated alias for `sila standup`: decisions, commits shipped, open items, approvals needed |
| **`config`** | `sila config [path|explain|schema|init|set|get <key>] [--reveal]` | Manage API keys, search engine credentials, explain schema, and inspect XDG paths |
| **`doc sites`** | `sila doc sites [add\|rm\|list]` | Manage registered documentation sites |
| **`mcp`** | `sila mcp` | Start Model Context Protocol JSON-RPC stdio server |
| **`intro`** | `sila intro [--agent=] [--out=]` | Agent onboarding guide + AGENTS.md block |
| **`export`** | `sila export [--out=file]` | Dump messages + claims as JSON |
| **`completions`** | `sila completions [bash\|zsh\|fish]` | Print a shell completion script |

---

## Canonical CLI Command & Flag Registry (`internal/cli`)

`internal/cli/registry.go` is the single importable source of truth for Sila command, alias, subcommand, and flag metadata:

- `cli.All() []CommandSpec` and `cli.Lookup(name) (CommandSpec, bool)` expose every canonical command, alias, category, visible tier (`core|standard|advanced|hidden`), subcommand path (`SubcommandSpec`), mutability, and safety tier (`query|mutation|destructive`).
- `cli.Flags() []FlagSpec` and `cli.LookupFlag(name) (FlagSpec, bool)` expose every root and scoped CLI flag, alias, arity, type, scope (`FlagSpec.Scope`), safety class (`none|control|destructive`), and destination.
- `cli.Resolve(argv) (Resolved, bool)` and `cli.CanonicalAccepts(argv) bool` perform static, side-effect-free resolution of command/alias, explicit subcommand path, unknown subcommands, extra positionals, and `--` flag termination without executing commands or touching the store/filesystem.

Contribution rules:
1. When adding a command, alias, or subcommand, register its `CommandSpec` in `internal/cli/registry.go` and add the handler case in `cmd/sila/dispatch.go`. Do not create a parallel command list.
2. When adding a flag, register its `FlagSpec` in `internal/cli/registry.go` and wire it in `cmd/sila/flags.go:newRootFlags`.
3. `cmd/sila/usage_data.go`, `cmd/sila/main.go:isValidSubcommand`, CCLI catalog validation, canonical-first precedence, help tiering, and conformance tests all consume `internal/cli`.
4. Run `go test -race ./internal/cli/... ./cmd/sila/...` (`TestRegistryParityWithDispatchAndFlags`) to verify zero drift between `internal/cli`, `dispatch.go`, and `newRootFlags`.

## Conversational CLI (CCLI) Catalog (`internal/ccli`)

`internal/ccli/catalog.json` (embedded via `//go:embed` in `internal/ccli/catalog.go`) is the single versioned source of truth for Sila's Conversational CLI (CCLI) grammar productions, safety policies, and canonical argv lowering (catalog version `1.0.0`, schema `sila/ccli-catalog/v1`):

- `ccli.LoadCatalog() (*Inventory, *Catalog, error)` and `ccli.LoadCatalogFromFile(path) (*Inventory, *Catalog, []CatalogDiagnostic, error)` parse `catalog.json` with `DisallowUnknownFields` and run `ccli.ValidateCatalog`.
- `ccli.NewSilaInventory()` returns a validated copy of the embedded catalog's 111 productions across 76 phrase families (`[]*Production`), and `ccli.SilaFlagCatalog()` derives the CCLI flag map directly from `internal/cli.Flags()` so flag metadata is never duplicated in `catalog.json`.
- `PlanRowSpec` entries (`plan_rows`) track the 76-row CCLI plan across 6 domains (`row_status`: `implemented|canonical_only|mixed|forbidden|blocked`) separately from executable `productions`.
- `ccli.ValidateCatalog` enforces schema version `sila/ccli-catalog/v1`, unique production IDs, unique action IDs (unless `variant_of` is set), pairwise phrase-skeleton and grammar collision freedom, forbidden secret/credential slot rejection, canonical command/flag resolution against `internal/cli`, and safety policy completeness (`target_policy`, `dry_run_policy`, `confirmation_policy`, `precedence`, and `preserve_exit`).

### Two ways to run Sila

Sila provides two command syntaxes that share the exact same JSON/TOON/porcelain output contracts, exit-code semantics, and stdout/stderr stream discipline:

1. **Canonical syntax (`sila <command> [subcommand] [flags]`)**: Stable for scripts, hooks, CI pipelines, and automation. Canonical commands always take precedence (`canonical-first`): if the first positional token matches any registered command or alias in `internal/cli`, auto-rewrite is bypassed.
2. **Conversational phrase syntax (`sila <phrase...>` or `sila do "<phrase>"`)**: Deterministic, zero-LLM phrase routing across 76 phrase families. Non-overlapping phrases auto-rewrite directly; phrases whose first word overlaps a canonical command (`phrase-only-via-do`) run via `sila do "<phrase>"`.

### Choosing a syntax

| Syntax | Stability | Precedence | Recommended use |
| :--- | :--- | :--- | :--- |
| `sila memo "<directive>"` | Stable | Always wins (`canonical-first`) | Scripts, automation, CI, docs |
| `sila save memo "<directive>"` | Convenience | Auto-rewrites when first token is non-canonical | Interactive use without canonical overlap |
| `sila do "sync rules"` | Explicit convenience | Explicit CCLI routing (`RewriteDo`) | Overlapping canonical commands (`phrase-only-via-do`) |
| `sila ccli explain <phrase...>` | Introspection | Side-effect-free inspection (`RewriteExplain`) | Inspecting token classes, tier, safety, and canonical argv |
| `sila ccli list [--domain=...] [--tier=...]` | Introspection | Read-only catalog query | Discovering phrase families and machine filtering |
| `sila help phrases` | Discoverability | Read-only help page | Viewing all 76 phrase families grouped by 6 domains |

### Precedence, safety tiers, and opt-out controls

- **Canonical-first precedence**: If the first positional word is a canonical command or alias (such as `prime`, `search`, `sync`, `release`, `verify`, `clean`, `heal`, `install`), Sila dispatches the canonical command unless invoked via `sila do "<phrase>"`.
- **No arbitrary abbreviations**: Phrase matching requires exact catalog keywords; arbitrary prefix abbreviations (`si`, `rem`) are never guessed.
- **Safety tiers & central dry-run**:
  - `query` (`TIER 1`): Read-only; executes immediately.
  - `mutation` (`TIER 2`): State-changing; enforces explicit target/operand policies (`required`, `directional`, `permitted-default`) and central `--dry-run` / `--preview` / `--what-if` preview (`[dry-run] would run: sila ...` on stderr) before invoking the canonical command.
  - `destructive` (`TIER 3`): Requires interactive `[y/N]` confirmation on a TTY or explicit `--yes`/`--force`/`--confirm` in non-interactive mode (otherwise refuses with exit `4`, `CCLI_CONFIRMATION_REQUIRED`).
- **Secret refusal & redaction**: Positional secrets (`sk-...`, `ghp_...`, `AKIA...`, JWTs, PEM blocks, `password=...`, `token=...`) and sensitive config keys are refused at the CCLI boundary with exit `3` (`CCLI_SECRET_DETECTED`) and replaced with `<redacted:kind>`.
- **Opt-out & Denylist**:
  - `--no-ccli`: Disables auto-rewrite for one invocation (`sila do --no-ccli` fails with exit `2`).
  - `SILA_CCLI=off` (`0`, `false`, `no`, `disable`, `disabled`): Disables auto-rewrite globally while keeping explicit `sila do` available.
  - `SILA_CCLI_DENYLIST="save,drop"`: Comma-separated leading verb/prefix denylist for auto-rewrite.

### Domain examples

```bash
# Memory & Epistemic Core
sila say hello
sila remember error "database is locked"
sila save memo "use WAL and busy_timeout"
sila do "prime context"

# Goals & Coordination
sila show ready goals
sila claim goal ccli-dual-syntax-help
sila do "release goal ccli-dual-syntax-help"

# Quality Gates & Health
sila run pre-commit gate
sila check doctor
sila do "verify files"

# Store & Ingestion
sila fix repo
sila start live watch
sila do "sync rules"

# Setup & Onboarding
sila create config
sila check onboarding

# Surfaces, Cloud & Configuration
sila check cloud status
sila list personas
```
