package agents

import (
	"fmt"
	"strings"
)

// Delimiters for the Universal Principles Pack managed block.
const (
	PrinciplesBlockBegin = "<!-- sila:begin (principles) -->"
	PrinciplesBlockEnd   = "<!-- sila:end -->"
)

// ConstitutionLayer defines a single architectural philosophy layer.
type ConstitutionLayer struct {
	Name        string   `json:"name"`        // e.g. "Layer 1: Radical Simplicity (KISS & YAGNI)"
	Philosophy  string   `json:"philosophy"`  // Core philosophy sentence
	Invariants  []string `json:"invariants"`  // Non-negotiable rules
	AntiPattern string   `json:"antipattern"` // What bloat to avoid
}

// PrinciplesPack bundles Sila's Layered Engineering Constitution, the 9-verb
// active memory loop, and stack-specific tailoring.
type PrinciplesPack struct {
	Layers      []ConstitutionLayer `json:"layers"`
	MemoryLoop  string              `json:"memory_loop"` // How agents interact with the 9 memory verbs
	TechStack   string              `json:"tech_stack"`  // go, typescript, rust, python, polyglot
	CustomRules []string            `json:"custom_rules,omitempty"`
}

// CanonicalConstitutionLayers returns Sila's 5 foundational architectural layers.
func CanonicalConstitutionLayers() []ConstitutionLayer {
	return []ConstitutionLayer{
		{
			Name:       "Layer 1: Radical Simplicity (KISS & YAGNI - The Baseline Default)",
			Philosophy: "Write only what is strictly necessary today. Reject speculative abstractions, hollow interfaces, premature generalization, and configurable frameworks before real demand exists.",
			Invariants: []string{
				"Write simple, pure functions before designing multi-layer abstractions or class hierarchies.",
				"Do not add configuration options, extension points, or plugins for hypothetical future requirements.",
				"Delete dead code, obsolete parameters, and speculative stubs immediately.",
				"Keep the dependency footprint minimal; prefer standard library solutions.",
			},
			AntiPattern: "Speculative enterprise OOP hierarchies, hollow wrapper structs, premature generalization, configurable frameworks with single implementations.",
		},
		{
			Name:       "Layer 2: Human & Agent Ergonomics (CUPID - Surface & Interfaces)",
			Philosophy: "Surfaces, APIs, and CLI interfaces must optimize for agent and developer joy: Composable, Unix-like, Predictable, Idiomatic, and Domain-based.",
			Invariants: []string{
				"Composable: Small, independent pieces that fit together cleanly with clear input/output contracts.",
				"Unix Philosophy: Do one thing well; support standard stdio streams, composable pipelines, and JSON output.",
				"Predictable: Consistent error returns, deterministic behavior, zero hidden side effects, idempotent mutations.",
				"Idiomatic: Follow the natural conventions, formatting, and idioms of the target language.",
				"Domain-Based: Model business reality directly in terms of the problem domain without artificial design-pattern jargon.",
			},
			AntiPattern: "Inconsistent return envelopes, hidden global state mutations, non-standard naming conventions, generic catch-all manager/helper classes.",
		},
		{
			Name:       "Layer 3: Pragmatic Structural Seams (Pragmatic SOLID - Module Boundaries)",
			Philosophy: "Enforce structural discipline at module boundaries without falling into dogmatic OOP abstraction traps.",
			Invariants: []string{
				"Single Responsibility Principle (SRP): Enforce file weight and complexity ceilings (<1,000 LOC; <10,000 tokens). Every module owns one cohesive concept.",
				"Interface Segregation Principle (ISP): Consume tiny, focused interfaces (1-3 methods max, e.g. io.Reader, io.Closer) at package boundaries. Return concrete types, accept interfaces.",
				"Pragmatic Substitution (LSP): Prefer concrete struct composition over deep inheritance trees or multi-layer polymorphic hierarchies.",
				"Explicit Seams: Separate pure computational logic from I/O and persistence layers.",
			},
			AntiPattern: "God files (>1,000 LOC), kitchen-sink interfaces with a dozen methods, deep inheritance trees, abstract base classes that only have one concrete child.",
		},
		{
			Name:       "Layer 4: Data Flow & Concurrency (Functional Programming - FP)",
			Philosophy: "Structure data flow through pure transformations, append-only immutable logs, and strict concurrency safety.",
			Invariants: []string{
				"Pure Transforms: Write stateless transform functions at data, JSON, and I/O boundaries.",
				"Immutable Ledgers: Treat historical state and logs as append-only value objects.",
				"Zero Shared Mutable State: Eliminate shared mutable variables across concurrent goroutines, workers, or asynchronous tasks.",
				"Concurrency Discipline: Always bound concurrency, handle cancellation contexts, and ensure goroutine leak prevention.",
			},
			AntiPattern: "Unsynchronized global variables, in-place mutation of shared data structures across concurrent workers, unbuffered goroutines without context cancellation.",
		},
		{
			Name:       "Layer 5: Epistemic Grounding (Digital Data Sovereignty)",
			Philosophy: "Logs record claims; Code is truth. Every architectural claim and instruction must be grounded in live filesystem reality.",
			Invariants: []string{
				"Grounding Verification: All architectural claims, paths, and instructions must reference live files (file_refs_exist=true).",
				"Never Hide Stale State: Flag, surface, or prune stale claims and drifting documentation rather than masking errors.",
				"Closed-Loop Attribution: Complete the verification loop for every task before claiming completion.",
				"Data Sovereignty: Respect private boundaries; never leak confidential intelligence, secrets, or keys into public repositories.",
			},
			AntiPattern: "Hallucinated file paths, phantom dependencies, unverified claims, blind copy-pasting from outdated documentation without local verification.",
		},
	}
}

