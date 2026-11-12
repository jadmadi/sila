package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestAgentSessionStartTicksRoundTrip(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().UTC()
	if err := st.UpsertAgentSession(AgentSession{SessionID: "s1", ProjectID: "p", Harness: "h", Agent: "a", Source: "test", PID: 42, PIDStartTicks: 123456, StartedAt: now, LastSeenAt: now}); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetAgentSession("s1")
	if err != nil || got == nil {
		t.Fatalf("get: %v", err)
	}
	if got.PIDStartTicks != 123456 || got.PID != 42 {
		t.Fatalf("session = %+v", got)
	}
	if err := st.UpsertAgentSession(AgentSession{SessionID: "s1", ProjectID: "p", Harness: "h", Agent: "a", Source: "test", PID: 42, PIDStartTicks: 654321, StartedAt: now, LastSeenAt: now}); err != nil {
		t.Fatal(err)
	}
	got, _ = st.GetAgentSession("s1")
	if got.PIDStartTicks != 654321 {
		t.Fatalf("ticks not refreshed: %d", got.PIDStartTicks)
	}
	if version, err := st.SchemaVersion(); err != nil || version != CurrentSchemaVersion() {
		t.Fatalf("schema = %d err=%v", version, err)
	}
}
