package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/session"
	"github.com/jadmadi/sila/internal/store"
)

// resolveCommandProjectScope resolves a canonical project ID across repository subdirectories
// so session, lesson, case, memo, and handoff share one unified project_id.
func resolveCommandProjectScope(st *store.Store, projectFlag, cwd string) (string, error) {
	req := strings.TrimSpace(projectFlag)
	if req == "*" {
		return "*", nil
	}
	effectiveDir := store.CanonicalProjectPath(cwd)
	if found := goals.FindGoalsRoot(effectiveDir); found != "" {
		effectiveDir = found
	}
	if st != nil {
		if req != "" {
			p, err := st.ResolveScope(req, effectiveDir)
			if err != nil {
				return "", err
			}
			if p.ID != "" {
				return p.ID, nil
			}
			return req, nil
		}
		if p, registered, err := st.ProjectForDir(effectiveDir); err == nil && registered && p.ID != "" {
			return p.ID, nil
		}
	}
	if req != "" {
		return req, nil
	}
	return store.ProjectID(effectiveDir), nil
}

func runSession(ui render.UI, dbPath, project, agent, sessionID, harness, goal, olderThan, since string, force, yes, jsonOut bool, args []string) {
	cwd, _ := os.Getwd()
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila session: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()
	if resolved, rErr := resolveCommandProjectScope(st, project, cwd); rErr == nil && resolved != "" {
		project = resolved
	} else if project == "" {
		project = store.ProjectID(cwd)
	}

	sub, opts, positional := parseSessionArgs(args)
	if sub == "" {
		sub = "status"
	}
	if sessionID == "" {
		sessionID = opts["session"]
	}
	if harness == "" {
		harness = opts["harness"]
	}
	if goal == "" {
		goal = opts["goal"]
	}
	if agent == "" {
		agent = opts["agent"]
	}
	if olderThan == "" {
		olderThan = opts["older-than"]
	}
	if since == "" {
		since = opts["since"]
	}
	if opts["force"] == "true" || opts["force"] == "1" {
		force = true
	}
	if opts["yes"] == "true" || opts["yes"] == "1" {
		yes = true
	}
	root, err := goals.ResolveProjectDir(st, cwd)
	if err != nil {
		root = cwd
	}

	switch sub {
	case "start":
		startSession(ui, st, root, cwd, project, agent, sessionID, harness, goal, opts, force, jsonOut)
	case "status":
		statusSession(ui, st, root, cwd, project, sessionID, goal, jsonOut, opts)
	case "env":
		envSession(ui, root, cwd, project, sessionID, goal, opts["shell"], jsonOut)
	case "end":
		endSession(ui, st, root, cwd, project, sessionID, jsonOut)
	case "list", "ls":
		listSessions(ui, st, project, since, jsonOut, opts)
	case "prune":
		pruneSessions(ui, st, olderThan, yes, jsonOut)
	case "help", "--help", "-h":
		printSessionHelp(ui)
	default:
		fmt.Fprintf(os.Stderr, "sila session: unknown subcommand %q\n", sub)
		printSessionHelp(ui)
		os.Exit(2)
	}
	_ = positional
}

func parseSessionArgs(args []string) (string, map[string]string, []string) {
	opts := map[string]string{}
	var positional []string
	sub := ""
	valueFlags := map[string]bool{"harness": true, "parent": true, "shell": true, "session": true, "goal": true, "agent": true, "older-than": true, "since": true}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			if sub == "" {
				sub = a
			} else {
				positional = append(positional, a)
			}
			continue
		}
		key := strings.TrimLeft(a, "-")
		val := "true"
		if before, after, ok := strings.Cut(key, "="); ok {
			key, val = before, after
		} else if valueFlags[key] && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			val = args[i+1]
			i++
		}
		opts[key] = val
	}
	return sub, opts, positional
}

func sessionIdentity(cwd, project, explicit string) (session.Identity, error) {
	return session.ResolveIdentity(explicit, project, cwd)
}

// sessionContext prefers the session's stored snapshot, then re-resolves and
// reports drift. It never reads another session's row.
func sessionContext(st *store.Store, sessionID, cwd string) (session.GitContext, string) {
	prev := session.GitContext{}
	if st != nil && sessionID != "" {
		if existing, _ := st.GetAgentSession(sessionID); existing != nil {
			prev = session.GitContext{Branch: existing.Branch, Head: existing.Head, Detached: existing.Detached}
		}
	}
	return session.ResumeContext(prev, cwd)
}

