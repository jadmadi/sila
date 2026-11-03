package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestPrimeEventsLedger(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "prime.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().UTC()
	events := []PrimeEvent{
		{ProjectID: "p", SessionID: "s1", Harness: "claude", Agent: "claude", Source: "hook", Status: "ok", DurationMS: 5, BriefBytes: 400, CreatedAt: now.Add(-2 * time.Hour)},
		{ProjectID: "p", SessionID: "s2", Harness: "claude", Agent: "claude", Source: "hook", Status: "failed", ErrorClass: "brief_build", CreatedAt: now.Add(-time.Hour)},
		{ProjectID: "p", SessionID: "s3", Harness: "cursor", Agent: "cursor", Source: "hook", Status: "ok", CreatedAt: now.Add(-30 * time.Minute)},
	}
	for _, e := range events {
		if err := st.RecordPrimeEvent(e); err != nil {
			t.Fatal(err)
		}
	}
	// Same-second duplicate is deduped.
	if err := st.RecordPrimeEvent(events[2]); err != nil {
		t.Fatal(err)
	}
	all, err := st.ListPrimeEvents(PrimeEventFilter{ProjectID: "p"})
	if err != nil || len(all) != 3 {
		t.Fatalf("events=%d err=%v", len(all), err)
	}
	claude, err := st.ListPrimeEvents(PrimeEventFilter{ProjectID: "p", Harness: "claude"})
	if err != nil || len(claude) != 2 {
		t.Fatalf("claude=%d err=%v", len(claude), err)
	}
	latest, err := st.LatestPrimeEvent("p", "claude")
	if err != nil || latest == nil || latest.Status != "failed" {
		t.Fatalf("latest=%+v err=%v", latest, err)
	}
	failures, err := st.ListPrimeEvents(PrimeEventFilter{ProjectID: "p", Status: "failed"})
	if err != nil || len(failures) != 1 || failures[0].ErrorClass != "brief_build" {
		t.Fatalf("failures=%+v err=%v", failures, err)
	}
	since, err := st.ListPrimeEvents(PrimeEventFilter{ProjectID: "p", Since: now.Add(-45 * time.Minute)})
	if err != nil || len(since) != 1 || since[0].Harness != "cursor" {
		t.Fatalf("since=%+v err=%v", since, err)
	}
	deleted, err := st.PrunePrimeEvents(now.Add(-90 * time.Minute))
	if err != nil || deleted != 1 {
		t.Fatalf("prune=%d err=%v", deleted, err)
	}
}
