package sentry

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

// StopGateOptions configures the generic stop/session-end gate.
type StopGateOptions struct {
	Event          string // stop|session-end
	Policy         string // off|warn|enforce
	ProjectID      string
	SessionID      string
	GoalSlug       string
	Cwd            string
	Mutated        bool
	GoalBound      bool
	DocsOnly       bool
	StopHookActive bool
}

// StopGateResult is the sila/stop-gate/v1 contract.
type StopGateResult struct {
	Schema      string   `json:"schema"`
	Verdict     string   `json:"verdict"` // pass|warn|block|skip|degraded
	Policy      string   `json:"policy"`
	Event       string   `json:"event"`
	Missing     []string `json:"missing,omitempty"`
	Recovery    []string `json:"recovery,omitempty"`
	Bypassed    bool     `json:"bypassed,omitempty"`
	Diagnostics []string `json:"diagnostics,omitempty"`
	HandoffID   string   `json:"handoff_id,omitempty"`
}

// StopGateExitCode maps a result to the hook exit code.
func StopGateExitCode(res StopGateResult) int {
	switch res.Verdict {
	case "block":
		return 2
	case "degraded":
		return 3
	default:
		return 0
	}
}

// EvaluateStopGate evaluates one stop or session-end event. It never writes
// memory content and never blocks when the store is missing.
func EvaluateStopGate(ctx context.Context, st *store.Store, opts StopGateOptions) (StopGateResult, error) {
	policy := strings.ToLower(strings.TrimSpace(opts.Policy))
	if policy == "" {
		policy = "warn"
	}
	res := StopGateResult{Schema: "sila/stop-gate/v1", Policy: policy, Event: opts.Event}
	if opts.Cwd == "" {
		opts.Cwd = "."
	}
	if opts.Event == "" {
		opts.Event = "stop"
		res.Event = "stop"
	}

	if policy == "off" {
		res.Verdict = "skip"
		res.Diagnostics = append(res.Diagnostics, "policy off")
		return res, nil
	}
	if opts.StopHookActive {
		res.Verdict = "skip"
		res.Diagnostics = append(res.Diagnostics, "stop_hook_active: true")
		return res, nil
	}
	if strings.EqualFold(os.Getenv("SILA_STOP_GATE"), "off") {
		recordStopBypass(opts.Cwd, opts.SessionID)
		res.Verdict = "skip"
		res.Bypassed = true
		res.Diagnostics = append(res.Diagnostics, "SILA_STOP_GATE=off: one-session bypass recorded")
		return res, nil
	}
	if opts.DocsOnly {
		res.Verdict = "skip"
		res.Diagnostics = append(res.Diagnostics, "documentation-only session")
		return res, nil
	}
	if opts.Event == "stop" && !opts.Mutated && !opts.GoalBound {
		res.Verdict = "skip"
		res.Diagnostics = append(res.Diagnostics, "read-only stop")
		return res, nil
	}
	if opts.Event == "session-end" && !opts.Mutated && !opts.GoalBound {
		res.Verdict = "skip"
		res.Diagnostics = append(res.Diagnostics, "read-only session")
		return res, nil
	}
	if _, err := os.Stat(filepath.Join(opts.Cwd, "goals")); err != nil && st == nil {
		res.Verdict = "skip"
		res.Diagnostics = append(res.Diagnostics, "no goals directory and no store")
		return res, nil
	}
	if retries := stopRetryCount(opts.Cwd, opts.SessionID); retries >= 3 {
		res.Verdict = "warn"
		res.Diagnostics = append(res.Diagnostics, "retry_cap_reached: true")
		return res, nil
	}
	incrementStopRetry(opts.Cwd, opts.SessionID)

	// A checked-in waiver covers one event, including a handoff-specific waiver.
	if waiver, code, detail := loadStopWaiver(opts); code != "" {
		res.Diagnostics = append(res.Diagnostics, code+": "+detail)
		res.Missing = append(res.Missing, "waiver")
		res = finishStopGate(res, policy, nil, "", detail)
		return res, nil
	} else if waiver {
		res.Verdict = "pass"
		res.Diagnostics = append(res.Diagnostics, "waived: "+detail)
		return res, nil
	}

	if st == nil {
		dbPath := store.DefaultDBPath()
		if _, err := os.Stat(dbPath); err != nil {
			res.Verdict = "skip"
			res.Diagnostics = append(res.Diagnostics, "no knowledge store in "+dbPath)
			return res, nil
		}
		s, err := store.OpenRO(dbPath)
		if err != nil {
			res.Verdict = "degraded"
			res.Diagnostics = append(res.Diagnostics, "MEMORY_STORE_UNAVAILABLE: "+err.Error())
			return res, nil
		}
		defer s.Close()
		st = s
	}

	projectID := opts.ProjectID
	if p, err := st.ResolveScope("", opts.Cwd); err == nil && p.ID != "" {
		projectID = p.ID
	}
	if projectID == "" {
		projectID = store.ProjectID(opts.Cwd)
	}
	eval, err := EvaluateMemoryWrites(ctx, st, MemoryWriteScope{
		ProjectID:   projectID,
		SessionID:   opts.SessionID,
		GoalSlug:    opts.GoalSlug,
		ActiveGoal:  opts.GoalSlug,
		WindowStart: time.Now().UTC().Add(-48 * time.Hour),
		Files:       changedCodeFiles(opts.Cwd),
	})
	if err != nil {
		res.Verdict = "degraded"
		res.Diagnostics = append(res.Diagnostics, err.Error())
		return res, nil
	}
	res.Diagnostics = append(res.Diagnostics, eval.Diagnostics...)
	if eval.Grounded == 0 {
		res.Missing = append(res.Missing, "write")
	}
	handoffID := ""
	if opts.Event == "session-end" {
		for _, id := range uniqueIDs(opts.ProjectID, projectID) {
			if h, err := st.LatestHandoff(id); err == nil && h != nil {
				handoffID = h.ID
				break
			}
		}
		if handoffID == "" {
			res.Missing = append(res.Missing, "handoff")
		}
	}
	res.HandoffID = handoffID
	return finishStopGate(res, policy, eval.RecoveryCommands(opts.Cwd), handoffID, ""), nil
}

