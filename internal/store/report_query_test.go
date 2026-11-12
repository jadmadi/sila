package store

import (
	"path/filepath"
	"testing"
	"time"
)

func reportQueryStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.DB.Exec(query, args...); err != nil {
			t.Fatalf("%v\n%s", err, query)
		}
	}
	exec(`INSERT INTO agent_sessions (session_id, project_id, harness, agent, source, started_at, last_seen_at) VALUES
		('s1','p','claude','a','hook','2026-11-05T09:00:00Z','2026-11-05T10:00:00Z'),
		('s2','p','codex','b','hook','2026-11-05T10:00:00Z','2026-11-05T11:00:00Z'),
		('s3','p','claude','a','hook','2026-11-05T11:00:00Z','2026-11-05T12:00:00Z'),
		('s4','p','grok','c','hook','2026-11-05T12:00:00Z','2026-11-05T13:00:00Z'),
		('s5','p','claude','a','hook','2026-11-05T13:00:00Z','2026-11-05T14:00:00Z'),
		('s6','p','codex','b','hook','2026-11-05T14:00:00Z','2026-11-05T15:00:00Z'),
		('old','p','claude','a','hook','2026-01-01T00:00:00Z','2026-01-01T01:00:00Z')`)
	exec(`INSERT INTO tool_events (id, project_id, session_id, agent, source, tool_name, category, outcome, created_at) VALUES
		('e1','p','s1','a','hook','prime','continuity','ok','2026-11-05T09:05:00Z'),
		('e2','p','s2','b','hook','prime_context','continuity','ok','2026-11-05T10:05:00Z'),
		('e3','p','s5','a','hook','recall','continuity','ok','2026-11-05T13:05:00Z')`)
	exec(`INSERT INTO messages (project_id, content_hash, ts, role, content, source_tools, first_seen) VALUES
		('p','h1','2026-11-05T09:10:00Z','user','x','claude','2026-11-05T09:10:00Z'),
		('p','h2','2026-11-05T09:11:00Z','assistant','y','claude,codex','2026-11-05T09:11:00Z')`)
	exec(`INSERT INTO memory_records (id, project_id, fact_id, kind, title, summary, digest, envelope_json, created_at, updated_at) VALUES
		('m1','p','f1','memo','t','s','d1','{}','2026-11-05T09:20:00Z','2026-11-05T09:20:00Z'),
		('l1','p','f2','lesson','t','s','d2','{}','2026-11-05T09:30:00Z','2026-11-05T09:30:00Z')`)
	exec(`INSERT INTO handoffs (id, project_id, agent, summary, open_items, created_at, status) VALUES
		('h1','p','a','handoff','["x","y"]','2026-11-05T10:00:00Z','open')`)
	exec(`INSERT INTO goal_audit_receipts (project_id, goal_slug, commit_sha, spec_hash, sandbox_nonce, certified_at)
		VALUES ('p','g','abc','def','nonce','2026-11-05T11:00:00Z')`)
	return st
}

func TestReportSessionsCount(t *testing.T) {
	st := reportQueryStore(t)
	since := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	sessions, err := st.ReportSessions("p", since, until)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 6 {
		t.Fatalf("sessions = %d", len(sessions))
	}
	active, err := st.ReportActiveSessionIDs("p", since, until)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 3 {
		t.Fatalf("active = %d", len(active))
	}
}

func TestReportPrimeEventsGroupByHarness(t *testing.T) {
	st := reportQueryStore(t)
	events, err := st.ReportPrimeRecallEvents("p", time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	harnesses := map[string]int{}
	for _, event := range events {
		harnesses[event.Harness]++
	}
	if harnesses["claude"] != 2 || harnesses["codex"] != 1 {
		t.Fatalf("harness groups = %v", harnesses)
	}
}

func TestReportKnowledgeDeltaCounts(t *testing.T) {
	st := reportQueryStore(t)
	delta, err := st.ReportKnowledgeDelta("p", time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if delta.Memos != 1 || delta.Lessons != 1 || delta.Handoffs != 1 || delta.GoalsClosed != 1 {
		t.Fatalf("delta = %+v", delta)
	}
	handoffs, err := st.ReportOpenHandoffs("p", 3)
	if err != nil || len(handoffs) != 1 || handoffs[0].OpenItems != 2 {
		t.Fatalf("handoffs = %+v err=%v", handoffs, err)
	}
	counts, err := st.ReportMessageCounts("p", time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC))
	if err != nil || len(counts) != 2 {
		t.Fatalf("message counts = %+v err=%v", counts, err)
	}
}
