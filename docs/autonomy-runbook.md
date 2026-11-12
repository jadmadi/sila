# Autonomous Run Runbook

Long autonomous runs must not take the machine down. This page is the protocol
for running many goals back to back on a workstation.

## The hazard

- `/tmp` is a 16 GB tmpfs on this host, so every byte written there is RAM and
  swap. A killed `go test` leaves `/tmp/go-build*` directories behind; six stale
  directories held 2.5 GB before this runbook existed.
- `go test -race ./...` builds many large test binaries at once. Without a
  bound, parallel linking, test execution, and the race detector can exhaust
  memory and fill the cache disk.
- An unguarded agent loop can also fill `$GOCACHE` and any directory it is
  pointed at, which is how a 40 GB temp footprint happens.

## The guard

`tools/resguard` is a small Go command that:

1. refuses a tmpfs temp root;
2. checks free disk, available memory, and swap before starting;
3. takes an exclusive lock so two heavy runs never overlap;
4. redirects `GOTMPDIR` and `TMPDIR` to a disk-backed run directory;
5. caps Go package parallelism with `GOFLAGS=-p=N`;
6. watches every 10 seconds and stops the process group with SIGTERM, then
   SIGKILL after 15 seconds, when disk or memory crosses a threshold;
7. removes the run directory on success, keeps the log on failure, and prunes
   old runs.

## Commands

Use a disk with at least 40 GB free for the temporary run root.

```bash
cd cli
TEMP="${TMPDIR:-/tmp}/sila-run"

go run ./tools/resguard status --temp-root=$TEMP
go run ./tools/resguard preflight --temp-root=$TEMP

go run ./tools/resguard run --temp-root=$TEMP -- \
  go test -race -p 2 ./internal/store ./internal/mcp

go run ./tools/resguard clean --temp-root=$TEMP --age=2h
```

Clear the Go cache only when the disk is tight:

```bash
go run ./tools/resguard clean --temp-root=$TEMP --age=2h --cache
```

Defaults: 8 GB minimum free disk, 2 GB minimum available memory, `-p=2`,
30-minute lock wait, 128 MB captured output per stream.

## The autonomous loop

1. Preflight once per wave, not once per task.
2. Claim exactly one goal.
3. Implement task by task, keeping edits small.
4. Test only the packages the goal touches, through the guard.
5. Commit with the goal slug and run the goal's acceptance checks.
6. Write evidence with `Verdict: PASS`, certify, reconcile the registry.
7. Clean temp and cache when free disk drops below 15 GB.
8. Move to the next goal.

Never run a bare `go test ./...` in this loop. The full sweep runs at the end of
a wave, once, inside the guard with `-p 2`.

## Output discipline

- Pipe long output through `tail -n 40` so the harness does not capture
  megabytes into its own temp files.
- Let the guard keep the full log on disk; read only the tail.
- Do not start background servers unless the goal's acceptance test needs one,
  and stop them in the same command.

## Stop conditions

The guard exits 124 when it aborts. Treat that as a hard stop:

- free disk below 8 GB on the temp root;
- available memory below 2 GB;
- swap above 95% used.

Stop the loop, run `clean --cache`, and report the receipt before continuing.

## Evidence

Every goal's evidence file should record:

- the guard command used;
- the package list tested;
- exit codes;
- the guard status line before and after the wave.
