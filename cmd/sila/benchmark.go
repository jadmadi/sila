package main

import (
	"fmt"
	"os"

	"github.com/jadmadi/sila/internal/assess"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

// runBenchmark implements `sila benchmark [--project=<id>] [--min-sessions=<n>] [--toon] [--json]`.
// It computes local, empirical quality metrics across all installed AI harnesses,
// grounded in SQLite rows and physical disk verification.
func runBenchmark(ui render.UI, dbPath, project string, minSessions int, jsonOut, toonOut bool) {
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

	sc, err := assess.ComputeBenchmark(st, project, cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila benchmark: %v\n", err)
		os.Exit(1)
	}

	// Filter by minimum sessions (projects) if requested.
	if minSessions > 0 {
		var filtered []assess.ToolScore
		for _, t := range sc.Tools {
			if t.Projects >= minSessions {
				filtered = append(filtered, t)
			}
		}
		sc.Tools = filtered
		sc.TotalTools = len(filtered)
	}

	if jsonOut {
		emitJSON(sc)
		return
	}

	if toonOut {
		fmt.Fprint(os.Stdout, assess.RenderScorecardTOON(sc))
		return
	}

	fmt.Fprint(os.Stdout, assess.RenderScorecardTable(sc))

	// Legend footer.
	if sc.TotalTools > 0 {
		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout, ui.Dim("Health: 🟢 >95% grounded · 🟡 85-95% · 🔴 <85% · ⚪ no claims"))
		fmt.Fprintln(os.Stdout, ui.Dim("Metrics derived strictly from SQLite rows + disk verification. No synthetic ratings."))
	} else {
		fmt.Fprintln(os.Stdout, ui.Dim("\n_No tools with recorded activity. Run `sila sync` to ingest agent transcripts._"))
	}
}
