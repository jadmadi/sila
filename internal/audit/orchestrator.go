package audit

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/persona"
)

// Severity indicates finding criticality.
type Severity string

const (
	// SeverityCritical marks an urgent blocker or security vulnerability.
	SeverityCritical Severity = "critical"
	// SeverityHigh marks a significant architectural or operational defect.
	SeverityHigh Severity = "high"
	// SeverityMedium marks a noticeable defect or standard divergence.
	SeverityMedium Severity = "medium"
	// SeverityLow marks a minor usability issue or minor non-compliance.
	SeverityLow Severity = "low"
	// SeverityNit marks a minor cosmetic, styling, or documentation suggestion.
	SeverityNit Severity = "nit"
)

// AuditTarget specifies the web application to evaluate.
type AuditTarget struct {
	URL          string   `json:"url"`
	AppName      string   `json:"app_name"`
	ScopePages   []string `json:"scope_pages"`
	ExtraContext string   `json:"extra_context"`
	AuthNotes    string   `json:"auth_notes"`
}

// Finding represents a concrete issue or opportunity discovered by a persona.
type Finding struct {
	ID          string   `json:"id"`
	PersonaID   string   `json:"persona_id"`
	Category    string   `json:"category"`
	Severity    Severity `json:"severity"`
	PageURL     string   `json:"page_url"`
	Evidence    string   `json:"evidence"`
	Description string   `json:"description"`
	Impact      string   `json:"impact"`
}

// Suggestion represents an actionable improvement proposal.
type Suggestion struct {
	Title       string `json:"title"`
	PersonaID   string `json:"persona_id"`
	Action      string `json:"action"`
	Priority    string `json:"priority"`
	ExpectedROI string `json:"expected_roi"`
}

// EvaluationCard stores a persona's complete audit report.
type EvaluationCard struct {
	PersonaID              string        `json:"persona_id"`
	PersonaName            string        `json:"persona_name"`
	Domain                 string        `json:"domain"`
	Status                 string        `json:"status"` // pass, warning, critical
	ExecutiveSummary       string        `json:"executive_summary"`
	Findings               []Finding     `json:"findings"`
	Suggestions            []Suggestion  `json:"suggestions"`
	SkillsUsed             []string      `json:"skills_used"`
	ExecutionDuration      time.Duration `json:"execution_duration"`
	NegativeLessonsApplied []string      `json:"negative_lessons_applied"`
}

// AuditSession represents the full groupthink audit run.
type AuditSession struct {
	ID           string            `json:"id"`
	Target       AuditTarget       `json:"target"`
	Squad        []persona.Persona `json:"squad"`
	Cards        []EvaluationCard  `json:"cards"`
	GapsDetected []PersonaProposal `json:"gaps_detected"`
	CreatedAt    time.Time         `json:"created_at"`
}

