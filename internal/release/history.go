package release

import (
	"bufio"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/version"
)

// ReleaseEntry represents a parsed release section from CHANGELOG.md.
type ReleaseEntry struct {
	Version  string              `json:"version"`
	Date     string              `json:"date,omitempty"`
	URL      string              `json:"url,omitempty"`
	Breaking []string            `json:"breaking,omitempty"`
	Features []string            `json:"features,omitempty"`
	Fixes    []string            `json:"fixes,omitempty"`
	Other    []string            `json:"other,omitempty"`
	Details  map[string][]string `json:"details,omitempty"`
}

// ReleaseHistoryReport represents the parsed release history ready for display or JSON output.
type ReleaseHistoryReport struct {
	CurrentVersion string         `json:"current_version"`
	TotalReleases  int            `json:"total_releases"`
	Releases       []ReleaseEntry `json:"releases"`
}

// HistoryRenderOptions controls the density and progressive disclosure of the changelog output.
type HistoryRenderOptions struct {
	Full          bool
	Timeline      bool
	TargetVersion string
	MaxFeatures   int
	MaxFixes      int
	MaxOther      int
}

// DefaultHistoryRenderOptions returns clean, executive defaults that fit on one standard screen.
func DefaultHistoryRenderOptions() HistoryRenderOptions {
	return HistoryRenderOptions{
		MaxFeatures: 4,
		MaxFixes:    3,
		MaxOther:    2,
	}
}

var (
	releaseHeaderRegex   = regexp.MustCompile(`^##\s+\[?([^\]\s]+)\]?(?:\(([^\)]+)\))?\s*(?:[-–—]\s*)?(?:\(([^)]+)\)|([0-9]{4}-[0-9]{2}-[0-9]{2}))?`)
	commitHashStripRegex = regexp.MustCompile(`\s*\(\[[0-9a-fA-F]+\]\([^\)]+\)\)|\s*\(` + "`[0-9a-fA-F]{7,40}`" + `\)|\s*` + "`[0-9a-fA-F]{7,40}`")
)

