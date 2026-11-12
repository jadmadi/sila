# sila (صِلَة)

> **Local-first continuity, memory, and governance for AI coding agents.**
> One thread across every agent.

[![CI](https://github.com/jadmadi/sila/actions/workflows/ci.yml/badge.svg)](https://github.com/jadmadi/sila/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.27-00ADD8?logo=go)](go.mod)
[![MCP](https://img.shields.io/badge/MCP-Native%20Server-8A2BE2)](docs/mcp.md)
[![Release](https://img.shields.io/badge/Release-v0.18.2-orange)](https://github.com/jadmadi/sila/releases)

Sila is a single static Go binary that reads the session stores of the AI coding
tools you already use, normalizes them into one local SQLite store, grounds
claims against the files they cite, and serves the result back through a CLI and
a native Model Context Protocol (MCP) Server (90 Tools). No cloud account, no telemetry, no CGO.

## Why Sila

AI coding agents are amnesiac by default. Switching from Claude Code to Codex to
OpenCode on the same repository means rediscovering the same decisions, bugs,
and failed approaches. Sila gives every harness one local, inspectable memory:

- **Cross-session continuity** — handoffs, active goals, and prior decisions.
- **Cross-harness ingestion** — read-only loaders for 12 session sources.
- **Grounded recall** — claims cite files; drift is surfaced, not hidden.
- **Native MCP** — 90 tools in the full profile, 36 in the continuity profile. The full profile exposes all 90 tools; see the 90-tool MCP reference.
- **Governance** — goals, leases, gates, audits, and task contracts.
- **Local custody** — SQLite on your disk; network commands are opt-in.

## Context-aware recall

Recall packs carry a bounded task and context into one result:

- `sila recall --task "..." --context-file path` assembles a `recall-pack/v1`.
- The same pack is available over MCP as `prime_context`.

Packs are tiered, budgeted, and marked with an empty reason when no candidates fit.

## Quickstart

```bash
# 1. Install the standalone binary (SHA-256 verified)
curl -fsSL https://raw.githubusercontent.com/jadmadi/sila/main/install.sh | sh

# 2. See what Sila can read on this machine (probe only)
sila sources

# 3. Explore a throwaway sandbox before indexing your own data
sila demo

# 4. Read every supported AI tool's session logs into one local store
sila sync

# 5. Search everything you and your agents have done
sila search "the race condition in the payment worker"

# 6. Brief the next agent session
sila prime
```

`sila sources` and `sila demo` need no store and write nothing. `sila sync`
opens every external source read-only and never modifies another tool's data.

## Install

| Method | Command |
| :--- | :--- |
| Linux & macOS | `curl -fsSL https://raw.githubusercontent.com/jadmadi/sila/main/install.sh \| sh` |
| Windows (PowerShell) | `irm https://raw.githubusercontent.com/jadmadi/sila/main/install.ps1 \| iex` |
| Go toolchain | `go install github.com/jadmadi/sila/cmd/sila@v0.18.2` |
| From source | `git clone https://github.com/jadmadi/sila.git && cd sila && ./build.sh` |
| Release assets | [GitHub Releases](https://github.com/jadmadi/sila/releases) |

The installer verifies the release metadata and SHA-256 checksum before
replacing the binary. `SILA_NO_VERIFY=1` exists for local debugging only.

## MCP

Sila exposes a native JSON-RPC 2.0 server over stdio:

```bash
claude mcp add sila -- sila mcp
# or, for the full 90-tool surface:
sila mcp --profile=full
```

The default `continuity` profile keeps the agent-facing surface small:
orientation, search, recall, memory writes, handoffs, goals, gates, skills, and
activity. The `full` profile adds administrative, external-network, and
swarm-spawning tools. Client setup and the complete catalog live in
[docs/mcp.md](docs/mcp.md).

## What Sila does

- **Continuity** — `sila prime`, `sila handoff`, `sila recall`, `sila salam`.
- **Memory** — `sila memo`, `sila lesson`, `sila case`, `sila search claims`.
- **Archaeology** — `sila search`, `sila search expand`, `sila digest`.
- **Governance** — `sila goals`, `sila gate`, `sila audit`, `sila hire`.
- **Rules** — `sila agents sync`, `sila agents check`, `sila install`.
- **Skills** — `sila skill assess|preview|apply|remove`, on-demand MCP loading.
- **Hygiene** — `sila doctor`, `sila backup`, `sila clean`, `sila storage`.
- **Optional edge** — `sila cloud` drains a Cloudflare D1 mailbox when enabled.

The complete command reference is [docs/reference.md](docs/reference.md).

## Two ways to run Sila

Sila supports two first-class syntaxes backed by the same execution contracts,
JSON envelopes, exit codes, and stream discipline (catalog version `1.0.0`):

1. **Canonical syntax** — `sila <command> [subcommand] [flags]`. Stable for
   scripts, CI, hooks, and agent tool calls. Canonical commands always win
   (`canonical-first`).
2. **Conversational phrase syntax** — `sila <phrase...>` or
   `sila do "<phrase>"`. Deterministic grammar-lowered phrases with zero LLM
   calls.

### Choosing a syntax

| Syntax | Recommended use |
| :--- | :--- |
| `sila memo "<directive>"` | Scripts, hooks, CI, documentation |
| `sila save memo "<directive>"` | Interactive convenience |
| `sila do "sync rules"` | Explicit phrase when the first word overlaps a command |
| `sila ccli explain <phrase...>` | Inspect token classes, tier, safety, canonical argv |

Disable auto-rewrite per command with `--no-ccli`, globally with
`SILA_CCLI=off`, or for specific leading verbs with
`SILA_CCLI_DENYLIST="save,drop"`.

## Supported session sources

Sila ingests 12 sources and detects additional tools without indexing them:

| Ingested | Detected only |
| :--- | :--- |
| Claude Code, Codex, OpenCode, MiMoCode, Devin, Command Code, Codewhale, Antigravity, ZCode, Muse, Zed, Git | Cursor, Grok CLI, Droid |

Session formats are reverse-engineered and can change without notice. Run
`sila sources` for the live status on your machine, and see
[docs/harness-capability-matrix.md](docs/harness-capability-matrix.md) for
lifecycle hooks and trust rules.

## Verification and limits

Sila separates implementation from verification. The local suite is the release
gate; hosted model quality, live harness behavior, and production-scale
performance are not claimed unless separately measured.

- [Verification status](docs/verification.md)
- [Public roadmap](docs/roadmap.md)
- [Architecture and settled decisions](docs/architecture.md)
- [Data sovereignty covenant](docs/covenant.md)

## Security and privacy

- Source tool databases are opened read-only (`mode=ro`).
- The store is a `0600` SQLite file you own.
- Core commands make no network request.
- Optional assistance and web commands require explicit configuration/consent.
- Report vulnerabilities privately via [SECURITY.md](SECURITY.md).

Read the full [privacy and security boundaries](docs/covenant.md).

## Documentation

| Document | Purpose |
| :--- | :--- |
| [docs/reference.md](docs/reference.md) | Command and feature reference |
| [docs/mcp.md](docs/mcp.md) | MCP client setup and tool catalog |
| [docs/architecture.md](docs/architecture.md) | Architecture and decisions |
| [docs/verification.md](docs/verification.md) | What is and is not verified |
| [docs/roadmap.md](docs/roadmap.md) | Public roadmap |
| [docs/harness-capability-matrix.md](docs/harness-capability-matrix.md) | Harness support matrix |
| [CONTRIBUTING.md](CONTRIBUTING.md) | Development and review workflow |
| [SUPPORT.md](SUPPORT.md) | Where to ask for help |

## Contributing

Contributions are welcome. Start with [CONTRIBUTING.md](CONTRIBUTING.md), run
`go test -race ./...`, and read [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).
Security reports should follow [SECURITY.md](SECURITY.md), not public issues.

## License

MIT License — Copyright (c) 2026 Jad Madi. See [LICENSE](LICENSE).

Conceived and built by [Jad Madi](https://github.com/jadmadi).

- Blog: [jadmadi.net](https://jadmadi.net)
- X: [@jadmadi](https://x.com/jadmadi)
- Support: [paypal.me/madise](https://paypal.me/madise)
