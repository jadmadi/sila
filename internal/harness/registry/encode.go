package registry

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jadmadi/sila/internal/harness/contract"
)

// EncodeDecision encodes a core contract.Decision into the native output format
// and exit code of the given OutputStyle and Phase.
// It returns (stdout, stderr, exitCode, error).
// Rules:
// - warn always maps to allow + one bounded context/system message; never deny.
// - unsupported maps to no stdout and a bounded stderr warning.
// - git never writes stdout; deny exits 1 with stderr reason.
// - cursor stop uses followup_message and is continue-only (exit 0).
// - muse blocking decisions return unsupported rather than inventing JSON fields.
func EncodeDecision(style OutputStyle, phase contract.Phase, d contract.Decision) ([]byte, string, int, error) {
	msg := formatDecisionMessage(d)

	if d.Decision == contract.DecisionUnsupported {
		return nil, msg, 0, nil
	}

	switch style {
	case OutputGit:
		if d.Decision == contract.DecisionDeny {
			return nil, msg, 1, nil
		}
		if d.Decision == contract.DecisionWarn && msg != "" {
			return nil, msg, 0, nil
		}
		return nil, "", 0, nil

	case OutputMuse:
		// Exact JSON fields for Muse blocking are unverified; fail honest with unsupported.
		if d.Decision == contract.DecisionDeny || d.Decision == contract.DecisionWarn {
			return nil, "muse hook output schema is unverified; run `sila gate` for enforcement (" + msg + ")", 0, nil
		}
		return nil, "", 0, nil

	case OutputCursor:
		return encodeCursor(phase, d, msg)

	case OutputAntigravity:
		return encodeAntigravity(phase, d, msg)

	case OutputDevin:
		return encodeDevin(phase, d, msg)

	case OutputCodewhale:
		return encodeCodewhale(phase, d, msg)

	case OutputZCode:
		return encodeZCode(phase, d, msg)

	case OutputClaude, OutputCodex, OutputCommandCode:
		return encodeClaudeFamily(style, phase, d, msg)

	case OutputPlugin:
		b, err := json.Marshal(d)
		if err != nil {
			return nil, "", 1, err
		}
		return append(b, '\n'), "", 0, nil

	default:
		return nil, fmt.Sprintf("unsupported output style %q", style), 0, nil
	}
}

func formatDecisionMessage(d contract.Decision) string {
	base := strings.TrimSpace(contract.Summary(d))
	if d.Recovery != "" && !strings.Contains(base, d.Recovery) {
		base += " | recovery: " + strings.TrimSpace(d.Recovery)
	}
	if len(base) > 512 {
		base = base[:512]
	}
	return base
}

func encodeClaudeFamily(style OutputStyle, phase contract.Phase, d contract.Decision, msg string) ([]byte, string, int, error) {
	switch d.Decision {
	case contract.DecisionDeny:
		if phase == contract.PhaseToolBefore {
			out := map[string]any{
				"hookSpecificOutput": map[string]any{
					"hookEventName":            "PreToolUse",
					"permissionDecision":       "deny",
					"permissionDecisionReason": msg,
				},
			}
			b, err := json.Marshal(out)
			exitCode := 0
			if style == OutputCodex {
				exitCode = 2
			}
			return append(b, '\n'), "", exitCode, err
		}
		out := map[string]any{
			"decision": "block",
			"reason":   msg,
		}
		b, err := json.Marshal(out)
		exitCode := 0
		if style == OutputCodex {
			exitCode = 2
		}
		return append(b, '\n'), "", exitCode, err

	case contract.DecisionWarn:
		if phase == contract.PhaseToolBefore {
			out := map[string]any{
				"hookSpecificOutput": map[string]any{
					"hookEventName":      "PreToolUse",
					"permissionDecision": "allow",
					"additionalContext":  msg,
				},
				"systemMessage": msg,
			}
			b, err := json.Marshal(out)
			return append(b, '\n'), "", 0, err
		}
		out := map[string]any{
			"hookSpecificOutput": map[string]any{
				"additionalContext": msg,
			},
			"systemMessage": msg,
		}
		b, err := json.Marshal(out)
		return append(b, '\n'), "", 0, err

	default:
		return nil, "", 0, nil
	}
}

