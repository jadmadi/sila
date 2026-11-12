# Public Release Preflight

The public Release workflow runs:

```sh
./build.sh --check --public --no-install
```

It uses the same Go Report Card and coverage gates as the private preflight.
The difference is the test set: private-only tests are skipped by name, with a
reason, only when `--public` is set.

## Private-only skips

| Test | Reason |
|---|---|
| `TestAuditReceiptTamperAllFields_OnRecertifiedGoals` | reads private `goals/` audit receipts that the public export removes |
| `TestGoalStateGatingWithTamperedReceipt_OnRecertifiedGoals` | reads the same private `goals/` receipts |
| `TestBuildGlobalBrief` | host-global test; it runs only when `$HOME/.agents` is mounted, which the public CI does not have |

The exporter also excludes `internal/goals/receipt_challenge_test.go`. The
`--public` skip regex lists both names for safety. The private
`./build.sh --check --no-install` runs every test with no skip.

## Public-safe surfaces

- `TestDocumentedMCPToolCountsMatchRegistry` checks the `HANDOFF.md` surface
  only when the private handoff exists. The public README, `docs/mcp.md`, and
  `AGENTS.md` are always checked.
- `TestGateParity` uses the public `docs/reference.md` overlay, which links
  public files only.
- The public README, CHANGELOG, `project.jsonc`, agents hub, and reference
  overlay own the public-facing content. The private documents stay private.

## Local verification

The exporter creates a plain directory, not a git checkout. To run the
preflight locally, initialize git in the export first:

```sh
git init -q
git add -A
git -c user.name=check -c user.email=check@example.com commit -qm public-export
./build.sh --check --public --no-install
```

The workflow runs from a real public checkout, so no initialization is needed
there.
