package dream

import (
	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/store"
	"github.com/jadmadi/sila/internal/symbols"
)

// EstimatorDocumentation defines the canonical token estimator formula used across Sila.
const EstimatorDocumentation = "chars/4 (store.EstimateTokens: (len+3)/4)"

// PrimeBudgetMetrics records measured token consumption across serialized surfaces.
type PrimeBudgetMetrics struct {
	Estimator              string `json:"estimator"`                  // Documented estimator formula
	MaxTokens              int    `json:"max_tokens"`                 // Configured budget limit
	ReservedMetadataTokens int    `json:"reserved_metadata_tokens"`   // Immutable envelope headers & routing
	ContentTokens          int    `json:"content_tokens"`             // Dynamic context (handoff, facts, goals, runbook)
	SerializedTotalTokens  int    `json:"serialized_total_tokens"`    // Total estimated tokens of the serialized payload
	AlwaysLoadedTokens     int    `json:"always_loaded_tokens"`       // Separate measure of static files on disk (CLAUDE.md, etc.)
	FitsBudget             bool   `json:"fits_budget"`                // Whether SerializedTotalTokens <= MaxTokens
	BudgetTooSmall         bool   `json:"budget_too_small,omitempty"` // True when MaxTokens cannot fit immutable envelope
	MinimumRequiredTokens  int    `json:"minimum_required_tokens,omitempty"`
}

// CompactGoalSummary provides a bounded representation of a completed roadmap goal.
type CompactGoalSummary struct {
	Slug   string `json:"slug"`
	Title  string `json:"title"`
	Commit string `json:"commit,omitempty"`
}

// PrimeOptions configures context priming, token budgeting, and representation selection.
type PrimeOptions struct {
	ProjectSel         string               `json:"project"`
	ProjectDir         string               `json:"project_dir"`
	MaxFacts           int                  `json:"max_facts"`
	Budget             int                  `json:"budget"`
	MaxTokens          int                  `json:"max_tokens"`
	IncludeMarkdown    bool                 `json:"include_markdown"`
	AllGoals           bool                 `json:"all_goals"`
	CachePinned        bool                 `json:"cache_pinned"`
	ForcedPressure     store.WindowPressure `json:"forced_pressure,omitempty"`
	DisableCodebaseMap bool                 `json:"disable_codebase_map,omitempty"`
	CodebaseMapChars   int                  `json:"codebase_map_chars,omitempty"`
}

// PrimeSummary contains the consolidated context briefing for an agent session start.
type PrimeSummary struct {
	Initialized         bool                        `json:"initialized"`
	ProjectID           string                      `json:"project_id"`
	ProjectDir          string                      `json:"project_dir,omitempty"`
	Git                 GitContext                  `json:"git"`
	ActiveHandoff       *store.Handoff              `json:"active_handoff,omitempty"`
	ActiveGoal          *goals.Goal                 `json:"active_goal,omitempty"`
	ActiveContract      *goals.VerificationContract `json:"active_contract,omitempty"`
	CompletedGoals      []goals.Goal                `json:"completed_goals,omitempty"`
	CompletedGoalsCount int                         `json:"completed_goals_count"`
	CompletedGoalsRoute string                      `json:"completed_goals_route,omitempty"`
	Runbook             *RunbookSummary             `json:"runbook,omitempty"`
	CodebaseMap         *symbols.CodebaseMap        `json:"codebase_map,omitempty"`
	TopFacts            []store.Fact                `json:"top_facts"`
	Lessons             []store.Fact                `json:"lessons,omitempty"`
	Cases               []store.Fact                `json:"cases,omitempty"`
	Candidates          []store.Fact                `json:"candidates,omitempty"`
	AttachedDocs        []store.Doc                 `json:"attached_docs,omitempty"`
	VisionNonGoals      []string                    `json:"vision_nongoals,omitempty"`
	TotalVerified       int                         `json:"total_verified"`
	TotalClaims         int                         `json:"total_claims"`
	ContextBudget       store.ContextBudget         `json:"context_budget"`
	Markdown            string                      `json:"markdown,omitempty"`
	BudgetMetrics       *PrimeBudgetMetrics         `json:"budget_metrics,omitempty"`
	Compression         *CompressionResult          `json:"compression,omitempty"`
	Shrink              *store.ShrinkResult         `json:"shrink,omitempty"`
	Error               string                      `json:"error,omitempty"`
}
