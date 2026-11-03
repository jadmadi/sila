package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/dream"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func runDream(ui render.UI, dbPath, project, tool string, refsExistOnly, staleOnly, factsOnly, dryRun, semantic bool, llmProvider string, retryFailed, jsonOut, toonOut, verbose bool, top int, rest []string) {
	var st *store.Store
	var err error
	if dryRun {
		st, err = store.OpenRO(dbPath)
	} else {
		st, err = store.Open(dbPath)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	if len(rest) > 0 {
		switch rest[0] {
		case "reconcile":
			proj := project
			if proj == "" && len(rest) > 1 {
				proj = rest[1]
			}
			runReconcile(ui, st, proj, dryRun, semantic, jsonOut, verbose)
			return
		case "query":
			runDreamQuery(ui, st, project, tool, refsExistOnly, staleOnly, factsOnly, jsonOut, toonOut, verbose, top, rest[1:])
			return
		case "compact":
			runDreamCompact(ui, st, project, dryRun, jsonOut, toonOut, verbose, rest[1:])
			return
		}
	}

	if llmProvider != "" {
		runLLM(ui, st, llmProvider, project, retryFailed, jsonOut, verbose)
		return
	}

	projSel := project
	if projSel == "" && len(rest) > 0 {
		projSel = rest[0]
	}

	if dryRun {
		if !jsonOut {
			fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · dream"))+" "+ui.Yellow("[PREVIEW / DRY-RUN]"))
		}
		facts, _ := st.FactsAll()
		if projSel != "" {
			facts = filterProject(facts, projSel)
		}
		printFacts(ui, st, facts, jsonOut, toonOut, verbose)
		if !jsonOut {
			fmt.Fprintln(os.Stdout)
			fmt.Fprintln(os.Stdout, ui.Dim("  (Dry-run: no claims were distilled or pruned)."))
		}
		return
	}

	if !jsonOut {
		fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · dream")))
	}
	produced, verifiedN, projCount, _, err := distill(st, projSel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}

	facts, err := st.FactsAll()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}
	if projSel != "" {
		facts = filterProject(facts, projSel)
	}
	if factsOnly {
		var onlyFacts []store.Fact
		for _, f := range facts {
			if f.IsFact() {
				onlyFacts = append(onlyFacts, f)
			}
		}
		facts = onlyFacts
	}
	if staleOnly {
		var unverified []store.Fact
		for _, f := range facts {
			if !(f.FileRefsExist || f.Verified) {
				unverified = append(unverified, f)
			}
		}
		facts = unverified
	}
	printFacts(ui, st, facts, jsonOut, toonOut, verbose)
	if jsonOut {
		return
	}
	fmt.Fprintln(os.Stdout)

	if produced == 0 {
		fmt.Fprintln(os.Stdout, ui.Dim("No grounded claims distilled yet. Run `sila ingest` first."))
	} else {
		fmt.Fprintln(os.Stdout, ui.Bold(ui.Yellow(fmt.Sprintf(
			"Distilled %d claims (%d file_refs_exist) across %d %s.", produced, verifiedN, projCount, plural(int64(projCount), "project")))))
	}
	fmt.Fprintln(os.Stdout, ui.Dim("Legend: refs∃=referenced files exist on disk · corroboration=corroborated | grounded | ungrounded."))
}

func runDreamQuery(ui render.UI, st *store.Store, project, tool string, refsExistOnly, staleOnly, factsOnly, jsonOut, toonOut, verbose bool, top int, terms []string) {
	ranked, err := dream.QueryStoreFacts(st, terms, project, tool, refsExistOnly, staleOnly, factsOnly, top)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}
	if !jsonOut && !toonOut {
		if len(ranked) == 0 {
			queryDesc := strings.Join(terms, " ")
			if queryDesc == "" {
				queryDesc = "all"
			}
			fmt.Fprintf(os.Stdout, "facts: 0 claims found matching %q %s\n", queryDesc, scopeLabel(project))
			fmt.Fprintln(os.Stdout, "help: try searching without filters or run 'sila sync' to refresh")
			if len(terms) > 1 {
				fmt.Fprintf(os.Stdout, "help: every term must appear in one claim — try fewer terms\n")
			}
			return
		}
		fmt.Fprintln(os.Stdout, ui.Dim("Claims are extracted source messages. Verify referenced files on disk before trusting."))
	}
	printFacts(ui, st, ranked, jsonOut, toonOut, verbose)
}

