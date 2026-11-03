<!-- sila:begin (principles) -->
# Sila Universal Principles Pack & Engineering Constitution

## 1. Layer 1: Radical Simplicity (KISS & YAGNI - The Baseline Default)

**Philosophy**: Write only what is strictly necessary today. Reject speculative abstractions, hollow interfaces, premature generalization, and configurable frameworks before real demand exists.

- Write simple, pure functions before designing multi-layer abstractions or class hierarchies.
- Do not add configuration options, extension points, or plugins for hypothetical future requirements.
- Delete dead code, obsolete parameters, and speculative stubs immediately.
- Keep the dependency footprint minimal; prefer standard library solutions.
- *Anti-Pattern to Avoid*: Speculative enterprise OOP hierarchies, hollow wrapper structs, premature generalization, configurable frameworks with single implementations.

## 2. Layer 2: Human & Agent Ergonomics (CUPID - Surface & Interfaces)

**Philosophy**: Surfaces, APIs, and CLI interfaces must optimize for agent and developer joy: Composable, Unix-like, Predictable, Idiomatic, and Domain-based.

- Composable: Small, independent pieces that fit together cleanly with clear input/output contracts.
- Unix Philosophy: Do one thing well; support standard stdio streams, composable pipelines, and JSON output.
- Predictable: Consistent error returns, deterministic behavior, zero hidden side effects, idempotent mutations.
- Idiomatic: Follow the natural conventions, formatting, and idioms of the target language.
- Domain-Based: Model business reality directly in terms of the problem domain without artificial design-pattern jargon.
- *Anti-Pattern to Avoid*: Inconsistent return envelopes, hidden global state mutations, non-standard naming conventions, generic catch-all manager/helper classes.

## 3. Layer 3: Pragmatic Structural Seams (Pragmatic SOLID - Module Boundaries)

**Philosophy**: Enforce structural discipline at module boundaries without falling into dogmatic OOP abstraction traps.

- Single Responsibility Principle (SRP): Enforce file weight and complexity ceilings (<1,000 LOC; <10,000 tokens). Every module owns one cohesive concept.
- Interface Segregation Principle (ISP): Consume tiny, focused interfaces (1-3 methods max, e.g. io.Reader, io.Closer) at package boundaries. Return concrete types, accept interfaces.
- Pragmatic Substitution (LSP): Prefer concrete struct composition over deep inheritance trees or multi-layer polymorphic hierarchies.
- Explicit Seams: Separate pure computational logic from I/O and persistence layers.
- *Anti-Pattern to Avoid*: God files (>1,000 LOC), kitchen-sink interfaces with a dozen methods, deep inheritance trees, abstract base classes that only have one concrete child.

## 4. Layer 4: Data Flow & Concurrency (Functional Programming - FP)

**Philosophy**: Structure data flow through pure transformations, append-only immutable logs, and strict concurrency safety.

- Pure Transforms: Write stateless transform functions at data, JSON, and I/O boundaries.
- Immutable Ledgers: Treat historical state and logs as append-only value objects.
- Zero Shared Mutable State: Eliminate shared mutable variables across concurrent goroutines, workers, or asynchronous tasks.
- Concurrency Discipline: Always bound concurrency, handle cancellation contexts, and ensure goroutine leak prevention.
- *Anti-Pattern to Avoid*: Unsynchronized global variables, in-place mutation of shared data structures across concurrent workers, unbuffered goroutines without context cancellation.

## 5. Layer 5: Epistemic Grounding (Digital Data Sovereignty)

**Philosophy**: Logs record claims; Code is truth. Every architectural claim and instruction must be grounded in live filesystem reality.

- Grounding Verification: All architectural claims, paths, and instructions must reference live files (file_refs_exist = 1).
- Never Hide Stale State: Flag, surface, or prune stale claims and drifting documentation rather than masking errors.
- Closed-Loop Attribution: Complete the verification loop for every task before claiming completion.
- Data Sovereignty: Respect private boundaries; never leak confidential intelligence, secrets, or keys into public repositories.
- *Anti-Pattern to Avoid*: Hallucinated file paths, phantom dependencies, unverified claims, blind copy-pasting from outdated documentation without local verification.

