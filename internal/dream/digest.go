package dream

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/store"
)

// ToolActivity clusters per-tool metrics for the digest.
type ToolActivity struct {
	Tool          string `json:"tool"`
	Messages      int    `json:"messages"`
	LastMessageAt string `json:"last_message_at,omitempty"`
}

// DigestReport is the synthesized multi-agent standup briefing.
type DigestReport struct {
	GeneratedAt      string          `json:"generated_at"`
	Since            string          `json:"since"`
	Project          string          `json:"project,omitempty"`
	TotalMessages    int             `json:"total_messages"`
	TotalNewFacts    int             `json:"total_new_facts"`
	TotalNewMemos    int             `json:"total_new_memos"`
	TotalHandoffs    int             `json:"total_handoffs"`
	OpenHandoffs     int             `json:"open_handoffs"`
	StaleClaims      int             `json:"stale_claims"`
	ImplementedGoals int             `json:"implemented_goals"`
	ClaimedGoals     int             `json:"claimed_goals"`
	ToolActivity     []ToolActivity  `json:"tool_activity"`
	NewInvariants    []store.Fact    `json:"new_invariants,omitempty"`
	OpenHandoffItems []store.Handoff `json:"open_handoff_items,omitempty"`
	StaleFacts       []store.Fact    `json:"stale_facts,omitempty"`
	ClosedGoals      []goals.Goal    `json:"closed_goals,omitempty"`
	ClaimedGoalList  []goals.Goal    `json:"claimed_goal_list,omitempty"`
}

// BuildDigest aggregates multi-agent activity since `sinceDur` into a single
// executive briefing. When projectSel is empty, activity across all projects
// is summarized. When goalsRoot is non-empty, recently implemented and claimed
// goals from that repository are surfaced.
func BuildDigest(st *store.Store, projectSel, sinceDur string, goalsRoot string) (*DigestReport, error) {
	sinceRFC := computeSinceRFC3339(sinceDur)
	report := &DigestReport{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Since:       sinceRFC,
		Project:     projectSel,
	}

	// 1. Messages grouped by tool.
	msgs, err := st.MessagesSince(projectSel, sinceRFC, 0)
	if err != nil {
		return nil, fmt.Errorf("query messages since %s: %w", sinceRFC, err)
	}
	report.TotalMessages = len(msgs)
	toolCounts := map[string]int{}
	toolLast := map[string]string{}
	for _, m := range msgs {
		for _, t := range m.SourceTools {
			toolCounts[t]++
			if m.TS > toolLast[t] {
				toolLast[t] = m.TS
			}
		}
	}
	for tool, n := range toolCounts {
		report.ToolActivity = append(report.ToolActivity, ToolActivity{
			Tool: tool, Messages: n, LastMessageAt: toolLast[tool],
		})
	}
	sort.Slice(report.ToolActivity, func(i, j int) bool {
		return report.ToolActivity[i].Messages > report.ToolActivity[j].Messages
	})

	// 2. New facts & invariants.
	facts, err := st.FactsSince(projectSel, sinceRFC, 0)
	if err != nil {
		return nil, fmt.Errorf("query facts since %s: %w", sinceRFC, err)
	}
	report.TotalNewFacts = len(facts)
	for _, f := range facts {
		if f.IsInvariant() || f.SessionRef == "manual_memo" {
			report.NewInvariants = append(report.NewInvariants, f)
			if f.SessionRef == "manual_memo" {
				report.TotalNewMemos++
			}
		}
	}

	// 3. Handoffs.
	handoffs, err := st.HandoffsSince(projectSel, sinceRFC, 0)
	if err != nil {
		return nil, fmt.Errorf("query handoffs since %s: %w", sinceRFC, err)
	}
	report.TotalHandoffs = len(handoffs)
	for _, h := range handoffs {
		if h.Status == "open" {
			report.OpenHandoffs++
			report.OpenHandoffItems = append(report.OpenHandoffItems, h)
		}
	}

	// 4. Stale claims (ungrounded, non-invariant).
	stale, err := st.StaleFacts(projectSel, 50)
	if err != nil {
		return nil, fmt.Errorf("query stale facts: %w", err)
	}
	report.StaleClaims = len(stale)
	report.StaleFacts = stale

	// 5. Goals (implemented & claimed) from the repository.
	if goalsRoot != "" {
		if audit, err := goals.ScanGoals(goalsRoot); err == nil && audit != nil {
			report.ImplementedGoals = audit.ImplementedCount
			report.ClaimedGoals = audit.ClaimedCount
			for _, g := range audit.Goals {
				if g.Status == "implemented" {
					report.ClosedGoals = append(report.ClosedGoals, g)
				} else if g.Status == "claimed" {
					report.ClaimedGoalList = append(report.ClaimedGoalList, g)
				}
			}
		}
	}

	return report, nil
}

