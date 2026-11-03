package registry

import (
	"fmt"
	"strings"

	"github.com/jadmadi/sila/internal/harness/contract"
)

// Kind describes how a harness integrates with Sila.
type Kind string

const (
	KindShellHook  Kind = "shell_hook"
	KindPluginHook Kind = "plugin_hook"
	KindCommitHook Kind = "commit_hook"
)

// OutputStyle identifies the response JSON/exit-code family of a harness.
type OutputStyle string

const (
	OutputClaude      OutputStyle = "claude"
	OutputCursor      OutputStyle = "cursor"
	OutputCodex       OutputStyle = "codex"
	OutputDevin       OutputStyle = "devin"
	OutputCommandCode OutputStyle = "commandcode"
	OutputCodewhale   OutputStyle = "codewhale"
	OutputAntigravity OutputStyle = "antigravity"
	OutputZCode       OutputStyle = "zcode"
	OutputMuse        OutputStyle = "muse"
	OutputGit         OutputStyle = "git"
	OutputPlugin      OutputStyle = "plugin"
)

// HookDialect identifies the native on-disk hook configuration schema of a harness.
type HookDialect string

const (
	// DialectClaudeNested writes {"hooks": {"<Event>": [{"matcher": ..., "hooks": [...]}]}}.
	DialectClaudeNested HookDialect = "claude_nested"
	// DialectCursorFlat writes {"version": 1, "hooks": {"<event>": [{"command": ..., "timeout": ...}]}}.
	DialectCursorFlat HookDialect = "cursor_flat"
	// DialectAntigravityNamed writes a named top-level hook group with flat and matcher-grouped events.
	DialectAntigravityNamed HookDialect = "antigravity_named"
	// DialectCodewhaleTOML writes [[hooks]] TOML blocks between markers.
	DialectCodewhaleTOML HookDialect = "codewhale_toml"
)

// ConfigSpec describes where and how a harness reads hook/plugin configuration.
type ConfigSpec struct {
	ProjectPaths        []string    `json:"project_paths,omitempty"`
	UserPaths           []string    `json:"user_paths,omitempty"`
	Format              string      `json:"format"`
	Dialect             HookDialect `json:"dialect,omitempty"`
	TrustRequired       bool        `json:"trust_required"`
	ProjectHooksIgnored bool        `json:"project_hooks_ignored"`
	AtomicWrite         bool        `json:"atomic_write"`
	PreserveForeign     bool        `json:"preserve_foreign"`
}

// EventSpec describes one canonical phase mapped to a native harness event.
type EventSpec struct {
	NativeName       string `json:"native_name"`
	Matcher          string `json:"matcher,omitempty"`
	Command          string `json:"command,omitempty"`
	Blocking         bool   `json:"blocking"`
	ContextInjection bool   `json:"context_injection"`
	StopContinuation string `json:"stop_continuation,omitempty"` // hard | continue-only | none
}

// PayloadSpec lists ordered candidate field names for payload normalization.
type PayloadSpec struct {
	SessionIDFields      []string `json:"session_id_fields,omitempty"`
	ParentSessionFields  []string `json:"parent_session_fields,omitempty"`
	ProjectFields        []string `json:"project_fields,omitempty"`
	CwdFields            []string `json:"cwd_fields,omitempty"`
	PhaseFields          []string `json:"phase_fields,omitempty"`
	HarnessGoalFields    []string `json:"harness_goal_fields,omitempty"`
	ToolNameFields       []string `json:"tool_name_fields,omitempty"`
	ToolInputFields      []string `json:"tool_input_fields,omitempty"`
	PermissionModeFields []string `json:"permission_mode_fields,omitempty"`
	SubagentIDFields     []string `json:"subagent_id_fields,omitempty"`
	CompactionFields     []string `json:"compaction_fields,omitempty"`
}

// HarnessSpec is the declarative specification of one supported harness.
type HarnessSpec struct {
	ID           string                       `json:"id"`
	DisplayName  string                       `json:"display_name"`
	Aliases      []string                     `json:"aliases,omitempty"`
	Kind         Kind                         `json:"kind"`
	Output       OutputStyle                  `json:"output"`
	Config       ConfigSpec                   `json:"config"`
	Events       map[contract.Phase]EventSpec `json:"events"`
	ExtraEvents  []string                     `json:"extra_events,omitempty"`
	Capabilities map[contract.Capability]bool `json:"capabilities"`
	Payload      PayloadSpec                  `json:"payload"`
	Trust        string                       `json:"trust"` // none | workspace | review-digest | project-trust
	DocsURL      string                       `json:"docs_url"`
	Notes        []string                     `json:"notes,omitempty"`
}

