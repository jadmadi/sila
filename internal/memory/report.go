// Package memory aggregates local, privacy-preserving adoption events.
package memory

import (
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/store"
)

var verbs = []string{"prime", "recall", "memo", "lesson", "case", "facts", "gate", "handoff", "sync"}

// EventCounts is verb -> outcome -> count.
type EventCounts map[string]map[string]int

// SessionStats reports unique-session adoption rates.
type SessionStats struct {
	Total        int     `json:"total"`
	Prime        int     `json:"prime"`
	Recall       int     `json:"recall"`
	Handoff      int     `json:"handoff"`
	PrimeRate    float64 `json:"prime_rate"`
	RecallRate   float64 `json:"recall_rate"`
	HandoffRate  float64 `json:"handoff_rate"`
	PrimeDenom   int     `json:"prime_denominator"`
	RecallDenom  int     `json:"recall_denominator"`
	HandoffDenom int     `json:"handoff_denominator"`
}

// GoalStats reports goal-contract memory compliance.
type GoalStats struct {
	Implemented           int     `json:"implemented"`
	Required              int     `json:"required"`
	Legacy                int     `json:"legacy"`
	WithValidMemoryBlock  int     `json:"with_valid_memory_block"`
	WriteComplianceRate   float64 `json:"write_compliance_rate"`
	ComplianceDenominator int     `json:"compliance_denominator"`
}

// GateStats reports memory-write sentry outcomes.
type GateStats struct {
	Pass   int `json:"pass"`
	Fail   int `json:"fail"`
	Waiver int `json:"waiver"`
	Skip   int `json:"skip"`
}

// Report is the sila/memory-adoption/v1 contract.
type Report struct {
	Schema             string       `json:"schema"`
	Window             Window       `json:"window"`
	Project            string       `json:"project"`
	Events             EventCounts  `json:"events"`
	Sessions           SessionStats `json:"sessions"`
	Goals              GoalStats    `json:"goals"`
	Gate               GateStats    `json:"gate"`
	UnattributedEvents int          `json:"unattributed_events"`
	Verdict            string       `json:"verdict,omitempty"`
}

// Window is the report time range.
type Window struct {
	Since string `json:"since"`
	Until string `json:"until"`
}

// Thresholds are shared by the report, doctor, and stats line.
const (
	WarnPrimeRate       = 0.80
	WarnHandoffRate     = 0.70
	WarnWriteCompliance = 0.90
	WarnMinSessions     = 5
	FailMinSessions     = 20
)

// BuildReport aggregates already-filtered events and goal metadata.
func BuildReport(events []store.MemoryEvent, goalStats GoalStats, project string, since, until time.Time) Report {
	if until.IsZero() {
		until = time.Now().UTC()
	}
	res := Report{
		Schema:  "sila/memory-adoption/v1",
		Window:  Window{Since: since.UTC().Format(time.RFC3339), Until: until.UTC().Format(time.RFC3339)},
		Project: project,
		Events:  EventCounts{},
	}
	if res.Project == "" {
		res.Project = "*"
	}
	for _, verb := range verbs {
		res.Events[verb] = map[string]int{"ok": 0, "empty": 0, "error": 0, "skipped": 0}
	}
	sessions := map[string]bool{}
	primeSessions := map[string]bool{}
	recallSessions := map[string]bool{}
	handoffSessions := map[string]bool{}
	for _, e := range events {
		if _, ok := res.Events[e.Verb]; !ok {
			res.Events[e.Verb] = map[string]int{"ok": 0, "empty": 0, "error": 0, "skipped": 0}
		}
		outcome := e.Outcome
		if outcome == "" {
			outcome = "ok"
		}
		res.Events[e.Verb][outcome]++
		if e.SessionID == "" {
			res.UnattributedEvents++
		} else {
			sessions[e.SessionID] = true
			switch e.Verb {
			case "prime":
				primeSessions[e.SessionID] = true
			case "recall":
				recallSessions[e.SessionID] = true
			case "handoff":
				handoffSessions[e.SessionID] = true
			}
		}
		if e.Verb == "gate" {
			switch outcome {
			case "ok", "pass":
				res.Gate.Pass++
			case "error", "fail":
				res.Gate.Fail++
			case "waiver":
				res.Gate.Waiver++
			default:
				res.Gate.Skip++
			}
		}
	}
	res.Sessions = SessionStats{
		Total: len(sessions), Prime: len(primeSessions), Recall: len(recallSessions), Handoff: len(handoffSessions),
		PrimeDenom: len(sessions), RecallDenom: len(sessions), HandoffDenom: len(sessions),
	}
	res.Sessions.PrimeRate = rate(len(primeSessions), len(sessions))
	res.Sessions.RecallRate = rate(len(recallSessions), len(sessions))
	res.Sessions.HandoffRate = rate(len(handoffSessions), len(sessions))
	res.Goals = goalStats
	res.Goals.ComplianceDenominator = goalStats.Implemented
	res.Goals.WriteComplianceRate = rate(goalStats.WithValidMemoryBlock, goalStats.Implemented)
	return res
}

func rate(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return math.Round(float64(n)/float64(d)*10000) / 10000
}

// Verdict applies the advisory thresholds used by doctor --memory.
func Verdict(r Report) string {
	if r.Sessions.Total >= FailMinSessions &&
		(r.Sessions.PrimeRate == 0 || r.Sessions.HandoffRate == 0 || r.Goals.WriteComplianceRate == 0) {
		return "fail"
	}
	if r.Sessions.Total >= WarnMinSessions &&
		(r.Sessions.PrimeRate < WarnPrimeRate || r.Sessions.HandoffRate < WarnHandoffRate || r.Goals.WriteComplianceRate < WarnWriteCompliance) {
		return "warn"
	}
	return "pass"
}

// CollectGoalStats scans implemented goal packages under rootDir.
func CollectGoalStats(rootDir string, since time.Time) GoalStats {
	audit, err := goals.ScanGoals(rootDir)
	if err != nil || audit == nil {
		return GoalStats{}
	}
	status := map[string]string{}
	for _, g := range audit.Goals {
		status[g.Slug] = g.Status
	}
	entries, err := os.ReadDir(filepath.Join(rootDir, "goals"))
	if err != nil {
		return GoalStats{}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	var out GoalStats
	for _, entry := range entries {
		if !entry.IsDir() || status[entry.Name()] != "implemented" {
			continue
		}
		dir := filepath.Join(rootDir, "goals", entry.Name())
		if !since.IsZero() {
			if fi, err := entry.Info(); err == nil && fi.ModTime().Before(since) {
				continue
			}
		}
		out.Implemented++
		data, err := os.ReadFile(filepath.Join(dir, "goal.md"))
		if err != nil {
			continue
		}
		if goals.ParseMemoryProtocol(string(data)) == "required" {
			out.Required++
		} else {
			out.Legacy++
		}
		evidence, err := os.ReadFile(filepath.Join(dir, "evidence.md"))
		if err != nil {
			continue
		}
		if mw, diags := goals.ParseMemoryWrites(string(evidence)); len(diags) == 0 && mw != nil &&
			mw.Schema == "sila/memory-writes/v1" && mw.GoalSlug == entry.Name() {
			out.WithValidMemoryBlock++
		}
	}
	return out
}
