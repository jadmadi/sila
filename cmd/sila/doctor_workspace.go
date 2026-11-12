package main

import (
	"fmt"
	"github.com/jadmadi/sila/internal/debt"
	"github.com/jadmadi/sila/internal/render"
	"os"
	"strings"

	"github.com/jadmadi/sila/internal/store"
)

// appendWorkspaceRubbishDoctorCheck reports and optionally removes stray
// workspace .sila stores and non-repository state.
func appendWorkspaceRubbishDoctorCheck(rep *store.DoctorReport, st *store.Store, cwd string, repair, dryRun bool) {
	if st == nil || rep.HasCheck("Workspace Store & .sila Hygiene") {
		return
	}
	home, _ := os.UserHomeDir()
	report, err := st.SweepWorkspaceRubbish(store.RubbishSweepOptions{
		CWD:     cwd,
		HomeDir: home,
		TempDir: os.TempDir(),
		DryRun:  !repair || dryRun,
	})
	if err != nil {
		rep.AddCheck("Disk Hygiene", "Workspace Store & .sila Hygiene", store.StatusWarn,
			fmt.Sprintf("Workspace rubbish sweep failed: %v", err), "", "Run `sila doctor --repair` manually.")
		rep.Finalize()
		return
	}
	remaining := report.FoundWorkspaceDBs - report.RemovedWorkspaceDBs
	rubbish := report.OrphanSidecarsRemoved + report.NonRepoStateDirsRemoved + report.CanaryDirsRemoved
	if remaining == 0 && rubbish == 0 {
		detail := fmt.Sprintf("%d workspace stores, %d bytes reclaimed", report.RemovedWorkspaceDBs, report.ReclaimedBytes)
		rep.AddCheck("Disk Hygiene", "Workspace Store & .sila Hygiene", store.StatusPass, "clean", detail, "")
		rep.Finalize()
		return
	}
	detail := fmt.Sprintf("%d stale workspace stores, %d sidecars, %d non-repo state dirs, %d canaries, %d bytes reclaimable",
		remaining, report.OrphanSidecarsRemoved, report.NonRepoStateDirsRemoved, report.CanaryDirsRemoved, report.ReclaimedBytes)
	if repair && !dryRun && remaining == 0 {
		rep.AddCheck("Disk Hygiene", "Workspace Store & .sila Hygiene", store.StatusPass, "cleaned", detail, "")
		rep.Finalize()
		return
	}
	rep.AddCheck("Disk Hygiene", "Workspace Store & .sila Hygiene", store.StatusWarn, "workspace rubbish found", detail, "Run `sila doctor --repair`.")
	rep.Finalize()
}

func appendDebtDoctorCheck(rep *store.DoctorReport, cwd string) {
	for _, c := range rep.Checks {
		if c.Name == "Tracked Technical Debt & Rot Sentry" {
			return
		}
	}
	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	report, err := debt.ScanRepoDebt(cwd, debt.DebtScanOptions{IncludeTests: false})
	if err != nil {
		rep.AddCheck("Audit", "Tracked Technical Debt & Rot Sentry", store.StatusWarn,
			fmt.Sprintf("Failed to scan repository for debt comments: %v", err), "", "")
		rep.Finalize()
		return
	}

	if report.TotalItems == 0 {
		rep.AddCheck("Audit", "Tracked Technical Debt & Rot Sentry", store.StatusPass,
			"0 tracked debt comments (shortcut/ponytail) found; codebase is clean.", "", "")
	} else if report.RotRiskCount > 0 {
		var risks []string
		for _, item := range report.Items {
			if item.Status != debt.StatusHealthy {
				risks = append(risks, fmt.Sprintf("%s:%d (%s): %s", item.File, item.Line, item.Marker, item.Details))
			}
		}
		rep.AddCheck("Audit", "Tracked Technical Debt & Rot Sentry", store.StatusWarn,
			fmt.Sprintf("%d tracked debt comment(s) found (%d rot risk(s) without upgrade triggers or ceilings)",
				report.TotalItems, report.RotRiskCount),
			strings.Join(risks, "\n"),
			"Run `sila doctor --debt` to inspect tracked shortcuts and add explicit upgrade triggers.")
	} else {
		rep.AddCheck("Audit", "Tracked Technical Debt & Rot Sentry", store.StatusPass,
			fmt.Sprintf("%d tracked debt comment(s) properly bounded with ceilings and upgrade triggers.", report.TotalItems),
			"", "")
	}
	rep.Finalize()
}

