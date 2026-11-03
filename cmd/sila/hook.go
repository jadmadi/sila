package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/dream"
	"github.com/jadmadi/sila/internal/git"
	"github.com/jadmadi/sila/internal/harness/contract"
	"github.com/jadmadi/sila/internal/harness/registry"
	"github.com/jadmadi/sila/internal/hookconfig"
	"github.com/jadmadi/sila/internal/loaders"
	"github.com/jadmadi/sila/internal/onboard"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/session"
	"github.com/jadmadi/sila/internal/store"
	"github.com/jadmadi/sila/internal/version"
)

type hookOptions struct {
	Harness      string
	Replace      bool
	AutoHandoff  bool
	Budget       int
	DryRun       bool
	JSON         bool
	Policy       string
	User         bool
	UserExplicit bool
	Strict       bool
	Surface      string
	Trust        bool
}

// parseHookArgs accepts flags after the hook action, so both
// `sila hook --harness=claude install` and `sila hook install --harness=claude`
// work. Unknown tokens are preserved for the action.
func parseHookArgs(opts hookOptions, args []string) (hookOptions, []string) {
	var rest []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		next := ""
		if i+1 < len(args) {
			next = args[i+1]
		}
		switch {
		case strings.HasPrefix(arg, "--harness="):
			opts.Harness = strings.TrimPrefix(arg, "--harness=")
		case arg == "--harness" && next != "":
			opts.Harness = next
			i++
		case strings.HasPrefix(arg, "--budget="):
			opts.Budget = parseHookBudget(strings.TrimPrefix(arg, "--budget="))
		case arg == "--budget" && next != "":
			opts.Budget = parseHookBudget(next)
			i++
		case arg == "--dry-run":
			opts.DryRun = true
		case arg == "--replace":
			opts.Replace = true
		case arg == "--user" || arg == "--global" || arg == "-g":
			opts.User = true
			opts.UserExplicit = true
		case strings.HasPrefix(arg, "--scope="):
			switch strings.TrimPrefix(arg, "--scope=") {
			case "user", "global":
				opts.User = true
			case "project":
				opts.User = false
			}
			opts.UserExplicit = true
		case arg == "--no-auto-handoff":
			opts.AutoHandoff = false
		case arg == "--auto-handoff":
			opts.AutoHandoff = true
		case arg == "--json":
			opts.JSON = true
		case arg == "--strict":
			opts.Strict = true
		case arg == "--trust":
			opts.Trust = true
		case strings.HasPrefix(arg, "--surface="):
			opts.Surface = strings.TrimPrefix(arg, "--surface=")
		case arg == "--surface" && next != "":
			opts.Surface = next
			i++
		case arg == "--enforce":
			opts.Policy = "enforce"
		case strings.HasPrefix(arg, "--policy="):
			opts.Policy = strings.TrimPrefix(arg, "--policy=")
		case arg == "--policy" && next != "":
			opts.Policy = next
			i++
		default:
			rest = append(rest, arg)
		}
	}
	return opts, rest
}

func runHook(ui render.UI, dbPath, project, eventFlag, agentFlag, sessionFlag, cwdFlag, payloadFlag string, drainFlag, autoSync, jsonOut bool, top int, args []string) {
	runHookWithOptions(ui, dbPath, project, eventFlag, agentFlag, sessionFlag, cwdFlag, payloadFlag, drainFlag, autoSync, jsonOut, top, args, hookOptions{
		AutoHandoff: true,
	})
}

