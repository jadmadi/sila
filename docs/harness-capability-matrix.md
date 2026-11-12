# Harness Capability Matrix

Grounded in the official lifecycle/hook documentation checked on 2026-10-22. This
matrix is the source of truth for the generic harness contract and its adapters,
and is verified against `internal/harness/registry` in CI.
Where a tool has no documented event, the cell says **none**; no event is
invented.

## Capability classes

| Class | Tools | Enforcement path |
|---|---|---|
| Native shell hooks | Claude Code, Cursor, Codex, Devin, Command Code, Codewhale, Antigravity, ZCode, Muse | Core receives normalized hook events and replies in the harness's output schema |
| In-process plugin API | OpenCode, MiMoCode | One shared bridge plugin emits normalized events to `sila hook`; policy stays in core |
| Commit only | Git | `sila gate` pre-commit/pre-push is the universal chokepoint |

## Native shell-hook adapters

| Harness | Config locations | Events | Stop can block? | Subagent events | Compaction events | Trust / project notes |
|---|---|---|---|---|---|---|
| **Claude Code** | `.claude/settings.json`, `.claude/settings.local.json`, `~/.claude/settings.json`, plugin `hooks/hooks.json` | `SessionStart`, `SessionEnd`, `Stop`, `StopFailure`, `PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `PostToolBatch`, `UserPromptSubmit`, `SubagentStart`, `SubagentStop`, `PreCompact`, `PostCompact`, `Notification`, `Interrupt`, more | Yes (`decision: "block"` continues the turn) | Yes | Yes | Project hooks require workspace trust; `SessionStart` matcher includes `compact` |
| **Cursor** | `.cursor/hooks.json`, `~/.cursor/hooks.json`, enterprise/team config | `sessionStart`, `sessionEnd`, `preToolUse`, `postToolUse`, `postToolUseFailure`, `subagentStart`, `subagentStop`, `beforeShellExecution`, `afterShellExecution`, `beforeMCPExecution`, `afterMCPExecution`, `beforeReadFile`, `afterFileEdit`, `beforeSubmitPrompt`, `preCompact`, `stop`, `afterAgentResponse`, `afterAgentThought` | Continue-only (`followup_message`); no hard deny at `stop` | Yes | `preCompact` only | `failClosed` opt-in; project hooks run from project root; permission hooks block on invalid JSON |
| **Codex** | `~/.codex/hooks.json`, `~/.codex/config.toml`, `<repo>/.codex/hooks.json`, `<repo>/.codex/config.toml`, plugin `hooks/hooks.json` | `SessionStart`, `SessionEnd`, `PreToolUse`, `PermissionRequest`, `PostToolUse`, `PreCompact`, `PostCompact`, `UserPromptSubmit`, `SubagentStart`, `SubagentStop`, `Stop`, `Interrupt` | Yes (`decision: "block"` / continue) | Yes | Yes | Non-managed hooks require review/trust; project hooks only in trusted projects; `Stop` has continuation semantics |
| **Devin** | `.devin/hooks.v1.json`, `.devin/config.json`, `.devin/config.local.json`, `.claude/settings.json` (compat), user config | `PreToolUse`, `PostToolUse`, `PermissionRequest`, `UserPromptSubmit`, `Stop`, `PostCompaction`, `SessionStart`, `SessionEnd` | Yes (`decision: "block"` / exit 2) | none | `PostCompaction` only | Reads `.claude` hooks when `read_config_from.claude`; project hooks found up to repo root |
| **Command Code** | `.commandcode/settings.json`, `~/.commandcode/settings.json` | `SessionStart`, `PreToolUse`, `PostToolUse`, `Stop`; mods for broader lifecycle | Yes (`Stop`: retry, max 3) | Mods, no shell event | none | Project > user; hooks skipped in plan mode; `stop_hook_active` loop guard |
| **Codewhale** | `~/.codewhale/config.toml`, `.codewhale/hooks.toml` | `session_start`, `session_end`, `message_submit`, `tool_call_before`, `tool_call_after`, `shell_env`, `turn_end`, `waiting_for_user`, `session_idle`, `session_busy`, `session_error`, `on_error`, `mode_change`, `subagent_spawn`, `subagent_complete` | Yes (`tool_call_before`, `turn_end`) | Yes | none | Project hooks require workspace trust + digest approval; `continue_on_error`, `background`, timeouts |
| **Antigravity (AGY)** | `.agents/hooks.json`, `~/.gemini/config/hooks.json`, CLI settings, plugin `hooks.json` | `SessionStart`, `PreToolUse`, `PostToolUse`, `PreInvocation`, `PostInvocation`, `Stop` | Yes (`PreToolUse` deny, `Stop` continue) | none | none | App/CLI/IDE resolve different app-data dirs; camelCase payload |
| **ZCode** | `~/.zcode/cli/config.json`, plugin `hooks/hooks.json` | `SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PermissionRequest`, `PostToolUse`, `PostToolUseFailure`, `Stop` | Yes (`decision: "block"`, max 3) | none | none | **Project-level hooks are ignored entirely**; distribute via user config or plugin; config snapshotted at session start |
| **Muse** | `<repo>/.muse/hooks.json`, user settings, `muse.managed_hooks_path` | `SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PermissionRequest`, `PostToolUse`, `PostToolUseFailure`, `PostToolBatch`, `PreLLMCall`, `PostLLMCall`, `PreCompact`, `PostCompact`, `SubagentStart`, `SubagentStop`, `Notification`, `Stop`, `StopFailure`, `SessionEnd` | Yes | Yes | Yes | Hooks run outside the sandbox; project hooks require trust; managed hooks bypass project trust |

## Native hook config dialects

`ConfigSpec.Dialect` (`internal/harness/registry/spec.go`) is the source of truth
for how each shell-hook harness serializes hooks; `TestShellHookSpecsDeclareDialect`
keeps the registry synchronized.

| Dialect | Harnesses | On-disk shape |
|---|---|---|
| `claude_nested` | Claude Code, Codex, Devin, Command Code, ZCode, Muse | `{"hooks": {"<Event>": [{"matcher": "...", "hooks": [{"type": "command", "command": "sila hook ..."}]}]}}` |
| `cursor_flat` | Cursor | `{"version": 1, "hooks": {"<event>": [{"command": "sila hook ...", "timeout": 10}]}}` |
| `antigravity_named` | Antigravity | `{"sila": {"enabled": true, "<Event>": [flat handlers], "PreToolUse"/"PostToolUse": [{"matcher": "*", "hooks": [...]}]}}` alongside sibling named groups |
| `codewhale_toml` | Codewhale | `[[hooks]]` blocks between `# sila:begin hooks` / `# sila:end hooks` |

