package cockpit

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

var ansiRE = regexp.MustCompile("\x1b\\[[0-9;]*m")

// visibleWidth returns the display width of s in terminal columns, ignoring ANSI escape codes.
func visibleWidth(s string) int {
	return utf8.RuneCountInString(ansiRE.ReplaceAllString(s, ""))
}

// truncateVisible shortens s to at most max terminal columns, preserving ANSI color resets.
func truncateVisible(s string, max int) string {
	if max <= 0 || visibleWidth(s) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	budget := max - 1
	var b strings.Builder
	vis := 0
	hadANSI := strings.Contains(s, "\x1b[")
	i := 0
	for i < len(s) {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && !((s[j] >= 'A' && s[j] <= 'Z') || (s[j] >= 'a' && s[j] <= 'z')) {
				j++
			}
			if j < len(s) {
				j++
			}
			b.WriteString(s[i:j])
			i = j
			continue
		}
		if vis >= budget {
			break
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		b.WriteRune(r)
		vis++
		i += size
	}
	b.WriteString("…")
	if hadANSI {
		b.WriteString("\x1b[0m")
	}
	return b.String()
}

// padRight pads s with spaces so its visible display width equals targetWidth.
func padRight(s string, targetWidth int) string {
	vw := visibleWidth(s)
	if vw >= targetWidth {
		return truncateVisible(s, targetWidth)
	}
	return s + strings.Repeat(" ", targetWidth-vw)
}

