package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/adoption"
	"github.com/jadmadi/sila/internal/assess"
	"github.com/jadmadi/sila/internal/assist"
	"github.com/jadmadi/sila/internal/clean"
	"github.com/jadmadi/sila/internal/daemon"
	"github.com/jadmadi/sila/internal/debt"
	silagate "github.com/jadmadi/sila/internal/gate"
	"github.com/jadmadi/sila/internal/heal"
	"github.com/jadmadi/sila/internal/importmem"
	"github.com/jadmadi/sila/internal/mcp"
	"github.com/jadmadi/sila/internal/memory"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

// runDoctor runs an end-to-end system diagnostic audit on DB integrity, FTS5 sync, storage, skill bloat, covenant compliance, tracked debt, and edge test isolation.
var doctorMemory bool
var doctorAdoption bool

func runDoctor(ui render.UI, dbPath, project string, repair, dryRun, jsonOut, toonOut, skills, covenant, edge, debtFlag, mcpOnly bool) {
	if doctorAdoption {
		st, _, err := openReadStore(dbPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila doctor --adoption: %v\n", err)
			return
		}
		defer st.Close()
		report, err := adoption.BuildFunnelReport(context.Background(), st, adoption.ReportOptions{
			ProjectID: project, Since: time.Now().UTC().Add(-7 * 24 * time.Hour),
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila doctor --adoption: %v\n", err)
			return
		}
		report.Verdict = adoption.Verdict(report)
		if jsonOut || toonOut {
			emitJSON(report)
			return
		}
		fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · doctor (adoption)"))+" — advisory only")
		fmt.Fprintf(os.Stdout, "  Verdict:  %s\n", strings.ToUpper(report.Verdict))
		fmt.Fprintf(os.Stdout, "  Sessions: %d · prime %.1f%% · writes %.1f%% · handoff %.1f%%\n",
			report.Sessions.Total,
			report.Rates["prime"].Value*100, report.Rates["write"].Value*100, report.Rates["handoff"].Value*100)
		fmt.Fprintf(os.Stdout, "  Tools:    %d/%d continuity tools used\n", report.Coverage.Used, report.Coverage.Exposed)
		return
	}
	if doctorMemory {
		st, _, err := openReadStore(dbPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila doctor --memory: %v\n", err)
			return
		}
		defer st.Close()
		since := time.Now().UTC().Add(-7 * 24 * time.Hour)
		events, err := st.ListMemoryEvents(store.MemoryEventFilter{ProjectID: project, Since: since, Limit: 100000})
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila doctor --memory: %v\n", err)
			return
		}
		cwd, _ := os.Getwd()
		report := memory.BuildReport(events, memory.CollectGoalStats(cwd, since), project, since, time.Now().UTC())
		report.Verdict = memory.Verdict(report)
		if jsonOut || toonOut {
			emitJSON(report)
			return
		}
		fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · doctor (memory)"))+" — advisory only")
		fmt.Fprintf(os.Stdout, "  Verdict:  %s\n", strings.ToUpper(report.Verdict))
		fmt.Fprintf(os.Stdout, "  Sessions: %d · prime %.0f%% · recall %.0f%% · handoff %.0f%%\n",
			report.Sessions.Total, report.Sessions.PrimeRate*100, report.Sessions.RecallRate*100, report.Sessions.HandoffRate*100)
		fmt.Fprintf(os.Stdout, "  Goals:    %d implemented · write compliance %.0f%%\n", report.Goals.Implemented, report.Goals.WriteComplianceRate*100)
		fmt.Fprintf(os.Stdout, "  Gate:     pass=%d fail=%d waiver=%d skip=%d\n", report.Gate.Pass, report.Gate.Fail, report.Gate.Waiver, report.Gate.Skip)
		return
	}
	if mcpOnly {
		rep := &store.DoctorReport{Timestamp: time.Now().UTC()}
		appendMCPFreshnessCheck(rep)
		bridge := buildMCPBridgeDiagnostic(os.Getenv("HOME"))
		daemonReport := daemon.RunMCPDoctor(daemon.MCPDoctorOptions{Repair: repair, DryRun: dryRun})
		if jsonOut || toonOut {
			emitJSON(map[string]any{
				"freshness": rep.MCP,
				"bridge":    bridge,
				"checks":    rep.Checks,
				"mcp":       daemonReport,
			})
			if !daemonReport.Healthy && !repair {
				os.Exit(1)
			}
			return
		}
		fmt.Fprintf(os.Stdout, "%s\n", ui.Cyan(ui.Bold("sila · doctor (MCP)")))
		fmt.Fprintf(os.Stdout, "  Running: %d\n", rep.MCP.Running)
		fmt.Fprintf(os.Stdout, "  Stale:   %d\n", rep.MCP.Stale)
		fmt.Fprintf(os.Stdout, "  Restart: %t\n", rep.MCP.RestartRequired)
		fmt.Fprintf(os.Stdout, "  Continuity tools: %d (profile: %s)\n", bridge.ContinuityTools, bridge.Profile)
		fmt.Fprintf(os.Stdout, "  Schema digest:    %s\n", bridge.SchemaDigest)
		fmt.Fprintf(os.Stdout, "  Expected envelope: %s\n", bridge.ExpectedEnvelope)
		fmt.Fprintf(os.Stdout, "  Last proxy error:  %s\n", bridge.LastProxyError)
		rows := make([][]string, 0, len(daemonReport.Checks))
		for _, check := range daemonReport.Checks {
			rows = append(rows, []string{check.Name, check.Status, check.Detail})
		}
		fmt.Fprint(os.Stdout, ui.Table([]string{"check", "status", "detail"}, rows, []string{"left", "left", "left"}))
		for _, action := range daemonReport.Actions {
			fmt.Fprintf(os.Stdout, "  action: %s\n", action)
		}
		if !daemonReport.Healthy && !repair {
			os.Exit(1)
		}
		return
	}
	cwd, _ := os.Getwd()
	targetDir := cwd
	if project != "" {
		if st, err := store.OpenRO(dbPath); err == nil {
			if p, err := st.ResolveScope(project, cwd); err == nil && p.Path != "" {
				targetDir = p.Path
			}
			st.Close()
		}
	}
	if debtFlag {
		runDoctorDebt(ui, targetDir, jsonOut, toonOut)
		return
	}
	if edge {
		runDoctorEdge(ui, targetDir, jsonOut, toonOut)
		return
	}
	if covenant {
		runDoctorCovenant(ui, dbPath, targetDir, jsonOut, toonOut)
		return
	}

	// Diagnosis is a read operation: without --repair the store opens
	// read-only, so a health check never migrates or chmods anything.
	var st *store.Store
	var err error
	if repair {
		st, err = store.Open(dbPath)
	} else {
		st, _, err = openReadStore(dbPath)
	}
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error(), "store": dbPath})
		} else {
			fmt.Fprintln(os.Stderr, ui.Red("sila doctor: "+err.Error()))
		}
		os.Exit(1)
	}
	defer st.Close()

	if skills {
		runDoctorSkills(ui, st, project, jsonOut, toonOut)
		return
	}

	rep, err := st.Doctor(repair)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila doctor: %v\n", err)
		os.Exit(1)
	}
	// Running-server freshness belongs to the command layer: it is how sila is
	// being run right now, not a property of the store.
	appendMCPFreshnessCheck(rep)
	appendAssistanceDoctorCheck(rep)
	appendToolchainDoctorCheck(rep, repair)
	appendStorageBloatDoctorCheck(rep, repair)
	appendSkillsDoctorCheck(rep, st, project)
	appendCovenantDoctorCheck(rep, dbPath)
	appendEdgeDoctorCheck(rep, cwd)
	appendCFCleanDoctorCheck(rep, cwd, repair)
	appendRulesBudgetDoctorCheck(rep, cwd, repair)
	appendDebtDoctorCheck(rep, cwd)
	appendNegativeSignalsDoctorCheck(rep, st, project, cwd)
	recallGateDoctorCheck(rep, targetDir)
	appendImportManifestDoctorCheck(rep, cwd)

	if jsonOut {
		emitJSON(rep)
		return
	}
	if toonOut {
		fmt.Fprint(os.Stdout, renderDoctorTOON(rep))
		return
	}

	fmt.Fprint(os.Stdout, render.RenderHeader(ui, render.Header{Command: "doctor"}))
	fmt.Fprintf(os.Stdout, "  Store: %s\n\n", rep.StorePath)

	for _, c := range rep.Checks {
		var icon, statusStr string
		switch c.Status {
		case store.StatusPass:
			icon = ui.Green("✔")
			statusStr = ui.Green("PASS")
		case store.StatusWarn:
			icon = ui.Yellow("▲")
			statusStr = ui.Yellow("WARN")
		case store.StatusFail:
			icon = ui.Red("✖")
			statusStr = ui.Red("FAIL")
		}

		fmt.Fprintf(os.Stdout, "  %s %s [%s] %s\n", icon, statusStr, ui.Dim(c.Category), c.Name)
		fmt.Fprintf(os.Stdout, "    %s\n", c.Message)
		if c.Detail != "" {
			fmt.Fprintf(os.Stdout, "    %s\n", ui.Dim("Detail: "+c.Detail))
		}
		if c.Repair != "" && !repair {
			fmt.Fprintf(os.Stdout, "    %s\n", ui.Cyan("→ Suggested Fix: "+c.Repair))
		}
		fmt.Fprintln(os.Stdout)
	}

	if rep.RepairExecuted {
		fmt.Fprintln(os.Stdout, ui.Bold(ui.Green("Repairs Executed:")))
		for _, item := range rep.RepairedItems {
			fmt.Fprintf(os.Stdout, "  ✔ %s\n", item)
		}
		fmt.Fprintln(os.Stdout)
	}

	summary := fmt.Sprintf("Summary: %d passed, %d warnings, %d failures.", rep.Passed, rep.Warnings, rep.Failures)
	switch rep.OverallStatus {
	case store.StatusPass:
		fmt.Fprintln(os.Stdout, ui.Bold(ui.Green("✔ "+summary)))
	case store.StatusWarn:
		fmt.Fprintln(os.Stdout, ui.Bold(ui.Yellow("▲ "+summary)))
		if !repair {
			fmt.Fprintln(os.Stdout, actionLine(ui, "sila doctor --repair", "automatically resolve index and storage warnings"))
		}
	case store.StatusFail:
		fmt.Fprintln(os.Stdout, ui.Bold(ui.Red("✖ "+summary)))
	}
}

