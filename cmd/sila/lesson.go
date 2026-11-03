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

// lessonDuplicateThreshold is the token-similarity floor at which an existing
// lesson is named as a near match. It never blocks a write.
const lessonDuplicateThreshold = 0.6

// formatLesson renders the canonical single-line lesson record.
func formatLesson(symptom, cause, fix, tags string) string {
	return store.FormatLesson(symptom, cause, fix, tags)
}

// normalizeLessonText lowercases and collapses whitespace for exact-duplicate checks.
func normalizeLessonText(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// lessonDuplicates partitions existing lessons into an exact duplicate and
// near matches for the candidate content. Pure, so the decision is testable
// without a store or a terminal.
func lessonDuplicates(existing []store.Fact, content string) (*store.Fact, []store.Fact) {
	target := normalizeLessonText(content)
	var exact *store.Fact
	var similar []store.Fact
	for i := range existing {
		c := existing[i]
		if normalizeLessonText(c.Claim) == target {
			if exact == nil {
				exact = &existing[i]
			}
			continue
		}
		if dream.Similarity(c.Claim, content) >= lessonDuplicateThreshold {
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

// runLesson records and reads structured solution lessons.
func runLesson(ui render.UI, dbPath, project, agent, files, cause, fix, tags, since string, force, save, jsonOut, toonOut bool, args []string) {
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
		case "distill":
			mode = "distill"
			args = args[1:]
		default:
			if isSingleWordToken(args[0]) {
				unknownVerbUsage("lesson", args[0])
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
			fmt.Fprintf(os.Stderr, "sila lesson: unknown flag %q\n", a)
			os.Exit(2)
		}
		filteredArgs = append(filteredArgs, a)
	}
	args = filteredArgs

	var st *store.Store
	var err error
	if mode == "list" || mode == "show" || (mode == "distill" && !save) {
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
	case "distill":
		runLessonDistill(ui, st, project, agent, cwd, since, save, jsonOut, toonOut)
		return
	case "list":
		lessons, err := st.LessonFacts(project, 100)

		if err != nil {
			fmt.Fprintf(os.Stderr, "sila: %v\n", err)
			os.Exit(1)
		}
		sinceTime := parseSince(since)
		var filtered []store.Fact
		for _, l := range lessons {
			if !sinceTime.IsZero() {
				ts, err := time.Parse(time.RFC3339, l.FirstSeen)
				if err == nil && ts.Before(sinceTime) {
					continue
				}
			}
			if tags != "" {
				_, _, _, lt := store.ParseLesson(l.Claim)
				if !lessonHasTag(lt, tags) {
					continue
				}
			}
			filtered = append(filtered, l)
		}
		if jsonOut {
			emitJSON(map[string]any{"project": project, "count": len(filtered), "lessons": filtered})
			return
		}
		if toonOut {
			rows := make([][]string, 0, len(filtered))
			for _, l := range filtered {
				symptom, _, fix, _ := store.ParseLesson(l.Claim)
				rows = append(rows, []string{l.ID, l.ProjectID, symptom, fix})
			}
			fmt.Fprint(os.Stdout, render.FormatTOONTable("lessons", []string{"id", "project", "symptom", "fix"}, rows))
			return
		}
		if len(filtered) == 0 {
			fmt.Fprintf(os.Stdout, "%s No lessons recorded for %s.\n", ui.Yellow("▲"), ui.Bold(project))
			fmt.Fprintln(os.Stdout, "help: record one with 'sila lesson \"<symptom>\" --cause=<text> --fix=<text>'")
			return
		}
		headers := []string{"date", "symptom", "fix", "grounded"}
		rows := make([][]string, 0, len(filtered))
		for _, l := range filtered {
			symptom, _, fix, _ := store.ParseLesson(l.Claim)
			day := l.FirstSeen
			if t, err := time.Parse(time.RFC3339, l.FirstSeen); err == nil {
				day = t.Format("2006-01-02")
			}
			grounded := ui.Dim("no")
			if l.FileRefsExist || l.Verified {
				grounded = ui.Green("yes")
			}
			rows = append(rows, []string{day, trunc(symptom, 48), trunc(fix, 42), grounded})
		}
		fmt.Fprint(os.Stdout, render.RenderHeader(ui, render.Header{Command: "lessons", Scope: cardProjectLabel(st, project), Title: fmt.Sprintf("%d recorded", len(filtered))}))
		fmt.Fprintln(os.Stdout, ui.Table(headers, rows, []string{"l", "l", "l", "l"}))
		return

	case "show":
		if len(args) == 0 {
			fmt.Fprintln(os.Stderr, "sila lesson show: specify a lesson id (see 'sila lesson list --json')")
			os.Exit(2)
		}
		id := args[0]
		lessons, err := st.LessonFacts(project, 500)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila: %v\n", err)
			os.Exit(1)
		}
		var found *store.Fact
		for i := range lessons {
			if lessons[i].ID == id {
				found = &lessons[i]
				break
			}
		}
		if found == nil {
			if jsonOut {
				emitJSON(map[string]any{"error": "lesson not found", "id": id})
				os.Exit(1)
			}
			fmt.Fprintf(os.Stderr, "sila lesson show: no lesson %s in %s\n", id, project)
			os.Exit(1)
		}
		if jsonOut {
			emitJSON(found)
			return
		}
		symptom, cause, fix, tagList := store.ParseLesson(found.Claim)
		fmt.Fprint(os.Stdout, render.RenderHeader(ui, render.Header{Command: "lesson", Title: found.ID}))
		fmt.Fprintf(os.Stdout, "  Symptom:  %s\n", ui.Bold(symptom))
		if cause != "" {
			fmt.Fprintf(os.Stdout, "  Cause:    %s\n", cause)
		}
		if fix != "" {
			fmt.Fprintf(os.Stdout, "  Fix:      %s\n", fix)
		}
		if tagList != "" {
			fmt.Fprintf(os.Stdout, "  Tags:     %s\n", tagList)
		}
		fmt.Fprintf(os.Stdout, "  Project:  %s\n", found.ProjectID)
		fmt.Fprintf(os.Stdout, "  Recorded: %s\n", found.FirstSeen)
		if len(found.RepoFiles) > 0 {
			grounded := ui.Red("missing")
			if found.FileRefsExist || found.Verified {
				grounded = ui.Green("grounded")
			}
			fmt.Fprintf(os.Stdout, "  Evidence: %s (%s)\n", strings.Join(found.RepoFiles, ", "), grounded)
		}
		return
	}

	// add
	symptom := strings.TrimSpace(strings.Join(args, " "))
	if symptom == "" {
		fmt.Fprintln(os.Stderr, "sila lesson: specify the symptom (e.g. sila lesson \"xlsx export hangs on large sheets\" --cause=\"shared strings\" --fix=\"stream rows\")")
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
	content := formatLesson(symptom, cause, fix, tags)

	existing, err := st.LessonFacts("", 500)
	if err == nil {
		exact, similar := lessonDuplicates(existing, content)
		if exact != nil && !force {
			if jsonOut {
				emitJSON(map[string]any{"error": "duplicate lesson", "duplicate": exact})
				return
			}
			fmt.Fprintf(os.Stderr, "sila lesson: an identical lesson already exists (%s). Use --force to record it anyway.\n", exact.ID)
			fmt.Fprintf(os.Stdout, "help: sila lesson show %s\n", exact.ID)
			os.Exit(1)
		}
		if len(similar) > 0 && !jsonOut {
			fmt.Fprintln(os.Stdout, ui.Dim("Similar lessons already recorded:"))
			for _, s := range similar {
				ss, _, _, _ := store.ParseLesson(s.Claim)
				fmt.Fprintf(os.Stdout, "  %s %s %s\n", ui.Dim(s.ID), trunc(ss, 60), ui.Dim("("+s.ProjectID+")"))
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
		fact, err = st.InsertLessonWithOrigin(project, content, agent, cwd, fileList, charter.ParentSessionID, charter.OriginRole())
	} else if sessionRef := resolveWriteSession(project); sessionRef != "" {
		fact, err = st.InsertLessonWithSession(project, content, agent, cwd, fileList, sessionRef)
	} else {
		fact, err = st.InsertLesson(project, content, agent, cwd, fileList)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}
	recordMemoryEvent(st, project, agent, "lesson", "ok", "", "cli", 0)

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
			grounding = "evidence files missing on disk"
		}
	}
	symptom, cause, fix, _ = store.ParseLesson(fact.Claim)
	fmt.Fprint(os.Stdout, render.RenderCard(ui, render.Card{
		Verb:   "Recorded",
		Object: "lesson",
		ID:     fact.ID,
		Fields: []render.Field{
			{Label: "Symptom", Value: symptom},
			{Label: "Cause", Value: cause},
			{Label: "Fix", Value: fix},
			{Label: "Project", Value: cardProjectLabel(st, project)},
			{Label: "Grounding", Value: grounding},
		},
	}))
}

// lessonHasTag reports whether a comma-separated tag list contains the wanted tag.
func lessonHasTag(tagList, want string) bool {
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

// runLessonDistill scans recent session transcripts for solved bugs and candidate lessons.
func runLessonDistill(ui render.UI, st *store.Store, project, agent, cwd, since string, save, jsonOut, toonOut bool) {
	sinceTime := parseSince(since)
	candidates := dream.ExtractProjectLessonCandidates(st, project, sinceTime)

	if save {
		existing, _ := st.LessonFacts("", 500)
		var saved []*store.Fact
		for _, cand := range candidates {
			content := formatLesson(cand.Symptom, cand.Cause, cand.Fix, strings.Join(cand.Tags, ","))
			exact, _ := lessonDuplicates(existing, content)
			if exact != nil {
				continue
			}
			fact, err := st.InsertLesson(project, content, agent, cwd, cand.Files)
			if err == nil && fact != nil {
				saved = append(saved, fact)
				existing = append(existing, *fact)
			}
		}

		if jsonOut {
			emitJSON(map[string]any{
				"project":          project,
				"candidates_count": len(candidates),
				"saved_count":      len(saved),
				"saved":            saved,
				"status":           "saved",
			})
			return
		}
		if toonOut {
			rows := make([][]string, 0, len(saved))
			for _, l := range saved {
				symptom, _, fix, _ := store.ParseLesson(l.Claim)
				rows = append(rows, []string{l.ID, l.ProjectID, symptom, fix})
			}
			fmt.Fprint(os.Stdout, render.FormatTOONTable("distilled_lessons", []string{"id", "project", "symptom", "fix"}, rows))
			return
		}
		if len(saved) == 0 {
			if len(candidates) == 0 {
				fmt.Fprintf(os.Stdout, "%s No unrecorded lesson candidates found in recent messages for %s.\n", ui.Yellow("▲"), ui.Bold(project))
			} else {
				fmt.Fprintf(os.Stdout, "%s All %d candidate(s) already exist in store for %s.\n", ui.Green("✔"), len(candidates), ui.Bold(project))
			}
			return
		}
		fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · lesson distill"))+" "+ui.Dim(fmt.Sprintf("(%s · %d saved)", project, len(saved))))
		for _, f := range saved {
			symptom, cause, fix, _ := store.ParseLesson(f.Claim)
			groundNote := ""
			if len(f.RepoFiles) > 0 {
				if f.FileRefsExist || f.Verified {
					groundNote = ui.Green(" · grounded")
				} else {
					groundNote = ui.Yellow(" · missing on disk")
				}
			}
			fmt.Fprintf(os.Stdout, "  %s %s: %s%s\n", ui.Green("✔"), ui.Bold(f.ID), symptom, groundNote)
			if cause != "" {
				fmt.Fprintf(os.Stdout, "    %s %s\n", ui.Dim("cause:"), cause)
			}
			if fix != "" {
				fmt.Fprintf(os.Stdout, "    %s %s\n", ui.Dim("fix:  "), fix)
			}
		}
		return
	}

	// Preview mode (save == false)
	if jsonOut {
		emitJSON(map[string]any{
			"project":    project,
			"count":      len(candidates),
			"candidates": candidates,
			"status":     "preview",
		})
		return
	}
	if toonOut {
		rows := make([][]string, 0, len(candidates))
		for _, c := range candidates {
			rows = append(rows, []string{c.Symptom, c.Cause, c.Fix, strings.Join(c.Files, ", ")})
		}
		fmt.Fprint(os.Stdout, render.FormatTOONTable("candidate_lessons", []string{"symptom", "cause", "fix", "files"}, rows))
		return
	}

	if len(candidates) == 0 {
		fmt.Fprintf(os.Stdout, "%s No unrecorded lesson candidates found in recent messages for %s.\n", ui.Yellow("▲"), ui.Bold(project))
		fmt.Fprintln(os.Stdout, "help: record manually with 'sila lesson \"<symptom>\" --cause=<text> --fix=<text>'")
		return
	}

	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · lesson distill"))+" "+ui.Dim(fmt.Sprintf("(%s · %d candidate(s))", project, len(candidates))))
	headers := []string{"symptom", "fix", "evidence"}
	rows := make([][]string, 0, len(candidates))
	for _, c := range candidates {
		evidence := strings.Join(c.Files, ", ")
		if evidence == "" {
			evidence = ui.Dim("-")
		}
		rows = append(rows, []string{trunc(c.Symptom, 45), trunc(c.Fix, 40), trunc(evidence, 30)})
	}
	fmt.Fprintln(os.Stdout, ui.Table(headers, rows, []string{"l", "l", "l"}))
	fmt.Fprintf(os.Stdout, "\nhelp: run 'sila lesson distill --save' to persist into store, or 'sila lesson \"<symptom>\" --cause=... --fix=...' to record individually\n")
}