// RenderSnapshot formats the 9-box cockpit snapshot into a responsive ANSI terminal screen.
func RenderSnapshot(snap *CockpitSnapshot, width int, color bool) string {
	if width < 40 {
		width = 40
	}

	// ANSI Styler closures
	green := func(s string) string {
		if !color {
			return s
		}
		return "\x1b[32m" + s + "\x1b[0m"
	}
	yellow := func(s string) string {
		if !color {
			return s
		}
		return "\x1b[33m" + s + "\x1b[0m"
	}
	red := func(s string) string {
		if !color {
			return s
		}
		return "\x1b[31m" + s + "\x1b[0m"
	}
	cyan := func(s string) string {
		if !color {
			return s
		}
		return "\x1b[36m" + s + "\x1b[0m"
	}
	magenta := func(s string) string {
		if !color {
			return s
		}
		return "\x1b[35m" + s + "\x1b[0m"
	}
	bold := func(s string) string {
		if !color {
			return s
		}
		return "\x1b[1m" + s + "\x1b[0m"
	}
	dim := func(s string) string {
		if !color {
			return s
		}
		return "\x1b[2m" + s + "\x1b[0m"
	}

	var sb strings.Builder

	// Top Pulse Header
	pulseDot := green("●")
	if snap.Header.FleetHealth == "PAUSED" {
		pulseDot = yellow("⏸")
	} else if snap.Header.FleetHealth != "100% GREEN" {
		pulseDot = yellow("▲")
	}

	pulseText := "LIVE (1s)"
	if snap.Header.Pulse != "" {
		pulseText = snap.Header.Pulse
		if strings.HasSuffix(pulseText, " pulse") {
			pulseText = strings.TrimSuffix(pulseText, " pulse")
		}
		if !strings.HasPrefix(pulseText, "LIVE") && !strings.HasPrefix(pulseText, "PAUSED") && !strings.HasPrefix(pulseText, "RESUMED") && !strings.HasPrefix(pulseText, "✔") && !strings.HasPrefix(pulseText, "✖") && !strings.HasPrefix(pulseText, "Filter") && !strings.HasPrefix(pulseText, "ℹ") {
			pulseText = fmt.Sprintf("LIVE (%s)", pulseText)
		}
	}

	ledgerBadge := green("LEDGER: intact")
	if snap.Header.LedgerSummary != "" {
		if snap.Header.LedgerIntact {
			ledgerBadge = green(snap.Header.LedgerSummary)
		} else {
			ledgerBadge = red(snap.Header.LedgerSummary)
		}
	} else if snap.Ledger.Status == "unavailable" {
		if snap.Ledger.Error != "" {
			ledgerBadge = red(fmt.Sprintf("▲ ERROR: %s", snap.Ledger.Error))
		} else {
			ledgerBadge = red("▲ ERROR: verification unavailable")
		}
	} else if !snap.Ledger.Intact {
		ledgerBadge = red(fmt.Sprintf("▲ LEDGER TAMPER: %d broken links", snap.Ledger.BrokenLinks))
	}
	headerLine := fmt.Sprintf(" %s %s │ %s │ %s │ %s │ %s │ %s",
		pulseDot,
		bold(pulseText),
		cyan(fmt.Sprintf("%d Agents Active", snap.Header.ActiveAgentsCount)),
		yellow(fmt.Sprintf("%d Approval Waiting", snap.Header.PendingApprovalsCount)),
		ledgerBadge,
		dim(fmt.Sprintf("Substrate: %d Repos", snap.Header.SubstrateRepos)),
		dim(fmt.Sprintf("Sila %s", snap.Header.Version)),
	)
	sb.WriteString(headerLine + "\n\n")

	// Determine column layout: 3 columns if width >= 110, 2 if width >= 75, else 1
	numCols := 1
	if width >= 110 {
		numCols = 3
	} else if width >= 75 {
		numCols = 2
	}

	// Render each of the 9 boxes
	b1 := renderFleetBox(snap, color, green, yellow, red, cyan, magenta, dim, bold)
	b2 := renderApprovalBox(snap, color, green, yellow, red, cyan, dim, bold)
	b3 := renderStreamBox(snap, color, green, yellow, red, cyan, magenta, dim, bold)
	b4 := renderConcurrencyBox(snap, color, green, yellow, red, cyan, magenta, dim, bold)
	b5 := renderGateBox(snap, color, green, yellow, red, cyan, dim, bold)
	b6 := renderRoadmapBox(snap, color, green, yellow, red, cyan, magenta, dim, bold)
	b7 := renderContextBox(snap, color, green, yellow, red, cyan, dim, bold)
	b8 := renderDiffBox(snap, color, green, yellow, red, cyan, dim, bold)
	b9 := renderWorktreeBox(snap, color, green, yellow, red, cyan, dim, bold)

	type boxData struct {
		title string
		tag   string
		lines []string
	}

	allBoxes := []boxData{
		{"Live Agent Fleet Roster", "harness step turn", b1},
		{"Human-in-the-Loop Steering Desk", "[a]pprove [r]eject timer", b2},
		{"Live Epistemic Stream", "latest facts lessons", b3},
		{"Concurrency & File Sentry", "active touch collisions", b4},
		{"Real-Time Gate Sentries", "race format changelog", b5},
		{"In-Flight Goal Roadmap DAG", "tasks velocity complete", b6},
		{"Context & Token Economy", "turn prime compression", b7},
		{"Active Mutation & Diff Velocity", "deltas churn worktree", b8},
		{"Sandbox Worktree Pool", "path branch status", b9},
	}

	// Layout grid
	if numCols == 3 {
		colWidth := (width - 2) / 3
		for r := 0; r < 3; r++ {
			bA := allBoxes[r*3]
			bB := allBoxes[r*3+1]
			bC := allBoxes[r*3+2]

			renderedA := formatBoxLines(bA.title, bA.tag, bA.lines, colWidth, 0, dim, bold, cyan, magenta)
			renderedB := formatBoxLines(bB.title, bB.tag, bB.lines, colWidth, 0, dim, bold, cyan, magenta)
			renderedC := formatBoxLines(bC.title, bC.tag, bC.lines, colWidth, 0, dim, bold, cyan, magenta)

			// Align row heights
			rowH := max(len(renderedA), max(len(renderedB), len(renderedC)))
			renderedA = padBoxHeight(renderedA, rowH, colWidth, dim)
			renderedB = padBoxHeight(renderedB, rowH, colWidth, dim)
			renderedC = padBoxHeight(renderedC, rowH, colWidth, dim)

			for l := 0; l < rowH; l++ {
				sb.WriteString(renderedA[l] + " " + renderedB[l] + " " + renderedC[l] + "\n")
			}
			sb.WriteString("\n")
		}
	} else if numCols == 2 {
		colWidth := (width - 1) / 2
		for r := 0; r < len(allBoxes); r += 2 {
			bA := allBoxes[r]
			renderedA := formatBoxLines(bA.title, bA.tag, bA.lines, colWidth, 0, dim, bold, cyan, magenta)

			if r+1 < len(allBoxes) {
				bB := allBoxes[r+1]
				renderedB := formatBoxLines(bB.title, bB.tag, bB.lines, colWidth, 0, dim, bold, cyan, magenta)
				rowH := max(len(renderedA), len(renderedB))
				renderedA = padBoxHeight(renderedA, rowH, colWidth, dim)
				renderedB = padBoxHeight(renderedB, rowH, colWidth, dim)
				for l := 0; l < rowH; l++ {
					sb.WriteString(renderedA[l] + " " + renderedB[l] + "\n")
				}
			} else {
				for _, l := range renderedA {
					sb.WriteString(l + "\n")
				}
			}
			sb.WriteString("\n")
		}
	} else {
		// Stacked 1 column
		for _, b := range allBoxes {
			rendered := formatBoxLines(b.title, b.tag, b.lines, width, 0, dim, bold, cyan, magenta)
			for _, l := range rendered {
				sb.WriteString(l + "\n")
			}
			sb.WriteString("\n")
		}
	}

	// Bottom Hotkey Steering Bar
	hotkeys := fmt.Sprintf(" %s %s  %s %s  %s %s  %s %s  %s %s",
		cyan("[Space]"), "Pause/Resume",
		green("[a]"), "Quick Approve",
		red("[r]"), "Quick Reject",
		magenta("[p]"), "Filter Repo",
		dim("[q]"), "Quit",
	)
	sb.WriteString(hotkeys + "\n")

	return sb.String()
}