func contextDriftTime(drift string) *time.Time {
	if drift == "" {
		return nil
	}
	now := time.Now().UTC()
	return &now
}

func startSession(ui render.UI, st *store.Store, root, cwd, project, agent, sessionID, harness, goal string, opts map[string]string, force, jsonOut bool) {
	id, err := sessionIdentity(cwd, project, sessionID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila session start: %v\n", err)
		os.Exit(1)
	}
	if id.SessionID == "" {
		id.SessionID = session.GenerateSessionID()
		id.Source = "generated"
	}
	if harness != "" {
		id.Harness = harness
	}
	if id.Harness == "" {
		id.Harness = "cli"
	}
	if agent != "" {
		id.Agent = agent
	}
	if id.Agent == "" {
		id.Agent = "agent"
	}
	id.ParentSessionID = firstNonEmpty(opts["parent"], id.ParentSessionID)
	id.Cwd = cwd
	id.PID = os.Getpid()
	id.ProjectID = project

	existing, _ := st.GetAgentSession(id.SessionID)
	if existing != nil {
		if id.ActiveGoal == "" {
			id.ActiveGoal = existing.ActiveGoal
			id.ClaimID = existing.ClaimID
		}
		if goal != "" && existing.ActiveGoal != "" && existing.ActiveGoal != goal && !force {
			fmt.Fprintf(os.Stderr, "sila session start: session %s is already bound to goal %q (use --force to move it)\n", id.SessionID, existing.ActiveGoal)
			os.Exit(2)
		}
	}

	bound := false
	claimHint := ""
	claimID := id.ClaimID
	if goal != "" {
		claim, claimErr := readGoalClaim(root, goal)
		if claimErr != nil {
			fmt.Fprintf(os.Stderr, "sila session start: %v\n", claimErr)
			os.Exit(1)
		}
		if claim == nil {
			claimHint = fmt.Sprintf("sila goals claim %s --agent=%s --session=%s", goal, id.Agent, id.SessionID)
			if !jsonOut {
				fmt.Fprintf(os.Stdout, "sila session start: goal %q is unclaimed\n", goal)
				fmt.Fprintf(os.Stdout, "  run: %s\n", claimHint)
			}
		} else if sameSessionClaim(claim, id.SessionID, id.Agent) {
			cl, err := goals.FocusGoal(root, goal, id.Agent, "", id.SessionID, force)
			if err != nil {
				fmt.Fprintf(os.Stderr, "sila session start: %v\n", err)
				os.Exit(2)
			}
			bound = true
			id.ActiveGoal = goal
			if cl != nil {
				claimID = claimIdentifier(cl)
			}
		} else {
			fmt.Fprintf(os.Stderr, "sila session start: goal %q is claimed by %s (session %s)\n", goal, claimDisplayAgent(claim), claim.SessionID)
			fmt.Fprintln(os.Stderr, "  run 'sila goals claims' for full claim details")
			os.Exit(2)
		}
	}
	if !bound && goal != "" {
		id.ActiveGoal = ""
	}
	id.ClaimID = claimID
	gitCtx := session.SnapshotGit(id.Cwd)
	contextDrift := ""
	if existing != nil {
		gitCtx, contextDrift = session.ResumeContext(session.GitContext{
			Branch: existing.Branch, Head: existing.Head, Detached: existing.Detached,
		}, id.Cwd)
	}
	if err := st.UpsertAgentSession(store.AgentSession{
		SessionID:       id.SessionID,
		ProjectID:       id.ProjectID,
		Harness:         id.Harness,
		Agent:           id.Agent,
		ParentSessionID: id.ParentSessionID,
		ActiveGoal:      id.ActiveGoal,
		ClaimID:         id.ClaimID,
		Cwd:             id.Cwd,
		PID:             id.PID,
		Source:          id.Source,
		Branch:          gitCtx.Branch,
		Head:            gitCtx.Head,
		Detached:        gitCtx.Detached,
		ContextDrift:    contextDrift,
		ContextDriftAt:  contextDriftTime(contextDrift),
	}); err != nil {
		fmt.Fprintf(os.Stderr, "sila session start: %v\n", err)
		os.Exit(1)
	}
	if err := session.WriteCurrent(cwd, id); err != nil {
		fmt.Fprintf(os.Stderr, "sila session start: write current pointer: %v\n", err)
		os.Exit(1)
	}
	if jsonOut {
		reported := id
		if opts["include-paths"] != "true" {
			reported.Cwd = ""
		}
		emitJSON(map[string]any{
			"identity": reported, "bound": bound, "claim_command": claimHint,
			"context": gitCtx, "context_drift": contextDrift,
		})
		return
	}
	fmt.Fprintf(os.Stdout, "%s Session %s (%s) started for %s\n", ui.Green("✔"), ui.Bold(id.SessionID), id.Source, ui.Bold(project))
	if !gitCtx.IsZero() {
		fmt.Fprintf(os.Stdout, "  branch: %s\n", gitCtx.Label())
	}
	if contextDrift != "" {
		fmt.Fprintf(os.Stdout, "  context drift: %s\n", contextDrift)
	}
	if bound {
		fmt.Fprintf(os.Stdout, "  goal: %s (claim %s)\n", id.ActiveGoal, id.ClaimID)
	}
}

