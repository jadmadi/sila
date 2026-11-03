package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/agents"
	"github.com/jadmadi/sila/internal/cloudflare"
	"github.com/jadmadi/sila/internal/dream"
	"github.com/jadmadi/sila/internal/loaders"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func (s *Server) handleOpsTool(req JSONRPCRequest, name string, rawArgs json.RawMessage) bool {
	switch name {
	case "compile_rules":
		var args struct {
			Project string `json:"project"`
			Target  string `json:"target"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		projectID, err := s.resolveTargetProject(args.Project)
		if err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return true
		}
		rulesContent, err := dream.CompileRules(s.store, projectID, args.Target)
		if err != nil {
			s.sendError(req.ID, -32603, "Internal error", err.Error())
			return true
		}
		s.sendToolResult(req.ID, map[string]string{"rules": rulesContent}, rulesContent)
		return true

	case "vacuum_database":
		var args struct {
			Force bool `json:"force"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		stats, err := s.store.Stats()
		if err != nil {
			s.sendError(req.ID, -32603, "Internal error", err.Error())
			return true
		}
		if !args.Force && stats.FreelistCount == 0 && stats.FragmentationPct == 0 {
			text := fmt.Sprintf("Database is already compact (0 free pages · 0.0%% fragmentation). Current size: %s.",
				render.FormatBytes(stats.FileSize))
			s.sendToolResult(req.ID, map[string]any{
				"executed":  false,
				"file_size": stats.FileSize,
				"freelist":  0,
			}, text)
			return true
		}
		vacRes, err := s.store.Vacuum()
		if err != nil {
			s.sendError(req.ID, -32603, "Internal error", err.Error())
			return true
		}
		text := fmt.Sprintf("Vacuum completed in %s: reclaimed %s (%.1f%% reduction). New size: %s.",
			vacRes.Duration.Round(time.Millisecond),
			render.FormatBytes(vacRes.ReclaimedBytes),
			vacRes.ReclaimedPct,
			render.FormatBytes(vacRes.After.FileSize))
		s.sendToolResult(req.ID, vacRes, text)
		return true

	case "get_db_stats":
		stats, err := s.store.Stats()
		if err != nil {
			s.sendError(req.ID, -32603, "Internal error", err.Error())
			return true
		}
		formatted, _ := json.MarshalIndent(stats, "", "  ")
		s.sendToolResult(req.ID, stats, string(formatted))
		return true

	case "diagnose_system":
		var args struct {
			Repair bool `json:"repair"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		report, err := s.store.Doctor(args.Repair)
		if err != nil {
			s.sendError(req.ID, -32603, "Internal error", err.Error())
			return true
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("System Doctor Status: %s (%d passed, %d warnings, %d failures)\n\n",
			report.OverallStatus, report.Passed, report.Warnings, report.Failures))
		for _, c := range report.Checks {
			icon := "✔"
			if c.Status == store.StatusWarn {
				icon = "▲"
			} else if c.Status == store.StatusFail {
				icon = "✖"
			}
			sb.WriteString(fmt.Sprintf("%s [%s] %s: %s\n", icon, c.Category, c.Name, c.Message))
			if c.Repair != "" {
				sb.WriteString(fmt.Sprintf("   → Action: %s\n", c.Repair))
			}
		}
		if report.RepairExecuted {
			sb.WriteString("\nRepairs executed:\n")
			for _, r := range report.RepairedItems {
				sb.WriteString(fmt.Sprintf(" - %s\n", r))
			}
		}
		s.sendToolResult(req.ID, report, strings.TrimSpace(sb.String()))
		return true

	case "sync_rules":
		var args struct {
			Project           string   `json:"project"`
			Tools             []string `json:"tools"`
			Replace           bool     `json:"replace"`
			PolicyPack        string   `json:"policy_pack"`
			IncludePrinciples bool     `json:"include_principles"`
			Stack             string   `json:"stack"`
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
		projectID, _ := s.resolveTargetProject(args.Project)
		toolsList := args.Tools
		if len(toolsList) == 0 {
			toolsList = []string{"all"}
		}
		opts := agents.CompileOptions{
			RepoRoot:          repoDir,
			ProjectName:       projectID,
			Replace:           args.Replace,
			PolicyPack:        args.PolicyPack,
			IncludePrinciples: args.IncludePrinciples,
			Stack:             args.Stack,
		}
		res, err := agents.DeployTargets(repoDir, opts, toolsList)
		if err != nil {
			s.sendError(req.ID, -32603, "Internal error", fmt.Sprintf("sync rules: %v", err))
			return true
		}
		summary := fmt.Sprintf("Synced agent rules (%d files written, %d symlinks created, %d errors)", len(res.FilesWritten), len(res.Symlinks), len(res.Errors))
		s.sendToolResult(req.ID, res, summary)
		return true

	case "cloudflare_d1_query":
		var args struct {
			Query    string `json:"query"`
			Database string `json:"database"`
			Params   []any  `json:"params"`
			Project  string `json:"project"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		if strings.TrimSpace(args.Query) == "" {
			s.sendError(req.ID, -32602, "Invalid params", "query is required")
			return true
		}
		targetDir := args.Project
		if targetDir == "" {
			targetDir, _ = os.Getwd()
		}
		token, accountID, defaultDB := cloudflare.ResolveCredentials(targetDir)
		dbID := args.Database
		if dbID == "" {
			dbID = defaultDB
		}
		conn := cloudflare.NewConnector(token, accountID, nil)
		res, err := conn.QueryD1(context.Background(), dbID, args.Query, args.Params, targetDir)
		if err != nil {
			s.sendError(req.ID, -32603, "Cloudflare D1 query error", err.Error())
			return true
		}
		summary := fmt.Sprintf("D1 Query on %q: %d rows returned in %.2fms (via %s)", res.Database, res.RowsCount, res.DurationMs, res.Provider)
		s.sendToolResult(req.ID, res, summary)
		return true

	case "cloudflare_worker_status":
		var args struct {
			ScriptName string `json:"script_name"`
			Project    string `json:"project"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		targetDir := args.Project
		if targetDir == "" {
			targetDir, _ = os.Getwd()
		}
		token, accountID, _ := cloudflare.ResolveCredentials(targetDir)
		conn := cloudflare.NewConnector(token, accountID, nil)
		res, err := conn.GetWorkerStatus(context.Background(), args.ScriptName, targetDir)
		if err != nil {
			s.sendError(req.ID, -32603, "Cloudflare Worker status error", err.Error())
			return true
		}
		deployState := "Not deployed (local configuration)"
		if res.Deployed {
			deployState = fmt.Sprintf("Deployed (%s)", res.ActiveVersion)
		}
		summary := fmt.Sprintf("Worker %q: %s, %d binding(s), compatibility date %s (via %s)", res.ScriptName, deployState, len(res.Bindings), res.CompatibilityDate, res.Provider)
		s.sendToolResult(req.ID, res, summary)
		return true

	case "cloudflare_edge_telemetry":
		var args struct {
			ScriptName string `json:"script_name"`
			TimeRange  string `json:"time_range"`
			Project    string `json:"project"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		targetDir := args.Project
		if targetDir == "" {
			targetDir, _ = os.Getwd()
		}
		token, accountID, _ := cloudflare.ResolveCredentials(targetDir)
		conn := cloudflare.NewConnector(token, accountID, nil)
		res, err := conn.GetEdgeTelemetry(context.Background(), args.ScriptName, args.TimeRange, targetDir)
		if err != nil {
			s.sendError(req.ID, -32603, "Cloudflare edge telemetry error", err.Error())
			return true
		}
		summary := fmt.Sprintf("Telemetry for %q (%s): %d requests, %d errors, CPU p50=%.2fms, CPU p99=%.2fms (via %s)", res.ScriptName, res.TimeRange, res.RequestsTotal, res.ErrorsTotal, res.CPUTimeP50Ms, res.CPUTimeP99Ms, res.Provider)
		s.sendToolResult(req.ID, res, summary)
		return true

	case "skill_search", "search_skills":
		s.handleSkillSearch(req, rawArgs)
		return true

	case "skill_load", "load_skill", "read_skill":
		s.handleSkillLoad(req, rawArgs)
		return true

	case "skill_list", "list_skills":
		s.handleSkillList(req, rawArgs)
		return true

	case "audit_hygiene", "hygiene_audit":
		s.handleAuditHygiene(req, rawArgs)
		return true

	case "clean_caches", "prune_caches":
		s.handleCleanCaches(req, rawArgs)
		return true

	case "emit_lifecycle_event", "hook_emit":
		s.handleEmitLifecycleEvent(req, rawArgs)
		return true

	default:
		return false
	}
}

func (s *Server) handleEmitLifecycleEvent(req JSONRPCRequest, rawArgs json.RawMessage) {
	var args struct {
		Event     string                 `json:"event"`
		Agent     string                 `json:"agent"`
		SessionID string                 `json:"session_id"`
		Cwd       string                 `json:"cwd"`
		Project   string                 `json:"project"`
		Payload   map[string]interface{} `json:"payload"`
		Drain     bool                   `json:"drain"`
	}
	if len(rawArgs) > 0 {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return
		}
	}
	if strings.TrimSpace(args.Event) == "" {
		s.sendError(req.ID, -32602, "Invalid params", "event parameter is required")
		return
	}

	spooler, err := loaders.NewSpooler("")
	if err != nil {
		s.sendError(req.ID, -32603, "Internal error", fmt.Sprintf("initialize spooler: %v", err))
		return
	}

	cwd := args.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	projID := args.Project
	if projID == "" && cwd != "" {
		projID = store.ProjectID(cwd)
	}
	agent := args.Agent
	if agent == "" {
		agent = "agent"
	}
	session := args.SessionID
	if session == "" {
		session = fmt.Sprintf("sess-%d", time.Now().UnixNano())
	}
	payload := args.Payload
	if payload == nil {
		payload = make(map[string]interface{})
	}

	env := &loaders.HookEnvelope{
		Event:     loaders.NormalizeEventKind(args.Event),
		Agent:     agent,
		ProjectID: projID,
		Cwd:       cwd,
		SessionID: session,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Payload:   payload,
	}

	spoolPath, err := spooler.Spool(env)
	if err != nil {
		s.sendError(req.ID, -32603, "Spool failed", err.Error())
		return
	}

	var drainReport *loaders.DrainReport
	if args.Drain && s.store != nil {
		drainReport, _ = spooler.Drain(s.store, nil)
	}

	res := map[string]interface{}{
		"status":       "spooled",
		"event":        env.Event,
		"agent":        env.Agent,
		"session_id":   env.SessionID,
		"project_id":   env.ProjectID,
		"spool_path":   spoolPath,
		"drained":      args.Drain,
		"drain_report": drainReport,
	}
	summary := fmt.Sprintf("Spooled lifecycle event %s [%s] (%s)", env.Event, env.Agent, env.SessionID)
	s.sendToolResult(req.ID, res, summary)
}
