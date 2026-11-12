package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/config"
	"github.com/jadmadi/sila/internal/hookconfig"
	"github.com/jadmadi/sila/internal/loaders"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func newRecallGateTestEnv(t *testing.T, dir, session string) *loaders.HookEnvelope {
	t.Helper()
	return &loaders.HookEnvelope{
		Event: loaders.EventPreToolUse, Agent: "claude", ProjectID: "demo",
		Cwd: dir, SessionID: session, Payload: map[string]any{},
	}
}

func TestRecallStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	state := &RecallGateState{
		V: recallGateStateVersion, Prompts: 2, LastCheckPrompt: 1, Checks: 3,
		Denied: 1, Retried: 1, Bypassed: 0, LastSignature: strings.Repeat("a", 64),
		LastDecision: "retry", LastDecisionAt: recallGateNow(),
	}
	if err := SaveRecallGateState(dir, "session-one", state); err != nil {
		t.Fatal(err)
	}
	path, err := RecallGateStatePath(dir, "session-one")
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("state mode = %o, want 600", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(RecallGateStateDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("state dir mode = %o, want 700", dirInfo.Mode().Perm())
	}
	if leftovers, _ := filepath.Glob(filepath.Join(RecallGateStateDir(dir), ".recall-*")); len(leftovers) != 0 {
		t.Fatalf("temp files left behind: %v", leftovers)
	}
	loaded, err := LoadRecallGateState(dir, "session-one")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Prompts != 2 || loaded.Checks != 3 || loaded.Denied != 1 || loaded.Retried != 1 {
		t.Fatalf("round trip mismatch: %+v", loaded)
	}
	if strings.Contains(path, "session-one") {
		t.Fatalf("session id leaked into the state path: %s", path)
	}
}