// SpecialistRoutingSection maps six high-frequency specialist intents to both
// CLI and MCP commands. It is the mandatory pre-fallback routing layer: an
// agent checks these surfaces before inventing a generic alternative.
var SpecialistRoutingSection = "" +
	"## Specialist Capability Routing (Mandatory Pre-Fallback Checks)\n\n" +
	"| Task trigger | CLI | MCP |\n" +
	"| :--- | :--- | :--- |\n" +
	"| Prior research, specs, or 'why did we choose X?' (before `grep`, `find`, `git log`) | `sila search \"<query>\" --omni`, `sila search expand <id>`, `sila doc search \"<query>\"` | `omni_search`, `get_message_context`, `search_docs` |\n" +
	"| Temporal progress ('what did we work on in the last 24h?') | `sila standup last 24h`, `sila digest --since=24h`, `sila recap` | — |\n" +
	"| Domain rules and codebase learning capture | `sila guide [domain]`, `sila learn` | `get_operational_guidebook` |\n" +
	"| Adversarial review and security/compliance audits | `sila persona list`, `sila hire --for-goal=<slug>`, `sila audit` | `audit_goal` |\n" +
	"| Reusable skills and workflow discovery | `sila skill list`, `sila skill create` | `list_skills`, `skill_search`, `skill_load`, `route_workflow` |\n" +
	"| Symbol blast-radius and epistemic decisions | `sila edit-check --symbol=<sym>`, `sila safe-delete --symbol=<sym>`, `sila note --symbol=<sym>`, `sila decision`, `sila vision triage` | `create_decision`, `list_decisions`, `compare_proposals` |"

