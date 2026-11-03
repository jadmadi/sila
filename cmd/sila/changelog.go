package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/jadmadi/sila/internal/release"
	"github.com/jadmadi/sila/internal/render"
)

type changelogOptions struct {
	projectFlag    string
	tagFlag        string
	writeFlag      bool
	unreleasedFlag bool
	scanFlag       bool
	top            int
	topSpecified   bool
	allReleases    bool
	timeline       bool
	full           bool
	targetVersion  string
	jsonOut        bool
	toonOut        bool
}

// runChangelog implements `sila changelog [version] [--top=N] [--all] [--timeline] [--full] [--json] [--toon] [--scan] [--write] [--tag-range=<v>]`.
// By default, it displays Sila's own release history in high-craft, bounded terminal cards.
// With --scan, --write, or --tag-range, it scans the Git repository to generate or update repository release notes.
func runChangelog(ui render.UI, opts changelogOptions) {
	// If the user wants to scan git repository commits (e.g. for release preparation or custom range)
	if opts.writeFlag || opts.scanFlag || opts.tagFlag != "" || opts.unreleasedFlag {
		runRepoChangelog(ui, opts)
		return
	}

	// Default mode: display Sila's own release history from embedded/local CHANGELOG.md!
	limit := 2
	if opts.allReleases || opts.timeline {
		limit = 0
	} else if opts.topSpecified && opts.top > 0 {
		limit = opts.top
	}

	rep := release.GetReleaseHistory(limit, opts.targetVersion)

	if opts.jsonOut {
		fmt.Println(release.RenderReleaseHistoryJSON(rep))
		return
	}

	if opts.toonOut {
		head := render.FormatTOONObject("sila_releases", [][2]string{
			{"current_version", render.EscapeTOONValue(rep.CurrentVersion)},
			{"total_releases", fmt.Sprint(rep.TotalReleases)},
			{"showing", fmt.Sprint(len(rep.Releases))},
		})
		rows := make([][]string, 0, len(rep.Releases))
		for _, r := range rep.Releases {
			featuresCount := fmt.Sprint(len(r.Features))
			fixesCount := fmt.Sprint(len(r.Fixes))
			breakingCount := fmt.Sprint(len(r.Breaking))
			rows = append(rows, []string{r.Version, r.Date, breakingCount, featuresCount, fixesCount})
		}
		table := render.FormatTOONTable("releases", []string{"version", "date", "breaking", "features", "fixes"}, rows)
		fmt.Fprint(os.Stdout, head+table)
		return
	}

	renderOpts := release.DefaultHistoryRenderOptions()
	renderOpts.Full = opts.full
	renderOpts.Timeline = opts.timeline
	renderOpts.TargetVersion = opts.targetVersion
	if opts.targetVersion != "" && !opts.full {
		// When inspecting a single release, give a slightly higher section budget by default
		renderOpts.MaxFeatures = 8
		renderOpts.MaxFixes = 6
		renderOpts.MaxOther = 4
	}

	fmt.Print(release.RenderReleaseHistory(rep, ui, renderOpts))
}

func runRepoChangelog(ui render.UI, opts changelogOptions) {
	if msg := changelogFlagConflict(opts.writeFlag, opts.jsonOut, opts.toonOut); msg != "" {
		fmt.Fprintf(os.Stderr, "sila changelog: %s\n", msg)
		os.Exit(2)
	}

	targetDir := opts.projectFlag
	if targetDir == "" {
		targetDir, _ = os.Getwd()
	}
	repoDir := release.ResolveGitRepoDir(targetDir)

	tagRange := opts.tagFlag
	if opts.unreleasedFlag || tagRange == "" {
		tagRange = "" // empty = last 100 commits
	}

	content, report, err := release.GenerateChangelog(repoDir, tagRange)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila changelog: %v\n", err)
		os.Exit(1)
	}

	if opts.jsonOut {
		emitJSON(report)
		return
	}

	if opts.toonOut {
		fmt.Fprint(os.Stdout, release.RenderChangelogTOON(report))
		return
	}

	if opts.writeFlag {
		changelogPath := filepath.Join(repoDir, "CHANGELOG.md")
		updated, err := release.WriteChangelogToFile(changelogPath, content)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila changelog --write: %v\n", err)
			os.Exit(1)
		}
		newLines := len(updated) - len(content)
		fmt.Fprintln(os.Stdout, ui.Green("✔")+" Updated CHANGELOG.md with "+ui.Bold(fmt.Sprintf("%d commits", len(report.Commits)))+" across "+ui.Bold(fmt.Sprintf("%d categories", countCategories(report))))
		if newLines > 0 {
			fmt.Fprintf(os.Stdout, "  %s %d new lines added (idempotent — duplicates skipped).\n", ui.Dim("→"), newLines)
		} else {
			fmt.Fprintf(os.Stdout, "  %s All commits already present — no changes needed.\n", ui.Dim("→"))
		}
		return
	}

	// Dry-run mode: print to stdout
	if content == "" {
		fmt.Fprintln(os.Stdout, ui.Dim("No commits to categorize."))
		return
	}
	fmt.Print(content)
}

// countCategories returns the number of non-empty categories in a report.
func countCategories(r *release.ChangelogReport) int {
	if r == nil {
		return 0
	}
	count := 0
	for _, entries := range r.Groups {
		if len(entries) > 0 {
			count++
		}
	}
	return count
}

func changelogFlagConflict(write, json, toon bool) string {
	var count int
	if write {
		count++
	}
	if json {
		count++
	}
	if toon {
		count++
	}
	if count > 1 {
		return "--write, --json, and --toon are mutually exclusive; pick one"
	}
	return ""
}
