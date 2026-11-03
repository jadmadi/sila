# Verification Status

_Last reviewed: 2026-11-03 for the v0.18.0 public snapshot._

Sila separates **implementation** from **verification**. An implemented command
is not automatically a verified promise, and a green fixture is not proof of
live model quality. This page records what the public repository can and cannot
claim.

## Verification tiers

| Tier | What it means | Current status |
| :--- | :--- | :--- |
| Local suite | `go test -race ./...`, `go vet ./...`, `./build.sh --check` | Release gate; run in CI |
| Fixtures | Deterministic synthetic tests for parsers, storage, MCP, hooks, and gates | Broad coverage; not live behavior |
| Live harness | A real Claude Code, Codex, OpenCode, etc. session on a real repository | Best-effort; formats change |
| Provider assistance | Optional semantic rerank, drift, recap, or instruction review | Opt-in; not a release gate |
| Production scale | Sustained multi-user or very large-store behavior | Not claimed |

## What is verified locally

- All packages compile and `go vet` passes.
- The race-enabled test suite covers the CLI, store, MCP server, loaders,
  dream/recall, gates, skills, hooks, and renderers.
- The release script builds every advertised platform asset and verifies the
  host-native binary.
- Installer checksums are verified before a binary replaces an existing one.
- Same-basename project isolation and covenant fail-closed behavior have
  dedicated regression tests in the current tree.

## What is not verified

- **Hosted model quality.** No public claim is made that a particular model
  completes coding tasks better with Sila. Optional provider features are
  experimental.
- **Every live harness version.** Session formats are reverse-engineered and can
  change without notice. Run `sila sources` and `sila doctor` on your machine.
- **Production-scale latency.** Local benchmark numbers are measurements, not
  service-level guarantees.
- **Every historical goal.** The public repository does not publish the private
  goal/evidence archive. Implementation counts are not verification counts.

## Known limitations

- Live harness ingestion is best-effort. A loader can break when an upstream
  tool changes its private session schema.
- Optional semantic assistance depends on an external provider and explicit
  consent. It is disabled by default.
- Execution-contract rollout is opt-in. The private pilot stopped; public
  documentation describes the mechanism, not a proven outcome improvement.
- Skill-governor tests verify catalog access, atomic file writes, rollback, and
  token accounting in fixtures. They do not prove live model task accuracy.
- Archived/legacy memory behavior may differ from current behavior; the public
  snapshot starts at v0.18.0.

## How to verify locally

```bash
# Compile, vet, and run the race suite
go test -race ./...
go vet ./...

# Full release preflight (tests, coverage, build)
./build.sh --check --no-install

# Inspect the live harness/source status on this machine
sila sources
sila doctor

# Confirm the MCP tool surface
sila mcp --print-schema | jq '.tools | length'
```

If a claim on the README, docs, or release page is not supported here, treat
this page as authoritative and open an issue so the claim can be corrected.
