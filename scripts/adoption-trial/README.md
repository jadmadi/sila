# Sila External Adoption Trial Kit

This directory contains the consent-based, local-only five-user, two-week onboarding and
trigger-adherence trial kit for Sila (`adoption-trigger-adherence-eval`, R5).

## Purpose & Protocol

1. **Participants**: 5 external developers using supported AI coding harnesses (`claude`,
   `command-code`, `opencode`, `codex`, or `antigravity`).
2. **Duration**: 2 weeks (or at least 1 completed onboarding + real-task session per
   participant).
3. **Local-Only Custody**: Every command runs on the participant's machine against their
   local `~/.local/share/sila/sila.db`. No background telemetry, cloud drain, or central
   upload is enabled.
4. **Redacted Export**: Participants share only the aggregate JSON from
   `sila eval adoption --offline --json` and `sila memory report --json` (or the completed
   `results-template.md`), which contain only case IDs, tool names, durations, and
   rates—never prompts, source code, file paths, or secrets.

## Kit Contents

- [`consent.md`](./consent.md) — Informed consent, exact field inventory, retention, and
  one-command local deletion (`sila memory prune --older-than=0s --yes`).
- [`onboarding.md`](./onboarding.md) — Step-by-step participant script covering install,
  `sila salam`, `sila prime`, one goal, one recall, one memo, one lesson, and one handoff.
- [`results-template.md`](./results-template.md) — Anonymized 5-participant table,
  aggregate rates, qualitative exit interview, and `Verdict: PASS|FAIL|UNKNOWN`.