// formatBoxLines renders a bordered box with rounded corners and tag.
func formatBoxLines(title, tag string, lines []string, width, minHeight int, dim, bold, cyan, magenta func(string) string) []string {
	if width < 24 {
		width = 24
	}
	innerWidth := width - 4
	if innerWidth < 10 {
		innerWidth = 10
	}

	titleStyled := bold(title)
	tagStyled := dim(tag)

	// Top frame calculation
	visTitle := visibleWidth(title)
	visTag := visibleWidth(tag)

	topFill := width - visTitle - visTag - 8
	if topFill < 1 {
		topFill = 1
	}

	topBorder := dim("╭─ ") + titleStyled + " " + dim(strings.Repeat("─", topFill)) + " " + tagStyled + dim(" ─╮")
	bottomBorder := dim("╰─" + strings.Repeat("─", width-4) + "─╯")

	var result []string
	result = append(result, topBorder)

	for _, l := range lines {
		padded := padRight(l, innerWidth)
		result = append(result, dim("│ ")+padded+dim(" │"))
	}

	for len(result)-1 < minHeight {
		empty := strings.Repeat(" ", innerWidth)
		result = append(result, dim("│ ")+empty+dim(" │"))
	}

	result = append(result, bottomBorder)
	return result
}

func padBoxHeight(boxLines []string, targetHeight, width int, dim func(string) string) []string {
	if len(boxLines) >= targetHeight {
		return boxLines
	}
	innerWidth := width - 4
	emptyRow := dim("│ ") + strings.Repeat(" ", innerWidth) + dim(" │")

	// Insert empty rows right before the bottom border
	lastIdx := len(boxLines) - 1
	bottomBorder := boxLines[lastIdx]
	var padded []string
	padded = append(padded, boxLines[:lastIdx]...)

	for len(padded) < targetHeight-1 {
		padded = append(padded, emptyRow)
	}
	padded = append(padded, bottomBorder)
	return padded
}

// Individual Box Content Renderers