func statusSession(ui render.UI, st *store.Store, root, cwd, project, sessionID, goal string, jsonOut bool, opts map[string]string) {
	id, err := sessionIdentity(cwd, project, sessionID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila session status: %v\n", err)
		os.Exit(1)
	}
	var stored *store.AgentSession
	if id.SessionID != "" {
		stored, _ = st.GetAgentSession(id.SessionID)
	}
	if stored != nil {
		if id.Harness == "" {
			id.Harness = stored.Harness
		}
		if id.Agent == "" {
			id.Agent = stored.Agent
		}
		if id.ActiveGoal == "" {
			id.ActiveGoal = stored.ActiveGoal
		}
		if id.ClaimID == "" {
			id.ClaimID = stored.ClaimID
		}
	}
	pointerValid := id.Source == "current-file" || id.Source == "global-current"
	if !pointerValid && id.SessionID != "" {
		if pointer, _ := session.ResolveIdentity("", project, cwd); pointer.SessionID == id.SessionID &&
			(pointer.Source == "current-file" || pointer.Source == "global-current") {
			pointerValid = true
		}
	}
	reportedID := id
	reportedStored := stored
	includePaths := opts["include-paths"] == "true"
	if !includePaths {
		reportedID.Cwd = ""
		if reportedStored != nil {
			copy := *reportedStored
			copy.Cwd = ""
			reportedStored = &copy
		}
	}
	payload := map[string]any{
		"identity":      reportedID,
		"pointer_valid": pointerValid,
		"stored":        reportedStored,
	}
	if jsonOut {
		emitJSON(payload)
		return
	}
	fmt.Fprintf(os.Stdout, "session: %s\n", displayOr(id.SessionID, "unbound"))
	fmt.Fprintf(os.Stdout, "project: %s\n", displayOr(id.ProjectID, "unknown"))
	fmt.Fprintf(os.Stdout, "source: %s\n", id.Source)
	fmt.Fprintf(os.Stdout, "harness: %s\n", displayOr(id.Harness, "unknown"))
	fmt.Fprintf(os.Stdout, "agent: %s\n", displayOr(id.Agent, "unknown"))
	fmt.Fprintf(os.Stdout, "pointer: %v\n", pointerValid)
	if id.ActiveGoal != "" {
		fmt.Fprintf(os.Stdout, "active_goal: %s\n", id.ActiveGoal)
	}
	if stored != nil {
		if stored.Branch != "" || stored.Head != "" {
			state := "attached"
			if stored.Detached {
				state = "detached"
			}
			fmt.Fprintf(os.Stdout, "branch: %s (%s)\n", session.GitContext{Branch: stored.Branch, Head: stored.Head, Detached: stored.Detached}.Label(), state)
		}
		if stored.ContextDrift != "" {
			fmt.Fprintf(os.Stdout, "context_drift: %s\n", stored.ContextDrift)
		}
		fmt.Fprintf(os.Stdout, "started: %s\n", stored.StartedAt.Format(time.RFC3339))
		fmt.Fprintf(os.Stdout, "last_seen: %s\n", stored.LastSeenAt.Format(time.RFC3339))
	}
	if id.ActiveGoal == "" && goal == "" {
		if ready := readyGoalHint(root); ready != "" {
			fmt.Fprintf(os.Stdout, "next: sila goals claim %s --agent=%s --session=%s\n", ready, displayOr(id.Agent, "agent"), displayOr(id.SessionID, "<session>"))
		}
	}
}