// GeneratePersonaPrompt generates a prompt tailored to an individual persona.
func GeneratePersonaPrompt(target AuditTarget, p persona.Persona) string {
	var b strings.Builder

	fmt.Fprintf(&b, "### PERSONA HAT: %s (%s)\n\n", p.Name, p.ID)
	fmt.Fprintf(&b, "**System Directives**:\n%s\n\n", p.SystemPrompt)

	if len(p.SkillsInvoked) > 0 {
		fmt.Fprintf(&b, "**Required Skills to Dispatch**:\n")
		for _, s := range p.SkillsInvoked {
			fmt.Fprintf(&b, "- `%s`\n", s)
		}
		b.WriteString("\n")
	}

	if len(p.NegativeLessons) > 0 {
		fmt.Fprintf(&b, "**Negative Memory (Do NOT Repeat These Mistakes)**:\n")
		for _, l := range p.NegativeLessons {
			fmt.Fprintf(&b, "- ⚠️ %s\n", l)
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "**Evaluation Rubric**:\n%s\n\n", p.EvaluationRubric)

	fmt.Fprintf(&b, "**Audit Target**:\n")
	fmt.Fprintf(&b, "- Base URL: %s\n", target.URL)
	if target.AppName != "" {
		fmt.Fprintf(&b, "- Application: %s\n", target.AppName)
	}
	if len(target.ScopePages) > 0 {
		fmt.Fprintf(&b, "- Sub-pages Scope:\n")
		for _, page := range target.ScopePages {
			fmt.Fprintf(&b, "  - %s\n", page)
		}
	}
	if target.AuthNotes != "" {
		fmt.Fprintf(&b, "- Auth Protocol: %s\n", target.AuthNotes)
	}
	if target.ExtraContext != "" {
		fmt.Fprintf(&b, "- Context: %s\n", target.ExtraContext)
	}

	b.WriteString("\n**Output Requirement**:\n")
	b.WriteString("Return structured JSON matching the EvaluationCard schema with concrete DOM/visual evidence.\n")

	return b.String()
}

// AssembleMasterGroupthinkPrompt creates the complete multi-hat orchestration document.
func AssembleMasterGroupthinkPrompt(target AuditTarget, squad []persona.Persona, gaps []PersonaProposal) string {
	var b strings.Builder

	b.WriteString("# Autonomous Multi-Persona Groupthink Audit Protocol\n\n")
	fmt.Fprintf(&b, "Target: `%s` | Squad Size: %d personas | Timestamp: %s\n\n",
		target.URL, len(squad), time.Now().UTC().Format(time.RFC3339))

	b.WriteString("## Mission Overview\n")
	b.WriteString("Execute parallel, multi-perspective evaluations of the target application without group polarization. ")
	b.WriteString("Each persona operates under strict domain boundaries, invokes specific verification skills, ")
	b.WriteString("and generates evidenced findings before the Master Arbiter synthesizes consensus.\n\n")

	b.WriteString("## Active Squad Roster\n")
	for i, p := range squad {
		skillsStr := "prompt-only"
		if len(p.SkillsInvoked) > 0 {
			skillsStr = strings.Join(p.SkillsInvoked, ", ")
		}
		fmt.Fprintf(&b, "%d. **%s** (`%s`)\n   - Domain: `%s` | Skills: `%s` | Approval: `%s`\n",
			i+1, p.Name, p.ID, p.Domain, skillsStr, p.ApprovalRate)
	}
	b.WriteString("\n")

	if len(gaps) > 0 {
		b.WriteString("## Detected Domain Gaps (Suggested Personas)\n")
		for _, g := range gaps {
			fmt.Fprintf(&b, "- 💡 **%s** (`%s`): %s\n", g.Name, g.ID, g.Reason)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Persona Instructions\n")
	for _, p := range squad {
		b.WriteString("---\n")
		b.WriteString(GeneratePersonaPrompt(target, p))
	}

	return b.String()
}

// ExecuteMock generates a deterministic mock evaluation for dry-runs, tests, and CLI validation.
func ExecuteMock(ctx context.Context, target AuditTarget, squad []persona.Persona) (*AuditSession, error) {
	sessionID := fmt.Sprintf("audit-%d", time.Now().Unix())
	cards := make([]EvaluationCard, 0, len(squad))

	for _, p := range squad {
		card := EvaluationCard{
			PersonaID:              p.ID,
			PersonaName:            p.Name,
			Domain:                 p.Domain,
			Status:                 "warning",
			SkillsUsed:             p.SkillsInvoked,
			ExecutionDuration:      120 * time.Millisecond,
			NegativeLessonsApplied: p.NegativeLessons,
		}

		// Phase 4: Refactoring UI Evaluator for UI/UX personas
		if p.ID == "ui-craftsperson" || p.ID == "ui-craft" ||
			p.ID == "ux-information-architect" || p.ID == "dom-ergonomist" ||
			p.ID == "accessibility-sentinel" || p.ID == "a11y-sentry" ||
			p.ID == "first-time-user-advocate" || p.ID == "onboard-guide" ||
			p.Domain == "ui" {
			card = EvaluatePhase4Persona(target, p, target.ExtraContext)
			cards = append(cards, card)
			continue
		}

		switch p.Domain {
		case "access":
			card.ExecutiveSummary = "Accessible keyboard navigation mostly functional, but color contrast on primary CTA falls below WCAG AA."
			card.Findings = append(card.Findings, Finding{
				ID:          p.ID + "-f1",
				PersonaID:   p.ID,
				Category:    "Accessibility",
				Severity:    SeverityHigh,
				PageURL:     target.URL,
				Evidence:    "button.primary-cta background #3b82f6 on white text has 3.8:1 contrast ratio",
				Description: "Primary action button fails WCAG 2.2 AA 4.5:1 minimum contrast requirement.",
				Impact:      "Low-vision users will struggle to locate the main conversion trigger.",
			})
			card.Suggestions = append(card.Suggestions, Suggestion{
				Title:       "Darken Primary Button Background",
				PersonaID:   p.ID,
				Action:      "Adjust button color to #2563eb or darken text to meet 4.8:1 contrast ratio.",
				Priority:    "P1",
				ExpectedROI: "100% WCAG 2.2 AA compliance on primary view",
			})

		case "design":
			card.ExecutiveSummary = "Layout has pleasing typography, but card grid gutters lack consistent spacing on desktop viewports."
			card.Findings = append(card.Findings, Finding{
				ID:          p.ID + "-f1",
				PersonaID:   p.ID,
				Category:    "UI Craft",
				Severity:    SeverityMedium,
				PageURL:     target.URL,
				Evidence:    ".grid-cols-3 gap-3 vs .feature-card padding 24px",
				Description: "Horizontal margin asymmetry between left feature card and right feature card.",
				Impact:      "Subtle visual discord that undermines perceived polish and enterprise trust.",
			})
			card.Suggestions = append(card.Suggestions, Suggestion{
				Title:       "Harmonize 8pt Spacing Scale Across Card Grids",
				PersonaID:   p.ID,
				Action:      "Standardize on gap-6 (24px) across all container breakpoints.",
				Priority:    "P2",
				ExpectedROI: "Pixel-perfect visual harmony",
			})

		case "performance":
			card.ExecutiveSummary = "First Contentful Paint is swift, but large unoptimized hero graphic delays LCP."
			card.Findings = append(card.Findings, Finding{
				ID:          p.ID + "-f1",
				PersonaID:   p.ID,
				Category:    "Performance",
				Severity:    SeverityHigh,
				PageURL:     target.URL,
				Evidence:    "Hero PNG image size 2.8MB without WebP/AVIF format or explicit width/height",
				Description: "Unoptimized hero banner causes 2.9s Largest Contentful Paint (LCP) on simulated 4G.",
				Impact:      "High bounce rate on mobile cellular connections.",
			})
			card.Suggestions = append(card.Suggestions, Suggestion{
				Title:       "Convert Hero Image to Modern AVIF with Responsive Srcset",
				PersonaID:   p.ID,
				Action:      "Serve AVIF/WebP image below 200KB with priority fetch and preloaded link header.",
				Priority:    "P1",
				ExpectedROI: "Sub-1.2s LCP and improved Core Web Vitals score",
			})

		case "copy":
			card.ExecutiveSummary = "Headline is catchy, but body copy lacks a concrete value proposition or social proof."
			card.Findings = append(card.Findings, Finding{
				ID:          p.ID + "-f1",
				PersonaID:   p.ID,
				Category:    "Copywriting",
				Severity:    SeverityMedium,
				PageURL:     target.URL,
				Evidence:    "Subheading: 'Empowering seamless synergies across workflows'",
				Description: "Vague corporate jargon obscures what the software actually accomplishes in the first 5 seconds.",
				Impact:      "Visitor confusion leading to rapid tab abandonment.",
			})
			card.Suggestions = append(card.Suggestions, Suggestion{
				Title:       "Replace Synergies Jargon with Concrete Outcome Headline",
				PersonaID:   p.ID,
				Action:      "Rewrite subheading to: 'Sync autonomous AI agent memory across your team with zero configuration'.",
				Priority:    "P1",
				ExpectedROI: "+18% visitor-to-signup conversion",
			})

		default:
			card.ExecutiveSummary = fmt.Sprintf("Evaluated under %s domain perspective. No critical blockers detected.", p.Domain)
			card.Status = "pass"
		}

		cards = append(cards, card)
	}

	gaps := DetectGaps(squad, target.AppName+" "+target.ExtraContext)

	return &AuditSession{
		ID:           sessionID,
		Target:       target,
		Squad:        squad,
		Cards:        cards,
		GapsDetected: gaps,
		CreatedAt:    time.Now().UTC(),
	}, nil
}
