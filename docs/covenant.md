# Digital Data Sovereignty Covenant (عهد السيادة الرقمية)

> *"Your thoughts, your cognitive traces, and your architectural decisions belong to you. No tool, harness, or vendor may hold them hostage."*

The **Sila Digital Data Sovereignty Covenant** establishes the ethical, legal, and architectural guarantees governing how Sila observes, ingests, indexes, and retains developer data.

In an era where proprietary developer platforms enclose engineer interactions inside walled gardens, telemetry collectors, and vendor-locked cloud databases, Sila is architected as an **emancipatory, sovereign substrate**. Sila does not view developer activity as raw exhaust to be harvested; rather, it honors the **dignity of developer thought** and guarantees the developer's absolute sovereignty over their cognitive artifacts.

---

## The Four Pillars of Digital Data Sovereignty

### 1. The Right to Absolute Local Custody (حق الحيازة المحلية المطلقة)
> *Every developer interaction, code trace, architectural memo, and institutional lesson remains 100% on the developer's physical hardware by default.*

- **Zero Ambient Telemetry:** Sila contains **zero** ambient telemetry libraries, background tracking daemons, analytics SDKs (no Segment, PostHog, Mixpanel, Amplitude, Statsig, or Sentry), or hidden phone-home pings.
- **Physical SQLite Sovereignty:** All knowledge, goals, memos, and session histories are stored in a standard, local SQLite database (`~/.local/share/sila/sila.db` or `.sila/sila.db`) located directly on the developer's filesystem.
- **Offline By Default:** Sila executes completely offline without internet connectivity. No remote server is required for full indexing, searching, synthesis, goal tracking, rule compilation, or MCP tool dispatch.
- **Air-Gapped Compliance:** Sila can be run inside completely isolated, air-gapped corporate, governmental, or confidential research environments without fear of silent external network leakage.

### 2. The Right to Epistemic Non-Destruction (حق عدم المساس الإبستيمي)
> *Sila is a non-destructive, read-only observer that honors existing tools and never corrupts or encloses external session state.*

- **Strict Read-Only Observation:** When indexing external AI coding tool transcripts (Claude Code, Devin, Codex, OpenCode, OpenHands, Gemini CLI, Pi, Cursor, Windsurf, Replit, ZCode), Sila opens SQLite databases and session logs strictly in read-only mode (`mode=ro`).
- **Zero Tool Mutation:** Sila never mutates, modifies, or truncates the session files or operational databases of other AI assistants. Your original transcripts remain pristine and untouched.
- **Zero Proprietary Lock-In:** Sila never injects proprietary lock files, non-standard runtime dependencies, or harness-exclusive markers into your codebase. You are free to adopt, evaluate, or discard any AI harness at any moment.

### 3. The Right to Cognitive Interoperability & Portability (حق الانتقالية والتكامل المعرفي)
> *Developers possess an inalienable right to cross-harness continuity without forfeiting hard-won lessons, architectural memory, or decisions.*

- **Open Standard Relational Storage:** All facts, goals, lessons, and handoffs reside in standard relational SQLite tables with standard FTS5 full-text indexing. There are no opaque binary blobs or proprietary vector store file formats.
- **Universal Multi-Agent Interoperability:** Through its 44-tool Model Context Protocol (MCP) server, Sila provides equal, instantaneous memory access to all AI tools simultaneously. A lesson learned during a debugging session in Claude Code is immediately available to Devin, Codex, or Gemini.
- **Instant Exportability & Migration:** Developers can export their entire knowledge base into open JSON, Token-Oriented Object Notation (TOON), or standard SQL dumps (`sila backup`, `sqlite3 sila.db .dump`) with a single command.

### 4. The Right to Strict Cryptographic Privacy Boundaries (حق الحماية والخصوصية الصارمة)
> *When optional external relays, edge synchronizations, or cloud bridges are explicitly enabled, local secret scrubbing is mathematically enforced prior to network dispatch.*

- **Local Sentry Pre-Redaction:** Any payload destined for an external surface (Discord webhooks, Telegram bots, Gemini edge bridge, remote MCP relays) is sanitized locally on the machine before opening any network socket.
- **High-Precision Secret Scrubbing:**
  - Local home directory paths (`/home/username/...` or `/Users/username/...`) are normalized to `~` to prevent username and infrastructure enumeration.
  - Personal Access Tokens (`ghp_...`, `gho_...`, `github_pat_...`), Bearer tokens, OpenAI-style API keys (`sk-...`), and private keys (PEM blocks) are scrubbed and replaced with safe redaction markers (`[REDACTED_API_KEY]`, `[REDACTED_GH_TOKEN]`, `[REDACTED_PRIVATE_KEY]`).
  - Query parameter secrets (`password=`, `token=`, `client_secret=`) are stripped before transmission.
- **Explicit Consent Architecture:** External gateways and edge mailboxes are **strictly disabled by default** (`gateway_enabled = false`). Zero network calls are made unless the developer explicitly opts in via configuration or command-line flags.

---

## Verification & Continuous Enforcement

The principles of this Covenant are not merely aspirational prose; they are continuously enforced in software:

```bash
# Verify adherence to the Digital Data Sovereignty Covenant on your machine
sila doctor --covenant
```

The Covenant verification engine audits:
1. **Loaders Custody:** Proves all 10+ AI CLI session loaders open sources in strict read-only mode (`mode=ro`).
2. **Ambient Telemetry Sentry:** Confirms zero analytics and tracking libraries in the runtime binary.
3. **Secret Redaction Pipeline:** Tests high-precision secret scrubbing with known sensitive token vectors.
4. **Local Database Integrity:** Validates that the active store resides on local disk with open relational schema.

---

## Architectural Pledge

By building with and maintaining Sila, we commit to honoring these rights across all future releases, ensuring that Sila remains the definitive **open, sovereign, and incorruptible substrate for human and artificial intelligence collaboration**.