// runDreamCompact executes the A2 zero-LLM extractive keep-token compaction sweep over cold memories.
func runDreamCompact(ui render.UI, st *store.Store, project string, dryRun, jsonOut, toonOut, verbose bool, rest []string) {
	cfs := flag.NewFlagSet("compact", flag.ContinueOnError)
	cfs.SetOutput(io.Discard)
	flagProj := cfs.String("project", project, "Project ID or path to scope compaction")
	flagProjAlias := cfs.String("p", "", "Project ID or path to scope compaction (shorthand)")
	flagDryRun := cfs.Bool("dry-run", dryRun, "Simulate compaction without updating store")
	flagDryRunAlias := cfs.Bool("n", false, "Simulate compaction without updating store (shorthand)")
	flagThreshold := cfs.Float64("threshold", 0.35, "Cold retention threshold (0.0-1.0)")
	flagLimit := cfs.Int("limit", 100, "Maximum candidate memories to compact")
	flagJSON := cfs.Bool("json", jsonOut, "Output machine-readable JSON")
	flagToon := cfs.Bool("toon", toonOut, "Output compact TOON format")

	_ = cfs.Parse(rest)

	effProj := *flagProj
	if effProj == "" {
		effProj = *flagProjAlias
	}
	if effProj == "" && len(cfs.Args()) > 0 {
		effProj = cfs.Args()[0]
	}

	isDryRun := *flagDryRun || *flagDryRunAlias || dryRun
	isJSON := *flagJSON || jsonOut
	isToon := *flagToon || toonOut

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	opts := dream.CompactionSweepOptions{
		ProjectID:     effProj,
		ColdThreshold: *flagThreshold,
		DryRun:        isDryRun,
		MaxCandidates: *flagLimit,
	}

	telem, err := dream.RunCompactionSweep(ctx, st, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: compaction failed: %v\n", err)
		os.Exit(1)
	}

	if isJSON {
		emitJSON(telem)
		return
	}
	if isToon {
		var fields [][2]string
		fields = append(fields, [2]string{"project", telem.ProjectID})
		fields = append(fields, [2]string{"candidates_found", fmt.Sprint(telem.CandidatesFound)})
		fields = append(fields, [2]string{"compacted_count", fmt.Sprint(telem.CompactedCount)})
		fields = append(fields, [2]string{"skipped_count", fmt.Sprint(telem.SkippedCount)})
		fields = append(fields, [2]string{"original_tokens", fmt.Sprint(telem.OriginalTokens)})
		fields = append(fields, [2]string{"final_tokens", fmt.Sprint(telem.FinalTokens)})
		fields = append(fields, [2]string{"saved_tokens", fmt.Sprint(telem.SavedTokens)})
		fields = append(fields, [2]string{"savings_ratio", fmt.Sprintf("%.2f%%", telem.SavingsRatio*100)})
		fields = append(fields, [2]string{"dry_run", fmt.Sprint(telem.DryRun)})
		fmt.Fprint(os.Stdout, render.FormatTOONObject("compaction_sweep", fields))
		return
	}

	title := ui.Cyan(ui.Bold("sila · dream compact"))
	if isDryRun {
		title += " " + ui.Yellow("[PREVIEW / DRY-RUN]")
	}
	fmt.Fprintln(os.Stdout, title)
	if effProj != "" {
		fmt.Fprintf(os.Stdout, "  Scope: %s\n", ui.Bold(effProj))
	}
	fmt.Fprintf(os.Stdout, "  Candidates found: %d | Compacted: %s | Skipped: %s\n",
		telem.CandidatesFound,
		ui.Green(fmt.Sprint(telem.CompactedCount)),
		ui.Dim(fmt.Sprint(telem.SkippedCount)))
	if telem.CompactedCount > 0 {
		fmt.Fprintf(os.Stdout, "  Tokens: %d original → %d compacted (%s saved, %s ratio)\n",
			telem.OriginalTokens,
			telem.FinalTokens,
			ui.Green(fmt.Sprintf("%d tokens", telem.SavedTokens)),
			ui.Yellow(fmt.Sprintf("%.1f%%", telem.SavingsRatio*100)))
		fmt.Fprintf(os.Stdout, "  Extracted Keep-Tokens: %d paths, %d errors, %d symbols, %d commands\n",
			telem.TotalPathsMined,
			telem.TotalErrorsMined,
			telem.TotalSymsMined,
			telem.TotalCmdsMined)

		if verbose && len(telem.Items) > 0 {
			fmt.Fprintln(os.Stdout)
			for _, item := range telem.Items {
				idPrefix := item.ID
				if len(idPrefix) > 8 {
					idPrefix = idPrefix[:8]
				}
				fmt.Fprintf(os.Stdout, "  - [%s] %s (%d → %d tokens, -%.0f%%)\n",
					ui.Dim(idPrefix),
					trunc(item.Summary, 70),
					item.OriginalTokens,
					item.FinalTokens,
					item.SavingsRatio*100)
			}
		}
	}
	if isDryRun {
		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout, ui.Dim("  (Dry-run: no database records were modified. Run without --dry-run to apply)."))
	}
}

