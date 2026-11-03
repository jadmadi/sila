package assist

import (
	"context"
	"regexp"
	"strings"
	"time"
)

// Canonical workflow category constants.
const (
	WorkflowOnboarding               = "onboarding"
	WorkflowInvestigation            = "investigation"
	WorkflowGoalExecution            = "goal_execution"
	WorkflowGateAndVerification      = "gate_and_verification"
	WorkflowInstructionAndDrift      = "instruction_and_drift"
	WorkflowTemporalActivity         = "temporal_activity"
	WorkflowArchitectureAndDecisions = "architecture_and_decisions"
	WorkflowPersonaAndAudit          = "persona_and_audit"
	WorkflowSkillAndDomainLearning   = "skill_and_domain_learning"
	WorkflowSymbolAndBlastRadius     = "symbol_and_blast_radius"
	WorkflowGeneral                  = "general"
)

// workflowCategoryOrder is the deterministic priority order for heuristic
// classification. Equal scores resolve to the earlier category; never iterate
// a map when choosing the winner.
var workflowCategoryOrder = []string{
	WorkflowTemporalActivity,
	WorkflowSymbolAndBlastRadius,
	WorkflowArchitectureAndDecisions,
	WorkflowPersonaAndAudit,
	WorkflowSkillAndDomainLearning,
	WorkflowOnboarding,
	WorkflowInvestigation,
	WorkflowGateAndVerification,
	WorkflowInstructionAndDrift,
	WorkflowGoalExecution,
}

// WorkflowRecommendation represents an advisory toolchain suggestion tailored to a development objective.
type WorkflowRecommendation struct {
	Category    string   `json:"category"`
	Tools       []string `json:"recommended_tools"`
	CLICommands []string `json:"cli_commands"`
	Rationale   string   `json:"rationale"`
	Confidence  float64  `json:"confidence"`
	Provider    string   `json:"provider"` // "jev" or "heuristic"
	DurationMs  int64    `json:"duration_ms"`
}

// CanonicalToolChain defines the sequence of recommended Sila tools, the
// equivalent CLI commands, and their operational rationale.
type CanonicalToolChain struct {
	Tools       []string `json:"tools"`
	CLICommands []string `json:"cli_commands"`
	Rationale   string   `json:"rationale"`
}

