package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/dream"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

// runRecall finds prior resolutions across projects and prints them in tier
// order: symbol notes (tier 0), lessons, then grounded facts and memos, then transcripts.
func runRecall(ui render.UI, dbPath, project string, limit int, semantic, jsonOut, toonOut bool, args []string) {
	var filteredArgs []string
	symbolTarget := ""
	for _, a := range args {
		if strings.HasPrefix(a, "--symbol=") {
			symbolTarget = strings.TrimPrefix(a, "--symbol=")
		} else if strings.HasPrefix(a, "-s=") {
			symbolTarget = strings.TrimPrefix(a, "-s=")
		} else {
			filteredArgs = append(filteredArgs, a)
		}
	}

	query := strings.TrimSpace(strings.Join(filteredArgs, " "))
	if query == "" && symbolTarget == "" {
		fmt.Fprintln(os.Stderr, "sila recall: specify a symptom, error message, package name, API, or --symbol")
		fmt.Fprintln(os.Stdout, "help: sila recall \"xlsx export hangs\"")
		os.Exit(2)
	}
	if limit <= 0 {
		limit = 5
	}

	st, _, err := openReadStore(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	var res *dream.RecallResult
	if query != "" {
		r, err := dream.RecallSolutions(st, query, dream.RecallOptions{Project: project, Limit: limit, Semantic: semantic})
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila recall: %v\n", err)
			os.Exit(1)
		}
		res = r
	} else {
		res = &dream.RecallResult{Query: symbolTarget, Project: project}
	}

	// Surfacing Tier 0 Symbol Notes: query by symbolTarget or FTS query
	var symbolNotes []store.SymbolNote
	seenIDs := make(map[string]bool)

	if symbolTarget != "" {
		if notes, err := st.ListSymbolNotes(project, symbolTarget); err == nil {
			for _, n := range notes {
				if !seenIDs[n.ID] {
					seenIDs[n.ID] = true
					symbolNotes = append(symbolNotes, n)
				}
			}
		}
	}
	if query != "" {
		if notes, err := st.SearchSymbolNotesFTS(project, query, limit); err == nil {
			for _, n := range notes {
				if !seenIDs[n.ID] {
					seenIDs[n.ID] = true
					symbolNotes = append(symbolNotes, n)
				}
			}
		}
	}

	// Prepend symbol notes to res.Hits with Tier 0 score (1500.0)
	var tier0Hits []dream.RecallHit
	for _, sn := range symbolNotes {
		location := sn.FilePath
		if sn.SpanStartLine > 0 {
			location = fmt.Sprintf("%s:%d-%d", sn.FilePath, sn.SpanStartLine, sn.SpanEndLine)
		}
		tier0Hits = append(tier0Hits, dream.RecallHit{
			Kind:       "symbol_" + sn.NoteKind,
			ID:         sn.ID,
			ProjectID:  sn.ProjectID,
			Timestamp:  sn.CreatedAt,
			Symptom:    sn.CanonicalID,
			Cause:      location,
			Resolution: sn.NoteText,
			Grounded:   true,
			Score:      1500.0,
			SourceRef:  location,
		})
	}

	res.Hits = append(tier0Hits, res.Hits...)
	res.Total = len(res.Hits)

	if jsonOut {
		emitJSON(res)
		return
	}
	if toonOut {
		rows := make([][]string, 0, len(res.Hits))
		for _, h := range res.Hits {
			rows = append(rows, []string{h.Kind, h.ProjectID, recallAge(h.Timestamp), fmt.Sprintf("%t", h.Grounded), h.Resolution})
		}
		fmt.Fprint(os.Stdout, render.FormatTOONTable("recall_hits", []string{"kind", "project", "when", "grounded", "resolution"}, rows))
		return
	}

	if len(res.Hits) == 0 {
		fmt.Fprintf(os.Stdout, "%s No prior resolutions for %q %s.\n", ui.Yellow("▲"), query, scopeLabel(project))
		fmt.Fprintln(os.Stdout, "help: try fewer or broader terms, run 'sila sync' to ingest recent sessions, or record what you find with 'sila lesson'")
		return
	}

	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · recall"))+ui.Dim(fmt.Sprintf(" · %q · %d %s · showing %d", query, res.Total, plural(int64(res.Total), "match"), len(res.Hits))))
	fmt.Fprintln(os.Stdout)
	for _, h := range res.Hits {
		icon := "🧠"
		label := "claim"
		switch h.Kind {
		case "lesson":
			icon = "💡"
			label = "lesson"
		case "case":
			icon = "🏛️"
			label = "case"
		case "transcript":
			icon = "💬"
			label = "transcript"
		case "symbol_invariant", "symbol_memo", "symbol_lesson":
			icon = "📌"
			label = "symbol " + strings.TrimPrefix(h.Kind, "symbol_")
		}
		meta := []string{label}
		if h.ProjectID != "" {
			meta = append(meta, h.ProjectID)
		}
		if h.Tool != "" {
			meta = append(meta, h.Tool)
		}
		if age := recallAge(h.Timestamp); age != "" {
			meta = append(meta, age)
		}
		if h.Grounded {
			meta = append(meta, ui.Green("grounded"))
		}
		fmt.Fprintf(os.Stdout, "  %s %s\n", icon, ui.Dim(strings.Join(meta, " · ")))
		if strings.HasPrefix(h.Kind, "symbol_") {
			if h.Symptom != "" {
				fmt.Fprintf(os.Stdout, "     %s\n", ui.Bold("symbol:    "+h.Symptom))
			}
			if h.Cause != "" {
				fmt.Fprintf(os.Stdout, "     location:  %s\n", h.Cause)
			}
			if h.Resolution != "" {
				fmt.Fprintf(os.Stdout, "     %s\n", ui.Green("note:      "+h.Resolution))
			}
		} else if h.Kind == "case" {
			if h.Symptom != "" {
				fmt.Fprintf(os.Stdout, "     %s\n", ui.Bold("intent:    "+h.Symptom))
			}
			if h.Cause != "" {
				fmt.Fprintf(os.Stdout, "     approach:  %s\n", h.Cause)
			}
			if h.Fix != "" {
				fmt.Fprintf(os.Stdout, "     %s\n", ui.Green("invariant: "+h.Fix))
			} else if h.Resolution != "" {
				fmt.Fprintf(os.Stdout, "     %s\n", h.Resolution)
			}
		} else {
			if h.Symptom != "" {
				fmt.Fprintf(os.Stdout, "     %s\n", ui.Bold("symptom: "+h.Symptom))
			}
			if h.Cause != "" {
				fmt.Fprintf(os.Stdout, "     cause:   %s\n", h.Cause)
			}
			if h.Fix != "" {
				fmt.Fprintf(os.Stdout, "     %s\n", ui.Green("fix:     "+h.Fix))
			} else {
				fmt.Fprintf(os.Stdout, "     %s\n", h.Resolution)
			}
		}
		if h.SourceRef != "" {
			fmt.Fprintf(os.Stdout, "     %s %s\n", ui.Dim("→"), ui.Cyan(h.SourceRef))
		}
		fmt.Fprintln(os.Stdout)
	}
	for _, w := range res.Warnings {
		fmt.Fprintln(os.Stdout, ui.Yellow("▲ ")+ui.Dim(w))
	}
}

// recallAge renders an RFC3339 timestamp as a compact relative age.
func recallAge(ts string) string {
	if ts == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
