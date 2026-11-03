package cockpit

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/render"
)

// CockpitSnapshot captures the unified state across the Sila agent substrate at a single point in time.
type CockpitSnapshot struct {
	Header      HeaderInfo     `json:"header"`
	Fleet       FleetBox       `json:"fleet"`
	Approvals   ApprovalBox    `json:"approvals"`
	Stream      StreamBox      `json:"stream"`
	Concurrency ConcurrencyBox `json:"concurrency"`
	Gates       GateBox        `json:"gates"`
	Roadmap     RoadmapBox     `json:"roadmap"`
	Context     ContextBox     `json:"context"`
	Ledger      LedgerBox      `json:"ledger"`
	Diff        DiffBox        `json:"diff"`
	Worktrees   WorktreeBox    `json:"worktrees"`
}

// HeaderInfo captures top-level substrate pulse and situational summary.
type HeaderInfo struct {
	Pulse                 string    `json:"pulse"` // e.g. "1s pulse"
	ActiveAgentsCount     int       `json:"active_agents_count"`
	PendingApprovalsCount int       `json:"pending_approvals_count"`
	FleetHealth           string    `json:"fleet_health"` // e.g. "100% GREEN", "WARNING"
	Workspace             string    `json:"workspace"`
	Project               string    `json:"project,omitempty"`
	SubstrateRepos        int       `json:"substrate_repos"`
	Version               string    `json:"version"`
	Timestamp             time.Time `json:"timestamp"`
	LedgerSummary         string    `json:"ledger_summary,omitempty"`
	LedgerIntact          bool      `json:"ledger_intact"`
}

// AgentItem represents an active autonomous agent tracked across harnesses.
type AgentItem struct {
	Agent      string `json:"agent"`             // e.g. "@devin", "@agy", "@opencode"
	Harness    string `json:"harness"`           // e.g. "Devin", "Antigravity", "OpenCode", "Claude Code"
	Project    string `json:"project,omitempty"` // Project ID (e.g. "waqf-manara", "sila")
	GoalSlug   string `json:"goal_slug"`         // In-flight claimed goal
	ActiveTool string `json:"active_tool"`       // In-flight tool call / action
	Status     string `json:"status"`            // "EXECUTING", "WAITING_APPROVAL", "PAUSED", "IDLE"
	Elapsed    string `json:"elapsed"`           // e.g. "4m 12s"
	Turn       int    `json:"turn"`              // e.g. 18
}

// FleetBox contains Box 1 telemetry: live fleet roster across agent harnesses.
type FleetBox struct {
	Agents     []AgentItem `json:"agents"`
	Summary    string      `json:"summary"` // e.g. "2 executing, 1 paused, 0 crashed"
	TotalCount int         `json:"total_count"`
}

// PendingApprovalItem represents a high-risk agent action paused waiting for human decision.
type PendingApprovalItem struct {
	ID               string    `json:"id"`         // e.g. "app_8f21"
	Agent            string    `json:"agent"`      // Requesting agent
	Action           string    `json:"action"`     // Proposed command or mutation
	Reason           string    `json:"reason"`     // Rationale
	RiskLevel        string    `json:"risk_level"` // "low", "medium", "high", "critical"
	CreatedAt        time.Time `json:"created_at"`
	TimeoutSeconds   int       `json:"timeout_seconds"`
	RemainingSeconds int       `json:"remaining_seconds"`
	ProgressRatio    float64   `json:"progress_ratio"` // 0.0 to 1.0 (elapsed/timeout)
}

// ApprovalBox contains Box 2 telemetry: human-in-the-loop steering desk.
type ApprovalBox struct {
	HasPending bool                  `json:"has_pending"`
	Pending    []PendingApprovalItem `json:"pending"`
	Summary    string                `json:"summary"`
}