// CanonicalToolChains maps each workflow category to its recommended tools and rationale.
var CanonicalToolChains = map[string]CanonicalToolChain{
	WorkflowOnboarding: {
		Tools:       []string{"salam", "prime_context", "get_status", "list_goals"},
		CLICommands: []string{"sila salam", "sila prime", "sila status", "sila goals ready"},
		Rationale:   "Orienting in the codebase, checking project status and discovering pending roadmap goals.",
	},
	WorkflowInvestigation: {
		Tools:       []string{"recall", "omni_search", "search_knowledge", "search_docs", "get_message_context"},
		CLICommands: []string{"sila recall \"<query>\"", "sila search \"<query>\" --omni", "sila search expand <session_id>", "sila doc search <query>"},
		Rationale:   "Researching past incidents, solutions, and historical conversations for troubleshooting.",
	},
	WorkflowGoalExecution: {
		Tools:       []string{"claim_goal", "prime_context", "touch_goal", "report_goal", "save_lesson"},
		CLICommands: []string{"sila goals claim <slug>", "sila prime", "sila goals touch <slug>", "sila lesson \"<symptom>\" --cause=... --fix=..."},
		Rationale:   "Claiming and executing structured roadmap goals with progress checkpoints.",
	},
	WorkflowGateAndVerification: {
		Tools:       []string{"get_project_facts", "run_gate_pipeline", "reconcile_drift", "audit_goals", "save_handoff"},
		CLICommands: []string{"sila facts verify", "sila gate --pre-commit", "sila goals audit <slug>", "sila goals", "sila handoff \"<summary>\" --open=\"...\""},
		Rationale:   "Verifying quality gates, repository hygiene, and checking goal completion criteria.",
	},
	WorkflowInstructionAndDrift: {
		Tools:       []string{"reconcile_drift", "sync_rules", "search_docs", "compile_rules"},
		CLICommands: []string{"sila heal", "sila agents sync", "sila doc search <query>"},
		Rationale:   "Detecting documentation/code drift, keeping rule files in sync, and validating documentation.",
	},
	WorkflowTemporalActivity: {
		Tools:       []string{"get_status", "get_activity_digest", "omni_search", "get_message_context"},
		CLICommands: []string{"sila standup \"last 24h\"", "sila digest --activity --since=24h", "sila recap --since=7d"},
		Rationale:   "Summarizing recent activity and time-windowed progress across sessions.",
	},
	WorkflowArchitectureAndDecisions: {
		Tools:       []string{"save_memo", "save_case", "create_decision", "compare_proposals", "record_preference"},
		CLICommands: []string{"sila memo \"<directive>\" --files=...", "sila case \"<intent>\" --approach=\"...\" --invariant=\"...\"", "sila decision \"<title>\"", "sila vision"},
		Rationale:   "Capturing architectural directives, design cases, and structured trade-off decisions.",
	},
	WorkflowPersonaAndAudit: {
		Tools:       []string{"audit_goal", "audit_goals", "consult_personas", "run_gate_pipeline"},
		CLICommands: []string{"sila persona list", "sila hire --goal=<slug>", "sila hire assess", "sila hire audit-goal <slug>", "sila audit"},
		Rationale:   "Assembling specialist personas and running adversarial, security, or compliance audits.",
	},
	WorkflowSkillAndDomainLearning: {
		Tools:       []string{"get_operational_guidebook", "save_domain_learning", "list_skills", "skill_search", "skill_load"},
		CLICommands: []string{"sila guide <domain>", "sila learn add --domain=... --title=... --symptom=... --fix=...", "sila learn distil --save", "sila skill search <query>", "sila skill load <name>"},
		Rationale:   "Consulting domain guidebooks, capturing grounded learnings, and discovering reusable skills.",
	},
	WorkflowSymbolAndBlastRadius: {
		Tools:       []string{"go_symbol_references", "go_package_api", "go_diagnostics", "check_symbol_blast_radius", "save_memo"},
		CLICommands: []string{"sila edit-check <symbol>", "sila safe-delete <symbol>", "sila note \"<warning>\" --symbol=<pkg::Sym>", "sila recall --symbol=<pkg::Sym>"},
		Rationale:   "Measuring symbol blast radius before renaming or deleting, and pinning symbol warnings.",
	},
	WorkflowGeneral: {
		Tools:       []string{"prime_context", "omni_search", "list_goals"},
		CLICommands: []string{"sila prime", "sila search \"<query>\" --omni", "sila goals ready"},
		Rationale:   "General repository interaction, contextual orientation, and task discovery.",
	},
}

