package main

import (
	"encoding/json"
	"flag"
	"os"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestDecisionCreateInvocationForms(t *testing.T) {
	dbPath := t.TempDir() + "/decisions.db"
	ui := render.New(os.Stdout, true)
	forms := [][]string{
		{"create", "Title One", "-o", "A: x", "-o", "B: y", "-r", "A"},
		{"-o", "A: x", "-o", "B: y", "-r", "A", "create", "Title Two"},
		{"create", "--options=A: x;B: y", "--recommend=A", "Title Three"},
	}
	for i, args := range forms {
		out := captureStdout(t, func() {
			runDecision(ui, dbPath, "", "agent", "", true, args)
		})
		var decision store.Decision
		if err := json.Unmarshal([]byte(out), &decision); err != nil {
			t.Fatalf("form %d: invalid JSON: %v\n%s", i+1, err, out)
		}
		if len(decision.Options) != 2 || decision.Recommendation != "A" {
			t.Fatalf("form %d: unexpected decision: %+v", i+1, decision)
		}
	}
}

func TestUnknownCommandContract(t *testing.T) {
	near := captureStderr(t, func() { unknownCommandError("goalz") })
	lines := strings.Count(strings.TrimSpace(near), "\n") + 1
	if !strings.Contains(near, `unknown command "goalz"`) || !strings.Contains(near, `did you mean "goals"`) || lines > 6 {
		t.Fatalf("bad near-miss error:\n%s", near)
	}
	far := captureStderr(t, func() { unknownCommandError("definitely-not-a-command") })
	if strings.Contains(far, "did you mean") || !strings.Contains(far, "run 'sila --help' for usage") {
		t.Fatalf("bad unknown-command error:\n%s", far)
	}
	if got := suggestCommand("goalz"); got != "goals" {
		t.Fatalf("suggestCommand(goalz) = %q", got)
	}
	if got := suggestCommand("definitely-not-a-command"); got != "" {
		t.Fatalf("unexpected suggestion %q", got)
	}
}

func TestSubcommandHelpUsesProvidedStream(t *testing.T) {
	out := captureStdout(t, func() {
		ui := render.New(os.Stdout, true)
		subcommandUsageTo("gate", ui, os.Stdout)
	})
	if !strings.Contains(out, "sila gate") {
		t.Fatalf("gate help was not written to stdout:\n%s", out)
	}
}

func TestReorderFlagsLeavesDecisionOwnedFlags(t *testing.T) {
	flagSet := flag.NewFlagSet("test", flag.ContinueOnError)
	_ = newRootFlags(flagSet, "decision")
	flags, pos, unknown := reorderFlagsForSub(flagSet, "decision", []string{"create", "Title", "-o", "A", "--recommend=A", "--json"})
	if !unknown {
		t.Fatal("decision-owned flags should be unknown to the root parser")
	}
	if len(flags) == 0 || flags[0] != "--json" {
		t.Fatalf("global --json should still be a root flag: flags=%v pos=%v", flags, pos)
	}
	joined := strings.Join(pos, " ")
	if !strings.Contains(joined, "-o A") || !strings.Contains(joined, "--recommend=A") {
		t.Fatalf("decision-owned flags were not preserved: %v", pos)
	}
}
