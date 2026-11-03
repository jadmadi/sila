package assist

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/config"
	"github.com/jadmadi/sila/internal/store"
)

// ActiveProvider returns the configured assistance provider if assistance is explicitly enabled.
func ActiveProvider() Provider {
	return ActiveProviderFor("", "")
}

// ActiveProviderFor returns the configured assistance provider if assistance is explicitly enabled,
// the requested feature scope is consented, and the project is within the allowed project scope.
// Storing a key or selecting a provider alone causes zero uploads.
func ActiveProviderFor(scope, project string) Provider {
	cfg, err := config.Load("")
	if err != nil || cfg == nil {
		return nil
	}

	// Explicit enablement is strictly required. Storing a key or selecting a provider
	// alone causes zero uploads.
	if !cfg.AssistEnabled {
		return nil
	}

	// Feature scope consent is strictly required. Recap consent does not enable live hooks, gate, or doctor.
	if !cfg.HasAssistScope(scope) {
		return nil
	}

	// Project scope restriction.
	if !cfg.AllowsAssistProject(project) {
		return nil
	}

	providerName := strings.ToLower(strings.TrimSpace(cfg.AssistProvider))
	if providerName == "" {
		providerName = "typesafe"
	}

	switch providerName {
	case "typesafe", "jev":
		if cfg.TypeSafeAPIKey == "" {
			return nil
		}
		return NewTypeSafeAdapter(cfg.TypeSafeAPIKey, nil)
	case "mock":
		return NewMockAdapter()
	default:
		return nil
	}
}

// ActivityInsight represents one attributed semantic observation from recent activity.
type ActivityInsight struct {
	Category    string  `json:"category"` // "blocker", "architecture", "security"
	Status      string  `json:"status"`   // "unresolved", "confirmed_observation", "discussion", "attempted_action"
	Tool        string  `json:"tool"`
	ProjectID   string  `json:"project_id"`
	MessageID   any     `json:"message_id,omitempty"`
	Timestamp   string  `json:"timestamp,omitempty"`
	Summary     string  `json:"summary"`
	Confidence  float64 `json:"confidence"`
	Attribution string  `json:"attribution"`
}

// InterpretActivity generates high-level semantic insights across recent session messages with source attribution.
// Returns an empty slice on any provider failure or empty evidence, ensuring fail-open resilience.
func InterpretActivity(ctx context.Context, p Provider, msgs []store.StoredMessage) []string {
	insights := InterpretActivityStructured(ctx, p, msgs)
	if len(insights) == 0 {
		return nil
	}
	out := make([]string, 0, len(insights))
	for _, in := range insights {
		out = append(out, in.Attribution)
	}
	return out
}

// EvaluateLiveDelta evaluates recent session activity, returning formatted insights,
// a boolean indicating whether provider evaluation completed without error, and any error.
func EvaluateLiveDelta(ctx context.Context, p Provider, msgs []store.StoredMessage) ([]string, bool, error) {
	insights, evaluated, err := EvaluateLiveDeltaStructured(ctx, p, msgs)
	if err != nil || !evaluated {
		return nil, evaluated, err
	}
	out := make([]string, 0, len(insights))
	for _, in := range insights {
		out = append(out, in.Attribution)
	}
	return out, true, nil
}

