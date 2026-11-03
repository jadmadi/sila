# Command Code Integration

> The `Stop` hook now feeds the harness-neutral goal contract (`internal/harness/contract`)
> through the declarative adapter registry (`internal/harness/registry`). Command Code is
> a native shell-hook adapter (not an in-process JS plugin bridge); bind the session with
> `sila goals contract link --session=<id> --goal=<slug>`, and see
> [`docs/harness-capability-matrix.md`](harness-capability-matrix.md) for its
> four shell events (`SessionStart`, `PreToolUse`, `PostToolUse`, `Stop`), mods,
> and plan-mode limitation.

## Stop Memory Gate

Command Code exposes a `Stop` hook. Add this snippet to `.commandcode/settings.json`
(or let `sila hook install --harness=command-code` merge it):

```json
{
  "hooks": {
    "Stop": [
      {
        "hooks": [
          { "type": "command", "command": "sila hook stop --json" }
        ]
      }
    ]
  }
}
```

The adapter maps `SessionStart` to `sila hook session-start` (prime brief),
`PreToolUse` on web searches to one bounded recall advisory, `PostToolUse` to
the failure nudge, and `Stop` to `sila hook stop --auto-sync` (handoff plus
sync). Start failures are visible but fail-open. Every event appends a local
JSONL audit record at `.sila/state/hooks/audit.jsonl` with event, timestamp,
duration, exit code, session, agent, and summary; prompt/query/claim content is
never recorded.

The default policy is `warn`: the gate prints one bounded line and exits 0. Use
`sila hook stop --enforce --json` to return exit 2 when a mutating session has no
grounded memo/lesson/case, or when a session-end has no handoff. There is no
documented Command Code `SessionEnd` event in this build; do not invent one.
Use `Stop` for both turn and session closure, or wire
`sila hook session-end --enforce` manually.

The gate is not a git hook and does not audit old sessions. `SILA_STOP_GATE=off`
is a one-session bypass and appends a visible record to
`.sila/state/stop/bypass.jsonl`.

## Preflight gate

Sila can gate Command Code tool calls on proof that the session bridge ran.
The policy is opt-in and resolves project > user > compiled default (`warn`):

```toml
# .sila/config (project) or ~/.config/sila/config (user)
[command_code]
preflight_policy = "warn"   # off | warn | enforce
```

`.commandcode/settings.json` may carry the same value under
`{"sila":{"preflight_policy":"enforce"}}`.

| Policy | Behavior |
|---|---|
| `off` | Allow every tool call; no warnings. |
| `warn` | Allow every tool call; emit one bounded warning per session when the marker is missing/invalid. |
| `enforce` | Deny every non-allowlisted tool call until a valid marker exists. |

`SessionStart` runs the existing bridge and writes
`.sila/state/preflight/<session>.json` atomically (mode `0600`, parent `0700`).
Schema v2 records the session, project, Sila version, timestamp, policy,
`status` (`ok|degraded|failed|bypassed`), and the truthful prime receipt:
`prime_status` (`ok|failed|skipped|unsupported|bypassed`), `prime_error_class`,
`prime_error` (redacted, <=512 chars), `prime_at`, `prime_duration_ms`,
`prime_source`, `brief_bytes`, and `memory_unavailable`, plus `checks.prime`.
`checks.prime` is true only when `prime_status=ok` and memory was available.
Legacy/missing `prime_status` is treated as `unknown`, never `ok`.

`SessionStart` is fail-open by default: a failed brief writes a failed marker
and audit row but exits 0. Pass `--strict` or set `SILA_HOOK_STRICT=1` to exit 3
after writing the failed receipt. The hook emits a bounded session-start brief;
it does not run the full `sila prime` command.

Inspect or set the policy without editing files by hand:

```bash
sila preflight status [--json]
sila preflight set off|warn|enforce [--user]
```