// PluginBridge Capabilities exposed in status and specs.
const (
	CapPluginBridge contract.Capability = "plugin_bridge"
	CapShellHook    contract.Capability = "shell_hook"
)

// Validate checks internal consistency of a HarnessSpec.
func (s HarnessSpec) Validate() error {
	if strings.TrimSpace(s.ID) == "" {
		return fmt.Errorf("harness spec ID is required")
	}
	if strings.TrimSpace(s.DisplayName) == "" {
		return fmt.Errorf("harness %s: display name is required", s.ID)
	}
	if strings.TrimSpace(s.DocsURL) == "" {
		return fmt.Errorf("harness %s: docs URL is required", s.ID)
	}
	switch s.Kind {
	case KindShellHook, KindPluginHook, KindCommitHook:
	default:
		return fmt.Errorf("harness %s: invalid kind %q", s.ID, s.Kind)
	}
	if s.Kind == KindShellHook {
		switch s.Config.Dialect {
		case DialectClaudeNested, DialectCursorFlat, DialectAntigravityNamed, DialectCodewhaleTOML:
		default:
			return fmt.Errorf("harness %s: invalid hook dialect %q", s.ID, s.Config.Dialect)
		}
		if (s.Config.Dialect == DialectCodewhaleTOML) != (s.Config.Format == "toml") {
			return fmt.Errorf("harness %s: dialect %q requires format toml and no other dialect may use it", s.ID, s.Config.Dialect)
		}
	}
	for phase, ev := range s.Events {
		if strings.TrimSpace(ev.NativeName) == "" {
			return fmt.Errorf("harness %s: empty native event name for phase %s", s.ID, phase)
		}
	}
	if s.Capabilities[contract.CapSessionStart] && s.Events[contract.PhaseSessionStart].NativeName == "" {
		return fmt.Errorf("harness %s: claims session-start capability without PhaseSessionStart event", s.ID)
	}
	if s.Capabilities[contract.CapSessionEnd] && s.Events[contract.PhaseSessionEnd].NativeName == "" {
		return fmt.Errorf("harness %s: claims session-end capability without PhaseSessionEnd event", s.ID)
	}
	if s.Capabilities[contract.CapStop] && s.Events[contract.PhaseStop].NativeName == "" && s.Events[contract.PhaseTurnEnd].NativeName == "" {
		return fmt.Errorf("harness %s: claims stop capability without PhaseStop or PhaseTurnEnd event", s.ID)
	}
	if s.Capabilities[contract.CapStopBlock] {
		stopEv, hasStop := s.Events[contract.PhaseStop]
		turnEv, hasTurn := s.Events[contract.PhaseTurnEnd]
		if (!hasStop || stopEv.StopContinuation != "hard" || !stopEv.Blocking) &&
			(!hasTurn || turnEv.StopContinuation != "hard" || !turnEv.Blocking) {
			return fmt.Errorf("harness %s: claims stop-block capability without a hard-blocking stop/turn-end event", s.ID)
		}
	}
	if s.Capabilities[contract.CapToolBefore] && s.Events[contract.PhaseToolBefore].NativeName == "" {
		return fmt.Errorf("harness %s: claims tool-before capability without PhaseToolBefore event", s.ID)
	}
	if s.Capabilities[contract.CapToolAfter] && s.Events[contract.PhaseToolAfter].NativeName == "" {
		return fmt.Errorf("harness %s: claims tool-after capability without PhaseToolAfter event", s.ID)
	}
	if s.Capabilities[contract.CapSubagent] && s.Events[contract.PhaseSubagentStart].NativeName == "" && s.Events[contract.PhaseSubagentStop].NativeName == "" {
		return fmt.Errorf("harness %s: claims subagent capability without subagent events", s.ID)
	}
	if s.Capabilities[contract.CapCompaction] && s.Events[contract.PhaseCompactPre].NativeName == "" && s.Events[contract.PhaseCompactPost].NativeName == "" {
		return fmt.Errorf("harness %s: claims compaction capability without compaction events", s.ID)
	}
	return nil
}
