package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/config"
	"github.com/jadmadi/sila/internal/harness/contract"
	"github.com/jadmadi/sila/internal/harness/registry"
	"github.com/jadmadi/sila/internal/loaders"
	"github.com/jadmadi/sila/internal/store"
)

// The recall gate is a local, fail-open behavioral nudge for pre-tool-use
// hooks. It is not a security boundary. It never reads transcripts, command
// output, file bodies, prompts, or secrets. It observes only the hook
// envelope and content-free session counters.
const (
	recallGateStateVersion    = 1
	recallGateStateLimit      = 512
	recallGateStateTTL        = 14 * 24 * time.Hour
	recallGateCleanupBudget   = 32
	recallGateMaxScanFiles    = 1024
	recallGateSignatureDomain = "sila/recall-gate/signature/v1"
	recallGateFailOpenFile    = "fail-open.json"
	recallGateReasonNoCheck   = "no recall observed since the current user prompt"
	recallGateRetryHint       = "the same call may be retried once without a recall"
	recallGateRecovery        = `sila recall "<symptom>"`
)

// RecallGateDecision is one gate verdict.
type RecallGateDecision struct {
	Decision   string `json:"decision"` // allow | warn | deny
	ReasonCode string `json:"reason_code"`
	Reason     string `json:"reason,omitempty"`
	RetryHint  string `json:"retry_hint,omitempty"`
	FailOpen   bool   `json:"fail_open,omitempty"`
}

// RecallGateState is the bounded, content-free per-session state.
type RecallGateState struct {
	V               int    `json:"v"`
	Prompts         int    `json:"prompts"`
	LastCheckPrompt int    `json:"last_check_prompt"`
	Checks          int    `json:"checks"`
	Denied          int    `json:"denied"`
	Retried         int    `json:"retried"`
	Bypassed        int    `json:"bypassed"`
	LastSignature   string `json:"last_signature,omitempty"`
	LastDecision    string `json:"last_decision,omitempty"`
	LastDecisionAt  string `json:"last_decision_at,omitempty"`
}

type recallGateStateError struct {
	Code string
	Err  error
}

func (e *recallGateStateError) Error() string {
	if e == nil || e.Err == nil {
		return "recall gate state error"
	}
	return e.Code + ": " + e.Err.Error()
}

func (e *recallGateStateError) Unwrap() error { return e.Err }

func recallGateErr(code string, err error) error {
	return &recallGateStateError{Code: code, Err: err}
}

// RecallGateStateDir is the private per-project state directory.
func RecallGateStateDir(projectDir string) string {
	if strings.TrimSpace(projectDir) == "" {
		return ""
	}
	return filepath.Join(projectDir, ".sila", "state", "recall")
}

// RecallGateStatePath returns the hashed session state path. The session id is
// never stored on disk in the filename or body.
func RecallGateStatePath(projectDir, sessionID string) (string, error) {
	dir := RecallGateStateDir(projectDir)
	if dir == "" {
		return "", recallGateErr("NO_PROJECT", errors.New("project directory is required"))
	}
	if strings.TrimSpace(sessionID) == "" {
		return "", recallGateErr("NO_SESSION", errors.New("session id is required"))
	}
	sum := sha256.Sum256([]byte(sessionID))
	return filepath.Join(dir, hex.EncodeToString(sum[:])+".json"), nil
}

func newRecallGateState() *RecallGateState {
	return &RecallGateState{V: recallGateStateVersion}
}

// LoadRecallGateState reads one session state. A missing or expired file starts
// from zero. Corrupt, oversized, symlinked, or non-regular files are errors so
// the caller can fail open.
func LoadRecallGateState(projectDir, sessionID string) (*RecallGateState, error) {
	path, err := RecallGateStatePath(projectDir, sessionID)
	if err != nil {
		return nil, err
	}
	return loadRecallGateStateFile(path)
}

