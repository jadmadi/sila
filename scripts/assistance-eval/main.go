package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/assist"
	"github.com/jadmadi/sila/internal/config"
	"github.com/jadmadi/sila/internal/render"
)

func main() {
	var (
		providerName = flag.String("provider", "mock", "Model provider: 'mock' (offline) or 'typesafe' (hosted)")
		modelName    = flag.String("model", "jev-latest", "Model identifier to evaluate")
		fixturesDir  = flag.String("fixtures", "internal/assist/testdata", "Path to evaluation fixtures directory")
		jsonOut      = flag.Bool("json", false, "Emit benchmark report as JSON")
		spendCap     = flag.Float64("spend-cap", 5.0, "Hard spending limit in USD for hosted runs")
		strict       = flag.Bool("strict", false, "Exit with code 1 if any failures or errors occur")
	)
	flag.Float64Var(spendCap, "spend-limit", 5.0, "Hard spending limit in USD for hosted runs (alias for -spend-cap)")
	flag.Parse()

	ui := render.New(os.Stdout, false)

	var provider assist.Provider
	switch *providerName {
	case "mock":
		provider = assist.NewMockAdapter()
	case "typesafe":
		cfg, _ := config.Load("")
		apiKey := cfg.TypeSafeAPIKey
		if apiKey == "" {
			apiKey = os.Getenv("TYPESAFE_API_KEY")
		}
		if apiKey == "" {
			fmt.Fprintf(os.Stderr, "Error: typesafe provider requested but neither TYPESAFE_API_KEY nor sila config typesafe_api_key is set.\n")
			os.Exit(1)
		}
		adapter := assist.NewTypeSafeAdapter(apiKey, nil)
		adapter.SpendLimit = *spendCap
		provider = adapter
	default:
		fmt.Fprintf(os.Stderr, "Error: unsupported provider '%s' (supported: mock, typesafe)\n", *providerName)
		os.Exit(1)
	}

	runner := assist.NewBenchmarkRunner(provider)
	if err := runner.LoadFixtures(*fixturesDir); err != nil {
		fmt.Fprintf(os.Stderr, "Error loading fixtures: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	start := time.Now()
	metrics, err := runner.RunBenchmark(ctx, *modelName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Benchmark failed: %v\n", err)
		os.Exit(1)
	}
	totalDuration := time.Since(start)

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(map[string]any{
			"provider":       *providerName,
			"model":          *modelName,
			"fixtures_dir":   *fixturesDir,
			"metrics":        metrics,
			"total_duration": totalDuration.String(),
		})
		if *strict && metrics.HasErrors {
			os.Exit(1)
		}
		return
	}

	fmt.Println()
	fmt.Println(ui.Bold("═══════════════════════════════════════════════════════════════════"))
	fmt.Printf("  %s — Optional Semantic Assistance Benchmark\n", ui.Cyan("sila"))
	fmt.Println(ui.Bold("═══════════════════════════════════════════════════════════════════"))
	fmt.Printf("  Provider:       %s\n", ui.Bold(*providerName))
	fmt.Printf("  Model:          %s\n", *modelName)
	fmt.Printf("  Test Fixtures:  %d cases (%s)\n", metrics.TotalCases, *fixturesDir)
	if len(metrics.FixtureHash) >= 16 {
		fmt.Printf("  Fixture Hash:   %s\n", metrics.FixtureHash[:16]+"...")
	}
	if len(metrics.QuestionsHash) >= 16 {
		fmt.Printf("  Questions Hash: %s\n", metrics.QuestionsHash[:16]+"...")
	}
	fmt.Println("───────────────────────────────────────────────────────────────────")
	fmt.Printf("  Assistance Acc: %s (%.1f%%)\n", ui.Green(fmt.Sprintf("%d/%d", metrics.CorrectJudgments, metrics.TotalCases)), metrics.Accuracy*100)
	if metrics.HeldOutCases > 0 {
		fmt.Printf("  Held-Out Acc:   %s (%.1f%%) [%d cases]\n", ui.Cyan(fmt.Sprintf("%d/%d", metrics.HeldOutCorrect, metrics.HeldOutCases)), metrics.HeldOutAccuracy*100, metrics.HeldOutCases)
	}
	fmt.Printf("  Local Baseline: %.1f%% (deterministic rules; p50: %d ms, p95: %d ms)\n", metrics.LocalAccuracy*100, metrics.LocalP50LatencyMs, metrics.LocalP95LatencyMs)
	fmt.Printf("  Latency (p50):  %d ms (sorted percentiles)\n", metrics.P50LatencyMs)
	fmt.Printf("  Latency (p95):  %d ms (sorted percentiles)\n", metrics.P95LatencyMs)
	fmt.Printf("  Token Usage:    %d tokens\n", metrics.TotalTokens)
	fmt.Printf("  Est. Spend:     $%.6f (cap $%.2f)\n", metrics.EstimatedSpendUSD, *spendCap)
	if metrics.Failures > 0 {
		fmt.Printf("  Failures:       %s\n", ui.Red(fmt.Sprintf("%d", metrics.Failures)))
	} else {
		fmt.Printf("  Failures:       0\n")
	}

	if len(metrics.PerTaskMetrics) > 0 {
		fmt.Println("───────────────────────────────────────────────────────────────────")
		fmt.Println("  Per-Task Accuracy & Performance Matrix:")
		tasks := make([]string, 0, len(metrics.PerTaskMetrics))
		for t := range metrics.PerTaskMetrics {
			tasks = append(tasks, t)
		}
		sort.Strings(tasks)
		for _, task := range tasks {
			tm := metrics.PerTaskMetrics[task]
			fmt.Printf("    • %-22s Total:%-3d Prec:%.2f Rec:%.2f F1:%.2f Acc:%.1f%%\n",
				task, tm.Total, tm.Precision, tm.Recall, tm.F1, tm.Accuracy*100)
		}
	}

	if len(metrics.Cases) > 0 {
		fmt.Println("───────────────────────────────────────────────────────────────────")
		fmt.Println("  Fixture Breakdown (Sample):")
		limit := min(len(metrics.Cases), 10)
		for i := 0; i < limit; i++ {
			c := metrics.Cases[i]
			mark := ui.Green("✔")
			if !c.Matched {
				mark = ui.Red("✘")
			}
			var scoresStr strings.Builder
			for k, sc := range c.Scores {
				scoresStr.WriteString(fmt.Sprintf(" %s:%.2f", k, sc))
			}
			heldOutTag := ""
			if c.IsHeldOut {
				heldOutTag = " [held-out]"
			}
			fmt.Printf("    %s %-32s [%s]%s%s\n", mark, c.ID, c.Language, heldOutTag, scoresStr.String())
		}
		if len(metrics.Cases) > limit {
			fmt.Printf("    ... and %d more cases verified.\n", len(metrics.Cases)-limit)
		}
	}

	fmt.Println(ui.Bold("═══════════════════════════════════════════════════════════════════"))
	if metrics.HasErrors {
		fmt.Printf("  %s Benchmark completed with %d failures.\n", ui.Red("✘ FAILED:"), metrics.Failures)
		fmt.Println(ui.Bold("═══════════════════════════════════════════════════════════════════"))
		fmt.Println()
		if *strict {
			os.Exit(1)
		}
	} else {
		fmt.Printf("  %s All %d evaluation fixtures evaluated successfully.\n", ui.Green("✔ PASS:"), metrics.TotalCases)
		fmt.Println(ui.Bold("═══════════════════════════════════════════════════════════════════"))
		fmt.Println()
	}
}
