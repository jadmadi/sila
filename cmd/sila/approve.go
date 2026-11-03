package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/config"
	"github.com/jadmadi/sila/internal/gateway"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

// runApprove handles the 'sila approve' and 'sila gateway approve' command.
func runApprove(ui render.UI, dbPath, effProject string, reject bool, by, reason string, pendingOnly, jsonOut, toonOut bool, args []string) {
	sub := ""
	if len(args) > 0 {
		sub = strings.TrimSpace(args[0])
	}

	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila approve: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	// Initialize default coordinator with active store and gateway registry if configured
	cfg, _ := config.Load("")
	var reg *gateway.Registry
	if cfg != nil {
		reg = gateway.NewFromConfig(cfg)
	}
	coord := gateway.GetDefaultCoordinator(st, reg)

	switch {
	case sub == "" || sub == "list" || sub == "pending":
		listApprovals(ui, st, effProject, pendingOnly || sub == "pending" || sub == "", jsonOut, toonOut)

	case sub == "show" || sub == "get":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: sila approve show <approval_id> [--json]")
			os.Exit(2)
		}
		showApproval(ui, st, args[1], jsonOut, toonOut)

	case sub == "approve":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: sila approve approve <approval_id> [--by=...] [--reason=...] [--json]")
			os.Exit(2)
		}
		resolveApproval(ui, coord, args[1], true, by, reason, jsonOut, toonOut)

	case sub == "reject":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: sila approve reject <approval_id> [--by=...] [--reason=...] [--json]")
			os.Exit(2)
		}
		resolveApproval(ui, coord, args[1], false, by, reason, jsonOut, toonOut)

	default:
		// If the argument starts with "appr_" or is a direct approval ID:
		targetID := sub
		approved := !reject
		resolveApproval(ui, coord, targetID, approved, by, reason, jsonOut, toonOut)
	}
}

func listApprovals(ui render.UI, st *store.Store, project string, pendingOnly, jsonOut, toonOut bool) {
	var approvals []store.Approval
	var err error
	if pendingOnly {
		approvals, err = st.ListPendingApprovals(project)
	} else {
		approvals, err = st.ListApprovals(project, 50)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila approve: failed to list approvals: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if approvals == nil {
			approvals = []store.Approval{}
		}
		_ = enc.Encode(approvals)
		return
	}

	if len(approvals) == 0 {
		if pendingOnly {
			fmt.Fprintln(os.Stdout, ui.Dim("No pending approval requests."))
		} else {
			fmt.Fprintln(os.Stdout, ui.Dim("No approval records found."))
		}
		return
	}

	fmt.Fprintf(os.Stdout, "  %-14s  %-12s  %-10s  %-12s  %-30s  %s\n", "ID", "PROJECT", "RISK", "STATUS", "PROMPT", "CREATED")
	fmt.Fprintf(os.Stdout, "  %-14s  %-12s  %-10s  %-12s  %-30s  %s\n", "--------------", "------------", "----------", "------------", "------------------------------", "-------")

	for _, a := range approvals {
		statusStr := a.Status
		switch a.Status {
		case "approved":
			statusStr = ui.Green("approved")
		case "rejected":
			statusStr = ui.Red("rejected")
		case "pending":
			statusStr = ui.Yellow("pending")
		case "timed_out":
			statusStr = ui.Dim("timed_out")
		default:
			statusStr = ui.Dim(a.Status)
		}

		riskStr := a.RiskLevel
		switch strings.ToLower(a.RiskLevel) {
		case "critical":
			riskStr = ui.Red(ui.Bold("CRITICAL"))
		case "high":
			riskStr = ui.Red("HIGH")
		case "medium":
			riskStr = ui.Yellow("MEDIUM")
		case "low":
			riskStr = ui.Dim("LOW")
		}

		projStr := a.Project
		if projStr == "" {
			projStr = ui.Dim("-")
		}

		promptShort := a.Prompt
		if len(promptShort) > 30 {
			promptShort = promptShort[:27] + "..."
		}

		createdStr := a.CreatedAt.Format("15:04:05")
		if time.Since(a.CreatedAt) > 24*time.Hour {
			createdStr = a.CreatedAt.Format("2006-01-02")
		}

		fmt.Fprintf(os.Stdout, "  %-14s  %-12s  %-10s  %-12s  %-30s  %s\n",
			ui.Bold(a.ID),
			projStr,
			riskStr,
			statusStr,
			promptShort,
			ui.Dim(createdStr),
		)
	}
	fmt.Fprintln(os.Stdout)
}

