package registry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jadmadi/sila/internal/harness/contract"
	"github.com/jadmadi/sila/internal/store"
)

var defaultPayloadSpec = PayloadSpec{
	SessionIDFields:      []string{"session_id", "sessionId", "conversationId", "conversation_id", "session"},
	ParentSessionFields:  []string{"parent_session_id", "parentSessionId", "parent_id", "parentId"},
	ProjectFields:        []string{"project_id", "projectId", "project"},
	CwdFields:            []string{"cwd", "workspace", "workspace_root", "workspaceRoot", "workspacePaths"},
	PhaseFields:          []string{"hook_event_name", "hookEventName", "event", "hook", "type"},
	HarnessGoalFields:    []string{"harness_goal_id", "goal_id", "goalId", "goal"},
	ToolNameFields:       []string{"tool_name", "toolName", "tool", "name"},
	ToolInputFields:      []string{"tool_input", "toolInput", "toolCall", "arguments", "input", "command"},
	PermissionModeFields: []string{"permission_mode", "permissionMode", "mode"},
	SubagentIDFields:     []string{"subagent_id", "subagentId", "agent_id", "agentId"},
	CompactionFields:     []string{"sequence", "trigger", "source", "reason"},
}

var allSpecs = []HarnessSpec{
	{
		ID:          "claude",
		DisplayName: "Claude Code",
		Aliases:     []string{"claude-code", "claudecode", "claude_code"},
		Kind:        KindShellHook,
		Output:      OutputClaude,
		Config: ConfigSpec{
			ProjectPaths:    []string{".claude/settings.json", ".claude/settings.local.json"},
			UserPaths:       []string{"~/.claude/settings.json"},
			Format:          "json",
			Dialect:         DialectClaudeNested,
			TrustRequired:   false,
			AtomicWrite:     true,
			PreserveForeign: true,
		},
		Events: map[contract.Phase]EventSpec{
			contract.PhaseSessionStart:  {NativeName: "SessionStart", Command: "sila hook session-start --json", ContextInjection: true},
			contract.PhaseSessionEnd:    {NativeName: "SessionEnd", Command: "sila hook session-end --json"},
			contract.PhaseStop:          {NativeName: "Stop", Command: "sila hook stop --json", Blocking: true, ContextInjection: true, StopContinuation: "hard"},
			contract.PhaseToolBefore:    {NativeName: "PreToolUse", Matcher: "Bash", Command: "sila hook pre-tool-use --json", Blocking: true, ContextInjection: true},
			contract.PhaseToolAfter:     {NativeName: "PostToolUse", Matcher: "Bash", Command: "sila hook post-tool-use --json", ContextInjection: true},
			contract.PhaseSubagentStart: {NativeName: "SubagentStart", Command: "sila hook subagent-start --json"},
			contract.PhaseSubagentStop:  {NativeName: "SubagentStop", Command: "sila hook subagent-stop --json"},
			contract.PhaseCompactPre:    {NativeName: "PreCompact", Command: "sila hook pre-compact --json"},
			contract.PhaseCompactPost:   {NativeName: "PostCompact", Command: "sila hook post-compaction --json", ContextInjection: true},
			contract.PhaseUserPrompt:    {NativeName: "UserPromptSubmit", Command: "sila hook user-prompt --json", ContextInjection: true},
		},
		ExtraEvents: []string{"StopFailure", "PostToolUseFailure", "PostToolBatch", "Notification", "Interrupt"},
		Capabilities: map[contract.Capability]bool{
			contract.CapSessionStart:   true,
			contract.CapSessionEnd:     true,
			contract.CapStop:           true,
			contract.CapStopBlock:      true,
			contract.CapToolBefore:     true,
			contract.CapToolAfter:      true,
			contract.CapPermissionDeny: true,
			contract.CapContextInject:  true,
			contract.CapSubagent:       true,
			contract.CapCompaction:     true,
		},
		Payload: defaultPayloadSpec,
		Trust:   "workspace",
		DocsURL: "https://code.claude.com/docs/en/hooks",
		Notes: []string{
			"Project hooks require workspace trust; SessionStart matcher includes compact",
		},
	},
	{
		ID:          "cursor",
		DisplayName: "Cursor",
		Aliases:     []string{"cursor-agent", "cursor-cli"},
		Kind:        KindShellHook,
		Output:      OutputCursor,
		Config: ConfigSpec{
			ProjectPaths:    []string{".cursor/hooks.json"},
			UserPaths:       []string{"~/.cursor/hooks.json"},
			Format:          "json",
			Dialect:         DialectCursorFlat,
			TrustRequired:   false,
			AtomicWrite:     true,
			PreserveForeign: true,
		},
		Events: map[contract.Phase]EventSpec{
			contract.PhaseSessionStart:  {NativeName: "sessionStart", Command: "sila hook session-start --json", ContextInjection: true},
			contract.PhaseSessionEnd:    {NativeName: "sessionEnd", Command: "sila hook session-end --json"},
			contract.PhaseStop:          {NativeName: "stop", Command: "sila hook stop --json", Blocking: false, ContextInjection: true, StopContinuation: "continue-only"},
			contract.PhaseToolBefore:    {NativeName: "preToolUse", Command: "sila hook pre-tool-use --json", Blocking: true, ContextInjection: true},
			contract.PhaseToolAfter:     {NativeName: "postToolUse", Command: "sila hook post-tool-use --json", ContextInjection: true},
			contract.PhaseSubagentStart: {NativeName: "subagentStart", Command: "sila hook subagent-start --json"},
			contract.PhaseSubagentStop:  {NativeName: "subagentStop", Command: "sila hook subagent-stop --json"},
			contract.PhaseCompactPre:    {NativeName: "preCompact", Command: "sila hook pre-compact --json"},
			contract.PhaseUserPrompt:    {NativeName: "beforeSubmitPrompt", Command: "sila hook user-prompt --json"},
		},
		ExtraEvents: []string{"postToolUseFailure", "beforeShellExecution", "afterShellExecution", "beforeMCPExecution", "afterMCPExecution", "beforeReadFile", "afterFileEdit", "afterAgentResponse", "afterAgentThought"},
		Capabilities: map[contract.Capability]bool{
			contract.CapSessionStart:   true,
			contract.CapSessionEnd:     true,
			contract.CapStop:           true,
			contract.CapStopBlock:      false,
			contract.CapToolBefore:     true,
			contract.CapToolAfter:      true,
			contract.CapPermissionDeny: true,
			contract.CapContextInject:  true,
			contract.CapSubagent:       true,
			contract.CapCompaction:     true,
		},
		Payload: defaultPayloadSpec,
		Trust:   "none",
		DocsURL: "https://cursor.com/docs/hooks",
		Notes: []string{
			"stop is continue-only (followup_message); no hard deny at stop",
			"failClosed opt-in; project hooks run from project root",
		},
	},
	{
		ID:          "codex",
		DisplayName: "Codex",
		Aliases:     []string{"codex-cli", "openai-codex"},
		Kind:        KindShellHook,
		Output:      OutputCodex,
		Config: ConfigSpec{
			ProjectPaths:    []string{".codex/hooks.json", ".codex/config.toml"},
			UserPaths:       []string{"~/.codex/hooks.json", "~/.codex/config.toml"},
			Format:          "json",
			Dialect:         DialectClaudeNested,
			TrustRequired:   true,
			AtomicWrite:     true,
			PreserveForeign: true,
		},
		Events: map[contract.Phase]EventSpec{
			contract.PhaseSessionStart:  {NativeName: "SessionStart", Command: "sila hook session-start --json", ContextInjection: true},
			contract.PhaseSessionEnd:    {NativeName: "SessionEnd", Command: "sila hook session-end --json"},
			contract.PhaseStop:          {NativeName: "Stop", Command: "sila hook stop --json", Blocking: true, ContextInjection: true, StopContinuation: "hard"},
			contract.PhaseToolBefore:    {NativeName: "PreToolUse", Command: "sila hook pre-tool-use --json", Blocking: true, ContextInjection: true},
			contract.PhaseToolAfter:     {NativeName: "PostToolUse", Command: "sila hook post-tool-use --json", ContextInjection: true},
			contract.PhaseSubagentStart: {NativeName: "SubagentStart", Command: "sila hook subagent-start --json"},
			contract.PhaseSubagentStop:  {NativeName: "SubagentStop", Command: "sila hook subagent-stop --json"},
			contract.PhaseCompactPre:    {NativeName: "PreCompact", Command: "sila hook pre-compact --json"},
			contract.PhaseCompactPost:   {NativeName: "PostCompact", Command: "sila hook post-compaction --json", ContextInjection: true},
			contract.PhaseUserPrompt:    {NativeName: "UserPromptSubmit", Command: "sila hook user-prompt --json", ContextInjection: true},
		},
		ExtraEvents: []string{"PermissionRequest", "Interrupt"},
		Capabilities: map[contract.Capability]bool{
			contract.CapSessionStart:   true,
			contract.CapSessionEnd:     true,
			contract.CapStop:           true,
			contract.CapStopBlock:      true,
			contract.CapToolBefore:     true,
			contract.CapToolAfter:      true,
			contract.CapPermissionDeny: true,
			contract.CapContextInject:  true,
			contract.CapSubagent:       true,
			contract.CapCompaction:     true,
		},
		Payload: defaultPayloadSpec,
		Trust:   "project-trust",
		DocsURL: "https://developers.openai.com/codex/hooks",
		Notes: []string{
			"Non-managed hooks require review/trust; project hooks only in trusted projects",
			"Stop has continuation semantics",
		},
	},
	{
		ID:          "devin",
		DisplayName: "Devin",
		Aliases:     []string{"devin-cli"},
		Kind:        KindShellHook,
		Output:      OutputDevin,
		Config: ConfigSpec{
			ProjectPaths:    []string{".devin/hooks.v1.json", ".devin/config.json", ".devin/config.local.json"},
			UserPaths:       []string{"~/.config/devin/hooks.v1.json"},
			Format:          "json",
			Dialect:         DialectClaudeNested,
			TrustRequired:   false,
			AtomicWrite:     true,
			PreserveForeign: true,
		},
		Events: map[contract.Phase]EventSpec{
			contract.PhaseSessionStart: {NativeName: "SessionStart", Command: "sila hook session-start --json", ContextInjection: true},
			contract.PhaseSessionEnd:   {NativeName: "SessionEnd", Command: "sila hook session-end --json"},
			contract.PhaseStop:         {NativeName: "Stop", Command: "sila hook stop --json", Blocking: true, ContextInjection: true, StopContinuation: "hard"},
			contract.PhaseToolBefore:   {NativeName: "PreToolUse", Command: "sila hook pre-tool-use --json", Blocking: true, ContextInjection: true},
			contract.PhaseToolAfter:    {NativeName: "PostToolUse", Command: "sila hook post-tool-use --json", ContextInjection: true},
			contract.PhaseCompactPost:  {NativeName: "PostCompaction", Command: "sila hook post-compaction --json", ContextInjection: true},
			contract.PhaseUserPrompt:   {NativeName: "UserPromptSubmit", Command: "sila hook user-prompt --json", ContextInjection: true},
		},
		ExtraEvents: []string{"PermissionRequest"},
		Capabilities: map[contract.Capability]bool{
			contract.CapSessionStart:   true,
			contract.CapSessionEnd:     true,
			contract.CapStop:           true,
			contract.CapStopBlock:      true,
			contract.CapToolBefore:     true,
			contract.CapToolAfter:      true,
			contract.CapPermissionDeny: true,
			contract.CapContextInject:  true,
			contract.CapSubagent:       false,
			contract.CapCompaction:     true,
		},
		Payload: defaultPayloadSpec,
		Trust:   "none",
		DocsURL: "https://docs.devin.ai/cli/extensibility/hooks/overview",
		Notes: []string{
			"Reads .claude hooks when read_config_from.claude; project hooks found up to repo root",
		},
	},
	{
		ID:          "command-code",
		DisplayName: "Command Code",
		Aliases:     []string{"commandcode", "command_code", "cmd"},
		Kind:        KindShellHook,
		Output:      OutputCommandCode,
		Config: ConfigSpec{
			ProjectPaths:    []string{".commandcode/settings.json"},
			UserPaths:       []string{"~/.commandcode/settings.json"},
			Format:          "json",
			Dialect:         DialectClaudeNested,
			TrustRequired:   false,
			AtomicWrite:     true,
			PreserveForeign: true,
		},
		Events: map[contract.Phase]EventSpec{
			contract.PhaseSessionStart: {NativeName: "SessionStart", Command: "sila hook session-start --json", ContextInjection: true},
			contract.PhaseStop:         {NativeName: "Stop", Command: "sila hook stop --auto-sync --json", Blocking: true, ContextInjection: true, StopContinuation: "hard"},
			contract.PhaseToolBefore:   {NativeName: "PreToolUse", Matcher: "web_search", Command: "sila hook pre-tool-use --json", Blocking: true, ContextInjection: true},
			contract.PhaseToolAfter:    {NativeName: "PostToolUse", Matcher: "shell", Command: "sila hook post-tool-use --json", ContextInjection: true},
		},
		Capabilities: map[contract.Capability]bool{
			contract.CapSessionStart:   true,
			contract.CapSessionEnd:     false,
			contract.CapStop:           true,
			contract.CapStopBlock:      true,
			contract.CapToolBefore:     true,
			contract.CapToolAfter:      true,
			contract.CapPermissionDeny: true,
			contract.CapContextInject:  true,
			contract.CapSubagent:       false,
			contract.CapCompaction:     false,
		},
		Payload: defaultPayloadSpec,
		Trust:   "none",
		DocsURL: "https://commandcode.ai/docs/hooks",
		Notes: []string{
			"Project > user; hooks skipped in plan mode; stop_hook_active loop guard (retry max 3)",
			"Mods exist beyond the four shell events; no shell subagent or compaction events",
		},
	},
	{
		ID:          "codewhale",
		DisplayName: "Codewhale",
		Aliases:     []string{"code-whale"},
		Kind:        KindShellHook,
		Output:      OutputCodewhale,
		Config: ConfigSpec{
			ProjectPaths:    []string{".codewhale/hooks.toml"},
			UserPaths:       []string{"~/.codewhale/config.toml"},
			Format:          "toml",
			Dialect:         DialectCodewhaleTOML,
			TrustRequired:   true,
			AtomicWrite:     true,
			PreserveForeign: true,
		},
		Events: map[contract.Phase]EventSpec{
			contract.PhaseSessionStart:  {NativeName: "session_start", Command: "sila hook session-start --json", ContextInjection: true},
			contract.PhaseSessionEnd:    {NativeName: "session_end", Command: "sila hook session-end --json"},
			contract.PhaseTurnEnd:       {NativeName: "turn_end", Command: "sila hook stop --json", Blocking: true, ContextInjection: true, StopContinuation: "hard"},
			contract.PhaseStop:          {NativeName: "turn_end", Command: "sila hook stop --json", Blocking: true, ContextInjection: true, StopContinuation: "hard"},
			contract.PhaseToolBefore:    {NativeName: "tool_call_before", Command: "sila hook pre-tool-use --json", Blocking: true, ContextInjection: true},
			contract.PhaseToolAfter:     {NativeName: "tool_call_after", Command: "sila hook post-tool-use --json", ContextInjection: true},
			contract.PhaseUserPrompt:    {NativeName: "message_submit", Command: "sila hook user-prompt --json"},
			contract.PhaseSubagentStart: {NativeName: "subagent_spawn", Command: "sila hook subagent-start --json"},
			contract.PhaseSubagentStop:  {NativeName: "subagent_complete", Command: "sila hook subagent-stop --json"},
		},
		ExtraEvents: []string{"shell_env", "waiting_for_user", "session_idle", "session_busy", "session_error", "on_error", "mode_change"},
		Capabilities: map[contract.Capability]bool{
			contract.CapSessionStart:   true,
			contract.CapSessionEnd:     true,
			contract.CapStop:           true,
			contract.CapStopBlock:      true,
			contract.CapToolBefore:     true,
			contract.CapToolAfter:      true,
			contract.CapPermissionDeny: true,
			contract.CapContextInject:  true,
			contract.CapSubagent:       true,
			contract.CapCompaction:     false,
		},
		Payload: defaultPayloadSpec,
		Trust:   "review-digest",
		DocsURL: "https://codewhale.net/en/docs/hooks",
		Notes: []string{
			"Project hooks require workspace trust + digest approval",
			"No documented compaction event",
		},
	},
	{
		ID:          "antigravity",
		DisplayName: "Antigravity (AGY)",
		Aliases:     []string{"agy", "antigravity-cli", "gemini"},
		Kind:        KindShellHook,
		Output:      OutputAntigravity,
		Config: ConfigSpec{
			ProjectPaths:    []string{".agents/hooks.json"},
			UserPaths:       []string{"~/.gemini/config/hooks.json"},
			Format:          "json",
			Dialect:         DialectAntigravityNamed,
			TrustRequired:   false,
			AtomicWrite:     true,
			PreserveForeign: true,
		},
		Events: map[contract.Phase]EventSpec{
			contract.PhaseSessionStart: {NativeName: "SessionStart", Command: "sila hook session-start --json", ContextInjection: true},
			contract.PhaseUserPrompt:   {NativeName: "PreInvocation", Command: "sila hook user-prompt-submit --json", ContextInjection: true},
			contract.PhaseToolBefore:   {NativeName: "PreToolUse", Command: "sila hook pre-tool-use --json", Blocking: true, ContextInjection: true},
			contract.PhaseToolAfter:    {NativeName: "PostToolUse", Command: "sila hook post-tool-use --json", ContextInjection: true},
			contract.PhaseTurnEnd:      {NativeName: "PostInvocation", Command: "sila hook post-invocation --json"},
			contract.PhaseStop:         {NativeName: "Stop", Command: "sila hook stop --json", Blocking: true, ContextInjection: true, StopContinuation: "hard"},
		},
		Capabilities: map[contract.Capability]bool{
			contract.CapSessionStart:   true,
			contract.CapSessionEnd:     false,
			contract.CapStop:           true,
			contract.CapStopBlock:      true,
			contract.CapToolBefore:     true,
			contract.CapToolAfter:      true,
			contract.CapPermissionDeny: true,
			contract.CapContextInject:  true,
			contract.CapSubagent:       false,
			contract.CapCompaction:     false,
		},
		Payload: defaultPayloadSpec,
		Trust:   "none",
		DocsURL: "https://antigravity.google/docs/hooks",
		Notes: []string{
			"App/CLI/IDE resolve different app-data dirs; camelCase payload (conversationId, toolCall)",
			"No documented subagent or compaction events",
		},
	},
	{
		ID:          "zcode",
		DisplayName: "ZCode",
		Aliases:     []string{"z-code"},
		Kind:        KindShellHook,
		Output:      OutputZCode,
		Config: ConfigSpec{
			ProjectPaths:        nil,
			UserPaths:           []string{"~/.zcode/cli/config.json"},
			Format:              "json",
			Dialect:             DialectClaudeNested,
			TrustRequired:       false,
			ProjectHooksIgnored: true,
			AtomicWrite:         true,
			PreserveForeign:     true,
		},
		Events: map[contract.Phase]EventSpec{
			contract.PhaseSessionStart: {NativeName: "SessionStart", Command: "sila hook session-start --json", ContextInjection: true},
			contract.PhaseUserPrompt:   {NativeName: "UserPromptSubmit", Command: "sila hook user-prompt --json"},
			contract.PhaseToolBefore:   {NativeName: "PreToolUse", Command: "sila hook pre-tool-use --json", Blocking: true, ContextInjection: true},
			contract.PhaseToolAfter:    {NativeName: "PostToolUse", Command: "sila hook post-tool-use --json", ContextInjection: true},
			contract.PhaseStop:         {NativeName: "Stop", Command: "sila hook stop --json", Blocking: true, ContextInjection: true, StopContinuation: "hard"},
		},
		ExtraEvents: []string{"PermissionRequest", "PostToolUseFailure"},
		Capabilities: map[contract.Capability]bool{
			contract.CapSessionStart:   true,
			contract.CapSessionEnd:     false,
			contract.CapStop:           true,
			contract.CapStopBlock:      true,
			contract.CapToolBefore:     true,
			contract.CapToolAfter:      true,
			contract.CapPermissionDeny: true,
			contract.CapContextInject:  true,
			contract.CapSubagent:       false,
			contract.CapCompaction:     false,
		},
		Payload: defaultPayloadSpec,
		Trust:   "none",
		DocsURL: "https://zcode.z.ai/en/docs/hooks",
		Notes: []string{
			"Project-level hooks are ignored entirely; distribute via user config (~/.zcode/cli/config.json) or plugin",
			"Config snapshotted at session start; Stop max 3 retries",
		},
	},
	{
		ID:          "muse",
		DisplayName: "Muse",
		Aliases:     []string{"muse-code", "musecode"},
		Kind:        KindShellHook,
		Output:      OutputMuse,
		Config: ConfigSpec{
			ProjectPaths:    []string{".muse/hooks.json"},
			UserPaths:       []string{"~/.muse/hooks.json"},
			Format:          "json",
			Dialect:         DialectClaudeNested,
			TrustRequired:   true,
			AtomicWrite:     true,
			PreserveForeign: true,
		},
		Events: map[contract.Phase]EventSpec{
			contract.PhaseSessionStart:  {NativeName: "SessionStart", Command: "sila hook session-start --json"},
			contract.PhaseSessionEnd:    {NativeName: "SessionEnd", Command: "sila hook session-end --json"},
			contract.PhaseStop:          {NativeName: "Stop", Command: "sila hook stop --json", Blocking: false, StopContinuation: "none"},
			contract.PhaseToolBefore:    {NativeName: "PreToolUse", Command: "sila hook pre-tool-use --json"},
			contract.PhaseToolAfter:     {NativeName: "PostToolUse", Command: "sila hook post-tool-use --json"},
			contract.PhaseSubagentStart: {NativeName: "SubagentStart", Command: "sila hook subagent-start --json"},
			contract.PhaseSubagentStop:  {NativeName: "SubagentStop", Command: "sila hook subagent-stop --json"},
			contract.PhaseCompactPre:    {NativeName: "PreCompact", Command: "sila hook pre-compact --json"},
			contract.PhaseCompactPost:   {NativeName: "PostCompact", Command: "sila hook post-compaction --json"},
			contract.PhaseUserPrompt:    {NativeName: "UserPromptSubmit", Command: "sila hook user-prompt --json"},
		},
		ExtraEvents: []string{"PermissionRequest", "PostToolUseFailure", "PostToolBatch", "PreLLMCall", "PostLLMCall", "Notification", "StopFailure"},
		Capabilities: map[contract.Capability]bool{
			contract.CapSessionStart:   true,
			contract.CapSessionEnd:     true,
			contract.CapStop:           true,
			contract.CapStopBlock:      false,
			contract.CapToolBefore:     true,
			contract.CapToolAfter:      true,
			contract.CapPermissionDeny: false,
			contract.CapContextInject:  false,
			contract.CapSubagent:       true,
			contract.CapCompaction:     true,
		},
		Payload: defaultPayloadSpec,
		Trust:   "project-trust",
		DocsURL: "https://dev.meta.ai/docs/muse-code/extending",
		Notes: []string{
			"Hooks run outside the sandbox; project hooks require trust; managed hooks bypass project trust",
			"Exact output JSON fields are unverified; blocking decisions return unsupported rather than inventing fields",
		},
	},
	{
		ID:          "opencode",
		DisplayName: "OpenCode",
		Aliases:     []string{"open-code"},
		Kind:        KindPluginHook,
		Output:      OutputPlugin,
		Config: ConfigSpec{
			ProjectPaths: []string{".opencode/plugins/sila-goal-contract.js", "opencode.json"},
			UserPaths: []string{
				"~/.config/opencode/plugins/sila-goal-contract.js",
				"~/.var/app/ai.opencode.opencode/config/opencode/plugins/sila-goal-contract.js",
			},
			Format:          "json",
			TrustRequired:   false,
			AtomicWrite:     true,
			PreserveForeign: true,
		},
		Events: map[contract.Phase]EventSpec{
			contract.PhaseSessionStart: {NativeName: "session.created", ContextInjection: true},
			contract.PhaseStop:         {NativeName: "experimental.idle", Blocking: false, StopContinuation: "continue-only"},
			contract.PhaseToolBefore:   {NativeName: "execute.before", Blocking: true},
			contract.PhaseToolAfter:    {NativeName: "execute.after"},
			contract.PhaseCompactPre:   {NativeName: "compaction"},
		},
		Capabilities: map[contract.Capability]bool{
			contract.CapSessionStart:   true,
			contract.CapSessionEnd:     false,
			contract.CapStop:           true,
			contract.CapStopBlock:      false,
			contract.CapToolBefore:     true,
			contract.CapToolAfter:      true,
			contract.CapPermissionDeny: true,
			contract.CapContextInject:  true,
			contract.CapSubagent:       false,
			contract.CapCompaction:     true,
		},
		Payload: defaultPayloadSpec,
		Trust:   "none",
		DocsURL: "https://opencode.ai/docs/plugins/",
		Notes: []string{
			"No shell-hook config; in-process JS/TS plugin bridge only",
			"session.idle is a notification, not a hard turn-end blocker",
		},
	},
	{
		ID:          "mimocode",
		DisplayName: "MiMoCode",
		Aliases:     []string{"mimo-code", "mimo"},
		Kind:        KindPluginHook,
		Output:      OutputPlugin,
		Config: ConfigSpec{
			ProjectPaths:    []string{".mimocode/plugins/sila-goal-contract.js", "mimocode.json"},
			UserPaths:       []string{"~/.config/mimocode/plugins/sila-goal-contract.js"},
			Format:          "json",
			TrustRequired:   false,
			AtomicWrite:     true,
			PreserveForeign: true,
		},
		Events: map[contract.Phase]EventSpec{
			contract.PhaseSessionStart: {NativeName: "session.created", ContextInjection: true},
			contract.PhaseStop:         {NativeName: "session.idle", Blocking: false, StopContinuation: "continue-only"},
			contract.PhaseToolBefore:   {NativeName: "tool.execute.before", Blocking: true},
			contract.PhaseToolAfter:    {NativeName: "tool.execute.after"},
			contract.PhaseCompactPre:   {NativeName: "experimental.session.compacting"},
			contract.PhaseCompactPost:  {NativeName: "session.compacted", ContextInjection: true},
		},
		ExtraEvents: []string{"session.error"},
		Capabilities: map[contract.Capability]bool{
			contract.CapSessionStart:   true,
			contract.CapSessionEnd:     false,
			contract.CapStop:           true,
			contract.CapStopBlock:      false,
			contract.CapToolBefore:     true,
			contract.CapToolAfter:      true,
			contract.CapPermissionDeny: true,
			contract.CapContextInject:  true,
			contract.CapSubagent:       false,
			contract.CapCompaction:     true,
		},
		Payload: defaultPayloadSpec,
		Trust:   "none",
		DocsURL: "https://mimo.xiaomi.com/mimocode/config-files",
		Notes: []string{
			"OpenCode-derived plugin API; official /plugins page returned 404",
			"Uses shared plugin bridge template with mimocode.json plugin array",
		},
	},
	{
		ID:          "git",
		DisplayName: "Git",
		Aliases:     []string{"git-hooks"},
		Kind:        KindCommitHook,
		Output:      OutputGit,
		Config: ConfigSpec{
			ProjectPaths:    []string{".git/hooks/pre-commit", ".git/hooks/pre-push"},
			Format:          "shell",
			TrustRequired:   false,
			AtomicWrite:     true,
			PreserveForeign: true,
		},
		Events: map[contract.Phase]EventSpec{
			contract.PhaseStop: {NativeName: "pre-commit", Command: "sila gate --pre-commit", Blocking: true, StopContinuation: "hard"},
		},
		ExtraEvents: []string{"pre-push"},
		Capabilities: map[contract.Capability]bool{
			contract.CapStop:      true,
			contract.CapStopBlock: true,
		},
		Payload: defaultPayloadSpec,
		Trust:   "none",
		DocsURL: "https://git-scm.com/docs/githooks",
		Notes: []string{
			"Exit-code only; universal pre-commit/pre-push chokepoint via sila gate",
		},
	},
}

