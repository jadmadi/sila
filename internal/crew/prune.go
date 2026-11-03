package crew

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

// ResourceType identifies the kind of ephemeral resource evaluated during prune.
type ResourceType string

const (
	ResourceCrewmate    ResourceType = "crewmate"
	ResourceWorktree    ResourceType = "worktree"
	ResourceTmuxSession ResourceType = "tmux_session"
	ResourceTmuxWindow  ResourceType = "tmux_window"
	ResourceLiveLock    ResourceType = "scoped_lock"
)

// PruneAction represents the outcome of evaluating an ephemeral resource.
type PruneAction string

const (
	ActionPruned         PruneAction = "pruned"
	ActionRetained       PruneAction = "retained"
	ActionSkipped        PruneAction = "skipped"
	ActionManualFollowup PruneAction = "manual_followup"
)

// ResourceEvaluation records the disposition and reason for an inspected resource.
type ResourceEvaluation struct {
	Type       ResourceType `json:"type"`
	Identifier string       `json:"identifier"`
	Action     PruneAction  `json:"action"`
	Reason     string       `json:"reason"`
	Details    string       `json:"details,omitempty"`
}

// PruneOptions configures dead resource and scoped-lock pruning.
type PruneOptions struct {
	DryRun  bool     `json:"dry_run"`
	Force   bool     `json:"force"`
	DBPaths []string `json:"db_paths,omitempty"`
}

// PruneReport aggregates all evaluated resources and summary counts.
type PruneReport struct {
	DryRun         bool                 `json:"dry_run"`
	TotalEvaluated int                  `json:"total_evaluated"`
	PrunedCount    int                  `json:"pruned_count"`
	RetainedCount  int                  `json:"retained_count"`
	ManualFollowup int                  `json:"manual_followup_count"`
	Evaluations    []ResourceEvaluation `json:"evaluations"`
}

func (r *PruneReport) add(ev ResourceEvaluation) {
	r.Evaluations = append(r.Evaluations, ev)
	r.TotalEvaluated++
	switch ev.Action {
	case ActionPruned:
		r.PrunedCount++
	case ActionRetained, ActionSkipped:
		r.RetainedCount++
	case ActionManualFollowup:
		r.ManualFollowup++
	}
}

