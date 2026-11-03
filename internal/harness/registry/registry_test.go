package registry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/harness/contract"
)

func TestAllSpecsValidAndUnique(t *testing.T) {
	specs := All()
	if len(specs) != 12 {
		t.Fatalf("got %d specs, want 12", len(specs))
	}
	seenID := map[string]bool{}
	seenAlias := map[string]string{}
	for _, sp := range specs {
		if err := sp.Validate(); err != nil {
			t.Errorf("spec %s failed validation: %v", sp.ID, err)
		}
		if seenID[sp.ID] {
			t.Errorf("duplicate spec ID %q", sp.ID)
		}
		seenID[sp.ID] = true
		for _, al := range sp.Aliases {
			if prev, exists := seenAlias[al]; exists {
				t.Errorf("alias %q claimed by both %s and %s", al, prev, sp.ID)
			}
			seenAlias[al] = sp.ID
			got, ok := Lookup(al)
			if !ok || got.ID != sp.ID {
				t.Errorf("Lookup(%q) = %q, %v; want %q", al, got.ID, ok, sp.ID)
			}
		}
		if !strings.HasPrefix(sp.DocsURL, "https://") {
			t.Errorf("spec %s DocsURL %q must start with https://", sp.ID, sp.DocsURL)
		}
		if len(sp.Notes) == 0 {
			t.Errorf("spec %s must document notes/limitations", sp.ID)
		}
	}
}

func TestShellHookSpecsDeclareDialect(t *testing.T) {
	want := map[string]HookDialect{
		"claude":       DialectClaudeNested,
		"cursor":       DialectCursorFlat,
		"codex":        DialectClaudeNested,
		"devin":        DialectClaudeNested,
		"command-code": DialectClaudeNested,
		"codewhale":    DialectCodewhaleTOML,
		"antigravity":  DialectAntigravityNamed,
		"zcode":        DialectClaudeNested,
		"muse":         DialectClaudeNested,
	}
	seen := map[string]bool{}
	for _, sp := range All() {
		if sp.Kind != KindShellHook {
			continue
		}
		seen[sp.ID] = true
		expected, ok := want[sp.ID]
		if !ok {
			t.Errorf("shell-hook harness %s has no dialect expectation", sp.ID)
			continue
		}
		if sp.Config.Dialect != expected {
			t.Errorf("harness %s dialect = %q, want %q", sp.ID, sp.Config.Dialect, expected)
		}
	}
	for id := range want {
		if !seen[id] {
			t.Errorf("missing shell-hook harness %s", id)
		}
	}
}

func TestValidateRejectsInvalidDialect(t *testing.T) {
	sp, ok := Lookup("claude")
	if !ok {
		t.Fatal("missing claude spec")
	}
	sp.Config.Dialect = "claude_flat"
	if err := sp.Validate(); err == nil {
		t.Fatal("Validate must reject unknown hook dialects")
	}
	sp.Config.Dialect = DialectCodewhaleTOML
	if err := sp.Validate(); err == nil {
		t.Fatal("Validate must reject codewhale_toml on a json harness")
	}
	sp.Config.Dialect = DialectCursorFlat
	sp.Config.Format = "toml"
	if err := sp.Validate(); err == nil {
		t.Fatal("Validate must reject cursor_flat with toml format")
	}
}

func TestSpecificHarnessLimitationsRecorded(t *testing.T) {
	cursor, ok := Lookup("cursor")
	if !ok {
		t.Fatal("missing cursor spec")
	}
	if cursor.Events[contract.PhaseStop].StopContinuation != "continue-only" {
		t.Fatalf("cursor stop continuation = %q, want continue-only", cursor.Events[contract.PhaseStop].StopContinuation)
	}
	if cursor.Capabilities[contract.CapStopBlock] {
		t.Fatal("cursor must not claim CapStopBlock")
	}

	zcode, ok := Lookup("zcode")
	if !ok {
		t.Fatal("missing zcode spec")
	}
	if !zcode.Config.ProjectHooksIgnored {
		t.Fatal("zcode must mark ProjectHooksIgnored=true")
	}
	if len(zcode.Config.ProjectPaths) != 0 {
		t.Fatalf("zcode must have zero project paths, got %v", zcode.Config.ProjectPaths)
	}

	cc, ok := Lookup("command-code")
	if !ok {
		t.Fatal("missing command-code spec")
	}
	if cc.Capabilities[contract.CapCompaction] || cc.Capabilities[contract.CapSubagent] {
		t.Fatal("command-code must not claim shell subagent or compaction capabilities")
	}
	if !strings.Contains(strings.Join(cc.Notes, " "), "plan mode") {
		t.Fatal("command-code notes must record plan mode limitation")
	}

	for _, id := range []string{"codex", "codewhale", "muse"} {
		sp, ok := Lookup(id)
		if !ok {
			t.Fatalf("missing %s spec", id)
		}
		if !sp.Config.TrustRequired {
			t.Errorf("%s must require trust review", id)
		}
	}

	antigravity, ok := Lookup("antigravity")
	if !ok {
		t.Fatal("missing antigravity spec")
	}
	if got := antigravity.Events[contract.PhaseUserPrompt].NativeName; got != "PreInvocation" {
		t.Fatalf("antigravity user-prompt event = %q, want PreInvocation", got)
	}
	if got := antigravity.Events[contract.PhaseTurnEnd].NativeName; got != "PostInvocation" {
		t.Fatalf("antigravity turn-end event = %q, want PostInvocation", got)
	}
}

