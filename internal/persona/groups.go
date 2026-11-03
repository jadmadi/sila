package persona

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Group represents a logical category or operational group of personas.
type Group struct {
	ID          int      `json:"id"`
	Slug        string   `json:"slug"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	PersonaIDs  []string `json:"persona_ids"`
}

// PersonaAliases maps common shortened persona nicknames to canonical foundation persona IDs.
var PersonaAliases = map[string]string{
	"ui-craft":          "ui-craftsperson",
	"dom-ergonomist":    "ux-information-architect",
	"a11y-sentry":       "accessibility-sentinel",
	"onboard-guide":     "first-time-user-advocate",
	"mystery":           "mystery-auditor",
	"mystery-evaluator": "mystery-auditor",
	"mystery-shopper":   "mystery-auditor",
}

// CanonicalGroups defines the 8 primary operational categories for all foundation personas.
var CanonicalGroups = []Group{
	{
		ID:          1,
		Slug:        "agent-ready",
		Name:        "AI Agent Readiness & Synthetic Protocols",
		Description: "Machine discoverability, MCP Server Card, A2A, WebMCP, markdown negotiation, token economics, and agentic commerce",
		PersonaIDs: []string{
			"agent-readiness-architect",
			"content-negotiation-token-economist",
			"agentic-commerce-auditor",
			"machine-identity-auth-counsel",
		},
	},
	{
		ID:          2,
		Slug:        "engineering",
		Name:        "Core Systems, Architecture & QA",
		Description: "System architecture, automated test suites, Core Web Vitals, application security, and event observability",
		PersonaIDs: []string{
			"senior-fullstack-engineer",
			"qa-automation-lead",
			"performance-sentinel",
			"security-compliance-sentry",
			"telemetry-data-forensic",
			"mystery-auditor",
		},
	},
	{
		ID:          3,
		Slug:        "ui-ux",
		Name:        "UI, UX & Design Ergonomics",
		Description: "Design systems, typography tokens, information architecture, WCAG accessibility, and keyboard navigation",
		PersonaIDs: []string{
			"ui-craftsperson",
			"ux-information-architect",
			"accessibility-sentinel",
			"keyboard-power-maximalist",
		},
	},
	{
		ID:          4,
		Slug:        "marketing",
		Name:        "Marketing, Copy & Growth",
		Description: "High-conversion copywriting, viral onboarding funnels, and search engine discoverability",
		PersonaIDs: []string{
			"conversion-copywriter",
			"growth-product-strategist",
			"seo-discoverability-auditor",
		},
	},
	{
		ID:          5,
		Slug:        "business",
		Name:        "Commercial, Sales & Moat Strategy",
		Description: "Enterprise procurement viability, investor economics, and competitive moat defense",
		PersonaIDs: []string{
			"enterprise-sales-investor",
			"competitor-recon-strategist",
		},
	},
	{
		ID:          6,
		Slug:        "psychology",
		Name:        "Customer Psychology & Contrarian Review",
		Description: "Day-0 user empathy, passionate customer champions, churn-risk detractors, and ruthless contrarian stress-testing",
		PersonaIDs: []string{
			"first-time-user-advocate",
			"positive-champion-customer",
			"negative-churn-risk-customer",
			"devils-advocate-contrarian",
		},
	},
	{
		ID:          7,
		Slug:        "governance",
		Name:        "Strategy, Ethics & Governance",
		Description: "10x unconstrained blue-sky vision, human dignity sentinels, and legal regulatory compliance",
		PersonaIDs: []string{
			"visionary-dreamer",
			"epistemic-philosopher",
			"legal-compliance-counsel",
		},
	},
	{
		ID:          8,
		Slug:        "specialists",
		Name:        "Domain & Cultural Specialists",
		Description: "RTL Arabization, Waqf etiquette, global bidi i18n, mobile touch ergonomics, HIPAA clinical privacy, Web3 wallets, and open-source stewardship",
		PersonaIDs: []string{
			"arabic-waqf-craftsperson",
			"i18n-localization-architect",
			"mobile-native-ergonomist",
			"hipaa-healthcare-compliance",
			"web3-contract-auditor",
			"community-open-source-steward",
		},
	},
}

// CanonicalOrder provides the deterministic sequential ordering of persona slugs (1 to 31).
var CanonicalOrder = []string{
	// Group 1: AI Agent Readiness & Protocols
	"agent-readiness-architect",           // 1
	"content-negotiation-token-economist", // 2
	"agentic-commerce-auditor",            // 3
	"machine-identity-auth-counsel",       // 4

	// Group 2: Core Systems, Architecture & QA
	"senior-fullstack-engineer",  // 5
	"qa-automation-lead",         // 6
	"performance-sentinel",       // 7
	"security-compliance-sentry", // 8
	"telemetry-data-forensic",    // 9

	// Group 3: UI, UX & Design Ergonomics
	"ui-craftsperson",           // 10
	"ux-information-architect",  // 11
	"accessibility-sentinel",    // 12
	"keyboard-power-maximalist", // 13

	// Group 4: Marketing, Copy & Growth
	"conversion-copywriter",       // 14
	"growth-product-strategist",   // 15
	"seo-discoverability-auditor", // 16

	// Group 5: Commercial, Sales & Moat Strategy
	"enterprise-sales-investor",   // 17
	"competitor-recon-strategist", // 18

	// Group 6: Customer Psychology & Contrarian Review
	"first-time-user-advocate",     // 19
	"positive-champion-customer",   // 20
	"negative-churn-risk-customer", // 21
	"devils-advocate-contrarian",   // 22

	// Group 7: Strategy, Ethics & Governance
	"visionary-dreamer",        // 23
	"epistemic-philosopher",    // 24
	"legal-compliance-counsel", // 25

	// Group 8: Domain & Cultural Specialists
	"arabic-waqf-craftsperson",      // 26
	"i18n-localization-architect",   // 27
	"mobile-native-ergonomist",      // 28
	"hipaa-healthcare-compliance",   // 29
	"web3-contract-auditor",         // 30
	"community-open-source-steward", // 31
	"mystery-auditor",               // 32
}

// GroupedPersonas contains a group and its enriched persona members.
type GroupedPersonas struct {
	Group    Group     `json:"group"`
	Personas []Persona `json:"personas"`
}

// canonicalNumMap maps a persona slug to its 1-indexed numeric ID.
var canonicalNumMap map[string]int

// canonicalGroupMap maps a persona slug to its containing Group.
var canonicalGroupMap map[string]Group

func init() {
	canonicalNumMap = make(map[string]int, len(CanonicalOrder))
	for idx, id := range CanonicalOrder {
		canonicalNumMap[id] = idx + 1
	}

	canonicalGroupMap = make(map[string]Group)
	for _, g := range CanonicalGroups {
		for _, id := range g.PersonaIDs {
			canonicalGroupMap[id] = g
		}
	}
}

// EnrichPersona assigns NumID, GroupID, GroupSlug, and GroupName to a persona.
func EnrichPersona(p *Persona) {
	if p == nil {
		return
	}
	if num, ok := canonicalNumMap[p.ID]; ok {
		p.NumID = num
		if g, found := canonicalGroupMap[p.ID]; found {
			p.GroupID = g.ID
			p.GroupSlug = g.Slug
			p.GroupName = g.Name
		}
		return
	}

	// For custom or unmapped personas
	if p.NumID == 0 {
		p.NumID = 32
	}
	if p.GroupID == 0 {
		p.GroupID = 9
		p.GroupSlug = "custom"
		p.GroupName = "Custom & Community Personas"
	}
}

// EnrichPersonas enriches a slice of personas with numeric and group IDs.
func EnrichPersonas(personas []Persona) []Persona {
	customIdx := 0
	out := make([]Persona, len(personas))
	copy(out, personas)

	for i := range out {
		if num, ok := canonicalNumMap[out[i].ID]; ok {
			out[i].NumID = num
			if g, found := canonicalGroupMap[out[i].ID]; found {
				out[i].GroupID = g.ID
				out[i].GroupSlug = g.Slug
				out[i].GroupName = g.Name
			}
		} else {
			out[i].NumID = len(CanonicalOrder) + 1 + customIdx
			out[i].GroupID = 9
			out[i].GroupSlug = "custom"
			out[i].GroupName = "Custom & Community Personas"
			customIdx++
		}
	}
	return out
}

// GroupPersonas groups personas into their respective categories, sorted by group ID.
func GroupPersonas(personas []Persona) []GroupedPersonas {
	enriched := EnrichPersonas(personas)

	// Map personas by group ID
	groupMap := make(map[int][]Persona)
	for _, p := range enriched {
		groupMap[p.GroupID] = append(groupMap[p.GroupID], p)
	}

	var results []GroupedPersonas
	for _, g := range CanonicalGroups {
		if plist, ok := groupMap[g.ID]; ok && len(plist) > 0 {
			sort.Slice(plist, func(i, j int) bool {
				return plist[i].NumID < plist[j].NumID
			})
			results = append(results, GroupedPersonas{
				Group:    g,
				Personas: plist,
			})
		}
	}

	// Add custom group if present
	if customList, ok := groupMap[9]; ok && len(customList) > 0 {
		results = append(results, GroupedPersonas{
			Group: Group{
				ID:          9,
				Slug:        "custom",
				Name:        "Custom & Community Personas",
				Description: "Locally created or user-registered personas",
			},
			Personas: customList,
		})
	}

	return results
}

// FindGroup looks up a group by its numeric ID ("1", "#1") or slug ("agent-ready").
func FindGroup(idOrSlug string) (*Group, bool) {
	trimmed := strings.ToLower(strings.TrimSpace(idOrSlug))
	trimmed = strings.TrimPrefix(trimmed, "group")
	trimmed = strings.TrimSpace(trimmed)
	trimmed = strings.TrimPrefix(trimmed, "#")
	trimmed = strings.TrimSpace(trimmed)

	if num, err := strconv.Atoi(trimmed); err == nil {
		for i := range CanonicalGroups {
			if CanonicalGroups[i].ID == num {
				return &CanonicalGroups[i], true
			}
		}
		return nil, false
	}

	for i := range CanonicalGroups {
		if strings.EqualFold(CanonicalGroups[i].Slug, trimmed) || strings.EqualFold(CanonicalGroups[i].Name, trimmed) {
			return &CanonicalGroups[i], true
		}
	}
	return nil, false
}

// ResolvePersonaID resolves a single identifier which may be a numeric index ("1", "#1") or slug.
func ResolvePersonaID(idOrNum string) string {
	trimmed := strings.TrimSpace(idOrNum)
	trimmed = strings.TrimPrefix(trimmed, "#")
	trimmed = strings.TrimSpace(trimmed)

	if num, err := strconv.Atoi(trimmed); err == nil && num >= 1 && num <= len(CanonicalOrder) {
		return CanonicalOrder[num-1]
	}
	return strings.TrimSpace(idOrNum)
}

// ResolvePersonas parses a spec which can be:
// - "group 1" / "group #1" / "group agent-ready"
// - comma-separated numeric IDs: "1,4,6,3,8"
// - space-separated numeric IDs: "1 4 6 3 8"
// - comma-separated slugs: "agent-readiness-architect, qa-automation-lead"
// - mixed: "1, qa-automation-lead, #8"
func ResolvePersonas(allPersonas []Persona, spec string) ([]Persona, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, fmt.Errorf("no persona specification provided")
	}

	enriched := EnrichPersonas(allPersonas)
	numLookup := make(map[int]Persona, len(enriched))
	slugLookup := make(map[string]Persona, len(enriched))
	for _, p := range enriched {
		numLookup[p.NumID] = p
		slugLookup[strings.ToLower(p.ID)] = p
	}

	// Check if this is a group hire: e.g. "group 1", "group #1", "group agent-ready", "#1"
	lowerSpec := strings.ToLower(spec)
	if strings.HasPrefix(lowerSpec, "group ") || strings.HasPrefix(lowerSpec, "group#") {
		groupQuery := strings.TrimPrefix(lowerSpec, "group ")
		groupQuery = strings.TrimPrefix(groupQuery, "group#")
		grp, found := FindGroup(groupQuery)
		if !found {
			return nil, fmt.Errorf("group %q not found (available groups: 1..8 or slugs: agent-ready, engineering, ui-ux, marketing, business, psychology, governance, specialists)", groupQuery)
		}
		var squad []Persona
		for _, id := range grp.PersonaIDs {
			if p, ok := slugLookup[id]; ok {
				squad = append(squad, p)
			}
		}
		return squad, nil
	}

	// Split tokens by comma or space
	var rawTokens []string
	if strings.Contains(spec, ",") {
		rawTokens = strings.Split(spec, ",")
	} else {
		rawTokens = strings.Fields(spec)
	}

	var squad []Persona
	seen := make(map[string]bool)

	for _, raw := range rawTokens {
		token := strings.TrimSpace(raw)
		if token == "" {
			continue
		}

		cleanNum := strings.TrimPrefix(token, "#")
		if num, err := strconv.Atoi(cleanNum); err == nil {
			if p, ok := numLookup[num]; ok {
				if !seen[p.ID] {
					seen[p.ID] = true
					squad = append(squad, p)
				}
				continue
			}
			return nil, fmt.Errorf("persona ID #%d not found (valid IDs: 1..%d)", num, len(enriched))
		}

		lowerToken := strings.ToLower(token)
		if canonical, ok := PersonaAliases[lowerToken]; ok {
			lowerToken = canonical
		}
		if p, ok := slugLookup[lowerToken]; ok {
			if !seen[p.ID] {
				seen[p.ID] = true
				squad = append(squad, p)
			}
			continue
		}

		// Also check if token was a group slug directly
		if grp, found := FindGroup(token); found {
			for _, id := range grp.PersonaIDs {
				if p, ok := slugLookup[id]; ok && !seen[p.ID] {
					seen[p.ID] = true
					squad = append(squad, p)
				}
			}
			continue
		}

		return nil, fmt.Errorf("unknown persona or group %q", token)
	}

	if len(squad) == 0 {
		return nil, fmt.Errorf("no matching personas found for %q", spec)
	}

	return squad, nil
}
