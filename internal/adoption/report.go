package adoption

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

// DefaultExposedTools is the current continuity profile surface.
var DefaultExposedTools = []string{
	"recall", "prime_context", "save_memo", "save_lesson", "save_handoff", "get_status",
}

// ReportOptions scopes one funnel report.
type ReportOptions struct {
	Since        time.Time
	Until        time.Time
	ProjectID    string
	Harness      string
	ExposedTools []string
	Now          time.Time
}

// FunnelReport is the sila/adoption-funnel/v1 payload.
type FunnelReport struct {
	Schema             string          `json:"schema"`
	Window             Window          `json:"window"`
	Project            string          `json:"project,omitempty"`
	Harness            string          `json:"harness,omitempty"`
	Sessions           Sessions        `json:"sessions"`
	Rates              map[string]Rate `json:"rates"`
	Tools              []ToolReport    `json:"tools"`
	Coverage           Coverage        `json:"coverage"`
	UnattributedEvents int             `json:"unattributed_events"`
	Verdict            string          `json:"verdict,omitempty"`
}

// Window is the report time range.
type Window struct {
	Since string `json:"since"`
	Until string `json:"until"`
}

// Sessions is the funnel counts by session.
type Sessions struct {
	Total      int `json:"total"`
	Prime      int `json:"prime"`
	Recall     int `json:"recall"`
	Writes     int `json:"writes"`
	Handoff    int `json:"handoff"`
	Gate       int `json:"gate"`
	OmniSearch int `json:"omni_search"`
	SearchDocs int `json:"search_docs"`
	SkillLoad  int `json:"skill_load"`
	EditCheck  int `json:"edit_check"`
}

// Rate carries an explicit denominator.
type Rate struct {
	Value       float64 `json:"value"`
	Denominator int     `json:"denominator"`
}

// ToolReport is one tool's adoption slice.
type ToolReport struct {
	Name      string         `json:"name"`
	Calls     int            `json:"calls"`
	Sessions  int            `json:"sessions"`
	LastUsed  string         `json:"last_used,omitempty"`
	Harnesses map[string]int `json:"harnesses"`
	Category  string         `json:"category,omitempty"`
}

// Coverage reports exposed vs used tools.
type Coverage struct {
	Exposed int      `json:"exposed"`
	Used    int      `json:"used"`
	Missing []string `json:"missing"`
}

// BuildFunnelReport aggregates tool events into the adoption funnel.
func BuildFunnelReport(ctx context.Context, st *store.Store, opts ReportOptions) (*FunnelReport, error) {
	if st == nil {
		return nil, fmt.Errorf("adoption report: store is nil")
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	until := opts.Until
	if until.IsZero() {
		until = now
	}
	since := opts.Since
	if since.IsZero() {
		since = until.Add(-7 * 24 * time.Hour)
	}
	events, err := st.ListToolEvents(store.ToolEventFilter{
		ProjectID: opts.ProjectID, Since: since, Until: until, Limit: 0,
	})
	if err != nil {
		return nil, err
	}
	report := &FunnelReport{
		Schema:  "sila/adoption-funnel/v1",
		Window:  Window{Since: since.UTC().Format(time.RFC3339), Until: until.UTC().Format(time.RFC3339)},
		Project: opts.ProjectID, Harness: opts.Harness,
		Rates: map[string]Rate{},
	}
	sessionSet := map[string]map[string]bool{}
	unattributed := 0
	type toolAgg struct {
		calls     int
		sessions  map[string]bool
		last      time.Time
		harnesses map[string]int
		category  string
	}
	tools := map[string]*toolAgg{}
	for _, ev := range events {
		if opts.Harness != "" && ev.Agent != opts.Harness {
			continue
		}
		if ev.SessionID == "" {
			unattributed++
		} else {
			if sessionSet[ev.SessionID] == nil {
				sessionSet[ev.SessionID] = map[string]bool{}
			}
			sessionSet[ev.SessionID][ev.ToolName] = true
		}
		agg := tools[ev.ToolName]
		if agg == nil {
			agg = &toolAgg{sessions: map[string]bool{}, harnesses: map[string]int{}}
			tools[ev.ToolName] = agg
		}
		agg.calls++
		if ev.SessionID != "" {
			agg.sessions[ev.SessionID] = true
		}
		if ev.CreatedAt.After(agg.last) {
			agg.last = ev.CreatedAt
		}
		agg.harnesses[ev.Agent]++
		if ev.Category != "" {
			agg.category = ev.Category
		}
	}
	report.UnattributedEvents = unattributed
	report.Sessions.Total = len(sessionSet)
	for _, verbs := range sessionSet {
		if verbs["prime"] || verbs["prime_context"] {
			report.Sessions.Prime++
		}
		if verbs["recall"] {
			report.Sessions.Recall++
		}
		if verbs["memo"] || verbs["save_memo"] || verbs["lesson"] || verbs["save_lesson"] || verbs["case"] || verbs["save_case"] {
			report.Sessions.Writes++
		}
		if verbs["handoff"] || verbs["save_handoff"] {
			report.Sessions.Handoff++
		}
		if verbs["gate"] {
			report.Sessions.Gate++
		}
		if verbs["omni_search"] {
			report.Sessions.OmniSearch++
		}
		if verbs["search_docs"] {
			report.Sessions.SearchDocs++
		}
		if verbs["skill_load"] {
			report.Sessions.SkillLoad++
		}
		if verbs["edit-check"] || verbs["edit_check"] {
			report.Sessions.EditCheck++
		}
	}
	report.Rates["prime"] = rate(report.Sessions.Prime, report.Sessions.Total)
	report.Rates["recall"] = rate(report.Sessions.Recall, report.Sessions.Total)
	report.Rates["write"] = rate(report.Sessions.Writes, report.Sessions.Total)
	report.Rates["handoff"] = rate(report.Sessions.Handoff, report.Sessions.Total)
	report.Rates["gate"] = rate(report.Sessions.Gate, report.Sessions.Total)

	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		agg := tools[name]
		tool := ToolReport{
			Name: name, Calls: agg.calls, Sessions: len(agg.sessions),
			Harnesses: agg.harnesses, Category: agg.category,
		}
		if !agg.last.IsZero() {
			tool.LastUsed = agg.last.UTC().Format(time.RFC3339)
		}
		report.Tools = append(report.Tools, tool)
	}
	exposed := opts.ExposedTools
	if len(exposed) == 0 {
		exposed = DefaultExposedTools
	}
	report.Coverage.Exposed = len(exposed)
	for _, name := range exposed {
		if tools[name] != nil {
			report.Coverage.Used++
		} else {
			report.Coverage.Missing = append(report.Coverage.Missing, name)
		}
	}
	sort.Strings(report.Coverage.Missing)
	return report, nil
}