func TestCapabilityMatrixMatchesDocs(t *testing.T) {
	docPath := filepath.Join("..", "..", "..", "docs", "harness-capability-matrix.md")
	data, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read capability matrix doc: %v", err)
	}
	content := string(data)
	for _, sp := range All() {
		if sp.Kind == KindCommitHook {
			continue
		}
		if !strings.Contains(content, sp.DisplayName) {
			t.Errorf("docs/harness-capability-matrix.md missing display name %q", sp.DisplayName)
		}
		if !strings.Contains(content, sp.DocsURL) {
			t.Errorf("docs/harness-capability-matrix.md missing docs URL %q for %s", sp.DocsURL, sp.ID)
		}
		for _, ev := range sp.Events {
			if !strings.Contains(content, ev.NativeName) {
				t.Errorf("docs/harness-capability-matrix.md missing native event %q for %s", ev.NativeName, sp.ID)
			}
		}
	}
	rendered := RenderCapabilityMatrixTable()
	if !strings.Contains(rendered, "Claude Code") || !strings.Contains(rendered, "Continue-only") {
		t.Fatalf("unexpected rendered capability matrix table:\n%s", rendered)
	}
}

func TestHarnessFixturesAndConformance(t *testing.T) {
	if os.Getenv("GENERATE_HARNESS_FIXTURES") == "1" {
		generateFixtures(t)
	}
	fixedTime := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)

	for _, sp := range All() {
		dir := filepath.Join("testdata", sp.ID)
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("harness %s is missing fixture directory %s (adding a harness without fixtures fails CI)", sp.ID, dir)
		}

		// Verify stop.json round-trips to expected-signal.json, expected-decision.json, expected-output.json
		stopBytes, err := os.ReadFile(filepath.Join(dir, "stop.json"))
		if err != nil {
			t.Fatalf("harness %s missing stop.json: %v", sp.ID, err)
		}
		assertNoSecretsOrHomePaths(t, sp.ID+"/stop.json", stopBytes)

		sig, unsupportedDec, err := sp.NormalizePayload(stopBytes, "sila-cli", "/workspace/sila/cli")
		if err != nil {
			t.Fatalf("harness %s stop.json normalize error: %v", sp.ID, err)
		}
		if unsupportedDec != nil {
			t.Fatalf("harness %s stop.json returned unsupported: %+v", sp.ID, unsupportedDec)
		}
		sig.Timestamp = fixedTime

		wantSigBytes, err := os.ReadFile(filepath.Join(dir, "expected-signal.json"))
		if err != nil {
			t.Fatalf("harness %s missing expected-signal.json: %v", sp.ID, err)
		}
		var wantSig contract.Signal
		if err := json.Unmarshal(wantSigBytes, &wantSig); err != nil {
			t.Fatalf("harness %s invalid expected-signal.json: %v", sp.ID, err)
		}
		if sig.Harness != wantSig.Harness || sig.SessionID != wantSig.SessionID || sig.Phase != wantSig.Phase || sig.ProjectID != wantSig.ProjectID {
			t.Errorf("harness %s signal mismatch: got %+v, want %+v", sp.ID, sig, wantSig)
		}

		wantDecBytes, err := os.ReadFile(filepath.Join(dir, "expected-decision.json"))
		if err != nil {
			t.Fatalf("harness %s missing expected-decision.json: %v", sp.ID, err)
		}
		var dec contract.Decision
		if err := json.Unmarshal(wantDecBytes, &dec); err != nil {
			t.Fatalf("harness %s invalid expected-decision.json: %v", sp.ID, err)
		}

		stdout, stderr, exitCode, err := EncodeDecision(sp.Output, contract.PhaseStop, dec)
		if err != nil {
			t.Fatalf("harness %s encode decision failed: %v", sp.ID, err)
		}
		wantOutBytes, err := os.ReadFile(filepath.Join(dir, "expected-output.json"))
		if err != nil {
			t.Fatalf("harness %s missing expected-output.json: %v", sp.ID, err)
		}
		var wantOut struct {
			Stdout   string `json:"stdout"`
			Stderr   string `json:"stderr"`
			ExitCode int    `json:"exit_code"`
		}
		if err := json.Unmarshal(wantOutBytes, &wantOut); err != nil {
			t.Fatalf("harness %s invalid expected-output.json: %v", sp.ID, err)
		}
		if string(stdout) != wantOut.Stdout || stderr != wantOut.Stderr || exitCode != wantOut.ExitCode {
			t.Errorf("harness %s output mismatch:\ngot  stdout=%q stderr=%q exit=%d\nwant stdout=%q stderr=%q exit=%d",
				sp.ID, string(stdout), stderr, exitCode, wantOut.Stdout, wantOut.Stderr, wantOut.ExitCode)
		}

		// Verify additional phase fixtures exist and normalize cleanly
		if _, ok := sp.Events[contract.PhaseSessionStart]; ok {
			checkPhaseFixture(t, sp, filepath.Join(dir, "session-start.json"), contract.PhaseSessionStart)
		}
		if _, ok := sp.Events[contract.PhaseToolBefore]; ok {
			sigTool := checkPhaseFixture(t, sp, filepath.Join(dir, "pre-tool.json"), contract.PhaseToolBefore)
			if strings.Contains(sigTool.ToolShape, "go test") {
				t.Errorf("harness %s tool shape leaked argument value: %q", sp.ID, sigTool.ToolShape)
			}
		}
		if _, ok := sp.Events[contract.PhaseSubagentStart]; ok {
			checkPhaseFixture(t, sp, filepath.Join(dir, "subagent.json"), contract.PhaseSubagentStart)
		}
		if _, ok := sp.Events[contract.PhaseCompactPre]; ok {
			checkPhaseFixture(t, sp, filepath.Join(dir, "compact.json"), contract.PhaseCompactPre)
		} else if _, ok := sp.Events[contract.PhaseCompactPost]; ok {
			checkPhaseFixture(t, sp, filepath.Join(dir, "compact.json"), contract.PhaseCompactPost)
		}
	}
}

