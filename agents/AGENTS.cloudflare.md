# AGENTS.cloudflare.md: Cloudflare and Edge Architecture Standard

This document governs all Cloudflare Workers, D1 SQLite, R2 Storage,
and Durable Objects development across Jad Madi's projects.

## 1. Domain Scope, Toolchain and Secrets

- Target runtime: Cloudflare Workers V8 Isolates, D1 SQLite, R2 Object Storage, and Durable Objects.
- Configuration format: always use wrangler.jsonc for Worker configuration, never TOML.
- Observability: do not enable observability by default. It should be
  false by default in wrangler.jsonc unless the owner asks explicitly for it.
- CLI toolchain: manage Wrangler via mise (mise use wrangler@latest). Do not manage Wrangler with pnpm or npm. Wrangler is excluded from the 7-day release-age gate.
- Secrets and variables: store secrets and local environment variables strictly in .env files (which must be gitignored). Never use .dev.vars. Dotenv is a cross-platform standard that works across tools.
- Production secrets: configure production secrets via aubx wrangler secret put <KEY> or pnpm exec wrangler secret put <KEY>.
- Pre-commit scanning: scan staged diffs for accidental credentials or connection strings before committing.
- Credential sanitization (cfclean): use the cfclean git filter (via .gitattributes and cfclean setup)
  to sanitize account_id and database_id in wrangler.jsonc into placeholders upon staging,
  retaining real credentials locally in .env.
- Framework preference: favor Cloudflare Workers over Cloudflare Pages for static sites and fullstack applications (Workers Static Assets).
- Router preference: use Hono for HTTP API routing in worker applications.
- Testing toolchain: edge worker testing must use @cloudflare/vitest-plugin (vitest ^4.1.0 with the cloudflareTest() plugin). Never use the deprecated @cloudflare/vitest-pool-workers package.

## 2. Non-Negotiable Invariants and Hard Rules

- Hard ban on KV: NEVER use Cloudflare KV or Instant KV. Never suggest it, never configure it, and never implement it.
  - Relational and structured data: use D1 SQLite with Drizzle ORM.
  - Files, media, and blob storage: use R2 Object Storage with streaming.
  - Live state, sessions, room presence, and coordination: use Durable Objects with embedded SQLite.
  - Prohibit env.KV, @cloudflare/kv-asset-handler, and kv_namespaces bindings in wrangler.jsonc.
- D1 SQLite single source of truth: all D1 database access must use Drizzle ORM (drizzle-orm/d1). Schemas live in TypeScript (db/schema.ts or packages/db/src/schema.ts) as the single source of truth. Hand-written raw SQL strings for CRUD are prohibited.
- Schema placement: all table definitions, relationships, and migrations must reside exclusively in packages/db in monorepos.
- Data types: use INTEGER for numbers and booleans ({ mode: "boolean" }), TEXT for strings and ISO-8601 timestamps.
- Migration command: always apply migrations with wrangler d1 migrations apply <db> [--local|--remote]. Never use ad-hoc wrangler d1 execute for schema migrations.
- Migrations are immutable: never modify an already-applied migration file. Always create a new numbered .sql migration with drizzle-kit generate.
- Local migration verification: always apply and test migrations against --local before proposing deployment to remote databases.
- Batching discipline: batch multi-statement edge database operations with db.batch() to eliminate sequential round-trip latency and reduce billed queries.
- Zero-memory R2 streaming: never buffer files in worker memory (await request.arrayBuffer()). Stream uploads directly to R2 via env.BUCKET.put(key, request.body) and stream downloads via new Response(object.body).
- Non-blocking ingestion: never hold open client HTTP responses for secondary side effects (event tracking, audit logging, telemetry, cache updates). Return immediately (204 No Content or JSON) and run async writes inside ctx.waitUntil(promise).
- Floating promise prohibition: all promises in Workers must be explicitly awaited or passed to ctx.waitUntil() (await promise or void ctx.waitUntil(promise)). Floating promises cause silent runtime drops when isolates terminate.
- Module top-level isolation: never execute I/O, network requests, crypto operations, or read mutable environment state at the module top level. All I/O must resolve inside request/event handlers.
- Markdown Content Negotiation: for Markdown responses (Accept: text/markdown),
  always set the Vary: Accept header to isolate caches and emit x-markdown-tokens
  so calling agents track token economy.

## 3. Anti-Patterns and Prohibitions

