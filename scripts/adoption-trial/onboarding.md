# Sila Adoption Trial — Participant Onboarding Script

Complete the following steps in any Git repository on your machine. Each step includes the
exact command and expected observable output.

## 1. Install and Verify Wiring

```bash
sila install
sila salam
```

- **Expected output**: `sila salam` prints the 7-step working loop (`prime`, `goals`,
  `recall`, `memo`, `lesson`, `gate`, `handoff`) and exits `0`.

## 2. Prime Session Context

```bash
sila prime
```

- **Expected output**: Session-start context briefing showing active project identity,
  recent decisions/handoffs, and ready goals (`exit 0`).

## 3. Claim or Inspect One Goal

```bash
sila goals
```

- **Expected output**: Roadmap table or ready-goal summary (`exit 0`).

## 4. Run One Solution Recall Before Searching the Web

```bash
sila recall "SQLITE_BUSY concurrent writer"
```

- **Expected output**: Matching prior lessons/decisions from the local knowledge store (or
  `0 matches` on a fresh store) in `<100ms` (`exit 0`).

## 5. Record One Architectural Memo

```bash
sila memo "Use WAL mode with a 5000ms busy_timeout on SQLite connections" --files=README.md
```

- **Expected output**: Confirmation with a cryptographic `fact_<hash>` ID (`exit 0`).

## 6. Record One Empirical Bug-Fix Lesson

```bash
sila lesson "SQLITE_BUSY under parallel test runs" \
  --cause="Default busy_timeout is 0ms" \
  --fix="Configure _pragma=busy_timeout(5000) on open" \
  --files=README.md
```

- **Expected output**: Confirmation with a cryptographic `fact_<hash>` ID (`exit 0`).

## 7. Record One Session Handoff

```bash
sila handoff "Completed onboarding trial steps" --open="Run adoption report"
```

- **Expected output**: Confirmation with a `handoff_<id>` identifier (`exit 0`).

## 8. Export Anonymized Trial Summary

```bash
sila memory report --json > participant-funnel.json
sila eval adoption --offline --json > participant-eval.json
```

Inspect both JSON files before sharing to confirm they contain only tool names, counts,
rates, and case IDs.