func renderFleetBox(snap *CockpitSnapshot, color bool, green, yellow, red, cyan, magenta, dim, bold func(string) string) []string {
	var lines []string
	if len(snap.Fleet.Agents) == 0 {
		lines = append(lines, dim("• No active autonomous agents running"))
		lines = append(lines, dim("  Idle fleet ready for goal claims"))
	} else {
		for _, a := range snap.Fleet.Agents {
			icon := green("🟢")
			if strings.Contains(a.Status, "WAITING") {
				icon = yellow("🟡")
			} else if strings.Contains(a.Status, "BLOCKED") {
				icon = red("🔴")
			}
			harness := dim("[" + a.Harness + "]")
			projBadge := ""
			if a.Project != "" {
				projBadge = magenta("["+a.Project+"]") + " "
			}
			line := fmt.Sprintf("%s %s %s %s%s", icon, cyan(a.Agent), harness, projBadge, a.GoalSlug)
			lines = append(lines, line)
			lines = append(lines, fmt.Sprintf("  %s (%s)", dim(a.ActiveTool), dim(a.Elapsed)))
		}
	}
	lines = append(lines, dim("Fleet Status: ")+snap.Fleet.Summary)
	return lines
}

func renderApprovalBox(snap *CockpitSnapshot, color bool, green, yellow, red, cyan, dim, bold func(string) string) []string {
	var lines []string
	if !snap.Approvals.HasPending {
		lines = append(lines, green("✔")+" All agent workflows clear (0 pending holds)")
		lines = append(lines, dim("Steering: Press [a] to approve or [r] to reject"))
		lines = append(lines, dim("Hold timers: auto-timeout in 300s"))
	} else {
		top := snap.Approvals.Pending[0]
		lines = append(lines, yellow("▲ APPROVAL REQUIRED: ")+bold(top.ID)+" from "+cyan(top.Agent))
		lines = append(lines, "Action: "+dim(top.Action))
		lines = append(lines, "Reason: "+dim(top.Reason))
		timeoutBar := renderProgressBar(top.TimeoutSeconds-top.RemainingSeconds, top.TimeoutSeconds, 16)
		lines = append(lines, fmt.Sprintf("Timer: %s %ds left / %ds", timeoutBar, top.RemainingSeconds, top.TimeoutSeconds))
		lines = append(lines, green("[a]")+" Approve   "+red("[r]")+" Reject   "+cyan("[Space]")+" Diff")
	}
	return lines
}

func renderStreamBox(snap *CockpitSnapshot, color bool, green, yellow, red, cyan, magenta, dim, bold func(string) string) []string {
	var lines []string
	if len(snap.Stream.Items) == 0 {
		lines = append(lines, dim("• No recent facts distilled in store"))
		lines = append(lines, dim("  Continuous digest running in background"))
	} else {
		for _, it := range snap.Stream.Items {
			kindBadge := magenta("[" + it.Kind + "]")
			timeStr := dim(it.Time)
			line := fmt.Sprintf("%s %s %s %s", timeStr, kindBadge, cyan(it.Agent), it.Content)
			lines = append(lines, line)
		}
	}
	rateStr := fmt.Sprintf("Rate: %s │ %s", bold(snap.Stream.KnowledgeRate), cyan(snap.Stream.VelocitySparkline))
	lines = append(lines, rateStr)
	return lines
}

func renderConcurrencyBox(snap *CockpitSnapshot, color bool, green, yellow, red, cyan, magenta, dim, bold func(string) string) []string {
	var lines []string
	if len(snap.Concurrency.Touches) == 0 {
		lines = append(lines, dim("• No active file locks or modifications"))
		lines = append(lines, dim("  Clean worktrees across all agents"))
	} else {
		for _, t := range snap.Concurrency.Touches {
			projBadge := ""
			if t.Project != "" {
				projBadge = magenta("["+t.Project+"]") + " "
			}
			filesStr := strings.Join(t.Files, ", ")
			lines = append(lines, fmt.Sprintf("%s%s → %s", projBadge, cyan(t.Agent), dim(filesStr)))
		}
	}

	matrix := green("✔ 0 OVERLAPS (Clean isolation)")
	if snap.Concurrency.CollisionAlert {
		matrix = red(fmt.Sprintf("▲ COLLISION: %d overlap(s)!", len(snap.Concurrency.Overlaps)))
	}
	lines = append(lines, "Collision Matrix: "+matrix)
	return lines
}

