package mcp

import (
	"fmt"
	"strings"

	"github.com/jadmadi/sila/internal/dream"
	"github.com/jadmadi/sila/internal/store"
)

// CachePinningLayer represents a prompt segmentation layer for KV cache optimization.
type CachePinningLayer int

const (
	LayerStaticSystem CachePinningLayer = iota
	LayerStaticTools
	LayerStaticRules
	LayerStaticKnowledge
	LayerDynamicGoals
	LayerDynamicHandoff
	LayerDynamicUser
)

// CacheBoundaryMarker is the standardized Anthropic/OpenAI prompt cache segregation delimiter.
const CacheBoundaryMarker = "<!-- cache-pinning: dynamic-boundary -->"

// CacheAwarePrompt encapsulates an LLM prompt structured to maximize prompt cache hits
// following the Gobstopper pattern: static context pinned to the top, dynamic variables at the bottom.
type CacheAwarePrompt struct {
	StaticPrefix    string  `json:"static_prefix"`
	DynamicSuffix   string  `json:"dynamic_suffix"`
	FullPrompt      string  `json:"full_prompt"`
	StaticTokens    int     `json:"static_tokens"`
	DynamicTokens   int     `json:"dynamic_tokens"`
	TotalTokens     int     `json:"total_tokens"`
	EstimatedHitPct float64 `json:"estimated_hit_pct"`
}

// CachePromptOptions configures prompt assembly for cache pinning.
type CachePromptOptions struct {
	ProjectID          string
	SystemContract     string
	IncludeTools       bool
	CustomStaticRules  string
	RepoRunbook        string
	Facts              []store.Fact
	ActiveGoalTitle    string
	ActiveGoalSlug     string
	ActiveHandoff      *store.Handoff
	UserPrompt         string
	InsertCacheMarkers bool
}

// PromptArgument specifies an argument for an MCP prompt template.
type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// PromptDefinition represents an MCP prompt advertised via prompts/list.
type PromptDefinition struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

// PromptMessage represents a message in a prompts/get response.
type PromptMessage struct {
	Role    string        `json:"role"`
	Content PromptContent `json:"content"`
}

// PromptContent represents text content in a prompt message.
type PromptContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// GetPromptResult represents the payload returned by prompts/get.
type GetPromptResult struct {
	Description string          `json:"description"`
	Messages    []PromptMessage `json:"messages"`
}

// PromptDefinitions returns canonical MCP prompt templates.
func PromptDefinitions() []PromptDefinition {
	return []PromptDefinition{
		{
			Name:        "cache_pinned_session",
			Description: "Gobstopper cache-aware session initialization prompt; pins static system instructions, tools, and architectural facts at the top to maximize prompt cache hits",
			Arguments: []PromptArgument{
				{Name: "project", Description: "Optional project selector or repository path", Required: false},
				{Name: "user_task", Description: "Optional initial task or objective for the session", Required: false},
			},
		},
		{
			Name:        "cache_pinned_goal",
			Description: "Gobstopper cache-aware goal execution prompt; pins static repository context and tools at the top with active goal contract at the bottom",
			Arguments: []PromptArgument{
				{Name: "slug", Description: "Goal slug to execute", Required: true},
				{Name: "project", Description: "Optional project selector", Required: false},
			},
		},
	}
}

