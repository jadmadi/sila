package eval

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// StageAttempts bounds the propose -> validate repair loop to at most 1 retry per stage.
const StageAttempts = 2

// RecursionMode selects the contrastive recursion schedule.
type RecursionMode string

const (
	ModeParallel   RecursionMode = "parallel"
	ModeSequential RecursionMode = "sequential"
)

// Proposal dimension constants separating how-to-work (INV) from domain rules (DOM).
const (
	DimINV = "INV"
	DimDOM = "DOM"
	DimAGG = "AGG"
	DimSEQ = "SEQ"
)

// RolloutEvidence captures an answer-free view of a single rollout for contrastive comparison.
type RolloutEvidence struct {
	Label         string        `json:"label"`
	Deliverables  []string      `json:"deliverables"`
	Delivered     bool          `json:"delivered"`
	Duration      time.Duration `json:"duration"`
	SanitizedLog  string        `json:"sanitized_log"`
	CandidateTree ComponentTree `json:"candidate_tree,omitempty"`
}

// StageRecord captures the decision, retry count, validation outcome, and resulting tree of a recursion stage.
type StageRecord struct {
	Name            string        `json:"name"`
	Dimension       string        `json:"dimension"`
	Accepted        bool          `json:"accepted"`
	KeptBase        bool          `json:"kept_base"`
	Attempts        int           `json:"attempts"`
	ValidationError string        `json:"validation_error,omitempty"`
	Tree            ComponentTree `json:"tree"`
}

// StageProposalRequest encapsulates the answer-free inputs passed to a StageProposer.
type StageProposalRequest struct {
	StageName           string
	Dimension           string
	Surface             SurfaceSpec
	BaseTree            ComponentTree
	EvidencePrompt      string
	Attempt             int
	LastValidationError string
}

// StageProposer generates a candidate ComponentTree or signals keepBase=true (KEEP_BASE).
type StageProposer interface {
	ProposeStage(ctx context.Context, req StageProposalRequest) (tree ComponentTree, keepBase bool, err error)
}

// RecursionConfig configures a parallel (agg) or sequential (seq) contrastive harness recursion run.
type RecursionConfig struct {
	Case     *Case
	WorkDir  string
	K        int
	Surface  SurfaceSpec
	BaseTree ComponentTree
	Runner   HarnessRunner
	Proposer StageProposer
}

// RecursionResult holds all base rollouts, stage records, and stage rollouts from a recursion schedule.
type RecursionResult struct {
	Mode     RecursionMode              `json:"mode"`
	K        int                        `json:"k"`
	Base     map[string]RolloutEvidence `json:"base"`
	Stages   []StageRecord              `json:"stages"`
	Rollouts map[string]RolloutEvidence `json:"rollouts"`
	Trees    map[string]ComponentTree   `json:"trees"`
}

// BuildEvidenceContext formats rollout evidence into an answer-free prompt section,
// stripping grader verdict markers and flagging any non-delivering candidate rollouts.
func BuildEvidenceContext(taskPrompt string, spec SurfaceSpec, baseTree ComponentTree, rollouts map[string]RolloutEvidence) string {
	var b strings.Builder
	b.WriteString("## Task Prompt (Answer-Free Boundary)\n")
	b.WriteString(strings.TrimSpace(taskPrompt))
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("## Surface Budget (%s): %d / %d always-on chars\n\n",
		spec.Name, AlwaysOnChars(baseTree, spec), spec.AlwaysOnMax))

	keys := make([]string, 0, len(rollouts))
	for k := range rollouts {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	deliveringCount := 0
	for _, k := range keys {
		ev := rollouts[k]
		if ev.Delivered {
			deliveringCount++
		}
		b.WriteString(fmt.Sprintf("### Rollout `%s` (delivered=%v, deliverables=%d)\n", k, ev.Delivered, len(ev.Deliverables)))
		if !ev.Delivered {
			b.WriteString("WARNING: This rollout produced zero deliverables; any edit from a non-delivering rollout is unsafe to adopt unless corroborated by a delivering rollout.\n")
		}
		if ev.SanitizedLog != "" {
			b.WriteString(ev.SanitizedLog)
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}

	if len(keys) > 1 && deliveringCount > 0 && deliveringCount < len(keys) {
		b.WriteString("REPLICATE DIVERGENCE DETECTED: Some replicates delivered while others failed to produce deliverables. Target the divergence point.\n")
	}
	return b.String()
}

func toRolloutEvidence(label string, res *RunResult, tree ComponentTree) RolloutEvidence {
	rawOut := ""
	var dur time.Duration
	if res != nil {
		rawOut = res.Output
		dur = res.Duration
	}
	var cleanLines []string
	var deliverables []string
	for _, line := range strings.Split(rawOut, "\n") {
		if graderLeakRE.MatchString(line) {
			continue
		}
		cleanLines = append(cleanLines, line)
		trimmed := strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(trimmed, "deliverable:"); ok {
			d := strings.TrimSpace(after)
			if d != "" {
				deliverables = append(deliverables, d)
			}
		}
	}
	sanitized := strings.TrimSpace(strings.Join(cleanLines, "\n"))
	if len(deliverables) == 0 && sanitized != "" {
		deliverables = append(deliverables, "stdout_artifact")
	}
	return RolloutEvidence{
		Label:         label,
		Deliverables:  deliverables,
		Delivered:     len(deliverables) > 0,
		Duration:      dur,
		SanitizedLog:  sanitized,
		CandidateTree: CloneTree(tree),
	}
}

// RunStage runs up to StageAttempts (2) propose + validate cycles for a single recursion stage.
func RunStage(ctx context.Context, req StageProposalRequest, proposer StageProposer) (StageRecord, error) {
	fallback := CloneTree(req.BaseTree)
	if proposer == nil {
		return StageRecord{
			Name:      req.StageName,
			Dimension: req.Dimension,
			Accepted:  false,
			KeptBase:  true,
			Attempts:  1,
			Tree:      fallback,
		}, nil
	}

	lastErr := ""
	for attempt := 1; attempt <= StageAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return StageRecord{}, err
		}
		attemptReq := req
		attemptReq.Attempt = attempt
		attemptReq.LastValidationError = lastErr

		cand, keepBase, err := proposer.ProposeStage(ctx, attemptReq)
		if err != nil {
			return StageRecord{}, fmt.Errorf("stage %s attempt %d: %w", req.StageName, attempt, err)
		}
		if keepBase {
			return StageRecord{
				Name:      req.StageName,
				Dimension: req.Dimension,
				Accepted:  false,
				KeptBase:  true,
				Attempts:  attempt,
				Tree:      fallback,
			}, nil
		}

		triggered := AutoTriggerSkills(cand, req.Surface)
		if valErr := ValidateTree(triggered, req.Surface); valErr != nil {
			lastErr = valErr.Error()
			continue
		}

		return StageRecord{
			Name:      req.StageName,
			Dimension: req.Dimension,
			Accepted:  true,
			KeptBase:  false,
			Attempts:  attempt,
			Tree:      triggered,
		}, nil
	}

	return StageRecord{
		Name:            req.StageName,
		Dimension:       req.Dimension,
		Accepted:        false,
		KeptBase:        true,
		Attempts:        StageAttempts,
		ValidationError: lastErr,
		Tree:            fallback,
	}, nil
}

