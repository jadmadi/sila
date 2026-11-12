# Sila Agent Contract

This public hub carries the same compact trigger contract that `sila agents sync`
projects into managed instruction blocks. The private development archive is not
required to use Sila.

## Sila Trigger Contract

Memory writes are required by the goal contract and enforced by `sila gate`.
- `sila prime` (start) → briefing + goals.
- `sila recall "<symptom>"` (stuck) → prior resolutions.
- `sila memo "<directive>" --files=...` (decision) → grounded directive.
- `sila lesson "<symptom>" --cause=... --fix=...` (solved) → grounded lesson.
- `sila case "<intent>" --approach=... --invariant=...` (pattern) → case record.
- `sila facts verify` (grounding) → file/ledger evidence.
- `sila gate` (before commit) → deterministic quality receipt.
- `sila handoff "<summary>" --open=...` (end) → handoff record.
- `sila sync` (after changes) → cross-harness continuity.
- `sila salam`, `sila guide`, `docs/reference.md` → detail.
- Prior research/specs (BEFORE grep/find): `sila search "<q>" --omni` (`omni_search`), `sila search expand <id>` (`get_message_context`), `sila doc search "<q>"` (`search_docs`).
- Activity/domains/review/skills: `sila standup last 24h`, `sila digest --since=24h`, `sila recap`; `sila guide` (`get_operational_guidebook`), `sila learn`, `sila persona`, `sila hire --for-goal=<slug>`, `sila audit` (`audit_goal`), `sila skill` (`skill_search`, `skill_load`).
- Symbols/decisions: `sila edit-check --symbol=<sym>`, `sila safe-delete --symbol=<sym>`, `sila note --symbol=<sym>`, `sila decision` (`create_decision`), `sila vision triage`.
