package main

import (
	"fmt"
	"os"

	"github.com/jadmadi/sila/internal/assess"
	"github.com/jadmadi/sila/internal/dream"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func runAssess(ui render.UI, dbPath, project string, jsonOut, verbose bool, tools []string) {
	var st *store.Store
	storeExists := false
	if _, err := os.Stat(dbPath); err == nil {
		s, err := store.OpenRO(dbPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila: %v\n", err)
			os.Exit(1)
		}
		defer s.Close()
		st = s
		storeExists = true
	}

	reports, err := assess.Run(st, assess.Options{Tools: tools, ProjectSel: project})
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		out := make([]jsonAssessReport, 0, len(reports))
		for _, r := range reports {
			jr := jsonAssessReport{
				Tool: r.Tool, Source: r.Source, Exists: r.Exists,
				Size: r.Size, ModTime: r.ModTime, HasWatermark: r.HasWatermark,
				Total: r.Total, New: r.New,
			}
			if r.Error != nil {
				jr.Error = r.Error.Error()
			}
			out = append(out, jr)
		}
		emitJSON(out)
		return
	}

	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · assess")))

	headers := []string{"tool", "state", "total", "new"}
	aligns := []string{"l", "l", "r", "r"}
	rows := make([][]string, 0, len(reports))
	var wouldIngest, sourcesWithData, rowCounts int64
	for _, r := range reports {
		if r.NotScoped {
			rows = append(rows, []string{r.Tool, ui.Dim("n/a"), ui.Dim("-"), ui.Dim("-")})
			continue
		}
		state, stCol := "ok", render.Green
		switch {
		case !r.Exists:
			state, stCol = "absent", render.Yellow
		case r.Error != nil:
			state, stCol = "error", render.Red
		}
		total := ui.Dim(fmt.Sprint(r.Total))
		if r.Total > 0 {
			sourcesWithData++
			total = fmt.Sprint(r.Total)
		}
		newCell := ui.Dim("0")
		if r.New > 0 {
			wouldIngest += r.New
			newCell = ui.Bold(ui.Yellow(fmt.Sprint(r.New)))
		}
		rowCounts++
		rows = append(rows, []string{r.Tool, ui.Paint(stCol, state), total, newCell})
	}
	fmt.Fprintln(os.Stdout, ui.Table(headers, rows, aligns))

	// Semantic verdict + the exact command to run.
	var verdict string
	var act string
	switch {
	case !storeExists:
		verdict = ui.Dim("Nothing to ingest yet — no knowledge store.")
		act = actionLine(ui, "sila sync", "build the store from all tools on first run")
	case wouldIngest > 0:
		verdict = ui.Bold(ui.Yellow(fmt.Sprintf(
			"%d new messages waiting across %d %s — ingest them.", wouldIngest, sourcesWithData, plural(sourcesWithData, "source"))))
		act = actionLine(ui, "sila sync", "ingest + distill in one pass")
	default:
		verdict = ui.Green("Up to date — nothing new to ingest.")
		act = actionLine(ui, "sila status", "confirm health at a glance")
	}
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, verdict)
	fmt.Fprintln(os.Stdout, act)
	fmt.Fprintln(os.Stdout, ui.Dim("Legend: state ok=readable · absent=no data · error=unreadable · new=pending messages."))
	if project != "" {
		fmt.Fprintln(os.Stdout, ui.Dim("n/a = no working dir, so --project does not apply (codex/command-code/codewhale/agy)."))
	}

	if verbose {
		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout, ui.Bold("Sources"))
		for _, r := range reports {
			if !r.Exists {
				fmt.Fprintln(os.Stdout, ui.Dim("  "+r.Tool+": <not present>"))
				continue
			}
			meta := render.FormatBytes(r.Size)
			if r.ModTime != "" {
				meta += " · modified " + r.ModTime
			}
			fmt.Fprintln(os.Stdout, "  "+r.Tool+": "+r.Source+" ("+ui.Dim(meta)+")")
		}
	}
}

// DistillProgress tracks progress of claim distillation across projects.
type DistillProgress struct {
	ProjectID   string
	ProjectPath string
	Index       int
	Total       int
	Phase       string // "start", "done"
	Facts       int
	Verified    int
	Skipped     bool // history unchanged since the last distillation
}

