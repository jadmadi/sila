package main

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/config"
	"github.com/jadmadi/sila/internal/dream"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

var reWindow = regexp.MustCompile(`^(\d+)\s*([a-zA-Z]+)$`)

// parseHumanStandupWindow parses a human-friendly duration window from CLI args
// (e.g. ["last", "5h"], ["last", "2d"], ["last", "1w"], ["last", "30m"], ["5h"], ["2d"], ["last", "week"])
// or falls back to sinceFlag, defaulting to "24h".
func parseHumanStandupWindow(args []string, sinceFlag string) (string, error) {
	if len(args) == 0 {
		if strings.TrimSpace(sinceFlag) != "" {
			return strings.TrimSpace(sinceFlag), nil
		}
		return "24h", nil
	}

	raw := strings.ToLower(strings.TrimSpace(strings.Join(args, " ")))
	raw = strings.TrimPrefix(raw, "last")
	raw = strings.TrimPrefix(raw, "-")
	raw = strings.TrimPrefix(raw, "_")
	raw = strings.TrimSpace(raw)

	switch raw {
	case "", "day", "1 day":
		return "24h", nil
	case "week", "1 week":
		return "1w", nil
	case "month", "1 month":
		return "30d", nil
	case "hour", "1 hour":
		return "1h", nil
	}

	m := reWindow.FindStringSubmatch(raw)
	if len(m) == 3 {
		n, err := strconv.Atoi(m[1])
		if err != nil || n <= 0 {
			return "", fmt.Errorf("invalid window number %q", m[1])
		}
		unit := strings.ToLower(m[2])
		switch unit {
		case "h", "hr", "hrs", "hour", "hours":
			return fmt.Sprintf("%dh", n), nil
		case "d", "day", "days":
			return fmt.Sprintf("%dd", n), nil
		case "w", "wk", "wks", "week", "weeks":
			return fmt.Sprintf("%dw", n), nil
		case "m", "min", "mins", "minute", "minutes":
			return fmt.Sprintf("%dm", n), nil
		case "mo", "month", "months":
			return fmt.Sprintf("%dd", n*30), nil
		case "s", "sec", "secs", "second", "seconds":
			return fmt.Sprintf("%ds", n), nil
		}
	}

	// Check if already a valid Go duration or date timestamp
	if _, err := time.ParseDuration(raw); err == nil {
		return raw, nil
	}
	if _, err := time.Parse(time.RFC3339, raw); err == nil {
		return raw, nil
	}
	if _, err := time.Parse("2006-01-02", raw); err == nil {
		return raw, nil
	}

	return "", fmt.Errorf("invalid window %q (expected e.g. \"last 5h\", \"2d\", \"1w\", \"30m\", or --since=24h)", strings.Join(args, " "))
}

