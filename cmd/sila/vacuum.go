package main

import (
	"fmt"
	"os"
	"time"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

type jsonVacuum struct {
	Store          string  `json:"store"`
	BeforeBytes    int64   `json:"before_bytes"`
	AfterBytes     int64   `json:"after_bytes"`
	ReclaimedBytes int64   `json:"reclaimed_bytes"`
	ReclaimedPct   float64 `json:"reclaimed_pct"`
	PagesBefore    int64   `json:"pages_before"`
	PagesAfter     int64   `json:"pages_after"`
	FreeBefore     int64   `json:"free_before"`
	FreeAfter      int64   `json:"free_after"`
	DurationMS     int64   `json:"duration_ms"`
	Executed       bool    `json:"executed"`
}

// runVacuum defragments the knowledge database, reclaims unused freelist pages,
// flushes WAL logs, and optimizes query planner statistics.
// runBackup writes a consistent snapshot of the knowledge store via
// VACUUM INTO. Unlike copying sila.db alone, the snapshot includes every
// committed transaction even while writers are active.

func runVacuum(ui render.UI, dbPath string, statsOnly, force, jsonOut bool) {
	if _, err := os.Stat(dbPath); err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": "knowledge store not found", "store": dbPath})
		} else {
			fmt.Fprintln(os.Stdout, ui.Dim("No knowledge store found at "+dbPath))
		}
		return
	}

	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	stats, err := st.Stats()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}

	lastVac, _ := st.LastVacuumTime()
	lastVacStr := "never"
	if !lastVac.IsZero() {
		lastVacStr = fmt.Sprintf("%s (%s)", lastVac.Format("2006-01-02 15:04:05 UTC"), fmtAge(lastVac))
	}

	if statsOnly {
		if jsonOut {
			emitJSON(map[string]any{
				"path":              stats.Path,
				"file_size":         stats.FileSize,
				"wal_size":          stats.WALSize,
				"page_count":        stats.PageCount,
				"page_size":         stats.PageSize,
				"freelist_count":    stats.FreelistCount,
				"free_bytes":        stats.FreeBytes,
				"fragmentation_pct": stats.FragmentationPct,
				"last_vacuum":       lastVacStr,
			})
			return
		}

		fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · vacuum (analyze)")))
		fmt.Fprintf(os.Stdout, "  Store: %s (%s · WAL: %s)\n", stats.Path, render.FormatBytes(stats.FileSize), render.FormatBytes(stats.WALSize))
		fmt.Fprintf(os.Stdout, "  Pages: %s total · %s free (%s reclaimable · %.1f%% fragmentation)\n",
			render.FormatNumber(stats.PageCount), render.FormatNumber(stats.FreelistCount),
			render.FormatBytes(stats.FreeBytes), stats.FragmentationPct)
		fmt.Fprintf(os.Stdout, "  Last vacuum: %s\n\n", lastVacStr)

		if stats.FreelistCount > 0 {
			fmt.Fprintln(os.Stdout, actionLine(ui, "sila vacuum", fmt.Sprintf("run defragmentation and reclaim %s", render.FormatBytes(stats.FreeBytes))))
		} else {
			fmt.Fprintln(os.Stdout, ui.Green("Database is compact — 0 free pages."))
		}
		return
	}

	if !force && stats.FreelistCount == 0 && stats.FragmentationPct == 0 {
		if jsonOut {
			emitJSON(jsonVacuum{
				Store:          stats.Path,
				BeforeBytes:    stats.FileSize + stats.WALSize,
				AfterBytes:     stats.FileSize + stats.WALSize,
				ReclaimedBytes: 0,
				ReclaimedPct:   0,
				PagesBefore:    stats.PageCount,
				PagesAfter:     stats.PageCount,
				FreeBefore:     0,
				FreeAfter:      0,
				DurationMS:     0,
				Executed:       false,
			})
			return
		}
		fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · vacuum")))
		fmt.Fprintf(os.Stdout, "  Store: %s (%s)\n", stats.Path, render.FormatBytes(stats.FileSize))
		fmt.Fprintln(os.Stdout, ui.Green("Database is already compact (0 free pages · 0.0% fragmentation)."))
		fmt.Fprintln(os.Stdout, ui.Dim("Use `sila vacuum --force` to force a complete repack."))
		return
	}

	res, err := st.Vacuum()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(jsonVacuum{
			Store:          res.After.Path,
			BeforeBytes:    res.Before.FileSize + res.Before.WALSize,
			AfterBytes:     res.After.FileSize + res.After.WALSize,
			ReclaimedBytes: res.ReclaimedBytes,
			ReclaimedPct:   res.ReclaimedPct,
			PagesBefore:    res.Before.PageCount,
			PagesAfter:     res.After.PageCount,
			FreeBefore:     res.Before.FreelistCount,
			FreeAfter:      res.After.FreelistCount,
			DurationMS:     res.DurationMS,
			Executed:       true,
		})
		return
	}

	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · vacuum")))
	fmt.Fprintf(os.Stdout, "  Store: %s\n", res.After.Path)
	fmt.Fprintf(os.Stdout, "  Before:    %s (%s pages · %s free)\n",
		render.FormatBytes(res.Before.FileSize+res.Before.WALSize),
		render.FormatNumber(res.Before.PageCount),
		render.FormatNumber(res.Before.FreelistCount))
	fmt.Fprintf(os.Stdout, "  After:     %s (%s pages · %s free)\n",
		render.FormatBytes(res.After.FileSize+res.After.WALSize),
		render.FormatNumber(res.After.PageCount),
		render.FormatNumber(res.After.FreelistCount))
	fmt.Fprintf(os.Stdout, "  Reclaimed: %s (%.1f%% reduction) in %s\n",
		ui.Bold(ui.Green(render.FormatBytes(res.ReclaimedBytes))),
		res.ReclaimedPct,
		res.Duration.Round(time.Millisecond))
	fmt.Fprintln(os.Stdout, ui.Green("✔ Storage defragmented and query planner statistics optimized."))
}

// introMarkdown builds the self-contained agent guide.
