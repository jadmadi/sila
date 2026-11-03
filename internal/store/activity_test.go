package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecentActivityInterleavedToolsAndOrdering(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_activity.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	if _, err := st.EnsureProject("/workspace/example-project"); err != nil {
		t.Fatalf("EnsureProject: %v", err)
	}
	if _, err := st.EnsureProject("/workspace/other-project"); err != nil {
		t.Fatalf("EnsureProject: %v", err)
	}

	now := time.Date(2026, 9, 16, 15, 0, 0, 0, time.UTC)
	// Base window: last 24h
	windowStart := now.Add(-24 * time.Hour)

	// Ingest session 1: Codex (session …a1) at 11:10
	t1Req := now.Add(-3*time.Hour - 50*time.Minute) // 11:10
	t1Rep := now.Add(-3*time.Hour - 45*time.Minute) // 11:15
	_, _, err = st.WriteRun("", "codex", "sess-a1", []LoadMessage{
		{ProjectID: "example-project", ContentHash: "h-codex-req", TS: t1Req.Format(time.RFC3339), Role: "user", Content: "Investigate why settings fail to save", Tool: "codex", SessionID: "sess-a1", SourceOrder: 1, SourceTime: SortableTime(t1Req)},
		{ProjectID: "example-project", ContentHash: "h-codex-rep", TS: t1Rep.Format(time.RFC3339), Role: "assistant", Content: "The API rejects an empty timezone value", Tool: "codex", SessionID: "sess-a1", SourceOrder: 2, SourceTime: SortableTime(t1Rep)},
	})
	if err != nil {
		t.Fatalf("WriteRun codex: %v", err)
	}

	// Ingest session 2: OpenCode (session …b2) at 13:40
	t2Req := now.Add(-1*time.Hour - 20*time.Minute) // 13:40
	t2Rep := now.Add(-1*time.Hour - 15*time.Minute) // 13:45
	_, _, err = st.WriteRun("", "opencode", "sess-b2", []LoadMessage{
		{ProjectID: "example-project", ContentHash: "h-opencode-req", TS: t2Req.Format(time.RFC3339), Role: "user", Content: "Fix the settings form validation", Tool: "opencode", SessionID: "sess-b2", SourceOrder: 1, SourceTime: SortableTime(t2Req)},
		{ProjectID: "example-project", ContentHash: "h-opencode-rep", TS: t2Rep.Format(time.RFC3339), Role: "assistant", Content: "Added validation and tests; ready for review", Tool: "opencode", SessionID: "sess-b2", SourceOrder: 2, SourceTime: SortableTime(t2Rep)},
	})
	if err != nil {
		t.Fatalf("WriteRun opencode: %v", err)
	}

	// Ingest session 3: Muse (session …c3) at 14:25
	t3Req := now.Add(-35 * time.Minute) // 14:25
	t3Rep := now.Add(-30 * time.Minute) // 14:30
	_, _, err = st.WriteRun("", "muse", "sess-c3", []LoadMessage{
		{ProjectID: "example-project", ContentHash: "h-muse-req", TS: t3Req.Format(time.RFC3339), Role: "user", Content: "Check keyboard navigation in the settings form", Tool: "muse", SessionID: "sess-c3", SourceOrder: 1, SourceTime: SortableTime(t3Req)},
		{ProjectID: "example-project", ContentHash: "h-muse-rep", TS: t3Rep.Format(time.RFC3339), Role: "assistant", Content: "Found a missing label on the timezone selector", Tool: "muse", SessionID: "sess-c3", SourceOrder: 2, SourceTime: SortableTime(t3Rep)},
	})
	if err != nil {
		t.Fatalf("WriteRun muse: %v", err)
	}

	// Ingest a session in another project to test isolation
	_, _, err = st.WriteRun("", "codex", "sess-other", []LoadMessage{
		{ProjectID: "other-project", ContentHash: "h-other", TS: now.Format(time.RFC3339), Role: "user", Content: "Other project message", Tool: "codex", SessionID: "sess-other", SourceOrder: 1, SourceTime: SortableTime(now)},
	})
	if err != nil {
		t.Fatalf("WriteRun other: %v", err)
	}

	res, err := st.RecentActivity(ActivityQueryOpts{
		ProjectID: "example-project",
		SinceTime: windowStart,
		Top:       10,
	})
	if err != nil {
		t.Fatalf("RecentActivity: %v", err)
	}

	if res.TotalMatching != 3 {
		t.Fatalf("expected 3 matching sessions, got %d", res.TotalMatching)
	}
	if len(res.Sessions) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(res.Sessions))
	}

	// Verify ordering: newest activity first (muse, then opencode, then codex)
	if res.Sessions[0].Tool != "muse" || res.Sessions[0].SessionID != "sess-c3" {
		t.Errorf("session 0: expected muse / sess-c3, got %s / %s", res.Sessions[0].Tool, res.Sessions[0].SessionID)
	}
	if res.Sessions[1].Tool != "opencode" || res.Sessions[1].SessionID != "sess-b2" {
		t.Errorf("session 1: expected opencode / sess-b2, got %s / %s", res.Sessions[1].Tool, res.Sessions[1].SessionID)
	}
	if res.Sessions[2].Tool != "codex" || res.Sessions[2].SessionID != "sess-a1" {
		t.Errorf("session 2: expected codex / sess-a1, got %s / %s", res.Sessions[2].Tool, res.Sessions[2].SessionID)
	}

	// Verify excerpts and inspect references
	museSess := res.Sessions[0]
	if museSess.Request == nil || !strings.Contains(museSess.Request.Text, "Check keyboard navigation") {
		t.Errorf("muse request mismatch: %+v", museSess.Request)
	}
	if museSess.LatestReply == nil || !strings.Contains(museSess.LatestReply.Text, "missing label") {
		t.Errorf("muse reply mismatch: %+v", museSess.LatestReply)
	}

	// Test drill-down with MessageInContext
	ctxReply, err := st.MessageInContext(museSess.LatestReply.MessageID, 3, museSess.LatestReply.Occurrence)
	if err != nil {
		t.Fatalf("MessageInContext reply drill-down: %v", err)
	}
	if !ctxReply.SessionAvailable || ctxReply.SessionID != "sess-c3" {
		t.Fatalf("expanded context mismatch: %+v", ctxReply)
	}
}