---

## Active 9-Verb Memory & Knowledge Protocol

AI coding agents MUST actively use Sila's 9-verb continuity toolchain during development:

1. **Instant Pre-Search (`sila recall "<symptom or error>"`)**:
   Before guessing, hallucinating, or initiating expensive external web searches, agents MUST run `sila recall "<symptom or error>"` to check if the problem was previously solved in this or any registered project.
2. **Upstream Specification Grounding (`sila doc search <query>`)**:
   Before writing integrations, scaffolding architectures, or changing external dependencies, agents MUST query `sila doc search <query>` to ground decisions in authoritative local docs.
3. **Durable Invariants Capture (`sila memo "<directive>" --files=...`)**:
   When an architectural boundary, policy invariant, or non-negotiable rule is decided, agents MUST persist it via `sila memo "<directive>" --files=...`.
4. **Empirical Bug-Fix Recording (`sila lesson "<symptom>" --cause="..." --fix="..." --files=...`)**:
   When resolving a non-trivial bug, race condition, or edge-case failure, agents MUST record a structured lesson via `sila lesson "<symptom>" --cause="..." --fix="..." --files=...`.
5. **Architectural Pattern Recording (`sila case "<intent>" --approach="..." --invariant="..." --files=...`)**:
   When introducing a structural design pattern, module seam, or system abstraction, agents MUST record a case via `sila case "<intent>" --approach="..." --invariant="..." --files=...`.
6. **Cryptographic Grounding Verification (`sila facts verify`)**:
   Before declaring a task or goal complete, agents MUST run `sila facts verify` to ensure all referenced files and dependencies exist on disk.
7. **Context Priming (`sila prime`)**:
   At the start of every session or task, agents run `sila prime` to load active context, ongoing goals, and architectural constraints.
8. **Deterministic Quality Gate (`sila gate`)**:
   Before committing, agents MUST run `sila gate` (or `sila gate --pre-commit`) to execute automated deterministic sentries, race detection, and style checks.
9. **Structured Session Handoff (`sila handoff "<summary>" --open="..."`)**:
   At the end of every work session, agents MUST persist progress, unblocked next steps, and open questions via `sila handoff "<summary>" --open="..."`.

---

## Stack-Specific Invariants (Go)

- 1. Code Review & Hygiene: maintain pure Go standard library with zero third-party dependencies, Pure-Go CGO-Free Portability with CGO_ENABLED=0, and hard file ceilings under 1,000 LOC.
- 2. Goroutine & Context Safety: require context.Context on all background routines; declare defer cancel() immediately after timeout context creation; ensure goroutine leak prevention.
- 3. Goal Scaffolding: break findings into discrete single-outcome packages under goals/<slug>/ with goal.md, facts.md, plan.md, and explicit dependencies.
- 4. Test-Driven Implementation: claim goal locks before editing; write table-driven tests verifying both acceptance assertions and negative rejection paths.
- 5. Quality Gates & Race Detector Discipline: verify changes locally with linter scripts and full race detection ('go test -race ./...') with zero tolerance for data races.
- 6. Attributed Commits: use semantic prefixes containing the goal slug: feat(scope): desc [goal: <slug>]; run sila goals audit to certify receipts.
- 7. Git Branch Discipline: develop on dedicated feature branches; never push unprompted to remotes; re-verify race tests on merged main.
- 8. Security & Trust Boundaries: validate all CLI flags and stdin readers; enforce UID ownership; use mode 0600 on sensitive temporary storage.
- 9. Release Lifecycle & Value Changelog: implement a 24-hour cached daily version check, offer direct upgrades, provide a human-readable benefits-focused changelog, and credit contributors.
<!-- sila:end -->
