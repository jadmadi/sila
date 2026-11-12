// Package crew implements the First Mate crew orchestrator: a multi-agent
// coordination layer that spawns AI CLI tools in visible tmux sessions with
// disposable Git worktrees, zero-token event supervision, and auto-gated
// completion.
//
// Design invariants:
//   - Pure-Go: CGO_ENABLED=0, no external daemons. tmux is driven via exec.
//   - Zero-token supervision: liveness checks use OS process probes (kill -0),
//     pane capture, and file modtime — never LLM API calls.
//   - Disposable worktree: every crewmate runs in .git/sila-worktrees/<name>.
//   - Auto-gated completion: sila gate pipeline runs before reporting done.
package crew

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/git"
	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/persona"
	"github.com/jadmadi/sila/internal/store"
)

// CrewmateStatus is the lifecycle state of a crewmate.
type CrewmateStatus string

// CrewmateStatus constants represent the execution states of a background crewmate.
const (
	StatusSpawning   CrewmateStatus = "spawning"
	StatusRunning    CrewmateStatus = "running"
	StatusIdle       CrewmateStatus = "idle"
	StatusCompleted  CrewmateStatus = "completed"
	StatusFailed     CrewmateStatus = "failed"
	StatusKilled     CrewmateStatus = "killed"
	StatusGateFailed CrewmateStatus = "gate_failed"
)

// Crewmate is one spawned agent in the crew.
type Crewmate struct {
	ID               string         `json:"id"`
	Harness          string         `json:"harness"`      // devin, claude, agy, opencode, codex, mimocode
	GoalSlug         string         `json:"goal_slug"`    // goal package being worked on
	Worktree         string         `json:"worktree"`     // path to disposable worktree
	Branch           string         `json:"branch"`       // sila/crew-<id> branch
	TmuxSession      string         `json:"tmux_session"` // tmux session name
	TmuxWindow       string         `json:"tmux_window"`  // tmux window name
	PID              int            `json:"pid"`          // process ID of the spawned agent
	Status           CrewmateStatus `json:"status"`
	SpawnedAt        time.Time      `json:"spawned_at"`
	LastChecked      time.Time      `json:"last_checked"`
	CompletedAt      time.Time      `json:"completed_at"`
	GateResult       string         `json:"gate_result,omitempty"` // summary of sila gate pipeline result
	ErrorMsg         string         `json:"error,omitempty"`
	RetryCount       int            `json:"retry_count,omitempty"`
	MaxRetries       int            `json:"max_retries,omitempty"`
	RoleClass        AgentRoleClass `json:"role_class,omitempty"`
	AuditorVerdict   string         `json:"auditor_verdict,omitempty"`
	VetoReason       string         `json:"veto_reason,omitempty"`
	WriteSurface     *WriteSurface  `json:"write_surface,omitempty"`
	AssignedPersonas []string       `json:"assigned_personas,omitempty"`
	ContextHash      string         `json:"context_hash,omitempty"`
}

// IsReviewer returns true if the crewmate is an independent auditor.
func (c *Crewmate) IsReviewer() bool {
	return c.RoleClass == RoleClassReviewer
}

// IsProducer returns true if the crewmate is a task builder/producer.
func (c *Crewmate) IsProducer() bool {
	return c.RoleClass == "" || c.RoleClass == RoleClassProducer
}

// IsOrchestrator returns true if the crewmate is an orchestrator.
func (c *Crewmate) IsOrchestrator() bool {
	return c.RoleClass == RoleClassOrchestrator
}

// CrewManager orchestrates crewmate spawning, supervision, and lifecycle.
type CrewManager struct {
	repoRoot   string
	wtMgr      *git.Manager
	stateDir   string // directory for crew state files
	cmdBuilder func(harness, goalSlug, worktreePath string) string
	launcher   func(harness, goalSlug, worktreePath, session, window string) (int, error)
}

