package goals

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/jadmadi/sila/internal/render"
)

// Live stats defaults. The minimum keeps a typo from spinning the store.
const (
	DefaultStatsLiveInterval = 20 * time.Second
	MinStatsLiveInterval     = time.Second
)

// ClampStatsLiveInterval raises d to MinStatsLiveInterval. The bool reports
// whether d was raised, so the caller can tell the user.
func ClampStatsLiveInterval(d time.Duration) (time.Duration, bool) {
	if d < MinStatsLiveInterval {
		return MinStatsLiveInterval, true
	}
	return d, false
}

// StatsLiveOptions configures RunStatsLive.
type StatsLiveOptions struct {
	// Interval between refreshes. Ignored when Ticks is set.
	Interval time.Duration
	// Full means Render writes a multi-line frame. Otherwise it writes one
	// line with no trailing newline.
	Full bool
	// TTY enables in-place redraw. Without it each frame is appended and no
	// ANSI cursor codes are written.
	TTY bool
	// Render writes one frame to w. It must not write to anything else.
	Render func(w io.Writer) error
	// Errw receives per-refresh errors. Nil discards them.
	Errw io.Writer
	// Ticks replaces the interval ticker. Tests use it to drive refreshes.
	Ticks <-chan time.Time
}

// RunStatsLive renders one frame immediately, then one per tick, until ctx is
// cancelled. A failed refresh keeps the last good frame, marks it stale with a
// trailing "?", reports the error on Errw, and tries again on the next tick.
// It returns nil on cancellation.
func RunStatsLive(ctx context.Context, w io.Writer, opts StatsLiveOptions) error {
	ticks := opts.Ticks
	if ticks == nil {
		interval, _ := ClampStatsLiveInterval(opts.Interval)
		t := time.NewTicker(interval)
		defer t.Stop()
		ticks = t.C
	}

	var good string
	refresh := func() {
		var buf bytes.Buffer
		if err := opts.Render(&buf); err != nil {
			if opts.Errw != nil {
				fmt.Fprintf(opts.Errw, "sila goals stats live: %v\n", err)
			}
			stale := good
			switch {
			case good == "" && opts.Full:
				stale = "Goals: ?\n"
			case good == "":
				stale = "Goals: ?"
			case !opts.Full:
				stale = good + " ?"
			}
			emitStatsFrame(w, stale, opts)
			return
		}
		good = buf.String()
		emitStatsFrame(w, good, opts)
	}

	refresh()
	for {
		select {
		case <-ctx.Done():
			if opts.TTY && !opts.Full {
				fmt.Fprintln(w) // leave the prompt on its own line
			}
			return nil
		case <-ticks:
			refresh()
		}
	}
}

func emitStatsFrame(w io.Writer, frame string, opts StatsLiveOptions) {
	switch {
	case opts.TTY && opts.Full:
		fmt.Fprint(w, render.UI{}.ClearScreen(frame))
	case opts.TTY:
		fmt.Fprint(w, render.UI{}.RewriteLine(frame))
	case opts.Full:
		fmt.Fprint(w, frame)
	default:
		fmt.Fprintln(w, frame)
	}
}
