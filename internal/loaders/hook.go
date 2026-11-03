package loaders

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/workspace"
)

// LifecycleEventKind defines normalized agent lifecycle transition events.
type LifecycleEventKind string

const (
	EventSessionStart  LifecycleEventKind = "session-start"
	EventUserPrompt    LifecycleEventKind = "user-prompt"
	EventPreToolUse    LifecycleEventKind = "pre-tool-use"
	EventPostToolUse   LifecycleEventKind = "post-tool-use"
	EventPreCompact    LifecycleEventKind = "pre-compact"
	EventPostCompact   LifecycleEventKind = "post-compaction"
	EventSubagentStart LifecycleEventKind = "subagent-start"
	EventSubagentStop  LifecycleEventKind = "subagent-stop"
	EventStop          LifecycleEventKind = "stop"
	EventSessionEnd    LifecycleEventKind = "session-end"
	EventOther         LifecycleEventKind = "other"
)

// MaxHookPayloadBytes is the hard ceiling on individual hook payloads (16 KiB).
const MaxHookPayloadBytes = 16 * 1024

// ErrPayloadTooLarge is returned when inbound hook payload exceeds 16 KiB.
var ErrPayloadTooLarge = errors.New("hook payload exceeds 16 KiB limit")

var (
	homeUnixRE    = regexp.MustCompile(`/(?:home|Users)/[a-zA-Z0-9_\.\-]+`)
	homeWindowsRE = regexp.MustCompile(`[a-zA-Z]:\\(?:[Uu]sers)\\[a-zA-Z0-9_\.\-]+`)
	ghTokenRE     = regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[a-zA-Z0-9]{36}\b|\bgithub_pat_[a-zA-Z0-9_]{82}\b`)
	bearerRE      = regexp.MustCompile(`(?i)\b(Bearer\s+)[a-zA-Z0-9_\-\.]{20,}\b`)
	apiKeyRE      = regexp.MustCompile(`\b(?:sk-[a-zA-Z0-9]{20,})\b`)
	pemKeyRE      = regexp.MustCompile("-----" + "BEGIN [A-Z ]*PRIVATE KEY-----[\\s\\S]*?-----END [A-Z ]*PRIVATE KEY-----")
	paramSecretRE = regexp.MustCompile(`(?i)(password|token|secret|api_key|client_secret)=([^&\s]+)`)
	awsKeyRE      = regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)
	jwtRE         = regexp.MustCompile(`\beyJ[a-zA-Z0-9_-]{10,}\.[a-zA-Z0-9_-]{10,}\.[a-zA-Z0-9_-]{10,}\b`)
)

func deriveProjectID(path string) string {
	if identity, err := workspace.Resolve(path); err == nil && identity.ProjectID != "" {
		return identity.ProjectID
	}
	slug := workspace.Slugify(filepath.Base(filepath.Clean(path)))
	if slug == "" {
		slug = "project"
	}
	return slug
}

// HookEnvelope encapsulates an inbound agent lifecycle transition event.
type HookEnvelope struct {
	Event     LifecycleEventKind     `json:"event"`
	Agent     string                 `json:"agent"`
	ProjectID string                 `json:"project_id"`
	Cwd       string                 `json:"cwd"`
	SessionID string                 `json:"session_id"`
	Timestamp string                 `json:"timestamp"`
	Payload   map[string]interface{} `json:"payload"`
}

// NormalizeEventKind maps arbitrary tool event strings into canonical LifecycleEventKind.
func NormalizeEventKind(raw string) LifecycleEventKind {
	norm := strings.ToLower(strings.TrimSpace(raw))
	norm = strings.ReplaceAll(norm, "_", "-")

	switch norm {
	case "session-start", "start", "session-begin", "begin", "init", "launch", "sessionstart", "session.created":
		return EventSessionStart
	case "user-prompt", "prompt", "user-input", "input", "userpromptsubmit", "user-prompt-submit", "beforesubmitprompt", "before-submit-prompt", "message-submit":
		return EventUserPrompt
	case "pre-tool-use", "pre-tool", "tool-call", "tool-use", "pre-invocation", "pretoolexecution", "pre-tool-execution", "pretooluse", "tool-call-before", "tool.execute.before", "beforeshellexecution", "beforemcpexecution", "beforereadfile":
		return EventPreToolUse
	case "post-tool-use", "post-tool", "tool-result", "post-invocation", "posttoolexecution", "post-tool-execution", "posttooluse", "tool-call-after", "tool.execute.after", "aftershellexecution", "aftermcpexecution", "afterfileedit":
		return EventPostToolUse
	case "pre-compact", "precompact", "compact-start", "experimental.session.compacting":
		return EventPreCompact
	case "post-compaction", "postcompact", "post-compact", "compacted", "compaction", "compact", "session.compacted":
		return EventPostCompact
	case "subagent-start", "task-start", "subagentstart", "subagent-spawn":
		return EventSubagentStart
	case "subagent-stop", "task-stop", "subagentstop", "subagent-complete":
		return EventSubagentStop
	case "stop", "cancel", "interrupt", "abort", "turn-end", "session.idle":
		return EventStop
	case "session-end", "end", "done", "finish", "exit", "terminate", "sessionend":
		return EventSessionEnd
	default:
		return EventOther
	}
}

// ParseHookEnvelope parses, validates, and normalizes raw JSON data into a HookEnvelope.
func ParseHookEnvelope(data []byte) (*HookEnvelope, error) {
	if len(data) > MaxHookPayloadBytes {
		return nil, ErrPayloadTooLarge
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse hook json: %w", err)
	}

	env := &HookEnvelope{
		Payload: make(map[string]interface{}),
	}

	// 1. Resolve event kind with adapter heuristics
	var rawEvent string
	if ev, ok := raw["event"].(string); ok {
		rawEvent = ev
	} else if ev, ok := raw["hook"].(string); ok {
		rawEvent = ev
	} else if ev, ok := raw["type"].(string); ok {
		rawEvent = ev
	} else if ev, ok := raw["hook_event_name"].(string); ok {
		rawEvent = ev
	} else if ev, ok := raw["hookEventName"].(string); ok {
		rawEvent = ev
	}

	isAntigravity := strings.EqualFold(rawEvent, "PreInvocation") ||
		raw["invocationNum"] != nil ||
		raw["conversationId"] != nil ||
		raw["stepIdx"] != nil ||
		raw["executionNum"] != nil ||
		raw["terminationReason"] != nil
	isClaudeCode := raw["hook_event_name"] != nil || raw["hookEventName"] != nil || raw["tool_name"] != nil

	// Antigravity adapter: only PreInvocation carries invocation semantics.
	// Other Antigravity events omit the event name entirely, so the phase comes
	// from the installed hook command (`sila hook <phase>`) instead.
	if isAntigravity {
		if strings.EqualFold(rawEvent, "PreInvocation") {
			if invNum, ok := raw["invocationNum"].(float64); ok && invNum == 0 {
				env.Event = EventSessionStart
			} else {
				env.Event = EventPostToolUse
			}
		} else if rawEvent != "" {
			env.Event = NormalizeEventKind(rawEvent)
		} else {
			env.Event = EventOther
		}
	} else if rawEvent != "" {
		env.Event = NormalizeEventKind(rawEvent)
	} else {
		env.Event = EventOther
	}

	// 2. Resolve Agent
	if a, ok := raw["agent"].(string); ok && a != "" {
		env.Agent = strings.TrimSpace(a)
	} else if t, ok := raw["tool"].(string); ok && t != "" {
		env.Agent = strings.TrimSpace(t)
	} else if h, ok := raw["harness"].(string); ok && h != "" {
		env.Agent = strings.TrimSpace(h)
	} else if isAntigravity {
		env.Agent = "antigravity"
	} else if isClaudeCode {
		env.Agent = "claude-code"
	} else {
		env.Agent = "agent"
	}

	// 3. Resolve SessionID
	if s, ok := raw["session_id"].(string); ok && s != "" {
		env.SessionID = s
	} else if s, ok := raw["sessionId"].(string); ok && s != "" {
		env.SessionID = s
	} else if s, ok := raw["conversationId"].(string); ok && s != "" {
		env.SessionID = s
	} else if s, ok := raw["conversation_id"].(string); ok && s != "" {
		env.SessionID = s
	} else if s, ok := raw["session"].(string); ok && s != "" {
		env.SessionID = s
	} else {
		env.SessionID = generateRandomID("sess")
	}

	// 4. Resolve Cwd & ProjectID
	if c, ok := raw["cwd"].(string); ok && c != "" {
		env.Cwd = c
	} else if w, ok := raw["workspace"].(string); ok && w != "" {
		env.Cwd = w
	} else if wp, ok := raw["workspacePaths"].([]interface{}); ok && len(wp) > 0 {
		if first, ok := wp[0].(string); ok && first != "" {
			env.Cwd = first
		}
	} else {
		if wd, err := os.Getwd(); err == nil {
			env.Cwd = wd
		}
	}

	if p, ok := raw["project_id"].(string); ok && p != "" {
		env.ProjectID = p
	} else if p, ok := raw["projectId"].(string); ok && p != "" {
		env.ProjectID = p
	} else if p, ok := raw["project"].(string); ok && p != "" {
		env.ProjectID = p
	} else if env.Cwd != "" {
		env.ProjectID = deriveProjectID(env.Cwd)
	} else {
		env.ProjectID = "default"
	}

	// 5. Resolve Timestamp
	if ts, ok := raw["timestamp"].(string); ok && ts != "" {
		env.Timestamp = ts
	} else if ts, ok := raw["time"].(string); ok && ts != "" {
		env.Timestamp = ts
	} else {
		env.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}

	// 6. Resolve Payload
	if p, ok := raw["payload"].(map[string]interface{}); ok {
		env.Payload = p
	} else {
		// Include arbitrary extra keys from top-level into payload
		for k, v := range raw {
			switch k {
			case "event", "hook", "type", "agent", "tool", "harness", "session_id", "sessionId", "session", "cwd", "workspace", "project_id", "projectId", "project", "timestamp", "time":
				continue
			default:
				env.Payload[k] = v
			}
		}
	}

	return env, nil
}

// ScrubSecrets applies comprehensive secret redaction to all strings in HookEnvelope.
func ScrubSecrets(text string) string {
	if text == "" {
		return ""
	}
	if h, err := os.UserHomeDir(); err == nil && h != "" && h != "/" {
		text = strings.ReplaceAll(text, h, "~")
	}
	text = homeUnixRE.ReplaceAllString(text, "~")
	text = homeWindowsRE.ReplaceAllString(text, "~")
	text = pemKeyRE.ReplaceAllString(text, "[REDACTED_PRIVATE_KEY]")
	text = ghTokenRE.ReplaceAllString(text, "[REDACTED_GH_TOKEN]")
	text = bearerRE.ReplaceAllString(text, "$1[REDACTED]")
	text = apiKeyRE.ReplaceAllString(text, "[REDACTED_API_KEY]")
	text = paramSecretRE.ReplaceAllString(text, "$1=[REDACTED]")
	text = awsKeyRE.ReplaceAllString(text, "[REDACTED_AWS_KEY]")
	text = jwtRE.ReplaceAllString(text, "[REDACTED_JWT]")
	return text
}

// ScrubEnvelope returns a copy of HookEnvelope with all string values sanitized.
func ScrubEnvelope(env *HookEnvelope) *HookEnvelope {
	if env == nil {
		return nil
	}
	clean := *env
	clean.Cwd = ScrubSecrets(env.Cwd)
	clean.SessionID = ScrubSecrets(env.SessionID)
	clean.Payload = scrubMap(env.Payload)
	return &clean
}

func scrubMap(m map[string]interface{}) map[string]interface{} {
	if m == nil {
		return nil
	}
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		cleanKey := ScrubSecrets(k)
		switch val := v.(type) {
		case string:
			out[cleanKey] = ScrubSecrets(val)
		case map[string]interface{}:
			out[cleanKey] = scrubMap(val)
		case []interface{}:
			cleanArr := make([]interface{}, len(val))
			for i, elem := range val {
				if s, ok := elem.(string); ok {
					cleanArr[i] = ScrubSecrets(s)
				} else if mElem, ok := elem.(map[string]interface{}); ok {
					cleanArr[i] = scrubMap(mElem)
				} else {
					cleanArr[i] = elem
				}
			}
			out[cleanKey] = cleanArr
		default:
			out[cleanKey] = v
		}
	}
	return out
}

func generateRandomID(prefix string) string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	return fmt.Sprintf("%s-%s", prefix, hex.EncodeToString(b))
}

// DefaultSpoolDir returns the standard local disk spool directory for Sila hooks.
func DefaultSpoolDir() string {
	home, _ := os.UserHomeDir()
	if home == "" {
		home = os.TempDir()
	}
	return filepath.Join(home, ".local", "share", "sila", "spool")
}
