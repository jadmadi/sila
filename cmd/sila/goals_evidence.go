package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jadmadi/sila/internal/goals"
)

func (gc *goalsContext) handleEvidence(targetDir string, args []string) {
	jsonOut := gc.jsonOut
	runChecks := false
	importFile := ""
	var positional []string

	for _, a := range append(args, os.Args...) {
		if a == "--json" || a == "-json" {
			jsonOut = true
		} else if a == "--run" || a == "-run" {
			runChecks = true
		} else if after, ok := strings.CutPrefix(a, "--import="); ok {
			importFile = after
		} else if after, ok := strings.CutPrefix(a, "-import="); ok {
			importFile = after
		}
	}

	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			positional = append(positional, a)
		}
	}

	if len(positional) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: sila goals evidence <slug> <task-id> [--run] [--import=<file>] [--json]")
		os.Exit(2)
	}

	slug := positional[0]
	taskID := positional[1]

	if targetDir == "" {
		targetDir = goals.ResolveGoalsRootDir(".")
	}

	// 1. Handle Import Receipt
	if importFile != "" {
		cleanImport := filepath.Clean(importFile)
		data, err := os.ReadFile(cleanImport)
		if err != nil {
			if jsonOut {
				emitJSON(map[string]any{"error": fmt.Sprintf("read import file: %v", err), "status": "blocked"})
			} else {
				fmt.Fprintf(os.Stderr, "Error reading import file %s: %v\n", cleanImport, err)
			}
			os.Exit(1)
		}

		ev, err := goals.DecodeTaskEvidence(data)
		if err != nil {
			if jsonOut {
				emitJSON(map[string]any{"error": fmt.Sprintf("decode imported evidence: %v", err), "status": "blocked"})
			} else {
				fmt.Fprintf(os.Stderr, "Error decoding imported evidence: %v\n", err)
			}
			os.Exit(1)
		}

		// AC-011: Import cannot escape goal paths or claim origin=runner
		if ev.GoalSlug != slug {
			errMsg := fmt.Sprintf("imported evidence goal_slug %q does not match target goal %q", ev.GoalSlug, slug)
			if jsonOut {
				emitJSON(map[string]any{"error": errMsg, "status": "blocked"})
			} else {
				fmt.Fprintf(os.Stderr, "Error: %s\n", errMsg)
			}
			os.Exit(1)
		}
		if ev.TaskID != taskID {
			errMsg := fmt.Sprintf("imported evidence task_id %q does not match target task %q", ev.TaskID, taskID)
			if jsonOut {
				emitJSON(map[string]any{"error": errMsg, "status": "blocked"})
			} else {
				fmt.Fprintf(os.Stderr, "Error: %s\n", errMsg)
			}
			os.Exit(1)
		}
		if ev.Origin == goals.OriginRunner {
			errMsg := "imported evidence cannot claim origin=runner; must be origin=imported"
			if jsonOut {
				emitJSON(map[string]any{"error": errMsg, "status": "blocked"})
			} else {
				fmt.Fprintf(os.Stderr, "Error: %s\n", errMsg)
			}
			os.Exit(1)
		}

		pkg, err := goals.LoadWorkPackage(targetDir, slug)
		if err != nil {
			if jsonOut {
				emitJSON(map[string]any{"error": fmt.Sprintf("load work package: %v", err), "status": "blocked"})
			} else {
				fmt.Fprintf(os.Stderr, "Error loading work package: %v\n", err)
			}
			os.Exit(1)
		}

		diags := goals.ValidateTaskEvidence(pkg, ev)
		diags = append(diags, goals.ValidateEvidenceArtifacts(targetDir, slug, ev)...)
		hasError := false
		for _, d := range diags {
			if d.Severity == "error" {
				hasError = true
				break
			}
		}
		if hasError {
			if jsonOut {
				emitJSON(map[string]any{"error": "evidence validation failed", "diagnostics": diags, "status": "blocked"})
			} else {
				fmt.Fprintf(os.Stderr, "Evidence validation failed:\n")
				for _, d := range diags {
					fmt.Fprintf(os.Stderr, "  - [%s] %s\n", d.Code, d.Message)
				}
			}
			os.Exit(1)
		}

		if err := goals.SaveTaskEvidence(targetDir, slug, ev); err != nil {
			if jsonOut {
				emitJSON(map[string]any{"error": fmt.Sprintf("save imported evidence: %v", err), "status": "blocked"})
			} else {
				fmt.Fprintf(os.Stderr, "Error saving imported evidence: %v\n", err)
			}
			os.Exit(1)
		}

		if jsonOut {
			emitJSON(map[string]any{
				"imported":   true,
				"goal_slug":  slug,
				"task_id":    taskID,
				"attempt_id": ev.AttemptID,
				"case_id":    ev.CaseID,
				"check_id":   ev.CheckID,
				"outcome":    ev.Outcome,
				"origin":     ev.Origin,
				"receipt":    goals.TaskEvidenceReceiptPath(targetDir, slug, taskID, ev.AttemptID),
			})
		} else {
			fmt.Printf("✔ Successfully imported evidence receipt for task %s (attempt %s, outcome %s)\n", taskID, ev.AttemptID, ev.Outcome)
		}
		return
	}

	// 2. Handle Running Checks
	if runChecks {
		results, err := goals.RunTaskChecks(context.Background(), targetDir, slug, taskID)
		if err != nil {
			if jsonOut {
				emitJSON(map[string]any{
					"error":     err.Error(),
					"goal_slug": slug,
					"task_id":   taskID,
					"status":    "blocked",
				})
			} else {
				fmt.Fprintf(os.Stderr, "Error running task checks: %v\n", err)
			}
			os.Exit(1)
		}

		allPassed := true
		var caseDispositions []map[string]any
		attemptID := ""

		for _, ev := range results {
			if attemptID == "" {
				attemptID = ev.AttemptID
			}
			if err := goals.SaveTaskEvidence(targetDir, slug, &ev); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to save evidence %s: %v\n", ev.CheckID, err)
			}
			if ev.Outcome != goals.OutcomePass {
				allPassed = false
			}
			caseDispositions = append(caseDispositions, map[string]any{
				"id":           ev.CaseID,
				"check_id":     ev.CheckID,
				"scope":        ev.Scope,
				"outcome":      ev.Outcome,
				"failure_code": ev.FailureCode,
			})
		}

		if jsonOut {
			emitJSON(map[string]any{
				"schema_version": "sila/task-result/v1",
				"goal_slug":      slug,
				"task_id":        taskID,
				"accepted":       false, // AC-012: Evidence collection and acceptance are separate
				"all_passed":     allPassed,
				"attempt_id":     attemptID,
				"cases":          caseDispositions,
			})
		} else {
			fmt.Printf("Evidence collection for %s (%s):\n", slug, taskID)
			for _, cd := range caseDispositions {
				fmt.Printf("  - Case %s (check %s): %s\n", cd["id"], cd["check_id"], cd["outcome"])
			}
		}

		// AC-012: Nonzero on failed/blocked required cases
		if !allPassed {
			os.Exit(1)
		}
		return
	}

	// 3. Inspect / List recorded attempts
	attempts, err := goals.ListTaskEvidenceAttempts(targetDir, slug, taskID)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error(), "goal_slug": slug, "task_id": taskID})
		} else {
			fmt.Fprintf(os.Stderr, "Error listing evidence attempts: %v\n", err)
		}
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(map[string]any{
			"goal_slug": slug,
			"task_id":   taskID,
			"attempts":  attempts,
		})
	} else {
		if len(attempts) == 0 {
			fmt.Printf("No evidence receipts recorded for task %s under goal %s\n", taskID, slug)
		} else {
			fmt.Printf("Recorded evidence attempts for %s (%s):\n", slug, taskID)
			for _, a := range attempts {
				fmt.Printf("  - %s\n", a)
			}
		}
	}
}
