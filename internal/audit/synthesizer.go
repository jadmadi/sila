package audit

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// SynthesizedProposal represents an arbitrated finding ready for Sila Goal conversion.
type SynthesizedProposal struct {
	Slug                string   `json:"slug"`
	Title               string   `json:"title"`
	Category            string   `json:"category"`
	Severity            Severity `json:"severity"`
	PriorityTier        string   `json:"priority_tier"` // immediate, roadmap, nice-to-have
	Shape               string   `json:"shape"`         // ship, fix, spike
	Confidence          string   `json:"confidence"`
	PersonaAttributions []string `json:"persona_attributions"`
	Summary             string   `json:"summary"`
	Evidence            []string `json:"evidence"`
	ActionSteps         []string `json:"action_steps"`
	GoalMD              string   `json:"goal_md"`
	FactsMD             string   `json:"facts_md"`
	PlanMD              string   `json:"plan_md"`
}

var nonAlphaRegex = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(text string) string {
	lower := strings.ToLower(text)
	cleaned := nonAlphaRegex.ReplaceAllString(lower, "-")
	trimmed := strings.Trim(cleaned, "-")
	if len(trimmed) > 40 {
		trimmed = trimmed[:40]
		trimmed = strings.TrimRight(trimmed, "-")
	}
	return trimmed
}

// SynthesizeConsensus arbitrates across all evaluation cards to create unified proposals.
func SynthesizeConsensus(session *AuditSession) []SynthesizedProposal {
	type cluster struct {
		title       string
		category    string
		severity    Severity
		personas    map[string]bool
		evidence    []string
		actions     []string
		description string
	}

	clusters := make([]*cluster, 0)

	findCluster := func(category string, keyword string) *cluster {
		lowerKw := strings.ToLower(keyword)
		for _, c := range clusters {
			if strings.EqualFold(c.category, category) || strings.Contains(strings.ToLower(c.title), lowerKw) {
				return c
			}
		}
		return nil
	}

	for _, card := range session.Cards {
		for _, f := range card.Findings {
			c := findCluster(f.Category, f.Description)
			if c == nil {
				c = &cluster{
					title:       f.Description,
					category:    f.Category,
					severity:    f.Severity,
					personas:    make(map[string]bool),
					evidence:    make([]string, 0),
					actions:     make([]string, 0),
					description: f.Description,
				}
				clusters = append(clusters, c)
			}

			c.personas[card.PersonaID] = true
			if f.Evidence != "" {
				c.evidence = append(c.evidence, fmt.Sprintf("[%s] %s", card.PersonaID, f.Evidence))
			}
			if f.Severity == SeverityCritical || (f.Severity == SeverityHigh && c.severity != SeverityCritical) {
				c.severity = f.Severity
			}
		}

		for _, s := range card.Suggestions {
			c := findCluster(s.Title, s.Action)
			if c != nil {
				c.actions = append(c.actions, s.Action)
			}
		}
	}

	// Sort clusters by severity
	severityWeight := map[Severity]int{
		SeverityCritical: 5,
		SeverityHigh:     4,
		SeverityMedium:   3,
		SeverityLow:      2,
		SeverityNit:      1,
	}

	sort.Slice(clusters, func(i, j int) bool {
		return severityWeight[clusters[i].severity] > severityWeight[clusters[j].severity]
	})

	proposals := make([]SynthesizedProposal, 0, len(clusters))

	for i, c := range clusters {
		personas := make([]string, 0, len(c.personas))
		for p := range c.personas {
			personas = append(personas, p)
		}
		sort.Strings(personas)

		tier := "roadmap"
		shape := "ship"
		if c.severity == SeverityCritical || c.severity == SeverityHigh {
			tier = "immediate"
			shape = "fix"
		} else if c.severity == SeverityNit {
			tier = "nice-to-have"
		}

		slug := fmt.Sprintf("audit-%s", slugify(c.category+"-"+c.title))
		if slug == "audit-" {
			slug = fmt.Sprintf("audit-proposal-%d", i+1)
		}

		title := c.title
		if len(title) > 60 {
			title = title[:60] + "..."
		}

		goalMD := generateGoalMarkdown(slug, title, c.description, tier, shape, c.evidence, c.actions, personas, session.Target.URL)
		factsMD := generateFactsMarkdown(slug, title, session.Target.URL, c.evidence, personas)
		planMD := generatePlanMarkdown(slug, title, c.actions)

		proposals = append(proposals, SynthesizedProposal{
			Slug:                slug,
			Title:               title,
			Category:            c.category,
			Severity:            c.severity,
			PriorityTier:        tier,
			Shape:               shape,
			Confidence:          "high",
			PersonaAttributions: personas,
			Summary:             c.description,
			Evidence:            c.evidence,
			ActionSteps:         c.actions,
			GoalMD:              goalMD,
			FactsMD:             factsMD,
			PlanMD:              planMD,
		})
	}

	return proposals
}

