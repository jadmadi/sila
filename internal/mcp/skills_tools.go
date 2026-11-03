package mcp

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/jadmadi/sila/internal/skills"
)

type skillSearchParams struct {
	Query   string   `json:"query"`
	Tags    []string `json:"tags,omitempty"`
	Limit   int      `json:"limit,omitempty"`
	Project string   `json:"project,omitempty"`
}

type skillLoadParams struct {
	Slug             string `json:"slug"`
	IncludeResources bool   `json:"include_resources,omitempty"`
	IncludeScripts   bool   `json:"include_scripts,omitempty"`
	Project          string `json:"project,omitempty"`
}

type skillListParams struct {
	Filter  string `json:"filter,omitempty"`
	Project string `json:"project,omitempty"`
}

func (s *Server) handleSkillList(req JSONRPCRequest, rawArgs json.RawMessage) {
	var args skillListParams
	if len(rawArgs) > 0 {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return
		}
	}

	targetDir, err := s.resolveTargetProjectDir(args.Project)
	if err != nil {
		targetDir = "."
	}

	summaries, err := skills.ListSkillSummaries(targetDir, args.Filter)
	if err != nil {
		s.sendError(req.ID, -32603, "Internal error", err.Error())
		return
	}

	structured := map[string]any{
		"total_skills": len(summaries),
		"filter":       args.Filter,
		"skills":       summaries,
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Discovered %d skill(s)", len(summaries))
	if args.Filter != "" {
		fmt.Fprintf(&sb, " matching filter %q", args.Filter)
	}
	sb.WriteString(":\n\n")
	sb.WriteString("| Slug | Source | Tokens | Scripts | Prompt Trigger |\n")
	sb.WriteString("|---|---|:---:|:---:|---|\n")
	for _, sk := range summaries {
		hasScripts := "no"
		if sk.HasScripts {
			hasScripts = "yes"
		}
		trigger := sk.Trigger
		if len(trigger) > 90 {
			trigger = trigger[:87] + "..."
		}
		fmt.Fprintf(&sb, "| `%s` | %s | %d | %s | %s |\n", sk.Slug, sk.Source, sk.EstimatedTokens, hasScripts, trigger)
	}
	sb.WriteString("\n*To retrieve full instructions and supporting resources, invoke `read_skill` with `slug=\"<slug>\"`.*\n")

	s.sendToolResult(req.ID, structured, sb.String())
}

func (s *Server) handleSkillSearch(req JSONRPCRequest, rawArgs json.RawMessage) {
	var args skillSearchParams
	if len(rawArgs) > 0 {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return
		}
	}
	if strings.TrimSpace(args.Query) == "" {
		s.sendError(req.ID, -32602, "Invalid params", "query parameter is required")
		return
	}

	limit := clampLimit(args.Limit, 10, 50)
	targetDir, err := s.resolveTargetProjectDir(args.Project)
	if err != nil {
		targetDir = "."
	}

	fusedSkills, err := skills.SearchCatalogWithLessons(targetDir, args.Query, args.Tags, limit, s.store)
	if err != nil {
		s.sendError(req.ID, -32603, "Internal error", err.Error())
		return
	}

	structured := map[string]any{
		"query":       args.Query,
		"total_found": len(fusedSkills),
		"limit":       limit,
		"skills":      fusedSkills,
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d skill(s) & lesson(s) matching %q:\n\n", len(fusedSkills), args.Query)
	sb.WriteString("| Slug | Tier | Score | Source | Trigger / Fix |\n")
	sb.WriteString("|---|:---:|:---:|---|---|\n")
	for _, f := range fusedSkills {
		tierLabel := "Skill"
		if f.Tier == 2 {
			tierLabel = "Lesson"
		}
		trigger := f.Trigger
		if len(trigger) > 90 {
			trigger = trigger[:87] + "..."
		}
		fmt.Fprintf(&sb, "| `%s` | %s | %.4f | %s | %s |\n", f.Slug, tierLabel, f.RRFScore, f.Source, trigger)
	}
	sb.WriteString("\n*To retrieve full instructions and supporting resources, invoke `read_skill` with `slug=\"<slug>\"`.*\n")

	s.sendToolResult(req.ID, structured, sb.String())
}

func (s *Server) handleSkillLoad(req JSONRPCRequest, rawArgs json.RawMessage) {
	var args skillLoadParams
	if len(rawArgs) > 0 {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return
		}
	}

	if args.IncludeScripts {
		args.IncludeResources = true
	}

	cleanSlug := strings.TrimSpace(args.Slug)
	cleanSlug = strings.TrimPrefix(cleanSlug, ".agents/skills/")
	cleanSlug = strings.TrimSuffix(cleanSlug, "/SKILL.md")
	cleanSlug = strings.TrimSuffix(cleanSlug, "/skill.md")
	cleanSlug = strings.Trim(cleanSlug, "/")

	if cleanSlug == "" || cleanSlug != filepath.Base(cleanSlug) ||
		strings.ContainsAny(cleanSlug, `/\\`) || strings.Contains(cleanSlug, "..") {
		s.sendError(req.ID, -32602, "Invalid params", "invalid skill slug: path traversal or separators prohibited")
		return
	}

	targetDir, err := s.resolveTargetProjectDir(args.Project)
	if err != nil {
		targetDir = "."
	}

	if skills.IsDeniedByPolicy(targetDir, cleanSlug) {
		s.sendError(req.ID, -32603, "Access denied", fmt.Sprintf("skill %q is explicitly denied by project policy", cleanSlug))
		return
	}

	skill, err := skills.LoadSkill(targetDir, cleanSlug, args.IncludeResources)
	if err != nil {
		s.sendError(req.ID, -32602, "Skill not found", err.Error())
		return
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "# Skill: %s (source: %s)\n", skill.Slug, skill.Source)
	if skill.Description != "" {
		fmt.Fprintf(&sb, "**Description**: %s\n", skill.Description)
	}
	fmt.Fprintf(&sb, "**Bundle Directory**: `%s`\n", skill.BundleDir)
	fmt.Fprintf(&sb, "**Content Hash**: `%s`\n\n", skill.ContentHash)
	sb.WriteString("## Instructions\n")
	sb.WriteString(skill.Instructions)
	sb.WriteString("\n\n")

	if len(skill.Scripts) > 0 || len(skill.References) > 0 {
		sb.WriteString("## Available Resources\n")
		for _, sc := range skill.Scripts {
			fmt.Fprintf(&sb, "- `%s` (script)\n", sc)
		}
		for _, rf := range skill.References {
			fmt.Fprintf(&sb, "- `%s` (reference)\n", rf)
		}
		sb.WriteString("\n")
	}

	if args.IncludeResources && len(skill.Resources) > 0 {
		sb.WriteString("## Resource Contents\n")
		for _, res := range skill.Resources {
			fmt.Fprintf(&sb, "### %s (%s)\n```\n%s\n```\n\n", res.Path, res.Type, res.Content)
		}
	}

	s.sendToolResult(req.ID, skill, sb.String())
}
