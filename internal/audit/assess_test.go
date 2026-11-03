package audit

import (
	"testing"

	"github.com/jadmadi/sila/internal/persona"
)

func TestInferGoalDomains(t *testing.T) {
	tests := []struct {
		name          string
		goal          *GoalInfo
		wantGroupIDs  []int
		mustHaveGroup int
	}{
		{
			name: "UI and UX Goal",
			goal: &GoalInfo{
				Slug:         "mobile-dark-mode",
				Title:        "Implement accessible dark mode and touch layouts",
				Description:  "Update css spacing tokens and responsive mobile navigation components",
				FilesToTouch: []string{"web/styles/theme.css", "ui/components/nav.tsx"},
			},
			mustHaveGroup: 3, // Group 3: UI, UX & Design Ergonomics
		},
		{
			name: "AI Agent Readiness Goal",
			goal: &GoalInfo{
				Slug:         "mcp-server-cards",
				Title:        "Expose machine-readable MCP server cards and synthetic protocols",
				Description:  "Publish /.well-known/mcp.json and markdown negotiation headers",
				FilesToTouch: []string{"internal/mcp/server.go", "docs/llms.txt"},
			},
			mustHaveGroup: 1, // Group 1: AI Agent Readiness
		},
		{
			name: "Core Systems and Security Goal",
			goal: &GoalInfo{
				Slug:         "sqlite-migration-sentry",
				Title:        "Database schema migrations with concurrency lock and secret sanitization",
				Description:  "Refactor sqlite store schema, prevent race conditions, and audit credentials",
				FilesToTouch: []string{"internal/store/schema.go", "cmd/sila/gate.go"},
			},
			mustHaveGroup: 2, // Group 2: Core Systems, Architecture & QA
		},
		{
			name: "Marketing and Documentation Goal",
			goal: &GoalInfo{
				Slug:         "developer-onboarding-docs",
				Title:        "Comprehensive developer quickstart documentation and tutorials",
				Description:  "Write high-conversion copy and technical onboarding reference guides",
				FilesToTouch: []string{"docs/quickstart.md", "README.md"},
			},
			mustHaveGroup: 4, // Group 4: Marketing, Copy & Growth
		},
		{
			name: "Arabic and Waqf Cultural Specialist Goal",
			goal: &GoalInfo{
				Slug:         "arabic-rtl-waqf-typography",
				Title:        "Arabic Waqf legal compliance and RTL bidi font shaping",
				Description:  "Ensure traditional Islamic etiquette and right-to-left layout fidelity",
				FilesToTouch: []string{"i18n/ar.json", "styles/arabic.css"},
			},
			mustHaveGroup: 8, // Group 8: Domain & Cultural Specialists
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			groupIDs, groupNames := InferGoalDomains(tc.goal)
			if len(groupIDs) == 0 {
				t.Fatalf("expected non-empty group IDs, got empty")
			}
			if len(groupIDs) != len(groupNames) {
				t.Fatalf("mismatched groupIDs (%d) and groupNames (%d)", len(groupIDs), len(groupNames))
			}

			found := false
			for _, gid := range groupIDs {
				if gid == tc.mustHaveGroup {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected group ID %d to be inferred for %s, got: %v (%v)",
					tc.mustHaveGroup, tc.name, groupIDs, groupNames)
			}
		})
	}
}

func TestAssessSquadForGoals_CoveredAndUncovered(t *testing.T) {
	allPersonas := persona.EnrichPersonas(persona.FoundationPersonas)

	uiGoal := &GoalInfo{
		Slug:         "add-design-tokens",
		Title:        "UI Design Tokens and Accessibility",
		Description:  "Update css spacing tokens and WCAG contrast",
		FilesToTouch: []string{"ui/theme.css"},
	}

	coreGoal := &GoalInfo{
		Slug:         "sqlite-lock-tuning",
		Title:        "SQLite WAL Lock Optimization",
		Description:  "Database store transaction concurrency tuning",
		FilesToTouch: []string{"internal/store/db.go"},
	}

	targetGoals := []*GoalInfo{uiGoal, coreGoal}

	// 1. Squad with UI person (Group 3) and Core Engineering (Group 2) -> Both covered
	fullSquad, err := persona.ResolvePersonas(allPersonas, "ui-craftsperson, senior-fullstack-engineer")
	if err != nil {
		t.Fatalf("resolve squad error: %v", err)
	}

	assessmentFull := AssessSquadForGoals(allPersonas, fullSquad, targetGoals)
	if assessmentFull.RoadmapAlignment < 75 {
		t.Errorf("expected high roadmap alignment, got: %d", assessmentFull.RoadmapAlignment)
	}
	if len(assessmentFull.GoalsCoverage) != 2 {
		t.Fatalf("expected 2 goals covered, got %d", len(assessmentFull.GoalsCoverage))
	}
	for _, gc := range assessmentFull.GoalsCoverage {
		if gc.Status != "covered" {
			t.Errorf("expected goal %s to be covered, got: %s (missing: %v)", gc.GoalSlug, gc.Status, gc.MissingGroups)
		}
	}

	// 2. Squad with only Core Engineering (Group 2) -> UI goal is uncovered/partially covered
	coreOnlySquad, err := persona.ResolvePersonas(allPersonas, "senior-fullstack-engineer")
	if err != nil {
		t.Fatalf("resolve squad error: %v", err)
	}

	assessmentPartial := AssessSquadForGoals(allPersonas, coreOnlySquad, targetGoals)
	if assessmentPartial.RoadmapAlignment >= 100 {
		t.Errorf("expected partial alignment < 100, got: %d", assessmentPartial.RoadmapAlignment)
	}

	var foundUIBlindspot bool
	for _, b := range assessmentPartial.Blindspots {
		if len(b) > 0 {
			foundUIBlindspot = true
			break
		}
	}
	if !foundUIBlindspot {
		t.Errorf("expected UI-related blindspot when UI persona is missing")
	}

	var foundUISuggestion bool
	for _, s := range assessmentPartial.Suggestions {
		if s.PersonaID == "accessibility-sentinel" || s.PersonaID == "ui-craftsperson" {
			foundUISuggestion = true
			break
		}
	}
	if !foundUISuggestion {
		t.Errorf("expected UI persona suggestion when UI goal is uncovered")
	}
}

func TestRecommendSquadForGoals(t *testing.T) {
	allPersonas := persona.EnrichPersonas(persona.FoundationPersonas)

	goalsList := []*GoalInfo{
		{
			Slug:         "mcp-protocol-sync",
			Title:        "AI Agent MCP protocol synchronization",
			Description:  "Publish MCP server cards and llms.txt",
			FilesToTouch: []string{"internal/mcp/server.go"},
		},
		{
			Slug:         "accessible-dashboard",
			Title:        "Responsive accessible admin dashboard",
			Description:  "WCAG contrast and keyboard navigation in UI components",
			FilesToTouch: []string{"ui/dashboard.tsx"},
		},
	}

	recommended := RecommendSquadForGoals(allPersonas, goalsList)
	if len(recommended) < 3 {
		t.Errorf("expected at least 3 recommended personas, got %d", len(recommended))
	}

	hasAgentReady := false
	hasUI := false
	for _, p := range recommended {
		if p.GroupID == 1 {
			hasAgentReady = true
		}
		if p.GroupID == 3 {
			hasUI = true
		}
	}

	if !hasAgentReady {
		t.Errorf("expected recommended squad to contain Group 1 (AI Agent Readiness)")
	}
	if !hasUI {
		t.Errorf("expected recommended squad to contain Group 3 (UI/UX)")
	}
}
