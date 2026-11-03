package learn

import (
	"strings"
	"time"
)

// Standard domain taxonomy for operational learnings across engineering ecosystems.
const (
	DomainGo         = "golang"
	DomainTypeScript = "typescript"
	DomainCloudflare = "cloudflare"
	DomainD1         = "d1"
	DomainDevOps     = "devops"
	DomainCICD       = "ci/cd"
	DomainSecurity   = "security"
	DomainTesting    = "testing"
	DomainA11y       = "a11y"
	DomainGeneral    = "general"
)

// AllDomains enumerates the canonical operational learning domains in priority order.
var AllDomains = []string{
	DomainGo,
	DomainTypeScript,
	DomainCloudflare,
	DomainD1,
	DomainDevOps,
	DomainCICD,
	DomainSecurity,
	DomainTesting,
	DomainA11y,
	DomainGeneral,
}

// LearningKind distinguishes the record shape rendered for an operational
// learning. A case is not a lesson: its fields are intent/approach/invariant,
// not symptom/cause/fix.
type LearningKind string

const (
	KindLesson   LearningKind = "lesson"
	KindCase     LearningKind = "case"
	KindMemo     LearningKind = "memo"
	KindDecision LearningKind = "decision"
)

// OperationalLearning represents a battle-tested engineering postmortem,
// architectural discovery, or invariant distilled across agent sessions.
type OperationalLearning struct {
	ID                string       `json:"id"`
	ProjectID         string       `json:"project_id"`
	Kind              LearningKind `json:"kind,omitempty"`
	Domain            string       `json:"domain"`
	TechStack         string       `json:"tech_stack"`
	Title             string       `json:"title"`
	Symptom           string       `json:"symptom,omitempty"`
	RootCause         string       `json:"root_cause,omitempty"`
	PivotalFix        string       `json:"pivotal_fix,omitempty"`
	Intent            string       `json:"intent,omitempty"`
	Approach          string       `json:"approach,omitempty"`
	Invariant         string       `json:"invariant,omitempty"`
	Directive         string       `json:"directive,omitempty"`
	GuardrailRule     string       `json:"guardrail_rule"`
	Tags              []string     `json:"tags,omitempty"`
	SourceOccurrences int          `json:"source_occurrences,omitempty"`
	Confidence        string       `json:"confidence"`
	CreatedAt         time.Time    `json:"created_at"`
}

// EffectiveKind returns the explicit kind, defaulting legacy lesson-shaped
// records (which predate kind tracking) to a lesson.
func (l OperationalLearning) EffectiveKind() LearningKind {
	switch l.Kind {
	case KindCase, KindMemo, KindDecision:
		return l.Kind
	default:
		return KindLesson
	}
}

// DirectiveText returns the full directive for memo/decision records, falling
// back to the display title for legacy rows.
func (l OperationalLearning) DirectiveText() string {
	if d := strings.TrimSpace(l.Directive); d != "" {
		return d
	}
	return strings.TrimSpace(l.Title)
}

// DomainDisplayName returns a human-friendly title for a domain.
func DomainDisplayName(domain string) string {
	switch strings.ToLower(domain) {
	case DomainGo:
		return "Golang Engineering Disciplines"
	case DomainTypeScript:
		return "TypeScript & Modern Frontend"
	case DomainCloudflare:
		return "Cloudflare Workers & Edge Compute"
	case DomainD1:
		return "Cloudflare D1 & SQLite Data Tier"
	case DomainDevOps:
		return "DevOps, Subshells & Linux Systems"
	case DomainCICD:
		return "CI/CD, Worktrees & Quality Gates"
	case DomainSecurity:
		return "Security, Redaction & Data Sovereignty"
	case DomainTesting:
		return "Testing, Flakiness & Storage Sandboxes"
	case DomainA11y:
		return "Accessibility (WCAG 2.1 AA) & Design Systems"
	default:
		return "Universal Architectural Patterns"
	}
}

// DomainDescription returns a concise overview of the domain's scope.
func DomainDescription(domain string) string {
	switch strings.ToLower(domain) {
	case DomainGo:
		return "Goroutine leak sentries, SQLite CGO-free invariants, pure-Go AST parsers, and race detector discipline."
	case DomainTypeScript:
		return "ESM modules, type contracts, build configurations, and bundling reliability."
	case DomainCloudflare:
		return "V8 isolates, CPU budget optimizations, waitUntil deferrals, miniflare bindings, and edge routing."
	case DomainD1:
		return "D1 SQLite transactions, schema migrations, batch queries, and per-test storage isolation."
	case DomainDevOps:
		return "Process supervision, subshell PATH resilience, mount boundaries, and file descriptor management."
	case DomainCICD:
		return "Local-first quality gates, fast-path pre-commit sentries, isolated worktrees, and reproducible binaries."
	case DomainSecurity:
		return "Local secret redaction sentries, synthetic test fixture sanitization, private repo isolation, and digital data sovereignty."
	case DomainTesting:
		return "Cross-test storage sandbox isolation, table-driven test suites, race detection, and order-independent flakiness elimination."
	case DomainA11y:
		return "Semantic HTML, keyboard traps, ARIA compliance, color contrast, and high-visibility terminal TrueColor themes."
	default:
		return "Cross-cutting invariants, radical simplicity, atomic two-stage commits, and hub-and-spoke rule hierarchies."
	}
}