func runHookWithOptions(ui render.UI, dbPath, project, eventFlag, agentFlag, sessionFlag, cwdFlag, payloadFlag string, drainFlag, autoSync, jsonOut bool, top int, args []string, opts hookOptions) {
	opts, args = parseHookArgs(opts, args)
	if opts.JSON {
		jsonOut = true
	}
	spooler, err := loaders.NewSpooler("")
	if err != nil {
		if jsonOut {
			emitJSON(map[string]string{"error": err.Error()})
		} else {
			fmt.Fprintf(os.Stderr, "sila hook: %v\n", err)
		}
		os.Exit(1)
	}

	hookType := "session-start"
	explicitHookType := len(args) > 0
	if explicitHookType {
		hookType = strings.ToLower(args[0])
	}

	cwd := cwdFlag
	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	switch hookType {
	case "pre-commit":
		runGatePreCommit(ui, jsonOut, false, false, false, false)
		return
	case "pre-push":
		runGatePipelineCmd(ui, jsonOut, "", "origin", false)
		return
	case "uninstall":
		if opts.Harness == "" {
			fmt.Fprintln(os.Stderr, "sila hook uninstall: --harness=<name> is required")
			os.Exit(2)
		}
		var res hookconfig.Result
		var err error
		if hookUseUserScope(opts) {
			home, homeErr := os.UserHomeDir()
			if homeErr != nil {
				fmt.Fprintf(os.Stderr, "sila hook uninstall: resolve user home: %v\n", homeErr)
				os.Exit(1)
			}
			res, err = hookconfig.UninstallUser(opts.Harness, home, opts.DryRun)
		} else {
			res, err = hookconfig.Uninstall(opts.Harness, cwd, opts.DryRun)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila hook uninstall: %v\n", err)
			os.Exit(1)
		}
		os.Exit(hookInstallSummary(res, jsonOut))
	case "install":
		if opts.Harness != "" {
			trustBase := cwd
			if hookUseUserScope(opts) {
				if home, homeErr := os.UserHomeDir(); homeErr == nil {
					trustBase = home
				}
			}
			if sp, ok := registry.Lookup(opts.Harness); ok && sp.Config.TrustRequired &&
				!opts.Trust && !registry.TrustApproved(opts.Harness, trustBase) {
				msg := fmt.Sprintf("%s requires explicit trust; rerun with --trust to record the approval", opts.Harness)
				if jsonOut {
					emitJSON(map[string]any{"error": msg, "status": "refused", "trust_required": true})
				} else {
					fmt.Fprintf(os.Stderr, "sila hook install: %s\n", msg)
				}
				os.Exit(1)
			}
			var res hookconfig.Result
			var err error
			if hookUseUserScope(opts) {
				home, homeErr := os.UserHomeDir()
				if homeErr != nil {
					fmt.Fprintf(os.Stderr, "sila hook install: resolve user home: %v\n", homeErr)
					os.Exit(1)
				}
				res, err = hookconfig.InstallUserSurface(opts.Harness, home, opts.Surface, opts.DryRun, opts.Replace)
			} else {
				res, err = hookconfig.Install(opts.Harness, cwd, opts.DryRun, opts.Replace)
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "sila hook install: %v\n", err)
				os.Exit(1)
			}
			if opts.Trust {
				if sp, ok := registry.Lookup(opts.Harness); ok && sp.Config.TrustRequired {
					if err := registry.ApproveTrust(opts.Harness, trustBase); err != nil {
						fmt.Fprintf(os.Stderr, "sila hook install: record trust: %v\n", err)
						os.Exit(1)
					}
					res.Trusted = true
					res.Installed = true
				}
			}
			os.Exit(hookInstallSummary(res, jsonOut))
		}
		if !onboard.IsSilaSourceRepo(cwd) {
			msg := "sila's pre-commit hook runs 'sila gate', which checks the sila source repository; install your project's own hook instead"
			if jsonOut {
				emitJSON(map[string]any{"error": msg, "status": "refused"})
			} else {
				fmt.Fprintf(os.Stderr, "sila hook install: %s\n", msg)
			}
			os.Exit(1)
		}
		mgr, err := git.NewManager(cwd)
		if err != nil {
			if jsonOut {
				emitJSON(map[string]any{"error": err.Error()})
			} else {
				fmt.Fprintf(os.Stderr, "sila hook install: %v\n", err)
			}
			os.Exit(1)
		}
		h, err := mgr.InstallPreCommitHook()
		if errors.Is(err, git.ErrForeignHook) {
			msg := "existing pre-commit hook is not sila-managed; left untouched"
			if jsonOut {
				emitJSON(map[string]any{"error": msg, "status": "refused"})
			} else {
				fmt.Fprintf(os.Stderr, "sila hook install: %s\n", msg)
			}
			os.Exit(1)
		}
		if err != nil {
			if jsonOut {
				emitJSON(map[string]any{"error": err.Error()})
			} else {
				fmt.Fprintf(os.Stderr, "sila hook install: %v\n", err)
			}
			os.Exit(1)
		}
		if jsonOut {
			emitJSON(map[string]any{"installed": []string{h}, "status": "installed"})
			return
		}
		fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · hook install")))
		fmt.Fprintf(os.Stdout, "  %s installed hook: %s\n", ui.Green("✔"), ui.Dim(h))
		return
	case "drain":
		runHookDrain(ui, dbPath, project, spooler, jsonOut)
		return
	case "status":
		runHookStatus(ui, dbPath, spooler, cwd, jsonOut, opts.Strict)
		return
	case "serve":
		port := 7453
		if len(args) > 1 {
			if p, err := strconv.Atoi(args[1]); err == nil && p > 0 {
				port = p
			}
		}
		runHookServe(ui, dbPath, port, spooler, drainFlag)
		return
	}

	// 1. Check if stdin has piped data
	var stdinData []byte
	fi, err := os.Stdin.Stat()
	if err == nil && (fi.Mode()&os.ModeCharDevice) == 0 {
		lr := io.LimitReader(os.Stdin, int64(loaders.MaxHookPayloadBytes)+1)
		stdinData, _ = io.ReadAll(lr)
	}

	// If no piped data, and it's a traditional session-start or session-end/stop invocation
	if len(stdinData) == 0 && eventFlag == "" && payloadFlag == "" {
		switch hookType {
		case "session-end", "stop":
			if autoSync {
				runSync(ui, dbPath, project, false, false, false, false, true, false, "15s", time.Time{}, false, false, nil)
			}
			st, err := store.Open(dbPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "sila: %v\n", err)
				os.Exit(1)
			}
			defer st.Close()
			_ = spoolLifecycleEvent(spooler, loaders.NormalizeEventKind(hookType), agentFlag, sessionFlag, cwd, project, nil)
			code := runHookSessionEndWithOptions(ui, st, project, cwd, sessionFlag, agentFlag, autoSync, opts.AutoHandoff, opts.DryRun, jsonOut, sessionEndOptions{Event: hookType, Policy: opts.Policy})
			if code != 0 {
				os.Exit(code)
			}
			return

		case "session-start":
			st, err := store.Open(dbPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "sila: %v\n", err)
				os.Exit(1)
			}
			defer st.Close()
			_ = spoolLifecycleEvent(spooler, loaders.EventSessionStart, agentFlag, sessionFlag, cwd, project, nil)
			receipt := runHookSessionStart(ui, st, project, cwd, sessionFlag, agentFlag, opts.Budget, jsonOut)
			markerSession := receipt.SessionID
			if markerSession == "" {
				markerSession = sessionFlag
			}
			writePreflightMarkerForSession(cwd, project, markerSession, receipt)
			if code := primeExitCode(receipt, hookStrict(opts)); code != 0 {
				os.Exit(code)
			}
			return
		}
	}

	var env *loaders.HookEnvelope
	if len(stdinData) > 0 {
		if len(stdinData) > loaders.MaxHookPayloadBytes {
			fmt.Fprintf(os.Stderr, "sila hook: payload exceeds 16 KiB limit\n")
			os.Exit(1)
		}
		env, err = loaders.ParseHookEnvelope(stdinData)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila hook: parse stdin: %v\n", err)
			os.Exit(1)
		}
		if eventFlag != "" {
			env.Event = loaders.NormalizeEventKind(eventFlag)
		}
		if agentFlag != "" {
			env.Agent = agentFlag
		}
		if sessionFlag != "" {
			env.SessionID = sessionFlag
		}
		if project != "" {
			env.ProjectID = project
		}
		if cwdFlag != "" {
			env.Cwd = cwdFlag
		}
	} else {
		evKind := eventFlag
		if evKind == "" {
			evKind = hookType
		}
		projID := project
		if projID == "" && cwd != "" {
			projID = store.ProjectID(cwd)
		}
		agent := agentFlag
		if agent == "" {
			agent = "agent"
		}
		session := sessionFlag
		if session == "" {
			session = fmt.Sprintf("sess-%d", time.Now().UnixNano())
		}
		payload := make(map[string]interface{})
		if payloadFlag != "" {
			_ = json.Unmarshal([]byte(payloadFlag), &payload)
		}
		env = &loaders.HookEnvelope{
			Event:     loaders.NormalizeEventKind(evKind),
			Agent:     agent,
			ProjectID: projID,
			Cwd:       cwd,
			SessionID: session,
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
			Payload:   payload,
		}
	}

	// The installed hook command is authoritative for the phase: Antigravity
	// payloads (among others) omit the event name, so stdin alone cannot map it.
	if eventFlag == "" && explicitHookType {
		if kind := loaders.NormalizeEventKind(hookType); kind != loaders.EventOther {
			env.Event = kind
		}
	}

	if env.Event == loaders.EventSubagentStart || env.Event == loaders.EventSubagentStop {
		parentSession, _ := env.Payload["parent_session_id"].(string)
		subagentID, _ := env.Payload["subagent_id"].(string)
		if parentSession == "" || subagentID == "" {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]string{
				"status":  "unsupported",
				"command": "sila subagent charter --parent-session=<id> --subagent=<id> --goal=<slug>",
			})
			return
		}
		st, openErr := store.Open(dbPath)
		if openErr != nil {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]string{
				"status": "unsupported", "reason": openErr.Error(),
				"command": "sila subagent charter --parent-session=<id> --subagent=<id> --goal=<slug>",
			})
			return
		}
		defer st.Close()

		goalSlug, _ := env.Payload["goal"].(string)
		subagentGit := session.SnapshotGit(env.Cwd)
		sess := store.AgentSession{
			SessionID: subagentID, ProjectID: env.ProjectID, Harness: env.Agent,
			ParentSessionID: parentSession, ActiveGoal: goalSlug, Cwd: env.Cwd, Source: "hook",
			Branch: subagentGit.Branch, Head: subagentGit.Head, Detached: subagentGit.Detached,
		}
		if env.Event == loaders.EventSubagentStop {
			sess.ClosedAt = time.Now().UTC()
			_ = st.UpsertAgentSession(sess)
			_ = json.NewEncoder(os.Stdout).Encode(map[string]string{
				"status": "ok", "event": string(env.Event), "parent_session_id": parentSession, "subagent_id": subagentID,
			})
			return
		}

		out, brief, briefErr := buildSubagentBrief(st, env, parentSession, subagentID)
		event := store.PrimeEvent{
			ProjectID: env.ProjectID, SessionID: subagentID, Harness: env.Agent,
			Agent: env.Agent, Source: "subagent", Status: "ok",
		}
		if briefErr != nil {
			event.Status = "failed"
			event.ErrorClass = "brief_build"
			event.Reason = briefErr.Error()
			_ = st.RecordPrimeEvent(event)
			_ = json.NewEncoder(os.Stdout).Encode(map[string]string{
				"status": "unsupported", "reason": briefErr.Error(),
				"command": "sila subagent charter --parent-session=<id> --subagent=<id> --goal=<slug>",
			})
			return
		}
		event.ProjectID = brief.Project
		event.BriefBytes = brief.Bytes
		_ = st.RecordPrimeEvent(event)
		_ = st.UpsertAgentSession(sess)
		_, _ = os.Stdout.Write(append(out, '\n'))
		return
	}
	if env.Event == loaders.EventPreCompact {
		// Pre-compaction is a correlation point only; emit nothing and write
		// no store state. The post-compaction anchor does the work.
		recordHookAdoption(dbPath, env, string(env.Event))
		return
	}
	if env.Event == loaders.EventPostCompact {
		recordHookAdoption(dbPath, env, string(env.Event))
		runHookReanchor(ui, dbPath, cwd, env, opts.Budget, jsonOut)
		return
	}

	// Pre-tool-use is a synchronous gate: stdout must stay reserved for the
	// harness-native decision. Internal spool/summary text is never emitted.
	if env.Event == loaders.EventPreToolUse {
		if runPreflightGate(env, cwd) {
			return
		}
		if st, openErr := store.Open(dbPath); openErr == nil {
			runHookPreToolUse(ui, st, env, jsonOut)
			st.Close()
		}
		writeNativeHookAllow(env, contract.PhaseToolBefore)
		return
	}

	// Harness envelope path: a start/end/stop event must run the session
	// bridge, and a failed post-tool-use event gets a bounded recall nudge.
	if env.Event == loaders.EventSessionStart || env.Event == loaders.EventSessionEnd ||
		env.Event == loaders.EventStop || env.Event == loaders.EventPostToolUse {
		if st, openErr := store.Open(dbPath); openErr == nil {
			defer st.Close()
			envCwd := env.Cwd
			if envCwd == "" {
				envCwd = cwd
			}
			drain := func() {
				if drainFlag {
					_, _ = spooler.Drain(st, nil)
				}
			}
			switch env.Event {
			case loaders.EventPostToolUse:
				if runHookFailureNudge(ui, st, env, jsonOut) {
					_, _ = spooler.Spool(env)
					drain()
					return
				}
			case loaders.EventSessionStart:
				_, _ = spooler.Spool(env)
				drain()
				if isCompactionSessionStart(env) {
					runHookReanchor(ui, dbPath, envCwd, env, opts.Budget, jsonOut)
					return
				}
				receipt := runHookSessionStart(ui, st, env.ProjectID, envCwd, env.SessionID, env.Agent, opts.Budget, jsonOut)
				markerSession := receipt.SessionID
				if markerSession == "" {
					markerSession = env.SessionID
				}
				writePreflightMarkerForSession(envCwd, env.ProjectID, markerSession, receipt)
				if code := primeExitCode(receipt, hookStrict(opts)); code != 0 {
					os.Exit(code)
				}
				return
			case loaders.EventSessionEnd, loaders.EventStop:
				if env.Event == loaders.EventStop && runGoalContractGate(ui, st, env, envCwd, jsonOut) {
					return
				}
				_, _ = spooler.Spool(env)
				drain()
				code := runHookSessionEndWithOptions(ui, st, env.ProjectID, envCwd, env.SessionID, env.Agent, autoSync, opts.AutoHandoff, opts.DryRun, jsonOut, sessionEndOptions{Event: string(env.Event), Policy: opts.Policy})
				if code != 0 {
					os.Exit(code)
				}
				return
			}
		}
	}

	spoolPath, err := spooler.Spool(env)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]string{"error": err.Error()})
		} else {
			fmt.Fprintf(os.Stderr, "sila hook: spool failed: %v\n", err)
		}
		os.Exit(1)
	}

	if drainFlag {
		st, err := store.Open(dbPath)
		if err == nil {
			defer st.Close()
			_, _ = spooler.Drain(st, nil)
		}
	}

	if jsonOut {
		summary := map[string]interface{}{
			"status":     "spooled",
			"event":      env.Event,
			"agent":      env.Agent,
			"session_id": env.SessionID,
			"project_id": env.ProjectID,
			"path":       spoolPath,
			"drained":    drainFlag,
		}
		// Harnesses parse hook stdout as their own response schema. When the
		// payload came from a harness (piped stdin), keep stdout clean and send
		// Sila's internal summary to stderr instead.
		if len(stdinData) > 0 {
			if b, err := json.Marshal(summary); err == nil {
				fmt.Fprintln(os.Stderr, string(b))
			}
			return
		}
		emitJSON(summary)
		return
	}

	if len(stdinData) > 0 {
		fmt.Fprintf(os.Stderr, "%s spooled lifecycle event %s [%s] (%s)\n",
			ui.Green("✔"), ui.Bold(string(env.Event)), ui.Cyan(env.Agent), env.SessionID)
		return
	}
	fmt.Fprintf(os.Stdout, "%s spooled lifecycle event %s [%s] (%s)\n",
		ui.Green("✔"), ui.Bold(string(env.Event)), ui.Cyan(env.Agent), env.SessionID)
}