// runDoctorSkills executes the skill and MCP bloat audit and renders formatted tables and token estimates.
func appendImportManifestDoctorCheck(rep *store.DoctorReport, cwd string) {
	for _, c := range rep.Checks {
		if c.Name == "Agent Memory Import Cache" {
			return
		}
	}
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	status := importmem.ReadManifestStatus(cwd, time.Now().UTC())
	switch {
	case !status.Exists:
		rep.AddCheck("Import", "Agent Memory Import Cache", store.StatusPass,
			"No agent-memory import cache exists (optional).", "", "")
	case status.Error != "":
		rep.AddCheck("Import", "Agent Memory Import Cache", store.StatusWarn,
			"The agent-memory import cache cannot be read.", status.Error,
			"Remove .sila/state/import/manifest.json and re-run `sila import --yes`.")
	case status.Stale:
		rep.AddCheck("Import", "Agent Memory Import Cache", store.StatusWarn,
			fmt.Sprintf("The agent-memory import cache is stale (%d entries).", status.Entries),
			fmt.Sprintf("updated_at=%s", status.UpdatedAt),
			"Re-run `sila import --yes` to refresh the cache.")
	default:
		rep.AddCheck("Import", "Agent Memory Import Cache", store.StatusPass,
			fmt.Sprintf("Agent-memory import cache is current (%d entries).", status.Entries),
			"", "")
	}
	rep.Finalize()
}