func renderGateBox(snap *CockpitSnapshot, color bool, green, yellow, red, cyan, dim, bold func(string) string) []string {
	var lines []string
	for _, c := range snap.Gates.Checks {
		icon := green("✔")
		if c.Status == "FAIL" {
			icon = red("✖")
		} else if c.Status == "WARN" {
			icon = yellow("▲")
		} else if c.Status == "SKIP" {
			icon = dim("•")
		}
		lines = append(lines, fmt.Sprintf("%s %s: %s", icon, bold(c.Name), dim(c.Details)))
	}
	if len(lines) == 0 {
		lines = append(lines, green("✔")+" All gate sentries passing cleanly")
	}
	return lines
}

func renderRoadmapBox(snap *CockpitSnapshot, color bool, green, yellow, red, cyan, magenta, dim, bold func(string) string) []string {
	var lines []string
	if len(snap.Roadmap.InFlight) == 0 {
		lines = append(lines, dim("• No active goals in progress"))
	} else {
		for _, g := range snap.Roadmap.InFlight {
			statusBadge := cyan(g.Status)
			if g.Status == "READY TO COMMIT" {
				statusBadge = green("✔ READY TO COMMIT")
			}
			projBadge := ""
			if g.Project != "" {
				projBadge = magenta("["+g.Project+"]") + " "
			}
			line := fmt.Sprintf("%s%s %s %d/%d (%d%%) %s",
				projBadge, bold(g.Slug), g.ProgressBar, g.TasksMet, g.TasksTotal, g.ProgressPct, statusBadge)
			lines = append(lines, line)
		}
	}
	lines = append(lines, fmt.Sprintf("Next: %s", cyan("⚡ "+snap.Roadmap.NextUnblocked)))
	return lines
}

func renderContextBox(snap *CockpitSnapshot, color bool, green, yellow, red, cyan, dim, bold func(string) string) []string {
	var lines []string
	if len(snap.Context.Agents) == 0 {
		lines = append(lines, dim("• No agent context usage recorded (unknown)"))
	} else {
		for _, a := range snap.Context.Agents {
			lim := dim("(" + a.LimitStatus + ")")
			if a.LimitStatus == "approaching threshold" {
				lim = yellow("(" + a.LimitStatus + ")")
			}
			ctxStr := a.ContextTokensFormatted
			if ctxStr == "" || ctxStr == "unknown" {
				ctxStr = "unknown"
			} else {
				ctxStr = ctxStr + " ctx"
			}
			promptStr := a.PromptTokensFormatted
			if promptStr == "" || promptStr == "unknown" {
				promptStr = "unknown/turn"
			} else {
				promptStr = promptStr + "/turn"
			}
			lines = append(lines, fmt.Sprintf("%s: %s  %s %s",
				cyan(a.Agent), bold(ctxStr), bold(promptStr), lim))
		}
	}
	if snap.Context.PrimeSummary != "" {
		lines = append(lines, "Sila Prime: "+dim(snap.Context.PrimeSummary))
	} else {
		lines = append(lines, "Sila Prime: "+dim("unknown"))
	}
	return lines
}

