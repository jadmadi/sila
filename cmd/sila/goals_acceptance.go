package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/goals"
)

func (gc *goalsContext) handleAccept(targetDir string, args []string) {
	ui := gc.ui
	jsonOut := gc.jsonOut
	selectionFile := gc.selectionFile
	reviewFile := gc.reviewOpt
	var positional []string

	for _, a := range append(args, os.Args...) {
		if a == "--json" || a == "-json" {
			jsonOut = true
		} else if after, ok := strings.CutPrefix(a, "--selection="); ok {
			selectionFile = after
		} else if after, ok := strings.CutPrefix(a, "-selection="); ok {
			selectionFile = after
		} else if after, ok := strings.CutPrefix(a, "--review="); ok {
			reviewFile = after
		} else if after, ok := strings.CutPrefix(a, "-review="); ok {
			reviewFile = after
		}
	}

	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			positional = append(positional, a)
		}
	}

	if len(positional) < 2 || selectionFile == "" || reviewFile == "" {
		usage := "Usage: sila goals accept <slug> <task-id> --selection=<file> --review=<file> [--json]"
		if jsonOut {
			emitJSON(map[string]any{"error": usage, "accepted": false})
		} else {
			fmt.Fprintln(os.Stderr, usage)
		}
		os.Exit(2)
	}

	slug := positional[0]
	taskID := positional[1]

	if targetDir == "" {
		targetDir, _ = goals.ResolveProjectDir(gc.st, "")
	}
	if targetDir == "" {
		targetDir = "."
	}
	rootDir := goals.ResolveGoalsRootDir(targetDir)

	// 1. Read and decode selection file
	cleanSel := filepath.Clean(selectionFile)
	selData, err := os.ReadFile(cleanSel)
	if err != nil {
		errMsg := fmt.Sprintf("read selection file: %v", err)
		if jsonOut {
			emitJSON(map[string]any{"error": errMsg, "accepted": false})
		} else {
			fmt.Fprintf(os.Stderr, "%s %s\n", ui.Red("✖"), errMsg)
		}
		os.Exit(1)
	}
	selection, err := goals.DecodeReceiptSelection(selData)
	if err != nil {
		errMsg := fmt.Sprintf("invalid receipt selection: %v", err)
		if jsonOut {
			emitJSON(map[string]any{"error": errMsg, "accepted": false})
		} else {
			fmt.Fprintf(os.Stderr, "%s %s\n", ui.Red("✖"), errMsg)
		}
		os.Exit(1)
	}

	// 2. Read and decode review file
	cleanRev := filepath.Clean(reviewFile)
	revData, err := os.ReadFile(cleanRev)
	if err != nil {
		errMsg := fmt.Sprintf("read review file: %v", err)
		if jsonOut {
			emitJSON(map[string]any{"error": errMsg, "accepted": false})
		} else {
			fmt.Fprintf(os.Stderr, "%s %s\n", ui.Red("✖"), errMsg)
		}
		os.Exit(1)
	}
	review, err := goals.DecodeAcceptanceReview(revData)
	if err != nil {
		errMsg := fmt.Sprintf("invalid acceptance review: %v", err)
		if jsonOut {
			emitJSON(map[string]any{"error": errMsg, "accepted": false})
		} else {
			fmt.Fprintf(os.Stderr, "%s %s\n", ui.Red("✖"), errMsg)
		}
		os.Exit(1)
	}

	// 3. Determine if task is implemented
	taskImplemented := false
	if tasks, err := goals.GetGoalTasks(rootDir, slug); err == nil {
		for _, t := range tasks {
			if t.ID == taskID {
				taskImplemented = t.Completed
				break
			}
		}
	}

	// 4. Check for existing acceptance record (idempotency check)
	existing, _ := goals.LoadTaskAcceptance(rootDir, slug, taskID)

	// 5. Evaluate task acceptance
	res, evalErr := goals.EvaluateTaskAcceptance(rootDir, slug, taskID, *selection, *review)
	if evalErr != nil || res == nil || !res.Accepted {
		// Record failed attempt as evidence without overwriting existing accepted history
		if existing != nil && existing.Accepted {
			_ = recordFailedAttemptEvidence(rootDir, slug, taskID, res, evalErr)
		} else if res != nil {
			_ = recordFailedAttemptEvidence(rootDir, slug, taskID, res, evalErr)
		}

		errMsg := "task acceptance criteria not satisfied"
		if evalErr != nil {
			errMsg = evalErr.Error()
		}

		if jsonOut {
			outMap := map[string]any{
				"schema_version":    goals.AcceptanceResultSchemaVersion,
				"goal_slug":         slug,
				"task_id":           taskID,
				"accepted":          false,
				"acceptance_status": "failed",
				"evidence_status":   "failed",
				"implemented":       taskImplemented,
				"error":             errMsg,
			}
			if res != nil {
				outMap["acceptance_status"] = string(res.EvidenceStatus)
				outMap["evidence_status"] = string(res.EvidenceStatus)
				outMap["spec_digest"] = res.SpecDigest
				outMap["candidate_digest"] = res.CandidateDigest
				outMap["receipt_paths"] = res.ReceiptPaths
				outMap["missing_case_ids"] = res.MissingCaseIDs
				outMap["failed_case_ids"] = res.FailedCaseIDs
				outMap["diagnostics"] = res.Diagnostics
				outMap["reviewed_at"] = res.ReviewedAt
			}
			emitJSON(outMap)
		} else {
			fmt.Fprintf(os.Stderr, "%s Task %s acceptance rejected: %s\n", ui.Red("✖"), taskID, errMsg)
			if res != nil {
				for _, diag := range res.Diagnostics {
					fmt.Fprintf(os.Stderr, "  - [%s] %s\n", diag.Code, diag.Message)
				}
			}
		}
		os.Exit(1)
	}

	// 6. Acceptance succeeded: atomically persist
	if err := goals.SaveTaskAcceptance(rootDir, slug, taskID, res, selection, review); err != nil {
		errMsg := fmt.Sprintf("save task acceptance: %v", err)
		if jsonOut {
			emitJSON(map[string]any{"error": errMsg, "accepted": false})
		} else {
			fmt.Fprintf(os.Stderr, "%s %s\n", ui.Red("✖"), errMsg)
		}
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(map[string]any{
			"schema_version":    res.SchemaVersion,
			"goal_slug":         res.GoalSlug,
			"task_id":           res.TaskID,
			"accepted":          res.Accepted,
			"acceptance_status": string(res.EvidenceStatus),
			"evidence_status":   string(res.EvidenceStatus),
			"implemented":       taskImplemented,
			"spec_digest":       res.SpecDigest,
			"candidate_digest":  res.CandidateDigest,
			"receipt_paths":     res.ReceiptPaths,
			"missing_case_ids":  res.MissingCaseIDs,
			"failed_case_ids":   res.FailedCaseIDs,
			"diagnostics":       res.Diagnostics,
			"reviewed_at":       res.ReviewedAt,
		})
		return
	}

	fmt.Fprintf(os.Stdout, "%s Task %s accepted\n", ui.Green("✔"), ui.Bold(taskID))
	fmt.Fprintf(os.Stdout, "  Goal:              %s\n", slug)
	fmt.Fprintf(os.Stdout, "  Accepted:          %t\n", res.Accepted)
	fmt.Fprintf(os.Stdout, "  Acceptance Status: %s\n", res.EvidenceStatus)
	fmt.Fprintf(os.Stdout, "  Implemented:       %t\n", taskImplemented)
	fmt.Fprintf(os.Stdout, "  Spec Digest:       %s\n", res.SpecDigest)
	fmt.Fprintf(os.Stdout, "  Candidate Digest:  %s\n", res.CandidateDigest)
	if len(res.ReceiptPaths) > 0 {
		fmt.Fprintf(os.Stdout, "  Receipts:          %s\n", strings.Join(res.ReceiptPaths, ", "))
	}
}

func recordFailedAttemptEvidence(rootDir, slug, taskID string, res *goals.AcceptanceResult, err error) error {
	dir := filepath.Join(rootDir, "goals", slug, "evidence", taskID)
	_ = os.MkdirAll(dir, 0755)
	p := filepath.Join(dir, fmt.Sprintf("rejected-%d.json", time.Now().UTC().UnixNano()))
	data, _ := json.MarshalIndent(map[string]any{
		"goal_slug": slug,
		"task_id":   taskID,
		"error":     fmt.Sprintf("%v", err),
		"result":    res,
		"time":      time.Now().UTC().Format(time.RFC3339),
	}, "", "  ")
	return os.WriteFile(p, data, 0644)
}
