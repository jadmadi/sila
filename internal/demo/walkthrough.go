package demo

import (
	"fmt"
	"io"
	"strings"

	"github.com/jadmadi/sila/internal/dream"
	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/salam"
	"github.com/jadmadi/sila/internal/sentry"
	"github.com/jadmadi/sila/internal/store"
)

// WalkthroughVersion is the stable schema id emitted by `sila demo --json`.
const WalkthroughVersion = "sila/demo-walkthrough/v1"

// CanonicalStageOrder defines the 7 ordered stages of the CLI walkthrough.
var CanonicalStageOrder = []string{
	"sync",
	"salam",
	"prime",
	"recall",
	"facts",
	"gate",
	"standup",
}

// stageTitles maps each stage id to its human title.
var stageTitles = map[string]string{
	"sync":    "Multi-harness ingestion and corroboration",
	"salam":   "Repository orientation and goal board",
	"prime":   "Bounded session-start briefing",
	"recall":  "Context-aware pre-task recall pack",
	"facts":   "Ledger integrity and file grounding audit",
	"gate":    "Pre-commit memory-write discipline",
	"standup": "Executive recap and handoff continuity",
}

// stageCommands maps each stage id to the live command a user would run.
var stageCommands = map[string]string{
	"sync":    "sila sync",
	"salam":   "sila salam",
	"prime":   "sila prime --budget=1200",
	"recall":  `sila recall "3DS webhook 500 double write" --paths=src/payments/checkout.go,src/payments/refunds.go`,
	"facts":   "sila facts verify",
	"gate":    "sila gate --pre-commit",
	"standup": "sila standup last 48h",
}

// WalkthroughOptions configures a CLI walkthrough execution.
type WalkthroughOptions struct {
	Steps []string // empty or ["all"] runs all 7 stages in CanonicalStageOrder
	Keep  bool     // retain Sandbox on disk after RunWalkthrough returns
	Width int      // target visible width (default 80 when <= 0)
}

// StageReceipt captures the deterministic outcome of one walkthrough stage.
type StageReceipt struct {
	Index      int            `json:"index"`
	ID         string         `json:"id"`
	Title      string         `json:"title"`
	Command    string         `json:"command"`
	Status     string         `json:"status"` // "ok"
	Summary    string         `json:"summary"`
	Highlights []string       `json:"highlights"`
	Metrics    map[string]any `json:"metrics,omitempty"`
}

// WalkthroughReport is the machine-readable envelope for `sila demo --json`.
type WalkthroughReport struct {
	Version    string         `json:"version"`
	ProjectID  string         `json:"project_id"`
	SandboxDir string         `json:"sandbox_dir"`
	StorePath  string         `json:"store_path"`
	Ephemeral  bool           `json:"ephemeral"`
	CleanedUp  bool           `json:"cleaned_up"`
	Stages     []StageReceipt `json:"stages"`
	NextAction string         `json:"next_action"`
}

// ParseWalkthroughSteps validates and normalizes a comma-separated --step value
// into CanonicalStageOrder. Returns an error on any unknown stage name.
func ParseWalkthroughSteps(raw string) ([]string, error) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" || raw == "all" {
		return append([]string(nil), CanonicalStageOrder...), nil
	}
	valid := make(map[string]int, len(CanonicalStageOrder))
	for i, id := range CanonicalStageOrder {
		valid[id] = i
	}
	seen := make(map[string]bool, len(CanonicalStageOrder))
	for tok := range strings.SplitSeq(raw, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		if tok == "all" {
			return append([]string(nil), CanonicalStageOrder...), nil
		}
		if _, ok := valid[tok]; !ok {
			return nil, fmt.Errorf("unknown --step value %q (valid: %s)", tok, strings.Join(append(append([]string(nil), CanonicalStageOrder...), "all"), ", "))
		}
		seen[tok] = true
	}
	if len(seen) == 0 {
		return append([]string(nil), CanonicalStageOrder...), nil
	}
	out := make([]string, 0, len(seen))
	for _, id := range CanonicalStageOrder {
		if seen[id] {
			out = append(out, id)
		}
	}
	return out, nil
}

