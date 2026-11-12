package goals

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/render"
)

func TestPrintGlobalStatsTable(t *testing.T) {
	now := time.Now()
	res := dashboardFixture(now)

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	ui := render.New(w, true)

	PrintGlobalStatsTable(ui, res)
	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	io.Copy(&buf, r)
	out := buf.String()

	// Verify headers and structure
	for _, expected := range []string{"Global Multi-Project Statistics", "project", "ready", "wip", "done", "blocked", "total", "heavy", "light", "done-repo", "TOTAL (3)"} {
		if !strings.Contains(out, expected) {
			t.Errorf("PrintGlobalStatsTable output missing %q:\n%s", expected, out)
		}
	}

	// Verify JSON payload serialization
	payload := res.ToStatsPayload()
	if payload.TotalProjects != 3 {
		t.Errorf("expected 3 projects in payload, got %d", payload.TotalProjects)
	}
	if len(payload.Projects) != 3 {
		t.Errorf("expected 3 project entries, got %d", len(payload.Projects))
	}
	if payload.Projects[0].Project != "heavy" {
		t.Errorf("expected heavy to be ranked first, got %s", payload.Projects[0].Project)
	}

	// Verify TOON output
	toon := res.ToStatsTOON()
	if !strings.Contains(toon, "goals_stats") || !strings.Contains(toon, "heavy") {
		t.Errorf("ToStatsTOON output missing expected content:\n%s", toon)
	}
}

func TestPrintProjectStatsTable(t *testing.T) {
	audit := &AuditResult{
		Project:          "demo-proj",
		Total:            10,
		ReadyCount:       4,
		ClaimedCount:     2,
		ImplementedCount: 3,
		BlockedCount:     1,
		OldestReadyAge:   "2h",
	}

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	ui := render.New(w, true)

	PrintProjectStatsTable(ui, audit)
	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	io.Copy(&buf, r)
	out := buf.String()

	for _, expected := range []string{
		"demo-proj · 30.0% done (3/10)",
		"⛔ Blocked: 1",
		"▶ Ready: 4 (oldest: 2h)",
		"◐ WIP: 2",
		"Verdict: Active: proceed with in-progress goals",
		"↳ Hint: run sila goals --blocked to inspect blocker dependencies",
	} {
		if !strings.Contains(out, expected) {
			t.Errorf("PrintProjectStatsTable output missing %q:\n%s", expected, out)
		}
	}

	payload := audit.ToStatsPayload()
	if payload.Project != "demo-proj" || payload.Ready != 4 || payload.WIP != 2 || payload.Done != 3 || payload.CompletionPct != 30.0 || payload.OldestReadyAge != "2h" {
		t.Errorf("unexpected ProjectGoalsStatsPayload: %+v", payload)
	}
}

func TestPrintProjectStatsTableIsCompact(t *testing.T) {
	audit := &AuditResult{Project: "demo-proj", Total: 10, ReadyCount: 4}
	var buf bytes.Buffer
	FprintProjectStatsTable(&buf, render.New(os.Stdout, true), audit)
	out := buf.String()
	if n := strings.Count(out, "\n"); n > 3 {
		t.Errorf("project stats view should be at most 3 lines, got %d:\n%s", n, out)
	}
	if strings.ContainsAny(out, "┌│└") {
		t.Errorf("project stats view must not repeat the numbers in a table:\n%s", out)
	}
}

func TestRenderStatsLine(t *testing.T) {
	plain := render.New(os.Stdout, true)
	tests := []struct {
		name string
		in   StatsCounts
		want string
	}{
		{"normal", StatsCounts{Ready: 2, WIP: 0, Blocked: 1, Done: 232, Total: 236}, "Goals: Ready 2 · WIP 0 · Blocked 1 · Done 232"},
		{"active wip", StatsCounts{Ready: 2, WIP: 1, Blocked: 0, Done: 10, Total: 13}, "Goals: Ready 2 · WIP 1 · Blocked 0 · Done 10"},
		{"all zero but total", StatsCounts{Total: 1}, "Goals: Ready 0 · WIP 0 · Blocked 0 · Done 0"},
		{"no goals", StatsCounts{}, "Goals: none"},
		{"large", StatsCounts{Ready: 999, WIP: 999, Blocked: 999, Done: 999, Total: 3996}, "Goals: Ready 999 · WIP 999 · Blocked 999 · Done 999"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RenderStatsLine(plain, tt.in)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
			if strings.Contains(got, "\x1b") {
				t.Errorf("plain output must have no ANSI codes: %q", got)
			}
			if strings.Contains(got, "\n") {
				t.Errorf("line must have no newline: %q", got)
			}
			if n := len([]rune(got)); n > 51 {
				t.Errorf("line is %d characters, want at most 51", n)
			}
		})
	}
}

func TestRenderStatsLineColour(t *testing.T) {
	t.Setenv("COLORTERM", "truecolor")
	t.Setenv("TERM", "xterm-256color")
	// /dev/null is a character device, so render.New enables colour for it.
	dev, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Skipf("cannot open %s: %v", os.DevNull, err)
	}
	defer dev.Close()
	ui := render.New(dev, false)
	if !ui.ColorEnabled() {
		t.Skip("colour not enabled in this environment")
	}
	got := RenderStatsLine(ui, StatsCounts{Ready: 2, WIP: 0, Blocked: 1, Done: 5, Total: 8})
	for label, code := range map[string]string{"Ready 2": render.Yellow, "Blocked 1": render.Red, "Done 5": render.Green} {
		if !strings.Contains(got, code+label) {
			t.Errorf("%q should be painted with %q: %q", label, code, got)
		}
	}
	if !strings.Contains(got, render.Dim+"WIP 0") {
		t.Errorf("zero count should be dimmed: %q", got)
	}
}

func TestStatsCountsRollup(t *testing.T) {
	p := (&AuditResult{Total: 10, ReadyCount: 4, ClaimedCount: 2, ImplementedCount: 3, BlockedCount: 1}).StatsCounts()
	if p != (StatsCounts{Ready: 4, WIP: 2, Blocked: 1, Done: 3, Total: 10}) {
		t.Errorf("unexpected project counts: %+v", p)
	}
	g := (&GlobalAuditResult{TotalGoals: 9, ReadyCount: 1, ClaimedCount: 2, ImplementedCount: 5, BlockedCount: 1}).StatsCounts()
	if g != (StatsCounts{Ready: 1, WIP: 2, Blocked: 1, Done: 5, Total: 9}) {
		t.Errorf("unexpected global counts: %+v", g)
	}
}
