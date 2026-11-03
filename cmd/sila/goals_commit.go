package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/render"
)

type goalsCommitResult struct {
	Slug         string `json:"slug"`
	Stage1SHA    string `json:"stage1_sha"`
	Stage1Msg    string `json:"stage1_msg"`
	Stage2SHA    string `json:"stage2_sha,omitempty"`
	Stage2Msg    string `json:"stage2_msg,omitempty"`
	RegistrySync bool   `json:"registry_sync"`
	Rebuilt      bool   `json:"rebuilt"`
	Success      bool   `json:"success"`
	Error        string `json:"error,omitempty"`
}

// runGoalsCommit executes the atomic two-stage goal resolution protocol:
// Stage 1: Ensures goals/README.md is unstaged, commits implementation with exact (goals/<slug>/goal.md) attribution.
// Optional rebuild: Refreshes target binary so embedded commit SHA matches.
// Stage 2: Reconciles goal registry and commits goals/README.md in a separate docs(goals) commit.
func runGoalsCommit(ui render.UI, targetDir string, args []string, commitMsg string, noBuild, checkTasks, jsonOut, toonOut bool) {
	res, err := executeGoalsCommitWithOptions(ui, targetDir, args, commitMsg, noBuild, checkTasks)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error(), "success": false})
		} else {
			fmt.Fprintf(os.Stderr, "sila goals commit error: %v\n", err)
		}
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(res)
		return
	}
	if toonOut {
		fields := [][2]string{
			{"slug", res.Slug},
			{"stage1_sha", res.Stage1SHA},
			{"stage1_msg", res.Stage1Msg},
			{"stage2_sha", res.Stage2SHA},
			{"registry_sync", fmt.Sprintf("%t", res.RegistrySync)},
			{"rebuilt", fmt.Sprintf("%t", res.Rebuilt)},
			{"success", fmt.Sprintf("%t", res.Success)},
		}
		fmt.Fprint(os.Stdout, render.FormatTOONObject("goals_commit", fields))
		return
	}

	fmt.Fprintf(os.Stdout, "%s Two-stage goal commit complete for %s\n", ui.Green("✔"), ui.Bold(res.Slug))
	fmt.Fprintf(os.Stdout, "  → Stage 1 (Implementation): [%s] %s\n", ui.Cyan(res.Stage1SHA), res.Stage1Msg)
	if res.RegistrySync && res.Stage2SHA != "" {
		fmt.Fprintf(os.Stdout, "  → Stage 2 (Registry Sync):  [%s] %s\n", ui.Cyan(res.Stage2SHA), res.Stage2Msg)
	}
	if res.Rebuilt {
		fmt.Fprintf(os.Stdout, "  → Rebuild:                   %s\n", ui.Green("binary rebuilt and verified"))
	}
}

func executeGoalsCommit(ui render.UI, targetDir string, args []string, commitMsg string, noBuild bool) (*goalsCommitResult, error) {
	return executeGoalsCommitWithOptions(ui, targetDir, args, commitMsg, noBuild, false)
}

