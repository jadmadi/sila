package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mattn/go-isatty"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/skills"
)

func dedupeStrings(items []string) []string {
	if len(items) == 0 {
		return items
	}
	seen := make(map[string]bool, len(items))
	var out []string
	for _, item := range items {
		if !seen[item] {
			seen[item] = true
			out = append(out, item)
		}
	}
	return out
}

func parseAdapterFlags(subArgs, rawArgs []string, defaultDir string, baseOpts skillCreateOptions) (skills.AdapterOptions, string, bool, bool, bool, bool) {
	harness := "antigravity"
	if baseOpts.harness != "" {
		harness = baseOpts.harness
	}
	opts := skills.AdapterOptions{
		Harness: harness,
		DryRun:  baseOpts.dryRun,
		Force:   baseOpts.force,
	}
	targetDir := defaultDir
	if baseOpts.outDir != "" {
		targetDir = baseOpts.outDir
	}
	jsonOut := false
	toonOut := false
	yesFlag := false
	if baseOpts.yes {
		yesFlag = true
	}
	interactiveFlag := false
	if baseOpts.interactive {
		interactiveFlag = true
	}

	if baseOpts.skills != "" {
		for _, s := range strings.Split(baseOpts.skills, ",") {
			if s = strings.TrimSpace(s); s != "" {
				opts.Skills = append(opts.Skills, s)
			}
		}
	}
	if baseOpts.exclude != "" {
		for _, s := range strings.Split(baseOpts.exclude, ",") {
			if s = strings.TrimSpace(s); s != "" {
				opts.Exclude = append(opts.Exclude, s)
			}
		}
	}

	allArgs := append([]string{}, rawArgs...)
	allArgs = append(allArgs, subArgs...)

	for i := 0; i < len(allArgs); i++ {
		a := allArgs[i]
		switch {
		case a == "--dry-run" || a == "-n" || a == "--preview":
			opts.DryRun = true
		case a == "--force" || a == "-f":
			opts.Force = true
		case a == "--yes" || a == "-y":
			yesFlag = true
		case a == "--interactive" || a == "-i":
			interactiveFlag = true
		case a == "--json":
			jsonOut = true
		case a == "--toon":
			toonOut = true
		case strings.HasPrefix(a, "--harness="):
			opts.Harness = strings.TrimPrefix(a, "--harness=")
		case a == "--harness" && i+1 < len(allArgs):
			i++
			opts.Harness = allArgs[i]
		case strings.HasPrefix(a, "--skills="):
			list := strings.TrimPrefix(a, "--skills=")
			for _, s := range strings.Split(list, ",") {
				if s = strings.TrimSpace(s); s != "" {
					opts.Skills = append(opts.Skills, s)
				}
			}
		case strings.HasPrefix(a, "--skill="):
			list := strings.TrimPrefix(a, "--skill=")
			for _, s := range strings.Split(list, ",") {
				if s = strings.TrimSpace(s); s != "" {
					opts.Skills = append(opts.Skills, s)
				}
			}
		case (a == "--skills" || a == "--skill") && i+1 < len(allArgs):
			i++
			for _, s := range strings.Split(allArgs[i], ",") {
				if s = strings.TrimSpace(s); s != "" {
					opts.Skills = append(opts.Skills, s)
				}
			}
		case strings.HasPrefix(a, "--exclude="):
			list := strings.TrimPrefix(a, "--exclude=")
			for _, s := range strings.Split(list, ",") {
				if s = strings.TrimSpace(s); s != "" {
					opts.Exclude = append(opts.Exclude, s)
				}
			}
		case a == "--exclude" && i+1 < len(allArgs):
			i++
			for _, s := range strings.Split(allArgs[i], ",") {
				if s = strings.TrimSpace(s); s != "" {
					opts.Exclude = append(opts.Exclude, s)
				}
			}
		case strings.HasPrefix(a, "--out="):
			targetDir = strings.TrimPrefix(a, "--out=")
		case a == "--out" && i+1 < len(allArgs):
			i++
			targetDir = allArgs[i]
		}
	}

	for _, a := range subArgs {
		if !strings.HasPrefix(a, "-") && a != "" {
			opts.Skills = append(opts.Skills, a)
		}
	}

	opts.Skills = dedupeStrings(opts.Skills)
	opts.Exclude = dedupeStrings(opts.Exclude)

	return opts, targetDir, jsonOut, toonOut, yesFlag, interactiveFlag
}