var (
	reOnboarding   = regexp.MustCompile(`(?i)\b(onboard\w*|orient\w*|start\w*|hello|salam|hi|status|summary|brief\w*|session\w*|overview|first[- ]contact)\b`)
	reInvestigate  = regexp.MustCompile(`(?i)\b(investigat\w*|debug\w*|error\w*|bug\w*|fix\w*|why|fail\w*|trace\w*|search\w*|recall\w*|issue\w*|crash\w*|problem\w*|incident\w*|exception\w*|root[- ]cause|diagnos\w*)\b`)
	reGoal         = regexp.MustCompile(`(?i)\b(goal\w*|claim\w*|task\w*|execut\w*|implement\w*|roadmap\w*|progress\w*|feature\w*|build\w*|milestone\w*|deliverable\w*)\b`)
	reGate         = regexp.MustCompile(`(?i)\b(gate\w*|verif\w*|test\w*|check\w*|commit\w*|audit\w*|disciplin\w*|push\w*|pre-commit|pre-push|quality|clean\w*|assert\w*|scar[- ]tissue)\b`)
	reDrift        = regexp.MustCompile(`(?i)\b(drift\w*|rule\w*|sop\w*|agents\.md|instruction\w*|docs\w*|sync\w*|reconcil\w*|policy|policies|guideline\w*|stale|outdated)\b`)
	reTemporal     = regexp.MustCompile(`(?i)\b(standup|digest|recap|activity feed|timeline|what have i been working on|what did we work on|last\s+(?:24\s*h(?:ours?)?|week|day|month|night|session)|recent(?:ly)?|today|yesterday|this week)\b`)
	reArchDecision = regexp.MustCompile(`(?i)\b(architect\w*|decision\w*|memo\w*|adr\w*|trade[- ]?off\w*|rationale|why\s+did\s+we\s+(?:choose|pick|decide)|choose|chose|design\w*|proposal\w*|vision)\b`)
	rePersonaAudit = regexp.MustCompile(`(?i)\b(persona\w*|hire|squad|specialist\w*|audit\w*|security review|compliance|adversarial)\b`)
	reSkillLearn   = regexp.MustCompile(`(?i)\b(skill\w*|guide\w*|guidebook|learn\w*|domain standard\w*|capabilit\w*|workflow discov\w*)\b`)
	reSymbolBlast  = regexp.MustCompile(`(?i)\b(symbol\w*|blast[- ]?radius|rename\w*|refactor\w*|safe[- ]delete|edit[- ]check|references?|callers?|api surface|package api|diagnostic\w*)\b`)
)

// HeuristicRoute evaluates an objective and optional current tool to recommend a workflow deterministically.
func HeuristicRoute(objective, currentTool string) (string, float64, string) {
	combined := strings.TrimSpace(objective)
	if combined == "" && currentTool != "" {
		combined = currentTool
	}

	// Score every concrete category. General is the explicit fallback, never a
	// competitor: a zero score means "no signal", not a weak match.
	scores := map[string]int{
		WorkflowOnboarding:               len(reOnboarding.FindAllString(combined, -1)),
		WorkflowInvestigation:            len(reInvestigate.FindAllString(combined, -1)),
		WorkflowGoalExecution:            len(reGoal.FindAllString(combined, -1)),
		WorkflowGateAndVerification:      len(reGate.FindAllString(combined, -1)),
		WorkflowInstructionAndDrift:      len(reDrift.FindAllString(combined, -1)),
		WorkflowTemporalActivity:         len(reTemporal.FindAllString(combined, -1)),
		WorkflowArchitectureAndDecisions: len(reArchDecision.FindAllString(combined, -1)),
		WorkflowPersonaAndAudit:          len(rePersonaAudit.FindAllString(combined, -1)),
		WorkflowSkillAndDomainLearning:   len(reSkillLearn.FindAllString(combined, -1)),
		WorkflowSymbolAndBlastRadius:     len(reSymbolBlast.FindAllString(combined, -1)),
	}

	// Tool affinity boost if current_tool is provided
	switch currentTool {
	case "prime_context", "get_status", "salam":
		scores[WorkflowOnboarding] += 2
	case "search_knowledge", "recall", "get_message_context", "omni_search":
		scores[WorkflowInvestigation] += 2
	case "claim_goal", "touch_goal", "report_goal", "create_goal":
		scores[WorkflowGoalExecution] += 2
	case "run_gate_pipeline", "audit_goals":
		scores[WorkflowGateAndVerification] += 2
	case "reconcile_drift", "sync_rules", "compile_rules":
		scores[WorkflowInstructionAndDrift] += 2
	case "get_activity_digest", "standup", "digest", "recap":
		scores[WorkflowTemporalActivity] += 2
	case "save_memo", "save_case", "create_decision", "compare_proposals", "record_preference":
		scores[WorkflowArchitectureAndDecisions] += 2
	case "audit_goal", "consult_personas", "persona", "hire":
		scores[WorkflowPersonaAndAudit] += 2
	case "get_operational_guidebook", "learn", "skill_search", "skill_load", "list_skills":
		scores[WorkflowSkillAndDomainLearning] += 2
	case "go_symbol_references", "go_package_api", "go_diagnostics", "edit_check", "safe_delete", "note":
		scores[WorkflowSymbolAndBlastRadius] += 2
	}

	bestCat := WorkflowGeneral
	maxScore := 0
	for _, cat := range workflowCategoryOrder {
		if scores[cat] > maxScore {
			maxScore = scores[cat]
			bestCat = cat
		}
	}

	if maxScore == 0 {
		return WorkflowGeneral, 0.70, CanonicalToolChains[WorkflowGeneral].Rationale
	}

	confidence := 0.85
	if maxScore >= 2 {
		confidence = 0.95
	}

	chain := CanonicalToolChains[bestCat]
	return bestCat, confidence, chain.Rationale
}