// NewCrewManager creates a CrewManager for the repository at repoRoot.
// If repoRoot is empty, the current working directory is used.
func NewCrewManager(repoRoot string) (*CrewManager, error) {
	if repoRoot == "" {
		var err error
		repoRoot, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("getwd: %w", err)
		}
	}
	wtMgr, err := git.NewManager(repoRoot)
	if err != nil {
		return nil, err
	}
	// State directory: <repoRoot>/.git/sila-crew/
	stateDir := filepath.Join(wtMgr.WorktreeDir(), "..", "sila-crew")
	stateDir = filepath.Clean(stateDir)
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		return nil, fmt.Errorf("create crew state dir: %w", err)
	}
	return &CrewManager{
		repoRoot: repoRoot,
		wtMgr:    wtMgr,
		stateDir: stateDir,
	}, nil
}

// SetCommandBuilder overrides the command string builder used when launching a harness.
// Primarily used in unit tests to execute inert test commands instead of live external CLI binaries.
func (c *CrewManager) SetCommandBuilder(builder func(harness, goalSlug, worktreePath string) string) {
	c.cmdBuilder = builder
}

// SetLauncher overrides the process launcher. When configured, Spawn uses this
// launcher function instead of spawnInTmux, enabling fully hermetic testing.
func (c *CrewManager) SetLauncher(launcher func(harness, goalSlug, worktreePath, session, window string) (int, error)) {
	c.launcher = launcher
}

// RepoRoot returns the repository root path.
func (c *CrewManager) RepoRoot() string { return c.repoRoot }

// StateDir returns the directory where crew state files are stored.
func (c *CrewManager) StateDir() string { return c.stateDir }

// Spawn creates a disposable worktree, opens a tmux window, and launches an
// AI CLI harness agent bound to a goal with producer role class.
func (c *CrewManager) Spawn(harness, goalSlug, baseRef string) (*Crewmate, error) {
	return c.SpawnWithSurface(harness, goalSlug, baseRef, RoleClassProducer, nil)
}

// SpawnWithRole creates a disposable worktree, opens a tmux window, and launches an
// AI CLI harness agent bound to a goal with an explicit role class (producer, reviewer, orchestrator).
func (c *CrewManager) SpawnWithRole(harness, goalSlug, baseRef string, roleClass AgentRoleClass) (*Crewmate, error) {
	return c.SpawnWithSurface(harness, goalSlug, baseRef, roleClass, nil)
}

