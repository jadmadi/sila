package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

// runOverlaps lists cross-tool overlaps discovered during ingest. Read-only.
func runOverlaps(ui render.UI, dbPath, project string, jsonOut, verbose bool) {
	if _, err := os.Stat(dbPath); err != nil {
		if jsonOut {
			emitJSON([]jsonOverlap{})
		} else {
			fmt.Fprintln(os.Stdout, ui.Dim("No knowledge store yet. Run `sila ingest` first."))
		}
		return
	}
	st, err := store.OpenRO(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	prj := project
	if prj != "" {
		ps, err := st.Projects()
		if err == nil {
			for _, p := range ps {
				if projSelMatch(p, prj) {
					prj = p.ID
					break
				}
			}
		}
	}
	ov, err := st.Overlaps(prj)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		out := make([]jsonOverlap, 0, len(ov))
		for _, o := range ov {
			out = append(out, jsonOverlap{ProjectID: o.ProjectID, ToolsA: o.ToolsA, ToolsB: o.ToolsB, Similarity: o.Similarity, FirstSeen: o.FirstSeen})
		}
		emitJSON(out)
		return
	}
	if len(ov) == 0 {
		fmt.Fprintln(os.Stdout, ui.Dim("No cross-tool overlaps found."))
		return
	}

	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · overlaps")))
	headers := []string{"project", "pair", "similarity", "first seen"}
	rows := make([][]string, 0, len(ov))
	for _, o := range ov {
		a := strings.Join(o.ToolsA, ",")
		b := strings.Join(o.ToolsB, ",")
		if a == "" {
			a = "-"
		}
		if b == "" {
			b = "-"
		}
		simPct := fmt.Sprintf("%.0f%%", o.Similarity*100)
		rows = append(rows, []string{o.ProjectID, ui.Bold(a + " ↔ " + b), ui.Yellow(simPct), ui.Dim(o.FirstSeen)})
	}
	fmt.Fprintln(os.Stdout, ui.Table(headers, rows, []string{"l", "l", "r", "l"}))
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, ui.Dim(fmt.Sprintf("%d cross-tool %s — same project+role, similar text, different tools.",
		len(ov), plural(int64(len(ov)), "overlap"))))
}

// jsonOverlap is the machine-readable overlap shape.
type jsonOverlap struct {
	ProjectID  string   `json:"project_id"`
	ToolsA     []string `json:"tools_a"`
	ToolsB     []string `json:"tools_b"`
	Similarity float64  `json:"similarity"`
	FirstSeen  string   `json:"first_seen"`
}

// runProjects lists every project with its message/fact/verified counts. It is
// read-only and, like assess, never creates a missing store.
func runProjects(ui render.UI, dbPath string, force, dryRun, jsonOut, verbose bool, args []string) {
	if len(args) > 0 && args[0] == "merge" {
		runProjectsMerge(ui, dbPath, force, dryRun, jsonOut, args[1:])
		return
	}
	if len(args) > 0 && args[0] == "prune" {
		runProjectsPrune(ui, dbPath, force, dryRun, jsonOut)
		return
	}
	if len(args) > 0 && args[0] == "rename" {
		runProjectsRename(ui, dbPath, dryRun, jsonOut, args[1:])
		return
	}
	if _, err := os.Stat(dbPath); err != nil {
		if jsonOut {
			emitJSON([]jsonProject{})
		} else {
			fmt.Fprintln(os.Stdout, ui.Dim("No knowledge store yet. Run `sila ingest` first."))
		}
		return
	}
	st, err := store.OpenRO(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	stats, err := st.ProjectsWithCounts()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		out := make([]jsonProject, 0, len(stats))
		for _, p := range stats {
			out = append(out, jsonProject{ID: p.ID, Path: p.Path, Messages: p.Messages, Facts: p.Facts, Verified: p.Verified})
		}
		emitJSON(out)
		return
	}

	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · projects")))
	headers := []string{"id", "dir", "messages", "claims", "refs∃"}
	rows := make([][]string, 0, len(stats))
	var tm, tf, tv int64
	for _, p := range stats {
		tm += int64(p.Messages)
		tf += int64(p.Facts)
		tv += int64(p.Verified)
		pathCell := shortParent(p.Path)
		if p.ID == "no-project" {
			pathCell = "(unscoped / global logs)"
		} else if verbose {
			pathCell = p.Path
		}
		idCell := ui.Bold(p.ID)
		if p.ID == "no-project" {
			idCell = ui.Dim("no-project")
		}
		rows = append(rows, []string{idCell, ui.Dim(pathCell),
			fmt.Sprint(p.Messages), fmt.Sprint(p.Facts), verifiedCell(ui, p.Verified)})
	}
	fmt.Fprintln(os.Stdout, ui.Table(headers, rows, []string{"l", "l", "r", "r", "r"}))
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, ui.Bold(fmt.Sprintf("%d %s, %d messages, %d claims (%d file_refs_exist).",
		len(stats), plural(int64(len(stats)), "project"), tm, tf, tv)))
	if tf == 0 {
		fmt.Fprintln(os.Stdout, ui.Dim("Hint: Run `sila dream` or `sila sync` to distill claims from messages."))
	}
	fmt.Fprintln(os.Stdout, ui.Dim("Note: 'no-project' stores ungrounded/global commands without a repository working directory."))
	if verbose {
		fmt.Fprintln(os.Stdout, ui.Dim("Use --project=<id> with ingest/assess/dream to scope."))
	}
}

