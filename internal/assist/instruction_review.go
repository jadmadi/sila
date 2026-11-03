package assist

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/agents"
	"github.com/jadmadi/sila/internal/store"
)

// InstructionAction classifies proposed rule updates.
type InstructionAction string

// InstructionAction constants categorize the nature of an instruction update proposal.
const (
	ActionAdd      InstructionAction = "add"
	ActionClarify  InstructionAction = "clarify"
	ActionNarrow   InstructionAction = "narrow"
	ActionRemove   InstructionAction = "remove"
	ActionNoChange InstructionAction = "no_change"
)

// Scope constants define the target scope of an instruction update.
const (
	ScopeProject = "project"
	ScopeGlobal  = "global"
)

// InstructionProposal is an attributable, human-reviewable instruction update suggestion.
type InstructionProposal struct {
	Action              InstructionAction `json:"action"`
	Scope               string            `json:"scope"`
	TargetFile          string            `json:"target_file"`
	TargetContentHash   string            `json:"target_content_hash"`
	FailureSummary      string            `json:"failure_summary"`
	EvidenceSources     []string          `json:"evidence_sources"`
	ProposedDiff        string            `json:"proposed_diff"`
	PreventedRecurrence string            `json:"prevented_recurrence"`
	ExceptionNotes      string            `json:"exception_notes,omitempty"`
}

// IsTargetStale reports whether the target file's content on disk has changed
// since the proposal was generated.
func (p InstructionProposal) IsTargetStale(currentContent string) bool {
	h := sha256.Sum256([]byte(currentContent))
	return p.TargetContentHash != hex.EncodeToString(h[:])
}

// InstructionReviewOptions configures proposal generation.
type InstructionReviewOptions struct {
	Scope string // ScopeProject (default) or ScopeGlobal
}

// ProposeInstructionUpdates evaluates recent friction or repeated decisions against existing instructions.
// It generates proposals for human review and NEVER writes files or grants model outputs authority.
func ProposeInstructionUpdates(ctx context.Context, p Provider, existingRules string, targetPath string, msgs []store.StoredMessage) []InstructionProposal {
	return ProposeInstructionUpdatesWithOptions(ctx, p, existingRules, targetPath, msgs, InstructionReviewOptions{Scope: ScopeProject})
}