// writeNativeHookAllow emits the harness-native allow response for hooks whose
// stdout is parsed as a decision object. Antigravity's PreToolUse requires an
// explicit `{"decision":"allow"}`; other harnesses treat empty stdout as allow.
func writeNativeHookAllow(env *loaders.HookEnvelope, phase contract.Phase) {
	sp, ok := registry.Lookup(env.Agent)
	if !ok || sp.Output != registry.OutputAntigravity {
		return
	}
	stdout, stderr, _, err := registry.EncodeDecision(sp.Output, phase, contract.Decision{
		Schema:   contract.DecisionSchema,
		Decision: contract.DecisionAllow,
	})
	if err != nil {
		return
	}
	if len(stdout) > 0 {
		_, _ = os.Stdout.Write(stdout)
	}
	if stderr != "" {
		fmt.Fprintln(os.Stderr, stderr)
	}
}

func spoolLifecycleEvent(spooler *loaders.Spooler, ev loaders.LifecycleEventKind, agent, session, cwd, project string, payload map[string]interface{}) error {
	if spooler == nil {
		return nil
	}
	if agent == "" {
		agent = "agent"
	}
	if session == "" {
		session = fmt.Sprintf("sess-%d", time.Now().UnixNano())
	}
	if project == "" && cwd != "" {
		project = store.ProjectID(cwd)
	}
	if payload == nil {
		payload = make(map[string]interface{})
	}
	env := &loaders.HookEnvelope{
		Event:     ev,
		Agent:     agent,
		ProjectID: project,
		Cwd:       cwd,
		SessionID: session,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Payload:   payload,
	}
	_, err := spooler.Spool(env)
	return err
}

