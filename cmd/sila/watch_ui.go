package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/ingest"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

var watchAnsiRE = regexp.MustCompile("\x1b\\[[0-9;]*m")

// watchSpinnerFrames animates the in-place live status line without new lines.
var watchSpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// isStdoutTTY reports whether stdout is an interactive terminal. In-place
// rewriting is only enabled on TTYs; pipes and log files keep append mode.
func isStdoutTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func stripWatchANSI(s string) string {
	return watchAnsiRE.ReplaceAllString(s, "")
}

func visibleWidthOf(s string) int {
	return utf8.RuneCountInString(stripWatchANSI(s))
}

// truncateANSI shortens s to at most max visible runes, preserving ANSI color
// sequences and appending an ellipsis. Lines longer than the terminal width
// would wrap and break fixed-height in-place redrawing, so watch status lines
// are truncated to the detected width.
func truncateANSI(s string, max int) string {
	if max <= 0 || visibleWidthOf(s) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	budget := max - 1
	var b strings.Builder
	vis := 0
	hadANSI := strings.Contains(s, "\x1b[")
	i := 0
	for i < len(s) {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && !((s[j] >= 'A' && s[j] <= 'Z') || (s[j] >= 'a' && s[j] <= 'z')) {
				j++
			}
			if j < len(s) {
				j++
			}
			b.WriteString(s[i:j])
			i = j
			continue
		}
		if vis >= budget {
			break
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		b.WriteRune(r)
		vis++
		i += size
	}
	b.WriteString("…")
	if hadANSI {
		b.WriteString("\x1b[0m")
	}
	return b.String()
}