Install merges into the native shape, `sila hook status` inspects only that
shape (a legacy wrong-dialect entry is reported as not installed), and uninstall
removes only Sila-owned handlers while preserving foreign ones (`orca-status`,
`herdr`, `atuin`) and `"version": 1`.

## Plugin-only adapters (`plugin_hook`)

| Harness | Kind | Plugin API | Events/hooks | Config | Constraint |
|---|---|---|---|---|---|
| **OpenCode** | `plugin_hook` | OpenCode 2 server plugin (`export default { id: "sila-bridge", setup }`) | `session.created` via `ctx.event.subscribe`, `experimental.idle`, `compaction`, `execute.before`, `execute.after` | `.opencode/plugins/sila-bridge.js`, `~/.config/opencode/plugins/`, npm `plugins` array | No shell-hook config; idle warns only; `execute.before` throws on deny; V2-only artifact, MiMoCode uses a separate template; a file without `sila:plugin-api=2` reports installed but not configured |
| **MiMoCode** | `plugin_hook` | OpenCode 1 plugin API (`SilaBridge` async function) | `session.created`, `session.idle`, `session.compacted`, `session.error`, `tool.execute.before`, `tool.execute.after`, `experimental.session.compacting` | `mimocode.json` `plugin` array + `.mimocode/plugins/sila-bridge.js` | Separate V1 template, no OpenCode 2 code; official `/plugins` page returned 404; installer requires existing `"plugin": []` array in `mimocode.json` or `SILA_MIMOCODE_PLUGIN_VERIFIED=1` and refuses with `unsupported` otherwise |

## Output encoding families

| Family | Pre-tool deny | Context injection | Stop/continue | Notes |
|---|---|---|---|---|
| Claude / Codex / ZCode | `hookSpecificOutput.permissionDecision: "deny"` + reason | `hookSpecificOutput.additionalContext` | `decision: "block"` + reason | Claude and Codex also accept exit 2; preserve their per-event fields |
| Command Code | `hookSpecificOutput.permissionDecision: "deny"` + reason | `hookSpecificOutput.additionalContext` | `decision: "block"` + reason | `stop_hook_active` must be checked; plan mode skips hooks |
| Cursor | `permission: "deny"` + `agent_message` | `additional_context` | `followup_message` | Stop is continue-only; do not treat it as a hard gate |
| Devin | `decision: "block"` + reason | `hookSpecificOutput.additionalContext` | `decision: "block"` + reason | Exit 2 also blocks |
| Antigravity | `decision: "deny"` + reason | `injectSteps[].systemMessage` | `decision: "continue"` + reason | camelCase payload (`conversationId`, `toolCall`) |
| Codewhale | `decision: "deny"` + reason | `additionalContext` | `turn_end` decision | Conditions and trust digests are adapter-specific |
| Muse | documented lifecycle exists; exact JSON fields must be verified from official docs/source before coding | — | — | Do not invent an output shape |