// runProjectsMerge moves every record from one project registration to another
// and removes the dropped row. It is the supported fix for path-alias duplicates.
func runProjectsMerge(ui render.UI, dbPath string, force, dryRun, jsonOut bool, args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: sila projects merge <keep> <drop> [--dry-run] [--force]")
		os.Exit(2)
	}
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	keep, err := st.ResolveProject(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila projects merge: %v\n", err)
		os.Exit(1)
	}
	drop, err := st.ResolveProject(args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila projects merge: %v\n", err)
		os.Exit(1)
	}
	if keep.ID == drop.ID {
		fmt.Fprintf(os.Stderr, "sila projects merge: %s and %s name the same project (%s)\n", args[0], args[1], keep.ID)
		os.Exit(1)
	}

	keepKey, keepErr := filepath.EvalSymlinks(keep.Path)
	dropKey, dropErr := filepath.EvalSymlinks(drop.Path)
	if !force && keepErr == nil && dropErr == nil && filepath.Clean(keepKey) != filepath.Clean(dropKey) {
		fmt.Fprintf(os.Stderr, "sila projects merge: %s and %s resolve to different directories (%s vs %s). Use --force to merge anyway.\n", keep.ID, drop.ID, keepKey, dropKey)
		os.Exit(1)
	}

	if dryRun {
		stats, _ := st.ProjectsWithCounts()
		var dropStat store.ProjectStat
		for _, s := range stats {
			if s.ID == drop.ID {
				dropStat = s
			}
		}
		if jsonOut {
			emitJSON(map[string]any{"dry_run": true, "keep": keep.ID, "keep_path": keep.Path, "drop": drop.ID, "drop_path": drop.Path, "messages": dropStat.Messages, "facts": dropStat.Facts})
			return
		}
		fmt.Fprintf(os.Stdout, "%s Would merge %s into %s: %d messages and %d claims move; the %s registration is removed.\n",
			ui.Yellow("▲"), ui.Bold(drop.ID), ui.Bold(keep.ID), dropStat.Messages, dropStat.Facts, drop.ID)
		return
	}

	if err := st.MergeProjects(keep.ID, drop.ID); err != nil {
		fmt.Fprintf(os.Stderr, "sila projects merge: %v\n", err)
		os.Exit(1)
	}
	if jsonOut {
		emitJSON(map[string]any{"merged": true, "keep": keep.ID, "drop": drop.ID})
		return
	}
	fmt.Fprintf(os.Stdout, "%s Merged duplicate registration %s into %s (%s)\n", ui.Green("✔"), ui.Bold(drop.ID), ui.Bold(keep.ID), keep.Path)
}

