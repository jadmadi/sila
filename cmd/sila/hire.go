package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/assess"
	"github.com/jadmadi/sila/internal/audit"
	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/hire"
	"github.com/jadmadi/sila/internal/persona"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

// runReports is the top-level handler for `sila reports` and `sila report`.
func runReports(ui render.UI, dbPath string, jsonOut, toonOut bool, args []string) {
	st, _ := store.Open(dbPath)
	if st != nil {
		defer st.Close()
	}
	runHireReports(ui, st, jsonOut, toonOut, args)
}

func runHire(ui render.UI, dbPath string, jsonOut, toonOut bool, opts auditOptions, args []string) {
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila hire: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	mgr := persona.NewManager(st)
	allPersonas, err := mgr.List("", "")
	if err != nil || len(allPersonas) == 0 {
		allPersonas = persona.FoundationPersonas
	}
	allPersonas = persona.EnrichPersonas(allPersonas)

	// Subcommand routing: "assess"
	if len(args) > 0 && args[0] == "assess" {
		runHireAssess(ui, st, allPersonas, jsonOut, toonOut, opts, args[1:])
		return
	}

	// Subcommand routing: "audit-goal" or "audit"
	if len(args) > 0 && (args[0] == "audit-goal" || args[0] == "audit") {
		subArgs := args[1:]
		if opts.goal != "" {
			subArgs = append([]string{"--slug=" + opts.goal}, subArgs...)
		}
		runHireAuditGoal(ui, st, jsonOut, toonOut, subArgs)
		return
	}

	// Subcommand routing: "reports" or "report"
	if len(args) > 0 && (args[0] == "reports" || args[0] == "report") {
		subArgs := args[1:]
		if opts.goal != "" {
			subArgs = append([]string{"--goal=" + opts.goal}, subArgs...)
		}
		runHireReports(ui, st, jsonOut, toonOut, subArgs)
		return
	}

	// Subcommand routing: "panel"
	if len(args) > 0 && args[0] == "panel" {
		subArgs := args[1:]
		if opts.goal != "" {
			subArgs = append([]string{"--goal=" + opts.goal}, subArgs...)
		}
		runHirePanel(ui, st, jsonOut, toonOut, subArgs)
		return
	}

	// Subcommand routing: "unbind"
	if len(args) > 0 && args[0] == "unbind" {
		subArgs := args[1:]
		if opts.goal != "" && len(subArgs) == 0 {
			subArgs = []string{opts.goal}
		}
		runHireUnbind(ui, st, jsonOut, toonOut, subArgs)
		return
	}

	targetURL := ""
	appName := opts.app
	contextOpt := opts.context
	pagesOpt := opts.pages
	promptOnly := opts.promptOnly
	registerGoals := opts.registerGoals
	dryRun := opts.dryRun
	goalOpt := opts.goal
	clearPersonas := false

	var specTokens []string
	if opts.personas != "" {
		specTokens = append(specTokens, opts.personas)
	} else if opts.squad != "" {
		specTokens = append(specTokens, opts.squad)
	}

	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "--app=") {
			appName = strings.TrimPrefix(a, "--app=")
		} else if strings.HasPrefix(a, "--context=") {
			contextOpt = strings.TrimPrefix(a, "--context=")
		} else if strings.HasPrefix(a, "--pages=") {
			pagesOpt = strings.TrimPrefix(a, "--pages=")
		} else if strings.HasPrefix(a, "--goal=") {
			goalOpt = strings.TrimPrefix(a, "--goal=")
		} else if strings.HasPrefix(a, "--for-goal=") {
			goalOpt = strings.TrimPrefix(a, "--for-goal=")
		} else if (a == "--goal" || a == "--for-goal" || a == "-g") && i+1 < len(args) {
			goalOpt = args[i+1]
			i++
		} else if strings.HasPrefix(a, "-g=") {
			goalOpt = strings.TrimPrefix(a, "-g=")
		} else if a == "--clear-personas" || a == "--clear-squad" {
			clearPersonas = true
		} else if a == "--prompt-only" {
			promptOnly = true
		} else if a == "--register-goals" {
			registerGoals = true
		} else if a == "--dry-run" {
			dryRun = true
		} else if strings.HasPrefix(a, "http://") || strings.HasPrefix(a, "https://") || strings.HasPrefix(a, "localhost") {
			targetURL = a
		} else if !strings.HasPrefix(a, "-") {
			specTokens = append(specTokens, a)
		}
	}

	// Case 0a: Clear personas on goal
	if goalOpt != "" && clearPersonas {
		rootDir, err := goals.ResolveProjectDir(st, "")
		if err != nil {
			rootDir = "."
		}
		_, err = goals.UpdateGoal(goals.UpdateGoalParams{
			RootDir:       rootDir,
			Slug:          goalOpt,
			ClearPersonas: true,
		})
		if err != nil {
			if jsonOut {
				emitJSON(map[string]any{"error": err.Error(), "goal": goalOpt, "unbound": false})
				return
			}
			fmt.Fprintf(os.Stderr, "sila hire: %v\n", err)
			os.Exit(1)
		}
		if res, err := goals.ScanGoals(rootDir); err == nil {
			_ = goals.SyncReadme(rootDir, res)
		}
		if jsonOut {
			emitJSON(map[string]any{"goal": goalOpt, "unbound": true})
			return
		}
		fmt.Fprintf(os.Stdout, "%s Unbound review squad from goal %s\n", ui.Green("✔"), ui.Bold(goalOpt))
		return
	}

	// Case 0b: Bind persona squad to goal
	if goalOpt != "" && targetURL == "" {
		rootDir, err := goals.ResolveProjectDir(st, "")
		if err != nil {
			rootDir = "."
		}
		var squad []persona.Persona
		if len(specTokens) > 0 {
			hireSpec := strings.Join(specTokens, " ")
			squad, err = persona.ResolvePersonas(allPersonas, hireSpec)
			if err != nil {
				fmt.Fprintf(os.Stderr, "sila hire: %v\n", err)
				os.Exit(1)
			}
		} else {
			var targetGoals []*audit.GoalInfo
			if res, scanErr := goals.ScanGoals(rootDir); scanErr == nil && res != nil {
				for i := range res.Goals {
					if strings.EqualFold(res.Goals[i].Slug, goalOpt) {
						targetGoals = append(targetGoals, &res.Goals[i])
						break
					}
				}
			}
			squad = audit.RecommendSquadForGoals(allPersonas, targetGoals)
			if len(squad) == 0 {
				squad, _ = persona.ResolvePersonas(allPersonas, "group 1")
			}
		}
		var personaIDs []string
		for _, p := range squad {
			personaIDs = append(personaIDs, p.ID)
		}
		_, err = goals.UpdateGoal(goals.UpdateGoalParams{
			RootDir:  rootDir,
			Slug:     goalOpt,
			Personas: personaIDs,
		})
		if err != nil {
			if jsonOut {
				emitJSON(map[string]any{"error": err.Error(), "goal": goalOpt, "bound": false})
				os.Exit(1)
			}
			fmt.Fprintf(os.Stderr, "sila hire: %v\n", err)
			os.Exit(1)
		}
		if res, err := goals.ScanGoals(rootDir); err == nil {
			_ = goals.SyncReadme(rootDir, res)
		}
		if jsonOut {
			emitJSON(map[string]any{"goal": goalOpt, "personas": personaIDs, "bound": true})
			return
		}
		fmt.Fprintf(os.Stdout, "%s Bound review squad (%d personas) to goal %s: [%s]\n",
			ui.Green("✔"), len(squad), ui.Bold(goalOpt), ui.Cyan(strings.Join(personaIDs, ", ")))
		return
	}

	// If no specification is provided, print interactive hire instructions
	if len(specTokens) == 0 {
		if jsonOut {
			emitJSON(map[string]interface{}{
				"error":   "spec_required",
				"message": "Specify group ID or persona IDs to hire, or run 'assess' or 'reports'",
				"groups":  persona.CanonicalGroups,
			})
			return
		}

		fmt.Fprintf(os.Stdout, "%s sila · hire personas for audit\n\n", ui.Cyan("·"))
		fmt.Fprintln(os.Stdout, "Usage:")
		fmt.Fprintln(os.Stdout, "  sila hire group <id|slug> [url] [options]")
		fmt.Fprintln(os.Stdout, "  sila hire <id1,id2,id3...> [url] [options]")
		fmt.Fprintln(os.Stdout, "  sila hire assess [personas|group] [url] [options]")
		fmt.Fprintln(os.Stdout, "  sila hire reports")
		fmt.Fprintln(os.Stdout, "  sila hire report <id>")
		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout, "Examples:")
		fmt.Fprintln(os.Stdout, "  sila hire assess                        # Recommend balanced squad & execution schedule")
		fmt.Fprintln(os.Stdout, "  sila hire assess 1,4,6,3,8              # Assess squad balance & partition disjoint phases")
		fmt.Fprintln(os.Stdout, "  sila hire group 1                       # Hire Group 1 (AI Agent Readiness)")
		fmt.Fprintln(os.Stdout, "  sila hire 1,4,6,3,8 https://target.app  # Run autonomous audit immediately")
		fmt.Fprintln(os.Stdout, "  sila hire 1,4,6,3,8 https://target.app --register-goals # Audit & register goals")
		fmt.Fprintln(os.Stdout, "  sila hire reports                       # List all reports in .sila/reports/")
		fmt.Fprintln(os.Stdout, "  sila hire report rep_perf_123           # Inspect individual report findings")
		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout, "Run 'sila persona list' to browse all 8 groups and 31 persona IDs.")
		return
	}

	// Check if the last positional token is a URL without scheme
	if targetURL == "" && len(specTokens) > 1 {
		last := specTokens[len(specTokens)-1]
		if strings.Contains(last, ".") && !strings.Contains(last, ",") && !strings.HasPrefix(last, "group") {
			targetURL = last
			if !strings.HasPrefix(targetURL, "http://") && !strings.HasPrefix(targetURL, "https://") {
				targetURL = "https://" + targetURL
			}
			specTokens = specTokens[:len(specTokens)-1]
		}
	}

	hireSpec := strings.Join(specTokens, " ")
	squad, err := persona.ResolvePersonas(allPersonas, hireSpec)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila hire: %v\n", err)
		fmt.Fprintln(os.Stderr, "Run 'sila persona list' to inspect available groups and persona IDs.")
		os.Exit(1)
	}

	// Case 1: Target URL provided -> execute simulated or live audit
	if targetURL != "" {
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

		if promptOnly {
			promptPkg := audit.AssembleMasterGroupthinkPrompt(target, squad, gaps)
			fmt.Println(promptPkg)
			return
		}

		session, err := audit.ExecuteMock(context.Background(), target, squad)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila hire execution error: %v\n", err)
			os.Exit(1)
		}

		proposals := audit.SynthesizeConsensus(session)

		// Persist each persona's evaluation card into .sila/reports/
		savedReports := make([]*audit.Report, 0, len(session.Cards))
		personaMap := make(map[string]persona.Persona, len(squad))
		for _, p := range squad {
			personaMap[p.ID] = p
		}

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
							break
						}
					}
				}
			}

			if res, err := goals.ScanGoals(rootDir); err == nil {
				_ = goals.SyncReadme(rootDir, res)
			}
		}

		if jsonOut {
			emitJSON(map[string]interface{}{
				"session":   session,
				"proposals": proposals,
				"reports":   savedReports,
				"gaps":      gaps,
				"hired":     squad,
			})
			return
		}

		if toonOut {
			fmt.Fprintf(os.Stdout, "hired_audit{target,squad_size,proposals_count,reports_saved}:\n")
			fmt.Fprintf(os.Stdout, "  %q,%d,%d,%d\n\n", target.URL, len(squad), len(proposals), len(savedReports))
			fmt.Fprintf(os.Stdout, "proposals[%d]{slug,severity,tier,title}:\n", len(proposals))
			for _, prop := range proposals {
				fmt.Fprintf(os.Stdout, "  %s,%s,%s,%q\n", prop.Slug, prop.Severity, prop.PriorityTier, prop.Title)
			}
			return
		}

		fmt.Fprintf(os.Stdout, "%s sila · autonomous groupthink audit with hired squad\n\n", ui.Cyan("·"))
		fmt.Fprintf(os.Stdout, "  Target URL:   %s\n", ui.Bold(target.URL))
		if target.AppName != "" {
			fmt.Fprintf(os.Stdout, "  Application:  %s\n", target.AppName)
		}
		fmt.Fprintf(os.Stdout, "  Hired Squad:  %d personas [%s]\n", len(squad), formatSquadList(ui, squad))
		fmt.Fprintf(os.Stdout, "  Specification: %s\n\n", ui.Cyan(hireSpec))

		if len(gaps) > 0 {
			fmt.Fprintf(os.Stdout, "  %s Detected Domain Gaps (%d proposed):\n", ui.Yellow("💡"), len(gaps))
			for _, g := range gaps {
				fmt.Fprintf(os.Stdout, "    • %s %s: %s\n", ui.Bold(g.ID), ui.Dim("("+g.Domain+")"), g.Reason)
			}
			fmt.Fprintln(os.Stdout)
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

		// Report storage invariants feedback
		fmt.Fprintf(os.Stdout, "  %s Saved %d evaluation reports under %s\n",
			ui.Green("✔"), len(savedReports), ui.Dim(".sila/reports/"))
		if registerGoals && !dryRun {
			fmt.Fprintf(os.Stdout, "  %s Linked reports to registered Sila Goal packages under %s\n",
				ui.Green("✔"), ui.Dim("goals/"))
			fmt.Fprintf(os.Stdout, "  Run 'sila goals' to claim goals for implementation, or 'sila hire reports' to view reports.\n\n")
		} else {
			fmt.Fprintf(os.Stdout, "  %s Pass %s to persist proposals directly as formal Sila Goal packages.\n",
				ui.Dim("Tip:"), ui.Cyan("--register-goals"))
			fmt.Fprintf(os.Stdout, "  Inspect saved reports anytime via 'sila hire reports' or 'sila hire report <id>'.\n\n")
		}
		return
	}

	// Case 2: Target URL not provided -> generate prompt or print hired squad briefing
	if promptOnly {
		target := audit.AuditTarget{
			URL:          "https://target-app.internal",
			AppName:      appName,
			ExtraContext: contextOpt,
		}
		gaps := audit.DetectGaps(allPersonas, appName+" "+contextOpt)
		promptPkg := audit.AssembleMasterGroupthinkPrompt(target, squad, gaps)
		fmt.Println(promptPkg)
		return
	}

	if jsonOut {
		emitJSON(map[string]interface{}{
			"squad": squad,
			"count": len(squad),
			"spec":  hireSpec,
		})
		return
	}

	if toonOut {
		fmt.Fprintf(os.Stdout, "hired_squad[%d]{num_id,group_id,id,name,skills_count}:\n", len(squad))
		for _, p := range squad {
			fmt.Fprintf(os.Stdout, "  %d  %d  %s  %q  %d\n", p.NumID, p.GroupID, p.ID, p.Name, len(p.SkillsInvoked))
		}
		return
	}

	fmt.Fprintf(os.Stdout, "%s sila · hired audit squad (%d personas ready)\n\n", ui.Cyan("·"), len(squad))
	for _, p := range squad {
		skillsStr := ui.Dim("none (prompt only)")
		if len(p.SkillsInvoked) > 0 {
			skillsStr = ui.Cyan(strings.Join(p.SkillsInvoked, ", "))
		}

		fmt.Fprintf(os.Stdout, "  %s %s %s\n",
			ui.Bold(fmt.Sprintf("[%d]", p.NumID)),
			ui.Bold(p.Name),
			ui.Dim("("+p.Domain+" · "+p.ID+")"),
		)
		fmt.Fprintf(os.Stdout, "    Skills: %s\n", skillsStr)
		promptSnippet := p.SystemPrompt
		if len(promptSnippet) > 130 {
			promptSnippet = promptSnippet[:127] + "..."
		}
		fmt.Fprintf(os.Stdout, "    Stance: %s\n\n", ui.Dim(promptSnippet))
	}

	fmt.Fprintf(os.Stdout, "%s\n", ui.Bold("Ready to Audit:"))
	fmt.Fprintf(os.Stdout, "  sila hire %s https://target.app                 # Run autonomous audit against URL\n", hireSpec)
	fmt.Fprintf(os.Stdout, "  sila hire %s https://target.app --register-goals # Run audit and persist to goals/\n", hireSpec)
	fmt.Fprintf(os.Stdout, "  sila hire assess %s                             # Assess balance and disjoint task schedule\n\n", hireSpec)
}

