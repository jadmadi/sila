package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/jadmadi/sila/internal/clean"
	"github.com/jadmadi/sila/internal/render"
)

// cleanRunOptions bundles CLI flags for `sila clean`.
type cleanRunOptions struct {
	repoClean   bool
	globalClean bool
	allClean    bool
	heavy       bool
	dryRun      bool
	yes         bool
	force       bool
	jsonOut     bool
	toonOut     bool
}

func runClean(ui render.UI, cwd string, targets []string, opts cleanRunOptions) {
	ctx := context.Background()
	reg := clean.DefaultRegistry()

	// 1. Determine Scope
	scope := clean.ScopeAll
	if opts.repoClean && !opts.globalClean && !opts.allClean {
		scope = clean.ScopeRepo
	} else if opts.globalClean && !opts.repoClean && !opts.allClean {
		scope = clean.ScopeGlobal
	}

	// 2. Run Inspection
	reports, err := reg.InspectAll(ctx, cwd, targets, scope, opts.heavy)
	if err != nil {
		if opts.jsonOut {
			emitJSON(map[string]any{"error": err.Error()})
		} else {
			fmt.Fprintln(os.Stderr, ui.Red("sila clean: "+err.Error()))
		}
		os.Exit(1)
	}

	totalItems := 0
	var totalReclaimable int64
	for _, rep := range reports {
		totalItems += len(rep.Items)
		totalReclaimable += rep.TotalBytes
	}

	if totalItems == 0 {
		if opts.jsonOut {
			emitJSON(map[string]any{
				"status":            "clean",
				"total_items":       0,
				"reclaimable_bytes": 0,
				"reports":           reports,
			})
		} else {
			fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · clean")))
			fmt.Fprintln(os.Stdout, ui.Green("✔ All toolchains and repositories are clean. Zero reclaimable cache bloat detected."))
		}
		return
	}

	// 3. Determine if execution or preview
	dryRun := opts.dryRun
	shouldExecute := opts.yes || opts.force

	if !shouldExecute && !dryRun {
		// If interactive terminal, display preview first, then ask
		if ui.IsTerminal() {
			renderCleanPreview(ui, reports, totalReclaimable, false)
			fmt.Fprintln(os.Stdout)
			fmt.Fprint(os.Stdout, ui.Bold("Clean the items listed above? [y/N]: "))
			reader := bufio.NewReader(os.Stdin)
			ans, _ := reader.ReadString('\n')
			ans = strings.TrimSpace(strings.ToLower(ans))
			if ans == "y" || ans == "yes" {
				shouldExecute = true
			} else {
				fmt.Fprintln(os.Stdout, ui.Dim("Cleanup aborted. No files were removed."))
				return
			}
		} else {
			// Non-interactive shell without --yes/--force: fail-safe to dry-run preview
			dryRun = true
		}
	}

	// If dry run requested or determined
	if dryRun || !shouldExecute {
		if opts.jsonOut {
			emitJSON(map[string]any{
				"dry_run":           true,
				"total_items":       totalItems,
				"reclaimable_bytes": totalReclaimable,
				"reports":           reports,
			})
			return
		}
		if opts.toonOut {
			fmt.Fprint(os.Stdout, renderCleanReportsTOON(reports, totalReclaimable, true))
			return
		}
		renderCleanPreview(ui, reports, totalReclaimable, true)
		return
	}

	// 4. Execute Cleanup
	results, err := reg.CleanAll(ctx, cwd, targets, scope, opts.heavy, false)
	if err != nil {
		if opts.jsonOut {
			emitJSON(map[string]any{"error": err.Error()})
		} else {
			fmt.Fprintln(os.Stderr, ui.Red("sila clean error: "+err.Error()))
		}
		os.Exit(1)
	}

	var totalCleaned int64
	for _, res := range results {
		totalCleaned += res.TotalReclaimed
	}

	if opts.jsonOut {
		emitJSON(map[string]any{
			"dry_run":         false,
			"total_reclaimed": totalCleaned,
			"results":         results,
		})
		return
	}
	if opts.toonOut {
		fmt.Fprint(os.Stdout, renderCleanResultsTOON(results, totalCleaned))
		return
	}

	renderCleanExecution(ui, results, totalCleaned)
}

func renderCleanPreview(ui render.UI, reports []*clean.Report, totalBytes int64, isDryRun bool) {
	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · clean"))+" (preview)")
	fmt.Fprintln(os.Stdout)

	headers := []string{"toolchain", "item", "category", "reclaimable", "target path"}
	aligns := []string{"l", "l", "l", "r", "l"}
	var rows [][]string

	for _, rep := range reports {
		for _, item := range rep.Items {
			cat := "Repo Residue"
			if item.Category == clean.CategoryGlobalCache {
				cat = "Global Cache"
			} else if item.Category == clean.CategoryGlobalStore {
				cat = "Global Store"
			}
			rows = append(rows, []string{
				rep.DisplayName,
				item.Name,
				cat,
				render.FormatBytes(item.Bytes),
				shortenHome(item.Path),
			})
		}
	}

	fmt.Fprintln(os.Stdout, ui.Table(headers, rows, aligns))
	fmt.Fprintf(os.Stdout, "  %s %s across %d toolchain(s)\n",
		ui.Bold("Total reclaimable:"),
		ui.Bold(ui.Green(render.FormatBytes(totalBytes))),
		len(reports))

	if isDryRun {
		fmt.Fprintln(os.Stdout, ui.Dim("  Run with `sila clean --yes` or `sila clean -y` to execute cleanup."))
	}
}

