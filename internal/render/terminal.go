package render

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Markdown formats markdown text according to the UI's terminal capability.
func (u UI) Markdown(markdown string) string {
	return RenderMarkdownString(markdown, u)
}

// RenderMarkdown renders markdown text to w according to the UI's terminal capability.
func (u UI) RenderMarkdown(w io.Writer, markdown string) error {
	return RenderMarkdown(w, markdown, u)
}

// Box renders an aligned, framed callout box around content.
func (u UI) Box(title string, content string) string {
	lines := strings.Split(content, "\n")
	maxW := visibleWidth(title) + 4
	for _, l := range lines {
		if w := visibleWidth(l); w > maxW {
			maxW = w
		}
	}
	if maxW < 40 {
		maxW = 40
	}

	var sb strings.Builder

	// Top frame
	titlePart := ""
	if title != "" {
		styledTitle := title
		if u.enabled {
			styledTitle = u.Cyan(u.Bold(title))
		}
		titlePart = " " + styledTitle + " "
	}
	topFill := maxW - visibleWidth(title) - 2
	if topFill < 2 {
		topFill = 2
	}
	sb.WriteString("┌─" + titlePart + strings.Repeat("─", topFill) + "┐\n")

	// Body lines
	for _, l := range lines {
		pad := maxW - visibleWidth(l)
		if pad < 0 {
			pad = 0
		}
		sb.WriteString("│ " + l + strings.Repeat(" ", pad) + " │\n")
	}

	// Bottom frame
	sb.WriteString("└" + strings.Repeat("─", maxW+2) + "┘")
	return sb.String()
}

// Spinner provides a lightweight, zero-dependency terminal spinner for long operations.
// It activates animated frames only on interactive TTYs with color enabled, ensuring
// zero log noise or escape sequence spam in piped, CI, or --no-color environments.
type Spinner struct {
	ui     UI
	out    io.Writer
	msg    string
	active bool
	mu     sync.Mutex
	done   chan struct{}
	wg     sync.WaitGroup
}

// NewSpinner constructs a new terminal spinner bound to the UI.
func (u UI) NewSpinner(msg string) *Spinner {
	var out io.Writer = os.Stdout
	if u.out != nil {
		out = u.out
	}
	return &Spinner{
		ui:  u,
		out: out,
		msg: msg,
	}
}

// Start begins the spinner animation if running on an interactive TTY.
func (s *Spinner) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.active {
		return
	}
	s.active = true

	// Non-interactive or disabled: do not spawn animation ticker
	if !s.ui.IsTerminal() || !s.ui.enabled {
		return
	}

	s.done = make(chan struct{})

	s.wg.Go(func() {
		ticker := time.NewTicker(80 * time.Millisecond)
		defer ticker.Stop()

		idx := 0
		for {
			select {
			case <-s.done:
				return
			case <-ticker.C:
				s.mu.Lock()
				msg := s.msg
				s.mu.Unlock()

				frame := spinnerFrames[idx%len(spinnerFrames)]
				idx++
				// \r moves to line start, \x1b[K clears line to the right
				_, _ = fmt.Fprintf(s.out, "\r\x1b[K%s %s", s.ui.Cyan(frame), msg)
			}
		}
	})
}

// Update changes the active spinner message atomically.
func (s *Spinner) Update(msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msg = msg
}

// Stop terminates the spinner and clears the active terminal line cleanly.
func (s *Spinner) Stop() {
	s.mu.Lock()
	if !s.active {
		s.mu.Unlock()
		return
	}
	s.active = false
	done := s.done
	s.mu.Unlock()

	if done != nil {
		close(done)
		s.wg.Wait()
		if s.ui.IsTerminal() && s.ui.enabled {
			_, _ = fmt.Fprint(s.out, "\r\x1b[K")
		}
	}
}

// Success stops the spinner and writes a final success line.
func (s *Spinner) Success(msg string) {
	s.Stop()
	if msg == "" {
		s.mu.Lock()
		msg = s.msg
		s.mu.Unlock()
	}
	prefix := "✔"
	if s.ui.enabled {
		prefix = s.ui.Green("✔")
	}
	_, _ = fmt.Fprintf(s.out, "%s %s\n", prefix, msg)
}

// Failure stops the spinner and writes a final failure line.
func (s *Spinner) Failure(msg string) {
	s.Stop()
	if msg == "" {
		s.mu.Lock()
		msg = s.msg
		s.mu.Unlock()
	}
	prefix := "✖"
	if s.ui.enabled {
		prefix = s.ui.Red("✖")
	}
	_, _ = fmt.Fprintf(s.out, "%s %s\n", prefix, msg)
}