// ExecuteWalkthrough runs the selected stages against an open demo Sandbox and
// Store. It never deletes the sandbox; the caller decides that and sets
// CleanedUp to match.
func ExecuteWalkthrough(sb *Sandbox, st *store.Store, opts WalkthroughOptions) (*WalkthroughReport, error) {
	if sb == nil || st == nil {
		return nil, fmt.Errorf("walkthrough requires a sandbox and an open store")
	}
	steps := opts.Steps
	if len(steps) == 0 {
		steps = append([]string(nil), CanonicalStageOrder...)
	}
	selected := make(map[string]bool, len(steps))
	for _, id := range steps {
		selected[id] = true
	}

	rep := &WalkthroughReport{
		Version:    WalkthroughVersion,
		ProjectID:  ProjectName,
		SandboxDir: sb.Root,
		StorePath:  sb.DBPath,
		Ephemeral:  !opts.Keep,
		NextAction: "Next: run 'sila salam' in your repo, or 'sila demo --web' for Web Cockpit.",
	}

	// Run in canonical order, independent of the caller's --step order.
	for i, id := range CanonicalStageOrder {
		if !selected[id] {
			continue
		}
		var (
			rec StageReceipt
			err error
		)
		switch id {
		case "sync":
			rec, err = runSyncStage(st)
		case "salam":
			rec, err = runSalamStage(st, sb.Dir)
		case "prime":
			rec, err = runPrimeStage(st, sb.Dir)
		case "recall":
			rec, err = runRecallStage(st, sb.Dir)
		case "facts":
			rec, err = runFactsStage(st, sb.Dir)
		case "gate":
			rec, err = runGateStage(sb)
		case "standup":
			rec, err = runStandupStage(st, sb.Dir)
		default:
			err = fmt.Errorf("unhandled stage %q", id)
		}
		if err != nil {
			return nil, fmt.Errorf("stage %s: %w", id, err)
		}
		rec.Index = i + 1
		rec.ID = id
		rec.Title = stageTitles[id]
		rec.Command = stageCommands[id]
		rec.Status = "ok"
		rep.Stages = append(rep.Stages, rec)
	}
	return rep, nil
}

func runSyncStage(st *store.Store) (StageReceipt, error) {
	msgs, factsN, verified, projects, err := st.Counts()
	if err != nil {
		return StageReceipt{}, err
	}
	rows, err := st.BenchmarkRows(ProjectName)
	if err != nil {
		return StageReceipt{}, err
	}
	harnesses := make([]string, 0, len(rows))
	for _, r := range rows {
		harnesses = append(harnesses, r.Tool)
	}
	facts, err := st.FactsByProject(ProjectName)
	if err != nil {
		return StageReceipt{}, err
	}
	corroborated := 0
	for _, f := range facts {
		if len(f.Tools) > 1 {
			corroborated++
		}
	}
	return StageReceipt{
		Summary: fmt.Sprintf("%d messages from %d harnesses; %d claims, %d grounded; sources unchanged",
			msgs, len(harnesses), factsN, verified),
		Highlights: []string{
			"Harnesses: " + strings.Join(harnesses, ", "),
			fmt.Sprintf("%d claim(s) corroborated by more than one harness", corroborated),
			"Ingestion is read-only: no source log was mutated",
		},
		Metrics: map[string]any{
			"messages":     msgs,
			"harnesses":    len(harnesses),
			"facts":        factsN,
			"verified":     verified,
			"projects":     projects,
			"corroborated": corroborated,
		},
	}, nil
}

