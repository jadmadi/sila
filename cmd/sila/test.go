package main

import (
	"context"
	"fmt"
	"os"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/testmatrix"
)

// runTest routes test commands, primarily `sila test selector`.
func runTest(ui render.UI, jsonOut, toonOut, runTests bool, args []string) {
	if len(args) == 0 || args[0] != "selector" {
		fmt.Fprintln(os.Stderr, "Usage: sila test selector [--run] [--json]")
		os.Exit(1)
	}

	cwd, _ := os.Getwd()
	res, err := testmatrix.SelectTargetedTests(cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: test selector: %v\n", err)
		os.Exit(2)
	}

	if runTests && res.Command != "" {
		out, exitCode, _ := testmatrix.RunTargeted(context.Background(), cwd, res.Command)
		res.ExitCode = exitCode
		if jsonOut || toonOut {
			emitJSON(res)
		} else {
			printTestSelectorReport(ui, res)
			fmt.Println("\n--- Test Output ---")
			fmt.Print(out)
		}
		if exitCode != 0 {
			os.Exit(exitCode)
		}
		return
	}

	if jsonOut || toonOut {
		emitJSON(res)
	} else {
		printTestSelectorReport(ui, res)
	}
}

func printTestSelectorReport(ui render.UI, res *testmatrix.SelectorResult) {
	icon := ui.Green("✔")
	if len(res.UntestedSymbols) > 0 {
		icon = ui.Yellow("▲")
	}

	fmt.Fprintf(os.Stdout, "%s Test Blast-Radius Selector (Status: %s)\n", icon, res.Status)

	if len(res.ChangedSymbols) > 0 {
		fmt.Fprintf(os.Stdout, "\nChanged Symbols (%d):\n", len(res.ChangedSymbols))
		for _, s := range res.ChangedSymbols {
			fmt.Fprintf(os.Stdout, "  • %s\n", s)
		}
	} else {
		fmt.Fprintf(os.Stdout, "  No changed symbols detected in working tree.\n")
	}

	if len(res.TestsToRun) > 0 {
		fmt.Fprintf(os.Stdout, "\nTargeted Tests to Run (%d):\n", len(res.TestsToRun))
		for _, t := range res.TestsToRun {
			fmt.Fprintf(os.Stdout, "  %s %s (%s in %s)\n", ui.Green("▶"), t.TestFunction, t.TestFile, t.Package)
		}
	} else {
		fmt.Fprintf(os.Stdout, "\nNo targeted tests mapped to changed symbols.\n")
	}

	if len(res.UntestedSymbols) > 0 {
		fmt.Fprintf(os.Stdout, "\n%s Untested Symbols (%d):\n", ui.Yellow("▲"), len(res.UntestedSymbols))
		for _, s := range res.UntestedSymbols {
			fmt.Fprintf(os.Stdout, "  %s %s\n", ui.Yellow("!"), s)
		}
	}

	if res.Command != "" {
		fmt.Fprintf(os.Stdout, "\nCommand:\n  %s\n", res.Command)
	}
}
