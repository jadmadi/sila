package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/harness/contract"
	"github.com/jadmadi/sila/internal/loaders"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestHookCLI_SpoolAndDrain(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	dbPath := filepath.Join(tempHome, "test_hook.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	ui := render.New(os.Stdout, true)

	// 1. Check initial empty status
	out := captureStdout(t, func() {
		runHook(ui, dbPath, "test-proj", "", "", "", "", "", false, false, false, 5, []string{"status"})
	})
	if !strings.Contains(out, "Spool queue is empty") {
		t.Errorf("expected empty queue message, got: %s", out)
	}

	// 2. Spool an event via CLI arguments
	out = captureStdout(t, func() {
		runHook(ui, dbPath, "test-proj", "post-tool-use", "cli-agent", "sess-cli-1", tempHome, `{"command":"go build"}`, false, false, false, 5, nil)
	})
	if !strings.Contains(out, "spooled lifecycle event post-tool-use") {
		t.Errorf("expected spooled confirmation, got: %s", out)
	}

	// 3. Verify status shows 1 pending event
	out = captureStdout(t, func() {
		runHook(ui, dbPath, "test-proj", "", "", "", "", "", false, false, false, 5, []string{"status"})
	})
	if !strings.Contains(out, "1 pending spooled lifecycle events") {
		t.Errorf("expected 1 pending event in status, got: %s", out)
	}

	// 4. Drain into store
	out = captureStdout(t, func() {
		runHook(ui, dbPath, "test-proj", "", "", "", "", "", false, false, false, 5, []string{"drain"})
	})
	if !strings.Contains(out, "DRAIN COMPLETE") {
		t.Errorf("expected DRAIN COMPLETE in drain output, got: %s", out)
	}

	// 5. Verify status is now 0 pending
	out = captureStdout(t, func() {
		runHook(ui, dbPath, "test-proj", "", "", "", "", "", false, false, false, 5, []string{"status"})
	})
	if !strings.Contains(out, "Spool queue is empty") {
		t.Errorf("expected empty queue after drain, got: %s", out)
	}

	// 6. Verify record in SQLite store
	var count int
	err = st.DB.QueryRow(`SELECT count(*) FROM source_occurrences WHERE session_id = 'sess-cli-1'`).Scan(&count)
	if err != nil {
		t.Fatalf("query messages count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 message in store for session sess-cli-1, got %d", count)
	}
}

func TestHookCLI_JSONOutput(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	dbPath := filepath.Join(tempHome, "test_hook_json.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	ui := render.New(os.Stdout, true)

	// Pre-compaction is a silent correlation point.
	out := captureStdout(t, func() {
		runHook(ui, dbPath, "test-proj", "pre-compact", "json-agent", "sess-json-1", tempHome, `{"tokens": 12000}`, true, false, true, 5, nil)
	})
	if out != "" {
		t.Errorf("pre-compact must be silent, got: %s", out)
	}

	// A generic lifecycle event still spools with JSON output.
	out = captureStdout(t, func() {
		runHook(ui, dbPath, "test-proj", "user-prompt", "json-agent", "sess-json-1", tempHome, `{"tokens": 12000}`, true, false, true, 5, nil)
	})
	if !strings.Contains(out, `"status"`) || !strings.Contains(out, `"spooled"`) {
		t.Errorf("expected JSON spooled output, got: %s", out)
	}

	out = captureStdout(t, func() {
		runHook(ui, dbPath, "test-proj", "", "", "", "", "", false, false, true, 5, []string{"status"})
	})
	if !strings.Contains(out, `"pending_count":`) {
		t.Errorf("expected JSON status output, got: %s", out)
	}

	out = captureStdout(t, func() {
		runHook(ui, dbPath, "test-proj", "", "", "", "", "", false, false, true, 5, []string{"drain"})
	})
	if !strings.Contains(out, `"total_entries":`) {
		t.Errorf("expected JSON drain output, got: %s", out)
	}
}

func TestHookCLI_PipedStdin(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	dbPath := filepath.Join(tempHome, "test_hook_pipe.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	// Mock stdin using pipe
	origStdin := os.Stdin
	defer func() { os.Stdin = origStdin }()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = r

	go func() {
		defer w.Close()
		_, _ = io.WriteString(w, `{"event":"stop","agent":"pipe-agent","session_id":"sess-pipe-42","payload":{"exit_code":0}}`)
	}()

	ui := render.New(os.Stdout, true)

	out := captureStdout(t, func() {
		runHook(ui, dbPath, "test-proj", "", "", "", "", "", true, false, false, 5, nil)
	})
	if !strings.Contains(out, "Session concluded") && !strings.Contains(out, "handoff:") {
		t.Errorf("expected session-end bridge output from piped stdin, got: %s", out)
	}

	// Verify store has the record
	var count int
	err = st.DB.QueryRow(`SELECT count(*) FROM source_occurrences WHERE session_id = 'sess-pipe-42'`).Scan(&count)
	if err != nil {
		t.Fatalf("query messages count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 message in store for session sess-pipe-42, got %d", count)
	}
}

func TestHookCLI_AntigravityPreToolUseEmitsNativeDecision(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	dbPath := filepath.Join(tempHome, "agy_pretool.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	origStdin := os.Stdin
	defer func() { os.Stdin = origStdin }()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = r
	go func() {
		defer w.Close()
		_, _ = io.WriteString(w, `{"conversationId":"agy-conv-1","workspacePaths":["/workspace/proj"],"toolCall":{"name":"run_command","args":{"CommandLine":"ls"}},"stepIdx":3}`)
	}()

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runHook(ui, dbPath, "", "", "", "", "", "", false, false, true, 5, []string{"pre-tool-use"})
	})

	var parsed map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &parsed); err != nil {
		t.Fatalf("antigravity pre-tool stdout must be valid JSON, got %q: %v", out, err)
	}
	if parsed["decision"] != "allow" {
		t.Fatalf("expected decision allow, got %#v", parsed)
	}
	for _, forbidden := range []string{"status", "agent", "session_id", "project_id", "path", "drained"} {
		if _, ok := parsed[forbidden]; ok {
			t.Fatalf("stdout leaked Sila spool field %q: %#v", forbidden, parsed)
		}
	}
}

func TestEmitAntigravitySessionStart(t *testing.T) {
	out := captureStdout(t, func() { emitAntigravitySessionStart("# Session Brief") })
	var parsed map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v (%s)", err, out)
	}
	steps, ok := parsed["injectSteps"].([]any)
	if !ok || len(steps) != 1 {
		t.Fatalf("expected one injectStep, got %#v", parsed)
	}
	step := steps[0].(map[string]any)
	msg, ok := step["systemMessage"].(map[string]any)
	if !ok || msg["systemMessage"] != "# Session Brief" {
		t.Fatalf("systemMessage must be a nested object, got %#v", step)
	}
}

