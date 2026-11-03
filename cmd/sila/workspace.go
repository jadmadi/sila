package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

// runWorkspace reports the active workspace identity used by prime, salam,
// goals, MCP, and hooks. It exits 2 when the workspace is ambiguous.
func runWorkspace(ui render.UI, project string, jsonOut bool, args []string) int {
	if len(args) == 0 || args[0] != "status" {
		fmt.Fprintln(os.Stderr, "usage: sila workspace status [--json] [--project=<id|path>]")
		return 2
	}

	base := project
	if base == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila workspace: %v\n", err)
			return 1
		}
		base = cwd
	}

	identity, err := store.ResolveWorkspaceIdentity(base)
	if err != nil && !errors.Is(err, store.ErrAmbiguousWorkspace) {
		fmt.Fprintf(os.Stderr, "sila workspace: %v\n", err)
		return 1
	}

	if jsonOut {
		emitJSON(identity)
		if identity.Ambiguous {
			return 2
		}
		return 0
	}

	fmt.Fprintf(os.Stdout, "%s\n", ui.Cyan(ui.Bold("sila · workspace")))
	fmt.Fprintf(os.Stdout, "  Root:    %s\n", identity.WorkspaceRoot)
	fmt.Fprintf(os.Stdout, "  Active:  %s\n", identity.ActiveDir)
	fmt.Fprintf(os.Stdout, "  Project: %s\n", identity.ProjectID)
	fmt.Fprintf(os.Stdout, "  Source:  %s\n", identity.Source)

	if identity.Ambiguous {
		fmt.Fprintf(os.Stdout, "\n  %s\n", ui.Yellow("Ambiguous workspace: multiple child repositories own goals."))
		for _, candidate := range identity.Candidates {
			candidateID := store.ProjectID(candidate)
			fmt.Fprintf(os.Stdout, "    • %s (%s)\n", candidate, candidateID)
			fmt.Fprintf(os.Stdout, "      sila prime --project=%s\n", candidateID)
		}
		return 2
	}

	if identity.Source == "preferred-child" || identity.Source == "single-child" {
		fmt.Fprintf(os.Stdout, "\n  %s\n", ui.Dim("Run commands from the workspace root or pass --project to override."))
	}
	return 0
}
