package store

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestToolEventLedgerRoundTrip(t *testing.T) {
	st, err := Open(t.TempDir() + "/tool-events.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	now := time.Now().UTC().Truncate(time.Second)
	events := []ToolEvent{
		{ProjectID: "p", SessionID: "s1", Agent: "command-code", Source: "mcp", ToolName: "recall", Category: "context", Outcome: "ok", CreatedAt: now},
		{ProjectID: "p", SessionID: "s1", Agent: "command-code", Source: "cli", ToolName: "prime", Category: "context", Outcome: "ok", CreatedAt: now.Add(time.Second)},
		{ProjectID: "p", SessionID: "", Agent: "cli", Source: "cli", ToolName: "status", Category: "cli", Outcome: "ok", CreatedAt: now},
	}
	for _, ev := range events {
		if err := st.RecordToolEvent(ev); err != nil {
			t.Fatal(err)
		}
	}
	// Duplicate suppression: same session/source/tool/second.
	if err := st.RecordToolEvent(events[0]); err != nil {
		t.Fatal(err)
	}
	got, err := st.ListToolEvents(ToolEventFilter{ProjectID: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("events = %d, want 3: %+v", len(got), got)
	}
	counts, err := st.ToolEventCounts(ToolEventFilter{ProjectID: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if counts["recall"] != 1 || counts["prime"] != 1 || counts["status"] != 1 {
		t.Fatalf("counts = %+v", counts)
	}
	pruned, err := st.PruneToolEvents(now.Add(-time.Hour))
	if err != nil || pruned != 0 {
		t.Fatalf("prune future = %d, %v", pruned, err)
	}
	pruned, err = st.PruneToolEvents(now.Add(2 * time.Second))
	if err != nil || pruned != 3 {
		t.Fatalf("prune all = %d, %v", pruned, err)
	}
}

func TestBuildArgsShapePrivacy(t *testing.T) {
	shape := BuildArgsShape([]byte(`{"query":"secret text","api_token":"abc","limit":10,"nested":{"path":"/home/x"},"ok":true}`))
	if strings.Contains(shape, "secret text") || strings.Contains(shape, "abc") || strings.Contains(shape, "/home/x") {
		t.Fatalf("shape leaked values: %s", shape)
	}
	for _, want := range []string{"query:string", "redacted:string", "limit:number", "nested:object", "ok:boolean"} {
		if !strings.Contains(shape, want) {
			t.Errorf("shape %q missing %q", shape, want)
		}
	}
	long := `{"` + strings.Repeat("k", 100) + `":"` + strings.Repeat("v", 1000) + `"}`
	if got := BuildArgsShape([]byte(long)); len(got) > 512 {
		t.Fatalf("shape %d bytes, want <=512", len(got))
	}
	many := "{"
	for i := 0; i < 100; i++ {
		if i > 0 {
			many += ","
		}
		many += fmt.Sprintf(`"k%d":1`, i)
	}
	many += "}"
	if got := BuildArgsShape([]byte(many)); len(got) > 512 || !strings.HasPrefix(got, "k0:number") {
		t.Fatalf("shape cap failed (%d bytes): %s", len(got), got)
	}
}
