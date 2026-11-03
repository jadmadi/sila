## Working with me

- Be direct. No glazing. Never write "You're absolutely right!" or
  similar sycophantic openers.
- Push back with specific reasons when you disagree. If it's a gut
  feeling, say so.
- If you don't know something (env vars, API endpoints, CLI flags, model
  names, library APIs), stop and verify or say you don't know. Never
  invent technical details.
- Your training data is stale. Verify model names, package versions,
  and API surfaces before relying on them.
- Don't say a task is done until typechecks, linters, and tests pass.
  If none are configured, say so explicitly instead of claiming
  success.
- When renaming a function, type, or variable, search separately for:
  direct references, type-level references, string literals containing
  the name, dynamic imports, re-exports and barrel files, and test or
  mock files. One grep is not enough.

## Decisions and evidence

- Label every recommendation by decision type: fact (objectively
  verifiable), inference (conclusion derived from facts), design choice
  (multiple reasonable options exist), optimization (depends on an
  objective or tradeoff), domain judgment (requires expertise you may
  not possess), or irreversible/risky action (deserves extra scrutiny).
- Separate confidence from correctness. Never lead with "I'm X%
  confident". Instead state what evidence would make the decision
  trustworthy: rename variable: internal code consistency; API behavior
  change: existing callers + tests + API contract; database schema
  change: data model + migrations + usage; security: best practices +
  threat model; performance optimization: benchmark data; architecture:
  requirements + constraints + competing designs. Confidence is a
  property of the model, evidence is a property of the decision;
  optimize for the latter.
- If a decision depends on domain knowledge not established in the
  project, explicitly identify the knowledge gap rather than treating
  your preferred solution as optimal.

## Planning and implementation handoffs

Plan with the stronger model; write for a weaker implementer with none of
the conversation context. Over-specify decisions, boundaries and evidence
in every new or revised goal, task and delegated assignment.

1. Make every task self-contained: goal, one-line why, repository/cwd,
   prerequisite task IDs, exact edit/create/read paths, and the relevant
   interfaces or short source excerpts inline. Links supplement the contract.
2. Decide architecture, names, libraries, data shapes, error behavior and
   edge cases before dispatch. Verify them against current code. Mark a
   missing design decision NOT READY and return it to the planner.
3. Keep one outcome per task, normally 1–4 implementation files. Split larger
   work into ordered cards with explicit dependencies. Do not split away the
   verification needed to prove that outcome.
4. Give stable acceptance IDs, exact commands/cwd, expected exits and
   observable results, including negative cases. Name required new tests;
   distinguish future acceptance from checks actually run. Compilation,
   tracker status and a commit alone do not prove behavior.
5. List allowed files, constraints and non-goals. Default to no new
   dependencies or unrelated refactors. Preserve unrelated work and history.
   Settle exceptions in the plan rather than leaving them to the implementer.
6. Include at least one concrete input/output example or existing code-style
   excerpt, plus the edge case most likely to be implemented incorrectly.
7. Fix the return format: files, case results, commands/exits, evidence,
   candidate identity and blockers. Validate programmatic handoffs against
   the project's schema, or define one before dispatch.
8. Stop affected work when blocked; preserve valid changes and report evidence
   and the decision needed. Never guess, widen scope, invent evidence or
   bypass a guard. After two repeated failures, return to the planner.

The strong planner reviews the diff and receipts against each case, then
accepts or sends a targeted correction. Implementers return ready_for_review.
Name the reviewer; self-review is not independent. Add recurring mistakes as
constraints or examples in the same card.

Keep durable conventions in AGENTS.md and task-specific details in each card.
Preserve goal ownership and acceptance IDs. With Sila, register/read back goals
and dependencies before dispatch. Registration alone is not implementation.

Replace all template placeholders before dispatch; use the verified repo toolchain.