func runHookDrain(ui render.UI, dbPath, project string, spooler *loaders.Spooler, jsonOut bool) {
	st, err := store.Open(dbPath)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]string{"error": err.Error()})
		} else {
			fmt.Fprintf(os.Stderr, "sila hook drain: open store: %v\n", err)
		}
		os.Exit(1)
	}
	defer st.Close()

	var projPtr *string
	if project != "" {
		projPtr = &project
	}

	report, err := spooler.Drain(st, projPtr)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]string{"error": err.Error()})
		} else {
			fmt.Fprintf(os.Stderr, "sila hook drain: %v\n", err)
		}
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(report)
		return
	}

	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · hook drain")))
	fmt.Fprintf(os.Stdout, "  %s %s: %d spooled events (%d new records, %d merged, %d errors)\n",
		ui.Green("✔"), ui.Bold("DRAIN COMPLETE"), report.Drained, report.NewRecords, report.Merged, len(report.Errors))
	for _, e := range report.Errors {
		fmt.Fprintf(os.Stderr, "    %s %s\n", ui.Red("✖"), e)
	}
}

func runHookStatus(ui render.UI, dbPath string, spooler *loaders.Spooler, projectDir string, jsonOut, strict bool) {
	count, err := spooler.PendingCount()
	if err != nil {
		if jsonOut {
			emitJSON(map[string]string{"error": err.Error()})
		} else {
			fmt.Fprintf(os.Stderr, "sila hook status: %v\n", err)
		}
		os.Exit(1)
	}

	hooks := hookconfig.StatusAll(projectDir)
	prime := primeStatusMap(dbPath)
	for id, summary := range prime {
		if st, ok := hooks[id]; ok {
			st.LastPrime = summary.Last
			st.PrimeCounts24h = summary.Counts
			hooks[id] = st
		}
	}
	if strict {
		for _, sp := range registry.All() {
			st := hooks[sp.ID]
			if st.Installed && !st.BinaryResolved {
				fmt.Fprintf(os.Stderr, "sila hook status: %s has no resolved hook binary; run sila hook install --harness=%s --replace\n", sp.ID, sp.ID)
				os.Exit(1)
			}
			for _, surface := range st.Surfaces {
				if surface.Detected && !surface.Installed {
					fmt.Fprintf(os.Stderr, "sila hook status: %s surface %s is detected but not installed\n", sp.ID, surface.Name)
					os.Exit(1)
				}
			}
		}
	}
	if jsonOut {
		emitJSON(map[string]interface{}{
			"pending_count":       count,
			"spool_dir":           spooler.SpoolDir(),
			"memory_contract":     hookconfig.MemoryContractVersion,
			"harness_hooks":       hooks,
			"harness_hooks_ready": anyHookInstalled(hooks),
			"coverage":            hookCoverage(hooks),
		})
		return
	}

	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · hook status")))
	fmt.Fprintf(os.Stdout, "  Queue directory: %s\n", ui.Dim(spooler.SpoolDir()))
	if count == 0 {
		fmt.Fprintf(os.Stdout, "  %s %s\n", ui.Green("✔"), ui.Bold("Spool queue is empty (0 pending events)"))
	} else {
		fmt.Fprintf(os.Stdout, "  %s %s pending spooled lifecycle events awaiting drain\n",
			ui.Yellow("▲"), ui.Bold(strconv.Itoa(count)))
		fmt.Fprintf(os.Stdout, "  %s run %s to persist events into the SQLite store.\n",
			ui.Dim("tip:"), ui.Cyan("sila hook drain"))
	}
	fmt.Fprintf(os.Stdout, "  Memory contract: %s\n", hookconfig.MemoryContractVersion)
	for _, sp := range registry.All() {
		harness := sp.ID
		status := hooks[harness]
		switch {
		case !status.Supported:
			fmt.Fprintf(os.Stdout, "  %s %s: %s\n", ui.Dim("–"), harness, "unsupported; manual wiring")
		case status.Installed:
			label := "installed"
			if status.Capabilities["plugin_bridge"] {
				label = "plugin_bridge installed"
			}
			if status.Scope == "user" {
				fmt.Fprintf(os.Stdout, "  %s %s: %s (user: %s)\n", ui.Green("✔"), harness, label, status.Path)
			} else {
				fmt.Fprintf(os.Stdout, "  %s %s: %s (%s)\n", ui.Green("✔"), harness, label, status.Path)
			}
		case status.Configured && !status.Trusted:
			fmt.Fprintf(os.Stdout, "  %s %s: trust pending (%s)\n", ui.Yellow("▲"), harness, status.Detail)
		case status.ProjectHooksIgnored:
			fmt.Fprintf(os.Stdout, "  %s %s: not installed; run `sila hook install --harness=%s --user` (%s)\n", ui.Yellow("▲"), harness, harness, status.Detail)
		default:
			fmt.Fprintf(os.Stdout, "  %s %s: not installed; run `sila hook install --harness=%s`\n", ui.Yellow("▲"), harness, harness)
		}
		if status.CompactionSupported {
			state := "not installed"
			if status.CompactionInstalled {
				state = "installed"
			}
			fmt.Fprintf(os.Stdout, "      compaction: %s (%s)\n", state, status.CompactionDetail)
		} else {
			fmt.Fprintf(os.Stdout, "      compaction: unsupported (%s)\n", status.CompactionDetail)
		}
	}
}

