package dream

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/assist"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

// RecapCommit is one shipped commit in the window.
type RecapCommit struct {
	Project string `json:"project,omitempty"`
	Hash    string `json:"hash"`
	Subject string `json:"subject"`
	Time    int64  `json:"-"`
}

// RecapReport is the human-centric daily executive briefing: what the agent
// team decided, what shipped, what is still open, and what needs a human.
// It contrasts with the agent-facing `sila prime` briefing.
type RecapReport struct {
	GeneratedAt      string        `json:"generated_at"`
	Since            string        `json:"since"`
	Project          string        `json:"project,omitempty"`
	TotalMessages    int           `json:"total_messages"`
	ToolsActive      int           `json:"tools_active"`
	SessionsDaily    []int         `json:"sessions_daily,omitempty"`
	Commits          []RecapCommit `json:"commits,omitempty"`
	Decisions        []store.Fact  `json:"decisions,omitempty"`
	OpenItems        []string      `json:"open_items,omitempty"`
	NeedsApproval    []string      `json:"needs_approval,omitempty"`
	ActivityInsights []string      `json:"activity_insights,omitempty"`
	Interactive      bool          `json:"-"`
}

// BuildRecap synthesizes the human executive briefing over the window.
// projectSel "" scopes to all projects; repoDir grounds the goal board and
// git history. st may be nil (no store yet) — the report degrades to
// commit/goal data only.
func BuildRecap(st *store.Store, projectSel, sinceDur, repoDir string) (*RecapReport, error) {
	// No store yet — degrade to commit/goal data only instead of failing.
	if st == nil {
		sinceRFC := computeSinceRFC3339(sinceDur)
		return &RecapReport{
			GeneratedAt: time.Now().UTC().Format(time.RFC3339),
			Since:       sinceRFC,
			Project:     projectSel,
			Commits:     gitCommitsSince(repoDir, sinceRFC, projectSel),
		}, nil
	}

	digest, err := BuildDigest(st, projectSel, sinceDur, repoDir)
	if err != nil {
		return nil, err
	}

	r := &RecapReport{
		GeneratedAt:   digest.GeneratedAt,
		Since:         digest.Since,
		Project:       digest.Project,
		TotalMessages: digest.TotalMessages,
		ToolsActive:   len(digest.ToolActivity),
	}

	if daily, _, err := st.RecentDailySessionActivity(projectSel, 7); err == nil {
		r.SessionsDaily = daily
	}

	// 1. Decisions: invariants and explicit memos recorded in the window.
	for _, f := range digest.NewInvariants {
		if f.IsNonGoal() || f.IsMilestone() {
			continue
		}
		r.Decisions = append(r.Decisions, f)
	}

	// 2. Commits shipped in the window (effect-first subjects).
	if projectSel != "" {
		r.Commits = gitCommitsSince(repoDir, digest.Since, projectSel)
	} else {
		// Global mode: collect recent commits from all known project paths.
		seenDirs := make(map[string]bool)
		if repoDir != "" {
			seenDirs[filepath.Clean(repoDir)] = true
			r.Commits = append(r.Commits, gitCommitsSince(repoDir, digest.Since, store.ProjectID(repoDir))...)
		}
		projects, err := st.Projects()
		if err == nil {
			for _, p := range projects {
				dir := filepath.Clean(p.Path)
				if seenDirs[dir] {
					continue
				}
				seenDirs[dir] = true
				r.Commits = append(r.Commits, gitCommitsSince(dir, digest.Since, p.ID)...)
			}
		}
		sort.Slice(r.Commits, func(i, j int) bool {
			return r.Commits[i].Time > r.Commits[j].Time
		})
		if len(r.Commits) > 15 {
			r.Commits = r.Commits[:15]
		}
	}

	// 3. Open action items: handoff checklists + recently filed feedback.
	// Items that reference an implemented goal are stale; drop handoff items
	// and turn stale feedback into an explicit resolve prompt.
	implemented := implementedGoalSet(digest)
	for _, h := range digest.OpenHandoffItems {
		for _, item := range h.OpenItems {
			if _, stale := implementedGoalIn(item, implemented); stale {
				continue
			}
			if projectSel == "" && h.ProjectID != "" {
				r.OpenItems = append(r.OpenItems, fmt.Sprintf("[%s] %s (from %s handoff on %s)", h.ProjectID, item, h.Agent, h.Branch))
			} else {
				r.OpenItems = append(r.OpenItems, fmt.Sprintf("%s (from %s handoff on %s)", item, h.Agent, h.Branch))
			}
		}
	}
	if st != nil {
		if fb, err := st.QueryFeedback(store.FeedbackFilter{ProjectID: projectSel, Limit: 100}); err == nil {
			for _, f := range fb {
				if f.Status != "open" && f.Status != "in_progress" {
					continue
				}
				if f.CreatedAt < digest.Since {
					continue
				}
				if slug, stale := implementedGoalIn(f.Content, implemented); stale {
					r.NeedsApproval = append(r.NeedsApproval, fmt.Sprintf(
						"Open feedback %s references implemented goal %q — resolve it with `sila feedback resolve %s` if done.",
						f.ID, slug, f.ID))
					continue
				}
				if projectSel == "" && f.ProjectID != "" {
					r.OpenItems = append(r.OpenItems, fmt.Sprintf("[%s] [%s] %s (filed by %s)", f.ProjectID, f.Category, f.Content, f.Agent))
				} else {
					r.OpenItems = append(r.OpenItems, fmt.Sprintf("[%s] %s (filed by %s)", f.Category, f.Content, f.Agent))
				}
			}
		}
	}

	// 4. Items needing human approval or attention.
	for _, f := range digest.StaleFacts {
		if projectSel == "" && f.ProjectID != "" {
			r.NeedsApproval = append(r.NeedsApproval, fmt.Sprintf("[%s] Unverified claim references missing files — review: %s", f.ProjectID, truncateClaim(f.Claim, 140)))
		} else {
			r.NeedsApproval = append(r.NeedsApproval, fmt.Sprintf("Unverified claim references missing files — review: %s", truncateClaim(f.Claim, 140)))
		}
	}
	for _, g := range digest.ClaimedGoalList {
		agent := ""
		if g.Claim != nil && g.Claim.Agent != "" {
			agent = g.Claim.Agent
		}
		r.NeedsApproval = append(r.NeedsApproval, fmt.Sprintf("Goal %q is in progress by agent %q — review when it reports done", g.Title, agent))
	}

	// 5. Optional semantic assistance (fail-open: nil if disabled or unavailable)
	if st != nil {
		if provider := assist.ActiveProviderFor("recap", projectSel); provider != nil {
			msgs, _ := st.MessagesSince(projectSel, digest.Since, 20)
			r.ActivityInsights = assist.InterpretActivity(context.Background(), provider, msgs)
		}
	}

	return r, nil
}