func encodeCursor(phase contract.Phase, d contract.Decision, msg string) ([]byte, string, int, error) {
	switch d.Decision {
	case contract.DecisionDeny:
		if phase == contract.PhaseStop || phase == contract.PhaseTurnEnd {
			// Cursor stop is continue-only; never emit a hard deny at stop.
			out := map[string]any{
				"followup_message":  msg,
				"stop_continuation": "continue-only",
			}
			b, err := json.Marshal(out)
			return append(b, '\n'), "", 0, err
		}
		out := map[string]any{
			"permission":    "deny",
			"agent_message": msg,
		}
		b, err := json.Marshal(out)
		return append(b, '\n'), "", 0, err

	case contract.DecisionWarn:
		if phase == contract.PhaseStop || phase == contract.PhaseTurnEnd {
			out := map[string]any{
				"followup_message":  msg,
				"stop_continuation": "continue-only",
			}
			b, err := json.Marshal(out)
			return append(b, '\n'), "", 0, err
		}
		out := map[string]any{
			"permission":         "allow",
			"additional_context": msg,
		}
		b, err := json.Marshal(out)
		return append(b, '\n'), "", 0, err

	default:
		return nil, "", 0, nil
	}
}

func encodeDevin(phase contract.Phase, d contract.Decision, msg string) ([]byte, string, int, error) {
	switch d.Decision {
	case contract.DecisionDeny:
		out := map[string]any{
			"decision": "block",
			"reason":   msg,
		}
		b, err := json.Marshal(out)
		return append(b, '\n'), "", 2, err

	case contract.DecisionWarn:
		out := map[string]any{
			"hookSpecificOutput": map[string]any{
				"additionalContext": msg,
			},
		}
		b, err := json.Marshal(out)
		return append(b, '\n'), "", 0, err

	default:
		return nil, "", 0, nil
	}
}

func encodeAntigravity(phase contract.Phase, d contract.Decision, msg string) ([]byte, string, int, error) {
	switch d.Decision {
	case contract.DecisionAllow:
		// Antigravity's PreToolUse and Stop contracts require an explicit
		// decision field; other events accept an empty stdout as no-op.
		switch phase {
		case contract.PhaseToolBefore, contract.PhaseStop, contract.PhaseTurnEnd:
			return marshalHookJSON(map[string]any{"decision": "allow"})
		}
		return nil, "", 0, nil

	case contract.DecisionDeny:
		if phase == contract.PhaseStop || phase == contract.PhaseTurnEnd {
			return marshalHookJSON(map[string]any{
				"decision": "continue",
				"reason":   msg,
			})
		}
		if phase == contract.PhaseToolAfter {
			// PostToolUse expects an empty JSON object and has no deny semantics.
			return nil, "", 0, nil
		}
		return marshalHookJSON(map[string]any{
			"decision": "deny",
			"reason":   msg,
		})

	case contract.DecisionWarn:
		switch phase {
		case contract.PhaseToolBefore, contract.PhaseStop, contract.PhaseTurnEnd:
			return marshalHookJSON(map[string]any{
				"decision": "allow",
				"reason":   msg,
			})
		case contract.PhaseToolAfter:
			return nil, "", 0, nil
		default:
			// SessionStart/PreInvocation/PostInvocation accept injectSteps.
			// ephemeralMessage is a plain string in the documented schema.
			return marshalHookJSON(map[string]any{
				"injectSteps": []any{
					map[string]any{"ephemeralMessage": msg},
				},
				"terminationBehavior": "",
			})
		}

	default:
		return nil, "", 0, nil
	}
}

func marshalHookJSON(payload map[string]any) ([]byte, string, int, error) {
	b, err := json.Marshal(payload)
	return append(b, '\n'), "", 0, err
}

func encodeCodewhale(phase contract.Phase, d contract.Decision, msg string) ([]byte, string, int, error) {
	switch d.Decision {
	case contract.DecisionDeny:
		out := map[string]any{
			"decision": "deny",
			"reason":   msg,
		}
		b, err := json.Marshal(out)
		return append(b, '\n'), "", 0, err

	case contract.DecisionWarn:
		out := map[string]any{
			"additionalContext": msg,
		}
		b, err := json.Marshal(out)
		return append(b, '\n'), "", 0, err

	default:
		return nil, "", 0, nil
	}
}

func encodeZCode(phase contract.Phase, d contract.Decision, msg string) ([]byte, string, int, error) {
	switch d.Decision {
	case contract.DecisionDeny:
		if phase == contract.PhaseToolBefore {
			out := map[string]any{
				"permissionDecision":       "deny",
				"permissionDecisionReason": msg,
			}
			b, err := json.Marshal(out)
			return append(b, '\n'), "", 0, err
		}
		out := map[string]any{
			"decision": "block",
			"reason":   msg,
		}
		b, err := json.Marshal(out)
		return append(b, '\n'), "", 0, err

	case contract.DecisionWarn:
		out := map[string]any{
			"additionalContext": msg,
		}
		b, err := json.Marshal(out)
		return append(b, '\n'), "", 0, err

	default:
		return nil, "", 0, nil
	}
}
