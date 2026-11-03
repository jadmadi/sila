package dream

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/store"
	"github.com/jadmadi/sila/internal/symbols"
)

// PrimeContext gathers the active handoff, top verified facts, and grounding stats for a project.
func PrimeContext(st *store.Store, projectSel, projectDir string, maxFacts int) (*PrimeSummary, error) {
	return BuildPrimeBrief(st, projectSel, projectDir, maxFacts, 0)
}

// BuildPrimeBrief gathers the active handoff, top verified facts, runbook, and grounding stats,
// applying declarative context compression and token pruning when a budget is set or exceeded.
func BuildPrimeBrief(st *store.Store, projectSel, projectDir string, maxFacts int, budget int) (*PrimeSummary, error) {
	return BuildPrimeBriefWithPressure(st, projectSel, projectDir, maxFacts, budget, "")
}

// BuildPrimeBriefWithPressure gathers the session brief applying multi-pressure context degradation.
func BuildPrimeBriefWithPressure(st *store.Store, projectSel, projectDir string, maxFacts int, budget int, forcedPressure store.WindowPressure) (*PrimeSummary, error) {
	return BuildPrimeBriefWithOptions(st, PrimeOptions{
		ProjectSel:      projectSel,
		ProjectDir:      projectDir,
		MaxFacts:        maxFacts,
		Budget:          budget,
		MaxTokens:       0,
		IncludeMarkdown: true,
		ForcedPressure:  forcedPressure,
	})
}

