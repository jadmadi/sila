package main

import (
	"context"
	"fmt"
	"os"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func runForget(ui render.UI, dbPath, project string, includeTranscripts, yes, jsonOut bool) {
	projectID := project
	if projectID == "" || projectID == "all" {
		cwd, _ := os.Getwd()
		projectID = store.ProjectID(cwd)
	}
	if !yes && !gateIsTerminal(os.Stdin) {
		fmt.Fprintf(os.Stderr, "sila forget: refusing to archive without confirmation\n")
		fmt.Fprintf(os.Stderr, "run: sila forget --project=%s --yes\n", projectID)
		os.Exit(2)
	}
	if !yes {
		fmt.Fprintf(os.Stdout, "archive every memo, lesson, case, fact, and handoff for %s? [y/N] ", projectID)
		var answer string
		if _, err := fmt.Fscan(os.Stdin, &answer); err != nil || (answer != "y" && answer != "Y" && answer != "yes") {
			fmt.Fprintln(os.Stderr, "sila forget: cancelled")
			os.Exit(2)
		}
	}
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila forget: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()
	counts, err := st.ForgetProject(context.Background(), projectID, includeTranscripts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila forget: %v\n", err)
		os.Exit(1)
	}
	if jsonOut {
		emitJSON(map[string]any{
			"project": projectID, "forgotten": true,
			"memos": counts.Memos, "lessons": counts.Lessons, "cases": counts.Cases,
			"facts": counts.Facts, "handoffs": counts.Handoffs,
			"include_transcripts": includeTranscripts,
		})
		return
	}
	fmt.Fprintf(os.Stdout, "forgotten %s: %d memos, %d lessons, %d cases, %d facts, %d handoffs archived\n",
		projectID, counts.Memos, counts.Lessons, counts.Cases, counts.Facts, counts.Handoffs)
	if includeTranscripts {
		fmt.Fprintln(os.Stdout, "transcripts excluded from search and recall")
	}
	fmt.Fprintln(os.Stdout, "run sila resume to restore capture; archived rows stay archived")
}