`set` writes atomically, preserves unrelated config lines, and leaves a `.bak`
backup. The gate validates the marker's session, project, schema, mode,
`prime_status=ok`, `checks.prime=true`, memory availability, and age (30-minute
TTL); a marker from another project or older than the TTL denies in `enforce`.
`SILA_PREFLIGHT_BYPASS=1` remains a one-call escape hatch and now writes a
`preflight` audit entry.

`PreToolUse` allows `mcp__sila__*`, `sila hook session-start`, `sila prime`,
`sila salam`, `sila heal --dry-run`, `sila goals ready`, `sila recall`,
`sila doc search`, and the adapter's `preflight` command. Everything else needs
a valid marker: correct session, status `ok`, `prime_status=ok`,
`checks.prime=true`, `memory_unavailable=false`, mode `0600`, and created after
the session start (and within 24h). Missing, corrupt, stale, legacy, failed, or
cross-session markers deny in `enforce` with the recovery command
`sila hook session-start`; the hot path never spawns `sila`, hits the network,
or executes `tool_input`.

`SILA_PREFLIGHT_BYPASS=1` is a one-call escape hatch in `enforce` mode and emits
a visible warning. Command Code skips hooks in plan mode, so the gate cannot
block there; plan mode is reported as an allow with a warning. Rollback is
`preflight_policy = "off"` or removing the hook. `sila install
--tools=command-code` owns the settings-file write; this gate owns only the
policy and decision engine.

## Subagent charters

Command Code does not document a subagent event or mod. Use the manual flow:

```bash
sila subagent charter --parent-session=<parent> --subagent=<child> \
  --goal=<slug> --paths=<repo-relative paths> --handoff=parent-only
sila memo "<decision>" --charter=.sila/state/charters/<child>.json --files=<file>
```

The child inherits the parent session for attribution and cannot write outside
its allow paths. `parent-only` prevents a duplicate child handoff; the parent
runs `sila handoff` at session end. `sila hook` returns `unsupported` for a
subagent event that lacks `parent_session_id`/`subagent_id`; it never guesses a
parent.

## MCP bridge contract

The Sila server accepts the standard JSON-RPC envelope and forwards it
unchanged:

```json
{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"recall","arguments":{"query":"..."}}}
```

String-encoded scalars and arrays are normalized once, only for fields the tool
schema declares. `sila doctor --mcp` reports the running/stale server counts
plus the expected envelope, continuity tool count, schema digest, and the last
`mcp__sila__` `Invalid params` line found in
`~/.commandcode/logs/command.log`.

The golden fixtures under `internal/mcp/testdata/commandcode/` replay
`initialize`, `tools/list`, `recall`, `prime_context`, `save_memo`,
`save_lesson`, `save_handoff`, and an empty-argument `get_status` call.
`contract.json` pins the schema digest and continuity count so a server change
fails the conformance test until the fixture is updated deliberately.

### CLI fallback

If the proxy cannot reach the server, use the CLI and state the fallback
explicitly rather than failing silently:

| Proxied tool | CLI fallback |
|---|---|
| `recall` | `sila recall "<query>"` |
| `prime_context` | `sila prime` |
| `save_memo` | `sila memo "<directive>"` |
| `save_lesson` | `sila lesson "<symptom>" --cause=... --fix=...` |
| `save_handoff` | `sila handoff "<summary>" --open="..."` |

## Install target

`sila install --tools=command-code` provisions Command Code without touching
foreign configuration:

- `.commandcode/settings.json` — merges Sila's `SessionStart`, `Stop`,
  `PreToolUse`, and `PostToolUse` hooks; other hooks and user keys are
  preserved.
- `~/.commandcode/mcp.json` — merges the `sila` stdio server entry
  (`transport`, `enabled`, `command`, `args`); other servers and extra entry
  keys are preserved.
- `AGENTS.md` — the compiled Sila managed block is deployed through the
  dedicated `agents` target.

`--dry-run` lists every write, `--no-hooks` and `--no-mcp` skip their
respective provisioning, and `--replace` authorizes replacing a non-object
hooks member. Roll back hooks with `sila hook uninstall --harness=command-code`;
the installer never removes a server entry it did not create.
