package goals

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/render"
)

// GlobalGoalsStatsPayload is the structured JSON output for multi-project goals statistics.
type GlobalGoalsStatsPayload struct {
	TotalProjects    int                      `json:"total_projects"`
	TotalGoals       int                      `json:"total_goals"`
	CompletionPct    float64                  `json:"completion_pct"`
	Verdict          string                   `json:"verdict"`
	ReadyCount       int                      `json:"ready_count"`
	WIPCount         int                      `json:"wip_count"`
	ClaimedCount     int                      `json:"claimed_count"`
	ImplementedCount int                      `json:"implemented_count"`
	DoneCount        int                      `json:"done_count"`
	BlockedCount     int                      `json:"blocked_count"`
	DraftCount       int                      `json:"draft_count"`
	Projects         []ProjectGoalStatSummary `json:"projects"`
}

// ProjectGoalStatSummary is the per-project count rollup.
type ProjectGoalStatSummary struct {
	Project        string  `json:"project"`
	ProjectDir     string  `json:"project_dir,omitempty"`
	CompletionPct  float64 `json:"completion_pct"`
	Verdict        string  `json:"verdict"`
	OldestReadyAge string  `json:"oldest_ready_age,omitempty"`
	Ready          int     `json:"ready"`
	WIP            int     `json:"wip"`
	Claimed        int     `json:"claimed"`
	Done           int     `json:"done"`
	Implemented    int     `json:"implemented"`
	Blocked        int     `json:"blocked"`
	Draft          int     `json:"draft"`
	Total          int     `json:"total"`
}

// ComputeCompletionPct calculates rounded completion percentage.
func ComputeCompletionPct(done, total int) float64 {
	if total <= 0 {
		return 0.0
	}
	pct := (float64(done) / float64(total)) * 100.0
	return float64(int(pct*10+0.5)) / 10.0
}

// ComputeVerdict derives an actionable next step from goal counts.
func ComputeVerdict(ready, wip, blocked, total int) string {
	if total == 0 {
		return "No goals registered; run sila goals create to scaffold work"
	}
	if wip == 0 && ready > 0 {
		return "Idle warning: pick up ready work with sila goals ready"
	}
	if ready == 0 && blocked > 0 && wip == 0 {
		return "Blocked: inspect blocker dependencies with sila goals --blocked"
	}
	if ready == 0 && wip == 0 && blocked == 0 {
		return "All registered goals completed"
	}
	return "Active: proceed with in-progress goals"
}

// OldestReadyAge returns a compact age string for the oldest ready goal.
func OldestReadyAge(res *AuditResult) string {
	if res == nil || res.ReadyCount == 0 {
		return ""
	}
	if res.OldestReadyAge != "" {
		return res.OldestReadyAge
	}
	var oldestMod time.Time
	if len(res.Goals) > 0 {
		for _, g := range res.Goals {
			if g.Blocked || g.Commit != "" || g.Claim != nil {
				continue
			}
			path := filepath.Join(res.ProjectDir, "goals", g.Slug, "goal.md")
			if fi, err := os.Stat(path); err == nil {
				if oldestMod.IsZero() || fi.ModTime().Before(oldestMod) {
					oldestMod = fi.ModTime()
				}
			}
		}
	}
	if !oldestMod.IsZero() {
		dur := time.Since(oldestMod)
		if dur < time.Minute {
			return "<1m"
		}
		if dur < time.Hour {
			return fmt.Sprintf("%dm", int(dur.Minutes()))
		}
		if dur < 24*time.Hour {
			return fmt.Sprintf("%dh", int(dur.Hours()))
		}
		return fmt.Sprintf("%dd", int(dur.Hours()/24))
	}
	return ""
}