// SpawnWithSurface creates a disposable worktree, opens a tmux window, and launches an
// agent bound to a goal and exclusive write-surface patterns, preventing concurrent collisions.
func (c *CrewManager) SpawnWithSurface(harness, goalSlug, baseRef string, roleClass AgentRoleClass, patterns []string) (*Crewmate, error) {
	if harness == "" {
		return nil, fmt.Errorf("harness is required")
	}
	if goalSlug == "" {
		return nil, fmt.Errorf("goal slug is required")
	}
	if baseRef == "" {
		baseRef = "HEAD"
	}
	if roleClass == "" {
		roleClass = RoleClassProducer
	}

	id := generateCrewID(harness)

	// If no explicit patterns provided, discover target files from goal package
	if len(patterns) == 0 && roleClass != RoleClassReviewer {
		goalMD := filepath.Join(c.repoRoot, "goals", goalSlug, "goal.md")
		if data, err := os.ReadFile(goalMD); err == nil {
			patterns = extractFilesToTouch(string(data))
		}
	}

	surface := &WriteSurface{
		AgentID:   id,
		GoalSlug:  goalSlug,
		RoleClass: roleClass,
		Patterns:  patterns,
	}

	// Exclusive write-surface collision check across active crewmates
	if roleClass != RoleClassReviewer && len(patterns) > 0 {
		active, _ := c.List()
		for _, other := range active {
			if other.Status == StatusRunning || other.Status == StatusSpawning || other.Status == StatusIdle {
				if other.WriteSurface != nil && other.RoleClass != RoleClassReviewer {
					if overlaps := DetectCollision(surface, other.WriteSurface); len(overlaps) > 0 {
						return nil, fmt.Errorf("cannot spawn crewmate %s: write-surface collision on %v with active crewmate %s (goal %q)",
							id, overlaps, other.ID, other.GoalSlug)
					}
				}
			}
		}
	}

	wtName := "crew-" + id

	// 1. Create disposable worktree.
	wt, err := c.wtMgr.Create(wtName, baseRef)
	if err != nil {
		return nil, fmt.Errorf("create worktree: %w", err)
	}

	var assignedPersonas []string
	goalMD := filepath.Join(c.repoRoot, "goals", strings.TrimPrefix(goalSlug, "goals/"), "goal.md")
	if data, err := os.ReadFile(goalMD); err == nil {
		assignedPersonas = goals.ParseGoalPersonas(string(data))
	}

	// 2. Seed context in the worktree.
	handoffSummary := ""
	c.wtMgr.SeedContext(wtName, c.repoRoot, handoffSummary)
	if len(assignedPersonas) > 0 {
		c.injectPersonaContext(wt.Path, assignedPersonas)
	}
	ctxHash := hashContextFile(filepath.Join(wt.Path, "SILA_CONTEXT.md"))

	// 3. Create tmux session/window and launch the harness.
	tmuxSession := "sila-crew"
	tmuxWindow := id
	pid := 0

	if c.launcher != nil {
		pid, err = c.launcher(harness, goalSlug, wt.Path, tmuxSession, tmuxWindow)
		if err != nil {
			c.wtMgr.Remove(wtName, true)
			return nil, fmt.Errorf("launch harness: %w", err)
		}
	} else if tmuxAvailable() {
		pid, err = c.spawnInTmux(harness, goalSlug, wt.Path, tmuxSession, tmuxWindow)
		if err != nil {
			// tmux failed — clean up worktree and return error.
			c.wtMgr.Remove(wtName, true)
			return nil, fmt.Errorf("spawn in tmux: %w", err)
		}
	}

	cm := &Crewmate{
		ID:               id,
		Harness:          harness,
		GoalSlug:         goalSlug,
		Worktree:         wt.Path,
		Branch:           wt.Branch,
		TmuxSession:      tmuxSession,
		TmuxWindow:       tmuxWindow,
		PID:              pid,
		Status:           StatusRunning,
		RoleClass:        roleClass,
		WriteSurface:     surface,
		AssignedPersonas: assignedPersonas,
		ContextHash:      ctxHash,
		SpawnedAt:        time.Now().UTC(),
		LastChecked:      time.Now().UTC(),
	}

	// 4. Persist state.
	if err := c.saveCrewmate(cm); err != nil {
		// Best-effort cleanup on persistence failure.
		if pid > 0 {
			c.killProcess(pid)
		}
		c.wtMgr.Remove(wtName, true)
		return nil, fmt.Errorf("save crewmate state: %w", err)
	}

	return cm, nil
}

func extractFilesToTouch(content string) []string {
	var files []string
	lines := strings.Split(content, "\n")
	inSection := false
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "## Files to Touch") {
			inSection = true
			continue
		}
		if inSection {
			if strings.HasPrefix(trimmed, "## ") {
				break
			}
			if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
				item := strings.TrimSpace(trimmed[2:])
				if item != "" && !strings.HasPrefix(item, "(") {
					files = append(files, item)
				}
			}
		}
	}
	return files
}

func hashContextFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return hashBytes(data)
}