func runDoctorSkills(ui render.UI, st *store.Store, project string, jsonOut, toonOut bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cwd, _ := os.Getwd()
	audit, err := assess.AuditSkillBloat(ctx, st, assess.AuditOptions{
		ProjectID:    project,
		ProjectDir:   cwd,
		AuditPeriod:  30 * 24 * time.Hour,
		BuiltinTools: defaultSilaRegisteredTools(),
	})
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error()})
		} else {
			fmt.Fprintf(os.Stderr, "%s %v\n", ui.Red("✖"), err)
		}
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(audit)
		return
	}
	if toonOut {
		fmt.Fprint(os.Stdout, renderSkillsAuditTOON(audit))
		return
	}

	fmt.Fprint(os.Stdout, render.RenderHeader(ui, render.Header{Command: "doctor --skills", Title: fmt.Sprintf("audit window: last %d days", audit.AuditPeriodDays)}))
	fmt.Fprintln(os.Stdout)

	// Summary Cards
	fmt.Fprintf(os.Stdout, "  %s  %s registered tools (%s active, %s dormant)\n",
		ui.Bold("Tools:"),
		ui.Bold(fmt.Sprintf("%d", audit.TotalRegistered)),
		ui.Green(fmt.Sprintf("%d", audit.ActiveCount)),
		ui.Yellow(fmt.Sprintf("%d", audit.DormantCount)),
	)
	if audit.WastedTokensTurn > 0 {
		fmt.Fprintf(os.Stdout, "  %s  ~%s tokens / turn attributable to dormant schemas\n",
			ui.Bold("Waste:"),
			ui.Yellow(ui.Bold(fmt.Sprintf("%d", audit.WastedTokensTurn))),
		)
	} else {
		fmt.Fprintf(os.Stdout, "  %s  %s\n", ui.Bold("Waste:"), ui.Green("0 tokens / turn (prompt context optimal)"))
	}
	fmt.Fprintln(os.Stdout)

	// Dormant Tools Table
	if len(audit.DormantTools) > 0 {
		fmt.Fprintln(os.Stdout, ui.Bold(ui.Yellow("Dormant Tools & Skills (0 calls in audit period):")))
		headers := []string{"SERVER", "TOOL", "SOURCE", "WASTE/TURN"}
		var rows [][]string
		for _, dt := range audit.DormantTools {
			sourceShort := dt.SourceConfig
			if len(sourceShort) > 35 {
				sourceShort = "…" + sourceShort[len(sourceShort)-34:]
			}
			rows = append(rows, []string{
				dt.ServerName,
				ui.Bold(dt.ToolName),
				ui.Dim(sourceShort),
				fmt.Sprintf("~%d tok", dt.EstimatedTokens),
			})
		}
		fmt.Fprintln(os.Stdout, ui.Table(headers, rows, []string{"l", "l", "l", "r"}))
		fmt.Fprintln(os.Stdout)
	}

	// Active Tools Table
	if len(audit.ActiveTools) > 0 {
		fmt.Fprintln(os.Stdout, ui.Bold(ui.Green("Active Tools & Skills:")))
		headers := []string{"SERVER", "TOOL", "CALLS", "LAST INVOKED", "HARNESSES"}
		var rows [][]string
		for i, at := range audit.ActiveTools {
			if i >= 15 {
				break
			}
			var hList []string
			for h, cnt := range at.Harnesses {
				hList = append(hList, fmt.Sprintf("%s:%d", h, cnt))
			}
			sort.Strings(hList)
			lastTime := at.LastInvoked.Format("2006-01-02 15:04")
			if at.LastInvoked.IsZero() {
				lastTime = "unknown"
			}
			rows = append(rows, []string{
				at.ServerName,
				ui.Bold(at.ToolName),
				fmt.Sprintf("%d", at.CallCount),
				ui.Dim(lastTime),
				strings.Join(hList, ", "),
			})
		}
		fmt.Fprintln(os.Stdout, ui.Table(headers, rows, []string{"l", "l", "r", "l", "l"}))
		fmt.Fprintln(os.Stdout)
	}

	// Actionable Recommendations
	if len(audit.Recommendations) > 0 {
		fmt.Fprintln(os.Stdout, ui.Bold("Recommendations:"))
		for _, rec := range audit.Recommendations {
			fmt.Fprintf(os.Stdout, "  %s %s\n", ui.Cyan("→"), rec)
		}
		fmt.Fprintln(os.Stdout)
	}
}