func runSalamStage(st *store.Store, dir string) (StageReceipt, error) {
	brief, err := salam.BuildBrief(st, dir, ProjectName)
	if err != nil {
		return StageReceipt{}, err
	}
	audit, err := goals.ScanGoals(dir)
	if err != nil {
		return StageReceipt{}, err
	}
	claimed := 0
	implemented := 0
	for _, g := range audit.Goals {
		switch g.Status {
		case "claimed":
			claimed++
		case "implemented":
			implemented++
		}
	}
	next := brief.Wiring.NextCommand
	if next == "" {
		next = "sila goals ready"
	}
	highlights := []string{
		fmt.Sprintf("Wiring: %s (automatic heal steps: %d)", brief.Wiring.State, brief.HealAutomatic),
		fmt.Sprintf("Goal board: %d total, %d claimed, %d ready, %d blocked, %d implemented",
			audit.Total, claimed, audit.ReadyCount, audit.BlockedCount, implemented),
		"Recommended next: " + next,
	}
	return StageReceipt{
		Summary:    fmt.Sprintf("%s is %s; the goal board has %d goals", ProjectName, brief.Wiring.State, audit.Total),
		Highlights: highlights,
		Metrics: map[string]any{
			"wiring_status":  brief.Wiring.State,
			"heal_automatic": brief.HealAutomatic,
			"goals_total":    audit.Total,
			"claimed":        claimed,
			"ready":          audit.ReadyCount,
			"blocked":        audit.BlockedCount,
			"implemented":    implemented,
			"next_command":   next,
		},
	}, nil
}

func runPrimeStage(st *store.Store, dir string) (StageReceipt, error) {
	sum, err := dream.BuildPrimeBriefWithOptions(st, dream.PrimeOptions{
		ProjectSel:         ProjectName,
		ProjectDir:         dir,
		MaxFacts:           5,
		Budget:             1200,
		MaxTokens:          1200,
		IncludeMarkdown:    false,
		DisableCodebaseMap: true,
	})
	if err != nil {
		return StageReceipt{}, err
	}
	// The briefing is deliberately trimmed to 1200 tokens, so count the
	// available corpus from the store instead of implying the pack carried it.
	vision, _ := st.VisionFacts(ProjectName)
	lessons, _ := st.LessonFacts(ProjectName, 50)
	cases, _ := st.CaseFacts(ProjectName, 50)

	handoff := "no active handoff"
	if sum.ActiveHandoff != nil {
		handoff = fmt.Sprintf("Handoff from @%s: %s", sum.ActiveHandoff.Agent, sum.ActiveHandoff.Summary)
	}
	fits := true
	tokens := 0
	maxTokens := 1200
	if sum.BudgetMetrics != nil {
		fits = sum.BudgetMetrics.FitsBudget
		tokens = sum.BudgetMetrics.SerializedTotalTokens
		maxTokens = sum.BudgetMetrics.MaxTokens
	}
	return StageReceipt{
		Summary: fmt.Sprintf("Briefing built in %d tokens (budget %d, fits=%t)", tokens, maxTokens, fits),
		Highlights: []string{
			handoff,
			fmt.Sprintf("Corpus available: %d invariants/non-goals, %d lessons, %d cases",
				len(vision), len(lessons), len(cases)),
			fmt.Sprintf("Briefing kept %d fact(s), %d lesson(s), %d case(s) inside the budget",
				len(sum.TopFacts), len(sum.Lessons), len(sum.Cases)),
			"Every section is bounded; nothing is loaded without a budget check",
		},
		Metrics: map[string]any{
			"tokens":          tokens,
			"max_tokens":      maxTokens,
			"fits_budget":     fits,
			"facts":           len(sum.TopFacts),
			"lessons":         len(lessons),
			"cases":           len(cases),
			"vision":          len(vision),
			"briefed_lessons": len(sum.Lessons),
			"briefed_cases":   len(sum.Cases),
		},
	}, nil
}

