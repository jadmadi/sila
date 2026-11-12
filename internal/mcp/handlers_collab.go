package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/assist"
	"github.com/jadmadi/sila/internal/config"
	"github.com/jadmadi/sila/internal/crew"
	"github.com/jadmadi/sila/internal/dream"
	"github.com/jadmadi/sila/internal/eval"
	"github.com/jadmadi/sila/internal/feedback"
	"github.com/jadmadi/sila/internal/gateway"
	"github.com/jadmadi/sila/internal/git"
	"github.com/jadmadi/sila/internal/learn"
	"github.com/jadmadi/sila/internal/salam"
	"github.com/jadmadi/sila/internal/store"
)

func (s *Server) handleCollabTool(req JSONRPCRequest, name string, rawArgs json.RawMessage) bool {
	switch name {
	case "record_feedback":
		var args struct {
			ID       string `json:"id"`
			Content  string `json:"content"`
			Category string `json:"category"`
			Project  string `json:"project"`
			Agent    string `json:"agent"`
			FactID   string `json:"fact_id"`
			Share    bool   `json:"share"`
		}
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return true
		}
		if args.Content == "" {
			s.sendError(req.ID, -32602, "Invalid params", "content is required")
			return true
		}
		cat := "idea"
		if args.Category != "" {
			cat = strings.ToLower(strings.TrimSpace(args.Category))
		}
		agent := "agent"
		if args.Agent != "" {
			agent = args.Agent
		}
		projectID, _ := s.resolveTargetProject(args.Project)
		fb := &store.Feedback{
			ID:        args.ID,
			ProjectID: projectID,
			Agent:     agent,
			Category:  cat,
			Content:   args.Content,
			FactID:    args.FactID,
		}
		if err := s.store.SaveFeedback(fb); err != nil {
			s.sendError(req.ID, -32603, "Internal error", err.Error())
			return true
		}
		var extraMsg string
		if args.Share {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			res, err := feedback.SubmitToEdge(ctx, "", fb)
			if err == nil && res != nil {
				extraMsg = fmt.Sprintf(" (shared to edge: %s)", res.ID)
				_ = s.store.UpdateFeedbackNote(fb.ID, "Shared to Sila edge: "+res.ID)
			}
		}
		s.sendToolResult(req.ID, fb, fmt.Sprintf("Feedback recorded successfully (id: %s, category: %s)%s", fb.ID, fb.Category, extraMsg))
		return true

	case "list_feedback":
		var args struct {
			Project  string `json:"project"`
			Category string `json:"category"`
			Status   string `json:"status"`
			Limit    int    `json:"limit"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		projectID := args.Project
		if projectID != "" {
			if resolved, err := s.resolveTargetProject(projectID); err == nil {
				projectID = resolved
			}
		}
		items, err := s.store.QueryFeedback(store.FeedbackFilter{
			ProjectID: projectID,
			Category:  args.Category,
			Status:    args.Status,
			Limit:     clampLimit(args.Limit, 0, 200),
		})
		if err != nil {
			s.sendError(req.ID, -32603, "Internal error", err.Error())
			return true
		}
		formatted, _ := json.MarshalIndent(items, "", "  ")
		s.sendToolResult(req.ID, items, string(formatted))
		return true

	case "resolve_feedback":
		var args struct {
			ID    string `json:"id"`
			Agent string `json:"agent"`
			Goal  string `json:"goal"`
			Note  string `json:"note"`
		}
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return true
		}
		if args.ID == "" {
			s.sendError(req.ID, -32602, "Invalid params", "id is required")
			return true
		}
		effAgent := args.Agent
		if effAgent == "" {
			effAgent = "agent"
		}
		if err := s.store.ResolveFeedback(args.ID, effAgent, args.Goal, args.Note); err != nil {
			s.sendError(req.ID, -32603, "Internal error", err.Error())
			return true
		}
		fb, err := s.store.GetFeedback(args.ID)
		if err != nil {
			s.sendError(req.ID, -32603, "Internal error", err.Error())
			return true
		}
		s.sendToolResult(req.ID, fb, fmt.Sprintf("Feedback %s marked closed and resolved", fb.ID))
		return true

	case "compare_proposals":
		var args struct {
			Proposals    []string `json:"proposals"`
			Project      string   `json:"project"`
			ContextQuery string   `json:"context_query"`
		}
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return true
		}
		if len(args.Proposals) < 2 {
			s.sendError(req.ID, -32602, "Invalid params", "proposals requires at least 2 candidate proposals")
			return true
		}
		targetDir, _ := s.resolveTargetProjectDir(args.Project)
		projectID, _ := s.resolveTargetProject(args.Project)
		res, err := dream.CompareProposals(s.store, projectID, targetDir, args.ContextQuery, args.Proposals)
		if err != nil {
			s.sendError(req.ID, -32603, "Internal error", err.Error())
			return true
		}
		s.sendToolResult(req.ID, res, fmt.Sprintf("Tournament evaluated across %d proposals: Candidate %d chosen as winner (%s)", len(res.Candidates), res.WinningIndex+1, res.Summary))
		return true

	case "record_preference":
		var args struct {
			CandidateA   string `json:"candidate_a"`
			CandidateB   string `json:"candidate_b"`
			Winner       string `json:"winner"`
			Rationale    string `json:"rationale"`
			Project      string `json:"project"`
			ContextQuery string `json:"context_query"`
			Agent        string `json:"agent"`
			Decision     string `json:"decision"`
		}
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return true
		}
		if args.CandidateA == "" || args.CandidateB == "" {
			s.sendError(req.ID, -32602, "Invalid params", "candidate_a and candidate_b are required")
			return true
		}
		projectID, _ := s.resolveTargetProject(args.Project)
		decAddr := ""
		if args.Decision != "" {
			if _, canonical, pErr := store.ParseAddress(args.Decision); pErr == nil {
				decAddr = canonical
			}
		}
		pref := &store.Preference{
			ProjectID:       projectID,
			ContextQuery:    args.ContextQuery,
			CandidateA:      args.CandidateA,
			CandidateB:      args.CandidateB,
			Winner:          args.Winner,
			Rationale:       args.Rationale,
			Agent:           args.Agent,
			DecisionAddress: decAddr,
		}
		if err := s.store.SavePreference(pref); err != nil {
			s.sendError(req.ID, -32603, "Internal error", err.Error())
			return true
		}
		s.sendToolResult(req.ID, pref, fmt.Sprintf("Pairwise preference %s recorded (winner: %s)", pref.ID, pref.Winner))
		return true

	case "create_decision":
		var args struct {
			Title          string `json:"title"`
			Options        any    `json:"options"`
			Recommendation string `json:"recommendation"`
			Rationale      string `json:"rationale"`
			Context        string `json:"context"`
			Goal           string `json:"goal"`
			Project        string `json:"project"`
			Address        string `json:"address"`
		}
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return true
		}
		if strings.TrimSpace(args.Title) == "" {
			s.sendError(req.ID, -32602, "Invalid params", "title is required")
			return true
		}
		var parsedOpts []store.DecisionOption
		switch val := args.Options.(type) {
		case []any:
			for _, item := range val {
				switch optVal := item.(type) {
				case string:
					if opt := parseMCPOptionString(optVal); opt != nil {
						parsedOpts = append(parsedOpts, *opt)
					}
				case map[string]any:
					letter, _ := optVal["letter"].(string)
					title, _ := optVal["title"].(string)
					tradeOff, _ := optVal["trade_off"].(string)
					parsedOpts = append(parsedOpts, store.DecisionOption{
						Letter:   letter,
						Title:    title,
						TradeOff: tradeOff,
					})
				}
			}
		}
		if len(parsedOpts) < 2 {
			s.sendError(req.ID, -32602, "Invalid params", "options requires at least 2 lettered options")
			return true
		}
		if strings.TrimSpace(args.Recommendation) == "" {
			s.sendError(req.ID, -32602, "Invalid params", "recommendation is required")
			return true
		}
		projectID, _ := s.resolveTargetProject(args.Project)
		d, err := s.store.CreateDecision(&store.Decision{
			ProjectID:      projectID,
			Address:        args.Address,
			Title:          args.Title,
			Context:        args.Context,
			Options:        parsedOpts,
			Recommendation: args.Recommendation,
			Rationale:      args.Rationale,
			GoalSlug:       args.Goal,
			CreatedBy:      "mcp",
		})
		if err != nil {
			s.sendError(req.ID, -32603, "Internal error", err.Error())
			return true
		}
		s.sendToolResult(req.ID, d, fmt.Sprintf("Created addressed decision %s: %s (recommended: %s)", d.Address, d.Title, d.Recommendation))
		return true

	case "resolve_decision":
		var args struct {
			Address    string `json:"address"`
			Choice     string `json:"choice"`
			Notes      string `json:"notes"`
			ResolvedBy string `json:"resolved_by"`
			Project    string `json:"project"`
		}
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return true
		}
		if strings.TrimSpace(args.Address) == "" || strings.TrimSpace(args.Choice) == "" {
			s.sendError(req.ID, -32602, "Invalid params", "address and choice are required")
			return true
		}
		projectID, _ := s.resolveTargetProject(args.Project)
		resolvedBy := args.ResolvedBy
		if resolvedBy == "" {
			resolvedBy = "mcp"
		}
		d, err := s.store.ResolveDecision(projectID, args.Address, args.Choice, args.Notes, resolvedBy)
		if err != nil {
			s.sendError(req.ID, -32603, "Internal error", err.Error())
			return true
		}
		s.sendToolResult(req.ID, d, fmt.Sprintf("Resolved decision %s: selected %s", d.Address, d.Resolution))
		return true

	case "list_decisions":
		var args struct {
			Project string `json:"project"`
			Status  string `json:"status"`
			Goal    string `json:"goal"`
			Limit   int    `json:"limit"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		projectID, _ := s.resolveTargetProject(args.Project)
		decisions, err := s.store.ListDecisions(store.DecisionFilter{
			ProjectID: projectID,
			Status:    args.Status,
			GoalSlug:  args.Goal,
			Limit:     args.Limit,
		})
		if err != nil {
			s.sendError(req.ID, -32603, "Internal error", err.Error())
			return true
		}
		s.sendToolResult(req.ID, decisions, fmt.Sprintf("Retrieved %d decisions for %s", len(decisions), projectID))
		return true

	case "run_gate_pipeline":
		args, err := decodeGatePipelineArgs(rawArgs)
		if err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return true
		}
		repoDir, err := s.resolveTargetProjectDir(args.Project)
		if err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return true
		}
		mgr, err := git.NewManager(repoDir)
		if err != nil {
			s.sendError(req.ID, -32603, "Internal error", fmt.Sprintf("git manager: %v", err))
			return true
		}
		res, err := mgr.RunGatePipeline(args.Branch, args.ForwardTarget, args.AutoForward)
		if err != nil {
			s.sendError(req.ID, -32603, "Internal error", fmt.Sprintf("run gate pipeline: %v", err))
			return true
		}
		view := buildGatePipelineResponse(*res, args.Verbose)
		summary := fmt.Sprintf("Gate pipeline result: clean=%v, branch=%s, duration=%s, stages=%d", view.Clean, view.Branch, res.Duration.Round(time.Millisecond), len(view.StageLines))
		if res.Forwarded {
			summary += fmt.Sprintf(", forwarded to %s", res.TargetRef)
		}
		s.sendToolResult(req.ID, view, summary)
		return true

	case "run_preflight_eval":
		var args struct {
			EvalDir        string `json:"eval_dir"`
			CaseName       string `json:"case_name"`
			Tag            string `json:"tag"`
			TargetRule     string `json:"target_rule"`
			Harness        string `json:"harness"`
			Ablation       bool   `json:"ablation"`
			Project        string `json:"project"`
			TimeoutSeconds int    `json:"timeout_seconds"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		if args.TimeoutSeconds < 0 {
			s.sendError(req.ID, -32602, "Invalid params", "timeout_seconds must be >= 0")
			return true
		}

		repoDir, err := s.resolveTargetProjectDir(args.Project)
		if err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return true
		}

		evalDir := args.EvalDir
		if evalDir == "" {
			evalDir = filepath.Join(repoDir, "evals")
			if info, err := os.Stat(evalDir); err != nil || !info.IsDir() {
				evalDir = repoDir
			}
		} else if !filepath.IsAbs(evalDir) {
			evalDir = filepath.Join(repoDir, evalDir)
		}

		cases, err := eval.LoadSuite(evalDir)
		if err != nil || len(cases) == 0 {
			if singleCase, singleErr := eval.LoadCase(evalDir); singleErr == nil && singleCase != nil {
				cases = []*eval.Case{singleCase}
			} else {
				s.sendError(req.ID, -32602, "Invalid params", fmt.Sprintf("No evaluation cases found in %s", evalDir))
				return true
			}
		}

		harnessName := strings.ToLower(strings.TrimSpace(args.Harness))
		reg := eval.NewRegistry()
		var runner eval.HarnessRunner
		if harnessName == "" || harnessName == "auto" {
			var err error
			runner, err = reg.AutoDetect()
			if err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		} else {
			var ok bool
			runner, ok = reg.Get(harnessName)
			if !ok {
				s.sendError(req.ID, -32602, "Invalid params", fmt.Sprintf("unknown harness runner: %q (available: claude, agy, mock)", harnessName))
				return true
			}
			if !runner.IsAvailable() {
				s.sendError(req.ID, -32602, "Invalid params", fmt.Sprintf("harness runner %q is not available on PATH", harnessName))
				return true
			}
		}

		timeout := 5 * time.Minute
		if args.TimeoutSeconds > 0 {
			timeout = time.Duration(args.TimeoutSeconds) * time.Second
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		var suiteRes *eval.SuiteResult
		if args.Ablation || args.TargetRule != "" {
			suiteRes, err = eval.ExecuteAblationSuite(ctx, runner, cases, args.TargetRule, repoDir, args.CaseName, args.Tag)
		} else {
			suiteRes, err = eval.ExecuteSuite(ctx, runner, cases, repoDir, args.CaseName, args.Tag)
		}

		if err != nil {
			s.sendError(req.ID, -32603, "Internal error", fmt.Sprintf("run eval suite: %v", err))
			return true
		}

		syntheticNote := ""
		if suiteRes.Synthetic {
			syntheticNote = " (synthetic/mock)"
		}
		statusText := "PASSED"
		if !suiteRes.Passed {
			statusText = "FAILED"
		}
		if suiteRes.Error != "" {
			statusText = fmt.Sprintf("FAILED (%s)", suiteRes.Error)
		}
		s.sendToolResult(req.ID, suiteRes, fmt.Sprintf("Preflight evaluation %s: %d/%d passed (Score: %.1f/%.1f, %s)%s",
			statusText, suiteRes.PassedCases, suiteRes.TotalCases, suiteRes.TotalScore, suiteRes.MaxScore, suiteRes.Duration.Round(time.Millisecond), syntheticNote))
		return true

	case "spawn_crewmate":
		var args struct {
			Harness   string              `json:"harness"`
			GoalSlug  string              `json:"goal_slug"`
			BaseRef   string              `json:"base_ref"`
			RoleClass crew.AgentRoleClass `json:"role_class"`
			Project   string              `json:"project"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		if args.Harness == "" || args.GoalSlug == "" {
			s.sendError(req.ID, -32602, "Invalid params", "harness and goal_slug are required")
			return true
		}
		repoDir, err := s.resolveTargetProjectDir(args.Project)
		if err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return true
		}
		crewMgr, err := crew.NewCrewManager(repoDir)
		if err != nil {
			s.sendError(req.ID, -32603, "Internal error", fmt.Sprintf("crew manager: %v", err))
			return true
		}
		cm, err := crewMgr.SpawnWithRole(args.Harness, args.GoalSlug, args.BaseRef, args.RoleClass)
		if err != nil {
			s.sendError(req.ID, -32603, "Internal error", fmt.Sprintf("spawn crewmate: %v", err))
			return true
		}
		s.sendToolResult(req.ID, cm, fmt.Sprintf("Crewmate %s spawned (%s → %s, role: %s) in worktree %s", cm.ID, cm.Harness, cm.GoalSlug, cm.RoleClass, cm.Worktree))
		return true

	case "list_crew":
		var args struct {
			Project string `json:"project"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		repoDir, err := s.resolveTargetProjectDir(args.Project)
		if err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return true
		}
		crewMgr, err := crew.NewCrewManager(repoDir)
		if err != nil {
			s.sendError(req.ID, -32603, "Internal error", fmt.Sprintf("crew manager: %v", err))
			return true
		}
		crewmates, err := crewMgr.List()
		if err != nil {
			s.sendError(req.ID, -32603, "Internal error", fmt.Sprintf("list crew: %v", err))
			return true
		}
		s.sendToolResult(req.ID, crewmates, fmt.Sprintf("%d crewmates active", len(crewmates)))
		return true

	case "supervise_crew":
		var args struct {
			Project  string `json:"project"`
			Sentinel bool   `json:"sentinel"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		repoDir, err := s.resolveTargetProjectDir(args.Project)
		if err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return true
		}
		crewMgr, err := crew.NewCrewManager(repoDir)
		if err != nil {
			s.sendError(req.ID, -32603, "Internal error", fmt.Sprintf("crew manager: %v", err))
			return true
		}

		if args.Sentinel {
			cfg := crew.DefaultSentinelConfig()
			orchestrator := crew.NewSentinelOrchestrator(crewMgr, cfg, nil)
			rep, err := orchestrator.RunPass()
			if err != nil {
				s.sendError(req.ID, -32603, "Internal error", fmt.Sprintf("sentinel supervision: %v", err))
				return true
			}
			s.sendToolResult(req.ID, rep, fmt.Sprintf("Sentinel dual-cron pass: %d crewmates (🟢 %d active, 🔍 %d audited: ✔ %d approved, ✖ %d rejected, 🛑 %d halted runaway)",
				rep.TotalCrewmates, rep.Active, rep.Audited, rep.Approved, rep.Rejected, rep.HaltedRunaway))
			return true
		}

		crewmates, err := crewMgr.Supervise()
		if err != nil {
			s.sendError(req.ID, -32603, "Internal error", fmt.Sprintf("supervise crew: %v", err))
			return true
		}
		running, completed, failed := 0, 0, 0
		for _, cm := range crewmates {
			switch cm.Status {
			case crew.StatusRunning, crew.StatusIdle:
				running++
			case crew.StatusCompleted:
				completed++
			case crew.StatusFailed, crew.StatusGateFailed, crew.StatusKilled:
				failed++
			}
		}
		s.sendToolResult(req.ID, crewmates, fmt.Sprintf("Supervision pass: %d crewmates (🟢 %d active, ✔ %d completed, ✖ %d failed)", len(crewmates), running, completed, failed))
		return true

	case "salam":
		var args struct {
			Project string `json:"project"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		targetDir, err := s.resolveTargetProjectDir(args.Project)
		if err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return true
		}
		projectSel := strings.TrimSpace(args.Project)
		if projectSel == "" {
			projectSel = targetDir
		}
		brief, err := salam.BuildBrief(s.store, targetDir, projectSel)
		if err != nil {
			s.sendError(req.ID, -32603, "Internal error", err.Error())
			return true
		}
		s.sendToolResult(req.ID, brief, brief.Markdown)
		return true

	case "get_operational_guidebook":
		var args struct {
			Domain  string `json:"domain"`
			Format  string `json:"format"`
			Project string `json:"project"`
			Search  string `json:"search"`
			Query   string `json:"query"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		searchFilter := args.Search
		if searchFilter == "" {
			searchFilter = args.Query
		}
		opts := learn.DefaultHarvesterOptions()
		opts.Domain = args.Domain
		opts.SearchQuery = searchFilter
		if args.Project != "" {
			opts.ProjectID, _ = s.resolveTargetProject(args.Project)
		}
		learnings, err := learn.Harvest(s.store, opts)
		if err != nil {
			s.sendError(req.ID, -32603, "Internal error", err.Error())
			return true
		}
		gb := learn.CompileGuidebook(learnings, args.Domain)
		switch strings.ToLower(args.Format) {
		case "json":
			s.sendToolResult(req.ID, gb, fmt.Sprintf("Operational Guidebook: %d learnings across %d domains", gb.TotalLearnings, len(gb.Sections)))
		case "html":
			s.sendToolResult(req.ID, gb, gb.ToHTML())
		case "toon":
			s.sendToolResult(req.ID, gb, gb.ToTOON())
		case "summary":
			s.sendToolResult(req.ID, gb, gb.ToSummary())
		default:
			s.sendToolResult(req.ID, gb, gb.ToMarkdown())
		}
		return true

	case "route_workflow":
		var args struct {
			Objective   string `json:"objective"`
			CurrentTool string `json:"current_tool"`
			Project     string `json:"project"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		if strings.TrimSpace(args.Objective) == "" {
			s.sendError(req.ID, -32602, "Invalid params", "objective is required")
			return true
		}
		projectID, _ := s.resolveTargetProject(args.Project)
		rec := assist.RouteWorkflow(context.Background(), args.Objective, args.CurrentTool, projectID, nil)
		s.sendToolResult(req.ID, rec, formatWorkflowRecommendationText(rec))
		return true

	case "request_approval":
		var args struct {
			Prompt         string `json:"prompt"`
			Project        string `json:"project"`
			RiskLevel      string `json:"risk_level"`
			TimeoutSeconds int    `json:"timeout_seconds"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		if strings.TrimSpace(args.Prompt) == "" {
			s.sendError(req.ID, -32602, "Invalid params", "prompt is required")
			return true
		}
		proj := args.Project
		if proj == "" {
			proj, _ = s.resolveTargetProject("")
		} else if resolved, err := s.resolveTargetProject(proj); err == nil {
			proj = resolved
		}

		cfg, _ := config.Load("")
		reg := gateway.NewFromConfig(cfg)
		coord := gateway.GetDefaultCoordinator(s.store, reg)

		apprReq := gateway.ApprovalRequest{
			Project:        proj,
			Agent:          "mcp",
			Prompt:         args.Prompt,
			RiskLevel:      args.RiskLevel,
			TimeoutSeconds: args.TimeoutSeconds,
		}

		res, err := coord.RequestApproval(context.Background(), apprReq)
		if err != nil {
			s.sendError(req.ID, -32603, "Internal error", err.Error())
			return true
		}

		var summary string
		if res.Approved {
			summary = fmt.Sprintf("✅ Approval GRANTED for %s by %s", res.ID, res.DecidedBy)
			if res.Reason != "" {
				summary += fmt.Sprintf(" (Reason: %s)", res.Reason)
			}
		} else if res.Status == "timed_out" {
			summary = fmt.Sprintf("⏱️ Approval request %s TIMED OUT after %dms with zero human decision recorded", res.ID, res.DurationMs)
		} else {
			summary = fmt.Sprintf("❌ Approval REJECTED for %s by %s", res.ID, res.DecidedBy)
			if res.Reason != "" {
				summary += fmt.Sprintf(" (Reason: %s)", res.Reason)
			}
		}

		s.sendToolResult(req.ID, res, summary)
		return true

	default:
		return false
	}
}

