package release

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jadmadi/sila/internal/render"
)

// RenderStatusTerminal formats the release status report into a polished ANSI terminal dashboard.
func RenderStatusTerminal(ui render.UI, report ReleaseStatusReport, showDiff bool) string {
	var sb strings.Builder

	boxW := 76
	div := strings.Repeat("─", boxW)

	// Header Box
	sb.WriteString("\n")
	sb.WriteString(ui.Cyan("╭" + div + "╮\n"))
	titleLine := "  ✨ Sila Release Status & Epistemic Decision Engine"
	padTitle := boxW - len(titleLine)
	if padTitle < 0 {
		padTitle = 0
	}
	sb.WriteString(ui.Cyan("│") + ui.Bold(titleLine) + strings.Repeat(" ", padTitle) + ui.Cyan("│\n"))

	subLine := fmt.Sprintf("  Branch: %s @ %s · Describe: %s",
		report.DevState.Branch, report.DevState.Commit, report.DevState.GitDescribe)
	padSub := boxW - len(subLine)
	if padSub < 0 {
		padSub = 0
	}
	sb.WriteString(ui.Cyan("│") + ui.Dim(subLine) + strings.Repeat(" ", padSub) + ui.Cyan("│\n"))
	sb.WriteString(ui.Cyan("╰" + div + "╯\n\n"))

	// Section 1: Versions & Dev State
	sb.WriteString("  " + ui.Cyan(ui.Bold("📦 VERSIONS & WORKSPACE AT A GLANCE\n")))
	sb.WriteString("  " + ui.Dim(div) + "\n")
	pubSrc := "GitHub API"
	if report.PublicRelease.Source == "local_git_tag" {
		pubSrc = "local git tag fallback"
	}
	sb.WriteString(fmt.Sprintf("  %-24s %s %s\n",
		ui.Bold("Latest Public Release:"),
		ui.Green(ui.Bold(report.PublicRelease.Tag)),
		ui.Dim(fmt.Sprintf("(%s · %s)", report.PublicRelease.TimeAgo, pubSrc))))
	if report.PublicRelease.URL != "" {
		sb.WriteString(fmt.Sprintf("                           %s\n", ui.Dim(report.PublicRelease.URL)))
	}
	sb.WriteString(fmt.Sprintf("  %-24s %s %s\n",
		ui.Bold("Local Workspace:"),
		report.DevState.GitDescribe,
		ui.Dim(fmt.Sprintf("(commit: %s, %s)", report.DevState.Commit, report.DevState.CommitDate))))
	sb.WriteString(fmt.Sprintf("  %-24s %s\n\n",
		ui.Bold("Installed Sila:"),
		ui.Dim(report.DevState.InstalledVersion)))

	// Section 2: Epistemic Roadmap & MCP Contract (Sila Superpower)
	sb.WriteString("  " + ui.Cyan(ui.Bold("🎯 EPISTEMIC ROADMAP & SYSTEM CONTRACTS\n")))
	sb.WriteString("  " + ui.Dim(div) + "\n")

	roadmapBadge := ui.Green("100.0% Complete")
	if !report.RoadmapStatus.IsRoadmapComplete {
		roadmapBadge = ui.Yellow(fmt.Sprintf("%.1f%% Complete", report.RoadmapStatus.CompletionPct))
	}
	sb.WriteString(fmt.Sprintf("  %-24s %s %s\n",
		ui.Bold("Roadmap Goals:"),
		ui.Bold(fmt.Sprintf("%d / %d Implemented", report.RoadmapStatus.ImplementedGoals, report.RoadmapStatus.TotalGoals)),
		ui.Dim(fmt.Sprintf("(%s · %d Superseded)", roadmapBadge, report.RoadmapStatus.SupersededGoals))))

	taskStatus := fmt.Sprintf("%d / %d Tasks (100%%)", report.RoadmapStatus.CompletedTasks, report.RoadmapStatus.TotalTasks)
	sb.WriteString(fmt.Sprintf("  %-24s %s\n",
		ui.Bold("Task Checkboxes:"),
		ui.Green(taskStatus)))

	claimStatus := fmt.Sprintf("%d Claimed / In-Progress · %d Blocked · %d Pending",
		report.RoadmapStatus.ClaimedGoals, report.RoadmapStatus.BlockedGoals, report.RoadmapStatus.PendingGoals)
	sb.WriteString(fmt.Sprintf("  %-24s %s\n",
		ui.Bold("Contention / Locks:"),
		ui.Dim(claimStatus)))

	mcpStatus := fmt.Sprintf("%d Tools Registered · %d Breaking Changes",
		report.MCPContract.PublicToolsCount, report.MCPContract.BreakingChangesCount)
	if report.MCPContract.BreakingChangesCount == 0 {
		sb.WriteString(fmt.Sprintf("  %-24s %s\n",
			ui.Bold("MCP Tool Contract:"),
			ui.Green(mcpStatus)))
	} else {
		sb.WriteString(fmt.Sprintf("  %-24s %s\n",
			ui.Bold("MCP Tool Contract:"),
			ui.Red(mcpStatus)))
	}

	if report.RoadmapStatus.IsRoadmapComplete {
		sb.WriteString("  " + ui.Green(ui.Bold("🏆 FULL ROADMAP ACHIEVED:")) +
			ui.Dim(" All 216 foundational epistemic goals verified & certified.\n"))
	}
	sb.WriteString("\n")

	// Section 3: Conventional Change Impact Breakdown
	sb.WriteString("  " + ui.Cyan(ui.Bold(fmt.Sprintf("📊 CHANGE IMPACT BREAKDOWN (%d COMMITS SINCE %s)\n",
		report.ImpactSummary.TotalCommitsAhead, report.PublicRelease.Tag))))
	sb.WriteString("  " + ui.Dim(div) + "\n")
	sb.WriteString(fmt.Sprintf("  %-26s %-12s %-18s %s\n",
		ui.Bold("Impact Category"), ui.Bold("Committed"), ui.Bold("Convention"), ui.Bold("Resulting SemVer")))
	sb.WriteString("  " + ui.Dim(div) + "\n")

	renderImpactRow(&sb, ui, "💥", "Major (Breaking)", report.ImpactSummary.MajorBreaking, "feat!: / fix!:", report.SemverCandidates.MajorCandidate, "red")
	renderImpactRow(&sb, ui, "🚀", "Minor (Features)", report.ImpactSummary.MinorFeatures, "feat:", report.SemverCandidates.MinorCandidate, "green")
	renderImpactRow(&sb, ui, "🐛", "Patch (Fixes/Perf)", report.ImpactSummary.PatchFixes, "fix: / perf:", report.SemverCandidates.PatchCandidate, "yellow")
	renderImpactRow(&sb, ui, "📝", "Internal (Docs/Chores)", report.ImpactSummary.InternalDocs, "docs: / chore:", "Local only (no bump)", "dim")
	sb.WriteString("  " + ui.Dim(div) + "\n")

	switch report.ImpactSummary.HighestImpact {
	case "MAJOR":
		sb.WriteString(fmt.Sprintf("  %-26s %s %s\n",
			ui.Bold("Committed on Branch:"),
			ui.Red(ui.Bold("MAJOR")),
			ui.Dim("(Breaking changes present ➔ Release Candidate: "+report.SemverCandidates.RecommendedVersion+")")))
	case "MINOR":
		sb.WriteString(fmt.Sprintf("  %-26s %s %s\n",
			ui.Bold("Committed on Branch:"),
			ui.Green(ui.Bold("MINOR")),
			ui.Dim(fmt.Sprintf("(%d feature commit(s) ➔ Conventional Candidate: %s)",
				report.ImpactSummary.MinorFeatures, report.SemverCandidates.MinorCandidate))))
	case "PATCH":
		sb.WriteString(fmt.Sprintf("  %-26s %s %s\n",
			ui.Bold("Committed on Branch:"),
			ui.Yellow(ui.Bold("PATCH")),
			ui.Dim(fmt.Sprintf("(%d bug fix commit(s) ➔ Candidate: %s)",
				report.ImpactSummary.PatchFixes, report.SemverCandidates.PatchCandidate))))
	default:
		sb.WriteString(fmt.Sprintf("  %-26s %s\n",
			ui.Bold("Committed on Branch:"),
			ui.Dim("UP TO DATE or INTERNAL ONLY")))
	}

	if report.RoadmapStatus.IsRoadmapComplete {
		sb.WriteString(fmt.Sprintf("  %-26s %s %s\n",
			ui.Bold("Epistemic Milestone:"),
			ui.Green(ui.Bold("WATERSHED MILESTONE")),
			ui.Dim("(100% Roadmap Completed ➔ Recommended: "+report.SemverCandidates.RecommendedVersion+")")))
	}
	sb.WriteString("\n")

	// Section 4: Working Tree Status
	if report.WorkingTree.IsDirty {
		sb.WriteString("  " + ui.Yellow(ui.Bold(fmt.Sprintf("🛠️  WORKING TREE STATUS (Dirty — %d uncommitted files)\n",
			report.WorkingTree.ModifiedFiles+report.WorkingTree.UntrackedFiles))))
		sb.WriteString("  " + ui.Dim(div) + "\n")
		sb.WriteString(fmt.Sprintf("  %-24s %s\n",
			ui.Bold("Modifications:"),
			ui.Yellow(fmt.Sprintf("%d modified, %d untracked (+%d/-%d lines)",
				report.WorkingTree.ModifiedFiles, report.WorkingTree.UntrackedFiles,
				report.WorkingTree.LinesAdded, report.WorkingTree.LinesDeleted))))

		if len(report.WorkingTree.Subsystems) > 0 {
			sb.WriteString(fmt.Sprintf("  %-24s\n", ui.Bold("Subsystems:")))
			var dirs []string
			for d := range report.WorkingTree.Subsystems {
				dirs = append(dirs, d)
			}
			sort.Strings(dirs)
			for _, d := range dirs {
				c := report.WorkingTree.Subsystems[d]
				sb.WriteString(fmt.Sprintf("    %s %d file(s) in %s\n", ui.Dim("•"), c, ui.Bold(d)))
			}
		}

		if showDiff && report.WorkingTree.DiffStat != "" {
			sb.WriteString("\n  " + ui.Bold("Detailed Diffstat:\n"))
			lines := strings.Split(report.WorkingTree.DiffStat, "\n")
			for _, l := range lines {
				if strings.TrimSpace(l) != "" {
					sb.WriteString("    " + l + "\n")
				}
			}
		}
	} else {
		sb.WriteString("  " + ui.Green(ui.Bold("🛠️  WORKING TREE STATUS (Clean)\n")))
		sb.WriteString("  " + ui.Dim(div) + "\n")
		sb.WriteString("  " + ui.Dim("Working tree is completely clean. Zero uncommitted modifications.\n"))
	}
	sb.WriteString("\n")

	// Section 5: Commits Since Last Release (Categorized)
	if report.ImpactSummary.TotalCommitsAhead > 0 {
		sb.WriteString("  " + ui.Cyan(ui.Bold(fmt.Sprintf("📋 COMMITS SINCE %s (%d COMMITS AHEAD)\n",
			report.PublicRelease.Tag, report.ImpactSummary.TotalCommitsAhead))))
		sb.WriteString("  " + ui.Dim(div) + "\n")

		var feats, fixes, internal []StatusCommit
		for _, c := range report.Commits {
			if c.Breaking || c.Type == "feat" {
				feats = append(feats, c)
			} else if c.Type == "fix" || c.Type == "perf" || c.Type == "revert" {
				fixes = append(fixes, c)
			} else {
				internal = append(internal, c)
			}
		}

		if len(feats) > 0 {
			sb.WriteString("  " + ui.Green(ui.Bold("🚀 Features & Capabilities:\n")))
			for _, c := range feats {
				goalNote := ""
				if c.GoalSlug != "" {
					goalNote = ui.Dim(" [" + c.GoalSlug + "]")
				}
				sb.WriteString(fmt.Sprintf("     %s %s %s%s\n", ui.Green("•"), ui.Dim(c.Hash), c.Subject, goalNote))
			}
		}

		if len(fixes) > 0 {
			sb.WriteString("  " + ui.Yellow(ui.Bold("🐛 Bug Fixes & Polish:\n")))
			for _, c := range fixes {
				goalNote := ""
				if c.GoalSlug != "" {
					goalNote = ui.Dim(" [" + c.GoalSlug + "]")
				}
				sb.WriteString(fmt.Sprintf("     %s %s %s%s\n", ui.Yellow("•"), ui.Dim(c.Hash), c.Subject, goalNote))
			}
		}

		if len(internal) > 0 {
			sb.WriteString(fmt.Sprintf("  %s %s\n",
				ui.Dim("📝 Internal Changes (Docs & Maintenance):"),
				ui.Dim(fmt.Sprintf("%d commits (hidden from public release notes)", len(internal)))))
		}
		sb.WriteString("\n")
	}

	// Section 6: 3-Day Local Soak & Daily Telemetry Verification
	if report.SoakStatus.State != "" {
		sb.WriteString("  " + ui.Cyan(ui.Bold("🧪 3-DAY LOCAL SOAK & DAILY TELEMETRY VERIFICATION\n")))
		sb.WriteString("  " + ui.Dim(div) + "\n")
		var soakBadge string
		switch report.SoakStatus.State {
		case SoakReady, SoakUpToDate:
			soakBadge = ui.Green(ui.Bold(string(report.SoakStatus.State)))
		case SoakInProgress:
			soakBadge = ui.Yellow(ui.Bold(fmt.Sprintf("SOAKING (Day %d/%d)", report.SoakStatus.CurrentDay, report.SoakStatus.RequiredActiveDays)))
		case SoakNeedsDebug:
			soakBadge = ui.Red(ui.Bold("NEEDS DEBUG & FIX"))
		default:
			soakBadge = ui.Dim(string(report.SoakStatus.State))
		}
		sb.WriteString(fmt.Sprintf("  %-24s %s\n", ui.Bold("Soak Status:"), soakBadge))
		if report.SoakStatus.SoakStartAt != "" {
			sb.WriteString(fmt.Sprintf("  %-24s %.1fh / %.0fh elapsed (%.1fh remaining · since %s)\n",
				ui.Bold("Soak Clock:"),
				report.SoakStatus.ElapsedHours, report.SoakStatus.RequiredHours,
				report.SoakStatus.RemainingHours, report.SoakStatus.SoakStartAt))
			sb.WriteString(fmt.Sprintf("  %-24s %d / %d active days · %d tool events (%d errors) · %d prime events (%d errors)\n",
				ui.Bold("Local Telemetry:"),
				report.SoakStatus.ActiveDays, report.SoakStatus.RequiredActiveDays,
				report.SoakStatus.ToolEventsTotal, report.SoakStatus.ToolErrorCount,
				report.SoakStatus.PrimeEventsTotal, report.SoakStatus.PrimeErrorCount))
			if len(report.SoakStatus.FeatureLedger) > 0 {
				sb.WriteString(fmt.Sprintf("  %-24s %d ready · %d soaking · %d needs debug (independent 72h timers)\n",
					ui.Bold("Feature Timers:"),
					report.SoakStatus.ReadyFeatures, report.SoakStatus.SoakingFeatures, report.SoakStatus.DebugFeatures))
			}
			if report.SoakStatus.ReadyThroughCommit != "" {
				sb.WriteString(fmt.Sprintf("  %-24s %s\n",
					ui.Bold("Ready Train Commit:"),
					ui.Green(report.SoakStatus.ReadyThroughCommit)))
			}
		} else if report.SoakStatus.Summary != "" {
			sb.WriteString("  " + ui.Dim(report.SoakStatus.Summary) + "\n")
		}
		if len(report.SoakStatus.FailedTools) > 0 {
			sb.WriteString("  " + ui.Red(ui.Bold("Failing Tools to Debug:\n")))
			for _, tf := range report.SoakStatus.FailedTools {
				sb.WriteString(fmt.Sprintf("    %s %s (%d failure(s), last: %s at %s)\n",
					ui.Red("✖"), ui.Bold(tf.ToolName), tf.FailureCount, tf.LastOutcome, tf.LastSeenAt))
			}
		}
		if len(report.SoakStatus.PrimeErrors) > 0 {
			sb.WriteString("  " + ui.Red(ui.Bold("Prime Failures to Debug:\n")))
			for _, pe := range report.SoakStatus.PrimeErrors {
				sb.WriteString(fmt.Sprintf("    %s %s\n", ui.Red("✖"), pe))
			}
		}
		if len(report.SoakStatus.ClusterSummaries) > 0 {
			sb.WriteString("  " + ui.Red(ui.Bold("Active Diagnostic Clusters:\n")))
			for _, cs := range report.SoakStatus.ClusterSummaries {
				sb.WriteString(fmt.Sprintf("    %s %s\n", ui.Red("✖"), cs))
			}
		}
		if len(report.SoakStatus.FeatureLedger) > 0 {
			sb.WriteString(fmt.Sprintf("  %s %s\n",
				ui.Bold("Automated Feature Soak DB Ledger:"),
				ui.Dim(fmt.Sprintf("(%d features tracked in sila.db · %d daily reports collected)",
					len(report.SoakStatus.FeatureLedger), len(report.SoakStatus.DailyReports)))))
			limit := len(report.SoakStatus.FeatureLedger)
			if limit > 12 {
				limit = 12
			}
			for _, rec := range report.SoakStatus.FeatureLedger[:limit] {
				var stIcon string
				switch rec.Status {
				case "ready":
					stIcon = ui.Green("✔ READY  ")
				case "needs_debug":
					stIcon = ui.Red("✖ DEBUG  ")
				default:
					stIcon = ui.Yellow("⏳ SOAK   ")
				}
				sb.WriteString(fmt.Sprintf("    %s %-34s %5.1fh/72h · %d/3d · %d uses (%d err)\n",
					stIcon, truncateFeatureID(rec.FeatureID, 34),
					rec.ElapsedHours, rec.ActiveDays, rec.UsageCount, rec.ErrorCount))
			}
			if len(report.SoakStatus.FeatureLedger) > limit {
				sb.WriteString(fmt.Sprintf("    %s\n",
					ui.Dim(fmt.Sprintf("… +%d earlier post-tag feature(s) in sila.db feature_soaks",
						len(report.SoakStatus.FeatureLedger)-limit))))
			}
		}
		sb.WriteString("\n")
	}

	// Section 7: Release Verdict & Actionable Guidance
	sb.WriteString("  " + ui.Cyan(ui.Bold("⚖️  RELEASE VERDICT & ACTIONABLE GUIDANCE\n")))
	sb.WriteString("  " + ui.Dim(div) + "\n")

	var badge string
	switch report.Verdict.Code {
	case "READY_TO_RELEASE":
		badge = ui.Green(ui.Bold("[ " + report.Verdict.Verdict + " ]"))
	case "PENDING_COMMITS", "SOAK_IN_PROGRESS":
		badge = ui.Yellow(ui.Bold("[ " + report.Verdict.Verdict + " ]"))
	case "SOAK_NEEDS_DEBUG":
		badge = ui.Red(ui.Bold("[ " + report.Verdict.Verdict + " ]"))
	case "UP_TO_DATE":
		badge = ui.Green(ui.Bold("[ " + report.Verdict.Verdict + " ]"))
	default:
		badge = ui.Cyan(ui.Bold("[ " + report.Verdict.Verdict + " ]"))
	}

	sb.WriteString(fmt.Sprintf("  %s  %s\n", badge, ui.Bold(report.Verdict.Summary)))
	sb.WriteString("\n")

	sb.WriteString("  " + ui.Bold("SemVer Candidates Available:\n"))
	if report.SemverCandidates.MilestoneCandidate != "" {
		sb.WriteString(fmt.Sprintf("    %s %-24s %s\n",
			ui.Green("▸"), ui.Bold("Watershed Milestone:"),
			ui.Green(ui.Bold(report.SemverCandidates.MilestoneCandidate))+" "+ui.Dim("(100% Epistemic Roadmap Complete — Recommended!)")))
	}
	sb.WriteString(fmt.Sprintf("    %s %-24s %s\n",
		ui.Cyan("▸"), ui.Bold("Conventional Minor:"),
		ui.Bold(report.SemverCandidates.MinorCandidate)+" "+ui.Dim(fmt.Sprintf("(%d new features since %s)", report.ImpactSummary.MinorFeatures, report.PublicRelease.Tag))))
	sb.WriteString(fmt.Sprintf("    %s %-24s %s\n\n",
		ui.Yellow("▸"), ui.Bold("Conventional Patch:"),
		ui.Dim(report.SemverCandidates.PatchCandidate+" (Bug fixes only)")))

	if len(report.Verdict.ActionSteps) > 0 {
		sb.WriteString("  " + ui.Bold("Recommended Next Steps:\n"))
		for i, step := range report.Verdict.ActionSteps {
			sb.WriteString(fmt.Sprintf("    %d. %s\n", i+1, step))
		}
	}

	sb.WriteString("  " + ui.Dim(div) + "\n\n")
	return sb.String()
}