func executeGoalsCommitWithOptions(ui render.UI, targetDir string, args []string, commitMsg string, noBuild, checkTasks bool) (*goalsCommitResult, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("missing goal slug\nUsage: sila goals commit <slug> -m \"<commit-message>\" [--no-build]")
	}
	slug := args[1]

	// Extract commit message from args if not provided via -m
	if commitMsg == "" && len(args) > 2 {
		commitMsg = strings.Join(args[2:], " ")
	}
	commitMsg = strings.TrimSpace(commitMsg)
	if commitMsg == "" {
		return nil, fmt.Errorf("commit message required (-m \"<message>\")\nUsage: sila goals commit %s -m \"feat: implement %s\"", slug, slug)
	}

	if targetDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		targetDir = cwd
	}

	// 1. Verify inside git repo
	gitCheck := exec.Command("git", "-C", targetDir, "rev-parse", "--is-inside-work-tree")
	if out, err := gitCheck.CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != "true" {
		return nil, fmt.Errorf("target directory is not a git repository: %s", targetDir)
	}

	// 2. Verify goal definition exists
	goalPath := filepath.Join(targetDir, "goals", slug, "goal.md")
	if _, err := os.Stat(goalPath); err != nil {
		return nil, fmt.Errorf("goal definition not found: %s", goalPath)
	}

	// 3. Preflight: Check staged files
	stagedCmd := exec.Command("git", "-C", targetDir, "diff", "--cached", "--name-only")
	stagedOut, err := stagedCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to inspect staged git changes: %v", err)
	}

	stagedFiles := strings.Split(strings.TrimSpace(string(stagedOut)), "\n")
	hasStagedChanges := false
	readmeStaged := false
	for _, f := range stagedFiles {
		clean := strings.TrimSpace(f)
		if clean == "" {
			continue
		}
		hasStagedChanges = true
		if clean == "goals/README.md" || filepath.ToSlash(clean) == "goals/README.md" {
			readmeStaged = true
		}
	}

	// 4. Preflight invariant: Unstage goals/README.md if present
	if readmeStaged {
		unstageCmd := exec.Command("git", "-C", targetDir, "restore", "--staged", "goals/README.md")
		if err := unstageCmd.Run(); err != nil {
			return nil, fmt.Errorf("failed to unstage goals/README.md for stage 1: %v", err)
		}
		fmt.Fprintf(os.Stdout, "%s Unstaged goals/README.md (reserved for Stage 2 registry sync)\n", ui.Yellow("ℹ"))

		// Re-check staged files after unstaging README
		stagedCmd2 := exec.Command("git", "-C", targetDir, "diff", "--cached", "--name-only")
		stagedOut2, _ := stagedCmd2.Output()
		if len(strings.TrimSpace(string(stagedOut2))) == 0 {
			return nil, fmt.Errorf("no implementation files staged for stage 1 commit. Stage code files first (e.g. git add <files>)")
		}
	} else if !hasStagedChanges {
		return nil, fmt.Errorf("no staged changes to commit. Stage implementation files first with 'git add <files>'")
	}

	// 4b. Enforce non-attribution plan checkboxes before Stage 1.
	unchecked, err := goals.CountUncheckedNonAttributionTasks(targetDir, slug)
	if err != nil {
		return nil, fmt.Errorf("inspect plan tasks: %w", err)
	}
	if unchecked > 0 {
		if !checkTasks {
			return nil, fmt.Errorf("goal %q has %d unchecked task(s) in goals/%s/plan.md; complete them or pass --check-tasks to auto-check non-attribution tasks", slug, unchecked, slug)
		}
		updated, err := goals.CheckAllNonAttributionTasks(targetDir, slug)
		if err != nil {
			return nil, fmt.Errorf("auto-check plan tasks: %w", err)
		}
		if updated > 0 {
			if err := exec.Command("git", "-C", targetDir, "add", filepath.Join("goals", slug, "plan.md")).Run(); err != nil {
				return nil, fmt.Errorf("stage updated plan.md: %w", err)
			}
		}
		remaining, err := goals.CountUncheckedNonAttributionTasks(targetDir, slug)
		if err != nil {
			return nil, err
		}
		if remaining > 0 {
			return nil, fmt.Errorf("goal %q still has %d unchecked non-attribution task(s) after --check-tasks", slug, remaining)
		}
	}

	// 5. Format Stage 1 commit message with exact attribution
	attribution := fmt.Sprintf("(goals/%s/goal.md)", slug)
	if !strings.Contains(commitMsg, attribution) {
		commitMsg = commitMsg + " " + attribution
	}

	// 6. Execute Stage 1 commit
	commitCmd := exec.Command("git", "-C", targetDir, "commit", "-m", commitMsg)
	commitCmd.Env = append(os.Environ(), "SILA_ACTIVE_GOAL="+slug)
	if out, err := commitCmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("stage 1 implementation commit failed:\n%s", strings.TrimSpace(string(out)))
	}

	// Read Stage 1 commit SHA
	shaCmd := exec.Command("git", "-C", targetDir, "rev-parse", "--short", "HEAD")
	shaOut, err := shaCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve stage 1 commit SHA: %v", err)
	}
	stage1SHA := strings.TrimSpace(string(shaOut))

	result := &goalsCommitResult{
		Slug:      slug,
		Stage1SHA: stage1SHA,
		Stage1Msg: commitMsg,
		Success:   true,
	}

	// 7. Optional Rebuild Step (Phase 4)
	buildScript := filepath.Join(targetDir, "build.sh")
	if !noBuild {
		if fi, err := os.Stat(buildScript); err == nil && !fi.IsDir() {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			bCmd := exec.CommandContext(ctx, "./build.sh")
			bCmd.Dir = targetDir
			if bOut, err := bCmd.CombinedOutput(); err == nil {
				result.Rebuilt = true
			} else {
				fmt.Fprintf(os.Stderr, "warning: ./build.sh rebuild returned: %v\n%s\n", err, strings.TrimSpace(string(bOut)))
			}
		}
	}

	// 8. Stage 2: Reconcile Goal Registry & Commit goals/README.md (Phase 5)
	_ = goals.RemoveCharter(targetDir, slug)
	if res, err := goals.ScanGoals(targetDir); err == nil {
		_, _ = goals.ClearClosedClaims(targetDir, res)
		_ = goals.SyncReadme(targetDir, res)
	}

	// Check if goals/README.md is modified
	diffCmd := exec.Command("git", "-C", targetDir, "status", "--porcelain", "goals/README.md")
	diffOut, _ := diffCmd.Output()
	if len(strings.TrimSpace(string(diffOut))) > 0 {
		stage2Msg := fmt.Sprintf("docs(goals): sync goal registry after verifying %s", slug)
		_ = exec.Command("git", "-C", targetDir, "add", "goals/README.md").Run()

		c2Cmd := exec.Command("git", "-C", targetDir, "commit", "-m", stage2Msg)
		if out2, err := c2Cmd.CombinedOutput(); err == nil {
			sha2Cmd := exec.Command("git", "-C", targetDir, "rev-parse", "--short", "HEAD")
			if sha2Out, err := sha2Cmd.Output(); err == nil {
				result.Stage2SHA = strings.TrimSpace(string(sha2Out))
				result.Stage2Msg = stage2Msg
				result.RegistrySync = true

				// Post-stage 2 rebuild if build script exists
				if !noBuild && result.Rebuilt {
					ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
					defer cancel()
					b2Cmd := exec.CommandContext(ctx, "./build.sh")
					b2Cmd.Dir = targetDir
					_ = b2Cmd.Run()
				}
			}
		} else {
			fmt.Fprintf(os.Stderr, "warning: stage 2 registry commit returned: %v\n%s\n", err, strings.TrimSpace(string(out2)))
		}
	}

	return result, nil
}