// RunParallel executes the parallel (agg) schedule:
// k base rollouts -> k INV + k DOM proposals & rollouts -> k AGG compositions & rollouts (4k rollouts total).
func RunParallel(ctx context.Context, cfg RecursionConfig) (*RecursionResult, error) {
	if err := validateRecursionConfig(&cfg); err != nil {
		return nil, err
	}
	out := &RecursionResult{
		Mode:     ModeParallel,
		K:        cfg.K,
		Base:     make(map[string]RolloutEvidence, cfg.K),
		Stages:   make([]StageRecord, 0, 3*cfg.K),
		Rollouts: make(map[string]RolloutEvidence, 4*cfg.K),
		Trees:    make(map[string]ComponentTree, 4*cfg.K),
	}

	for i := 1; i <= cfg.K; i++ {
		label := fmt.Sprintf("base%d", i)
		runRes, err := RunRolloutWithTree(ctx, cfg.Runner, cfg.Case, cfg.WorkDir, label, cfg.BaseTree)
		if err != nil {
			return nil, err
		}
		ev := toRolloutEvidence(label, runRes, cfg.BaseTree)
		out.Base[label] = ev
		out.Rollouts[label] = ev
		out.Trees[label] = CloneTree(cfg.BaseTree)
	}

	baseEvidence := BuildEvidenceContext(cfg.Case.Prompt.Body, cfg.Surface, cfg.BaseTree, out.Base)

	for _, dim := range []struct {
		prefix string
		dim    string
	}{
		{prefix: "inv", dim: DimINV},
		{prefix: "dom", dim: DimDOM},
	} {
		for i := 1; i <= cfg.K; i++ {
			stageName := fmt.Sprintf("%s%d", dim.prefix, i)
			stage, err := RunStage(ctx, StageProposalRequest{
				StageName:      stageName,
				Dimension:      dim.dim,
				Surface:        cfg.Surface,
				BaseTree:       cfg.BaseTree,
				EvidencePrompt: baseEvidence,
			}, cfg.Proposer)
			if err != nil {
				return nil, err
			}
			out.Stages = append(out.Stages, stage)
			out.Trees[stageName] = CloneTree(stage.Tree)

			runRes, err := RunRolloutWithTree(ctx, cfg.Runner, cfg.Case, cfg.WorkDir, stageName, stage.Tree)
			if err != nil {
				return nil, err
			}
			out.Rollouts[stageName] = toRolloutEvidence(stageName, runRes, stage.Tree)
		}
	}

	poolEvidence := BuildEvidenceContext(cfg.Case.Prompt.Body, cfg.Surface, cfg.BaseTree, out.Rollouts)
	for i := 1; i <= cfg.K; i++ {
		stageName := fmt.Sprintf("agg%d", i)
		stage, err := RunStage(ctx, StageProposalRequest{
			StageName:      stageName,
			Dimension:      DimAGG,
			Surface:        cfg.Surface,
			BaseTree:       cfg.BaseTree,
			EvidencePrompt: poolEvidence,
		}, cfg.Proposer)
		if err != nil {
			return nil, err
		}
		out.Stages = append(out.Stages, stage)
		out.Trees[stageName] = CloneTree(stage.Tree)

		runRes, err := RunRolloutWithTree(ctx, cfg.Runner, cfg.Case, cfg.WorkDir, stageName, stage.Tree)
		if err != nil {
			return nil, err
		}
		out.Rollouts[stageName] = toRolloutEvidence(stageName, runRes, stage.Tree)
	}

	return out, nil
}

