package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/dream"
	"github.com/jadmadi/sila/internal/harness/contract"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

// runHandoff views, records, claims, pops, or lists structured session handoffs.
func runHandoff(ui render.UI, dbPath, project, branch, agent, openItems, decisions, files string, jsonOut, verbose bool, args []string) {
	expectedRevision := ""
	var filteredArgs []string
	for _, a := range args {
		if strings.HasPrefix(a, "--expected-revision=") {
			expectedRevision = strings.TrimSpace(strings.TrimPrefix(a, "--expected-revision="))
			continue
		}
		filteredArgs = append(filteredArgs, a)
	}
	args = filteredArgs
	if expectedRevision != "" && len(expectedRevision) != 64 {
		fmt.Fprintln(os.Stderr, "sila handoff: --expected-revision must be a full 64-character hash")
		os.Exit(2)
	}
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	cwd, _ := os.Getwd()
	if charter, ok, cerr := charterForWrite(os.Args[1:]); cerr != nil {
		charterFail(cerr)
	} else if ok && charter.HandoffPolicy != "subagent-allowed" {
		if jsonOut {
			emitJSON(map[string]any{"written": false, "parent_session_id": charter.ParentSessionID, "command": "sila handoff \"<summary>\" --open=..."})
		} else {
			fmt.Fprintf(os.Stdout, "handoff not written (parent-only): parent session %s owns continuity; ask the parent to run sila handoff\n", charter.ParentSessionID)
		}
		return
	}
	gitCtx := dream.DetectGitContext(cwd)

	if len(args) > 0 {
		sub := strings.ToLower(args[0])
		subPos, subAgent, subSession, subBranch, subStatus := parseHandoffSubcommandFlags(args[1:])
		if subAgent != "" {
			agent = subAgent
		}
		if subBranch != "" {
			branch = subBranch
		}

		switch sub {
		case "claim":
			runHandoffClaim(ui, st, agent, subSession, jsonOut, subPos)
			return
		case "pop":
			runHandoffPop(ui, st, project, branch, cwd, agent, subSession, gitCtx, jsonOut)
			return
		case "list":
			runHandoffList(ui, st, project, subStatus, cwd, gitCtx, jsonOut)
			return
		case "resolve":
			runHandoffResolve(ui, st, jsonOut, subPos)
			return
		}
	}

	summary := strings.Join(args, " ")
	isView := (summary == "" && openItems == "" && decisions == "")

	if project != "" {
		p, err := st.ResolveScope(project, cwd)
		if err != nil {
			if strings.Contains(err.Error(), "is ambiguous") {
				fmt.Fprintf(os.Stderr, "sila: %v\n", err)
				os.Exit(1)
			}
			if isView {
				h, hErr := st.LatestHandoff(project)
				if hErr == nil && h != nil {
					project = h.ProjectID
				} else if jsonOut {
					emitJSON(store.Handoff{ProjectID: project, Status: "none"})
					return
				} else {
					fmt.Fprintf(os.Stderr, "sila: %v\n", err)
					os.Exit(1)
				}
			} else {
				fmt.Fprintf(os.Stderr, "sila: %v\n", err)
				os.Exit(1)
			}
		} else {
			project = p.ID
		}
	} else {
		p, err := st.ResolveScope("", cwd)
		if err == nil && p.ID != "" {
			project = p.ID
		} else if gitCtx.RootPath != "" {
			project = store.ProjectID(gitCtx.RootPath)
		} else {
			project = store.ProjectID(cwd)
		}
	}
	if branch == "" && gitCtx.Branch != "" {
		branch = gitCtx.Branch
	}

	if isView {
		// View latest handoff
		h, err := st.LatestHandoff(project)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila: %v\n", err)
			os.Exit(1)
		}
		if h == nil {
			if jsonOut {
				emitJSON(store.Handoff{ProjectID: project, Status: "none"})
				return
			}
			if outModes.porcelain {
				fmt.Fprintf(os.Stdout, "none\t%s\n", project)
				return
			}
			fmt.Fprintf(os.Stdout, "handoff: 0 active handoffs found for project %s\n", ui.Bold(project))
			if !outModes.quiet {
				fmt.Fprintln(os.Stdout, "help: record one with 'sila handoff \"<summary>\" --open=\"<task1>,<task2>\"'")
			}
			return
		}
		if jsonOut {
			emitJSON(h)
			return
		}
		if outModes.porcelain {
			fmt.Fprintf(os.Stdout, "%s\t%s\t%s\t%s\t%s\t%s\n", h.ID, h.Status, h.Agent, h.Branch, h.CreatedAt, sanitizePorcelain(h.Summary))
			return
		}
		timeDesc := h.CreatedAt
		if t, err := time.Parse(time.RFC3339, h.CreatedAt); err == nil {
			timeDesc = t.Format("2006-01-02 15:04")
		}
		branchNote := ""
		if h.Branch != "" {
			branchNote = fmt.Sprintf(" on %s", ui.Cyan(h.Branch))
		}
		statusNote := ""
		if h.Status != "" {
			statusNote = fmt.Sprintf(" [%s]", ui.Bold(h.Status))
		}
		fmt.Fprint(os.Stdout, render.RenderHeader(ui, render.Header{Command: "handoff", Title: "active session handoff" + statusNote}))
		fmt.Fprintln(os.Stdout)
		fmt.Fprintf(os.Stdout, "  Project:  %s\n", ui.Bold(h.ProjectID))
		fmt.Fprintf(os.Stdout, "  Agent:    %s (%s)%s\n", ui.Bold(h.Agent), ui.Dim(timeDesc), branchNote)
		if h.ClaimedBy != "" {
			fmt.Fprintf(os.Stdout, "  Claimed:  %s by %s\n", ui.Dim(h.ClaimedAt), ui.Bold(h.ClaimedBy))
		}
		fmt.Fprintf(os.Stdout, "  Summary:  %s\n", strings.TrimSpace(render.RenderMarkdownString(h.Summary, ui)))
		if len(h.OpenItems) > 0 {
			fmt.Fprintln(os.Stdout, "\n  Open Tasks:")
			for _, it := range h.OpenItems {
				fmt.Fprintf(os.Stdout, "    %s\n", strings.TrimSpace(render.RenderMarkdownString(it, ui)))
			}
		}
		if len(h.Decisions) > 0 {
			fmt.Fprintln(os.Stdout, "\n  Decisions:")
			for _, d := range h.Decisions {
				fmt.Fprintf(os.Stdout, "    • %s\n", strings.TrimSpace(render.RenderMarkdownString(d, ui)))
			}
		}
		return
	}

	// Insert handoff
	var openList []string
	if openItems != "" {
		for it := range strings.SplitSeq(openItems, ",") {
			if it = strings.TrimSpace(it); it != "" {
				openList = append(openList, it)
			}
		}
	}
	var decList []string
	if decisions != "" {
		for d := range strings.SplitSeq(decisions, ",") {
			if d = strings.TrimSpace(d); d != "" {
				decList = append(decList, d)
			}
		}
	}
	var fileList []string
	if files != "" {
		for f := range strings.SplitSeq(files, ",") {
			if f = strings.TrimSpace(f); f != "" {
				fileList = append(fileList, f)
			}
		}
	}

	parentSession := resolveWriteSession(project)
	if parentSession != "" {
		if rows, qerr := st.DB.Query(`SELECT session_id, COALESCE(active_goal, '') FROM agent_sessions WHERE parent_session_id = ? ORDER BY session_id`, parentSession); qerr == nil {
			var children []string
			for rows.Next() {
				var childID, childGoal string
				if rows.Scan(&childID, &childGoal) == nil {
					var writes int
					_ = st.DB.QueryRow(`SELECT COUNT(*) FROM tool_events WHERE session_id = ?`, childID).Scan(&writes)
					children = append(children, fmt.Sprintf("- %s · goal: %s · writes: %d", childID, childGoal, writes))
				}
			}
			rows.Close()
			if len(children) > 0 {
				summary += "\n\n## Subagents\n" + strings.Join(children, "\n")
			}
		}
	}
	openList = append(openList, handoffContractOpenItems(st, project, cwd, parentSession, agent)...)
	h := store.Handoff{
		ProjectID: project,
		Agent:     agent,
		Branch:    branch,
		Summary:   summary,
		OpenItems: openList,
		Decisions: decList,
		RepoFiles: fileList,
		Status:    string(store.HandoffPending),
		SessionID: parentSession,
	}
	if h.CreatedAt == "" {
		h.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if h.ID == "" {
		h.ID = store.HandoffID(h.ProjectID, h.Summary, h.CreatedAt)
	}

	newRevision, replayed, currentRevision, revisionOK := handoffRevisionDecision(st, project, branch, expectedRevision, h)
	if !revisionOK {
		fmt.Fprintf(os.Stderr, "revision conflict: current %s\n", currentRevision)
		os.Exit(1)
	}
	if !replayed {
		if err := st.InsertHandoff(h); err != nil {
			fmt.Fprintf(os.Stderr, "sila: %v\n", err)
			os.Exit(1)
		}
		recordMemoryEvent(st, project, agent, "handoff", "ok", "", "cli", 0)
	}

	if jsonOut {
		type handoffWithRevision struct {
			store.Handoff
			RevisionHash string `json:"revision_hash"`
			Unchecked    bool   `json:"unchecked,omitempty"`
			Replayed     bool   `json:"replayed,omitempty"`
		}
		h.RepoFiles = jsonCardFiles(cwd, h.RepoFiles)
		emitJSON(handoffWithRevision{Handoff: h, RevisionHash: newRevision, Unchecked: expectedRevision == "", Replayed: replayed})
		return
	}
	if outModes.porcelain {
		fmt.Fprintf(os.Stdout, "%s\t%s\t%s\n", h.ID, h.ProjectID, h.Status)
		return
	}
	if !outModes.quiet {
		fmt.Fprint(os.Stdout, render.RenderCard(ui, render.Card{
			Verb:   "Saved",
			Object: "handoff",
			ID:     h.ID,
			Fields: []render.Field{
				{Label: "Project", Value: cardProjectLabel(st, project)},
				{Label: "Summary", Value: strings.ReplaceAll(summary, "\n", " ")},
				{Label: "Open items", Value: strings.Join(openList, ", ")},
				{Label: "Repo files", Value: displayCardFiles(cwd, fileList)},
				{Label: "Revision", Value: newRevision},
			},
		}))
	}
}