// ParseReleases parses a Keep a Changelog formatted markdown string into a slice of ReleaseEntry objects.
// It separates top-level release headlines from indented sub-bullets, preventing line explosion.
func ParseReleases(content string) ([]ReleaseEntry, error) {
	var releases []ReleaseEntry
	var current *ReleaseEntry
	var currentSection string
	var lastHeadline string

	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		rawLine := scanner.Text()
		trimmed := strings.TrimSpace(rawLine)
		if trimmed == "" {
			continue
		}

		// Detect Release header: e.g. ## [0.11.0] - 2026-10-04 or ## [Unreleased]
		if strings.HasPrefix(trimmed, "## ") {
			if current != nil && (len(current.Breaking) > 0 || len(current.Features) > 0 || len(current.Fixes) > 0 || len(current.Other) > 0) {
				releases = append(releases, *current)
			}

			matches := releaseHeaderRegex.FindStringSubmatch(trimmed)
			if len(matches) > 1 {
				v := matches[1]
				url := ""
				date := ""
				if len(matches) > 2 {
					url = matches[2]
				}
				if len(matches) > 3 && matches[3] != "" {
					date = matches[3]
				} else if len(matches) > 4 && matches[4] != "" {
					date = matches[4]
				}
				current = &ReleaseEntry{
					Version: v,
					URL:     url,
					Date:    date,
					Details: make(map[string][]string),
				}
				currentSection = ""
				lastHeadline = ""
				continue
			}
		}

		if current == nil {
			continue
		}

		// Detect section headers: ### Added, ### Features, ### Bug Fixes, etc.
		if strings.HasPrefix(trimmed, "### ") {
			secTitle := strings.ToLower(strings.TrimPrefix(trimmed, "### "))
			switch {
			case strings.Contains(secTitle, "break"):
				currentSection = "breaking"
			case strings.Contains(secTitle, "feature") || strings.Contains(secTitle, "add"):
				currentSection = "features"
			case strings.Contains(secTitle, "fix"):
				currentSection = "fixes"
			default:
				currentSection = "other"
			}
			lastHeadline = ""
			continue
		}

		// Check if line is a bullet item (* or -)
		isBullet := strings.HasPrefix(trimmed, "* ") || strings.HasPrefix(trimmed, "- ")
		if !isBullet {
			continue
		}

		// Check indentation to separate top-level headlines from nested sub-bullets
		indent := len(rawLine) - len(strings.TrimLeft(rawLine, " \t"))

		bullet := strings.TrimSpace(trimmed[2:])
		// Strip markdown commit link or hash at the end
		bullet = commitHashStripRegex.ReplaceAllString(bullet, "")
		bullet = strings.TrimSpace(bullet)
		if bullet == "" {
			continue
		}

		// Clean markdown bold prefix: **scope:** to scope:
		if strings.HasPrefix(bullet, "**") {
			if idx := strings.Index(bullet[2:], "**"); idx != -1 {
				scope := bullet[2 : 2+idx]
				rest := strings.TrimSpace(bullet[2+idx+2:])
				bullet = scope + " " + rest
			}
		}

		// If indented, attach as a detail to the previous top-level headline
		if indent > 0 && lastHeadline != "" {
			current.Details[lastHeadline] = append(current.Details[lastHeadline], bullet)
			continue
		}

		// Top-level bullet: clean trailing colon
		bullet = strings.TrimSuffix(bullet, ":")
		bullet = strings.TrimSpace(bullet)
		lastHeadline = bullet

		// Check for breaking changes prefix
		lower := strings.ToLower(bullet)
		if strings.HasPrefix(lower, "breaking:") || strings.HasPrefix(lower, "breaking -") {
			trimmedBreaking := strings.TrimSpace(bullet[len("breaking:"):])
			current.Breaking = append(current.Breaking, trimmedBreaking)
			continue
		}

		switch currentSection {
		case "breaking":
			current.Breaking = append(current.Breaking, bullet)
		case "features":
			current.Features = append(current.Features, bullet)
		case "fixes":
			current.Fixes = append(current.Fixes, bullet)
		case "other":
			current.Other = append(current.Other, bullet)
		default:
			// Unsectioned top-level bullets
			if strings.HasPrefix(lower, "fix") || strings.HasPrefix(lower, "eliminate") || strings.HasPrefix(lower, "prevent") || strings.HasPrefix(lower, "resolve") {
				current.Fixes = append(current.Fixes, bullet)
			} else {
				current.Features = append(current.Features, bullet)
			}
		}
	}

	if current != nil && (len(current.Breaking) > 0 || len(current.Features) > 0 || len(current.Fixes) > 0 || len(current.Other) > 0) {
		releases = append(releases, *current)
	}

	return releases, scanner.Err()
}

// GetReleaseHistory parses Sila's changelog and returns a ReleaseHistoryReport.
// If targetVersion is non-empty, it filters exclusively to that release.
func GetReleaseHistory(limit int, targetVersion string) ReleaseHistoryReport {
	raw := LoadChangelogContent()
	all, _ := ParseReleases(raw)

	total := len(all)
	if targetVersion != "" {
		norm := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(targetVersion)), "v")
		for _, r := range all {
			rNorm := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(r.Version)), "v")
			if rNorm == norm || strings.EqualFold(r.Version, targetVersion) {
				return ReleaseHistoryReport{
					CurrentVersion: version.Version,
					TotalReleases:  total,
					Releases:       []ReleaseEntry{r},
				}
			}
		}
		// Not found: return empty report
		return ReleaseHistoryReport{
			CurrentVersion: version.Version,
			TotalReleases:  total,
			Releases:       nil,
		}
	}

	selected := all
	if limit > 0 && limit < len(all) {
		selected = all[:limit]
	}

	return ReleaseHistoryReport{
		CurrentVersion: version.Version,
		TotalReleases:  total,
		Releases:       selected,
	}
}