func showApproval(ui render.UI, st *store.Store, id string, jsonOut, toonOut bool) {
	appr, err := st.GetApproval(id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila approve: %v\n", err)
		os.Exit(1)
	}
	if appr == nil {
		fmt.Fprintf(os.Stderr, "sila approve: approval %q not found\n", id)
		os.Exit(1)
	}

	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(appr)
		return
	}

	statusStr := appr.Status
	switch appr.Status {
	case "approved":
		statusStr = ui.Green(ui.Bold("APPROVED"))
	case "rejected":
		statusStr = ui.Red(ui.Bold("REJECTED"))
	case "pending":
		statusStr = ui.Yellow(ui.Bold("PENDING"))
	case "timed_out":
		statusStr = ui.Dim("TIMED OUT")
	}

	fmt.Fprintf(os.Stdout, "%s %s [%s]\n\n", ui.Bold("Approval Request:"), ui.Bold(appr.ID), statusStr)
	fmt.Fprintf(os.Stdout, "  Project:    %s\n", appr.Project)
	fmt.Fprintf(os.Stdout, "  Agent:      %s\n", appr.Agent)
	fmt.Fprintf(os.Stdout, "  Risk Level: %s\n", appr.RiskLevel)
	fmt.Fprintf(os.Stdout, "  Created At: %s\n", appr.CreatedAt.Format(time.RFC3339))
	if !appr.DecidedAt.IsZero() {
		fmt.Fprintf(os.Stdout, "  Decided At: %s\n", appr.DecidedAt.Format(time.RFC3339))
		fmt.Fprintf(os.Stdout, "  Decided By: %s\n", appr.DecidedBy)
	}
	if appr.DecisionReason != "" {
		fmt.Fprintf(os.Stdout, "  Reason:     %s\n", appr.DecisionReason)
	}
	fmt.Fprintf(os.Stdout, "\n  Prompt:\n    %s\n", strings.ReplaceAll(appr.Prompt, "\n", "\n    "))
	if len(appr.Metadata) > 0 {
		fmt.Fprintln(os.Stdout, "\n  Metadata:")
		for k, v := range appr.Metadata {
			fmt.Fprintf(os.Stdout, "    %s: %s\n", k, v)
		}
	}
	fmt.Fprintln(os.Stdout)
}

func resolveApproval(ui render.UI, coord *gateway.ApprovalCoordinator, id string, approved bool, by, reason string, jsonOut, toonOut bool) {
	if strings.TrimSpace(by) == "" {
		by = os.Getenv("USER")
		if by == "" {
			by = os.Getenv("LOGNAME")
		}
		if by == "" {
			by = "human:cli"
		} else {
			by = "human:" + by
		}
	}

	appr, err := coord.Get(id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila approve: %v\n", err)
		os.Exit(1)
	}
	if appr == nil {
		fmt.Fprintf(os.Stderr, "sila approve: approval %q not found\n", id)
		os.Exit(1)
	}
	if appr.Status != "pending" {
		fmt.Fprintf(os.Stderr, "sila approve: approval %q has already been resolved as %s (by %s)\n", id, appr.Status, appr.DecidedBy)
		os.Exit(1)
	}

	res, err := coord.Resolve(id, approved, by, reason)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila approve: failed to resolve approval: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(res)
		return
	}

	if approved {
		fmt.Fprintf(os.Stdout, "%s Approval %s %s by %s\n", ui.Green("✔"), ui.Bold(id), ui.Green("approved"), ui.Bold(by))
	} else {
		fmt.Fprintf(os.Stdout, "%s Approval %s %s by %s\n", ui.Red("✖"), ui.Bold(id), ui.Red("rejected"), ui.Bold(by))
	}
	if reason != "" {
		fmt.Fprintf(os.Stdout, "  Reason: %s\n", reason)
	}
}
