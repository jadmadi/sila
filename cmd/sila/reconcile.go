package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jadmadi/sila/internal/assist"
	"github.com/jadmadi/sila/internal/dream"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func runReconcile(ui render.UI, st *store.Store, project string, dryRun, semantic, jsonOut, verbose bool) {
	if semantic {
		provider := assist.ActiveProviderFor("verify", project)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		semRes, err := dream.ReconcileSemanticDrift(ctx, st, provider, project, dryRun)
		if err == nil {
			if jsonOut {
				emitJSON(semRes)
				return
			}
			title := ui.Cyan(ui.Bold("sila · reconcile (semantic)"))
			if dryRun {
				title += " " + ui.Yellow("[PREVIEW / DRY-RUN]")
			}
			fmt.Fprintln(os.Stdout, title)
			fmt.Fprintf(os.Stdout, "  Checked %d claims: %s physical re-verified, %s newly stale, %s recovered.\n",
				semRes.TotalChecked,
				ui.Green(fmt.Sprint(semRes.PhysicalReVerified)),
				ui.Red(fmt.Sprint(semRes.NewlyStale)),
				ui.Yellow(fmt.Sprint(semRes.Recovered)))
			fmt.Fprintf(os.Stdout, "  System One semantic audit (%dms): %s substantiated, %s drifted.\n",
				semRes.DurationMs,
				ui.Green(fmt.Sprint(semRes.SemanticVerified)),
				ui.Yellow(fmt.Sprint(semRes.SemanticDrift)))
			if len(semRes.DriftWarnings) > 0 {
				fmt.Fprintln(os.Stdout, ui.Yellow("\nSemantic Drift Warnings:"))
				for _, w := range semRes.DriftWarnings {
					fmt.Fprintf(os.Stdout, "  ▲ %s\n", w)
				}
			}
			if dryRun {
				fmt.Fprintln(os.Stdout, ui.Dim("\n  (Dry-run: no database changes were written)."))
			}
			return
		}
	}
	projects, err := st.Projects()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}
	var totalRes store.ReconcileResult
	for _, p := range projects {
		if project != "" && !projSelMatch(p, project) {
			continue
		}
		var res store.ReconcileResult
		var err error
		if dryRun {
			res, err = st.ReconcileFactDriftPreview(p.ID, p.Path)
		} else {
			res, err = st.ReconcileFactDrift(p.ID, p.Path)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila reconcile %s: %v\n", p.ID, err)
			continue
		}
		totalRes.TotalChecked += res.TotalChecked
		totalRes.ReVerified += res.ReVerified
		totalRes.NewlyStale += res.NewlyStale
		totalRes.Recovered += res.Recovered
		totalRes.AssertionsChecked += res.AssertionsChecked
		totalRes.AssertionsVerified += res.AssertionsVerified
	}
	if jsonOut {
		if dryRun {
			emitJSON(map[string]any{
				"dry_run":             true,
				"total_checked":       totalRes.TotalChecked,
				"re_verified":         totalRes.ReVerified,
				"newly_stale":         totalRes.NewlyStale,
				"recovered":           totalRes.Recovered,
				"assertions_checked":  totalRes.AssertionsChecked,
				"assertions_verified": totalRes.AssertionsVerified,
			})
		} else {
			emitJSON(totalRes)
		}
		return
	}
	title := ui.Cyan(ui.Bold("sila · reconcile"))
	if dryRun {
		title += " " + ui.Yellow("[PREVIEW / DRY-RUN]")
	}
	fmt.Fprintln(os.Stdout, title)
	fmt.Fprintf(os.Stdout, "  Checked %d claims: %s re-verified, %s newly stale, %s recovered.\n",
		totalRes.TotalChecked,
		ui.Green(fmt.Sprint(totalRes.ReVerified)),
		ui.Red(fmt.Sprint(totalRes.NewlyStale)),
		ui.Yellow(fmt.Sprint(totalRes.Recovered)))
	fmt.Fprintf(os.Stdout, "  Checked %d assertion references: %d sources available.\n",
		totalRes.AssertionsChecked, totalRes.AssertionsVerified)
	if dryRun {
		fmt.Fprintln(os.Stdout, ui.Dim("  (Dry-run: no database changes were written)."))
	}
}

func runLLM(ui render.UI, st *store.Store, llmProvider, project string, retryFailed bool, jsonOut, verbose bool) {
	var llm dream.LLM
	switch llmProvider {
	case "stdio":
		llm = dream.NewStdioLLM(os.Stdin, os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "sila: unsupported llm provider %q (supported: stdio)\n", llmProvider)
		os.Exit(1)
	}
	stats, err := dream.RunLLMDistillation(st, llm, project, retryFailed, 50)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}
	if jsonOut {
		emitJSON(stats)
		return
	}
	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · distill (LLM)")))
	fmt.Fprintf(os.Stdout, "  Processed %d claims: %s distilled, %s skipped, %s failed.\n",
		stats.Total,
		ui.Green(fmt.Sprint(stats.Distilled)),
		ui.Dim(fmt.Sprint(stats.Skipped)),
		ui.Red(fmt.Sprint(stats.Failed)))
}
