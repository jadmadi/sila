## Summary

Describe the user-visible effect and why it belongs in Sila.

## Changes

-

## Verification

- [ ] `go test -race ./...`
- [ ] `go vet ./...`
- [ ] `./build.sh --check --no-install`
- [ ] `gofmt -l cmd internal scripts tools test` is empty
- [ ] Documentation updated for any command, flag, MCP tool, or behavior change
- [ ] CHANGELOG entry added under `[Unreleased]`
- [ ] No secrets, tokens, private paths, or raw session transcripts added

## Privacy and boundaries

- [ ] External session stores remain read-only.
- [ ] Core commands still make no network request.
- [ ] No new mandatory cloud dependency or telemetry.

## Related issues

Closes #