func appendSkillsDoctorCheck(rep *store.DoctorReport, st *store.Store, project string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cwd, _ := os.Getwd()
	audit, err := assess.AuditSkillBloat(ctx, st, assess.AuditOptions{
		ProjectID:    project,
		ProjectDir:   cwd,
		AuditPeriod:  30 * 24 * time.Hour,
		BuiltinTools: defaultSilaRegisteredTools(),
	})
	if err != nil {
		rep.AddCheck("Skills", "Agent Skill & MCP Bloat", store.StatusWarn,
			fmt.Sprintf("Unable to evaluate skill bloat: %v", err), "", "")
		rep.Finalize()
		return
	}

	if audit.DormantCount > 0 {
		detail := fmt.Sprintf("%d dormant tools (~%d wasted tok/turn across %d registered tools).",
			audit.DormantCount, audit.WastedTokensTurn, audit.TotalRegistered)
		repair := "Run `sila doctor --skills` to inspect and prune dormant tools."
		rep.AddCheck("Skills", "Agent Skill & MCP Bloat", store.StatusWarn,
			fmt.Sprintf("%d dormant MCP tools/skills detected (~%d tokens/turn wasted context).", audit.DormantCount, audit.WastedTokensTurn),
			detail, repair)
	} else {
		rep.AddCheck("Skills", "Agent Skill & MCP Bloat", store.StatusPass,
			fmt.Sprintf("All %d registered MCP tools/skills actively utilized; zero dormant context bloat.", audit.TotalRegistered),
			"", "")
	}
	rep.Finalize()
}

