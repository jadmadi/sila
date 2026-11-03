package registry

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/harness/contract"
)

func TestEncodeDecisionFamilies(t *testing.T) {
	deny := contract.Fail(contract.ReasonGoalUnbound, "no Sila goal binding", "sila goals claim <slug>")
	warn := contract.Decision{
		Schema:     contract.DecisionSchema,
		Decision:   contract.DecisionWarn,
		ReasonCode: contract.ReasonTasksIncomplete,
		Reason:     "0/5 tasks checked",
		Recovery:   "check tasks in plan.md",
	}
	allow := contract.Decision{
		Schema:   contract.DecisionSchema,
		Decision: contract.DecisionAllow,
	}
	unsupported := contract.Decision{
		Schema:     contract.DecisionSchema,
		Decision:   contract.DecisionUnsupported,
		ReasonCode: contract.ReasonHarnessUnsupport,
		Reason:     "unsupported phase",
	}

	// 1. Claude / Command Code pre-tool deny and stop block
	stdout, stderr, code, err := EncodeDecision(OutputClaude, contract.PhaseToolBefore, deny)
	if err != nil || code != 0 || stderr != "" {
		t.Fatalf("claude pre-tool deny: code=%d stderr=%q err=%v", code, stderr, err)
	}
	if !strings.Contains(string(stdout), `"permissionDecision":"deny"`) {
		t.Fatalf("claude pre-tool deny missing permissionDecision: %s", stdout)
	}

	stdout, _, code, _ = EncodeDecision(OutputClaude, contract.PhaseStop, deny)
	if code != 0 || !strings.Contains(string(stdout), `"decision":"block"`) {
		t.Fatalf("claude stop deny: code=%d stdout=%s", code, stdout)
	}

	// 2. Codex exit 2 compatibility on deny
	stdout, _, code, _ = EncodeDecision(OutputCodex, contract.PhaseStop, deny)
	if code != 2 || !strings.Contains(string(stdout), `"decision":"block"`) {
		t.Fatalf("codex stop deny: code=%d stdout=%s", code, stdout)
	}

	// 3. Cursor pre-tool deny vs continue-only stop
	stdout, _, code, _ = EncodeDecision(OutputCursor, contract.PhaseToolBefore, deny)
	if code != 0 || !strings.Contains(string(stdout), `"permission":"deny"`) || !strings.Contains(string(stdout), `"agent_message"`) {
		t.Fatalf("cursor pre-tool deny: %s", stdout)
	}
	stdout, _, code, _ = EncodeDecision(OutputCursor, contract.PhaseStop, deny)
	if code != 0 || strings.Contains(string(stdout), `"deny"`) || strings.Contains(string(stdout), `"block"`) {
		t.Fatalf("cursor stop must never emit deny/block: %s", stdout)
	}
	if !strings.Contains(string(stdout), `"followup_message"`) || !strings.Contains(string(stdout), `"continue-only"`) {
		t.Fatalf("cursor stop missing followup_message / continue-only: %s", stdout)
	}

	// 4. Devin block + exit 2
	stdout, _, code, _ = EncodeDecision(OutputDevin, contract.PhaseStop, deny)
	if code != 2 || !strings.Contains(string(stdout), `"decision":"block"`) {
		t.Fatalf("devin stop deny: code=%d stdout=%s", code, stdout)
	}

	// 5. Antigravity pre-tool deny, stop continue, allow decision, warn reason
	stdout, _, code, _ = EncodeDecision(OutputAntigravity, contract.PhaseToolBefore, deny)
	if code != 0 || !strings.Contains(string(stdout), `"decision":"deny"`) {
		t.Fatalf("antigravity pre-tool deny: %s", stdout)
	}
	stdout, _, code, _ = EncodeDecision(OutputAntigravity, contract.PhaseStop, deny)
	if code != 0 || !strings.Contains(string(stdout), `"decision":"continue"`) {
		t.Fatalf("antigravity stop continue: %s", stdout)
	}
	stdout, _, code, _ = EncodeDecision(OutputAntigravity, contract.PhaseToolBefore, allow)
	if code != 0 || strings.TrimSpace(string(stdout)) != `{"decision":"allow"}` {
		t.Fatalf("antigravity pre-tool allow must emit an explicit decision: %s", stdout)
	}
	stdout, _, code, _ = EncodeDecision(OutputAntigravity, contract.PhaseToolBefore, warn)
	if code != 0 || !strings.Contains(string(stdout), `"decision":"allow"`) || !strings.Contains(string(stdout), `"reason"`) {
		t.Fatalf("antigravity warn must carry allow + reason: %s", stdout)
	}
	stdout, _, code, _ = EncodeDecision(OutputAntigravity, contract.PhaseSessionStart, warn)
	if code != 0 || !strings.Contains(string(stdout), `"injectSteps"`) || !strings.Contains(string(stdout), `"ephemeralMessage"`) {
		t.Fatalf("antigravity session-start warn missing injectSteps: %s", stdout)
	}

	// 6. Codewhale deny and warn
	stdout, _, code, _ = EncodeDecision(OutputCodewhale, contract.PhaseTurnEnd, deny)
	if code != 0 || !strings.Contains(string(stdout), `"decision":"deny"`) {
		t.Fatalf("codewhale turn_end deny: %s", stdout)
	}

	// 7. ZCode pre-tool permissionDecision vs stop block
	stdout, _, code, _ = EncodeDecision(OutputZCode, contract.PhaseToolBefore, deny)
	if !strings.Contains(string(stdout), `"permissionDecision":"deny"`) {
		t.Fatalf("zcode pre-tool deny: %s", stdout)
	}
	stdout, _, code, _ = EncodeDecision(OutputZCode, contract.PhaseStop, deny)
	if !strings.Contains(string(stdout), `"decision":"block"`) {
		t.Fatalf("zcode stop block: %s", stdout)
	}

	// 8. Muse returns no stdout and stderr warning (never invents JSON fields)
	stdout, stderr, code, _ = EncodeDecision(OutputMuse, contract.PhaseStop, deny)
	if len(stdout) != 0 || code != 0 || !strings.Contains(stderr, "unverified") {
		t.Fatalf("muse deny must not emit stdout JSON: stdout=%q stderr=%q code=%d", stdout, stderr, code)
	}

	// 9. Git returns exit 1 with stderr and never writes stdout
	stdout, stderr, code, _ = EncodeDecision(OutputGit, contract.PhaseStop, deny)
	if len(stdout) != 0 || code != 1 || stderr == "" {
		t.Fatalf("git deny must exit 1 with empty stdout: stdout=%q stderr=%q code=%d", stdout, stderr, code)
	}

	// 10. Warn never maps to deny across any family
	for _, st := range []OutputStyle{OutputClaude, OutputCursor, OutputCodex, OutputDevin, OutputCommandCode, OutputCodewhale, OutputAntigravity, OutputZCode, OutputGit} {
		out, _, exitCode, err := EncodeDecision(st, contract.PhaseToolBefore, warn)
		if err != nil || exitCode != 0 {
			t.Errorf("warn for %s returned exit=%d err=%v", st, exitCode, err)
		}
		if strings.Contains(string(out), `"deny"`) || strings.Contains(string(out), `"block"`) {
			t.Errorf("warn for %s mapped to deny/block: %s", st, out)
		}
	}

	// 11. Allow produces empty stdout and exit 0 for shell/git hooks; Antigravity
	// PreToolUse/Stop require an explicit decision field instead.
	for _, st := range []OutputStyle{OutputClaude, OutputCursor, OutputCodex, OutputDevin, OutputCommandCode, OutputCodewhale, OutputZCode, OutputMuse, OutputGit} {
		out, _, exitCode, _ := EncodeDecision(st, contract.PhaseStop, allow)
		if len(out) != 0 || exitCode != 0 {
			t.Errorf("allow for %s produced stdout=%q exit=%d", st, out, exitCode)
		}
	}
	agyAllowStop, _, agyAllowExit, _ := EncodeDecision(OutputAntigravity, contract.PhaseStop, allow)
	if agyAllowExit != 0 || strings.TrimSpace(string(agyAllowStop)) != `{"decision":"allow"}` {
		t.Errorf("antigravity allow stop produced stdout=%q exit=%d", agyAllowStop, agyAllowExit)
	}

	// 12. Unsupported produces empty stdout and stderr message
	out, errMsg, exitCode, _ := EncodeDecision(OutputClaude, contract.PhaseStop, unsupported)
	if len(out) != 0 || exitCode != 0 || !strings.Contains(errMsg, "HARNESS_UNSUPPORTED") {
		t.Fatalf("unsupported encoding: out=%q errMsg=%q exit=%d", out, errMsg, exitCode)
	}

	// Verify JSON validity of all non-empty stdout payloads
	for _, st := range []OutputStyle{OutputClaude, OutputCursor, OutputCodex, OutputDevin, OutputCommandCode, OutputCodewhale, OutputAntigravity, OutputZCode} {
		out, _, _, _ := EncodeDecision(st, contract.PhaseStop, deny)
		var parsed map[string]any
		if err := json.Unmarshal(out, &parsed); err != nil {
			t.Errorf("style %s produced invalid JSON: %v (%s)", st, err, out)
		}
	}
}
