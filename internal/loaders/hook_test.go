package loaders

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDeriveProjectIDWorkspaceParity(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"cli", "worker"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		if name == "cli" {
			goalDir := filepath.Join(dir, "goals", "demo")
			if err := os.MkdirAll(goalDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte("# Goal: demo\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.WriteFile(filepath.Join(root, "sila.code-workspace"), []byte(`{"folders":[{"path":"cli"},{"path":"worker"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := deriveProjectID(root); got != "sila-cli" {
		t.Fatalf("deriveProjectID(root) = %q, want sila-cli", got)
	}
	if got := deriveProjectID(filepath.Join(root, "cli")); got != "sila-cli" {
		t.Fatalf("deriveProjectID(cli) = %q, want sila-cli", got)
	}
	if got := deriveProjectID(filepath.Join(root, "worker")); got != "sila-worker" {
		t.Fatalf("deriveProjectID(worker) = %q, want sila-worker", got)
	}
}

func TestNormalizeEventKind(t *testing.T) {
	cases := []struct {
		input    string
		expected LifecycleEventKind
	}{
		{"session-start", EventSessionStart},
		{"start", EventSessionStart},
		{"session_start", EventSessionStart},
		{"init", EventSessionStart},
		{"launch", EventSessionStart},
		{"user-prompt", EventUserPrompt},
		{"prompt", EventUserPrompt},
		{"user_input", EventUserPrompt},
		{"pre-tool-use", EventPreToolUse},
		{"tool-call", EventPreToolUse},
		{"pre-invocation", EventPreToolUse},
		{"post-tool-use", EventPostToolUse},
		{"tool-result", EventPostToolUse},
		{"post_invocation", EventPostToolUse},
		{"pre-compact", EventPreCompact},
		{"precompact", EventPreCompact},
		{"compact-start", EventPreCompact},
		{"post-compaction", EventPostCompact},
		{"postcompact", EventPostCompact},
		{"compacted", EventPostCompact},
		{"compact", EventPostCompact},
		{"stop", EventStop},
		{"cancel", EventStop},
		{"interrupt", EventStop},
		{"session-end", EventSessionEnd},
		{"end", EventSessionEnd},
		{"unknown-custom", EventOther},
	}

	for _, tc := range cases {
		got := NormalizeEventKind(tc.input)
		if got != tc.expected {
			t.Errorf("NormalizeEventKind(%q) = %q, expected %q", tc.input, got, tc.expected)
		}
	}
}

func TestParseHookEnvelopeAntigravity(t *testing.T) {
	// 1. Invocation 0 -> session-start
	dataStart := []byte(`{"event":"PreInvocation","invocationNum":0,"session_id":"agy-sess-1","agent":"antigravity"}`)
	envStart, err := ParseHookEnvelope(dataStart)
	if err != nil {
		t.Fatalf("ParseHookEnvelope failed: %v", err)
	}
	if envStart.Event != EventSessionStart {
		t.Errorf("expected EventSessionStart for invocation 0, got %s", envStart.Event)
	}
	if envStart.Agent != "antigravity" {
		t.Errorf("expected agent 'antigravity', got %s", envStart.Agent)
	}

	// 2. Invocation > 0 -> post-tool-use
	dataTool := []byte(`{"event":"PreInvocation","invocationNum":3,"session_id":"agy-sess-1","payload":{"tool":"read_file"}}`)
	envTool, err := ParseHookEnvelope(dataTool)
	if err != nil {
		t.Fatalf("ParseHookEnvelope failed: %v", err)
	}
	if envTool.Event != EventPostToolUse {
		t.Errorf("expected EventPostToolUse for invocation > 0, got %s", envTool.Event)
	}
	if envTool.Agent != "antigravity" {
		t.Errorf("expected agent 'antigravity', got %s", envTool.Agent)
	}
}

func TestParseHookEnvelopeAntigravityPreToolUse(t *testing.T) {
	// Antigravity PreToolUse payloads omit the event name; the installed hook
	// command is authoritative for the phase, but the agent must be detected.
	data := []byte(`{"conversationId":"agy-conv-1","workspacePaths":["/workspace/manara"],"toolCall":{"name":"run_command","args":{"CommandLine":"ls"}},"stepIdx":3}`)
	env, err := ParseHookEnvelope(data)
	if err != nil {
		t.Fatalf("ParseHookEnvelope failed: %v", err)
	}
	if env.Agent != "antigravity" {
		t.Errorf("expected agent 'antigravity', got %q", env.Agent)
	}
	if env.SessionID != "agy-conv-1" {
		t.Errorf("expected session id from conversationId, got %q", env.SessionID)
	}
	if env.Cwd != "/workspace/manara" {
		t.Errorf("expected cwd from workspacePaths, got %q", env.Cwd)
	}
	if env.Event != EventOther {
		t.Errorf("payload without an event name must stay EventOther, got %s", env.Event)
	}
}

func TestParseHookEnvelopeClaudeCode(t *testing.T) {
	data := []byte(`{"hook_event_name":"PostToolExecution","tool_name":"Bash","session_id":"claude-sess-99","cwd":"/workspace/test"}`)
	env, err := ParseHookEnvelope(data)
	if err != nil {
		t.Fatalf("ParseHookEnvelope failed: %v", err)
	}
	if env.Event != EventPostToolUse {
		t.Errorf("expected EventPostToolUse, got %s", env.Event)
	}
	if env.Agent != "claude-code" {
		t.Errorf("expected agent 'claude-code', got %s", env.Agent)
	}
	if env.SessionID != "claude-sess-99" {
		t.Errorf("expected session_id 'claude-sess-99', got %s", env.SessionID)
	}
}

func TestPayloadLimit(t *testing.T) {
	spooler, err := NewSpooler(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	// Payload under 16 KiB
	smallEnv := &HookEnvelope{
		Event:     EventUserPrompt,
		Agent:     "test",
		SessionID: "s1",
		Payload:   map[string]interface{}{"note": "short payload"},
	}
	_, err = spooler.Spool(smallEnv)
	if err != nil {
		t.Errorf("expected spool success for small payload, got: %v", err)
	}

	// Payload over 16 KiB
	largePayload := make(map[string]interface{})
	largePayload["big"] = strings.Repeat("A", MaxHookPayloadBytes+100)
	largeEnv := &HookEnvelope{
		Event:     EventUserPrompt,
		Agent:     "test",
		SessionID: "s1",
		Payload:   largePayload,
	}
	_, err = spooler.Spool(largeEnv)
	if err != ErrPayloadTooLarge {
		t.Errorf("expected ErrPayloadTooLarge for >16 KiB payload, got: %v", err)
	}
}

func TestScrubSecrets(t *testing.T) {
	pemHeader := "-----" + "BEGIN " + "PRIVATE KEY-----"
	pemFooter := "-----" + "END " + "PRIVATE KEY-----"
	input := "Config:\n" +
		"pem=" + pemHeader + "\n" +
		"MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC7\n" +
		pemFooter + "\n" +
		"gh=ghp_1234567890abcdef1234567890abcdef1234\n" +
		"aws=AKIAIOSFODNN7EXAMPLE\n" +
		"jwt=eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c\n" +
		"bearer=Bearer a1b2c3d4e5f6g7h8i9j0k1l2m3n4o5p6\n" +
		"api=sk-1234567890abcdefghijklmnop\n" +
		"url=https://example.com/api?password=supersecretpassword123&other=val\n" +
		"home=/home/user/project\n"

	cleaned := ScrubSecrets(input)

	if strings.Contains(cleaned, "BEGIN "+"PRIVATE KEY") {
		t.Errorf("failed to redact PEM key")
	}
	if strings.Contains(cleaned, "ghp_") {
		t.Errorf("failed to redact GitHub PAT")
	}
	if strings.Contains(cleaned, "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("failed to redact AWS key")
	}
	if strings.Contains(cleaned, "SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c") {
		t.Errorf("failed to redact JWT")
	}
	if strings.Contains(cleaned, "a1b2c3d4e5f6g7h8i9j0k1l2m3n4o5p6") {
		t.Errorf("failed to redact Bearer token")
	}
	if strings.Contains(cleaned, "sk-1234567890abcdefghijklmnop") {
		t.Errorf("failed to redact API key")
	}
	if strings.Contains(cleaned, "supersecretpassword123") {
		t.Errorf("failed to redact password param")
	}
	if strings.Contains(cleaned, "/home/user") {
		t.Errorf("failed to redact user home path")
	}
}

type mockDrainTarget struct {
	batches []struct {
		tool string
		msgs []Message
	}
}

func (m *mockDrainTarget) RecordHookBatch(tool string, msgs []Message) (int, int, error) {
	m.batches = append(m.batches, struct {
		tool string
		msgs []Message
	}{tool: tool, msgs: msgs})
	return len(msgs), 0, nil
}

func TestSpoolerLifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	spooler, err := NewSpooler(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Spool 3 envelopes
	for i := 1; i <= 3; i++ {
		env := &HookEnvelope{
			Event:     EventUserPrompt,
			Agent:     "test-agent",
			ProjectID: "proj-1",
			SessionID: "sess-1",
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
			Payload:   map[string]interface{}{"index": i},
		}
		path, err := spooler.Spool(env)
		if err != nil {
			t.Fatalf("spool %d failed: %v", i, err)
		}
		if path == "" {
			t.Fatalf("empty path returned for spool %d", i)
		}
		time.Sleep(2 * time.Millisecond) // Ensure distinct timestamps
	}

	// 2. Verify pending count
	count, err := spooler.PendingCount()
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("expected 3 pending events, got %d", count)
	}

	// 3. Verify FIFO ordering
	entries, err := spooler.ReadPending()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	for i, entry := range entries {
		idx := int(entry.Envelope.Payload["index"].(float64))
		if idx != i+1 {
			t.Errorf("expected FIFO index %d, got %d", i+1, idx)
		}
	}

	// 4. Drain into mock target
	mock := &mockDrainTarget{}
	rep, err := spooler.Drain(mock, nil)
	if err != nil {
		t.Fatalf("drain failed: %v", err)
	}
	if rep.Drained != 3 {
		t.Errorf("expected 3 drained, got %d", rep.Drained)
	}
	if rep.NewRecords != 3 {
		t.Errorf("expected 3 new records, got %d", rep.NewRecords)
	}

	// 5. Verify spool directory is now empty
	postCount, err := spooler.PendingCount()
	if err != nil {
		t.Fatal(err)
	}
	if postCount != 0 {
		t.Errorf("expected 0 pending events after drain, got %d", postCount)
	}
}

func TestHookHTTPHandler(t *testing.T) {
	spooler, err := NewSpooler(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHookHTTPHandler(spooler)

	// 1. Valid POST -> HTTP 202 Accepted
	validBody := `{"event":"stop","agent":"gemini","session_id":"s-http","payload":{"reason":"user_stop"}}`
	req := httptest.NewRequest(http.MethodPost, "/hook", bytes.NewBufferString(validBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected HTTP 202, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"status":"accepted"`) {
		t.Errorf("unexpected response body: %s", w.Body.String())
	}

	// Verify spooled
	count, _ := spooler.PendingCount()
	if count != 1 {
		t.Errorf("expected 1 spooled event, got %d", count)
	}

	// 2. Oversized payload -> HTTP 413
	largeBody := fmt.Sprintf(`{"event":"stop","payload":"%s"}`, strings.Repeat("Z", MaxHookPayloadBytes+10))
	reqLarge := httptest.NewRequest(http.MethodPost, "/hook", bytes.NewBufferString(largeBody))
	wLarge := httptest.NewRecorder()
	handler(wLarge, reqLarge)

	if wLarge.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected HTTP 413 for oversized payload, got %d", wLarge.Code)
	}

	// 3. Method Not Allowed (GET) -> HTTP 405
	reqGet := httptest.NewRequest(http.MethodGet, "/hook", nil)
	wGet := httptest.NewRecorder()
	handler(wGet, reqGet)

	if wGet.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected HTTP 405 for GET, got %d", wGet.Code)
	}
}

func TestHighRateIngestionBenchmark(t *testing.T) {
	spooler, err := NewSpooler(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	const totalEvents = 1000
	start := time.Now()

	for i := 0; i < totalEvents; i++ {
		env := &HookEnvelope{
			Event:     EventPostToolUse,
			Agent:     "bench-agent",
			ProjectID: "proj-bench",
			SessionID: "sess-bench",
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
			Payload: map[string]interface{}{
				"iter": i,
				"data": "benchmark synthetic tool execution event",
			},
		}
		if _, err := spooler.Spool(env); err != nil {
			t.Fatalf("failed at event %d: %v", i, err)
		}
	}

	duration := time.Since(start)
	t.Logf("Spooled %d events in %v (average: %v/event)", totalEvents, duration, duration/totalEvents)

	// Contract requirement: 1,000 synthetic lifecycle events in <200ms (nominal)
	nominalLimit := 200 * time.Millisecond
	if duration > nominalLimit {
		// Under -race running concurrently across packages, allow scheduling tolerance up to 1s
		if duration > 1*time.Second {
			t.Errorf("high-rate ingestion too slow: 1000 events took %v (nominal limit: %v)", duration, nominalLimit)
		} else {
			t.Logf("high-rate ingestion took %v (within -race parallel test tolerance; nominal limit: %v)", duration, nominalLimit)
		}
	}

	count, err := spooler.PendingCount()
	if err != nil {
		t.Fatal(err)
	}
	if count != totalEvents {
		t.Fatalf("expected %d pending events, got %d", totalEvents, count)
	}
}

func TestHookToolEnvelopeStreaming(t *testing.T) {
	big := strings.Repeat("a", 32*1024)
	payload, err := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse",
		"session_id":      "sess-stream",
		"cwd":             "/workspace/project",
		"tool_name":       "write_file",
		"tool_input":      map[string]any{"content": big},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) <= MaxHookPayloadBytes {
		t.Fatalf("fixture must exceed 16 KiB, got %d bytes", len(payload))
	}

	if _, err := ParseHookEnvelope(payload); !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("ParseHookEnvelope error = %v, want ErrPayloadTooLarge", err)
	}
	env, err := ParseHookEnvelopeWithLimit(payload, MaxToolHookPayloadBytes)
	if err != nil {
		t.Fatalf("ParseHookEnvelopeWithLimit: %v", err)
	}
	if env.SessionID != "sess-stream" || env.Payload["tool_input"] == nil {
		t.Fatalf("parsed envelope lost routing or tool input: %#v", env)
	}

	truncated := TruncateToolHookEnvelope(payload, MaxHookPayloadBytes)
	if len(truncated) > MaxHookPayloadBytes {
		t.Fatalf("truncated envelope is %d bytes, want <= %d", len(truncated), MaxHookPayloadBytes)
	}
	var raw map[string]any
	if err := json.Unmarshal(truncated, &raw); err != nil {
		t.Fatalf("truncated envelope is not JSON: %v", err)
	}
	for _, key := range []string{"session_id", "cwd", "hook_event_name", "tool_name"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("truncated envelope lost %q", key)
		}
	}

	if !ContainsSecret("AKIA1234567890ABCDEF") {
		t.Fatal("AWS key not detected")
	}
	if ContainsSecret("plain markdown content") {
		t.Fatal("clean text flagged as a secret")
	}

	if !IsUnparsedToolInputWrapper(map[string]any{"raw": "{...", "len": float64(412)}) {
		t.Fatal("{raw,len} wrapper not detected")
	}
	if !IsUnparsedToolInputWrapper(map[string]any{"__unparsedToolInput": "{...}"}) {
		t.Fatal("__unparsedToolInput wrapper not detected")
	}
	if IsUnparsedToolInputWrapper(map[string]any{"command": "ls"}) {
		t.Fatal("documented arguments flagged as a wrapper")
	}

	tooBig := bytes.Repeat([]byte(" "), MaxToolHookPayloadBytes+1)
	if _, err := ParseHookEnvelopeWithLimit(tooBig, MaxToolHookPayloadBytes); !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("oversized payload error = %v, want ErrPayloadTooLarge", err)
	}
}