func TestHookCLI_PipedUnknownEventKeepsStdoutClean(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	dbPath := filepath.Join(tempHome, "unknown_event.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	origStdin := os.Stdin
	defer func() { os.Stdin = origStdin }()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = r
	go func() {
		defer w.Close()
		_, _ = io.WriteString(w, `{"conversationId":"agy-conv-2","workspacePaths":["/workspace/proj"]}`)
	}()

	origStderr := os.Stderr
	rErr, wErr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = wErr

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runHook(ui, dbPath, "", "", "", "", "", "", false, false, true, 5, []string{"unknown-phase"})
	})
	_ = wErr.Close()
	os.Stderr = origStderr
	stderrOut, _ := io.ReadAll(rErr)

	if strings.TrimSpace(out) != "" {
		t.Fatalf("harness-fed unknown events must not write stdout, got %q", out)
	}
	if !strings.Contains(string(stderrOut), "spooled") {
		t.Fatalf("expected spool summary on stderr, got %q", stderrOut)
	}
}

func TestGoalContractGateDeniesUnboundStop(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SILA_GOAL_CONTRACT", "enforce")
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(t.TempDir(), "contract.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	env := &loaders.HookEnvelope{
		Event: loaders.EventStop, Agent: "claude", SessionID: "sess-unbound",
		ProjectID: "proj", Cwd: dir,
		Payload: map[string]interface{}{},
	}
	var handled bool
	out := captureStdout(t, func() {
		handled = runGoalContractGate(render.New(os.Stdout, true), st, env, dir, true)
	})
	if !handled {
		t.Fatal("expected contract gate to handle the stop")
	}
	if !strings.Contains(out, `"decision":"block"`) {
		t.Fatalf("expected blocking output, got %s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, ".sila", "state", "goal-contract", "claude-sess-unbound.json")); err != nil {
		t.Fatalf("expected receipt: %v", err)
	}
}

func TestContractStopEncodingFamilies(t *testing.T) {
	d := contract.Decision{Decision: contract.DecisionDeny, ReasonCode: contract.ReasonGoalUnbound, Reason: "unbound"}
	cases := []struct{ harness, want string }{
		{"claude", `"decision":"block"`},
		{"cursor", `"followup_message"`},
		{"antigravity", `"decision":"continue"`},
	}
	for _, tc := range cases {
		out := captureStdout(t, func() { writeContractStopDecision(tc.harness, d) })
		if !strings.Contains(out, tc.want) {
			t.Fatalf("%s: expected %s, got %s", tc.harness, tc.want, out)
		}
	}
}