// Prune inventories Sila-owned crew resources and provably dead scoped locks,
// safely removing completed, dead, or orphan resources while strictly preserving
// live continuous-sync watchers, user tmux windows, and dirty worktrees.
func (c *CrewManager) Prune(opts PruneOptions) (*PruneReport, error) {
	rep := &PruneReport{
		DryRun:      opts.DryRun,
		Evaluations: make([]ResourceEvaluation, 0),
	}

	trackedCrewmates, err := c.List()
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("list crewmates for prune: %w", err)
	}

	knownCrewIDs := make(map[string]bool, len(trackedCrewmates))
	liveCrewIDs := make(map[string]bool)
	for _, cm := range trackedCrewmates {
		knownCrewIDs[cm.ID] = true
	}

	// 1. Evaluate tracked crewmates, their worktrees, and their tmux windows
	for _, cm := range trackedCrewmates {
		isLive := false
		isReused := false

		if cm.PID > 0 {
			ident := store.InspectProcess(cm.PID)
			if ident.Alive {
				// Recheck PID start identity: if process started after crewmate was spawned, PID was reused
				if !cm.SpawnedAt.IsZero() && !ident.StartTime.IsZero() && ident.StartTime.After(cm.SpawnedAt.Add(10*time.Second)) {
					isReused = true
				} else if ident.Cmdline != "" && !strings.Contains(ident.Cmdline, cm.Harness) && !strings.Contains(ident.Cmdline, "sleep") && !strings.Contains(ident.Cmdline, "test") {
					isReused = true
				} else {
					isLive = true
				}
			}
		}

		if isLive {
			liveCrewIDs[cm.ID] = true
			rep.add(ResourceEvaluation{
				Type:       ResourceCrewmate,
				Identifier: cm.ID,
				Action:     ActionRetained,
				Reason:     fmt.Sprintf("live crewmate process (PID #%d running)", cm.PID),
			})
			continue
		}

		if isReused {
			rep.add(ResourceEvaluation{
				Type:       ResourceCrewmate,
				Identifier: cm.ID,
				Action:     ActionRetained,
				Reason:     fmt.Sprintf("PID #%d reused by unrelated process; preserved without termination", cm.PID),
			})
		}

		// Evaluate worktree for dead or completed crewmate
		wtPath := cm.Worktree
		if wtPath == "" {
			wtPath = filepath.Join(c.wtMgr.WorktreeDir(), "crew-"+cm.ID)
		}

		worktreeClean := true
		if fi, statErr := os.Stat(wtPath); statErr == nil && fi.IsDir() {
			dirty, dirtySummary, checkErr := c.isWorktreeDirtyWithCrewmate(wtPath, &cm)
			if checkErr != nil {
				worktreeClean = false
				rep.add(ResourceEvaluation{
					Type:       ResourceWorktree,
					Identifier: wtPath,
					Action:     ActionManualFollowup,
					Reason:     fmt.Sprintf("cannot inspect worktree: %v", checkErr),
				})
			} else if dirty {
				worktreeClean = false
				rep.add(ResourceEvaluation{
					Type:       ResourceWorktree,
					Identifier: wtPath,
					Action:     ActionManualFollowup,
					Reason:     "worktree contains uncommitted or untracked files; manual follow-up required",
					Details:    dirtySummary,
				})
			} else {
				if opts.DryRun {
					rep.add(ResourceEvaluation{
						Type:       ResourceWorktree,
						Identifier: wtPath,
						Action:     ActionSkipped,
						Reason:     "clean worktree of completed crewmate would be removed",
					})
				} else {
					wtName := filepath.Base(wtPath)
					if c.isContextFileUnmodified(wtPath, &cm) {
						_ = os.Remove(filepath.Join(wtPath, "SILA_CONTEXT.md"))
					} else {
						worktreeClean = false
						rep.add(ResourceEvaluation{
							Type:       ResourceWorktree,
							Identifier: wtPath,
							Action:     ActionManualFollowup,
							Reason:     "SILA_CONTEXT.md modified from baseline; preserved",
						})
						continue
					}
					if removeErr := c.wtMgr.Remove(wtName, false); removeErr != nil {
						rep.add(ResourceEvaluation{
							Type:       ResourceWorktree,
							Identifier: wtPath,
							Action:     ActionManualFollowup,
							Reason:     fmt.Sprintf("worktree removal failed: %v", removeErr),
						})
					} else {
						rep.add(ResourceEvaluation{
							Type:       ResourceWorktree,
							Identifier: wtPath,
							Action:     ActionPruned,
							Reason:     "removed clean worktree of completed crewmate",
						})
					}
				}
			}
		}

		// Evaluate tmux window
		if tmuxAvailable() && cm.TmuxSession != "" && cm.TmuxWindow != "" {
			if windowExists(cm.TmuxSession, cm.TmuxWindow) {
				if opts.DryRun {
					rep.add(ResourceEvaluation{
						Type:       ResourceTmuxWindow,
						Identifier: cm.TmuxSession + ":" + cm.TmuxWindow,
						Action:     ActionSkipped,
						Reason:     "dead crewmate tmux window would be closed",
					})
				} else {
					exec.Command("tmux", "kill-window", "-t", cm.TmuxSession+":"+cm.TmuxWindow).Run()
					rep.add(ResourceEvaluation{
						Type:       ResourceTmuxWindow,
						Identifier: cm.TmuxSession + ":" + cm.TmuxWindow,
						Action:     ActionPruned,
						Reason:     "closed dead crewmate tmux window",
					})
				}
			}
		}

		// Prune state record if worktree was clean or absent
		if worktreeClean && !isReused {
			if opts.DryRun {
				rep.add(ResourceEvaluation{
					Type:       ResourceCrewmate,
					Identifier: cm.ID,
					Action:     ActionSkipped,
					Reason:     "completed crewmate record would be pruned",
				})
			} else {
				_ = c.Delete(cm.ID)
				rep.add(ResourceEvaluation{
					Type:       ResourceCrewmate,
					Identifier: cm.ID,
					Action:     ActionPruned,
					Reason:     "pruned completed crewmate record",
				})
			}
		} else if !worktreeClean {
			rep.add(ResourceEvaluation{
				Type:       ResourceCrewmate,
				Identifier: cm.ID,
				Action:     ActionRetained,
				Reason:     "retained completed crewmate record due to dirty worktree",
			})
		}
	}

	// 2. Scan for orphan worktrees under .git/sila-worktrees/
	wtDir := c.wtMgr.WorktreeDir()
	if entries, err := os.ReadDir(wtDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() || !strings.HasPrefix(e.Name(), "crew-") {
				continue
			}
			crewID := strings.TrimPrefix(e.Name(), "crew-")
			if knownCrewIDs[crewID] {
				continue // already evaluated in phase 1
			}

			orphanPath := filepath.Join(wtDir, e.Name())
			dirty, dirtySummary, checkErr := c.isWorktreeDirtyWithCrewmate(orphanPath, nil)
			if checkErr != nil {
				rep.add(ResourceEvaluation{
					Type:       ResourceWorktree,
					Identifier: orphanPath,
					Action:     ActionManualFollowup,
					Reason:     fmt.Sprintf("cannot inspect orphan worktree: %v", checkErr),
				})
			} else if dirty {
				rep.add(ResourceEvaluation{
					Type:       ResourceWorktree,
					Identifier: orphanPath,
					Action:     ActionManualFollowup,
					Reason:     "orphan worktree contains uncommitted or untracked files; manual follow-up required",
					Details:    dirtySummary,
				})
			} else {
				if opts.DryRun {
					rep.add(ResourceEvaluation{
						Type:       ResourceWorktree,
						Identifier: orphanPath,
						Action:     ActionSkipped,
						Reason:     "orphan clean worktree would be removed",
					})
				} else {
					if c.isContextFileUnmodified(orphanPath, nil) {
						_ = os.Remove(filepath.Join(orphanPath, "SILA_CONTEXT.md"))
					} else {
						rep.add(ResourceEvaluation{
							Type:       ResourceWorktree,
							Identifier: orphanPath,
							Action:     ActionManualFollowup,
							Reason:     "orphan worktree SILA_CONTEXT.md modified from baseline; preserved",
						})
						continue
					}
					if removeErr := c.wtMgr.Remove(e.Name(), false); removeErr != nil {
						rep.add(ResourceEvaluation{
							Type:       ResourceWorktree,
							Identifier: orphanPath,
							Action:     ActionManualFollowup,
							Reason:     fmt.Sprintf("failed removing orphan worktree: %v", removeErr),
						})
					} else {
						rep.add(ResourceEvaluation{
							Type:       ResourceWorktree,
							Identifier: orphanPath,
							Action:     ActionPruned,
							Reason:     "removed orphan clean worktree",
						})
					}
				}
			}
		}
	}

	// 3. Evaluate Sila-owned tmux session (sila-crew)
	if tmuxAvailable() {
		session := "sila-crew"
		cmd := exec.Command("tmux", "has-session", "-t", session)
		if cmd.Run() == nil {
			windows, listErr := listTmuxWindows(session)
			if listErr == nil {
				var activeCrewWins []string
				var userWins []string

				for _, w := range windows {
					if liveCrewIDs[w] {
						activeCrewWins = append(activeCrewWins, w)
					} else if !knownCrewIDs[w] {
						userWins = append(userWins, w)
					}
				}

				if len(activeCrewWins) > 0 {
					rep.add(ResourceEvaluation{
						Type:       ResourceTmuxSession,
						Identifier: session,
						Action:     ActionRetained,
						Reason:     fmt.Sprintf("session contains %d active crew window(s)", len(activeCrewWins)),
					})
				} else if len(userWins) > 0 {
					rep.add(ResourceEvaluation{
						Type:       ResourceTmuxSession,
						Identifier: session,
						Action:     ActionRetained,
						Reason:     fmt.Sprintf("session contains %d user window(s) (%s); preserved", len(userWins), strings.Join(userWins, ", ")),
					})
				} else {
					// No active crew and no user windows
					if opts.DryRun {
						rep.add(ResourceEvaluation{
							Type:       ResourceTmuxSession,
							Identifier: session,
							Action:     ActionSkipped,
							Reason:     "idle Sila-owned tmux session with no live crew or user windows would be removed",
						})
					} else {
						exec.Command("tmux", "kill-session", "-t", session).Run()
						rep.add(ResourceEvaluation{
							Type:       ResourceTmuxSession,
							Identifier: session,
							Action:     ActionPruned,
							Reason:     "terminated idle Sila-owned tmux session (no live crew or user windows)",
						})
					}
				}
			}
		}
	}

	// 4. Evaluate scoped live locks
	candidateDBs := opts.DBPaths
	if len(candidateDBs) == 0 {
		wsDB := filepath.Join(c.repoRoot, ".sila", "k.db")
		if _, err := os.Stat(filepath.Dir(wsDB)); err == nil {
			candidateDBs = append(candidateDBs, wsDB)
		}
		if defDB := store.DefaultDBPath(); defDB != "" {
			candidateDBs = append(candidateDBs, defDB)
		}
	}

	for _, dbPath := range candidateDBs {
		evals, lockErr := store.PruneDeadLocks(dbPath, store.PruneDeadLocksOpts{
			DryRun: opts.DryRun,
			Force:  opts.Force,
		})
		if lockErr != nil {
			continue
		}
		for _, le := range evals {
			var act PruneAction
			switch le.Action {
			case "pruned":
				act = ActionPruned
			case "manual_followup":
				act = ActionManualFollowup
			default:
				if opts.DryRun && strings.Contains(le.Reason, "would prune") {
					act = ActionSkipped
				} else {
					act = ActionRetained
				}
			}
			rep.add(ResourceEvaluation{
				Type:       ResourceLiveLock,
				Identifier: le.Path,
				Action:     act,
				Reason:     le.Reason,
			})
		}
	}

	return rep, nil
}

