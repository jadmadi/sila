package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/render"
)

func TestTruncateANSIPlain(t *testing.T) {
	got := truncateANSI("hello world", 5)
	if visibleWidthOf(got) > 5 {
		t.Fatalf("expected truncated width <= 5, got %q", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("expected ellipsis suffix, got %q", got)
	}
}

func TestTruncateANSINoOpWhenShort(t *testing.T) {
	in := "short"
	if got := truncateANSI(in, 80); got != in {
		t.Fatalf("expected no truncation, got %q", got)
	}
}

func TestTruncateANSIColor(t *testing.T) {
	in := "\x1b[1mhello world foo bar baz\x1b[0m"
	got := truncateANSI(in, 8)
	if visibleWidthOf(got) > 8 {
		t.Fatalf("expected truncated width <= 8, got %q (width %d)", got, visibleWidthOf(got))
	}
	if !strings.HasSuffix(strings.TrimSuffix(got, "\x1b[0m"), "…") {
		t.Fatalf("expected ellipsis before reset, got %q", got)
	}
}

func TestCountdownSurvivesNarrowTruncation(t *testing.T) {
	line := buildWatchLines("12:00:00", "⠋",
		"In sync for sila (0 pending · 1133 verified facts) | [█████████░] 90% — 9 done · 1 active",
		" · next in 12s", "           ↳ next")[0]
	narrow := truncateANSI(line, 60)
	if !strings.Contains(stripWatchANSI(narrow), "next in 12s") {
		t.Fatalf("countdown must survive narrow truncation, got %q", narrow)
	}
}

func TestBuildWatchLines(t *testing.T) {
	one := buildWatchLines("12:00:00", "⠋", "core", " · next in 5s", "")
	if len(one) != 1 || !strings.Contains(one[0], "core") || !strings.Contains(one[0], "next in 5s") {
		t.Fatalf("expected single status line, got %q", one)
	}
	if strings.Index(one[0], "next in 5s") > strings.Index(one[0], "core") {
		t.Fatalf("countdown must precede the status core so truncation preserves liveness, got %q", one)
	}
	two := buildWatchLines("12:00:00", "⠋", "core", " · next in 5s", "           ↳ next up")
	if len(two) != 2 || two[1] != "           ↳ next up" {
		t.Fatalf("expected two-line block, got %q", two)
	}
	blank := buildWatchLines("12:00:00", "⠋", "core", "", "   ")
	if len(blank) != 1 {
		t.Fatalf("expected blank action to collapse to one line, got %q", blank)
	}
}

func TestWatchStateHash(t *testing.T) {
	a := watchStateHash("sync", "action", 3, 1)
	b := watchStateHash("sync", "action", 3, 1)
	c := watchStateHash("sync changed", "action", 3, 1)
	if a != b {
		t.Fatal("identical inputs must hash identically")
	}
	if a == c {
		t.Fatal("changed status must change the hash")
	}
}

func TestWatchRendererRowTracking(t *testing.T) {
	var buf bytes.Buffer
	r := &watchRenderer{out: &buf, width: func() int { return 80 }}
	r.draw([]string{"line one", "line two"})
	if r.prevRows != 2 {
		t.Fatalf("expected 2 tracked rows, got %d", r.prevRows)
	}
	first := buf.String()
	if !strings.Contains(first, "line one") || !strings.Contains(first, "line two") {
		t.Fatalf("expected both lines in output, got %q", first)
	}
	buf.Reset()
	r.draw([]string{"line three"})
	out := buf.String()
	if r.prevRows != 1 {
		t.Fatalf("expected 1 tracked row after redraw, got %d", r.prevRows)
	}
	if !strings.Contains(out, "\033[2A") {
		t.Fatalf("expected cursor-up rewrite sequence, got %q", out)
	}
	if !strings.Contains(out, "line three") {
		t.Fatalf("expected new line in redraw output, got %q", out)
	}
}

func TestWatchRendererTruncatesToWidth(t *testing.T) {
	var buf bytes.Buffer
	r := &watchRenderer{out: &buf, width: func() int { return 25 }}
	r.draw([]string{"0123456789ABCDEFGHIJ0123456789ABCDEFGHIJ"})
	for ln := range strings.SplitSeq(strings.TrimSpace(stripWatchANSI(buf.String())), "\n") {
		if visibleWidthOf(ln) > 25 {
			t.Fatalf("expected line truncated to width 25, got %q", ln)
		}
	}
	if !strings.Contains(buf.String(), "…") {
		t.Fatalf("expected ellipsis truncation marker, got %q", buf.String())
	}
}

func TestFormatCountdownDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{5 * time.Second, "5s"},
		{59 * time.Second, "59s"},
		{60 * time.Second, "1m"},
		{90 * time.Second, "1m 30s"},
		{10 * time.Minute, "10m"},
		{10*time.Minute + 5*time.Second, "10m 5s"},
	}
	for _, tc := range cases {
		got := formatCountdownDuration(tc.d)
		if got != tc.want {
			t.Errorf("formatCountdownDuration(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestBuildWatchLines_MultiLines(t *testing.T) {
	lines := buildWatchLines("12:00:00", "⠋", "status", " · next in 5s", "line 1", "line 2", "")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (line1 + 2 extra, blank omitted), got %d: %v", len(lines), lines)
	}
	if lines[1] != "line 1" || lines[2] != "line 2" {
		t.Errorf("unexpected extra lines: %v", lines)
	}
}

func TestWatchSyncState_TransitionsAndProgress(t *testing.T) {
	state := newWatchSyncState(15 * time.Second)
	ui := render.New(os.Stdout, true)

	// Initial phase
	lines := state.renderLines(ui, 0, "")
	if !strings.Contains(lines[0], "Starting live sync") {
		t.Errorf("expected initial starting message, got %q", lines[0])
	}

	// Scanning phase
	state.setScanning("opencode", 1, 10)
	lines = state.renderLines(ui, 1, "")
	if !strings.Contains(lines[0], "Scanning opencode (1/10)") {
		t.Errorf("expected scanning message with tool and index, got %q", lines[0])
	}

	// Committing phase
	state.setCommitting("opencode", 1, 10)
	lines = state.renderLines(ui, 2, "")
	if !strings.Contains(lines[0], "Committing opencode") {
		t.Errorf("expected committing message, got %q", lines[0])
	}

	// Tool committed with records
	state.setToolCommitted("opencode", 5, 2, 1, nil)
	if state.RecordsAdded != 5 || state.RecordsMerged != 2 {
		t.Errorf("expected 5 added, 2 merged, got %d, %d", state.RecordsAdded, state.RecordsMerged)
	}

	// Distilling phase
	state.setDistilling()
	lines = state.renderLines(ui, 3, "")
	if !strings.Contains(lines[0], "Distilling facts") {
		t.Errorf("expected distilling message, got %q", lines[0])
	}

	// Idle phase
	state.setIdle(120*time.Millisecond, time.Now().Add(15*time.Second), 5, 10, 2, nil, ui)
	lines = state.renderLines(ui, 4, "")
	if !strings.Contains(lines[0], "In sync globally") {
		t.Errorf("expected in sync message, got %q", lines[0])
	}
	if !strings.Contains(lines[0], "10 verified facts") {
		t.Errorf("expected verified facts count, got %q", lines[0])
	}
}

func TestWatchSyncState_SourceErrorRetentionAndRecovery(t *testing.T) {
	state := newWatchSyncState(15 * time.Second)
	ui := render.New(os.Stdout, true)

	// Simulate source failure
	state.setToolCommitted("devin", 0, 0, 0, fmt.Errorf("session store locked"))
	state.setIdle(50*time.Millisecond, time.Now().Add(15*time.Second), 0, 8, 1, nil, ui)

	lines := state.renderLines(ui, 0, "")
	// Line 1 must report partial sync and identify failed source
	if !strings.Contains(lines[0], "Partial sync") || !strings.Contains(lines[0], "devin") {
		t.Fatalf("expected partial sync warning with failed source, got %q", lines[0])
	}
	// Line 2 must detail the error
	if len(lines) < 2 || !strings.Contains(lines[1], "session store locked") {
		t.Fatalf("expected error detail on line 2, got: %v", lines)
	}

	// Simulate recovery on subsequent poll
	state.setToolCommitted("devin", 3, 0, 0, nil)
	state.setIdle(30*time.Millisecond, time.Now().Add(15*time.Second), 0, 8, 1, nil, ui)

	lines = state.renderLines(ui, 1, "")
	if strings.Contains(lines[0], "Partial sync") {
		t.Fatalf("expected recovered in-sync status, still got partial sync: %q", lines[0])
	}
	if !strings.Contains(lines[0], "In sync globally") {
		t.Fatalf("expected in sync status, got %q", lines[0])
	}
}

func TestWatchSyncState_SlowSourceEmitsProgressBeforeCompletion(t *testing.T) {
	state := newWatchSyncState(15 * time.Second)
	ui := render.New(os.Stdout, true)

	// Step 1: Slow source starts scanning
	state.setScanning("slow-source", 1, 5)
	lines := state.renderLines(ui, 1, "")
	if !strings.Contains(lines[0], "Scanning slow-source (1/5)") {
		t.Fatalf("expected visible scanning progress before completion, got %q", lines[0])
	}

	// Step 2: Slow source starts committing
	state.setCommitting("slow-source", 1, 5)
	lines = state.renderLines(ui, 2, "")
	if !strings.Contains(lines[0], "Committing slow-source") {
		t.Fatalf("expected visible committing progress before completion, got %q", lines[0])
	}

	// Step 3: Slow source finishes
	state.setToolCommitted("slow-source", 12, 4, 0, nil)
	state.setIdle(450*time.Millisecond, time.Now().Add(15*time.Second), 0, 8, 1, nil, ui)
	lines = state.renderLines(ui, 3, "")
	if !strings.Contains(lines[0], "In sync globally") {
		t.Fatalf("expected in sync status after completion, got %q", lines[0])
	}
	foundActivity := false
	for _, l := range lines {
		if strings.Contains(l, "slow-source: +12 new, 4 merged") {
			foundActivity = true
			break
		}
	}
	if !foundActivity {
		t.Fatalf("expected retained activity line for completed slow source, got %v", lines)
	}
}

func TestEvaluationConcurrentWatchDraw(t *testing.T) {
	r := &watchRenderer{out: io.Discard, width: func() int { return 80 }}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 100 {
				r.draw([]string{"progress"})
			}
		})
	}
	wg.Wait()
}
