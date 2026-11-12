<!-- section: 1 -->
# Graph

## Synopsis

Build, audit, query, and blast-radius the code + memory graph.

## Usage

```sh
sila graph build [--dry-run] [--force] [--if-stale] [--code-only]
sila graph report [--json]
sila graph query <terms> [--top=N]
sila graph slice [<terms>] [--files=a.go,b.go] [--budget=N]
sila graph path <source> <target> [--max-hops=N] [--include-ambiguous] [--code-only]
sila graph affected <target> [--max-depth=N] [--code-only]
sila graph stats
sila graph doctor
```

`--force` permits replacing a memory-rich graph with a `--code-only` build. The
graph size cap defaults to 50 MiB and follows `SILA_GRAPH_MAX_BYTES`.

## Examples

```sh
sila graph build
sila graph build --if-stale
sila graph build --code-only --force
sila graph report
sila graph query "Store.Open"
sila graph slice "Store.Open" --files=internal/store/store.go --budget=100
sila graph path "fact:abc" "Store.Open"
sila graph path "Store.Open" "Store.Close" --code-only
sila graph affected "Store"
sila graph affected "Store" --code-only
sila graph stats
sila graph doctor
```

`slice` returns a bounded neighbourhood for a query and/or file seeds.
`stats` reports node, edge, file, orphan, size, and freshness data without
writing. `doctor` returns a pass or warn verdict with fixes.

## See Also

- `sila man` lists every topic in this section.
- `sila help graph` shows the command help.
