package main

import (
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/agents"
	"github.com/jadmadi/sila/internal/assess"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

type statusRow struct {
	name, synced string
	total, new   int64
}

// jsonStatus is the machine-readable status shape.
type jsonStatus struct {
	StoreExists   bool     `json:"store_exists"`
	Scanned       bool     `json:"scanned"`
	InSync        bool     `json:"in_sync"`
	Messages      int      `json:"messages"`
	Claims        int      `json:"claims"`
	Facts         int      `json:"facts"`
	FileRefsExist int      `json:"file_refs_exist"`
	Verified      int      `json:"verified"`
	Projects      int      `json:"projects"`
	Pending       int64    `json:"pending"`
	PendingTools  []string `json:"pending_tools,omitempty"`
	StaleClaims   int      `json:"stale_claims"`
	StaleFacts    int      `json:"stale_facts"`
	ClaimVelocity []int    `json:"claim_velocity,omitempty"`
	GroundedTrend []int    `json:"grounded_trend,omitempty"`
	PageGrowth    []int    `json:"page_growth,omitempty"`
	LastAgentSync string   `json:"last_agent_sync,omitempty"`
	HubCommit     string   `json:"hub_commit,omitempty"`
}

// runStatus is the sync-health dashboard: a verdict plus per-tool freshness and
// pending work, store totals, fact staleness, and activity sparklines. Read-only.
func runStatus(ui render.UI, dbPath string, jsonOut, verbose, scan bool) {
	if _, err := os.Stat(dbPath); err != nil {
		if jsonOut {
			emitJSON(jsonStatus{StoreExists: false})
		} else {
			fmt.Fprintln(os.Stdout, ui.Dim("No knowledge store yet. Run `sila sync` to populate it."))
		}
		return
	}
	st, err := store.OpenRO(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	msgs, facts, verified, projects, err := st.Counts()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}
	wmTimes, err := st.WatermarkTimes()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}

	// Observability trends (7-day claim velocity, grounding, and page growth)
	claimCounts, groundedCounts, totalClaims, totalGrounded, _ := st.DailyFactTrend("", 7)
	pageTrend, pageCount, dbBytes, _ := st.DailyStorageGrowthTrend(7)

	var rows []statusRow
	var pending int64
	var pendingTools []string
	if scan {
		reports, err := assess.Run(st, assess.Options{}) // live source scan, read-only
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila: %v\n", err)
			os.Exit(1)
		}
		for _, r := range reports {
			if r.NotScoped {
				continue
			}
			synced := "never"
			if at := wmTimes[r.Tool]; at != "" {
				if t, err := time.Parse(time.RFC3339, at); err == nil {
					synced = fmtAge(t)
				} else {
					synced = at
				}
			}
			rows = append(rows, statusRow{r.Tool, synced, r.Total, r.New})
			if r.New > 0 {
				pending += r.New
				pendingTools = append(pendingTools, r.Tool)
			}
		}
	} else {
		// lite (default): stored counts + watermark ages only — fast, no live scan.
		for tool, at := range wmTimes {
			synced := "never"
			if t, err := time.Parse(time.RFC3339, at); err == nil {
				synced = fmtAge(t)
			} else {
				synced = at
			}
			rows = append(rows, statusRow{tool, synced, 0, 0})
		}
	}
	// Claims referencing files that disappeared on disk. Unapproved candidates
	// and conceptual invariants are excluded: they are not filesystem drift.
	stale := 0
	if staleFacts, sErr := st.StaleFacts("", 0); sErr == nil {
		stale = len(staleFacts)
	}
	inSync := scan && pending == 0

	var lastAgentSync, hubCommit string
	if rec, err := agents.LoadLastSyncReceipt(st); err == nil && rec != nil {
		lastAgentSync = agents.FormatRelativeAge(rec.Timestamp, time.Now().UTC())
		hubCommit = rec.HubCommit
	}

	if jsonOut {
		emitJSON(jsonStatus{
			StoreExists:   true,
			Scanned:       scan,
			InSync:        inSync,
			Messages:      msgs,
			Claims:        facts,
			Facts:         facts,
			FileRefsExist: verified,
			Verified:      verified,
			Projects:      projects,
			Pending:       pending,
			PendingTools:  pendingTools,
			StaleClaims:   stale,
			StaleFacts:    stale,
			ClaimVelocity: claimCounts,
			GroundedTrend: groundedCounts,
			PageGrowth:    pageTrend,
			LastAgentSync: lastAgentSync,
			HubCommit:     hubCommit,
		})
		return
	}

	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · status")))
	fmt.Fprintf(os.Stdout, "  %d messages · %d claims (%d file_refs_exist) · %d projects\n",
		msgs, facts, verified, projects)
	if lastAgentSync != "" {
		fmt.Fprintf(os.Stdout, "  Agent rules: synced %s (%s)\n", ui.Bold(lastAgentSync), ui.Dim("hub: "+hubCommit))
	}

	// Sparklines for claim grounding and database page growth (interactive terminals only)
	if ui.IsTerminal() {
		if totalClaims > 0 {
			groundedPct := 0
			if totalClaims > 0 {
				groundedPct = int(math.Round(float64(totalGrounded) / float64(totalClaims) * 100))
			}
			spark := render.SparklineInts(claimCounts)
			fmt.Fprintf(os.Stdout, "  Claims velocity (7d):  %s  [%s new · %d%% grounded]\n",
				ui.Cyan(spark), render.FormatNumber(int64(totalClaims)), groundedPct)
		}
		if pageCount > 0 {
			spark := render.SparklineInts(pageTrend)
			fmt.Fprintf(os.Stdout, "  Database growth (7d):  %s  [%s · %s pages]\n",
				ui.Cyan(spark), render.FormatBytes(dbBytes), render.FormatNumber(pageCount))
		}
	}

	fmt.Fprintln(os.Stdout)

	var verdict string
	switch {
	case scan && inSync:
		verdict = ui.Green("IN SYNC — everything is imported and caught up.")
	case scan:
		verdict = ui.Bold(ui.Yellow(fmt.Sprintf("NEEDS SYNC — %d new %s pending from %s.",
			pending, plural(pending, "message"), strings.Join(pendingTools, ", "))))
	default:
		verdict = ui.Dim("Store snapshot (live source scan skipped).")
	}
	fmt.Fprintln(os.Stdout, verdict)
	if stale > 0 {
		fmt.Fprintln(os.Stdout, "  "+ui.Dim(fmt.Sprintf("%d %s reference files not found on disk (stale). Run `sila dream query --stale` to inspect.",
			stale, plural(int64(stale), "claim"))))
	}
	switch {
	case scan && inSync:
		fmt.Fprintln(os.Stdout, actionLine(ui, "sila dream query <term>", "explore the distilled claims; or sync to stay current"))
	case scan:
		cmd := "sila sync"
		cwd, _ := os.Getwd()
		if p, ok, _ := st.ProjectForDir(cwd); ok && p.ID != "" {
			cmd = "sila sync --global"
		}
		fmt.Fprintln(os.Stdout, actionLine(ui, cmd, fmt.Sprintf("%d new messages waiting from %s", pending, strings.Join(pendingTools, ", "))))
	default:
		fmt.Fprintln(os.Stdout, actionLine(ui, "sila status --scan", "check sources for new messages (slower; ~10s)"))
	}
	fmt.Fprintln(os.Stdout)

	if scan {
		headers := []string{"tool", "last synced", "have", "new"}
		tblRows := make([][]string, 0, len(rows))
		for _, r := range rows {
			syncedCell := ui.Dim(r.synced)
			if r.synced == "never" {
				syncedCell = ui.Red("never")
			}
			totalCell := fmt.Sprint(r.total)
			if r.total == 0 {
				totalCell = ui.Dim("0")
			}
			newCell := ui.Dim("0")
			if r.new > 0 {
				newCell = ui.Bold(ui.Yellow(fmt.Sprint(r.new)))
			}
			tblRows = append(tblRows, []string{r.name, syncedCell, totalCell, newCell})
		}
		fmt.Fprintln(os.Stdout, ui.Table(headers, tblRows, []string{"l", "l", "r", "r"}))
		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout, ui.Dim("`new` = messages a `sync` would pull next. `have` = discoverable in sources."))
	} else if len(rows) > 0 {
		headers := []string{"tool", "last synced"}
		tblRows := make([][]string, 0, len(rows))
		for _, r := range rows {
			syncedCell := ui.Dim(r.synced)
			if r.synced == "never" {
				syncedCell = ui.Red("never")
			}
			tblRows = append(tblRows, []string{r.name, syncedCell})
		}
		fmt.Fprintln(os.Stdout, ui.Table(headers, tblRows, []string{"l", "l"}))
	}
}
