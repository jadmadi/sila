# Changelog

All notable changes to Sila are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed
- The public release preflight uses `./build.sh --check --public`; the private-only goal-receipt tests are excluded and documented in `docs/release-preflight.md`.

### Added
- Context-aware recall packs: `sila recall --task` and `--context-file` assemble a bounded `recall-pack/v1`; `prime_context` carries the same pack over MCP.

## [1.0.2] - 2026-11-12

### Fixed
- `sila changelog` now shows released sections instead of Unreleased. The private changelog was cut for v1.0.0 and v1.0.1, the `# Changelog` header was restored, and the embedded changelog copy was synchronized. No runtime changes.

## [1.0.1] - 2026-11-12

### Changed

- Release preflight tests now use hermetic fixtures and generic project names,
  so the public release pipeline does not depend on private host data.
- The public `v1.0.0` changelog section is included on main. No runtime changes.

## [1.0.0] - 2026-11-12

### Added

- Thirteen graph audit features: default MCP graph exposure, read purity,
  freshness metadata, output budgets, structured path errors, multi-graph
  caching, code-only paths, bounded builds, fragmented-community merging,
  graph stats and doctor, contract docs, bounded agent context, and a
  content-addressed build cache.
- Graph CLI and MCP surfaces: `sila graph stats`, `sila graph doctor`,
  `sila graph slice`, `query_graph` file seeds, and a bounded
  `prime_context` graph section.

### Fixed

- Sandboxed fallback stores are scoped to their owning repository and never
  start daemons or global soak checks.
- Non-repository directories no longer receive `.sila/state`.
- The workspace drain is a schema-complete `ATTACH DATABASE` merge; drained
  stores are removed under a non-blocking flock, and `sila doctor --repair`
  sweeps stray stores, sidecars, dead locks, and stale canaries.
- Assertion IDs use a 128-bit hash, preventing rare collisions that aborted a
  global sync.
- The daemon doctor builds on Windows.
- Security: `github.com/klauspost/compress` v1.20.2 for GO-2026-6865 and Go
  1.27.4 for the net/http fix GO-2026-6859. `govulncheck ./...` reports no
  vulnerabilities.

## [0.18.2] - 2026-11-03

### Fixed

- Honor explicit `ForcePrompt` in the `sila salam` assistance handshake even
  when `CI` is set, so non-interactive test harnesses can exercise the prompt.
- Fetch annotated tags in CI and release workflows so `build.sh
  --release-artifact` can resolve the release identity.
- Build release assets with Go 1.27.2 to clear standard-library vulnerability
  findings.

## [0.18.1] - 2026-11-03

### Fixed

- Use an inert synthetic Slack fixture in the MCP redaction test so the
  release preflight passes without resembling a live token. No runtime change.

## [0.18.0] - 2026-11-03

### Added

- First public source snapshot of Sila: one Pure-Go binary, local SQLite
  (WAL + FTS5), and a native Model Context Protocol server.
- Cross-harness continuity across Claude Code, Codex, OpenCode, MiMoCode,
  Devin, Command Code, Codewhale, Antigravity, ZCode, Muse, Zed, and Git.
- Continuity profile for MCP (`--profile=continuity`, 36 tools) alongside the
  full administrative surface (`--profile=full`, 76 tools).
- Grounded memory primitives: memos, lessons, cases, handoffs, claims, and
  source-aware search expansion.
- Goal lifecycle, claim leases, task contracts, acceptance packets, quality
  gates, and goal audits.
- Project skill governor with deterministic catalog reduction, atomic apply,
  and on-demand MCP skill retrieval.
- Universal multi-toolchain clean engine for Go, Rust/Cargo, pnpm, aube, npm,
  uv/pip, Bun, Zig, and mise caches.
- Optional Cloudflare D1 edge mailbox for asynchronous task intake.
- Public CI, security scanning, issue/PR templates, and a clean-snapshot
  release workflow.

### Changed

- Public documentation now separates implemented behavior from verified
  behavior. The README no longer promotes blanket “100% verified” claims.
- Installer prefers GitHub Releases; the vanity/R2 endpoint remains an optional
  mirror.
- Public repository excludes the private goals/evidence archive and commercial
  strategy material. A curated roadmap replaces the internal registry.

### Fixed

- Same-basename repository isolation: unregistered repositories can no longer
  inherit another project's memory through basename fallback.
- Covenant audit now fails closed when the store path is missing, empty, or
  unreadable instead of certifying a nonexistent database.
- Installer checksum verification fails closed when metadata or checksums are
  missing.

### Security

- Source tool databases remain read-only (`mode=ro`); core commands make no
  network request.
- Local secret scrubbing covers tokens, private keys, home paths, and
  credential-bearing arguments before optional egress.
- Release artifacts are checksummed and can be attested by the public release
  workflow.

## [0.17.5] - 2026-10-29


### Changed

- Final private-history release before the public snapshot. Added global
`sila salam` fleet orientation, heal/onboard fixes, hook dialect parity, and
session-resume context integrity.

## [0.17.0] - 2026-10-24


### Changed

- Introduced the dual-syntax Conversational CLI, the lean MCP continuity profile,
typed goal task contracts, recursive harness evaluation, and the interactive
documentation suite.

## [0.16.0] - 2026-10-20


### Changed

- Added the project skill governor, first-contact bootstrap, and harness hook
lifecycle improvements.

## [0.15.0] - 2026-10-19


### Changed

- Expanded the MCP surface, goal reconciliation, and cross-project activity
digests.

## [0.14.0] - 2026-10-13


### Changed

- Introduced the local web cockpit, workspace identity resolution, and multi-agent
standup views.

## [0.13.0] - 2026-10-13


### Changed

- Added universal clean, storage reporting, and doctor/repair improvements.

## [0.12.0] - 2026-10-06


### Changed

- Added recursive contrastive evaluation, harness2 ingestion, and semantic
assistance boundaries.

Earlier release history is intentionally not republished here. The public
release history starts at `v0.18.0`; the private development archive retains the
full pre-public ledger.