func loadRecallGateStateFile(path string) (*RecallGateState, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return newRecallGateState(), nil
		}
		return nil, recallGateErr("READ", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, recallGateErr("SYMLINK", errors.New("state path is a symlink"))
	}
	if !info.Mode().IsRegular() {
		return nil, recallGateErr("NOT_REGULAR", errors.New("state path is not a regular file"))
	}
	if info.Size() > recallGateStateLimit {
		return nil, recallGateErr("OVERSIZED", fmt.Errorf("state file is %d bytes (limit %d)", info.Size(), recallGateStateLimit))
	}
	if time.Since(info.ModTime()) > recallGateStateTTL {
		_ = os.Remove(path)
		return newRecallGateState(), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, recallGateErr("READ", err)
	}
	state := newRecallGateState()
	if err := json.Unmarshal(data, state); err != nil {
		return nil, recallGateErr("PARSE", err)
	}
	if state.V != recallGateStateVersion {
		return nil, recallGateErr("VERSION", fmt.Errorf("unsupported state version %d", state.V))
	}
	state.normalize()
	return state, nil
}

func (s *RecallGateState) normalize() {
	if s == nil {
		return
	}
	for _, value := range []*int{&s.Prompts, &s.LastCheckPrompt, &s.Checks, &s.Denied, &s.Retried, &s.Bypassed} {
		if *value < 0 {
			*value = 0
		}
	}
	if s.LastCheckPrompt > s.Prompts {
		s.LastCheckPrompt = s.Prompts
	}
}