func TestRecentActivityDeduplicatedContentRetainsToolAndOccurrence(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_dedup.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	if _, err := st.EnsureProject("/workspace/proj"); err != nil {
		t.Fatalf("EnsureProject: %v", err)
	}

	t1 := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)

	// User sends EXACT same prompt "Fix tests" in Codex at 10:00, then in OpenCode at 12:00
	_, _, err = st.WriteRun("", "codex", "sess-codex", []LoadMessage{
		{ProjectID: "proj", ContentHash: "hash-fix-tests", TS: t1.Format(time.RFC3339), Role: "user", Content: "Fix tests", Tool: "codex", SessionID: "sess-codex", SourceOrder: 1, SourceTime: SortableTime(t1)},
		{ProjectID: "proj", ContentHash: "hash-codex-done", TS: t1.Add(time.Minute).Format(time.RFC3339), Role: "assistant", Content: "Codex finished", Tool: "codex", SessionID: "sess-codex", SourceOrder: 2, SourceTime: SortableTime(t1.Add(time.Minute))},
	})
	if err != nil {
		t.Fatalf("WriteRun codex: %v", err)
	}

	_, _, err = st.WriteRun("", "opencode", "sess-opencode", []LoadMessage{
		{ProjectID: "proj", ContentHash: "hash-fix-tests", TS: t2.Format(time.RFC3339), Role: "user", Content: "Fix tests", Tool: "opencode", SessionID: "sess-opencode", SourceOrder: 1, SourceTime: SortableTime(t2)},
		{ProjectID: "proj", ContentHash: "hash-opencode-done", TS: t2.Add(time.Minute).Format(time.RFC3339), Role: "assistant", Content: "OpenCode finished", Tool: "opencode", SessionID: "sess-opencode", SourceOrder: 2, SourceTime: SortableTime(t2.Add(time.Minute))},
	})
	if err != nil {
		t.Fatalf("WriteRun opencode: %v", err)
	}

	res, err := st.RecentActivity(ActivityQueryOpts{
		ProjectID: "proj",
		SinceTime: t1.Add(-1 * time.Hour),
		Top:       10,
	})
	if err != nil {
		t.Fatalf("RecentActivity: %v", err)
	}

	if len(res.Sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(res.Sessions))
	}

	// OpenCode session is newer (12:00) than Codex (10:00)
	s0 := res.Sessions[0]
	if s0.Tool != "opencode" || s0.SessionID != "sess-opencode" {
		t.Fatalf("expected session 0 to be opencode, got %+v", s0)
	}
	if s0.Request == nil || s0.Request.Occurrence != 2 {
		t.Fatalf("expected opencode request to have occurrence 2, got %+v", s0.Request)
	}

	s1 := res.Sessions[1]
	if s1.Tool != "codex" || s1.SessionID != "sess-codex" {
		t.Fatalf("expected session 1 to be codex, got %+v", s1)
	}
	if s1.Request == nil || s1.Request.Occurrence != 1 {
		t.Fatalf("expected codex request to have occurrence 1, got %+v", s1.Request)
	}

	// Expand occurrence 2: should belong to opencode sess-opencode
	ctx2, err := st.MessageInContext(s0.Request.MessageID, 2, s0.Request.Occurrence)
	if err != nil {
		t.Fatalf("MessageInContext occurrence 2: %v", err)
	}
	if ctx2.SessionID != "sess-opencode" {
		t.Fatalf("expected session sess-opencode, got %s", ctx2.SessionID)
	}

	// Expand occurrence 1: should belong to codex sess-codex
	ctx1, err := st.MessageInContext(s1.Request.MessageID, 2, s1.Request.Occurrence)
	if err != nil {
		t.Fatalf("MessageInContext occurrence 1: %v", err)
	}
	if ctx1.SessionID != "sess-codex" {
		t.Fatalf("expected session sess-codex, got %s", ctx1.SessionID)
	}
}

