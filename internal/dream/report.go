package dream

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

// ContinuityReportSchema is the JSON schema id for the report.
const ContinuityReportSchema = "sila/continuity-report/v1"

// ReportMaxWindow bounds a continuity report to ninety days.
const ReportMaxWindow = 90 * 24 * time.Hour

// HarnessActivity is one row of the Harnesses section.
type HarnessActivity struct {
	Harness  string `json:"harness"`
	Sessions int    `json:"sessions"`
	Messages int64  `json:"messages"`
}

// Coverage is the primed-session ratio.
type Coverage struct {
	Primed   int `json:"primed"`
	Eligible int `json:"eligible"`
	Percent  int `json:"percent"`
}

// CrossHarnessEdge is one writer-reader-record triple.
type CrossHarnessEdge struct {
	Writer string `json:"writer"`
	Reader string `json:"reader"`
	Record string `json:"record"`
}

// KnowledgeDelta counts knowledge written in the window.
type KnowledgeDelta struct {
	Memos       int64 `json:"memos"`
	Lessons     int64 `json:"lessons"`
	GoalsClosed int64 `json:"goals_closed"`
	Handoffs    int64 `json:"handoffs"`
}

// HealthCounts is the health summary.
type HealthCounts struct {
	StaleFacts         int64 `json:"stale_facts"`
	DisputedDirectives int64 `json:"disputed_directives"`
	ExpiredMemos       int64 `json:"expired_memos"`
	BrokenChains       int64 `json:"broken_chains"`
}

// HandoffSummary is one open handoff row.
type HandoffSummary struct {
	Agent     string    `json:"agent"`
	Summary   string    `json:"summary"`
	OpenItems int       `json:"open_items"`
	CreatedAt time.Time `json:"created_at"`
}

// ContinuityReport is the deterministic weekly continuity report.
type ContinuityReport struct {
	Schema       string             `json:"schema"`
	Since        time.Time          `json:"since"`
	Until        time.Time          `json:"until"`
	Harnesses    []HarnessActivity  `json:"harnesses"`
	Coverage     Coverage           `json:"coverage"`
	CrossHarness []CrossHarnessEdge `json:"cross_harness"`
	Knowledge    KnowledgeDelta     `json:"knowledge"`
	Health       HealthCounts       `json:"health"`
	OpenHandoffs []HandoffSummary   `json:"open_handoffs"`
	Verdict      string             `json:"verdict"`
}

// BuildContinuityReport reads the local store only. It never calls a model or
// the network, and the window is capped at ninety days.
func BuildContinuityReport(ctx context.Context, st *store.Store, projectID string, since, until time.Time) (ContinuityReport, error) {
	if until.IsZero() {
		until = time.Now().UTC()
	}
	if since.IsZero() {
		since = until.Add(-7 * 24 * time.Hour)
	}
	if until.Sub(since) > ReportMaxWindow {
		since = until.Add(-ReportMaxWindow)
	}
	rep := ContinuityReport{
		Schema: ContinuityReportSchema,
		Since:  since.UTC(),
		Until:  until.UTC(),
	}
	if st == nil {
		rep.Verdict = "no sessions in this window"
		return rep, nil
	}
	sessions, err := st.ReportSessions(projectID, since, until)
	if err != nil {
		return rep, err
	}
	events, err := st.ReportPrimeRecallEvents(projectID, since, until)
	if err != nil {
		return rep, err
	}
	messageCounts, err := st.ReportMessageCounts(projectID, since, until)
	if err != nil {
		return rep, err
	}
	delta, err := st.ReportKnowledgeDelta(projectID, since, until)
	if err != nil {
		return rep, err
	}
	health, err := st.ReportHealthCounts(projectID)
	if err != nil {
		return rep, err
	}
	handoffs, err := st.ReportOpenHandoffs(projectID, 3)
	if err != nil {
		return rep, err
	}
	active, err := st.ReportActiveSessionIDs(projectID, since, until)
	if err != nil {
		return rep, err
	}
	rep.Knowledge = KnowledgeDelta{Memos: delta.Memos, Lessons: delta.Lessons, GoalsClosed: delta.GoalsClosed, Handoffs: delta.Handoffs}
	rep.Health = HealthCounts{StaleFacts: health.StaleFacts, DisputedDirectives: health.DisputedDirectives, ExpiredMemos: health.ExpiredMemos, BrokenChains: health.BrokenChains}

	sessionsByHarness := map[string]int{}
	for _, session := range sessions {
		sessionsByHarness[session.Harness]++
	}
	messagesByHarness := map[string]int64{}
	for _, count := range messageCounts {
		messagesByHarness[count.Tool] += count.Messages
	}
	seen := map[string]bool{}
	for harness := range sessionsByHarness {
		seen[harness] = true
	}
	for harness := range messagesByHarness {
		seen[harness] = true
	}
	for harness := range seen {
		rep.Harnesses = append(rep.Harnesses, HarnessActivity{Harness: harness, Sessions: sessionsByHarness[harness], Messages: messagesByHarness[harness]})
	}
	sort.Slice(rep.Harnesses, func(i, j int) bool { return rep.Harnesses[i].Harness < rep.Harnesses[j].Harness })

	primedSessions := map[string]bool{}
	for _, event := range events {
		if event.ToolName == "prime" || event.ToolName == "prime_context" {
			primedSessions[event.SessionID] = true
		}
	}
	rep.Coverage.Eligible = len(active)
	for id := range primedSessions {
		if active[id] {
			rep.Coverage.Primed++
		}
	}
	if rep.Coverage.Eligible > 0 {
		rep.Coverage.Percent = int(float64(rep.Coverage.Primed)*100/float64(rep.Coverage.Eligible) + 0.5)
	}

	edges := map[string]bool{}
	for _, handoff := range handoffs {
		written, err := time.Parse(time.RFC3339, handoff.CreatedAt)
		if err != nil {
			continue
		}
		for _, event := range events {
			read, err := time.Parse(time.RFC3339, event.CreatedAt)
			if err != nil || !read.After(written) {
				continue
			}
			if normalizeAgent(event.Agent) == normalizeAgent(handoff.Agent) {
				continue
			}
			key := handoff.Agent + "\x00" + event.Agent + "\x00" + handoff.ID
			if edges[key] {
				continue
			}
			edges[key] = true
			rep.CrossHarness = append(rep.CrossHarness, CrossHarnessEdge{Writer: handoff.Agent, Reader: event.Agent, Record: handoff.ID})
		}
	}
	sort.Slice(rep.CrossHarness, func(i, j int) bool {
		if rep.CrossHarness[i].Writer != rep.CrossHarness[j].Writer {
			return rep.CrossHarness[i].Writer < rep.CrossHarness[j].Writer
		}
		return rep.CrossHarness[i].Record < rep.CrossHarness[j].Record
	})

	for _, handoff := range handoffs {
		created, _ := time.Parse(time.RFC3339, handoff.CreatedAt)
		rep.OpenHandoffs = append(rep.OpenHandoffs, HandoffSummary{
			Agent:     handoff.Agent,
			Summary:   truncateRunes(handoff.Summary, 120),
			OpenItems: handoff.OpenItems,
			CreatedAt: created,
		})
	}

	rep.Verdict = continuityVerdict(rep)
	return rep, nil
}