// BuildPrimeBriefWithOptions gathers session context with explicit envelope budgeting, bounded completed goals,
// and single canonical representation selection.
func BuildPrimeBriefWithOptions(st *store.Store, opts PrimeOptions) (*PrimeSummary, error) {
	maxFacts := opts.MaxFacts
	if maxFacts <= 0 {
		maxFacts = 5
	}

	maxTokens := opts.MaxTokens
	compBudget := opts.Budget
	if compBudget <= 0 && maxTokens > 0 {
		compBudget = maxTokens
	}

	projectDir := opts.ProjectDir
	if projectDir == "" {
		cwd, err := os.Getwd()
		if err == nil {
			projectDir = cwd
		}
	}

	projectSel := opts.ProjectSel
	resolvedDir := projectDir
	if projectSel != "" {
		if st != nil {
			p, err := st.ResolveScope(projectSel, projectDir)
			if err != nil {
				return nil, err
			}
			projectSel = p.ID
			if p.Path != "" {
				resolvedDir = p.Path
			}
		}
	} else {
		identity, err := store.ResolveWorkspaceIdentity(projectDir)
		if err != nil {
			return nil, err
		}
		resolvedDir = identity.ActiveDir
		if st != nil {
			p, err := st.ResolveScope("", resolvedDir)
			if err == nil && p.ID != "" {
				projectSel = p.ID
				if p.Path != "" {
					resolvedDir = p.Path
				}
			} else {
				projectSel = identity.ProjectID
			}
		} else {
			projectSel = identity.ProjectID
		}
	}

	gitCtx := DetectGitContext(resolvedDir)
	if projectSel == "" {
		if gitCtx.RootPath != "" {
			projectSel = store.ProjectID(gitCtx.RootPath)
		} else if resolvedDir != "" {
			projectSel = store.ProjectID(resolvedDir)
		}
	}

	// 1. Initial grounding totals and static context measurement
	ctxBudget := st.MeasureContextBudget(resolvedDir, nil)
	totalVerified := 0
	totalClaims := 0
	allFacts, err := st.FactsByProject(projectSel)
	if err == nil {
		totalClaims = len(allFacts)
		for _, f := range allFacts {
			if f.FileRefsExist || f.Verified {
				totalVerified++
			}
		}
	}

	// 2. Measure immutable metadata envelope reservation before content selection
	baseEnvelope := &PrimeSummary{
		ProjectID:           projectSel,
		ProjectDir:          resolvedDir,
		Git:                 gitCtx,
		TotalVerified:       totalVerified,
		TotalClaims:         totalClaims,
		ContextBudget:       ctxBudget,
		CompletedGoalsRoute: "sila goals list --status=implemented",
	}
	baseBytes, _ := json.Marshal(baseEnvelope)
	reservedMetadataTokens := store.EstimateTokens(len(baseBytes))
	if reservedMetadataTokens < 75 {
		reservedMetadataTokens = 75
	}
	minRequiredTokens := reservedMetadataTokens + 50

	// 3. Return explicit budget-too-small result if max-tokens cannot fit the immutable envelope
	if maxTokens > 0 && maxTokens < minRequiredTokens {
		return &PrimeSummary{
			ProjectID:           projectSel,
			ProjectDir:          resolvedDir,
			Git:                 gitCtx,
			ContextBudget:       ctxBudget,
			TotalVerified:       totalVerified,
			TotalClaims:         totalClaims,
			CompletedGoalsCount: 0,
			CompletedGoalsRoute: "sila goals list --status=implemented",
			BudgetMetrics: &PrimeBudgetMetrics{
				Estimator:              EstimatorDocumentation,
				MaxTokens:              maxTokens,
				ReservedMetadataTokens: reservedMetadataTokens,
				MinimumRequiredTokens:  minRequiredTokens,
				AlwaysLoadedTokens:     ctxBudget.TotalTokens,
				FitsBudget:             false,
				BudgetTooSmall:         true,
			},
			Error: fmt.Sprintf("budget_too_small: requested max_tokens (%d) is smaller than required immutable metadata envelope (%d tokens); minimum budget is %d tokens", maxTokens, reservedMetadataTokens, minRequiredTokens),
		}, nil
	}

	summary := &PrimeSummary{
		ProjectID:     projectSel,
		ProjectDir:    resolvedDir,
		Git:           gitCtx,
		TotalVerified: totalVerified,
		TotalClaims:   totalClaims,
		ContextBudget: ctxBudget,
	}

	// 4. Detect runbook in selected project
	summary.Runbook = DetectRunbook(resolvedDir)

	// 5. Goals aggregation and bounding
	var allCompleted []goals.Goal
	if goalsRoot := goals.FindGoalsRoot(resolvedDir); goalsRoot != "" {
		if audit, err := goals.ScanGoals(goalsRoot); err == nil && audit != nil {
			for i := range audit.Goals {
				g := &audit.Goals[i]
				if g.Claim != nil && summary.ActiveGoal == nil {
					summary.ActiveGoal = g
					if vc, err := goals.LoadContract(goalsRoot, g.Slug); err == nil && vc != nil {
						summary.ActiveContract = vc
					}
				} else if g.Status == "implemented" {
					allCompleted = append(allCompleted, *g)
				}
			}
		}
	}
	summary.CompletedGoalsCount = len(allCompleted)
	summary.CompletedGoalsRoute = "sila goals list --status=implemented"

	if opts.AllGoals {
		summary.CompletedGoals = allCompleted
	} else {
		// Bounded: top 5 recent completed goals by default
		limit := 5
		if len(allCompleted) < limit {
			limit = len(allCompleted)
		}
		summary.CompletedGoals = allCompleted[:limit]
	}

	// 6. Handoff fetching

	// Handoff fetching
	handoff, err := st.LatestHandoffForBranch(projectSel, gitCtx.Branch)
	if err == nil && handoff != nil {
		if len(handoff.RepoFiles) > 0 && resolvedDir != "" {
			allExist := true
			for _, rf := range handoff.RepoFiles {
				if !projectPathExists(resolvedDir, rf) {
					allExist = false
					break
				}
			}
			handoff.FileRefsExist = allExist
		}
		if stale, reason := handoffIsBehindHEAD(resolvedDir, gitCtx.Commit, handoff); stale {
			handoff.Stale = true
			handoff.StaleReason = reason
		}
		summary.ActiveHandoff = handoff
	}

	// Fetch top facts
	facts, err := st.TopFactsForProject(projectSel, maxFacts)
	if err == nil {
		summary.TopFacts = facts
	}

	// Surface inferred candidates separately, never as trusted facts
	candidates, err := st.CandidateFacts(projectSel, 3)
	if err == nil && len(candidates) > 0 {
		summary.Candidates = candidates
	}

	lessons, err := st.LessonFacts(projectSel, 3)
	if err == nil && len(lessons) > 0 {
		summary.Lessons = lessons
	}

	cases, err := st.CaseFacts(projectSel, 3)
	if err == nil && len(cases) > 0 {
		summary.Cases = cases
	}

	docs, err := st.DocsByProject(projectSel, 3)
	if err == nil && len(docs) > 0 {
		summary.AttachedDocs = docs
	}

	vFacts, err := st.VisionFacts(projectSel)
	if err == nil {
		for _, vf := range vFacts {
			if vf.IsNonGoal() {
				summary.VisionNonGoals = append(summary.VisionNonGoals, vf.Claim)
				if len(summary.VisionNonGoals) >= 3 {
					break
				}
			}
		}
	}

	// Update ContextBudget with active handoff
	summary.ContextBudget = st.MeasureContextBudget(resolvedDir, summary.ActiveHandoff)

	// Build token-budgeted codebase symbol map if enabled and project directory is available
	if !opts.DisableCodebaseMap && resolvedDir != "" && opts.ForcedPressure != store.PressureCritical {
		mapChars := opts.CodebaseMapChars
		if mapChars <= 0 {
			mapChars = 2000
		}
		cbMap, err := symbols.BuildCodebaseMap(symbols.MapOptions{
			RootPath: resolvedDir,
			MaxChars: mapChars,
		})
		if err == nil && cbMap != nil && len(cbMap.Directories) > 0 {
			summary.CodebaseMap = cbMap
		}
	}

	// 7. Format markdown briefing and apply compression & pressure shrinking
	renderedMD := FormatPrimeMarkdown(summary)
	compOpts := DefaultCompressionOptions()
	compOpts.TargetBudget = compBudget
	comp := CompressContext(renderedMD, compOpts)
	summary.Compression = &comp
	mdToUse := renderedMD
	if comp.PrunedTokens > 0 {
		mdToUse = comp.CompressedText
		if !strings.Contains(mdToUse, "**Context Compression**") {
			mdToUse += fmt.Sprintf("\n**Context Compression**: %s\n", comp.FormatCompressionSummary())
		}
	}

	shrinkPressure := opts.ForcedPressure
	if shrinkPressure == "" {
		origToks := store.EstimateTokens(len(mdToUse))
		shrinkPressure = store.DeterminePressure(origToks)
		if compBudget >= store.PressureThresholdNormal && origToks > compBudget {
			if compBudget <= store.PressureThresholdWatch {
				shrinkPressure = store.PressureHeavy
			} else {
				shrinkPressure = store.PressureWatch
			}
		}
	}
	shrinkRes := store.ShrinkBriefing(context.Background(), 0, mdToUse, shrinkPressure)
	summary.Shrink = &shrinkRes
	if shrinkRes.Pressure != store.PressureNormal && shrinkRes.Payload != "" {
		mdToUse = shrinkRes.Payload
		if comp.PrunedTokens > 0 && !strings.Contains(mdToUse, "**Context Compression**") {
			mdToUse += fmt.Sprintf("\n**Context Compression**: %s\n", comp.FormatCompressionSummary())
		}
	}

	// By default, machine-readable JSON omits duplicated full markdown unless explicitly selected
	if opts.IncludeMarkdown {
		summary.Markdown = mdToUse
	} else {
		summary.Markdown = ""
		if summary.Compression != nil {
			summary.Compression.CompressedText = ""
		}
		if summary.Shrink != nil {
			summary.Shrink.Payload = ""
		}
	}

	// 8. Measure complete serialized context and bound content to strictly fit max-tokens
	rawJSON, _ := json.Marshal(summary)
	serializedTokens := store.EstimateTokens(len(rawJSON))

	if maxTokens > 0 && serializedTokens > maxTokens {
		if summary.CodebaseMap != nil {
			summary.CodebaseMap = nil
			rawJSON, _ = json.Marshal(summary)
			serializedTokens = store.EstimateTokens(len(rawJSON))
		}
		if len(summary.Candidates) > 0 {
			summary.Candidates = nil
			rawJSON, _ = json.Marshal(summary)
			serializedTokens = store.EstimateTokens(len(rawJSON))
		}
		if serializedTokens > maxTokens && len(summary.AttachedDocs) > 0 {
			summary.AttachedDocs = nil
			rawJSON, _ = json.Marshal(summary)
			serializedTokens = store.EstimateTokens(len(rawJSON))
		}
		if serializedTokens > maxTokens && len(summary.VisionNonGoals) > 0 {
			summary.VisionNonGoals = nil
			rawJSON, _ = json.Marshal(summary)
			serializedTokens = store.EstimateTokens(len(rawJSON))
		}
		if serializedTokens > maxTokens && len(summary.Cases) > 0 {
			summary.Cases = nil
			rawJSON, _ = json.Marshal(summary)
			serializedTokens = store.EstimateTokens(len(rawJSON))
		}
		if serializedTokens > maxTokens && len(summary.Lessons) > 0 {
			summary.Lessons = nil
			rawJSON, _ = json.Marshal(summary)
			serializedTokens = store.EstimateTokens(len(rawJSON))
		}
		if serializedTokens > maxTokens && len(summary.TopFacts) > 2 {
			summary.TopFacts = summary.TopFacts[:2]
			rawJSON, _ = json.Marshal(summary)
			serializedTokens = store.EstimateTokens(len(rawJSON))
		}
		if serializedTokens > maxTokens && summary.ActiveHandoff != nil && len(summary.ActiveHandoff.OpenItems) > 2 {
			hCopy := *summary.ActiveHandoff
			hCopy.OpenItems = hCopy.OpenItems[:2]
			summary.ActiveHandoff = &hCopy
			rawJSON, _ = json.Marshal(summary)
			serializedTokens = store.EstimateTokens(len(rawJSON))
		}
		if serializedTokens > maxTokens && len(summary.CompletedGoals) > 1 {
			summary.CompletedGoals = summary.CompletedGoals[:1]
			rawJSON, _ = json.Marshal(summary)
			serializedTokens = store.EstimateTokens(len(rawJSON))
		}
		if serializedTokens > maxTokens && len(summary.TopFacts) > 1 {
			summary.TopFacts = summary.TopFacts[:1]
			rawJSON, _ = json.Marshal(summary)
			serializedTokens = store.EstimateTokens(len(rawJSON))
		}
		if serializedTokens > maxTokens && len(summary.CompletedGoals) > 0 {
			summary.CompletedGoals = nil
			rawJSON, _ = json.Marshal(summary)
			serializedTokens = store.EstimateTokens(len(rawJSON))
		}
		if serializedTokens > maxTokens && len(summary.TopFacts) > 0 {
			summary.TopFacts = nil
			rawJSON, _ = json.Marshal(summary)
			serializedTokens = store.EstimateTokens(len(rawJSON))
		}
		if serializedTokens > maxTokens && summary.ActiveHandoff != nil && len(summary.ActiveHandoff.OpenItems) > 0 {
			hCopy := *summary.ActiveHandoff
			hCopy.OpenItems = nil
			summary.ActiveHandoff = &hCopy
			rawJSON, _ = json.Marshal(summary)
			serializedTokens = store.EstimateTokens(len(rawJSON))
		}
		if serializedTokens > maxTokens && summary.Runbook != nil {
			summary.Runbook = nil
			rawJSON, _ = json.Marshal(summary)
			serializedTokens = store.EstimateTokens(len(rawJSON))
		}
		if serializedTokens > maxTokens && summary.Shrink != nil {
			summary.Shrink = nil
			rawJSON, _ = json.Marshal(summary)
			serializedTokens = store.EstimateTokens(len(rawJSON))
		}
		if serializedTokens > maxTokens && summary.Markdown != "" {
			summary.Markdown = FormatPrimeMarkdown(summary)
			rawJSON, _ = json.Marshal(summary)
			serializedTokens = store.EstimateTokens(len(rawJSON))
		}
		if serializedTokens > maxTokens && summary.Markdown != "" {
			availTokens := maxTokens - (serializedTokens - store.EstimateTokens(len(summary.Markdown)))
			if availTokens > 50 {
				availBytes := availTokens * 4
				if availBytes < len(summary.Markdown) {
					summary.Markdown = summary.Markdown[:availBytes] + "\n..."
				}
			} else {
				summary.Markdown = ""
			}
			rawJSON, _ = json.Marshal(summary)
			serializedTokens = store.EstimateTokens(len(rawJSON))
		}
	}

	contentTokens := serializedTokens - reservedMetadataTokens
	if contentTokens < 0 {
		contentTokens = 0
	}

	fits := maxTokens <= 0 || serializedTokens <= maxTokens
	tooSmall := false
	if maxTokens > 0 && !fits {
		tooSmall = true
		summary.Error = fmt.Sprintf("budget_too_small: serialized payload (%d tokens) exceeds max_tokens (%d tokens); minimum required envelope is %d tokens", serializedTokens, maxTokens, minRequiredTokens)
	}

	effectiveBudget := maxTokens
	if effectiveBudget <= 0 {
		effectiveBudget = compBudget
	}

	summary.BudgetMetrics = &PrimeBudgetMetrics{
		Estimator:              EstimatorDocumentation,
		MaxTokens:              effectiveBudget,
		ReservedMetadataTokens: reservedMetadataTokens,
		ContentTokens:          contentTokens,
		SerializedTotalTokens:  serializedTokens,
		AlwaysLoadedTokens:     summary.ContextBudget.TotalTokens,
		FitsBudget:             fits,
		BudgetTooSmall:         tooSmall,
		MinimumRequiredTokens:  minRequiredTokens,
	}

	return summary, nil
}