func TestRecentActivityResumedSessionAndSameTimeTies(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_resumed.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	if _, err := st.EnsureProject("/workspace/proj"); err != nil {
		t.Fatalf("EnsureProject: %v", err)
	}

	tEarly := time.Date(2026, 9, 16, 9, 0, 0, 0, time.UTC)
	tSame := time.Date(2026, 9, 16, 15, 0, 0, 0, time.UTC)

	// Resumed session: turn 1 at 09:00, turn 2 at 15:00
	_, _, err = st.WriteRun("", "codex", "sess-resumed", []LoadMessage{
		{ProjectID: "proj", ContentHash: "h-early", TS: tEarly.Format(time.RFC3339), Role: "user", Content: "Initial request at 9", Tool: "codex", SessionID: "sess-resumed", SourceOrder: 1, SourceTime: SortableTime(tEarly)},
	})
	if err != nil {
		t.Fatalf("WriteRun resumed turn 1: %v", err)
	}
	_, _, err = st.WriteRun("", "codex", "sess-resumed", []LoadMessage{
		{ProjectID: "proj", ContentHash: "h-resumed-late", TS: tSame.Format(time.RFC3339), Role: "assistant", Content: "Final answer at 15", Tool: "codex", SessionID: "sess-resumed", SourceOrder: 2, SourceTime: SortableTime(tSame)},
	})
	if err != nil {
		t.Fatalf("WriteRun resumed turn 2: %v", err)
	}

	// Another tool at the exact same latest time 15:00 to test deterministic tie-breaking
	_, _, err = st.WriteRun("", "opencode", "sess-other", []LoadMessage{
		{ProjectID: "proj", ContentHash: "h-other-req", TS: tSame.Format(time.RFC3339), Role: "user", Content: "Concurrent opencode req", Tool: "opencode", SessionID: "sess-other", SourceOrder: 1, SourceTime: SortableTime(tSame)},
	})
	if err != nil {
		t.Fatalf("WriteRun other: %v", err)
	}

	res, err := st.RecentActivity(ActivityQueryOpts{
		ProjectID: "proj",
		SinceTime: tEarly.Add(-1 * time.Hour),
		Top:       10,
	})
	if err != nil {
		t.Fatalf("RecentActivity: %v", err)
	}

	if len(res.Sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(res.Sessions))
	}

	// Tie-breaking: both have latest_time = 15:00, deterministic by tool ASC (codex < opencode)
	if res.Sessions[0].Tool != "codex" || res.Sessions[1].Tool != "opencode" {
		t.Fatalf("expected codex then opencode, got %s then %s", res.Sessions[0].Tool, res.Sessions[1].Tool)
	}

	// Verify resumed session kept earliest request (from 09:00) and latest reply (from 15:00)
	codexSess := res.Sessions[0]
	if codexSess.Request == nil || !strings.Contains(codexSess.Request.Text, "Initial request at 9") {
		t.Errorf("resumed request mismatch: %+v", codexSess.Request)
	}
	if codexSess.LatestReply == nil || !strings.Contains(codexSess.LatestReply.Text, "Final answer at 15") {
		t.Errorf("resumed reply mismatch: %+v", codexSess.LatestReply)
	}
}