// scopeLabel names the scope a query actually searched. Search and dream query
// do not resolve the working directory, so an unset project means every
// project: an empty-state message must say that rather than print a blank name.
func scopeLabel(project string) string {
	if project == "" {
		return "across all projects"
	}
	return "in project " + project
}

func anyMatch(facts []store.Fact, terms []string) bool {
	for _, f := range facts {
		if dream.MatchScore(f, terms) > 0 {
			return true
		}
	}
	return false
}

// printFacts renders claims as a compact box table, rich verbose inspection cards, TOON, or --json.
func printFacts(ui render.UI, st *store.Store, facts []store.Fact, jsonOut, toonOut, verbose bool) {
	if len(facts) == 0 {
		if jsonOut {
			emitJSON([]jsonFact{})
		} else if toonOut {
			fmt.Print("facts[0]{id,project,grounded,corroboration,claim}:\n")
		} else {
			fmt.Fprintln(os.Stdout, "facts: 0 claims to display.")
			fmt.Fprintln(os.Stdout, "help: run 'sila sync' to ingest sessions or 'sila dream' to distill claims")
		}
		return
	}
	if jsonOut {
		out := make([]jsonFact, 0, len(facts))
		for _, f := range facts {
			kind := f.Kind
			if kind == "" {
				if f.IsInvariant() {
					kind = "invariant"
				} else {
					kind = "code_claim"
				}
			}
			out = append(out, jsonFact{
				ProjectID:     f.ProjectID,
				Claim:         f.Claim,
				Kind:          kind,
				Confidence:    f.Corroboration,
				Corroboration: f.Corroboration,
				Verified:      f.FileRefsExist || f.Verified,
				FileRefsExist: f.FileRefsExist || f.Verified,
				IsFact:        f.IsFact(),
				IsInvariant:   f.IsInvariant(),
				Distilled:     f.Distilled,
				DistillStatus: f.DistillStatus,
				IsStale:       f.IsStaleFlag,
				RepoFiles:     f.RepoFiles,
				Tools:         f.Tools,
				SessionRef:    f.SessionRef,
				FirstSeen:     f.FirstSeen,
				RefsCheckedAt: f.RefsCheckedAt,
			})
		}
		emitJSON(out)
		return
	}

	if toonOut {
		rows := make([][]string, len(facts))
		for i, f := range facts {
			desc := f.Distilled
			if desc == "" {
				desc = f.Claim
			}
			if !verbose {
				desc = trunc(strings.ReplaceAll(desc, "\n", " "), 80)
			}
			corrob := f.Corroboration
			if corrob == "" {
				corrob = "ungrounded"
			}
			rows[i] = []string{
				f.ID,
				f.ProjectID,
				fmt.Sprintf("%t", f.FileRefsExist || f.Verified),
				corrob,
				desc,
			}
		}
		fmt.Fprint(os.Stdout, render.FormatTOONTable("facts", []string{"id", "project", "grounded", "corroboration", "claim"}, rows))
		if len(facts) > 0 {
			fmt.Fprintln(os.Stdout, "help[1]: Run `sila search expand <id>` to unroll full transcript context")
		}
		return
	}

	if verbose {
		printFactsVerbose(ui, st, facts)
		return
	}

	headers := []string{"project", "corroboration", "refs∃", "tools", "files", "claim"}
	rows := make([][]string, 0, len(facts))
	for _, f := range facts {
		conf := f.Corroboration
		if conf == "" {
			conf = f.Confidence
		}
		if f.IsInvariant() {
			conf = "invariant"
		}
		confCol := ui.Yellow(conf)
		if conf == "invariant" {
			confCol = ui.Cyan(ui.Bold("invariant"))
		} else if conf == "corroborated" || conf == "high" {
			confCol = ui.Green(conf)
		} else if conf == "ungrounded" || conf == "low" {
			confCol = ui.Dim(conf)
		}
		verCell := ui.Dim("no")
		if f.IsInvariant() {
			verCell = ui.Cyan("durable")
		} else if f.FileRefsExist || f.Verified {
			verCell = ui.Green("yes")
		}
		if f.IsStaleFlag {
			verCell = ui.Dim("archived")
		}
		tools := strings.Join(f.Tools, ",")
		if tools == "" {
			tools = "-"
		}
		files := strings.Join(f.RepoFiles, ", ")
		if files == "" {
			files = "-"
		}
		claim := f.Claim
		if f.Distilled != "" {
			claim = f.Distilled
		}
		if f.IsStaleFlag {
			claim = "[archived] " + claim
		}
		files = trunc(files, 40)
		claim = trunc(claim, 56)
		row := []string{f.ProjectID, confCol, verCell, ui.Dim(tools), ui.Dim(files), claim}
		rows = append(rows, row)
	}
	fmt.Fprintln(os.Stdout, ui.Table(headers, rows, []string{"l", "l", "l", "l", "l", "l"}))
}

