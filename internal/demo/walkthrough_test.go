package demo

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestParseWalkthroughSteps(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    []string
		wantErr bool
	}{
		{name: "empty runs all", raw: "", want: CanonicalStageOrder},
		{name: "all runs all", raw: "all", want: CanonicalStageOrder},
		{name: "canonical order", raw: "recall,prime,gate", want: []string{"prime", "recall", "gate"}},
		{name: "dedupe", raw: "sync,sync,prime", want: []string{"sync", "prime"}},
		{name: "trim and lower", raw: " Gate , PRIME ", want: []string{"prime", "gate"}},
		{name: "unknown fails", raw: "bogus", wantErr: true},
		{name: "partial unknown fails", raw: "prime,bogus", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseWalkthroughSteps(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %v", got)
				}
				if !strings.Contains(err.Error(), "valid:") {
					t.Errorf("error should list valid stages: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("steps = %v, want %v", got, tc.want)
			}
		})
	}
}

func newSandbox(t *testing.T) (*Sandbox, *store.Store) {
	t.Helper()
	sb, st, err := Create()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		st.Close()
		sb.Cleanup()
	})
	return sb, st
}

func TestWalkthroughSalamStage(t *testing.T) {
	if home, err := os.UserHomeDir(); err != nil {
		t.Skip("home directory unavailable")
	} else if _, err := os.Stat(home + "/.agents"); err != nil {
		t.Skip("host global agent workspace not mounted")
	}
	sb, st := newSandbox(t)

	rec, err := runSalamStage(st, sb.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := rec.Metrics["wiring_status"]; got != "wired" {
		t.Errorf("wiring_status = %v, want wired", got)
	}
	if got := rec.Metrics["heal_automatic"]; got != 0 {
		t.Errorf("heal_automatic = %v, want 0", got)
	}
	if got := rec.Metrics["goals_total"]; got != 4 {
		t.Errorf("goals_total = %v, want 4", got)
	}
	if got := rec.Metrics["claimed"]; got != 1 {
		t.Errorf("claimed = %v, want 1", got)
	}
	if got := rec.Metrics["ready"]; got != 1 {
		t.Errorf("ready = %v, want 1", got)
	}
	if got := rec.Metrics["blocked"]; got != 1 {
		t.Errorf("blocked = %v, want 1", got)
	}
	if got := rec.Metrics["implemented"]; got != 1 {
		t.Errorf("implemented = %v, want 1", got)
	}
}

func TestWalkthroughPrimeStage(t *testing.T) {
	sb, st := newSandbox(t)

	rec, err := runPrimeStage(st, sb.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := rec.Metrics["fits_budget"]; got != true {
		t.Errorf("fits_budget = %v, want true", got)
	}
	if tokens, _ := rec.Metrics["tokens"].(int); tokens > 1200 {
		t.Errorf("tokens = %d, want <= 1200", tokens)
	}
	joined := strings.Join(rec.Highlights, " ")
	if !strings.Contains(joined, "@devin") {
		t.Errorf("prime highlights should name the active handoff: %v", rec.Highlights)
	}
}

func TestWalkthroughRecallStage(t *testing.T) {
	sb, st := newSandbox(t)

	rec, err := runRecallStage(st, sb.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := rec.Metrics["top_kind"]; got != "lesson" {
		t.Errorf("top_kind = %v, want lesson", got)
	}
	if lessons, _ := rec.Metrics["lessons"].(int); lessons < 1 {
		t.Errorf("lessons = %d, want >= 1", lessons)
	}
	if cases, _ := rec.Metrics["cases"].(int); cases < 1 {
		t.Errorf("cases = %d, want >= 1", cases)
	}
	joined := strings.Join(rec.Highlights, " ")
	if !strings.Contains(joined, "untrusted") {
		t.Errorf("recall must frame records as untrusted: %v", rec.Highlights)
	}
}

func TestWalkthroughFactsStage(t *testing.T) {
	sb, st := newSandbox(t)

	rec, err := runFactsStage(st, sb.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := rec.Metrics["chain_intact"]; got != true {
		t.Errorf("chain_intact = %v, want true", got)
	}
	if got := rec.Metrics["stale_claims"]; got != 1 {
		t.Errorf("stale_claims = %v, want 1", got)
	}
	if got := rec.Metrics["newly_stale"]; got != 0 {
		t.Errorf("newly_stale = %v, want 0", got)
	}
}

func TestWalkthroughGateStage(t *testing.T) {
	sb, _ := newSandbox(t)

	rec, err := runGateStage(sb)
	if err != nil {
		t.Fatal(err)
	}
	if got := rec.Metrics["ungrounded_status"]; got != "fail" {
		t.Errorf("ungrounded_status = %v, want fail", got)
	}
	if got := rec.Metrics["ungrounded_code"]; got != "MEMORY_WRITE_MISSING" {
		t.Errorf("ungrounded_code = %v, want MEMORY_WRITE_MISSING", got)
	}
	if got := rec.Metrics["grounded_status"]; got != "pass" {
		t.Errorf("grounded_status = %v, want pass", got)
	}
}

func TestWalkthroughStandupStage(t *testing.T) {
	sb, st := newSandbox(t)

	rec, err := runStandupStage(st, sb.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if commits, _ := rec.Metrics["commits"].(int); commits < 1 {
		t.Errorf("commits = %d, want >= 1", commits)
	}
}

func TestWalkthroughTerminalWidthAndStyle(t *testing.T) {
	sb, st := newSandbox(t)

	rep, err := ExecuteWalkthrough(sb, st, WalkthroughOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	ui := render.New(os.Stdout, true)
	RenderWalkthroughHuman(&buf, ui, rep, 80)

	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if w := render.VisibleLen(line); w > 80 {
			t.Errorf("line exceeds 80 columns (%d): %q", w, line)
		}
		if strings.Contains(line, "—") {
			t.Errorf("line contains an em dash: %q", line)
		}
	}
	if strings.Count(buf.String(), "/7 ") != 7 {
		t.Errorf("expected 7 stage headers, got %d", strings.Count(buf.String(), "/7 "))
	}
}

func TestRenderWalkthroughTOON(t *testing.T) {
	sb, st := newSandbox(t)

	rep, err := ExecuteWalkthrough(sb, st, WalkthroughOptions{})
	if err != nil {
		t.Fatal(err)
	}
	out := RenderWalkthroughTOON(rep)
	if !strings.HasPrefix(out, "demo_stages[7]{index,id,command,status,summary}:") {
		t.Errorf("unexpected TOON header: %q", firstLine(out))
	}
	for _, id := range CanonicalStageOrder {
		if !strings.Contains(out, id) {
			t.Errorf("TOON output missing stage %s", id)
		}
	}
}