// EpistemicItem is a recently distilled knowledge item (fact, lesson, invariant, adr).
type EpistemicItem struct {
	Time    string `json:"time"`    // "15:04:05"
	Kind    string `json:"kind"`    // "lesson", "invariant", "fact", "adr"
	Agent   string `json:"agent"`   // Recording agent / harness
	Content string `json:"content"` // Fact or lesson text
}

// StreamBox contains Box 3 telemetry: live epistemic stream and knowledge velocity.
type StreamBox struct {
	Items             []EpistemicItem `json:"items"`
	KnowledgeRate     string          `json:"knowledge_rate"`     // e.g. "4 facts/min"
	VelocitySparkline string          `json:"velocity_sparkline"` // e.g. " ▂▃▅▇█▅"
	VelocityHistory   []int           `json:"velocity_history"`   // Fact counts across 10m intervals
}

// AgentTouchItem documents files currently touched or claimed by an agent.
type AgentTouchItem struct {
	Agent   string   `json:"agent"`
	Project string   `json:"project,omitempty"`
	Files   []string `json:"files"`
}

// ConcurrencyBox contains Box 4 telemetry: hot paths and collision sentry.
type ConcurrencyBox struct {
	Touches        []AgentTouchItem     `json:"touches"`
	CollisionAlert bool                 `json:"collision_alert"`
	Overlaps       []goals.FileConflict `json:"overlaps,omitempty"`
	Summary        string               `json:"summary"`
}

// GateCheckItem is a single automated verification sentry check.
type GateCheckItem struct {
	Name    string `json:"name"`    // "Tests (-race)", "Code Format", "Doc-Coverage", "Scar Tissue"
	Status  string `json:"status"`  // "PASS", "FAIL", "WARN", "SKIP"
	Details string `json:"details"` // e.g. "31 packages green in 1.82s"
}

// GateBox contains Box 5 telemetry: background pre-commit and race test sentries.
type GateBox struct {
	Checks   []GateCheckItem `json:"checks"`
	AllClean bool            `json:"all_clean"`
	Summary  string          `json:"summary"`
}

// InFlightGoalItem is an active development milestone on the project roadmap.
type InFlightGoalItem struct {
	Slug        string `json:"slug"`
	Project     string `json:"project,omitempty"`
	Title       string `json:"title"`
	TasksMet    int    `json:"tasks_met"`
	TasksTotal  int    `json:"tasks_total"`
	ProgressPct int    `json:"progress_pct"`
	ProgressBar string `json:"progress_bar"` // e.g. "[████████░░]"
	Status      string `json:"status"`       // "READY TO COMMIT", "In Progress", "Blocked"
	LaunchCmd   string `json:"launch_cmd"`
}

// RoadmapBox contains Box 6 telemetry: goal roadmap DAG and progress meters.
type RoadmapBox struct {
	InFlight       []InFlightGoalItem `json:"in_flight"`
	NextUnblocked  string             `json:"next_unblocked"`
	ClosedVelocity string             `json:"closed_velocity"`
	TotalGoals     int                `json:"total_goals"`
	ShippedGoals   int                `json:"shipped_goals"`
}

// AgentContextItem records estimated token footprint and context window health.
type AgentContextItem struct {
	Agent                  string `json:"agent"`
	ContextTokens          int    `json:"context_tokens"`
	ContextTokensFormatted string `json:"context_tokens_formatted"` // e.g. "42.1K" or "unknown"
	PromptTokensPerTurn    int    `json:"prompt_tokens_per_turn"`
	PromptTokensFormatted  string `json:"prompt_tokens_formatted"` // e.g. "1.2K" or "unknown"
	LimitStatus            string `json:"limit_status"`            // "within limits", "approaching threshold", "unknown"
	EstimationMethod       string `json:"estimation_method,omitempty"`
}

// ContextBox contains Box 7 telemetry: turn token load and prime compression health.
type ContextBox struct {
	Agents       []AgentContextItem `json:"agents"`
	PrimeSummary string             `json:"prime_summary"` // e.g. "unknown (no compression savings measured)"
	SavingsRatio float64            `json:"savings_ratio"`
}