func renderColorDiff(diff string, ui render.UI) string {
	lines := strings.Split(diff, "\n")
	var colored []string
	for _, l := range lines {
		if strings.HasPrefix(l, "---") || strings.HasPrefix(l, "+++") {
			colored = append(colored, ui.Bold(l))
		} else if strings.HasPrefix(l, "+") {
			colored = append(colored, ui.Green(l))
		} else if strings.HasPrefix(l, "-") {
			colored = append(colored, ui.Red(l))
		} else if strings.HasPrefix(l, "@@") {
			colored = append(colored, ui.Cyan(l))
		} else {
			colored = append(colored, l)
		}
	}
	return strings.Join(colored, "\n")
}

func hasHelpFlag(args ...[]string) bool {
	for _, argList := range args {
		for _, a := range argList {
			if a == "--help" || a == "-h" || a == "help" {
				return true
			}
		}
	}
	return false
}

func runSkillPreviewCmd(ui render.UI, jsonOut, toonOut bool, projectDir string, baseOpts skillCreateOptions, subArgs, rawArgs []string) {
	if hasHelpFlag(subArgs, rawArgs) {
		printSkillPreviewHelp(os.Stdout, ui)
		return
	}
	opts, targetDir, parsedJSON, parsedTOON, _, _ := parseAdapterFlags(subArgs, rawArgs, projectDir, baseOpts)
	if parsedJSON {
		jsonOut = true
	}
	if parsedTOON {
		toonOut = true
	}

	res, err := skills.PreviewHarnessConfig(targetDir, opts)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error(), "action": "preview"})
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "sila skill preview: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(map[string]any{
			"action":          "preview",
			"harness":         opts.Harness,
			"target_path":     res.TargetPath,
			"file_exists":     res.FileExists,
			"has_changes":     res.HasChanges,
			"selected_skills": res.SelectedSkills,
			"excluded_skills": res.ExcludedSkills,
			"diff":            res.Diff,
			"planned_config":  res.PlannedConfig,
			"zero_write":      res.ZeroWrite,
		})
		return
	}

	if toonOut {
		fmt.Fprintf(os.Stdout, "skill_preview[target=%q,changes=%t,selected=%d,excluded=%d]:\n",
			filepath.Base(res.TargetPath), res.HasChanges, len(res.SelectedSkills), len(res.ExcludedSkills))
		for _, s := range res.SelectedSkills {
			fmt.Fprintf(os.Stdout, "  +%s\n", s)
		}
		for _, ex := range res.ExcludedSkills {
			fmt.Fprintf(os.Stdout, "  -%s\n", ex)
		}
		return
	}

	statusLabel := "[NEW FILE]"
	if res.FileExists {
		statusLabel = "[MODIFIED]"
	}
	if !res.HasChanges {
		statusLabel = "[NO CHANGES]"
	}

	fmt.Fprintf(os.Stdout, "%s sila · project skill preview (%s)\n\n", ui.Cyan("·"), opts.Harness)
	fmt.Fprintf(os.Stdout, "  Target:      %s %s\n", res.TargetPath, statusLabel)
	fmt.Fprintf(os.Stdout, "  Selected:    %s (%d skills)\n", strings.Join(res.SelectedSkills, ", "), len(res.SelectedSkills))
	fmt.Fprintf(os.Stdout, "  Excluded:    %s (%d patterns)\n\n", strings.Join(res.ExcludedSkills, ", "), len(res.ExcludedSkills))

	if res.HasChanges {
		fmt.Fprintln(os.Stdout, "Planned Changes:")
		fmt.Fprintln(os.Stdout, "──────────────────────────────────────────────────────────────────")
		fmt.Fprint(os.Stdout, res.Diff)
		fmt.Fprintln(os.Stdout, "──────────────────────────────────────────────────────────────────")
		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout, "Zero disk writes performed. Run 'sila skill apply' to execute.")
	} else {
		fmt.Fprintln(os.Stdout, "No changes required: configuration is already optimal and up-to-date.")
	}
}