```markdown
# Task: <ID and imperative title>

## Context
<Goal, one-line why, repository/cwd, prerequisite IDs and readiness>

## Files
- Edit: <exact path and symbol>
- Create: <exact path>
- Read: <reference path; inline essential excerpt below>

## Requirements
1. <Fixed decision and specific behavior>

## Interfaces / signatures (fixed)
<Exact signature/data shape and required error behavior>

## Constraints and non-goals
<Allowed scope, forbidden edits/dependencies and preserved behavior>

## Example
<Input -> expected output; important boundary case>

## Acceptance criteria
- AC-001: <command, cwd, expected exit and observable assertion>
- AC-002: <negative input -> rejected result with no unwanted side effect>

## If blocked
Stop affected work; report evidence and the decision needed. Do not guess.

## Return
<Fixed project JSON schema or exact fields: status, summary, changed files,
case results, commands/exits, evidence, candidate, blocker>
```

## Before coding

- State assumptions explicitly before implementing. If uncertain, ask.
- If multiple interpretations of a request exist, present them, don't
  pick silently.
- If something is unclear, stop and name what's confusing instead of
  guessing.
- Write the minimum code that solves the problem. No speculative
  features, no abstractions for single-use code, no configurability
  that wasn't asked for.
- Don't add error handling for impossible scenarios.
- Touch only what the task requires. Don't "improve" adjacent code,
  comments, or formatting.
- Match existing style in a file, even if you'd write it differently.
- If you notice unrelated dead code or bugs, mention them, don't fix
  them unprompted.
- Clean up orphans your changes create (unused imports, variables).
  Don't remove pre-existing dead code unless asked.

- **Verify your changes work before marking tasks complete**: Run
  linting, type checks, and manual tests. If no verification tools
  exist, explicitly state this.
## Running scripts and commands

- Use GitHub's "Scripts to Rule Them All" approach to running scripts
  and commands:
  https://github.com/github/scripts-to-rule-them-all
- If the project has a "scripts" or "script" directory, run those
  scripts for tasks like testing, linting, formatting, etc.
- If the project has a `script/lint` or `scripts/lint` script, run it
  before committing changes with Git.
- If linting fails, fix the linting errors and run the linter until
  all the errors are resolved.

# Agent Rules

## Scripting Language Preference
- When creating helper scripts, one-off tools, or automation scripts,
  you **MUST** use Go.
- Do **NOT** use Python or other scripting languages unless explicitly
  requested by the user.
- Why: code review, not code writing, is now the bottleneck. Go is
  boring and uniform, so AI-generated Go is fast to verify. The compiler
  catches hallucinated APIs and type mismatches without running them.
  gofmt makes AI output match project style. A static binary needs no
  venv or dependency setup to test.

## Working with Git

- When creating git commits, always use a semantic commit prefixes,
  with or without parenthetical qualifiers.
- When opening pull requests or merge requests, always use a semantic
  commit message as the title.
- Never bypass pre-commit hooks. Never use `--no-verify` or equivalent
  flags without explicit permission.

## Working with Node.js and npm

- Always use `npx` when running global npm CLIs, e.g. `npx wrangler`
  instead of `wrangler`
- Always use aube, aubr and aubx; otherwise use pnpm but never npm.

## Working with Cloudflare

- Always using JSONC for Workers configs (not TOML)
- Use .env files for secrets and environment variables. Don't use
  .dev.vars as those are Cloudflare-specific. dotenv is a de facto
  standard that works across more platforms and tools.
- Always use the latest verions of Wrangler and Cloudflare's npm
  packages.
- Whenever it's possible to do something via API or CLI, favor that
  over using the Cloudflare dashboard.
- Favor Cloudflare Workers over Cloudflare Pages for static sites
- Use Hono for worker apps when appropriate

## Working with GitHub and GitLab

- Use `gh` for GitHub repositories and `glab` for GitLab repositories.
- When writing a pull request (GitHub) or merge request (GitLab) body,
  be concise. Explain the problem and the solution succinctly.
- Whenever you are commenting on a PR or MR, always make sure you're
  commenting in the right place.
