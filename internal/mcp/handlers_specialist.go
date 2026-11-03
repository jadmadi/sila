package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/contract"
	"github.com/jadmadi/sila/internal/dream"
	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/learn"
	"github.com/jadmadi/sila/internal/persona"
	"github.com/jadmadi/sila/internal/store"
)

// handleSpecialistTool executes the temporal, persona, domain-learning, and
// symbol-blast-radius tools that mirror their CLI counterparts.
func (s *Server) handleSpecialistTool(req JSONRPCRequest, name string, rawArgs json.RawMessage) bool {
	switch name {
	case "get_activity_digest":
		var args struct {
			Project  string `json:"project"`
			Since    string `json:"since"`
			Activity bool   `json:"activity"`
			Tool     string `json:"tool"`
			Top      int    `json:"top"`
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
		projectDir, _ := s.resolveTargetProjectDir(args.Project)
		since := strings.TrimSpace(args.Since)
		if since == "" {
			since = "24h"
		}
		if args.Activity {
			sinceTime, err := parseActivitySince(since)
			if err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
			top := args.Top
			if top <= 0 {
				top = 10
			}
			res, err := s.store.RecentActivity(store.ActivityQueryOpts{
				ProjectID: projectID,
				Tool:      args.Tool,
				SinceTime: sinceTime,
				Top:       top,
			})
			if err != nil {
				s.sendError(req.ID, -32603, "Internal error", err.Error())
				return true
			}
			formatted, _ := json.MarshalIndent(res, "", "  ")
			s.sendToolResult(req.ID, res, string(formatted))
			return true
		}
		report, err := dream.BuildDigest(s.store, projectID, since, projectDir)
		if err != nil {
			s.sendError(req.ID, -32603, "Internal error", err.Error())
			return true
		}
		s.sendToolResult(req.ID, report, dream.RenderDigestMarkdown(report))
		return true

	case "consult_personas":
		var args struct {
			Action   string   `json:"action"`
			Domain   string   `json:"domain"`
			Group    string   `json:"group"`
			Goal     string   `json:"goal"`
			Personas []string `json:"personas"`
			Project  string   `json:"project"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		mgr := persona.NewManager(s.store)
		action := strings.ToLower(strings.TrimSpace(args.Action))
		if action == "" {
			action = "list"
		}
		switch action {
		case "list", "recommend":
			var list []persona.Persona
			var group *persona.Group
			var err error
			if args.Group != "" {
				list, group, err = mgr.ListGroup(args.Group, args.Domain, "")
			} else {
				list, err = mgr.List(args.Domain, "")
			}
			if err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
			if action == "recommend" && len(list) > 5 {
				list = list[:5]
			}
			payload := map[string]any{"action": action, "domain": args.Domain, "goal": args.Goal, "count": len(list), "personas": list}
			if group != nil {
				payload["group"] = group
			}
			formatted, _ := json.MarshalIndent(payload, "", "  ")
			s.sendToolResult(req.ID, payload, string(formatted))
			return true
		case "bind_goal":
			if strings.TrimSpace(args.Goal) == "" || len(args.Personas) == 0 {
				s.sendError(req.ID, -32602, "Invalid params", "goal and personas are required for bind_goal")
				return true
			}
			dir, err := s.resolveTargetProjectDir(args.Project)
			if err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
			root := goals.FindGoalsRoot(dir)
			if root == "" {
				s.sendError(req.ID, -32603, "Internal error", "no goals/ directory found for project")
				return true
			}
			g, err := goals.UpdateGoal(goals.UpdateGoalParams{RootDir: root, Slug: args.Goal, Personas: args.Personas})
			if err != nil {
				s.sendError(req.ID, -32603, "Internal error", err.Error())
				return true
			}
			formatted, _ := json.MarshalIndent(g, "", "  ")
			s.sendToolResult(req.ID, g, string(formatted))
			return true
		default:
			s.sendError(req.ID, -32602, "Invalid params", fmt.Sprintf("unknown action %q (want list, recommend, or bind_goal)", action))
			return true
		}

	case "save_domain_learning":
		var args struct {
			Domain  string   `json:"domain"`
			Title   string   `json:"title"`
			Symptom string   `json:"symptom"`
			Cause   string   `json:"cause"`
			Fix     string   `json:"fix"`
			Rule    string   `json:"rule"`
			Tags    []string `json:"tags"`
			Files   []string `json:"files"`
			Project string   `json:"project"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		for field, value := range map[string]string{"domain": args.Domain, "title": args.Title, "symptom": args.Symptom, "fix": args.Fix} {
			if strings.TrimSpace(value) == "" {
				s.sendError(req.ID, -32602, "Invalid params", field+" is required")
				return true
			}
		}
		projectID, err := s.resolveTargetProject(args.Project)
		if err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return true
		}
		projectDir, _ := s.resolveTargetProjectDir(args.Project)
		op := learn.OperationalLearning{
			Domain:        args.Domain,
			Title:         args.Title,
			Symptom:       args.Symptom,
			RootCause:     args.Cause,
			PivotalFix:    args.Fix,
			GuardrailRule: args.Rule,
			Tags:          args.Tags,
			Confidence:    "high",
		}
		content := learn.FormatOperationalLearning(op)
		fact, err := s.store.SaveOperationalLearning(projectID, content, "mcp", projectDir, args.Files)
		if err != nil {
			s.sendError(req.ID, -32603, "Internal error", err.Error())
			return true
		}
		s.recordMemoryEvent(projectID, "learn", "ok", "", 0)
		formatted, _ := json.MarshalIndent(fact, "", "  ")
		s.sendToolResult(req.ID, fact, string(formatted))
		return true

	case "check_symbol_blast_radius":
		var args struct {
			Symbol           string `json:"symbol"`
			Mode             string `json:"mode"`
			CandidatePayload string `json:"candidate_payload"`
			Project          string `json:"project"`
		}
		if len(rawArgs) > 0 {
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				s.sendError(req.ID, -32602, "Invalid params", err.Error())
				return true
			}
		}
		if strings.TrimSpace(args.Symbol) == "" {
			s.sendError(req.ID, -32602, "Invalid params", "symbol is required")
			return true
		}
		dir, err := s.resolveTargetProjectDir(args.Project)
		if err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return true
		}
		mode := strings.ToLower(strings.TrimSpace(args.Mode))
		if mode == "" {
			mode = "edit_check"
		}
		switch mode {
		case "edit_check":
			res, err := contract.EditCheck(dir, args.Symbol, []byte(args.CandidatePayload))
			if err != nil {
				s.sendError(req.ID, -32603, "Internal error", err.Error())
				return true
			}
			formatted, _ := json.MarshalIndent(res, "", "  ")
			s.sendToolResult(req.ID, res, string(formatted))
			return true
		case "safe_delete":
			res, err := contract.VerifySafeDelete(dir, args.Symbol)
			if err != nil {
				s.sendError(req.ID, -32603, "Internal error", err.Error())
				return true
			}
			formatted, _ := json.MarshalIndent(res, "", "  ")
			s.sendToolResult(req.ID, res, string(formatted))
			return true
		default:
			s.sendError(req.ID, -32602, "Invalid params", fmt.Sprintf("unknown mode %q (want edit_check or safe_delete)", mode))
			return true
		}
	}
	return false
}

// parseActivitySince accepts a Go duration (5h), day/week suffix (2d, 1w), an
// RFC3339 timestamp, or YYYY-MM-DD.
func parseActivitySince(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Now().UTC().Add(-24 * time.Hour), nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, nil
	}
	if before, ok := strings.CutSuffix(s, "d"); ok {
		var days int
		if _, err := fmt.Sscanf(before, "%d", &days); err == nil {
			return time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour), nil
		}
	}
	if before, ok := strings.CutSuffix(s, "w"); ok {
		var weeks int
		if _, err := fmt.Sscanf(before, "%d", &weeks); err == nil {
			return time.Now().UTC().Add(-time.Duration(weeks) * 7 * 24 * time.Hour), nil
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid since %q (want duration, 2d, 1w, RFC3339, or YYYY-MM-DD)", s)
	}
	return time.Now().UTC().Add(-d), nil
}
