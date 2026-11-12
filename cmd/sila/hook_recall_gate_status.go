package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jadmadi/sila/internal/config"
	"github.com/jadmadi/sila/internal/hookconfig"
	"github.com/jadmadi/sila/internal/loaders"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/session"
	"github.com/jadmadi/sila/internal/store"
)

// RecallGateStatus is the local operational summary exposed by hook status.
type RecallGateStatus struct {
	Policy         string `json:"policy"`
	StateDir       string `json:"state_dir"`
	Sessions       int    `json:"sessions"`
	DeniedTotal    int    `json:"denied_total"`
	RetriedTotal   int    `json:"retried_total"`
	BypassedTotal  int    `json:"bypassed_total"`
	FailOpenTotal  int    `json:"fail_open_total"`
	LastDecision   string `json:"last_decision"`
	LastDecisionAt string `json:"last_decision_at"`
}

type recallGateFailOpenTally struct {
	Total int    `json:"fail_open_total"`
	At    string `json:"last_fail_open_at,omitempty"`
}

func recordRecallGateFailOpen(projectDir string) {
	dir := RecallGateStateDir(projectDir)
	if dir == "" || ensureRecallGateStateDir(dir) != nil {
		return
	}
	path := filepath.Join(dir, recallGateFailOpenFile)
	tally := recallGateFailOpenTally{}
	if data, err := os.ReadFile(path); err == nil && len(data) <= recallGateStateLimit {
		_ = json.Unmarshal(data, &tally)
	}
	tally.Total++
	tally.At = recallGateNow()
	data, err := json.Marshal(tally)
	if err != nil || len(data) > recallGateStateLimit {
		return
	}
	tmp, err := os.CreateTemp(dir, ".fail-open-*.tmp")
	if err != nil {
		return
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return
	}
	if err := tmp.Close(); err != nil {
		return
	}
	_ = os.Rename(tmpName, path)
}

func readRecallGateFailOpenTotal(projectDir string) int {
	dir := RecallGateStateDir(projectDir)
	if dir == "" {
		return 0
	}
	data, err := os.ReadFile(filepath.Join(dir, recallGateFailOpenFile))
	if err != nil || len(data) > recallGateStateLimit {
		return 0
	}
	var tally recallGateFailOpenTally
	if json.Unmarshal(data, &tally) != nil {
		return 0
	}
	return tally.Total
}

// BuildRecallGateStatus sums content-free counters across session files.
func BuildRecallGateStatus(projectDir string, policy config.RecallGatePolicy) RecallGateStatus {
	status := RecallGateStatus{
		Policy:        string(policy),
		StateDir:      RecallGateStateDir(projectDir),
		FailOpenTotal: readRecallGateFailOpenTotal(projectDir),
	}
	dir := status.StateDir
	if dir == "" {
		return status
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return status
	}
	scanned := 0
	for _, entry := range entries {
		if scanned >= recallGateMaxScanFiles {
			break
		}
		if entry.IsDir() || !isRecallGateSessionFile(entry.Name()) {
			continue
		}
		scanned++
		state, err := loadRecallGateStateFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		status.Sessions++
		status.DeniedTotal += state.Denied
		status.RetriedTotal += state.Retried
		status.BypassedTotal += state.Bypassed
		if state.LastDecisionAt > status.LastDecisionAt {
			status.LastDecisionAt = state.LastDecisionAt
			status.LastDecision = state.LastDecision
		}
	}
	return status
}

// setRecallGateBypass records a per-session bypass. It is a counter, never a
// memory check.
func setRecallGateBypass(projectDir, sessionID string) error {
	state, err := LoadRecallGateState(projectDir, sessionID)
	if err != nil {
		state = newRecallGateState()
	}
	state.Bypassed++
	state.LastDecision = "bypass"
	state.LastDecisionAt = recallGateNow()
	return SaveRecallGateState(projectDir, sessionID, state)
}

// emitRecallGateDecision writes the harness-native decision. Unknown harnesses
// fail open with a stderr diagnostic; non-JSON host contracts never receive
// malformed stdout.

func appendRecallGateAuditSummary(projectDir, sessionID, agent, summary string) {
	if projectDir == "" {
		return
	}
	_ = loaders.AppendHookAudit(projectDir, loaders.HookAuditEntry{
		Event: "recall-gate", SessionID: sessionID, Agent: agent,
		Summary: boundedRecallGateText(summary),
	})
}

// isRecallGateSubagentPayload reports whether a tool call belongs to a
// subagent. Subagent calls receive guidance but are never held.