func buildBaselineContextString(projectID, repoRoot, wtPath, handoffSummary string, personas []string) string {
	var b strings.Builder
	b.WriteString("# sila · worktree context\n\n")
	b.WriteString("> This worktree is a disposable agent sandbox managed by `sila worktree`.\n")
	b.WriteString("> Run `sila worktree clean <name>` when done to remove it.\n\n")
	fmt.Fprintf(&b, "**Project**: %s\n", projectID)
	fmt.Fprintf(&b, "**Repo root**: %s\n", repoRoot)
	fmt.Fprintf(&b, "**Worktree**: %s\n", wtPath)
	if handoffSummary != "" {
		fmt.Fprintf(&b, "\n## Handoff\n\n%s\n", handoffSummary)
	}
	b.WriteString("\n## Next steps\n\n")
	b.WriteString("1. Read this file for context.\n")
	b.WriteString("2. Run `sila prime` for full project briefing.\n")
	b.WriteString("3. Execute your task.\n")
	b.WriteString("4. Run `sila gate` before reporting completion.\n")
	b.WriteString("5. Run `sila worktree clean <name>` to dispose.\n")
	base := b.String()
	if len(personas) > 0 {
		base += renderPersonaContextString(personas)
	}
	return base
}

func (c *CrewManager) isContextFileUnmodified(wtPath string, cm *Crewmate) bool {
	ctxPath := filepath.Join(wtPath, "SILA_CONTEXT.md")
	data, err := os.ReadFile(ctxPath)
	if err != nil {
		if os.IsNotExist(err) {
			return true // No context file present to preserve
		}
		return false // Unreadable: fail closed
	}

	actualHash := hashBytes(data)

	// 1. If crewmate has a recorded ContextHash from spawn time, verify against it
	if cm != nil && cm.ContextHash != "" {
		return actualHash == cm.ContextHash
	}

	// 2. Deterministic baseline checks for orphan worktrees or legacy crewmates
	cleanWtPath := filepath.Clean(wtPath)
	var personas []string
	if cm != nil {
		personas = cm.AssignedPersonas
	}

	// Candidate baseline 1: projectID = c.repoRoot (used by crew.SpawnWithSurface)
	if actualHash == hashBytes([]byte(buildBaselineContextString(c.repoRoot, c.repoRoot, cleanWtPath, "", personas))) {
		return true
	}

	// Candidate baseline 2: projectID = store.ProjectID(c.repoRoot) (used by cmd/sila/worktree.go)
	projID := store.ProjectID(c.repoRoot)
	if projID != c.repoRoot {
		if actualHash == hashBytes([]byte(buildBaselineContextString(projID, c.repoRoot, cleanWtPath, "", personas))) {
			return true
		}
	}

	// Candidate baseline 3 & 4: without personas
	if len(personas) > 0 {
		if actualHash == hashBytes([]byte(buildBaselineContextString(c.repoRoot, c.repoRoot, cleanWtPath, "", nil))) {
			return true
		}
		if projID != c.repoRoot {
			if actualHash == hashBytes([]byte(buildBaselineContextString(projID, c.repoRoot, cleanWtPath, "", nil))) {
				return true
			}
		}
	}

	return false
}

