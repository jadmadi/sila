package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/git"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

// runWorktree implements `sila worktree [create|list|clean|run]`.
// It manages disposable Git worktrees for isolated agent sessions.
func runWorktree(ui render.UI, dbPath, project string, jsonOut, toonOut bool, args []string) {
	if len(args) == 0 {
		printWorktreeHelp(ui)
		os.Exit(2)
	}

	cwd, _ := os.Getwd()
	mgr, err := git.NewManager(cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila worktree: %v\n", err)
		os.Exit(1)
	}

	sub := args[0]
	rest := args[1:]

	switch sub {
	case "create":
		runWorktreeCreate(ui, mgr, dbPath, project, rest)
	case "list":
		runWorktreeList(ui, mgr, jsonOut, toonOut)
	case "clean":
		runWorktreeClean(ui, mgr, rest)
	case "run":
		runWorktreeRun(ui, mgr, rest)
	case "pilot":
		runWorktreePilot(ui, mgr, jsonOut, toonOut, rest)
	default:
		fmt.Fprintf(os.Stderr, "sila worktree: unknown subcommand %q (expected create, list, clean, run, or pilot)\n", sub)
		os.Exit(2)
	}
}

func runWorktreeCreate(ui render.UI, mgr *git.Manager, dbPath, project string, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "sila worktree create: name is required")
		os.Exit(2)
	}
	name := args[0]
	baseRef := ""
	if len(args) > 1 {
		baseRef = args[1]
	}

	wt, err := mgr.Create(name, baseRef)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila worktree create: %v\n", err)
		os.Exit(1)
	}

	// Seed context.
	projectID := project
	if projectID == "" {
		projectID = store.ProjectID(mgr.RepoRoot())
	}
	handoffSummary := ""
	if st, err := store.OpenRO(dbPath); err == nil {
		if h, err := st.LatestHandoff(projectID); err == nil && h != nil && h.Summary != "" {
			handoffSummary = h.Summary
		}
		st.Close()
	}
	mgr.SeedContext(name, projectID, handoffSummary)

	fmt.Fprintf(os.Stdout, "%s Created worktree %s\n", ui.Green("✔"), ui.Bold(name))
	fmt.Fprintf(os.Stdout, "  %s path:   %s\n", ui.Dim("→"), wt.Path)
	fmt.Fprintf(os.Stdout, "  %s branch: %s\n", ui.Dim("→"), wt.Branch)
	fmt.Fprintf(os.Stdout, "  %s context: %s/SILA_CONTEXT.md\n", ui.Dim("→"), wt.Path)
	fmt.Fprintf(os.Stdout, "\n  %s cd %s && sila prime\n", ui.Dim("Next:"), wt.Path)
}

func runWorktreeList(ui render.UI, mgr *git.Manager, jsonOut, toonOut bool) {
	wts, err := mgr.List()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila worktree list: %v\n", err)
		os.Exit(1)
	}

	// Filter to sila-managed worktrees + primary.
	var filtered []git.Worktree
	for _, wt := range wts {
		if wt.IsPrimary || strings.Contains(wt.Path, "sila-worktrees") {
			filtered = append(filtered, wt)
		}
	}

	if jsonOut {
		emitJSON(filtered)
		return
	}

	if toonOut {
		fmt.Fprintf(os.Stdout, "worktrees[%d]{path,branch,head,primary}:\n", len(filtered))
		for _, wt := range filtered {
			primary := "no"
			if wt.IsPrimary {
				primary = "yes"
			}
			fmt.Fprintf(os.Stdout, "  %s,%s,%s,%s\n", wt.Path, wt.Branch, wt.Head[:min(7, len(wt.Head))], primary)
		}
		return
	}

	if len(filtered) == 0 {
		fmt.Fprintln(os.Stdout, ui.Dim("_No sila-managed worktrees. Run `sila worktree create <name>` to create one._"))
		return
	}

	fmt.Fprintf(os.Stdout, "%s sila · worktree list (%d)\n\n", ui.Cyan("·"), len(filtered))
	for _, wt := range filtered {
		marker := "  "
		label := wt.Path
		if wt.IsPrimary {
			marker = ui.Green("★")
			label = ui.Bold(label) + " " + ui.Dim("(primary)")
		}
		fmt.Fprintf(os.Stdout, "%s %s\n", marker, label)
		if wt.Branch != "" {
			fmt.Fprintf(os.Stdout, "    %s branch: %s\n", ui.Dim("→"), wt.Branch)
		}
		if wt.Head != "" {
			head := wt.Head
			if len(head) > 7 {
				head = head[:7]
			}
			fmt.Fprintf(os.Stdout, "    %s head:   %s\n", ui.Dim("→"), head)
		}
		if wt.Locked != "" {
			fmt.Fprintf(os.Stdout, "    %s locked: %s\n", ui.Dim("→"), wt.Locked)
		}
	}
}