func runSkillApplyCmd(ui render.UI, jsonOut, toonOut bool, projectDir string, baseOpts skillCreateOptions, subArgs, rawArgs []string) {
	if hasHelpFlag(subArgs, rawArgs) {
		printSkillApplyHelp(os.Stdout, ui)
		return
	}
	opts, targetDir, parsedJSON, parsedTOON, yesFlag, interactiveFlag := parseAdapterFlags(subArgs, rawArgs, projectDir, baseOpts)
	if parsedJSON {
		jsonOut = true
	}
	if parsedTOON {
		toonOut = true
	}

	if opts.DryRun {
		runSkillPreviewCmd(ui, jsonOut, toonOut, targetDir, baseOpts, subArgs, rawArgs)
		return
	}

	// When --interactive is requested (and not auto-approved by --yes),
	// probe for collisions first by disabling Force on initial apply attempt.
	if interactiveFlag && !yesFlag {
		opts.Force = false
	}

	res, err := skills.ApplyHarnessConfig(targetDir, opts)
	if err != nil {
		var colErr *skills.CollisionError
		isCollision := errors.As(err, &colErr) || strings.Contains(err.Error(), "collision")

		if isCollision {
			if jsonOut {
				payload := map[string]any{
					"error":     err.Error(),
					"action":    "apply",
					"collision": true,
				}
				if colErr != nil {
					payload["target_path"] = colErr.TargetPath
					payload["recorded_hash"] = colErr.RecordedHash
					payload["current_hash"] = colErr.CurrentHash
					payload["diverged_keys"] = colErr.DivergedKeys
					payload["diff"] = colErr.Diff
				}
				emitJSON(payload)
				os.Exit(1)
			}

			if toonOut {
				fmt.Fprintf(os.Stdout, "collision[target=%q]: %s\n", filepath.Base(targetDir), err.Error())
				os.Exit(1)
			}

			// If --yes was passed, auto-approve and force apply
			if yesFlag {
				opts.Force = true
				res, err = skills.ApplyHarnessConfig(targetDir, opts)
				if err == nil {
					goto RenderSuccess
				}
				fmt.Fprintf(os.Stderr, "%s error: %v\n", ui.Red("✖"), err)
				os.Exit(1)
			}

			// Interactive prompt if running in interactive TTY (or --interactive requested)
			isTerminal := isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())
			canPrompt := isTerminal || interactiveFlag

			if canPrompt {
				if colErr != nil {
					fmt.Fprintf(os.Stderr, "%s error: concurrent edit collision detected in %s\n", ui.Red("✖"), colErr.TargetPath)
					fmt.Fprintf(os.Stderr, "  Recorded Hash: %s\n", colErr.RecordedHash)
					fmt.Fprintf(os.Stderr, "  Current Hash:  %s\n", colErr.CurrentHash)
					if len(colErr.DivergedKeys) > 0 {
						fmt.Fprintf(os.Stderr, "  Diverged Keys: %s\n", strings.Join(colErr.DivergedKeys, ", "))
					}
					if colErr.Diff != "" {
						fmt.Fprintf(os.Stderr, "\nDifferences (recorded -> on disk):\n")
						fmt.Fprintf(os.Stderr, "──────────────────────────────────────────────────────────────────\n")
						fmt.Fprintf(os.Stderr, "%s", renderColorDiff(colErr.Diff, ui))
						fmt.Fprintf(os.Stderr, "──────────────────────────────────────────────────────────────────\n\n")
					}
				} else {
					fmt.Fprintf(os.Stderr, "%s %s\n\n", ui.Red("✖"), err.Error())
				}

				fmt.Fprintf(os.Stdout, "Overwrite external modifications with Sila configuration? [y/N]: ")
				reader := bufio.NewReader(os.Stdin)
				input, _ := reader.ReadString('\n')
				input = strings.ToLower(strings.TrimSpace(input))
				if input == "y" || input == "yes" {
					opts.Force = true
					res, err = skills.ApplyHarnessConfig(targetDir, opts)
					if err == nil {
						goto RenderSuccess
					}
					fmt.Fprintf(os.Stderr, "%s error applying with force: %v\n", ui.Red("✖"), err)
					os.Exit(1)
				} else {
					fmt.Fprintf(os.Stderr, "Apply cancelled. Use 'sila skill apply --force' to override or 'sila skill preview' to inspect.\n")
					os.Exit(1)
				}
			}

			// Non-interactive fail closed
			fmt.Fprintf(os.Stderr, "%s error: %v\n\nTo inspect differences:\n    sila skill preview\nTo force Sila to overwrite external modifications:\n    sila skill apply --force\n", ui.Red("✖"), err)
			os.Exit(1)
		}

		if jsonOut {
			emitJSON(map[string]any{"error": err.Error(), "action": "apply"})
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "%s sila skill apply: %v\n", ui.Red("✖"), err)
		os.Exit(1)
	}