func TestRecentActivitySharedSessionIDAcrossTools(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_shared_sess_id.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	if _, err := st.EnsureProject("/workspace/proj"); err != nil {
		t.Fatalf("EnsureProject: %v", err)
	}

	t1 := time.Date(2026, 9, 16, 11, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)

	// Both tools use session ID "1"
	_, _, err = st.WriteRun("", "codex", "1", []LoadMessage{
		{ProjectID: "proj", ContentHash: "h-c1", TS: t1.Format(time.RFC3339), Role: "user", Content: "Codex session 1", Tool: "codex", SessionID: "1", SourceOrder: 1, SourceTime: SortableTime(t1)},
	})
	if err != nil {
		t.Fatalf("WriteRun codex 1: %v", err)
	}

	_, _, err = st.WriteRun("", "opencode", "1", []LoadMessage{
		{ProjectID: "proj", ContentHash: "h-o1", TS: t2.Format(time.RFC3339), Role: "user", Content: "OpenCode session 1", Tool: "opencode", SessionID: "1", SourceOrder: 1, SourceTime: SortableTime(t2)},
	})
	if err != nil {
		t.Fatalf("WriteRun opencode 1: %v", err)
	}

	res, err := st.RecentActivity(ActivityQueryOpts{
		ProjectID: "proj",
		SinceTime: t1.Add(-1 * time.Hour),
		Top:       10,
	})
	if err != nil {
		t.Fatalf("RecentActivity: %v", err)
	}

	if len(res.Sessions) != 2 {
		t.Fatalf("expected 2 distinct sessions for shared ID '1', got %d", len(res.Sessions))
	}
	if res.Sessions[0].Tool != "opencode" || res.Sessions[1].Tool != "codex" {
		t.Fatalf("tools mismatch: %s, %s", res.Sessions[0].Tool, res.Sessions[1].Tool)
	}
}

func TestRecentActivityMissingRolesAndUnicodeTruncation(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_roles_unicode.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	if _, err := st.EnsureProject("/workspace/proj"); err != nil {
		t.Fatalf("EnsureProject: %v", err)
	}

	t1 := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
	longUnicode := strings.Repeat("صِلَة ", 50) // 250 runes > 160 runes

	// Session with request only
	_, _, err = st.WriteRun("", "muse", "sess-req-only", []LoadMessage{
		{ProjectID: "proj", ContentHash: "h-long", TS: t1.Format(time.RFC3339), Role: "user", Content: longUnicode, Tool: "muse", SessionID: "sess-req-only", SourceOrder: 1, SourceTime: SortableTime(t1)},
	})
	if err != nil {
		t.Fatalf("WriteRun: %v", err)
	}

	// Session with reply only
	t2 := t1.Add(time.Hour)
	_, _, err = st.WriteRun("", "codex", "sess-rep-only", []LoadMessage{
		{ProjectID: "proj", ContentHash: "h-rep", TS: t2.Format(time.RFC3339), Role: "assistant", Content: "Assistant only response", Tool: "codex", SessionID: "sess-rep-only", SourceOrder: 1, SourceTime: SortableTime(t2)},
	})
	if err != nil {
		t.Fatalf("WriteRun: %v", err)
	}

	res, err := st.RecentActivity(ActivityQueryOpts{
		ProjectID: "proj",
		SinceTime: t1.Add(-1 * time.Hour),
		Top:       10,
	})
	if err != nil {
		t.Fatalf("RecentActivity: %v", err)
	}

	if len(res.Sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(res.Sessions))
	}

	// sess-rep-only
	repOnly := res.Sessions[0]
	if repOnly.Request != nil {
		t.Errorf("expected nil Request for reply-only session, got %+v", repOnly.Request)
	}
	if repOnly.LatestReply == nil {
		t.Errorf("expected non-nil LatestReply for reply-only session")
	}

	// sess-req-only
	reqOnly := res.Sessions[1]
	if reqOnly.LatestReply != nil {
		t.Errorf("expected nil LatestReply for request-only session, got %+v", reqOnly.LatestReply)
	}
	if reqOnly.Request == nil {
		t.Fatalf("expected non-nil Request for request-only session")
	}

	// Check Unicode rune truncation: must be at most 160 runes and end with ellipsis
	runes := []rune(reqOnly.Request.Text)
	if len(runes) > 160 {
		t.Errorf("expected <= 160 runes, got %d", len(runes))
	}
	if !strings.HasSuffix(reqOnly.Request.Text, "…") {
		t.Errorf("expected ellipsis suffix, got %s", reqOnly.Request.Text)
	}
}