func envSession(ui render.UI, root, cwd, project, sessionID, goal, shell string, jsonOut bool) {
	id, err := sessionIdentity(cwd, project, sessionID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila session env: %v\n", err)
		os.Exit(1)
	}
	if id.ActiveGoal == "" && goal != "" {
		id.ActiveGoal = goal
	}
	if jsonOut {
		emitJSON(id)
		return
	}
	switch shell {
	case "fish":
		fmt.Fprintf(os.Stdout, "set -gx SILA_SESSION_ID %q\n", id.SessionID)
		if id.ActiveGoal != "" {
			fmt.Fprintf(os.Stdout, "set -gx SILA_ACTIVE_GOAL %q\n", id.ActiveGoal)
		}
		if id.ClaimID != "" {
			fmt.Fprintf(os.Stdout, "set -gx SILA_CLAIM_ID %q\n", id.ClaimID)
		}
	case "powershell", "pwsh":
		fmt.Fprintf(os.Stdout, "$env:SILA_SESSION_ID = %q\n", id.SessionID)
		if id.ActiveGoal != "" {
			fmt.Fprintf(os.Stdout, "$env:SILA_ACTIVE_GOAL = %q\n", id.ActiveGoal)
		}
		if id.ClaimID != "" {
			fmt.Fprintf(os.Stdout, "$env:SILA_CLAIM_ID = %q\n", id.ClaimID)
		}
	default:
		fmt.Fprintf(os.Stdout, "export SILA_SESSION_ID=%q\n", id.SessionID)
		if id.ActiveGoal != "" {
			fmt.Fprintf(os.Stdout, "export SILA_ACTIVE_GOAL=%q\n", id.ActiveGoal)
		}
		if id.ClaimID != "" {
			fmt.Fprintf(os.Stdout, "export SILA_CLAIM_ID=%q\n", id.ClaimID)
		}
	}
	_ = root
}

func endSession(ui render.UI, st *store.Store, root, cwd, project, sessionID string, jsonOut bool) {
	id, err := sessionIdentity(cwd, project, sessionID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila session end: %v\n", err)
		os.Exit(1)
	}
	if id.SessionID == "" {
		fmt.Fprintln(os.Stderr, "sila session end: no session resolved; pass --session")
		os.Exit(2)
	}
	if err := st.CloseAgentSession(id.SessionID, time.Now().UTC()); err != nil {
		fmt.Fprintf(os.Stderr, "sila session end: %v\n", err)
		os.Exit(1)
	}
	if err := session.ClearCurrent(cwd, project, id.SessionID); err != nil {
		fmt.Fprintf(os.Stderr, "sila session end: clear current pointer: %v\n", err)
		os.Exit(1)
	}
	if jsonOut {
		emitJSON(map[string]any{"session_id": id.SessionID, "closed": true})
		return
	}
	fmt.Fprintf(os.Stdout, "%s Session %s ended\n", ui.Green("✔"), ui.Bold(id.SessionID))
	_ = root
}

func listSessions(ui render.UI, st *store.Store, project, since string, jsonOut bool, opts map[string]string) {
	var sinceTime time.Time
	if since != "" {
		sinceTime = parseSince(since)
	}
	sessions, err := st.ListAgentSessions(project, sinceTime, 200)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila session list: %v\n", err)
		os.Exit(1)
	}
	if jsonOut {
		if opts["include-paths"] != "true" {
			for i := range sessions {
				sessions[i].Cwd = ""
			}
		}
		emitJSON(sessions)
		return
	}
	if len(sessions) == 0 {
		fmt.Fprintln(os.Stdout, "no sessions recorded")
		return
	}
	for _, sess := range sessions {
		status := "open"
		if !sess.ClosedAt.IsZero() {
			status = "closed"
		}
		fmt.Fprintf(os.Stdout, "%s  %-12s  %-18s  %s\n", sess.SessionID, status, sess.ProjectID, sess.LastSeenAt.Format(time.RFC3339))
	}
	_ = ui
	_ = opts
}

