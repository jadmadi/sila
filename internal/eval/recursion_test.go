package eval

import (
	"context"
	"strings"
	"testing"
)

type stubProposer struct {
	fn func(ctx context.Context, req StageProposalRequest) (ComponentTree, bool, error)
}

func (s *stubProposer) ProposeStage(ctx context.Context, req StageProposalRequest) (ComponentTree, bool, error) {
	return s.fn(ctx, req)
}

type stubRolloutRunner struct {
	fn func(ctx context.Context, req RunRequest) (*RunResult, error)
}

func (r *stubRolloutRunner) Name() string      { return "stub" }
func (r *stubRolloutRunner) IsAvailable() bool { return true }
func (r *stubRolloutRunner) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	return r.fn(ctx, req)
}

func TestRecursionSchedules(t *testing.T) {
	baseTree := ComponentTree{
		"AGENTS.md": "# Base Instructions\n",
	}
	testCase := &Case{
		ID:     "case-contrastive-01",
		Prompt: PromptSpec{Body: "Generate financial_report.xlsx"},
	}

	t.Run("parallel agg schedule executes 4k rollouts with answer-free evidence", func(t *testing.T) {
		var promptsSeen []string
		proposer := &stubProposer{
			fn: func(ctx context.Context, req StageProposalRequest) (ComponentTree, bool, error) {
				promptsSeen = append(promptsSeen, req.EvidencePrompt)
				next := CloneTree(req.BaseTree)
				next["AGENTS.md"] = req.BaseTree["AGENTS.md"] + "\n- Stage " + req.StageName + " (" + req.Dimension + ")\n"
				return next, false, nil
			},
		}
		var rollouts []string
		runner := &stubRolloutRunner{
			fn: func(ctx context.Context, req RunRequest) (*RunResult, error) {
				rollouts = append(rollouts, req.Arm)
				out := "deliverable: report.xlsx\nPASS: grader_secret_should_be_stripped"
				if req.Arm == "base2" {
					out = "" // simulate non-delivering replicate
				}
				return &RunResult{
					Output:   out,
					ExitCode: 0,
				}, nil
			},
		}

		res, err := RunParallel(context.Background(), RecursionConfig{
			Case:     testCase,
			WorkDir:  t.TempDir(),
			K:        2,
			Surface:  CodexSurface,
			BaseTree: baseTree,
			Runner:   runner,
			Proposer: proposer,
		})
		if err != nil {
			t.Fatalf("RunParallel error: %v", err)
		}
		// 4k rollouts for k=2 -> 8 rollouts (base1, base2, inv1, inv2, dom1, dom2, agg1, agg2)
		if len(rollouts) != 8 {
			t.Fatalf("expected 8 rollouts (4k for k=2), got %d: %v", len(rollouts), rollouts)
		}
		if len(res.Stages) != 6 {
			t.Fatalf("expected 6 stages (3k for k=2), got %d", len(res.Stages))
		}
		for _, p := range promptsSeen {
			if strings.Contains(p, "PASS: grader") {
				t.Fatalf("expected grader strings to be stripped from stage evidence prompt, got:\n%s", p)
			}
		}
		// Verify non-delivering rollout warning appears in AGG stage prompt
		aggPrompt := promptsSeen[len(promptsSeen)-1]
		if !strings.Contains(aggPrompt, "unsafe to adopt unless corroborated") {
			t.Fatalf("expected non-delivering replicate warning in composition prompt, got:\n%s", aggPrompt)
		}
	})

	t.Run("sequential seq schedule executes 4k rollouts across k rounds", func(t *testing.T) {
		proposer := &stubProposer{
			fn: func(ctx context.Context, req StageProposalRequest) (ComponentTree, bool, error) {
				next := CloneTree(req.BaseTree)
				next["AGENTS.md"] = req.BaseTree["AGENTS.md"] + "\n- " + req.StageName + "\n"
				return next, false, nil
			},
		}
		var rollouts []string
		runner := &stubRolloutRunner{
			fn: func(ctx context.Context, req RunRequest) (*RunResult, error) {
				rollouts = append(rollouts, req.Arm)
				return &RunResult{Output: "deliverable: ok.txt", ExitCode: 0}, nil
			},
		}

		res, err := RunSequential(context.Background(), RecursionConfig{
			Case:     testCase,
			WorkDir:  t.TempDir(),
			K:        2,
			Surface:  CodexSurface,
			BaseTree: baseTree,
			Runner:   runner,
			Proposer: proposer,
		})
		if err != nil {
			t.Fatalf("RunSequential error: %v", err)
		}
		// k=2 -> base1, base2 + round1(inv1, dom1, seq1) + round2(inv2, dom2, seq2) = 8 rollouts
		if len(rollouts) != 8 {
			t.Fatalf("expected 8 rollouts in sequential mode, got %d: %v", len(rollouts), rollouts)
		}
		if len(res.Stages) != 6 {
			t.Fatalf("expected 6 stages in sequential mode, got %d", len(res.Stages))
		}
		if res.Stages[2].Name != "seq1" || res.Stages[5].Name != "seq2" {
			t.Fatalf("unexpected sequential stage names: %+v", res.Stages)
		}
	})
}