// FormatPrimeMarkdown formats the session priming summary as an LLM-optimized Markdown briefing.
func FormatPrimeMarkdown(p *PrimeSummary) string {
	var b strings.Builder
	title := p.ProjectID
	if title == "" {
		title = "global"
	}
	b.WriteString(fmt.Sprintf("### 🧠 Project Memory (sila · project: %s)\n\n", title))

	b.WriteString("**Working Contract (9-Verb Memory Loop)**:\n")
	b.WriteString("1. **Orient & Prime**: `sila heal` → `sila prime` (MCP `reconcile_drift` → `prime_context`) → `sila goals ready` and `sila goals claim <slug>` (MCP `list_goals` → `claim_goal`).\n")
	b.WriteString("2. **Recall & Ground**: `sila recall \"<query>\"` before searching (MCP `recall`); `sila doc search <query>` before integrations (MCP `search_docs`).\n")
	b.WriteString("3. **Record Knowledge**: `sila memo \"<directive>\"` (MCP `save_memo`), `sila lesson \"<symptom>\" --cause=... --fix=...` (MCP `save_lesson`), `sila case \"<intent>\" --approach=... --invariant=...` (MCP `save_case`).\n")
	b.WriteString("4. **Verify & Gate**: `sila facts verify` (MCP `get_project_facts` / `reconcile_drift`) before closure; `sila gate` (MCP `run_gate_pipeline`) before commit.\n")
	b.WriteString("5. **Close & Handoff**: commit `feat: ... (goals/<slug>/goal.md)` → `sila goals` (MCP `audit_goals`) → `sila handoff \"<summary>\"` (MCP `save_handoff`).\n")
	b.WriteString("6. **Report Back**: goal slug, commit SHA, recorded lessons, store enrichments; friction → `sila feedback` (MCP `record_feedback`).\n")
	b.WriteString("**Capability Triggers**:\n")
	b.WriteString("- History/why → `sila search \"<query>\" --omni` (MCP `omni_search`), `sila search expand <id>` (MCP `get_message_context`).\n")
	b.WriteString("- Activity window → `sila standup \"last 24h\"` / `sila digest --activity` / `sila recap`.\n")
	b.WriteString("- Domain/Persona/Skill → `sila guide <domain>` (MCP `get_operational_guidebook`), `sila persona list`, `sila hire --goal=<slug>`, `sila skill search <query>` (MCP `skill_search`), `sila learn`.\n")
	b.WriteString("- Pre-edit contract → `sila edit-check <symbol>`, `sila safe-delete <symbol>`, `sila note --symbol=<sym>`.\n")
	b.WriteString("_Note: If `sila` CLI is not in `$PATH` (subshell exit 127), invoke via `~/.local/bin/sila` or call Sila MCP tools directly._\n\n")

	if p.ActiveHandoff != nil {
		h := p.ActiveHandoff
		agentName := h.Agent
		if agentName == "" {
			agentName = "agent"
		}
		timeDesc := h.CreatedAt
		if t, err := time.Parse(time.RFC3339, h.CreatedAt); err == nil {
			timeDesc = t.Format("2006-01-02 15:04")
		}
		branchPart := ""
		if h.Branch != "" {
			branchPart = fmt.Sprintf(" on `%s`", h.Branch)
		}
		stalePart := ""
		if h.Stale {
			reason := h.StaleReason
			if reason == "" {
				reason = "behind HEAD"
			}
			stalePart = fmt.Sprintf(" [STALE: %s]", reason)
		}
		b.WriteString(fmt.Sprintf("**Active Session Handoff%s** (by %s, %s%s):\n", stalePart, agentName, timeDesc, branchPart))
		b.WriteString("> [!NOTE] Inbound Handoff Baton (Untrusted Cross-Agent Context): Evaluate tasks against current repository state.\n>\n")
		b.WriteString(fmt.Sprintf("- **Summary**: %s\n", h.Summary))
		if len(h.RepoFiles) > 0 {
			if h.FileRefsExist {
				b.WriteString(fmt.Sprintf("- **Referenced Files** (verified on disk): `%s`\n", strings.Join(h.RepoFiles, "`, `")))
			} else {
				b.WriteString(fmt.Sprintf("- **Referenced Files** (unverified or missing on disk): `%s`\n", strings.Join(h.RepoFiles, "`, `")))
			}
		}
		if len(h.OpenItems) > 0 {
			b.WriteString("- **Tasks & Open Items**:\n")
			for _, item := range h.OpenItems {
				item = strings.TrimSpace(item)
				if !strings.HasPrefix(item, "-") && !strings.HasPrefix(item, "*") && !strings.HasPrefix(item, "[") {
					item = "- [ ] " + item
				}
				b.WriteString(fmt.Sprintf("  %s\n", item))
			}
		}
		if len(h.Decisions) > 0 {
			b.WriteString("- **Decisions**:\n")
			for _, d := range h.Decisions {
				b.WriteString(fmt.Sprintf("  • %s\n", d))
			}
		}
		b.WriteString("\n")
	}

	if p.ActiveGoal != nil {
		g := p.ActiveGoal
		b.WriteString("**Active Goal Specification & Acceptance Contract**:\n")
		b.WriteString(fmt.Sprintf("• 🎯 **%s**: %s\n", g.Slug, g.Title))
		if g.Claim != nil {
			agent := g.Claim.Agent
			if agent == "" {
				agent = g.Claim.ClaimedBy
			}
			b.WriteString(fmt.Sprintf("  - Assigned: `%s`\n", agent))
		}
		if p.ActiveContract != nil && p.ActiveContract.Command != "" {
			b.WriteString(fmt.Sprintf("  - Verification Command: `%s`\n", p.ActiveContract.Command))
		}
		if len(g.Personas) > 0 {
			b.WriteString(fmt.Sprintf("  - Hired Squad: `%s` (run `sila hire audit-goal %s` before closing)\n", strings.Join(g.Personas, "`, `"), g.Slug))
		}
		b.WriteString("\n")
	}

	if p.CompletedGoalsCount > 0 || len(p.CompletedGoals) > 0 {
		b.WriteString("**Completed Roadmap Goals**:\n")
		for _, g := range p.CompletedGoals {
			vTag := ""
			if g.VerificationStatus != "" {
				vTag = fmt.Sprintf(" [%s]", g.VerificationStatus)
			}
			b.WriteString(fmt.Sprintf("• ✔ `%s`%s: %s\n", g.Slug, vTag, g.Title))
		}
		if p.CompletedGoalsCount > len(p.CompletedGoals) {
			route := p.CompletedGoalsRoute
			if route == "" {
				route = "sila goals list --status=implemented"
			}
			b.WriteString(fmt.Sprintf("• ... and %d earlier completed goals (run `%s` to view all).\n", p.CompletedGoalsCount-len(p.CompletedGoals), route))
		}
		b.WriteString("\n")
	}

	if p.Runbook != nil && len(p.Runbook.Entries) > 0 {
		b.WriteString("**Discovered Repo Runbook (Guidance)**:\n")
		for _, e := range p.Runbook.Entries {
			src := ""
			if e.Source != "" {
				src = fmt.Sprintf(" *(source: %s)*", e.Source)
			}
			b.WriteString(fmt.Sprintf("• **%s**: `%s`%s\n", e.Phase, e.Command, src))
		}
		b.WriteString("\n")
	}

	if p.CodebaseMap != nil && p.CodebaseMap.Formatted != "" {
		b.WriteString(p.CodebaseMap.Formatted)
		b.WriteString("\n")
	}

	if len(p.TopFacts) > 0 {
		// Authority is not uniform across TopFacts: invariants, explicit
		// memos, and distilled facts are knowledge, the rest are claims that
		// merely reference real files. Labeling the second group as "Facts"
		// would present raw conversational text as verified architecture, so
		// it gets its own section and a marker.
		var facts []store.Fact
		var claims []store.Fact
		for _, f := range p.TopFacts {
			if f.IsInvariant() || f.IsManualMemo() || f.IsFact() {
				facts = append(facts, f)
			} else {
				claims = append(claims, f)
			}
		}
		if len(facts) > 0 {
			b.WriteString("**Key Architectural Facts & Decisions**:\n")
			for _, f := range facts {
				b.WriteString(renderFactLine(f))
			}
			b.WriteString("\n")
		}
		if len(claims) > 0 {
			b.WriteString("**File-Referenced Claims** (observed in sessions, not distilled — verify before trusting):\n")
			for _, f := range claims {
				b.WriteString(renderFactLine(f))
			}
			b.WriteString("\n")
		}
	}

	if len(p.Lessons) > 0 {
		b.WriteString("**Solved Before (solution lessons)**:\n")
		for _, f := range p.Lessons {
			b.WriteString(lessonLine(f))
		}
		b.WriteString("\n")
	}

	if len(p.Cases) > 0 {
		b.WriteString("**Architectural Cases (procedural patterns)**:\n")
		for _, f := range p.Cases {
			b.WriteString(caseLine(f))
		}
		b.WriteString("\n")
	}

	if len(p.Candidates) > 0 {
		b.WriteString("**Candidate Directives (observed, NOT approved — review before treating as rules)**:\n")
		for _, f := range p.Candidates {
			text := f.Claim
			ctx := ""
			if f.Quoted {
				ctx += " · quoted"
			}
			if f.OriginRole != "" {
				ctx += fmt.Sprintf(" · role:%s", f.OriginRole)
			}
			b.WriteString(fmt.Sprintf("• ◻ %s _[%s%s]_\n", text, strings.Join(f.Tools, ","), ctx))
		}
		b.WriteString("\n")
	}

	if len(p.VisionNonGoals) > 0 {
		b.WriteString("**Strategic Guardrails (Explicit Non-Goals)**:\n")
		for _, ng := range p.VisionNonGoals {
			claim := cleanPrefix(ng, "non-goal:")
			claim = cleanPrefix(claim, "nongoal:")
			b.WriteString(fmt.Sprintf("• ⛔ %s\n", strings.TrimSpace(claim)))
		}
		b.WriteString("\n")
	}

	if len(p.AttachedDocs) > 0 {
		b.WriteString("**Attached Reference Docs & Sources**:\n")
		for _, d := range p.AttachedDocs {
			urlPart := ""
			if d.URL != "" {
				urlPart = fmt.Sprintf(" (%s)", d.URL)
			}
			b.WriteString(fmt.Sprintf("• **%s**%s\n", d.Title, urlPart))
		}
		b.WriteString("\n")
	}

	b.WriteString(fmt.Sprintf("**Disk Grounding**: %d/%d claims verified with existing files on disk.\n", p.TotalVerified, p.TotalClaims))
	if p.ContextBudget.TotalTokens > 0 {
		b.WriteString(fmt.Sprintf("**Context Surface**: %s\n", p.ContextBudget.FormatSummary()))
		if len(p.ContextBudget.TrimTargets) > 0 {
			for _, target := range p.ContextBudget.TrimTargets {
				b.WriteString(fmt.Sprintf("  ▲ _Trim target_: %s\n", target))
			}
		}
	}
	return b.String()
}