// RouteWorkflow provides an advisory, low-latency workflow and toolchain recommendation
// powered by Jev TypeChoice when available, with strict fail-open heuristic fallback under 120ms.
func RouteWorkflow(ctx context.Context, objective, currentTool, project string, p Provider) WorkflowRecommendation {
	start := time.Now()

	// If no explicit provider was passed, look up active consented provider
	if p == nil {
		p = ActiveProviderFor("routing", project)
		if p == nil {
			p = ActiveProviderFor("route", project)
		}
	}

	// Attempt Jev TypeChoice evaluation if provider is available
	if p != nil && ctx.Err() == nil {
		evalCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
		defer cancel()

		state := map[string]any{
			"objective":    objective,
			"current_tool": currentTool,
		}

		criteria := map[string]string{
			WorkflowOnboarding:               "Orienting in the codebase, checking project status and discovering pending roadmap goals.",
			WorkflowInvestigation:            "Researching past incidents, solutions, and historical conversations for troubleshooting.",
			WorkflowGoalExecution:            "Claiming and executing structured roadmap goals with progress checkpoints.",
			WorkflowGateAndVerification:      "Verifying quality gates, repository hygiene, and checking goal completion criteria.",
			WorkflowInstructionAndDrift:      "Detecting documentation/code drift, keeping rule files in sync, and validating documentation.",
			WorkflowTemporalActivity:         "Summarizing recent activity and time-windowed progress across sessions.",
			WorkflowArchitectureAndDecisions: "Capturing architectural directives, design cases, and structured trade-off decisions.",
			WorkflowPersonaAndAudit:          "Assembling specialist personas and running adversarial, security, or compliance audits.",
			WorkflowSkillAndDomainLearning:   "Consulting domain guidebooks, capturing grounded learnings, and discovering reusable skills.",
			WorkflowSymbolAndBlastRadius:     "Measuring symbol blast radius before renaming or deleting, and pinning symbol warnings.",
		}

		questions := map[string]Question{
			"workflow": {
				Type:         TypeChoice,
				Instructions: "Classify the user's objective into the most suitable Sila workflow category.",
				Criteria:     criteria,
			},
		}

		resp, err := p.Evaluate(evalCtx, "", state, questions)
		if err == nil && resp != nil {
			if choice, ok := resp.Choices["workflow"]; ok && choice.Choice != "" && choice.Choice != "unknown" {
				if chain, exists := CanonicalToolChains[choice.Choice]; exists {
					conf := choice.Confidence
					if conf <= 0 {
						conf = 0.90
					}
					return WorkflowRecommendation{
						Category:    choice.Choice,
						Tools:       chain.Tools,
						CLICommands: chain.CLICommands,
						Rationale:   chain.Rationale,
						Confidence:  conf,
						Provider:    "jev",
						DurationMs:  time.Since(start).Milliseconds(),
					}
				}
			}
		}
	}

	// Fail-open fallback: deterministic keyword-based heuristic
	cat, conf, rationale := HeuristicRoute(objective, currentTool)
	chain := CanonicalToolChains[cat]
	if rationale == "" {
		rationale = chain.Rationale
	}

	return WorkflowRecommendation{
		Category:    cat,
		Tools:       chain.Tools,
		CLICommands: chain.CLICommands,
		Rationale:   rationale,
		Confidence:  conf,
		Provider:    "heuristic",
		DurationMs:  time.Since(start).Milliseconds(),
	}
}
