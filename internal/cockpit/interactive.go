package cockpit

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

// FormatInterval formats a duration human-readably (e.g. "1s", "15s", "1m", "10m").
func FormatInterval(d time.Duration) string {
	if d <= 0 {
		return "1s"
	}
	secs := int(d.Seconds() + 0.5)
	if secs < 60 {
		return fmt.Sprintf("%ds", secs)
	}
	mins := secs / 60
	rem := secs % 60
	if rem == 0 {
		return fmt.Sprintf("%dm", mins)
	}
	return fmt.Sprintf("%dm %ds", mins, rem)
}

// interactiveState tracks live operator state, project filter, and transient notices.
type interactiveState struct {
	mu               sync.RWMutex
	isPaused         atomic.Bool
	currentProject   string
	latestSnap       *CockpitSnapshot
	lastStatusNotice string
	noticeExpiry     time.Time
}

func (s *interactiveState) handleKey(k byte, st *store.Store, project string, syncInterval time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch k {
	case 'q', 'Q', 3: // Ctrl+C
		return true

	case ' ':
		newPaused := !s.isPaused.Load()
		s.isPaused.Store(newPaused)
		if newPaused {
			s.lastStatusNotice = "PAUSED (Press Space to Resume)"
			s.noticeExpiry = time.Now().Add(10 * time.Minute)
		} else {
			s.lastStatusNotice = fmt.Sprintf("RESUMED (Live %s)", FormatInterval(syncInterval))
			s.noticeExpiry = time.Now().Add(2 * time.Second)
		}
		return false

	case 'a', 'A':
		if s.latestSnap != nil && s.latestSnap.Approvals.HasPending && len(s.latestSnap.Approvals.Pending) > 0 {
			top := s.latestSnap.Approvals.Pending[0]
			if st != nil {
				err := st.UpdateApprovalDecision(top.ID, "approved", "cockpit_operator", "Approved via live cockpit [a] hotkey")
				if err == nil {
					s.lastStatusNotice = fmt.Sprintf("✔ Approved #%s", top.ID)
				} else {
					s.lastStatusNotice = fmt.Sprintf("✖ Approve error: %v", err)
				}
				s.noticeExpiry = time.Now().Add(3 * time.Second)
			}
		} else {
			s.lastStatusNotice = "ℹ No pending holds to approve"
			s.noticeExpiry = time.Now().Add(2 * time.Second)
		}
		return false

	case 'r', 'R':
		if s.latestSnap != nil && s.latestSnap.Approvals.HasPending && len(s.latestSnap.Approvals.Pending) > 0 {
			top := s.latestSnap.Approvals.Pending[0]
			if st != nil {
				err := st.UpdateApprovalDecision(top.ID, "rejected", "cockpit_operator", "Rejected via live cockpit [r] hotkey")
				if err == nil {
					s.lastStatusNotice = fmt.Sprintf("✖ Rejected #%s", top.ID)
				} else {
					s.lastStatusNotice = fmt.Sprintf("✖ Reject error: %v", err)
				}
				s.noticeExpiry = time.Now().Add(3 * time.Second)
			}
		} else {
			s.lastStatusNotice = "ℹ No pending holds to reject"
			s.noticeExpiry = time.Now().Add(2 * time.Second)
		}
		return false

	case 'p', 'P':
		if st != nil {
			projs, err := st.Projects()
			if err == nil && len(projs) > 0 {
				// Discover projects that currently have active claims or in-flight goals
				var activeProjects []string
				seenActive := make(map[string]bool)
				if s.latestSnap != nil {
					for _, a := range s.latestSnap.Fleet.Agents {
						if a.Project != "" && !seenActive[a.Project] {
							seenActive[a.Project] = true
							activeProjects = append(activeProjects, a.Project)
						}
					}
					for _, r := range s.latestSnap.Roadmap.InFlight {
						if r.Project != "" && !seenActive[r.Project] {
							seenActive[r.Project] = true
							activeProjects = append(activeProjects, r.Project)
						}
					}
				}

				// If there are projects with active work, cycle through them first
				candidates := activeProjects
				if len(candidates) == 0 {
					// Fallback: cycle through all registered substrate projects
					for _, pr := range projs {
						candidates = append(candidates, pr.ID)
					}
				}

				nextProj := ""
				found := false
				for idx, id := range candidates {
					if s.currentProject == id {
						if idx+1 < len(candidates) {
							nextProj = candidates[idx+1]
						} else {
							nextProj = "" // back to global
						}
						found = true
						break
					}
				}
				if !found {
					nextProj = candidates[0]
				}
				s.currentProject = nextProj
				if s.currentProject == "" {
					s.lastStatusNotice = "Filter: Global Workspace (All Projects)"
				} else {
					s.lastStatusNotice = fmt.Sprintf("Filter: Project %s", s.currentProject)
				}
				s.noticeExpiry = time.Now().Add(2 * time.Second)
				return false
			}
		}
		if s.currentProject == "" && project != "" {
			s.currentProject = project
			s.lastStatusNotice = fmt.Sprintf("Filter: Project %s", s.currentProject)
		} else if s.currentProject != "" {
			s.currentProject = ""
			s.lastStatusNotice = "Filter: Global Workspace (All Projects)"
		} else {
			s.lastStatusNotice = "Filter: Global Workspace (All Projects)"
		}
		s.noticeExpiry = time.Now().Add(2 * time.Second)
		return false
	}
	return false
}

