package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/render"
)

func writeGoalsFixture(t *testing.T, dir string) {
	t.Helper()
	pkg := filepath.Join(dir, "goals", "demo")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "goal.md"), []byte("# Goal: demo\n\n## Goal Description\nA demo goal.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "plan.md"), []byte("- [x] first\n- [ ] second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGoalsSyncNoticeContract(t *testing.T) {
	cases := []struct {
		name                   string
		noSync, machine, quiet bool
		filter                 string
		total                  int
		want                   bool
	}{
		{"default", false, false, false, "", 1, true},
		{"no-sync", true, false, false, "", 1, false},
		{"machine", false, true, false, "", 1, false},
		{"quiet", false, false, true, "", 1, false},
		{"filtered", false, false, false, "ready", 1, false},
		{"empty", false, false, false, "", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := goalsSyncNotice(tc.noSync, tc.machine, tc.quiet, tc.filter, tc.total); got != tc.want {
				t.Fatalf("goalsSyncNotice = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGoalsTasksHeaderUsesRendererSeparator(t *testing.T) {
	tmp := t.TempDir()
	writeGoalsFixture(t, tmp)
	gc := &goalsContext{ui: render.New(os.Stdout, true)}
	out := captureStdout(t, func() { gc.handleTasks(tmp, []string{"tasks", "demo"}) })
	if !strings.Contains(out, "sila · goals · tasks — demo (1/2 completed)") {
		t.Fatalf("tasks header missing separator/slug/count:\n%s", out)
	}
}

func TestGoalsASCIICLIFlag(t *testing.T) {
	tmp := t.TempDir()
	writeGoalsFixture(t, tmp)
	out := captureStdout(t, func() {
		runGoals(render.New(os.Stdout, true), filepath.Join(tmp, "store.db"), tmp,
			"agent", "", "", "", false, false, "", "", "", "", "", 0,
			false, false, false, false, false, false, false, false,
			false, false, false, false, true,
			"", "", "", false, nil,
			goalsRunOptions{ascii: true, noBuild: true})
	})
	for _, r := range out {
		if r > 127 {
			t.Fatalf("--ascii output contains non-ASCII rune %q:\n%s", r, out)
		}
	}
	if !strings.Contains(out, "Legend: ship") {
		t.Fatalf("--ascii legend missing:\n%s", out)
	}
	if strings.Contains(out, "AGENT PROTOCOL") {
		t.Fatal("--ascii output contains the protocol block")
	}
}

func TestGoalsJSONStateLabel(t *testing.T) {
	tmp := t.TempDir()
	writeGoalsFixture(t, tmp)
	res, err := goals.ScanGoals(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Goals) != 1 {
		t.Fatalf("goals = %d, want 1", len(res.Goals))
	}
	g := res.Goals[0]
	if (g.Status != "pending" && g.Status != "draft") || g.State != goals.StateDraft {
		t.Fatalf("status/state = %q/%q, want pending|draft/draft", g.Status, g.State)
	}
	if g.StateLabel == "" || !strings.Contains(g.StateLabel, "draft") {
		t.Fatalf("state_label = %q", g.StateLabel)
	}
}
