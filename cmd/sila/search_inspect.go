package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/jadmadi/sila/internal/dream"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

var errInspectTargetNotFound = errors.New("target not found")

// searchInspectTrace runs the same recall query with tracing enabled.
func searchInspectTrace(st *store.Store, project, query string, showMisses bool, target string) ([]dream.RecallTraceEntry, error) {
	trace := &dream.RecallTrace{}
	if _, err := dream.RecallSolutions(st, query, dream.RecallOptions{
		Project: project, Limit: 20, Trace: trace,
	}); err != nil {
		return nil, err
	}
	entries := trace.Entries
	if target != "" {
		for _, entry := range entries {
			if entry.ID == target {
				return []dream.RecallTraceEntry{entry}, nil
			}
		}
		return nil, errInspectTargetNotFound
	}
	if showMisses {
		return entries, nil
	}
	kept := entries[:0]
	for _, entry := range entries {
		if entry.Decision == "kept" {
			kept = append(kept, entry)
		}
	}
	return kept, nil
}

func runSearchInspect(ui render.UI, dbPath, project string, jsonOut bool, args []string) {
	target := ""
	showMisses := false
	var query []string
	for _, arg := range args {
		switch {
		case strings.HasPrefix(arg, "--target="):
			target = strings.TrimSpace(strings.TrimPrefix(arg, "--target="))
		case arg == "--show-misses":
			showMisses = true
		case arg == "--json":
			jsonOut = true
		default:
			query = append(query, arg)
		}
	}
	if len(query) == 0 {
		fmt.Fprintln(os.Stderr, "sila search inspect: usage: sila search inspect \"<query>\" [--target=<id>] [--show-misses] [--json]")
		os.Exit(2)
	}
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila search inspect: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()
	entries, err := searchInspectTrace(st, project, strings.Join(query, " "), showMisses, target)
	if errors.Is(err, errInspectTargetNotFound) {
		fmt.Fprintln(os.Stderr, "target not found")
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila search inspect: %v\n", err)
		os.Exit(1)
	}
	if jsonOut {
		emitJSON(entries)
		return
	}
	if len(entries) == 0 {
		fmt.Fprintln(os.Stdout, "no candidates")
		return
	}
	rows := make([][]string, 0, len(entries))
	for _, entry := range entries {
		rows = append(rows, []string{
			entry.Stage, entry.ID, fmt.Sprintf("%.1f", entry.Base), fmt.Sprintf("%d", entry.Match),
			fmt.Sprintf("%.1f", entry.ProjectBoost), fmt.Sprintf("%.2f", entry.FinalScore),
			entry.Decision, entry.Reason,
		})
	}
	fmt.Fprint(os.Stdout, ui.Table(
		[]string{"stage", "id", "base", "match", "boost", "final", "decision", "reason"},
		rows, []string{"left", "left", "right", "right", "right", "right", "left", "left"}))
}
