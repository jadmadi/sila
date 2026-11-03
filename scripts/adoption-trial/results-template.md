# Sila External Adoption Trial — Anonymized Results Template

## Per-Participant Summary Table

| Participant | Harness      | Sessions | Prime Rate | Recall Rate | Write Compliance | Handoff Rate | Forbidden Tool Rate | Status  |
| ----------- | ------------ | -------- | ---------- | ----------- | ---------------- | ------------ | ------------------- | ------- |
| P1          | command-code | 0        | —          | —           | —                | —            | —                   | pending |
| P2          | claude-code  | 0        | —          | —           | —                | —            | —                   | pending |
| P3          | opencode     | 0        | —          | —           | —                | —            | —                   | pending |
| P4          | codex        | 0        | —          | —           | —                | —            | —                   | pending |
| P5          | antigravity  | 0        | —          | —           | —                | —            | —                   | pending |

## Aggregate Rates (`sila/adoption-eval/v1`)

| Metric                          | Value / Rate | Denominator | Threshold | Verdict |
| ------------------------------- | ------------ | ----------- | --------- | ------- |
| `trigger_recall_rate`           | `0.0000`     | `0`         | `>= 0.80` | UNKNOWN |
| `prime_rate`                    | `0.0000`     | `0`         | `>= 0.80` | UNKNOWN |
| `handoff_rate`                  | `0.0000`     | `0`         | `>= 0.70` | UNKNOWN |
| `write_compliance_rate`         | `0.0000`     | `0`         | `>= 0.90` | UNKNOWN |
| `median_turns_to_first_sila`    | `0.0`        | `0`         | `<= 1.0`  | UNKNOWN |
| `forbidden_tool_rate`           | `0.0000`     | `0`         | `<= 0.10` | UNKNOWN |
| `unknown_rate`                  | `1.0000`     | `5`         | `< 0.20`  | UNKNOWN |

## Qualitative Exit Interview Prompts

1. Which Sila trigger moment fired without manual prompting in your harness?
2. Which Sila tool did the agent skip or call only after generic `grep` / `web_search`?
3. Did any instruction in `AGENTS.md` or `SKILL.md` feel redundant or ambiguous?

## Trial Verdict

Verdict: UNKNOWN

Reason: Per R5.5 and AC-009, the external 5-user, two-week human onboarding trial requires
five completed external participant sessions (`P1`–`P5`). Because an automated repository
build has 0 external human participant sessions (`denominator = 0`), this report records an
explicit `Verdict: UNKNOWN` rather than fabricating participant data.
