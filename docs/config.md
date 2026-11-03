# Configuration Reference & Architecture

Sila (`صِلَة`) uses an offline-first, cascading configuration architecture designed for radical transparency, data sovereignty, and zero unconsented network egress.

---

## 1. Cascading Resolution Precedence

Configuration values are resolved following standard 12-factor precedence (highest to lowest):

```
┌────────────────────────────────────────────────────────┐
│  1. Command-Line Flags (e.g., --reveal, --json, etc.)  │
└───────────────────────────┬────────────────────────────┘
                            │ overrides
┌───────────────────────────▼────────────────────────────┐
│  2. Environment Variables (SILA_*, TYPESAFE_*, etc.)   │
└───────────────────────────┬────────────────────────────┘
                            │ overrides
┌───────────────────────────▼────────────────────────────┐
│  3. Project Configuration (.sila/config.json in repo)  │
└───────────────────────────┬────────────────────────────┘
                            │ overrides
┌───────────────────────────▼────────────────────────────┐
│  4. Global Configuration (~/.config/sila/config.json)  │
└───────────────────────────┬────────────────────────────┘
                            │ overrides
┌───────────────────────────▼────────────────────────────┐
│  5. Built-in Safe Defaults (strictly offline)          │
└────────────────────────────────────────────────────────┘
```

When Sila executes inside any project subdirectory, it walks up the directory tree to the Git repository root looking for `.sila/config.json`. If present, project-specific settings (such as allowed doc sites, custom search provider, or repository-scoped assistance) seamlessly override global settings.

---

## 2. Standard File Locations

In accordance with the **Linux XDG Base Directory Specification**:

| Target | Path | Environment Override | Default Location |
| :--- | :--- | :--- | :--- |
| **Primary Global Config** | `config.json` | `XDG_CONFIG_HOME` | `~/.config/sila/config.json` |
| **Project Config Override** | `.sila/config.json` | - | `<repo-root>/.sila/config.json` |
| **Legacy Config Fallback** | `config.json` | `XDG_DATA_HOME` | `~/.local/share/sila/config.json` (auto-migrated) |
| **Primary SQLite DB** | `sila.db` | `SILA_DB` or `XDG_DATA_HOME` | `~/.local/share/sila/sila.db` |
| **State Directory** | - | `XDG_STATE_HOME` | `~/.local/state/sila/` |
| **Cache Directory** | - | `XDG_CACHE_HOME` | `~/.cache/sila/` |

> [!NOTE]
> When `~/.config/sila/config.json` is missing and legacy `~/.local/share/sila/config.json` is found, Sila automatically reads the legacy configuration, creates `~/.config/sila/` (mode `0700`), and writes `~/.config/sila/config.json` (mode `0600`), ensuring continuous operation without manual intervention.

---

## 3. Schema & Keys Specification

All keys in `config.json` are typed, validated, and documented:

| Key | Type | Default | Valid Values | Description | Security Classification |
| :--- | :--- | :--- | :--- | :--- | :--- |
| `search_provider` | `string` | `"duckduckgo"` | `duckduckgo`, `brave`, `tavily`, `kagi`, `google` | Active web search provider for `sila search` and doc grounding. | Public configuration. |
| `brave_api_key` | `string` | `""` | - | API key for Brave Search REST API (2k free queries/mo). | Secret (mode 0600, masked). |
| `tavily_api_key` | `string` | `""` | - | API key for Tavily AI developer search engine. | Secret (mode 0600, masked). |
| `kagi_api_key` | `string` | `""` | - | API key for Kagi Universal Search API. | Secret (mode 0600, masked). |
| `google_api_key` | `string` | `""` | - | API key for Google Custom Search Engine. | Secret (mode 0600, masked). |
| `google_cse_cx` | `string` | `""` | - | Programmable Search Engine CX ID. | Public configuration. |
| `github_token` | `string` | `""` | - | Personal access token for GitHub API. | Secret (mode 0600, masked). |
| `sourcegraph_token` | `string` | `""` | - | Access token for Sourcegraph code search. | Secret (mode 0600, masked). |
| `stackoverflow_key` | `string` | `""` | - | Stack Exchange API application key. | Secret (mode 0600, masked). |
| `typesafe_api_key` | `string` | `""` | - | API key for TypeSafe assistance & semantic sentry. | Secret (mode 0600, masked). |
| `assist_provider` | `string` | `"typesafe"` | `typesafe`, `mock`, `offline` | Assistance backend engine. | Public configuration. |
| `assist_enabled` | `boolean` | `false` | `true`, `false` | Master toggle for AI assistance calls. | Strict Consent Gate. |
| `assist_scopes` | `[]string` | `[]` | `gate`, `recap`, `search`, `doctor`, `live`, `verify`, `all` | Consented feature scopes allowed to use assistance. | Capability Consent Fence. |
| `assist_projects` | `[]string` | `[]` | - | Allowed project IDs for assistance calls (empty = all). | Privacy Fence. |
| `cloud_enabled` | `boolean` | `false` | `true`, `false` | Master toggle for Sila Cloud edge sync. | Strict Consent Gate. |
| `cloud_endpoint` | `string` | `""` | - | URL of Cloudflare Worker edge bridge. | Remote Endpoint URL. |
| `cloud_token` | `string` | `""` | - | Bearer authentication token for edge worker. | Secret (mode 0600, masked). |
| `cloud_scopes` | `[]string` | `[]` | `sync`, `backup`, `mcp`, `all` | Consented remote sync scopes. | Capability Consent Fence. |
| `cloud_projects` | `[]string` | `[]` | - | Allowed project IDs for cloud synchronization. | Privacy Fence. |
| `doc_sites` | `[]string` | `[]` | URL patterns | Registered documentation domains for deep search. | Allowlist boundary. |

---

## 4. Security Invariants & Data Sovereignty

1. **Zero Ambient Network Calls:**
   Storing an API key or selecting an assistance provider **never** triggers ambient network calls. Remote assistance requires:
   - `assist_enabled: true`
   - An explicit, matching scope in `assist_scopes` (e.g. `"gate"`)
   - The active project listed in `assist_projects` (or `assist_projects` empty)
2. **Strict Redaction:**
   Running `sila config` or `sila config get` redacts all secrets (`••••••••1234`) by default. Plaintext secrets are output only when the explicit `--reveal` flag is passed.
3. **Restricted File Permissions:**
   All configuration directories are created with `0700` (`rwx------`) and configuration files with `0600` (`rw-------`).

---

## 5. CLI Guided Experience

### Inspecting Paths
```bash
# Human-readable path report
sila config path

# Machine-readable JSON
sila config path --json
```

### Explaining Configuration Keys
```bash
# List all supported keys, their types, defaults, and descriptions
sila config explain

# Detailed explanation of a single key
sila config explain assist_enabled

# JSON schema and descriptions
sila config explain search_provider --json
```

### Inspecting JSON Schema
```bash
# Output Draft-07 JSON Schema for config.json
sila config schema
```

### Initializing Configuration
```bash
# Initialize global config (~/.config/sila/config.json)
sila config init

# Initialize repository-scoped config (.sila/config.json)
sila config init --project
```

### Setting and Getting Keys
```bash
# Set global configuration key
sila config set search_provider brave
sila config set brave_api_key "BSA..."

# Set project-scoped configuration key
sila config set --project search_provider tavily
sila config set --project assist_enabled true

# Read configuration key
sila config get search_provider
sila config get brave_api_key --reveal
```