func runWorktreeClean(ui render.UI, mgr *git.Manager, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "sila worktree clean: name is required (or use --all to clean all sila worktrees)")
		os.Exit(2)
	}

	if args[0] == "--all" || args[0] == "-a" {
		// Remove all sila-managed worktrees.
		wts, err := mgr.List()
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila worktree clean: %v\n", err)
			os.Exit(1)
		}
		count := 0
		for _, wt := range wts {
			if wt.IsPrimary || !strings.Contains(wt.Path, "sila-worktrees") {
				continue
			}
			if err := mgr.Remove(wt.Path, true); err != nil {
				fmt.Fprintf(os.Stderr, "  %s failed to remove %s: %v\n", ui.Red("✗"), wt.Path, err)
				continue
			}
			fmt.Fprintf(os.Stdout, "  %s removed %s\n", ui.Green("✔"), wt.Path)
			count++
		}
		mgr.Prune()
		fmt.Fprintf(os.Stdout, "\n%s Cleaned %d worktree(s).\n", ui.Green("✔"), count)
		return
	}

	name := args[0]
	if err := mgr.Remove(name, true); err != nil {
		fmt.Fprintf(os.Stderr, "sila worktree clean: %v\n", err)
		os.Exit(1)
	}
	mgr.Prune()
	fmt.Fprintf(os.Stdout, "%s Removed worktree %s\n", ui.Green("✔"), ui.Bold(name))
}

func runWorktreeRun(ui render.UI, mgr *git.Manager, args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "sila worktree run: usage: sila worktree run <name> <cmd> [args...]")
		os.Exit(2)
	}
	name := args[0]
	cmdName := args[1]
	cmdArgs := args[2:]

	// Verify worktree exists.
	wtPath := mgr.RepoRoot() + "/.git/sila-worktrees/" + name
	if _, err := os.Stat(wtPath); err != nil {
		// Try resolving as path.
		wts, _ := mgr.List()
		found := false
		for _, wt := range wts {
			if filepath.Base(wt.Path) == name || wt.Path == name {
				found = true
				break
			}
		}
		if !found {
			fmt.Fprintf(os.Stderr, "sila worktree run: worktree %q not found\n", name)
			os.Exit(1)
		}
	}

	fmt.Fprintf(os.Stdout, "%s Running in worktree %s: %s %s\n\n",
		ui.Cyan("·"), ui.Bold(name), cmdName, strings.Join(cmdArgs, " "))

	err := mgr.RunStreamed(name, cmdName, cmdArgs...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\n%s Command failed: %v\n", ui.Red("✗"), err)
		os.Exit(1)
	}

	// Run sila gate in the worktree if the command succeeded.
	fmt.Fprintf(os.Stdout, "\n%s Running sila gate in worktree...\n", ui.Dim("→"))
	gateOut, gateErr, gateErr2 := mgr.Run(name, "sila", "gate")
	if gateErr2 != nil {
		fmt.Fprintf(os.Stderr, "%s sila gate failed in worktree:\n%s\n%s\n", ui.Red("✗"), gateOut, gateErr)
	} else {
		fmt.Fprintf(os.Stdout, "%s sila gate passed in worktree.\n", ui.Green("✔"))
	}
}