func anyHookInstalled(hooks map[string]hookconfig.Status) bool {
	for _, status := range hooks {
		if status.Installed {
			return true
		}
	}
	return false
}

func hookCoverage(hooks map[string]hookconfig.Status) map[string]int {
	out := map[string]int{"supported": 0, "configured": 0, "installed": 0, "trusted": 0}
	for _, status := range hooks {
		if status.Supported {
			out["supported"]++
		}
		if status.Configured {
			out["configured"]++
		}
		if status.Installed {
			out["installed"]++
		}
		if status.Trusted {
			out["trusted"]++
		}
	}
	return out
}

func runHookServe(ui render.UI, dbPath string, port int, spooler *loaders.Spooler, withDrain bool) {
	mux := http.NewServeMux()
	handler := loaders.NewHookHTTPHandler(spooler)
	mux.HandleFunc("/hook", handler)
	mux.HandleFunc("/api/hook", handler)

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	if withDrain {
		st, err := store.Open(dbPath)
		if err == nil {
			worker := loaders.NewDrainWorker(spooler, st, 2*time.Second)
			worker.Start(context.Background())
			defer worker.Stop()
			defer st.Close()
		}
	}

	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · hook serve"))+fmt.Sprintf(" listening on http://%s/hook", addr))
	fmt.Fprintln(os.Stdout, "  "+ui.Dim("Ready to ingest non-blocking agent lifecycle hooks (POST /hook, POST /api/hook). Press Ctrl+C to stop."))

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "sila hook serve: %v\n", err)
		os.Exit(1)
	}
}