// implementedGoalSet returns the lower-cased slugs of goals the digest
// verified as implemented.
func implementedGoalSet(d *DigestReport) map[string]struct{} {
	out := make(map[string]struct{}, len(d.ClosedGoals))
	for _, g := range d.ClosedGoals {
		out[strings.ToLower(g.Slug)] = struct{}{}
	}
	return out
}

// implementedGoalIn reports whether text references one of the implemented
// goals. It matches whole tokens and requires the word "goal" in the text, so
// a slug that is also a common word cannot silently hide an open item.
func implementedGoalIn(text string, implemented map[string]struct{}) (string, bool) {
	if len(implemented) == 0 || !strings.Contains(strings.ToLower(text), "goal") {
		return "", false
	}
	tokens := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(r == '-' || r == '_' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
	})
	for _, tok := range tokens {
		if _, ok := implemented[tok]; ok {
			return tok, true
		}
	}
	return "", false
}

// gitCommitsSince lists commit subjects (max 10) committed since sinceRFC.
func gitCommitsSince(dir, sinceRFC, projectID string) []RecapCommit {
	if dir == "" {
		return nil
	}
	out, err := exec.Command("git", "-C", dir, "log", "--since="+sinceRFC, "--pretty=format:%h%x1f%s%x1f%ct", "-n", "10").Output()
	if err != nil {
		return nil
	}
	var commits []RecapCommit
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "\x1f", 3)
		if len(parts) < 2 || parts[1] == "" {
			continue
		}
		var ts int64
		if len(parts) >= 3 {
			ts, _ = strconv.ParseInt(strings.TrimSpace(parts[2]), 10, 64)
		}
		commits = append(commits, RecapCommit{
			Project: projectID,
			Hash:    parts[0],
			Subject: parts[1],
			Time:    ts,
		})
	}
	return commits
}

// truncateClaim shortens a claim to at most max runes with an ellipsis.
func truncateClaim(claim string, max int) string {
	claim = strings.TrimSpace(claim)
	runes := []rune(claim)
	if len(runes) <= max {
		return claim
	}
	return string(runes[:max]) + "…"
}