// ProposeInstructionUpdatesWithOptions evaluates recent session evidence with explicit scope constraints.
func ProposeInstructionUpdatesWithOptions(ctx context.Context, p Provider, existingRules string, targetPath string, msgs []store.StoredMessage, opts InstructionReviewOptions) []InstructionProposal {
	if p == nil || len(msgs) == 0 {
		return nil
	}

	effScope := strings.ToLower(strings.TrimSpace(opts.Scope))
	if effScope == "" {
		effScope = ScopeProject
	}

	// Global proposals strictly require evidence across multiple projects.
	if effScope == ScopeGlobal {
		projects := make(map[string]struct{})
		for _, m := range msgs {
			if m.ProjectID != "" {
				projects[m.ProjectID] = struct{}{}
			}
		}
		if len(projects) < 2 {
			return nil
		}
	}

	hashBytes := sha256.Sum256([]byte(existingRules))
	targetHash := hex.EncodeToString(hashBytes[:])

	// Bounded sample of messages (most recent 15)
	sample := msgs
	if len(sample) > 15 {
		sample = sample[:15]
	}

	evidence := make(map[string]any)
	evidence["existing_rules_excerpt"] = truncateText(existingRules, 4000)
	evidenceCriteria := make(map[string]string)
	for i, m := range sample {
		key := fmt.Sprintf("evidence_%02d", i)
		toolStr := strings.Join(m.SourceTools, ",")
		if toolStr == "" {
			toolStr = "agent"
		}
		evidence[key] = map[string]any{
			"id":        m.ID,
			"project":   m.ProjectID,
			"tool":      toolStr,
			"role":      m.Role,
			"timestamp": m.TS,
			"content":   truncateText(m.Content, 200),
		}
		evidenceCriteria[key] = fmt.Sprintf("[%s] %s", m.Role, truncateText(m.Content, 160))
	}

	questions := map[string]Question{
		"needs_instruction_change": {
			Type:         TypeNoul,
			Instructions: "Does this evidence demonstrate recurring agent friction or an unrecorded invariant that is NOT already covered in existing_rules_excerpt?",
		},
		"suggested_action": {
			Type:         TypeChoice,
			Instructions: "What is the minimal action required to prevent recurrence?",
			Criteria: map[string]string{
				"add":       "Add a missing architectural invariant or explicit rule",
				"clarify":   "Clarify an ambiguous or frequently misinterpreted rule",
				"narrow":    "Narrow an overly broad rule that causes false positives",
				"remove":    "Remove an obsolete, conflicting, or disproven rule",
				"no_change": "No change needed; existing rules or code already address this",
			},
		},
		"primary_evidence": {
			Type:         TypeChoice,
			Instructions: "Which evidence item best demonstrates the failure or invariant to record?",
			Criteria:     evidenceCriteria,
		},
	}

	subCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()

	resp, err := p.Evaluate(subCtx, "", evidence, questions)
	if err != nil {
		return nil // Fail-open: return nil on error
	}

	noulAns, okNoul := resp.Nouls["needs_instruction_change"]
	choiceAns, okChoice := resp.Choices["suggested_action"]
	if !okNoul || !okChoice || noulAns.Noul < 0.65 || choiceAns.Choice == "no_change" {
		return nil // Weak evidence or no change needed
	}

	// Select primary supporting message
	primaryKey := resp.Choices["primary_evidence"].Choice
	var chosenMsg store.StoredMessage
	found := false
	if primaryKey != "" {
		for i := range sample {
			if fmt.Sprintf("evidence_%02d", i) == primaryKey {
				chosenMsg = sample[i]
				found = true
				break
			}
		}
	}
	if !found {
		// Fall back to first message with error/friction indicators or the last message
		chosenMsg = selectBestEvidence(sample)
	}

	evidenceSrc := formatEvidenceSource(chosenMsg)
	incidentSummary := extractIncidentSummary(chosenMsg.Content)
	if incidentSummary == "" {
		incidentSummary = "unrecorded session invariant"
	}

	// Check if already covered in existing rules
	if isAlreadyCovered(existingRules, incidentSummary) {
		return nil
	}

	action := InstructionAction(choiceAns.Choice)
	updatedRules, failureSummary, preventedRecurrence, exceptionNotes := synthesizeRules(action, existingRules, incidentSummary)
	if updatedRules == "" || updatedRules == existingRules {
		return nil
	}

	diff := agents.UnifiedDiff(existingRules, updatedRules, targetPath)
	if diff == "" {
		return nil
	}

	return []InstructionProposal{
		{
			Action:              action,
			Scope:               effScope,
			TargetFile:          targetPath,
			TargetContentHash:   targetHash,
			FailureSummary:      failureSummary,
			EvidenceSources:     []string{evidenceSrc},
			ProposedDiff:        diff,
			PreventedRecurrence: preventedRecurrence,
			ExceptionNotes:      exceptionNotes,
		},
	}
}

func selectBestEvidence(sample []store.StoredMessage) store.StoredMessage {
	if len(sample) == 0 {
		return store.StoredMessage{}
	}
	for _, s := range slices.Backward(sample) {
		lower := strings.ToLower(s.Content)
		if strings.Contains(lower, "fail") || strings.Contains(lower, "error") ||
			strings.Contains(lower, "corrupt") || strings.Contains(lower, "delete") ||
			strings.Contains(lower, "break") || strings.Contains(lower, "cgo") ||
			strings.Contains(lower, "block") {
			return s
		}
	}
	return sample[len(sample)-1]
}

func formatEvidenceSource(m store.StoredMessage) string {
	if m.ID > 0 && m.TS != "" {
		return fmt.Sprintf("msg_%d (%s)", m.ID, m.TS)
	}
	if m.ID > 0 {
		return fmt.Sprintf("msg_%d", m.ID)
	}
	role := m.Role
	if role == "" {
		role = "message"
	}
	return fmt.Sprintf("[%s] %s", role, truncateText(m.Content, 60))
}