// BuildCachePinnedPrompt assembles a segregated prompt payload where all static rules,
// tools, runbooks, and invariants are strictly placed in StaticPrefix, and all dynamic
// handoffs, goal states, and user variables are in DynamicSuffix.
func BuildCachePinnedPrompt(opts CachePromptOptions) *CacheAwarePrompt {
	var staticB strings.Builder
	var dynamicB strings.Builder

	// 1. LayerStaticSystem: System Context & Working Contract (Pinned Top)
	systemContract := opts.SystemContract
	if systemContract == "" {
		systemContract = DefaultWorkingContract(opts.ProjectID)
	}
	staticB.WriteString(strings.TrimSpace(systemContract))
	staticB.WriteString("\n\n")

	// 2. LayerStaticTools: MCP Tools Summary (Pinned Top)
	if opts.IncludeTools {
		staticB.WriteString("#### 🛠️ Available MCP Tools (Sila Core)\n")
		staticB.WriteString("- `search_knowledge`: Query verified architectural facts and claims\n")
		staticB.WriteString("- `prime_context`: Retrieve project memory briefing\n")
		staticB.WriteString("- `list_goals` / `claim_goal`: Epistemic roadmap goal management\n")
		staticB.WriteString("- `save_memo` / `save_lesson`: Persist architectural directives and solved lessons\n")
		staticB.WriteString("- `save_handoff` / `get_latest_handoff`: Baton-passing continuity across sessions\n\n")
	}

	// 3. LayerStaticRules: Static Runbooks & Workspace Invariants
	if opts.CustomStaticRules != "" {
		staticB.WriteString("#### 📜 Workspace Rules & Invariants\n")
		staticB.WriteString(strings.TrimSpace(opts.CustomStaticRules))
		staticB.WriteString("\n\n")
	}
	if opts.RepoRunbook != "" {
		staticB.WriteString("#### 📖 Repository Runbook (Static Guidance)\n")
		staticB.WriteString(strings.TrimSpace(opts.RepoRunbook))
		staticB.WriteString("\n\n")
	}

	// 4. LayerStaticKnowledge: Verified Architectural Facts & Decisions
	if len(opts.Facts) > 0 {
		var facts []store.Fact
		for _, f := range opts.Facts {
			if f.IsInvariant() || f.IsManualMemo() || f.IsFact() || f.FileRefsExist || f.Verified {
				facts = append(facts, f)
			}
		}
		if len(facts) > 0 {
			staticB.WriteString("#### 🏛️ Verified Architectural Facts & Decisions\n")
			for _, f := range facts {
				kind := f.Kind
				if kind == "" {
					kind = "fact"
				}
				filesPart := ""
				if len(f.RepoFiles) > 0 {
					filesPart = fmt.Sprintf(" *(refs: `%s`)*", strings.Join(f.RepoFiles, "`, `"))
				}
				staticB.WriteString(fmt.Sprintf("• [%s] %s%s\n", kind, f.Claim, filesPart))
			}
			staticB.WriteString("\n")
		}
	}

	// 5. LayerDynamicGoals: Active Goal Contract (Dynamic Variables)
	if opts.ActiveGoalSlug != "" || opts.ActiveGoalTitle != "" {
		dynamicB.WriteString("#### 🎯 Active Goal Execution Contract\n")
		if opts.ActiveGoalSlug != "" {
			dynamicB.WriteString(fmt.Sprintf("- **Slug**: `%s`\n", opts.ActiveGoalSlug))
		}
		if opts.ActiveGoalTitle != "" {
			dynamicB.WriteString(fmt.Sprintf("- **Title**: %s\n", opts.ActiveGoalTitle))
		}
		dynamicB.WriteString("\n")
	}

	// 6. LayerDynamicHandoff: Active Session Handoff & Open Tasks (Dynamic Variables)
	if opts.ActiveHandoff != nil {
		h := opts.ActiveHandoff
		dynamicB.WriteString("#### 📋 Active Session Handoff (Ephemeral Baton)\n")
		if h.Summary != "" {
			dynamicB.WriteString(fmt.Sprintf("- **Summary**: %s\n", h.Summary))
		}
		if len(h.OpenItems) > 0 {
			dynamicB.WriteString("- **Pending Tasks**:\n")
			for _, item := range h.OpenItems {
				item = strings.TrimSpace(item)
				if !strings.HasPrefix(item, "-") && !strings.HasPrefix(item, "*") && !strings.HasPrefix(item, "[") {
					item = "- [ ] " + item
				}
				dynamicB.WriteString(fmt.Sprintf("  %s\n", item))
			}
		}
		if len(h.Decisions) > 0 {
			dynamicB.WriteString("- **Recent Decisions**:\n")
			for _, d := range h.Decisions {
				dynamicB.WriteString(fmt.Sprintf("  • %s\n", d))
			}
		}
		dynamicB.WriteString("\n")
	}

	// 7. LayerDynamicUser: Immediate User Prompt / Task
	if opts.UserPrompt != "" {
		dynamicB.WriteString("#### 💬 User Objective & Request\n")
		dynamicB.WriteString(strings.TrimSpace(opts.UserPrompt))
		dynamicB.WriteString("\n")
	}

	staticPrefix := strings.TrimRight(staticB.String(), "\n")
	dynamicSuffix := strings.TrimRight(dynamicB.String(), "\n")

	staticTok := store.EstimateTokens(len(staticPrefix))
	dynamicTok := store.EstimateTokens(len(dynamicSuffix))
	totalTok := staticTok + dynamicTok

	var hitPct float64
	if totalTok > 0 {
		hitPct = (float64(staticTok) / float64(totalTok)) * 100.0
	}

	var fullPrompt strings.Builder
	fullPrompt.WriteString(staticPrefix)
	if opts.InsertCacheMarkers {
		fullPrompt.WriteString("\n\n")
		fullPrompt.WriteString(CacheBoundaryMarker)
	}
	if dynamicSuffix != "" {
		fullPrompt.WriteString("\n\n")
		fullPrompt.WriteString(dynamicSuffix)
	}

	return &CacheAwarePrompt{
		StaticPrefix:    staticPrefix,
		DynamicSuffix:   dynamicSuffix,
		FullPrompt:      fullPrompt.String(),
		StaticTokens:    staticTok,
		DynamicTokens:   dynamicTok,
		TotalTokens:     totalTok,
		EstimatedHitPct: hitPct,
	}
}