func rate(numerator, denominator int) Rate {
	if denominator == 0 {
		return Rate{Value: 0, Denominator: 0}
	}
	return Rate{Value: float64(numerator) / float64(denominator), Denominator: denominator}
}

// Verdict applies the documented advisory thresholds for a 7-day window.
func Verdict(report *FunnelReport) string {
	if report == nil || report.Sessions.Total == 0 {
		return "unknown"
	}
	total := report.Sessions.Total
	if total >= 20 {
		for _, key := range []string{"prime", "write", "handoff"} {
			if report.Rates[key].Value == 0 {
				return "fail"
			}
		}
	}
	if total >= 5 {
		if report.Rates["prime"].Value < 0.8 || report.Rates["write"].Value < 0.9 || report.Rates["handoff"].Value < 0.7 {
			return "warn"
		}
	}
	return "pass"
}

// FormatRate is a stable percentage for human output.
func FormatRate(r Rate) string {
	if r.Denominator == 0 {
		return "0.0% (0)"
	}
	return fmt.Sprintf("%.1f%% (%d)", r.Value*100, r.Denominator)
}

// ToolNames returns the sorted tool names used in a report.
func ToolNames(report *FunnelReport) string {
	if report == nil {
		return ""
	}
	names := make([]string, 0, len(report.Tools))
	for _, tool := range report.Tools {
		names = append(names, tool.Name)
	}
	return strings.Join(names, ", ")
}

// ComputeEvalMetrics calculates all R4 metrics with explicit denominators and 4-decimal rounding.
func ComputeEvalMetrics(cases []AdoptionCase, results []CaseResult, sessions []LiveSessionTrace) EvalMetrics {
	totalCases := len(cases)
	eligibleCases := 0
	recalledCases := 0
	forbiddenCases := 0
	unknownCases := 0
	var turns []int

	for _, r := range results {
		if r.Verdict == "unknown" {
			unknownCases++
			continue
		}
		eligibleCases++
		if r.Observed == r.Expected && !r.ForbiddenCalled && r.Verdict == "pass" {
			recalledCases++
		}
		if r.ForbiddenCalled {
			forbiddenCases++
		}
		if r.TurnsToFirstSila > 0 {
			turns = append(turns, r.TurnsToFirstSila)
		}
	}

	totalSessions := len(sessions)
	primeSessions := 0
	handoffSessions := 0
	goalSessions := 0
	goalWriteSessions := 0

	for _, s := range sessions {
		hasPrime := false
		hasHandoff := false
		hasWrite := s.GroundedWrite
		for _, t := range s.ToolsCalled {
			switch t {
			case "prime", "prime_context", "salam":
				hasPrime = true
			case "handoff", "save_handoff":
				hasHandoff = true
			case "memo", "save_memo", "lesson", "save_lesson", "case", "save_case":
				hasWrite = true
			}
		}
		if hasPrime {
			primeSessions++
		}
		if hasHandoff {
			handoffSessions++
		}
		if s.GoalBound {
			goalSessions++
			if hasWrite {
				goalWriteSessions++
			}
		}
	}

	return EvalMetrics{
		TriggerRecallRate:          roundedRate(recalledCases, eligibleCases),
		PrimeRate:                  roundedRate(primeSessions, totalSessions),
		HandoffRate:                roundedRate(handoffSessions, totalSessions),
		WriteComplianceRate:        roundedRate(goalWriteSessions, goalSessions),
		MedianTurnsToFirstSilaCall: computeMedianInts(turns),
		ForbiddenToolRate:          roundedRate(forbiddenCases, totalCases),
		UnknownRate:                roundedRate(unknownCases, totalCases),
	}
}