// runProjectsRename corrects a registration's id in place. It is the fix for a
// stored id that no longer describes the directory — a stale workspace file in
// a container directory naming an unrelated project, or a hand-corrected
// collision. The path and every record stay put; only the name moves.
func runProjectsRename(ui render.UI, dbPath string, dryRun, jsonOut bool, args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: sila projects rename <project> <new-id> [--dry-run]")
		os.Exit(2)
	}
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	old, err := st.ResolveProject(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila projects rename: %v\n", err)
		os.Exit(1)
	}

	if dryRun {
		var stat store.ProjectStat
		if stats, err := st.ProjectsWithCounts(); err == nil {
			for _, s := range stats {
				if s.ID == old.ID {
					stat = s
				}
			}
		}
		hashed, _ := st.HashedFactCount(old.ID)
		if jsonOut {
			emitJSON(map[string]any{"dry_run": true, "from": old.ID, "to": args[1], "path": old.Path, "messages": stat.Messages, "facts": stat.Facts, "hashed_facts": hashed})
			return
		}
		fmt.Fprintf(os.Stdout, "%s Would rename %s to %s: %d messages and %d claims stay with %s.\n",
			ui.Yellow("▲"), ui.Bold(old.ID), ui.Bold(args[1]), stat.Messages, stat.Facts, old.Path)
		printLedgerNote(ui, hashed, old.ID, args[1])
		return
	}

	renamed, err := st.RenameProject(old.ID, args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila projects rename: %v\n", err)
		os.Exit(1)
	}
	if jsonOut {
		emitJSON(map[string]any{"renamed": true, "from": old.ID, "to": renamed.Project.ID, "path": renamed.Project.Path, "hashed_facts": renamed.HashedFacts})
		return
	}
	fmt.Fprintf(os.Stdout, "%s Renamed %s to %s (%s)\n", ui.Green("✔"), ui.Bold(old.ID), ui.Bold(renamed.Project.ID), renamed.Project.Path)
	printLedgerNote(ui, renamed.HashedFacts, old.ID, renamed.Project.ID)
}

// printLedgerNote explains the one thing a rename cannot move: the fact ledger
// hash binds project_id, so moved facts report as ledger mismatches until the
// operator authorizes a rebaseline. sila never rewrites those hashes itself,
// because that would erase the difference between a rename and tampering.
func printLedgerNote(ui render.UI, hashed int, oldID, newID string) {
	if hashed == 0 {
		return
	}
	fmt.Fprintln(os.Stdout)
	fmt.Fprintf(os.Stdout, "%s %d hashed %s moved with the id; the ledger hash chain binds project_id, so they now report as mismatches in %s.\n",
		ui.Yellow("▲"), hashed, plural(int64(hashed), "fact"), ui.Bold("sila facts verify"))
	fmt.Fprintf(os.Stdout, "  To re-seal the ledger after this authorized rename:\n")
	fmt.Fprintf(os.Stdout, "    %s\n", ui.Bold(fmt.Sprintf("sila facts rebaseline --reason=\"project rename %s → %s\"", oldID, newID)))
}

// runProjectsPrune removes registrations whose temp-directory path no longer
// exists, with every record they own. Without --force it only previews.
func runProjectsPrune(ui render.UI, dbPath string, force, dryRun, jsonOut bool) {
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()
	dead, err := st.DeadTempProjects()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila projects prune: %v\n", err)
		os.Exit(1)
	}
	apply := force && !dryRun
	if apply && len(dead) > 0 {
		ids := make([]string, 0, len(dead))
		for _, d := range dead {
			ids = append(ids, d.ID)
		}
		if err := st.PruneProjects(ids); err != nil {
			fmt.Fprintf(os.Stderr, "sila projects prune: %v\n", err)
			os.Exit(1)
		}
	}
	if jsonOut {
		emitJSON(map[string]any{"pruned": apply, "dry_run": !apply, "projects": dead, "count": len(dead)})
		return
	}
	if len(dead) == 0 {
		fmt.Fprintf(os.Stdout, "%s No dead temp-directory projects.\n", ui.Green("✔"))
		return
	}
	var msgs, facts int
	for _, d := range dead {
		msgs += d.Messages
		facts += d.Facts
		fmt.Fprintf(os.Stdout, "  %s %s\n", ui.Bold(d.ID), ui.Dim(d.Path))
	}
	if apply {
		fmt.Fprintf(os.Stdout, "%s Pruned %d projects (%d messages, %d claims) and re-chained the fact ledger.\n", ui.Green("✔"), len(dead), msgs, facts)
		return
	}
	fmt.Fprintf(os.Stdout, "%s Would prune %d projects (%d messages, %d claims). Run with --force to apply.\n", ui.Yellow("▲"), len(dead), msgs, facts)
}
