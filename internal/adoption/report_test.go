package adoption

import (
	"context"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

func seedToolEvents(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/adoption.db")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	events := []store.ToolEvent{
		{ProjectID: "p", SessionID: "s1", Agent: "command-code", Source: "mcp", ToolName: "prime", Category: "context", CreatedAt: now.Add(-4 * time.Minute)},
		{ProjectID: "p", SessionID: "s1", Agent: "command-code", Source: "mcp", ToolName: "recall", Category: "context", CreatedAt: now.Add(-3 * time.Minute)},
		{ProjectID: "p", SessionID: "s1", Agent: "command-code", Source: "mcp", ToolName: "memo", Category: "write", CreatedAt: now.Add(-2 * time.Minute)},
		{ProjectID: "p", SessionID: "s1", Agent: "command-code", Source: "mcp", ToolName: "handoff", Category: "write", CreatedAt: now.Add(-time.Minute)},
		{ProjectID: "p", SessionID: "s2", Agent: "claude", Source: "mcp", ToolName: "prime_context", Category: "context", CreatedAt: now.Add(-3 * time.Minute)},
		{ProjectID: "p", SessionID: "s2", Agent: "claude", Source: "mcp", ToolName: "recall", Category: "context", CreatedAt: now.Add(-2 * time.Minute)},
		{ProjectID: "p", SessionID: "s2", Agent: "claude", Source: "mcp", ToolName: "omni_search", Category: "read", CreatedAt: now.Add(-time.Minute)},
		{ProjectID: "p", SessionID: "s3", Agent: "command-code", Source: "mcp", ToolName: "prime", Category: "context", CreatedAt: now.Add(-time.Minute)},
		{ProjectID: "p", SessionID: "", Agent: "cli", Source: "cli", ToolName: "status", Category: "cli", CreatedAt: now},
	}
	for _, ev := range events {
		if err := st.RecordToolEvent(ev); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

func TestBuildFunnelReport(t *testing.T) {
	st := seedToolEvents(t)
	defer st.Close()
	report, err := BuildFunnelReport(context.Background(), st, ReportOptions{
		ProjectID: "p", Since: time.Now().UTC().Add(-time.Hour),
		ExposedTools: []string{"prime", "recall", "save_case"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Schema != "sila/adoption-funnel/v1" || report.Sessions.Total != 3 {
		t.Fatalf("report = %+v", report)
	}
	if report.Sessions.Prime != 3 || report.Sessions.Recall != 2 || report.Sessions.Writes != 1 || report.Sessions.Handoff != 1 || report.Sessions.Gate != 0 {
		t.Fatalf("funnel = %+v", report.Sessions)
	}
	if report.Rates["prime"].Value != 1 || report.Rates["prime"].Denominator != 3 {
		t.Fatalf("prime rate = %+v", report.Rates["prime"])
	}
	if report.Coverage.Used != 2 || report.Coverage.Exposed != 3 || len(report.Coverage.Missing) != 1 || report.Coverage.Missing[0] != "save_case" {
		t.Fatalf("coverage = %+v", report.Coverage)
	}
	if report.UnattributedEvents != 1 {
		t.Fatalf("unattributed = %d", report.UnattributedEvents)
	}
	if len(report.Tools) == 0 || report.Tools[0].Name != "handoff" {
		t.Fatalf("tools not deterministic: %+v", report.Tools)
	}
}

func TestFunnelZeroDenominator(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/empty.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	report, err := BuildFunnelReport(context.Background(), st, ReportOptions{ProjectID: "p", Since: time.Now().Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if report.Sessions.Total != 0 || report.Rates["prime"].Value != 0 || report.Rates["prime"].Denominator != 0 {
		t.Fatalf("zero report = %+v", report)
	}
	if Verdict(report) != "unknown" {
		t.Fatalf("verdict = %q, want unknown", Verdict(report))
	}
}

func TestVerdictThresholds(t *testing.T) {
	base := &FunnelReport{Rates: map[string]Rate{
		"prime": {Value: 0.5, Denominator: 4}, "write": {Value: 0.5, Denominator: 4}, "handoff": {Value: 0.5, Denominator: 4},
	}}
	cases := []struct {
		total int
		rates map[string]Rate
		want  string
	}{
		{4, base.Rates, "pass"},
		{5, base.Rates, "warn"},
		{19, base.Rates, "warn"},
		{20, map[string]Rate{"prime": {Value: 0}, "write": {Value: 0}, "handoff": {Value: 0}}, "fail"},
		{20, map[string]Rate{"prime": {Value: 1}, "write": {Value: 1}, "handoff": {Value: 1}}, "pass"},
	}
	for _, tc := range cases {
		report := &FunnelReport{Sessions: Sessions{Total: tc.total}, Rates: tc.rates}
		if got := Verdict(report); got != tc.want {
			t.Errorf("total=%d verdict=%q, want %q", tc.total, got, tc.want)
		}
	}
}