func renderDiffBox(snap *CockpitSnapshot, color bool, green, yellow, red, cyan, dim, bold func(string) string) []string {
	var lines []string
	if len(snap.Diff.Items) == 0 {
		lines = append(lines, green("✔ Clean working tree")+" "+dim("(0 uncommitted mutations)"))
		lines = append(lines, dim("Velocity: 0 additions, 0 deletions across 0 files"))
		lines = append(lines, dim("Worktrees: fully synced with HEAD"))
		return lines
	}

	vel := fmt.Sprintf("Velocity: %s %s %s",
		green(fmt.Sprintf("+%d", snap.Diff.TotalAdditions)),
		red(fmt.Sprintf("-%d", snap.Diff.TotalDeletions)),
		dim(fmt.Sprintf("(%d files)", snap.Diff.TotalFiles)),
	)
	lines = append(lines, vel)

	maxItems := 3
	for i, item := range snap.Diff.Items {
		if i >= maxItems {
			remaining := len(snap.Diff.Items) - maxItems
			lines = append(lines, dim(fmt.Sprintf("… and %d more mutated file(s)", remaining)))
			break
		}
		statusTag := yellow("M")
		switch item.Status {
		case "A":
			statusTag = green("A")
		case "D":
			statusTag = red("D")
		case "R":
			statusTag = cyan("R")
		case "?":
			statusTag = dim("?")
		}

		wtTag := ""
		if item.Worktree != "" {
			wtTag = " " + dim("["+item.Worktree+"]")
		}

		delta := fmt.Sprintf("%s/%s", green(fmt.Sprintf("+%d", item.Additions)), red(fmt.Sprintf("-%d", item.Deletions)))
		lines = append(lines, fmt.Sprintf("%s %s %s%s", statusTag, bold(item.File), delta, wtTag))
	}

	if snap.Diff.ChurnSummary != "" {
		lines = append(lines, dim("Churn: "+snap.Diff.ChurnSummary))
	}
	return lines
}

func renderLedgerBox(snap *CockpitSnapshot, color bool, green, yellow, red, cyan, dim, bold func(string) string) []string {
	var lines []string
	lines = append(lines, fmt.Sprintf("Blocks: %s (%d verified)", bold(fmt.Sprintf("%d", snap.Ledger.TotalBlocks)), snap.Ledger.VerifiedBlocks))
	if snap.Ledger.TipHashShort != "" {
		lines = append(lines, fmt.Sprintf("Tip:    %s", dim(snap.Ledger.TipHashShort)))
	} else {
		lines = append(lines, fmt.Sprintf("Tip:    %s", dim("none")))
	}
	var health string
	switch snap.Ledger.Status {
	case "intact":
		health = green(fmt.Sprintf("✔ 100%% INTACT (%d blocks verified)", snap.Ledger.VerifiedBlocks))
	case "empty":
		health = dim("• Ledger empty (0 blocks)")
	case "tampered":
		health = red(fmt.Sprintf("▲ TAMPER: %d broken links", snap.Ledger.BrokenLinks))
	case "unavailable":
		if snap.Ledger.Error != "" {
			health = red("▲ ERROR: " + snap.Ledger.Error)
		} else {
			health = red("▲ UNAVAILABLE: verification failed")
		}
	default:
		if snap.Ledger.Intact {
			health = green("✔ 100% INTACT")
		} else {
			health = red(snap.Ledger.HealthSummary)
		}
	}
	lines = append(lines, fmt.Sprintf("Health: %s", health))
	if snap.Ledger.Status == "tampered" {
		lines = append(lines, dim("Repair: AVAILABLE (sila facts repair)"))
	}
	return lines
}

func renderWorktreeBox(snap *CockpitSnapshot, color bool, green, yellow, red, cyan, dim, bold func(string) string) []string {
	var lines []string
	if len(snap.Worktrees.ActiveWorktrees) == 0 {
		lines = append(lines, dim("• Primary workspace only (0 isolated worktrees)"))
	} else {
		count := 0
		for _, wt := range snap.Worktrees.ActiveWorktrees {
			if count >= 3 {
				break
			}
			branchStr := dim("branch: " + wt.Branch)
			lines = append(lines, fmt.Sprintf("%s (%s) %s", cyan(wt.Name), dim(wt.Path), branchStr))
			count++
		}
	}
	capStr := fmt.Sprintf("Capacity: %d/%d active │ Auto-prune: %s",
		snap.Worktrees.ActiveCount, snap.Worktrees.TotalCapacity, green("ENABLED"))
	lines = append(lines, capStr)
	return lines
}
