package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jadmadi/sila/internal/lint"
	"github.com/jadmadi/sila/internal/render"
	"github.com/mattn/go-isatty"
)

func runUnslop(ui render.UI, jsonOut bool, checkOnly, fix, showDiff bool, args []string) {
	// Parse sub-flags from args if any
	var paths []string
	for _, arg := range args {
		switch {
		case arg == "--check":
			checkOnly = true
		case arg == "--fix":
			fix = true
		case arg == "--diff":
			showDiff = true
		case arg == "--json":
			jsonOut = true
		case !strings.HasPrefix(arg, "-"):
			paths = append(paths, arg)
		}
	}

	// 1. Stdin mode if no paths provided and stdin is piped
	if len(paths) == 0 && (!isatty.IsTerminal(os.Stdin.Fd()) && !isatty.IsCygwinTerminal(os.Stdin.Fd())) {
		inputBytes, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila unslop: read stdin: %v\n", err)
			os.Exit(1)
		}
		text := string(inputBytes)
		rep := lint.CheckText(text)
		if jsonOut {
			emitJSON(rep)
			if !rep.Clean && !fix {
				os.Exit(1)
			}
			return
		}
		if fix {
			fmt.Print(rep.CleanedText)
			return
		}
		if rep.Clean {
			fmt.Fprintln(os.Stderr, ui.Green("✔")+" clean: zero conversational slop detected")
			return
		}
		fmt.Fprintf(os.Stderr, "%s %d conversational tell(s) detected:\n", ui.Red("✖"), len(rep.Violations))
		for _, v := range rep.Violations {
			fmt.Fprintf(os.Stderr, "  • line %d: %s [%s]\n", v.LineNumber, v.Rule, v.Suggestion)
		}
		os.Exit(1)
	}

	// 2. Default to current directory if no paths specified
	if len(paths) == 0 {
		paths = []string{"."}
	}

	// Collect target markdown files
	var targetFiles []string
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		if !fi.IsDir() {
			targetFiles = append(targetFiles, p)
			continue
		}
		_ = filepath.Walk(p, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				// Skip git, vendor, node_modules, rm, .sila
				if info != nil && info.IsDir() && (info.Name() == ".git" || info.Name() == "node_modules" || info.Name() == "vendor" || info.Name() == "dist" || info.Name() == "rm" || info.Name() == ".sila") {
					return filepath.SkipDir
				}
				return nil
			}
			ext := strings.ToLower(filepath.Ext(path))
			if ext == ".md" || ext == ".markdown" || ext == ".txt" {
				targetFiles = append(targetFiles, path)
			}
			return nil
		})
	}

	totalViolations := 0
	filesWithViolations := 0
	type fileReport struct {
		Path       string                 `json:"path"`
		Clean      bool                   `json:"clean"`
		Violations []lint.UnslopViolation `json:"violations,omitempty"`
	}
	var reports []fileReport

	for _, f := range targetFiles {
		rep, err := lint.CleanFile(f, fix)
		if err != nil {
			continue
		}
		if !rep.Clean {
			totalViolations += len(rep.Violations)
			filesWithViolations++
			reports = append(reports, fileReport{
				Path:       f,
				Clean:      false,
				Violations: rep.Violations,
			})
			if !jsonOut {
				action := "found"
				if fix {
					action = "fixed"
				}
				fmt.Fprintf(os.Stdout, "  %s %s: %s %d violation(s)\n", ui.Yellow("▲"), ui.Bold(f), action, len(rep.Violations))
				if showDiff && !fix {
					for _, v := range rep.Violations {
						fmt.Fprintf(os.Stdout, "    line %d: %s -> %s\n", v.LineNumber, ui.Red(v.Rule), ui.Green(v.Suggestion))
					}
				}
			}
		}
	}

	if jsonOut {
		emitJSON(map[string]any{
			"total_scanned": len(targetFiles),
			"files_flagged": filesWithViolations,
			"violations":    totalViolations,
			"fixed":         fix,
			"reports":       reports,
		})
		if filesWithViolations > 0 && !fix {
			os.Exit(1)
		}
		return
	}

	if filesWithViolations == 0 {
		fmt.Fprintf(os.Stdout, "%s all %d scanned file(s) clean of conversational slop.\n", ui.Green("✔"), len(targetFiles))
		return
	}

	if fix {
		fmt.Fprintf(os.Stdout, "\n%s successfully scrubbed %d violation(s) across %d file(s).\n", ui.Green("✔"), totalViolations, filesWithViolations)
		return
	}

	fmt.Fprintf(os.Stderr, "\n%s %d conversational tell(s) detected across %d file(s). Run with --fix to clean.\n",
		ui.Red("✖"), totalViolations, filesWithViolations)
	os.Exit(1)
}