// SaveRecallGateState writes the state atomically with mode 0600. It also
// opportunistically removes expired sibling state files, bounded per write.
func SaveRecallGateState(projectDir, sessionID string, state *RecallGateState) error {
	if state == nil {
		return recallGateErr("EMPTY", errors.New("state is required"))
	}
	path, err := RecallGateStatePath(projectDir, sessionID)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := ensureRecallGateStateDir(dir); err != nil {
		return err
	}
	state.V = recallGateStateVersion
	state.normalize()
	data, err := json.Marshal(state)
	if err != nil {
		return recallGateErr("ENCODE", err)
	}
	if len(data) > recallGateStateLimit {
		return recallGateErr("OVERSIZED", fmt.Errorf("encoded state is %d bytes (limit %d)", len(data), recallGateStateLimit))
	}
	tmp, err := os.CreateTemp(dir, ".recall-*.tmp")
	if err != nil {
		return recallGateErr("TEMP", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return recallGateErr("CHMOD", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return recallGateErr("WRITE", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return recallGateErr("SYNC", err)
	}
	if err := tmp.Close(); err != nil {
		return recallGateErr("CLOSE", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return recallGateErr("RENAME", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return recallGateErr("CHMOD", err)
	}
	cleanupRecallGateStates(dir, time.Now().UTC())
	return nil
}

func ensureRecallGateStateDir(dir string) error {
	info, err := os.Lstat(dir)
	switch {
	case err == nil && info.Mode()&os.ModeSymlink != 0:
		return recallGateErr("SYMLINK_DIR", errors.New("state directory is a symlink"))
	case err == nil && !info.IsDir():
		return recallGateErr("NOT_DIR", errors.New("state path is not a directory"))
	case err != nil && !os.IsNotExist(err):
		return recallGateErr("STAT_DIR", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return recallGateErr("MKDIR", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return recallGateErr("CHMOD_DIR", err)
	}
	return nil
}

func cleanupRecallGateStates(dir string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	removed := 0
	for _, entry := range entries {
		if removed >= recallGateCleanupBudget {
			return
		}
		if entry.IsDir() || !isRecallGateSessionFile(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) <= recallGateStateTTL {
			continue
		}
		if os.Remove(filepath.Join(dir, entry.Name())) == nil {
			removed++
		}
	}
}

func isRecallGateSessionFile(name string) bool {
	base := strings.TrimSuffix(name, ".json")
	if base == name || len(base) != 64 {
		return false
	}
	_, err := hex.DecodeString(base)
	return err == nil
}

// recallGateSignature is a salted SHA-256 of the tool name and a bounded,
// normalized identity. Raw tool arguments never leave the process.
func recallGateSignature(sessionID, toolName string, payload map[string]any) string {
	identity := recallGateIdentity(toolName, payload)
	if len(identity) > 256 {
		identity = identity[:256]
	}
	h := sha256.New()
	h.Write([]byte(recallGateSignatureDomain))
	h.Write([]byte{0})
	h.Write([]byte(sessionID))
	h.Write([]byte{0})
	h.Write([]byte(strings.ToLower(strings.TrimSpace(toolName))))
	h.Write([]byte{0})
	h.Write([]byte(identity))
	return hex.EncodeToString(h.Sum(nil))
}

// recallGateIdentity extracts only the bounded identity the gate needs: a file
// path for edit tools or a command for shell tools. It is hashed immediately.
func recallGateIdentity(toolName string, payload map[string]any) string {
	key := recallGateToolKey(toolName)
	if isRecallGateShellTool(key) {
		return boundedRecallGateText(recallGateCommand(payload))
	}
	for _, source := range recallGatePayloadMaps(payload) {
		if text := hookString(source, "file_path", "filePath", "path", "notebook_path", "notebookPath", "file"); text != "" {
			return boundedRecallGateText(text)
		}
	}
	return ""
}

func boundedRecallGateText(text string) string {
	return strings.TrimSpace(strings.Join(strings.Fields(text), " "))
}

func recallGatePayloadMaps(payload map[string]any) []map[string]any {
	out := []map[string]any{payload}
	for _, key := range []string{"tool_input", "toolInput", "arguments", "input", "args", "params"} {
		switch value := payload[key].(type) {
		case map[string]any:
			out = append(out, value)
		case string:
			var nested map[string]any
			if json.Unmarshal([]byte(value), &nested) == nil {
				out = append(out, nested)
			}
		}
	}
	for _, key := range []string{"toolCall", "tool_call"} {
		nested, ok := payload[key].(map[string]any)
		if !ok {
			continue
		}
		out = append(out, nested)
		if args, ok := nested["args"].(map[string]any); ok {
			out = append(out, args)
		}
	}
	return out
}

// recallGateToolName extracts the tool identity from the documented hook
// payload shapes without inspecting results or content.
func recallGateToolName(payload map[string]any) string {
	if payload == nil {
		return ""
	}
	if name := hookString(payload, "tool_name", "toolName", "tool", "name"); name != "" {
		return name
	}
	for _, key := range []string{"toolCall", "tool_call"} {
		if nested, ok := payload[key].(map[string]any); ok {
			if name := hookString(nested, "name", "tool_name", "toolName", "tool"); name != "" {
				return name
			}
		}
	}
	return ""
}

// recallGateCommand extracts a shell command from the documented envelopes.
func recallGateCommand(payload map[string]any) string {
	if payload == nil {
		return ""
	}
	if command := hookString(payload, "command", "CommandLine", "command_line", "cmd"); command != "" {
		return command
	}
	for _, source := range recallGatePayloadMaps(payload) {
		if command := hookString(source, "command", "CommandLine", "command_line", "cmd"); command != "" {
			return command
		}
	}
	return ""
}

func recallGateToolKey(toolName string) string {
	key := strings.ToLower(strings.TrimSpace(toolName))
	key = strings.ReplaceAll(key, "-", "_")
	key = strings.ReplaceAll(key, ".", "_")
	if idx := strings.LastIndex(key, "/"); idx >= 0 {
		key = key[idx+1:]
	}
	return key
}

func isRecallGateShellTool(key string) bool {
	switch key {
	case "bash", "shell", "sh", "run_command", "runcommand", "run_shell_command",
		"shell_command", "execute", "exec", "terminal", "bashcommand":
		return true
	default:
		return false
	}
}

// DetectRecallInvocation reports whether the observed tool call is a Sila
// recall check. An observed attempt counts even if the check found nothing or
// failed; the gate never inspects result content.
func DetectRecallInvocation(toolName string, payload map[string]any) bool {
	if isRecallGateMCPRecall(toolName) {
		return true
	}
	if !isRecallGateShellTool(recallGateToolKey(toolName)) && strings.TrimSpace(toolName) != "" {
		return false
	}
	return isRecallGateCLIRecall(recallGateCommand(payload))
}

func isRecallGateMCPRecall(toolName string) bool {
	key := strings.ToLower(strings.TrimSpace(toolName))
	key = strings.ReplaceAll(key, "__", "_")
	key = strings.ReplaceAll(key, "-", "_")
	key = strings.ReplaceAll(key, ".", "_")
	for {
		switch {
		case strings.HasPrefix(key, "mcp_"):
			key = strings.TrimPrefix(key, "mcp_")
		case strings.HasPrefix(key, "sila_"):
			key = strings.TrimPrefix(key, "sila_")
		default:
			return key == "recall" || key == "omni_search"
		}
	}
}

func isRecallGateCLIRecall(command string) bool {
	for _, segment := range recallGateShellSegments(command) {
		tokens := recallGateShellTokens(segment)
		if len(tokens) < 2 {
			continue
		}
		if !recallGateIsSilaBinary(tokens[0]) {
			continue
		}
		sub := strings.ToLower(tokens[1])
		switch sub {
		case "recall":
			return true
		case "search":
			for _, arg := range tokens[2:] {
				arg = strings.Trim(arg, `"'`)
				if arg == "--omni" || strings.HasPrefix(arg, "--omni=") {
					return true
				}
			}
		}
	}
	return false
}

func recallGateIsSilaBinary(token string) bool {
	base := strings.ToLower(filepath.Base(strings.Trim(token, `"'`)))
	base = strings.TrimSuffix(base, ".exe")
	return base == "sila"
}

// recallGateShellSegments splits a command on shell separators. It is
// intentionally simple and never executes anything.
func recallGateShellSegments(command string) []string {
	command = stripQuotedShellSpans(command)
	replacer := strings.NewReplacer("&&", "\n", "||", "\n", ";", "\n", "|", "\n", "\n", "\n")
	return strings.Split(replacer.Replace(command), "\n")
}

func recallGateShellTokens(segment string) []string {
	fields := strings.Fields(segment)
	for len(fields) > 0 {
		token := strings.Trim(fields[0], `"'`)
		if strings.Contains(token, "=") && !strings.HasPrefix(token, "-") {
			fields = fields[1:]
			continue
		}
		switch token {
		case "sudo", "command", "time", "env", "nohup":
			fields = fields[1:]
			continue
		}
		break
	}
	return fields
}

// ClassifyMutatingAction reports whether a tool call can write to the
// workspace. Unknown tools are never mutating, so they are never denied.
func ClassifyMutatingAction(toolName string, payload map[string]any) bool {
	key := recallGateToolKey(toolName)
	switch key {
	case "edit", "write", "multiedit", "multi_edit", "notebookedit", "notebook_edit":
		return true
	}
	if !isRecallGateShellTool(key) {
		return false
	}
	return recallGateBashMutating(recallGateCommand(payload))
}

func recallGateBashMutating(command string) bool {
	if strings.TrimSpace(command) == "" {
		return false
	}
	for _, segment := range recallGateShellSegments(command) {
		if recallGateShellSegmentMutating(segment) {
			return true
		}
	}
	return false
}

func recallGateShellSegmentMutating(segment string) bool {
	segment = stripQuotedShellSpans(segment)
	tokens := recallGateShellTokens(segment)
	if len(tokens) == 0 {
		return false
	}
	if strings.Contains(segment, ">") || strings.Contains(segment, "<<") {
		return true
	}
	first := strings.ToLower(filepath.Base(strings.Trim(tokens[0], `"'`)))
	switch first {
	case "ls", "pwd", "cat", "head", "tail", "grep", "rg", "find", "wc", "file",
		"stat", "tree", "du", "df", "echo", "printf", "which", "type", "printenv",
		"date", "uname", "whoami", "id", "cd", "sleep", "test", "[", "true", "false",
		"awk", "sort", "uniq", "cut", "tr", "diff", "comm", "basename", "dirname",
		"realpath", "readlink", "jq", "yq":
		if first == "find" {
			for _, arg := range tokens[1:] {
				if arg == "-delete" || arg == "-exec" || arg == "-execdir" || arg == "-ok" {
					return true
				}
			}
		}
		return false
	case "sed":
		return recallGateHasFlag(tokens[1:], "-i", "--in-place")
	case "perl":
		return recallGateHasFlag(tokens[1:], "-i")
	case "gofmt", "goimports":
		return recallGateHasFlag(tokens[1:], "-w")
	case "git":
		return recallGateGitMutating(tokens[1:])
	case "go":
		if len(tokens) < 2 {
			return false
		}
		switch strings.ToLower(tokens[1]) {
		case "test", "vet", "build", "doc", "list", "env", "version":
			return false
		default:
			return true
		}
	case "npm", "pnpm", "yarn", "aube", "aubx", "bun":
		if len(tokens) < 2 {
			return false
		}
		switch strings.ToLower(tokens[1]) {
		case "test", "run", "exec", "x":
			return false
		default:
			return true
		}
	case "curl", "wget":
		return recallGateHasFlag(tokens[1:], "-o", "-O", "--output", "--output-document")
	default:
		return true
	}
}

func recallGateHasFlag(args []string, flags ...string) bool {
	for _, arg := range args {
		for _, flag := range flags {
			if arg == flag || strings.HasPrefix(arg, flag+"=") || (strings.HasPrefix(flag, "-") && !strings.HasPrefix(flag, "--") && strings.HasPrefix(arg, flag)) {
				return true
			}
		}
	}
	return false
}

func recallGateGitMutating(args []string) bool {
	if len(args) == 0 {
		return false
	}
	sub := strings.ToLower(args[0])
	rest := args[1:]
	switch sub {
	case "status", "diff", "log", "show", "blame", "rev-parse", "ls-files", "ls-tree",
		"describe", "shortlog", "reflog", "grep", "whatchanged", "cat-file", "name-rev":
		return false
	case "branch":
		for _, arg := range rest {
			lower := strings.ToLower(arg)
			if lower == "-d" || lower == "-D" || lower == "--delete" || lower == "-m" || lower == "-M" ||
				lower == "--move" || lower == "-c" || lower == "--copy" || lower == "-f" || lower == "--force" {
				return true
			}
		}
		return false
	case "remote":
		if len(rest) == 0 {
			return false
		}
		switch strings.ToLower(rest[0]) {
		case "add", "remove", "rm", "rename", "set-url", "set-head", "prune", "update":
			return true
		default:
			return false
		}
	case "config":
		for _, arg := range rest {
			if arg == "--get" || arg == "--get-all" || arg == "-l" || arg == "--list" {
				return false
			}
		}
		return true
	case "tag":
		if len(rest) == 0 {
			return false
		}
		for _, arg := range rest {
			if arg == "-l" || arg == "--list" || arg == "-n" {
				return false
			}
		}
		return true
	case "stash":
		if len(rest) > 0 {
			switch strings.ToLower(rest[0]) {
			case "list", "show":
				return false
			}
		}
		return true
	default:
		return true
	}
}

// EvaluateRecallGate is the pure decision state machine. It mutates only
// counters and the bounded signature marker; it performs no I/O.
func EvaluateRecallGate(state *RecallGateState, policy config.RecallGatePolicy, mutating, checked bool, signature string) RecallGateDecision {
	if state == nil {
		return RecallGateDecision{Decision: "allow", ReasonCode: "FAIL_OPEN", FailOpen: true}
	}
	switch policy {
	case config.RecallGateOff:
		return RecallGateDecision{Decision: "allow", ReasonCode: "OFF"}
	case config.RecallGateEnforce:
		if !mutating {
			return RecallGateDecision{Decision: "allow", ReasonCode: "READ_ONLY"}
		}
		if checked {
			state.LastCheckPrompt = state.Prompts
			return RecallGateDecision{Decision: "allow", ReasonCode: "ALLOW"}
		}
		if state.Bypassed > 0 {
			return RecallGateDecision{Decision: "allow", ReasonCode: "BYPASS"}
		}
		if signature == "" {
			return RecallGateDecision{Decision: "allow", ReasonCode: "ALLOW"}
		}
		if state.Prompts <= state.LastCheckPrompt {
			return RecallGateDecision{Decision: "allow", ReasonCode: "ALLOW"}
		}
		if state.LastSignature == signature {
			if state.LastDecision == "deny" {
				state.Retried++
				state.LastDecision = "retry"
				state.LastDecisionAt = recallGateNow()
				return RecallGateDecision{Decision: "allow", ReasonCode: "RETRY"}
			}
			return RecallGateDecision{Decision: "allow", ReasonCode: "ALLOW"}
		}
		state.Denied++
		state.LastSignature = signature
		state.LastDecision = "deny"
		state.LastDecisionAt = recallGateNow()
		return RecallGateDecision{
			Decision:   "deny",
			ReasonCode: "NO_CHECK",
			Reason:     recallGateReasonNoCheck + "; run " + recallGateRecovery + " (or MCP recall)",
			RetryHint:  recallGateRetryHint,
		}
	case config.RecallGateWarn:
		if !mutating {
			return RecallGateDecision{Decision: "allow", ReasonCode: "READ_ONLY"}
		}
		if checked {
			state.LastCheckPrompt = state.Prompts
			return RecallGateDecision{Decision: "allow", ReasonCode: "ALLOW"}
		}
		if state.Bypassed > 0 {
			return RecallGateDecision{Decision: "allow", ReasonCode: "BYPASS"}
		}
		if state.Prompts > state.LastCheckPrompt && state.LastSignature != recallGateWarnMarker(state.Prompts) {
			state.LastSignature = recallGateWarnMarker(state.Prompts)
			state.LastDecision = "warn"
			state.LastDecisionAt = recallGateNow()
			return RecallGateDecision{
				Decision:   "warn",
				ReasonCode: "NO_CHECK",
				Reason:     recallGateReasonNoCheck + "; run " + recallGateRecovery + " (or MCP recall)",
				RetryHint:  recallGateRetryHint,
			}
		}
		return RecallGateDecision{Decision: "allow", ReasonCode: "ALLOW"}
	default:
		return RecallGateDecision{Decision: "allow", ReasonCode: "OFF"}
	}
}

func recallGateNow() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// recallGateWarnMarker binds an advisory warning to one prompt without storing
// any prompt content, so warn fires at most once per prompt.
func recallGateWarnMarker(prompt int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|warn|%d", recallGateSignatureDomain, prompt)))
	return hex.EncodeToString(sum[:])
}

func recallGateStateChanged(before, after *RecallGateState) bool {
	if before == nil || after == nil {
		return true
	}
	return before.Prompts != after.Prompts ||
		before.LastCheckPrompt != after.LastCheckPrompt ||
		before.Checks != after.Checks ||
		before.Denied != after.Denied ||
		before.Retried != after.Retried ||
		before.Bypassed != after.Bypassed ||
		before.LastSignature != after.LastSignature ||
		before.LastDecision != after.LastDecision ||
		before.LastDecisionAt != after.LastDecisionAt
}

func cloneRecallGateState(state *RecallGateState) *RecallGateState {
	if state == nil {
		return nil
	}
	copy := *state
	return &copy
}

// recallGateEnvMap snapshots the process environment for the resolver.
func recallGateEnvMap() map[string]string {
	env := make(map[string]string)
	for _, entry := range os.Environ() {
		if idx := strings.IndexByte(entry, '='); idx > 0 {
			env[entry[:idx]] = entry[idx+1:]
		}
	}
	return env
}

// resolveRecallGatePolicyForHook resolves the policy for a hook invocation. Any
// error degrades to warn with one bounded diagnostic, because a malformed
// configuration must never block a user's tool call.
func resolveRecallGatePolicyForHook(opts hookOptions, projectDir string) config.RecallGatePolicy {
	if opts.NoRecallGate {
		return config.RecallGateOff
	}
	policy, err := config.ResolveRecallGatePolicy(opts.RecallPolicy, projectDir, recallGateHome(), recallGateEnvMap())
	if err == nil {
		return policy
	}
	fmt.Fprintf(os.Stderr, "sila recall gate: %v; using warn\n", err)
	return config.RecallGateWarn
}

func recallGateHome() string {
	home, _ := os.UserHomeDir()
	return home
}

// recordRecallGatePrompt arms the gate for the current user prompt. It clears
// only the signature marker; historical counters are preserved.
func recordRecallGatePrompt(env *loaders.HookEnvelope, opts hookOptions) {
	if env == nil || strings.TrimSpace(env.SessionID) == "" {
		return
	}
	policy := resolveRecallGatePolicyForHook(opts, env.Cwd)
	if policy == config.RecallGateOff {
		return
	}
	state, err := LoadRecallGateState(env.Cwd, env.SessionID)
	if err != nil {
		recordRecallGateFailOpen(env.Cwd)
		return
	}
	state.Prompts++
	state.LastSignature = ""
	state.LastDecision = "prompt"
	state.LastDecisionAt = recallGateNow()
	if err := SaveRecallGateState(env.Cwd, env.SessionID, state); err != nil {
		recordRecallGateFailOpen(env.Cwd)
	}
}

// recordRecallGateSessionStart arms a fresh session so the first mutating call
// is covered even when a host omits the user-prompt event.
func recordRecallGateSessionStart(env *loaders.HookEnvelope, opts hookOptions) {
	if env == nil || strings.TrimSpace(env.SessionID) == "" {
		return
	}
	policy := resolveRecallGatePolicyForHook(opts, env.Cwd)
	if policy == config.RecallGateOff {
		return
	}
	state, err := LoadRecallGateState(env.Cwd, env.SessionID)
	if err != nil {
		recordRecallGateFailOpen(env.Cwd)
		return
	}
	if state.Prompts > 0 {
		return
	}
	state.Prompts = 1
	state.LastDecision = "session-start"
	state.LastDecisionAt = recallGateNow()
	if err := SaveRecallGateState(env.Cwd, env.SessionID, state); err != nil {
		recordRecallGateFailOpen(env.Cwd)
	}
}

// applyRecallGate loads state, records an observed recall, evaluates the
// decision, and persists changes. Every failure returns allow with fail_open.
func applyRecallGate(env *loaders.HookEnvelope, policy config.RecallGatePolicy) RecallGateDecision {
	if env == nil {
		return RecallGateDecision{Decision: "allow", ReasonCode: "FAIL_OPEN", FailOpen: true}
	}
	if policy == config.RecallGateOff {
		return RecallGateDecision{Decision: "allow", ReasonCode: "OFF"}
	}
	if isRecallGateSubagentPayload(env.Payload) {
		return RecallGateDecision{Decision: "allow", ReasonCode: "SUBAGENT"}
	}
	if strings.TrimSpace(env.Cwd) == "" || strings.TrimSpace(env.SessionID) == "" {
		fmt.Fprintln(os.Stderr, "sila recall gate: missing project or session; allowing the tool call")
		return RecallGateDecision{Decision: "allow", ReasonCode: "FAIL_OPEN", FailOpen: true}
	}
	state, err := LoadRecallGateState(env.Cwd, env.SessionID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila recall gate: state unavailable (%s); allowing the tool call\n", recallGateErrorCode(err))
		recordRecallGateFailOpen(env.Cwd)
		return RecallGateDecision{Decision: "allow", ReasonCode: "FAIL_OPEN", FailOpen: true}
	}
	before := cloneRecallGateState(state)

	toolName := recallGateToolName(env.Payload)
	checked := DetectRecallInvocation(toolName, env.Payload)
	if checked {
		state.Checks++
		state.LastCheckPrompt = state.Prompts
		state.LastDecision = "recall"
		state.LastDecisionAt = recallGateNow()
	}
	mutating := ClassifyMutatingAction(toolName, env.Payload)
	signature := recallGateSignature(env.SessionID, toolName, env.Payload)
	decision := EvaluateRecallGate(state, policy, mutating, checked, signature)
	if recallGateStateChanged(before, state) {
		if err := SaveRecallGateState(env.Cwd, env.SessionID, state); err != nil {
			fmt.Fprintf(os.Stderr, "sila recall gate: state write failed (%s); allowing the tool call\n", recallGateErrorCode(err))
			recordRecallGateFailOpen(env.Cwd)
			return RecallGateDecision{Decision: "allow", ReasonCode: "FAIL_OPEN", FailOpen: true}
		}
	}
	if decision.Reason != "" && env.ProjectID != "" {
		decision.Reason = "project " + env.ProjectID + ": " + decision.Reason
	}
	return decision
}

func recallGateErrorCode(err error) string {
	var typed *recallGateStateError
	if errors.As(err, &typed) {
		return typed.Code
	}
	return "ERROR"
}

func emitRecallGateDecision(env *loaders.HookEnvelope, decision RecallGateDecision) bool {
	if decision.Decision == "allow" || decision.Decision == "" {
		return false
	}
	if env == nil {
		return false
	}
	sp, ok := registry.Lookup(env.Agent)
	if !ok {
		fmt.Fprintf(os.Stderr, "sila recall gate: %s (%s); harness %q cannot carry a native decision, allowing the call\n",
			decision.Reason, decision.ReasonCode, env.Agent)
		return true
	}
	verdict := contract.DecisionAllow
	if decision.Decision == "deny" {
		verdict = contract.DecisionDeny
	} else if decision.Decision == "warn" {
		verdict = contract.DecisionWarn
	}
	out, stderr, _, err := registry.EncodeDecision(sp.Output, contract.PhaseToolBefore, contract.Decision{
		Schema:     contract.DecisionSchema,
		Decision:   verdict,
		ReasonCode: decision.ReasonCode,
		Reason:     decision.Reason,
		Recovery:   recallGateRecovery,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila recall gate: encode decision: %v; allowing the call\n", err)
		return true
	}
	if len(out) > 0 {
		_, _ = os.Stdout.Write(out)
	}
	if stderr != "" {
		fmt.Fprintln(os.Stderr, stderr)
	}
	return true
}

// appendRecallGateAudit records one bounded decision line. It never echoes
// prompts, commands, paths, or memory content.
func appendRecallGateAudit(env *loaders.HookEnvelope, decision RecallGateDecision) {
	if env == nil || env.Cwd == "" {
		return
	}
	summary := "recall gate " + decision.Decision + ": " + decision.ReasonCode
	if decision.FailOpen {
		summary = "recall gate fail open: " + decision.ReasonCode
	}
	_ = loaders.AppendHookAudit(env.Cwd, loaders.HookAuditEntry{
		Event: "recall-gate", SessionID: env.SessionID, Agent: env.Agent,
		Summary: boundedRecallGateText(summary),
	})
}

// appendRecallGateAuditSummary is used by status/bypass commands outside the
// hook envelope path.
func isRecallGateSubagentPayload(payload map[string]any) bool {
	if payload == nil {
		return false
	}
	if hookString(payload, "parent_session_id", "parentSessionId", "subagent_id", "subagentId") != "" {
		return true
	}
	for _, key := range []string{"is_subagent", "isSubagent", "subagent"} {
		if value, ok := payload[key].(bool); ok && value {
			return true
		}
	}
	return false
}

// writeRecallGateSubagentContext emits the bounded subagent recall reminder.
// Subagent mutations receive guidance but are never denied, because some hosts
// cannot reliably retry a held subagent call.
func writeRecallGateSubagentContext(env *loaders.HookEnvelope, opts hookOptions) {
	if env == nil {
		return
	}
	if resolveRecallGatePolicyForHook(opts, env.Cwd) == config.RecallGateOff {
		return
	}
	project := strings.TrimSpace(env.ProjectID)
	if project == "" && strings.TrimSpace(env.Cwd) != "" {
		project = store.ProjectID(env.Cwd)
	}
	if project == "" {
		project = "unbound"
	}
	fmt.Fprintf(os.Stderr, "sila recall gate: project %s; subagent calls are never held. Run %s before mutating files.\n",
		project, recallGateRecovery)
}
