# CCLI Golden Snapshots

This directory contains deterministic golden snapshots for Sila's Conversational CLI (CCLI)
surfaces:

- `explain/`: human explain traces for routed and canonical-blocked phrases.
- `do/`: explicit `sila do` execution and dry-run previews.
- `safety/`: Tier 3 non-TTY refusal, `--yes` bypass, and Tier 2 `--dry-run`.
- `errors/`: unknown conversational phrase suggestions and secret refusal.
- `json/`: machine-readable JSON explain envelope.

To regenerate after an intentional output change:

```bash
cd cli
go test -run TestCCLIGolden -update ./cmd/sila
```
