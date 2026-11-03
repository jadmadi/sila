# Contributing to Sila (صِلَة)

Thank you for your interest in contributing to Sila! Sila is the cross-harness continuity engine and local knowledge store for AI coding agents. It unifies transcripts across twelve AI session sources into one durable, disk-grounded store without telemetry, remote daemons, or CGO dependencies.

This guide provides the shortest path from cloning the repository to landing a high-quality contribution. It establishes our local-first engineering discipline, architectural invariants, code conventions, and verification gates.

---

## Table of Contents

1. [Quick Contributor Checklist](#quick-contributor-checklist)
2. [Development Environment Setup](#development-environment-setup)
3. [Architectural Invariants & Principles](#architectural-invariants--principles)
4. [Code Conventions & Toolchain](#code-conventions--toolchain)
5. [Quality Standards & Coverage Thresholds](#quality-standards--coverage-thresholds)
6. [The Local Verification Gate (`sila gate`)](#the-local-verification-gate-sila-gate)
7. [High-Value Contribution Areas](#high-value-contribution-areas)
8. [Commit Conventions](#commit-conventions)
9. [Pull Request & Review Workflow](#pull-request--review-workflow)
10. [Compatibility & Semantic Versioning](#compatibility--semantic-versioning)
11. [Security & Responsible Disclosure](#security--responsible-disclosure)

---

## Quick Contributor Checklist

Before pushing changes or submitting a Pull Request, verify each item:

- [ ] **Toolchain:** Built with **Go 1.27.2+** with `CGO_ENABLED=0`. Zero CGO or external runtime dependencies.
- [ ] **Tests with Race Detector:** `go test -race ./...` passes 100% cleanly with zero race warnings.
- [ ] **Formatting:** Code formatted via `gofmt -s -w .` (zero diffs reported by `gofmt -l cmd internal`).
- [ ] **Changelog:** Updated `CHANGELOG.md` under `## [Unreleased]` documenting the user-facing effect.
- [ ] **Rebuilt Binary:** Executed `./build.sh` so the embedded VCS commit SHA matches `HEAD`.
- [ ] **Command Reference:** Updated `docs/reference.md` if any command, flag, or subcommand changed.
- [ ] **Discipline Gate:** Ran `sila gate` (or `go run ./cmd/sila gate`) and received `✔ all checks pass — discipline clean`.
- [ ] **Commit Message:** Formatted with an effect-first conventional commit subject and an issue reference when applicable (e.g. `feat: description (fixes #123)`).

---

## Development Environment Setup

### Prerequisites

- **Go 1.27.2+** (download from [go.dev](https://go.dev/dl/))
- **Git**
- **SQLite:** Pure-Go SQLite (`modernc.org/sqlite`) is embedded directly in the binary. No local SQLite C-libraries, header files, or `gcc` required.
- **No Node.js, Python, or auxiliary runtimes:** Sila is an entirely self-contained Go application.

### Build and Test

```bash
# Clone the repository
git clone https://github.com/jadmadi/sila.git && cd sila

# Run full test suite with race detector (acceptance gate)
go test -race ./...

# Build and install the standalone binary to ~/.local/bin/sila
./build.sh

# Verify binary freshness and local health
sila doctor

# Inspect local storage footprint
sila storage
```

### Useful Workflows During Development

```bash
# Test a single package during rapid iteration
go test -race ./internal/store/
go test -race ./internal/loaders/

# Build standalone test binary without replacing ~/.local/bin/sila
./build.sh --release --no-install --output /tmp/sila-dev

# Test the local verification gate
sila gate
```

---

## Architectural Invariants & Principles

Every contribution must honor Sila's foundational architectural invariants:

1. **Local Verification First:**
   Run the same checks locally that CI runs: `go test -race ./...`,
   `go vet ./...`, `gofmt`, and `./build.sh --check --no-install`. CI mirrors
   those commands; it is not a substitute for a local preflight.
2. **Rights-Based Data Sovereignty:**
   - External harness logs and IDE transcripts are treated as strictly **read-only**.
   - Zero ambient or unconsented telemetry, analytics beacons, or remote tracking. Network calls must be strictly explicit, opt-in, and consented by the user.
3. **Radical Simplicity (Ban Architecture Astronautics):**
   - Solve the explicit ask directly. Do not build speculative meta-frameworks, background daemons, or auxiliary abstractions.
   - If a feature takes more than two plain sentences to explain why it exists and how to use it, it is an anti-pattern. Reject it.
   - Sila's core identity is a fast, pure-Go local knowledge store and search engine (`sync`, `search`, `prime`, `memo`, `handoff`, `doc`, `gate`, `goals`). Keep it focused and lean.
4. **Offline by Default:**
   All core operations (`sync`, `search`, `prime`, `doc`, `gate`, `goals`, `mcp`) operate entirely offline against local SQLite and filesystem state.
5. **Single Public Core:**
   This repository is the public Go core. The private development archive and
   the optional edge Worker are not part of the public contribution surface.
   Keep the public repository self-contained and buildable with `go build ./...`.

---

## Code Conventions & Toolchain

### Language & Dependencies

- **Pure Go 1.27.2+:** We strictly enforce `CGO_ENABLED=0`. Never introduce dependencies requiring CGO or native C compilation.
- **Approved Permissive Licenses:** Direct and indirect dependencies must carry approved permissive open-source licenses (MIT, BSD-2-Clause, BSD-3-Clause, Apache-2.0, ISC). Copyleft dependencies (GPL, AGPL, LGPL, SSPL, MPL) are strictly prohibited.
- **Zero Script Sprawl:** Do not write Python or Bash automation scripts for repo logic. Write all automation, checkers, and tooling in Go. The only exception is the root `build.sh` compilation wrapper.

### UI & Output Styling (`internal/render`)

- Style every human-facing terminal output through `internal/render` UI abstractions:
  - `ui.Table` for tabular data with headers and proper alignment.
  - `ui.Cyan`, `ui.Green`, `ui.Yellow`, `ui.Red`, `ui.Bold`, `ui.Dim` for consistent palette accents.
  - `FormatBytes` and `FormatNumber` for human-readable numeric values.
  - `emitJSON` or TOON helpers when `--json` or `--toon` flags are passed.
- **Never** hardcode ANSI escape sequences or raw terminal codes.
- **Never** emit colored output to redirected streams or JSON endpoints.
- Hyrum's Law applies to CLI stdout: human output must remain clean and structured, and machine outputs (`--json`) must remain backwards-compatible.

---

## Quality Standards & Coverage Thresholds

Sila adheres to strict quality benchmarks:

- **Formatting and static checks:** `gofmt` and `go vet` must be clean.
- **Coverage:** Coverage thresholds are enforced by `./build.sh --check`; do not
  lower a threshold to make a change pass.
- **Concurrency Safety:** Clean `-race` suite pass across all packages. Zero
  leaked goroutines or data races allowed.
- **Documentation Parity:** Every user-facing command and flag must be
  documented in `docs/reference.md`. Repository URLs and relative markdown
  links must resolve without drift.

---

## The Local Verification Gate (`sila gate`)

The local quality gate is executed via:

```bash
sila gate
```

It executes the following automated discipline checks:

| Check | What It Validates |
| :--- | :--- |
| **tests** | `go test -race ./...` passes across all packages. |
| **format** | `gofmt` reports zero formatting discrepancies under `cmd/` and `internal/`. |
| **changelog** | `CHANGELOG.md` under `## [Unreleased]` records recent source modifications. |
| **binary** | Built binary is fresh and embeds the current Git `HEAD` commit SHA. |
| **readme** | `docs/reference.md` documents every available CLI command. |
| **parity** | Canonical URLs, author links, and markdown relative links resolve cleanly across surfaces. |
| **telemetry** | Source code contains zero ambient network beacons or unauthorized outbound calls. |
| **knowledge** | Local knowledge store has recorded recent engineering decisions and friction. |
| **concurrency** | Goroutine profile is clean with no dangling background workers. |
| **license** | All build dependencies carry approved permissive open-source licenses. |

Installing the Git pre-commit hook ensures these checks run automatically before every commit:

```bash
sila gate install
```

---

## High-Value Contribution Areas

### 1. Source-Adapter Fixtures (Highest Value)

Sila ingests session logs and transcripts from twelve AI session sources (Claude Code, Codex, OpenCode, MiMoCode, Devin, Command Code, Codewhale, Antigravity, ZCode, Muse, Zed, and Git). Cursor, Grok CLI, and Droid are detected but not indexed. As AI tools evolve, their on-disk log schemas, JSON formats, or SQLite schemas change.

- **Synthetic & Sanitized Fixtures:** Always create synthetic, sanitized test fixtures. **Never commit real user transcripts**, which may contain proprietary code, tokens, or personal data.
- **Test Pattern:** Follow existing patterns in `internal/loaders/*_test.go` (e.g. creating temp JSONL files via `t.TempDir()` or SQLite test databases via `mustExec`).
- **Session Provenance:** Ensure every adapter correctly populates `SessionID`, `Role`, `Timestamp`, and `ProjectPath`. Without `SessionID`, `sila search expand` cannot reconstruct multi-turn conversations.

### 2. Retrieval Quality & Benchmarks

Rather than speculative ranking algorithm tweaks, we prioritize **empirically labeled evaluation queries**:
- If search fails to surface a relevant conversation or fact, document the query, expected match, and actual ranking.
- Add test assertions to `internal/store/search_test.go` to measure precision and recall improvements.

### 3. Documentation & Code Clarification

Bug fixes, documentation improvements, error message clarifications, and code simplifications are always welcome. Sila has a strong bias toward simplifying existing code rather than expanding surface area.

---

## Commit Conventions

### Conventional Commits with Effect-First Subjects

Write commit messages following the Conventional Commits specification. The subject line must state the **user-facing effect**, not internal implementation details:

- **Good:** `fix: prevent duplicate facts during concurrent session ingest`
- **Bad:** `fix: add mutex lock in facts.go`
- **Good:** `feat: add export format decoding for Notion and Confluence docs`
- **Bad:** `feat: parse markdown files`

Allowed types:
- `feat:` New features or capabilities.
- `fix:` Bug fixes.
- `refactor:` Code refactoring with zero external behavior change.
- `perf:` Performance improvements.
- `docs:` Documentation updates.
- `test:` Adding or updating tests.
- `chore:` Maintenance, dependency bumps, or tooling updates.

### Issue References

Reference the issue a change closes when one exists:

```bash
git commit -m "fix(store): preserve project scope on same-basename reads (fixes #42)"
```

The internal Sila goal registry is private. Public contributors do not need it;
use conventional commits, tests, and issue references instead.

---

## Pull Request & Review Workflow

1. **Create a Topic Branch:**
   ```bash
   git checkout -b feat/your-feature-name
   ```
2. **Implement & Test:**
   Write clean code and comprehensive tests. Ensure test coverage remains above project thresholds.
3. **Verify Locally:**
   Run `./build.sh` and `sila gate`. Ensure all checks pass with zero warnings.
4. **Push & Open a Pull Request:**
   Provide a concise PR description highlighting:
   - The problem being solved.
   - The proposed solution and design rationale.
   - Evidence of passing local verification (`sila gate`).
5. **Review Expectations:**
   Reviewers evaluate contributions against our core tenets: simplicity, safety, performance, test coverage, and absence of architectural bloat.

---

## Compatibility & Semantic Versioning

- CLI flags, stdout formats, and Model Context Protocol (MCP) tool schemas constitute Sila's public API contract.
- Adding an optional flag or MCP tool is a **minor** release change.
- Deprecating or altering an existing flag or MCP schema requires a deprecation warning and at least one transition release.
- Releases are strictly identified by annotated Git tags (`vMAJOR.MINOR.PATCH`). Go pseudo-versions are not accepted for release identification.

---

## Security & Responsible Disclosure

If you discover a security vulnerability or sensitive data handling issue, please do **NOT** open a public issue. Review our [SECURITY.md](SECURITY.md) and report findings confidentially to the maintainer via the secure contact channels listed there.
