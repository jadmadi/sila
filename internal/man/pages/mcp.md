<!-- section: 7 -->
# MCP server

## Synopsis

Expose continuity tools over JSON-RPC.

## Usage

```sh
sila mcp [--profile=continuity|graph|full]
```

Profiles: `continuity` (10 tools), `graph` (13 tools), and `full` (all native
tools plus any installed plugin tools).

## Examples

```sh
sila mcp
sila mcp --profile=graph
```

## See Also

- `sila man` lists every topic in this section.