func renderCleanExecution(ui render.UI, results []*clean.Result, totalBytes int64) {
	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · clean")))
	fmt.Fprintln(os.Stdout)

	headers := []string{"toolchain", "item", "reclaimed", "status"}
	aligns := []string{"l", "l", "r", "l"}
	var rows [][]string

	var lockedItems []string
	hasStoreCleanWithoutOrphans := false

	for _, res := range results {
		for _, ir := range res.Results {
			var status string
			if ir.Locked {
				status = ui.Yellow("🔒 locked (in use)")
				lockedName := res.DisplayName
				if ir.Item.Name != "" {
					lockedName += " (" + ir.Item.Name + ")"
				}
				lockedItems = append(lockedItems, lockedName)
			} else if !ir.Success {
				status = ui.Red("✖ " + ir.Error)
			} else if ir.ReclaimedBytes > 0 {
				status = ui.Green("✔ cleaned")
			} else {
				if ir.Item.Category == clean.CategoryGlobalStore {
					status = ui.Dim("• 0B (all in use)")
					hasStoreCleanWithoutOrphans = true
				} else {
					status = ui.Dim("• 0B (clean)")
				}
			}
			rows = append(rows, []string{
				res.DisplayName,
				ir.Item.Name,
				render.FormatBytes(ir.ReclaimedBytes),
				status,
			})
		}
	}

	fmt.Fprintln(os.Stdout, ui.Table(headers, rows, aligns))
	fmt.Fprintf(os.Stdout, "  %s %s reclaimed across %d toolchain(s)\n",
		ui.Bold("Total disk space freed:"),
		ui.Bold(ui.Green(render.FormatBytes(totalBytes))),
		len(results))

	if len(lockedItems) > 0 {
		fmt.Fprintln(os.Stdout)
		fmt.Fprintf(os.Stdout, "  %s %s is locked now which means someone or another task is working and using it.\n",
			ui.Yellow("🔒 Lock Notice:"), strings.Join(lockedItems, ", "))
		fmt.Fprintln(os.Stdout, ui.Dim("    Sila respects active locks and does not override them. When the process is over, you can clean it."))
	}

	if hasStoreCleanWithoutOrphans {
		if len(lockedItems) == 0 {
			fmt.Fprintln(os.Stdout)
		}
		fmt.Fprintln(os.Stdout, ui.Dim("  • Package stores only prune unreferenced packages; active dependencies currently used by local projects are preserved."))
	}
}

func renderCleanReportsTOON(reports []*clean.Report, totalBytes int64, dryRun bool) string {
	var b strings.Builder
	summaryFields := [][2]string{
		{"total_reclaimable_bytes", fmt.Sprintf("%d", totalBytes)},
		{"formatted_total", render.FormatBytes(totalBytes)},
		{"dry_run", fmt.Sprintf("%t", dryRun)},
		{"toolchains_count", fmt.Sprintf("%d", len(reports))},
	}
	b.WriteString(render.FormatTOONObject("clean_preview", summaryFields))

	headers := []string{"toolchain", "name", "bytes", "path"}
	var rows [][]string
	for _, rep := range reports {
		for _, it := range rep.Items {
			rows = append(rows, []string{rep.Toolchain, it.Name, fmt.Sprintf("%d", it.Bytes), it.Path})
		}
	}
	b.WriteString(render.FormatTOONTable("cleanable_items", headers, rows))
	return b.String()
}

func renderCleanResultsTOON(results []*clean.Result, totalBytes int64) string {
	var b strings.Builder
	summaryFields := [][2]string{
		{"total_reclaimed_bytes", fmt.Sprintf("%d", totalBytes)},
		{"formatted_total", render.FormatBytes(totalBytes)},
		{"toolchains_count", fmt.Sprintf("%d", len(results))},
	}
	b.WriteString(render.FormatTOONObject("clean_result", summaryFields))

	headers := []string{"toolchain", "name", "reclaimed_bytes", "success", "locked"}
	var rows [][]string
	for _, res := range results {
		for _, ir := range res.Results {
			rows = append(rows, []string{
				res.Toolchain,
				ir.Item.Name,
				fmt.Sprintf("%d", ir.ReclaimedBytes),
				fmt.Sprintf("%t", ir.Success),
				fmt.Sprintf("%t", ir.Locked),
			})
		}
	}
	b.WriteString(render.FormatTOONTable("cleaned_items", headers, rows))
	return b.String()
}