// ComputeLiveVerdict applies the R3.4 and R4.2/R4.3 rules to determine the aggregate verdict.
func ComputeLiveVerdict(summary EvalSummary, m EvalMetrics) string {
	if summary.Total == 0 {
		return "unknown"
	}
	if m.TriggerRecallRate.Denominator == 0 ||
		m.PrimeRate.Denominator == 0 ||
		m.HandoffRate.Denominator == 0 ||
		m.WriteComplianceRate.Denominator == 0 ||
		m.ForbiddenToolRate.Denominator == 0 {
		return "unknown"
	}
	knownRatio := float64(summary.Total-summary.Unknown) / float64(summary.Total)
	if knownRatio+1e-9 < ThresholdKnownDataCoverage {
		return "unknown"
	}
	if m.TriggerRecallRate.Value+1e-9 < ThresholdTriggerRecallRate ||
		m.PrimeRate.Value+1e-9 < ThresholdPrimeRate ||
		m.HandoffRate.Value+1e-9 < ThresholdHandoffRate ||
		m.WriteComplianceRate.Value+1e-9 < ThresholdWriteComplianceRate ||
		m.MedianTurnsToFirstSilaCall > ThresholdMedianTurnsFirstSila ||
		m.MedianTurnsToFirstSilaCall <= 0 ||
		m.ForbiddenToolRate.Value-1e-9 > ThresholdForbiddenToolRate {
		return "fail"
	}
	return "pass"
}

func roundedRate(numerator, denominator int) Rate {
	if denominator <= 0 {
		return Rate{Value: 0.0, Denominator: 0}
	}
	val := float64(numerator) / float64(denominator)
	val = math.Round(val*10000) / 10000
	return Rate{Value: val, Denominator: denominator}
}

func computeMedianInts(vals []int) float64 {
	if len(vals) == 0 {
		return 0.0
	}
	sorted := make([]int, len(vals))
	copy(sorted, vals)
	sort.Ints(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return float64(sorted[mid])
	}
	return float64(sorted[mid-1]+sorted[mid]) / 2.0
}

func isWriteTool(name string) bool {
	switch name {
	case "memo", "save_memo", "lesson", "save_lesson", "case", "save_case":
		return true
	default:
		return false
	}
}

func coalesceSessionsForOffline(traces []LiveSessionTrace) []LiveSessionTrace {
	if len(traces) == 0 {
		return nil
	}
	var allTools []string
	var goals []LiveSessionTrace
	for _, tr := range traces {
		allTools = append(allTools, tr.ToolsCalled...)
		if tr.GoalBound {
			goals = append(goals, tr)
		}
	}
	return coalesceLiveSessions(allTools, goals)
}

func coalesceLiveSessions(allTools []string, goalTraces []LiveSessionTrace) []LiveSessionTrace {
	if len(allTools) == 0 {
		return nil
	}
	if len(goalTraces) == 0 {
		return []LiveSessionTrace{{
			SessionID:   "eval-session",
			ToolsCalled: allTools,
		}}
	}
	out := make([]LiveSessionTrace, 0, len(goalTraces))
	for _, gt := range goalTraces {
		combined := make([]string, 0, len(allTools)+len(gt.ToolsCalled))
		combined = append(combined, allTools...)
		combined = append(combined, gt.ToolsCalled...)
		out = append(out, LiveSessionTrace{
			SessionID:     gt.SessionID,
			GoalBound:     true,
			GroundedWrite: gt.GroundedWrite,
			ToolsCalled:   combined,
		})
	}
	return out
}

func writeLiveReportFiles(outDir string, report EvalReport) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", outDir, err)
	}
	for _, c := range report.Cases {
		raw, err := json.MarshalIndent(c, "", "  ")
		if err != nil {
			return err
		}
		raw = append(raw, '\n')
		casePath := filepath.Join(outDir, fmt.Sprintf("case-%s.json", c.ID))
		if err := os.WriteFile(casePath, raw, 0o644); err != nil {
			return err
		}
	}
	aggRaw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	aggRaw = append(aggRaw, '\n')
	return os.WriteFile(filepath.Join(outDir, "report.json"), aggRaw, 0o644)
}