## Recall gate hook coverage

The opt-in pre-tool recall gate (`recall_gate_policy`) uses the same
`PhaseToolBefore` row as every other pre-tool sentry. It never reads transcript
content or tool results.

| Harness | Pre-tool event | Recall gate decision | Failure behavior |
|---|---|---|---|
| Claude Code | `PreToolUse` | `hookSpecificOutput.permissionDecision: "deny"` + reason | stderr diagnostic, allow |
| Codex | `PreToolUse` | `hookSpecificOutput.permissionDecision: "deny"` + reason | stderr diagnostic, allow |
| Cursor | `preToolUse` | `permission: "deny"` + `agent_message` | stderr diagnostic, allow |
| Antigravity | `PreToolUse` | `decision: "deny"` + reason | stderr diagnostic, allow |
| Command Code | `PreToolUse` | `hookSpecificOutput.permissionDecision: "deny"` + reason | stderr diagnostic, allow |
| Devin | `PreToolUse` | `decision: "block"` + reason | stderr diagnostic, allow |
| Codewhale | `tool_call_before` | `decision: "deny"` + reason | stderr diagnostic, allow |
| ZCode | `PreToolUse` | `permissionDecision: "deny"` + reason | stderr diagnostic, allow |
| Muse | `PreToolUse` | unverified; returns `unsupported` | stderr diagnostic, allow |
| OpenCode / MiMoCode | `tool.execute.before` | plugin bridge throws on deny | plugin warning, allow |
| Unknown harness | none | no native decision | stderr diagnostic, allow |

Denial is limited to one signature per prompt; retries pass. Reads and unknown
tools are never denied.

## Native goal features to integrate, not duplicate

| Harness | Native feature | Contract rule |
|---|---|---|
| Codex | persisted goals (`features.goals`) | link harness goal to Sila slug; never create a parallel goal store |
| ZCode | Goal Mode | link or explicit waiver; Sila remains evidence/claim authority |
| Muse | goal-tracking observer | map observer goal to Sila goal; Sila remains closure authority |
| Command Code | `/goal` standing objective | link or waiver; Stop evidence gate |
| Claude/Cursor/Devin/Codewhale/Antigravity | no documented native goal object | create/link a Sila goal at session start |

## Design implications

1. **Core owns the contract.** Nine of eleven harnesses expose native shell hooks;
   policy must not live in per-tool plugins.
2. **Adapters are data.** Each spec declares config paths, event names, payload
   keys, capabilities, trust rules, and output family. Adding a harness is a spec
   plus fixtures, not a new binary.
3. **Two integration classes.** Shell-hook adapters for the nine native tools; one
   shared in-process bridge for OpenCode and MiMoCode.
4. **Capability honesty.** Cursor has no hard stop-deny; ZCode ignores project
   hooks; Command Code skips hooks in plan mode; Antigravity/Command Code have no
   documented compaction hook. The contract must degrade to `warn` or rely on
   `sila gate`/`certify`/`handoff` rather than pretend to block.
5. **Universal chokepoints.** Even a harness with no hook support is covered by
   `sila gate --pre-commit`, `sila goals certify`, and `sila handoff`.

## Sources

- Claude Code hooks: <https://code.claude.com/docs/en/hooks>
- Cursor hooks: <https://cursor.com/docs/hooks>
- Codex hooks: <https://developers.openai.com/codex/hooks>
- Devin hooks: <https://docs.devin.ai/cli/extensibility/hooks/overview>
- Devin lifecycle: <https://docs.devin.ai/cli/extensibility/hooks/lifecycle-hooks>
- Command Code hooks: <https://commandcode.ai/docs/hooks>
- Codewhale hooks: <https://codewhale.net/en/docs/hooks>
- Antigravity hooks: <https://antigravity.google/docs/hooks>
- ZCode hooks: <https://zcode.z.ai/en/docs/hooks>
- Muse hooks: <https://dev.meta.ai/docs/muse-code/extending>
- OpenCode plugins: <https://opencode.ai/docs/plugins/>
- MiMoCode config: <https://mimo.xiaomi.com/mimocode/config-files>