// runHireAssess evaluates squad balance, identifies blindspots, and builds disjoint phase schedules.
func runHireAssess(ui render.UI, st *store.Store, allPersonas []persona.Persona, jsonOut, toonOut bool, opts auditOptions, args []string) {
	targetURL := ""
	contextOpt := opts.context
	goalsMode := opts.goals
	goalSlug := opts.goal

	var specTokens []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--goals" {
			goalsMode = true
		} else if strings.HasPrefix(a, "--goal=") {
			goalSlug = strings.TrimPrefix(a, "--goal=")
			goalsMode = true
		} else if a == "--goal" && i+1 < len(args) {
			goalSlug = args[i+1]
			goalsMode = true
			i++
		} else if strings.HasPrefix(a, "--context=") {
			contextOpt = strings.TrimPrefix(a, "--context=")
		} else if strings.HasPrefix(a, "http://") || strings.HasPrefix(a, "https://") || strings.HasPrefix(a, "localhost") {
			targetURL = a
		} else if !strings.HasPrefix(a, "-") {
			specTokens = append(specTokens, a)
		}
	}

	// Check if trailing token is a URL
	if targetURL == "" && len(specTokens) > 1 {
		last := specTokens[len(specTokens)-1]
		if strings.Contains(last, ".") && !strings.Contains(last, ",") && !strings.HasPrefix(last, "group") {
			targetURL = last
			if !strings.HasPrefix(targetURL, "http://") && !strings.HasPrefix(targetURL, "https://") {
				targetURL = "https://" + targetURL
			}
			specTokens = specTokens[:len(specTokens)-1]
		}
	}

	rootDir, err := goals.ResolveProjectDir(st, "")
	if err != nil {
		rootDir = "."
	}

	// Check if any positional token is a goal slug
	if !goalsMode && goalSlug == "" && len(specTokens) > 0 {
		for idx, tok := range specTokens {
			goalDir := filepath.Join(rootDir, "goals", tok)
			if stat, err := os.Stat(goalDir); err == nil && stat.IsDir() {
				if _, ok := persona.PersonaAliases[strings.ToLower(tok)]; !ok && !strings.HasPrefix(strings.ToLower(tok), "group") {
					goalSlug = tok
					goalsMode = true
					specTokens = append(specTokens[:idx], specTokens[idx+1:]...)
					break
				}
			}
		}
	}

	var assessment *audit.SquadAssessment

	if goalsMode || goalSlug != "" {
		res, err := goals.ScanGoals(rootDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila hire assess: scan goals: %v\n", err)
			os.Exit(1)
		}

		var targetGoals []*audit.GoalInfo
		if goalSlug != "" {
			for i := range res.Goals {
				if strings.EqualFold(res.Goals[i].Slug, goalSlug) {
					targetGoals = append(targetGoals, &res.Goals[i])
					break
				}
			}
			if len(targetGoals) == 0 {
				fmt.Fprintf(os.Stderr, "sila hire assess: goal %q not found under goals/\n", goalSlug)
				os.Exit(1)
			}
		} else {
			for i := range res.Goals {
				g := &res.Goals[i]
				if g.Status == "claimed" || (g.Status == "pending" && !g.Blocked) {
					targetGoals = append(targetGoals, g)
				}
			}
			if len(targetGoals) == 0 {
				for i := range res.Goals {
					g := &res.Goals[i]
					if g.Status == "pending" || g.Status == "draft" {
						targetGoals = append(targetGoals, g)
					}
				}
			}
			if len(targetGoals) == 0 {
				for i := range res.Goals {
					targetGoals = append(targetGoals, &res.Goals[i])
				}
			}
			if len(targetGoals) == 0 {
				fmt.Fprintf(os.Stderr, "sila hire assess: no goals found under goals/\n")
				os.Exit(1)
			}
		}

		if len(specTokens) > 0 {
			hireSpec := strings.Join(specTokens, " ")
			squad, err := persona.ResolvePersonas(allPersonas, hireSpec)
			if err != nil {
				fmt.Fprintf(os.Stderr, "sila hire assess: %v\n", err)
				os.Exit(1)
			}
			assessment = audit.AssessSquadForGoals(allPersonas, squad, targetGoals)
		} else {
			recommended := audit.RecommendSquadForGoals(allPersonas, targetGoals)
			assessment = audit.AssessSquadForGoals(allPersonas, recommended, targetGoals)
		}
	} else if len(specTokens) > 0 {
		hireSpec := strings.Join(specTokens, " ")
		squad, err := persona.ResolvePersonas(allPersonas, hireSpec)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila hire assess: %v\n", err)
			os.Exit(1)
		}
		assessment = audit.AssessSquad(allPersonas, squad, targetURL, contextOpt)
	} else {
		// General assessment mode
		assessment = audit.GeneralAssess(allPersonas, targetURL, contextOpt)
	}

	if jsonOut {
		emitJSON(assessment)
		return
	}

	if toonOut {
		fmt.Fprintf(os.Stdout, "squad_assessment{squad_size,balance_score,roadmap_alignment,target_url}:\n")
		fmt.Fprintf(os.Stdout, "  %d,%d,%d,%q\n\n", assessment.SquadSize, assessment.BalanceScore, assessment.RoadmapAlignment, assessment.TargetURL)
		if len(assessment.GoalsCoverage) > 0 {
			fmt.Fprintf(os.Stdout, "goals_coverage[%d]{slug,status,required_groups_count,covering_count}:\n", len(assessment.GoalsCoverage))
			for _, gc := range assessment.GoalsCoverage {
				fmt.Fprintf(os.Stdout, "  %s,%s,%d,%d\n", gc.GoalSlug, gc.Status, len(gc.RequiredGroups), len(gc.CoveringPersonas))
			}
			fmt.Fprintln(os.Stdout)
		}
		fmt.Fprintf(os.Stdout, "schedule[%d]{phase,name,personas_count}:\n", len(assessment.Schedule))
		for _, s := range assessment.Schedule {
			fmt.Fprintf(os.Stdout, "  %d,%q,%d\n", s.Phase, s.Name, len(s.Personas))
		}
		return
	}

	fmt.Fprintf(os.Stdout, "%s sila · squad balance & execution assessment\n\n", ui.Cyan("·"))
	if assessment.TargetURL != "" {
		fmt.Fprintf(os.Stdout, "  Target URL:     %s\n", ui.Bold(assessment.TargetURL))
	}
	fmt.Fprintf(os.Stdout, "  Squad Size:     %d personas\n", assessment.SquadSize)

	// Balance score badge
	scoreStr := fmt.Sprintf("%d/100", assessment.BalanceScore)
	if assessment.BalanceScore >= 75 {
		fmt.Fprintf(os.Stdout, "  Balance Score:  %s %s\n", ui.Green(scoreStr), ui.Dim("(Well Balanced & Diversified)"))
	} else if assessment.BalanceScore >= 45 {
		fmt.Fprintf(os.Stdout, "  Balance Score:  %s %s\n", ui.Yellow(scoreStr), ui.Dim("(Moderate Balance · Some Blindspots)"))
	} else {
		fmt.Fprintf(os.Stdout, "  Balance Score:  %s %s\n", ui.Red(scoreStr), ui.Dim("(Unbalanced · Missing Key Perspectives)"))
	}

	if len(assessment.GoalsCoverage) > 0 {
		alignStr := fmt.Sprintf("%d/100", assessment.RoadmapAlignment)
		var coveredCount, partialCount, uncoveredCount int
		for _, gc := range assessment.GoalsCoverage {
			switch gc.Status {
			case "covered":
				coveredCount++
			case "partially_covered":
				partialCount++
			default:
				uncoveredCount++
			}
		}
		alignNote := fmt.Sprintf("(%d covered, %d partial, %d uncovered)", coveredCount, partialCount, uncoveredCount)
		if assessment.RoadmapAlignment >= 75 {
			fmt.Fprintf(os.Stdout, "  Roadmap Squad Alignment: %s %s\n\n", ui.Green(alignStr), ui.Dim(alignNote))
		} else if assessment.RoadmapAlignment >= 45 {
			fmt.Fprintf(os.Stdout, "  Roadmap Squad Alignment: %s %s\n\n", ui.Yellow(alignStr), ui.Dim(alignNote))
		} else {
			fmt.Fprintf(os.Stdout, "  Roadmap Squad Alignment: %s %s\n\n", ui.Red(alignStr), ui.Dim(alignNote))
		}

		fmt.Fprintf(os.Stdout, "  %s Roadmap Goal Coverage Matrix (%d active goals):\n", ui.Bold("Goal Coverage:"), len(assessment.GoalsCoverage))
		for _, gc := range assessment.GoalsCoverage {
			statusBadge := ui.Green("✔ Covered")
			if gc.Status == "partially_covered" {
				statusBadge = ui.Yellow("⚠️ Partially Covered")
			} else if gc.Status == "uncovered" {
				statusBadge = ui.Red("✗ Uncovered")
			}

			fmt.Fprintf(os.Stdout, "    • %s [%s]\n", ui.Bold(gc.GoalSlug), statusBadge)
			if gc.Title != "" && gc.Title != gc.GoalSlug {
				fmt.Fprintf(os.Stdout, "      %s %s\n", ui.Dim("Title:"), gc.Title)
			}
			fmt.Fprintf(os.Stdout, "      %s %s\n", ui.Dim("Requires:"), strings.Join(gc.RequiredGroups, ", "))
			covStr := "(none)"
			if len(gc.CoveringPersonas) > 0 {
				covStr = strings.Join(gc.CoveringPersonas, ", ")
			}
			fmt.Fprintf(os.Stdout, "      %s %s\n", ui.Dim("Covering:"), covStr)
			if len(gc.MissingGroups) > 0 {
				fmt.Fprintf(os.Stdout, "      %s  %s\n", ui.Yellow("Missing:"), strings.Join(gc.MissingGroups, ", "))
			}
		}
		fmt.Fprintln(os.Stdout)
	}

	// Domain Coverage
	fmt.Fprintf(os.Stdout, "  %s Domain Coverage Across 8 Canonical Groups:\n", ui.Bold("Domain Balance:"))
	for _, cov := range assessment.DomainCoverages {
		switch cov.Status {
		case "covered":
			names := strings.Join(cov.Personas, ", ")
			if len(names) > 60 {
				names = names[:57] + "..."
			}
			fmt.Fprintf(os.Stdout, "    %s Group %d: %-38s %s\n",
				ui.Green("✔"), cov.GroupID, cov.Name, ui.Dim("("+names+")"))
		case "overrepresented":
			fmt.Fprintf(os.Stdout, "    %s Group %d: %-38s %s\n",
				ui.Yellow("⚠️"), cov.GroupID, cov.Name, ui.Yellow(fmt.Sprintf("(%d personas — Overrepresented)", cov.Count)))
		default:
			fmt.Fprintf(os.Stdout, "    %s Group %d: %-38s %s\n",
				ui.Dim("✗"), cov.GroupID, cov.Name, ui.Dim("(0 personas — Uncovered)"))
		}
	}
	fmt.Fprintln(os.Stdout)

	// Blindspots
	if len(assessment.Blindspots) > 0 {
		fmt.Fprintf(os.Stdout, "  %s Detected Domain Blindspots (%d items):\n", ui.Yellow("⚠️"), len(assessment.Blindspots))
		for _, b := range assessment.Blindspots {
			fmt.Fprintf(os.Stdout, "    • %s\n", b)
		}
		fmt.Fprintln(os.Stdout)
	}

	// Actionable Recommendations
	if len(assessment.Suggestions) > 0 {
		fmt.Fprintf(os.Stdout, "  %s Actionable Squad Recommendations:\n", ui.Green("💡"))
		for _, s := range assessment.Suggestions {
			prefix := ui.Green("[+]")
			if s.Action == "remove" {
				prefix = ui.Red("[-]")
			}
			fmt.Fprintf(os.Stdout, "    %s %s %s (%s)\n",
				prefix,
				ui.Bold(fmt.Sprintf("[%d]", s.PersonaNumID)),
				ui.Bold(s.PersonaName),
				ui.Cyan(s.PersonaID),
			)
			fmt.Fprintf(os.Stdout, "       %s\n", s.Reason)
		}
		fmt.Fprintln(os.Stdout)
	}

	// Disjoint Execution Schedule
	fmt.Fprintf(os.Stdout, "  %s Disjoint Execution Schedule (Partitioned Running Order):\n", ui.Bold("Execution Plan:"))
	for _, sch := range assessment.Schedule {
		var personaLabels []string
		for _, p := range sch.Personas {
			personaLabels = append(personaLabels, fmt.Sprintf("[%d] %s", p.NumID, p.Name))
		}

		fmt.Fprintf(os.Stdout, "    %s: %s %s\n",
			ui.Bold(fmt.Sprintf("Phase %d", sch.Phase)),
			ui.Cyan(sch.Name),
			ui.Dim(fmt.Sprintf("(%d personas)", len(sch.Personas))),
		)
		fmt.Fprintf(os.Stdout, "      %s %s\n", ui.Dim("Disjoint Scope:"), sch.TaskScope)
		fmt.Fprintf(os.Stdout, "      %s %s\n\n", ui.Dim("Assigned:"), strings.Join(personaLabels, ", "))
	}

	// Suggested action
	var idList []string
	for _, sch := range assessment.Schedule {
		for _, p := range sch.Personas {
			idList = append(idList, fmt.Sprintf("%d", p.NumID))
		}
	}
	execMesh := strings.Join(idList, ",")
	fmt.Fprintf(os.Stdout, "  %s\n", ui.Bold("Ready to Run:"))
	if goalSlug != "" {
		fmt.Fprintf(os.Stdout, "    sila hire %s --goal=%s\n", execMesh, goalSlug)
		fmt.Fprintf(os.Stdout, "    sila hire audit-goal %s\n\n", goalSlug)
	} else if len(assessment.GoalsCoverage) > 0 {
		fmt.Fprintf(os.Stdout, "    sila hire %s --goals\n\n", execMesh)
	} else if assessment.TargetURL != "" {
		fmt.Fprintf(os.Stdout, "    sila hire %s %s\n", execMesh, assessment.TargetURL)
		fmt.Fprintf(os.Stdout, "    sila hire %s %s --register-goals\n\n", execMesh, assessment.TargetURL)
	} else {
		fmt.Fprintf(os.Stdout, "    sila hire %s https://target.app\n", execMesh)
		fmt.Fprintf(os.Stdout, "    sila hire %s https://target.app --register-goals\n\n", execMesh)
	}
}