// runDigest implements `sila digest`: the multi-agent activity and status engine.
// By default it renders the agent-facing standup briefing. With --activity it renders
// compact source-attributed session history. With --human it renders the human briefing.
func runDigest(ui render.UI, dbPath, project, sinceFlag, toolFlag string, top int, topSpecified, jsonOut, toonOut, humanOut, activityOut, globalFlag bool, args []string) {
	if activityOut && humanOut {
		fmt.Fprintln(os.Stderr, "sila digest: --activity cannot be combined with --human")
		os.Exit(2)
	}

	if activityOut && globalFlag {
		fmt.Fprintln(os.Stderr, "sila digest: --activity requires a single project; --global is not supported")
		os.Exit(2)
	}

	sinceWindow, err := parseHumanStandupWindow(args, sinceFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila digest: %v\n", err)
		os.Exit(2)
	}

	if activityOut {
		runActivity(ui, dbPath, project, sinceWindow, toolFlag, top, topSpecified, jsonOut, toonOut)
		return
	}

	if humanOut {
		runHumanBriefing(ui, dbPath, project, sinceWindow, jsonOut, toonOut, globalFlag)
		return
	}
	st, _, err := openReadStore(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	cwd, _ := os.Getwd()
	if project == "" && !globalFlag {
		project = store.ProjectID(cwd)
	}

	report, err := dream.BuildDigest(st, project, sinceWindow, cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila digest: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(report)
		return
	}

	if toonOut {
		fmt.Fprint(os.Stdout, dream.RenderDigestTOON(report))
		return
	}

	fmt.Fprint(os.Stdout, dream.RenderDigestMarkdown(report))

	// Footer hint.
	if report.TotalMessages == 0 && report.TotalNewFacts == 0 && report.TotalHandoffs == 0 {
		fmt.Fprintln(os.Stdout, ui.Dim("\n_No activity in this window. Try `sila digest --since=7d` for a wider view._"))
	}
}

// runActivity implements `sila digest --activity`: compact source-attributed session list.
func runActivity(ui render.UI, dbPath, projectSel, sinceFlag, toolFlag string, top int, topSpecified, jsonOut, toonOut bool) {
	if topSpecified && (top < 1 || top > 100) {
		fmt.Fprintf(os.Stderr, "sila digest: --top must be between 1 and 100 (got %d)\n", top)
		os.Exit(2)
	}
	if !topSpecified || top <= 0 {
		top = 10
	}

	sinceTime, sinceDesc, err := parseActivitySince(sinceFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila digest: %v\n", err)
		os.Exit(2)
	}

	st, _, err := openReadStore(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	cwd, _ := os.Getwd()
	var resolvedProject store.Project
	if strings.TrimSpace(projectSel) != "" {
		p, err := st.ResolveProject(projectSel)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila digest: %v\n", err)
			os.Exit(2)
		}
		resolvedProject = p
	} else {
		p, ok, err := st.ProjectForDir(cwd)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila digest: %v\n", err)
			os.Exit(1)
		}
		if !ok || p.ID == "" {
			fmt.Fprintf(os.Stderr, "sila digest: current directory is not inside a known project; specify --project=<id>\n")
			os.Exit(2)
		}
		resolvedProject = p
	}

	res, err := st.RecentActivity(store.ActivityQueryOpts{
		ProjectID: resolvedProject.ID,
		Tool:      toolFlag,
		SinceTime: sinceTime,
		Top:       top,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila digest: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(res)
		return
	}

	if toonOut {
		fmt.Fprint(os.Stdout, renderActivityTOON(res))
		return
	}

	renderActivityCLI(ui, res, sinceDesc)
}

// parseActivitySince turns the --since flag into a time and display label.
func parseActivitySince(s string) (time.Time, string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Now().UTC().Add(-24 * time.Hour), "last 24h", nil
	}
	// RFC3339
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), "since " + s, nil
	}
	// YYYY-MM-DD
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.UTC(), "since " + s, nil
	}
	// 'd' (days) suffix
	if before, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.Atoi(before)
		if err == nil && days > 0 {
			return time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour), "last " + s, nil
		}
	}
	// 'w' (weeks) suffix
	if before, ok := strings.CutSuffix(s, "w"); ok {
		weeks, err := strconv.Atoi(before)
		if err == nil && weeks > 0 {
			return time.Now().UTC().Add(-time.Duration(weeks) * 7 * 24 * time.Hour), "last " + s, nil
		}
	}
	// 'mo' (months) suffix
	if before, ok := strings.CutSuffix(s, "mo"); ok {
		mos, err := strconv.Atoi(before)
		if err == nil && mos > 0 {
			return time.Now().UTC().Add(-time.Duration(mos) * 30 * 24 * time.Hour), "last " + s, nil
		}
	}
	// Go duration
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return time.Now().UTC().Add(-d), "last " + s, nil
	}
	return time.Time{}, "", fmt.Errorf("invalid --since %q (wanted duration e.g. 24h, 7d, 1mo or RFC3339 timestamp)", s)
}

// renderActivityTOON produces compact Token-Oriented Object Notation for activity reports.
func renderActivityTOON(res *store.ActivityResult) string {
	var b strings.Builder
	summaryFields := [][2]string{
		{"project", res.ProjectID},
		{"since", res.Since},
		{"total_matching", strconv.Itoa(res.TotalMatching)},
		{"returned", strconv.Itoa(len(res.Sessions))},
		{"top", strconv.Itoa(res.Top)},
	}
	if res.ToolFilter != "" {
		summaryFields = append(summaryFields, [2]string{"tool_filter", res.ToolFilter})
	}
	if res.LegacyMessagesCount > 0 {
		summaryFields = append(summaryFields, [2]string{"legacy_messages", strconv.Itoa(res.LegacyMessagesCount)})
	}
	b.WriteString(render.FormatTOONObject("activity_summary", summaryFields))

	headers := []string{"time", "tool", "session", "request_id", "request_occ", "reply_id", "reply_occ", "request", "reply"}
	var rows [][]string
	for _, s := range res.Sessions {
		reqID := ""
		reqOcc := ""
		reqText := ""
		if s.Request != nil {
			reqID = strconv.FormatInt(s.Request.MessageID, 10)
			reqOcc = strconv.Itoa(s.Request.Occurrence)
			reqText = s.Request.Text
		}
		replyID := ""
		replyOcc := ""
		replyText := ""
		if s.LatestReply != nil {
			replyID = strconv.FormatInt(s.LatestReply.MessageID, 10)
			replyOcc = strconv.Itoa(s.LatestReply.Occurrence)
			replyText = s.LatestReply.Text
		}
		rows = append(rows, []string{
			s.LastActiveAt,
			s.Tool,
			s.SessionID,
			reqID,
			reqOcc,
			replyID,
			replyOcc,
			reqText,
			replyText,
		})
	}
	b.WriteString(render.FormatTOONTable("sessions", headers, rows))
	return b.String()
}

