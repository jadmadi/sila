# Sila Architecture & Public Design Decisions

Sila (`صِلَة`) is the production-grade continuity engine, SQLite knowledge store, rule compiler, and Model Context Protocol (MCP) server for autonomous AI coding agents.

This document describes the high-level system architecture, subsystem boundaries, core invariants, and public Architecture Decision Records (ADRs).

---

## 1. High-Level System Architecture

Sila operates as a single standalone, CGO-free binary compiled for high-speed terminal and MCP operations:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           AI CODING AGENT ECOSYSTEM                         │
│   Devin · Claude Code · Antigravity · OpenCode · Codex · MiMoCode · ZCode   │
└──────────────────────────────────────┬──────────────────────────────────────┘
                                       │
                        ┌──────────────┴──────────────┐
                        ▼                             ▼
              [MCP JSON-RPC Transport]       [Session Transcripts]
              63 Grounded Tools              Read-Only Loaders (mode=ro)
                        │                             │
                        ▼                             ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                             SILA CORE ENGINE                                │
│                                                                             │
│   ┌───────────────────────────┐             ┌───────────────────────────┐   │
│   │   internal/store          │             │   internal/dream          │   │
│   │   • SQLite WAL Engine     │             │   • Grounding Verifier    │   │
│   │   • FTS5 Full-Text Search │◄───────────►│   • Lesson Distillation   │   │
│   │   • SHA-256 Fact Ledger   │             │   • Rule Compiler         │   │
│   │   • Invariant Authority   │             │   • Context Budgeting     │   │
│   └─────────────┬─────────────┘             └─────────────┬─────────────┘   │
│                 │                                         │                 │
│                 ▼                                         ▼                 │
│   ┌───────────────────────────┐             ┌───────────────────────────┐   │
│   │   internal/gate           │             │   internal/cockpit        │   │
│   │   • Fast Pre-Commit Sentries│           │   • 9-Box Mission Control │   │
│   │   • Race-Detector Runner  │             │   • Live Fleet Telemetry  │   │
│   │   • Scar-Tissue Linter    │             │   • HITL Steering Desk    │   │
│   └───────────────────────────┘             └───────────────────────────┘   │
└──────────────────────────────────────┬──────────────────────────────────────┘
                                       │
                                       ▼ (Optional Network Sync)
┌─────────────────────────────────────────────────────────────────────────────┐
│                        CLOUDFLARE EDGE MCP BRIDGE                           │
│   Cloudflare Worker · D1 Mailbox · Asynchronous Inbound Task Drain          │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Core Subsystems

1. **`internal/store` (Relational Engine & Knowledge Base):**
   - Single pure-Go SQLite database (`~/.local/share/sila/sila.db`) running in Write-Ahead Logging (`WAL`) mode with `NORMAL` synchronous writes.
   - Synchronized FTS5 virtual tables (`messages_fts`, `facts_fts`, `docs_fts`) for sub-5ms BM25 ranking.
   - Tamper-evident SHA-256 cryptographic hash-chained fact ledger (`fact_hash` and `prev_hash`).
2. **`internal/loaders` (Multi-Harness Ingestion):**
   - Strictly read-only (`mode=ro`) parsers for 10 AI coding tools: Antigravity, Claude Code, Cursor, Devin, OpenCode, Codex, MiMoCode, ZCode, Cline, and Roo Code.
3. **`internal/dream` (Epistemic Synthesis & Distillation):**
   - Heuristic and stdio LLM candidate extraction with file existence grounding (`file_refs_exist`).
   - Problem-solution lesson extraction (`sila lesson distill`) capturing symptoms, root causes, and verified fixes.
   - Hub-and-spoke rule compilation across 6 AI agent configuration standards (`sila compile-rules`).
4. **`internal/gate` (Engineering Discipline Gates):**
   - Fast (<3s) pre-commit hook sentry validating code formatting, race-free tests, and documentation synchronization.
   - Fail-open commit sentry inspecting staged diffs for secret leaks, destructive migrations, and disabled test assertions.
5. **`internal/cockpit` (Multi-Agent Mission Control):**
   - Responsive full-screen alternate buffer ANSI cockpit (`sila watch`) and one-shot snapshot (`sila stats`) monitoring fleet status, approval requests, epistemic velocity, and worktree capacity.
6. **`internal/mcp` (Model Context Protocol Server):**
   - Production JSON-RPC 2.0 server exposing 63 grounded tools for search, context priming, goal roadmaps, human-in-the-loop steering, and system diagnostics.
7. **`worker/` (Cloudflare Edge Mailbox):**
   - Edge Worker and Cloudflare D1 bridge providing an always-on landing zone for tasks initiated from mobile or browser assistants.

---

## 2. Foundational Invariants

- **Pure Go & Zero CGO:** Built strictly with `CGO_ENABLED=0` using `modernc.org/sqlite`. Produces a single, hermetic static binary without external C library or dynamic runtime dependencies.
- **Absolute Local Custody:** All institutional memory remains 100% on the local developer workstation. Zero ambient telemetry, zero analytics tracking, and zero phone-home pings.
- **Epistemic Non-Destruction:** Sila operates as a read-only observer of external tools. Sila never alters or corrupts session databases created by third-party agents.
- **Subshell & Sandbox Resilience:** 4-tier fallback resolution (`MCP Transport` → `System CLI` → `Workspace Shim .bin/sila` → `go run ./cmd/sila`) ensures continuous execution across containerized sandboxes and restricted subshells.