// runHireReports lists historical reports or renders details for an individual report.
func runHireReports(ui render.UI, st *store.Store, jsonOut, toonOut bool, args []string) {
	goalFilter := ""
	var cleanArgs []string
	for _, a := range args {
		if strings.HasPrefix(a, "--goal=") {
			goalFilter = strings.TrimPrefix(a, "--goal=")
		} else if strings.HasPrefix(a, "-g=") {
			goalFilter = strings.TrimPrefix(a, "-g=")
		} else {
			cleanArgs = append(cleanArgs, a)
		}
	}

	rootDir, _ := goals.ResolveProjectDir(st, "")
	if rootDir == "" {
		rootDir = "."
	}

	// If a specific report ID is provided -> detail view
	if len(cleanArgs) > 0 && !strings.HasPrefix(cleanArgs[0], "-") && cleanArgs[0] != "list" {
		repID := cleanArgs[0]
		rep, err := audit.GetReport(rootDir, repID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila hire report: %v\n", err)
			fmt.Fprintln(os.Stderr, "Run 'sila hire reports' to list available reports.")
			os.Exit(1)
		}

		if jsonOut {
			emitJSON(rep)
			return
		}

		if toonOut {
			fmt.Fprintf(os.Stdout, "report{id,persona_id,status,phase,associated_goal,created_at,updated_at}:\n")
			fmt.Fprintf(os.Stdout, "  %s,%s,%s,%d,%s,%s,%s\n\n",
				rep.ID, rep.PersonaID, rep.Status, rep.Phase, rep.AssociatedGoal,
				rep.CreatedAt.Format(time.RFC3339), rep.UpdatedAt.Format(time.RFC3339))
			return
		}

		statusBadge := ui.Yellow(strings.ToUpper(rep.Status))
		if rep.Status == "critical" {
			statusBadge = ui.Red(strings.ToUpper(rep.Status))
		} else if rep.Status == "pass" {
			statusBadge = ui.Green(strings.ToUpper(rep.Status))
		}

		fmt.Fprintf(os.Stdout, "%s sila · evaluation report [%s]\n\n", ui.Cyan("·"), ui.Bold(rep.ID))
		fmt.Fprintf(os.Stdout, "  Persona:          %s %s\n",
			ui.Bold(fmt.Sprintf("[%d] %s", rep.PersonaNumID, rep.PersonaName)),
			ui.Dim("("+rep.PersonaID+" · "+rep.Domain+")"))
		fmt.Fprintf(os.Stdout, "  Status:           %s\n", statusBadge)
		if rep.Phase > 0 {
			name, _, _ := audit.GetPhaseDetails(rep.Phase)
			fmt.Fprintf(os.Stdout, "  Disjoint Phase:   Phase %d (%s)\n", rep.Phase, name)
		}
		if rep.TargetURL != "" {
			fmt.Fprintf(os.Stdout, "  Target URL:       %s\n", rep.TargetURL)
		}
		assocBadge := ui.Dim("unassigned")
		if rep.AssociatedGoal != "" && rep.AssociatedGoal != "unassigned" {
			assocBadge = ui.Cyan("goals/" + strings.TrimPrefix(rep.AssociatedGoal, "goals/"))
		}
		fmt.Fprintf(os.Stdout, "  Associated Goal:  %s\n", assocBadge)
		fmt.Fprintf(os.Stdout, "  Last Written:     %s\n", rep.CreatedAt.Format("2006-01-02 15:04:05 UTC"))
		fmt.Fprintf(os.Stdout, "  Last Updated:     %s\n\n", rep.UpdatedAt.Format("2006-01-02 15:04:05 UTC"))

		if rep.Summary != "" {
			fmt.Fprintf(os.Stdout, "  %s\n    %s\n\n", ui.Bold("Executive Summary:"), rep.Summary)
		}

		if len(rep.Findings) > 0 {
			fmt.Fprintf(os.Stdout, "  %s Evidenced Findings (%d items):\n", ui.Bold("Findings:"), len(rep.Findings))
			for i, f := range rep.Findings {
				sevBadge := ui.Yellow(string(f.Severity))
				if f.Severity == audit.SeverityCritical || f.Severity == audit.SeverityHigh {
					sevBadge = ui.Red(string(f.Severity))
				}
				fmt.Fprintf(os.Stdout, "    %d. %s [%s · %s]\n", i+1, ui.Bold(f.Description), sevBadge, f.Category)
				if f.PageURL != "" {
					fmt.Fprintf(os.Stdout, "       Page:     %s\n", f.PageURL)
				}
				if f.Evidence != "" {
					fmt.Fprintf(os.Stdout, "       Evidence: %s\n", f.Evidence)
				}
				if f.Impact != "" {
					fmt.Fprintf(os.Stdout, "       Impact:   %s\n", f.Impact)
				}
			}
			fmt.Fprintln(os.Stdout)
		}

		if len(rep.Suggestions) > 0 {
			fmt.Fprintf(os.Stdout, "  %s Actionable Recommendations (%d items):\n", ui.Bold("Suggestions:"), len(rep.Suggestions))
			for i, s := range rep.Suggestions {
				fmt.Fprintf(os.Stdout, "    %d. %s [%s]\n", i+1, ui.Bold(s.Title), ui.Cyan(s.Priority))
				fmt.Fprintf(os.Stdout, "       Action: %s\n", s.Action)
				if s.ExpectedROI != "" {
					fmt.Fprintf(os.Stdout, "       ROI:    %s\n", s.ExpectedROI)
				}
			}
			fmt.Fprintln(os.Stdout)
		}

		if len(rep.SkillsUsed) > 0 {
			fmt.Fprintf(os.Stdout, "  %s %s\n\n", ui.Dim("Skills Invoked:"), strings.Join(rep.SkillsUsed, ", "))
		}
		return
	}

	// List view
	var reports []*audit.Report
	var err error
	if goalFilter != "" {
		reports, err = audit.FindReportsForGoal(rootDir, goalFilter)
	} else {
		reports, err = audit.ListReports(rootDir)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila hire reports: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(reports)
		return
	}

	if toonOut {
		fmt.Fprintf(os.Stdout, "reports[%d]{id,persona_id,status,phase,associated_goal,created_at,updated_at}:\n", len(reports))
		for _, r := range reports {
			fmt.Fprintf(os.Stdout, "  %s,%s,%s,%d,%s,%s,%s\n",
				r.ID, r.PersonaID, r.Status, r.Phase, r.AssociatedGoal,
				r.CreatedAt.Format(time.RFC3339), r.UpdatedAt.Format(time.RFC3339))
		}
		return
	}

	if goalFilter != "" {
		fmt.Fprintf(os.Stdout, "%s sila · evaluation reports for goal %s in .sila/reports/ (%d reports)\n\n", ui.Cyan("·"), ui.Bold(goalFilter), len(reports))
	} else {
		fmt.Fprintf(os.Stdout, "%s sila · evaluation reports in .sila/reports/ (%d reports)\n\n", ui.Cyan("·"), len(reports))
	}

	if len(reports) == 0 {
		if goalFilter != "" {
			fmt.Fprintf(os.Stdout, "  No evaluation reports found for goal %s in .sila/reports/.\n", ui.Bold(goalFilter))
		} else {
			fmt.Fprintln(os.Stdout, "  No evaluation reports found in .sila/reports/.")
			fmt.Fprintln(os.Stdout, "  Run 'sila hire 1,4,6,3,8 https://target.app' to execute an audit and generate reports.")
		}
		return
	}

	fmt.Fprintf(os.Stdout, "  %-24s %-28s %-8s %-8s %-24s %-16s %-16s\n",
		"REPORT ID", "PERSONA", "STATUS", "PHASE", "ASSOCIATED GOAL", "LAST WRITTEN", "LAST UPDATED")
	fmt.Fprintf(os.Stdout, "  %s\n", strings.Repeat("─", 116))

	for _, r := range reports {
		personaLabel := fmt.Sprintf("[%d] %s", r.PersonaNumID, r.PersonaID)
		if len(personaLabel) > 26 {
			personaLabel = personaLabel[:24] + ".."
		}

		statusLabel := strings.ToUpper(r.Status)
		if r.Status == "critical" {
			statusLabel = ui.Red(statusLabel)
		} else if r.Status == "warning" {
			statusLabel = ui.Yellow(statusLabel)
		} else {
			statusLabel = ui.Green(statusLabel)
		}

		phaseLabel := fmt.Sprintf("Phase %d", r.Phase)
		if r.Phase == 0 {
			phaseLabel = "—"
		}

		goalLabel := r.AssociatedGoal
		if goalLabel == "" || goalLabel == "unassigned" {
			goalLabel = ui.Dim("unassigned")
		} else {
			clean := strings.TrimPrefix(goalLabel, "goals/")
			if len(clean) > 22 {
				clean = clean[:20] + ".."
			}
			goalLabel = ui.Cyan(clean)
		}

		fmt.Fprintf(os.Stdout, "  %-24s %-28s %-8s %-8s %-24s %-16s %-16s\n",
			r.ID,
			personaLabel,
			statusLabel,
			phaseLabel,
			goalLabel,
			r.CreatedAt.Format("2006-01-02 15:04"),
			r.UpdatedAt.Format("2006-01-02 15:04"),
		)
	}

	fmt.Fprintln(os.Stdout)
	fmt.Fprintf(os.Stdout, "  %s Run 'sila hire report <id>' to inspect full findings, evidence, and suggestions.\n\n", ui.Dim("Tip:"))
}