// All returns all registered HarnessSpecs in canonical order.
func All() []HarnessSpec {
	out := make([]HarnessSpec, len(allSpecs))
	copy(out, allSpecs)
	return out
}

// NormalizeID maps any harness name or alias to its canonical spec ID.
func NormalizeID(raw string) string {
	norm := strings.ToLower(strings.TrimSpace(raw))
	norm = strings.ReplaceAll(norm, "_", "-")
	norm = strings.ReplaceAll(norm, " ", "-")
	for _, sp := range allSpecs {
		if sp.ID == norm {
			return sp.ID
		}
		for _, al := range sp.Aliases {
			if strings.ToLower(al) == norm {
				return sp.ID
			}
		}
	}
	return norm
}

// Lookup returns the HarnessSpec for a harness name or alias.
func Lookup(harness string) (HarnessSpec, bool) {
	id := NormalizeID(harness)
	for _, sp := range allSpecs {
		if sp.ID == id {
			return sp, true
		}
	}
	return HarnessSpec{}, false
}

// ResolvePhase maps a native event name to a canonical contract.Phase for a spec.
func (s HarnessSpec) ResolvePhase(nativeEvent string) (contract.Phase, bool) {
	evTrim := strings.TrimSpace(nativeEvent)
	if evTrim == "" {
		return "", false
	}
	// Exact or case-insensitive match against declared spec events in deterministic phase order.
	for _, phase := range sortedPhases(s.Events) {
		specEv := s.Events[phase]
		if strings.EqualFold(specEv.NativeName, evTrim) {
			return phase, true
		}
	}
	// Also accept canonical phase names directly (when invoked via `sila hook <phase>`).
	norm := strings.ToLower(strings.ReplaceAll(evTrim, "_", "-"))
	switch norm {
	case "session-start":
		if _, ok := s.Events[contract.PhaseSessionStart]; ok {
			return contract.PhaseSessionStart, true
		}
	case "session-end":
		if _, ok := s.Events[contract.PhaseSessionEnd]; ok {
			return contract.PhaseSessionEnd, true
		}
	case "stop", "turn-end":
		if _, ok := s.Events[contract.PhaseStop]; ok {
			return contract.PhaseStop, true
		}
		if _, ok := s.Events[contract.PhaseTurnEnd]; ok {
			return contract.PhaseTurnEnd, true
		}
	case "pre-tool-use", "tool-before", "tool-call-before":
		if _, ok := s.Events[contract.PhaseToolBefore]; ok {
			return contract.PhaseToolBefore, true
		}
	case "post-tool-use", "tool-after", "tool-call-after":
		if _, ok := s.Events[contract.PhaseToolAfter]; ok {
			return contract.PhaseToolAfter, true
		}
	case "subagent-start", "subagent-spawn":
		if _, ok := s.Events[contract.PhaseSubagentStart]; ok {
			return contract.PhaseSubagentStart, true
		}
	case "subagent-stop", "subagent-complete":
		if _, ok := s.Events[contract.PhaseSubagentStop]; ok {
			return contract.PhaseSubagentStop, true
		}
	case "pre-compact", "precompact":
		if _, ok := s.Events[contract.PhaseCompactPre]; ok {
			return contract.PhaseCompactPre, true
		}
	case "post-compaction", "post-compact", "postcompact":
		if _, ok := s.Events[contract.PhaseCompactPost]; ok {
			return contract.PhaseCompactPost, true
		}
	case "user-prompt", "userpromptsubmit", "message-submit", "beforesubmitprompt":
		if _, ok := s.Events[contract.PhaseUserPrompt]; ok {
			return contract.PhaseUserPrompt, true
		}
	}
	return "", false
}

