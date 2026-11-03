package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/render"
)

func TestCanaryEnvelopeShapes(t *testing.T) {
	var claude map[string]any
	if err := json.Unmarshal([]byte(canaryEnvelope("claude-code", "/repo", "s1")), &claude); err != nil {
		t.Fatal(err)
	}
	if claude["hook_event_name"] != "SessionStart" || claude["session_id"] != "s1" {
		t.Fatalf("claude envelope = %#v", claude)
	}
	var agy map[string]any
	if err := json.Unmarshal([]byte(canaryEnvelope("antigravity", "/repo", "s2")), &agy); err != nil {
		t.Fatal(err)
	}
	if agy["event"] != "PreInvocation" || agy["invocationNum"] != float64(0) || agy["conversationId"] != "s2" {
		t.Fatalf("antigravity envelope = %#v", agy)
	}
}

func TestPrimeCanaryEndToEnd(t *testing.T) {
	bin, err := filepath.Abs("../../bin/sila")
	if err != nil {
		t.Skip("workspace binary unavailable")
	}
	if _, err := os.Stat(bin); err != nil {
		t.Skip("workspace binary unavailable; run ./build.sh")
	}

	ui := render.New(os.Stdout, true)
	var code int
	out := captureStdout(t, func() {
		code = runPrimeCanary(ui, []string{"--binary=" + bin, "--harness=claude-code", "--keep"}, true)
	})
	if code != 0 {
		t.Fatalf("canary exit = %d\n%s", code, out)
	}
	var rep canaryReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("canary JSON: %v\n%s", err, out)
	}
	if !rep.OK || len(rep.Cases) != 1 || !rep.Cases[0].OK {
		t.Fatalf("canary report = %+v", rep)
	}
	if rep.FailureInjection == nil || !rep.FailureInjection.OK {
		t.Fatalf("failure injection = %+v", rep.FailureInjection)
	}
	if rep.TempRoot != "" {
		_ = os.RemoveAll(rep.TempRoot)
	}
}