// RenderTimeline renders a concise, high-signal tabular timeline of all releases.
func RenderTimeline(rep ReleaseHistoryReport, ui render.UI) string {
	var sb strings.Builder
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("  %s %s %s\n\n",
		ui.Orange(ui.Bold("Sila")),
		ui.Dim("·"),
		ui.Bold(fmt.Sprintf("release timeline (%d releases)", rep.TotalReleases)),
	))

	headers := []string{"RELEASE", "DATE", "BREAKING", "FEATURES", "FIXES", "KEY HIGHLIGHTS"}
	var rows [][]string

	for _, r := range rep.Releases {
		v := r.Version
		if v != "Unreleased" && !strings.HasPrefix(v, "v") {
			v = "v" + v
		}
		date := r.Date
		if date == "" {
			date = "-"
		}
		breakingStr := fmt.Sprintf("%d", len(r.Breaking))
		if len(r.Breaking) == 0 {
			breakingStr = "-"
		}
		featStr := fmt.Sprintf("%d", len(r.Features))
		fixStr := fmt.Sprintf("%d", len(r.Fixes))

		// Highlight summary
		var highlights []string
		for i := 0; i < len(r.Features) && i < 2; i++ {
			f := r.Features[i]
			if idx := strings.Index(f, ":"); idx > 0 && idx < 45 {
				f = f[:idx]
			}
			f = strings.TrimSpace(f)
			if len(f) > 36 {
				f = f[:33] + "..."
			}
			highlights = append(highlights, f)
		}
		hlText := strings.Join(highlights, ", ")
		if hlText == "" && len(r.Fixes) > 0 {
			hlText = r.Fixes[0]
			if len(hlText) > 36 {
				hlText = hlText[:33] + "..."
			}
		}

		rows = append(rows, []string{v, date, breakingStr, featStr, fixStr, hlText})
	}

	aligns := []string{"l", "l", "r", "r", "r", "l"}
	sb.WriteString(ui.Table(headers, rows, aligns))
	sb.WriteString("\n\n")
	sb.WriteString(fmt.Sprintf("  %s\n\n",
		ui.Dim("Tip: run 'sila changelog <version>' to view a release card, or 'sila changelog --full' for details"),
	))
	return sb.String()
}

