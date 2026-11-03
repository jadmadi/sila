package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/jadmadi/sila/internal/dream"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func runVision(ui render.UI, dbPath, project, outFile, agent string, jsonOut, toonOut bool, args []string) {
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	cwd, _ := os.Getwd()
	if project == "" {
		project = store.ProjectID(cwd)
	}
	if agent == "" {
		agent = os.Getenv("SILA_AGENT")
		if agent == "" {
			agent = "agent"
		}
	}

	sub := ""
	if len(args) > 0 {
		sub = strings.ToLower(args[0])
	}

	switch sub {
	case "triage":
		propArgs := args[1:]
		if len(propArgs) == 0 {
			fmt.Fprintln(os.Stderr, "sila vision triage: specify proposal statement to evaluate (or --compare <cand1> <cand2>...)")
			fmt.Fprintln(os.Stdout, "help: sila vision triage \"<feature or refactor proposal>\"")
			fmt.Fprintln(os.Stdout, "help: sila vision triage --compare \"<candidate 1>\" \"<candidate 2>\" [...]")
			os.Exit(2)
		}

		// Check for --compare tournament mode
		if propArgs[0] == "--compare" {
			cands := propArgs[1:]
			if len(cands) < 2 {
				fmt.Fprintln(os.Stderr, "sila vision triage --compare requires at least 2 candidate proposals")
				os.Exit(2)
			}
			tRes, err := dream.CompareProposals(st, project, cwd, "", cands)
			if err != nil {
				fmt.Fprintf(os.Stderr, "sila: %v\n", err)
				os.Exit(1)
			}

			if jsonOut {
				emitJSON(tRes)
				return
			}

			if toonOut {
				var rows [][]string
				for _, c := range tRes.Candidates {
					rows = append(rows, []string{
						fmt.Sprintf("%d", c.Rank),
						fmt.Sprintf("%d", c.Index+1),
						c.Proposal,
						c.Triage.Verdict,
						fmt.Sprintf("%d", c.TournamentScore),
					})
				}
				fmt.Fprint(os.Stdout, render.FormatTOONTable("vision_triage_compare", []string{"rank", "candidate", "proposal", "verdict", "score"}, rows))
				return
			}

			fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold(fmt.Sprintf("sila · vision triage --compare · %s", project))))
			fmt.Fprintf(os.Stdout, "  Tournament Summary: %s\n\n", ui.Bold(tRes.Summary))
			for _, c := range tRes.Candidates {
				prefix := "  "
				if c.Index == tRes.WinningIndex {
					prefix = ui.Green("✔ ")
				}
				fmt.Fprintf(os.Stdout, "%s[Rank %d] Candidate %d (Score: %d/100, Verdict: %s):\n",
					prefix, c.Rank, c.Index+1, c.TournamentScore, c.Triage.Verdict)
				fmt.Fprintf(os.Stdout, "    Proposal: %s\n", c.Proposal)
				if len(c.Triage.DirectConflicts) > 0 {
					for _, conf := range c.Triage.DirectConflicts {
						fmt.Fprintf(os.Stdout, "    Conflict: %s\n", ui.Red(conf))
					}
				}
			}
			return
		}

		proposal := strings.Join(propArgs, " ")
		result, err := dream.TriageProposal(st, project, cwd, proposal)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila: %v\n", err)
			os.Exit(1)
		}

		if jsonOut {
			emitJSON(result)
			return
		}

		if toonOut {
			conflictsStr := strings.Join(result.DirectConflicts, "; ")
			rows := [][]string{
				{result.Proposal, result.Verdict, fmt.Sprintf("%d", result.FitScore), conflictsStr, result.RecommendedAction},
			}
			fmt.Fprint(os.Stdout, render.FormatTOONTable("vision_triage", []string{"proposal", "verdict", "fit_score", "conflicts", "action"}, rows))
			return
		}

		fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold(fmt.Sprintf("sila · vision triage · %s", project))))
		fmt.Fprintf(os.Stdout, "  Proposal: %s\n", ui.Bold(result.Proposal))

		switch result.Verdict {
		case "APPROVED":
			fmt.Fprintf(os.Stdout, "  Verdict:  %s (%s: %d/100)\n", ui.Green(ui.Bold("✔ APPROVED")), ui.Dim("Fit Score"), result.FitScore)
		case "NEEDS_ALIGNMENT":
			fmt.Fprintf(os.Stdout, "  Verdict:  %s (%s: %d/100)\n", ui.Yellow(ui.Bold("▲ NEEDS ALIGNMENT")), ui.Dim("Fit Score"), result.FitScore)
		default:
			fmt.Fprintf(os.Stdout, "  Verdict:  %s (%s: %d/100)\n", ui.Red(ui.Bold("✖ REJECTED")), ui.Dim("Fit Score"), result.FitScore)
		}

		fmt.Fprintf(os.Stdout, "  Analysis: %s\n", result.StrategicFit)
		if len(result.DirectConflicts) > 0 {
			fmt.Fprintln(os.Stdout, ui.Yellow("  Conflicts:"))
			for _, c := range result.DirectConflicts {
				fmt.Fprintf(os.Stdout, "    • %s\n", ui.Red(c))
			}
		}
		fmt.Fprintf(os.Stdout, "  Action:   %s\n", ui.Bold(result.RecommendedAction))

	case "add-nongoal":
		stmtArgs := args[1:]
		if len(stmtArgs) == 0 {
			fmt.Fprintln(os.Stderr, "sila vision add-nongoal: specify non-goal text (e.g. sila vision add-nongoal \"No cloud databases\")")
			fmt.Fprintln(os.Stdout, "help: sila vision add-nongoal \"<negative constraint statement>\"")
			os.Exit(2)
		}
		stmt := strings.Join(stmtArgs, " ")
		fact, err := dream.AddNonGoal(st, project, stmt, agent)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila: %v\n", err)
			os.Exit(1)
		}
		if jsonOut {
			emitJSON(fact)
			return
		}
		fmt.Fprintln(os.Stdout, ui.Green("✔")+" Recorded non-goal into "+ui.Bold(project)+" vision:")
		fmt.Fprintf(os.Stdout, "  ⛔ %s\n", fact.Claim)
		fmt.Fprintf(os.Stdout, "\n%s Run 'sila vision compile --out=VISION.md' to update strategic documentation.\n", ui.Dim("→"))

	case "add-milestone":
		stmtArgs := args[1:]
		if len(stmtArgs) == 0 {
			fmt.Fprintln(os.Stderr, "sila vision add-milestone: specify milestone text (e.g. sila vision add-milestone \"Living ADR engine\")")
			fmt.Fprintln(os.Stdout, "help: sila vision add-milestone \"<milestone description>\"")
			os.Exit(2)
		}
		stmt := strings.Join(stmtArgs, " ")
		fact, err := dream.AddMilestone(st, project, stmt, agent)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila: %v\n", err)
			os.Exit(1)
		}
		if jsonOut {
			emitJSON(fact)
			return
		}
		fmt.Fprintln(os.Stdout, ui.Green("✔")+" Recorded milestone into "+ui.Bold(project)+" vision:")
		fmt.Fprintf(os.Stdout, "  🎯 %s\n", fact.Claim)
		fmt.Fprintf(os.Stdout, "\n%s Run 'sila vision compile --out=VISION.md' to update strategic documentation.\n", ui.Dim("→"))

	case "list":
		items, err := dream.ListVisionItems(st, project)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila: %v\n", err)
			os.Exit(1)
		}
		if len(items) == 0 {
			if jsonOut {
				fmt.Fprintln(os.Stdout, "[]")
			} else if toonOut {
				fmt.Printf("vision_items[0]{id,claim,kind}:\n")
			} else {
				fmt.Fprintf(os.Stdout, "vision: 0 vision items found for project %s\n", ui.Bold(project))
				fmt.Fprintf(os.Stdout, "help: use 'sila vision add-nongoal \"<statement>\"' or 'add-milestone' to record strategic boundaries\n")
			}
			return
		}

		if jsonOut {
			emitJSON(items)
			return
		}
		if toonOut {
			rows := make([][]string, len(items))
			for i, it := range items {
				rows[i] = []string{it.ID, it.Claim, it.Kind}
			}
			fmt.Fprint(os.Stdout, render.FormatTOONTable("vision_items", []string{"id", "claim", "kind"}, rows))
			return
		}

		fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold(fmt.Sprintf("sila · vision items · %s (%d items)", project, len(items)))))
		for _, it := range items {
			icon := "•"
			if it.IsNonGoal() {
				icon = "⛔"
			} else if it.IsMilestone() {
				icon = "🎯"
			} else if it.IsInvariant() {
				icon = "🛡️"
			}
			fmt.Fprintf(os.Stdout, "  %s %s\n", icon, it.Claim)
		}
		fmt.Fprintf(os.Stdout, "\n%s Run 'sila vision compile --out=VISION.md' to compile project vision.\n", ui.Dim("→"))

	case "compile", "":
		content, err := dream.CompileVision(st, project, cwd)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila: %v\n", err)
			os.Exit(1)
		}

		if outFile != "" {
			if err := os.WriteFile(outFile, []byte(content), 0644); err != nil {
				fmt.Fprintf(os.Stderr, "sila: %v\n", err)
				os.Exit(1)
			}
			fmt.Fprintf(os.Stdout, "%s Compiled living vision written to %s (%d bytes)\n", ui.Green("✔"), ui.Bold(outFile), len(content))
			return
		}

		if toonOut {
			fmt.Print(render.FormatTOONObject("vision", [][2]string{
				{"project", project},
				{"content", content},
			}))
			return
		}

		fmt.Fprint(os.Stdout, content)
	default:
		fmt.Fprintf(os.Stderr, "sila vision: unknown subcommand %q\n", sub)
		fmt.Fprintln(os.Stdout, "Usage: sila vision [compile|triage|add-nongoal|add-milestone|list] [args...]")
		os.Exit(2)
	}
}
