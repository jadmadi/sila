package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/audit"
	"github.com/jadmadi/sila/internal/goals"
)

func (s *Server) handleGoalsTool(req JSONRPCRequest, name string, rawArgs json.RawMessage) bool {
	switch name {
	case "list_goals":
		var args struct {
			Project     string `json:"project"`
			Status      string `json:"status"`
			PendingOnly bool   `json:"pending_only"`
			Tier        string `json:"tier"`
			Limit       int    `json:"limit"`
			Slug        string `json:"slug"`
			TaskID      string `json:"task_id"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}

		if args.Slug != "" && args.TaskID != "" {
			targetDir, err := goals.ResolveProjectDir(s.store, args.Project)
			if err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
			packet, err := goals.BuildTaskPacket(targetDir, args.Slug, args.TaskID)
			if err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
			resMap := map[string]any{
				"task_packet": packet,
			}
			formatted, err := goals.EncodeTaskPacketJSON(packet)
			if err != nil {
				s.sendError(req.ID, -32603, "Internal error", err.Error())
				return true
			}
			s.sendToolResult(req.ID, resMap, string(formatted))
			return true
		}
		targetDir, err := goals.ResolveProjectDir(s.store, args.Project)
		if err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return true
		}
		res, err := goals.ScanGoals(targetDir)
		if err != nil {
			s.sendError(req.ID, -32603, "Internal error", err.Error())
			return true
		}
		statusFilter := strings.ToLower(strings.TrimSpace(args.Status))
		if statusFilter == "" && args.PendingOnly {
			statusFilter = "pending"
		}
		tierFilter := strings.ToLower(strings.TrimSpace(args.Tier))
		type mcpGoalEntry struct {
			goals.Goal
			Blockers []goals.DependencyBlocker `json:"blockers,omitempty"`
		}
		var out []mcpGoalEntry
		for _, g := range res.Goals {
			if args.Slug != "" && !strings.EqualFold(g.Slug, args.Slug) {
				continue
			}
			if tierFilter != "" && goals.NormalizeTier(g.Tier) != goals.NormalizeTier(tierFilter) {
				continue
			}
			matched := true
			switch statusFilter {
			case "pending":
				matched = (g.Status == "pending")
			case "claimed", "active", "in-progress", "in_progress":
				matched = (g.Status == "claimed")
			case "implemented", "done":
				matched = (g.Status == "implemented")
			case "ready":
				matched = (!g.Blocked && g.Status == "pending")
			case "blocked":
				matched = (g.Blocked && g.Status == "pending")
			case "superseded":
				matched = (g.Status == "superseded")
			case "", "all":
				matched = true
			default:
				matched = (g.Status == statusFilter)
			}
			if matched {
				entry := mcpGoalEntry{Goal: g}
				if g.Blocked {
					entry.Blockers = goals.ResolveGoalBlockers(targetDir, g.Slug)
				}
				out = append(out, entry)
			}
		}
		if args.Limit > 0 && len(out) > args.Limit {
			out = out[:args.Limit]
		}
		formatted, _ := json.MarshalIndent(out, "", "  ")
		s.sendToolResult(req.ID, out, string(formatted))
		return true

	case "audit_goals":
		var args struct {
			Project string `json:"project"`
			NoSync  bool   `json:"no_sync"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		targetDir, err := goals.ResolveProjectDir(s.store, args.Project)
		if err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return true
		}
		res, err := goals.ScanGoals(targetDir)
		if err != nil {
			s.sendError(req.ID, -32603, "Internal error", err.Error())
			return true
		}
		if !args.NoSync && res.Total > 0 {
			if err := goals.SyncReadme(targetDir, res); err != nil {
				s.sendError(req.ID, -32603, "Internal error", "sync goals README: "+err.Error())
				return true
			}
		}
		formatted, _ := json.MarshalIndent(res, "", "  ")
		s.sendToolResult(req.ID, res, string(formatted))
		return true

	case "audit_goal":
		var args struct {
			Slug       string `json:"slug"`
			Project    string `json:"project"`
			Persona    string `json:"persona"`
			URL        string `json:"url"`
			Binary     string `json:"binary"`
			SkipCanary bool   `json:"skip_canary"`
			Timeout    string `json:"timeout"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		if args.Slug == "" {
			s.sendError(req.ID, -32602, "Invalid params", "slug is required")
			return true
		}
		targetDir, err := goals.ResolveProjectDir(s.store, args.Project)
		if err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return true
		}

		timeout := 10 * time.Minute
		if args.Timeout != "" {
			if d, err := time.ParseDuration(args.Timeout); err == nil {
				timeout = d
			}
		}

		personaID := args.Persona
		if personaID == "" {
			personaID = "mystery-auditor"
		}

		opts := audit.GoalAuditOptions{
			GoalSlug:     args.Slug,
			ProjectDir:   targetDir,
			PersonaID:    personaID,
			SessionID:    "mcp-audit-goal",
			TargetURL:    args.URL,
			TargetBinary: args.Binary,
			Timeout:      timeout,
			Blindfold:    true,
			SkipCanary:   args.SkipCanary,
		}

		eval, err := audit.AuditGoal(context.Background(), s.store, opts)
		if err != nil {
			s.sendError(req.ID, -32603, "Internal error", err.Error())
			return true
		}

		formatted, _ := json.MarshalIndent(eval, "", "  ")
		s.sendToolResult(req.ID, eval, string(formatted))
		return true

	case "claim_goal":
		var args struct {
			Slug       string   `json:"slug"`
			Agent      string   `json:"agent"`
			Note       string   `json:"note"`
			Force      bool     `json:"force"`
			Project    string   `json:"project"`
			Session    string   `json:"session"`
			AllowPaths []string `json:"allow_paths"`
			DenyPaths  []string `json:"deny_paths"`
			Duration   string   `json:"duration"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		if args.Slug == "" {
			s.sendError(req.ID, -32602, "Invalid params", "slug is required")
			return true
		}
		targetDir, err := goals.ResolveProjectDir(s.store, args.Project)
		if err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return true
		}
		var dur time.Duration
		if args.Duration != "" {
			if d, err := time.ParseDuration(args.Duration); err == nil {
				dur = d
			}
		}
		claimOpts := goals.ClaimOptions{
			AllowPaths:  args.AllowPaths,
			DenyPaths:   args.DenyPaths,
			MaxDuration: dur,
		}
		claim, charter, err := goals.ClaimGoalWithCharter(targetDir, args.Slug, args.Agent, args.Note, strings.TrimSpace(args.Session), args.Force, claimOpts)
		if err != nil {
			s.sendError(req.ID, -32603, "Claim error", err.Error())
			return true
		}
		if res, err := goals.ScanGoals(targetDir); err == nil {
			_ = goals.SyncReadme(targetDir, res)
		}
		msg := fmt.Sprintf("✔ Goal %q successfully claimed and locked for @%s", args.Slug, claim.Agent)
		if charter != nil && len(charter.AllowPaths) > 0 {
			msg = fmt.Sprintf("✔ Goal %q successfully claimed and locked for @%s with GoalCharter (%d allowed paths, %d denied boundaries)",
				args.Slug, claim.Agent, len(charter.AllowPaths), len(charter.DenyPaths))
		}
		s.sendToolResult(req.ID, map[string]any{"slug": args.Slug, "claimed": true, "claim": claim, "charter": charter}, msg)
		return true

	case "release_goal":
		var args struct {
			Slug    string `json:"slug"`
			Project string `json:"project"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		if args.Slug == "" {
			s.sendError(req.ID, -32602, "Invalid params", "slug is required")
			return true
		}
		targetDir, err := goals.ResolveProjectDir(s.store, args.Project)
		if err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return true
		}
		if err := goals.ReleaseGoal(targetDir, args.Slug); err != nil {
			s.sendError(req.ID, -32603, "Release error", err.Error())
			return true
		}
		if res, err := goals.ScanGoals(targetDir); err == nil {
			_ = goals.SyncReadme(targetDir, res)
		}
		msg := fmt.Sprintf("✔ Goal %q released back to pending queue", args.Slug)
		s.sendToolResult(req.ID, map[string]any{"slug": args.Slug, "released": true}, msg)
		return true

	case "report_goal":
		var args struct {
			Slug           string                `json:"slug"`
			Note           string                `json:"note"`
			Status         string                `json:"status"`
			Project        string                `json:"project"`
			ToolName       string                `json:"tool_name"`
			ClientKind     string                `json:"client_kind"`
			OriginEntity   string                `json:"origin_entity"`
			TaskCheckpoint *goals.TaskCheckpoint `json:"task_checkpoint"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		if args.Slug == "" {
			s.sendError(req.ID, -32602, "Invalid params", "slug is required")
			return true
		}
		if args.Note == "" && args.TaskCheckpoint == nil {
			s.sendError(req.ID, -32602, "Invalid params", "either note or task_checkpoint is required")
			return true
		}
		targetDir, err := goals.ResolveProjectDir(s.store, args.Project)
		if err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return true
		}

		if args.TaskCheckpoint != nil {
			claim, err := goals.LoadGoalClaim(targetDir, args.Slug)
			if err != nil || claim == nil {
				s.sendToolError(req.ID, map[string]any{"error": fmt.Sprintf("goal %s has no active claim", args.Slug)}, fmt.Sprintf("goal %s has no active claim", args.Slug))
				return true
			}
			if err := goals.SaveTaskCheckpoint(targetDir, args.Slug, *args.TaskCheckpoint); err != nil {
				s.sendToolError(req.ID, map[string]any{"error": err.Error()}, err.Error())
				return true
			}
		}

		var claim *goals.GoalClaim
		effectiveNote := args.Note
		if effectiveNote != "" {
			if args.OriginEntity != "" && !strings.Contains(effectiveNote, args.OriginEntity) {
				effectiveNote = fmt.Sprintf("[%s] %s", args.OriginEntity, effectiveNote)
			}
			claim, err = goals.ReportGoal(targetDir, args.Slug, effectiveNote, args.Status)
			if err != nil {
				s.sendError(req.ID, -32603, "Report error", err.Error())
				return true
			}
			if res, err := goals.ScanGoals(targetDir); err == nil {
				_ = goals.SyncReadme(targetDir, res)
			}
		} else {
			claim, _ = goals.LoadGoalClaim(targetDir, args.Slug)
		}

		msg := fmt.Sprintf("✔ Progress report updated for goal %q", args.Slug)
		if claim != nil && claim.Note != "" {
			msg = fmt.Sprintf("✔ Progress report updated for goal %q: %s", args.Slug, claim.Note)
		}
		resMap := map[string]any{"slug": args.Slug, "updated": true, "claim": claim}
		if args.TaskCheckpoint != nil {
			resMap["task_checkpoint"] = args.TaskCheckpoint
		}
		s.sendToolResult(req.ID, resMap, msg)
		return true

	case "create_goal":
		var args struct {
			Slug         string          `json:"slug"`
			Title        string          `json:"title"`
			Description  string          `json:"description"`
			DependsOn    []string        `json:"depends_on"`
			Independent  *bool           `json:"independent"`
			Sequence     int             `json:"sequence"`
			FilesToTouch []string        `json:"files_to_touch"`
			Overwrite    bool            `json:"overwrite"`
			Shape        string          `json:"shape"`
			Tier         string          `json:"tier"`
			Invariants   []string        `json:"invariants"`
			Tasks        []string        `json:"tasks"`
			Status       string          `json:"status"`
			Commit       string          `json:"commit"`
			Strict       bool            `json:"strict"`
			Project      string          `json:"project"`
			WorkPackage  json.RawMessage `json:"work_package"`
			SpecReview   json.RawMessage `json:"spec_review"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		if args.Slug == "" {
			s.sendError(req.ID, -32602, "Invalid params", "slug is required")
			return true
		}
		var loadedWP *goals.WorkPackage
		if len(args.WorkPackage) > 0 && string(args.WorkPackage) != "null" {
			if len(args.Tasks) > 0 {
				s.sendError(req.ID, -32602, "Invalid params", "cannot specify both work_package and tasks")
				return true
			}
			if len(args.Invariants) > 0 {
				s.sendError(req.ID, -32602, "Invalid params", "cannot specify both work_package and invariants")
				return true
			}
			wp, err := goals.DecodeWorkPackage(args.WorkPackage)
			if err != nil {
				s.sendError(req.ID, -32602, "Invalid work package", err.Error())
				return true
			}
			if wp.GoalSlug != args.Slug {
				s.sendError(req.ID, -32602, "Invalid params", fmt.Sprintf("work package goal_slug %q does not match requested slug %q", wp.GoalSlug, args.Slug))
				return true
			}
			loadedWP = wp
		}
		var loadedReview *goals.SpecReview
		if len(args.SpecReview) > 0 && string(args.SpecReview) != "null" {
			if loadedWP == nil {
				s.sendError(req.ID, -32602, "Invalid params", "spec_review requires a work_package")
				return true
			}
			var rev goals.SpecReview
			dec := json.NewDecoder(strings.NewReader(string(args.SpecReview)))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&rev); err != nil {
				s.sendError(req.ID, -32602, "Invalid spec_review", err.Error())
				return true
			}
			reviewDiags := goals.ValidateSpecReview(loadedWP, rev)
			if len(reviewDiags) > 0 {
				detail := fmt.Sprintf("spec review validation failed: %s: %s", reviewDiags[0].Code, reviewDiags[0].Message)
				s.sendError(req.ID, -32602, "Invalid spec_review: "+detail, detail)
				return true
			}
			loadedReview = &rev
		}
		targetDir, err := goals.ResolveProjectDir(s.store, args.Project)
		if err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return true
		}
		g, err := goals.CreateGoal(goals.CreateGoalParams{
			RootDir:      targetDir,
			Slug:         args.Slug,
			Title:        args.Title,
			Description:  args.Description,
			DependsOn:    args.DependsOn,
			Independent:  args.Independent,
			Sequence:     args.Sequence,
			FilesToTouch: args.FilesToTouch,
			Overwrite:    args.Overwrite,
			Shape:        args.Shape,
			Tier:         args.Tier,
			Invariants:   args.Invariants,
			Tasks:        args.Tasks,
			Status:       args.Status,
			Commit:       args.Commit,
			Strict:       args.Strict,
			WorkPackage:  loadedWP,
		})
		if err != nil {
			s.sendError(req.ID, -32603, "Creation error", err.Error())
			return true
		}
		modeStr := "Independent ⚡"
		if !g.Independent {
			modeStr = "Dependent 🔗"
		}
		statusLabel := ""
		if g.Status == "implemented" {
			statusLabel = fmt.Sprintf(" [implemented in %s]", g.Commit)
		}
		msg := fmt.Sprintf("✔ Goal %q (%s%s) created under goals/%s with goal.md, facts.md, and plan.md", g.Slug, modeStr, statusLabel, g.Slug)
		res := map[string]any{"created": true, "goal": g}
		if loadedReview != nil {
			if err := goals.SaveSpecReview(targetDir, args.Slug, *loadedReview); err != nil {
				s.sendError(req.ID, -32603, "Creation error", fmt.Sprintf("save spec review: %v", err))
				return true
			}
			res["spec_review"] = loadedReview
		}
		if g.PreFlight != nil {
			res["preflight"] = g.PreFlight
			if len(g.PreFlight.Warnings) > 0 {
				res["conflicts_detected"] = g.PreFlight.Warnings
				msg += fmt.Sprintf("\n▲ %d pre-flight warning(s) detected:\n- %s", len(g.PreFlight.Warnings), strings.Join(g.PreFlight.Warnings, "\n- "))
			}
		}
		s.sendToolResult(req.ID, res, msg)
		return true

	case "reopen_goal":
		var args struct {
			Slug    string `json:"slug"`
			Reason  string `json:"reason"`
			Agent   string `json:"agent"`
			Claim   bool   `json:"claim"`
			Project string `json:"project"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		if args.Slug == "" {
			s.sendError(req.ID, -32602, "Invalid params", "slug is required")
			return true
		}
		targetDir, err := goals.ResolveProjectDir(s.store, args.Project)
		if err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return true
		}
		rec, cl, err := goals.ReopenGoal(targetDir, args.Slug, args.Agent, args.Reason, args.Claim)
		if err != nil {
			s.sendError(req.ID, -32603, "Reopen error", err.Error())
			return true
		}
		if res, err := goals.ScanGoals(targetDir); err == nil {
			_ = goals.SyncReadme(targetDir, res)
		}
		resMap := map[string]any{"slug": args.Slug, "reopened": true, "record": rec}
		if cl != nil {
			resMap["claim"] = cl
		}
		msg := fmt.Sprintf("✔ Goal %q successfully reopened for remediation by @%s (reason: %q)", args.Slug, rec.ReopenedBy, rec.Reason)
		s.sendToolResult(req.ID, resMap, msg)
		return true

	case "touch_goal":
		var args struct {
			Slug    string `json:"slug"`
			Agent   string `json:"agent"`
			Project string `json:"project"`
		}
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return true
		}
		if args.Slug == "" {
			s.sendError(req.ID, -32602, "Invalid params", "slug is required")
			return true
		}
		targetDir, err := goals.ResolveProjectDir(s.store, args.Project)
		if err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return true
		}
		claim, err := goals.TouchGoal(targetDir, args.Slug, args.Agent)
		if err != nil {
			s.sendError(req.ID, -32603, "Internal error", err.Error())
			return true
		}
		s.sendToolResult(req.ID, claim, fmt.Sprintf("Heartbeat renewed for goal %s (lease extended to %s)", args.Slug, claim.UpdatedAt.Format(time.RFC3339)))
		return true

	case "update_goal":
		var args struct {
			Slug          string          `json:"slug"`
			Title         *string         `json:"title"`
			Description   *string         `json:"description"`
			DependsOn     []string        `json:"depends_on"`
			AddDeps       []string        `json:"add_deps"`
			RemoveDeps    []string        `json:"remove_deps"`
			Independent   *bool           `json:"independent"`
			Sequence      *int            `json:"sequence"`
			FilesToTouch  []string        `json:"files_to_touch"`
			Shape         *string         `json:"shape"`
			Tier          *string         `json:"tier"`
			Personas      []string        `json:"personas"`
			AddPersonas   []string        `json:"add_personas"`
			ClearPersonas bool            `json:"clear_personas"`
			Strict        bool            `json:"strict"`
			SpecReview    json.RawMessage `json:"spec_review"`
			Project       string          `json:"project"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		if args.Slug == "" {
			s.sendError(req.ID, -32602, "Invalid params", "slug is required")
			return true
		}
		var review *goals.SpecReview
		if len(args.SpecReview) > 0 && string(args.SpecReview) != "null" {
			var rev goals.SpecReview
			dec := json.NewDecoder(strings.NewReader(string(args.SpecReview)))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&rev); err != nil {
				s.sendError(req.ID, -32602, "Invalid spec_review", err.Error())
				return true
			}
			review = &rev
		}
		targetDir, err := goals.ResolveProjectDir(s.store, args.Project)
		if err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return true
		}
		g, err := goals.UpdateGoal(goals.UpdateGoalParams{
			RootDir:       targetDir,
			Slug:          args.Slug,
			Title:         args.Title,
			Description:   args.Description,
			DependsOn:     args.DependsOn,
			AddDeps:       args.AddDeps,
			RemoveDeps:    args.RemoveDeps,
			Independent:   args.Independent,
			Sequence:      args.Sequence,
			FilesToTouch:  args.FilesToTouch,
			Shape:         args.Shape,
			Tier:          args.Tier,
			Personas:      args.Personas,
			AddPersonas:   args.AddPersonas,
			ClearPersonas: args.ClearPersonas,
			Strict:        args.Strict,
			SpecReview:    review,
		})
		if err != nil {
			s.sendError(req.ID, -32603, "Update error", err.Error())
			return true
		}
		s.sendToolResult(req.ID, g, fmt.Sprintf("✔ Goal %q updated", args.Slug))
		return true

	case "certify_goal":
		var args struct {
			Slug     string `json:"slug"`
			Evidence string `json:"evidence"`
			Agent    string `json:"agent"`
			Note     string `json:"note"`
			AutoRun  bool   `json:"auto_run"`
			Project  string `json:"project"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		if args.Slug == "" {
			s.sendError(req.ID, -32602, "Invalid params", "slug is required")
			return true
		}
		targetDir, err := goals.ResolveProjectDir(s.store, args.Project)
		if err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return true
		}
		if args.AutoRun {
			cert, contractRes, err := goals.AutoCertifyGoal(context.Background(), targetDir, args.Slug, args.Agent, args.Note)
			if err != nil {
				s.sendError(req.ID, -32603, "Certification error", err.Error())
				return true
			}
			s.sendToolResult(req.ID, map[string]any{"certification": cert, "contract_result": contractRes}, fmt.Sprintf("✔ Goal %q verified and certified automatically", args.Slug))
			return true
		}
		cert, err := goals.CertifyGoal(targetDir, args.Slug, args.Evidence, args.Agent, args.Note)
		if err != nil {
			s.sendError(req.ID, -32603, "Certification error", err.Error())
			return true
		}
		s.sendToolResult(req.ID, cert, fmt.Sprintf("✔ Goal %q certified from evidence", args.Slug))
		return true

	case "supersede_goal":
		var args struct {
			Slug         string `json:"slug"`
			SupersededBy string `json:"superseded_by"`
			Agent        string `json:"agent"`
			Reason       string `json:"reason"`
			Project      string `json:"project"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		if args.Slug == "" {
			s.sendError(req.ID, -32602, "Invalid params", "slug is required")
			return true
		}
		targetDir, err := goals.ResolveProjectDir(s.store, args.Project)
		if err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return true
		}
		rec, err := goals.SupersedeGoal(targetDir, args.Slug, args.SupersededBy, args.Agent, args.Reason)
		if err != nil {
			s.sendError(req.ID, -32603, "Supersede error", err.Error())
			return true
		}
		s.sendToolResult(req.ID, rec, fmt.Sprintf("✔ Goal %q superseded by %s", args.Slug, args.SupersededBy))
		return true

	case "validate_goals":
		var args struct {
			Slug         string `json:"slug"`
			Strict       bool   `json:"strict"`
			ManifestPath string `json:"manifest_path"`
			Project      string `json:"project"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		targetDir, err := goals.ResolveProjectDir(s.store, args.Project)
		if err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return true
		}
		report, err := goals.ValidateGoals(targetDir, goals.ValidateOptions{
			Slug:         args.Slug,
			Strict:       args.Strict,
			ManifestPath: args.ManifestPath,
		})
		if err != nil {
			s.sendError(req.ID, -32603, "Validation error", err.Error())
			return true
		}
		s.sendToolResult(req.ID, report, fmt.Sprintf("✔ Goal validation complete: %d error(s), %d warning(s)", report.ErrorsCount, report.WarningsCount))
		return true

	default:
		return false
	}
}
