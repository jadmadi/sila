package goals

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/render"
)

// tickDriver feeds ticks to RunStatsLive and signals after every rendered frame.
type tickDriver struct {
	ticks  chan time.Time
	frames chan struct{}
}

func newTickDriver() *tickDriver {
	return &tickDriver{ticks: make(chan time.Time), frames: make(chan struct{}, 16)}
}

// run starts RunStatsLive and returns a function that fires n ticks, waits for
// each resulting frame, cancels, and returns the output once the loop exits.
func (d *tickDriver) run(t *testing.T, opts StatsLiveOptions, n int) (out, errOut string) {
	t.Helper()
	var buf, errBuf bytes.Buffer
	render := opts.Render
	opts.Render = func(w io.Writer) error {
		defer func() { d.frames <- struct{}{} }()
		return render(w)
	}
	opts.Ticks = d.ticks
	opts.Errw = &errBuf

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunStatsLive(ctx, &buf, opts) }()

	<-d.frames // initial frame
	for i := 0; i < n; i++ {
		d.ticks <- time.Now()
		<-d.frames
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunStatsLive returned %v on cancel, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunStatsLive did not stop after cancel")
	}
	return buf.String(), errBuf.String()
}

func TestRunStatsLiveTwoTicksTwoRendersPlusInitial(t *testing.T) {
	calls := 0
	out, _ := newTickDriver().run(t, StatsLiveOptions{
		Render: func(w io.Writer) error {
			calls++
			fmt.Fprintf(w, "Goals: n=%d", calls)
			return nil
		},
	}, 2)
	if calls != 3 {
		t.Fatalf("render calls = %d, want 3 (initial plus 2 ticks)", calls)
	}
	want := "Goals: n=1\nGoals: n=2\nGoals: n=3\n"
	if out != want {
		t.Errorf("non-terminal output = %q, want %q", out, want)
	}
}

func TestRunStatsLiveNonTTYHasNoANSI(t *testing.T) {
	out, _ := newTickDriver().run(t, StatsLiveOptions{
		Render: func(w io.Writer) error { _, err := io.WriteString(w, "Goals: x"); return err },
	}, 1)
	if strings.Contains(out, "\x1b") || strings.Contains(out, "\r") {
		t.Errorf("non-terminal output must have no ANSI or carriage returns: %q", out)
	}
}

func TestRunStatsLiveTTYRewritesInPlace(t *testing.T) {
	out, _ := newTickDriver().run(t, StatsLiveOptions{
		TTY:    true,
		Render: func(w io.Writer) error { _, err := io.WriteString(w, "Goals: x"); return err },
	}, 1)
	ui := render.UI{}
	want := ui.RewriteLine("Goals: x") + ui.RewriteLine("Goals: x") + "\n"
	if out != want {
		t.Errorf("tty output = %q, want %q", out, want)
	}
}

func TestRunStatsLiveFullTTYClearsScreen(t *testing.T) {
	out, _ := newTickDriver().run(t, StatsLiveOptions{
		TTY:    true,
		Full:   true,
		Render: func(w io.Writer) error { _, err := io.WriteString(w, "a\nb\n"); return err },
	}, 1)
	if got := strings.Count(out, render.UI{}.ClearScreen("")); got != 2 {
		t.Errorf("screen cleared %d times, want 2: %q", got, out)
	}
	if strings.HasSuffix(out, "\n\n") {
		t.Errorf("cancel must not add a blank line in full mode: %q", out)
	}
}

func TestRunStatsLiveFailureKeepsStaleLine(t *testing.T) {
	calls := 0
	out, errOut := newTickDriver().run(t, StatsLiveOptions{
		Render: func(w io.Writer) error {
			calls++
			if calls >= 2 {
				return errors.New("store busy")
			}
			_, err := io.WriteString(w, "Goals: ok")
			return err
		},
	}, 2)
	want := "Goals: ok\nGoals: ok ?\nGoals: ok ?\n"
	if out != want {
		t.Errorf("output = %q, want %q (marker must not accumulate)", out, want)
	}
	if !strings.Contains(errOut, "store busy") {
		t.Errorf("error should be reported on Errw, got %q", errOut)
	}
}

func TestRunStatsLiveFailureBeforeFirstFrame(t *testing.T) {
	out, errOut := newTickDriver().run(t, StatsLiveOptions{
		Render: func(w io.Writer) error { return errors.New("no store") },
	}, 0)
	if out != "Goals: ?\n" {
		t.Errorf("output = %q, want %q", out, "Goals: ?\n")
	}
	if !strings.Contains(errOut, "no store") {
		t.Errorf("error should be reported on Errw, got %q", errOut)
	}
}

func TestRunStatsLiveStopsOnCancelWithRealTicker(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var buf bytes.Buffer
	err := RunStatsLive(ctx, &buf, StatsLiveOptions{
		Interval: time.Hour,
		Render:   func(w io.Writer) error { _, err := io.WriteString(w, "Goals: x"); return err },
	})
	if err != nil {
		t.Fatalf("got %v, want nil", err)
	}
	if buf.String() != "Goals: x\n" {
		t.Errorf("a cancelled loop should still draw one frame, got %q", buf.String())
	}
}

func TestClampStatsLiveInterval(t *testing.T) {
	tests := []struct {
		in         time.Duration
		want       time.Duration
		wantRaised bool
	}{
		{0, MinStatsLiveInterval, true},
		{100 * time.Millisecond, MinStatsLiveInterval, true},
		{time.Second, time.Second, false},
		{DefaultStatsLiveInterval, DefaultStatsLiveInterval, false},
	}
	for _, tt := range tests {
		got, raised := ClampStatsLiveInterval(tt.in)
		if got != tt.want || raised != tt.wantRaised {
			t.Errorf("Clamp(%v) = %v, %v; want %v, %v", tt.in, got, raised, tt.want, tt.wantRaised)
		}
	}
	if DefaultStatsLiveInterval != 20*time.Second {
		t.Errorf("default interval = %v, want 20s", DefaultStatsLiveInterval)
	}
}