// LedgerBox contains Box 8 telemetry: cryptographic SHA-256 fact ledger integrity.
type LedgerBox struct {
	TotalBlocks           int    `json:"total_blocks"`
	VerifiedBlocks        int    `json:"verified_blocks"`
	TipHash               string `json:"tip_hash"`
	TipHashShort          string `json:"tip_hash_short"`
	Intact                bool   `json:"intact"`
	BrokenLinks           int    `json:"broken_links"`
	VerificationLatencyMs int64  `json:"verification_latency_ms"`
	SelfHealingAvailable  bool   `json:"self_healing_available"`
	HealthSummary         string `json:"health_summary"`
	Status                string `json:"status,omitempty"` // "intact", "tampered", "empty", "unavailable"
	Error                 string `json:"error,omitempty"`
}

// DiffItem records per-file or per-worktree git mutation metrics.
type DiffItem struct {
	File      string `json:"file"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Status    string `json:"status"` // "M" (modified), "A" (added), "D" (deleted), "R" (renamed), "?" (untracked)
	Worktree  string `json:"worktree,omitempty"`
	Agent     string `json:"agent,omitempty"`
}

// DiffBox contains Box 8 telemetry: real-time git diff and mutation velocity.
type DiffBox struct {
	Items          []DiffItem `json:"items"`
	TotalFiles     int        `json:"total_files"`
	TotalAdditions int        `json:"total_additions"`
	TotalDeletions int        `json:"total_deletions"`
	VelocityRate   string     `json:"velocity_rate"` // e.g. "+142/-18 (5 files)"
	ChurnSummary   string     `json:"churn_summary"` // e.g. "3 modified, 2 added, 0 deleted across 1 worktree"
}

// WorktreeItem is an isolated git worktree created for an agent sandbox.
type WorktreeItem struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Branch    string `json:"branch"`
	Agent     string `json:"agent"`
	IsPrimary bool   `json:"is_primary"`
}

// WorktreeBox contains Box 9 telemetry: disposable git worktree sandbox pool.
type WorktreeBox struct {
	ActiveWorktrees []WorktreeItem `json:"active_worktrees"`
	ActiveCount     int            `json:"active_count"`
	TotalCapacity   int            `json:"total_capacity"`
	AutoPruneMerged bool           `json:"auto_prune_merged"`
}

// ToJSON serializes the snapshot into indented JSON.
func (s *CockpitSnapshot) ToJSON() (string, error) {
	bytes, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

// ToTOON formats the complete 9-box snapshot into Token-Oriented Object Notation (~40% tokens saved).
func (s *CockpitSnapshot) ToTOON() string {
	var b strings.Builder

	// Top summary object
	b.WriteString(render.FormatTOONObject("cockpit_summary", [][2]string{
		{"pulse", s.Header.Pulse},
		{"active_agents", fmt.Sprintf("%d", s.Header.ActiveAgentsCount)},
		{"pending_approvals", fmt.Sprintf("%d", s.Header.PendingApprovalsCount)},
		{"fleet_health", s.Header.FleetHealth},
		{"workspace", s.Header.Workspace},
		{"repos", fmt.Sprintf("%d", s.Header.SubstrateRepos)},
		{"ledger", fmt.Sprintf("intact=%t blocks=%d", s.Ledger.Intact, s.Ledger.VerifiedBlocks)},
		{"version", s.Header.Version},
		{"timestamp", s.Header.Timestamp.UTC().Format(time.RFC3339)},
	}))

	// Box 1: Fleet table
	var fleetRows [][]string
	for _, a := range s.Fleet.Agents {
		goalStr := a.GoalSlug
		if a.Project != "" {
			goalStr = fmt.Sprintf("[%s] %s", a.Project, a.GoalSlug)
		}
		fleetRows = append(fleetRows, []string{
			a.Agent, a.Harness, goalStr, a.ActiveTool, a.Status, a.Elapsed, fmt.Sprintf("%d", a.Turn),
		})
	}
	b.WriteString(render.FormatTOONTable("fleet", []string{"agent", "harness", "goal", "tool", "status", "elapsed", "turn"}, fleetRows))

	// Box 2: Approvals table
	if s.Approvals.HasPending {
		var appRows [][]string
		for _, a := range s.Approvals.Pending {
			appRows = append(appRows, []string{
				a.ID, a.Agent, a.Action, a.Reason, a.RiskLevel, fmt.Sprintf("%ds", a.RemainingSeconds),
			})
		}
		b.WriteString(render.FormatTOONTable("approvals", []string{"id", "agent", "action", "reason", "risk", "remaining"}, appRows))
	}

	// Box 3: Epistemic stream table
	var streamRows [][]string
	for _, it := range s.Stream.Items {
		streamRows = append(streamRows, []string{
			it.Time, it.Kind, it.Agent, it.Content,
		})
	}
	b.WriteString(render.FormatTOONTable("knowledge_stream", []string{"time", "kind", "agent", "content"}, streamRows))

	// Box 4: Concurrency table
	var concRows [][]string
	for _, t := range s.Concurrency.Touches {
		agentStr := t.Agent
		if t.Project != "" {
			agentStr = fmt.Sprintf("[%s] %s", t.Project, t.Agent)
		}
		concRows = append(concRows, []string{
			agentStr, strings.Join(t.Files, " | "),
		})
	}
	b.WriteString(render.FormatTOONTable("concurrency", []string{"agent", "touched_files"}, concRows))

	// Box 5: Gates table
	var gateRows [][]string
	for _, g := range s.Gates.Checks {
		gateRows = append(gateRows, []string{
			g.Name, g.Status, g.Details,
		})
	}
	b.WriteString(render.FormatTOONTable("gates", []string{"check", "status", "details"}, gateRows))

	// Box 6: Roadmap table
	var roadRows [][]string
	for _, r := range s.Roadmap.InFlight {
		slugStr := r.Slug
		if r.Project != "" {
			slugStr = fmt.Sprintf("[%s] %s", r.Project, r.Slug)
		}
		roadRows = append(roadRows, []string{
			slugStr, fmt.Sprintf("%d/%d", r.TasksMet, r.TasksTotal), fmt.Sprintf("%d%%", r.ProgressPct), r.Status,
		})
	}
	b.WriteString(render.FormatTOONTable("roadmap", []string{"slug", "tasks", "progress", "status"}, roadRows))

	// Box 7: Context table
	var ctxRows [][]string
	for _, c := range s.Context.Agents {
		ctxRows = append(ctxRows, []string{
			c.Agent, c.ContextTokensFormatted, c.PromptTokensFormatted, c.LimitStatus,
		})
	}
	b.WriteString(render.FormatTOONTable("context_pulse", []string{"agent", "context", "prompt_turn", "status"}, ctxRows))

	// Box 8: Real-Time Diff velocity table
	var diffRows [][]string
	for _, d := range s.Diff.Items {
		diffRows = append(diffRows, []string{
			d.File, d.Status, fmt.Sprintf("+%d", d.Additions), fmt.Sprintf("-%d", d.Deletions), d.Worktree,
		})
	}
	b.WriteString(render.FormatTOONTable("diff_velocity", []string{"file", "status", "additions", "deletions", "worktree"}, diffRows))

	// Box 9: Worktrees table
	var wtRows [][]string
	for _, w := range s.Worktrees.ActiveWorktrees {
		wtRows = append(wtRows, []string{
			w.Name, w.Path, w.Branch, w.Agent,
		})
	}
	b.WriteString(render.FormatTOONTable("worktrees", []string{"name", "path", "branch", "agent"}, wtRows))

	return b.String()
}
