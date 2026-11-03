# Sila Adoption Trigger-Adherence Eval (`evals/adoption/`)

This directory contains the deterministic catalog, JSON Schema, surface map, and offline fixture recipes for `sila eval adoption`.

## Purpose

Existing evaluation layers measure different questions:
- `sila benchmark` scores grounded claims retrospectively from ingested transcripts.
- `evals/feature-promises` verifies that shipped CLI and MCP surfaces behave as promised.
- `evals/adoption` tests the trigger-adherence question: when a user prompt creates a Sila trigger moment, does the instruction surface route the agent to the right Sila tool before generic fallbacks (`grep`, `find`, `web_search`, `git_log`)?

## Files

- `schema.json`: JSON Schema (draft 2020-12) for `sila/adoption-catalog/v1` and `sila/adoption-case/v1`. Rejects unknown fields, unknown assertion types, empty prompts, duplicate case IDs, and overlap between `allowed_tools` and `forbidden_tools`.
- `cases.json`: 12 canonical trigger-adherence cases covering session start, error recall, prior research omnisearch, decision memo, bug-fix lesson, architectural case, session-end handoff, specialist skill routing, symbol blast radius, temporal activity digest, pre-commit gate, and goal status inspection.
- `surface-map.json`: Maps each case to its required CLI command, MCP tool, `SKILL.md` line, compiled `AGENTS.md` rulebook line, `sila salam`/`prime` briefing line, and MCP tool description trigger.
- `fixtures/`: Deterministic local fixture recipes referenced by `setup.fixture`.

## Running

```bash
# Offline deterministic surface & scripted-agent contract test (default, no network/model)
sila eval adoption --offline --cases=evals/adoption/cases.json --json

# Dry-run plan for live opt-in harness evaluation
sila eval adoption --live --harness=command-code --cases=evals/adoption/cases.json --dry-run
```