func TestRecursionValidationRetryAndKeepBase(t *testing.T) {
	baseTree := ComponentTree{
		"AGENTS.md": "# Base Rules\n",
	}

	t.Run("repairs validation error on attempt 2 after feeding error prose back", func(t *testing.T) {
		callCount := 0
		var retryErrorSeen string
		proposer := &stubProposer{
			fn: func(ctx context.Context, req StageProposalRequest) (ComponentTree, bool, error) {
				callCount++
				retryErrorSeen = req.LastValidationError
				if callCount == 1 {
					// Emit an over-budget tree on attempt 1
					return ComponentTree{
						"AGENTS.md": strings.Repeat("x", CodexSurface.AlwaysOnMax+500),
					}, false, nil
				}
				// Repair on attempt 2
				return ComponentTree{
					"AGENTS.md": "# Base Rules\n- Trimmed repair\n",
				}, false, nil
			},
		}

		stage, err := RunStage(context.Background(), StageProposalRequest{
			StageName:      "inv1",
			Dimension:      DimINV,
			Surface:        CodexSurface,
			BaseTree:       baseTree,
			EvidencePrompt: "Base rollouts summary",
		}, proposer)
		if err != nil {
			t.Fatalf("RunStage error: %v", err)
		}
		if !stage.Accepted || stage.KeptBase || stage.Attempts != 2 {
			t.Fatalf("expected Accepted=true, KeptBase=false, Attempts=2; got %+v", stage)
		}
		if !strings.Contains(retryErrorSeen, "exceeds budget") {
			t.Fatalf("expected attempt 2 to receive budget error from attempt 1, got %q", retryErrorSeen)
		}
	})

	t.Run("falls back to baseTree when validation fails on both attempts", func(t *testing.T) {
		proposer := &stubProposer{
			fn: func(ctx context.Context, req StageProposalRequest) (ComponentTree, bool, error) {
				return ComponentTree{
					"AGENTS.md": strings.Repeat("z", CodexSurface.AlwaysOnMax+100),
				}, false, nil
			},
		}

		stage, err := RunStage(context.Background(), StageProposalRequest{
			StageName: "dom1",
			Dimension: DimDOM,
			Surface:   CodexSurface,
			BaseTree:  baseTree,
		}, proposer)
		if err != nil {
			t.Fatalf("RunStage error: %v", err)
		}
		if stage.Accepted || !stage.KeptBase || stage.Attempts != StageAttempts {
			t.Fatalf("expected Accepted=false, KeptBase=true, Attempts=%d; got %+v", StageAttempts, stage)
		}
		if stage.Tree["AGENTS.md"] != baseTree["AGENTS.md"] {
			t.Fatalf("expected fallback to baseTree")
		}
		if !strings.Contains(stage.ValidationError, "exceeds budget") {
			t.Fatalf("expected ValidationError to record failure reason, got %q", stage.ValidationError)
		}
	})

	t.Run("short-circuits cleanly when proposer emits KEEP_BASE", func(t *testing.T) {
		proposer := &stubProposer{
			fn: func(ctx context.Context, req StageProposalRequest) (ComponentTree, bool, error) {
				return nil, true, nil
			},
		}

		stage, err := RunStage(context.Background(), StageProposalRequest{
			StageName: "inv1",
			Dimension: DimINV,
			Surface:   CodexSurface,
			BaseTree:  baseTree,
		}, proposer)
		if err != nil {
			t.Fatalf("RunStage error: %v", err)
		}
		if stage.Accepted || !stage.KeptBase || stage.Attempts != 1 {
			t.Fatalf("expected KEEP_BASE on attempt 1, got %+v", stage)
		}
	})
}
