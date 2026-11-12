package dream

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

func reportFixture(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.DB.Exec(query, args...); err != nil {
			t.Fatalf("exec: %v\n%s", err, query)
		}
	}
	exec(`INSERT INTO agent_sessions (session_id, project_id, harness, agent, source, started_at, last_seen_at) VALUES
		('s1','p','claude','agent-a','hook','2026-11-06T09:00:00Z','2026-11-06T10:30:00Z'),
		('s2','p','codex','agent-b','hook','2026-11-06T10:45:00Z','2026-11-06T11:30:00Z'),
		('s3','p','claude','agent-a','hook','2026-11-06T11:00:00Z','2026-11-06T11:10:00Z')`)
	exec(`INSERT INTO tool_events (id, project_id, session_id, agent, source, tool_name, category, outcome, created_at) VALUES
		('e1','p','s1','agent-a','hook','prime','continuity','ok','2026-11-06T09:05:00Z'),
		('e2','p','s1','agent-a','hook','recall','continuity','ok','2026-11-06T09:30:00Z'),
		('e3','p','s2','agent-b','hook','prime_context','continuity','ok','2026-11-06T11:00:00Z'),
		('e4','p','s2','agent-b','hook','recall','continuity','ok','2026-11-06T11:05:00Z')`)
	exec(`INSERT INTO handoffs (id, project_id, agent, summary, created_at, status) VALUES
		('h1','p','agent-a','first handoff', '2026-11-06T10:00:00Z','open')`)
	exec(`INSERT INTO messages (project_id, content_hash, ts, role, content, source_tools, first_seen) VALUES
		('p','x1','2026-11-06T09:10:00Z','user','hello','claude','2026-11-06T09:10:00Z'),
		('p','x2','2026-11-06T11:10:00Z','assistant','hi','codex','2026-11-06T11:10:00Z')`)
	exec(`INSERT INTO memory_records (id, project_id, fact_id, kind, title, summary, digest, envelope_json, created_at, updated_at) VALUES
		('m1','p','f1','memo','d','s','d1','{}','2026-11-06T09:20:00Z','2026-11-06T09:20:00Z'),
		('l1','p','f2','lesson','l','s','d2','{}','2026-11-06T09:40:00Z','2026-11-06T09:40:00Z')`)
	exec(`INSERT INTO facts (id, project_id, claim, confidence, verified, repo_files, tools, session_ref, first_seen, promoted_at) VALUES
		('f1','p','stale claim','low',0,'["a.go"]','claude','s1','2026-11-06T09:00:00Z','2026-11-06T09:00:00Z')`)
	return st
}

func TestBuildContinuityReportFixture(t *testing.T) {
	st := reportFixture(t)
	since := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	until := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	rep, err := BuildContinuityReport(context.Background(), st, "p", since, until)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Harnesses) != 2 {
		t.Fatalf("harnesses = %+v", rep.Harnesses)
	}
	// s3 has no events, so it is excluded from the denominator.
	if rep.Coverage.Eligible != 2 || rep.Coverage.Primed != 2 || rep.Coverage.Percent != 100 {
		t.Fatalf("coverage = %+v", rep.Coverage)
	}
	// Only agent-b reads after agent-a's handoff; the earlier same-agent recall
	// and the pre-write read do not count.
	if len(rep.CrossHarness) != 1 || rep.CrossHarness[0].Writer != "agent-a" || rep.CrossHarness[0].Reader != "agent-b" {
		t.Fatalf("cross-harness = %+v", rep.CrossHarness)
	}
	if rep.Knowledge.Memos != 1 || rep.Knowledge.Lessons != 1 || rep.Knowledge.Handoffs != 1 {
		t.Fatalf("knowledge = %+v", rep.Knowledge)
	}
	if rep.Health.StaleFacts != 1 {
		t.Fatalf("health = %+v", rep.Health)
	}
	if !strings.Contains(rep.Verdict, "2 of 2 sessions") || !strings.Contains(rep.Verdict, "1 handoffs crossed tools") {
		t.Fatalf("verdict = %q", rep.Verdict)
	}
}

func TestContinuityReportZeroWindow(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rep, err := BuildContinuityReport(context.Background(), st, "p", time.Now().Add(-48*time.Hour), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Verdict != "no sessions in this window" {
		t.Fatalf("verdict = %q", rep.Verdict)
	}
	md := RenderContinuityMarkdown(rep)
	for _, heading := range []string{"## Harnesses", "## Continuity", "## Knowledge", "## Health", "## Open handoffs", "## Verdict"} {
		if !strings.Contains(md, heading) {
			t.Fatalf("missing %q", heading)
		}
	}
	if strings.Count(md, "none") < 3 {
		t.Fatalf("empty sections missing none:\n%s", md)
	}
}

func TestContinuityReportCapsWindow(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	until := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	rep, err := BuildContinuityReport(context.Background(), st, "p", until.Add(-200*24*time.Hour), until)
	if err != nil {
		t.Fatal(err)
	}
	if want := until.Add(-ReportMaxWindow); !rep.Since.Equal(want) {
		t.Fatalf("since = %v, want %v", rep.Since, want)
	}
}

func TestContinuityMarkdownOrder(t *testing.T) {
	st := reportFixture(t)
	rep, err := BuildContinuityReport(context.Background(), st, "p", time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	md := RenderContinuityMarkdown(rep)
	headings := []string{"## Harnesses", "## Continuity", "## Knowledge", "## Health", "## Open handoffs", "## Verdict"}
	last := -1
	for _, heading := range headings {
		idx := strings.Index(md, heading)
		if idx < 0 || idx <= last {
			t.Fatalf("heading %q out of order", heading)
		}
		last = idx
	}
}

func TestContinuityReportJSON(t *testing.T) {
	st := reportFixture(t)
	rep, err := BuildContinuityReport(context.Background(), st, "p", time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"schema":"sila/continuity-report/v1"`) {
		t.Fatalf("json = %s", data)
	}
	var back ContinuityReport
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.Coverage != rep.Coverage || back.Knowledge != rep.Knowledge || back.Verdict != rep.Verdict {
		t.Fatalf("round trip = %+v", back)
	}
}