// DistillProgressFunc is called as each project undergoes distillation.
type DistillProgressFunc func(p DistillProgress)

// distill computes grounded facts from assistant messages, consolidates multi-tool corroborations,
// reconciles filesystem drift against disk, and prunes stale facts. Shared by `dream` and `sync`.
func distill(st *store.Store, projSel string) (produced, verifiedN, projCount int, activeProjects []string, err error) {
	return distillWithProgress(st, projSel, false, nil)
}

// distillWithProgress is distill with an optional per-project progress callback.
func distillWithProgress(st *store.Store, projSel string, force bool, onProgress DistillProgressFunc) (produced, verifiedN, projCount int, activeProjects []string, err error) {
	projects, err := st.Projects()
	if err != nil {
		return 0, 0, 0, nil, err
	}
	var targets []store.Project
	for _, p := range projects {
		if projSel == "" || projSelMatch(p, projSel) {
			targets = append(targets, p)
		}
	}
	total := len(targets)
	for idx, p := range targets {
		// Distillation is the dominant cost of a sync, and it is proportional to
		// total history rather than to what changed. Skip a project whose newest
		// message has already been distilled: a no-change sync then distills
		// nothing instead of re-deriving every project's facts. The decision is
		// made before reporting anything, so a skipped project never appears to
		// start work it will not do.
		latest, err := st.LatestMessageID(p.ID)
		if err != nil {
			return 0, 0, 0, nil, err
		}
		if !force {
			if wm, werr := st.DistillWatermark(p.ID); werr == nil && latest <= wm {
				if onProgress != nil {
					onProgress(DistillProgress{
						ProjectID:   p.ID,
						ProjectPath: p.Path,
						Index:       idx + 1,
						Total:       total,
						Phase:       "done",
						Skipped:     true,
					})
				}
				continue
			}
		}
		if onProgress != nil {
			onProgress(DistillProgress{
				ProjectID:   p.ID,
				ProjectPath: p.Path,
				Index:       idx + 1,
				Total:       total,
				Phase:       "start",
			})
		}
		dream.InvalidateProjectFileCache(p.Path)
		msgs, err := st.MessagesForDistill(p.ID)
		if err != nil {
			return 0, 0, 0, nil, err
		}
		var rawFacts []store.Fact
		for _, m := range msgs {
			f := dream.Distill(p.ID, p.Path, m.Content, m.SessionID, m.Tools, m.TS)
			if f != nil {
				f.OriginRole = m.Role
				rawFacts = append(rawFacts, *f)
			}
		}
		consolidated := dream.ConsolidateFacts(rawFacts)
		projAboutTo := produced
		var producedIDs []string
		projVerified := 0
		for _, f := range consolidated {
			if err := st.UpsertFact(f); err != nil {
				return 0, 0, 0, nil, err
			}
			if f.SupersedesID != "" && f.SupersedesID != f.ID {
				_ = st.MarkFactStale(f.SupersedesID)
			}
			producedIDs = append(producedIDs, f.ID)
			produced++
			if f.Verified {
				verifiedN++
				projVerified++
			}
		}
		if err := st.PruneFacts(p.ID, producedIDs); err != nil {
			return 0, 0, 0, nil, err
		}
		// Reconcile drift against current repository filesystem state
		_, _ = st.ReconcileFactDrift(p.ID, p.Path)
		// Only now is this project's history distilled: record the watermark so
		// the next sync can skip it.
		if serr := st.SetDistillWatermark(p.ID, latest); serr != nil {
			return 0, 0, 0, nil, serr
		}

		projProduced := produced - projAboutTo
		if projProduced > 0 {
			projCount++
			activeProjects = append(activeProjects, p.ID)
		}
		if onProgress != nil {
			onProgress(DistillProgress{
				ProjectID:   p.ID,
				ProjectPath: p.Path,
				Index:       idx + 1,
				Total:       total,
				Phase:       "done",
				Facts:       projProduced,
				Verified:    projVerified,
			})
		}
	}
	return produced, verifiedN, projCount, activeProjects, nil
}

// runSync is the workflow to ingest all tools, then distill facts, with optional continuous live watching.