// renderFactLine formats one fact or file-referenced claim for the prime
// briefing: distilled text when the store distilled one, the raw claim
// otherwise, then the files it references and the tools that observed it.
func renderFactLine(f store.Fact) string {
	text := f.Distilled
	if text == "" {
		text = f.Claim
	}
	tools := strings.Join(f.Tools, ",")
	fileNote := ""
	if len(f.RepoFiles) > 0 {
		fileNote = fmt.Sprintf(" (`%s`)", strings.Join(f.RepoFiles, ", "))
	}
	return fmt.Sprintf("• %s%s _[%s]_\n", text, fileNote, tools)
}

// lessonLine renders one solution lesson compactly for the prime briefing.
func lessonLine(f store.Fact) string {
	symptom, cause, fix, _ := store.ParseLesson(f.Claim)
	line := "• 💡 " + symptom
	if cause != "" {
		line += " · cause: " + cause
	}
	if fix != "" {
		line += " · fix: " + fix
	}
	if f.FileRefsExist || f.Verified {
		line += " · grounded"
	}
	return line + "\n"
}

// caseLine formats one architectural procedural case for the prime briefing.
func caseLine(f store.Fact) string {
	intent, approach, invariant, _ := store.ParseCase(f.Claim)
	line := "• 🏛️ " + intent
	if approach != "" {
		line += " · approach: " + approach
	}
	if invariant != "" {
		line += " · invariant: " + invariant
	}
	if f.FileRefsExist || f.Verified {
		line += " · grounded"
	}
	return line + "\n"
}

