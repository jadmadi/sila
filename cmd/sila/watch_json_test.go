package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func decodeWatchJSON(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal watch event: %v", err)
	}
	// Each event must survive a marshal/unmarshal round trip as one object.
	var obj map[string]any
	dec := json.NewDecoder(strings.NewReader(string(raw) + "\n"))
	if err := dec.Decode(&obj); err != nil {
		t.Fatalf("event is not one JSON object per line: %v", err)
	}
	return obj
}

func TestWatchScopeName(t *testing.T) {
	if got := watchScopeName("sila"); got != "sila" {
		t.Errorf("project scope = %q, expected %q", got, "sila")
	}
	if got := watchScopeName(""); got != "global" {
		t.Errorf("empty scope = %q, expected %q", got, "global")
	}
}

func TestWatchStartedEventShape(t *testing.T) {
	obj := decodeWatchJSON(t, newWatchStartedEvent(123, "/tmp/k.db", "15s", "sila"))
	for _, k := range []string{"event", "ts", "pid", "store", "interval", "scope"} {
		if _, ok := obj[k]; !ok {
			t.Errorf("started event missing required key %q: %v", k, obj)
		}
	}
	if obj["event"] != "started" || obj["scope"] != "sila" {
		t.Errorf("unexpected started event: %v", obj)
	}
	if _, ok := obj["status"]; ok {
		t.Errorf("started event must not carry status fields: %v", obj)
	}
}

func TestWatchStatusEventStripsANSI(t *testing.T) {
	obj := decodeWatchJSON(t, newWatchStatusEvent(
		"sila", 3, 5, 100, 2,
		"In sync for \x1b[1msila\x1b[0m (0 pending)",
		"[\x1b[38;2;255;138;60m███\x1b[0m] 50%",
		"           ↳ \x1b[1mworker\x1b[0m",
	))
	for _, k := range []string{"status", "roadmap", "action"} {
		s, _ := obj[k].(string)
		if strings.Contains(s, "\x1b[") {
			t.Errorf("%s must be ANSI-free for machines, got %q", k, s)
		}
	}
	if obj["event"] != "status" || obj["new_records"] != float64(3) || obj["facts_distilled"] != float64(5) {
		t.Errorf("unexpected status event: %v", obj)
	}
}

func TestWatchStatusEventIdleStaysLean(t *testing.T) {
	obj := decodeWatchJSON(t, newWatchStatusEvent("global", 0, 0, 7, 1, "In sync", "", ""))
	for _, k := range []string{"new_records", "facts_distilled", "roadmap", "action"} {
		if _, ok := obj[k]; ok {
			t.Errorf("idle heartbeat must omit %q via omitempty: %v", k, obj)
		}
	}
	if obj["verified"] != float64(7) {
		t.Errorf("idle heartbeat must keep totals: %v", obj)
	}
}

func TestWatchStoppedEventShape(t *testing.T) {
	obj := decodeWatchJSON(t, newWatchStoppedEvent())
	if obj["event"] != "stopped" {
		t.Errorf("expected stopped event, got %v", obj)
	}
	if _, ok := obj["ts"]; !ok {
		t.Errorf("stopped event missing ts: %v", obj)
	}
}
