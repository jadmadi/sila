package store

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestMemoryEventSchemaHasNoContentFields(t *testing.T) {
	allowed := map[string]bool{
		"ID": true, "ProjectID": true, "SessionID": true, "Agent": true,
		"Verb": true, "Outcome": true, "GoalSlug": true, "Source": true,
		"Matched": true, "CreatedAt": true,
	}
	typ := reflect.TypeOf(MemoryEvent{})
	for i := 0; i < typ.NumField(); i++ {
		if !allowed[typ.Field(i).Name] {
			t.Errorf("unexpected content-bearing field %q", typ.Field(i).Name)
		}
	}
}

func TestMemoryEventsRecordListDedupPrune(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().UTC().Truncate(time.Second)
	e := MemoryEvent{ProjectID: "p", SessionID: "s", Agent: "a", Verb: "memo", Outcome: "ok", Source: "cli", CreatedAt: now}
	if err := st.RecordMemoryEvent(e); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordMemoryEvent(e); err != nil {
		t.Fatal(err)
	}
	events, err := st.ListMemoryEvents(MemoryEventFilter{ProjectID: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("dedup failed: %+v", events)
	}
	if err := st.RecordMemoryEvent(MemoryEvent{ProjectID: "p", SessionID: "s2", Verb: "recall", Outcome: "empty", Source: "cli", CreatedAt: now.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	events, err = st.ListMemoryEvents(MemoryEventFilter{Verb: "recall"})
	if err != nil || len(events) != 1 || events[0].Outcome != "empty" {
		t.Fatalf("filter: %+v err=%v", events, err)
	}
	deleted, err := st.PruneMemoryEvents(now.Add(30 * time.Second))
	if err != nil || deleted != 2 {
		t.Fatalf("prune deleted=%d err=%v", deleted, err)
	}
}