func appendAssistanceDoctorCheck(rep *store.DoctorReport) {
	provider := assist.ActiveProviderFor("doctor", "")
	if provider == nil {
		rep.AddCheck("Runtime", "Semantic Assistance", store.StatusPass,
			"Optional semantic assistance unconfigured (pure local offline operation).", "", "")
		rep.Finalize()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	res, err := assist.VerifyFact(ctx, provider, "Sila doctor assistance ping", "main.go", "package main")
	if err != nil || res == nil {
		rep.AddCheck("Runtime", "Semantic Assistance", store.StatusWarn,
			fmt.Sprintf("Semantic assistance configured (%s) but ping failed or timed out.", provider.Name()),
			"", "Verify API key and network connectivity; core operations fail-open safely.")
	} else {
		rep.AddCheck("Runtime", "Semantic Assistance", store.StatusPass,
			fmt.Sprintf("Semantic assistance active (%s, %dms RTT). Rerank, diff sentry, and fact verification enabled.", provider.Name(), res.DurationMs),
			"", "")
	}
	rep.Finalize()
}

func appendToolchainDoctorCheck(rep *store.DoctorReport, repair bool) {
	if rep.HasCheck("Toolchain") {
		return
	}
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	check := store.AuditToolchain(cwd)
	rep.AddCheck(check.Category, check.Name, check.Status, check.Message, check.Detail, check.Repair)
	if repair && check.Status != store.StatusPass {
		if repaired, err := store.RepairToolchain(cwd); err == nil && len(repaired) > 0 {
			rep.RepairExecuted = true
			rep.RepairedItems = append(rep.RepairedItems, repaired...)
		}
	}
	rep.Finalize()
}

func appendStorageBloatDoctorCheck(rep *store.DoctorReport, repair bool) {
	if rep.HasCheck("Toolchain Storage Bloat") {
		return
	}
	cwd, err := os.Getwd()
	if err != nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	reg := clean.DefaultRegistry()
	reports, err := reg.InspectAll(ctx, cwd, nil, clean.ScopeAll, false)
	if err != nil {
		rep.AddCheck("Disk Hygiene", "Toolchain Storage Bloat", store.StatusWarn,
			fmt.Sprintf("Failed to audit toolchain caches: %v", err), "", "Run `sila clean` to inspect manually.")
		rep.Finalize()
		return
	}

	var totalReclaimable int64
	activeToolchains := 0
	var topContributors []string

	for _, r := range reports {
		if r.TotalBytes > 0 {
			activeToolchains++
			totalReclaimable += r.TotalBytes
			if r.TotalBytes > 250*1024*1024 { // > 250 MB
				topContributors = append(topContributors, fmt.Sprintf("%s (%s)", r.DisplayName, render.FormatBytes(r.TotalBytes)))
			}
		}
	}

	// Threshold: 2 GB of reclaimable safe caches triggers a WARN
	const warnThreshold = 2 * 1024 * 1024 * 1024
	if totalReclaimable >= warnThreshold {
		detail := ""
		if len(topContributors) > 0 {
			detail = "Largest caches: " + strings.Join(topContributors, ", ")
		}
		if repair {
			cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cleanCancel()
			results, cleanErr := reg.CleanAll(cleanCtx, cwd, nil, clean.ScopeAll, false, false)
			var totalCleaned int64
			for _, r := range results {
				totalCleaned += r.TotalReclaimed
			}
			if cleanErr == nil && totalCleaned > 0 {
				rep.RepairExecuted = true
				rep.RepairedItems = append(rep.RepairedItems, fmt.Sprintf("Toolchain disk hygiene: reclaimed %s across %d toolchain(s)", render.FormatBytes(totalCleaned), len(results)))
				rep.AddCheck("Disk Hygiene", "Toolchain Storage Bloat", store.StatusPass,
					fmt.Sprintf("Toolchain caches cleaned via repair (reclaimed %s).", render.FormatBytes(totalCleaned)),
					"", "")
				rep.Finalize()
				return
			}
		}
		rep.AddCheck("Disk Hygiene", "Toolchain Storage Bloat", store.StatusWarn,
			fmt.Sprintf("Toolchain caches are consuming %s across %d toolchain(s).", render.FormatBytes(totalReclaimable), activeToolchains),
			detail, "Run `sila clean` to preview or `sila clean -y` to reclaim disk space.")
	} else if totalReclaimable > 0 {
		rep.AddCheck("Disk Hygiene", "Toolchain Storage Bloat", store.StatusPass,
			fmt.Sprintf("Toolchain disk hygiene healthy (%s across %d toolchain(s)).", render.FormatBytes(totalReclaimable), activeToolchains),
			"", "")
	} else {
		rep.AddCheck("Disk Hygiene", "Toolchain Storage Bloat", store.StatusPass,
			"Toolchain disk hygiene optimal (zero stale caches detected).", "", "")
	}
	rep.Finalize()
}

func renderDoctorTOON(rep *store.DoctorReport) string {
	var b strings.Builder
	summaryFields := [][2]string{
		{"store_path", rep.StorePath},
		{"passed", fmt.Sprintf("%d", rep.Passed)},
		{"warnings", fmt.Sprintf("%d", rep.Warnings)},
		{"failures", fmt.Sprintf("%d", rep.Failures)},
		{"status", string(rep.OverallStatus)},
	}
	b.WriteString(render.FormatTOONObject("doctor_summary", summaryFields))

	headers := []string{"category", "name", "status", "message"}
	var rows [][]string
	for _, c := range rep.Checks {
		rows = append(rows, []string{c.Category, c.Name, string(c.Status), c.Message})
	}
	b.WriteString(render.FormatTOONTable("checks", headers, rows))
	return b.String()
}

func renderSkillsAuditTOON(audit *assess.SkillBloatAudit) string {
	var b strings.Builder
	summaryFields := [][2]string{
		{"audit_period_days", fmt.Sprintf("%d", audit.AuditPeriodDays)},
		{"total_registered", fmt.Sprintf("%d", audit.TotalRegistered)},
		{"active_count", fmt.Sprintf("%d", audit.ActiveCount)},
		{"dormant_count", fmt.Sprintf("%d", audit.DormantCount)},
		{"wasted_tokens_per_turn", fmt.Sprintf("%d", audit.WastedTokensTurn)},
	}
	b.WriteString(render.FormatTOONObject("skill_bloat_audit", summaryFields))

	if len(audit.DormantTools) > 0 {
		headers := []string{"server", "tool", "waste_tokens", "source"}
		var rows [][]string
		for _, dt := range audit.DormantTools {
			rows = append(rows, []string{dt.ServerName, dt.ToolName, fmt.Sprintf("%d", dt.EstimatedTokens), dt.SourceConfig})
		}
		b.WriteString(render.FormatTOONTable("dormant_tools", headers, rows))
	}
	return b.String()
}

func defaultSilaRegisteredTools() []assess.RegisteredTool {
	var tools []assess.RegisteredTool
	for _, t := range mcp.ToolDefinitions() {
		b, _ := json.Marshal(t)
		tools = append(tools, assess.RegisteredTool{
			ServerName:      "sila",
			ToolName:        t.Name,
			SourceConfig:    "builtin/sila",
			SchemaBytes:     len(b),
			EstimatedTokens: assess.EstimateTokens(len(b)),
		})
	}
	return tools
}

func appendCovenantDoctorCheck(rep *store.DoctorReport, dbPath string) {
	cwd, _ := os.Getwd()
	cov, err := assess.AuditCovenant(cwd, dbPath)
	if err != nil {
		rep.AddCheck("Covenant", "Digital Data Sovereignty", store.StatusWarn,
			fmt.Sprintf("Unable to evaluate data sovereignty covenant: %v", err), "", "")
		rep.Finalize()
		return
	}

	if !cov.Passed {
		rep.AddCheck("Covenant", "Digital Data Sovereignty", store.StatusFail,
			"Data sovereignty covenant violation detected.", "", "Run `sila doctor --covenant` to inspect non-compliant pillars.")
	} else {
		rep.AddCheck("Covenant", "Digital Data Sovereignty", store.StatusPass,
			fmt.Sprintf("Compliant across 4 pillars (local custody, epistemic non-destruction, portability, privacy boundaries); %d loaders active.", cov.LoadersCount), "", "")
	}
	rep.Finalize()
}

func runDoctorCovenant(ui render.UI, dbPath, repoDir string, jsonOut, toonOut bool) {
	cov, err := assess.AuditCovenant(repoDir, dbPath)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error()})
		} else {
			fmt.Fprintln(os.Stderr, ui.Red("sila doctor: "+err.Error()))
		}
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(cov)
		if !cov.Passed {
			os.Exit(1)
		}
		return
	}
	if toonOut {
		fmt.Fprint(os.Stdout, assess.RenderCovenantTOON(cov))
		if !cov.Passed {
			os.Exit(1)
		}
		return
	}

	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · doctor --covenant"))+" — Digital Data Sovereignty Integrity Audit")
	fmt.Fprintf(os.Stdout, "  Store: %s\n", dbPath)
	overallBadge := ui.Green(ui.Bold("[COMPLIANT]"))
	if !cov.Passed {
		overallBadge = ui.Red(ui.Bold("[NON-COMPLIANT]"))
	}
	fmt.Fprintf(os.Stdout, "  Status: %s\n\n", overallBadge)

	for i, p := range cov.Pillars {
		icon := ui.Green("✔")
		statusStr := ui.Green("PASS")
		if !p.Passed {
			icon = ui.Red("✖")
			statusStr = ui.Red("FAIL")
		} else if p.Status == "WARN" {
			icon = ui.Yellow("▲")
			statusStr = ui.Yellow("WARN")
		}

		fmt.Fprintf(os.Stdout, "  %s %s Pillar %d: %s\n", icon, statusStr, i+1, ui.Bold(p.Name))
		fmt.Fprintf(os.Stdout, "    %s %s\n", ui.Dim("Right:"), p.Right)
		fmt.Fprintf(os.Stdout, "    %s %s\n\n", ui.Dim("Evidence:"), p.Evidence)
	}

	fmt.Fprintln(os.Stdout, ui.Dim("For details and guarantees, see docs/covenant.md"))
	if !cov.Passed {
		os.Exit(1)
	}
}

