# Public Roadmap

Sila's private development archive tracks detailed acceptance contracts,
evidence receipts, and internal audits. This page is the public view: the
capabilities that shape the product, not the private work ledger.

## Shipped

- **Local continuity** — session ingestion, `sila prime`, handoffs, recall,
  source-aware search, and transcript expansion.
- **Cross-harness bridge** — read-only loaders for 12 session sources plus Git;
  rule projection into harness-native instruction files.
- **Native MCP** — stdio JSON-RPC server with continuity and full profiles.
- **Grounded memory** — memos, lessons, cases, claims, file-reference grounding,
  drift reconciliation, and provenance.
- **Governance** — goal packages, claim leases, task contracts, acceptance
  packets, quality gates, and audits.
- **Project skill governor** — catalog assessment, atomic apply, rollback, and
  on-demand MCP skill loading.
- **Operational hygiene** — doctor/repair, backups, storage reporting, and
  multi-toolchain cache cleanup.
- **Optional edge mailbox** — Cloudflare D1 intake/drain, disabled by default.

## Next

- **Typed memory envelope** — a strict `sila/memory/v1` record with assertions,
  evidence references, confidence, scope, validity, and supersession, bridged
  to the existing local fact store.
- **Context-aware recall pack** — rank recall by the task, files in play, branch,
  and HEAD; return a bounded pack that can be empty.
- **Recall gate** — an opt-in, deny-once pre-tool nudge that makes memory checks
  happen before the first mutation, without blocking reads or trapping retries.
- **Relevance feedback** — retrieval-bound `helped`, `verified`,
  `less_important`, `irrelevant`, and `wrong` signals that adjust ranking without
  changing memory content.
- **Archive and restore** — reversible memory lifecycle with explicit authority,
  a 24-hour undo window, and an append-only audit chain. No hard delete.
- **Import existing agent memory** — deterministic, local, redacted import of
  durable memory files from Claude Code, Codex, and Grok. Instruction files are
  never imported as memory.
- **CI and release hardening** — race suite, coverage, secret scanning, CodeQL,
  dependency updates, checksummed release assets, and install smoke tests.

## Exploring

- More harness adapters where the upstream store exposes stable timestamps and
  assistant output.
- Optional local embeddings or a pluggable reranker, without making a cloud
  provider mandatory.
- Richer agent-to-agent handoff contracts for multi-agent worktrees.
- A public benchmark harness that measures continuity outcomes on real tasks.

## Out of scope

- A hosted memory service or mandatory cloud account.
- Replacing source tools' own memory or instruction systems.
- Telemetry, analytics SDKs, or background phone-home behavior.
- Silent writes to another tool's store.
- Prompt-injection “memory” that treats retrieved text as instructions.

Roadmap items are directional, not release commitments. The README and
[docs/verification.md](verification.md) describe what is shipped and verified.
