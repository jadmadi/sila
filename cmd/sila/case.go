package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/dream"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

// caseDuplicateThreshold is the token-similarity floor at which an existing
// architectural case is named as a near match.
const caseDuplicateThreshold = 0.6

// formatCase renders the canonical single-line case record.
func formatCase(intent, approach, invariant, tags string) string {
	return store.FormatCase(intent, approach, invariant, tags)
}

// normalizeCaseText lowercases and collapses whitespace for exact-duplicate checks.
func normalizeCaseText(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// caseDuplicates partitions existing cases into an exact duplicate and
// near matches for the candidate content.
func caseDuplicates(existing []store.Fact, content string) (*store.Fact, []store.Fact) {
	target := normalizeCaseText(content)
	var exact *store.Fact
	var similar []store.Fact
	for i := range existing {
		c := existing[i]
		if normalizeCaseText(c.Claim) == target {
			if exact == nil {
				exact = &existing[i]
			}
			continue
		}
		if dream.Similarity(c.Claim, content) >= caseDuplicateThreshold {
			similar = append(similar, c)
		}
	}
	sort.SliceStable(similar, func(i, j int) bool {
		return dream.Similarity(similar[i].Claim, content) > dream.Similarity(similar[j].Claim, content)
	})
	if len(similar) > 3 {
		similar = similar[:3]
	}
	return exact, similar
}

// caseHasTag reports whether a comma-separated tag list contains the wanted tag.
func caseHasTag(tagList, want string) bool {
	want = strings.ToLower(strings.TrimSpace(want))
	if want == "" {
		return true
	}
	for t := range strings.SplitSeq(tagList, ",") {
		if strings.ToLower(strings.TrimSpace(t)) == want {
			return true
		}
	}
	return false
}

// runCase records and reads structured architectural procedural cases.
func runCase(ui render.UI, dbPath, project, agent, files, approach, invariant, tags, since string, force, jsonOut, toonOut bool, args []string) {
	mode := "add"
	if len(args) > 0 {
		switch args[0] {
		case "add":
			mode = "add"
			args = args[1:]
		case "list", "ls":
			mode = "list"
			args = args[1:]
		case "show", "get":
			mode = "show"
			args = args[1:]
		default:
			if isSingleWordToken(args[0]) {
				unknownVerbUsage("case", args[0])
			}
			mode = "add"
		}
	}

	filteredArgs := args[:0]
	for _, a := range args {
		if strings.HasPrefix(a, "--charter=") {
			continue
		}
		if strings.HasPrefix(a, "-") {
			fmt.Fprintf(os.Stderr, "sila case: unknown flag %q\n", a)
			os.Exit(2)
		}
		filteredArgs = append(filteredArgs, a)
	}
	args = filteredArgs

	var st *store.Store
	var err error
	if mode == "list" || mode == "show" {
		st, _, err = openReadStore(dbPath)
	} else {
		st, err = store.Open(dbPath)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	cwd, _ := os.Getwd()
	if resolved, rErr := resolveCommandProjectScope(st, project, cwd); rErr == nil && resolved != "" {
		project = resolved
	} else if project == "" {
		project = store.ProjectID(cwd)
	}

	switch mode {
	case "list":
		cases, err := st.CaseFacts(project, 100)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila: %v\n", err)
			os.Exit(1)
		}
		sinceTime := parseSince(since)
		var filtered []store.Fact
		for _, c := range cases {
			if !sinceTime.IsZero() {
				ts, err := time.Parse(time.RFC3339, c.FirstSeen)
				if err == nil && ts.Before(sinceTime) {
					continue
				}
			}
			if tags != "" {
				_, _, _, ct := store.ParseCase(c.Claim)
				if !caseHasTag(ct, tags) {
					continue
				}
			}
			filtered = append(filtered, c)
		}
		if jsonOut {
			emitJSON(map[string]any{"project": project, "count": len(filtered), "cases": filtered})
			return
		}
		if toonOut {
			rows := make([][]string, 0, len(filtered))
			for _, c := range filtered {
				intent, approachVal, invariantVal, _ := store.ParseCase(c.Claim)
				rows = append(rows, []string{c.ID, c.ProjectID, intent, approachVal, invariantVal})
			}
			fmt.Fprint(os.Stdout, render.FormatTOONTable("cases", []string{"id", "project", "intent", "approach", "invariant"}, rows))
			return
		}
		if len(filtered) == 0 {
			fmt.Fprintf(os.Stdout, "%s No architectural cases recorded for %s.\n", ui.Yellow("▲"), ui.Bold(project))
			fmt.Fprintln(os.Stdout, "help: record one with 'sila case \"<intent>\" --approach=<text> --invariant=<text>'")
			return
		}
		headers := []string{"date", "intent", "approach", "grounded"}
		rows := make([][]string, 0, len(filtered))
		for _, c := range filtered {
			intent, approachVal, _, _ := store.ParseCase(c.Claim)
			day := c.FirstSeen
			if t, err := time.Parse(time.RFC3339, c.FirstSeen); err == nil {
				day = t.Format("2006-01-02")
			}
			grounded := ui.Dim("no")
			if c.FileRefsExist || c.Verified {
				grounded = ui.Green("yes")
			}
			rows = append(rows, []string{day, trunc(intent, 40), trunc(approachVal, 40), grounded})
		}
		fmt.Fprint(os.Stdout, render.RenderHeader(ui, render.Header{Command: "cases", Scope: cardProjectLabel(st, project), Title: fmt.Sprintf("%d recorded", len(filtered))}))
		fmt.Fprintln(os.Stdout, ui.Table(headers, rows, []string{"l", "l", "l", "l"}))
		return

	case "show":
		if len(args) == 0 {
			fmt.Fprintln(os.Stderr, "sila case show: specify a case id (see 'sila case list --json')")
			os.Exit(2)
		}
		id := args[0]
		cases, err := st.CaseFacts(project, 500)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila: %v\n", err)
			os.Exit(1)
		}
		var found *store.Fact
		for i := range cases {
			if cases[i].ID == id {
				found = &cases[i]
				break
			}
		}
		if found == nil {
			if jsonOut {
				emitJSON(map[string]any{"error": "case not found", "id": id})
				os.Exit(1)
			}
			fmt.Fprintf(os.Stderr, "sila case show: no case %s in %s\n", id, project)
			os.Exit(1)
		}
		if jsonOut {
			emitJSON(found)
			return
		}
		intent, approachVal, invariantVal, tagList := store.ParseCase(found.Claim)
		fmt.Fprint(os.Stdout, render.RenderHeader(ui, render.Header{Command: "case", Title: found.ID}))
		fmt.Fprintf(os.Stdout, "  Intent:    %s\n", ui.Bold(intent))
		if approachVal != "" {
			fmt.Fprintf(os.Stdout, "  Approach:  %s\n", approachVal)
		}
		if invariantVal != "" {
			fmt.Fprintf(os.Stdout, "  Invariant: %s\n", invariantVal)
		}
		if tagList != "" {
			fmt.Fprintf(os.Stdout, "  Tags:      %s\n", tagList)
		}
		fmt.Fprintf(os.Stdout, "  Project:   %s\n", found.ProjectID)
		fmt.Fprintf(os.Stdout, "  Recorded:  %s\n", found.FirstSeen)
		if len(found.RepoFiles) > 0 {
			grounded := ui.Red("missing")
			if found.FileRefsExist || found.Verified {
				grounded = ui.Green("grounded")
			}
			fmt.Fprintf(os.Stdout, "  Evidence:  %s (%s)\n", strings.Join(found.RepoFiles, ", "), grounded)
		}
		return
	}

	// add
	intent := strings.TrimSpace(strings.Join(args, " "))
	if intent == "" {
		fmt.Fprintln(os.Stderr, "sila case: specify the intent (e.g. sila case \"SQLite connection pooling\" --approach=\"single-writer WAL mode with busy timeout\" --invariant=\"only one write transaction at a time\")")
		os.Exit(2)
	}
	var fileList []string
	if files != "" {
		for f := range strings.SplitSeq(files, ",") {
			if f = strings.TrimSpace(f); f != "" {
				fileList = append(fileList, f)
			}
		}
	}
	content := formatCase(intent, approach, invariant, tags)

	existing, err := st.CaseFacts("", 500)
	if err == nil {
		exact, similar := caseDuplicates(existing, content)
		if exact != nil && !force {
			if jsonOut {
				emitJSON(map[string]any{"error": "duplicate case", "duplicate": exact})
				return
			}
			fmt.Fprintf(os.Stderr, "sila case: an identical case already exists (%s). Use --force to record it anyway.\n", exact.ID)
			fmt.Fprintf(os.Stdout, "help: sila case show %s\n", exact.ID)
			os.Exit(1)
		}
		if len(similar) > 0 && !jsonOut {
			fmt.Fprintln(os.Stdout, ui.Dim("Similar cases already recorded:"))
			for _, s := range similar {
				si, _, _, _ := store.ParseCase(s.Claim)
				fmt.Fprintf(os.Stdout, "  %s %s %s\n", ui.Dim(s.ID), trunc(si, 60), ui.Dim("("+s.ProjectID+")"))
			}
		}
	}

	var fact *store.Fact
	if charter, ok, cerr := charterForWrite(os.Args[1:]); cerr != nil {
		charterFail(cerr)
	} else if ok {
		for _, f := range fileList {
			if perr := charter.ValidatePath(f); perr != nil {
				charterFail(perr)
			}
		}
		project, agent = charter.ProjectID, charter.OriginRole()
		fact, err = st.InsertCaseWithOrigin(project, content, agent, cwd, fileList, charter.ParentSessionID, charter.OriginRole())
	} else if sessionRef := resolveWriteSession(project); sessionRef != "" {
		fact, err = st.InsertCaseWithSession(project, content, agent, cwd, fileList, sessionRef)
	} else {
		fact, err = st.InsertCase(project, content, agent, cwd, fileList)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}
	recordMemoryEvent(st, project, agent, "case", "ok", "", "cli", 0)

	if jsonOut {
		fact.RepoFiles = jsonCardFiles(cwd, fact.RepoFiles)
		emitJSON(fact)
		return
	}
	grounding := "not required"
	if len(fileList) > 0 {
		if fact.FileRefsExist || fact.Verified {
			grounding = "grounded"
		} else {
			grounding = "evidence missing on disk"
		}
	}
	fmt.Fprint(os.Stdout, render.RenderCard(ui, render.Card{
		Verb:   "Recorded",
		Object: "architectural case",
		ID:     fact.ID,
		Fields: []render.Field{
			{Label: "Intent", Value: intent},
			{Label: "Approach", Value: approach},
			{Label: "Invariant", Value: invariant},
			{Label: "Project", Value: cardProjectLabel(st, project)},
			{Label: "Grounding", Value: grounding},
		},
	}))
}