// RenderReleaseHistory formats the Sila release history report for terminal display.
func RenderReleaseHistory(rep ReleaseHistoryReport, ui render.UI, optArgs ...HistoryRenderOptions) string {
	opts := DefaultHistoryRenderOptions()
	if len(optArgs) > 0 {
		opts = optArgs[0]
	}

	if opts.Timeline {
		return RenderTimeline(rep, ui)
	}

	if opts.TargetVersion != "" && len(rep.Releases) == 0 {
		var sb strings.Builder
		sb.WriteString("\n")
		sb.WriteString(fmt.Sprintf("  %s %s release %q not found in embedded changelog.\n\n",
			ui.Red("Error:"),
			ui.Bold("sila changelog"),
			opts.TargetVersion,
		))
		sb.WriteString(fmt.Sprintf("  %s\n\n",
			ui.Dim("Run 'sila changelog --timeline' to see all available releases."),
		))
		return sb.String()
	}

	var sb strings.Builder
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("  %s %s %s\n\n",
		ui.Orange(ui.Bold("Sila")),
		ui.Dim("·"),
		ui.Bold("changelog · release history & new features"),
	))

	curVer := rep.CurrentVersion
	if curVer == "" || curVer == "dev" {
		curVer = "dev (source build)"
	} else if !strings.HasPrefix(curVer, "v") {
		curVer = "v" + curVer
	}

	shown := len(rep.Releases)
	if opts.TargetVersion != "" && shown == 1 {
		sb.WriteString(fmt.Sprintf("  Current: %s  ·  Release: %s (of %d releases)\n\n",
			ui.Cyan(ui.Bold(curVer)),
			ui.Yellow(ui.Bold(rep.Releases[0].Version)),
			rep.TotalReleases,
		))
	} else {
		sb.WriteString(fmt.Sprintf("  Current: %s  ·  Showing %d of %d releases\n\n",
			ui.Cyan(ui.Bold(curVer)),
			shown,
			rep.TotalReleases,
		))
	}

	if len(rep.Releases) == 0 {
		sb.WriteString("  " + ui.Dim("No changelog entries found.") + "\n\n")
		return sb.String()
	}

	cardWidth := render.BoundedCardWidth(2)
	innerWidth := cardWidth - 4

	for i, rel := range rep.Releases {
		var lines []string

		// 1. Breaking Changes (always all)
		if len(rel.Breaking) > 0 {
			lines = append(lines, " "+ui.Red("Breaking Changes:"))
			for _, b := range rel.Breaking {
				lines = append(lines, render.WrapBullet("  • ", b, innerWidth, func(s string) string { return ui.Red(s) })...)
				if opts.Full && len(rel.Details[b]) > 0 {
					for _, d := range rel.Details[b] {
						lines = append(lines, render.WrapBullet("      - ", d, innerWidth, func(s string) string { return ui.Dim(s) })...)
					}
				}
			}
			lines = append(lines, "")
		}

		// 2. Features / Added (budgeted unless --full)
		if len(rel.Features) > 0 {
			lines = append(lines, " "+ui.Green("Features:"))
			count := len(rel.Features)
			limit := count
			if !opts.Full && opts.MaxFeatures > 0 && count > opts.MaxFeatures {
				limit = opts.MaxFeatures
			}
			for j := 0; j < limit; j++ {
				feat := rel.Features[j]
				lines = append(lines, render.WrapBullet("  • ", feat, innerWidth)...)
				if opts.Full && len(rel.Details[feat]) > 0 {
					for _, d := range rel.Details[feat] {
						lines = append(lines, render.WrapBullet("      - ", d, innerWidth, func(s string) string { return ui.Dim(s) })...)
					}
				}
			}
			if limit < count {
				rem := count - limit
				relRef := rel.Version
				if relRef != "Unreleased" && !strings.HasPrefix(relRef, "v") {
					relRef = "v" + relRef
				}
				lines = append(lines, " "+ui.Dim(fmt.Sprintf("  • … and %d more features (run 'sila changelog %s' or '--full')", rem, relRef)))
			}
		}

		// 3. Bug Fixes (budgeted unless --full)
		if len(rel.Fixes) > 0 {
			if len(rel.Features) > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, " "+ui.Dim("Bug Fixes:"))
			count := len(rel.Fixes)
			limit := count
			if !opts.Full && opts.MaxFixes > 0 && count > opts.MaxFixes {
				limit = opts.MaxFixes
			}
			for j := 0; j < limit; j++ {
				fix := rel.Fixes[j]
				lines = append(lines, render.WrapBullet("  • ", fix, innerWidth, func(s string) string { return ui.Dim(s) })...)
				if opts.Full && len(rel.Details[fix]) > 0 {
					for _, d := range rel.Details[fix] {
						lines = append(lines, render.WrapBullet("      - ", d, innerWidth, func(s string) string { return ui.Dim(s) })...)
					}
				}
			}
			if limit < count {
				rem := count - limit
				lines = append(lines, " "+ui.Dim(fmt.Sprintf("  • … and %d more bug fixes", rem)))
			}
		}

		// 4. Other / Changes (budgeted unless --full)
		if len(rel.Other) > 0 {
			if len(rel.Features) > 0 || len(rel.Fixes) > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, " "+ui.Dim("Changes:"))
			count := len(rel.Other)
			limit := count
			if !opts.Full && opts.MaxOther > 0 && count > opts.MaxOther {
				limit = opts.MaxOther
			}
			for j := 0; j < limit; j++ {
				o := rel.Other[j]
				lines = append(lines, render.WrapBullet("  • ", o, innerWidth)...)
				if opts.Full && len(rel.Details[o]) > 0 {
					for _, d := range rel.Details[o] {
						lines = append(lines, render.WrapBullet("      - ", d, innerWidth, func(s string) string { return ui.Dim(s) })...)
					}
				}
			}
			if limit < count {
				rem := count - limit
				lines = append(lines, " "+ui.Dim(fmt.Sprintf("  • … and %d more changes", rem)))
			}
		}

		// Clean up trailing empty line if any
		for len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}

		title := rel.Version
		if title != "Unreleased" && !strings.HasPrefix(title, "v") {
			title = "v" + title
		}

		titleColor := render.Cyan
		if i == 0 {
			titleColor = render.Orange
		}

		sb.WriteString(render.RenderBox(render.CardOptions{
			Title:       title,
			RightHeader: rel.Date,
			Lines:       lines,
			Width:       cardWidth,
			Indent:      2,
			UI:          ui,
			TitleColor:  titleColor,
			BorderColor: render.Dim,
		}))
		sb.WriteString("\n")
	}

	if opts.TargetVersion == "" && shown < rep.TotalReleases {
		remainder := rep.TotalReleases - shown
		sb.WriteString(fmt.Sprintf("  %s\n\n",
			ui.Dim(fmt.Sprintf("… and %d more releases (run 'sila changelog --timeline' or --top %d)", remainder, rep.TotalReleases)),
		))
	}

	return sb.String()
}

// RenderReleaseHistoryJSON formats the release history as indented JSON.
func RenderReleaseHistoryJSON(rep ReleaseHistoryReport) string {
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(b)
}