- Using or suggesting KV: reject any introduction or suggestion of Cloudflare KV or Instant KV.
- N+1 D1 queries in loops: reject sequential round-trip queries inside loops; use batching or joined queries.
- Unbounded memory buffers: never buffer multi-megabyte payloads in memory; stream directly to R2 or the client.
- Blocking computation: avoid heavy pure-JS cryptographic hashing or large regex evaluations in the request hot path.
- Root dumping in monorepos: never place UI components, auxiliary worker scripts, or route handlers directly in the repository root.
- Non-null assertions: avoid env.BINDING!. Use explicit type narrowing or runtime checks (if (!env.DB) throw new Error("DB binding missing")).
- Leaking DOM APIs to Workers: never allow window, document, or browser events into worker code.

## 4. Fullstack Monorepo Scaffolding (cf-monorepo-scaffold)

- Scaffolding skill: use the cf-monorepo-scaffold skill when setting up or auditing Cloudflare projects.
- Package freshness: do not rely on training data for package versions.
  Always target latest package releases. Right after scaffolding, run
  aube audit and aube outdated to inspect and upgrade all dependencies.
- Standard topology layout:
  - apps/web: React 19 / Vite frontend deployed to Cloudflare Workers Static Assets.
  - apps/api: primary Hono HTTP API running on Cloudflare Workers.
  - apps/<worker>: dedicated directories for auxiliary edge processes (tail workers, queue consumers, cron workers).
  - packages/db: canonical Drizzle ORM schemas, D1 migrations, client factory, and seeds.
  - packages/types: shared API request/response contracts, Zod/Valibot validation schemas, and domain models.
  - packages/ui: optional shared UI design system and Tailwind components.
- Root boundary: repository root is strictly for orchestration (pnpm-workspace.yaml, root package.json, root tsconfig.base.json, .env, AGENTS.md, and goals/).
- Strict runtime and type isolation:
  - apps/web/tsconfig.json must configure "lib": ["DOM", "DOM.Iterable", "ESNext"] and must not include or inherit @cloudflare/workers-types.
  - apps/api/tsconfig.json must include "types": ["@cloudflare/workers-types/2023-07-01"] and must not include "DOM" in "lib".
- Cross-package fences: apps/ packages must never import directly from sibling apps/ packages (apps/api cannot import from apps/web). All shared entities must live in packages/types or packages/db.

## 5. Stateful Agents and Durable Objects

- Stateful Actor model: for interactive, multi-turn AI workloads, chat, or collaboration, extend the Agent<Env, State> base class backed by Durable Objects and embedded SQLite.
- Type-safe RPC: expose callable methods with the @callable() decorator instead of manual WebSocket routing.
- Route requests via routeAgentRequest(request, env) in the Worker fetch handler.
- Export both Agent class and State type from server.ts for full-stack type safety with client-side useAgent hooks.
- Per-agent timers: use instance alarms (this.schedule()) for session timers rather than heavy global database polling crons.
- Zero-polling realtime state: use Durable Objects with in-memory state and WebSockets (with alarm sweeps) for live visitor counts and room presence.

## 6. Operational Runbook, Sila Integration and Cloudflare MCP

- Bundled Cloudflare MCP: Sila MCP bundles Cloudflare MCP (cf_search, cf_execute) via https://mcp.cloudflare.com/mcp as a default upstream. Query Cloudflare API docs and inspect resources directly through Sila MCP without separate server setup.
- Automatic documentation caching: reference documentation retrieved through cf_search is automatically cached in Sila's local SQLite FTS5 database (sila.db). Recall it offline at any time via sila recall "Cloudflare <topic>".
- Specialized edge skills: use Sila's cf-d1-audit to audit queries for billing inflation and N+1 patterns, and cf-cpu-audit to detect isolate CPU execution bottlenecks.
- Pre-flight quality gates: run sila gate before committing to verify RTL directionality, strict goal attribution, and test passes.
- Epistemic goals: track edge milestones through sila goals to ensure clean verification loops across worker services.
- WAF Bot Management on MCP and Agent Endpoints: Cloudflare WAF bot management blocks
  generic User-Agents (such as Python-urllib). Edge endpoints and MCP services must
  document required custom User-Agent strings or configure WAF custom bypass rules to
  prevent spurious HTTP 403 Forbidden errors.
- AgentReady manifest synchronization: synchronize metadata across SEP-1649 server-card.json,
  ai-catalog.json, and llms.txt to prevent identity and discovery drift during rebrands.
- Operational commands:
  - Debugging D1 latency or timeout: run sila recall "D1 query batching timeout".
  - Resolved an isolate CPU limit error: record sila lesson "CPU limit exceeded on crypto" --cause=... --fix=....
  - Verifying changes: run pnpm test && pnpm check && sila gate.
