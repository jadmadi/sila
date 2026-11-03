# Security and Privacy

Sila indexes your AI CLI session history into a local SQLite store. That means it holds a copy of conversations, commands, file paths, and code that were previously scattered across other tools' private data. This page states the boundaries the design intends to hold, and how to report a problem.

## Reporting a vulnerability

Report privately — open a [security advisory](https://github.com/jadmadi/sila/security/advisories/new) or contact the maintainer directly. Please do not open a public issue for anything that could expose another user's data.

Useful reports include: the version (`sila --version`), the platform, what you did, what happened, and what you expected. A minimal reproduction against a sanitized store is ideal. Do not attach a real knowledge store or transcript.

## Supported versions

| Version | Supported |
| :--- | :--- |
| 0.18.x | Yes |
| 0.17.x and earlier | No; upgrade to the latest release before reporting |

Security fixes are published as patch releases on the supported line. The public
repository starts at v0.18.0; pre-public development history is not part of the
public support surface.

## The boundaries that matter

Sila normally runs with your own filesystem authority, so there is no multi-user boundary to defend. The boundaries that do matter are these:

| Boundary | Intended behavior |
|---|---|
| Source data vs source mutation | Every source tool's database is opened read-only (`mode=ro`, 256 MB mmap). Sila never writes to another tool's data. |
| Observed text vs authorized rules | Text distilled from a transcript is a **candidate**, never an approved rule. Only an explicit `sila memo` (or an explicit approval) grants authority, and candidates are excluded from exported rules. |
| One repository vs another | Each briefing resolves one project: Git, runbook, and context measurement come from the selected project's own path. |
| A browser page vs the local API | The cockpit binds loopback by default and validates Host, origin (scheme + host + port), and media type on every request. |
| A downloaded release vs your machine | Release artifacts are verified against a SHA-256 manifest and release metadata from a single resolved release identity; `sila upgrade` verifies before replacing the binary. |
| Imported private data vs export | The store is a plain SQLite file you own. Nothing is sent anywhere unless you invoke an opt-in network command. |

## What "local" does not mean

- **Local storage is not encryption.** The store is a `0600` SQLite file readable by anything running as you.
- **Exposing the cockpit makes reads network-reachable.** The launch token guards mutations only. Anyone who can reach the port can read transcripts, claims, and handoffs. Only `--expose` a store you are willing to serve.
- **A cloud-backed agent is not local.** When an MCP client retrieves context and sends it to a remote model, that text left the device. Sila does not control that, and it is worth knowing before connecting it to a hosted agent.
- **Deleting a row is not secure erasure.** SQLite pages, the WAL, and any backups you made may retain earlier content until they are vacuumed or discarded. Re-importing from the original source can also restore records you deleted.
- **Optional network commands exist.** `sila doc web-search`, `code-search`, `pkg`, and `so` query external services when you invoke them. The core loop (`sync`, `search`, `prime`, `handoff`, `memo`, `doc <url>`) does not.

## Reporting a data-fidelity problem

A "security" issue and a "fidelity" issue are different: if Sila ingests the wrong role, attributes a message to the wrong project, or presents an unapproved observation as a rule, that is a data-integrity bug — please report it as a normal issue with a sanitized fixture. Those defects are the ones that erode trust in the store, and they are treated as first-class bugs.