// EvaluateLiveDeltaStructured evaluates recent session activity returning structured insights.
func EvaluateLiveDeltaStructured(ctx context.Context, p Provider, msgs []store.StoredMessage) ([]ActivityInsight, bool, error) {
	if p == nil || len(msgs) == 0 {
		return nil, false, nil
	}

	sample := msgs
	if len(sample) > 15 {
		sample = sample[:15]
	}

	evidence := make(map[string]any)
	for i, m := range sample {
		content := strings.TrimSpace(m.Content)
		if len([]rune(content)) > 250 {
			runes := []rune(content)
			content = string(runes[:250]) + "..."
		}
		toolStr := strings.Join(m.SourceTools, ",")
		if toolStr == "" {
			toolStr = "agent"
		}
		evidence[fmt.Sprintf("msg_%02d", i)] = map[string]any{
			"id":        m.ID,
			"project":   m.ProjectID,
			"tool":      toolStr,
			"role":      m.Role,
			"timestamp": m.TS,
			"content":   content,
		}
	}

	questions := map[string]Question{
		"has_blockers": {
			Type:         TypeNoul,
			Instructions: "Do these session messages discuss an unresolved blocker or critical failure?",
		},
		"has_architecture_change": {
			Type:         TypeNoul,
			Instructions: "Do these session messages describe an architectural change, invariant, or database migration?",
		},
		"has_security_issue": {
			Type:         TypeNoul,
			Instructions: "Do these session messages mention a credential, token leak, or security concern?",
		},
	}

	subCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()

	resp, err := p.Evaluate(subCtx, "", evidence, questions)
	if err != nil {
		return nil, false, err
	}

	insights := extractInsightsFromResponse(resp, sample)
	return insights, true, nil
}

// InterpretActivityStructured generates structured semantic insights across recent session messages.
func InterpretActivityStructured(ctx context.Context, p Provider, msgs []store.StoredMessage) []ActivityInsight {
	insights, _, _ := EvaluateLiveDeltaStructured(ctx, p, msgs)
	return insights
}

func extractInsightsFromResponse(resp *ProviderResponse, sample []store.StoredMessage) []ActivityInsight {
	var insights []ActivityInsight

	// 1. Blocker inspection
	if ans, ok := resp.Nouls["has_blockers"]; ok && ans.Noul >= 0.60 {
		supp := findSupportingMessage(sample, []string{"blocker", "deadlock", "failure", "crash", "outage", "locked error", "panic", "عطل", "انهيار", "توقف"}, "fixed", "resolved", "تم حل")
		tool := extractTool(supp)
		ts := extractTime(supp)
		summary := extractSnippet(supp, "Active blocker discussed in recent session")
		status := "unresolved"
		attr := formatAttributedInsight("Active Blocker", status, tool, ts, summary, ans.Noul)
		insights = append(insights, ActivityInsight{
			Category:    "blocker",
			Status:      status,
			Tool:        tool,
			ProjectID:   supp.ProjectID,
			MessageID:   supp.ID,
			Timestamp:   ts,
			Summary:     summary,
			Confidence:  ans.Noul,
			Attribution: attr,
		})
	}

	// 2. Architecture inspection
	if ans, ok := resp.Nouls["has_architecture_change"]; ok && ans.Noul >= 0.60 {
		supp := findSupportingMessage(sample, []string{"decision", "invariant", "wal mode", "pragma", "refactor", "schema", "architecture", "cgo", "قرار", "ثابت", "هيكلة", "معيار"})
		tool := extractTool(supp)
		ts := extractTime(supp)
		summary := extractSnippet(supp, "Architectural topic reviewed in recent session")
		status := classifyArchitectureStatus(supp.Content)
		attr := formatAttributedInsight("Architectural Topic", status, tool, ts, summary, ans.Noul)
		insights = append(insights, ActivityInsight{
			Category:    "architecture",
			Status:      status,
			Tool:        tool,
			ProjectID:   supp.ProjectID,
			MessageID:   supp.ID,
			Timestamp:   ts,
			Summary:     summary,
			Confidence:  ans.Noul,
			Attribution: attr,
		})
	}

	// 3. Security inspection
	if ans, ok := resp.Nouls["has_security_issue"]; ok && ans.Noul >= 0.60 {
		supp := findSupportingMessage(sample, []string{"token", "secret", "credential", "sk_live", "ghp_", "key", "مفتاح", "سري"})
		tool := extractTool(supp)
		ts := extractTime(supp)
		summary := extractSnippet(supp, "Security credential or access path reviewed")
		status := "reviewed"
		attr := formatAttributedInsight("Security Concern", status, tool, ts, summary, ans.Noul)
		insights = append(insights, ActivityInsight{
			Category:    "security",
			Status:      status,
			Tool:        tool,
			ProjectID:   supp.ProjectID,
			MessageID:   supp.ID,
			Timestamp:   ts,
			Summary:     summary,
			Confidence:  ans.Noul,
			Attribution: attr,
		})
	}

	return insights
}