// ToStatsPayload converts a GlobalAuditResult into a clean GlobalGoalsStatsPayload.
func (res *GlobalAuditResult) ToStatsPayload() GlobalGoalsStatsPayload {
	payload := GlobalGoalsStatsPayload{
		TotalProjects:    res.TotalProjects,
		TotalGoals:       res.TotalGoals,
		CompletionPct:    ComputeCompletionPct(res.ImplementedCount, res.TotalGoals),
		Verdict:          ComputeVerdict(res.ReadyCount, res.ClaimedCount, res.BlockedCount, res.TotalGoals),
		ReadyCount:       res.ReadyCount,
		WIPCount:         res.ClaimedCount,
		ClaimedCount:     res.ClaimedCount,
		ImplementedCount: res.ImplementedCount,
		DoneCount:        res.ImplementedCount,
		BlockedCount:     res.BlockedCount,
		DraftCount:       res.DraftCount,
		Projects:         make([]ProjectGoalStatSummary, 0, len(res.Projects)),
	}

	projIDs := sortedProjectIDsForStats(res)
	for _, pid := range projIDs {
		p := res.Projects[pid]
		payload.Projects = append(payload.Projects, ProjectGoalStatSummary{
			Project:        pid,
			ProjectDir:     p.ProjectDir,
			CompletionPct:  ComputeCompletionPct(p.ImplementedCount, p.Total),
			Verdict:        ComputeVerdict(p.ReadyCount, p.ClaimedCount, p.BlockedCount, p.Total),
			OldestReadyAge: OldestReadyAge(p),
			Ready:          p.ReadyCount,
			WIP:            p.ClaimedCount,
			Claimed:        p.ClaimedCount,
			Done:           p.ImplementedCount,
			Implemented:    p.ImplementedCount,
			Blocked:        p.BlockedCount,
			Draft:          p.DraftCount,
			Total:          p.Total,
		})
	}

	return payload
}

// ToStatsPayload converts a single project's AuditResult into a ProjectGoalStatSummary.
func (res *AuditResult) ToStatsPayload() ProjectGoalStatSummary {
	return ProjectGoalStatSummary{
		Project:        res.Project,
		ProjectDir:     res.ProjectDir,
		CompletionPct:  ComputeCompletionPct(res.ImplementedCount, res.Total),
		Verdict:        ComputeVerdict(res.ReadyCount, res.ClaimedCount, res.BlockedCount, res.Total),
		OldestReadyAge: OldestReadyAge(res),
		Ready:          res.ReadyCount,
		WIP:            res.ClaimedCount,
		Claimed:        res.ClaimedCount,
		Done:           res.ImplementedCount,
		Implemented:    res.ImplementedCount,
		Blocked:        res.BlockedCount,
		Draft:          res.DraftCount,
		Total:          res.Total,
	}
}

// ToStatsTOON converts a GlobalAuditResult into compact Token-Oriented Object Notation.
func (res *GlobalAuditResult) ToStatsTOON() string {
	headers := []string{"project", "ready", "wip", "blocked", "done", "total"}
	projIDs := sortedProjectIDsForStats(res)
	rows := make([][]string, 0, len(projIDs))
	for _, pid := range projIDs {
		p := res.Projects[pid]
		rows = append(rows, []string{
			pid,
			strconv.Itoa(p.ReadyCount),
			strconv.Itoa(p.ClaimedCount),
			strconv.Itoa(p.BlockedCount),
			strconv.Itoa(p.ImplementedCount),
			strconv.Itoa(p.Total),
		})
	}
	return render.FormatTOONTable("goals_stats", headers, rows)
}

// ToStatsTOON converts a single project's AuditResult into TOON.
func (res *AuditResult) ToStatsTOON() string {
	headers := []string{"project", "ready", "wip", "blocked", "done", "total"}
	rows := [][]string{
		{
			res.Project,
			strconv.Itoa(res.ReadyCount),
			strconv.Itoa(res.ClaimedCount),
			strconv.Itoa(res.BlockedCount),
			strconv.Itoa(res.ImplementedCount),
			strconv.Itoa(res.Total),
		},
	}
	return render.FormatTOONTable("goals_stats", headers, rows)
}