// CanonicalMemoryLoop returns the operational protocol for Sila's 9 knowledge verbs.
func CanonicalMemoryLoop() string {
	return `## Active 9-Verb Memory & Knowledge Protocol

AI coding agents MUST actively use Sila's 9-verb continuity toolchain during development:

1. **Instant Pre-Search (` + "`sila recall \"<symptom or error>\"`" + `)**:
   Before guessing, hallucinating, or initiating expensive external web searches, agents MUST run ` + "`sila recall \"<symptom or error>\"`" + ` to check if the problem was previously solved in this or any registered project.
2. **Upstream Specification Grounding (` + "`sila doc search <query>`" + `)**:
   Before writing integrations, scaffolding architectures, or changing external dependencies, agents MUST query ` + "`sila doc search <query>`" + ` to ground decisions in authoritative local docs.
3. **Durable Invariants Capture (` + "`sila memo \"<directive>\" --files=...`" + `)**:
   When an architectural boundary, policy invariant, or non-negotiable rule is decided, agents MUST persist it via ` + "`sila memo \"<directive>\" --files=...`" + `.
4. **Empirical Bug-Fix Recording (` + "`sila lesson \"<symptom>\" --cause=\"...\" --fix=\"...\" --files=...`" + `)**:
   When resolving a non-trivial bug, race condition, or edge-case failure, agents MUST record a structured lesson via ` + "`sila lesson \"<symptom>\" --cause=\"...\" --fix=\"...\" --files=...`" + `.
5. **Architectural Pattern Recording (` + "`sila case \"<intent>\" --approach=\"...\" --invariant=\"...\" --files=...`" + `)**:
   When introducing a structural design pattern, module seam, or system abstraction, agents MUST record a case via ` + "`sila case \"<intent>\" --approach=\"...\" --invariant=\"...\" --files=...`" + `.
6. **Cryptographic Grounding Verification (` + "`sila facts verify`" + `)**:
   Before declaring a task or goal complete, agents MUST run ` + "`sila facts verify`" + ` to ensure all referenced files and dependencies exist on disk.
7. **Context Priming (` + "`sila prime`" + `)**:
   At the start of every session or task, agents run ` + "`sila prime`" + ` to load active context, ongoing goals, and architectural constraints.
8. **Deterministic Quality Gate (` + "`sila gate`" + `)**:
   Before committing, agents MUST run ` + "`sila gate`" + ` (or ` + "`sila gate --pre-commit`" + `) to execute automated deterministic sentries, race detection, and style checks.
9. **Structured Session Handoff (` + "`sila handoff \"<summary>\" --open=\"...\"`" + `)**:
   At the end of every work session, agents MUST persist progress, unblocked next steps, and open questions via ` + "`sila handoff \"<summary>\" --open=\"...\"`" + `.

` + SpecialistRoutingSection
}

// CanonicalPrinciplesBlock returns the canonical Principles Pack markdown:
// the five constitution layers, the 9-verb memory loop, and specialist
// capability routing, without managed-block delimiters.
func CanonicalPrinciplesBlock() string {
	return DefaultPrinciplesPack("polyglot").RenderMarkdown()
}