RenderSuccess:

	if jsonOut {
		emitJSON(map[string]any{
			"action":          "apply",
			"harness":         opts.Harness,
			"target_path":     res.TargetPath,
			"status":          res.Status,
			"applied":         res.Applied,
			"no_op":           res.NoOp,
			"content_hash":    res.ContentHash,
			"selected_skills": res.SelectedSkills,
			"bytes_written":   res.BytesWritten,
			"applied_at":      res.AppliedAt.Format(time.RFC3339),
		})
		return
	}

	if toonOut {
		fmt.Fprintf(os.Stdout, "skill_apply[target=%q,status=%q,no_op=%t,bytes=%d]:\n",
			filepath.Base(res.TargetPath), res.Status, res.NoOp, res.BytesWritten)
		return
	}

	if res.NoOp {
		fmt.Fprintf(os.Stdout, "%s Harness configuration is already optimal and up-to-date. (0 bytes written, no changes)\n", ui.Green("✔"))
		return
	}

	fmt.Fprintf(os.Stdout, "%s sila · project skill configuration applied successfully (%s)\n\n", ui.Green("✔"), filepath.Base(res.TargetPath))
	fmt.Fprintf(os.Stdout, "  Target:        %s\n", res.TargetPath)
	fmt.Fprintf(os.Stdout, "  Selected:      %s (%d skills)\n", strings.Join(res.SelectedSkills, ", "), len(res.SelectedSkills))
	fmt.Fprintf(os.Stdout, "  Excluded:      %d global patterns\n", len(res.ExcludedSkills))
	fmt.Fprintf(os.Stdout, "  Content Hash:  %s\n", res.ContentHash)
	fmt.Fprintf(os.Stdout, "  Bytes Written: %d bytes\n", res.BytesWritten)
}

func runSkillRemoveCmd(ui render.UI, jsonOut, toonOut bool, projectDir string, baseOpts skillCreateOptions, subArgs, rawArgs []string) {
	if hasHelpFlag(subArgs, rawArgs) {
		printSkillRemoveHelp(os.Stdout, ui)
		return
	}
	opts, targetDir, parsedJSON, parsedTOON, _, _ := parseAdapterFlags(subArgs, rawArgs, projectDir, baseOpts)
	if parsedJSON {
		jsonOut = true
	}
	if parsedTOON {
		toonOut = true
	}

	res, err := skills.RemoveHarnessConfig(targetDir, opts)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error(), "action": "remove"})
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "%s sila skill remove: %v\n", ui.Red("✖"), err)
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(map[string]any{
			"action":                "remove",
			"harness":               opts.Harness,
			"target_path":           res.TargetPath,
			"status":                res.Status,
			"file_deleted":          res.FileDeleted,
			"parent_dir_removed":    res.ParentDirRemoved,
			"user_entries_restored": res.UserEntriesRestored,
			"orphan_files":          res.OrphanFiles,
		})
		return
	}

	if toonOut {
		fmt.Fprintf(os.Stdout, "skill_remove[target=%q,status=%q,deleted=%t,restored=%t]:\n",
			filepath.Base(res.TargetPath), res.Status, res.FileDeleted, res.UserEntriesRestored)
		return
	}

	if res.Status == "not_found" {
		fmt.Fprintf(os.Stdout, "No skill configuration found at %s (nothing to remove).\n", res.TargetPath)
		return
	}

	if res.UserEntriesRestored {
		fmt.Fprintf(os.Stdout, "%s sila · Sila-managed skill configuration removed (restored user configuration)\n\n", ui.Green("✔"))
		fmt.Fprintf(os.Stdout, "  Target:              %s (restored)\n", res.TargetPath)
		fmt.Fprintf(os.Stdout, "  User Entries:        restored successfully\n")
		fmt.Fprintf(os.Stdout, "  Orphan Files:        0 .tmp or .lock files remaining\n")
		return
	}

	fmt.Fprintf(os.Stdout, "%s sila · project skill configuration removed cleanly\n\n", ui.Green("✔"))
	fmt.Fprintf(os.Stdout, "  Target:              %s (deleted)\n", res.TargetPath)
	if res.ParentDirRemoved {
		fmt.Fprintf(os.Stdout, "  Parent Directory:    %s/ (pruned, directory was empty)\n", filepath.Base(filepath.Dir(res.TargetPath)))
	} else {
		fmt.Fprintf(os.Stdout, "  Parent Directory:    %s/ (preserved, directory not empty)\n", filepath.Base(filepath.Dir(res.TargetPath)))
	}
	fmt.Fprintf(os.Stdout, "  Orphan Files:        0 .tmp or .lock files remaining\n")
}