var cleanPrefixRe = regexp.MustCompile(`^(?i)(an agent|agent|the agent|user|assistant|build|error|failed|failure):?\s*`)

func extractIncidentSummary(content string) string {
	trimmed := strings.TrimSpace(content)
	// Strip standard markdown prefixes or brackets
	if idx := strings.Index(trimmed, "\n"); idx != -1 {
		trimmed = trimmed[:idx]
	}
	trimmed = strings.TrimPrefix(trimmed, "- ")
	trimmed = strings.TrimPrefix(trimmed, "* ")
	trimmed = strings.TrimRight(trimmed, ".!?; \t\n\r")
	return truncateText(trimmed, 120)
}

func isAlreadyCovered(existingRules, incidentSummary string) bool {
	if existingRules == "" || incidentSummary == "" {
		return false
	}
	lowerRules := strings.ToLower(existingRules)
	lowerIncident := strings.ToLower(incidentSummary)

	// Clean common prefixes
	cleaned := cleanPrefixRe.ReplaceAllString(lowerIncident, "")
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return false
	}

	// Exact substring check
	if strings.Contains(lowerRules, cleaned) {
		return true
	}

	// Key term presence check: if all significant words (len >= 4) appear in the same line
	words := strings.Fields(cleaned)
	var sigWords []string
	for _, w := range words {
		w = strings.Trim(w, `"',.:;()[]{}!`)
		if len(w) >= 4 && w != "with" && w != "from" && w != "that" && w != "this" && w != "have" {
			sigWords = append(sigWords, w)
		}
	}
	if len(sigWords) >= 2 {
		for line := range strings.SplitSeq(lowerRules, "\n") {
			matchedAll := true
			for _, sw := range sigWords {
				if !strings.Contains(line, sw) {
					matchedAll = false
					break
				}
			}
			if matchedAll {
				return true
			}
		}
	}
	return false
}