func checkPhaseFixture(t *testing.T, sp HarnessSpec, path string, wantPhase contract.Phase) contract.Signal {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("harness %s missing fixture %s: %v", sp.ID, path, err)
	}
	assertNoSecretsOrHomePaths(t, path, raw)
	sig, unsupported, err := sp.NormalizePayload(raw, "sila-cli", "/workspace/sila/cli")
	if err != nil {
		t.Fatalf("harness %s fixture %s error: %v", sp.ID, path, err)
	}
	if unsupported != nil {
		t.Fatalf("harness %s fixture %s returned unsupported: %+v", sp.ID, path, unsupported)
	}
	if sig.Phase != wantPhase {
		t.Fatalf("harness %s fixture %s phase = %q, want %q", sp.ID, path, sig.Phase, wantPhase)
	}
	return sig
}

func assertNoSecretsOrHomePaths(t *testing.T, label string, data []byte) {
	t.Helper()
	s := string(data)
	for _, forbidden := range []string{"/home/", "/Users/", "ghp_", "sk-"} {
		if strings.Contains(s, forbidden) {
			t.Errorf("fixture %s contains forbidden token %q", label, forbidden)
		}
	}
}

func TestHostileAndNegativePayloads(t *testing.T) {
	claude, _ := Lookup("claude")

	// 1. Unknown phase returns HARNESS_UNSUPPORTED
	_, dec, err := claude.NormalizePayload([]byte(`{"hook_event_name":"InventedEvent","session_id":"s1"}`), "proj", "/tmp")
	if err != nil {
		t.Fatalf("unexpected error on unknown phase: %v", err)
	}
	if dec == nil || dec.Decision != contract.DecisionUnsupported || dec.ReasonCode != contract.ReasonHarnessUnsupport {
		t.Fatalf("expected HARNESS_UNSUPPORTED decision, got %+v", dec)
	}

	// 2. Missing session id uses "unbound" for receipt key
	sig, dec, err := claude.NormalizePayload([]byte(`{"hook_event_name":"Stop"}`), "proj", "/tmp")
	if err != nil || dec != nil {
		t.Fatalf("unexpected error/decision on missing session: %v %+v", err, dec)
	}
	if sig.SessionID != "unbound" {
		t.Fatalf("missing session id = %q, want unbound", sig.SessionID)
	}

	// 3. Invalid JSON
	if _, _, err := claude.NormalizePayload([]byte(`{invalid`), "proj", "/tmp"); err == nil {
		t.Fatal("expected error on invalid JSON")
	}

	// 4. Oversized payload (>16 KiB)
	big := `{"hook_event_name":"Stop","padding":"` + strings.Repeat("a", 17*1024) + `"}`
	if _, _, err := claude.NormalizePayload([]byte(big), "proj", "/tmp"); err == nil {
		t.Fatal("expected error on oversized payload")
	}
}

