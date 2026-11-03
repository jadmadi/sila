package main

import (
	"fmt"
	"os"

	"github.com/jadmadi/sila/internal/dream"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

// runRecap implements the deprecated `sila recap` alias. It warns on stderr
// and delegates to `sila standup`.
func runRecap(ui render.UI, dbPath, project, sinceFlag string, jsonOut bool) {
	fmt.Fprintln(os.Stderr, ui.Dim("sila: 'recap' is now 'standup'; the alias will be removed in a future release."))
	runHumanBriefing(ui, dbPath, project, sinceFlag, jsonOut, false, false)
}

// runHumanBriefing renders a human-centric executive briefing over the last
// window: decisions recorded, commits shipped, open action items, and items
// needing human approval. It contrasts with the agent-facing `sila prime`.
func runHumanBriefing(ui render.UI, dbPath, project, sinceFlag string, jsonOut, toonOut, globalFlag bool) {
	var st *store.Store
	if _, err := os.Stat(dbPath); err == nil {
		s, _, err := openReadStore(dbPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila: %v\n", err)
			os.Exit(1)
		}
		defer s.Close()
		st = s
	}

	cwd, _ := os.Getwd()
	if project == "" && !globalFlag {
		project = store.ProjectID(cwd)
	}

	report, err := dream.BuildRecap(st, project, sinceFlag, cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila standup: %v\n", err)
		os.Exit(1)
	}
	report.Interactive = ui.IsTerminal()

	if jsonOut {
		emitJSON(report)
		return
	}

	if toonOut {
		fmt.Fprint(os.Stdout, dream.RenderRecapTOON(report))
		return
	}

	recapMD := dream.RenderRecapMarkdown(report)
	if ui.IsTerminal() && ui.ColorEnabled() {
		_ = ui.RenderMarkdown(os.Stdout, recapMD)
	} else {
		fmt.Fprint(os.Stdout, recapMD)
	}

	if report.TotalMessages == 0 && len(report.Commits) == 0 {
		if ui.IsTerminal() && ui.ColorEnabled() {
			fmt.Fprintln(os.Stdout, ui.Dim("\nQuiet window. Try `sila standup last 7d` for a wider view."))
		} else {
			fmt.Fprintln(os.Stdout, "\n_Quiet window. Try `sila standup last 7d` for a wider view._")
		}
	}
}
