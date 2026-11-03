package main

import (
	"time"
)

// watchJSONEvent is one JSON Lines record on `sila watch --json` stdout.
// Every line carries event/ts/scope; status polls add activity counters and
// plain-text (ANSI-stripped) human-parity fields so machines get exactly what
// terminals show. Counters use omitempty so idle heartbeats stay lean.
type watchJSONEvent struct {
	Event          string `json:"event"`
	TS             string `json:"ts"`
	PID            int    `json:"pid,omitempty"`
	Store          string `json:"store,omitempty"`
	Interval       string `json:"interval,omitempty"`
	Scope          string `json:"scope,omitempty"`
	NewRecords     int64  `json:"new_records,omitempty"`
	FactsDistilled int    `json:"facts_distilled,omitempty"`
	Verified       int    `json:"verified,omitempty"`
	Projects       int    `json:"projects,omitempty"`
	Status         string `json:"status,omitempty"`
	Roadmap        string `json:"roadmap,omitempty"`
	Action         string `json:"action,omitempty"`
}

// watchScopeName renders the machine-readable scope: project ID or "global".
func watchScopeName(project string) string {
	if project != "" {
		return project
	}
	return "global"
}

// newWatchStartedEvent builds the first JSONL line, replacing the human
// banner when --json is set.
func newWatchStartedEvent(pid int, dbPath, interval, scope string) watchJSONEvent {
	return watchJSONEvent{
		Event:    "started",
		TS:       time.Now().Format(time.RFC3339),
		PID:      pid,
		Store:    dbPath,
		Interval: interval,
		Scope:    scope,
	}
}

// newWatchStatusEvent builds one per-poll JSONL line. Human-parity strings
// arrive with ANSI color codes and are stripped for machine consumption.
func newWatchStatusEvent(scope string, newRec int64, produced, verifiedN, projCount int, status, roadmap, action string) watchJSONEvent {
	return watchJSONEvent{
		Event:          "status",
		TS:             time.Now().Format(time.RFC3339),
		Scope:          scope,
		NewRecords:     newRec,
		FactsDistilled: produced,
		Verified:       verifiedN,
		Projects:       projCount,
		Status:         stripWatchANSI(status),
		Roadmap:        stripWatchANSI(roadmap),
		Action:         stripWatchANSI(action),
	}
}

// newWatchStoppedEvent builds the final JSONL line on clean shutdown.
func newWatchStoppedEvent() watchJSONEvent {
	return watchJSONEvent{Event: "stopped", TS: time.Now().Format(time.RFC3339)}
}