// NormalizePayload extracts a canonical contract.Signal from a raw JSON payload
// using the spec's ordered candidate fields. If the phase is unsupported, it
// returns a HARNESS_UNSUPPORTED decision.
func (s HarnessSpec) NormalizePayload(rawBytes []byte, defaultProject, defaultCwd string) (contract.Signal, *contract.Decision, error) {
	if len(rawBytes) > 16*1024 {
		return contract.Signal{}, nil, fmt.Errorf("hook payload exceeds 16 KiB limit")
	}
	var raw map[string]any
	if err := json.Unmarshal(rawBytes, &raw); err != nil {
		return contract.Signal{}, nil, fmt.Errorf("parse hook json: %w", err)
	}
	nested, _ := raw["payload"].(map[string]any)

	pickString := func(fields []string) string {
		for _, f := range fields {
			if v, ok := raw[f]; ok && v != nil {
				if str := extractStringOrFirst(v); str != "" {
					return str
				}
			}
			if nested != nil {
				if v, ok := nested[f]; ok && v != nil {
					if str := extractStringOrFirst(v); str != "" {
						return str
					}
				}
			}
		}
		return ""
	}

	rawPhase := pickString(s.Payload.PhaseFields)
	// Antigravity PreInvocation / PostInvocation heuristic when mapped to canonical phases:
	if s.ID == "antigravity" && strings.EqualFold(rawPhase, "PreInvocation") {
		if inv, ok := raw["invocationNum"].(float64); ok && inv == 0 {
			rawPhase = "SessionStart"
		} else {
			rawPhase = "PreToolUse"
		}
	} else if s.ID == "antigravity" && strings.EqualFold(rawPhase, "PostInvocation") {
		rawPhase = "PostToolUse"
	}

	phase, ok := s.ResolvePhase(rawPhase)
	if !ok {
		dec := &contract.Decision{
			Schema:     contract.DecisionSchema,
			Decision:   contract.DecisionUnsupported,
			ReasonCode: contract.ReasonHarnessUnsupport,
			Reason:     fmt.Sprintf("harness %s does not support phase %q", s.ID, rawPhase),
			Recovery:   "run `sila gate --pre-commit` or wire a documented event (`sila hook status`)",
			Checks:     map[string]contract.CheckResult{},
		}
		return contract.Signal{}, dec, nil
	}

	sessionID := pickString(s.Payload.SessionIDFields)
	if sessionID == "" {
		sessionID = "unbound"
	}
	parentSession := pickString(s.Payload.ParentSessionFields)
	cwd := pickString(s.Payload.CwdFields)
	if cwd == "" {
		cwd = defaultCwd
	}
	projectID := pickString(s.Payload.ProjectFields)
	if projectID == "" {
		projectID = defaultProject
	}
	harnessGoal := pickString(s.Payload.HarnessGoalFields)
	toolName := pickString(s.Payload.ToolNameFields)
	permMode := pickString(s.Payload.PermissionModeFields)
	compactionSeq := pickString(s.Payload.CompactionFields)

	// Extract tool input shape without echoing values.
	var toolShape string
	for _, f := range s.Payload.ToolInputFields {
		val, exists := raw[f]
		if !exists && nested != nil {
			val, exists = nested[f]
		}
		if exists && val != nil {
			// If toolCall is an object with name/arguments (e.g. Antigravity), extract toolName if empty
			if m, isMap := val.(map[string]any); isMap {
				if toolName == "" {
					if tn, ok := m["name"].(string); ok && tn != "" {
						toolName = strings.TrimSpace(tn)
					}
				}
				if argsVal, hasArgs := m["arguments"]; hasArgs && argsVal != nil {
					if b, err := json.Marshal(argsVal); err == nil {
						toolShape = store.BuildArgsShape(b)
						break
					}
				}
				if inputVal, hasInput := m["input"]; hasInput && inputVal != nil {
					if b, err := json.Marshal(inputVal); err == nil {
						toolShape = store.BuildArgsShape(b)
						break
					}
				}
			}
			if b, err := json.Marshal(val); err == nil {
				toolShape = store.BuildArgsShape(b)
				break
			}
		}
	}

	silaGoal := pickString([]string{"sila_goal_slug", "sila_goal", "goal_slug"})
	claimID := pickString([]string{"claim_id", "claimId"})

	sig := contract.NormalizeSignal(contract.Signal{
		SchemaVersion:   contract.SignalSchema,
		Harness:         contract.HarnessID(s.ID),
		SessionID:       sessionID,
		ParentSessionID: parentSession,
		ProjectID:       projectID,
		Cwd:             cwd,
		Phase:           phase,
		HarnessGoalID:   harnessGoal,
		SilaGoalSlug:    silaGoal,
		ClaimID:         claimID,
		PermissionMode:  permMode,
		ToolName:        toolName,
		ToolShape:       toolShape,
		Sequence:        compactionSeq,
	})
	return sig, nil, nil
}

