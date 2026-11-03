package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

// runGoalsReleaseStale releases stale claims in one project or across every
// registered project. A live process is never released.
func runGoalsReleaseStale(ui render.UI, dbPath, targetDir string, isGlobal bool, olderThan string, dryRun, jsonOut bool) {
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	threshold := parseOlderThan(olderThan)
	var all []goals.StaleRelease
	for _, dir := range hygieneProjectDirs(st, targetDir, isGlobal) {
		released, err := goals.ReleaseStaleClaims(dir, threshold, dryRun)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila goals release --stale: %v\n", err)
			os.Exit(1)
		}
		all = append(all, released...)
	}

	if jsonOut {
		emitJSON(map[string]any{"dry_run": dryRun, "older_than": threshold.String(), "count": len(all), "released": all})
		return
	}
	if len(all) == 0 {
		fmt.Fprintf(os.Stdout, "%s No stale claims older than %s.\n", ui.Green("✔"), threshold)
		return
	}
	action := "Released"
	if dryRun {
		action = "Would release"
	}
	fmt.Fprintf(os.Stdout, "%s %s %d stale %s (older than %s):\n\n", ui.Yellow("▲"), action, len(all), plural(int64(len(all)), "claim"), threshold)
	headers := []string{"project", "goal", "agent", "age", "reason"}
	rows := make([][]string, 0, len(all))
	for _, r := range all {
		agent := r.Agent
		if agent == "" {
			agent = "agent"
		}
		rows = append(rows, []string{r.Project, r.Goal, agent, r.Age, r.Reason})
	}
	fmt.Fprintln(os.Stdout, ui.Table(headers, rows, []string{"l", "l", "l", "r", "l"}))
	if dryRun {
		fmt.Fprintln(os.Stdout, ui.Dim("Dry run: nothing was released. Re-run without --dry-run to release these claims."))
	}
}

// runGoalsPruneClosed removes claim records left on goals that already closed.
func runGoalsPruneClosed(ui render.UI, dbPath, targetDir string, isGlobal, dryRun, jsonOut bool) {
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	type entry struct {
		Project string `json:"project"`
		Path    string `json:"path"`
	}
	var all []entry
	for _, dir := range hygieneProjectDirs(st, targetDir, isGlobal) {
		paths, err := goals.PruneClosedClaims(dir, dryRun)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila goals prune --claims: %v\n", err)
			os.Exit(1)
		}
		project := filepath.Base(filepath.Clean(dir))
		for _, p := range paths {
			all = append(all, entry{Project: project, Path: p})
		}
	}

	if jsonOut {
		emitJSON(map[string]any{"dry_run": dryRun, "count": len(all), "pruned": all})
		return
	}
	if len(all) == 0 {
		fmt.Fprintf(os.Stdout, "%s No claim records left on closed goals.\n", ui.Green("✔"))
		return
	}
	action := "Removed"
	if dryRun {
		action = "Would remove"
	}
	fmt.Fprintf(os.Stdout, "%s %s %d claim %s on closed goals:\n\n", ui.Yellow("▲"), action, len(all), plural(int64(len(all)), "record"))
	headers := []string{"project", "claim"}
	rows := make([][]string, 0, len(all))
	for _, e := range all {
		rows = append(rows, []string{e.Project, e.Path})
	}
	fmt.Fprintln(os.Stdout, ui.Table(headers, rows, []string{"l", "l"}))
	if dryRun {
		fmt.Fprintln(os.Stdout, ui.Dim("Dry run: nothing was removed."))
	}
}

// hygieneProjectDirs resolves the directories a hygiene command should touch.
func hygieneProjectDirs(st *store.Store, targetDir string, isGlobal bool) []string {
	if !isGlobal {
		if targetDir == "" {
			cwd, _ := os.Getwd()
			targetDir = cwd
		}
		return []string{targetDir}
	}
	projects, err := st.Projects()
	if err != nil {
		return nil
	}
	var dirs []string
	for _, p := range projects {
		if p.Path == "" || p.ID == "no-project" {
			continue
		}
		if fi, err := os.Stat(filepath.Join(p.Path, "goals")); err == nil && fi.IsDir() {
			dirs = append(dirs, p.Path)
		}
	}
	sort.Strings(dirs)
	return dirs
}

// parseOlderThan turns an age expression such as 72h or 3d into a duration.
func parseOlderThan(s string) time.Duration {
	if strings.TrimSpace(s) == "" {
		return 72 * time.Hour
	}
	if t := parseSince(s); !t.IsZero() {
		if d := time.Since(t); d > 0 {
			return d
		}
	}
	return 72 * time.Hour
}