func synthesizeRules(action InstructionAction, existingRules, incident string) (updatedRules, failureSummary, recurrence, exceptionNotes string) {
	rulePhrase := incident
	// Clean up for invariant phrasing
	cleaned := cleanPrefixRe.ReplaceAllString(incident, "")
	if cleaned != "" {
		rulePhrase = cleaned
	}

	switch action {
	case ActionAdd:
		failureSummary = fmt.Sprintf("Recent sessions experienced friction: %s.", incident)
		recurrence = fmt.Sprintf("Prevents agents from repeating: %s.", incident)
		exceptionNotes = "Review rule scope and project-specific constraints before adopting."

		newRule := fmt.Sprintf("- Invariant: Never allow %s.", rulePhrase)
		if strings.HasPrefix(strings.ToLower(rulePhrase), "preserve") || strings.HasPrefix(strings.ToLower(rulePhrase), "use ") || strings.HasPrefix(strings.ToLower(rulePhrase), "always ") {
			newRule = fmt.Sprintf("- Invariant: %s.", rulePhrase)
		}

		if existingRules == "" {
			updatedRules = fmt.Sprintf("# Rules\n\n## Invariants\n%s\n", newRule)
			return
		}

		if strings.Contains(existingRules, "## Invariants") {
			idx := strings.Index(existingRules, "## Invariants")
			endOfLine := strings.Index(existingRules[idx:], "\n")
			if endOfLine != -1 {
				splitPt := idx + endOfLine + 1
				updatedRules = existingRules[:splitPt] + newRule + "\n" + existingRules[splitPt:]
				return
			}
		}

		// Append at end
		trimmed := strings.TrimRight(existingRules, "\n")
		updatedRules = trimmed + "\n\n## Invariants\n" + newRule + "\n"

	case ActionClarify:
		if existingRules == "" {
			return "", "", "", ""
		}
		lines := strings.Split(existingRules, "\n")
		matchIdx := findBestMatchingRuleIndex(lines, incident)
		if matchIdx == -1 {
			return "", "", "", ""
		}
		failureSummary = fmt.Sprintf("Existing instruction was ambiguous or misinterpreted: %s.", incident)
		recurrence = fmt.Sprintf("Clarifies boundary condition to prevent recurrence of: %s.", incident)

		clarification := fmt.Sprintf(" (Clarification: verify %s boundary conditions).", rulePhrase)
		lines[matchIdx] = lines[matchIdx] + clarification
		updatedRules = strings.Join(lines, "\n")
		if !strings.HasSuffix(updatedRules, "\n") {
			updatedRules += "\n"
		}

	case ActionNarrow:
		if existingRules == "" {
			return "", "", "", ""
		}
		lines := strings.Split(existingRules, "\n")
		matchIdx := findBestMatchingRuleIndex(lines, incident)
		if matchIdx == -1 {
			return "", "", "", ""
		}
		failureSummary = fmt.Sprintf("Overly broad rule caused false rejections or friction: %s.", incident)
		recurrence = fmt.Sprintf("Narrows scope to avoid blocking valid operations during: %s.", incident)
		exceptionNotes = "Narrowed exception applies only when explicitly justified."

		narrowNote := fmt.Sprintf(" (Scope exception: permit when %s is explicitly authorized).", rulePhrase)
		lines[matchIdx] = lines[matchIdx] + narrowNote
		updatedRules = strings.Join(lines, "\n")
		if !strings.HasSuffix(updatedRules, "\n") {
			updatedRules += "\n"
		}

	case ActionRemove:
		if existingRules == "" {
			return "", "", "", ""
		}
		lines := strings.Split(existingRules, "\n")
		matchIdx := findBestMatchingRuleIndex(lines, incident)
		if matchIdx == -1 {
			return "", "", "", ""
		}
		failureSummary = fmt.Sprintf("Obsolete or conflicting instruction identified: %s.", incident)
		recurrence = fmt.Sprintf("Removes obsolete constraint to prevent unnecessary friction: %s.", incident)

		var remaining []string
		for i, l := range lines {
			if i == matchIdx {
				continue
			}
			remaining = append(remaining, l)
		}
		updatedRules = strings.Join(remaining, "\n")
		if !strings.HasSuffix(updatedRules, "\n") {
			updatedRules += "\n"
		}

	default:
		return "", "", "", ""
	}

	return
}

func truncateText(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func findBestMatchingRuleIndex(lines []string, incident string) int {
	incidentWords := extractKeywords(incident)
	if len(incidentWords) == 0 {
		return -1
	}

	bestIdx := -1
	bestScore := 0

	for i, l := range lines {
		trimmed := strings.TrimSpace(l)
		if !strings.HasPrefix(trimmed, "- ") && !strings.HasPrefix(trimmed, "* ") {
			continue
		}
		ruleWords := extractKeywords(trimmed)
		score := 0
		for _, rw := range ruleWords {
			for _, iw := range incidentWords {
				if rw == iw || strings.HasPrefix(rw, iw) || strings.HasPrefix(iw, rw) {
					score++
				}
			}
		}
		if score > bestScore {
			bestScore = score
			bestIdx = i
		}
	}

	if bestScore == 0 {
		return -1
	}
	return bestIdx
}

func extractKeywords(s string) []string {
	lower := strings.ToLower(s)
	var b strings.Builder
	for _, r := range lower {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r >= 0x0600 {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	words := strings.Fields(b.String())
	stopwords := map[string]bool{
		"the": true, "and": true, "for": true, "with": true, "that": true, "this": true,
		"have": true, "from": true, "because": true, "agent": true, "agents": true,
		"failed": true, "failure": true, "error": true, "errors": true, "conflict": true,
		"conflicts": true, "requirement": true, "requirements": true, "support": true,
		"supported": true, "current": true, "when": true, "where": true, "which": true,
		"rule": true, "rules": true, "invariant": true, "never": true, "always": true,
	}
	var res []string
	for _, w := range words {
		if len([]rune(w)) >= 3 && !stopwords[w] {
			res = append(res, w)
		}
	}
	return res
}
