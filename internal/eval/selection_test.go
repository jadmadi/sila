package eval

import (
	"context"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/dream"
)

func TestSelectionAnonymizationAndPool(t *testing.T) {
	rollouts := map[string]RolloutEvidence{
		"base1": {Label: "base1", Delivered: true, Deliverables: []string{"report.xlsx"}, SanitizedLog: "base1 output"},
		"base2": {Label: "base2", Delivered: false, Deliverables: nil, SanitizedLog: ""},
		"inv1":  {Label: "inv1", Delivered: true, Deliverables: []string{"report.xlsx"}, SanitizedLog: "inv1 output"},
		"dom1":  {Label: "dom1", Delivered: false, Deliverables: nil, SanitizedLog: ""},
		"agg1":  {Label: "agg1", Delivered: true, Deliverables: []string{"report.xlsx", "audit.txt"}, SanitizedLog: "agg1 output"},
	}

	t.Run("filters non-delivering candidates and assigns deterministic anonymous IDs", func(t *testing.T) {
		pool := FilterDeliveringPool(rollouts)
		if len(pool) != 3 {
			t.Fatalf("expected 3 delivering candidates, got %d: %v", len(pool), pool)
		}
		for _, k := range pool {
			if k == "base2" || k == "dom1" {
				t.Fatalf("non-delivering candidate %q was not filtered out", k)
			}
		}

		ids1 := AssignAnonymousIDs(pool, "task-01", 2, ModeParallel)
		ids2 := AssignAnonymousIDs(pool, "task-01", 2, ModeParallel)
		if len(ids1) != 3 {
			t.Fatalf("expected 3 assigned IDs, got %d", len(ids1))
		}
		seenAnon := make(map[string]bool)
		for cand, anon := range ids1 {
			if ids2[cand] != anon {
				t.Fatalf("AssignAnonymousIDs is non-deterministic for %s: %q vs %q", cand, anon, ids2[cand])
			}
			if seenAnon[anon] {
				t.Fatalf("duplicate anonymous ID %q assigned", anon)
			}
			seenAnon[anon] = true
		}
	})

	t.Run("blind selector hides candidate stage names and resolves chosen candidate", func(t *testing.T) {
		pool := FilterDeliveringPool(rollouts)
		ids := AssignAnonymousIDs(pool, "task-01", 2, ModeParallel)
		wantAnon := ids["agg1"]

		var promptSeen string
		sel, err := RunBlindSelection(context.Background(), BlindSelectionRequest{
			TaskID:     "task-01",
			TaskPrompt: "Create financial report",
			K:          2,
			Mode:       ModeParallel,
			Rollouts:   rollouts,
		}, func(ctx context.Context, prompt string) (string, error) {
			promptSeen = prompt
			return "<rationale>Candidate " + wantAnon + " delivered complete verified output.</rationale>\n<selected>" + wantAnon + "</selected>", nil
		})
		if err != nil {
			t.Fatalf("RunBlindSelection error: %v", err)
		}
		if sel.Chosen != "agg1" {
			t.Fatalf("expected Chosen='agg1', got %q (ids=%v)", sel.Chosen, ids)
		}
		for _, stageName := range []string{"base1", "inv1", "agg1"} {
			if strings.Contains(promptSeen, "Candidate "+stageName) {
				t.Fatalf("selection prompt leaked raw stage label %s:\n%s", stageName, promptSeen)
			}
		}
		if !strings.Contains(promptSeen, "1. Delivery") || !strings.Contains(promptSeen, "6. Finish") {
			t.Fatalf("selection prompt missing 6 ordered deliverable criteria:\n%s", promptSeen)
		}
	})
}

func TestCausalAttributionAndDreamDistill(t *testing.T) {
	baseTree := ComponentTree{
		"AGENTS.md": "# Base Rules\n",
	}

	t.Run("marks multi-component unisolated edits as confounded and excludes them from lessons", func(t *testing.T) {
		trees := map[string]ComponentTree{
			"base1": baseTree,
			"agg1": {
				"AGENTS.md":        "# Base Rules\n- Modified prompt\n",
				"scripts/check.py": "print('new script')\n",
			},
		}
		rollouts := map[string]RolloutEvidence{
			"base1": {Label: "base1", Delivered: false},
			"agg1":  {Label: "agg1", Delivered: true, Deliverables: []string{"out.xlsx"}},
		}

		attrs := AttributeComponentEdits(baseTree, trees, rollouts, CodexSurface)
		if len(attrs) != 2 {
			t.Fatalf("expected 2 component attributions for agg1, got %d: %+v", len(attrs), attrs)
		}
		for _, a := range attrs {
			if a.Verdict != VerdictConfounded {
				t.Fatalf("expected VerdictConfounded for bundled unisolated edit %+v, got %s", a, a.Verdict)
			}
		}

		exp := ExtractCausalExperience("task-01", "financial_xlsx", attrs)
		if exp != nil {
			t.Fatalf("expected nil ExperienceRecord when all edits are confounded, got %+v", exp)
		}
	})

	t.Run("attributes isolated single-component improvement as helped and distills into CandidateLesson", func(t *testing.T) {
		trees := map[string]ComponentTree{
			"base1": baseTree,
			"inv1": {
				"AGENTS.md":               "# Base Rules\n",
				"scripts/verify_links.go": "package main\nfunc main() {}\n",
			},
		}
		rollouts := map[string]RolloutEvidence{
			"base1": {Label: "base1", Delivered: false},
			"inv1":  {Label: "inv1", Delivered: true, Deliverables: []string{"verified.json"}},
		}

		attrs := AttributeComponentEdits(baseTree, trees, rollouts, CodexSurface)
		if len(attrs) != 1 {
			t.Fatalf("expected 1 attribution for inv1, got %d: %+v", len(attrs), attrs)
		}
		if attrs[0].Verdict != VerdictHelped || attrs[0].Component != CompScripts {
			t.Fatalf("expected VerdictHelped on CompScripts, got %+v", attrs[0])
		}

		exp := ExtractCausalExperience("task-01", "web_verification", attrs)
		if exp == nil {
			t.Fatalf("expected non-nil ExperienceRecord for isolated helped edit")
		}
		lesson := dream.DistillExperienceToLesson("task-01", exp.Domain, exp.Cue, exp.Lesson, exp.Evidence, []string{attrs[0].Path})
		if lesson.Symptom == "" || lesson.Fix == "" || len(lesson.Files) != 1 || lesson.Files[0] != "scripts/verify_links.go" {
			t.Fatalf("unexpected distilled CandidateLesson: %+v", lesson)
		}
	})
}
