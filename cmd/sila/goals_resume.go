package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/jadmadi/sila/internal/goals"
)

func (gc *goalsContext) handleCheckpoint(targetDir string, args []string) {
	jsonOut := gc.jsonOut
	var positional []string
	fromFile := ""
	agentOpt := ""
	sessionOpt := ""
	stopAt := ""
	clearStop := false

	for _, a := range args {
		if a == "--json" || a == "-json" {
			jsonOut = true
		} else if after, ok := strings.CutPrefix(a, "--from="); ok {
			fromFile = after
		} else if after, ok := strings.CutPrefix(a, "-from="); ok {
			fromFile = after
		} else if after, ok := strings.CutPrefix(a, "--agent="); ok {
			agentOpt = after
		} else if after, ok := strings.CutPrefix(a, "-agent="); ok {
			agentOpt = after
		} else if after, ok := strings.CutPrefix(a, "--session="); ok {
			sessionOpt = after
		} else if after, ok := strings.CutPrefix(a, "-session="); ok {
			sessionOpt = after
		} else if after, ok := strings.CutPrefix(a, "--stop-at="); ok {
			stopAt = strings.TrimSpace(after)
		} else if after, ok := strings.CutPrefix(a, "-stop-at="); ok {
			stopAt = strings.TrimSpace(after)
		} else if a == "--clear-stop" || a == "-clear-stop" {
			clearStop = true
		} else if strings.HasPrefix(a, "-") {
			fmt.Fprintf(os.Stderr, "unknown flag: %s\nUsage: sila goals checkpoint <slug> <task-id> --from=<file> [--json]\n", a)
			os.Exit(2)
		} else {
			positional = append(positional, a)
		}
	}

	if len(positional) < 1 || (fromFile == "" && stopAt == "" && !clearStop) {
		fmt.Fprintln(os.Stderr, "Usage: sila goals checkpoint <slug> <task-id> --from=<file> [--stop-at=<task-id>|--clear-stop] [--json]")
		os.Exit(2)
	}

	slug := positional[0]
	taskID := ""
	if len(positional) > 1 {
		taskID = positional[1]
	}

	if targetDir == "" {
		targetDir = goals.ResolveGoalsRootDir(".")
	}

	// Validate active claim existence and ownership
	claim, err := goals.LoadGoalClaim(targetDir, slug)
	if err != nil || claim == nil {
		errStr := fmt.Sprintf("goal %s has no active claim", slug)
		if jsonOut {
			emitJSON(map[string]any{
				"error":     errStr,
				"goal_slug": slug,
				"task_id":   taskID,
				"status":    "conflict",
			})
		} else {
			fmt.Fprintf(os.Stderr, "Error: %s\n", errStr)
		}
		os.Exit(1)
	}

	callerAgent := gc.agent
	if agentOpt != "" {
		callerAgent = agentOpt
	}
	if callerAgent == "" {
		callerAgent = os.Getenv("SILA_AGENT")
	}
	if callerAgent != "" {
		normCaller := strings.TrimPrefix(callerAgent, "@")
		claimAgent := strings.TrimPrefix(claim.Agent, "@")
		if claimAgent == "" {
			claimAgent = strings.TrimPrefix(claim.ClaimedBy, "@")
		}
		if claimAgent != "" && !strings.EqualFold(normCaller, claimAgent) {
			errStr := fmt.Sprintf("active claim owner mismatch: goal %s is claimed by @%s, not @%s", slug, claimAgent, normCaller)
			if jsonOut {
				emitJSON(map[string]any{
					"error":     errStr,
					"goal_slug": slug,
					"task_id":   taskID,
					"status":    "conflict",
				})
			} else {
				fmt.Fprintf(os.Stderr, "Error: %s\n", errStr)
			}
			os.Exit(1)
		}
	}

	callerSession := gc.sessionID
	if sessionOpt != "" {
		callerSession = sessionOpt
	}
	if callerSession != "" && claim.SessionID != "" && callerSession != claim.SessionID {
		errStr := fmt.Sprintf("active claim session mismatch: goal %s held by session %s, not %s", slug, claim.SessionID, callerSession)
		if jsonOut {
			emitJSON(map[string]any{
				"error":     errStr,
				"goal_slug": slug,
				"task_id":   taskID,
				"status":    "conflict",
			})
		} else {
			fmt.Fprintf(os.Stderr, "Error: %s\n", errStr)
		}
		os.Exit(1)
	}

	if stale, why, _ := claim.CheckStale(); stale {
		errStr := fmt.Sprintf("active claim on goal %s is stale (%s)", slug, why)
		if jsonOut {
			emitJSON(map[string]any{
				"error":     errStr,
				"goal_slug": slug,
				"task_id":   taskID,
				"status":    "conflict",
			})
		} else {
			fmt.Fprintf(os.Stderr, "Error: %s\n", errStr)
		}
		os.Exit(1)
	}

	if stopAt != "" || clearStop {
		newStop := stopAt
		if clearStop {
			newStop = ""
		}
		updated, stopErr := goals.SetGoalStopAtTask(targetDir, slug, newStop)
		if stopErr != nil {
			if jsonOut {
				emitJSON(map[string]any{"error": stopErr.Error(), "goal_slug": slug, "status": "error"})
			} else {
				fmt.Fprintf(os.Stderr, "Error setting stop lease: %v\n", stopErr)
			}
			os.Exit(1)
		}
		if fromFile == "" {
			if jsonOut {
				emitJSON(map[string]any{
					"goal_slug":       slug,
					"stop_at_task_id": updated.StopAtTaskID,
					"saved":           true,
				})
			} else {
				fmt.Fprintf(os.Stdout, "%s Stop lease updated for %s (stop-at: %q)\n",
					gc.ui.Green("✔"), slug, updated.StopAtTaskID)
			}
			os.Exit(0)
		}
	}

	if taskID == "" {
		fmt.Fprintln(os.Stderr, "Usage: sila goals checkpoint <slug> <task-id> --from=<file> [--json]")
		os.Exit(2)
	}

	data, err := os.ReadFile(fromFile)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{
				"error":     err.Error(),
				"goal_slug": slug,
				"task_id":   taskID,
				"status":    "error",
			})
		} else {
			fmt.Fprintf(os.Stderr, "Error reading checkpoint file: %v\n", err)
		}
		os.Exit(1)
	}

	cp, err := goals.DecodeTaskCheckpoint(data)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{
				"error":     err.Error(),
				"goal_slug": slug,
				"task_id":   taskID,
				"status":    "error",
			})
		} else {
			fmt.Fprintf(os.Stderr, "Invalid task checkpoint: %v\n", err)
		}
		os.Exit(1)
	}

	if cp.TaskID != taskID {
		errStr := fmt.Sprintf("task_id mismatch: checkpoint specifies %q, argument is %q", cp.TaskID, taskID)
		if jsonOut {
			emitJSON(map[string]any{
				"error":     errStr,
				"goal_slug": slug,
				"task_id":   taskID,
				"status":    "conflict",
			})
		} else {
			fmt.Fprintf(os.Stderr, "Error: %s\n", errStr)
		}
		os.Exit(1)
	}

	if cp.GoalSlug != "" && cp.GoalSlug != slug {
		errStr := fmt.Sprintf("goal_slug mismatch: checkpoint specifies %q, target is %q", cp.GoalSlug, slug)
		if jsonOut {
			emitJSON(map[string]any{
				"error":     errStr,
				"goal_slug": slug,
				"task_id":   taskID,
				"status":    "conflict",
			})
		} else {
			fmt.Fprintf(os.Stderr, "Error: %s\n", errStr)
		}
		os.Exit(1)
	}

	if err := goals.SaveTaskCheckpoint(targetDir, slug, *cp); err != nil {
		if jsonOut {
			emitJSON(map[string]any{
				"error":     err.Error(),
				"goal_slug": slug,
				"task_id":   taskID,
				"status":    "conflict",
			})
		} else {
			fmt.Fprintf(os.Stderr, "Error saving checkpoint: %v\n", err)
		}
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(map[string]any{
			"goal_slug":  slug,
			"task_id":    taskID,
			"saved":      true,
			"sequence":   cp.Sequence,
			"status":     cp.Status,
			"checkpoint": cp,
		})
	} else {
		fmt.Fprintf(os.Stdout, "%s Checkpoint saved for %s/%s (sequence %d, status: %s)\n",
			gc.ui.Green("✔"), slug, taskID, cp.Sequence, cp.Status)
	}
	os.Exit(0)
}