func appendEdgeDoctorCheck(rep *store.DoctorReport, targetDir string) {
	if !assess.IsCloudflareWorkerProject(targetDir) {
		return
	}
	edgeRep, err := assess.AuditEdgeTests(targetDir, assess.DefaultEdgeAuditOptions())
	if err != nil {
		rep.AddCheck("Edge Tests", "Cloudflare Workers Test Sandbox", store.StatusWarn,
			fmt.Sprintf("Unable to evaluate edge test suites: %v", err), "", "")
		rep.Finalize()
		return
	}
	if !edgeRep.Passed {
		rep.AddCheck("Edge Tests", "Cloudflare Workers Test Sandbox", store.StatusFail,
			fmt.Sprintf("Cross-test state leakage detected across %d test file(s).", len(edgeRep.Issues)),
			fmt.Sprintf("%d mutating test(s) unisolated in D1/KV storage.", edgeRep.Summary.MutatingTests),
			"Run `sila doctor --edge` to inspect test storage sandbox recommendations.")
	} else if edgeRep.Summary.Warnings > 0 {
		rep.AddCheck("Edge Tests", "Cloudflare Workers Test Sandbox", store.StatusWarn,
			fmt.Sprintf("%d test warning(s) detected across %d file(s).", edgeRep.Summary.Warnings, edgeRep.Summary.TotalFiles),
			"", "Run `sila doctor --edge` to inspect test storage sandbox recommendations.")
	} else {
		rep.AddCheck("Edge Tests", "Cloudflare Workers Test Sandbox", store.StatusPass,
			fmt.Sprintf("Cloudflare Workers test suites verified: isolated D1/KV storage across %d test file(s).", edgeRep.Summary.TotalFiles),
			"", "")
	}
	rep.Finalize()
}