func findSupportingMessage(msgs []store.StoredMessage, keywords []string, excludeKeywords ...string) store.StoredMessage {
	for _, m := range msgs {
		lower := strings.ToLower(m.Content)
		hasExclude := false
		for _, ex := range excludeKeywords {
			if strings.Contains(lower, ex) {
				hasExclude = true
				break
			}
		}
		if hasExclude {
			continue
		}
		for _, kw := range keywords {
			if strings.Contains(lower, kw) {
				return m
			}
		}
	}
	// Fallback without exclusions
	for _, m := range msgs {
		lower := strings.ToLower(m.Content)
		for _, kw := range keywords {
			if strings.Contains(lower, kw) {
				return m
			}
		}
	}
	if len(msgs) > 0 {
		return msgs[0]
	}
	return store.StoredMessage{}
}

func extractTool(m store.StoredMessage) string {
	if len(m.SourceTools) > 0 {
		var validTools []string
		for _, t := range m.SourceTools {
			if trimmed := strings.TrimSpace(t); trimmed != "" {
				validTools = append(validTools, trimmed)
			}
		}
		if len(validTools) > 0 {
			return strings.Join(validTools, ", ")
		}
	}
	if m.Role != "" {
		return m.Role
	}
	return "agent"
}

func extractTime(m store.StoredMessage) string {
	if m.TS != "" {
		return m.TS
	}
	if m.FirstSeen != "" {
		return m.FirstSeen
	}
	return ""
}

func extractSnippet(m store.StoredMessage, fallback string) string {
	content := strings.TrimSpace(m.Content)
	if content == "" {
		return fallback
	}
	// Take first line or up to 120 runes
	lines := strings.Split(content, "\n")
	firstLine := strings.TrimSpace(lines[0])
	runes := []rune(firstLine)
	if len(runes) > 120 {
		return string(runes[:120]) + "..."
	}
	return firstLine
}

func classifyArchitectureStatus(content string) string {
	lower := strings.ToLower(content)
	if strings.Contains(lower, "not implemented") || strings.Contains(lower, "unimplemented") ||
		strings.Contains(lower, "not yet") || strings.Contains(lower, "not resolved") ||
		strings.Contains(lower, "not fixed") || strings.Contains(lower, "لم يتم") ||
		strings.Contains(lower, "غير مفعل") || strings.Contains(lower, "غير منجز") {
		return "unresolved"
	}
	if strings.Contains(lower, "fixed") || strings.Contains(lower, "resolved") || strings.Contains(lower, "implemented") || strings.Contains(lower, "applied") || strings.Contains(lower, "تم حل") || strings.Contains(lower, "تم تفعيل") {
		return "confirmed observation"
	}
	if strings.Contains(lower, "attempt") || strings.Contains(lower, "trying") || strings.Contains(lower, "adding") || strings.Contains(lower, "updating") {
		return "attempted action"
	}
	if strings.Contains(lower, "propose") || strings.Contains(lower, "discuss") || strings.Contains(lower, "consider") || strings.Contains(lower, "proposal") {
		return "discussion"
	}
	return "discussion"
}

func formatAttributedInsight(topic, status, tool, ts, summary string, conf float64) string {
	toolStr := tool
	if toolStr == "" {
		toolStr = "agent"
	}
	timeStr := ""
	if ts != "" {
		if t, err := time.Parse(time.RFC3339, ts); err == nil {
			timeStr = " @ " + t.UTC().Format("01-02 15:04")
		} else if len(ts) >= 10 {
			timeStr = " @ " + ts[:10]
		}
	}
	return fmt.Sprintf("[%s%s] %s: %s (%s, confidence: %.0f%%)",
		toolStr, timeStr, topic, summary, status, conf*100)
}