func pruneSessions(ui render.UI, st *store.Store, olderThan string, yes, jsonOut bool) {
	if !yes {
		fmt.Fprintln(os.Stderr, "sila session prune: pass --yes to delete closed sessions")
		os.Exit(2)
	}
	age, err := parseSessionAge(olderThan)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila session prune: %v\n", err)
		os.Exit(2)
	}
	count, err := st.PruneAgentSessions(time.Now().UTC().Add(-age))
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila session prune: %v\n", err)
		os.Exit(1)
	}
	if jsonOut {
		emitJSON(map[string]any{"deleted": count, "older_than": age.String()})
		return
	}
	fmt.Fprintf(os.Stdout, "%s Pruned %d closed session(s) older than %s\n", ui.Green("✔"), count, age)
}

func parseSessionAge(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 90 * 24 * time.Hour, nil
	}
	if strings.HasSuffix(raw, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(raw, "d"))
		if err != nil {
			return 0, err
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(raw)
}

func readGoalClaim(root, slug string) (*goals.GoalClaim, error) {
	data, err := os.ReadFile(filepath.Join(root, "goals", slug, "claim.json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var claim goals.GoalClaim
	if err := json.Unmarshal(data, &claim); err != nil {
		return nil, fmt.Errorf("parse claim.json for %s: %w", slug, err)
	}
	return &claim, nil
}

func sameSessionClaim(claim *goals.GoalClaim, sessionID, agent string) bool {
	if claim == nil {
		return false
	}
	if claim.SessionID != "" {
		return claim.SessionID == sessionID
	}
	if agent == "" {
		return false
	}
	return claim.Agent == agent || claim.ClaimedBy == agent
}

func claimIdentifier(claim *goals.GoalClaim) string {
	if claim == nil {
		return ""
	}
	if claim.SessionID != "" {
		return claim.SessionID
	}
	return fmt.Sprintf("%s:%d", claim.GoalSlug, claim.ClaimedAt.UnixNano())
}

func claimDisplayAgent(claim *goals.GoalClaim) string {
	if claim == nil {
		return "unknown"
	}
	if claim.Agent != "" {
		return claim.Agent
	}
	return claim.ClaimedBy
}

func readyGoalHint(root string) string {
	entries, err := os.ReadDir(filepath.Join(root, "goals"))
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		goalPath := filepath.Join(root, "goals", entry.Name(), "goal.md")
		data, err := os.ReadFile(goalPath)
		if err != nil {
			continue
		}
		if strings.Contains(string(data), "Status: ready") {
			return entry.Name()
		}
	}
	return ""
}

func displayOr(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

var (
	writeSessionOverride string
	writeSessionVerbose  bool
)

// resolveWriteSession attaches the active local session to memory writes. A
// missing session is non-fatal; the hint is only printed in verbose mode.
func resolveWriteSession(project string) string {
	cwd, _ := os.Getwd()
	id, err := session.ResolveIdentity(writeSessionOverride, project, cwd)
	if err != nil || id.SessionID == "" {
		if writeSessionVerbose {
			fmt.Fprintln(os.Stderr, "session: unbound; run sila session env or pass --session")
		}
		return ""
	}
	return id.SessionID
}

func printSessionHelp(ui render.UI) {
	fmt.Fprintln(os.Stdout, ui.Bold("sila session")+" — local session identity and goal binding")
	fmt.Fprintln(os.Stdout, "  sila session start --harness=<name> [--session=<id>] [--goal=<slug>]")
	fmt.Fprintln(os.Stdout, "  sila session status [--session=<id>] [--json]")
	fmt.Fprintln(os.Stdout, "  sila session env [--shell=sh|fish|powershell] [--json]")
	fmt.Fprintln(os.Stdout, "  sila session end [--session=<id>] [--json]")
	fmt.Fprintln(os.Stdout, "  sila session list [--project=<id>] [--since=24h] [--json]")
	fmt.Fprintln(os.Stdout, "  sila session prune --older-than=90d --yes")
}