// runHookReanchor emits the bounded post-compaction memory anchor. It is
// read-only except for the per-session dedupe marker and never writes a
// handoff, memo, lesson, case, fact, goal, or claim.
func runHookReanchor(ui render.UI, dbPath, cwd string, env *loaders.HookEnvelope, budget int, jsonOut bool) {
	projectDir := env.Cwd
	if projectDir == "" {
		projectDir = cwd
	}
	var st *store.Store
	if opened, err := store.OpenRO(dbPath); err == nil {
		st = opened
		defer st.Close()
	}

	brief, text, err := dream.BuildReanchorBrief(st, env.ProjectID, projectDir, env.SessionID, budget)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila hook: re-anchor: %v\n", err)
		os.Exit(1)
	}

	key := dream.ReanchorKey(env.SessionID, brief.Head, string(env.Event), reanchorSequence(env.Payload))
	markerPath := dream.ReanchorMarkerPath(projectDir, env.SessionID)
	marker, existing := readReanchorMarker(markerPath)
	count := 1
	if existing && marker.LastKey == key {
		if emitted, parseErr := time.Parse(time.RFC3339, marker.EmittedAt); parseErr == nil && time.Since(emitted) <= 5*time.Second {
			if marker.LastText != "" {
				text = marker.LastText
				brief.Bytes = len(text)
			}
			emitReanchor(ui, brief, text, jsonOut)
			return
		}
		count = marker.Count + 1
	}
	next := dream.ReanchorMarker{
		Schema:    "sila/reanchor-state/v1",
		LastKey:   key,
		EmittedAt: time.Now().UTC().Format(time.RFC3339),
		Count:     count,
		LastText:  text,
	}
	if err := writeReanchorMarker(markerPath, next); err != nil {
		fmt.Fprintf(os.Stderr, "sila hook: re-anchor marker: %v\n", err)
	}
	emitReanchor(ui, brief, text, jsonOut)
}

func emitReanchor(ui render.UI, brief *dream.ReanchorBrief, text string, jsonOut bool) {
	if jsonOut {
		emitJSON(struct {
			*dream.ReanchorBrief
			Text string `json:"text"`
		}{ReanchorBrief: brief, Text: text})
		return
	}
	fmt.Fprint(os.Stdout, text)
}

func reanchorSequence(payload map[string]interface{}) string {
	for _, key := range []string{"sequence", "trigger"} {
		if value, ok := payload[key]; ok && value != nil {
			return fmt.Sprint(value)
		}
	}
	return ""
}

func readReanchorMarker(path string) (dream.ReanchorMarker, bool) {
	var marker dream.ReanchorMarker
	if path == "" {
		return marker, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return marker, false
	}
	if err := json.Unmarshal(data, &marker); err != nil {
		return marker, false
	}
	return marker, true
}