var handoffSummaryHEADRE = regexp.MustCompile(`\bHEAD\s+([0-9a-f]{7,40})\b`)

func projectPathExists(projectRoot, relPath string) bool {
	if strings.TrimSpace(projectRoot) == "" || strings.TrimSpace(relPath) == "" {
		return false
	}
	cleanRoot, err := filepath.Abs(filepath.Clean(projectRoot))
	if err != nil {
		return false
	}
	realRoot, err := filepath.EvalSymlinks(cleanRoot)
	if err != nil {
		realRoot = cleanRoot
	}
	cleanRel := filepath.Clean(relPath)
	if filepath.IsAbs(cleanRel) {
		if !strings.HasPrefix(cleanRel, cleanRoot+string(filepath.Separator)) && cleanRel != cleanRoot {
			return false
		}
	} else if strings.HasPrefix(cleanRel, ".."+string(filepath.Separator)) || cleanRel == ".." {
		return false
	}
	targetPath := filepath.Join(realRoot, cleanRel)
	realTarget, err := filepath.EvalSymlinks(targetPath)
	if err != nil {
		return false
	}
	rootWithSep := realRoot
	if !strings.HasSuffix(rootWithSep, string(filepath.Separator)) {
		rootWithSep += string(filepath.Separator)
	}
	if !strings.HasPrefix(realTarget, rootWithSep) && realTarget != realRoot {
		return false
	}
	_, err = os.Stat(realTarget)
	return err == nil
}