// RunSequential executes the sequential (seq) schedule:
// k base rollouts -> t=1..k rounds of (inv_t + dom_t proposals & rollouts -> seq_t composition & rollout) (4k rollouts total).
func RunSequential(ctx context.Context, cfg RecursionConfig) (*RecursionResult, error) {
	if err := validateRecursionConfig(&cfg); err != nil {
		return nil, err
	}
	out := &RecursionResult{
		Mode:     ModeSequential,
		K:        cfg.K,
		Base:     make(map[string]RolloutEvidence, cfg.K),
		Stages:   make([]StageRecord, 0, 3*cfg.K),
		Rollouts: make(map[string]RolloutEvidence, 4*cfg.K),
		Trees:    make(map[string]ComponentTree, 4*cfg.K),
	}

	for i := 1; i <= cfg.K; i++ {
		label := fmt.Sprintf("base%d", i)
		runRes, err := RunRolloutWithTree(ctx, cfg.Runner, cfg.Case, cfg.WorkDir, label, cfg.BaseTree)
		if err != nil {
			return nil, err
		}
		ev := toRolloutEvidence(label, runRes, cfg.BaseTree)
		out.Base[label] = ev
		out.Rollouts[label] = ev
		out.Trees[label] = CloneTree(cfg.BaseTree)
	}

	currentTree := CloneTree(cfg.BaseTree)
	for round := 1; round <= cfg.K; round++ {
		roundEvidence := BuildEvidenceContext(cfg.Case.Prompt.Body, cfg.Surface, currentTree, out.Rollouts)
		for _, dim := range []struct {
			prefix string
			dim    string
		}{
			{prefix: "inv", dim: DimINV},
			{prefix: "dom", dim: DimDOM},
		} {
			stageName := fmt.Sprintf("%s%d", dim.prefix, round)
			stage, err := RunStage(ctx, StageProposalRequest{
				StageName:      stageName,
				Dimension:      dim.dim,
				Surface:        cfg.Surface,
				BaseTree:       currentTree,
				EvidencePrompt: roundEvidence,
			}, cfg.Proposer)
			if err != nil {
				return nil, err
			}
			out.Stages = append(out.Stages, stage)
			out.Trees[stageName] = CloneTree(stage.Tree)

			runRes, err := RunRolloutWithTree(ctx, cfg.Runner, cfg.Case, cfg.WorkDir, stageName, stage.Tree)
			if err != nil {
				return nil, err
			}
			out.Rollouts[stageName] = toRolloutEvidence(stageName, runRes, stage.Tree)
		}

		compEvidence := BuildEvidenceContext(cfg.Case.Prompt.Body, cfg.Surface, currentTree, out.Rollouts)
		seqName := fmt.Sprintf("seq%d", round)
		seqStage, err := RunStage(ctx, StageProposalRequest{
			StageName:      seqName,
			Dimension:      DimSEQ,
			Surface:        cfg.Surface,
			BaseTree:       currentTree,
			EvidencePrompt: compEvidence,
		}, cfg.Proposer)
		if err != nil {
			return nil, err
		}
		out.Stages = append(out.Stages, seqStage)
		out.Trees[seqName] = CloneTree(seqStage.Tree)

		runRes, err := RunRolloutWithTree(ctx, cfg.Runner, cfg.Case, cfg.WorkDir, seqName, seqStage.Tree)
		if err != nil {
			return nil, err
		}
		out.Rollouts[seqName] = toRolloutEvidence(seqName, runRes, seqStage.Tree)
		currentTree = CloneTree(seqStage.Tree)
	}

	return out, nil
}

func validateRecursionConfig(cfg *RecursionConfig) error {
	if cfg.Case == nil {
		return fmt.Errorf("recursion config requires non-nil Case")
	}
	if cfg.Runner == nil {
		return fmt.Errorf("recursion config requires non-nil Runner")
	}
	if cfg.K < 1 {
		return fmt.Errorf("recursion k must be >= 1, got %d", cfg.K)
	}
	if cfg.Surface.AlwaysOnMax <= 0 {
		cfg.Surface = CodexSurface
	}
	if len(cfg.BaseTree) == 0 {
		primary := "AGENTS.md"
		if len(cfg.Surface.AlwaysOn) > 0 {
			primary = cfg.Surface.AlwaysOn[0]
		}
		cfg.BaseTree = ComponentTree{primary: "# Base Harness Instructions\n"}
	}
	return nil
}
