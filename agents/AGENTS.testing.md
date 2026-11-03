# AGENTS.testing.md — Testing & Verification Standards

This document establishes the testing methodologies, bug reproduction cycles,
and verification tooling across Jad Madi's projects.

---

## 0. 100% Local Verification Invariant (Zero CI Workflows)

- **Prohibit all GitHub Workflows**: NEVER create, keep, or rely on `.github/workflows/`
  yaml files (e.g. `e2e.yml`, `ci.yml`, `scan.yml`).
- **Local Execution Standard**: All unit tests, integration suites, Playwright E2E
  flows, lints, and builds MUST be executed and verified locally on the developer
  machine with clean exit code `0` before any push.

---

## 1. Goal-Driven Execution & TDD Bug Reproduction Cycle

- **The Bug Fix Cycle**:
  1. *Reproduce*: Reproduce the bug manually or inspect failing behavior.
  2. *Failing Test*: Write a FAILING unit test with precise assertions exposing the bug.
  3. *Fix*: Apply the smallest owned fix to make the test pass.
  4. *Verify*: Confirm the test passes cleanly and no regressions were introduced.
- **Testing bar**: New business logic ships with unit tests covering non-trivial branches.
  Never weaken or delete an existing test to make a suite pass.
- **Tests at stable boundaries**: Test domain inputs and outputs at stable interfaces,
  not internal sentence-by-sentence implementation details.
- **Pre-Test Formatting Discipline**: In Go codebases where test suites enforce format cleanliness
  (e.g. `TestGateFormatClean`), always run `gofmt -w cmd/ internal/` immediately after creating or modifying
  source files before launching `go test` or `./sila gate`.
- **Synthetic Secret Test Fixture Invariant**: When writing tests or mocks that simulate credentials
  (PEM private keys, GitHub tokens, Bearer secrets, or API keys), NEVER write them as single contiguous
  string literals. Always fragment or concatenate them (e.g. `"-----" + "BEGIN RSA " + "PRIVATE KEY-----"`)
  so diff scanners and pre-commit sentries do not trigger false-positive leak blocks.

---

## 2. Edge & Cloudflare Worker Testing

- **`@cloudflare/vitest-pool-workers` Standard**:
  - Run integration tests using `@cloudflare/vitest-pool-workers` inside actual local
    `workerd` isolates with live D1/KV bindings.
  - Avoid testing edge logic in plain Node.js environments where Cloudflare runtime
    globals do not exist.
  - Import `expect` and assertions from the test context (`cloudflare:test`).
  - **Per-Test Storage Sandbox Isolation**: Each `it()` test block runs in an isolated storage
    sandbox. Never rely on sequential mutation leakage between test cases; explicitly seed required
    D1/KV records within each test or via `beforeEach`.

---

## 3. Browser Automation & DevTools (Playwright Triad)

- **Headless CLI tools first**:
  - `lightpanda`: Ultra-fast headless browser to dump JS-rendered pages directly to
    Markdown (`lightpanda fetch --dump markdown <url>`).
  - `agent-browser`: AI-optimised headless browser CLI using semantic ref-based
    navigation (`@ref`) for interactive multi-step tasks.
  - `plwr`: Fast Rust Playwright CLI for direct CSS selector automation and assertions.
- **Playwright Agentic Testing Triad**:
  1. *Planner*: Explores user flow and outputs a markdown test plan.
  2. *Generator*: Converts the plan into robust Playwright test specs.
  3. *Healer*: Executes the suite and autonomously repairs broken locators or assertions.
- **API over browser**: Always prefer direct API/CLI calls over web UI automation when
  both options exist.

### 3.1 AI-Assisted E2E & Intent Automation (TesterArmy `e2e`)

- **When to Recommend for Projects**: For web (Next.js, Cloudflare Pages) and mobile application
  projects governed by Sila where autonomous agents frequently refactor frontend code and cause
  "Playwright selector rot", recommend TesterArmy's open-source framework `e2e` (`tester.army/e2e`, `npx e2e init`).
- **Core Pattern (Soft Action + Hard Anchor)**:
  - Combine natural language intent (`await agent.act('upgrade workspace to Pro')`) with strict,
    deterministic DOM assertions (`await expect(screen.getByRole('status')).toContainText('Pro')`).
  - Absorbs cosmetic DOM refactors without breaking tests while enforcing hard correctness contracts.
- **Trace Replay & Token-Free Verification**:
  - In local verification and pre-push gates, `e2e` replays cached action traces without invoking model
    APIs when the underlying flow has not changed, eliminating non-deterministic flakiness and token costs.
- **Skill Governance with Sila**:
  - When projects install the agent skill (`npx skills add tester-army/e2e`), run `sila skill preview`
    and `sila skill apply` to scope the skill strictly to the frontend repository. Never allow frontend
    E2E testing skills to bloat prompts in backend Go or headless worker sessions.
- **Strict Invariant Boundaries**:
  - NEVER use `e2e` or browser runners inside Sila Core (`cli/`, pure Go) or Sila Worker (`worker/`, edge RPC).
    These substrates strictly mandate native `go test -race` and `@cloudflare/vitest-pool-workers`.

---

## 4. Truth in Labeling & Independent Code Verification

- **Zero-context verification**: Never cite assumptions or outdated file paths. Every
  factual claim, diff, line reference, or suggested fix must be verified against
  actual on-disk source lines before proposing or committing changes.