// FormatOperationalLearning renders the canonical single-line representation
// stored in Sila's SQLite facts ledger under kind='operational_learning'.
func FormatOperationalLearning(l OperationalLearning) string {
	title := strings.TrimSpace(l.Title)
	if title == "" {
		title = strings.TrimSpace(l.Intent)
	}
	if title == "" {
		title = strings.TrimSpace(l.Directive)
	}
	if title == "" {
		title = strings.TrimSpace(l.Symptom)
	}
	parts := []string{"OperationalLearning: " + title}

	if l.Kind != "" {
		parts = append(parts, "kind: "+string(l.Kind))
	}
	if l.Domain != "" {
		parts = append(parts, "domain: "+strings.TrimSpace(l.Domain))
	}
	if l.TechStack != "" {
		parts = append(parts, "stack: "+strings.TrimSpace(l.TechStack))
	}
	if l.Symptom != "" {
		parts = append(parts, "symptom: "+strings.TrimSpace(l.Symptom))
	}
	if l.RootCause != "" {
		parts = append(parts, "cause: "+strings.TrimSpace(l.RootCause))
	}
	if l.PivotalFix != "" {
		parts = append(parts, "fix: "+strings.TrimSpace(l.PivotalFix))
	}
	if l.Intent != "" {
		parts = append(parts, "intent: "+strings.TrimSpace(l.Intent))
	}
	if l.Approach != "" {
		parts = append(parts, "approach: "+strings.TrimSpace(l.Approach))
	}
	if l.Invariant != "" {
		parts = append(parts, "invariant: "+strings.TrimSpace(l.Invariant))
	}
	if l.Directive != "" {
		parts = append(parts, "directive: "+strings.TrimSpace(l.Directive))
	}
	if l.GuardrailRule != "" {
		parts = append(parts, "rule: "+strings.TrimSpace(l.GuardrailRule))
	}
	if len(l.Tags) > 0 {
		parts = append(parts, "tags: "+strings.Join(l.Tags, ","))
	}
	return strings.Join(parts, " | ")
}

// ParseOperationalLearning parses a canonical operational learning line into its structured fields.
func ParseOperationalLearning(content string) (*OperationalLearning, bool) {
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "OperationalLearning: ") {
		return nil, false
	}

	body := strings.TrimPrefix(trimmed, "OperationalLearning: ")
	l := &OperationalLearning{
		Kind:       KindLesson,
		Domain:     DomainGeneral,
		Confidence: "high",
	}

	parts := strings.Split(body, " | ")
	if len(parts) > 0 {
		l.Title = strings.TrimSpace(parts[0])
	}

	for _, seg := range parts[1:] {
		seg = strings.TrimSpace(seg)
		lower := strings.ToLower(seg)
		switch {
		case strings.HasPrefix(lower, "kind:"):
			switch LearningKind(strings.ToLower(strings.TrimSpace(seg[len("kind:"):]))) {
			case KindCase:
				l.Kind = KindCase
			case KindMemo:
				l.Kind = KindMemo
			case KindDecision:
				l.Kind = KindDecision
			default:
				l.Kind = KindLesson
			}
		case strings.HasPrefix(lower, "domain:"):
			l.Domain = strings.TrimSpace(seg[len("domain:"):])
		case strings.HasPrefix(lower, "stack:"):
			l.TechStack = strings.TrimSpace(seg[len("stack:"):])
		case strings.HasPrefix(lower, "symptom:"):
			l.Symptom = strings.TrimSpace(seg[len("symptom:"):])
		case strings.HasPrefix(lower, "cause:"):
			l.RootCause = strings.TrimSpace(seg[len("cause:"):])
		case strings.HasPrefix(lower, "fix:"):
			l.PivotalFix = strings.TrimSpace(seg[len("fix:"):])
		case strings.HasPrefix(lower, "intent:"):
			l.Intent = strings.TrimSpace(seg[len("intent:"):])
		case strings.HasPrefix(lower, "approach:"):
			l.Approach = strings.TrimSpace(seg[len("approach:"):])
		case strings.HasPrefix(lower, "invariant:"):
			l.Invariant = strings.TrimSpace(seg[len("invariant:"):])
		case strings.HasPrefix(lower, "directive:"):
			l.Directive = strings.TrimSpace(seg[len("directive:"):])
		case strings.HasPrefix(lower, "rule:"):
			l.GuardrailRule = strings.TrimSpace(seg[len("rule:"):])
		case strings.HasPrefix(lower, "tags:"):
			rawTags := strings.TrimSpace(seg[len("tags:"):])
			for _, t := range strings.Split(rawTags, ",") {
				if ct := strings.TrimSpace(t); ct != "" {
					l.Tags = append(l.Tags, ct)
				}
			}
		}
	}

	// Only lesson-shaped records use the symptom/cause/fix template. Cases and
	// directives must not gain a synthesized symptom.
	if l.Kind == KindLesson && l.Symptom == "" && l.Title != "" {
		l.Symptom = l.Title
	}

	return l, true
}