func sortedProjectIDsForStats(res *GlobalAuditResult) []string {
	var projIDs []string
	for id, p := range res.Projects {
		if p.Total > 0 {
			projIDs = append(projIDs, id)
		}
	}

	sort.SliceStable(projIDs, func(i, j int) bool {
		a, b := res.Projects[projIDs[i]], res.Projects[projIDs[j]]
		aActive := (a.ReadyCount + a.ClaimedCount) > 0
		bActive := (b.ReadyCount + b.ClaimedCount) > 0
		if aActive != bActive {
			return aActive
		}
		if aActive {
			if (a.ReadyCount + a.ClaimedCount) != (b.ReadyCount + b.ClaimedCount) {
				return (a.ReadyCount + a.ClaimedCount) > (b.ReadyCount + b.ClaimedCount)
			}
			if a.Total != b.Total {
				return a.Total > b.Total
			}
			return projIDs[i] < projIDs[j]
		}
		if a.Total != b.Total {
			return a.Total > b.Total
		}
		return projIDs[i] < projIDs[j]
	})

	return projIDs
}

// FprintGlobalStatsTable renders the tabular goals statistics across all registered projects.
func FprintGlobalStatsTable(w io.Writer, ui render.UI, res *GlobalAuditResult) {
	fmt.Fprintln(w, ui.Cyan(ui.Bold("sila · goals"))+" — Global Multi-Project Statistics")
	fmt.Fprintf(w, "  Projects with Goals: %s\n", ui.Bold(fmt.Sprint(res.TotalProjects)))
	fmt.Fprintf(w, "  Total Goals:         %s\n", ui.Bold(fmt.Sprint(res.TotalGoals)))
	fmt.Fprintf(w, "  🟡 Ready to Pick:      %s\n", ui.Yellow(fmt.Sprint(res.ReadyCount)))
	if res.ClaimedCount > 0 {
		fmt.Fprintf(w, "  🔵 WIP (Claimed):      %s\n", ui.Cyan(fmt.Sprint(res.ClaimedCount)))
	}
	if res.BlockedCount > 0 {
		fmt.Fprintf(w, "  ⛔ Blocked:            %s\n", ui.Red(fmt.Sprint(res.BlockedCount)))
	}
	fmt.Fprintf(w, "  🟢 Done (Implemented): %s\n", ui.Green(fmt.Sprint(res.ImplementedCount)))
	if res.DraftCount > 0 {
		fmt.Fprintf(w, "  ⚪ Draft / Setup:      %s\n", ui.Dim(fmt.Sprint(res.DraftCount)))
	}
	fmt.Fprintln(w)

	projIDs := sortedProjectIDsForStats(res)
	headers := []string{"project", "ready", "wip", "blocked", "done", "total"}
	aligns := []string{"l", "r", "r", "r", "r", "r"}
	rows := make([][]string, 0, len(projIDs)+1)

	for _, pid := range projIDs {
		p := res.Projects[pid]
		rows = append(rows, []string{
			pid,
			strconv.Itoa(p.ReadyCount),
			strconv.Itoa(p.ClaimedCount),
			strconv.Itoa(p.BlockedCount),
			strconv.Itoa(p.ImplementedCount),
			strconv.Itoa(p.Total),
		})
	}

	// Totals summary row
	rows = append(rows, []string{
		fmt.Sprintf("TOTAL (%d)", len(projIDs)),
		strconv.Itoa(res.ReadyCount),
		strconv.Itoa(res.ClaimedCount),
		strconv.Itoa(res.BlockedCount),
		strconv.Itoa(res.ImplementedCount),
		strconv.Itoa(res.TotalGoals),
	})

	fmt.Fprintln(w, ui.Table(headers, rows, aligns))
}