func normalizeAgent(agent string) string {
	return strings.ToLower(strings.TrimSpace(agent))
}

func truncateRunes(text string, max int) string {
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	return string(runes[:max]) + "..."
}

func continuityVerdict(rep ContinuityReport) string {
	if rep.Coverage.Eligible == 0 {
		return "no sessions in this window"
	}
	verdict := fmt.Sprintf("Continuity worked in %d of %d sessions", rep.Coverage.Primed, rep.Coverage.Eligible)
	if len(rep.CrossHarness) > 0 {
		verdict += fmt.Sprintf("; %d handoffs crossed tools", len(rep.CrossHarness))
	}
	return verdict + "."
}

// RenderContinuityMarkdown renders the six sections in the documented order.
func RenderContinuityMarkdown(r ContinuityReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Continuity report\n\n")
	fmt.Fprintf(&b, "Window: %s to %s\n\n", r.Since.Format(time.RFC3339), r.Until.Format(time.RFC3339))
	fmt.Fprintf(&b, "## Harnesses\n")
	fmt.Fprintf(&b, "_Sessions and messages ingested per tool in the window._\n\n")
	if len(r.Harnesses) == 0 {
		b.WriteString("none\n\n")
	} else {
		for _, h := range r.Harnesses {
			fmt.Fprintf(&b, "- %s: %d sessions, %d messages\n", h.Harness, h.Sessions, h.Messages)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "## Continuity\n")
	fmt.Fprintf(&b, "_How many sessions were primed, and how many handoffs crossed tools._\n\n")
	if r.Coverage.Eligible == 0 {
		b.WriteString("none\n\n")
	} else {
		fmt.Fprintf(&b, "- Coverage: %d%% (%d of %d primed sessions)\n", r.Coverage.Percent, r.Coverage.Primed, r.Coverage.Eligible)
		if len(r.CrossHarness) == 0 {
			b.WriteString("- Cross-harness handoffs: none\n")
		} else {
			for _, edge := range r.CrossHarness {
				fmt.Fprintf(&b, "- %s to %s: %s\n", edge.Writer, edge.Reader, edge.Record)
			}
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "## Knowledge\n")
	fmt.Fprintf(&b, "_New decisions, lessons, closed goals, and handoffs written._\n\n")
	fmt.Fprintf(&b, "- Memos: %d\n- Lessons: %d\n- Goals closed: %d\n- Handoffs written: %d\n\n", r.Knowledge.Memos, r.Knowledge.Lessons, r.Knowledge.GoalsClosed, r.Knowledge.Handoffs)
	fmt.Fprintf(&b, "## Health\n")
	fmt.Fprintf(&b, "_Stale facts, disputed directives, expired memos, and broken chains._\n\n")
	fmt.Fprintf(&b, "- Stale facts: %d\n- Disputed directives: %d\n- Expired memos: %d\n- Broken chains: %d\n\n", r.Health.StaleFacts, r.Health.DisputedDirectives, r.Health.ExpiredMemos, r.Health.BrokenChains)
	fmt.Fprintf(&b, "## Open handoffs\n")
	fmt.Fprintf(&b, "_At most three open handoffs, newest first._\n\n")
	if len(r.OpenHandoffs) == 0 {
		b.WriteString("none\n\n")
	} else {
		for _, h := range r.OpenHandoffs {
			fmt.Fprintf(&b, "- %s (%d open): %s\n", h.Agent, h.OpenItems, h.Summary)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "## Verdict\n")
	fmt.Fprintf(&b, "_One sentence generated from the numbers above._\n\n")
	fmt.Fprintf(&b, "%s\n", r.Verdict)
	return b.String()
}