func appendCFCleanDoctorCheck(rep *store.DoctorReport, targetDir string, repair bool) {
	for _, c := range rep.Checks {
		if c.Name == "Cloudflare Credential Sanitizer (cfclean)" {
			return
		}
	}
	if !assess.IsCFWorkerProject(targetDir) && !assess.IsCloudflareWorkerProject(targetDir) {
		return
	}
	if _, err := exec.LookPath("cfclean"); err != nil {
		rep.AddCheck("Hygiene", "Cloudflare Credential Sanitizer (cfclean)", store.StatusWarn,
			"cfclean is not installed on PATH", "", "Run `sila plugins install cfclean` to install the credential sanitizer.")
		rep.Finalize()
		return
	}

	needsFilter := heal.NeedsCFGitFilter(targetDir)
	if needsFilter && repair {
		if repaired, err := heal.EnsureCFGitFilter(targetDir); err == nil && repaired {
			rep.RepairExecuted = true
			rep.RepairedItems = append(rep.RepairedItems, ".gitattributes (cfclean filter)")
			needsFilter = heal.NeedsCFGitFilter(targetDir)
		}
	}

	if needsFilter {
		cmdSmudge := exec.Command("git", "config", "filter.cfclean.smudge")
		cmdSmudge.Dir = targetDir
		outSmudge, _ := cmdSmudge.Output()
		smudgeVal := strings.TrimSpace(string(outSmudge))

		msg := "cfclean is installed on PATH but git filter is not configured in .gitattributes or git config"
		if smudgeVal == "cat" || smudgeVal == "" {
			msg = "cfclean is installed on PATH but smudge filter is not configured (or set to cat; hydration disabled)"
		}
		rep.AddCheck("Hygiene", "Cloudflare Credential Sanitizer (cfclean)", store.StatusWarn,
			msg,
			"", "Run `sila heal` or `cfclean setup --smudge` to configure git filter.")
	} else {
		rep.AddCheck("Hygiene", "Cloudflare Credential Sanitizer (cfclean)", store.StatusPass,
			"cfclean is installed and configured as git filter in .gitattributes and git config.",
			"", "")
	}
	rep.Finalize()
}

func runDoctorEdge(ui render.UI, targetDir string, jsonOut, toonOut bool) {
	rep, err := assess.AuditEdgeTests(targetDir, assess.DefaultEdgeAuditOptions())
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error()})
		} else {
			fmt.Fprintln(os.Stderr, ui.Red("sila doctor: "+err.Error()))
		}
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(rep)
		return
	}
	if toonOut {
		fmt.Fprint(os.Stdout, assess.RenderEdgeAuditTOON(rep))
		return
	}

	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · doctor --edge"))+" — Cloudflare Workers Test Sandbox Audit")
	fmt.Fprintf(os.Stdout, "  Target:  %s\n", rep.TargetDir)
	if !rep.Applicable {
		fmt.Fprintf(os.Stdout, "  Status:  %s\n\n", ui.Dim("[SKIPPED]"))
		fmt.Fprintf(os.Stdout, "  %s %s\n\n", ui.Yellow("ℹ"), rep.Reason)
		return
	}

	overallBadge := ui.Green(ui.Bold("[ISOLATED]"))
	if rep.Status == "FAIL" {
		overallBadge = ui.Red(ui.Bold("[LEAKAGE DETECTED]"))
	} else if rep.Status == "WARN" {
		overallBadge = ui.Yellow(ui.Bold("[WARNINGS]"))
	}
	fmt.Fprintf(os.Stdout, "  Status:  %s\n", overallBadge)
	fmt.Fprintf(os.Stdout, "  Harness: %s\n", ui.Dim(rep.Framework))
	fmt.Fprintf(os.Stdout, "  Summary: %d files, %d tests (%d mutating), %d violations, %d warnings\n\n",
		rep.Summary.TotalFiles, rep.Summary.TotalTests, rep.Summary.MutatingTests, rep.Summary.Violations, rep.Summary.Warnings)

	// File breakdown table
	if len(rep.Files) > 0 {
		headers := []string{"Test File", "Tests", "Mutating", "Isolation Hook", "Status"}
		var rows [][]string
		for _, f := range rep.Files {
			isoHook := ui.Green("beforeEach")
			if !f.HasBeforeEach {
				isoHook = ui.Red("none")
			}
			st := ui.Green("PASS")
			if !f.Isolated {
				st = ui.Red("FAIL")
			}
			rows = append(rows, []string{
				f.File,
				fmt.Sprintf("%d", f.TestCount),
				fmt.Sprintf("%d", f.MutatingCount),
				isoHook,
				st,
			})
		}
		fmt.Fprint(os.Stdout, ui.Table(headers, rows, []string{"l", "r", "r", "l", "l"}))
		fmt.Fprintln(os.Stdout)
	}

	// Issues detail
	if len(rep.Issues) > 0 {
		fmt.Fprintln(os.Stdout, ui.Bold("Detected Architectural Issues:"))
		for _, is := range rep.Issues {
			var icon, statusStr string
			switch is.Severity {
			case "FAIL":
				icon = ui.Red("✖")
				statusStr = ui.Red("FAIL")
			case "WARN":
				icon = ui.Yellow("▲")
				statusStr = ui.Yellow("WARN")
			default:
				icon = ui.Cyan("ℹ")
				statusStr = ui.Cyan("INFO")
			}

			loc := is.File
			if is.Line > 0 {
				loc = fmt.Sprintf("%s:%d", is.File, is.Line)
			}
			testPart := ""
			if is.TestName != "" {
				testPart = fmt.Sprintf(" in '%s'", is.TestName)
			}
			fmt.Fprintf(os.Stdout, "  %s %s [%s] %s%s\n", icon, statusStr, ui.Bold(is.RuleID), ui.Cyan(loc), testPart)
			fmt.Fprintf(os.Stdout, "    %s %s\n", ui.Dim("Message:"), is.Message)
			if is.Detail != "" {
				fmt.Fprintf(os.Stdout, "    %s %s\n", ui.Dim("Detail:"), is.Detail)
			}
			if is.Suggestion != "" {
				fmt.Fprintf(os.Stdout, "    %s %s\n", ui.Cyan("→ Suggested Fix:"), is.Suggestion)
			}
			fmt.Fprintln(os.Stdout)
		}
	} else {
		fmt.Fprintln(os.Stdout, ui.Green("✔ Zero cross-test state leakage detected across all edge test suites."))
		fmt.Fprintln(os.Stdout)
	}
}