func isWorktreeDirty(wtPath string) (bool, string, error) {
	c := &CrewManager{}
	return c.isWorktreeDirtyWithCrewmate(wtPath, nil)
}

func (c *CrewManager) isWorktreeDirtyWithCrewmate(wtPath string, cm *Crewmate) (bool, string, error) {
	if _, err := os.Stat(wtPath); err != nil {
		if os.IsNotExist(err) {
			return false, "", nil
		}
		return false, "", err
	}
	cmd := exec.Command("git", "-C", wtPath, "status", "--porcelain")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, "", fmt.Errorf("git status in %s: %w", wtPath, err)
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return false, "", nil
	}
	var meaningful []string
	for line := range strings.SplitSeq(trimmed, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// If porcelain mentions SILA_CONTEXT.md, only ignore if it is unmodified
		if strings.HasSuffix(line, "SILA_CONTEXT.md") {
			if c.isContextFileUnmodified(wtPath, cm) {
				continue
			}
			meaningful = append(meaningful, line+" (user-modified context)")
			continue
		}
		meaningful = append(meaningful, line)
	}
	if len(meaningful) == 0 {
		return false, "", nil
	}
	firstFew := meaningful
	if len(firstFew) > 3 {
		firstFew = meaningful[:3]
	}
	summary := fmt.Sprintf("%d file(s) modified or untracked: %s", len(meaningful), strings.Join(firstFew, "; "))
	return true, summary, nil
}

func windowExists(session, window string) bool {
	cmd := exec.Command("tmux", "list-windows", "-t", session, "-F", "#{window_name}")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		if strings.TrimSpace(line) == window {
			return true
		}
	}
	return false
}

func listTmuxWindows(session string) ([]string, error) {
	cmd := exec.Command("tmux", "list-windows", "-t", session, "-F", "#{window_name}")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var names []string
	for line := range strings.SplitSeq(string(out), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			names = append(names, trimmed)
		}
	}
	return names, nil
}
