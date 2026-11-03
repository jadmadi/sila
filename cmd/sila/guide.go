package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/jadmadi/sila/internal/dream"
	"github.com/jadmadi/sila/internal/learn"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

// runGuide compiles and displays or exports the Developer Operational Guidebook.
// Usage: sila guide [domain] [--format=markdown|html|json|toon|summary] [--out=<file>]
//
//	sila guide export-rules [--target=AGENTS.md] [--domain=...] [--all]
func runGuide(ui render.UI, dbPath, project string, jsonOut, toonOut bool, args []string) {
	if len(args) > 0 && args[0] == "export-rules" {
		runGuideExportRules(ui, dbPath, project, args[1:])
		return
	}
	if len(args) > 0 && (args[0] == "sync" || args[0] == "harvest") {
		runGuideSync(ui, dbPath, project, jsonOut, toonOut, args[1:])
		return
	}

	fs := flag.NewFlagSet("guide", flag.ContinueOnError)
	formatFlag := fs.String("format", "terminal", "output format: terminal, markdown, html, json, toon, summary")
	outFlag := fs.String("out", "", "write guidebook output to destination file path")
	domainFlag := fs.String("domain", "", "filter guidebook by domain (golang, typescript, cloudflare, d1, devops, ci/cd, security, testing, a11y)")
	projectFlag := fs.String("project", "", "filter guidebook by specific project ID (defaults to cross-project)")
	fs.StringVar(projectFlag, "p", "", "short for --project")
	_ = fs.Parse(args)

	targetDomain := *domainFlag
	if targetDomain == "" && fs.NArg() > 0 {
		targetDomain = fs.Arg(0)
	}

	st, err := store.OpenRO(dbPath)
	if err != nil {
		// Fallback to harvest foundational learnings even if store cannot be opened
		st = nil
	} else {
		defer st.Close()
	}

	opts := learn.DefaultHarvesterOptions()
	if *projectFlag != "" {
		opts.ProjectID = *projectFlag
	}
	opts.Domain = targetDomain

	learnings, err := learn.Harvest(st, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila guide: error harvesting learnings: %v\n", err)
		os.Exit(1)
	}

	guidebook := learn.CompileGuidebook(learnings, targetDomain)

	format := strings.ToLower(*formatFlag)
	if jsonOut {
		format = "json"
	} else if toonOut {
		format = "toon"
	}

	var outputContent string
	switch format {
	case "markdown", "md":
		outputContent = guidebook.ToMarkdown()
	case "html":
		outputContent = guidebook.ToHTML()
	case "json":
		data, err := guidebook.ToJSON()
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila guide: json marshal: %v\n", err)
			os.Exit(1)
		}
		outputContent = string(data)
	case "toon":
		outputContent = guidebook.ToTOON()
	case "summary":
		outputContent = guidebook.ToSummary()
	default:
		// Terminal formatted display
		renderGuideTerminal(ui, guidebook)
		return
	}

	if *outFlag != "" {
		if err := os.WriteFile(*outFlag, []byte(outputContent), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "sila guide: write %s: %v\n", *outFlag, err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stdout, "✔ Guidebook exported to %s (%s, %d learnings)\n",
			ui.Cyan(*outFlag), format, guidebook.TotalLearnings)
		return
	}

	fmt.Fprint(os.Stdout, outputContent)
	if !strings.HasSuffix(outputContent, "\n") {
		fmt.Fprintln(os.Stdout)
	}
}