// RenderRecapMarkdown formats a RecapReport as a human-readable morning
// briefing in Markdown.
func RenderRecapMarkdown(r *RecapReport) string {
	if r == nil {
		return "_Nothing to recap._\n"
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

	if r.Interactive && len(r.SessionsDaily) > 0 {
		totSessions := 0
		for _, c := range r.SessionsDaily {
			totSessions += c
		}
		spark := render.SparklineInts(r.SessionsDaily)
		fmt.Fprintf(&b, "> Sessions (7d): %s [%d total]\n\n", spark, totSessions)
	}

	fmt.Fprintf(&b, "**%d agent messages** · **%d decisions recorded** · **%d commits shipped** · **%d open items** · **%d need you**\n\n",
		r.TotalMessages, len(r.Decisions), len(r.Commits), len(r.OpenItems), len(r.NeedsApproval))

	b.WriteString("## Decisions Recorded\n\n")
	if len(r.Decisions) == 0 {
		b.WriteString("_No architectural decisions recorded in this window._\n\n")
	} else {
		for _, f := range r.Decisions {
			if (r.Project == "" || r.Project == "all") && f.ProjectID != "" {
				fmt.Fprintf(&b, "- [%s] %s\n", f.ProjectID, truncateClaim(f.Claim, 140))
			} else {
				fmt.Fprintf(&b, "- %s\n", truncateClaim(f.Claim, 140))
			}
		}
		b.WriteString("\n")
	}

	b.WriteString("## Commits Shipped\n\n")
	if len(r.Commits) == 0 {
		b.WriteString("_No commits in this window._\n\n")
	} else {
		for _, c := range r.Commits {
			if (r.Project == "" || r.Project == "all") && c.Project != "" {
				fmt.Fprintf(&b, "- `%s` [%s] %s\n", c.Hash, c.Project, c.Subject)
			} else {
				fmt.Fprintf(&b, "- `%s` %s\n", c.Hash, c.Subject)
			}
		}
		b.WriteString("\n")
	}

	b.WriteString("## Open Action Items\n\n")
	if len(r.OpenItems) == 0 {
		b.WriteString("_Nothing open — all clear._\n\n")
	} else {
		for _, item := range r.OpenItems {
			fmt.Fprintf(&b, "- [ ] %s\n", item)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Needs Human Approval\n\n")
	if len(r.NeedsApproval) == 0 {
		b.WriteString("_Nothing blocked or unverified — no action required._\n\n")
	} else {
		for _, n := range r.NeedsApproval {
			fmt.Fprintf(&b, "- ⚠ %s\n", n)
		}
		b.WriteString("\n")
	}

	if len(r.ActivityInsights) > 0 {
		b.WriteString("## Activity Insights (Assisted)\n\n")
		for _, insight := range r.ActivityInsights {
			fmt.Fprintf(&b, "- 💡 %s\n", insight)
		}
		b.WriteString("\n")
	}

	return b.String()
}

// RenderRecapTOON formats a RecapReport as compact Token-Oriented Object Notation.
func RenderRecapTOON(r *RecapReport) string {
	if r == nil {
		return ""
	}
	var b strings.Builder
	scope := "all"
	if r.Project != "" {
		scope = r.Project
	}
	summaryFields := [][2]string{
		{"generated_at", r.GeneratedAt},
		{"since", r.Since},
		{"scope", scope},
		{"total_messages", strconv.Itoa(r.TotalMessages)},
		{"tools_active", strconv.Itoa(r.ToolsActive)},
		{"decisions", strconv.Itoa(len(r.Decisions))},
		{"commits", strconv.Itoa(len(r.Commits))},
		{"open_items", strconv.Itoa(len(r.OpenItems))},
		{"needs_approval", strconv.Itoa(len(r.NeedsApproval))},
	}
	if len(r.SessionsDaily) > 0 {
		var sessStrs []string
		for _, c := range r.SessionsDaily {
			sessStrs = append(sessStrs, strconv.Itoa(c))
		}
		summaryFields = append(summaryFields, [2]string{"sessions_daily", "[" + strings.Join(sessStrs, ",") + "]"})
	}
	b.WriteString(render.FormatTOONObject("standup_summary", summaryFields))

	if len(r.Decisions) > 0 {
		var rows [][]string
		for _, f := range r.Decisions {
			rows = append(rows, []string{f.ProjectID, truncateClaim(f.Claim, 120)})
		}
		b.WriteString(render.FormatTOONTable("decisions", []string{"project", "claim"}, rows))
	}

	if len(r.Commits) > 0 {
		var rows [][]string
		for _, c := range r.Commits {
			p := c.Project
			if p == "" {
				p = scope
			}
			rows = append(rows, []string{p, c.Hash, c.Subject})
		}
		b.WriteString(render.FormatTOONTable("commits", []string{"project", "hash", "subject"}, rows))
	}

	if len(r.OpenItems) > 0 {
		var rows [][]string
		for _, item := range r.OpenItems {
			rows = append(rows, []string{item})
		}
		b.WriteString(render.FormatTOONTable("open_items", []string{"item"}, rows))
	}

	if len(r.NeedsApproval) > 0 {
		var rows [][]string
		for _, n := range r.NeedsApproval {
			rows = append(rows, []string{n})
		}
		b.WriteString(render.FormatTOONTable("needs_approval", []string{"item"}, rows))
	}

	if len(r.ActivityInsights) > 0 {
		var rows [][]string
		for _, insight := range r.ActivityInsights {
			rows = append(rows, []string{insight})
		}
		b.WriteString(render.FormatTOONTable("activity_insights", []string{"insight"}, rows))
	}

	return b.String()
}
