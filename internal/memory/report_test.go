package memory

import (
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

func TestVerdictThresholds(t *testing.T) {
	cases := []struct {
		name string
		r    Report
		want string
	}{
		{"small sample", Report{Sessions: SessionStats{Total: 4, PrimeRate: 0, HandoffRate: 0}}, "pass"},
		{"warn prime", Report{Sessions: SessionStats{Total: 5, PrimeRate: 0.5, HandoffRate: 1}}, "warn"},
		{"warn handoff", Report{Sessions: SessionStats{Total: 5, PrimeRate: 1, HandoffRate: 0.5}}, "warn"},
		{"warn writes", Report{Sessions: SessionStats{Total: 5, PrimeRate: 1, HandoffRate: 1}, Goals: GoalStats{WriteComplianceRate: 0.5}}, "warn"},
		{"fail zero", Report{Sessions: SessionStats{Total: 20, PrimeRate: 0, HandoffRate: 1}}, "fail"},
	}
	for _, tc := range cases {
		if got := Verdict(tc.r); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestBuildReportRatesZeroDenominatorAndGate(t *testing.T) {
	now := time.Now().UTC()
	report := BuildReport(nil, GoalStats{}, "", now.Add(-time.Hour), now)
	if report.Schema != "sila/memory-adoption/v1" || report.Sessions.PrimeRate != 0 || report.Goals.WriteComplianceRate != 0 {
		t.Fatalf("zero report: %+v", report)
	}
	if report.Verdict != "" && report.Verdict != "pass" {
		t.Fatalf("zero verdict: %q", report.Verdict)
	}
	events := []store.MemoryEvent{
		{SessionID: "s1", Verb: "prime", Outcome: "ok"},
		{SessionID: "s1", Verb: "recall", Outcome: "ok"},
		{SessionID: "s2", Verb: "prime", Outcome: "ok"},
		{SessionID: "s2", Verb: "gate", Outcome: "error"},
		{SessionID: "", Verb: "memo", Outcome: "ok"},
	}
	report = BuildReport(events, GoalStats{Implemented: 2, Required: 1, Legacy: 1, WithValidMemoryBlock: 1}, "p", now.Add(-time.Hour), now)
	if report.Sessions.Total != 2 || report.Sessions.Prime != 2 || report.Sessions.Recall != 1 {
		t.Fatalf("sessions: %+v", report.Sessions)
	}
	if report.Sessions.PrimeRate != 1 || report.Sessions.RecallRate != 0.5 {
		t.Fatalf("rates: %+v", report.Sessions)
	}
	if report.Gate.Fail != 1 || report.UnattributedEvents != 1 {
		t.Fatalf("gate/unattributed: %+v %d", report.Gate, report.UnattributedEvents)
	}
	if report.Goals.WriteComplianceRate != 0.5 {
		t.Fatalf("goal rate: %+v", report.Goals)
	}
}