// FprintProjectStatsTable renders goals statistics with actionable hierarchy:
// a completion ratio headline, actionable glyphs, and decision verdict.
func FprintProjectStatsTable(w io.Writer, ui render.UI, res *AuditResult) {
	pct := ComputeCompletionPct(res.ImplementedCount, res.Total)
	verdict := ComputeVerdict(res.ReadyCount, res.ClaimedCount, res.BlockedCount, res.Total)

	fmt.Fprintf(w, "%s — %s · %.1f%% done (%d/%d)\n",
		ui.Cyan(ui.Bold("sila · goals")),
		ui.Magenta(ui.Bold(res.Project)),
		pct,
		res.ImplementedCount,
		res.Total,
	)

	readyAge := ""
	if age := OldestReadyAge(res); age != "" {
		readyAge = fmt.Sprintf(" (oldest: %s)", age)
	}

	wipStr := fmt.Sprintf("%d", res.ClaimedCount)
	if res.ClaimedCount == 0 && res.ReadyCount > 0 {
		wipStr = ui.Yellow("0 (idle)")
	} else if res.ClaimedCount == 0 {
		wipStr = ui.Dim("0")
	} else {
		wipStr = ui.Cyan(wipStr)
	}

	blockedStr := fmt.Sprint(res.BlockedCount)
	if res.BlockedCount == 0 {
		blockedStr = ui.Dim("0")
	} else {
		blockedStr = ui.Red(blockedStr)
	}

	readyStr := fmt.Sprint(res.ReadyCount)
	if res.ReadyCount == 0 {
		readyStr = ui.Dim("0")
	} else {
		readyStr = ui.Yellow(readyStr)
	}

	fmt.Fprintf(w, "  ⛔ Blocked: %s · ▶ Ready: %s%s · ◐ WIP: %s\n",
		blockedStr,
		readyStr,
		readyAge,
		wipStr,
	)

	fmt.Fprintf(w, "  Verdict: %s\n", ui.Bold(verdict))
	if res.BlockedCount > 0 {
		fmt.Fprintf(w, "  %s\n", ui.Dim("↳ Hint: run sila goals --blocked to inspect blocker dependencies"))
	}
}

// StatsCounts is the four-state rollup shown by the one-line stats view.
type StatsCounts struct {
	Ready   int
	WIP     int
	Blocked int
	Done    int
	Total   int
}

// StatsCounts returns the project rollup for the one-line view.
func (res *AuditResult) StatsCounts() StatsCounts {
	return StatsCounts{
		Ready:   res.ReadyCount,
		WIP:     res.ClaimedCount,
		Blocked: res.BlockedCount,
		Done:    res.ImplementedCount,
		Total:   res.Total,
	}
}

// StatsCounts returns the all-projects rollup for the one-line view.
func (res *GlobalAuditResult) StatsCounts() StatsCounts {
	return StatsCounts{
		Ready:   res.ReadyCount,
		WIP:     res.ClaimedCount,
		Blocked: res.BlockedCount,
		Done:    res.ImplementedCount,
		Total:   res.TotalGoals,
	}
}

// RenderStatsLine renders `Goals: Ready N · WIP N · Blocked N · Done N` with no
// trailing newline. Each state has a fixed colour (the colour is the legend);
// zero counts are dimmed. With colour off the words carry the meaning alone.
// A project with no goals renders `Goals: none`.
func RenderStatsLine(ui render.UI, c StatsCounts) string {
	if c.Total == 0 {
		return ui.Bold("Goals:") + " " + ui.Dim("none")
	}
	seg := func(label string, n int, paint func(string) string) string {
		s := fmt.Sprintf("%s %d", label, n)
		if n == 0 {
			return ui.Dim(s)
		}
		return paint(s)
	}
	return ui.Bold("Goals:") + " " + strings.Join([]string{
		seg("Ready", c.Ready, ui.Yellow),
		seg("WIP", c.WIP, ui.Cyan),
		seg("Blocked", c.Blocked, ui.Red),
		seg("Done", c.Done, ui.Green),
	}, ui.Dim(" · "))
}

// PrintGlobalStatsTable writes the global stats view to stdout.
func PrintGlobalStatsTable(ui render.UI, res *GlobalAuditResult) {
	FprintGlobalStatsTable(os.Stdout, ui, res)
}

// PrintProjectStatsTable writes the project stats view to stdout.
func PrintProjectStatsTable(ui render.UI, res *AuditResult) {
	FprintProjectStatsTable(os.Stdout, ui, res)
}
