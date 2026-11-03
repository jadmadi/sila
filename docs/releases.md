# Sila Release Procedure

Sila publishes from an annotated Git tag. The release tag is the public
identity consumed by the CLI, MCP clients, installers, and update checks; a
commit hash is provenance only.

## Release policy

| Change | Version | Example |
|---|---|---|
| Correctness, security, or performance fix with no public-contract change | Patch | `v0.5.1` |
| Backward-compatible command, flag, MCP tool, optional field, or capability | Minor | `v0.6.0` |
| Removed/renamed MCP tool or field, changed type, or newly required field | Breaking release after deprecation | `v1.0.0`, then next major |
| Candidate for a stable release | Prerelease | `v0.5.0-rc.1` |

The repository is pre-1.0, but Sila still treats MCP payload changes as public
compatibility events. Deprecate first, keep the old field/tool working for at
least one minor release, document the migration, then remove it in the next
breaking release.

## Before releasing

1. Choose the SemVer tag, for example `v0.5.0`.
2. Move the relevant entries from `## [Unreleased]` into a dated
   `## [0.5.0]` section in `CHANGELOG.md`.
3. Review the MCP baseline at `contracts/mcp-v1.json`. Additive changes are
   expected; removals, type changes, and new required fields need a deliberate
   compatibility decision.
4. Commit the complete release content. The release script refuses a dirty
   worktree and never creates a commit for you.

## Local release automation

Run the release script from the repository root:

```bash
scripts/release.sh v0.5.0 --dry-run
scripts/release.sh v0.5.0
```

The real invocation runs `go test -race ./...`, creates an annotated tag,
builds every supported platform (uncompressed, via `build.sh --all`), then
verifies the release with the **host-native** executable — a foreign-platform
binary cannot be executed, so the check runs `dist/sila-<your-os>-<your-arch>` —
and writes:

- `dist/sila-<os>-<arch>` binaries;
- `dist/checksums.txt` SHA-256 manifest;
- `dist/mcp-contract.json` executable MCP contract snapshot;
- `dist/release-metadata.json` release version, MCP contract hash, and schema support metadata.

**Assets are uncompressed.** A release asset is downloaded once and executed
many times, and `upx --best --lzma` costs ~354 ms of decompression on every
invocation (360.6 ms versus 6.7 ms for `sila --version`) to save ~9.7 MB of
download (4.8 MB versus 14.5 MB). Startup latency is what a user feels on every
run, so the smaller download loses. `./build.sh --upx` still produces a
compressed binary when the artifact size matters more than the first
millisecond. Measurements: [perf-baseline.md](perf-baseline.md).

Nothing is pushed by default. After inspecting `dist/`, publish what you
inspected by re-running the same command with `--push`:

```bash
scripts/release.sh v0.5.0            # prepare: tag, build, verify
# inspect dist/ and the checksums
scripts/release.sh v0.5.0 --push     # publish the inspected artifacts
```

The second invocation detects the existing tag, verifies it is annotated and
points at `HEAD`, and pushes **without retagging or rebuilding** — so the
inspected bytes are the published bytes. Re-running `--push` again is a no-op
once the tag is on the remote.

Use `--publish` only when the GitHub CLI is authenticated; it implies `--push`
and runs `gh release create` with the generated assets.

### Identity rules

`--verify-only` reports how a version string is classified without tagging or
building. A release identity is an exact SemVer tag; the script rejects the
others deliberately:

| Input | Treated as | Why |
|---|---|---|
| `v0.6.0` | stable release | the normal case |
| `v0.6.0-rc.1` | prerelease | a real identity, labeled as not stable in `sila --version` |
| `v0.0.0-20260101120000-abcdef123456` | rejected | a Go pseudo-version from `go install`, not a published release |
| `v0.6.0` (lightweight tag) | rejected | releases require an annotated tag |
| `v0.6.0` (tag not at `HEAD`) | rejected | releases are immutable; identity must describe the released commit |

### Recovery

A failure **before** the tag is pushed removes the local tag so the version can
be prepared again. A failure **after** the tag is pushed never deletes or moves
it, even if the GitHub release step failed: the identity is already public.
Recover by fixing the cause and re-running `scripts/release.sh v0.5.0 --publish`,
which pushes nothing new and creates the missing release.

The complete workflow is rehearsed without network access by
`internal/release/rehearsal_test.go` against a local bare remote: prepare,
publish, idempotent retry, the identity rejections above, and the post-push
failure path.

## Verification and recovery

`sila release check` runs inside the tagged build and verifies the exact
annotated tag, clean tree, matching changelog section, MCP baseline, and schema
metadata. `sila upgrade` and `install.sh` verify release metadata and the SHA-256
manifest before replacing a binary.

Do not move or recreate a published tag. If a release is defective, publish a
new patch release with a clear changelog entry. SQLite migrations are
forward-only; recover data by restoring a backup rather than downgrading a
schema.