// runGuideExportRules exports operational guardrails into a markdown rule file non-destructively.
func runGuideExportRules(ui render.UI, dbPath, project string, args []string) {
	fs := flag.NewFlagSet("guide export-rules", flag.ContinueOnError)
	targetFlag := fs.String("target", "AGENTS.md", "target rule file path (e.g. AGENTS.md, CLAUDE.md, .cursorrules)")
	fs.StringVar(targetFlag, "t", "AGENTS.md", "short for --target")
	domainFlag := fs.String("domain", "", "comma-separated domains to export (e.g. golang,cloudflare,d1)")
	fs.StringVar(domainFlag, "d", "", "short for --domain")
	allFlag := fs.Bool("all", false, "export rules across all 10 domains")
	projectFlag := fs.String("project", project, "filter by project ID")
	fs.StringVar(projectFlag, "p", project, "short for --project")
	_ = fs.Parse(args)

	var domains []string
	if *allFlag || *domainFlag == "" || strings.EqualFold(*domainFlag, "all") {
		domains = []string{"all"}
	} else {
		for _, d := range strings.Split(*domainFlag, ",") {
			if td := strings.TrimSpace(d); td != "" {
				domains = append(domains, td)
			}
		}
	}

	st, err := store.OpenRO(dbPath)
	if err != nil {
		st = nil
	} else {
		defer st.Close()
	}

	opts := learn.DefaultHarvesterOptions()
	if *projectFlag != "" {
		opts.ProjectID = *projectFlag
	}

	learnings, err := learn.Harvest(st, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila guide export-rules: harvest: %v\n", err)
		os.Exit(1)
	}

	if err := learn.ExportRulesToFile(*targetFlag, learnings, domains); err != nil {
		fmt.Fprintf(os.Stderr, "sila guide export-rules: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stdout, "✔ Exported operational guardrail rules to %s\n", ui.Cyan(*targetFlag))
}

// renderGuideTerminal renders the operational guidebook with high-visibility terminal formatting.
func renderGuideTerminal(ui render.UI, gb *learn.Guidebook) {
	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · guide"))+" — Developer Operational Guidebook")
	fmt.Fprintf(os.Stdout, "  Total Learnings: %s across %s domain(s)\n\n",
		ui.Bold(fmt.Sprintf("%d", gb.TotalLearnings)),
		ui.Bold(fmt.Sprintf("%d", len(gb.Sections))))

	for _, s := range gb.Sections {
		fmt.Fprintln(os.Stdout, ui.Bold(ui.Orange("═══ "+s.DisplayName+" ("+fmt.Sprintf("%d", s.Count)+") ═══")))
		fmt.Fprintln(os.Stdout, "  "+ui.Dim(s.Description))
		fmt.Fprintln(os.Stdout)

		for idx, l := range s.Learnings {
			fmt.Fprintf(os.Stdout, "  %s %s\n",
				ui.Bold(fmt.Sprintf("%d.", idx+1)),
				ui.Bold(l.Title))

			meta := []string{}
			if l.TechStack != "" {
				meta = append(meta, "Stack: "+l.TechStack)
			}
			if l.ProjectID != "" {
				meta = append(meta, "Project: "+l.ProjectID)
			}
			if len(l.Tags) > 0 {
				meta = append(meta, "Tags: "+strings.Join(l.Tags, ", "))
			}
			if len(meta) > 0 {
				fmt.Fprintf(os.Stdout, "    %s\n", ui.Dim(strings.Join(meta, " · ")))
			}

			switch l.EffectiveKind() {
			case learn.KindCase:
				if l.Intent != "" {
					fmt.Fprintf(os.Stdout, "    %s %s\n", ui.Cyan("◆ Intent:"), l.Intent)
				}
				if l.Approach != "" {
					fmt.Fprintf(os.Stdout, "    %s %s\n", ui.Magenta("• Approach:"), l.Approach)
				}
				if l.Invariant != "" {
					fmt.Fprintf(os.Stdout, "    %s %s\n", ui.Green("✔ Invariant:"), l.Invariant)
				}
			case learn.KindMemo:
				if directive := l.DirectiveText(); directive != "" {
					fmt.Fprintf(os.Stdout, "    %s %s\n", ui.Cyan("→ Directive:"), ui.Bold(directive))
				}
			case learn.KindDecision:
				if directive := l.DirectiveText(); directive != "" {
					fmt.Fprintf(os.Stdout, "    %s %s\n", ui.Cyan("→ Decision:"), ui.Bold(directive))
				}
			default:
				if l.Symptom != "" {
					fmt.Fprintf(os.Stdout, "    %s %s\n", ui.Red("✖ Symptom:"), l.Symptom)
				}
				if l.RootCause != "" {
					fmt.Fprintf(os.Stdout, "    %s %s\n", ui.Magenta("• Cause:"), l.RootCause)
				}
				if l.PivotalFix != "" {
					fmt.Fprintf(os.Stdout, "    %s %s\n", ui.Green("✔ Fix:"), l.PivotalFix)
				}
				if l.GuardrailRule != "" {
					fmt.Fprintf(os.Stdout, "    %s %s\n", ui.Cyan("→ Rule:"), ui.Bold(l.GuardrailRule))
				}
			}
			fmt.Fprintln(os.Stdout)
		}
	}

	fmt.Fprintln(os.Stdout, ui.Dim("Tip: Export full HTML guidebook via `sila guide --format=html --out=guidebook.html`"))
}

// runLearn dispatches `sila learn [list|export|add]`.
func runLearn(ui render.UI, dbPath, project, domain, title, symptom, cause, fix, rule, stack, tags string, jsonOut, toonOut bool, args []string) {
	if len(args) == 0 {
		var guideArgs []string
		if domain != "" {
			guideArgs = []string{domain}
		}
		runGuide(ui, dbPath, project, jsonOut, toonOut, guideArgs)
		return
	}

	action := args[0]
	subArgs := args[1:]

	switch action {
	case "export", "compile":
		runGuide(ui, dbPath, project, jsonOut, toonOut, subArgs)

	case "sync", "harvest":
		runGuideSync(ui, dbPath, project, jsonOut, toonOut, subArgs)

	case "list":
		fs := flag.NewFlagSet("learn list", flag.ContinueOnError)
		domainFlag := fs.String("domain", domain, "filter by domain")
		projectFlag := fs.String("project", project, "filter by project ID (defaults to all projects)")
		fs.StringVar(projectFlag, "p", project, "short for --project")
		_ = fs.Parse(subArgs)

		targetDomain := *domainFlag
		if targetDomain == "" && fs.NArg() > 0 {
			targetDomain = fs.Arg(0)
		}

		st, err := store.OpenRO(dbPath)
		if err != nil {
			st = nil
		} else {
			defer st.Close()
		}

		opts := learn.DefaultHarvesterOptions()
		if *projectFlag != "" {
			opts.ProjectID = *projectFlag
		}
		opts.Domain = targetDomain

		learnings, err := learn.Harvest(st, opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila learn list: %v\n", err)
			os.Exit(1)
		}

		if jsonOut {
			emitJSON(map[string]any{"total": len(learnings), "learnings": learnings})
			return
		}
		if toonOut {
			gb := learn.CompileGuidebook(learnings, targetDomain)
			fmt.Fprint(os.Stdout, gb.ToTOON())
			return
		}

		fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · learn list"))+" — Distilled Operational Learnings")
		if len(learnings) == 0 {
			fmt.Fprintln(os.Stdout, "  No operational learnings found.")
			return
		}

		headers := []string{"Domain", "Title", "Guardrail Invariant Rule"}
		var rows [][]string
		for _, l := range learnings {
			ruleText := l.GuardrailRule
			if len(ruleText) > 60 {
				ruleText = ruleText[:57] + "..."
			}
			rows = append(rows, []string{l.Domain, l.Title, ruleText})
		}
		fmt.Fprint(os.Stdout, ui.Table(headers, rows, []string{"l", "l", "l"}))
		fmt.Fprintln(os.Stdout)

	case "add":
		// If subArgs contains flags (e.g. from programmatic test calls), parse them
		if len(subArgs) > 0 {
			fs := flag.NewFlagSet("learn add", flag.ContinueOnError)
			dFlag := fs.String("domain", domain, "domain category (golang, typescript, cloudflare, d1, devops, ci/cd, security, testing, a11y)")
			tFlag := fs.String("title", title, "learning title")
			sFlag := fs.String("symptom", symptom, "symptom or failure mode")
			cFlag := fs.String("cause", cause, "root cause explanation")
			fFlag := fs.String("fix", fix, "pivotal fix applied")
			rFlag := fs.String("rule", rule, "durable guardrail invariant rule")
			stkFlag := fs.String("stack", stack, "tech stack")
			tgFlag := fs.String("tags", tags, "comma-separated tags")
			if err := fs.Parse(subArgs); err == nil {
				domain = *dFlag
				title = *tFlag
				symptom = *sFlag
				cause = *cFlag
				fix = *fFlag
				rule = *rFlag
				stack = *stkFlag
				tags = *tgFlag
			}
		}

		if domain == "" {
			domain = "general"
		}
		if title == "" && symptom == "" {
			if len(subArgs) > 0 && !strings.HasPrefix(subArgs[0], "-") {
				title = subArgs[0]
			}
		}
		if title == "" && symptom == "" {
			fmt.Fprintln(os.Stderr, "sila learn add: requires --title or --symptom")
			os.Exit(1)
		}
		if title == "" {
			title = symptom
		}

		var tagsList []string
		if tags != "" {
			for _, t := range strings.Split(tags, ",") {
				if ct := strings.TrimSpace(t); ct != "" {
					tagsList = append(tagsList, ct)
				}
			}
		}

		op := learn.OperationalLearning{
			Kind:          learn.KindLesson,
			Domain:        domain,
			TechStack:     stack,
			Title:         title,
			Symptom:       symptom,
			RootCause:     cause,
			PivotalFix:    fix,
			GuardrailRule: rule,
			Tags:          tagsList,
		}

		st, err := store.Open(dbPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila learn add: open store: %v\n", err)
			os.Exit(1)
		}
		defer st.Close()

		cwd, _ := os.Getwd()
		formatted := learn.FormatOperationalLearning(op)
		fact, err := st.SaveOperationalLearning(project, formatted, "user", cwd, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila learn add: save learning: %v\n", err)
			os.Exit(1)
		}

		if jsonOut {
			emitJSON(map[string]any{"status": "saved", "fact": fact, "learning": op})
			return
		}

		hashStr := fact.FactHash
		if len(hashStr) > 12 {
			hashStr = hashStr[:12]
		}
		fmt.Fprintf(os.Stdout, "✔ Operational learning saved to knowledge store [%s] (hash: %s)\n",
			ui.Green(fact.ID), ui.Dim(hashStr))
		fmt.Fprintf(os.Stdout, "  Domain: %s · Rule: %s\n", ui.Bold(op.Domain), ui.Cyan(op.GuardrailRule))

	case "distil", "distill":
		fs := flag.NewFlagSet("learn distil", flag.ContinueOnError)
		sinceFlag := fs.String("since", "24h", "time window to inspect (e.g. 24h, 7d)")
		projectFlag := fs.String("project", project, "filter by project ID")
		fs.StringVar(projectFlag, "p", project, "short for --project")
		saveFlag := fs.Bool("save", false, "persist candidate learnings into the knowledge store")
		_ = fs.Parse(subArgs)

		st, err := store.Open(dbPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila learn distil: open store: %v\n", err)
			os.Exit(1)
		}
		defer st.Close()

		effProj := *projectFlag
		cwd, _ := os.Getwd()
		if effProj == "" {
			effProj = store.ProjectID(cwd)
		}

		sinceTime := parseSince(*sinceFlag)
		candidates := dream.ExtractProjectLessonCandidates(st, effProj, sinceTime)

		if *saveFlag {
			var saved []*store.Fact
			for _, cand := range candidates {
				d := learn.ClassifyDomain(cand.Symptom+" "+cand.Cause+" "+cand.Fix, cand.Tags)
				op := learn.OperationalLearning{
					Kind:          learn.KindLesson,
					Domain:        d,
					TechStack:     d,
					Title:         cand.Symptom,
					Symptom:       cand.Symptom,
					RootCause:     cand.Cause,
					PivotalFix:    cand.Fix,
					GuardrailRule: learn.HarmonizeRule(cand.Fix),
					Tags:          cand.Tags,
				}
				formatted := learn.FormatOperationalLearning(op)
				fact, err := st.SaveOperationalLearning(effProj, formatted, "user", cwd, cand.Files)
				if err == nil && fact != nil {
					saved = append(saved, fact)
				}
			}

			if jsonOut {
				emitJSON(map[string]any{"project": effProj, "saved": len(saved), "status": "saved"})
				return
			}
			if toonOut {
				var rows [][]string
				for _, f := range saved {
					rows = append(rows, []string{f.ID, f.ProjectID, f.Claim})
				}
				fmt.Fprint(os.Stdout, render.FormatTOONTable("distilled_learnings", []string{"id", "project", "claim"}, rows))
				return
			}

			fmt.Fprintf(os.Stdout, "%s Distilled and saved %d operational learning(s) for %s\n",
				ui.Green("✔"), len(saved), ui.Bold(effProj))
			return
		}

		// Preview mode
		if jsonOut {
			emitJSON(map[string]any{
				"project":    effProj,
				"count":      len(candidates),
				"candidates": candidates,
				"status":     "preview",
			})
			return
		}
		if toonOut {
			var rows [][]string
			for _, c := range candidates {
				d := learn.ClassifyDomain(c.Symptom+" "+c.Cause+" "+c.Fix, c.Tags)
				rows = append(rows, []string{d, c.Symptom, c.Fix})
			}
			fmt.Fprint(os.Stdout, render.FormatTOONTable("candidate_learnings", []string{"domain", "symptom", "fix"}, rows))
			return
		}

		if len(candidates) == 0 {
			fmt.Fprintf(os.Stdout, "%s No unrecorded operational learning candidates found in recent sessions for %s.\n",
				ui.Yellow("▲"), ui.Bold(effProj))
			fmt.Fprintln(os.Stdout, "help: record manually with 'sila learn add --domain=... --title=... --symptom=... --fix=...'")
			return
		}

		fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · learn distil"))+" "+ui.Dim(fmt.Sprintf("(%s · %d candidate(s))", effProj, len(candidates))))
		headers := []string{"Domain", "Candidate Symptom", "Pivotal Fix"}
		var rows [][]string
		for _, c := range candidates {
			d := learn.ClassifyDomain(c.Symptom+" "+c.Cause+" "+c.Fix, c.Tags)
			rows = append(rows, []string{d, trunc(c.Symptom, 45), trunc(c.Fix, 40)})
		}
		fmt.Fprintln(os.Stdout, ui.Table(headers, rows, []string{"l", "l", "l"}))
		fmt.Fprintf(os.Stdout, "\nhelp: run 'sila learn distil --save' to persist into store, or 'sila guide export-rules' to compile rules\n")

	default:
		// If first argument is a domain name, treat as `sila guide <domain>`
		runGuide(ui, dbPath, project, jsonOut, toonOut, args)
	}
}

// runGuideSync synchronizes operational learnings from the knowledge store into domain guidebooks and rules.
func runGuideSync(ui render.UI, dbPath, project string, jsonOut, toonOut bool, args []string) {
	fs := flag.NewFlagSet("guide sync", flag.ContinueOnError)
	targetFlag := fs.String("target", "", "target rule file to synchronize into (e.g. AGENTS.md)")
	domainFlag := fs.String("domain", "", "filter by domain")
	projectFlag := fs.String("project", project, "filter by project ID")
	fs.StringVar(projectFlag, "p", project, "short for --project")
	outFlag := fs.String("out", "", "write compiled guidebook to file")
	_ = fs.Parse(args)

	st, err := store.OpenRO(dbPath)
	if err != nil {
		st = nil
	} else {
		defer st.Close()
	}

	opts := learn.DefaultHarvesterOptions()
	if *projectFlag != "" {
		opts.ProjectID = *projectFlag
	}
	opts.Domain = *domainFlag

	learnings, err := learn.Harvest(st, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila guide sync: harvest: %v\n", err)
		os.Exit(1)
	}

	gb := learn.CompileGuidebook(learnings, *domainFlag)

	if *targetFlag != "" {
		var domains []string
		if *domainFlag != "" {
			domains = []string{*domainFlag}
		} else {
			domains = []string{"all"}
		}
		if err := learn.ExportRulesToFile(*targetFlag, learnings, domains); err != nil {
			fmt.Fprintf(os.Stderr, "sila guide sync: export rules to %s: %v\n", *targetFlag, err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stdout, "%s Synchronized %d operational learning rules into %s\n",
			ui.Green("✔"), gb.TotalLearnings, ui.Cyan(*targetFlag))
	}

	if *outFlag != "" {
		md := gb.ToMarkdown()
		if err := os.WriteFile(*outFlag, []byte(md), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "sila guide sync: write %s: %v\n", *outFlag, err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stdout, "%s Operational guidebook written to %s\n", ui.Green("✔"), ui.Cyan(*outFlag))
	}

	if jsonOut {
		emitJSON(map[string]any{
			"status":          "synced",
			"total_learnings": gb.TotalLearnings,
			"domains":         gb.DomainBreakdown,
		})
		return
	}
	if toonOut {
		fmt.Fprint(os.Stdout, gb.ToTOON())
		return
	}

	fmt.Fprintf(os.Stdout, "%s Auto-harvested and synchronized %d operational learning(s) across %d canonical domain(s)\n\n",
		ui.Green("✔"), gb.TotalLearnings, len(gb.Sections))

	headers := []string{"Domain", "DisplayName", "Learnings Count"}
	var rows [][]string
	for _, s := range gb.Sections {
		rows = append(rows, []string{s.Domain, s.DisplayName, fmt.Sprintf("%d", s.Count)})
	}
	fmt.Fprint(os.Stdout, ui.Table(headers, rows, []string{"l", "l", "r"}))
	fmt.Fprintln(os.Stdout)
}