// StackRules returns stack-specific rules tailored to the target technology.
func StackRules(stack string) []string {
	norm := strings.ToLower(strings.TrimSpace(stack))
	switch norm {
	case "go", "golang":
		return []string{
			"1. Code Review & Hygiene: maintain pure Go standard library with zero third-party dependencies, Pure-Go CGO-Free Portability with CGO_ENABLED=0, and hard file ceilings under 1,000 LOC.",
			"2. Goroutine & Context Safety: require context.Context on all background routines; declare defer cancel() immediately after timeout context creation; ensure goroutine leak prevention.",
			"3. Goal Scaffolding: break findings into discrete single-outcome packages under goals/<slug>/ with goal.md, facts.md, plan.md, and explicit dependencies.",
			"4. Test-Driven Implementation: claim goal locks before editing; write table-driven tests verifying both acceptance assertions and negative rejection paths.",
			"5. Quality Gates & Race Detector Discipline: verify changes locally with linter scripts and full race detection ('go test -race ./...') with zero tolerance for data races.",
			"6. Attributed Commits: use semantic prefixes containing the goal slug: feat(scope): desc [goal: <slug>]; run sila goals audit to certify receipts.",
			"7. Git Branch Discipline: develop on dedicated feature branches; never push unprompted to remotes; re-verify race tests on merged main.",
			"8. Security & Trust Boundaries: validate all CLI flags and stdin readers; enforce UID ownership; use mode 0600 on sensitive temporary storage.",
			"9. Release Lifecycle & Value Changelog: implement a 24-hour cached daily version check, offer direct upgrades, provide a human-readable benefits-focused changelog, and credit contributors.",
		}
	case "ts", "typescript", "cloudflare", "worker":
		return []string{
			"1. Monorepo Isolation: isolate apps/{web,api} and packages/{db,types} with separate tsconfig boundaries to prevent DOM and Worker runtime type pollution.",
			"2. D1 Query Batching: batch edge database operations via db.batch() to avoid round-trip latency and inflated row ops.",
			"3. V8 Isolate CPU Limits: respect Cloudflare Worker 50ms/100ms CPU execution limits; offload heavy background work to ctx.waitUntil() or Cloudflare Queues.",
			"4. Secret Hygiene: store secrets and tokens in .env files; never use .dev.vars or hardcode secrets in source code.",
			"5. Toolchain Management: manage Wrangler via mise ('mise use wrangler@latest') rather than package managers to avoid delayed release lags.",
			"6. Vitest Storage Sandboxes: use @cloudflare/vitest-plugin with ephemeral D1 schemas to ensure clean D1/KV storage isolation and prevent cross-test state leakage.",
		}
	case "astro":
		return []string{
			"Static-First Island Architecture: Favor static rendering (SSG) with targeted client islands (client:load, client:visible).",
			"CSS Logical Properties: Enforce margin-inline and padding-inline for RTL/LTR layout resilience.",
			"Type-Safe Content Collections: Use getCollection() with Zod schemas for content validation.",
		}
	case "node", "javascript", "js":
		return []string{
			"Package Manager Discipline: Adhere to project lockfile (pnpm, aube, npm); never commit conflicting lockfiles.",
			"Modern ECMAScript: Use ES modules (import/export), async/await, and structured error handling.",
			"Process Hygiene: Clean up child processes, listeners, and background timers before shutdown or test exit.",
		}
	case "python", "py":
		return []string{
			"Type Hinting & Mypy: Full PEP 484 type annotations on all function signatures; enforce strict typing.",
			"Pure Functions: Prefer dataclasses, pydantic frozen models, and pure transformations over deep mutable class hierarchies.",
			"Resource Management: Always use context managers ('with' statements) for file and network handles.",
		}
	case "rust", "rs":
		return []string{
			"Zero-Cost Abstractions: Leverage Rust's borrow checker and lifetimes; avoid unnecessary clones or Rc/Arc where references suffice.",
			"Safe Concurrency: Enforce Send/Sync invariants; avoid unsafe blocks unless wrapped in verified safe abstractions.",
			"Result/Option Ergonomics: Propagate errors via '?' operator and custom thiserror/anyhow types without unwrap() in production.",
		}
	default:
		return []string{
			"Universal Simplicity: Apply KISS, CUPID, Pragmatic SOLID, FP, and Epistemic Grounding across all languages.",
		}
	}
}

// DefaultPrinciplesPack creates a PrinciplesPack populated with the 5 canonical
// layers, the 9-verb memory loop, and stack-tailored rules.
func DefaultPrinciplesPack(stack string) *PrinciplesPack {
	normStack := strings.ToLower(strings.TrimSpace(stack))
	if normStack == "" {
		normStack = "polyglot"
	}
	return &PrinciplesPack{
		Layers:     CanonicalConstitutionLayers(),
		MemoryLoop: CanonicalMemoryLoop(),
		TechStack:  normStack,
	}
}

