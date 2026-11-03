# Sila External Adoption Trial — Informed Consent & Privacy Notice

## 1. What Is Collected

If you opt in to share your trial summary, only the following aggregate and metadata fields
are included:

- **Tool names**: Sila CLI subcommands and MCP tool identifiers (`prime_context`, `recall`,
  `save_memo`, `save_lesson`, `save_case`, `save_handoff`, `run_gate_pipeline`, etc.).
- **Case IDs**: Fixed catalog slugs from `evals/adoption/cases.json` (e.g.
  `recall-before-web-search`).
- **Execution metadata**: Call counts, boolean pass/fail/unknown statuses, turn numbers,
  and millisecond durations.

## 2. What Is Never Collected

The local ledger (`tool_events`) and evaluation reports (`sila/adoption-eval/v1` and
`sila/adoption-funnel/v1`) never record or export:

- Prompts, conversation transcripts, or model responses
- Source code snippets or diffs
- File paths, repository names, or directory trees
- Environment variables, API keys, tokens, or secrets

## 3. Storage, Retention & Local Deletion

- All events remain on your local machine in `~/.local/share/sila/sila.db`.
- Sila performs zero background network uploads.
- You can inspect every recorded event locally with `sila memory report --json`.
- You can delete all recorded tool events at any time with:

```bash
sila memory prune --older-than=0s --yes
```

## 4. Voluntary Participation & Withdrawal

Participation is voluntary. You may stop at any point and withhold or delete your local
results without penalty.