func runRecallStage(st *store.Store, dir string) (StageReceipt, error) {
	res, err := dream.RecallSolutions(st, "3DS webhook 500 double write", dream.RecallOptions{
		Project:      ProjectName,
		ProjectDir:   dir,
		Limit:        5,
		BudgetTokens: 1200,
		Context: &dream.RecallContext{
			Task:   "apply idempotency key to refund webhook",
			Paths:  []string{"src/payments/checkout.go", "src/payments/refunds.go"},
			Branch: "feat/payment-idempotency",
		},
	})
	if err != nil {
		return StageReceipt{}, err
	}
	var topKind string
	var highlights []string
	for i, hit := range res.Hits {
		if i == 0 {
			topKind = hit.Kind
		}
		highlights = append(highlights, fmt.Sprintf("%s (tier %.0f): %s", hit.Kind, hit.Score, firstLine(hit.Resolution)))
		if len(highlights) == 3 {
			break
		}
	}
	highlights = append(highlights, "Recalled records are untrusted context; verify before acting")
	return StageReceipt{
		Summary:    fmt.Sprintf("%d hit(s) ranked; top kind is %s", res.Total, topKind),
		Highlights: highlights,
		Metrics: map[string]any{
			"total":           res.Total,
			"top_kind":        topKind,
			"lessons":         len(res.Lessons),
			"cases":           len(res.Cases),
			"budget_tokens":   res.BudgetTokens,
			"ranking_version": res.RankingVersion,
		},
	}, nil
}

func runFactsStage(st *store.Store, dir string) (StageReceipt, error) {
	chain, err := st.VerifyChainExplained(ProjectName)
	if err != nil {
		return StageReceipt{}, err
	}
	rec, err := st.ReconcileFactDrift(ProjectName, dir)
	if err != nil {
		return StageReceipt{}, err
	}
	facts, err := st.FactsByProject(ProjectName)
	if err != nil {
		return StageReceipt{}, err
	}
	var grounded, stale []string
	for _, f := range facts {
		if f.Kind != "code_claim" {
			continue
		}
		if f.FileRefsExist {
			for _, rf := range f.RepoFiles {
				grounded = append(grounded, rf)
			}
		} else {
			stale = append(stale, f.ID)
		}
	}
	highlights := []string{
		fmt.Sprintf("SHA-256 chain: %d blocks, intact=%t", chain.TotalBlocks, chain.Intact),
		"Grounded claims: " + strings.Join(uniqueStrings(grounded), ", "),
		fmt.Sprintf("Stale claims caught: %d (%s)", len(stale), strings.Join(stale, ", ")),
	}
	return StageReceipt{
		Summary:    fmt.Sprintf("Ledger intact=%t; %d file claim(s) grounded; %d stale", chain.Intact, len(uniqueStrings(grounded)), len(stale)),
		Highlights: highlights,
		Metrics: map[string]any{
			"chain_intact":    chain.Intact,
			"blocks":          chain.TotalBlocks,
			"grounded_claims": len(uniqueStrings(grounded)),
			"stale_claims":    len(stale),
			"newly_stale":     rec.NewlyStale,
		},
	}, nil
}

func runGateStage(sb *Sandbox) (StageReceipt, error) {
	// The failing check attributes the commit to a goal that has no grounded
	// memory write for the staged file. The passing check stages the payment
	// files the seeded lesson and case actually reference.
	ungrounded := sentry.CheckMemoryWrite(sentry.MemoryWriteOptions{
		RepoRoot: sb.Dir,
		Subject:  "feat(search): add retry loop [goal: search-filters]",
		Staged:   []string{"src/payments/untracked_retry.go"},
		DBPath:   sb.DBPath,
	})
	grounded := sentry.CheckMemoryWrite(sentry.MemoryWriteOptions{
		RepoRoot: sb.Dir,
		Subject:  "feat(payments): idempotent refund webhook [goal: payment-webhooks]",
		Staged:   []string{"src/payments/checkout.go", "src/payments/refunds.go"},
		DBPath:   sb.DBPath,
	})
	return StageReceipt{
		Summary: fmt.Sprintf("Ungrounded diff: %s (%s); grounded diff: %s",
			ungrounded.Status, ungrounded.Code, grounded.Status),
		Highlights: []string{
			"Ungrounded file fails closed with " + ungrounded.Code,
			fmt.Sprintf("Grounded files pass with lesson=%d case=%d memo=%d", grounded.Lesson, grounded.Case, grounded.Memo),
			"The gate blocks the commit, not the developer: the fix is one memory write",
		},
		Metrics: map[string]any{
			"ungrounded_status": ungrounded.Status,
			"ungrounded_code":   ungrounded.Code,
			"grounded_status":   grounded.Status,
			"lessons":           grounded.Lesson,
			"cases":             grounded.Case,
			"memos":             grounded.Memo,
		},
	}, nil
}

