package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/clean"
	"github.com/jadmadi/sila/internal/render"
)

type auditHygieneParams struct {
	Scope   string   `json:"scope,omitempty"`
	Targets []string `json:"targets,omitempty"`
	Heavy   bool     `json:"heavy,omitempty"`
	Project string   `json:"project,omitempty"`
}

type cleanCachesParams struct {
	Scope   string   `json:"scope,omitempty"`
	Targets []string `json:"targets,omitempty"`
	Heavy   bool     `json:"heavy,omitempty"`
	DryRun  bool     `json:"dry_run,omitempty"`
	Project string   `json:"project,omitempty"`
}

func parseCleanScope(raw string) clean.Scope {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "repo", "repository", "local":
		return clean.ScopeRepo
	case "global", "system", "user":
		return clean.ScopeGlobal
	default:
		return clean.ScopeAll
	}
}

func (s *Server) handleAuditHygiene(req JSONRPCRequest, rawArgs json.RawMessage) {
	var args auditHygieneParams
	if len(rawArgs) > 0 {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return
		}
	}

	targetDir, err := s.resolveTargetProjectDir(args.Project)
	if err != nil || targetDir == "" {
		targetDir = "."
	}

	scope := parseCleanScope(args.Scope)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	reg := clean.DefaultRegistry()
	reports, err := reg.InspectAll(ctx, targetDir, args.Targets, scope, args.Heavy)
	if err != nil {
		s.sendError(req.ID, -32603, "Internal error", err.Error())
		return
	}

	totalItems := 0
	var totalReclaimable int64
	for _, rep := range reports {
		totalItems += len(rep.Items)
		totalReclaimable += rep.TotalBytes
	}

	structured := map[string]any{
		"scope":             string(scope),
		"heavy":             args.Heavy,
		"reclaimable_bytes": totalReclaimable,
		"reclaimable_human": render.FormatBytes(totalReclaimable),
		"total_items":       totalItems,
		"reports":           reports,
	}

	var sb strings.Builder
	if totalItems == 0 {
		sb.WriteString("All toolchain caches and repository build artifacts are clean. Reclaimable: 0 B.\n")
	} else {
		fmt.Fprintf(&sb, "Found %d cleanable item(s) across %d toolchain(s) — Reclaimable: %s\n\n",
			totalItems, len(reports), render.FormatBytes(totalReclaimable))
		sb.WriteString("| Toolchain | Item | Category | Reclaimable | Target Path |\n")
		sb.WriteString("|---|---|---|---:|---|\n")
		for _, rep := range reports {
			for _, it := range rep.Items {
				cat := "Global Cache"
				if it.Category == "repo_artifact" {
					cat = "Repo Artifact"
				} else if it.Category == "global_store" {
					cat = "Global Store"
				}
				sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s | `%s` |\n",
					rep.DisplayName, it.Name, cat, render.FormatBytes(it.Bytes), it.Path))
			}
		}
		sb.WriteString("\n*To prune these caches, invoke `clean_caches` with `scope` and optional `targets`.*")
	}

	s.sendToolResult(req.ID, structured, sb.String())
}

func (s *Server) handleCleanCaches(req JSONRPCRequest, rawArgs json.RawMessage) {
	var args cleanCachesParams
	if len(rawArgs) > 0 {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return
		}
	}

	targetDir, err := s.resolveTargetProjectDir(args.Project)
	if err != nil || targetDir == "" {
		targetDir = "."
	}

	scope := parseCleanScope(args.Scope)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	reg := clean.DefaultRegistry()
	results, err := reg.CleanAll(ctx, targetDir, args.Targets, scope, args.Heavy, args.DryRun)
	if err != nil {
		s.sendError(req.ID, -32603, "Internal error", err.Error())
		return
	}

	totalItems := 0
	var totalReclaimed int64
	for _, res := range results {
		totalItems += len(res.Results)
		totalReclaimed += res.TotalReclaimed
	}

	structured := map[string]any{
		"dry_run":         args.DryRun,
		"scope":           string(scope),
		"reclaimed_bytes": totalReclaimed,
		"reclaimed_human": render.FormatBytes(totalReclaimed),
		"total_items":     totalItems,
		"results":         results,
	}

	var sb strings.Builder
	actionVerb := "Reclaimed"
	if args.DryRun {
		actionVerb = "Would reclaim"
	}
	fmt.Fprintf(&sb, "%s %s across %d toolchain(s):\n\n", actionVerb, render.FormatBytes(totalReclaimed), len(results))
	for _, res := range results {
		sb.WriteString(fmt.Sprintf("- **%s**: %s (%d item(s))\n",
			res.DisplayName, render.FormatBytes(res.TotalReclaimed), len(res.Results)))
		for _, ir := range res.Results {
			status := "cleaned"
			if !ir.Success {
				status = fmt.Sprintf("failed: %s", ir.Error)
			}
			sb.WriteString(fmt.Sprintf("  - %s: %s (%s)\n", ir.Item.Name, render.FormatBytes(ir.ReclaimedBytes), status))
		}
	}

	s.sendToolResult(req.ID, structured, sb.String())
}
