package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/audit"
	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/persona"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

type auditOptions struct {
	promptOnly    bool
	registerGoals bool
	dryRun        bool
	squad         string
	personas      string
	pages         string
	app           string
	recommend     string
	preset        string
	register      bool
	context       string
	goals         bool
	goal          string
}

func runAudit(ui render.UI, dbPath string, jsonOut, toonOut bool, opts auditOptions, args []string) {
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila audit: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	mgr := persona.NewManager(st)

	sub := "web"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub = strings.ToLower(args[0])
		args = args[1:]
	}

	switch sub {
	case "web", "run", "site", "ui":
		targetURL := ""
		appName := opts.app
		squadOpt := opts.squad
		personasOpt := opts.personas
		contextOpt := opts.context
		pagesOpt := opts.pages
		promptOnly := opts.promptOnly
		registerGoals := opts.registerGoals
		dryRun := opts.dryRun

		for _, a := range args {
			if strings.HasPrefix(a, "--app=") {
				appName = strings.TrimPrefix(a, "--app=")
			} else if strings.HasPrefix(a, "--squad=") {
				squadOpt = strings.TrimPrefix(a, "--squad=")
			} else if strings.HasPrefix(a, "--personas=") {
				personasOpt = strings.TrimPrefix(a, "--personas=")
			} else if strings.HasPrefix(a, "--context=") {
				contextOpt = strings.TrimPrefix(a, "--context=")
			} else if strings.HasPrefix(a, "--pages=") {
				pagesOpt = strings.TrimPrefix(a, "--pages=")
			} else if a == "--prompt-only" {
				promptOnly = true
			} else if a == "--register-goals" {
				registerGoals = true
			} else if a == "--dry-run" {
				dryRun = true
			} else if !strings.HasPrefix(a, "-") && targetURL == "" {
				targetURL = a
			}
		}

		if sub == "ui" && squadOpt == "" && personasOpt == "" {
			squadOpt = "ui-ux"
		}

		if targetURL != "" {
			if fi, err := os.Stat(targetURL); err == nil && !fi.IsDir() {
				if fileData, err := os.ReadFile(targetURL); err == nil {
					if contextOpt != "" {
						contextOpt = contextOpt + "\n" + string(fileData)
					} else {
						contextOpt = string(fileData)
					}
				}
			}
		}

		if targetURL == "" {
			fmt.Fprintln(os.Stderr, "Usage: sila audit [web|ui] <url|file> [--app=<name>] [--squad=<preset>] [--personas=<ids>] [--context=\"...\"] [--pages=<p1,p2>] [--prompt-only] [--register-goals]")
			os.Exit(2)
		}

		// Retrieve all registered personas (local DB or fallback foundation)
		allPersonas, err := mgr.List("", "")
		if err != nil || len(allPersonas) == 0 {
			allPersonas = persona.FoundationPersonas
		}

		// Resolve squad
		var selectedSquad []persona.Persona
		if personasOpt != "" {
			if sq, err := persona.ResolvePersonas(allPersonas, personasOpt); err == nil {
				selectedSquad = sq
			}
		} else if squadOpt != "" {
			if preset, ok := audit.DefaultPresets[squadOpt]; ok {
				pMap := make(map[string]persona.Persona, len(allPersonas))
				for _, p := range allPersonas {
					pMap[p.ID] = p
				}
				for _, id := range preset.PersonaIDs {
					if p, ok := pMap[id]; ok {
						selectedSquad = append(selectedSquad, p)
					}
				}
			} else if sq, err := persona.ResolvePersonas(allPersonas, squadOpt); err == nil {
				selectedSquad = sq
			}
		}

		if len(selectedSquad) == 0 {
			selectedSquad = audit.RecommendSquad(allPersonas, targetURL, contextOpt)
		}

		var scopePages []string
		if pagesOpt != "" {
			for _, p := range strings.Split(pagesOpt, ",") {
				if s := strings.TrimSpace(p); s != "" {
					scopePages = append(scopePages, s)
				}
			}
		}

		target := audit.AuditTarget{
			URL:          targetURL,
			AppName:      appName,
			ScopePages:   scopePages,
			ExtraContext: contextOpt,
		}

		gaps := audit.DetectGaps(allPersonas, appName+" "+contextOpt)

		// Prompt-only generation mode
		if promptOnly {
			promptPkg := audit.AssembleMasterGroupthinkPrompt(target, selectedSquad, gaps)
			fmt.Println(promptPkg)
			return
		}

		// Execute simulated/mock multi-hat evaluation
		session, err := audit.ExecuteMock(context.Background(), target, selectedSquad)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila audit execution error: %v\n", err)
			os.Exit(1)
		}

		// Arbitrate consensus proposals
		proposals := audit.SynthesizeConsensus(session)

		// Persist structured reports for every persona under .sila/reports/
		personaMap := make(map[string]persona.Persona, len(allPersonas))
		for _, p := range allPersonas {
			personaMap[p.ID] = p
		}

		var savedReports []*audit.Report
		now := time.Now().UTC()
		for _, card := range session.Cards {
			p, ok := personaMap[card.PersonaID]
			numID := 0
			if ok {
				numID = p.NumID
			}
			repID := fmt.Sprintf("rep_%s_%d", card.PersonaID, now.Unix())
			rep := &audit.Report{
				ID:             repID,
				AssociatedGoal: "unassigned",
				CreatedAt:      now,
				UpdatedAt:      now,
				PersonaID:      card.PersonaID,
				PersonaNumID:   numID,
				PersonaName:    card.PersonaName,
				Domain:         card.Domain,
				TargetURL:      target.URL,
				Status:         card.Status,
				Phase:          audit.AssignPhase(p),
				Summary:        card.ExecutiveSummary,
				Findings:       card.Findings,
				Suggestions:    card.Suggestions,
				SkillsUsed:     card.SkillsUsed,
			}
			_ = audit.SaveReport(".", rep)
			savedReports = append(savedReports, rep)
		}

		if jsonOut {
			emitJSON(map[string]interface{}{
				"session":   session,
				"proposals": proposals,
				"gaps":      gaps,
				"reports":   savedReports,
			})
			return
		}

		if toonOut {
			fmt.Fprintf(os.Stdout, "audit_session{target,squad_size,proposals_count,gaps_count,reports_count}:\n")
			fmt.Fprintf(os.Stdout, "  %q,%d,%d,%d,%d\n\n", target.URL, len(selectedSquad), len(proposals), len(gaps), len(savedReports))
			fmt.Fprintf(os.Stdout, "proposals[%d]{slug,severity,tier,title}:\n", len(proposals))
			for _, prop := range proposals {
				fmt.Fprintf(os.Stdout, "  %s,%s,%s,%q\n", prop.Slug, prop.Severity, prop.PriorityTier, prop.Title)
			}
			return
		}

		// Terminal UI render
		fmt.Fprintf(os.Stdout, "%s sila · autonomous groupthink audit\n\n", ui.Cyan("·"))
		fmt.Fprintf(os.Stdout, "  Target URL:   %s\n", ui.Bold(target.URL))
		if target.AppName != "" {
			fmt.Fprintf(os.Stdout, "  Application:  %s\n", target.AppName)
		}
		fmt.Fprintf(os.Stdout, "  Squad Size:   %d personas\n", len(selectedSquad))
		fmt.Fprintf(os.Stdout, "  Roster:       %s\n", formatSquadList(ui, selectedSquad))
		fmt.Fprintf(os.Stdout, "  Reports:      %d persisted to .sila/reports/\n\n", len(savedReports))

		if len(gaps) > 0 {
			fmt.Fprintf(os.Stdout, "  %s Detected Domain Gaps (%d proposed):\n", ui.Yellow("💡"), len(gaps))
			for _, g := range gaps {
				fmt.Fprintf(os.Stdout, "    • %s %s: %s\n", ui.Bold(g.ID), ui.Dim("("+g.Domain+")"), g.Reason)
			}
			fmt.Fprintf(os.Stdout, "    %s\n\n", ui.Dim("Run 'sila audit gap --context=\"...\" --register' to add them to your persona vault."))
		}

		fmt.Fprintf(os.Stdout, "  %s Synthesized Consensus Proposals (%d items):\n\n", ui.Green("✔"), len(proposals))
		for i, p := range proposals {
			sevBadge := ui.Yellow(string(p.Severity))
			if p.Severity == audit.SeverityCritical || p.Severity == audit.SeverityHigh {
				sevBadge = ui.Red(string(p.Severity))
			}

			fmt.Fprintf(os.Stdout, "  %d. %s [%s · %s]\n", i+1, ui.Bold(p.Title), sevBadge, ui.Cyan(p.PriorityTier))
			fmt.Fprintf(os.Stdout, "     Slug:        %s\n", ui.Dim("goals/"+p.Slug+"/goal.md"))
			fmt.Fprintf(os.Stdout, "     Category:    %s\n", p.Category)
			fmt.Fprintf(os.Stdout, "     Consensus:   %s\n", strings.Join(p.PersonaAttributions, ", "))
			if len(p.Evidence) > 0 {
				fmt.Fprintf(os.Stdout, "     Evidence:    %s\n", p.Evidence[0])
			}
			fmt.Fprintf(os.Stdout, "     Summary:     %s\n\n", p.Summary)
		}

		// Register goals if requested
		if registerGoals && !dryRun {
			rootDir, err := goals.ResolveProjectDir(st, "")
			if err != nil {
				rootDir = "."
			}
			goalsBase := filepath.Join(rootDir, "goals")

			registeredCount := 0
			for _, p := range proposals {
				goalDir := filepath.Join(goalsBase, p.Slug)
				if err := os.MkdirAll(goalDir, 0755); err != nil {
					continue
				}

				_ = os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(p.GoalMD), 0644)
				_ = os.WriteFile(filepath.Join(goalDir, "facts.md"), []byte(p.FactsMD), 0644)
				_ = os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte(p.PlanMD), 0644)
				registeredCount++

				// Link matching reports to this goal slug
				for _, rep := range savedReports {
					for _, attr := range p.PersonaAttributions {
						if strings.EqualFold(rep.PersonaID, attr) || strings.EqualFold(rep.PersonaName, attr) {
							_ = audit.LinkReportToGoal(".", rep.ID, p.Slug)
						}
					}
				}
			}

			// Update goals/README.md
			if res, err := goals.ScanGoals(rootDir); err == nil {
				_ = goals.SyncReadme(rootDir, res)
			}

			fmt.Fprintf(os.Stdout, "%s Successfully registered %d Sila Goal packages under goals/\n", ui.Green("✔"), registeredCount)
			fmt.Fprintf(os.Stdout, "  Run 'sila goals' to inspect roadmap and claim goals for execution.\n")
		} else if !registerGoals {
			fmt.Fprintf(os.Stdout, "  %s Pass %s to persist proposals directly as formal Sila Goal packages.\n",
				ui.Dim("Tip:"), ui.Cyan("--register-goals"))
		}

	case "squad", "squads", "presets":
		recommendURL := opts.recommend
		contextOpt := opts.context
		presetOpt := opts.preset

		for _, a := range args {
			if strings.HasPrefix(a, "--recommend=") {
				recommendURL = strings.TrimPrefix(a, "--recommend=")
			} else if strings.HasPrefix(a, "--context=") {
				contextOpt = strings.TrimPrefix(a, "--context=")
			} else if strings.HasPrefix(a, "--preset=") {
				presetOpt = strings.TrimPrefix(a, "--preset=")
			}
		}

		allPersonas, _ := mgr.List("", "")
		if len(allPersonas) == 0 {
			allPersonas = persona.FoundationPersonas
		}

		if recommendURL != "" || contextOpt != "" {
			rec := audit.RecommendSquad(allPersonas, recommendURL, contextOpt)
			if jsonOut {
				emitJSON(rec)
				return
			}
			fmt.Fprintf(os.Stdout, "%s sila · recommended squad for %s\n\n", ui.Cyan("·"), ui.Bold(recommendURL))
			for _, p := range rec {
				fmt.Fprintf(os.Stdout, "  • %s %s: %s\n", ui.Bold(p.ID), ui.Dim("("+p.Domain+")"), p.Name)
			}
			return
		}

		if presetOpt != "" {
			if preset, ok := audit.DefaultPresets[presetOpt]; ok {
				if jsonOut {
					emitJSON(preset)
					return
				}
				fmt.Fprintf(os.Stdout, "%s Preset Squad: %s\n", ui.Cyan("·"), ui.Bold(preset.Name))
				fmt.Fprintf(os.Stdout, "  Description: %s\n", preset.Description)
				fmt.Fprintf(os.Stdout, "  Members:     %s\n", strings.Join(preset.PersonaIDs, ", "))
				return
			}
			fmt.Fprintf(os.Stderr, "unknown preset %q\n", presetOpt)
			os.Exit(1)
		}

		if jsonOut {
			emitJSON(audit.DefaultPresets)
			return
		}

		fmt.Fprintf(os.Stdout, "%s sila · squad presets (%d available)\n\n", ui.Cyan("·"), len(audit.DefaultPresets))
		for name, p := range audit.DefaultPresets {
			fmt.Fprintf(os.Stdout, "  %s\n", ui.Bold(name))
			fmt.Fprintf(os.Stdout, "    %s\n", p.Description)
			fmt.Fprintf(os.Stdout, "    Personas: %s\n\n", ui.Cyan(strings.Join(p.PersonaIDs, ", ")))
		}

	case "gap", "gaps":
		contextOpt := opts.context
		doRegister := opts.register
		for _, a := range args {
			if strings.HasPrefix(a, "--context=") {
				contextOpt = strings.TrimPrefix(a, "--context=")
			} else if a == "--register" {
				doRegister = true
			}
		}

		allPersonas, _ := mgr.List("", "")
		if len(allPersonas) == 0 {
			allPersonas = persona.FoundationPersonas
		}

		gaps := audit.DetectGaps(allPersonas, contextOpt)
		if jsonOut {
			emitJSON(gaps)
			return
		}

		if len(gaps) == 0 {
			fmt.Fprintf(os.Stdout, "%s No unrepresented persona gaps detected for given context.\n", ui.Green("✔"))
			return
		}

		fmt.Fprintf(os.Stdout, "%s sila · detected persona gaps (%d suggestions)\n\n", ui.Yellow("💡"), len(gaps))
		for _, g := range gaps {
			fmt.Fprintf(os.Stdout, "  • %s %s\n", ui.Bold(g.Name), ui.Dim("["+g.ID+"]"))
			fmt.Fprintf(os.Stdout, "    Domain: %s | Skills: %s\n", ui.Cyan(g.Domain), strings.Join(g.SkillsInvoked, ", "))
			fmt.Fprintf(os.Stdout, "    Reason: %s\n\n", g.Reason)

			if doRegister {
				newP := g.ToPersona()
				newP.CreatedAt = time.Now().UTC()
				newP.UpdatedAt = time.Now().UTC()
				_ = mgr.Upsert(newP)
				fmt.Fprintf(os.Stdout, "    %s Registered %s into local persona registry.\n\n", ui.Green("✔"), g.ID)
			}
		}

		if !doRegister {
			fmt.Fprintf(os.Stdout, "  %s Pass %s to persist suggested personas to your local/edge registry.\n",
				ui.Dim("Tip:"), ui.Cyan("--register"))
		}
	}
}

func formatSquadList(ui render.UI, squad []persona.Persona) string {
	names := make([]string, 0, len(squad))
	for _, p := range squad {
		names = append(names, p.ID)
	}
	return strings.Join(names, ", ")
}