func generateGoalMarkdown(slug, title, description, tier, shape string, evidence, actions, personas []string, url string) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# Goal: %s\n\n", title)
	b.WriteString("## Goal Description\n")
	fmt.Fprintf(&b, "%s\n\n", description)
	fmt.Fprintf(&b, "Discovered during autonomous multi-persona groupthink audit of `%s`.\n", url)
	fmt.Fprintf(&b, "Attributed consensus: %s.\n\n", strings.Join(personas, ", "))

	b.WriteString("## Dependencies & Execution Order\n")
	b.WriteString("- **Mode**: Independent (disjoint, parallelizable)\n")
	b.WriteString("- **Depends On**: none\n")
	b.WriteString("- **Sequence**: -\n")
	fmt.Fprintf(&b, "- **Shape**: %s\n", shape)
	fmt.Fprintf(&b, "- **Tier**: %s\n\n", tier)

	b.WriteString("## Files to Touch\n")
	b.WriteString("- (Identified by engineer during implementation)\n\n")

	b.WriteString("## References\n")
	b.WriteString("- **Shared Understanding & Fact Sheet**: [`facts.md`](facts.md)\n")
	b.WriteString("- **Execution Plan**: [`plan.md`](plan.md)\n\n")

	b.WriteString("## Done Condition\n")
	for i, a := range actions {
		fmt.Fprintf(&b, "%d. %s\n", i+1, a)
	}
	if len(actions) == 0 {
		b.WriteString("1. The identified defect or improvement is resolved with automated tests and zero visual regression.\n")
	}
	b.WriteString("2. All unit and integration tests pass cleanly with `go test -race ./...` or framework equivalent.\n")
	b.WriteString("3. Zero architectural regressions; definitions of done satisfied.\n\n")

	b.WriteString("## Verification & Self-Improvement Rubric\n")
	b.WriteString("- [ ] Changes verified against Refactoring UI atomic design heuristics\n")
	b.WriteString("- [ ] Zero arbitrary pixel values; margins and padding conform to 4pt/8pt spacing scale\n")
	b.WriteString("- [ ] Contrast verified against WCAG 2.2 AA (min 4.5:1 text, 3:1 components)\n")
	b.WriteString("- [ ] Self-improvement lesson recorded via `sila lesson` upon completion\n")

	return b.String()
}

func generateFactsMarkdown(slug, title, url string, evidence, personas []string) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# Facts: %s\n\n", title)
	b.WriteString("## Context & Audit Findings\n")
	fmt.Fprintf(&b, "- Target Application URL: `%s`\n", url)
	fmt.Fprintf(&b, "- Contributing Personas: %s\n\n", strings.Join(personas, ", "))

	b.WriteString("## Concrete Evidence\n")
	for _, ev := range evidence {
		fmt.Fprintf(&b, "- %s\n", ev)
	}
	if len(evidence) == 0 {
		b.WriteString("- Visual and functional inspection revealed actionable enhancement opportunities.\n")
	}

	b.WriteString("\n## Upstream Standards & Design Heuristics\n")
	b.WriteString("- **Design Principles**: Refactoring UI (Adam Wathan & Steve Schoger)\n")
	b.WriteString("- **Project Skills**: `.agents/skills/`\n")
	b.WriteString("- **Phase Invariant**: Phase 4 DOM Ergonomics & Visual Craft\n")

	return b.String()
}

func generatePlanMarkdown(slug, title string, actions []string) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# Plan: %s\n\n", title)
	b.WriteString("## Execution Steps\n")
	if len(actions) > 0 {
		for i, a := range actions {
			fmt.Fprintf(&b, "- [ ] **Phase %d**: %s\n", i+1, a)
		}
		fmt.Fprintf(&b, "- [ ] **Phase %d — Verification & Attribution**: Run tests and commit with `(goals/%s/goal.md)`\n", len(actions)+1, slug)
	} else {
		b.WriteString("- [ ] **Phase 1 — Discovery & Root Cause**: Inspect relevant components and trace defect.\n")
		b.WriteString("- [ ] **Phase 2 — Implementation**: Apply required changes and styling refinements.\n")
		fmt.Fprintf(&b, "- [ ] **Phase 3 — Verification & Attribution**: Run tests and commit with `(goals/%s/goal.md)`\n", slug)
	}

	return b.String()
}

// FormatLessonFeedback generates lesson strings for approved or rejected proposals.
func FormatLessonFeedback(p SynthesizedProposal, approved bool, reason string) (symptom, cause, fix string) {
	if approved {
		symptom = fmt.Sprintf("Audit proposal accepted: %s", p.Title)
		cause = fmt.Sprintf("High-consensus groupthink finding in %s domain validated by operator", p.Category)
		fix = fmt.Sprintf("Register Sila Goal %s for implementation", p.Slug)
		return
	}

	symptom = fmt.Sprintf("Audit proposal rejected: %s", p.Title)
	cause = fmt.Sprintf("Finding rejected by operator. Reason: %s", reason)
	fix = fmt.Sprintf("Record negative lesson against personas [%s] to avoid false positive proposals", strings.Join(p.PersonaAttributions, ", "))
	return
}