func extractStringOrFirst(v any) string {
	switch val := v.(type) {
	case string:
		return strings.TrimSpace(val)
	case []any:
		for _, item := range val {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	case []string:
		for _, item := range val {
			if strings.TrimSpace(item) != "" {
				return strings.TrimSpace(item)
			}
		}
	}
	return ""
}

// RenderCapabilityMatrixTable renders the Markdown table for native shell-hook adapters
// so tests can verify docs/harness-capability-matrix.md stays synchronized with the registry.
func RenderCapabilityMatrixTable() string {
	var buf bytes.Buffer
	buf.WriteString("| Harness | Config locations | Events | Stop can block? | Subagent events | Compaction events | Trust / project notes |\n")
	buf.WriteString("|---|---|---|---|---|---|---|\n")
	for _, sp := range allSpecs {
		if sp.Kind != KindShellHook {
			continue
		}
		configs := append([]string{}, sp.Config.ProjectPaths...)
		configs = append(configs, sp.Config.UserPaths...)
		stopBlock := "No"
		if ev, ok := sp.Events[contract.PhaseStop]; ok {
			switch ev.StopContinuation {
			case "hard":
				stopBlock = "Yes"
			case "continue-only":
				stopBlock = "Continue-only (`followup_message`)"
			}
		}
		subagent := "none"
		if sp.Capabilities[contract.CapSubagent] {
			subagent = "Yes"
		}
		compact := "none"
		if sp.Capabilities[contract.CapCompaction] {
			_, hasPre := sp.Events[contract.PhaseCompactPre]
			_, hasPost := sp.Events[contract.PhaseCompactPost]
			switch {
			case hasPre && hasPost:
				compact = "Yes"
			case hasPre:
				compact = fmt.Sprintf("`%s` only", sp.Events[contract.PhaseCompactPre].NativeName)
			case hasPost:
				compact = fmt.Sprintf("`%s` only", sp.Events[contract.PhaseCompactPost].NativeName)
			}
		}
		buf.WriteString(fmt.Sprintf("| **%s** | %s | %d mapped | %s | %s | %s | %s |\n",
			sp.DisplayName,
			strings.Join(configs, ", "),
			len(sp.Events),
			stopBlock,
			subagent,
			compact,
			strings.Join(sp.Notes, "; "),
		))
	}
	return buf.String()
}