// RenderMarkdown serializes the PrinciplesPack into Markdown format.
func (p *PrinciplesPack) RenderMarkdown() string {
	var sb strings.Builder

	sb.WriteString("# Sila Universal Principles Pack & Engineering Constitution\n\n")

	for i, l := range p.Layers {
		sb.WriteString(fmt.Sprintf("## %d. %s\n\n", i+1, l.Name))
		sb.WriteString(fmt.Sprintf("**Philosophy**: %s\n\n", l.Philosophy))
		for _, inv := range l.Invariants {
			sb.WriteString(fmt.Sprintf("- %s\n", inv))
		}
		if l.AntiPattern != "" {
			sb.WriteString(fmt.Sprintf("- *Anti-Pattern to Avoid*: %s\n", l.AntiPattern))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("---\n\n")
	sb.WriteString(strings.TrimSpace(p.MemoryLoop))
	sb.WriteString("\n\n")

	rules := StackRules(p.TechStack)
	if len(p.CustomRules) > 0 {
		rules = append(rules, p.CustomRules...)
	}

	if len(rules) > 0 {
		sb.WriteString("---\n\n")
		sb.WriteString(fmt.Sprintf("## Stack-Specific Invariants (%s)\n\n", strings.Title(p.TechStack)))
		for _, r := range rules {
			sb.WriteString(fmt.Sprintf("- %s\n", r))
		}
		sb.WriteString("\n")
	}

	return strings.TrimSpace(sb.String())
}

// RenderDelimited formats the principles markdown inside Sila's managed block delimiters.
func (p *PrinciplesPack) RenderDelimited() string {
	return PrinciplesBlockBegin + "\n" + p.RenderMarkdown() + "\n" + PrinciplesBlockEnd
}

// ExtractPrinciplesBlock extracts the principles content if present between delimiters.
func ExtractPrinciplesBlock(content string) (string, bool) {
	begin := strings.Index(content, PrinciplesBlockBegin)
	if begin < 0 {
		return "", false
	}
	end := strings.Index(content[begin:], PrinciplesBlockEnd)
	if end < 0 {
		return "", false
	}
	inner := content[begin+len(PrinciplesBlockBegin) : begin+end]
	return strings.TrimSpace(inner), true
}

// StripPrinciplesBlock removes any principles managed block and surrounding whitespace gaps.
func StripPrinciplesBlock(content string) string {
	begin := strings.Index(content, PrinciplesBlockBegin)
	if begin < 0 {
		return content
	}
	end := strings.Index(content[begin:], PrinciplesBlockEnd)
	if end < 0 {
		return content
	}
	endPos := begin + end + len(PrinciplesBlockEnd)
	before := strings.TrimRight(content[:begin], "\n")
	after := strings.TrimLeft(content[endPos:], "\n")
	if before == "" {
		return after
	}
	if after == "" {
		return before
	}
	return before + "\n\n" + after
}

// InjectPrinciples updates or inserts the principles block inside content, preserving
// all existing content outside the delimiters verbatim.
func InjectPrinciples(existing, renderedBody string) string {
	trimmedBody := strings.TrimSpace(renderedBody)
	block := PrinciplesBlockBegin + "\n" + trimmedBody + "\n" + PrinciplesBlockEnd

	beginIdx := strings.Index(existing, PrinciplesBlockBegin)
	if beginIdx >= 0 {
		endIdx := strings.Index(existing[beginIdx:], PrinciplesBlockEnd)
		if endIdx >= 0 {
			endPos := beginIdx + endIdx + len(PrinciplesBlockEnd)
			before := strings.TrimRight(existing[:beginIdx], "\n")
			after := strings.TrimLeft(existing[endPos:], "\n")
			if before == "" && after == "" {
				return block + "\n"
			}
			if before == "" {
				return block + "\n\n" + after
			}
			if after == "" {
				return before + "\n\n" + block + "\n"
			}
			return before + "\n\n" + block + "\n\n" + after
		}
	}

	if strings.TrimSpace(existing) == "" {
		return block + "\n"
	}
	return strings.TrimRight(existing, "\n") + "\n\n" + block + "\n"
}
