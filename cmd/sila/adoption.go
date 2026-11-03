package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/adoption"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func runAdoption(ui render.UI, dbPath, project string, jsonOut, toonOut bool, args []string) {
	action := "report"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		action = strings.ToLower(args[0])
		args = args[1:]
	}
	since, until := time.Now().UTC().Add(-7*24*time.Hour), time.Now().UTC()
	harness := ""
	olderThan := 90 * 24 * time.Hour
	yes := false
	for _, arg := range args {
		switch {
		case strings.HasPrefix(arg, "--since="):
			if parsed, err := parseAdoptionAge(strings.TrimPrefix(arg, "--since=")); err == nil {
				since = time.Now().UTC().Add(-parsed)
			}
		case strings.HasPrefix(arg, "--until="):
			if parsed, err := time.Parse(time.RFC3339, strings.TrimPrefix(arg, "--until=")); err == nil {
				until = parsed
			}
		case strings.HasPrefix(arg, "--harness="):
			harness = strings.TrimPrefix(arg, "--harness=")
		case strings.HasPrefix(arg, "--older-than="):
			if parsed, err := parseAdoptionAge(strings.TrimPrefix(arg, "--older-than=")); err == nil {
				olderThan = parsed
			}
		case arg == "--yes" || arg == "-y":
			yes = true
		}
	}

	switch action {
	case "report":
		st, err := store.OpenRO(dbPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila adoption: %v\n", err)
			os.Exit(1)
		}
		defer st.Close()
		report, err := adoption.BuildFunnelReport(context.Background(), st, adoption.ReportOptions{
			Since: since, Until: until, ProjectID: project, Harness: harness,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila adoption: %v\n", err)
			os.Exit(1)
		}
		if jsonOut {
			emitJSON(report)
			return
		}
		if toonOut {
			fmt.Fprintf(os.Stdout, "adoption{schema,sessions,prime,recall,writes,handoff,tools}:\n  %s,%d,%d,%d,%d,%d,%d/%d\n",
				report.Schema, report.Sessions.Total, report.Sessions.Prime, report.Sessions.Recall,
				report.Sessions.Writes, report.Sessions.Handoff, report.Coverage.Used, report.Coverage.Exposed)
			return
		}
		fmt.Fprint(os.Stdout, render.RenderHeader(ui, render.Header{Command: "adoption", Scope: report.Project, Mode: "advisory"}))
		fmt.Fprintf(os.Stdout, "  Sessions: %d (prime %d, recall %d, writes %d, handoff %d, gate %d)\n",
			report.Sessions.Total, report.Sessions.Prime, report.Sessions.Recall, report.Sessions.Writes, report.Sessions.Handoff, report.Sessions.Gate)
		fmt.Fprintf(os.Stdout, "  Rates:    prime %s · write %s · handoff %s\n",
			adoption.FormatRate(report.Rates["prime"]), adoption.FormatRate(report.Rates["write"]), adoption.FormatRate(report.Rates["handoff"]))
		fmt.Fprintf(os.Stdout, "  Coverage: %d/%d continuity tools used", report.Coverage.Used, report.Coverage.Exposed)
		if len(report.Coverage.Missing) > 0 {
			fmt.Fprintf(os.Stdout, " (missing: %s)", strings.Join(report.Coverage.Missing, ", "))
		}
		fmt.Fprintln(os.Stdout)
		if report.UnattributedEvents > 0 {
			fmt.Fprintf(os.Stdout, "  Unattributed: %d events without a session id\n", report.UnattributedEvents)
		}
		fmt.Fprintf(os.Stdout, "  Verdict:  %s (advisory)\n", strings.ToUpper(adoption.Verdict(report)))
	case "prune":
		if !yes {
			fmt.Fprintln(os.Stderr, "sila adoption prune: --yes is required to delete events")
			os.Exit(2)
		}
		st, err := store.Open(dbPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila adoption prune: %v\n", err)
			os.Exit(1)
		}
		defer st.Close()
		cutoff := time.Now().UTC().Add(-olderThan)
		n, err := st.PruneToolEvents(cutoff)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila adoption prune: %v\n", err)
			os.Exit(1)
		}
		if jsonOut {
			emitJSON(map[string]any{"pruned": n, "older_than": cutoff.Format(time.RFC3339)})
			return
		}
		fmt.Fprintf(os.Stdout, "Pruned %d tool events older than %s\n", n, cutoff.Format(time.RFC3339))
	default:
		fmt.Fprintf(os.Stderr, "sila adoption: unknown action %q (expected report, prune)\n", action)
		os.Exit(2)
	}
}

func parseAdoptionAge(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if strings.HasSuffix(raw, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(raw, "d"))
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("invalid duration %q", raw)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(raw)
}