// RecoveryCommands returns the exact recovery commands for missing evidence.
func (r MemoryWriteResult) RecoveryCommands(files ...string) []string {
	file := "."
	if len(files) > 0 && files[0] != "" {
		file = files[0]
	}
	return []string{
		fmt.Sprintf("sila memo \"<decision>\" --files=%s", file),
		fmt.Sprintf("sila lesson \"<symptom>\" --cause=... --fix=... --files=%s", file),
		"add .sila/stop-waiver.json",
	}
}

func finishStopGate(res StopGateResult, policy string, recovery []string, handoffID, _ string) StopGateResult {
	res.Recovery = recovery
	res.HandoffID = handoffID
	if len(res.Missing) == 0 && len(res.Diagnostics) == 0 {
		res.Verdict = "pass"
		return res
	}
	if len(res.Missing) == 0 {
		res.Verdict = "pass"
		return res
	}
	if policy == "enforce" {
		res.Verdict = "block"
	} else {
		res.Verdict = "warn"
	}
	return res
}

func loadStopWaiver(opts StopGateOptions) (bool, string, string) {
	path := filepath.Join(opts.Cwd, ".sila", "stop-waiver.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return false, "", ""
	}
	var waiver struct {
		Schema    string `json:"schema"`
		SessionID string `json:"session_id"`
		Goal      string `json:"goal"`
		Reason    string `json:"reason"`
		ExpiresAt string `json:"expires_at"`
		Handoff   bool   `json:"handoff"`
	}
	if err := json.Unmarshal(data, &waiver); err != nil {
		return false, "MEMORY_WAIVER_INVALID", "waiver is not valid JSON"
	}
	if waiver.Schema != "sila/stop-waiver/v1" || len(strings.TrimSpace(waiver.Reason)) < 20 {
		return false, "MEMORY_WAIVER_INVALID", "waiver schema or reason is invalid"
	}
	if waiver.SessionID != opts.SessionID && waiver.SessionID != "*" {
		return false, "MEMORY_WAIVER_SESSION_MISMATCH", "waiver session does not match"
	}
	expires, err := time.Parse(time.RFC3339, waiver.ExpiresAt)
	if err != nil || time.Now().After(expires) {
		return false, "MEMORY_WAIVER_EXPIRED", "waiver has expired"
	}
	if time.Until(expires) > 24*time.Hour {
		return false, "MEMORY_WAIVER_INVALID", "waiver expires more than 24 hours in the future"
	}
	return true, "", waiver.Reason
}

func stopRetryCount(cwd, sessionID string) int {
	if sessionID == "" {
		sessionID = "unknown"
	}
	var state struct {
		Retries int `json:"retries"`
	}
	data, err := os.ReadFile(filepath.Join(cwd, ".sila", "state", "stop", sessionID+".json"))
	if err != nil || json.Unmarshal(data, &state) != nil {
		return 0
	}
	return state.Retries
}

func incrementStopRetry(cwd, sessionID string) {
	if sessionID == "" {
		sessionID = "unknown"
	}
	dir := filepath.Join(cwd, ".sila", "state", "stop")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	path := filepath.Join(dir, sessionID+".json")
	state := struct {
		SessionID string `json:"session_id"`
		Retries   int    `json:"retries"`
		At        string `json:"at"`
	}{SessionID: sessionID, Retries: stopRetryCount(cwd, sessionID) + 1, At: time.Now().UTC().Format(time.RFC3339)}
	data, _ := json.Marshal(state)
	_ = os.WriteFile(path, data, 0o600)
}

func recordStopBypass(cwd, sessionID string) {
	dir := filepath.Join(cwd, ".sila", "state", "stop")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	line, _ := json.Marshal(map[string]string{
		"schema":     "sila/stop-bypass/v1",
		"session_id": sessionID,
		"at":         time.Now().UTC().Format(time.RFC3339),
		"reason":     "env",
	})
	f, err := os.OpenFile(filepath.Join(dir, "bypass.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

// HasCodeChanges reports whether git status contains a code-bearing path.
func HasCodeChanges(cwd string) bool {
	return len(changedCodeFiles(cwd)) > 0
}

func uniqueIDs(ids ...string) []string {
	seen := map[string]bool{}
	var out []string
	for _, id := range ids {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func changedCodeFiles(cwd string) []string {
	out, err := exec.Command("git", "-C", cwd, "status", "--porcelain").Output()
	if err != nil {
		return nil
	}
	var files []string
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if len(line) < 4 {
			continue
		}
		path := strings.TrimSpace(line[3:])
		if idx := strings.Index(path, " -> "); idx >= 0 {
			path = strings.TrimSpace(path[idx+4:])
		}
		if path == "" || strings.HasSuffix(path, ".md") || strings.HasPrefix(path, "goals/") || strings.HasPrefix(path, ".sila/") {
			continue
		}
		files = append(files, path)
	}
	return files
}
