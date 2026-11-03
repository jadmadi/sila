package assist

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestHeuristicRoute(t *testing.T) {
	tests := []struct {
		objective   string
		currentTool string
		wantCat     string
		minTools    int
	}{
		{
			objective: "I just cloned this repository and want to get oriented",
			wantCat:   WorkflowOnboarding,
			minTools:  3,
		},
		{
			objective: "investigate why the test suite is failing with database locked error",
			wantCat:   WorkflowInvestigation,
			minTools:  3,
		},
		{
			objective: "claim and implement the next roadmap goal for cloudflare worker",
			wantCat:   WorkflowGoalExecution,
			minTools:  4,
		},
		{
			objective: "run pre-commit quality gate checks before committing",
			wantCat:   WorkflowGateAndVerification,
			minTools:  3,
		},
		{
			objective: "reconcile documentation drift and sync AGENTS.md rules",
			wantCat:   WorkflowInstructionAndDrift,
			minTools:  3,
		},
		{
			objective: "hello, what should I do first?",
			wantCat:   WorkflowOnboarding,
			minTools:  3,
		},
		{
			objective: "unspecified generic prompt without clear keyword",
			wantCat:   WorkflowGeneral,
			minTools:  3,
		},
		{
			objective:   "continue with next step",
			currentTool: "claim_goal",
			wantCat:     WorkflowGoalExecution,
			minTools:    4,
		},
		{
			objective:   "what should we do next",
			currentTool: "search_knowledge",
			wantCat:     WorkflowInvestigation,
			minTools:    3,
		},
		{
			objective: "what have I been working on in the last 24 hours across sessions?",
			wantCat:   WorkflowTemporalActivity,
			minTools:  3,
		},
		{
			objective: "summarize today's activity",
			wantCat:   WorkflowTemporalActivity,
			minTools:  3,
		},
		{
			objective: "why did we choose pure-go sqlite instead of cgo?",
			wantCat:   WorkflowArchitectureAndDecisions,
			minTools:  5,
		},
		{
			objective: "run an adversarial security audit of this change",
			wantCat:   WorkflowPersonaAndAudit,
			minTools:  3,
		},
		{
			objective: "which skill should I load for cloudflare domain standards?",
			wantCat:   WorkflowSkillAndDomainLearning,
			minTools:  4,
		},
		{
			objective: "check references and blast radius before renaming parseArgs",
			wantCat:   WorkflowSymbolAndBlastRadius,
			minTools:  4,
		},
	}

	for _, tc := range tests {
		cat, conf, rationale := HeuristicRoute(tc.objective, tc.currentTool)
		if cat != tc.wantCat {
			t.Errorf("HeuristicRoute(%q, %q) cat = %q, want %q", tc.objective, tc.currentTool, cat, tc.wantCat)
		}
		if conf <= 0 || conf > 1.0 {
			t.Errorf("expected confidence in (0, 1], got %f", conf)
		}
		if rationale == "" {
			t.Errorf("expected non-empty rationale for %q", tc.objective)
		}
		chain, ok := CanonicalToolChains[cat]
		if !ok || len(chain.Tools) < tc.minTools {
			t.Errorf("expected at least %d tools for cat %q, got %d", tc.minTools, cat, len(chain.Tools))
		}
	}
}

func TestRouteWorkflow_FailOpenFallback(t *testing.T) {
	ctx := context.Background()

	// 1. Nil provider returns heuristic seamlessly
	rec := RouteWorkflow(ctx, "debug test failure in gate pipeline", "", "test-proj", nil)
	if rec.Provider != "heuristic" {
		t.Errorf("expected heuristic provider with nil provider, got %q", rec.Provider)
	}
	if rec.Category != WorkflowInvestigation && rec.Category != WorkflowGateAndVerification {
		t.Errorf("expected investigation or gate category, got %q", rec.Category)
	}
	if len(rec.Tools) == 0 {
		t.Errorf("expected recommended tools, got empty slice")
	}

	// 2. Pre-canceled context must fail-open to heuristic immediately without panic
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()

	mock := NewMockAdapter()
	recCanceled := RouteWorkflow(canceledCtx, "onboard new agent into repository", "", "test-proj", mock)
	if recCanceled.Provider != "heuristic" {
		t.Errorf("expected fail-open heuristic fallback on canceled context, got %q", recCanceled.Provider)
	}
	if recCanceled.Category != WorkflowOnboarding {
		t.Errorf("expected onboarding category, got %q", recCanceled.Category)
	}
}