func runEvalAdoption(ui render.UI, dbPath, project, harness, caseFilter string, jsonOut, toonOut bool, args []string) {
	casesPath := "evals/adoption/cases.json"
	live := false
	dryRun := false
	outDir := ""
	effHarness := harness
	if effHarness == "auto" {
		effHarness = ""
	}

	scanTokens := make([]string, 0, len(os.Args)+len(args))
	if len(os.Args) > 1 {
		scanTokens = append(scanTokens, os.Args[1:]...)
	}
	scanTokens = append(scanTokens, args...)

	for i := 0; i < len(scanTokens); i++ {
		tok := scanTokens[i]
		switch {
		case tok == "--live" || tok == "-live":
			live = true
		case tok == "--offline" || tok == "-offline":
			live = false
		case tok == "--dry-run" || tok == "-dry-run" || tok == "--preview" || tok == "-n":
			dryRun = true
		case tok == "--json" || tok == "-json":
			jsonOut = true
		case tok == "--toon" || tok == "-toon":
			toonOut = true
		case strings.HasPrefix(tok, "--cases="):
			casesPath = strings.TrimPrefix(tok, "--cases=")
		case strings.HasPrefix(tok, "-cases="):
			casesPath = strings.TrimPrefix(tok, "-cases=")
		case (tok == "--cases" || tok == "-cases") && i+1 < len(scanTokens):
			casesPath = scanTokens[i+1]
			i++
		case strings.HasPrefix(tok, "--harness="):
			effHarness = strings.TrimPrefix(tok, "--harness=")
		case strings.HasPrefix(tok, "--out="):
			outDir = strings.TrimPrefix(tok, "--out=")
		}
	}

	cat, err := adoption.LoadAdoptionCatalog(casesPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila eval adoption: %v\n", err)
		os.Exit(1)
	}
	cases := cat.Cases
	if caseFilter != "" && !strings.HasSuffix(caseFilter, ".json") {
		var filtered []adoption.AdoptionCase
		for _, c := range cases {
			if c.ID == caseFilter || strings.Contains(c.ID, caseFilter) {
				filtered = append(filtered, c)
			}
		}
		if len(filtered) > 0 {
			cases = filtered
		}
	}

	rootDir := "."
	if absCases, err := filepath.Abs(casesPath); err == nil {
		candidateRoot := filepath.Dir(filepath.Dir(filepath.Dir(absCases)))
		if _, statErr := os.Stat(filepath.Join(candidateRoot, "AGENTS.md")); statErr == nil {
			rootDir = candidateRoot
		}
	}

	var rep adoption.EvalReport
	if live {
		var st *store.Store
		if !dryRun && dbPath != "" {
			st, _ = store.Open(dbPath)
			if st != nil {
				defer st.Close()
			}
		}
		rep, err = adoption.RunLiveEval(context.Background(), cases, adoption.LiveEvalOptions{
			Harness:        effHarness,
			OutDir:         outDir,
			DryRun:         dryRun,
			CatalogVersion: cat.CatalogVersion,
			Store:          st,
		})
	} else {
		rep, err = adoption.RunOfflineEvalWithOptions(cases, rootDir, adoption.OfflineEvalOptions{
			CatalogVersion: cat.CatalogVersion,
		})
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila eval adoption: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(rep)
		if rep.Verdict == "fail" {
			os.Exit(1)
		}
		return
	}

	if toonOut {
		fmt.Fprintf(os.Stdout, "adoption_eval{schema,mode,total,pass,fail,unknown,verdict}:\n  %s,%s,%d,%d,%d,%d,%s\n",
			rep.Schema, rep.Mode, rep.Summary.Total, rep.Summary.Pass, rep.Summary.Fail, rep.Summary.Unknown, rep.Verdict)
		if rep.Verdict == "fail" {
			os.Exit(1)
		}
		return
	}

	fmt.Fprint(os.Stdout, render.RenderHeader(ui, render.Header{Command: "eval adoption", Scope: rep.Mode, Mode: rep.Verdict}))
	if rep.DryRun {
		fmt.Fprintf(os.Stdout, "  Dry-run plan (%d cases, harness=%s):\n", len(rep.DryRunPlan), rep.Harness)
		for _, p := range rep.DryRunPlan {
			fmt.Fprintf(os.Stdout, "    - %-32s expected=%-26s profile=%-10s max_turns=%d\n",
				p.ID, p.ExpectedFirstTool, p.Profile, p.MaxTurns)
		}
		fmt.Fprintf(os.Stdout, "  Verdict: %s (dry-run)\n", strings.ToUpper(rep.Verdict))
		return
	}

	for _, c := range rep.Cases {
		fmt.Fprintf(os.Stdout, "  [%-7s] %-32s expected=%-26s observed=%s\n",
			strings.ToUpper(c.Verdict), c.ID, c.Expected, c.Observed)
		for _, d := range c.Diagnostics {
			fmt.Fprintf(os.Stdout, "            ! %s\n", d)
		}
	}
	fmt.Fprintf(os.Stdout, "  Summary: total=%d pass=%d fail=%d unknown=%d\n",
		rep.Summary.Total, rep.Summary.Pass, rep.Summary.Fail, rep.Summary.Unknown)
	if rep.Metrics != nil {
		fmt.Fprintf(os.Stdout, "  Metrics: recall=%s prime=%s write=%s handoff=%s forbidden=%s\n",
			adoption.FormatRate(rep.Metrics.TriggerRecallRate),
			adoption.FormatRate(rep.Metrics.PrimeRate),
			adoption.FormatRate(rep.Metrics.WriteComplianceRate),
			adoption.FormatRate(rep.Metrics.HandoffRate),
			adoption.FormatRate(rep.Metrics.ForbiddenToolRate))
	}
	fmt.Fprintf(os.Stdout, "  Verdict: %s\n", strings.ToUpper(rep.Verdict))
	if rep.Verdict == "fail" {
		os.Exit(1)
	}
}