func appendRulesBudgetDoctorCheck(rep *store.DoctorReport, cwd string, repair bool) {
	for _, c := range rep.Checks {
		if c.Name == "Rule Budget & Truncation Sentry" {
			return
		}
	}
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	res := silagate.AuditRuleBudget(cwd)

	if repair && !res.Clean {
		if repaired, err := silagate.RepairRuleBudget(cwd); err == nil && len(repaired) > 0 {
			rep.RepairExecuted = true
			rep.RepairedItems = append(rep.RepairedItems, repaired...)
			res = silagate.AuditRuleBudget(cwd)
		}
	}

	if !res.Clean {
		rep.AddCheck("Context", "Rule Budget & Truncation Sentry", store.StatusFail,
			fmt.Sprintf("Rule files exceed 24 KB budget or have broken symlinks: %s", strings.Join(res.Errors, "; ")),
			strings.Join(res.Errors, "\n"),
			"Run `sila agents sync --replace` or prune rule files under 24,000 bytes.")
	} else if len(res.Warnings) > 0 {
		rep.AddCheck("Context", "Rule Budget & Truncation Sentry", store.StatusWarn,
			fmt.Sprintf("Rule files within 24 KB budget with warnings: %s", strings.Join(res.Warnings, "; ")),
			strings.Join(res.Warnings, "\n"),
			"Prune rule files approaching the 24,000-byte Antigravity ceiling.")
	} else {
		rep.AddCheck("Context", "Rule Budget & Truncation Sentry", store.StatusPass,
			"AGENTS.md and GEMINI.md are within the 24 KB ceiling with verified symlinks.",
			"", "")
	}
	rep.Finalize()
}

func appendNegativeSignalsDoctorCheck(rep *store.DoctorReport, st *store.Store, project, cwd string) {
	for _, c := range rep.Checks {
		if c.Name == "Epistemic Liveness & Negative Drift" {
			return
		}
	}
	if st == nil {
		return
	}
	proj, err := st.ResolveScope(project, cwd)
	if err != nil {
		return
	}

	signals, err := st.DetectNegativeSignals(context.Background(), proj.ID, 7*24*time.Hour)
	if err != nil {
		rep.AddCheck("Epistemic", "Epistemic Liveness & Negative Drift", store.StatusWarn,
			fmt.Sprintf("Failed to query negative signals: %v", err), "", "")
		rep.Finalize()
		return
	}

	if len(signals) == 0 {
		rep.AddCheck("Epistemic", "Epistemic Liveness & Negative Drift", store.StatusPass,
			"All architectural invariants and semantic rules have recent verification assertions.", "", "")
	} else {
		var msgs []string
		for _, s := range signals {
			msgs = append(msgs, fmt.Sprintf("%s (%s, %.1fd ago): %s", s.RuleID, s.Status, s.DaysSinceAssertion, s.RuleText))
		}
		rep.AddCheck("Epistemic", "Epistemic Liveness & Negative Drift", store.StatusWarn,
			fmt.Sprintf("%d architectural invariant(s) lack recent verification assertions (negative drift detected)", len(signals)),
			strings.Join(msgs, "\n"),
			"Run verification or emit epistemic heartbeats for silenced invariants.")
	}
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
