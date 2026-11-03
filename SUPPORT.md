# Support

Thanks for using Sila. This page explains where to get help and what to include
so a report is actionable.

## Documentation first

- [README](README.md) — install, quickstart, MCP setup.
- [docs/reference.md](docs/reference.md) — commands and flags.
- [docs/mcp.md](docs/mcp.md) — MCP clients and tool catalog.
- [docs/verification.md](docs/verification.md) — what is and is not verified.
- [docs/architecture.md](docs/architecture.md) — architecture and settled decisions.
- [docs/harness-capability-matrix.md](docs/harness-capability-matrix.md) — harness
  support, hooks, and trust rules.

## Questions and usage help

Open a GitHub Discussion or an issue with the `question` label. Include:

- Sila version (`sila --version`).
- OS and architecture.
- The command you ran, the output, and the exit code.
- Whether the store is new or existing.
- A sanitized minimal reproduction if possible.

Do not paste raw session transcripts, API keys, tokens, customer data, or
private repository contents. Redact paths and secrets.

## Bug reports

Use the bug report template. A useful report includes:

- Expected behavior and actual behavior.
- Exact reproduction steps.
- `sila doctor --json` and `sila sources --json` output, sanitized.
- Relevant logs with secrets and personal paths removed.
- Whether the issue reproduces in `sila demo` or a fresh store.

## Security reports

Do not open a public issue for a vulnerability or a data-exposure bug. Follow
[SECURITY.md](SECURITY.md) and report privately.

## Feature requests

Use the feature request template. Explain the user problem, the current
workaround, and why the change belongs in Sila rather than a downstream harness.

## Commercial or integration questions

Use the contact options at <https://jadmadi.net>. Do not include secrets or
private customer data in the first message.