func runDoctorDebt(ui render.UI, cwd string, jsonOut, toonOut bool) {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	report, err := debt.ScanRepoDebt(cwd, debt.DebtScanOptions{IncludeTests: true})
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error()})
		} else {
			fmt.Fprintln(os.Stderr, ui.Red("sila doctor --debt: "+err.Error()))
		}
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(report)
		return
	}
	if toonOut {
		fmt.Fprint(os.Stdout, renderDoctorDebtTOON(report))
		return
	}

	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · doctor --debt (tracked debt scanner)")))
	fmt.Fprintf(os.Stdout, "  Target:  %s\n", cwd)
	fmt.Fprintf(os.Stdout, "  Summary: %s\n\n", report.Summary)

	if len(report.Items) == 0 {
		fmt.Fprintln(os.Stdout, ui.Green("✔ Zero tracked debt comments (shortcut/ponytail) found; codebase is clean."))
		return
	}

	for _, it := range report.Items {
		var icon, statusStr string
		switch it.Status {
		case debt.StatusHealthy:
			icon = ui.Green("✔")
			statusStr = ui.Green("HEALTHY")
		default:
			icon = ui.Yellow("▲")
			statusStr = ui.Yellow("ROT RISK")
		}

		goalTag := ""
		if it.LinkedGoal != "" {
			goalTag = fmt.Sprintf(" [%s]", ui.Cyan("goal: "+it.LinkedGoal))
		}

		fmt.Fprintf(os.Stdout, "%s %s:%d [%s] %s%s\n", icon, it.File, it.Line, it.Marker, statusStr, goalTag)
		if it.Ceiling != "" {
			fmt.Fprintf(os.Stdout, "    %s %s\n", ui.Dim("ceiling:"), it.Ceiling)
		}
		if it.Trigger != "" {
			fmt.Fprintf(os.Stdout, "    %s %s\n", ui.Dim("trigger:"), it.Trigger)
		}
		if it.Details != "" && it.Status != debt.StatusHealthy {
			fmt.Fprintf(os.Stdout, "    %s %s\n", ui.Red("risk:"), it.Details)
		}
		fmt.Fprintln(os.Stdout)
	}

	if report.RotRiskCount > 0 {
		fmt.Fprintln(os.Stdout, ui.Dim("Shortcuts without upgrade triggers risk permanent code rot."))
		fmt.Fprintln(os.Stdout, ui.Dim("Format: `// shortcut: <ceiling>, <upgrade trigger>` or link with `[goal: <slug>]`."))
	}
}

func renderDoctorDebtTOON(rep *debt.DebtReport) string {
	var b strings.Builder
	summaryFields := [][2]string{
		{"total_items", fmt.Sprintf("%d", rep.TotalItems)},
		{"healthy_count", fmt.Sprintf("%d", rep.HealthyCount)},
		{"rot_risk_count", fmt.Sprintf("%d", rep.RotRiskCount)},
	}
	b.WriteString(render.FormatTOONObject("debt_summary", summaryFields))

	if len(rep.Items) > 0 {
		headers := []string{"file", "line", "marker", "status", "ceiling", "trigger", "linked_goal"}
		var rows [][]string
		for _, it := range rep.Items {
			rows = append(rows, []string{
				it.File,
				fmt.Sprintf("%d", it.Line),
				it.Marker,
				string(it.Status),
				it.Ceiling,
				it.Trigger,
				it.LinkedGoal,
			})
		}
		b.WriteString(render.FormatTOONTable("debt_items", headers, rows))
	}
	return b.String()
}