func writeReanchorMarker(path string, marker dream.ReanchorMarker) error {
	if path == "" {
		return fmt.Errorf("no project directory for re-anchor marker")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(dir, 0o700)
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, ".reanchor-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// isCompactionSessionStart detects the documented SessionStart fallback used
// by harnesses that expose a compaction source instead of a PostCompact event.
func isCompactionSessionStart(env *loaders.HookEnvelope) bool {
	if env == nil {
		return false
	}
	for _, key := range []string{"source", "trigger", "reason"} {
		if value, ok := env.Payload[key].(string); ok {
			lower := strings.ToLower(strings.TrimSpace(value))
			if lower == "compact" || lower == "compaction" || strings.Contains(lower, "compact") {
				return true
			}
		}
	}
	return false
}

// writePreflightMarkerForSession records the truthful result of the session
// bridge. It is best-effort: a marker write failure never blocks the session.
func writePreflightMarkerForSession(projectDir, projectID, sessionID string, receipt loaders.PrimeReceipt) {
	if projectDir == "" || sessionID == "" {
		return
	}
	policy := loaders.ResolvePreflightPolicy(projectDir)
	primeStatus := receipt.Status
	if primeStatus == "" {
		primeStatus = loaders.PrimeStatusFailed
	}
	status := "ok"
	switch primeStatus {
	case loaders.PrimeStatusOK:
		status = "ok"
	case loaders.PrimeStatusFailed:
		status = "failed"
	case loaders.PrimeStatusBypassed:
		status = "bypassed"
	default:
		status = "degraded"
	}
	if primeStatus == loaders.PrimeStatusOK {
		receipt.ErrorClass = ""
		receipt.Error = ""
	}
	primeAt := receipt.StartedAt
	if primeAt.IsZero() {
		primeAt = time.Now().UTC()
	}
	source := receipt.Source
	if source == "" {
		source = loaders.PrimeSourceSessionStart
	}
	duration := receipt.DurationMS
	if duration == 0 && !receipt.StartedAt.IsZero() {
		duration = time.Since(receipt.StartedAt).Milliseconds()
	}
	marker := loaders.PreflightMarker{
		SchemaVersion: loaders.PreflightSchema,
		SessionID:     sessionID,
		Project:       projectID,
		ProjectDir:    projectDir,
		SilaVersion:   version.String(),
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		Policy:        policy,
		Status:        status,
		Checks: map[string]bool{
			"sila_present":   true,
			"session_bridge": true,
			"prime":          receipt.IsOK(),
			"goals_ready":    true,
			"rules_in_sync":  true,
		},
		PrimeStatus:       primeStatus,
		PrimeErrorClass:   receipt.ErrorClass,
		PrimeError:        sanitizePrimeError(receipt.Error, projectDir),
		PrimeAt:           primeAt.UTC().Format(time.RFC3339),
		PrimeDurationMS:   duration,
		PrimeSource:       source,
		BriefBytes:        receipt.BriefBytes,
		MemoryUnavailable: receipt.MemoryUnavailable,
		Warnings:          []string{},
	}
	marker.ContextSHA256 = loaders.PreflightContextHash(projectID, sessionID, policy, marker.Status)
	_, _ = loaders.WritePreflightMarker(projectDir, marker)
}

// sanitizePrimeError keeps the marker bounded and avoids leaking the user's
// home directory or a long transcript-like error.
func sanitizePrimeError(text, projectDir string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		text = strings.ReplaceAll(text, filepath.Clean(home), "~")
	}
	if projectDir != "" {
		text = strings.ReplaceAll(text, filepath.Clean(projectDir), ".")
	}
	if len(text) > 512 {
		text = text[:509] + "..."
	}
	return text
}

// hookStrict reports whether a failed prime should exit non-zero. The env var
// supports harnesses whose hook command cannot be edited to add --strict.
func hookStrict(opts hookOptions) bool {
	if opts.Strict {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("SILA_HOOK_STRICT"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// primeExitCode keeps fail-open as the default and makes strict opt-in.
func primeExitCode(receipt loaders.PrimeReceipt, strict bool) int {
	if strict && !receipt.IsOK() {
		return 3
	}
	return 0
}

func isCommandCodeAgent(agent string) bool {
	normalized := strings.ToLower(strings.TrimSpace(agent))
	return strings.Contains(normalized, "command-code") || strings.Contains(normalized, "commandcode") || strings.Contains(normalized, "command_code")
}

// runPreflightGate evaluates the Command Code preflight policy. It returns true
// when it already emitted a decision (allow with warning or deny), meaning the
// caller must not run the ordinary pre-tool advisory.
func runPreflightGate(env *loaders.HookEnvelope, projectDir string) bool {
	if env == nil || !isCommandCodeAgent(env.Agent) {
		return false
	}
	payloadString := func(keys ...string) string {
		for _, key := range keys {
			if value, ok := env.Payload[key]; ok && value != nil {
				if text, ok := value.(string); ok {
					return text
				}
				if encoded, err := json.Marshal(value); err == nil {
					return string(encoded)
				}
			}
		}
		return ""
	}
	sessionStart := time.Time{}
	if raw := payloadString("session_start", "sessionStart"); raw != "" {
		sessionStart, _ = time.Parse(time.RFC3339, raw)
	}
	decision := loaders.EvaluatePreflight(loaders.PreflightRequest{
		Policy:         loaders.ResolvePreflightPolicy(projectDir),
		SessionID:      env.SessionID,
		ProjectDir:     projectDir,
		ToolName:       payloadString("tool_name", "tool", "name"),
		ToolInput:      payloadString("tool_input", "toolInput", "command", "input"),
		PermissionMode: payloadString("permission_mode", "permissionMode"),
		SessionStart:   sessionStart,
		Bypass:         os.Getenv("SILA_PREFLIGHT_BYPASS") == "1",
	})
	if decision.Bypassed {
		_ = loaders.AppendHookAudit(projectDir, loaders.HookAuditEntry{
			Event: "preflight", SessionID: env.SessionID, Agent: env.Agent,
			Summary: "SILA_PREFLIGHT_BYPASS=1 bypassed the preflight gate",
		})
	}
	switch decision.Decision {
	case "deny":
		payload := map[string]string{
			"permissionDecision":       "deny",
			"permissionDecisionReason": decision.Reason,
		}
		if decision.RecoveryCommand != "" {
			payload["recovery_command"] = decision.RecoveryCommand
		}
		if decision.MarkerPath != "" {
			payload["marker_path"] = decision.MarkerPath
		}
		_ = json.NewEncoder(os.Stdout).Encode(payload)
		return true
	case "warn":
		if decision.Warning == "" {
			return false
		}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]string{
			"permissionDecision": "allow",
			"systemMessage":      decision.Warning,
		})
		return false
	default:
		if decision.Warning != "" {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]string{
				"permissionDecision": "allow",
				"systemMessage":      decision.Warning,
			})
		}
		return false
	}
}