func handoffIsBehindHEAD(projectRoot, currentHEAD string, h *store.Handoff) (bool, string) {
	if h == nil || strings.TrimSpace(currentHEAD) == "" {
		return false, ""
	}
	head := strings.TrimSpace(currentHEAD)
	if m := handoffSummaryHEADRE.FindStringSubmatch(h.Summary); len(m) == 2 {
		refSHA := m[1]
		if !strings.HasPrefix(head, refSHA) && !strings.HasPrefix(refSHA, head) {
			return true, fmt.Sprintf("handoff HEAD %s is behind current HEAD %s", refSHA, head)
		}
	}
	if strings.TrimSpace(projectRoot) != "" && strings.TrimSpace(h.CreatedAt) != "" {
		if handoffTime, err := time.Parse(time.RFC3339, strings.TrimSpace(h.CreatedAt)); err == nil {
			out, err := exec.Command("git", "-C", projectRoot, "log", "-1", "--format=%cI", "HEAD").Output()
			if err == nil {
				if headTime, err := time.Parse(time.RFC3339, strings.TrimSpace(string(out))); err == nil {
					if headTime.After(handoffTime.Add(time.Second)) {
						return true, fmt.Sprintf("behind current HEAD %s", head)
					}
				}
			}
		}
	}
	return false, ""
}