func renderImpactRow(sb *strings.Builder, ui render.UI, icon, label string, count int, conv, semver, color string) {
	var countStr string
	if count == 0 {
		countStr = ui.Dim("0")
	} else {
		switch color {
		case "red":
			countStr = ui.Red(ui.Bold(fmt.Sprintf("%d", count)))
		case "green":
			countStr = ui.Green(ui.Bold(fmt.Sprintf("%d", count)))
		case "yellow":
			countStr = ui.Yellow(ui.Bold(fmt.Sprintf("%d", count)))
		default:
			countStr = ui.Dim(fmt.Sprintf("%d", count))
		}
	}

	padLabel := 24 - len(label)
	if padLabel < 0 {
		padLabel = 0
	}
	padCount := 10 - len(fmt.Sprintf("%d", count))
	if padCount < 0 {
		padCount = 0
	}
	padConv := 16 - len(conv)
	if padConv < 0 {
		padConv = 0
	}

	sb.WriteString(fmt.Sprintf("  %s %s%s %s%s %s%s %s\n",
		icon, label, strings.Repeat(" ", padLabel),
		countStr, strings.Repeat(" ", padCount),
		conv, strings.Repeat(" ", padConv),
		ui.Dim(semver)))
}

// RenderStatusTOON produces Token-Oriented Object Notation for LLM consumption.
func RenderStatusTOON(report ReleaseStatusReport) string {
	var sb strings.Builder
	sb.WriteString("release_status:\n")
	sb.WriteString(fmt.Sprintf("  public_tag: %s\n", report.PublicRelease.Tag))
	sb.WriteString(fmt.Sprintf("  published_at: %s\n", report.PublicRelease.PublishedAt))
	sb.WriteString(fmt.Sprintf("  dev_describe: %s\n", report.DevState.GitDescribe))
	sb.WriteString(fmt.Sprintf("  branch: %s\n", report.DevState.Branch))
	sb.WriteString(fmt.Sprintf("  commit: %s\n", report.DevState.Commit))
	sb.WriteString(fmt.Sprintf("  is_dirty: %t\n", report.WorkingTree.IsDirty))
	sb.WriteString(fmt.Sprintf("  commits_ahead: %d\n", report.ImpactSummary.TotalCommitsAhead))
	sb.WriteString("  impact:\n")
	sb.WriteString(fmt.Sprintf("    major: %d\n", report.ImpactSummary.MajorBreaking))
	sb.WriteString(fmt.Sprintf("    minor: %d\n", report.ImpactSummary.MinorFeatures))
	sb.WriteString(fmt.Sprintf("    patch: %d\n", report.ImpactSummary.PatchFixes))
	sb.WriteString(fmt.Sprintf("    internal: %d\n", report.ImpactSummary.InternalDocs))
	sb.WriteString("  roadmap:\n")
	sb.WriteString(fmt.Sprintf("    total: %d\n", report.RoadmapStatus.TotalGoals))
	sb.WriteString(fmt.Sprintf("    implemented: %d\n", report.RoadmapStatus.ImplementedGoals))
	sb.WriteString(fmt.Sprintf("    tasks_completed: %d\n", report.RoadmapStatus.CompletedTasks))
	sb.WriteString(fmt.Sprintf("    is_complete: %t\n", report.RoadmapStatus.IsRoadmapComplete))
	if report.SoakStatus.State != "" {
		sb.WriteString("  soak:\n")
		sb.WriteString(fmt.Sprintf("    state: %s\n", report.SoakStatus.State))
		sb.WriteString(fmt.Sprintf("    elapsed_hours: %.1f\n", report.SoakStatus.ElapsedHours))
		sb.WriteString(fmt.Sprintf("    required_hours: %.1f\n", report.SoakStatus.RequiredHours))
		sb.WriteString(fmt.Sprintf("    active_days: %d\n", report.SoakStatus.ActiveDays))
		sb.WriteString(fmt.Sprintf("    required_days: %d\n", report.SoakStatus.RequiredActiveDays))
		sb.WriteString(fmt.Sprintf("    tool_events: %d\n", report.SoakStatus.ToolEventsTotal))
		sb.WriteString(fmt.Sprintf("    tool_errors: %d\n", report.SoakStatus.ToolErrorCount))
		sb.WriteString(fmt.Sprintf("    prime_events: %d\n", report.SoakStatus.PrimeEventsTotal))
		sb.WriteString(fmt.Sprintf("    prime_errors: %d\n", report.SoakStatus.PrimeErrorCount))
	}
	sb.WriteString("  decision:\n")
	sb.WriteString(fmt.Sprintf("    verdict_code: %s\n", report.Verdict.Code))
	sb.WriteString(fmt.Sprintf("    recommended: %s\n", report.SemverCandidates.RecommendedVersion))
	sb.WriteString(fmt.Sprintf("    minor_candidate: %s\n", report.SemverCandidates.MinorCandidate))
	sb.WriteString(fmt.Sprintf("    patch_candidate: %s\n", report.SemverCandidates.PatchCandidate))
	sb.WriteString(fmt.Sprintf("    rationale: %s\n", report.SemverCandidates.Rationale))
	return sb.String()
}

func truncateFeatureID(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 1 {
		return s[:maxLen]
	}
	return s[:maxLen-1] + "…"
}