- If you're responding to a reviewer's inline comment, then comment on
  their comment, not the PR/MR itself.
- When analyzing an issue, PR, or MR, read all the comments and
  discussion threads, not just the title and opening description. The
  context and nuance is often in the conversation.
- After creating or updating a pull request or merge request or issue,
  open the URL in my default browser for me.
- When creating a new GitHub repo with `gh repo create`, set the
  `--homepage` and `--description` flags if there's enough context to
  do so.

## Writing a good PR body

Follow these guidelines when writing the body of the pull request:

- Be concise and descriptive
- Don't oversell the changes. It's not an advertisement.
- Don't use fancy words like "comprehensive", "utilize", "implement",
  "exhaustive", "simplify", "optimize", "seamlessly"
- Start the PR body with the words "This PR..."
- Do not include a "Summary" heading
- Do not mention the test plan
- If there is a Linear ticket or GitHub issue, include a link to the
  ticket or issue in the PR body.
- If there is a GitLab issue, include a link to the issue in the MR
  body.

## Style guide

Follow these style guidelines in chat, commit messages, and prose:

- Be concise and descriptive
- Don't oversell the changes. It's not an advertisement.
- Don't use fancy words like "comprehensive", "utilize", "implement",
  "exhaustive", "simplify", "optimize", "seamlessly"
- When writing markdown, avoid using headings smaller than H2
- When writing markdown, don't use bold.
- When writing markdown tables, pad cells with spaces so columns align.
  This makes tables legible in monospace contexts like terminals.
- When writing markdown, keep line length 80-110 characters (MD013).
  Wrap long lines, break links and tables accordingly.
- Never use em dashes (—). Use commas, colons, or separate sentences
  instead.

## Types and documentation

- Prefer types over prose documentation for API contracts. Types are
  executable and can't drift from the implementation.
- Define schemas (e.g. Zod) as the single source of truth, then derive
  TypeScript types, OpenAPI specs, and SDKs from them.
- Use schema-first design: the schema defines the contract, and the
  implementation conforms to it. Don't generate types from runtime
  behavior.
- For service-to-service communication, prefer RPC with shared types
  over HTTP endpoints with separate documentation.
- Reserve prose docs for explaining _why_ a system exists and _when_ to
  use it, not _what_ it accepts. Types handle the _what_.
- If an API is too complex to type, that's a design problem worth
  fixing.

## Fetching data

If you make web requests to public pages and get blocked by sites like
OpenAI's docs pages returning 403 status codes, use other methods to
fetch the data.

## Browser Automation

Use the following tools for browser automation tasks:

- https://agent-browser.dev - installed as the `agent-browser` CLI
  tool.
- https://github.com/andreasjansson/plwr for browser automation. It's
  installed as a `plwr` CLI tool.
- Favor these CLI tools over any available MCP servers.
- IMPORTANT: Never use the Chrome DevTools MCP unless explicitly asked
  to do so.
- When using the Chrome DevTools MCP, check for an existing tab already
  on the relevant page before opening a new one. If no such tab exists,
  open a new tab. Don't navigate away from or overtake unrelated
  existing tabs.
- IMPORTANT: Don't use browser automation for tasks that can be
  accomplished via API or CLI.

## Secrets and credentials

- NEVER hardcode API keys, tokens, passwords, or other secrets in
  source code. Always read them from environment variables.
- Before committing, scan staged changes for anything that looks like a
  secret (API keys, tokens, passwords, connection strings). If found,
  stop and flag it.
- Secrets belong in `.env` files (which must be in `.gitignore`), not
  in source code.
- If you find a secret already committed in a repo, flag it immediately
  and recommend rotating it.

## Important rules

- IMPORTANT: NEVER PUSH TO THE MAIN OR DEFAULT BRANCH. ALWAYS PUSH
  TO A FEATURE BRANCH.
- IMPORTANT: If your last message included HTTP or HTTPS URLs, offer
  to open those for me in my default browser.
