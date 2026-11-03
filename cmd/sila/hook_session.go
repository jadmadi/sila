package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"time"

	"github.com/jadmadi/sila/internal/dream"
	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/harness/registry"
	"github.com/jadmadi/sila/internal/hookconfig"
	"github.com/jadmadi/sila/internal/loaders"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/sentry"
	"github.com/jadmadi/sila/internal/session"
	"github.com/jadmadi/sila/internal/store"
)

func parseHookBudget(raw string) int {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return 0
	}
	mult := 1
	if strings.HasSuffix(raw, "k") {
		mult = 1024
		raw = strings.TrimSuffix(raw, "k")
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0
	}
	return n * mult
}

func runHookSessionStart(ui render.UI, st *store.Store, project, cwd, sessionID, harnessAgent string, budget int, jsonOut bool) (receipt loaders.PrimeReceipt) {
	started := time.Now()
	receipt = loaders.PrimeReceipt{
		Status:          loaders.PrimeStatusFailed,
		ErrorClass:      "unknown",
		StartedAt:       started,
		Source:          loaders.PrimeSourceSessionStart,
		ContractVersion: hookconfig.MemoryContractVersion,
	}
	defer func() {
		receipt.DurationMS = time.Since(started).Milliseconds()
	}()
	id, err := session.ResolveIdentity(sessionID, project, cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila hook session-start: %v\n", err)
		os.Exit(1)
	}
	if id.SessionID == "" {
		id.SessionID = session.GenerateSessionID()
		id.Source = "generated"
	}
	if id.Harness == "" {
		id.Harness = "hook"
	}
	if id.Agent == "" {
		id.Agent = "hook"
	}
	if harnessAgent != "" && (id.Harness == "hook" || id.Harness == "") {
		id.Harness = harnessAgent
	}
	if harnessAgent != "" && (id.Agent == "hook" || id.Agent == "") {
		id.Agent = harnessAgent
	}
	sessionID = id.SessionID
	receipt.SessionID = sessionID
	if goal := os.Getenv("SILA_ACTIVE_GOAL"); goal != "" {
		root := cwd
		if claim, claimErr := readGoalClaim(root, goal); claimErr == nil && claim != nil && sameSessionClaim(claim, id.SessionID, id.Agent) {
			if cl, focusErr := goals.FocusGoal(root, goal, id.Agent, "", id.SessionID, false); focusErr == nil {
				id.ActiveGoal = goal
				id.ClaimID = claimIdentifier(cl)
			}
		} else if !jsonOut {
			fmt.Fprintf(os.Stdout, "session: goal %q is not bound; run sila goals claim %s --agent=%s --session=%s\n", goal, goal, id.Agent, id.SessionID)
		}
	}
	gitCtx, contextDrift := sessionContext(st, id.SessionID, cwd)
	if err := st.UpsertAgentSession(store.AgentSession{
		SessionID:       id.SessionID,
		ProjectID:       project,
		Harness:         id.Harness,
		Agent:           id.Agent,
		ParentSessionID: id.ParentSessionID,
		ActiveGoal:      id.ActiveGoal,
		ClaimID:         id.ClaimID,
		Cwd:             cwd,
		PID:             os.Getpid(),
		Source:          id.Source,
		Branch:          gitCtx.Branch,
		Head:            gitCtx.Head,
		Detached:        gitCtx.Detached,
		ContextDrift:    contextDrift,
		ContextDriftAt:  contextDriftTime(contextDrift),
	}); err != nil {
		fmt.Fprintf(os.Stderr, "sila hook session-start: record session: %v\n", err)
		os.Exit(1)
	}
	if err := session.WriteCurrent(cwd, id); err != nil {
		fmt.Fprintf(os.Stderr, "sila hook session-start: write current pointer: %v\n", err)
	}

	failPrime := func(class string, cause error) loaders.PrimeReceipt {
		receipt.Status = loaders.PrimeStatusFailed
		receipt.ErrorClass = class
		if cause != nil {
			receipt.Error = cause.Error()
		}
		receipt.DurationMS = time.Since(started).Milliseconds()
		recordMemoryEvent(st, project, id.Agent, "prime", "failed", "", "hook", 0)
		recordPrimeEvent(st, project, sessionID, id.Harness, id.Agent, cwd, receipt)
		_ = loaders.AppendHookAudit(cwd, loaders.HookAuditEntry{
			Event: "session-start", DurationMS: time.Since(started).Milliseconds(), ExitCode: 1,
			SessionID: sessionID, Agent: id.Agent, Summary: "prime failed; fail-open: " + class,
		})
		return receipt
	}

	if os.Getenv("SILA_PRIME_FAIL") == "1" {
		return failPrime("brief_build", fmt.Errorf("injected prime failure"))
	}

	brief, err := dream.BuildSessionStartBrief(st, project, cwd, sessionID, budget)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila hook session-start: %v\n", err)
		return failPrime("brief_build", err)
	}
	receipt.Status = loaders.PrimeStatusOK
	receipt.ErrorClass = ""
	receipt.Error = ""
	receipt.MemoryUnavailable = brief.MemoryUnavailable
	receipt.BriefBytes = brief.Bytes
	if receipt.BriefBytes <= 0 {
		receipt.BriefBytes = len(brief.Markdown)
	}

	if jsonOut {
		if isAntigravityAgent(harnessAgent) {
			if err := emitAntigravitySessionStart(brief.Markdown); err != nil {
				return failPrime("output_encode", err)
			}
		} else if err := emitSessionStartJSON(brief); err != nil {
			return failPrime("output_encode", err)
		}
	} else if _, err := fmt.Fprint(os.Stdout, brief.Markdown); err != nil {
		return failPrime("output_encode", err)
	}

	receipt.DurationMS = time.Since(started).Milliseconds()
	recordMemoryEvent(st, project, id.Agent, "prime", "ok", "", "hook", 0)
	recordPrimeEvent(st, project, sessionID, id.Harness, id.Agent, cwd, receipt)
	_ = loaders.AppendHookAudit(cwd, loaders.HookAuditEntry{
		Event: "session-start", DurationMS: time.Since(started).Milliseconds(), ExitCode: 0,
		SessionID: sessionID, Agent: id.Agent, Summary: "prime briefing emitted",
	})
	return receipt
}