func runStandupStage(st *store.Store, dir string) (StageReceipt, error) {
	recap, err := dream.BuildRecap(st, ProjectName, "48h", dir)
	if err != nil {
		return StageReceipt{}, err
	}
	highlights := []string{
		fmt.Sprintf("Commits in window: %d", len(recap.Commits)),
		fmt.Sprintf("Decisions recorded: %d", len(recap.Decisions)),
		fmt.Sprintf("Open handoff items: %d", len(recap.OpenItems)),
	}
	if len(recap.NeedsApproval) > 0 {
		highlights = append(highlights, fmt.Sprintf("Needs human review: %s", strings.Join(recap.NeedsApproval, ", ")))
	}
	return StageReceipt{
		Summary:    fmt.Sprintf("%d commit(s), %d decision(s), %d open item(s)", len(recap.Commits), len(recap.Decisions), len(recap.OpenItems)),
		Highlights: highlights,
		Metrics: map[string]any{
			"commits":        len(recap.Commits),
			"decisions":      len(recap.Decisions),
			"open_items":     len(recap.OpenItems),
			"needs_approval": len(recap.NeedsApproval),
		},
	}, nil
}

// RenderWalkthroughHuman writes the 80-column terminal walkthrough to w.
func RenderWalkthroughHuman(w io.Writer, ui render.UI, rep *WalkthroughReport, width int) {
	if rep == nil {
		return
	}
	if width <= 0 || width > 80 {
		width = 80
	}
	write := func(s string) {
		fmt.Fprintln(w, fitLine(s, width))
	}

	write(ui.Bold(ui.Cyan("sila · demo walkthrough")) + "  " + ui.Dim(rep.ProjectID))
	write("")
	for _, st := range rep.Stages {
		write(fmt.Sprintf("%d/7 %-8s %s", st.Index, st.ID, st.Title))
		write("    " + st.Summary)
		for _, h := range st.Highlights {
			write("    - " + h)
		}
		write("    $ " + st.Command)
		write("")
	}

	write("Summary")
	write("  #  stage    command                       status")
	for _, st := range rep.Stages {
		write(fmt.Sprintf("  %d  %-8s %-29s %s", st.Index, st.ID, fitLine(st.Command, 29), st.Status))
	}
	write("")

	if !rep.CleanedUp {
		write("Sandbox kept for live exploration:")
		write("  dir: " + rep.SandboxDir)
		write("  db:  " + rep.StorePath)
		write(fmt.Sprintf("  sila prime --db=%s --project=%s", rep.StorePath, rep.ProjectID))
		write(fmt.Sprintf("  sila recall \"3DS webhook\" --db=%s --project=%s", rep.StorePath, rep.ProjectID))
		write(fmt.Sprintf("  sila facts verify %s --db=%s", rep.ProjectID, rep.StorePath))
		write("  rm -rf " + rep.SandboxDir)
		write("")
	}
	write(rep.NextAction)
}

// RenderWalkthroughTOON formats the walkthrough report as a TOON table.
func RenderWalkthroughTOON(rep *WalkthroughReport) string {
	if rep == nil {
		return ""
	}
	rows := make([][]string, 0, len(rep.Stages))
	for _, st := range rep.Stages {
		rows = append(rows, []string{
			fmt.Sprintf("%d", st.Index),
			st.ID,
			st.Command,
			st.Status,
			st.Summary,
		})
	}
	return render.FormatTOONTable("demo_stages", []string{"index", "id", "command", "status", "summary"}, rows)
}

// fitLine truncates plain text to width visible columns.
func fitLine(s string, width int) string {
	if width <= 0 {
		width = 80
	}
	if render.VisibleLen(s) <= width {
		return s
	}
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	if width <= 3 {
		return string(r[:width])
	}
	return string(r[:width-3]) + "..."
}

// firstLine returns the first non-empty line of s, trimmed.
func firstLine(s string) string {
	for line := range strings.SplitSeq(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

// uniqueStrings returns items with duplicates removed, preserving order.
func uniqueStrings(items []string) []string {
	seen := make(map[string]bool, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}