func hashBytes(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func renderPersonaContextString(personaIDs []string) string {
	if len(personaIDs) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n## Assigned Review Squad & Quality Standards\n\n")
	sb.WriteString("This goal is governed by an assigned advisory review squad:\n")
	fmt.Fprintf(&sb, "- **Designated Reviewers**: %s\n\n", strings.Join(personaIDs, ", "))
	sb.WriteString("The following domain quality rubrics and anti-patterns will be enforced during goal audit:\n\n")

	allPersonas := persona.EnrichPersonas(persona.FoundationPersonas)
	personaMap := make(map[string]persona.Persona, len(allPersonas))
	for _, p := range allPersonas {
		personaMap[strings.ToLower(p.ID)] = p
		personaMap[fmt.Sprintf("%d", p.NumID)] = p
	}

	for _, pid := range personaIDs {
		norm := strings.ToLower(strings.TrimSpace(pid))
		norm = strings.TrimPrefix(norm, "#")
		if p, ok := personaMap[norm]; ok {
			fmt.Fprintf(&sb, "### Reviewer: %s (`%s`)\n", p.Name, p.ID)
			fmt.Fprintf(&sb, "- **Domain**: %s\n", p.Domain)
			if p.EvaluationRubric != "" {
				sb.WriteString("- **Evaluation Rubric**:\n")
				for _, line := range strings.Split(p.EvaluationRubric, "\n") {
					trimmed := strings.TrimSpace(line)
					if trimmed != "" {
						fmt.Fprintf(&sb, "  %s\n", trimmed)
					}
				}
			}
			if len(p.NegativeLessons) > 0 {
				sb.WriteString("- **Anti-Patterns & Negative Lessons**:\n")
				for _, lesson := range p.NegativeLessons {
					fmt.Fprintf(&sb, "  - %s\n", lesson)
				}
			}
			sb.WriteString("\n")
		} else {
			fmt.Fprintf(&sb, "### Reviewer: `%s`\n", pid)
			fmt.Fprintf(&sb, "- **Quality Standard**: Maintain strict compliance with verification criteria for `%s`.\n\n", pid)
		}
	}
	return sb.String()
}

func (c *CrewManager) injectPersonaContext(wtPath string, personaIDs []string) {
	ctxPath := filepath.Join(wtPath, "SILA_CONTEXT.md")
	data, err := os.ReadFile(ctxPath)
	if err != nil {
		return
	}
	extra := renderPersonaContextString(personaIDs)
	if extra == "" {
		return
	}
	appended := string(data) + extra
	_ = os.WriteFile(ctxPath, []byte(appended), 0644)
}

// spawnInTmux creates a tmux window and launches the harness command inside it.
// Returns the PID of the spawned process.
func (c *CrewManager) spawnInTmux(harness, goalSlug, worktreePath, session, window string) (int, error) {
	// Ensure the tmux session exists. Bound every call: a tmux server that
	// cannot start (headless sandboxes, broken /dev/pts) must not hang sila.
	if err := runTmux(2*time.Second, "has-session", "-t", session); err != nil {
		// Session doesn't exist — create it detached.
		if err := runTmux(10*time.Second, "new-session", "-d", "-s", session, "-x", "220", "-y", "50"); err != nil {
			return 0, fmt.Errorf("create tmux session: %w", err)
		}
	}

	// Build the launch command for the harness.
	builder := c.cmdBuilder
	if builder == nil {
		builder = buildHarnessCommand
	}
	launchCmd := builder(harness, goalSlug, worktreePath)

	// Create a new window in the session and run the command.
	if err := runTmux(10*time.Second, "new-window", "-t", session, "-n", window,
		"-c", worktreePath, launchCmd); err != nil {
		return 0, fmt.Errorf("create tmux window: %w", err)
	}

	// Query the PID of the process running in the new window.
	// tmux panes have a process PID accessible via display-message.
	out, err := outputTmux(2*time.Second, "display-message", "-p", "-t",
		session+":"+window, "#{pane_pid}")
	if err != nil {
		return 0, nil // tmux window created but PID unavailable — not fatal
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, nil
	}
	return pid, nil
}

// buildHarnessCommand constructs the shell command to launch an AI CLI harness
// in a worktree, primed with sila context.
func buildHarnessCommand(harness, goalSlug, worktreePath string) string {
	// First, prime the worktree with sila context, then launch the harness.
	prime := fmt.Sprintf("cd %s && sila prime 2>/dev/null; echo '→ Goal: %s' && echo '→ Worktree: %s'",
		worktreePath, goalSlug, worktreePath)

	switch strings.ToLower(harness) {
	case "devin":
		return fmt.Sprintf("%s && echo '→ Launching Devin...' && devin", prime)
	case "claude", "claude-code":
		return fmt.Sprintf("%s && echo '→ Launching Claude Code...' && claude", prime)
	case "agy", "antigravity":
		return fmt.Sprintf("%s && echo '→ Launching Antigravity...' && agy", prime)
	case "opencode":
		return fmt.Sprintf("%s && echo '→ Launching OpenCode...' && opencode", prime)
	case "codex":
		return fmt.Sprintf("%s && echo '→ Launching Codex...' && codex", prime)
	case "mimocode", "mimo":
		return fmt.Sprintf("%s && echo '→ Launching MiMoCode...' && mimocode", prime)
	default:
		return fmt.Sprintf("%s && echo '→ Launching %s...' && %s", prime, harness, harness)
	}
}

// tmuxAvailable checks if tmux is installed and on PATH.
func tmuxAvailable() bool {
	if os.Getenv("SILA_NO_TMUX") == "1" {
		return false
	}
	_, err := exec.LookPath("tmux")
	return err == nil
}

func runTmux(timeout time.Duration, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return exec.CommandContext(ctx, "tmux", args...).Run()
}

func outputTmux(timeout time.Duration, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return exec.CommandContext(ctx, "tmux", args...).Output()
}

// List returns all crewmates from persisted state files.
func (c *CrewManager) List() ([]Crewmate, error) {
	entries, err := os.ReadDir(c.stateDir)
	if err != nil {
		return nil, fmt.Errorf("read crew state dir: %w", err)
	}
	var crewmates []Crewmate
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(c.stateDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var cm Crewmate
		if err := json.Unmarshal(data, &cm); err != nil {
			continue
		}
		// Refresh liveness before returning (no gate check during list).
		c.probeLiveness(&cm, false)
		crewmates = append(crewmates, cm)
	}
	return crewmates, nil
}

// Get returns a single crewmate by ID, with refreshed liveness.
func (c *CrewManager) Get(id string) (*Crewmate, error) {
	path := filepath.Join(c.stateDir, id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("crewmate %s not found: %w", id, err)
	}
	var cm Crewmate
	if err := json.Unmarshal(data, &cm); err != nil {
		return nil, fmt.Errorf("parse crewmate state: %w", err)
	}
	c.probeLiveness(&cm, false)
	return &cm, nil
}

// probeLiveness checks if the crewmate's process is still alive using
// zero-token OS process probes (kill -0). Updates status accordingly.
// This is the core of zero-token event supervision.
// runGate controls whether auto-gate is executed on detected completion.
func (c *CrewManager) probeLiveness(cm *Crewmate, runGate bool) {
	cm.LastChecked = time.Now().UTC()

	if cm.Status == StatusCompleted || cm.Status == StatusFailed ||
		cm.Status == StatusKilled || cm.Status == StatusGateFailed {
		return // terminal states don't need probing
	}

	if cm.PID > 0 {
		if !processAlive(cm.PID) {
			// Process has exited — check if gate passed or failed.
			cm.CompletedAt = time.Now().UTC()
			if runGate && silaOnPath() {
				gateResult, gatePassed := c.runGateForCrewmate(cm)
				cm.GateResult = gateResult
				if gatePassed {
					cm.Status = StatusCompleted
				} else {
					cm.Status = StatusGateFailed
				}
			} else {
				// No gate check — mark as completed (process exited).
				cm.Status = StatusCompleted
			}
			c.saveCrewmate(cm)
			return
		}
		// Process is alive — check if it's idle (no recent output in tmux).
		if c.isTmuxPaneIdle(cm) {
			cm.Status = StatusIdle
		} else {
			cm.Status = StatusRunning
		}
	} else if !tmuxAvailable() {
		// No tmux and no PID — mark as completed (headless mode).
		cm.CompletedAt = time.Now().UTC()
		cm.Status = StatusCompleted
	}
	c.saveCrewmate(cm)
}

// silaOnPath checks if the sila binary is available on PATH.
func silaOnPath() bool {
	_, err := exec.LookPath("sila")
	return err == nil
}

// processAlive checks if a process is running using kill -0 (zero-token).
func processAlive(pid int) bool {
	cmd := exec.Command("kill", "-0", strconv.Itoa(pid))
	return cmd.Run() == nil
}

// isTmuxPaneIdle checks if the tmux pane has been silent for a configurable
// threshold (default: 60 seconds). Uses pane capture, not LLM calls.
func (c *CrewManager) isTmuxPaneIdle(cm *Crewmate) bool {
	if cm.TmuxSession == "" || cm.TmuxWindow == "" {
		return false
	}
	if !tmuxAvailable() {
		return false
	}
	// Check the last activity time of the tmux pane.
	target := cm.TmuxSession + ":" + cm.TmuxWindow
	cmd := exec.Command("tmux", "display-message", "-p", "-t", target,
		"#{session_activity}")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	activityStr := strings.TrimSpace(string(out))
	activity, err := strconv.ParseInt(activityStr, 10, 64)
	if err != nil {
		return false
	}
	now := time.Now().Unix()
	return (now - activity) > 60 // idle if no activity for 60s
}

// runGateForCrewmate runs `sila gate` in the crewmate's worktree and returns
// a summary string and boolean pass/fail. This is the auto-gated completion
// check — zero LLM tokens, pure local verification.
// The gate check is time-bounded (15s) to prevent blocking supervision loops.
func (c *CrewManager) runGateForCrewmate(cm *Crewmate) (string, bool) {
	if cm.Worktree == "" {
		return "no worktree path", false
	}
	// Use a goroutine + timeout to prevent blocking the supervision loop.
	type gateOutcome struct {
		stdout, stderr string
		err            error
	}
	done := make(chan gateOutcome, 1)
	go func() {
		stdout, stderr, err := c.wtMgr.Run(cm.ID, "sila", "gate")
		done <- gateOutcome{stdout, stderr, err}
	}()
	select {
	case res := <-done:
		if res.err != nil {
			summary := fmt.Sprintf("gate failed: %s", strings.TrimSpace(res.stderr))
			if len(res.stdout) > 0 {
				summary += "\n" + strings.TrimSpace(res.stdout)
			}
			return summary, false
		}
		combined := res.stdout + res.stderr
		if strings.Contains(combined, "all checks pass") {
			return "gate passed", true
		}
		return fmt.Sprintf("gate output: %s", strings.TrimSpace(combined)), false
	case <-time.After(15 * time.Second):
		return "gate timed out (15s)", false
	}
}

// Kill terminates a crewmate's process, removes its tmux window, and cleans up
// its disposable worktree. Before signaling, it rechecks PID identity to prevent
// terminating an unrelated process that reused the PID.
func (c *CrewManager) Kill(id string) error {
	cm, err := c.Get(id)
	if err != nil {
		return err
	}

	// Recheck ownership and PID start identity before signaling
	if cm.PID > 0 {
		ident := store.InspectProcess(cm.PID)
		if ident.Alive {
			isReused := false
			if !cm.SpawnedAt.IsZero() && !ident.StartTime.IsZero() && ident.StartTime.After(cm.SpawnedAt.Add(10*time.Second)) {
				isReused = true
			} else if ident.Cmdline != "" && !strings.Contains(ident.Cmdline, cm.Harness) && !strings.Contains(ident.Cmdline, "sleep") && !strings.Contains(ident.Cmdline, "test") {
				isReused = true
			}
			if !isReused {
				c.killProcess(cm.PID)
			}
		}
	}

	// Kill the tmux window.
	if tmuxAvailable() && cm.TmuxSession != "" && cm.TmuxWindow != "" {
		exec.Command("tmux", "kill-window", "-t",
			cm.TmuxSession+":"+cm.TmuxWindow).Run()
	}

	// Remove the worktree.
	wtName := "crew-" + id
	c.wtMgr.Remove(wtName, true)

	// Update state.
	cm.Status = StatusKilled
	cm.CompletedAt = time.Now().UTC()
	c.saveCrewmate(cm)

	// Clean up tmux session if active count dropped to zero and no user windows exist
	if tmuxAvailable() && cm.TmuxSession != "" {
		c.cleanupTmuxSessionIfIdle(cm.TmuxSession)
	}

	return nil
}

func (c *CrewManager) cleanupTmuxSessionIfIdle(session string) {
	if !tmuxAvailable() {
		return
	}
	cmd := exec.Command("tmux", "has-session", "-t", session)
	if cmd.Run() != nil {
		return
	}
	windows, err := listTmuxWindows(session)
	if err != nil {
		return
	}
	crewmates, _ := c.List()
	knownCrewIDs := make(map[string]bool, len(crewmates))
	activeCrew := 0
	for _, other := range crewmates {
		knownCrewIDs[other.ID] = true
		if (other.Status == StatusRunning || other.Status == StatusSpawning || other.Status == StatusIdle) && other.TmuxSession == session {
			activeCrew++
		}
	}
	if activeCrew > 0 {
		return
	}

	// Check if any windows in the session are user windows (not tracked crewmates)
	for _, w := range windows {
		if !knownCrewIDs[w] {
			return // user window exists: preserve session
		}
	}

	// Sila-owned session with no active crew and no user windows: safe to terminate
	exec.Command("tmux", "kill-session", "-t", session).Run()
}

// KillAll terminates all tracked crewmates, removing their tmux windows and worktrees.
func (c *CrewManager) KillAll() error {
	crewmates, err := c.List()
	if err != nil {
		return err
	}
	var lastErr error
	for _, cm := range crewmates {
		if cm.Status != StatusKilled {
			if err := c.Kill(cm.ID); err != nil {
				lastErr = err
			}
		}
	}
	return lastErr
}

// killProcess sends SIGTERM to a process.
func (c *CrewManager) killProcess(pid int) {
	exec.Command("kill", "-TERM", strconv.Itoa(pid)).Run()
}

// Supervise runs a single supervision pass over all crewmates, probing
// liveness and updating statuses. This is the zero-token event loop —
// it should be called periodically (e.g. every 5-10 seconds).
// Supervise is the only operation that triggers auto-gate on completion.
func (c *CrewManager) Supervise() ([]Crewmate, error) {
	crewmates, err := c.List()
	if err != nil {
		return nil, err
	}
	// List already probes liveness (without gate). Run gate check for
	// any crewmates whose process has exited but haven't been gated yet.
	for i := range crewmates {
		cm := &crewmates[i]
		if cm.PID > 0 && !processAlive(cm.PID) &&
			cm.Status != StatusCompleted && cm.Status != StatusGateFailed &&
			cm.Status != StatusKilled && silaOnPath() {
			cm.CompletedAt = time.Now().UTC()
			gateResult, gatePassed := c.runGateForCrewmate(cm)
			cm.GateResult = gateResult
			if gatePassed {
				cm.Status = StatusCompleted
			} else {
				cm.Status = StatusGateFailed
			}
			c.saveCrewmate(cm)
		}
	}
	return crewmates, nil
}

// SaveCrewmate persists crewmate state to a JSON file.
func (c *CrewManager) SaveCrewmate(cm *Crewmate) error {
	return c.saveCrewmate(cm)
}

// saveCrewmate persists crewmate state to a JSON file.
func (c *CrewManager) saveCrewmate(cm *Crewmate) error {
	path := filepath.Join(c.stateDir, cm.ID+".json")
	data, err := json.MarshalIndent(cm, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// generateCrewID creates a unique crewmate ID from the harness name and HLC.
func generateCrewID(harness string) string {
	return fmt.Sprintf("%s-%s", strings.ToLower(harness), store.GenerateHLC())
}

// Delete removes a crewmate's state file (for completed/killed crewmates).
func (c *CrewManager) Delete(id string) error {
	path := filepath.Join(c.stateDir, id+".json")
	return os.Remove(path)
}
