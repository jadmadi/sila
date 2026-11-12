package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/jadmadi/sila/internal/dream"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func runExport(ui render.UI, dbPath, project, target, outFile string, verbose bool, review bool, scope string, force bool, jsonOut bool, args []string) {
	if len(args) > 0 && args[0] == "continuity" {
		runExportContinuity(ui, dbPath, project, outFile, force, jsonOut)
		return
	}
	if len(args) > 0 && args[0] == "rules" {
		if review {
			cwd, _ := os.Getwd()
			runAgentsReview(ui, dbPath, cwd, project, target, scope, jsonOut)
			return
		}
		st, err := store.OpenRO(dbPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila: %v\n", err)
			os.Exit(1)
		}
		defer st.Close()
		cwd, _ := os.Getwd()
		if project == "" {
			project = store.ProjectID(cwd)
		}
		rulesText, err := dream.CompileRules(st, project, target)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila: %v\n", err)
			os.Exit(1)
		}
		if outFile != "" {
			if err := os.WriteFile(outFile, []byte(rulesText), 0644); err != nil {
				fmt.Fprintf(os.Stderr, "sila: %v\n", err)
				os.Exit(1)
			}
			fmt.Fprintln(os.Stdout, ui.Green("✔")+" Compiled "+target+" rules into "+outFile)
			return
		}
		fmt.Fprint(os.Stdout, rulesText)
		return
	}

	if _, err := os.Stat(dbPath); err != nil {
		if outFile == "" {
			fmt.Fprintln(os.Stdout, "{}")
		}
		return
	}
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	projects, err := st.Projects()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}
	var docs []exportDoc
	for _, p := range projects {
		if project != "" && !projSelMatch(p, project) {
			continue
		}
		msgs, err := st.MessagesForDistill(p.ID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila: %v\n", err)
			os.Exit(1)
		}
		facts, err := st.FactsByProject(p.ID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila: %v\n", err)
			os.Exit(1)
		}
		em := make([]exportMsg, 0, len(msgs))
		for _, m := range msgs {
			em = append(em, exportMsg{Role: m.Role, Content: m.Content, TS: m.TS, Tools: m.Tools})
		}
		ef := make([]jsonFact, 0, len(facts))
		for _, f := range facts {
			ef = append(ef, jsonFact{ProjectID: f.ProjectID, Claim: f.Claim, Confidence: f.Confidence, Verified: f.Verified, RepoFiles: f.RepoFiles, Tools: f.Tools, SessionRef: f.SessionRef, FirstSeen: f.FirstSeen})
		}
		docs = append(docs, exportDoc{ProjectID: p.ID, Path: p.Path, Messages: em, Facts: ef})
	}

	var w io.Writer = os.Stdout
	if outFile != "" {
		f, err := os.Create(outFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		w = f
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(docs); err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}
	if verbose && outFile != "" {
		fmt.Fprintf(os.Stderr, "exported %d projects to %s\n", len(docs), outFile)
	}
}

// exportMsg / exportDoc shape the JSON export.
type exportMsg struct {
	Role    string   `json:"role"`
	Content string   `json:"content"`
	TS      string   `json:"ts"`
	Tools   []string `json:"tools"`
}
type exportDoc struct {
	ProjectID string      `json:"project_id"`
	Path      string      `json:"path"`
	Messages  []exportMsg `json:"messages"`
	Facts     []jsonFact  `json:"facts"`
}

// runIntro prints an agent onboarding guide plus a ready-to-paste AGENTS.md
// block, so AI CLIs learn to consume sila and wire it into their work
// cycle. Plain markdown (no ANSI) — it is meant to be read/consumed by agents.
func runIntro(ui render.UI, agent, outFile, dbPath string, args []string) {
	doc := ""
	if len(args) > 0 && (args[0] == "goals" || args[0] == "goal") {
		doc = introGoalsMarkdown(agent)
	} else {
		doc = introMarkdown(agent, dbPath)
	}
	if outFile != "" {
		f, err := os.Create(outFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		fmt.Fprintln(f, doc)
		fmt.Fprintf(os.Stderr, "intro written to %s\n", outFile)
		return
	}
	fmt.Fprintln(os.Stdout, doc)
}

// jsonVacuum is the machine-readable vacuum shape.