func TestRouteWorkflow_WithMockProvider(t *testing.T) {
	ctx := context.Background()
	mock := NewMockAdapter()

	// Objective mentions "goal_execution"
	rec := RouteWorkflow(ctx, "execute tasks under goal_execution roadmap", "", "test-proj", mock)
	if rec.Provider != "jev" {
		t.Errorf("expected jev provider with mock adapter, got %q", rec.Provider)
	}
	if rec.Category != WorkflowGoalExecution {
		t.Errorf("expected goal_execution category, got %q", rec.Category)
	}
	if !slices.Contains(rec.Tools, "claim_goal") {
		t.Errorf("expected claim_goal in tools, got %v", rec.Tools)
	}
	if rec.DurationMs < 0 {
		t.Errorf("expected non-negative duration, got %d", rec.DurationMs)
	}
}

func TestCanonicalChainsDisjointAndValid(t *testing.T) {
	expectedCategories := []string{
		WorkflowOnboarding,
		WorkflowInvestigation,
		WorkflowGoalExecution,
		WorkflowGateAndVerification,
		WorkflowInstructionAndDrift,
		WorkflowTemporalActivity,
		WorkflowArchitectureAndDecisions,
		WorkflowPersonaAndAudit,
		WorkflowSkillAndDomainLearning,
		WorkflowSymbolAndBlastRadius,
		WorkflowGeneral,
	}

	for _, cat := range expectedCategories {
		chain, ok := CanonicalToolChains[cat]
		if !ok {
			t.Errorf("missing canonical chain for category %q", cat)
			continue
		}
		if len(chain.Tools) == 0 {
			t.Errorf("empty tools for category %q", cat)
		}
		if len(chain.CLICommands) == 0 {
			t.Errorf("empty CLI commands for category %q", cat)
		}
		if strings.TrimSpace(chain.Rationale) == "" {
			t.Errorf("empty rationale for category %q", cat)
		}
	}
}

func TestHeuristicRouteDeterministicTieBreaking(t *testing.T) {
	objectives := []string{
		"audit symbol contract before commit",
		"check references and security review",
		"skill guide and audit decision",
	}
	for _, objective := range objectives {
		first, _, _ := HeuristicRoute(objective, "")
		for i := 0; i < 100; i++ {
			got, _, _ := HeuristicRoute(objective, "")
			if got != first {
				t.Fatalf("non-deterministic route for %q: %q then %q at iteration %d", objective, first, got, i)
			}
		}
	}
}

func TestHeuristicRouteGeneralFallbackContract(t *testing.T) {
	cat, conf, rationale := HeuristicRoute("", "")
	if cat != WorkflowGeneral {
		t.Fatalf("empty objective route = %q, want %q", cat, WorkflowGeneral)
	}
	if conf != 0.70 {
		t.Fatalf("general confidence = %v, want 0.70", conf)
	}
	if rationale == "" {
		t.Fatal("general route has empty rationale")
	}
	chain := CanonicalToolChains[WorkflowGeneral]
	if len(chain.Tools) == 0 || len(chain.CLICommands) == 0 {
		t.Fatalf("general chain must expose tools and CLI commands: %+v", chain)
	}
}

func TestRouteWorkflowPopulatesCLICommands(t *testing.T) {
	rec := RouteWorkflow(context.Background(), "what have I been working on in the last 24 hours?", "", "test-proj", nil)
	if rec.Category != WorkflowTemporalActivity {
		t.Fatalf("category = %q, want temporal_activity", rec.Category)
	}
	if len(rec.CLICommands) == 0 {
		t.Fatal("heuristic route returned no CLI commands")
	}
	if !slices.Contains(rec.CLICommands, "sila standup \"last 24h\"") {
		t.Fatalf("missing standup CLI command: %v", rec.CLICommands)
	}

	mock := NewMockAdapter()
	jevRec := RouteWorkflow(context.Background(), "execute tasks under goal_execution roadmap", "", "test-proj", mock)
	if jevRec.Provider != "jev" {
		t.Fatalf("provider = %q, want jev", jevRec.Provider)
	}
	if len(jevRec.CLICommands) == 0 {
		t.Fatal("jev route returned no CLI commands")
	}
}
