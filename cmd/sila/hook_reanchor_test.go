package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/dream"
	"github.com/jadmadi/sila/internal/loaders"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestRunHookReanchorHumanDedupeAndJSON(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "store.db")
	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	env := &loaders.HookEnvelope{Event: loaders.EventPostCompact, ProjectID: "proj", Cwd: dir, SessionID: "s1"}
	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() { runHookReanchor(ui, db, dir, env, 512, false) })
	if len(out) > 512 || !strings.HasSuffix(out, "\n") {
		t.Fatalf("anchor is %d bytes or lacks newline:\n%s", len(out), out)
	}
	for _, want := range []string{"Sila re-anchor", "verbs: prime recall memo lesson case facts gate handoff sync", "contract: sila/memory-contract/v1", "next: sila goals ready"} {
		if !strings.Contains(out, want) {
			t.Fatalf("anchor missing %q:\n%s", want, out)
		}
	}
	markerPath := dream.ReanchorMarkerPath(dir, "s1")
	info, err := os.Stat(markerPath)
	if err != nil {
		t.Fatalf("marker not written: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("marker mode = %o, want 600", perm)
	}
	var marker dream.ReanchorMarker
	data, _ := os.ReadFile(markerPath)
	if err := json.Unmarshal(data, &marker); err != nil {
		t.Fatal(err)
	}
	if marker.Schema != "sila/reanchor-state/v1" || marker.Count != 1 || marker.LastText != out {
		t.Fatalf("bad marker: %+v", marker)
	}

	second := captureStdout(t, func() { runHookReanchor(ui, db, dir, env, 512, false) })
	if second != out {
		t.Fatalf("dedupe changed the output:\nfirst:%s\nsecond:%s", out, second)
	}
	data, _ = os.ReadFile(markerPath)
	_ = json.Unmarshal(data, &marker)
	if marker.Count != 1 {
		t.Fatalf("dedupe incremented count: %+v", marker)
	}

	env.Payload = map[string]interface{}{"sequence": "2"}
	third := captureStdout(t, func() { runHookReanchor(ui, db, dir, env, 512, false) })
	if third == "" {
		t.Fatal("new sequence did not emit a new anchor")
	}

	jsonEnv := &loaders.HookEnvelope{Event: loaders.EventPostCompact, ProjectID: "proj", Cwd: dir, SessionID: "s2"}
	jsonOut := captureStdout(t, func() { runHookReanchor(ui, db, dir, jsonEnv, 512, true) })
	var payload struct {
		Schema            string `json:"schema"`
		Bytes             int    `json:"bytes"`
		Text              string `json:"text"`
		MemoryUnavailable bool   `json:"memory_unavailable"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, jsonOut)
	}
	if payload.Schema != "sila/post-compaction/v1" || payload.Bytes != len(payload.Text) || payload.Text == "" {
		t.Fatalf("bad JSON payload: %+v", payload)
	}
	if payload.MemoryUnavailable {
		t.Fatal("existing store reported memory_unavailable")
	}
}

func TestRunHookReanchorMissingStoreDegrades(t *testing.T) {
	dir := t.TempDir()
	env := &loaders.HookEnvelope{Event: loaders.EventPostCompact, ProjectID: "proj", Cwd: dir, SessionID: "s3"}
	out := captureStdout(t, func() {
		runHookReanchor(render.New(os.Stdout, true), filepath.Join(dir, "missing.db"), dir, env, 512, true)
	})
	var payload struct {
		MemoryUnavailable bool   `json:"memory_unavailable"`
		Text              string `json:"text"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if !payload.MemoryUnavailable || payload.Text == "" {
		t.Fatalf("missing store did not degrade: %+v", payload)
	}
}

func TestPreCompactEventIsSilent(t *testing.T) {
	dir := t.TempDir()
	stdin, err := os.CreateTemp(dir, "hook-stdin-*")
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	if _, err := stdin.WriteString(`{"event":"pre-compact","session_id":"pre-1"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := stdin.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	previous := os.Stdin
	os.Stdin = stdin
	defer func() { os.Stdin = previous }()

	out := captureStdout(t, func() {
		runHookWithOptions(render.New(os.Stdout, true), filepath.Join(dir, "store.db"), "", "", "", "", dir, "", false, false, false, 0, nil, hookOptions{})
	})
	if out != "" {
		t.Fatalf("pre-compact emitted output: %q", out)
	}
}

func TestCompactionSessionStartDetection(t *testing.T) {
	yes := &loaders.HookEnvelope{Payload: map[string]interface{}{"source": "compact"}}
	if !isCompactionSessionStart(yes) {
		t.Fatal("source=compact must trigger the re-anchor fallback")
	}
	no := &loaders.HookEnvelope{Payload: map[string]interface{}{"source": "startup"}}
	if isCompactionSessionStart(no) {
		t.Fatal("source=startup must not trigger the fallback")
	}
	if isCompactionSessionStart(nil) {
		t.Fatal("nil envelope must not trigger the fallback")
	}
}