// runHireUnbind removes assigned persona squads from a goal package.
func runHireUnbind(ui render.UI, st *store.Store, jsonOut, toonOut bool, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: sila hire unbind <slug>")
		os.Exit(2)
	}
	slug := args[0]
	rootDir, err := goals.ResolveProjectDir(st, "")
	if err != nil {
		rootDir = "."
	}
	_, err = goals.UpdateGoal(goals.UpdateGoalParams{
		RootDir:       rootDir,
		Slug:          slug,
		ClearPersonas: true,
	})
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error(), "slug": slug, "unbound": false})
			return
		}
		fmt.Fprintf(os.Stderr, "sila hire unbind error: %v\n", err)
		os.Exit(1)
	}
	if res, err := goals.ScanGoals(rootDir); err == nil {
		_ = goals.SyncReadme(rootDir, res)
	}
	if jsonOut {
		emitJSON(map[string]any{"slug": slug, "unbound": true})
		return
	}
	if toonOut {
		fmt.Fprintf(os.Stdout, "goal_unbind{slug,unbound}:\n  %s,true\n", slug)
		return
	}
	fmt.Fprintf(os.Stdout, "%s Unbound review squad from goal %s\n", ui.Green("✔"), ui.Bold(slug))
}

// runHireAuditGoal executes a decoupled epistemic mystery audit for a goal.
func runHireAuditGoal(ui render.UI, st *store.Store, jsonOut, toonOut bool, args []string) {
	slug := ""
	personaID := "mystery-auditor"
	hasExplicitPersona := false
	targetURL := ""
	targetBin := ""
	timeoutStr := ""
	skipCanary := false
	enablePanel := false
	project := ""

	for _, a := range args {
		if strings.HasPrefix(a, "--slug=") {
			slug = strings.TrimPrefix(a, "--slug=")
		} else if strings.HasPrefix(a, "--persona=") || strings.HasPrefix(a, "--hire=") {
			personaID = strings.TrimPrefix(strings.TrimPrefix(a, "--persona="), "--hire=")
			hasExplicitPersona = true
		} else if strings.HasPrefix(a, "--url=") {
			targetURL = strings.TrimPrefix(a, "--url=")
		} else if strings.HasPrefix(a, "--bin=") || strings.HasPrefix(a, "--binary=") {
			targetBin = strings.TrimPrefix(strings.TrimPrefix(a, "--bin="), "--binary=")
		} else if strings.HasPrefix(a, "--timeout=") {
			timeoutStr = strings.TrimPrefix(a, "--timeout=")
		} else if strings.HasPrefix(a, "--project=") || strings.HasPrefix(a, "-p=") {
			project = strings.TrimPrefix(strings.TrimPrefix(a, "--project="), "-p=")
		} else if a == "--skip-canary" {
			skipCanary = true
		} else if a == "--remote-panel" || a == "--panel" {
			enablePanel = true
		} else if !strings.HasPrefix(a, "-") && slug == "" {
			slug = a
		}
	}

	if slug == "" {
		fmt.Fprintln(os.Stderr, "Usage: sila hire audit-goal <slug> [--hire=<persona>] [--url=<url>] [--bin=<binary>] [--remote-panel] [--timeout=10m]")
		os.Exit(2)
	}

	rootDir, err := goals.ResolveProjectDir(st, project)
	if err != nil {
		rootDir = "."
	}

	if !hasExplicitPersona {
		goalPath := filepath.Join(rootDir, "goals", strings.TrimPrefix(slug, "goals/"), "goal.md")
		if data, err := os.ReadFile(goalPath); err == nil {
			assigned := goals.ParseGoalPersonas(string(data))
			if len(assigned) > 0 {
				personaID = strings.Join(assigned, ",")
			}
		}
	}

	timeout := 10 * time.Minute
	if timeoutStr != "" {
		if d, err := time.ParseDuration(timeoutStr); err == nil {
			timeout = d
		}
	}

	auditOpts := audit.GoalAuditOptions{
		GoalSlug:          slug,
		ProjectDir:        rootDir,
		PersonaID:         personaID,
		SessionID:         "cli-hire-audit",
		TargetURL:         targetURL,
		TargetBinary:      targetBin,
		Timeout:           timeout,
		Blindfold:         true,
		SkipCanary:        skipCanary,
		EnableRemotePanel: enablePanel,
		PanelConfig:       hire.LoadPanelConfig(rootDir),
	}

	if !jsonOut {
		fmt.Fprintf(os.Stdout, "%s sila · executing epistemic mystery audit on goal %s...\n", ui.Cyan("·"), ui.Bold(slug))
	}
	eval, err := audit.AuditGoal(context.Background(), st, auditOpts)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error(), "goal": slug})
			return
		}
		fmt.Fprintf(os.Stderr, "sila hire audit-goal error: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(eval)
		return
	}

	if eval.Verdict == "PASS" {
		fmt.Fprintf(os.Stdout, "%s Goal %s passed epistemic mystery audit!\n\n", ui.Green("✔"), ui.Bold(slug))
		fmt.Fprintf(os.Stdout, "  CCDSI Score:          %0.2f (floor: 0.40)\n", eval.CCDSI.CCDSIScore)
		fmt.Fprintf(os.Stdout, "  Probes Passed:        %d/%d\n", eval.CCDSI.PassedProbes, eval.CCDSI.TotalProbes)
		fmt.Fprintf(os.Stdout, "  Requirement Coverage: %0.2f (floor: 0.50)\n", eval.CCDSI.ReqCoverage)
		fmt.Fprintf(os.Stdout, "  Canary Detected:      %v\n", eval.CanaryPassed)
		fmt.Fprintf(os.Stdout, "  Blindfold Isolation:  %v\n", eval.BlindfoldVerified)
		if eval.RemotePanelResult != nil {
			fmt.Fprintf(os.Stdout, "  Advisory Jury Score:  %0.2f / 1.00\n", eval.RemotePanelResult.ConsensusScore)
			fmt.Fprintf(os.Stdout, "  Advisory Consensus:   %s\n", eval.RemotePanelResult.Summary)
		}
		if eval.Receipt != nil {
			fmt.Fprintf(os.Stdout, "  Receipt:              goals/%s/audit-receipt.json\n", slug)
		}
	} else {
		fmt.Fprintf(os.Stdout, "%s Goal %s was rejected by mystery audit and reopened for remediation.\n\n", ui.Red("✖"), ui.Bold(slug))
		fmt.Fprintf(os.Stdout, "  CCDSI Score:          %0.2f\n", eval.CCDSI.CCDSIScore)
		fmt.Fprintf(os.Stdout, "  Probes Passed:        %d/%d\n", eval.CCDSI.PassedProbes, eval.CCDSI.TotalProbes)
		fmt.Fprintf(os.Stdout, "  Failure Reason:       %s\n", eval.CCDSI.FailureReason)
		if eval.RemotePanelResult != nil {
			fmt.Fprintf(os.Stdout, "  Advisory Jury Score:  %0.2f / 1.00\n", eval.RemotePanelResult.ConsensusScore)
			fmt.Fprintf(os.Stdout, "  Advisory Consensus:   %s\n", eval.RemotePanelResult.Summary)
		}
		if eval.Dossier != nil {
			fmt.Fprintf(os.Stdout, "  Failed Probe:         %s\n", eval.Dossier.FailedProbeName)
			fmt.Fprintf(os.Stdout, "  Defect Dossier:       goals/%s/defect-dossier.json\n", slug)
			fmt.Fprintf(os.Stdout, "  Remediation Guide:    goals/%s/remediation.md\n", slug)
		}
	}
}