func (gc *goalsContext) handleResume(targetDir string, args []string) {
	jsonOut := gc.jsonOut
	var positional []string

	for _, a := range args {
		if a == "--json" || a == "-json" {
			jsonOut = true
		} else if strings.HasPrefix(a, "-") {
			fmt.Fprintf(os.Stderr, "unknown flag: %s\nUsage: sila goals resume <slug> <task-id> [--json]\n", a)
			os.Exit(2)
		} else {
			positional = append(positional, a)
		}
	}

	if len(positional) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: sila goals resume <slug> <task-id> [--json]")
		os.Exit(2)
	}

	slug := positional[0]
	taskID := positional[1]

	if targetDir == "" {
		targetDir = goals.ResolveGoalsRootDir(".")
	}

	packet, err := goals.BuildTaskResumePacket(targetDir, slug, taskID)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{
				"error":     err.Error(),
				"goal_slug": slug,
				"task_id":   taskID,
				"status":    "blocked",
			})
		} else {
			fmt.Fprintf(os.Stderr, "Error resuming task %s in goal %s: %v\n", taskID, slug, err)
		}
		os.Exit(1)
	}

	if jsonOut {
		data, err := goals.EncodeTaskPacketJSON(packet)
		if err != nil {
			emitJSON(map[string]any{
				"error": err.Error(),
			})
			os.Exit(1)
		}
		fmt.Println(string(data))
	} else {
		md, err := goals.RenderTaskPacketMarkdown(packet)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error rendering task resume packet: %v\n", err)
			os.Exit(1)
		}
		fmt.Print(md)
	}

	if !packet.Executable {
		os.Exit(1)
	}
	os.Exit(0)
}