// DefaultWorkingContract returns the standard Sila 7-step working contract.
func DefaultWorkingContract(projectID string) string {
	title := projectID
	if title == "" {
		title = "global"
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("### 🧠 Project Memory (sila · project: %s)\n\n", title))
	b.WriteString("**Working Contract (7-Step Loop)**:\n")
	b.WriteString("1. **Heal & Prime**: `sila heal` (or MCP `reconcile_drift`) → `sila prime` (or MCP `prime_context`)\n")
	b.WriteString("2. **Pick Work**: `sila goals ready -p .` (or MCP `list_goals(pending_only=true)`) → `sila goals claim <slug>` (or MCP `claim_goal`)\n")
	b.WriteString("3. **When Stuck**: `sila recall \"<query>\"` (or MCP `recall`) before web search → after fix: `sila lesson` (or MCP `save_lesson`)\n")
	b.WriteString("4. **When Deciding**: `sila memo \"<directive>\"` (or MCP `save_memo`) immediately\n")
	b.WriteString("5. **When Done**: commit `feat: ... (goals/<slug>/goal.md)` → `sila goals` (or MCP `audit_goals`) → `sila handoff` (or MCP `save_handoff`)\n")
	b.WriteString("6. **Report Back**: goal slug, commit SHA, recorded lessons, and store enrichments\n")
	b.WriteString("7. **Friction / Ideas**: `sila feedback \"<what happened>\"` (or MCP `record_feedback`)\n")
	b.WriteString("_Note: If `sila` CLI is not in `$PATH` (subshell exit 127), invoke via `~/.local/bin/sila` or call Sila MCP tools directly._\n")
	return b.String()
}

// FormatCachePinnedPrimeMarkdown formats a PrimeSummary using cache-aware segregation:
// static rules, runbook, and architectural facts are pinned at the top,
// while active handoff, open tasks, and active goals are placed at the bottom.
func FormatCachePinnedPrimeMarkdown(p *dream.PrimeSummary) string {
	if p == nil {
		return ""
	}

	var runbookStr strings.Builder
	if p.Runbook != nil && len(p.Runbook.Entries) > 0 {
		for _, e := range p.Runbook.Entries {
			src := ""
			if e.Source != "" {
				src = fmt.Sprintf(" *(source: %s)*", e.Source)
			}
			runbookStr.WriteString(fmt.Sprintf("• **%s**: `%s`%s\n", e.Phase, e.Command, src))
		}
	}

	goalSlug := ""
	goalTitle := ""
	if p.ActiveGoal != nil {
		goalSlug = p.ActiveGoal.Slug
		goalTitle = p.ActiveGoal.Title
	}

	prompt := BuildCachePinnedPrompt(CachePromptOptions{
		ProjectID:          p.ProjectID,
		IncludeTools:       true,
		RepoRunbook:        strings.TrimSpace(runbookStr.String()),
		Facts:              p.TopFacts,
		ActiveGoalSlug:     goalSlug,
		ActiveGoalTitle:    goalTitle,
		ActiveHandoff:      p.ActiveHandoff,
		InsertCacheMarkers: true,
	})

	return prompt.FullPrompt
}
