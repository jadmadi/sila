# Performance measurements (2026-09-15)

Corpus: live store, 77,341 messages, 4,493 claims, 95 projects, 322 MB.
Sources: 11 tools, 6.3 GB on disk. Host: linux/amd64 (the maintainer's machine).

## Startup dominates every CLI invocation

| Binary | Size | p50 startup |
| --- | --- | --- |
| `./build.sh --release` (no compression) | 14.5 MB | **6.7 ms** |
| `upx --best` | 6.1 MB | 78.2 ms |
| `upx --best --lzma` (current default) | 4.8 MB | **360.6 ms** |

UPX `--lzma` costs ~354 ms per invocation. Same binary, same directory:
plain 3.9 ms vs lzma 291.8 ms — so it is decompression, not filesystem.

## Query latency (UPX build, live store)

| Operation | n | p50 | p95 | max |
| --- | --- | --- | --- | --- |
| `search <query>` | 14 | 303.6 ms | 363.6 ms | 537.5 ms |
| `search --omni` | 5 | 303.1 ms | 306.8 ms | — |
| `prime` | 10 | 315.6 ms | 327.7 ms | — |

Bare startup is 292.7 ms of that: actual open + query + render is ~11 ms for
search and ~23 ms for prime. With the plain binary, search p50 would be ~15 ms
and prime ~30 ms — an order of magnitude inside the evaluation's proposed target
of p95 < 500 ms at 100k records.

## Ingestion

| Operation | Wall | Peak RSS | Result |
| --- | --- | --- | --- |
| Delta sync (no source changes) | 173.7 s | 714 MB | 931 new records, 4,733 facts distilled |
| Cold sync (empty store, all sources) | 253.9 s | 672 MB | 75,166 records, 4,381 facts |

A near-no-op delta costs ~3 minutes and 714 MB because every source is rescanned
and distillation re-reads each project's assistant messages. That is the
evaluation's F10 concern stated as a number.

## Where the delta sync spends its time (measured 2026-09-16)

Two runs on identical copies of the live store, plain (uncompressed) binary, so
startup is not a factor:

| Phase | Wall | Peak RSS | Work done |
| --- | --- | --- | --- |
| Scan + commit only (`ingest all`) | 26.1 s | 291 MB | read every source, commit 1,013 new records, detect cross-tool overlaps |
| Scan + commit + distillation (`sync --global`) | 119.2 s | 698 MB | the above, then re-derive 4,737 facts across 57 projects |
| **Distillation alone** | **~93 s (78%)** | **+407 MB** | re-reads 46,392 assistant messages (~82 MB of text) |

An earlier run of the same shape measured 173.7 s at 714 MB on colder page
caches, so treat this as "2–3 minutes depending on cache state"; the phase
ratio is what matters.

### Distillation is not incremental

`sila sync` re-derives every fact from **all** assistant history, not from what
changed. For each of the 95 registered projects it calls `MessagesForDistill`
(all assistant messages, 46,392 rows / ~82 MB), runs `dream.Distill` per
message, then `ConsolidateFacts` (all-pairs claim similarity per project),
`UpsertFact`, `PruneFacts`, and `ReconcileFactDrift` (which stats referenced
files on disk). A sync that adds 1,017 records therefore costs about the same as
one that adds 75,000 — the work is proportional to total history, not to the
delta.

### Scanning is a full source re-read

Watermarks filter what a loader *emits*, not what it *scans*. Per-tool scan-only
cost (no new records emitted):

| Tool | Scan wall | Why |
| --- | --- | --- |
| devin | 12.5 s | 3.3 GB `sessions.db` re-read each run |
| codex | 0.9 s | walks the rollout `sessions/` tree on every load |
| agy | 0.5 s | walks brain transcripts + summaries DB |
| others | ~0 s | small files |

The remaining ~12 s of the 26.1 s is commit-path work: cross-tool overlap
detection (FTS-based pairwise comparison across tools) plus dedup and merge.

### Peak memory has two retention points

1. **The ingest orchestrator** runs loaders concurrently with a bounded pool but
   **retains every loader's full message slice until all loaders finish**, then
   commits serially. Concurrency is bounded; retention is not.
2. **Distillation** holds a project's entire assistant-message set, plus
   `ConsolidateFacts`' per-claim token maps (all-pairs comparison).

### What fixing it requires, in order of payoff

1. **Skip re-distillation for projects with no new messages** (~93 s → ~0 s on a
   no-change sync). Needs a per-project distillation watermark and keeping the
   cheap drift check. This is the change the evaluation asked for as
   "demonstrate that a small source append does not require retaining every
   historical message again".
2. **Stream loader results in batches** instead of retaining every slice
   (lowers the 291 MB scan-path peak and the tail of the 698 MB).
3. **Per-file cursors for devin** (12.5 s → near zero when nothing changed);
   its cursor is per-session row ids, so unchanged sessions could be skipped.
4. **Cache the codex rollout session map** instead of walking the tree per load.

## Result: no-change sync cost, before and after (2026-09-16)

| Stage | Wall | Peak RSS | Facts distilled |
| --- | --- | --- | --- |
| Original measurement | 119–174 s | ~700 MB | 4,737 (all projects) |
| After incremental distillation | 13.5 s | 301 MB | 5 (only the project that changed) |
| After incremental distillation + per-session Devin cursors | **1.0 s** | **145 MB** | **0** |

Three changes produced that, in the order they landed:

1. **Incremental distillation.** `sila sync` records a per-project distillation
   watermark (`distill:<project-id>` in `state`) holding the newest message id it
   covered, and skips projects whose newest message is already distilled. A
   no-change sync now distills nothing instead of re-deriving every project's
   facts. `--full` and `--force` still distill everything, and `sila dream`
   always does. Measured alone: 123.5 s → 13.5 s (9×), 726 MB → 301 MB.
2. **Per-session Devin cursors.** `message_nodes` was read through a single
   global floor (`row_id > smallest per-session watermark`). Because the
   smallest of 166 session watermarks was 98, every sync still scanned 281,833
   of 281,931 rows — 12.5 s each time. Both the node and tool-activity passes now
   read per session through `idx_message_nodes_session` (75 µs per session
   instead of a 22.5 ms global scan), and a session's cursor advances past rows
   that content filtering drops, so a session of tool-call chunks no longer
   pins the floor low forever. Measured alone: 12.5 s → 0.033 s incremental.
   A full scan (fresh store) still costs ~110 s and emits the same content:
   verified against the real store — identical distinct-content counts per role
   and an identical 43,107 occurrence rows.
3. Correctness guard found along the way: `PruneFacts` deleted **every** fact
   for a project when distillation produced nothing, including explicit memos
   and vision records. Pruning now excludes `manual_memo`, `nongoal`, and
   `milestone` rows, so reconciling derived claims can never erase authorized
   writes.

### Still open

- **Bounded retention in the ingest orchestrator** (flatten the 291 MB scan-path
  peak): peak already fell from ~700 MB to 145 MB because distillation no longer
  holds whole histories, but loaders still retain their full message slice until
  all of them finish.
- **Codex rollout scan** (~0.9 s): the tree is walked on every load; a persisted
  file→(session, cwd, mtime) manifest would make an unchanged tree free.

## Watcher CPU budget and decoupled redraw (2026-10-08)

Global sync watch (`sila sync watch global`) continuously synchronizes background
AI transcripts and git commits.

### Clarification of historical measurements

Earlier evaluation notes reported CPU utilization figures between 16% and 129%.
Those historical numbers reflected active or diagnostic workloads rather than
warmed unchanged idle watch loops:

- 129% was the lifetime average CPU utilization of PID 2977712 over 1 hour 27
  minutes with an active backlog of uncompacted transcripts.
- 113.7% was an active one-shot sync pass for PID 3308241 over a 10-second run.
- 16.21% was an isolated collector probe (`2026-10-08-watch-cpu-probe.go.txt`)
  executing `cockpit.CollectSnapshot` every second for 30 seconds with Go CPU
  profiling (`pprof.StartCPUProfile`) enabled.

None of those figures represented an uninstrumented, warmed idle loop.

### Decoupled redraw and lightweight progress view

Two architectural changes enforce a strict <=1.0% idle CPU budget:

1. Decoupled collection from UI animation:
   The 1-second UI tick redraws only existing in-memory snapshots (`drawScreen`).
   Telemetry collection runs asynchronously on a slow interval or after sync
   passes, guarded by an atomic boolean so at most one collection runs at once.
   Repaints reuse existing snapshots without querying git or the ledger.

2. Lightweight in-place progress view as default:
   Interactive TTYs default to an in-place progress view (`runWatchInPlaceLoop`).
   Full 9-box cockpit inspection remains accessible via explicit `cockpit` mode.
   The lightweight view updates phase, active tool, records added or merged,
   next poll countdown, and retained source errors directly from in-memory state.

### Reproducible 60-second empirical telemetry (2026-10-08)

Telemetry was captured using a dedicated measurement harness running against
the compiled standalone release binary on an isolated copy of the production
knowledge store (77,341 messages, 95 projects, 1.4 GB SQLite database).

Host environment:
- Git revision: 6c0f47e (release candidate)
- Go compiler: go version go1.27.1 linux/amd64
- Operating system: Linux 6.12.111+deb13-amd64 x86_64
- Hardware: Intel(R) Core(TM) i7-9700 CPU @ 3.00GHz (8 logical cores)
- Host load average: 4.19 (1m), 9.54 (5m), 9.93 (15m)
- Build flags: CGO_ENABLED=0 ./build.sh --release (pure-Go SQLite)

#### Warmed unchanged idle workload (60-second measurement window)

Each mode was warmed up with an initial sync pass, allowed 8.0 seconds to settle
into idle state, and measured undisturbed for exactly 60.0 seconds. Zero source
files, transcripts, or git branches changed during the run.

| Mode                     | Wall (s) | User (s) | Sys (s)  | Total (s) | CPU % (1-core) | Peak RSS   | Child Git | Budget Verdict |
| :----------------------- | :------- | :------- | :------- | :-------- | :------------- | :--------- | :-------- | :------------- |
| Default in-place watch   | 60.001   | 0.010    | 0.000    | 0.010     | 0.017%         | 168.4 MB   | 0         | PASS (<=1.0%)  |
| Cockpit live (decoupled) | 60.000   | 0.010    | 0.010    | 0.020     | 0.033%         | 176.6 MB   | 0         | PASS (<=1.0%)  |
| Machine JSONL (`--json`) | 60.001   | 0.000    | 0.000    | 0.000     | 0.000%         | 165.3 MB   | 0         | PASS (<=1.0%)  |
| Compact mode (`--compact`)| 60.001  | 0.000    | 0.000    | 0.000     | 0.000%         | 171.2 MB   | 0         | PASS (<=1.0%)  |

#### Periodic ingestion scan costs versus idle redraw costs

The watcher separates the continuous 1-second UI animation tick from periodic
background ingestion passes:

- Idle redraw cost: 0.000% to 0.033% single-core CPU. 0 database queries, 0
  git child processes, and 0 filesystem walks occur during idle ticks.
- Periodic delta sync cost: when background sync triggers (for example, on a
  15-second interval), it scans 12 tool sources and verifies git heads. A single
  delta pass with no new records completes in 1.74s to 2.77s wall time, using
  approximately 3.0s to 3.8s total CPU across parallel worker routines.
- If polled at high frequency (every 15s), background ingestion accounts for
  19.5% to 25.9% CPU during active polling, while the decoupled UI redraw remains
  below 0.05% CPU.
- During warmed idle between polls, child git process invocations are strictly 0.

### Correctness and cancellation guarantees

- Zero collection on idle ticks: 1-second UI frames perform zero database,
  git, or loader operations.
- Fast cancellation: Ctrl+C and q signal context cancellation, returning in
  under two seconds even during slow loader operations.
- Error visibility: failed sources persist across polls as partial sync alerts
  until resolved, preventing false in-sync reports.