func generateFixtures(t *testing.T) {
	t.Helper()
	fixedTime := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	for _, sp := range All() {
		dir := filepath.Join("testdata", sp.ID)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		type phaseFile struct {
			file  string
			phase contract.Phase
		}
		var phases []phaseFile
		if _, ok := sp.Events[contract.PhaseSessionStart]; ok {
			phases = append(phases, phaseFile{"session-start.json", contract.PhaseSessionStart})
		}
		if _, ok := sp.Events[contract.PhaseToolBefore]; ok {
			phases = append(phases, phaseFile{"pre-tool.json", contract.PhaseToolBefore})
		}
		if _, ok := sp.Events[contract.PhaseStop]; ok {
			phases = append(phases, phaseFile{"stop.json", contract.PhaseStop})
		}
		if _, ok := sp.Events[contract.PhaseSubagentStart]; ok {
			phases = append(phases, phaseFile{"subagent.json", contract.PhaseSubagentStart})
		}
		if _, ok := sp.Events[contract.PhaseCompactPre]; ok {
			phases = append(phases, phaseFile{"compact.json", contract.PhaseCompactPre})
		} else if _, ok := sp.Events[contract.PhaseCompactPost]; ok {
			phases = append(phases, phaseFile{"compact.json", contract.PhaseCompactPost})
		}

		for _, item := range phases {
			ev := sp.Events[item.phase]
			payload := map[string]any{
				"hook_event_name": ev.NativeName,
				"session_id":      "sess-" + sp.ID,
				"project_id":      "sila-cli",
				"cwd":             "/workspace/sila/cli",
			}
			if sp.ID == "antigravity" {
				delete(payload, "hook_event_name")
				delete(payload, "session_id")
				payload["hookEventName"] = ev.NativeName
				payload["conversationId"] = "sess-" + sp.ID
				payload["workspacePaths"] = []string{"/workspace/sila/cli"}
				if item.phase == contract.PhaseToolBefore {
					payload["toolCall"] = map[string]any{
						"name":      "run_command",
						"arguments": map[string]any{"cmd": "go test"},
					}
				}
			} else if item.phase == contract.PhaseToolBefore {
				payload["tool_name"] = "Bash"
				payload["tool_input"] = map[string]any{"command": "go test"}
			}
			if item.phase == contract.PhaseSubagentStart {
				payload["parent_session_id"] = "sess-parent-" + sp.ID
				payload["subagent_id"] = "sub-" + sp.ID
			}
			if item.phase == contract.PhaseCompactPre || item.phase == contract.PhaseCompactPost {
				payload["sequence"] = "1"
			}
			b, _ := json.MarshalIndent(payload, "", "  ")
			if err := os.WriteFile(filepath.Join(dir, item.file), append(b, '\n'), 0o644); err != nil {
				t.Fatal(err)
			}
		}

		stopBytes, err := os.ReadFile(filepath.Join(dir, "stop.json"))
		if err != nil {
			t.Fatal(err)
		}
		sig, _, err := sp.NormalizePayload(stopBytes, "sila-cli", "/workspace/sila/cli")
		if err != nil {
			t.Fatal(err)
		}
		sig.Timestamp = fixedTime
		sigBytes, _ := json.MarshalIndent(sig, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, "expected-signal.json"), append(sigBytes, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}

		dec := contract.Fail(contract.ReasonGoalUnbound, "no Sila goal binding or waiver", "sila goals claim <slug>")
		decBytes, _ := json.MarshalIndent(dec, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, "expected-decision.json"), append(decBytes, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}

		stdout, stderr, exitCode, err := EncodeDecision(sp.Output, contract.PhaseStop, dec)
		if err != nil {
			t.Fatal(err)
		}
		outEnvelope := map[string]any{
			"stdout":    string(stdout),
			"stderr":    stderr,
			"exit_code": exitCode,
		}
		outBytes, _ := json.MarshalIndent(outEnvelope, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, "expected-output.json"), append(outBytes, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
