# Adoption Trigger-Adherence Evaluation & External Trial

`sila eval adoption` measures whether Sila's compiled surfaces (`AGENTS.md`, `SKILL.md`,
`salam`/`prime` briefings, and MCP tool descriptions) and live agent harnesses invoke the
right Sila tool before falling back to generic `web_search` or `grep`.

## 1. Case Catalog & Schema (`sila/adoption-case/v1`)

Catalog cases live in [`evals/adoption/cases.json`](../evals/adoption/cases.json) and
validate against [`evals/adoption/schema.json`](../evals/adoption/schema.json) (JSON
Schema draft 2020-12 with `DisallowUnknownFields`).

Each case defines:
- `id`: Unique trigger-moment identifier (12 canonical moments).
- `prompt`: Trigger scenario prompt (never persisted to result reports or `tool_events`).
- `expected_first_tool` and `expected_sequence`: Expected Sila MCP tool or CLI command.
- `allowed_tools` and `forbidden_tools`: Mutually exclusive tool sets.
- `max_turns`, `harness`, `profile` (`continuity` or `full`), `required_surface` (`mcp`,
  `cli`, or `skill`), `timeout_ms`, `setup`, and `assertions` (`tool_called`,
  `tool_not_called`, `tool_before`, `sequence_prefix`).

## 2. Offline Mode (Default, Model-Free & Network-Free)

```bash
sila eval adoption --offline --cases=evals/adoption/cases.json --json
```

Offline mode runs in `<5s` without a model or network connection:
1. Verifies the compiled rulebook (`AGENTS.md` / `agents/AGENTS.latest.md`), `.agents/skills/sila/SKILL.md`, `salam`/`prime` briefing, and MCP tool description each contain the case's trigger line from `evals/adoption/surface-map.json`.
2. Verifies `expected_first_tool` is exposed in the case's active MCP profile (`continuity` or `full`).
3. Runs `ScriptedAgent`, a deterministic fixture agent that emits the expected tool only when all surface and exposure checks succeed.

## 3. Live Mode (Opt-In)

```bash
sila eval adoption --live --harness=command-code --cases=evals/adoption/cases.json --dry-run
sila eval adoption --live --harness=command-code --cases=evals/adoption/cases.json --out=.sila/adoption-eval/run1 --json
```

Live mode reads tool events recorded for `case:<id>` (or a live harness driver) and
evaluates assertions up to `max_turns`, short-circuiting immediately if any tool in
`forbidden_tools` is called.

## 4. Metrics, Thresholds & `unknown` Semantics (`sila/adoption-eval/v1`)

| Metric                           | Definition                                                 | Live Pass Threshold |
| -------------------------------- | ---------------------------------------------------------- | ------------------- |
| `trigger_recall_rate`            | Cases where `observed == expected_first_tool` / eligible   | `>= 0.80`           |
| `prime_rate`                     | Sessions calling `prime` / `prime_context` / `salam`       | `>= 0.80`           |
| `handoff_rate`                   | Sessions calling `handoff` / `save_handoff`                | `>= 0.70`           |
| `write_compliance_rate`          | Goal-bound sessions with >= 1 grounded `memo`/`lesson`/`case` | `>= 0.90`        |
| `median_turns_to_first_sila_call`| Median turns before first Sila tool call                   | `<= 1.0`            |
| `forbidden_tool_rate`            | Cases calling a forbidden tool before expected Sila tool   | `<= 0.10`           |
| `unknown_rate`                   | Cases with no live harness / ledger data                   | `< 0.20` (`>=80%` known) |

- Every rate carries an explicit `{ "value": 0.0, "denominator": N }` object rounded to 4
  decimal places.
- Any metric with `denominator == 0`, or any live run with `<80%` known cases, resolves to
  `verdict: "unknown"`, never a false `"pass"`.

## 5. Privacy & External Trial Kit

- Live reports (`report.json` and `case-<id>.json`) and `tool_events` rows store only case
  IDs, tool names, turn counts, outcomes, and durations—never prompts or file paths.
- The 5-user, two-week external onboarding trial kit lives in
  [`scripts/adoption-trial/`](../scripts/adoption-trial/README.md).