func parseHandoffSubcommandFlags(args []string) (positional []string, agent, session, branch, status string) {
	for _, a := range args {
		if strings.HasPrefix(a, "--session=") {
			session = strings.TrimPrefix(a, "--session=")
		} else if strings.HasPrefix(a, "--status=") {
			status = strings.TrimPrefix(a, "--status=")
		} else if strings.HasPrefix(a, "--agent=") {
			agent = strings.TrimPrefix(a, "--agent=")
		} else if strings.HasPrefix(a, "--branch=") {
			branch = strings.TrimPrefix(a, "--branch=")
		} else if !strings.HasPrefix(a, "-") {
			positional = append(positional, a)
		}
	}
	return
}

func runHandoffClaim(ui render.UI, st *store.Store, agent, sessionID string, jsonOut bool, args []string) {
	if len(args) == 0 {
		if jsonOut {
			emitErrorEnvelope("USAGE", "handoff ID is required", "sila handoff list")
			os.Exit(2)
		}
		fmt.Fprintln(os.Stderr, "sila handoff claim: specify handoff ID to claim (e.g. sila handoff claim hoff_123456)")
		os.Exit(2)
	}
	handoffID := args[0]
	if agent == "" {
		agent = "agent"
	}
	claimed, err := st.ClaimHandoff(context.Background(), handoffID, agent, sessionID)
	if err != nil {
		if errors.Is(err, store.ErrHandoffAlreadyClaimed) {
			if jsonOut {
				emitErrorEnvelope("HANDOFF_ALREADY_CLAIMED", err.Error(), "sila handoff list")
				os.Exit(1)
			}
			fmt.Fprintf(os.Stderr, "%s Handoff %s has already been claimed by another agent session.\n", ui.Red("✖"), handoffID)
			os.Exit(1)
		}
		if errors.Is(err, store.ErrHandoffNotFound) {
			if jsonOut {
				emitErrorEnvelope("HANDOFF_NOT_FOUND", err.Error(), "sila handoff list")
				os.Exit(1)
			}
			fmt.Fprintf(os.Stderr, "%s Handoff %s not found.\n", ui.Red("✖"), handoffID)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(claimed)
		return
	}
	fmt.Fprintf(os.Stdout, "%s Claimed handoff %s for agent %s.\n", ui.Green("✔"), ui.Bold(claimed.ID), ui.Bold(claimed.ClaimedBy))
	fmt.Fprintln(os.Stdout, ui.Dim("> [!NOTE] Inbound Handoff Baton (Untrusted Cross-Agent Context): Evaluate tasks against current repository state."))
	fmt.Fprintln(os.Stdout)
	fmt.Fprintf(os.Stdout, "  Summary: %s\n", strings.TrimSpace(render.RenderMarkdownString(claimed.Summary, ui)))
	if len(claimed.OpenItems) > 0 {
		fmt.Fprintln(os.Stdout, "\n  Open Tasks:")
		for _, it := range claimed.OpenItems {
			fmt.Fprintf(os.Stdout, "    %s\n", strings.TrimSpace(render.RenderMarkdownString(it, ui)))
		}
	}
	if len(claimed.Decisions) > 0 {
		fmt.Fprintln(os.Stdout, "\n  Decisions:")
		for _, d := range claimed.Decisions {
			fmt.Fprintf(os.Stdout, "    • %s\n", strings.TrimSpace(render.RenderMarkdownString(d, ui)))
		}
	}
}

func runHandoffPop(ui render.UI, st *store.Store, project, branch, cwd, agent, sessionID string, gitCtx dream.GitContext, jsonOut bool) {
	if project == "" {
		p, err := st.ResolveScope("", cwd)
		if err == nil && p.ID != "" {
			project = p.ID
		} else if gitCtx.RootPath != "" {
			project = store.ProjectID(gitCtx.RootPath)
		} else {
			project = store.ProjectID(cwd)
		}
	}
	if branch == "" && gitCtx.Branch != "" {
		branch = gitCtx.Branch
	}
	if agent == "" {
		agent = "agent"
	}

	claimed, err := st.PopEligibleHandoff(context.Background(), project, branch, cwd, agent, sessionID)
	if err != nil {
		if errors.Is(err, store.ErrHandoffAlreadyClaimed) {
			if jsonOut {
				emitErrorEnvelope("HANDOFF_ALREADY_CLAIMED", err.Error(), "sila handoff list")
				os.Exit(1)
			}
			fmt.Fprintf(os.Stderr, "%s Handoff was already claimed by another session.\n", ui.Red("✖"))
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}

	if claimed == nil {
		if jsonOut {
			emitJSON(map[string]any{"status": "none", "project": project, "branch": branch})
			return
		}
		fmt.Fprintf(os.Stdout, "handoff: 0 pending handoffs found for project %s (branch: %s)\n", ui.Bold(project), ui.Cyan(branch))
		return
	}

	if jsonOut {
		emitJSON(claimed)
		return
	}

	fmt.Fprintf(os.Stdout, "%s Popped and claimed handoff %s on branch %s.\n", ui.Green("✔"), ui.Bold(claimed.ID), ui.Cyan(claimed.Branch))
	fmt.Fprintln(os.Stdout, ui.Dim("> [!NOTE] Inbound Handoff Baton (Untrusted Cross-Agent Context): Evaluate tasks against current repository state."))
	fmt.Fprintln(os.Stdout)
	fmt.Fprintf(os.Stdout, "  Summary: %s\n", strings.TrimSpace(render.RenderMarkdownString(claimed.Summary, ui)))
	if len(claimed.OpenItems) > 0 {
		fmt.Fprintln(os.Stdout, "\n  Open Tasks:")
		for _, it := range claimed.OpenItems {
			fmt.Fprintf(os.Stdout, "    %s\n", strings.TrimSpace(render.RenderMarkdownString(it, ui)))
		}
	}
	if len(claimed.Decisions) > 0 {
		fmt.Fprintln(os.Stdout, "\n  Decisions:")
		for _, d := range claimed.Decisions {
			fmt.Fprintf(os.Stdout, "    • %s\n", strings.TrimSpace(render.RenderMarkdownString(d, ui)))
		}
	}
}

func runHandoffList(ui render.UI, st *store.Store, project, status, cwd string, gitCtx dream.GitContext, jsonOut bool) {
	if project == "" {
		p, err := st.ResolveScope("", cwd)
		if err == nil && p.ID != "" {
			project = p.ID
		} else if gitCtx.RootPath != "" {
			project = store.ProjectID(gitCtx.RootPath)
		} else {
			project = store.ProjectID(cwd)
		}
	}

	list, err := st.ListHandoffs(context.Background(), project, status, 30)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(list)
		return
	}

	if outModes.porcelain {
		for _, h := range list {
			fmt.Fprintf(os.Stdout, "%s\t%s\t%s\t%s\t%s\n", h.ID, h.Status, h.Agent, h.Branch, sanitizePorcelain(h.Summary))
		}
		return
	}

	if len(list) == 0 {
		fmt.Fprintf(os.Stdout, "handoff: 0 handoffs found for project %s (status: %s)\n", ui.Bold(project), status)
		return
	}

	fmt.Fprintf(os.Stdout, "%s — %d handoffs for project %s\n\n", ui.Cyan(ui.Bold("sila · handoffs")), len(list), ui.Bold(project))
	for _, h := range list {
		statusColor := ui.Yellow(h.Status)
		if h.Status == string(store.HandoffClaimed) {
			statusColor = ui.Cyan(h.Status)
		} else if h.Status == string(store.HandoffResolved) {
			statusColor = ui.Green(h.Status)
		} else if h.Status == string(store.HandoffSuperseded) {
			statusColor = ui.Dim(h.Status)
		}
		branchPart := ""
		if h.Branch != "" {
			branchPart = fmt.Sprintf(" on %s", ui.Cyan(h.Branch))
		}
		fmt.Fprintf(os.Stdout, "  • %s [%s] %s%s: %s\n", ui.Bold(h.ID), statusColor, ui.Bold(h.Agent), branchPart, h.Summary)
		if h.ClaimedBy != "" {
			fmt.Fprintf(os.Stdout, "    %s\n", ui.Dim(fmt.Sprintf("claimed by %s at %s", h.ClaimedBy, h.ClaimedAt)))
		}
	}
}

func runHandoffResolve(ui render.UI, st *store.Store, jsonOut bool, args []string) {
	if len(args) == 0 {
		if jsonOut {
			emitErrorEnvelope("USAGE", "handoff ID is required", "sila handoff list")
			os.Exit(2)
		}
		fmt.Fprintln(os.Stderr, "sila handoff resolve: specify handoff ID (e.g. sila handoff resolve hoff_123456)")
		os.Exit(2)
	}
	handoffID := args[0]
	if err := st.ResolveHandoff(context.Background(), handoffID); err != nil {
		if errors.Is(err, store.ErrHandoffNotFound) {
			if jsonOut {
				emitErrorEnvelope("HANDOFF_NOT_FOUND", err.Error(), "sila handoff list")
				os.Exit(1)
			}
			fmt.Fprintf(os.Stderr, "%s Handoff %s not found.\n", ui.Red("✖"), handoffID)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}
	if jsonOut {
		emitJSON(map[string]any{"status": "resolved", "id": handoffID})
		return
	}
	if outModes.porcelain {
		fmt.Fprintf(os.Stdout, "%s\tresolved\n", handoffID)
		return
	}
	if !outModes.quiet {
		fmt.Fprintf(os.Stdout, "%s Resolved handoff %s.\n", ui.Green("✔"), ui.Bold(handoffID))
	}
}

// handoffContractOpenItems records a contract receipt and returns failed checks
// as handoff open items. Handoff never blocks; it surfaces the debt.
func handoffContractOpenItems(st *store.Store, project, cwd, session, agent string) []string {
	if st == nil {
		return nil
	}
	policy := contract.ResolvePolicy(cwd)
	if policy.Policy == contract.PolicyOff {
		return nil
	}
	if session == "" {
		session = "unbound"
	}
	sig := contract.NormalizeSignal(contract.Signal{
		Harness: contract.HarnessID(agent), SessionID: session,
		ProjectID: project, Cwd: cwd, Phase: contract.PhaseHandoff,
	})
	d := contract.NewChecker(st, cwd).Evaluate(sig, policy)
	d = contract.RecordDecision(cwd, sig, policy, d, 0)
	if d.Decision != contract.DecisionDeny {
		return nil
	}
	items := contract.FailedChecks(d)
	if d.ReceiptPath != "" {
		items = append(items, "contract receipt: "+d.ReceiptPath)
	}
	return items
}

// handoffRevisionDecision resolves whether a handoff write may proceed. It
// returns the new revision, replay status, the current revision, and whether
// the write is allowed.
func handoffRevisionDecision(st *store.Store, project, branch, expected string, h store.Handoff) (string, bool, string, bool) {
	newRevision := store.HandoffRevision(h)
	if expected == "" {
		return newRevision, false, "", true
	}
	latest, _ := st.LatestHandoffForBranch(project, branch)
	current := ""
	if latest != nil {
		current = store.HandoffRevision(*latest)
	}
	if latest == nil || current != expected {
		return newRevision, false, current, false
	}
	return newRevision, newRevision == current, current, true
}