func TestRecentActivityLegacyMessagesNotInventedAsSessions(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_legacy.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	if _, err := st.EnsureProject("/workspace/proj"); err != nil {
		t.Fatalf("EnsureProject: %v", err)
	}

	t1 := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)

	// Message with empty session_id (legacy provenance)
	_, _, err = st.WriteRun("", "codex", "", []LoadMessage{
		{ProjectID: "proj", ContentHash: "h-legacy", TS: t1.Format(time.RFC3339), Role: "user", Content: "Legacy message without session ID", Tool: "codex", SessionID: "", SourceOrder: 0, SourceTime: SortableTime(t1)},
	})
	if err != nil {
		t.Fatalf("WriteRun legacy: %v", err)
	}

	res, err := st.RecentActivity(ActivityQueryOpts{
		ProjectID: "proj",
		SinceTime: t1.Add(-1 * time.Hour),
		Top:       10,
	})
	if err != nil {
		t.Fatalf("RecentActivity: %v", err)
	}

	// Must NOT invent a session
	if len(res.Sessions) != 0 {
		t.Fatalf("expected 0 sessions from legacy message, got %d", len(res.Sessions))
	}
	if res.LegacyMessagesCount != 1 {
		t.Fatalf("expected 1 legacy message counted, got %d", res.LegacyMessagesCount)
	}
}

func TestRecentActivityReadOnlyLeavesStoreUnchanged(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_ro.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := st.EnsureProject("/workspace/proj"); err != nil {
		t.Fatalf("EnsureProject: %v", err)
	}
	st.Close()

	fiBefore, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("os.Stat before: %v", err)
	}

	stRO, err := OpenRO(dbPath)
	if err != nil {
		t.Fatalf("OpenRO: %v", err)
	}
	_, err = stRO.RecentActivity(ActivityQueryOpts{
		ProjectID: "proj",
		Top:       10,
	})
	stRO.Close()
	if err != nil {
		t.Fatalf("RecentActivity: %v", err)
	}

	fiAfter, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("os.Stat after: %v", err)
	}

	if fiBefore.Size() != fiAfter.Size() {
		t.Errorf("DB size changed under read-only query: %d -> %d", fiBefore.Size(), fiAfter.Size())
	}
}

func TestRecentActivityQueryPlanUsesIndexes(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_plan.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	// Verify group query plan uses idx_messages_project and idx_occurrences_message
	groupPlanQ := `EXPLAIN QUERY PLAN
		SELECT o.tool, o.session_id, MAX(o.source_time) AS latest_time
		FROM source_occurrences o
		JOIN messages m ON m.id = o.message_id
		WHERE m.project_id = ? AND o.session_id != '' AND m.role IN ('user', 'assistant') AND o.source_time >= ?
		GROUP BY o.tool, o.session_id
		ORDER BY latest_time DESC, o.tool ASC, o.session_id ASC
		LIMIT 10`
	rows, err := st.DB.Query(groupPlanQ, "proj", "2026-09-16")
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN group: %v", err)
	}
	defer rows.Close()

	var planLines []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("Scan plan: %v", err)
		}
		planLines = append(planLines, detail)
	}
	fullPlan := strings.Join(planLines, "\n")
	if (!strings.Contains(fullPlan, "idx_messages_project") && !strings.Contains(fullPlan, "idx_messages_near")) || !strings.Contains(fullPlan, "idx_occurrences_message") {
		t.Errorf("expected plan to use index on messages and idx_occurrences_message, got:\n%s", fullPlan)
	}
}

func TestFormatActivityTimeIncludesTheDate(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"rfc3339", "2026-09-16T18:25:13Z", "09-16 18:25"},
		{"nanoseconds are parsed", "2026-09-16T18:25:13.123456789Z", "09-16 18:25"},
		{"sortable time", "2026-09-16T18:25:13.000000000Z", "09-16 18:25"},
		{"offset is normalized to UTC", "2026-09-16T21:25:13+03:00", "09-16 18:25"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatActivityTime(tc.in); got != tc.want {
				t.Errorf("FormatActivityTime(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}

	// An unparseable stamp is echoed rather than silently replaced with a wrong
	// time: a reader must be able to see that the value was not understood.
	if got := FormatActivityTime("not-a-time"); got != "not-a-time" {
		t.Errorf("unparseable stamp = %q, want it echoed back", got)
	}
}