func printFactsVerbose(ui render.UI, st *store.Store, facts []store.Fact) {
	var projPaths map[string]string
	if st != nil {
		projects, err := st.Projects()
		if err == nil {
			projPaths = make(map[string]string, len(projects))
			for _, p := range projects {
				projPaths[p.ID] = p.Path
			}
		}
	}

	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold(fmt.Sprintf("sila · claim inspection (%d %s)", len(facts), plural(int64(len(facts)), "claim")))))
	fmt.Fprintln(os.Stdout)

	for i, f := range facts {
		if i > 0 {
			fmt.Fprintln(os.Stdout)
			fmt.Fprintln(os.Stdout, ui.Dim("─────────────────────────────────────────────────────────────────────────────"))
			fmt.Fprintln(os.Stdout)
		}

		conf := f.Corroboration
		if conf == "" {
			conf = f.Confidence
		}
		confCol := ui.Yellow(conf)
		if conf == "corroborated" || conf == "high" {
			confCol = ui.Green(conf)
		} else if conf == "ungrounded" || conf == "low" {
			confCol = ui.Dim(conf)
		}

		firstSeen := f.FirstSeen
		if t, err := time.Parse(time.RFC3339, f.FirstSeen); err == nil {
			firstSeen = t.Format("2006-01-02 15:04")
		} else if len(firstSeen) > 10 {
			firstSeen = firstSeen[:10]
		}

		toolsStr := strings.Join(f.Tools, ", ")
		if toolsStr == "" {
			toolsStr = "unknown"
		}

		projDir := ""
		if projPaths != nil {
			projDir = projPaths[f.ProjectID]
		}

		// Header line: [Project] (corroborated · devin · seen 2026-07-30)
		fmt.Fprintf(os.Stdout, "%s %s\n",
			ui.Bold(fmt.Sprintf("[%s]", f.ProjectID)),
			ui.Dim(fmt.Sprintf("(%s · %s · seen %s)", confCol, toolsStr, firstSeen)))

		// Claim vs Distilled
		if f.Distilled != "" {
			fmt.Fprintf(os.Stdout, "  %s %s\n", ui.Bold("Distilled Fact:"), ui.Green(f.Distilled))
			fmt.Fprintf(os.Stdout, "  %s %s\n", ui.Dim("[raw] Source Message:"), ui.Dim(f.Claim))
		} else {
			fmt.Fprintf(os.Stdout, "  %s %s\n", ui.Bold("Claim:"), f.Claim)
		}

		// Filesystem existence status
		if f.FileRefsExist || f.Verified {
			fmt.Fprintf(os.Stdout, "  %s %s\n", ui.Bold("Files on Disk:"), ui.Green("yes (all referenced files exist)"))
		} else if len(f.RepoFiles) == 0 {
			fmt.Fprintf(os.Stdout, "  %s %s\n", ui.Bold("Files on Disk:"), ui.Dim("no (conceptual directive without file paths)"))
		} else {
			fmt.Fprintf(os.Stdout, "  %s %s\n", ui.Bold("Files on Disk:"), ui.Red("no (referenced files missing or moved)"))
		}

		// Files breakdown with on-disk checks
		if len(f.RepoFiles) > 0 {
			dirNote := ""
			if projDir != "" {
				dirNote = fmt.Sprintf(" in %s", projDir)
			}
			presentCount := 0
			type fileStatus struct {
				path   string
				exists bool
			}
			statuses := make([]fileStatus, 0, len(f.RepoFiles))
			for _, rf := range f.RepoFiles {
				full := rf
				if projDir != "" {
					full = filepath.Join(projDir, rf)
				}
				fi, err := os.Stat(full)
				exists := (err == nil && !fi.IsDir())
				if exists {
					presentCount++
				}
				statuses = append(statuses, fileStatus{path: rf, exists: exists})
			}

			fmt.Fprintf(os.Stdout, "  %s (%d/%d present%s):\n",
				ui.Bold("Referenced Files"), presentCount, len(f.RepoFiles), dirNote)

			for _, s := range statuses {
				if s.exists {
					fmt.Fprintf(os.Stdout, "    %s %s\n", ui.Green(render.GlyphSuccess), s.path)
				} else {
					fmt.Fprintf(os.Stdout, "    %s %s %s\n", ui.Red("✗"), s.path, ui.Dim("(not found on disk)"))
				}
			}
		}
	}
}

func filterProject(facts []store.Fact, sel string) []store.Fact {
	var out []store.Fact
	base := filepath.Base(filepath.Clean(sel))
	for _, f := range facts {
		if projSelStr(f.ProjectID, sel) || strings.EqualFold(f.ProjectID, base) {
			out = append(out, f)
		}
	}
	return out
}

func filterTool(facts []store.Fact, tool string) []store.Fact {
	var out []store.Fact
	for _, f := range facts {
		if slices.Contains(f.Tools, tool) {
			out = append(out, f)
		}
	}
	return out
}

func projSelMatch(p store.Project, sel string) bool {
	return store.ProjectSelectorMatches(p, sel)
}

// projSelStr matches a project id/path against a selector case-insensitively,
// so a user typing a repo name in lowercase (e.g. "almatjar") still finds it.
func projSelStr(id, sel string) bool {
	return strings.EqualFold(id, sel)
}

// shortParent renders the parent directory of a path compactly for the table:
// the id column already shows the repo basename, so this shows where it lives
// — "~" for home, front-elided when still too long. Full path is available via
// --verbose.