func runHookRecallGate(ui render.UI, project, sessionFlag, cwd string, jsonOut bool, args []string, opts hookOptions) {
	action := "status"
	sessionID := sessionFlag
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--json":
			jsonOut = true
		case strings.HasPrefix(arg, "--session="):
			sessionID = strings.TrimPrefix(arg, "--session=")
		case arg == "--session" && i+1 < len(args):
			sessionID = args[i+1]
			i++
		case strings.HasPrefix(arg, "-"):
			fmt.Fprintf(os.Stderr, "sila hook recall-gate: unknown flag %q\n", arg)
			os.Exit(2)
		default:
			action = strings.ToLower(arg)
		}
	}
	policy, err := config.ResolveRecallGatePolicy(opts.RecallPolicy, cwd, recallGateHome(), recallGateEnvMap())
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila hook recall-gate: %v\n", err)
		os.Exit(2)
	}
	switch action {
	case "status":
		status := BuildRecallGateStatus(cwd, policy)
		if jsonOut {
			emitJSON(status)
			return
		}
		fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · hook recall-gate")))
		fmt.Fprintf(os.Stdout, "  Policy: %s\n", ui.Bold(status.Policy))
		fmt.Fprintf(os.Stdout, "  State:  %s\n", ui.Dim(status.StateDir))
		fmt.Fprintf(os.Stdout, "  Sessions: %d · denied %d · retried %d · bypassed %d · fail-open %d\n",
			status.Sessions, status.DeniedTotal, status.RetriedTotal, status.BypassedTotal, status.FailOpenTotal)
		if status.LastDecision != "" {
			fmt.Fprintf(os.Stdout, "  Last decision: %s at %s\n", status.LastDecision, status.LastDecisionAt)
		}
		fmt.Fprintf(os.Stdout, "  %s switch with %s or %s\n",
			ui.Dim("tip:"), ui.Cyan("sila config set recall_gate_policy warn"), ui.Cyan("SILA_RECALL_GATE=off"))
	case "bypass":
		if strings.TrimSpace(sessionID) == "" {
			if id, idErr := session.ResolveIdentity("", project, cwd); idErr == nil {
				sessionID = id.SessionID
			}
		}
		if strings.TrimSpace(sessionID) == "" {
			fmt.Fprintln(os.Stderr, "sila hook recall-gate bypass: --session=<id> is required when no current session is recorded")
			os.Exit(2)
		}
		if err := setRecallGateBypass(cwd, sessionID); err != nil {
			fmt.Fprintf(os.Stderr, "sila hook recall-gate bypass: %v\n", err)
			os.Exit(1)
		}
		appendRecallGateAuditSummary(cwd, sessionID, "cli", "recall gate bypassed for this session")
		if jsonOut {
			emitJSON(map[string]any{"status": "bypassed", "session_id": sessionID})
			return
		}
		fmt.Fprintf(os.Stdout, "%s recall gate bypassed for this session (a counter, not a memory check)\n", ui.Green("✔"))
	default:
		fmt.Fprintf(os.Stderr, "sila hook recall-gate: unknown action %q (use status or bypass)\n", action)
		os.Exit(2)
	}
}

// recallGateDoctorCheck reports policy, installation, and state health.
func recallGateDoctorCheck(rep *store.DoctorReport, projectDir string) {
	if rep == nil {
		return
	}
	policy, err := config.ResolveRecallGatePolicy("", projectDir, recallGateHome(), recallGateEnvMap())
	if err != nil {
		rep.AddCheck("Hooks", "Recall Gate", store.StatusWarn, "The recall gate policy cannot be resolved.", err.Error(), "Fix recall_gate_policy in .sila/config.json or run `sila config set recall_gate_policy warn`.")
		rep.Finalize()
		return
	}
	status := BuildRecallGateStatus(projectDir, policy)
	detail := fmt.Sprintf("sessions=%d denied=%d retried=%d bypassed=%d fail_open=%d",
		status.Sessions, status.DeniedTotal, status.RetriedTotal, status.BypassedTotal, status.FailOpenTotal)
	if policy != config.RecallGateEnforce {
		rep.AddCheck("Hooks", "Recall Gate", store.StatusPass,
			fmt.Sprintf("Recall gate policy is %s (advisory; reads are never blocked).", policy), detail, "")
		rep.Finalize()
		return
	}
	hooks := hookconfig.StatusAll(projectDir)
	ready, readyHarnesses := hookconfig.RecallGateReady(hooks)
	if !ready {
		rep.AddCheck("Hooks", "Recall Gate", store.StatusWarn,
			"Recall gate is set to enforce but no installed harness hook can carry a pre-tool decision.",
			detail, "Run `sila hook install --harness=<name>` or switch to warn with `sila config set recall_gate_policy warn`.")
		rep.Finalize()
		return
	}
	rep.AddCheck("Hooks", "Recall Gate", store.StatusPass,
		"Recall gate is set to enforce and pre-tool hooks are installed: "+strings.Join(readyHarnesses, ", ")+".", detail, "")
	rep.Finalize()
}

// sortedRecallGateSessionFiles is a deterministic helper for tests and status.

func sortedRecallGateSessionFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, entry := range entries {
		if !entry.IsDir() && isRecallGateSessionFile(entry.Name()) {
			out = append(out, filepath.Join(dir, entry.Name()))
		}
	}
	sort.Strings(out)
	return out
}
