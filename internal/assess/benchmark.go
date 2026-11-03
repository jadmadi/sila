package assess

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/store"
)

// ToolScore is one tool's computed benchmark score.
type ToolScore struct {
	Tool               string  `json:"tool"`
	Projects           int     `json:"projects"`
	Messages           int     `json:"messages"`
	AssistantTurns     int     `json:"assistant_turns"`
	UserTurns          int     `json:"user_turns"`
	TotalClaims        int     `json:"total_claims"`
	GroundedClaims     int     `json:"grounded_claims"`
	UngroundedClaims   int     `json:"ungrounded_claims"`
	GroundingRate      float64 `json:"grounding_rate"`        // 0..1 (grounded / total claims)
	HallucinationRate  float64 `json:"hallucination_rate"`    // 0..1 (ungrounded / total claims)
	AvgTurnsPerSession float64 `json:"avg_turns_per_session"` // messages / projects (proxy)
	GoalsAttributed    int     `json:"goals_attributed"`
	HealthBadge        string  `json:"health_badge"` // 🟢 / 🟡 / 🔴 / ⚪
}

// Scorecard is the full benchmark report.
type Scorecard struct {
	Project    string      `json:"project,omitempty"`
	TotalTools int         `json:"total_tools"`
	Tools      []ToolScore `json:"tools"`
}

// ComputeBenchmark aggregates per-tool metrics from the store and the goals
// directory. When goalsRoot is non-empty, goal commits are attributed to the
// agent that claimed the goal (via Claim.Agent, normalized to the tool name).
func ComputeBenchmark(st *store.Store, projectSel, goalsRoot string) (*Scorecard, error) {
	rows, err := st.BenchmarkRows(projectSel)
	if err != nil {
		return nil, fmt.Errorf("benchmark rows: %w", err)
	}

	// Goal attribution: map agent name → count of implemented goals.
	goalAttribution := map[string]int{}
	if goalsRoot != "" {
		if audit, err := goals.ScanGoals(goalsRoot); err == nil && audit != nil {
			for _, g := range audit.Goals {
				if g.Status != "implemented" {
					continue
				}
				agent := ""
				if g.Claim != nil {
					agent = g.Claim.Agent
				}
				if agent == "" {
					continue
				}
				goalAttribution[normalizeToolName(agent)]++
			}
		}
	}

	var tools []ToolScore
	for _, r := range rows {
		s := ToolScore{
			Tool:             r.Tool,
			Projects:         r.Projects,
			Messages:         r.Messages,
			AssistantTurns:   r.AssistantTurns,
			UserTurns:        r.UserTurns,
			TotalClaims:      r.TotalClaims,
			GroundedClaims:   r.GroundedClaims,
			UngroundedClaims: r.UngroundedClaims,
			GoalsAttributed:  goalAttribution[normalizeToolName(r.Tool)],
		}
		if r.TotalClaims > 0 {
			s.GroundingRate = float64(r.GroundedClaims) / float64(r.TotalClaims)
			s.HallucinationRate = float64(r.UngroundedClaims) / float64(r.TotalClaims)
		} else if r.Messages > 0 {
			// No claims extracted — neutral, not penalized.
			s.GroundingRate = 1.0
		}
		if r.Projects > 0 {
			s.AvgTurnsPerSession = float64(r.Messages) / float64(r.Projects)
		}
		s.HealthBadge = healthBadge(s.GroundingRate, r.TotalClaims)
		tools = append(tools, s)
	}

	return &Scorecard{
		Project:    projectSel,
		TotalTools: len(tools),
		Tools:      tools,
	}, nil
}

// normalizeToolName lowercases and trims a tool/agent name for matching.
func normalizeToolName(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// healthBadge returns a color-coded badge based on grounding rate thresholds.
// 🟢 >95%, 🟡 85-95%, 🔴 <85%, ⚪ no claims to evaluate.
func healthBadge(groundingRate float64, totalClaims int) string {
	if totalClaims == 0 {
		return "⚪"
	}
	if groundingRate > 0.95 {
		return "🟢"
	}
	if groundingRate >= 0.85 {
		return "🟡"
	}
	return "🔴"
}

// RenderScorecardTable formats the scorecard as an ASCII table with
// color-coded health badges.
func RenderScorecardTable(sc *Scorecard) string {
	if sc == nil || len(sc.Tools) == 0 {
		return "_No tools with recorded activity._\n"
	}
	var b strings.Builder
	scope := "all projects"
	if sc.Project != "" {
		scope = sc.Project
	}
	fmt.Fprintf(&b, "sila · benchmark — %s (%d tools)\n\n", scope, sc.TotalTools)
	headers := []string{"tool", "msgs", "claims", "grounded", "rate", "halluc", "goals", "health"}
	rows := make([][]string, len(sc.Tools))
	for i, t := range sc.Tools {
		rows[i] = []string{
			t.Tool,
			fmt.Sprintf("%d", t.Messages),
			fmt.Sprintf("%d", t.TotalClaims),
			fmt.Sprintf("%d", t.GroundedClaims),
			fmt.Sprintf("%.0f%%", t.GroundingRate*100),
			fmt.Sprintf("%.0f%%", t.HallucinationRate*100),
			fmt.Sprintf("%d", t.GoalsAttributed),
			t.HealthBadge,
		}
	}
	// Simple ASCII table.
	colWidths := make([]int, len(headers))
	for i, h := range headers {
		colWidths[i] = len(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if len(cell) > colWidths[i] {
				colWidths[i] = len(cell)
			}
		}
	}
	// Header row.
	for i, h := range headers {
		if i > 0 {
			b.WriteString("  ")
		}
		fmt.Fprintf(&b, "%-*s", colWidths[i], h)
	}
	b.WriteString("\n")
	for i := range headers {
		if i > 0 {
			b.WriteString("  ")
		}
		b.WriteString(strings.Repeat("-", colWidths[i]))
	}
	b.WriteString("\n")
	for _, row := range rows {
		for i, cell := range row {
			if i > 0 {
				b.WriteString("  ")
			}
			fmt.Fprintf(&b, "%-*s", colWidths[i], cell)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// RenderScorecardTOON formats the scorecard in Token-Oriented Object Notation.
func RenderScorecardTOON(sc *Scorecard) string {
	if sc == nil || len(sc.Tools) == 0 {
		return "benchmark[0]{tool,msgs,claims,grounded,rate,goals,health}:\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "benchmark[%d]{tool,msgs,claims,grounded,rate,goals,health}:\n", len(sc.Tools))
	for _, t := range sc.Tools {
		fmt.Fprintf(&b, "  %s,%d,%d,%d,%.0f%%,%d,%s\n",
			t.Tool, t.Messages, t.TotalClaims, t.GroundedClaims,
			t.GroundingRate*100, t.GoalsAttributed, t.HealthBadge)
	}
	return b.String()
}

// SortToolsByGrounding sorts the scorecard's tools by grounding rate descending,
// then by total claims descending. Returns a new slice (does not mutate).
func SortToolsByGrounding(tools []ToolScore) []ToolScore {
	out := make([]ToolScore, len(tools))
	copy(out, tools)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].GroundingRate != out[j].GroundingRate {
			return out[i].GroundingRate > out[j].GroundingRate
		}
		return out[i].TotalClaims > out[j].TotalClaims
	})
	return out
}