func (gc *goalsContext) handleAmend(targetDir string, args []string) {
	jsonOut := gc.jsonOut
	var positional []string
	fromFile := ""
	pkgFile := ""
	agentOpt := ""
	sessionOpt := ""

	for _, a := range args {
		if a == "--json" || a == "-json" {
			jsonOut = true
		} else if after, ok := strings.CutPrefix(a, "--from="); ok {
			fromFile = after
		} else if after, ok := strings.CutPrefix(a, "-from="); ok {
			fromFile = after
		} else if after, ok := strings.CutPrefix(a, "--package="); ok {
			pkgFile = after
		} else if after, ok := strings.CutPrefix(a, "-package="); ok {
			pkgFile = after
		} else if after, ok := strings.CutPrefix(a, "--agent="); ok {
			agentOpt = after
		} else if after, ok := strings.CutPrefix(a, "-agent="); ok {
			agentOpt = after
		} else if after, ok := strings.CutPrefix(a, "--session="); ok {
			sessionOpt = after
		} else if after, ok := strings.CutPrefix(a, "-session="); ok {
			sessionOpt = after
		} else if strings.HasPrefix(a, "-") {
			fmt.Fprintf(os.Stderr, "unknown flag: %s\nUsage: sila goals amend <slug> --from=<amendment.json> --package=<work-package.json> [--json]\n", a)
			os.Exit(2)
		} else {
			positional = append(positional, a)
		}
	}

	if len(positional) < 1 || fromFile == "" || pkgFile == "" {
		fmt.Fprintln(os.Stderr, "Usage: sila goals amend <slug> --from=<amendment.json> --package=<work-package.json> [--json]")
		os.Exit(2)
	}

	slug := positional[0]

	if targetDir == "" {
		targetDir = goals.ResolveGoalsRootDir(".")
	}

	// Validate active claim existence and ownership on writes
	claim, err := goals.LoadGoalClaim(targetDir, slug)
	if err != nil || claim == nil {
		errStr := fmt.Sprintf("goal %s has no active claim", slug)
		if jsonOut {
			emitJSON(map[string]any{
				"error":     errStr,
				"goal_slug": slug,
				"status":    "conflict",
			})
		} else {
			fmt.Fprintf(os.Stderr, "Error: %s\n", errStr)
		}
		os.Exit(1)
	}

	callerAgent := gc.agent
	if agentOpt != "" {
		callerAgent = agentOpt
	}
	if callerAgent == "" {
		callerAgent = os.Getenv("SILA_AGENT")
	}
	if callerAgent != "" {
		normCaller := strings.TrimPrefix(callerAgent, "@")
		claimAgent := strings.TrimPrefix(claim.Agent, "@")
		if claimAgent == "" {
			claimAgent = strings.TrimPrefix(claim.ClaimedBy, "@")
		}
		if claimAgent != "" && !strings.EqualFold(normCaller, claimAgent) {
			errStr := fmt.Sprintf("active claim owner mismatch: goal %s is claimed by @%s, not @%s", slug, claimAgent, normCaller)
			if jsonOut {
				emitJSON(map[string]any{
					"error":     errStr,
					"goal_slug": slug,
					"status":    "conflict",
				})
			} else {
				fmt.Fprintf(os.Stderr, "Error: %s\n", errStr)
			}
			os.Exit(1)
		}
	}

	callerSession := gc.sessionID
	if sessionOpt != "" {
		callerSession = sessionOpt
	}
	if callerSession != "" && claim.SessionID != "" && callerSession != claim.SessionID {
		errStr := fmt.Sprintf("active claim session mismatch: goal %s held by session %s, not %s", slug, claim.SessionID, callerSession)
		if jsonOut {
			emitJSON(map[string]any{
				"error":     errStr,
				"goal_slug": slug,
				"status":    "conflict",
			})
		} else {
			fmt.Fprintf(os.Stderr, "Error: %s\n", errStr)
		}
		os.Exit(1)
	}

	if stale, why, _ := claim.CheckStale(); stale {
		errStr := fmt.Sprintf("active claim on goal %s is stale (%s)", slug, why)
		if jsonOut {
			emitJSON(map[string]any{
				"error":     errStr,
				"goal_slug": slug,
				"status":    "conflict",
			})
		} else {
			fmt.Fprintf(os.Stderr, "Error: %s\n", errStr)
		}
		os.Exit(1)
	}

	amendData, err := os.ReadFile(fromFile)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{
				"error":     err.Error(),
				"goal_slug": slug,
				"status":    "error",
			})
		} else {
			fmt.Fprintf(os.Stderr, "Error reading amendment file: %v\n", err)
		}
		os.Exit(1)
	}

	amendment, err := goals.DecodeTaskAmendment(amendData)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{
				"error":     err.Error(),
				"goal_slug": slug,
				"status":    "error",
			})
		} else {
			fmt.Fprintf(os.Stderr, "Invalid task amendment: %v\n", err)
		}
		os.Exit(1)
	}

	if amendment.GoalSlug != "" && amendment.GoalSlug != slug {
		errStr := fmt.Sprintf("goal_slug mismatch: amendment specifies %q, argument is %q", amendment.GoalSlug, slug)
		if jsonOut {
			emitJSON(map[string]any{
				"error":     errStr,
				"goal_slug": slug,
				"status":    "conflict",
			})
		} else {
			fmt.Fprintf(os.Stderr, "Error: %s\n", errStr)
		}
		os.Exit(1)
	}

	pkgData, err := os.ReadFile(pkgFile)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{
				"error":     err.Error(),
				"goal_slug": slug,
				"status":    "error",
			})
		} else {
			fmt.Fprintf(os.Stderr, "Error reading work package file: %v\n", err)
		}
		os.Exit(1)
	}

	nextPkg, err := goals.DecodeWorkPackage(pkgData)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{
				"error":     err.Error(),
				"goal_slug": slug,
				"status":    "error",
			})
		} else {
			fmt.Fprintf(os.Stderr, "Invalid next work package: %v\n", err)
		}
		os.Exit(1)
	}

	if nextPkg.GoalSlug != "" && nextPkg.GoalSlug != slug {
		errStr := fmt.Sprintf("goal_slug mismatch: package specifies %q, argument is %q", nextPkg.GoalSlug, slug)
		if jsonOut {
			emitJSON(map[string]any{
				"error":     errStr,
				"goal_slug": slug,
				"status":    "conflict",
			})
		} else {
			fmt.Fprintf(os.Stderr, "Error: %s\n", errStr)
		}
		os.Exit(1)
	}

	if err := goals.ApplyTaskAmendment(targetDir, slug, *amendment, nextPkg); err != nil {
		if jsonOut {
			emitJSON(map[string]any{
				"error":     err.Error(),
				"goal_slug": slug,
				"status":    "conflict",
			})
		} else {
			fmt.Fprintf(os.Stderr, "Error applying amendment: %v\n", err)
		}
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(map[string]any{
			"goal_slug":    slug,
			"amended":      true,
			"old_revision": amendment.OldRevision,
			"new_revision": amendment.NewRevision,
			"amendment":    amendment,
		})
	} else {
		fmt.Fprintf(os.Stdout, "%s Amendment applied for goal %s (revision %d -> %d)\n",
			gc.ui.Green("✔"), slug, amendment.OldRevision, amendment.NewRevision)
	}
	os.Exit(0)
}