// isAntigravityAgent reports whether the hook is running for Antigravity,
// whose hook stdout is parsed as a strict response object.
func isAntigravityAgent(agent string) bool {
	sp, ok := registry.Lookup(agent)
	return ok && sp.Output == registry.OutputAntigravity
}

// emitAntigravitySessionStart renders the session brief as the documented
// Antigravity SessionStart response. systemMessage must be a nested object.
func emitAntigravitySessionStart(markdown string) error {
	payload := map[string]any{
		"injectSteps": []any{
			map[string]any{
				"systemMessage": map[string]any{"systemMessage": markdown},
			},
		},
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(os.Stdout, string(b))
	return err
}

// emitSessionStartJSON preserves the normal JSON envelope while returning an
// encoding/write error so the prime receipt can record output_encode.
func emitSessionStartJSON(v any) error {
	if outModes.envelope {
		return render.EmitJSONSuccess(os.Stdout, v, nil, nil)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// recordPrimeEvent is best-effort: observability must never block a session.
func recordPrimeEvent(st *store.Store, project, sessionID, harness, agent, cwd string, receipt loaders.PrimeReceipt) {
	if st == nil {
		return
	}
	_ = st.RecordPrimeEvent(store.PrimeEvent{
		ProjectID:  project,
		SessionID:  sessionID,
		Harness:    harness,
		Agent:      agent,
		Source:     string(receipt.Source),
		Status:     string(receipt.Status),
		ErrorClass: receipt.ErrorClass,
		Reason:     sanitizePrimeError(receipt.Error, cwd),
		DurationMS: receipt.DurationMS,
		BriefBytes: receipt.BriefBytes,
		CreatedAt:  receipt.StartedAt,
	})
}

type sessionEndOptions struct {
	Event  string
	Policy string
}

// runHookSessionEnd guarantees a handoff and returns an exit code.
func runHookSessionEnd(ui render.UI, st *store.Store, project, cwd, sessionID, agent string, autoSync, autoHandoff, dryRun, jsonOut bool) int {
	return runHookSessionEndWithOptions(ui, st, project, cwd, sessionID, agent, autoSync, autoHandoff, dryRun, jsonOut, sessionEndOptions{})
}

func runHookSessionEndWithOptions(ui render.UI, st *store.Store, project, cwd, sessionID, agent string, autoSync, autoHandoff, dryRun, jsonOut bool, opts sessionEndOptions) (code int) {
	started := time.Now()
	defer func() {
		event := opts.Event
		if event == "" {
			event = "session-end"
		}
		_ = loaders.AppendHookAudit(cwd, loaders.HookAuditEntry{
			Event: event, DurationMS: time.Since(started).Milliseconds(), ExitCode: code,
			SessionID: sessionID, Agent: agent, Summary: "session close",
		})
	}()
	if id, idErr := session.ResolveIdentity(sessionID, project, cwd); idErr == nil && id.SessionID != "" {
		sessionID = id.SessionID
		_ = st.CloseAgentSession(id.SessionID, time.Now().UTC())
		_ = session.ClearCurrent(cwd, project, id.SessionID)
	}
	res, err := dream.RunSessionEndHook(st, project, cwd, autoSync)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila hook session-end: %v\n", err)
		return 1
	}
	res.AutoHandoff = autoHandoff
	if !autoHandoff {
		line := `memory: no handoff recorded; run sila handoff "<summary>" --open="..." or pass --auto-handoff`
		if jsonOut {
			if isAntigravityAgent(agent) {
				fmt.Fprintln(os.Stdout, `{"decision":"allow"}`)
			} else {
				emitJSON(map[string]any{"error": line, "status": "refused", "auto_handoff": false})
			}
		} else {
			fmt.Fprintln(os.Stderr, line)
		}
		return 2
	}

	auto, err := dream.BuildAutoHandoff(st, res.ProjectID, cwd, sessionID, "", dryRun)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila hook session-end: %v\n", err)
		return 1
	}
	if auto != nil {
		res.HandoffID = auto.HandoffID
		res.HandoffWritten = auto.Written
		res.HandoffExisting = auto.Existing
	}

	event, policy := "session-end", "warn"
	if opts.Event != "" {
		event = opts.Event
	}
	if opts.Policy != "" {
		policy = opts.Policy
	}
	gate, gateErr := sentry.EvaluateStopGate(context.Background(), st, sentry.StopGateOptions{
		Event:     event,
		Policy:    policy,
		ProjectID: res.ProjectID,
		SessionID: sessionID,
		GoalSlug:  strings.TrimSpace(os.Getenv("SILA_ACTIVE_GOAL")),
		Cwd:       cwd,
		Mutated:   sentry.HasCodeChanges(cwd),
		GoalBound: strings.TrimSpace(os.Getenv("SILA_ACTIVE_GOAL")) != "",
	})
	if gateErr != nil {
		gate = sentry.StopGateResult{
			Schema:      "sila/stop-gate/v1",
			Verdict:     "degraded",
			Policy:      policy,
			Event:       event,
			Diagnostics: []string{gateErr.Error()},
		}
	}

	if jsonOut {
		if isAntigravityAgent(agent) {
			// Antigravity Stop requires a decision field: "continue" blocks the
			// stop, any other value allows it.
			if gate.Verdict == "block" {
				payload := map[string]any{
					"decision": "continue",
					"reason":   strings.Join(gate.Recovery, " | "),
				}
				if b, err := json.Marshal(payload); err == nil {
					fmt.Fprintln(os.Stdout, string(b))
				}
			} else {
				fmt.Fprintln(os.Stdout, `{"decision":"allow"}`)
			}
			return sentry.StopGateExitCode(gate)
		}
		emitJSON(map[string]any{
			"schema":           gate.Schema,
			"verdict":          gate.Verdict,
			"policy":           gate.Policy,
			"event":            gate.Event,
			"missing":          gate.Missing,
			"recovery":         gate.Recovery,
			"bypassed":         gate.Bypassed,
			"diagnostics":      gate.Diagnostics,
			"project_id":       res.ProjectID,
			"summary_message":  res.SummaryMessage,
			"handoff_id":       res.HandoffID,
			"handoff_written":  res.HandoffWritten,
			"handoff_existing": res.HandoffExisting,
			"auto_handoff":     res.AutoHandoff,
		})
		return sentry.StopGateExitCode(gate)
	}
	fmt.Fprintln(os.Stdout, ui.Green("✔")+" "+res.SummaryMessage)
	if res.HandoffID != "" {
		state := "recorded"
		switch {
		case res.HandoffWritten:
			state = "written"
		case res.HandoffExisting:
			state = "existing"
		}
		fmt.Fprintf(os.Stdout, "  handoff: %s (%s)\n", res.HandoffID, state)
	}
	switch gate.Verdict {
	case "block":
		fmt.Fprintf(os.Stderr, "sila stop gate: missing %s; %s\n", strings.Join(gate.Missing, ", "), strings.Join(gate.Recovery, " | "))
	case "warn":
		fmt.Fprintf(os.Stdout, "  memory gate: %s\n", strings.Join(gate.Diagnostics, "; "))
	case "degraded":
		fmt.Fprintf(os.Stderr, "  memory gate degraded: %s\n", strings.Join(gate.Diagnostics, "; "))
	}
	return sentry.StopGateExitCode(gate)
}

// runHookFailureNudge emits prior resolutions for a failed tool call.
// runHookPreToolUse emits one bounded warning when a web search starts without
// a recall marker in the current session, and records the audit entry. It never
// blocks the tool call.
func runHookPreToolUse(ui render.UI, st *store.Store, env *loaders.HookEnvelope, jsonOut bool) {
	started := time.Now()
	detail := ""
	for _, key := range []string{"tool_name", "tool", "name", "command", "query"} {
		if v, ok := env.Payload[key]; ok && v != nil {
			detail += " " + fmt.Sprint(v)
		}
	}
	summary := "pre-tool-use"
	lower := strings.ToLower(detail)
	if strings.Contains(lower, "web_search") || strings.Contains(lower, "web search") || strings.Contains(lower, "search_web") {
		summary = "web search before recall"
		if !jsonOut {
			fmt.Fprintln(os.Stderr, "sila hook: web search before recall — run `sila recall \"<query>\"` for local prior art first (advisory, not blocking)")
		}
	}
	_ = loaders.AppendHookAudit(env.Cwd, loaders.HookAuditEntry{
		Event: "pre-tool-use", DurationMS: time.Since(started).Milliseconds(), ExitCode: 0,
		SessionID: env.SessionID, Agent: env.Agent, Summary: summary,
	})
	_ = st
	_ = ui
}

func runHookFailureNudge(ui render.UI, st *store.Store, env *loaders.HookEnvelope, jsonOut bool) bool {
	if env == nil || env.Event != loaders.EventPostToolUse {
		return false
	}
	nudge := dream.BuildFailureNudge(st, env.ProjectID, env.Payload, 3)
	if nudge == nil {
		return false
	}
	if jsonOut {
		if isAntigravityAgent(env.Agent) {
			// PostToolUse expects an empty JSON object; keep stdout clean and
			// surface the advisory nudge on stderr.
			fmt.Fprintf(os.Stderr, "%s recall before retrying: %s\n", ui.Yellow("↳"), nudge.Query)
			return true
		}
		emitJSON(nudge)
		return true
	}
	fmt.Fprintf(os.Stdout, "%s recall before retrying: %s\n", ui.Yellow("↳"), nudge.Query)
	for _, match := range nudge.Matches {
		fmt.Fprintf(os.Stdout, "  • %s\n", match)
	}
	if len(nudge.Matches) == 0 {
		fmt.Fprintln(os.Stdout, "  no prior match")
	}
	fmt.Fprintf(os.Stdout, "  %s\n", nudge.Hint)
	return true
}

// hookUseUserScope reports whether install/uninstall should target the
// user-scope config. ZCode ignores project hooks, so it defaults to user scope
// unless --scope=project was passed explicitly.
func hookUseUserScope(opts hookOptions) bool {
	if opts.User {
		return true
	}
	return !opts.UserExplicit && registry.NormalizeID(opts.Harness) == "zcode"
}

func hookInstallSummary(res hookconfig.Result, jsonOut bool) int {
	if jsonOut {
		emitJSON(res)
	} else {
		switch {
		case res.Refused:
			fmt.Fprintf(os.Stderr, "sila hook: %s: %s\n", res.Harness, res.Detail)
		case res.Skipped:
			fmt.Fprintf(os.Stdout, "✔ %s hooks already installed (%s)\n", res.Harness, res.Path)
		case res.Installed:
			fmt.Fprintf(os.Stdout, "✔ %s hooks installed (%s)\n", res.Harness, res.Path)
		default:
			fmt.Fprintf(os.Stdout, "%s\n", res.Detail)
		}
	}
	if res.Refused {
		return 2
	}
	return 0
}