// RunLive starts the interactive, full-screen live 9-box mission control cockpit.
func RunLive(ctx context.Context, st *store.Store, project, workspaceDir string, syncInterval time.Duration, syncFn func()) error {
	if syncInterval <= 0 {
		syncInterval = 15 * time.Second
	}

	// 1. Enter alternate screen buffer & hide cursor
	fmt.Fprint(os.Stdout, "\033[?1049h\033[?25l\033[2J\033[H")
	defer func() {
		// Restore cursor & alternate screen buffer
		fmt.Fprint(os.Stdout, "\033[?25h\033[?1049l")
	}()

	// 2. Put stdin into raw mode for instant keypress handling
	restoreStdin := setupRawStdin()
	defer restoreStdin()

	// 3. Signals
	sigChan := make(chan os.Signal, 2)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// 4. Stdin reader goroutine
	keyChan := make(chan byte, 16)
	stopInput := make(chan struct{})
	go func() {
		buf := make([]byte, 1)
		for {
			select {
			case <-stopInput:
				return
			default:
				n, err := os.Stdin.Read(buf)
				if err != nil || n == 0 {
					return
				}
				keyChan <- buf[0]
			}
		}
	}()
	defer close(stopInput)

	// 5. State tracking
	state := &interactiveState{
		currentProject: project,
	}

	drawScreen := func() {
		w, _ := terminalDimensions()
		state.mu.RLock()
		snap := state.latestSnap
		if snap == nil {
			state.mu.RUnlock()
			return
		}

		if state.isPaused.Load() {
			snap.Header.Pulse = "PAUSED (Space to Resume)"
			snap.Header.FleetHealth = "PAUSED"
		} else {
			snap.Header.Pulse = fmt.Sprintf("LIVE (%s)", FormatInterval(syncInterval))
		}
		if time.Now().Before(state.noticeExpiry) && state.lastStatusNotice != "" {
			snap.Header.Pulse = state.lastStatusNotice
		}

		output := RenderSnapshot(snap, w, true)
		state.mu.RUnlock()

		// Move cursor to home and clear rest of screen cleanly
		cursorHome := string([]byte{0x1b, '[', 'H'})
		clearScreen := string([]byte{0x1b, '[', 'J'})
		fmt.Fprint(os.Stdout, cursorHome+output+clearScreen)
	}

	var isCollecting atomic.Bool
	collectNow := func() {
		if isCollecting.Swap(true) {
			return
		}
		go func() {
			defer isCollecting.Store(false)
			state.mu.RLock()
			curProj := state.currentProject
			state.mu.RUnlock()

			targetDir := workspaceDir
			if st != nil && curProj != "" {
				if projs, err := st.Projects(); err == nil {
					for _, pr := range projs {
						if pr.ID == curProj && pr.Path != "" {
							targetDir = pr.Path
							break
						}
					}
				}
			}
			snap, err := CollectSnapshot(ctx, st, curProj, targetDir)
			if err == nil && snap != nil {
				state.mu.Lock()
				state.latestSnap = snap
				state.mu.Unlock()
				drawScreen()
			}
		}()
	}

	// Initial render: gather initial snapshot synchronously so first frame renders immediately
	initialTargetDir := workspaceDir
	if st != nil && project != "" {
		if projs, err := st.Projects(); err == nil {
			for _, pr := range projs {
				if pr.ID == project && pr.Path != "" {
					initialTargetDir = pr.Path
					break
				}
			}
		}
	}
	initSnap, err := CollectSnapshot(ctx, st, project, initialTargetDir)
	if err == nil && initSnap != nil {
		state.mu.Lock()
		state.latestSnap = initSnap
		state.mu.Unlock()
		drawScreen()
	}

	// 6. Background sync & claim distillation runner
	redrawChan := make(chan struct{}, 1)
	stopSync := make(chan struct{})
	var syncWg sync.WaitGroup
	defer func() {
		close(stopSync)
		syncWg.Wait()
	}()

	if syncFn != nil {
		syncWg.Add(1)
		go func() {
			defer syncWg.Done()
			// Run initial sync shortly after start
			if !state.isPaused.Load() {
				syncFn()
				select {
				case redrawChan <- struct{}{}:
				default:
				}
			}

			syncTicker := time.NewTicker(syncInterval)
			defer syncTicker.Stop()

			for {
				select {
				case <-ctx.Done():
					return
				case <-stopSync:
					return
				case <-syncTicker.C:
					if !state.isPaused.Load() {
						syncFn()
						select {
						case redrawChan <- struct{}{}:
						default:
						}
					}
				}
			}
		}()
	}

	// Periodic telemetry refresh ticker (syncInterval) during idle periods
	collectTicker := time.NewTicker(syncInterval)
	defer collectTicker.Stop()

	// 7. Live 1-second refresh ticker for UI counters & animations (zero I/O redraw)
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil

		case <-sigChan:
			return nil

		case <-redrawChan:
			collectNow()

		case <-collectTicker.C:
			if !state.isPaused.Load() {
				collectNow()
			}

		case k := <-keyChan:
			if state.handleKey(k, st, project, syncInterval) {
				return nil
			}
			drawScreen()
			collectNow()

		case <-ticker.C:
			drawScreen()
		}
	}
}