func TestRecallStateTTLAndBounds(t *testing.T) {
	dir := t.TempDir()
	path, err := RecallGateStatePath(dir, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureRecallGateStateDir(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	oversized := make([]byte, recallGateStateLimit+1)
	for i := range oversized {
		oversized[i] = 'x'
	}
	if err := os.WriteFile(path, oversized, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRecallGateState(dir, "s1"); err == nil {
		t.Fatal("expected oversized state to be rejected")
	}
	if err := os.WriteFile(path, []byte(`{"v":1,"prompts":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-recallGateStateTTL - time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	state, err := LoadRecallGateState(dir, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if state.Prompts != 0 {
		t.Fatalf("expired state was not reset: %+v", state)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expired state file was not removed: %v", err)
	}
}

func TestRecallStateRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	path, err := RecallGateStatePath(dir, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureRecallGateStateDir(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "target.json")
	if err := os.WriteFile(target, []byte(`{"v":1,"prompts":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err = LoadRecallGateState(dir, "s1")
	if err == nil {
		t.Fatal("expected symlink state to be rejected")
	}
	if got := recallGateErrorCode(err); got != "SYMLINK" {
		t.Fatalf("error code = %q, want SYMLINK", got)
	}
}

func TestDetectRecallInvocationCLI(t *testing.T) {
	cases := []struct {
		command string
		want    bool
	}{
		{`sila recall "hook failure"`, true},
		{`/home/me/.local/bin/sila recall "x"`, true},
		{`cd repo && sila recall "x"`, true},
		{`sila search --omni "x"`, true},
		{`sila search --omni=x`, true},
		{`sila search "x"`, false},
		{`sila prime`, false},
		{`echo sila recall`, false},
		{`sila recall-gate status`, false},
		{`sila do "prime context"`, false},
	}
	for _, tc := range cases {
		got := DetectRecallInvocation("Bash", map[string]any{"tool_input": map[string]any{"command": tc.command}})
		if got != tc.want {
			t.Fatalf("DetectRecallInvocation(%q) = %v, want %v", tc.command, got, tc.want)
		}
	}
}

func TestDetectRecallInvocationMCP(t *testing.T) {
	cases := []struct {
		tool string
		want bool
	}{
		{"recall", true},
		{"omni_search", true},
		{"mcp__sila__recall", true},
		{"mcp__sila__omni_search", true},
		{"sila_recall", true},
		{"sila__recall", true},
		{"sila__omni_search", true},
		{"prime_context", false},
		{"search_messages", false},
		{"sila_prime_context", false},
		{"Edit", false},
	}
	for _, tc := range cases {
		if got := DetectRecallInvocation(tc.tool, nil); got != tc.want {
			t.Fatalf("DetectRecallInvocation(%q) = %v, want %v", tc.tool, got, tc.want)
		}
	}
}

func TestDetectRecallInvocationIgnoresUnrelatedTools(t *testing.T) {
	if DetectRecallInvocation("Grep", map[string]any{"tool_input": map[string]any{"command": `sila recall "x"`}}) {
		t.Fatal("non-shell tools must not be read as CLI recall")
	}
	if DetectRecallInvocation("Bash", map[string]any{"tool_input": map[string]any{"command": "git status"}}) {
		t.Fatal("unrelated shell command counted as recall")
	}
	if DetectRecallInvocation("Bash", map[string]any{"tool_input": `{"command":"sila recall \"json string\""}`}) == false {
		t.Fatal("JSON-encoded tool_input command was not detected")
	}
}

func TestClassifyMutatingTool(t *testing.T) {
	mutating := []string{"Edit", "Write", "MultiEdit", "multi_edit", "NotebookEdit", "notebook_edit"}
	for _, tool := range mutating {
		if !ClassifyMutatingAction(tool, nil) {
			t.Fatalf("%s must be mutating", tool)
		}
	}
	for _, tool := range []string{"Read", "Grep", "Glob", "WebFetch", "WebSearch", "Task", "mcp__sila__recall", "prime_context", "unknown_tool"} {
		if ClassifyMutatingAction(tool, nil) {
			t.Fatalf("%s must not be mutating", tool)
		}
	}
}

func TestClassifyBashCommand(t *testing.T) {
	cases := []struct {
		command string
		want    bool
	}{
		{`git status`, false},
		{`git diff HEAD`, false},
		{`git log --oneline -5`, false},
		{`git branch -a`, false},
		{`git commit -m x`, true},
		{`git branch -D old`, true},
		{`ls -la`, false},
		{`grep -r foo .`, false},
		{`rg foo`, false},
		{`cat file`, false},
		{`head -n 5 file`, false},
		{`tail -f log`, false},
		{`find . -name "*.go"`, false},
		{`find . -delete`, true},
		{`sed -n '1p' file`, false},
		{`sed -i 's/a/b/' file`, true},
		{`go test ./...`, false},
		{`go vet ./...`, false},
		{`go build ./...`, false},
		{`go run ./cmd`, true},
		{`rm -rf build`, true},
		{`echo hi > out.txt`, true},
		{`ls && rm -rf build`, true},
		{`npm test`, false},
		{`npm install`, true},
	}
	for _, tc := range cases {
		got := ClassifyMutatingAction("Bash", map[string]any{"tool_input": map[string]any{"command": tc.command}})
		if got != tc.want {
			t.Fatalf("ClassifyMutatingAction(%q) = %v, want %v", tc.command, got, tc.want)
		}
	}
}

func TestUnknownToolNeverDenied(t *testing.T) {
	state := &RecallGateState{V: 1, Prompts: 1}
	decision := EvaluateRecallGate(state, config.RecallGateEnforce, false, false, "signature")
	if decision.Decision != "allow" || decision.ReasonCode != "READ_ONLY" {
		t.Fatalf("unknown/read-only tool decision = %+v", decision)
	}
}

func TestRecallGateDenyOnce(t *testing.T) {
	state := &RecallGateState{V: 1, Prompts: 1}
	first := EvaluateRecallGate(state, config.RecallGateEnforce, true, false, "sig-a")
	if first.Decision != "deny" || first.ReasonCode != "NO_CHECK" {
		t.Fatalf("first decision = %+v", first)
	}
	if state.Denied != 1 || state.LastSignature != "sig-a" {
		t.Fatalf("deny was not persisted in state: %+v", state)
	}
	if !strings.Contains(first.Reason, "sila recall") || !strings.Contains(first.RetryHint, "retried") {
		t.Fatalf("deny reason/hint missing recovery: %+v", first)
	}
	second := EvaluateRecallGate(state, config.RecallGateEnforce, true, false, "sig-a")
	if second.Decision != "allow" || second.ReasonCode != "RETRY" {
		t.Fatalf("retry decision = %+v", second)
	}
	if state.Retried != 1 {
		t.Fatalf("retry counter = %d", state.Retried)
	}
	third := EvaluateRecallGate(state, config.RecallGateEnforce, true, false, "sig-a")
	if third.Decision != "allow" || third.ReasonCode != "ALLOW" {
		t.Fatalf("repeat retry decision = %+v", third)
	}
	if state.Retried != 1 || state.Denied != 1 {
		t.Fatalf("counters changed on repeated retry: %+v", state)
	}
}

func TestRecallGateRetryPasses(t *testing.T) {
	state := &RecallGateState{V: 1, Prompts: 1}
	if d := EvaluateRecallGate(state, config.RecallGateEnforce, true, false, "sig-b"); d.Decision != "deny" {
		t.Fatalf("expected deny: %+v", d)
	}
	if d := EvaluateRecallGate(state, config.RecallGateEnforce, true, false, "sig-b"); d.Decision != "allow" {
		t.Fatalf("expected retry allow: %+v", d)
	}
}

func TestRecallGateNewPromptRearms(t *testing.T) {
	state := &RecallGateState{V: 1, Prompts: 1}
	EvaluateRecallGate(state, config.RecallGateEnforce, true, false, "sig-c")
	state.Prompts++
	state.LastSignature = ""
	decision := EvaluateRecallGate(state, config.RecallGateEnforce, true, false, "sig-c")
	if decision.Decision != "deny" {
		t.Fatalf("new prompt did not re-arm the gate: %+v", decision)
	}
	if state.Denied != 2 {
		t.Fatalf("denied = %d, want 2", state.Denied)
	}
}

func TestRecallGateWarnOncePerPrompt(t *testing.T) {
	state := &RecallGateState{V: 1, Prompts: 1}
	first := EvaluateRecallGate(state, config.RecallGateWarn, true, false, "sig")
	if first.Decision != "warn" {
		t.Fatalf("first warn = %+v", first)
	}
	second := EvaluateRecallGate(state, config.RecallGateWarn, true, false, "other")
	if second.Decision != "allow" {
		t.Fatalf("warn repeated in the same prompt: %+v", second)
	}
	state.Prompts++
	state.LastSignature = ""
	third := EvaluateRecallGate(state, config.RecallGateWarn, true, false, "sig")
	if third.Decision != "warn" {
		t.Fatalf("new prompt did not re-arm warn: %+v", third)
	}
}

func TestRecallGateFailsOpen(t *testing.T) {
	if d := EvaluateRecallGate(nil, config.RecallGateEnforce, true, false, "sig"); d.Decision != "allow" || !d.FailOpen {
		t.Fatalf("nil state = %+v", d)
	}
	dir := t.TempDir()
	path, err := RecallGateStatePath(dir, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureRecallGateStateDir(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	errOut := captureStderr(t, func() {
		decision := applyRecallGate(newRecallGateTestEnv(t, dir, "s1"), config.RecallGateEnforce)
		if decision.Decision != "allow" || !decision.FailOpen {
			t.Fatalf("corrupt state did not fail open: %+v", decision)
		}
	})
	if !strings.Contains(errOut, "allowing the tool call") {
		t.Fatalf("missing fail-open diagnostic: %q", errOut)
	}
	if readRecallGateFailOpenTotal(dir) != 1 {
		t.Fatalf("fail-open tally = %d, want 1", readRecallGateFailOpenTotal(dir))
	}
	if d := applyRecallGate(newRecallGateTestEnv(t, dir, ""), config.RecallGateEnforce); d.Decision != "allow" || !d.FailOpen {
		t.Fatalf("missing session = %+v", d)
	}
}

func TestRecallGatePrivacySurface(t *testing.T) {
	dir := t.TempDir()
	if err := SaveRecallGateState(dir, "privacy-session", &RecallGateState{V: 1, Prompts: 1}); err != nil {
		t.Fatal(err)
	}
	secret := "sk-very-secret-token-value"
	command := `rm -rf /tmp/project/secret-file && echo ` + secret
	env := newRecallGateTestEnv(t, dir, "privacy-session")
	env.Payload = map[string]any{
		"tool_name": "Write",
		"tool_input": map[string]any{
			"file_path": "/tmp/project/secret-file.go",
			"content":   secret,
			"command":   command,
		},
		"prompt": "do not persist this prompt",
	}
	_ = captureStderr(t, func() {
		applyRecallGate(env, config.RecallGateEnforce)
	})
	path, err := RecallGateStatePath(dir, "privacy-session")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"secret-file", "sk-very-secret", "do not persist", "rm -rf", "/tmp/project"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("state leaked %q: %s", forbidden, data)
		}
	}
	if len(data) > recallGateStateLimit {
		t.Fatalf("state exceeds bound: %d bytes", len(data))
	}
	source, err := os.ReadFile("hook_recall_gate.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{`"net/http"`, `"net"`, "transcript_path"} {
		if strings.Contains(string(source), forbidden) {
			t.Fatalf("gate source contains forbidden import/read: %q", forbidden)
		}
	}
}

func TestRecallGateConcurrentSessions(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		session := "concurrent-" + string(rune('a'+i))
		state := &RecallGateState{V: 1, Prompts: 1}
		if err := SaveRecallGateState(dir, session, state); err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func(session string) {
			defer wg.Done()
			env := newRecallGateTestEnv(t, dir, session)
			env.Payload = map[string]any{"tool_name": "Write", "tool_input": map[string]any{"file_path": "/tmp/" + session + ".go"}}
			_ = applyRecallGate(env, config.RecallGateEnforce)
			if _, err := LoadRecallGateState(dir, session); err != nil {
				errs <- err
			}
		}(session)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent session: %v", err)
	}
	if got := len(sortedRecallGateSessionFiles(RecallGateStateDir(dir))); got != 8 {
		t.Fatalf("session files = %d, want 8", got)
	}
}

func TestRecallGateConcurrentRetry(t *testing.T) {
	dir := t.TempDir()
	session := "concurrent-retry"
	if err := SaveRecallGateState(dir, session, &RecallGateState{V: 1, Prompts: 1}); err != nil {
		t.Fatal(err)
	}
	env := newRecallGateTestEnv(t, dir, session)
	env.Payload = map[string]any{"tool_name": "Write", "tool_input": map[string]any{"file_path": "/tmp/retry.go"}}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var decisions []RecallGateDecision
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			decision := applyRecallGate(env, config.RecallGateEnforce)
			mu.Lock()
			decisions = append(decisions, decision)
			mu.Unlock()
		}()
	}
	wg.Wait()
	state, err := LoadRecallGateState(dir, session)
	if err != nil {
		t.Fatal(err)
	}
	if state.Denied > 10 || state.Retried > 10 {
		t.Fatalf("implausible counters: %+v", state)
	}
	for _, decision := range decisions {
		if decision.FailOpen {
			t.Fatalf("concurrent retry failed open: %+v", decision)
		}
	}
}

func TestRecallGateHarnessDialects(t *testing.T) {
	cases := []struct {
		agent    string
		wantJSON string
	}{
		{"claude", `"permissionDecision":"deny"`},
		{"codex", `"permissionDecision":"deny"`},
		{"command-code", `"permissionDecision":"deny"`},
		{"cursor", `"permission":"deny"`},
		{"antigravity", `"decision":"deny"`},
	}
	for _, tc := range cases {
		env := &loaders.HookEnvelope{Event: loaders.EventPreToolUse, Agent: tc.agent, SessionID: "s", ProjectID: "p"}
		out := captureStdout(t, func() {
			if !emitRecallGateDecision(env, RecallGateDecision{Decision: "deny", ReasonCode: "NO_CHECK", Reason: "recall first"}) {
				t.Fatalf("%s did not emit a decision", tc.agent)
			}
		})
		if !strings.Contains(out, tc.wantJSON) {
			t.Fatalf("%s decision = %q, want %s", tc.agent, out, tc.wantJSON)
		}
	}
}

func TestRecallGateUnknownHarnessFailsOpen(t *testing.T) {
	env := &loaders.HookEnvelope{Event: loaders.EventPreToolUse, Agent: "mystery-host", SessionID: "s", ProjectID: "p"}
	out := captureStdout(t, func() {
		errOut := captureStderr(t, func() {
			if !emitRecallGateDecision(env, RecallGateDecision{Decision: "deny", ReasonCode: "NO_CHECK", Reason: "recall first"}) {
				t.Fatal("unknown harness should be handled")
			}
		})
		if !strings.Contains(errOut, "cannot carry a native decision") {
			t.Fatalf("missing fail-open diagnostic: %q", errOut)
		}
	})
	if strings.TrimSpace(out) != "" {
		t.Fatalf("unknown harness must not write malformed stdout: %q", out)
	}
}

func TestHookCLIPreToolUseRecallGate(t *testing.T) {
	dir := t.TempDir()
	opts := hookOptions{RecallPolicy: "enforce"}
	env := newRecallGateTestEnv(t, dir, "cli-gate")
	recordRecallGatePrompt(env, opts)

	env.Payload = map[string]any{"tool_name": "Write", "tool_input": map[string]any{"file_path": filepath.Join(dir, "main.go"), "content": "package main"}}
	out := captureStdout(t, func() {
		handled := runHookPreToolUse(render.New(os.Stdout, true), nil, env, opts, true)
		if !handled {
			t.Fatal("expected a native deny decision")
		}
	})
	if !strings.Contains(out, `"permissionDecision":"deny"`) {
		t.Fatalf("deny output = %q", out)
	}

	replay := captureStdout(t, func() {
		handled := runHookPreToolUse(render.New(os.Stdout, true), nil, env, opts, true)
		if handled {
			t.Fatal("retry must pass without a native decision")
		}
	})
	if strings.TrimSpace(replay) != "" {
		t.Fatalf("retry wrote a deny decision: %q", replay)
	}

	state, err := LoadRecallGateState(dir, "cli-gate")
	if err != nil {
		t.Fatal(err)
	}
	if state.Denied != 1 || state.Retried != 1 {
		t.Fatalf("state counters = %+v", state)
	}
}

func TestHookCLIPreToolUseReadOnlyPasses(t *testing.T) {
	dir := t.TempDir()
	opts := hookOptions{RecallPolicy: "enforce"}
	env := newRecallGateTestEnv(t, dir, "cli-read")
	recordRecallGatePrompt(env, opts)
	env.Payload = map[string]any{"tool_name": "Read", "tool_input": map[string]any{"file_path": "main.go"}}
	out := captureStdout(t, func() {
		if runHookPreToolUse(render.New(os.Stdout, true), nil, env, opts, true) {
			t.Fatal("read-only tool must not emit a decision")
		}
	})
	if strings.TrimSpace(out) != "" {
		t.Fatalf("read-only tool wrote output: %q", out)
	}
	state, err := LoadRecallGateState(dir, "cli-read")
	if err != nil {
		t.Fatal(err)
	}
	if state.Denied != 0 {
		t.Fatalf("read-only tool denied: %+v", state)
	}
}

func TestHookCLISubagentStartContext(t *testing.T) {
	dir := t.TempDir()
	env := &loaders.HookEnvelope{Event: loaders.EventSubagentStart, Agent: "claude", Cwd: dir, ProjectID: "sub-project"}
	errOut := captureStderr(t, func() {
		writeRecallGateSubagentContext(env, hookOptions{})
	})
	if !strings.Contains(errOut, "sub-project") || !strings.Contains(errOut, "sila recall") || !strings.Contains(errOut, "never held") {
		t.Fatalf("subagent context = %q", errOut)
	}
	state, err := LoadRecallGateState(dir, "")
	if err == nil && state.Denied > 0 {
		t.Fatalf("subagent context recorded a denial: %+v", state)
	}
}

func TestRecallGateBypassAndStatus(t *testing.T) {
	dir := t.TempDir()
	if err := setRecallGateBypass(dir, "bypass-session"); err != nil {
		t.Fatal(err)
	}
	status := BuildRecallGateStatus(dir, config.RecallGateEnforce)
	if status.BypassedTotal != 1 || status.Sessions != 1 || status.Policy != "enforce" {
		t.Fatalf("status = %+v", status)
	}
	state, err := LoadRecallGateState(dir, "bypass-session")
	if err != nil {
		t.Fatal(err)
	}
	decision := EvaluateRecallGate(state, config.RecallGateEnforce, true, false, "sig")
	if decision.Decision != "allow" || decision.ReasonCode != "BYPASS" {
		t.Fatalf("bypass decision = %+v", decision)
	}
}

func TestRecallGatePolicyOffNeverLoadsState(t *testing.T) {
	dir := t.TempDir()
	env := newRecallGateTestEnv(t, dir, "off-session")
	env.Payload = map[string]any{"tool_name": "Write"}
	decision := applyRecallGate(env, config.RecallGateOff)
	if decision.Decision != "allow" || decision.ReasonCode != "OFF" {
		t.Fatalf("off decision = %+v", decision)
	}
	if _, err := os.Stat(RecallGateStateDir(dir)); !os.IsNotExist(err) {
		t.Fatalf("policy off created state: %v", err)
	}
}

func TestRecallGateStateErrorCode(t *testing.T) {
	err := recallGateErr("PARSE", errors.New("bad json"))
	if recallGateErrorCode(err) != "PARSE" {
		t.Fatalf("code = %q", recallGateErrorCode(err))
	}
	if recallGateErrorCode(errors.New("plain")) != "ERROR" {
		t.Fatal("plain error code mismatch")
	}
	var typed *recallGateStateError
	if !errors.As(err, &typed) || typed.Unwrap() == nil {
		t.Fatal("typed error should unwrap")
	}
}

func TestRecallGateFixtureHarnesses(t *testing.T) {
	fixtures, err := filepath.Glob(filepath.Join("testdata", "recall-gate", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(fixtures) == 0 {
		t.Fatal("no recall-gate fixtures found")
	}
	for _, fixture := range fixtures {
		data, err := os.ReadFile(fixture)
		if err != nil {
			t.Fatal(err)
		}
		var spec struct {
			Harness string `json:"harness"`
			Steps   []struct {
				Kind    string         `json:"kind"`
				Payload map[string]any `json:"payload"`
			} `json:"steps"`
		}
		if err := json.Unmarshal(data, &spec); err != nil {
			t.Fatalf("%s: %v", fixture, err)
		}
		dir := t.TempDir()
		env := &loaders.HookEnvelope{Event: loaders.EventPreToolUse, Agent: spec.Harness, Cwd: dir, ProjectID: "fixture", SessionID: "fixture-" + spec.Harness}
		if err := SaveRecallGateState(dir, env.SessionID, &RecallGateState{V: 1, Prompts: 1}); err != nil {
			t.Fatal(err)
		}
		for _, step := range spec.Steps {
			env.Payload = step.Payload
			switch step.Kind {
			case "mutate":
				decision := applyRecallGate(env, config.RecallGateEnforce)
				if decision.Decision != "deny" {
					t.Fatalf("%s: first mutating call = %+v, want deny", spec.Harness, decision)
				}
			case "recall":
				decision := applyRecallGate(env, config.RecallGateEnforce)
				if decision.Decision != "allow" {
					t.Fatalf("%s: recall call = %+v, want allow", spec.Harness, decision)
				}
			case "retry":
				decision := applyRecallGate(env, config.RecallGateEnforce)
				if decision.Decision != "allow" {
					t.Fatalf("%s: retry = %+v, want allow", spec.Harness, decision)
				}
			default:
				t.Fatalf("%s: unknown step %q", spec.Harness, step.Kind)
			}
		}
		status := BuildRecallGateStatus(dir, config.RecallGateEnforce)
		if status.DeniedTotal != 1 {
			t.Fatalf("%s: status = %+v", spec.Harness, status)
		}
	}
}

func TestHookRecallGateStatusJSON(t *testing.T) {
	dir := t.TempDir()
	if err := setRecallGateBypass(dir, "status-session"); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		runHookRecallGate(render.New(os.Stdout, true), "demo", "", dir, true, []string{"status"}, hookOptions{RecallPolicy: "enforce"})
	})
	var status RecallGateStatus
	if err := json.Unmarshal([]byte(out), &status); err != nil {
		t.Fatalf("status JSON: %v\n%s", err, out)
	}
	if status.Policy != "enforce" || status.Sessions != 1 || status.BypassedTotal != 1 {
		t.Fatalf("status = %+v", status)
	}
	if status.StateDir == "" || status.LastDecision == "" || status.LastDecisionAt == "" {
		t.Fatalf("status missing operational fields: %+v", status)
	}
}

func TestHookRecallGateBypassCommand(t *testing.T) {
	dir := t.TempDir()
	out := captureStdout(t, func() {
		runHookRecallGate(render.New(os.Stdout, true), "demo", "bypass-cli", dir, true, []string{"bypass"}, hookOptions{})
	})
	var payload map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &payload); err != nil {
		t.Fatalf("bypass JSON: %v\n%s", err, out)
	}
	if payload["status"] != "bypassed" || payload["session_id"] != "bypass-cli" {
		t.Fatalf("bypass payload = %+v", payload)
	}
	state, err := LoadRecallGateState(dir, "bypass-cli")
	if err != nil {
		t.Fatal(err)
	}
	if state.Bypassed != 1 || state.Checks != 0 {
		t.Fatalf("bypass must be a counter, not a memory check: %+v", state)
	}
}

func TestDoctorRecallGateEnforceWithoutHookWarns(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".sila"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".sila", "config.json"), []byte(`{"recall_gate_policy":"enforce"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	rep := &store.DoctorReport{}
	recallGateDoctorCheck(rep, dir)
	if len(rep.Checks) != 1 || rep.Checks[0].Status != store.StatusWarn {
		t.Fatalf("expected one WARN, got %+v", rep.Checks)
	}
	if !strings.Contains(rep.Checks[0].Message, "no installed harness hook") {
		t.Fatalf("warn message = %q", rep.Checks[0].Message)
	}
}

func TestDoctorRecallGateEnforceWithHookPasses(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".sila"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".sila", "config.json"), []byte(`{"recall_gate_policy":"enforce"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	if _, err := hookconfig.Install("claude", dir, false, false); err != nil {
		t.Fatal(err)
	}
	rep := &store.DoctorReport{}
	recallGateDoctorCheck(rep, dir)
	if len(rep.Checks) != 1 || rep.Checks[0].Status != store.StatusPass {
		t.Fatalf("expected one PASS, got %+v", rep.Checks)
	}
}

func TestRecallGateFailureModes(t *testing.T) {
	t.Run("empty payload", func(t *testing.T) {
		dir := t.TempDir()
		env := newRecallGateTestEnv(t, dir, "empty")
		if err := SaveRecallGateState(dir, "empty", &RecallGateState{V: 1, Prompts: 1}); err != nil {
			t.Fatal(err)
		}
		decision := applyRecallGate(env, config.RecallGateEnforce)
		if decision.Decision != "allow" || decision.FailOpen {
			t.Fatalf("empty payload = %+v", decision)
		}
	})
	t.Run("malformed payload types", func(t *testing.T) {
		dir := t.TempDir()
		env := newRecallGateTestEnv(t, dir, "malformed")
		env.Payload = map[string]any{"tool_name": 42, "tool_input": []any{"x"}, "command": 7}
		if err := SaveRecallGateState(dir, "malformed", &RecallGateState{V: 1, Prompts: 1}); err != nil {
			t.Fatal(err)
		}
		decision := applyRecallGate(env, config.RecallGateEnforce)
		if decision.Decision != "allow" {
			t.Fatalf("malformed payload = %+v", decision)
		}
	})
	t.Run("state directory replaced by file", func(t *testing.T) {
		dir := t.TempDir()
		env := newRecallGateTestEnv(t, dir, "blocked")
		stateDir := RecallGateStateDir(dir)
		if err := os.MkdirAll(filepath.Dir(stateDir), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(stateDir, []byte("not a dir"), 0o600); err != nil {
			t.Fatal(err)
		}
		var decision RecallGateDecision
		errOut := captureStderr(t, func() { decision = applyRecallGate(env, config.RecallGateEnforce) })
		if decision.Decision != "allow" || !decision.FailOpen {
			t.Fatalf("blocked state = %+v", decision)
		}
		if !strings.Contains(errOut, "allowing the tool call") {
			t.Fatalf("missing diagnostic: %q", errOut)
		}
	})
	t.Run("project removed mid-session", func(t *testing.T) {
		parent := t.TempDir()
		dir := filepath.Join(parent, "project")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		env := newRecallGateTestEnv(t, dir, "removed")
		if err := SaveRecallGateState(dir, "removed", &RecallGateState{V: 1, Prompts: 1}); err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dir, []byte("project replaced"), 0o600); err != nil {
			t.Fatal(err)
		}
		decision := applyRecallGate(env, config.RecallGateEnforce)
		if decision.Decision != "allow" || !decision.FailOpen {
			t.Fatalf("removed project = %+v", decision)
		}
	})
	t.Run("oversized payload", func(t *testing.T) {
		dir := t.TempDir()
		env := newRecallGateTestEnv(t, dir, "oversized")
		env.Payload = map[string]any{"tool_name": "Write", "tool_input": map[string]any{
			"file_path": "big.go", "content": strings.Repeat("x", loaders.MaxHookPayloadBytes*2),
		}}
		if err := SaveRecallGateState(dir, "oversized", &RecallGateState{V: 1, Prompts: 1}); err != nil {
			t.Fatal(err)
		}
		decision := applyRecallGate(env, config.RecallGateEnforce)
		if decision.Decision != "deny" {
			t.Fatalf("bounded payload should still classify: %+v", decision)
		}
		path, _ := RecallGateStatePath(dir, "oversized")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "xxxx") {
			t.Fatal("oversized payload content leaked into state")
		}
	})
}

func TestRecallGateMalformedConfigFailsOpen(t *testing.T) {
	dir := t.TempDir()
	env := newRecallGateTestEnv(t, dir, "bad-config")
	t.Setenv("SILA_RECALL_GATE", "loud")
	var handled bool
	errOut := captureStderr(t, func() {
		captureStdout(t, func() {
			handled = runHookPreToolUse(render.New(os.Stdout, true), nil, env, hookOptions{}, true)
		})
	})
	if !strings.Contains(errOut, "using warn") {
		t.Fatalf("missing policy diagnostic: %q", errOut)
	}
	_ = handled
	state, err := LoadRecallGateState(dir, "bad-config")
	if err != nil {
		t.Fatal(err)
	}
	if state.Denied != 0 {
		t.Fatalf("malformed policy must not deny: %+v", state)
	}
}

func TestRecallGateSubagentPayloadNeverDenied(t *testing.T) {
	dir := t.TempDir()
	env := newRecallGateTestEnv(t, dir, "subagent-call")
	env.Payload = map[string]any{
		"tool_name": "Write", "parent_session_id": "parent-1", "subagent_id": "sub-1",
		"tool_input": map[string]any{"file_path": "main.go"},
	}
	if err := SaveRecallGateState(dir, "subagent-call", &RecallGateState{V: 1, Prompts: 1}); err != nil {
		t.Fatal(err)
	}
	decision := applyRecallGate(env, config.RecallGateEnforce)
	if decision.Decision != "allow" || decision.ReasonCode != "SUBAGENT" {
		t.Fatalf("subagent call = %+v", decision)
	}
	state, err := LoadRecallGateState(dir, "subagent-call")
	if err != nil {
		t.Fatal(err)
	}
	if state.Denied != 0 {
		t.Fatalf("subagent call was denied: %+v", state)
	}
}

func TestClassifyMutatingActionQuotedRedirectsAndPipes(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    bool
	}{
		{"git format arrow", `git log --format="%h -> %s"`, false},
		{"ripgrep arrow", `rg "a -> b"`, false},
		{"jq pipe and comparison", `jq '.items[] | select(.n > 0)'`, false},
		{"escaped double quotes", `rg "a \" -> \" b"`, false},
		{"unquoted redirect", `echo "hi" > out.txt`, true},
		{"heredoc with redirect", `cat <<EOF > out.txt`, true},
		{"sed in place", `sed -i 's/a/b/' file`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyMutatingAction("bash", map[string]any{"command": tc.command})
			if got != tc.want {
				t.Fatalf("ClassifyMutatingAction(%q) = %v, want %v", tc.command, got, tc.want)
			}
		})
	}
}
