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
	Context            *RecallContext       `json:"context,omitempty"`
	IncludeArchived    bool                 `json:"include_archived,omitempty"`
}

// RecallContext scopes ranking to the task at hand. Paths are
// repository-relative and symbols are bare names or canonical ids.
type RecallContext struct {
	Task    string   `json:"task,omitempty"`
	Paths   []string `json:"paths,omitempty"`
	Branch  string   `json:"branch,omitempty"`
	Head    string   `json:"head,omitempty"`
	Symbols []string `json:"symbols,omitempty"`
}

// RecallContextSummary is the privacy-safe projection of RecallContext that
// output may carry. It reports counts instead of the task text or path list.
type RecallContextSummary struct {
	TaskPresent     bool             `json:"task_present"`
	PathsCount      int              `json:"paths_count"`
	Branch          string           `json:"branch,omitempty"`
	Head            string           `json:"head,omitempty"`
	SymbolsCount    int              `json:"symbols_count,omitempty"`
	PathDiagnostics []PathDiagnostic `json:"path_diagnostics,omitempty"`
}

// PathDiagnostic names one rejected context path by position. It never carries
// the rejected text, so a diagnostic cannot disclose filesystem data.
type PathDiagnostic struct {
	Index  int    `json:"index"`
	Code   string `json:"code"`
	Reason string `json:"reason,omitempty"`
}

// RecallPackItem is one bounded, ranked entry in a recall pack.
type RecallPackItem struct {
	Kind       string  `json:"kind"`
	ID         string  `json:"id"`
	MemoryID   string  `json:"memory_id,omitempty"`
	ProjectID  string  `json:"project_id,omitempty"`
	Resolution string  `json:"resolution"`
	Score      float64 `json:"score"`
	Grounded   bool    `json:"grounded"`
	SourceRef  string  `json:"source_ref,omitempty"`
}

// RecallPack is a task-scoped, token-bounded selection of ranked memories.
// An empty pack is a successful result; EmptyReason explains why it is empty.
// RetrievalID identifies the recorded item set that feedback binds to.
type RecallPack struct {
	Items          []RecallPackItem      `json:"items"`
	Tokens         int                   `json:"tokens"`
	Omitted        int                   `json:"omitted"`
	EmptyReason    string                `json:"empty_reason,omitempty"`
	RankingVersion string                `json:"ranking_version"`
	RetrievalID    string                `json:"retrieval_id,omitempty"`
	Context        *RecallContextSummary `json:"context,omitempty"`
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
	RecallPack          *RecallPack                 `json:"recall_pack,omitempty"`
	StateBlock          string                      `json:"state_block,omitempty"`
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
