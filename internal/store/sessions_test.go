package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestAgentSessionLifecycle(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	now := time.Now().UTC().Truncate(time.Second)
	sess := AgentSession{
		SessionID: "s1", ProjectID: "demo", Harness: "cli", Agent: "agent",
		ActiveGoal: "goal-a", ClaimID: "claim-1", Cwd: "/tmp/demo", PID: 42,
		Source: "flag", StartedAt: now, LastSeenAt: now,
	}
	if err := st.UpsertAgentSession(sess); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetAgentSession("s1")
	if err != nil || got == nil || got.Agent != "agent" || got.ActiveGoal != "goal-a" {
		t.Fatalf("get: %+v err=%v", got, err)
	}
	later := now.Add(time.Minute)
	if err := st.TouchAgentSession("s1", later); err != nil {
		t.Fatal(err)
	}
	got, _ = st.GetAgentSession("s1")
	if !got.LastSeenAt.Equal(later) {
		t.Fatalf("last_seen = %v, want %v", got.LastSeenAt, later)
	}
	// Upsert is idempotent and preserves the original started_at.
	got.StartedAt = now
	if err := st.UpsertAgentSession(*got); err != nil {
		t.Fatal(err)
	}
	again, _ := st.GetAgentSession("s1")
	if !again.StartedAt.Equal(now) {
		t.Fatalf("started_at changed: %v", again.StartedAt)
	}
	list, err := st.ListAgentSessions("demo", time.Time{}, 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %+v err=%v", list, err)
	}
	if err := st.CloseAgentSession("s1", later); err != nil {
		t.Fatal(err)
	}
	closed, _ := st.GetAgentSession("s1")
	if closed.ClosedAt.IsZero() {
		t.Fatal("session was not closed")
	}
}

func TestPruneAgentSessionsOnlyClosedOld(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().UTC()
	old := now.Add(-100 * 24 * time.Hour)
	entries := []AgentSession{
		{SessionID: "old-closed", ProjectID: "p", Harness: "h", Agent: "a", Source: "flag", StartedAt: old, LastSeenAt: old, ClosedAt: old},
		{SessionID: "old-open", ProjectID: "p", Harness: "h", Agent: "a", Source: "flag", StartedAt: old, LastSeenAt: old},
		{SessionID: "new-closed", ProjectID: "p", Harness: "h", Agent: "a", Source: "flag", StartedAt: now, LastSeenAt: now, ClosedAt: now},
	}
	for _, sess := range entries {
		if err := st.UpsertAgentSession(sess); err != nil {
			t.Fatal(err)
		}
	}
	deleted, err := st.PruneAgentSessions(now.Add(-90 * 24 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	for _, id := range []string{"old-open", "new-closed"} {
		if got, _ := st.GetAgentSession(id); got == nil {
			t.Fatalf("%s should remain", id)
		}
	}
	if got, _ := st.GetAgentSession("old-closed"); got != nil {
		t.Fatal("old closed session was not pruned")
	}
}

func TestAgentSessionGitContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	base := AgentSession{
		SessionID: "s1", ProjectID: "p", Harness: "codex", Agent: "codex", Source: "flag",
		Branch: "main", Head: "abc123",
	}
	if err := st.UpsertAgentSession(base); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetAgentSession("s1")
	if err != nil || got == nil {
		t.Fatalf("get: %v %+v", err, got)
	}
	if got.Branch != "main" || got.Head != "abc123" || got.Detached {
		t.Fatalf("snapshot not persisted: %+v", got)
	}

	// A later metadata-only upsert (subagent stop) must not erase the snapshot.
	base.Branch, base.Head = "", ""
	if err := st.UpsertAgentSession(base); err != nil {
		t.Fatal(err)
	}
	got, _ = st.GetAgentSession("s1")
	if got.Branch != "main" || got.Head != "abc123" {
		t.Fatalf("metadata upsert wiped snapshot: %+v", got)
	}

	base.Branch, base.Head = "feature", "def456"
	base.ContextDrift = "main@abc123 -> feature@def456"
	if err := st.UpsertAgentSession(base); err != nil {
		t.Fatal(err)
	}
	got, _ = st.GetAgentSession("s1")
	if got.Branch != "feature" || got.Head != "def456" || got.ContextDrift == "" || got.ContextDriftAt == nil {
		t.Fatalf("drift snapshot not persisted: %+v", got)
	}
	sessions, err := st.ListAgentSessions("p", time.Time{}, 10)
	if err != nil || len(sessions) != 1 || sessions[0].Branch != "feature" {
		t.Fatalf("list = %+v err=%v", sessions, err)
	}
}
