package audit

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/persona"
)

// PhaseSchedule represents an execution phase with its disjoint task boundary.
type PhaseSchedule struct {
	Phase       int               `json:"phase"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	TaskScope   string            `json:"task_scope"`
	Personas    []persona.Persona `json:"personas"`
}

// DomainCoverage details the presence of personas across canonical groups.
type DomainCoverage struct {
	GroupID  int      `json:"group_id"`
	Slug     string   `json:"slug"`
	Name     string   `json:"name"`
	Count    int      `json:"count"`
	Personas []string `json:"personas"`
	Status   string   `json:"status"` // "covered", "uncovered", "overrepresented"
}

// GoalInfo is a type alias for goals.Goal representing goal package metadata.
type GoalInfo = goals.Goal

// GoalCoverage details squad coverage for a roadmap goal.
type GoalCoverage struct {
	GoalSlug         string   `json:"goal_slug"`
	Title            string   `json:"title"`
	RequiredGroups   []string `json:"required_groups"`
	RequiredGroupIDs []int    `json:"required_group_ids,omitempty"`
	CoveringPersonas []string `json:"covering_personas"`
	MissingGroups    []string `json:"missing_groups,omitempty"`
	Status           string   `json:"status"` // "covered", "partially_covered", "uncovered"
}

// PersonaSuggestion represents an actionable squad adjustment recommendation.
type PersonaSuggestion struct {
	Action       string `json:"action"` // "add" or "remove"
	PersonaID    string `json:"persona_id"`
	PersonaNumID int    `json:"persona_num_id"`
	PersonaName  string `json:"persona_name"`
	Reason       string `json:"reason"`
}

// SquadAssessment summarizes squad balance, domain blindspots, and disjoint execution orders.
type SquadAssessment struct {
	TargetURL        string              `json:"target_url,omitempty"`
	SquadSize        int                 `json:"squad_size"`
	BalanceScore     int                 `json:"balance_score"`               // 0..100
	RoadmapAlignment int                 `json:"roadmap_alignment,omitempty"` // 0..100
	GoalsCoverage    []GoalCoverage      `json:"goals_coverage,omitempty"`
	DomainCoverages  []DomainCoverage    `json:"domain_coverages"`
	Blindspots       []string            `json:"blindspots"`
	Redundancies     []string            `json:"redundancies"`
	Suggestions      []PersonaSuggestion `json:"suggestions"`
	Schedule         []PhaseSchedule     `json:"schedule"`
}

// AssignPhase maps a persona to its deterministic disjoint execution phase (1..6).
func AssignPhase(p persona.Persona) int {
	switch p.ID {
	// Phase 1: Protocol & Network Recon (Passive external metadata, zero DOM interaction)
	case "agent-readiness-architect", "seo-discoverability-auditor", "telemetry-data-forensic", "competitor-recon-strategist":
		return 1

	// Phase 2: Content Density & Token Economics (Negotiation headers, llms.txt, machine auth manifests)
	case "content-negotiation-token-economist", "machine-identity-auth-counsel", "i18n-localization-architect":
		return 2

	// Phase 3: Core Architecture, Fullstack & Security (Runtime latency, API contracts, CORS/XSS, tests)
	case "senior-fullstack-engineer", "qa-automation-lead", "performance-sentinel", "security-compliance-sentry", "web3-contract-auditor", "mystery-auditor":
		return 3

	// Phase 4: DOM Ergonomics, UI & Visual Craft (Client visual rendering, WCAG contrast, keyboard focus, mobile gestures)
	case "ui-craftsperson", "ux-information-architect", "accessibility-sentinel", "keyboard-power-maximalist", "mobile-native-ergonomist", "arabic-waqf-craftsperson":
		return 4

	// Phase 5: Customer Psychology & Value Capture (Funnel friction, copywriting, churn risks, pricing objections, machine payments)
	case "conversion-copywriter", "growth-product-strategist", "enterprise-sales-investor", "first-time-user-advocate", "positive-champion-customer", "negative-churn-risk-customer", "agentic-commerce-auditor":
		return 5

	// Phase 6: Strategic Arbitration & Synthesis (Contrarian stress-testing, legal/HIPAA compliance, open-source governance)
	case "devils-advocate-contrarian", "visionary-dreamer", "epistemic-philosopher", "legal-compliance-counsel", "hipaa-healthcare-compliance", "community-open-source-steward":
		return 6

	default:
		// Fallback based on group ID
		switch p.GroupID {
		case 1:
			return 1
		case 2:
			return 3
		case 3:
			return 4
		case 4, 5, 6:
			return 5
		case 7:
			return 6
		default:
			return 4
		}
	}
}

// GetPhaseDetails returns human-readable metadata for a disjoint execution phase.
func GetPhaseDetails(phase int) (name, description, taskScope string) {
	switch phase {
	case 1:
		return "Protocol & Network Recon",
			"Passive network reconnaissance, schema discovery, and machine interoperability",
			"Read HTTP response headers, robots.txt, sitemaps, Open Graph tags, /.well-known/ server cards, and telemetry traces. Zero DOM interaction or state modification."
	case 2:
		return "Content Density & Token Economics",
			"Machine onboarding manifests, markdown negotiation, and prompt token budget auditing",
			"Audit Accept: text/markdown negotiation, x-markdown-tokens budgets, /llms.txt curation, /auth.md registration manifests, and bidirectional string fallbacks."
	case 3:
		return "Core Systems, Fullstack & Security",
			"Runtime architecture, bundle sizing, Core Web Vitals, API security, and test harnesses",
			"Inspect client bundle splits, LCP/CLS latency bottlenecks, API error envelopes, CORS/CSP policies, auth token handling, and automated test coverage."
	case 4:
		return "DOM Ergonomics, UI & Visual Craft",
			"Visual layout, 8pt spacing tokens, WCAG 2.2 AA contrast, and interactive focus trees",
			"Render client DOM tree, audit color contrast ratios on CTAs, inspect keyboard focus rings (tabindex), safe-area insets, and mobile 48x48dp touch targets."
	case 5:
		return "Customer Psychology & Value Capture",
			"Onboarding friction, conversion copy, churn risks, and commercial readiness",
			"Audit signup flow cognitive load, headline clarity, buyer objection handling, pricing transparency, and machine payment protocols (x402/MPP)."
	case 6:
		return "Strategic Arbitration & Governance",
			"Adversarial stress-testing, regulatory compliance, and cross-functional goal triage",
			"Cross-examine findings via devil's advocate, enforce GDPR/HIPAA/liability guardrails, evaluate open-source license integrity, and arbitrate priority rankings."
	default:
		return fmt.Sprintf("Phase %d", phase), "General evaluation", "General domain inspection"
	}
}

// AssessSquad analyzes squad balance, highlights blindspots, and builds a disjoint phase schedule.
func AssessSquad(allPersonas []persona.Persona, hired []persona.Persona, targetURL string, contextText string) *SquadAssessment {
	idMap := make(map[string]persona.Persona, len(allPersonas))
	for _, p := range allPersonas {
		idMap[p.ID] = p
	}

	hiredMap := make(map[string]persona.Persona, len(hired))
	groupCount := make(map[int]int)
	groupPersonas := make(map[int][]string)

	for _, p := range hired {
		hiredMap[p.ID] = p
		groupCount[p.GroupID]++
		groupPersonas[p.GroupID] = append(groupPersonas[p.GroupID], p.Name)
	}

	var coverages []DomainCoverage
	var blindspots []string
	var redundancies []string
	var suggestions []PersonaSuggestion

	coveredGroups := 0

	for _, g := range persona.CanonicalGroups {
		cnt := groupCount[g.ID]
		cov := DomainCoverage{
			GroupID:  g.ID,
			Slug:     g.Slug,
			Name:     g.Name,
			Count:    cnt,
			Personas: groupPersonas[g.ID],
			Status:   "uncovered",
		}

		if cnt > 0 {
			coveredGroups++
			if cnt >= 3 && len(hired) <= 6 {
				cov.Status = "overrepresented"
				redundancies = append(redundancies, fmt.Sprintf("Group %d (%s) has %d personas; may create redundant reports", g.ID, g.Name, cnt))
			} else {
				cov.Status = "covered"
			}
		} else {
			// Blindspot detection for critical groups
			switch g.ID {
			case 1:
				blindspots = append(blindspots, "No AI Agent Readiness coverage (lacks MCP, llms.txt, machine auth, or markdown negotiation audit)")
				if p, ok := idMap["agent-readiness-architect"]; ok {
					suggestions = append(suggestions, PersonaSuggestion{
						Action:       "add",
						PersonaID:    p.ID,
						PersonaNumID: p.NumID,
						PersonaName:  p.Name,
						Reason:       "Verify autonomous AI agent protocol discovery, server cards, and synthetic interoperability",
					})
				}
			case 2:
				blindspots = append(blindspots, "No Core Systems/Security coverage (lacks backend architecture, performance, or vulnerability auditing)")
				if p, ok := idMap["senior-fullstack-engineer"]; ok {
					suggestions = append(suggestions, PersonaSuggestion{
						Action:       "add",
						PersonaID:    p.ID,
						PersonaNumID: p.NumID,
						PersonaName:  p.Name,
						Reason:       "Audit API latency, bundle efficiency, and architectural failure modes",
					})
				}
			case 3:
				blindspots = append(blindspots, "No UI/UX or Accessibility coverage (lacks WCAG 2.2 AA contrast, visual spacing, or keyboard navigation audit)")
				if p, ok := idMap["accessibility-sentinel"]; ok {
					suggestions = append(suggestions, PersonaSuggestion{
						Action:       "add",
						PersonaID:    p.ID,
						PersonaNumID: p.NumID,
						PersonaName:  p.Name,
						Reason:       "Guarantee WCAG 2.2 AA compliance, color contrast, and keyboard focus accessibility",
					})
				}
			case 6:
				blindspots = append(blindspots, "No Customer Psychology or Contrarian Review (risk of unexamined blindspots or echo-chamber consensus)")
				if p, ok := idMap["devils-advocate-contrarian"]; ok {
					suggestions = append(suggestions, PersonaSuggestion{
						Action:       "add",
						PersonaID:    p.ID,
						PersonaNumID: p.NumID,
						PersonaName:  p.Name,
						Reason:       "Ruthlessly challenge happy-path assumptions and expose unaddressed failure modes",
					})
				}
			case 7:
				if strings.Contains(strings.ToLower(contextText), "compliance") || strings.Contains(strings.ToLower(contextText), "legal") {
					blindspots = append(blindspots, "No Governance/Legal coverage for compliance-sensitive context")
					if p, ok := idMap["legal-compliance-counsel"]; ok {
						suggestions = append(suggestions, PersonaSuggestion{
							Action:       "add",
							PersonaID:    p.ID,
							PersonaNumID: p.NumID,
							PersonaName:  p.Name,
							Reason:       "Audit regulatory compliance, terms of service, and privacy liability",
						})
					}
				}
			case 8:
				ctxLower := strings.ToLower(contextText)
				if strings.Contains(ctxLower, "hipaa") || strings.Contains(ctxLower, "health") || strings.Contains(ctxLower, "medical") || strings.Contains(ctxLower, "patient") || strings.Contains(ctxLower, "phi") {
					blindspots = append(blindspots, "No Healthcare/HIPAA coverage for clinical or patient data handling")
					if p, ok := idMap["hipaa-healthcare-compliance"]; ok {
						suggestions = append(suggestions, PersonaSuggestion{
							Action:       "add",
							PersonaID:    p.ID,
							PersonaNumID: p.NumID,
							PersonaName:  p.Name,
							Reason:       "Audit HIPAA compliance, PHI data boundaries, and statutory BAA terms",
						})
					}
				}
			}
		}

		coverages = append(coverages, cov)
	}

	// Calculate balance score (0..100)
	// Factors: group diversity (up to 70 pts), size adequacy (up to 20 pts), redundancy penalty (-10 pts)
	diversityScore := (coveredGroups * 70) / len(persona.CanonicalGroups)
	sizeScore := 20
	if len(hired) < 3 {
		sizeScore = len(hired) * 6
	}
	redundancyPenalty := len(redundancies) * 10

	balanceScore := diversityScore + sizeScore - redundancyPenalty
	if balanceScore < 0 {
		balanceScore = 0
	}
	if balanceScore > 100 {
		balanceScore = 100
	}

	// Build Disjoint Execution Phases Schedule
	phaseMap := make(map[int][]persona.Persona)
	for _, p := range hired {
		ph := AssignPhase(p)
		phaseMap[ph] = append(phaseMap[ph], p)
	}

	var schedule []PhaseSchedule
	for ph := 1; ph <= 6; ph++ {
		personasInPhase := phaseMap[ph]
		if len(personasInPhase) == 0 {
			continue
		}
		name, desc, scope := GetPhaseDetails(ph)
		schedule = append(schedule, PhaseSchedule{
			Phase:       ph,
			Name:        name,
			Description: desc,
			TaskScope:   scope,
			Personas:    personasInPhase,
		})
	}

	return &SquadAssessment{
		TargetURL:       targetURL,
		SquadSize:       len(hired),
		BalanceScore:    balanceScore,
		DomainCoverages: coverages,
		Blindspots:      blindspots,
		Redundancies:    redundancies,
		Suggestions:     suggestions,
		Schedule:        schedule,
	}
}

// GeneralAssess performs automated squad prescription and phased scheduling for a target or project.
func GeneralAssess(allPersonas []persona.Persona, targetURL string, contextText string) *SquadAssessment {
	recommended := RecommendSquad(allPersonas, targetURL, contextText)
	return AssessSquad(allPersonas, recommended, targetURL, contextText)
}

// InferGoalDomains infers required canonical persona groups for a goal based on files and keywords.
func InferGoalDomains(g *GoalInfo) ([]int, []string) {
	if g == nil {
		return []int{2}, []string{"Core Systems, Architecture & QA"}
	}

	text := strings.ToLower(g.Slug + " " + g.Title + " " + g.Description + " " + strings.Join(g.FilesToTouch, " "))
	if g.Slug != "" {
		for _, f := range []string{"goal.md", "facts.md", "plan.md"} {
			p := filepath.Join("goals", g.Slug, f)
			if data, err := os.ReadFile(p); err == nil {
				text += " " + strings.ToLower(string(data))
			}
		}
	}

	matched := make(map[int]bool)

	// Check files to touch
	for _, rawPath := range g.FilesToTouch {
		p := strings.ToLower(rawPath)
		ext := filepath.Ext(p)

		// UI/UX files
		if ext == ".html" || ext == ".css" || ext == ".scss" || ext == ".sass" || ext == ".less" ||
			ext == ".tsx" || ext == ".jsx" || ext == ".vue" || ext == ".svelte" || ext == ".astro" ||
			strings.Contains(p, "web/") || strings.Contains(p, "frontend/") || strings.Contains(p, "ui/") ||
			strings.Contains(p, "components/") || strings.Contains(p, "styles/") || strings.Contains(p, "views/") {
			matched[3] = true
		}

		// Agent readiness files
		if strings.Contains(p, "mcp") || strings.Contains(p, "llms.txt") || strings.Contains(p, "ai/") ||
			strings.Contains(p, "prompt") || strings.Contains(p, "agent") || strings.Contains(p, "servercard") {
			matched[1] = true
		}

		// Docs / Marketing files
		if strings.Contains(p, "docs/") || strings.Contains(p, "reference.md") || strings.Contains(p, "changelog") ||
			strings.Contains(p, "readme") || strings.Contains(p, "content/") || strings.Contains(p, "blog/") {
			matched[4] = true
		}

		// Core systems / QA files
		if ext == ".go" || ext == ".rs" || ext == ".py" || ext == ".c" || ext == ".cpp" || ext == ".sh" || ext == ".sql" ||
			strings.Contains(p, "cmd/") || strings.Contains(p, "internal/") || strings.Contains(p, "cli/") ||
			strings.Contains(p, "store/") || strings.Contains(p, "gate/") || strings.Contains(p, "db/") ||
			strings.Contains(p, "api/") || strings.Contains(p, "backend/") {
			matched[2] = true
		}

		// Legal / Governance
		if strings.Contains(p, "license") || strings.Contains(p, "compliance") || strings.Contains(p, "legal") ||
			strings.Contains(p, "privacy") || strings.Contains(p, "terms") {
			matched[7] = true
		}

		// Specialists
		if strings.Contains(p, "i18n") || strings.Contains(p, "locales") || strings.Contains(p, "ar/") ||
			strings.Contains(p, "rtl") || strings.Contains(p, "waqf") || strings.Contains(p, "hipaa") ||
			strings.Contains(p, "web3") || strings.Contains(p, "mobile") {
			matched[8] = true
		}
	}

	// Keywords check
	// Group 1: AI Agent Readiness
	if strings.Contains(text, "mcp") || strings.Contains(text, "llms.txt") || strings.Contains(text, "agent") ||
		strings.Contains(text, "synthetic") || strings.Contains(text, "server card") || strings.Contains(text, "markdown negotiation") ||
		strings.Contains(text, "a2a") || strings.Contains(text, "webmcp") || strings.Contains(text, "token economics") ||
		strings.Contains(text, "ai readiness") || strings.Contains(text, "persona") {
		matched[1] = true
	}

	// Group 2: Core Systems, Architecture & QA
	if strings.Contains(text, "backend") || strings.Contains(text, "database") || strings.Contains(text, "sqlite") ||
		strings.Contains(text, "store") || strings.Contains(text, "schema") || strings.Contains(text, "migration") ||
		strings.Contains(text, "concurrency") || strings.Contains(text, "race") || strings.Contains(text, "goroutine") ||
		strings.Contains(text, "architecture") || strings.Contains(text, "refactor") || strings.Contains(text, "fullstack") ||
		strings.Contains(text, "api") || strings.Contains(text, "cli") || strings.Contains(text, "gate") ||
		strings.Contains(text, "sentry") || strings.Contains(text, "security") || strings.Contains(text, "auth") ||
		strings.Contains(text, "token") || strings.Contains(text, "credential") || strings.Contains(text, "crypto") ||
		strings.Contains(text, "sanitize") || strings.Contains(text, "qa") || strings.Contains(text, "test") ||
		strings.Contains(text, "benchmark") || strings.Contains(text, "performance") || strings.Contains(text, "latency") ||
		strings.Contains(text, "telemetry") || strings.Contains(text, "cfclean") || strings.Contains(text, "hygiene") {
		matched[2] = true
	}

	// Group 3: UI, UX & Design Ergonomics
	if strings.Contains(text, "ui") || strings.Contains(text, "ux") || strings.Contains(text, "frontend") ||
		strings.Contains(text, "component") || strings.Contains(text, "css") || strings.Contains(text, "html") ||
		strings.Contains(text, "dom") || strings.Contains(text, "layout") || strings.Contains(text, "spacing") ||
		strings.Contains(text, "typography") || strings.Contains(text, "accessibility") || strings.Contains(text, "a11y") ||
		strings.Contains(text, "wcag") || strings.Contains(text, "contrast") || strings.Contains(text, "keyboard") ||
		strings.Contains(text, "navigation") || strings.Contains(text, "modal") || strings.Contains(text, "widget") ||
		strings.Contains(text, "color") || strings.Contains(text, "theme") {
		matched[3] = true
	}

	// Group 4: Marketing, Copy & Growth
	if strings.Contains(text, "copy") || strings.Contains(text, "copywriting") || strings.Contains(text, "docs") ||
		strings.Contains(text, "documentation") || strings.Contains(text, "seo") || strings.Contains(text, "discoverability") ||
		strings.Contains(text, "onboarding") || strings.Contains(text, "growth") || strings.Contains(text, "funnel") ||
		strings.Contains(text, "landing") || strings.Contains(text, "changelog") || strings.Contains(text, "release notes") {
		matched[4] = true
	}

	// Group 5: Commercial, Sales & Moat Strategy
	if strings.Contains(text, "pricing") || strings.Contains(text, "billing") || strings.Contains(text, "subscription") ||
		strings.Contains(text, "enterprise") || strings.Contains(text, "procurement") || strings.Contains(text, "roi") ||
		strings.Contains(text, "cost") || strings.Contains(text, "sales") || strings.Contains(text, "moat") ||
		strings.Contains(text, "monetization") || strings.Contains(text, "checkout") {
		matched[5] = true
	}

	// Group 6: Customer Psychology & Contrarian Review
	if strings.Contains(text, "psychology") || strings.Contains(text, "customer") || strings.Contains(text, "champion") ||
		strings.Contains(text, "churn") || strings.Contains(text, "detractor") || strings.Contains(text, "friction") ||
		strings.Contains(text, "contrarian") || strings.Contains(text, "devil's advocate") || strings.Contains(text, "user feedback") {
		matched[6] = true
	}

	// Group 7: Strategy, Ethics & Governance
	if strings.Contains(text, "governance") || strings.Contains(text, "legal") || strings.Contains(text, "compliance") ||
		strings.Contains(text, "license") || strings.Contains(text, "licensing") || strings.Contains(text, "ethics") ||
		strings.Contains(text, "terms of service") || strings.Contains(text, "privacy") || strings.Contains(text, "regulatory") ||
		strings.Contains(text, "gdpr") || strings.Contains(text, "visionary") {
		matched[7] = true
	}

	// Group 8: Domain & Cultural Specialists
	if strings.Contains(text, "i18n") || strings.Contains(text, "localization") || strings.Contains(text, "arabic") ||
		strings.Contains(text, "rtl") || strings.Contains(text, "waqf") || strings.Contains(text, "islamic") ||
		strings.Contains(text, "hipaa") || strings.Contains(text, "phi") || strings.Contains(text, "clinical") ||
		strings.Contains(text, "web3") || strings.Contains(text, "wallet") || strings.Contains(text, "mobile native") ||
		strings.Contains(text, "touch target") {
		matched[8] = true
	}

	// Fallback to Group 2 if nothing matched
	if len(matched) == 0 {
		matched[2] = true
	}

	var groupIDs []int
	for id := range matched {
		groupIDs = append(groupIDs, id)
	}
	sort.Ints(groupIDs)

	groupNameMap := make(map[int]string, len(persona.CanonicalGroups))
	for _, cg := range persona.CanonicalGroups {
		groupNameMap[cg.ID] = cg.Name
	}

	var groupNames []string
	for _, id := range groupIDs {
		if name, ok := groupNameMap[id]; ok {
			groupNames = append(groupNames, name)
		} else {
			groupNames = append(groupNames, fmt.Sprintf("Group %d", id))
		}
	}

	return groupIDs, groupNames
}

// RecommendSquadForGoals recommends a balanced squad tailored to active roadmap goals.
func RecommendSquadForGoals(allPersonas []persona.Persona, targetGoals []*GoalInfo) []persona.Persona {
	idMap := make(map[string]persona.Persona, len(allPersonas))
	for _, p := range allPersonas {
		idMap[p.ID] = p
	}

	selectedIDs := make(map[string]bool)

	// Baseline foundation personas always present for quality
	for _, coreID := range []string{"senior-fullstack-engineer", "qa-automation-lead", "devils-advocate-contrarian"} {
		if _, ok := idMap[coreID]; ok {
			selectedIDs[coreID] = true
		}
	}

	for _, g := range targetGoals {
		groupIDs, _ := InferGoalDomains(g)
		for _, gid := range groupIDs {
			switch gid {
			case 1:
				selectedIDs["agent-readiness-architect"] = true
			case 2:
				selectedIDs["senior-fullstack-engineer"] = true
				selectedIDs["security-compliance-sentry"] = true
			case 3:
				selectedIDs["ui-craftsperson"] = true
				selectedIDs["accessibility-sentinel"] = true
			case 4:
				selectedIDs["conversion-copywriter"] = true
			case 5:
				selectedIDs["enterprise-sales-investor"] = true
			case 6:
				selectedIDs["first-time-user-advocate"] = true
			case 7:
				selectedIDs["legal-compliance-counsel"] = true
			case 8:
				text := strings.ToLower(g.Slug + " " + g.Title + " " + g.Description)
				if strings.Contains(text, "arabic") || strings.Contains(text, "waqf") {
					selectedIDs["arabic-waqf-craftsperson"] = true
				} else if strings.Contains(text, "hipaa") || strings.Contains(text, "health") {
					selectedIDs["hipaa-healthcare-compliance"] = true
				} else if strings.Contains(text, "web3") || strings.Contains(text, "wallet") {
					selectedIDs["web3-contract-auditor"] = true
				} else if strings.Contains(text, "i18n") || strings.Contains(text, "locales") {
					selectedIDs["i18n-localization-architect"] = true
				} else {
					selectedIDs["community-open-source-steward"] = true
				}
			}
		}
	}

	var squad []persona.Persona
	for _, id := range persona.CanonicalOrder {
		if selectedIDs[id] {
			if p, ok := idMap[id]; ok {
				squad = append(squad, p)
			}
		}
	}
	return squad
}

// AssessSquadForGoals evaluates a squad against a list of active roadmap goals.
func AssessSquadForGoals(allPersonas []persona.Persona, hired []persona.Persona, targetGoals []*GoalInfo) *SquadAssessment {
	idMap := make(map[string]persona.Persona, len(allPersonas))
	for _, p := range allPersonas {
		idMap[p.ID] = p
	}

	hiredGroupPersonas := make(map[int][]persona.Persona)
	for _, p := range hired {
		hiredGroupPersonas[p.GroupID] = append(hiredGroupPersonas[p.GroupID], p)
	}

	baseAssessment := AssessSquad(allPersonas, hired, "", "")

	var goalsCoverage []GoalCoverage
	var goalBlindspots []string
	var goalSuggestions []PersonaSuggestion
	suggestedIDs := make(map[string]bool)
	for _, s := range baseAssessment.Suggestions {
		suggestedIDs[s.PersonaID] = true
	}

	var totalScore float64

	for _, g := range targetGoals {
		groupIDs, groupNames := InferGoalDomains(g)
		var covering []string
		var missing []string
		coveredGroupCount := 0

		coveringSeen := make(map[string]bool)
		for idx, gid := range groupIDs {
			gName := groupNames[idx]
			personasInGroup := hiredGroupPersonas[gid]
			if len(personasInGroup) > 0 {
				coveredGroupCount++
				for _, p := range personasInGroup {
					if !coveringSeen[p.Name] {
						coveringSeen[p.Name] = true
						covering = append(covering, p.Name)
					}
				}
			} else {
				missing = append(missing, gName)
			}
		}

		status := "uncovered"
		if len(groupIDs) == 0 || len(missing) == 0 {
			status = "covered"
			totalScore += 1.0
		} else if coveredGroupCount > 0 {
			status = "partially_covered"
			totalScore += float64(coveredGroupCount) / float64(len(groupIDs))
		} else {
			status = "uncovered"
		}

		// Goal-specific blindspots and suggestions
		text := strings.ToLower(g.Slug + " " + g.Title + " " + g.Description + " " + strings.Join(g.FilesToTouch, " "))
		for _, gid := range groupIDs {
			if len(hiredGroupPersonas[gid]) == 0 {
				switch gid {
				case 1:
					goalBlindspots = append(goalBlindspots, fmt.Sprintf("Goal %s requires AI Agent Readiness (MCP, tools, protocols), but squad lacks Agent Readiness Architect", g.Slug))
					if p, ok := idMap["agent-readiness-architect"]; ok && !suggestedIDs[p.ID] {
						suggestedIDs[p.ID] = true
						goalSuggestions = append(goalSuggestions, PersonaSuggestion{
							Action:       "add",
							PersonaID:    p.ID,
							PersonaNumID: p.NumID,
							PersonaName:  p.Name,
							Reason:       fmt.Sprintf("Audit agent discovery, tool schemas, and synthetic protocols for goal %s", g.Slug),
						})
					}
				case 2:
					if strings.Contains(text, "security") || strings.Contains(text, "auth") || strings.Contains(text, "token") || strings.Contains(text, "cfclean") {
						goalBlindspots = append(goalBlindspots, fmt.Sprintf("Goal %s modifies security/auth/credentials, but squad lacks Security Compliance Sentry", g.Slug))
						if p, ok := idMap["security-compliance-sentry"]; ok && !suggestedIDs[p.ID] {
							suggestedIDs[p.ID] = true
							goalSuggestions = append(goalSuggestions, PersonaSuggestion{
								Action:       "add",
								PersonaID:    p.ID,
								PersonaNumID: p.NumID,
								PersonaName:  p.Name,
								Reason:       fmt.Sprintf("Audit security boundaries, secrets, and auth flows for goal %s", g.Slug),
							})
						}
					} else {
						goalBlindspots = append(goalBlindspots, fmt.Sprintf("Goal %s requires Core Systems/QA review, but squad lacks Senior Fullstack Engineer", g.Slug))
						if p, ok := idMap["senior-fullstack-engineer"]; ok && !suggestedIDs[p.ID] {
							suggestedIDs[p.ID] = true
							goalSuggestions = append(goalSuggestions, PersonaSuggestion{
								Action:       "add",
								PersonaID:    p.ID,
								PersonaNumID: p.NumID,
								PersonaName:  p.Name,
								Reason:       fmt.Sprintf("Review core architecture, latency, and failure modes for goal %s", g.Slug),
							})
						}
					}
				case 3:
					goalBlindspots = append(goalBlindspots, fmt.Sprintf("Goal %s touches UI/UX components or styles, but squad lacks UI/UX or Accessibility reviewer", g.Slug))
					if p, ok := idMap["accessibility-sentinel"]; ok && !suggestedIDs[p.ID] {
						suggestedIDs[p.ID] = true
						goalSuggestions = append(goalSuggestions, PersonaSuggestion{
							Action:       "add",
							PersonaID:    p.ID,
							PersonaNumID: p.NumID,
							PersonaName:  p.Name,
							Reason:       fmt.Sprintf("Guarantee WCAG 2.2 AA compliance and UI ergonomics for goal %s", g.Slug),
						})
					}
				case 4:
					goalBlindspots = append(goalBlindspots, fmt.Sprintf("Goal %s modifies documentation or user-facing copy, but squad lacks Copywriter", g.Slug))
					if p, ok := idMap["conversion-copywriter"]; ok && !suggestedIDs[p.ID] {
						suggestedIDs[p.ID] = true
						goalSuggestions = append(goalSuggestions, PersonaSuggestion{
							Action:       "add",
							PersonaID:    p.ID,
							PersonaNumID: p.NumID,
							PersonaName:  p.Name,
							Reason:       fmt.Sprintf("Ensure clear, concise technical copy and documentation for goal %s", g.Slug),
						})
					}
				case 5:
					goalBlindspots = append(goalBlindspots, fmt.Sprintf("Goal %s impacts pricing/business models, but squad lacks Commercial Strategist", g.Slug))
					if p, ok := idMap["enterprise-sales-investor"]; ok && !suggestedIDs[p.ID] {
						suggestedIDs[p.ID] = true
						goalSuggestions = append(goalSuggestions, PersonaSuggestion{
							Action:       "add",
							PersonaID:    p.ID,
							PersonaNumID: p.NumID,
							PersonaName:  p.Name,
							Reason:       fmt.Sprintf("Evaluate enterprise procurement and business viability for goal %s", g.Slug),
						})
					}
				case 6:
					goalBlindspots = append(goalBlindspots, fmt.Sprintf("Goal %s alters user experience flows, but squad lacks Customer Detractor/Contrarian reviewer", g.Slug))
					if p, ok := idMap["devils-advocate-contrarian"]; ok && !suggestedIDs[p.ID] {
						suggestedIDs[p.ID] = true
						goalSuggestions = append(goalSuggestions, PersonaSuggestion{
							Action:       "add",
							PersonaID:    p.ID,
							PersonaNumID: p.NumID,
							PersonaName:  p.Name,
							Reason:       fmt.Sprintf("Stress-test user flow friction and edge cases for goal %s", g.Slug),
						})
					}
				case 7:
					goalBlindspots = append(goalBlindspots, fmt.Sprintf("Goal %s involves compliance/governance policies, but squad lacks Legal/Compliance Counsel", g.Slug))
					if p, ok := idMap["legal-compliance-counsel"]; ok && !suggestedIDs[p.ID] {
						suggestedIDs[p.ID] = true
						goalSuggestions = append(goalSuggestions, PersonaSuggestion{
							Action:       "add",
							PersonaID:    p.ID,
							PersonaNumID: p.NumID,
							PersonaName:  p.Name,
							Reason:       fmt.Sprintf("Verify legal liability and licensing terms for goal %s", g.Slug),
						})
					}
				case 8:
					if strings.Contains(text, "arabic") || strings.Contains(text, "waqf") {
						goalBlindspots = append(goalBlindspots, fmt.Sprintf("Goal %s involves Arabic/Waqf domain, but squad lacks Arabic Waqf Craftsperson", g.Slug))
						if p, ok := idMap["arabic-waqf-craftsperson"]; ok && !suggestedIDs[p.ID] {
							suggestedIDs[p.ID] = true
							goalSuggestions = append(goalSuggestions, PersonaSuggestion{
								Action:       "add",
								PersonaID:    p.ID,
								PersonaNumID: p.NumID,
								PersonaName:  p.Name,
								Reason:       fmt.Sprintf("Ensure Arabic RTL typography and Waqf domain fidelity for goal %s", g.Slug),
							})
						}
					}
				}
			}
		}

		cov := GoalCoverage{
			GoalSlug:         g.Slug,
			Title:            g.Title,
			RequiredGroups:   groupNames,
			RequiredGroupIDs: groupIDs,
			CoveringPersonas: covering,
			MissingGroups:    missing,
			Status:           status,
		}
		goalsCoverage = append(goalsCoverage, cov)
	}

	alignmentScore := 100
	if len(targetGoals) > 0 {
		alignmentScore = int((totalScore / float64(len(targetGoals))) * 100)
		if alignmentScore > 100 {
			alignmentScore = 100
		}
		if alignmentScore < 0 {
			alignmentScore = 0
		}
	}

	combinedBlindspots := append(goalBlindspots, baseAssessment.Blindspots...)
	combinedSuggestions := append(goalSuggestions, baseAssessment.Suggestions...)

	baseAssessment.GoalsCoverage = goalsCoverage
	baseAssessment.RoadmapAlignment = alignmentScore
	baseAssessment.Blindspots = combinedBlindspots
	baseAssessment.Suggestions = combinedSuggestions

	return baseAssessment
}