func runWorktreePilot(ui render.UI, mgr *git.Manager, jsonOut, toonOut bool, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "sila worktree pilot: specify command to run in isolated pilot sandbox")
		fmt.Fprintln(os.Stdout, "Usage: sila worktree pilot [base-ref] <command> [args...]")
		os.Exit(2)
	}

	baseRef := "HEAD"
	cmdArgs := args
	// If first arg looks like a branch/commit ref, use it
	if len(args) > 1 && (args[0] == "HEAD" || strings.HasPrefix(args[0], "v") || strings.HasPrefix(args[0], "origin/")) {
		baseRef = args[0]
		cmdArgs = args[1:]
	}

	cmdName := cmdArgs[0]
	var subArgs []string
	if len(cmdArgs) > 1 {
		subArgs = cmdArgs[1:]
	}

	pilotName := fmt.Sprintf("pilot-%d", time.Now().UnixNano()%1000000)
	timeout := 30 * time.Second

	fmt.Fprintf(os.Stdout, "%s Starting 30s sandboxed pilot trial: %s %s\n",
		ui.Cyan("·"), ui.Bold(cmdName), strings.Join(subArgs, " "))

	res, err := mgr.PilotRun(pilotName, baseRef, timeout, cmdName, subArgs...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s Pilot run failed: %v\n", ui.Red("✗"), err)
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(res)
		return
	}

	if toonOut {
		status := "FAIL"
		if res.Success {
			status = "PASS"
		}
		rows := [][]string{
			{res.Name, status, fmt.Sprintf("%d", res.ExitCode), fmt.Sprintf("%.2fs", res.Duration.Seconds()), res.Command},
		}
		fmt.Fprint(os.Stdout, render.FormatTOONTable("worktree_pilot", []string{"name", "status", "exit_code", "duration", "command"}, rows))
		return
	}

	if res.Success {
		fmt.Fprintf(os.Stdout, "%s Pilot run %s in %.2fs (exit code %d)\n",
			ui.Green("✔"), ui.Green("PASSED"), res.Duration.Seconds(), res.ExitCode)
	} else {
		fmt.Fprintf(os.Stdout, "%s Pilot run %s in %.2fs (exit code %d)\n",
			ui.Red("✖"), ui.Red("FAILED"), res.Duration.Seconds(), res.ExitCode)
	}

	if len(res.Stdout) > 0 {
		fmt.Fprintf(os.Stdout, "\n%s:\n%s\n", ui.Dim("Stdout"), strings.TrimSpace(res.Stdout))
	}
	if len(res.Stderr) > 0 {
		fmt.Fprintf(os.Stdout, "\n%s:\n%s\n", ui.Yellow("Stderr"), strings.TrimSpace(res.Stderr))
	}
	fmt.Fprintf(os.Stdout, "%s Sandboxed worktree cleaned up.\n", ui.Dim("✔"))
}

func printWorktreeHelp(ui render.UI) {
	fmt.Fprintln(os.Stderr, ui.Cyan(ui.Bold("sila worktree"))+" — disposable Git worktree pool & agent sandbox")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintln(os.Stderr, "  sila worktree create <name> [base-ref]   Create an isolated worktree on branch sila/<name>")
	fmt.Fprintln(os.Stderr, "  sila worktree list [--toon|--json]        List sila-managed worktrees")
	fmt.Fprintln(os.Stderr, "  sila worktree clean <name> | --all        Remove a worktree and its branch")
	fmt.Fprintln(os.Stderr, "  sila worktree run <name> <cmd> [args...]  Execute a command in the worktree, then run sila gate")
	fmt.Fprintln(os.Stderr, "  sila worktree pilot [ref] <cmd> [args...] Run time-bounded trial in a disposable sandbox")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Worktrees are stored under .git/sila-worktrees/<name> with independent branch state.")
	fmt.Fprintln(os.Stderr, "Each worktree is seeded with a SILA_CONTEXT.md briefing for agent startup.")
}
