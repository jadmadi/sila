package main

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/jadmadi/sila/internal/ingest"
	"github.com/jadmadi/sila/internal/render"
)

// syncProgress coordinates live progress display and verbose telemetry during sync.
type syncProgress struct {
	ui         render.UI
	isTTY      bool
	verbose    bool
	jsonOut    bool
	isGlobal   bool
	project    string
	totalTools int

	mu        sync.Mutex
	status    string
	stopChan  chan struct{}
	doneChan  chan struct{}
	frameIdx  int
	cleared   bool
	vacLogged bool
}

func newSyncProgress(ui render.UI, isTTY, verbose, jsonOut, isGlobal bool, project string) *syncProgress {
	p := &syncProgress{
		ui:       ui,
		isTTY:    isTTY && !verbose && !jsonOut,
		verbose:  verbose && !jsonOut,
		jsonOut:  jsonOut,
		isGlobal: isGlobal,
		project:  project,
	}

	if p.verbose {
		title := ui.Cyan(ui.Bold("sila · sync"))
		if project != "" {
			title += fmt.Sprintf(" (%s %s)", ui.Dim("scoped to"), ui.Bold(project))
		} else {
			title += fmt.Sprintf(" (%s)", ui.Dim("global workspace"))
		}
		fmt.Fprintln(os.Stdout, title)
		fmt.Fprintln(os.Stdout, ui.Bold("  Phase 1/3: Scanning & Ingesting AI sources..."))
	} else if p.isTTY {
		p.stopChan = make(chan struct{})
		p.doneChan = make(chan struct{})
		p.status = "Preparing sync..."
		go p.spinnerLoop()
	}

	return p
}

func (p *syncProgress) spinnerLoop() {
	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()
	defer close(p.doneChan)

	for {
		select {
		case <-p.stopChan:
			p.clearLine()
			return
		case <-ticker.C:
			p.mu.Lock()
			frame := watchSpinnerFrames[p.frameIdx%len(watchSpinnerFrames)]
			p.frameIdx++
			msg := p.status
			p.mu.Unlock()

			// In-place rewrite: carriage return + erase line
			line := fmt.Sprintf("  %s %s", p.ui.Cyan(frame), msg)
			fmt.Fprintf(os.Stdout, "\r\x1b[2K%s", line)
		}
	}
}

func (p *syncProgress) setStatus(status string) {
	p.mu.Lock()
	p.status = status
	p.mu.Unlock()
}

func (p *syncProgress) clearLine() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.cleared && p.isTTY {
		fmt.Fprint(os.Stdout, "\r\x1b[2K")
		p.cleared = true
	}
}

// OnToolProgress handles tool ingestion progress events.
func (p *syncProgress) OnToolProgress(tp ingest.ToolProgress) {
	if p.jsonOut {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	p.totalTools = tp.Total

	if p.verbose {
		if tp.Phase == "committed" {
			status := fmt.Sprintf("%d new %s", tp.NewRecords, plural(int64(tp.NewRecords), "record"))
			if tp.Merged > 0 {
				status += fmt.Sprintf(", %d merged", tp.Merged)
			}
			if tp.Overlaps > 0 {
				status += fmt.Sprintf(", %d %s", tp.Overlaps, plural(int64(tp.Overlaps), "overlap"))
			}
			if tp.Error != nil {
				status = p.ui.Red(fmt.Sprintf("error: %v", tp.Error))
			} else if tp.NewRecords > 0 {
				status = p.ui.Green(status)
			} else {
				status = p.ui.Dim(status)
			}
			fmt.Fprintf(os.Stdout, "    %s %-12s %s (scanned %s)\n",
				p.ui.Dim("•"), tp.Tool+":", status, render.FormatNumber(int64(tp.Messages)))
		}
		return
	}

	if p.isTTY {
		switch tp.Phase {
		case "scanning":
			p.status = fmt.Sprintf("Scanning sources [%d/%d] (%s)...", tp.Index, tp.Total, tp.Tool)
		case "committing":
			p.status = fmt.Sprintf("Ingesting records [%d/%d] (%s)...", tp.Index, tp.Total, tp.Tool)
		case "committed":
			if tp.NewRecords > 0 {
				p.status = fmt.Sprintf("Ingested %s (+%d new records)...", tp.Tool, tp.NewRecords)
			} else {
				p.status = fmt.Sprintf("Checked %s (up to date)...", tp.Tool)
			}
		}
	}
}

// StartDistill notifies the progress coordinator that distillation has begun.
func (p *syncProgress) StartDistill(totalProjects int) {
	if p.jsonOut {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.verbose {
		fmt.Fprintln(os.Stdout, p.ui.Bold(fmt.Sprintf("  Phase 2/3: Distilling facts across %d %s...",
			totalProjects, plural(int64(totalProjects), "project"))))
		return
	}
	if p.isTTY {
		p.status = fmt.Sprintf("Distilling facts across %d %s...", totalProjects, plural(int64(totalProjects), "project"))
	}
}

// OnDistillProgress handles per-project distillation progress events.
func (p *syncProgress) OnDistillProgress(dp DistillProgress) {
	if p.jsonOut {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.verbose {
		if dp.Phase == "done" && dp.Facts > 0 {
			fmt.Fprintf(os.Stdout, "    %s [%2d/%d] %s: %d %s (%d verified)\n",
				p.ui.Dim("•"), dp.Index, dp.Total, p.ui.Bold(dp.ProjectID),
				dp.Facts, plural(int64(dp.Facts), "fact"), dp.Verified)
		}
		return
	}

	if p.isTTY {
		name := dp.ProjectID
		if len(name) > 25 {
			name = name[:22] + "..."
		}
		p.status = fmt.Sprintf("Distilling facts [%d/%d] (%s)...", dp.Index, dp.Total, name)
	}
}

// StartVacuum notifies the progress coordinator that auto-vacuum inspection is underway.
func (p *syncProgress) StartVacuum() {
	if p.jsonOut {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.verbose && !p.vacLogged {
		p.vacLogged = true
		fmt.Fprintln(os.Stdout, p.ui.Bold("  Phase 3/3: Database maintenance & auto-vacuum..."))
		return
	}
	if p.isTTY {
		p.status = "Checking database fragmentation & auto-vacuum..."
	}
}

// Finish terminates any active in-place spinner, clears the line, and prints verbose summary header if needed.
func (p *syncProgress) Finish() {
	if p.isTTY && p.stopChan != nil {
		close(p.stopChan)
		<-p.doneChan
		p.clearLine()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.verbose {
		fmt.Fprintln(os.Stdout, p.ui.Bold("  Summary:"))
	}
}