// buildWatchLines composes the fixed-height live status block. Line 1 carries
// the spinner, the next-poll countdown, then the sync state; the countdown
// sits before the roadmap pulse so narrow-terminal truncation (which cuts the
// line tail) preserves liveness instead of cutting it. Lines 2+ carry
// decision guidance, errors, or activity and are omitted when empty.
func buildWatchLines(lastTime, spinner, statusCore, countdown string, extraLines ...string) []string {
	line1 := fmt.Sprintf("[%s] %s%s %s", lastTime, spinner, countdown, statusCore)
	lines := []string{line1}
	for _, l := range extraLines {
		if strings.TrimSpace(stripWatchANSI(l)) != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// watchStateHash identifies duplicate idle heartbeats so piped (non-TTY)
// output prints only on state change instead of every poll.
func watchStateHash(statusCore, action string, verified, projCount int) string {
	var b bytes.Buffer
	b.WriteString(statusCore)
	b.WriteByte(0)
	b.WriteString(action)
	fmt.Fprintf(&b, "\x00%d\x00%d", verified, projCount)
	return b.String()
}

// watchRenderer redraws a fixed-height status block in place using ANSI
// cursor movement. It is only used on TTYs; non-TTY output uses append mode.
type watchRenderer struct {
	mu       sync.Mutex
	out      io.Writer
	width    func() int
	prevRows int
}

func (w *watchRenderer) draw(lines []string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	var buf bytes.Buffer
	if w.prevRows > 0 {
		fmt.Fprint(&buf, render.CursorUp(w.prevRows))
		for i := 0; i < w.prevRows; i++ {
			fmt.Fprint(&buf, render.EraseLine())
			if i < w.prevRows-1 {
				fmt.Fprint(&buf, render.CursorDown())
			}
		}
		if w.prevRows > 1 {
			fmt.Fprint(&buf, render.CursorUp(w.prevRows-1))
		} else {
			fmt.Fprint(&buf, "\r")
		}
	}
	wd := 80
	if w.width != nil {
		wd = w.width()
	}
	if wd < 20 {
		wd = 20
	}
	for _, ln := range lines {
		fmt.Fprintln(&buf, truncateANSI(ln, wd))
	}
	w.prevRows = len(lines)
	_, _ = w.out.Write(buf.Bytes())
}

// formatCountdownDuration formats a countdown duration human-readably (e.g. "12s", "1m 30s", "10m").
func formatCountdownDuration(d time.Duration) string {
	secs := int(d.Seconds() + 0.5)
	if secs < 0 {
		secs = 0
	}
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

// WatchSyncState maintains real-time in-memory progress and sync status across polls.
type WatchSyncState struct {
	mu             sync.RWMutex
	Phase          string // "starting", "scanning", "committing", "distilling", "idle", "stopping"
	CurrentTool    string
	ToolIndex      int
	ToolTotal      int
	LastCompletion time.Time
	LastDuration   time.Duration
	NextPoll       time.Time
	Interval       time.Duration
	RecordsAdded   int64
	RecordsMerged  int64
	FactsDistilled int
	VerifiedFacts  int
	ProjectCount   int
	SourceErrors   map[string]string // tool -> error string
	LastSyncError  error
	RecentEvents   []string
	RoadmapPulse   string
	ActionPulse    string
	LastTimestamp  string
}

func newWatchSyncState(interval time.Duration) *WatchSyncState {
	return &WatchSyncState{
		Phase:         "starting",
		Interval:      interval,
		SourceErrors:  make(map[string]string),
		LastTimestamp: time.Now().Format("15:04:05"),
	}
}

func (s *WatchSyncState) addEvent(evt string) {
	s.RecentEvents = append(s.RecentEvents, evt)
	if len(s.RecentEvents) > 5 {
		s.RecentEvents = s.RecentEvents[len(s.RecentEvents)-5:]
	}
}

func (s *WatchSyncState) setScanning(tool string, idx, total int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Phase = "scanning"
	s.CurrentTool = tool
	s.ToolIndex = idx
	s.ToolTotal = total
	s.LastTimestamp = time.Now().Format("15:04:05")
}

func (s *WatchSyncState) setCommitting(tool string, idx, total int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Phase = "committing"
	s.CurrentTool = tool
	s.ToolIndex = idx
	s.ToolTotal = total
	s.LastTimestamp = time.Now().Format("15:04:05")
}

func (s *WatchSyncState) setToolCommitted(tool string, newRec, merged, overlaps int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ts := time.Now().Format("15:04:05")
	s.LastTimestamp = ts
	if err != nil {
		s.SourceErrors[tool] = err.Error()
		s.addEvent(fmt.Sprintf("%s: failed (%v)", tool, err))
	} else {
		delete(s.SourceErrors, tool)
		if newRec > 0 || merged > 0 {
			s.RecordsAdded += int64(newRec)
			s.RecordsMerged += int64(merged)
			s.addEvent(fmt.Sprintf("%s: +%d new, %d merged", tool, newRec, merged))
		}
	}
}

func (s *WatchSyncState) setDistilling() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Phase = "distilling"
	s.CurrentTool = ""
	s.LastTimestamp = time.Now().Format("15:04:05")
}

func (s *WatchSyncState) setIdle(dur time.Duration, nextPoll time.Time, produced, verified, projCount int, gSum *goals.GoalTelemetrySummary, ui render.UI) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Phase = "idle"
	s.CurrentTool = ""
	s.LastCompletion = time.Now()
	s.LastDuration = dur
	s.NextPoll = nextPoll
	s.FactsDistilled = produced
	s.VerifiedFacts = verified
	s.ProjectCount = projCount
	s.LastTimestamp = time.Now().Format("15:04:05")
	if produced > 0 {
		s.addEvent(fmt.Sprintf("distilled %d facts (%d verified)", produced, verified))
	}
	if gSum != nil && gSum.HasGoals {
		s.RoadmapPulse = gSum.RoadmapPulse()
		s.ActionPulse = gSum.ActionPulse(ui)
	} else {
		s.RoadmapPulse = ""
		s.ActionPulse = ""
	}
}

func (s *WatchSyncState) renderLines(ui render.UI, tick int, project string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	lastTime := s.LastTimestamp
	if lastTime == "" {
		lastTime = time.Now().Format("15:04:05")
	}
	spinner := watchSpinnerFrames[tick%len(watchSpinnerFrames)]

	countdown := ""
	if s.Phase == "idle" && !s.NextPoll.IsZero() {
		remaining := time.Until(s.NextPoll)
		if remaining < 0 {
			remaining = 0
		}
		countdown = ui.Dim(fmt.Sprintf(" · next in %s", formatCountdownDuration(remaining)))
	}

	var statusCore string
	switch s.Phase {
	case "starting":
		statusCore = ui.Dim("•") + " Starting live sync…"
	case "scanning":
		totalStr := ""
		if s.ToolTotal > 0 {
			totalStr = fmt.Sprintf(" (%d/%d)", s.ToolIndex, s.ToolTotal)
		}
		statusCore = fmt.Sprintf("%s Scanning %s%s…", ui.Cyan("⚙"), s.CurrentTool, totalStr)
	case "committing":
		statusCore = fmt.Sprintf("%s Committing %s…", ui.Cyan("⚙"), s.CurrentTool)
	case "distilling":
		statusCore = fmt.Sprintf("%s Distilling facts across projects…", ui.Cyan("⚙"))
	case "stopping":
		statusCore = ui.Dim("•") + " Stopping live sync…"
	default: // idle
		if len(s.SourceErrors) > 0 {
			var errTools []string
			for t := range s.SourceErrors {
				errTools = append(errTools, t)
			}
			sort.Strings(errTools)
			statusCore = ui.Paint(render.Yellow, fmt.Sprintf("▲ Partial sync (%d source error(s): %s)", len(s.SourceErrors), strings.Join(errTools, ", ")))
		} else {
			if project != "" {
				statusCore = fmt.Sprintf("%s In sync for %s (0 pending · %d verified facts)", ui.Green("✔"), ui.Magenta(ui.Bold(project)), s.VerifiedFacts)
			} else {
				statusCore = fmt.Sprintf("%s In sync globally (0 pending across %d %s · %d verified facts)", ui.Green("✔"), s.ProjectCount, plural(int64(s.ProjectCount), "project"), s.VerifiedFacts)
			}
		}
		if s.RoadmapPulse != "" {
			statusCore += ui.Dim(" | ") + s.RoadmapPulse
		}
	}

	var extraLines []string
	if len(s.SourceErrors) > 0 {
		var errTools []string
		for t := range s.SourceErrors {
			errTools = append(errTools, t)
		}
		sort.Strings(errTools)
		for _, t := range errTools {
			extraLines = append(extraLines, ui.Paint(render.Yellow, fmt.Sprintf("           ↳ %s: %s", t, s.SourceErrors[t])))
		}
	} else if s.ActionPulse != "" {
		extraLines = append(extraLines, s.ActionPulse)
	}

	if s.Phase == "idle" && len(s.RecentEvents) > 0 {
		latest := s.RecentEvents[len(s.RecentEvents)-1]
		durStr := ""
		if s.LastDuration > 0 {
			durStr = fmt.Sprintf(" in %s", s.LastDuration.Round(time.Millisecond))
		}
		extraLines = append(extraLines, ui.Dim(fmt.Sprintf("           ↳ activity: %s%s", latest, durStr)))
	}

	return buildWatchLines(lastTime, spinner, statusCore, countdown, extraLines...)
}

// runWatchInPlaceLoop rewrites a fixed-height live status block on each poll and
// each second (spinner + countdown) without appending scrollback lines. Real
// activity (new ingests, roadmap changes) updates the same block in place.
func runWatchInPlaceLoop(ui render.UI, st *store.Store, project, projPath string, interval time.Duration, since time.Time, sigChan chan os.Signal, quitChan chan struct{}) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	out := os.Stdout
	r := &watchRenderer{out: out, width: termWidth}
	wsState := newWatchSyncState(interval)

	fetchGoals := func() *goals.GoalTelemetrySummary {
		if project != "" {
			dir := projPath
			if dir == "" {
				dir, _ = os.Getwd()
			}
			s, _ := goals.FormatProjectGoalsTelemetry(ui, dir)
			return s
		}
		s, _ := goals.FormatGlobalGoalsTelemetry(ui, st)
		return s
	}

	tick := 0
	drawNow := func() {
		r.draw(wsState.renderLines(ui, tick, project))
	}

	redrawChan := make(chan struct{}, 1)
	triggerRedraw := func() {
		select {
		case redrawChan <- struct{}{}:
		default:
		}
	}

	var syncMu sync.Mutex
	doSync := func() {
		if !syncMu.TryLock() {
			return // at most one sync pass at once
		}
		defer syncMu.Unlock()

		passStart := time.Now()
		wsState.setScanning("sources", 0, 0)
		triggerRedraw()

		results, err := ingest.Run(st, ingest.Options{
			Ctx:        ctx,
			Tools:      nil,
			ProjectSel: project,
			Since:      since,
			Out:        io.Discard,
			Verbose:    false,
			OnProgress: func(p ingest.ToolProgress) {
				switch p.Phase {
				case "scanning":
					wsState.setScanning(p.Tool, p.Index, p.Total)
					triggerRedraw()
				case "committing":
					wsState.setCommitting(p.Tool, p.Index, p.Total)
					triggerRedraw()
				case "committed":
					wsState.setToolCommitted(p.Tool, p.NewRecords, p.Merged, p.Overlaps, p.Error)
					triggerRedraw()
				}
			},
		})
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			wsState.mu.Lock()
			wsState.LastSyncError = err
			wsState.Phase = "idle"
			wsState.mu.Unlock()
			fmt.Fprintf(os.Stderr, "[%s] %s %v\n", ui.Dim(time.Now().Format("15:04:05")), ui.Red("sync error:"), err)
			return
		}

		var newRec, merged int64
		for _, rr := range results {
			if rr.Error == nil {
				newRec += int64(rr.NewRecords)
				merged += int64(rr.Merged)
			}
		}

		produced, verifiedN, projCount := 0, 0, 0
		if newRec > 0 || merged > 0 {
			wsState.setDistilling()
			triggerRedraw()
			var err error
			produced, verifiedN, projCount, _, err = distill(st, project)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[%s] %s %v\n", ui.Dim(time.Now().Format("15:04:05")), ui.Red("distill error:"), err)
				return
			}
		} else {
			_, _, verifiedN, projCount, _ = st.Counts()
		}

		gSum := fetchGoals()
		dur := time.Since(passStart)
		wsState.setIdle(dur, time.Now().Add(interval), produced, verifiedN, projCount, gSum, ui)
		triggerRedraw()
	}

	drawNow()
	go doSync()

	syncTicker := time.NewTicker(interval)
	defer syncTicker.Stop()
	uiTicker := time.NewTicker(time.Second)
	defer uiTicker.Stop()

	for {
		select {
		case <-sigChan:
			cancel()
			fmt.Fprintln(out, ui.Green("✔")+" Continuous live sync stopped cleanly.")
			return
		case <-quitChan:
			cancel()
			fmt.Fprintln(out, ui.Green("✔")+" Continuous live sync stopped cleanly.")
			return
		case <-syncTicker.C:
			go doSync()
		case <-uiTicker.C:
			tick++
			drawNow()
		case <-redrawChan:
			drawNow()
		}
	}
}