func formatWorkflowRecommendationText(rec assist.WorkflowRecommendation) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Recommended Workflow: %s (Confidence: %.0f%%, Provider: %s)\n",
		rec.Category, rec.Confidence*100, rec.Provider))
	b.WriteString(fmt.Sprintf("Rationale: %s\n\n", rec.Rationale))
	b.WriteString("Recommended Sila Tool Sequence:\n")
	for i, t := range rec.Tools {
		b.WriteString(fmt.Sprintf("  %d. %s\n", i+1, t))
	}
	b.WriteString("\nEquivalent Sila CLI Commands:\n")
	for i, c := range rec.CLICommands {
		b.WriteString(fmt.Sprintf("  %d. %s\n", i+1, c))
	}
	return b.String()
}

func parseMCPOptionString(s string) *store.DecisionOption {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	idx := strings.Index(s, ":")
	if idx < 0 {
		return nil
	}
	letter := strings.ToUpper(strings.TrimSpace(s[:idx]))
	rest := strings.TrimSpace(s[idx+1:])
	tradeOff := ""
	if openP := strings.LastIndex(rest, "("); openP > 0 && strings.HasSuffix(rest, ")") {
		tradeOff = strings.TrimSuffix(rest[openP+1:], ")")
		rest = strings.TrimSpace(rest[:openP])
	} else if openB := strings.LastIndex(rest, "["); openB > 0 && strings.HasSuffix(rest, "]") {
		tradeOff = strings.TrimSuffix(rest[openB+1:], "]")
		rest = strings.TrimSpace(rest[:openB])
	}
	return &store.DecisionOption{
		Letter:   letter,
		Title:    rest,
		TradeOff: tradeOff,
	}
}
