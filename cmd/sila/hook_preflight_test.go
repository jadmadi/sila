package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/loaders"
)

func TestPreflightGateDeniesCommandCodeWithoutMarker(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".sila"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".sila", "config"), []byte("[command_code]\npreflight_policy = \"enforce\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := &loaders.HookEnvelope{
		Event: loaders.EventPreToolUse, Agent: "command-code", SessionID: "s1", Cwd: dir,
		Payload: map[string]interface{}{"tool_name": "Read", "tool_input": `{"path":"x"}`},
	}
	out := captureStdout(t, func() {
		if !runPreflightGate(env, dir) {
			t.Error("gate did not handle the deny decision")
		}
	})
	var payload struct {
		Decision string `json:"permissionDecision"`
		Reason   string `json:"permissionDecisionReason"`
		Recovery string `json:"recovery_command"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("invalid decision JSON: %v\n%s", err, out)
	}
	if payload.Decision != "deny" || !strings.Contains(payload.Reason, "session-start") || payload.Recovery != "sila hook session-start" {
		t.Fatalf("bad deny payload: %+v", payload)
	}
}

func TestPreflightGateWarnsOnceAndAllowsMCP(t *testing.T) {
	dir := t.TempDir()
	env := &loaders.HookEnvelope{
		Event: loaders.EventPreToolUse, Agent: "command-code", SessionID: "s1", Cwd: dir,
		Payload: map[string]interface{}{"tool_name": "Read", "tool_input": `{"path":"x"}`},
	}
	first := captureStdout(t, func() { runPreflightGate(env, dir) })
	if first == "" || !strings.Contains(first, `"permissionDecision":"allow"`) || !strings.Contains(first, "systemMessage") {
		t.Fatalf("warn output = %q", first)
	}
	second := captureStdout(t, func() { runPreflightGate(env, dir) })
	if second != "" {
		t.Fatalf("warning was not deduped: %q", second)
	}
	mcp := &loaders.HookEnvelope{
		Event: loaders.EventPreToolUse, Agent: "command-code", SessionID: "s1", Cwd: dir,
		Payload: map[string]interface{}{"tool_name": "mcp__sila__prime_context", "tool_input": `{}`},
	}
	if out := captureStdout(t, func() { runPreflightGate(mcp, dir) }); out != "" {
		t.Fatalf("MCP allow emitted output: %q", out)
	}
}

func TestPreflightMarkerAllowsLaterToolCalls(t *testing.T) {
	dir := t.TempDir()
	writePreflightMarkerForSession(dir, "proj", "s1", loaders.PrimeReceipt{
		Status: loaders.PrimeStatusOK,
		Source: loaders.PrimeSourceSessionStart,
	})
	info, err := os.Stat(loaders.PreflightMarkerPath(dir, "s1"))
	if err != nil {
		t.Fatalf("marker missing: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("marker mode = %o", info.Mode().Perm())
	}
	if err := os.WriteFile(filepath.Join(dir, ".sila", "config"), []byte("[command_code]\npreflight_policy = \"enforce\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := &loaders.HookEnvelope{
		Event: loaders.EventPreToolUse, Agent: "command-code", SessionID: "s1", Cwd: dir,
		Payload: map[string]interface{}{"tool_name": "Read", "tool_input": `{"path":"x"}`},
	}
	if out := captureStdout(t, func() { runPreflightGate(env, dir) }); out != "" {
		t.Fatalf("valid marker emitted output: %q", out)
	}
}

func TestPreflightGateRejectsShellInjection(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".sila"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".sila", "config"), []byte("[command_code]\npreflight_policy = \"enforce\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := &loaders.HookEnvelope{
		Event: loaders.EventPreToolUse, Agent: "command-code", SessionID: "s1", Cwd: dir,
		Payload: map[string]interface{}{"tool_name": "Shell", "tool_input": `{"command":"sila prime; rm -rf /"}`},
	}
	out := captureStdout(t, func() { runPreflightGate(env, dir) })
	if !strings.Contains(out, `"permissionDecision":"deny"`) {
		t.Fatalf("injection was not denied: %q", out)
	}
}

func TestPreflightPlanModeIsDocumentedAllow(t *testing.T) {
	dir := t.TempDir()
	env := &loaders.HookEnvelope{
		Event: loaders.EventPreToolUse, Agent: "command-code", SessionID: "s1", Cwd: dir,
		Payload: map[string]interface{}{"tool_name": "Read", "tool_input": `{"path":"x"}`, "permission_mode": "plan"},
	}
	out := captureStdout(t, func() { runPreflightGate(env, dir) })
	if !strings.Contains(out, "plan mode") {
		t.Fatalf("plan-mode warning missing: %q", out)
	}
}

func TestPreflightMarkerRecordsPrimeReceipt(t *testing.T) {
	dir := t.TempDir()
	failed := loaders.PrimeReceipt{
		Status:     loaders.PrimeStatusFailed,
		ErrorClass: "brief_build",
		Error:      "boom",
		DurationMS: 12,
		Source:     loaders.PrimeSourceSessionStart,
	}
	writePreflightMarkerForSession(dir, "proj", "s1", failed)
	marker, err := loaders.ReadPreflightMarker(loaders.PreflightMarkerPath(dir, "s1"))
	if err != nil {
		t.Fatal(err)
	}
	if marker.Status != "failed" || marker.PrimeStatus != loaders.PrimeStatusFailed {
		t.Fatalf("failed marker = %+v", marker)
	}
	if marker.Checks["prime"] || marker.PrimeErrorClass != "brief_build" || marker.PrimeError != "boom" {
		t.Fatalf("failed prime fields = %+v", marker)
	}
	if marker.PrimeDurationMS != 12 || marker.PrimeSource != loaders.PrimeSourceSessionStart {
		t.Fatalf("failed receipt fields = %+v", marker)
	}

	ok := loaders.PrimeReceipt{
		Status:     loaders.PrimeStatusOK,
		BriefBytes: 42,
		Source:     loaders.PrimeSourceSessionStart,
	}
	writePreflightMarkerForSession(dir, "proj", "s2", ok)
	marker, err = loaders.ReadPreflightMarker(loaders.PreflightMarkerPath(dir, "s2"))
	if err != nil {
		t.Fatal(err)
	}
	if marker.Status != "ok" || marker.PrimeStatus != loaders.PrimeStatusOK || !marker.Checks["prime"] {
		t.Fatalf("ok marker = %+v", marker)
	}
	if marker.BriefBytes != 42 {
		t.Fatalf("brief bytes = %d", marker.BriefBytes)
	}
}

func TestPrimeExitCode(t *testing.T) {
	ok := loaders.PrimeReceipt{Status: loaders.PrimeStatusOK}
	failed := loaders.PrimeReceipt{Status: loaders.PrimeStatusFailed}
	if got := primeExitCode(ok, true); got != 0 {
		t.Fatalf("ok strict exit = %d", got)
	}
	if got := primeExitCode(failed, false); got != 0 {
		t.Fatalf("failed fail-open exit = %d", got)
	}
	if got := primeExitCode(failed, true); got != 3 {
		t.Fatalf("failed strict exit = %d", got)
	}
}

func TestHookStrictEnv(t *testing.T) {
	t.Setenv("SILA_HOOK_STRICT", "1")
	if !hookStrict(hookOptions{}) {
		t.Fatal("SILA_HOOK_STRICT=1 was ignored")
	}
	t.Setenv("SILA_HOOK_STRICT", "0")
	if hookStrict(hookOptions{}) {
		t.Fatal("SILA_HOOK_STRICT=0 enabled strict mode")
	}
	if !hookStrict(hookOptions{Strict: true}) {
		t.Fatal("--strict was ignored")
	}
}

func TestSanitizePrimeErrorRedactsHomeAndProject(t *testing.T) {
	home, _ := os.UserHomeDir()
	project := t.TempDir()
	got := sanitizePrimeError(home+"/secret "+project+"/x", project)
	if strings.Contains(got, home) || strings.Contains(got, project) {
		t.Fatalf("unredacted error: %q", got)
	}
	if len(got) > 512 {
		t.Fatalf("error not truncated: %d", len(got))
	}
}