// recordHookAdoption records one hook lifecycle event when the ledger is on.
func recordHookAdoption(dbPath string, env *loaders.HookEnvelope, event string) {
	if env == nil || strings.ToLower(strings.TrimSpace(os.Getenv("SILA_TOOL_LEDGER"))) == "off" {
		return
	}
	st, err := store.Open(dbPath)
	if err != nil {
		return
	}
	defer st.Close()
	project := env.ProjectID
	if project == "" && env.Cwd != "" {
		project = store.ProjectID(env.Cwd)
	}
	agent := env.Agent
	if agent == "" {
		agent = "hook"
	}
	_ = st.RecordToolEvent(store.ToolEvent{
		ProjectID: project, SessionID: env.SessionID, Agent: agent, Source: "hook",
		ToolName: event, Category: "hook", Outcome: "ok", CreatedAt: time.Now().UTC(),
	})
}

// runGoalContractGate evaluates the harness-neutral goal contract at Stop.
// It returns true when it emitted a blocking response and the caller must stop.
func runGoalContractGate(ui render.UI, st *store.Store, env *loaders.HookEnvelope, cwd string, jsonOut bool) bool {
	policy := contract.ResolvePolicy(cwd)
	if policy.Policy == contract.PolicyOff {
		return false
	}
	projectID := env.ProjectID
	if projectID == "" {
		projectID = store.ProjectID(cwd)
	}
	payload := env.Payload
	if active, ok := payload["stop_hook_active"].(bool); ok && active {
		return false
	}
	if strings.EqualFold(hookString(payload, "permission_mode", "mode"), "plan") {
		if !jsonOut {
			fmt.Fprintln(os.Stderr, "sila goal contract: plan mode skips Stop enforcement; `sila gate` still applies")
		}
		return false
	}
	goalSlug := hookString(payload, "sila_goal", "goal_slug", "goal")
	harnessGoal := hookString(payload, "harness_goal_id", "goal_id")
	parent := hookString(payload, "parent_session_id", "parentSessionId")
	sig := contract.NormalizeSignal(contract.Signal{
		Harness: contract.HarnessID(env.Agent), SessionID: env.SessionID,
		ParentSessionID: parent, ProjectID: projectID, Cwd: cwd,
		Phase: contract.PhaseStop, HarnessGoalID: harnessGoal,
		SilaGoalSlug: goalSlug, ClaimID: hookString(payload, "claim_id"),
	})
	d := contract.NewChecker(st, cwd).Evaluate(sig, policy)
	d = contract.RecordDecision(cwd, sig, policy, d, time.Duration(d.DurationMS)*time.Millisecond)
	if d.Decision == contract.DecisionDeny {
		writeContractStopDecision(env.Agent, d)
		return true
	}
	if d.Decision == contract.DecisionUnsupported {
		if !jsonOut {
			fmt.Fprintf(os.Stderr, "sila goal contract: harness %s cannot enforce this phase; run `sila gate`\n", env.Agent)
		}
		return false
	}
	if d.Decision == contract.DecisionWarn && !jsonOut {
		fmt.Fprintf(os.Stderr, "sila goal contract: %s\n", contract.Summary(d))
	}
	return false
}

func hookString(payload map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if v, ok := payload[k].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func writeContractStopDecision(agent string, d contract.Decision) {
	style := registry.OutputClaude
	if sp, ok := registry.Lookup(agent); ok {
		style = sp.Output
	}
	stdout, stderr, _, _ := registry.EncodeDecision(style, contract.PhaseStop, d)
	if len(stdout) > 0 {
		_, _ = os.Stdout.Write(stdout)
	}
	if stderr != "" {
		fmt.Fprintln(os.Stderr, stderr)
	}
}

func normalizeHarnessName(agent string) string {
	return registry.NormalizeID(agent)
}
