# Performance & distill cleanup batch (perf-batch)

Author: commanding agent (sila). Status: **delegated** to worker agent for implementation.
User approved "do them all." Items below must be implemented, verified, and committed individually.

## Context
Base commits already landed (do NOT rework):
- `2b3401a` — distill fact-id stability / recall fixes.
- `efd2a62` — `status` fast by default (stored snapshot ~4ms); live scan behind `--scan`.

Repo: Go 1.26+, pure-Go sqlite (`modernc.org`), `CGO_ENABLED=0`. ~7.2k LOC. Tree must be clean after each commit.

## Invariants — do not break
- Every fact id MUST remain `FactID(project_id, claim)` (recomputed at query time).
- `--llm` / `distill_status` / `IsFact` / verified semantics unchanged.
- All `--json` output stays pure; warnings go to `os.Stderr`; never pollute stdout.
- Read-only access to tool DBs: `file:<path>?mode=ro`, dummy-only if opened, `PRAGMA mmap_size=268435456` after open (pattern already in `internal/ingest`/`internal/loaders` as `openReadOnly` — reuse it).
- Concurrency must arm `busy_timeout` before WAL (already handled in `store.Open`; parallel workers use each worker's own read connection, do not share one `*sql.DB` unsafely — mirror `internal/ingest/ingest.go:116-140` bounded pool + connection-per-goroutine).
- Do not change merge thresholds or ConsolidateFacts' verified/`file_refs_exist` bookkeeping.

## Item B+E — single-pass assess with cheap COUNT
- `internal/assess/assess.go` `assessTool` (~L95–120) currently calls `loader.Load()` **twice** per tool: once with `wm=""` (for `Total`) and once with the real watermark (for `New`).
- Fix: avoid the second full curated scan. Give loaders a lightweight count/recent capability — either a new `CountSource`/`CountNew` on the loader interface (SELECT COUNT(*) push-down, no content deserialization) or a `Recent(wm limit)` that scans once. `Total` = the COUNT (prefer a true SQL `COUNT(*)` where the source is SQLite; for JSONL sources walk once). `New` = watermark-filtered count.
- `assess.Run` is sequential (worker-safe single `*store.Store`). Parallelize with a bounded 16-worker pool returning per-tool reports (mirror `ingest.Run`'s pool shape). Each worker opens its own read-only source connection; the shared `store` is only read here (no writes during assess), so a single read conn POST-PARALLEL is fine too — but keep tool-source opening on the worker goroutines.
- Result object shape `assess.Report{Tool,int64 Total,int64 New,NotScoped}` is public + used by `status --scan` and `assess` command — keep it.

## Item C — Devin watermark push-down (higher value)
- `internal/loaders/devin.go` `loadDevinNodes` currently: `WHERE s.hidden=0` then discards chat_message 4k–7k-token content in Go (parser is the cost; Devin `sessions.db` ~1.34 GB / 138k rows).
- Add a coarse SQL bound BEFORE the precise in-Go per-session filter: `AND mn.row_id > <bound>` where `bound` = min(row_id) over already-seen sessions derived from the per-session watermark map passed in (when present). This skips fetch+parse of already-caught-up rows. When the watermark map is empty / a `wm=""` full-read (Total) is requested, keep today's no-bound behavior.
- Do NOT regress: the precise per-session filter must still run; the summary `rendered_max_node_id:<session>` watermark must still advance; the `hidden=0` predicate stays.

## Item D — parallelize assess.Run
Covered by Item B+E pool; if implemented there, mark D done by the same commit. If not, minimum: bounded 16-worker pool over tools, sequential report aggregation.

## Item 2 — distill ConsolidateFacts O(n²)
- `internal/dream/` ConsolidateFacts: current pairwise `Similarity()` re-tokenizes both claims on every call.
- Fix: pre-tokenize each fact's claim ONCE into a slice/hash BEFORE the loops (O(n) tokenization), store alongside; `Similarity` (or a local scorer) consumes the precomputed tokens. Keep the existing similarity formula + threshold.
- Bucket facts by `repo_files` intersection first: the file-overlap branch dominates; only the no-file cluster remains all-pairs (much smaller). Keep order stability and the `id=FactID(project,claim)` recompute per surviving fact.
- Do not change Distill's thresholds, `TrimClaim`, or the `maxClaimLen` truncation behavior.

## Vestigial const
- In `internal/dream` (or wherever), `maxClaimLen = 2000` is unreachable (both Distill branches `TrimClaim` to ~240 first). Either delete it + its only use, or add a one-line comment explaining why it's dead. Prefer delete-if-truly-unused.

## Verification (run all, per item before commit)
1. `go build ./...`
2. `go vet ./...`
3. `go test -race ./...`
4. `gofmt -l .` → must be empty
5. Runtime sanity: build `/tmp/td`; `time /tmp/td --no-color status` still ~ms; `time /tmp/td --no-color status --scan` and `time /tmp/td assess all` should be **measurably faster** than before for the same output (record before/after in the commit body). A `pure-Go` timing regression on Devin's big source is the key success signal.
6. `git status --porcelain` empty after commit.

## Commits
One conventional commit per item (`perf(assess): ...`, `perf(devin): ...`, `perf(distill): ...`, `chore(dream): drop dead maxClaimLen`). Never amend; new commit each time. Do not touch `EFD2a62`-era semantics. If part of the `.gitignore`: the binary is `/sila` at repo root — build to `/tmp/td`, never create a tracked binary.

## Report back (verbatim to me on finish)
For each item: status (done/skipped+why), commit sha, before/after timing for `status --scan` and `assess all` (in /10s → /s), and any deviation from this spec.