// computeSinceRFC3339 turns a duration string (e.g. "24h", "7d") into an
// RFC3339 timestamp `now - dur`. Empty defaults to 24h.
func computeSinceRFC3339(sinceDur string) string {
	if sinceDur == "" {
		sinceDur = "24h"
	}
	dur, err := parseDigestDuration(sinceDur)
	if err != nil {
		dur = 24 * time.Hour
	}
	return time.Now().UTC().Add(-dur).Format(time.RFC3339)
}

// parseDigestDuration parses Go durations plus day/week suffixes (7d, 2w).
func parseDigestDuration(s string) (time.Duration, error) {
	if before, ok := strings.CutSuffix(s, "d"); ok {
		days := before
		var n int
		if _, err := fmt.Sscanf(days, "%d", &n); err != nil {
			return 0, err
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	if before, ok := strings.CutSuffix(s, "w"); ok {
		weeks := before
		var n int
		if _, err := fmt.Sscanf(weeks, "%d", &n); err != nil {
			return 0, err
		}
		return time.Duration(n) * 7 * 24 * time.Hour, nil
	}
	if before, ok := strings.CutSuffix(s, "mo"); ok {
		mos := before
		var n int
		if _, err := fmt.Sscanf(mos, "%d", &n); err != nil {
			return 0, err
		}
		return time.Duration(n) * 30 * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

// RenderDigestMarkdown formats a DigestReport as a concise 1-page executive
// standup in Markdown.
func RenderDigestMarkdown(r *DigestReport) string {
	if r == nil {
		return "_No activity to report._\n"
	}
	var b strings.Builder
	sinceLabel := r.Since
	if t, err := time.Parse(time.RFC3339, r.Since); err == nil {
		sinceLabel = t.Format("2006-01-02 15:04 MST")
	}
	scope := "all projects"
	if r.Project != "" {
		scope = r.Project
	}
	fmt.Fprintf(&b, "# sila · standup — %s\n\n", time.Now().UTC().Format("2006-01-02"))
	fmt.Fprintf(&b, "> Window: since %s · Scope: %s\n\n", sinceLabel, scope)

	// Headline metrics.
	fmt.Fprintf(&b, "**%d messages** across %d tools · **%d new facts** (%d memos) · **%d handoffs** (%d open) · **%d stale claims**",
		r.TotalMessages, len(r.ToolActivity), r.TotalNewFacts, r.TotalNewMemos, r.TotalHandoffs, r.OpenHandoffs, r.StaleClaims)
	if r.ImplementedGoals > 0 || r.ClaimedGoals > 0 {
		fmt.Fprintf(&b, " · **%d goals implemented** · **%d claimed**", r.ImplementedGoals, r.ClaimedGoals)
	}
	b.WriteString("\n\n---\n\n")

	// 1. Tool activity.
	b.WriteString("## Active Sessions by Tool\n\n")
	if len(r.ToolActivity) == 0 {
		b.WriteString("_No messages recorded in this window._\n\n")
	} else {
		b.WriteString("| Tool | Messages | Last Activity |\n| :--- | ---: | :--- |\n")
		for _, t := range r.ToolActivity {
			last := t.LastMessageAt
			if last == "" {
				last = "—"
			}
			fmt.Fprintf(&b, "| %s | %d | %s |\n", t.Tool, t.Messages, last)
		}
		b.WriteString("\n")
	}

	// 2. New invariants / memos.
	b.WriteString("## New Architectural Invariants & Memos\n\n")
	if len(r.NewInvariants) == 0 {
		b.WriteString("_No new invariants or memos recorded._\n\n")
	} else {
		for _, f := range r.NewInvariants {
			claim := strings.TrimSpace(f.Claim)
			if len(claim) > 120 {
				claim = claim[:120] + "…"
			}
			tag := "invariant"
			if f.SessionRef == "manual_memo" {
				tag = "memo"
			}
			fmt.Fprintf(&b, "- **[%s]** %s\n", tag, claim)
		}
		b.WriteString("\n")
	}

	// 3. Closed goals.
	b.WriteString("## Closed Goals & Tasks\n\n")
	if len(r.ClosedGoals) == 0 {
		b.WriteString("_No goals implemented in this window._\n\n")
	} else {
		for _, g := range r.ClosedGoals {
			commit := g.Commit
			if commit == "" {
				commit = "—"
			}
			fmt.Fprintf(&b, "- ✔ **%s** (%s) — commit `%s`\n", g.Title, g.Slug, commit)
		}
		b.WriteString("\n")
	}

	// 4. Claimed goals (in progress).
	if len(r.ClaimedGoalList) > 0 {
		b.WriteString("## In Progress (Claimed Goals)\n\n")
		for _, g := range r.ClaimedGoalList {
			agent := "unknown"
			if g.Claim != nil && g.Claim.Agent != "" {
				agent = g.Claim.Agent
			}
			fmt.Fprintf(&b, "- 🔵 **%s** — claimed by @%s\n", g.Title, agent)
		}
		b.WriteString("\n")
	}

	// 5. Open handoffs.
	b.WriteString("## Open Handoffs\n\n")
	if len(r.OpenHandoffItems) == 0 {
		b.WriteString("_No open handoff items._\n\n")
	} else {
		for _, h := range r.OpenHandoffItems {
			summary := strings.TrimSpace(h.Summary)
			if len(summary) > 100 {
				summary = summary[:100] + "…"
			}
			fmt.Fprintf(&b, "- **%s** (agent: %s, branch: %s)\n", summary, h.Agent, h.Branch)
			for _, item := range h.OpenItems {
				fmt.Fprintf(&b, "  - ☐ %s\n", item)
			}
		}
		b.WriteString("\n")
	}

	// 6. Stale claims alert.
	b.WriteString("## Stale Claims Alert\n\n")
	if r.StaleClaims == 0 {
		b.WriteString("_All claims grounded — no drift detected._\n\n")
	} else {
		fmt.Fprintf(&b, "**%d ungrounded claims** reference files not found on disk:\n\n", r.StaleClaims)
		for _, f := range r.StaleFacts {
			claim := strings.TrimSpace(f.Claim)
			if len(claim) > 100 {
				claim = claim[:100] + "…"
			}
			fmt.Fprintf(&b, "- ⚠ %s\n", claim)
		}
		b.WriteString("\n")
		b.WriteString("_Run `sila dream reconcile` or `sila dream query --stale` to inspect._\n")
	}

	return b.String()
}

// RenderDigestTOON formats a DigestReport in Token-Oriented Object Notation
// for token-efficient agent consumption.
func RenderDigestTOON(r *DigestReport) string {
	if r == nil {
		return "digest[0]{tool,messages,last}:\n"
	}
	var b strings.Builder
	// Headline object.
	fields := [][2]string{
		{"window", r.Since},
		{"messages", fmt.Sprintf("%d", r.TotalMessages)},
		{"new_facts", fmt.Sprintf("%d", r.TotalNewFacts)},
		{"new_memos", fmt.Sprintf("%d", r.TotalNewMemos)},
		{"handoffs", fmt.Sprintf("%d", r.TotalHandoffs)},
		{"open_handoffs", fmt.Sprintf("%d", r.OpenHandoffs)},
		{"stale_claims", fmt.Sprintf("%d", r.StaleClaims)},
		{"implemented_goals", fmt.Sprintf("%d", r.ImplementedGoals)},
		{"claimed_goals", fmt.Sprintf("%d", r.ClaimedGoals)},
	}
	b.WriteString(fmt.Sprintf("digest:\n"))
	for _, f := range fields {
		b.WriteString(fmt.Sprintf("  %s: %s\n", f[0], f[1]))
	}
	// Tool activity table.
	rows := make([][]string, len(r.ToolActivity))
	for i, t := range r.ToolActivity {
		rows[i] = []string{t.Tool, fmt.Sprintf("%d", t.Messages), t.LastMessageAt}
	}
	b.WriteString(fmt.Sprintf("tools[%d]{tool,messages,last}:\n", len(r.ToolActivity)))
	for _, row := range rows {
		b.WriteString(fmt.Sprintf("  %s\n", strings.Join(row, ",")))
	}
	// New invariants.
	b.WriteString(fmt.Sprintf("invariants[%d]{claim}:\n", len(r.NewInvariants)))
	for _, f := range r.NewInvariants {
		claim := strings.ReplaceAll(strings.TrimSpace(f.Claim), "\n", " ")
		if len(claim) > 100 {
			claim = claim[:100] + "…"
		}
		b.WriteString(fmt.Sprintf("  %s\n", claim))
	}
	// Stale claims.
	b.WriteString(fmt.Sprintf("stale[%d]{claim}:\n", len(r.StaleFacts)))
	for _, f := range r.StaleFacts {
		claim := strings.ReplaceAll(strings.TrimSpace(f.Claim), "\n", " ")
		if len(claim) > 100 {
			claim = claim[:100] + "…"
		}
		b.WriteString(fmt.Sprintf("  %s\n", claim))
	}
	return b.String()
}