// runHirePanel executes a standalone multi-model epistemic advisory review.
func runHirePanel(ui render.UI, st *store.Store, jsonOut, toonOut bool, args []string) {
	slug := ""
	project := ""
	endpoint := ""
	timeoutStr := ""

	for _, a := range args {
		if strings.HasPrefix(a, "--slug=") || strings.HasPrefix(a, "--goal=") {
			slug = strings.TrimPrefix(strings.TrimPrefix(a, "--slug="), "--goal=")
		} else if strings.HasPrefix(a, "--endpoint=") {
			endpoint = strings.TrimPrefix(a, "--endpoint=")
		} else if strings.HasPrefix(a, "--timeout=") {
			timeoutStr = strings.TrimPrefix(a, "--timeout=")
		} else if strings.HasPrefix(a, "--project=") || strings.HasPrefix(a, "-p=") {
			project = strings.TrimPrefix(strings.TrimPrefix(a, "--project="), "-p=")
		} else if !strings.HasPrefix(a, "-") && slug == "" {
			slug = a
		}
	}

	rootDir, err := goals.ResolveProjectDir(st, project)
	if err != nil {
		rootDir = "."
	}

	cfg := hire.LoadPanelConfig(rootDir)
	if endpoint != "" {
		cfg.DefaultEndpoint = endpoint
		for r, rc := range cfg.Roles {
			rc.Endpoint = endpoint
			cfg.Roles[r] = rc
		}
	}
	if timeoutStr != "" {
		if d, err := time.ParseDuration(timeoutStr); err == nil && d.Seconds() > 0 {
			cfg.TimeoutSeconds = int(d.Seconds())
		}
	}

	contractSummary := "General codebase architectural and security audit"
	if slug != "" {
		goalMd := filepath.Join(rootDir, "goals", slug, "goal.md")
		if contract, err := assess.ExtractStrippedContract(goalMd); err == nil {
			contractSummary = fmt.Sprintf("Goal %s: %s\nRequirements:\n%s", contract.Slug, contract.Description, strings.Join(contract.Requirements, "\n"))
		}
	}

	diffBytes, _ := exec.Command("git", "-C", rootDir, "diff", "HEAD~1..HEAD").Output()
	if len(diffBytes) == 0 {
		diffBytes, _ = exec.Command("git", "-C", rootDir, "diff", "origin/main...HEAD").Output()
	}

	if !jsonOut {
		fmt.Fprintf(os.Stdout, "%s sila · querying 3-model epistemic advisory panel (Opus, Sol, Grok)...\n", ui.Cyan("·"))
	}

	res, err := hire.RunAdvisoryPanel(context.Background(), cfg, contractSummary, string(diffBytes))
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error()})
			return
		}
		fmt.Fprintf(os.Stderr, "sila hire panel error: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(res)
		return
	}

	fmt.Fprintf(os.Stdout, "\n%s Epistemic Advisory Panel Review\n", ui.Bold("⚖"))
	fmt.Fprintf(os.Stdout, "  Consensus Score: %0.2f / 1.00\n", res.ConsensusScore)
	fmt.Fprintf(os.Stdout, "  Verdict:         %s\n", res.Summary)
	fmt.Fprintf(os.Stdout, "  Offline Mode:    %v\n\n", res.OfflineFallback)

	if len(res.ConsensusFindings) > 0 {
		fmt.Fprintf(os.Stdout, "%s Consensus Defects (flagged by >=2 models):\n", ui.Red("✖"))
		for _, f := range res.ConsensusFindings {
			fmt.Fprintf(os.Stdout, "  • [%s] %s\n", f.Model, f.Description)
		}
		fmt.Fprintln(os.Stdout)
	}

	if len(res.LoneFindings) > 0 {
		fmt.Fprintf(os.Stdout, "%s Lone-Model Observations:\n", ui.Yellow("▲"))
		for _, f := range res.LoneFindings {
			fmt.Fprintf(os.Stdout, "  • [%s/%s] %s\n", f.Role, f.Model, f.Description)
		}
		fmt.Fprintln(os.Stdout)
	}
}