// renderActivityCLI formats the human-readable terminal output styled via internal/render.
func renderActivityCLI(ui render.UI, res *store.ActivityResult, sinceDesc string) {
	header := ui.Cyan(ui.Bold("sila · activity")) + " · " + ui.Bold(res.ProjectID)
	if res.ToolFilter != "" {
		header += " · " + ui.Bold(res.ToolFilter)
	}
	header += " · " + sinceDesc + " · " + ui.Dim("indexed history")
	fmt.Fprintln(os.Stdout, header)

	if len(res.Sessions) == 0 {
		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout, ui.Dim("No recent activity in this window (indexed history)."))
		fmt.Fprintln(os.Stdout, ui.Dim("Run `sila sync` to index recent sessions, or use `--since=7d` for a wider window."))
		if res.LegacyMessagesCount > 0 {
			fmt.Fprintln(os.Stdout)
			fmt.Fprintf(os.Stdout, "%s %s\n",
				ui.Yellow("Note:"),
				fmt.Sprintf("%d message(s) in this window have no session provenance; use `sila search` for raw history.", res.LegacyMessagesCount),
			)
		}
		return
	}

	// Column plan: two spaces of indent, an 8-column label, a "01-02 15:04"
	// stamp, then the text. Every wrapped continuation is indented to the text
	// column so a long excerpt still reads as one entry.
	const (
		labelWidth  = 8
		stampWidth  = 11
		textColumn  = 2 + labelWidth + stampWidth + 2
		minTextRoom = 30
	)
	budget := max(termWidth()-textColumn, minTextRoom)
	indent := strings.Repeat(" ", textColumn)

	for _, s := range res.Sessions {
		fmt.Fprintln(os.Stdout)

		toolStr := fmt.Sprintf("%-8s", s.Tool)
		shortSess := store.ShortenSessionID(s.SessionID)

		fmt.Fprintf(os.Stdout, "%s  %s  %s%s\n",
			ui.Dim(store.FormatActivityTime(s.LastActiveAt)),
			ui.Bold(toolStr),
			ui.Dim("session "),
			ui.Cyan(shortSess),
		)

		writeActivityExcerpt(ui, "Request", s.Request, "(no request in window)", indent, budget)
		writeActivityExcerpt(ui, "Reply", s.LatestReply, "(no reply in window)", indent, budget)

		if s.LatestReply != nil {
			fmt.Fprintf(os.Stdout, "  %s%s\n",
				ui.Dim("Inspect reply: "),
				ui.Cyan(fmt.Sprintf("sila search expand %d --occurrence=%d", s.LatestReply.MessageID, s.LatestReply.Occurrence)),
			)
		} else if s.Request != nil {
			fmt.Fprintf(os.Stdout, "  %s%s\n",
				ui.Dim("Inspect request: "),
				ui.Cyan(fmt.Sprintf("sila search expand %d --occurrence=%d", s.Request.MessageID, s.Request.Occurrence)),
			)
		}
	}

	if res.TotalMatching > len(res.Sessions) {
		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout, ui.Dim(fmt.Sprintf("Showing %d of %d matching sessions. Use --top=%d to view more.", len(res.Sessions), res.TotalMatching, res.TotalMatching)))
	}

	if res.LegacyMessagesCount > 0 {
		fmt.Fprintln(os.Stdout)
		fmt.Fprintf(os.Stdout, "%s %s\n",
			ui.Yellow("Note:"),
			fmt.Sprintf("%d message(s) in this window have no session provenance (ingested before occurrence tracking); use `sila search` for raw history.", res.LegacyMessagesCount),
		)
	}
}

// writeActivityExcerpt prints one request or reply line. The stamp is printed
// beside the excerpt so a reader can see that a session's earliest request and
// latest reply may be hours apart, rather than reading them as one exchange.
func writeActivityExcerpt(ui render.UI, label string, ex *store.ActivityExcerpt, empty, indent string, budget int) {
	padded := fmt.Sprintf("%-8s", label)
	if ex == nil {
		fmt.Fprintf(os.Stdout, "  %s%s\n", ui.Dim(padded), ui.Dim(empty))
		return
	}
	fmt.Fprintf(os.Stdout, "  %s%s  %s\n",
		ui.Dim(padded),
		ui.Dim(store.FormatActivityTime(ex.Timestamp)),
		render.Wrap(ex.Text, budget, indent),
	)
}

// resolveDigestDBPath returns the db path, defaulting to the standard location
// when the caller passes an empty string.
func resolveDigestDBPath(dbPath string) string {
	if dbPath != "" {
		return dbPath
	}
	return config.DBPath()
}