---

## 3. Public Architecture Decision Records (ADRs)

| ADR ID | Title | Status | Summary & Rationale |
| :--- | :--- | :--- | :--- |
| **ADR-01** | Embedded SQLite over Client-Server or Vector Databases | 🟢 **Adopted** | SQLite in WAL mode delivers sub-millisecond local reads, zero administrative overhead, and radical portability. Heavy vector databases and client-server architectures violate Sila's single-binary, local-first mandate. |
| **ADR-02** | Stdio Line-Delimited JSON Protocol for LLM Distillation | 🟢 **Adopted** | Rather than embedding heavy language model SDKs into the core binary, Sila communicates with external generative models via a simple, pluggable line-delimited JSON stdio stream (`docs/distill.md`). |
| **ADR-03** | Read-Only Transcript Loaders | 🟢 **Adopted** | Sila never injects invasive hooks, daemons, or monkey-patches into AI tools. It ingests historical transcripts by opening local tool logs in read-only mode (`mode=ro`). |
| **ADR-04** | Session Lesson Distillation over Multi-Language Static Regex Scans | 🟢 **Adopted** | Evaluated Devin-style 12-category static regex scans across arbitrary codebases. **Formally rejected** because regex scans without language servers invert Sila's grounding bar and turn Sila into a bloated linter. Sila instead extracts verified problem-solution pairs (`sila lesson distill`) directly from multi-agent debugging sessions. |
| **ADR-05** | Tiered Gate Latency & Fail-Open Semantic Sentries | 🟢 **Adopted** | Pre-commit gates must execute under 3 seconds to preserve developer momentum. Heavy race suites run in pre-push; semantic diff sentries are strictly fail-open when offline or unconfigured. |
| **ADR-06** | Asynchronous Edge Mailbox via Cloudflare D1 | 🟢 **Adopted** | Rather than requiring developers to maintain 24/7 port forwarding or public IP servers on their workstations, an asynchronous Cloudflare D1 worker acts as an always-on edge mailbox for mobile task intake. |
| **ADR-07** | Cryptographic SHA-256 Fact Ledger Hash Chaining | 🟢 **Adopted** | Facts and operational learnings are cryptographically linked in a tamper-evident SHA-256 hash chain (`fact_hash` and `prev_hash`), ensuring historical institutional knowledge cannot be altered or forged. |
| **ADR-08** | Pure-Go ANSI Mission Control Cockpit | 🟢 **Adopted** | Implemented a responsive 9-box mission control cockpit (`sila watch` / `sila stats`) using pure Go ANSI formatting and terminal dimension detection, avoiding heavy external TUI dependencies. |

---

## 4. Private Strategic Research & Competitive Intelligence

In accordance with Sila's data sovereignty and repository isolation invariants, deep competitive landscape studies, competitor reverse-engineering audits, and exploratory proposals are maintained separately in the **private `research/` repository**:

- **Competitive Landscape Studies:** 5-paradigm architectural comparison against 22 commercial and open-source agent memory frameworks.
- **Empirical Evaluation Ledgers:** Full benchmark suites, probe executables, and race-detection trace archives.
- **Exploratory Specifications:** Proposals under active research, deferred investigations (such as high-dimensional Joint Epistemic Vectors), and formal rejection records.

---

## 5. Release Evidence & Verification Receipts (2026-10-02)

To maintain absolute transparency and prevent ungrounded claims, Sila enforces an explicit evidence hierarchy across all releases:

### Evidence Hierarchy

1. **Local Suite Evidence (Deterministic Offline Proof):**
   - Verified via `go test -race ./...` and `sila gate --pre-commit` on the local workstation.
   - Proves schema immutability, SQLite transaction isolation, cryptographic hash chaining, and modular file boundaries ($\le 750$ lines).
2. **Fixture Evidence (Synthetically Labeled Proof):**
   - Isolated synthetic unit test vectors and mock runners (e.g., golden AST files, mock LLM runners).
   - Used exclusively to verify negative edge cases and parsing robustness; never counted toward live model accuracy or production quality credit.
3. **Live / Provider Evidence (Online Network Proof):**
   - Verified against live external endpoints (e.g., Cloudflare Workers D1 API, external gopls language servers).
   - Evaluated strictly when explicit credentials and network connectivity exist; cleanly isolated and fail-closed when offline.

### Verification Status Semantics

- **`disabled`**: The subsystem or adapter is dormant (e.g. missing `CLOUDFLARE_API_TOKEN` or deactivated semantic assistant). Sila fails closed or gracefully skips execution without issuing false passes.
- **`verified`**: The behavior is proven by reachable Git commits, exact SHA-256 build manifests, and passing test suites under `-race`.
- **`acknowledged`**: Diagnostic findings, warnings, or operator notes recorded in durable ledgers (`fact_incidents`, `goals/README.md`) without claiming unverified behavioral resolution.