- Don't push commits to branches with PRs that have already been
  merged.

## General advice

- Whenever it's possible to do something via API or CLI, favor that
  over using a web-based flow, which requires manual clicking and is
  less efficient for automation.
- Finish your messages with a list of any relevant URLs that I should
  know about. That could include pages you looked up, GitHub issues or
  PRs you created, etc. No need to repeat them too many times.
- Whenever you overcome some kind of obstacle or challenge or learns
  something that could be generally useful across all sessions, prompt
  to add a note to the global AGENTS.md file so that the future
  sessions can benefit. This could be a new rule, a new style
  guideline, a new tool to use, or anything else that would be helpful
  for future agents to know.

## Self-improvement
- **After encountering project-specific issues, propose adding them to
  global AGENTS.md if they apply broadly**: For example, migration
  conflicts, error boundary patterns, or architecture verification
  steps.

- When I correct you, push back, or express frustration, after you
  finish the immediate task, propose a one-line addition or edit to the
  relevant AGENTS.md so the same mistake doesn't recur.
- Decide scope explicitly. Global (your global AGENTS.md) if the rule
  applies across all my projects. Project (`./AGENTS.md`) if it only
  applies to this codebase. Neither if it's a one-off. State your scope
  decision and why before proposing the edit.
- Project rules should be project-specific (paths, scripts, codebase
  idioms), not general engineering preferences. If a proposed project
  rule could reasonably apply to other repos, propose it as a global
  rule instead.
- Before proposing, search the relevant AGENTS.md for an existing rule
  that covers this. If one exists, propose tightening it, not adding a
  new bullet.
- Show me the proposed diff. Do not edit the file until I approve.
- Match the style of the surrounding section: bullet, no bold, no em
  dashes, concise.
- If you suggest adding more than two rules in one session, stop and
  ask whether we're overcorrecting.
- When an AGENTS.md grows past about 200 lines, propose deletions or
  consolidations alongside additions, not just additions.
- If I ask you to "audit AGENTS.md", read the whole file and propose a
  list of rules to delete because they're obsolete, duplicated, or
  never followed in practice, with one-sentence reasoning each.
- At the start of work in a new project, check whether the project has
  its own `AGENTS.md`. If it doesn't, suggest creating one and offer to
  draft it. AGENTS.md is for agents: technical instructions about the
  project (stack, scripts, conventions, gotchas, paths, build and test
  commands). Include an instruction in the project-level AGENTS.md to
  make it update itself when meaningful changes are made to the
  project.
- Also check whether the project has a `README.md`. If it doesn't,
  suggest creating one. README.md is for humans: what the project is,
  why it exists, and how a person gets started. Don't conflate the two.
  If a project has only one of the two, don't duplicate content across
  them, link between them where useful. Link to AGENTS.md from the
  README.md when relevant.

## Error Handling and User Experience

- Always implement error boundaries in React Router apps to prevent
  generic 500 errors
- When encountering "Store not found" or similar routing issues, check
  if root domain handling is implemented
- Provide meaningful error messages to users instead of generic
  "Unexpected Server Error"
- Log errors for debugging but show user-friendly messages in the UI

## Root Domain Handling

- When an app resolves resources from hostname, add special handling
  for root domains
- Root domains should typically show landing pages or dashboards, not
  "not found" errors
- Test both root domain and subdomain routes to ensure proper behavior

## Database Migrations

- Always check existing schema before creating new migrations
- Migrations are sequential and immutable - never edit existing
  migration files
- If a migration fails, check if columns/tables already exist from
  previous migrations
- Test migrations locally before applying to remote databases
- Use valid D1 syntax (no BOOLEAN, use INTEGER; no DATETIME, use TEXT)

## Architecture Verification

- Before deploying, verify workers.json matches actual deployed workers
- Check for overlapping routes between workers
- Remove or redirect deprecated workers rather than leaving them active
- Document worker purposes clearly to avoid confusion
