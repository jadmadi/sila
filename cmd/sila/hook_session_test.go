package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/dream"
	"github.com/jadmadi/sila/internal/loaders"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func newHookTestStore(t *testing.T) (*store.Store, store.Project, string) {
	t.Helper()
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "hook-session.db"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.EnsureProject(tmp)
	if err != nil {
		st.Close()
		t.Fatal(err)
	}
	return st, p, tmp
}

func TestRunHookSessionStartJSON(t *testing.T) {
	st, p, tmp := newHookTestStore(t)
	defer st.Close()
	ui := render.New(os.Stdout, true)
	var out string
	out = captureStdout(t, func() {
		runHookSessionStart(ui, st, p.ID, tmp, "sess-start", "test-agent", 512, true)
	})
	var brief dream.SessionStartBrief
	if err := json.Unmarshal([]byte(out), &brief); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if brief.MemoryContractVersion != dream.MemoryContractVersion || brief.Bytes > 512 {
		t.Fatalf("unexpected brief: %+v", brief)
	}
}

func TestRunHookSessionStartFailureReceipt(t *testing.T) {
	st, p, tmp := newHookTestStore(t)
	defer st.Close()
	t.Setenv("SILA_PRIME_FAIL", "1")
	ui := render.New(os.Stdout, true)
	receipt := runHookSessionStart(ui, st, p.ID, tmp, "sess-fail", "test-agent", 512, false)
	if receipt.Status != loaders.PrimeStatusFailed || receipt.ErrorClass != "brief_build" {
		t.Fatalf("failed receipt = %+v", receipt)
	}
	if receipt.SessionID != "sess-fail" {
		t.Fatalf("session id = %q", receipt.SessionID)
	}
}

func TestRunHookSessionEndAutoHandoffAndRefusal(t *testing.T) {
	st, p, tmp := newHookTestStore(t)
	defer st.Close()
	ui := render.New(os.Stdout, true)

	var code int
	out := captureStdout(t, func() {
		code = runHookSessionEnd(ui, st, p.ID, tmp, "sess-end", "test", false, true, false, true)
	})
	if code != 0 {
		t.Fatalf("session-end code = %d, want 0", code)
	}
	var res dream.SessionEndResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if !res.HandoffWritten || res.HandoffID == "" {
		t.Fatalf("auto-handoff not written: %+v", res)
	}

	// A second session-end for the same session is idempotent.
	out = captureStdout(t, func() {
		code = runHookSessionEnd(ui, st, p.ID, tmp, "sess-end", "test", false, true, false, true)
	})
	if code != 0 {
		t.Fatalf("second session-end code = %d, want 0", code)
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if res.HandoffWritten || !res.HandoffExisting {
		t.Fatalf("second session-end not idempotent: %+v", res)
	}

	// A fresh store with auto-handoff disabled exits 2 and writes nothing.
	st2, p2, tmp2 := newHookTestStore(t)
	defer st2.Close()
	out = captureStdout(t, func() {
		code = runHookSessionEnd(ui, st2, p2.ID, tmp2, "sess-disabled", "test", false, false, false, true)
	})
	if code != 2 {
		t.Fatalf("disabled auto-handoff code = %d, want 2", code)
	}
	if h, _ := st2.LatestHandoff(p2.ID); h != nil {
		t.Fatalf("disabled auto-handoff wrote a handoff: %+v", h)
	}
}

func TestRunHookFailureNudge(t *testing.T) {
	st, p, _ := newHookTestStore(t)
	defer st.Close()
	if err := st.UpsertFact(store.Fact{
		ID:         "fact_nudge_lesson",
		ProjectID:  p.ID,
		Claim:      "ReferenceError undefined Foo during the build",
		Confidence: "high",
		Verified:   true,
		Kind:       "lesson",
		RepoFiles:  []string{"cmd/sila/hook.go"},
		FirstSeen:  time.Now().UTC().Format(time.RFC3339),
		PromotedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatal(err)
	}

	env := &loaders.HookEnvelope{
		Event:     loaders.EventPostToolUse,
		ProjectID: p.ID,
		Payload: map[string]any{
			"exit_code": float64(1),
			"command":   "go test ./...",
			"output":    "cmd/sila/hook.go:10: undefined: Foo",
		},
	}
	ui := render.New(os.Stdout, true)
	var out string
	out = captureStdout(t, func() {
		if !runHookFailureNudge(ui, st, env, true) {
			t.Fatal("expected a nudge")
		}
	})
	var nudge dream.FailureNudge
	if err := json.Unmarshal([]byte(out), &nudge); err != nil {
		t.Fatalf("invalid nudge JSON: %v\n%s", err, out)
	}
	if nudge.Query == "" || nudge.Hint == "" || nudge.Bytes > 512 {
		t.Fatalf("unexpected nudge: %+v", nudge)
	}
	if len(nudge.Matches) == 0 {
		t.Fatalf("expected the seeded lesson in the nudge: %+v", nudge)
	}
}

func TestRunHookFailureNudgeIgnoresSuccess(t *testing.T) {
	st, p, _ := newHookTestStore(t)
	defer st.Close()
	env := &loaders.HookEnvelope{
		Event:     loaders.EventPostToolUse,
		ProjectID: p.ID,
		Payload:   map[string]any{"exit_code": float64(0), "command": "true"},
	}
	ui := render.New(os.Stdout, true)
	if runHookFailureNudge(ui, st, env, true) {
		t.Fatal("successful command should not emit a nudge")
	}
}
