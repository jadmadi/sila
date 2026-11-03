package store

import (
	"context"
	"fmt"
	"strings"
)

// briefingSection represents a parsed block of a priming briefing.
type briefingSection struct {
	sectionType string   // "title", "contract", "handoff", "active_goal", "completed_goals", "runbook", "memos", "claims", "lessons", "candidates", "guardrails", "docs", "grounding", "other"
	header      string   // Section header line
	lines       []string // Section body lines
}

// ShrinkBriefing deterministically compacts a raw briefing string according to
// the 4-tier degradation hierarchy inspired by Raven's WindowPressure:
//   - Tier 1 (Inviolable — Never Dropped): Core rules/working contract, active goal contract, handoff.
//   - Tier 2 (WATCH): Collapse multi-line diff blocks in lessons; omit completed roadmap goals.
//   - Tier 3 (HEAVY): Memos collapsed to titles + bulleted invariants; suppress historical facts.
//   - Tier 4 (CRITICAL): Suppress all declarative memos and past lessons entirely.
//
// If forcedPressure is provided, that pressure level is applied directly.
// If targetTokens > 0, the minimal degradation level necessary to satisfy the target is selected.
// Otherwise, the pressure is determined automatically by the token count of rawBriefing.
func ShrinkBriefing(ctx context.Context, targetTokens int, rawBriefing string, forcedPressure ...WindowPressure) ShrinkResult {
	origTokens := EstimateTokens(len(rawBriefing))
	if rawBriefing == "" {
		return ShrinkResult{
			OriginalTokens: 0,
			FinalTokens:    0,
			Pressure:       PressureNormal,
			Payload:        "",
		}
	}

	// 1. If explicit forced pressure provided, execute directly
	if len(forcedPressure) > 0 && forcedPressure[0] != "" {
		p := forcedPressure[0]
		payload, pruned := applyPressureDegradation(rawBriefing, p)
		return ShrinkResult{
			OriginalTokens: origTokens,
			FinalTokens:    EstimateTokens(len(payload)),
			Pressure:       p,
			SectionsPruned: pruned,
			Payload:        payload,
		}
	}

	// 2. If target budget is provided, evaluate degradation ladder to fit budget
	if targetTokens > 0 {
		if origTokens <= targetTokens {
			// Already fits at NORMAL pressure
			return ShrinkResult{
				OriginalTokens: origTokens,
				FinalTokens:    origTokens,
				Pressure:       PressureNormal,
				Payload:        rawBriefing,
			}
		}

		// Try WATCH
		pWatchPayload, pWatchPruned := applyPressureDegradation(rawBriefing, PressureWatch)
		if EstimateTokens(len(pWatchPayload)) <= targetTokens {
			return ShrinkResult{
				OriginalTokens: origTokens,
				FinalTokens:    EstimateTokens(len(pWatchPayload)),
				Pressure:       PressureWatch,
				SectionsPruned: pWatchPruned,
				Payload:        pWatchPayload,
			}
		}

		// Try HEAVY
		pHeavyPayload, pHeavyPruned := applyPressureDegradation(rawBriefing, PressureHeavy)
		if EstimateTokens(len(pHeavyPayload)) <= targetTokens {
			return ShrinkResult{
				OriginalTokens: origTokens,
				FinalTokens:    EstimateTokens(len(pHeavyPayload)),
				Pressure:       PressureHeavy,
				SectionsPruned: pHeavyPruned,
				Payload:        pHeavyPayload,
			}
		}

		// Fallback to CRITICAL
		pCritPayload, pCritPruned := applyPressureDegradation(rawBriefing, PressureCritical)
		return ShrinkResult{
			OriginalTokens: origTokens,
			FinalTokens:    EstimateTokens(len(pCritPayload)),
			Pressure:       PressureCritical,
			SectionsPruned: pCritPruned,
			Payload:        pCritPayload,
		}
	}

	// 3. Auto-detect from token weight
	p := DeterminePressure(origTokens)
	if p == PressureNormal {
		return ShrinkResult{
			OriginalTokens: origTokens,
			FinalTokens:    origTokens,
			Pressure:       PressureNormal,
			Payload:        rawBriefing,
		}
	}

	payload, pruned := applyPressureDegradation(rawBriefing, p)
	return ShrinkResult{
		OriginalTokens: origTokens,
		FinalTokens:    EstimateTokens(len(payload)),
		Pressure:       p,
		SectionsPruned: pruned,
		Payload:        payload,
	}
}

// parseBriefingSections decomposes raw markdown briefing into logical sections.
func parseBriefingSections(raw string) []briefingSection {
	lines := strings.Split(raw, "\n")
	var sections []briefingSection
	var current *briefingSection

	startSection := func(secType, header string) {
		if current != nil {
			sections = append(sections, *current)
		}
		current = &briefingSection{
			sectionType: secType,
			header:      header,
			lines:       make([]string, 0),
		}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		switch {
		case strings.HasPrefix(trimmed, "### 🧠"):
			startSection("title", line)
		case strings.HasPrefix(trimmed, "**Working Contract"):
			startSection("contract", line)
		case strings.HasPrefix(trimmed, "**Active Session Handoff"):
			startSection("handoff", line)
		case strings.HasPrefix(trimmed, "**Active Goal"):
			startSection("active_goal", line)
		case strings.HasPrefix(trimmed, "**Completed Roadmap Goals") || strings.HasPrefix(trimmed, "**Completed Goals"):
			startSection("completed_goals", line)
		case strings.HasPrefix(trimmed, "**Discovered Repo Runbook"):
			startSection("runbook", line)
		case strings.HasPrefix(trimmed, "**Key Architectural Facts"):
			startSection("memos", line)
		case strings.HasPrefix(trimmed, "**File-Referenced Claims"):
			startSection("claims", line)
		case strings.HasPrefix(trimmed, "**Solved Before"):
			startSection("lessons", line)
		case strings.HasPrefix(trimmed, "**Candidate Directives"):
			startSection("candidates", line)
		case strings.HasPrefix(trimmed, "**Strategic Guardrails"):
			startSection("guardrails", line)
		case strings.HasPrefix(trimmed, "**Attached Reference Docs"):
			startSection("docs", line)
		case strings.HasPrefix(trimmed, "**Disk Grounding") || strings.HasPrefix(trimmed, "**Context Surface"):
			startSection("grounding", line)
		default:
			if current == nil {
				startSection("other", "")
			}
			current.lines = append(current.lines, line)
		}
	}

	if current != nil {
		sections = append(sections, *current)
	}

	return sections
}

// applyPressureDegradation applies the transformation rules for the given pressure.
func applyPressureDegradation(raw string, pressure WindowPressure) (string, []string) {
	if pressure == PressureNormal {
		return raw, nil
	}

	sections := parseBriefingSections(raw)
	var outputParts []string
	var sectionsPruned []string
	prunedMap := make(map[string]bool)

	markPruned := func(name string) {
		if !prunedMap[name] {
			prunedMap[name] = true
			sectionsPruned = append(sectionsPruned, name)
		}
	}

	for _, sec := range sections {
		switch sec.sectionType {
		case "title", "contract", "active_goal", "handoff":
			// Tier 1 Inviolable: NEVER DROPPED under any pressure level.
			outputParts = append(outputParts, renderSection(sec))

		case "completed_goals":
			// Tier 2 (WATCH): Omit completed roadmap goals
			markPruned("completed_roadmap_goals")

		case "lessons":
			if pressure == PressureCritical {
				// Tier 4 (CRITICAL): Suppress past lessons entirely
				markPruned("past_lessons")
			} else if pressure == PressureHeavy || pressure == PressureWatch {
				// Tier 2 (WATCH) & Tier 3 (HEAVY): Collapse multi-line diff blocks into 1-line semantic summaries
				collapsed, didCollapse := collapseLessonDiffs(sec)
				if didCollapse {
					markPruned("lessons_multiline_diffs")
				}
				outputParts = append(outputParts, renderSection(collapsed))
			} else {
				outputParts = append(outputParts, renderSection(sec))
			}

		case "memos":
			if pressure == PressureCritical {
				// Tier 4 (CRITICAL): Suppress declarative memos entirely
				markPruned("declarative_memos")
			} else if pressure == PressureHeavy {
				// Tier 3 (HEAVY): Strip narrative paragraphs, retain titles + bulleted invariant statements
				stripped, didStrip := stripMemoNarratives(sec)
				if didStrip {
					markPruned("memo_narratives")
				}
				outputParts = append(outputParts, renderSection(stripped))
			} else {
				outputParts = append(outputParts, renderSection(sec))
			}

		case "claims":
			if pressure == PressureCritical {
				markPruned("historical_facts")
			} else if pressure == PressureHeavy {
				// Tier 3 (HEAVY): Suppress historical facts outside active goal
				filtered, didFilter := filterHistoricalClaims(sec)
				if didFilter {
					markPruned("historical_facts")
				}
				if len(filtered.lines) > 0 {
					outputParts = append(outputParts, renderSection(filtered))
				}
			} else {
				outputParts = append(outputParts, renderSection(sec))
			}

		case "candidates":
			if pressure == PressureCritical || pressure == PressureHeavy {
				markPruned("candidate_directives")
			} else {
				outputParts = append(outputParts, renderSection(sec))
			}

		case "guardrails":
			if pressure == PressureCritical {
				markPruned("strategic_guardrails")
			} else {
				outputParts = append(outputParts, renderSection(sec))
			}

		case "docs":
			if pressure == PressureCritical || pressure == PressureHeavy {
				markPruned("attached_docs")
			} else {
				outputParts = append(outputParts, renderSection(sec))
			}

		case "runbook":
			if pressure == PressureCritical || pressure == PressureHeavy {
				markPruned("runbook_guidance")
			} else {
				outputParts = append(outputParts, renderSection(sec))
			}

		case "grounding":
			// Retain grounding summary
			outputParts = append(outputParts, renderSection(sec))

		default:
			if pressure != PressureCritical {
				outputParts = append(outputParts, renderSection(sec))
			}
		}
	}

	result := strings.TrimSpace(strings.Join(outputParts, "\n\n"))
	return result, sectionsPruned
}

// renderSection formats a briefingSection back into clean markdown.
func renderSection(sec briefingSection) string {
	var sb strings.Builder
	if sec.header != "" {
		sb.WriteString(sec.header)
		if len(sec.lines) > 0 {
			sb.WriteString("\n")
		}
	}
	for i, l := range sec.lines {
		sb.WriteString(l)
		if i < len(sec.lines)-1 {
			sb.WriteString("\n")
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// collapseLessonDiffs detects multi-line code diff blocks or verbose indented blocks
// in lessons and collapses them into 1-line semantic summaries.
func collapseLessonDiffs(sec briefingSection) (briefingSection, bool) {
	var newLines []string
	collapsed := false
	inCodeBlock := false
	var activeBullet string

	for _, line := range sec.lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "```") {
			collapsed = true
			if !inCodeBlock {
				inCodeBlock = true
				continue
			} else {
				inCodeBlock = false
				// Append 1-line semantic fix summary if the active bullet lacked inline fix
				if activeBullet != "" && !strings.Contains(activeBullet, "fix:") {
					activeBullet += " · fix: updated symbol to handle edge case"
					newLines = append(newLines, activeBullet)
					activeBullet = ""
				}
				continue
			}
		}

		if inCodeBlock {
			// Extract symbol or path if visible
			if strings.HasPrefix(trimmed, "+") || strings.HasPrefix(trimmed, "-") {
				codeExcerpt := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(trimmed, "+"), "-"))
				if len(codeExcerpt) > 0 && activeBullet != "" && !strings.Contains(activeBullet, "fix:") {
					activeBullet += fmt.Sprintf(" · fix: [%s] updated implementation", codeExcerpt)
					newLines = append(newLines, activeBullet)
					activeBullet = ""
				}
			}
			continue
		}

		if strings.HasPrefix(trimmed, "• ") || strings.HasPrefix(trimmed, "- ") {
			if activeBullet != "" {
				newLines = append(newLines, activeBullet)
			}
			activeBullet = line
			continue
		}

		if activeBullet != "" {
			if strings.HasPrefix(trimmed, "fix:") || strings.HasPrefix(trimmed, "Fix:") {
				collapsed = true
				fixText := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(trimmed, "fix:"), "Fix:"))
				activeBullet += fmt.Sprintf(" · fix: %s", fixText)
				newLines = append(newLines, activeBullet)
				activeBullet = ""
				continue
			}
			// Other indented line
			activeBullet += " " + trimmed
			continue
		}

		newLines = append(newLines, line)
	}

	if activeBullet != "" {
		newLines = append(newLines, activeBullet)
	}

	return briefingSection{
		sectionType: sec.sectionType,
		header:      sec.header,
		lines:       newLines,
	}, collapsed
}

// stripMemoNarratives keeps only section headers and bulleted invariant statements,
// dropping multi-line prose paragraphs.
func stripMemoNarratives(sec briefingSection) (briefingSection, bool) {
	var newLines []string
	stripped := false

	for _, line := range sec.lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// Keep bullets (e.g. "• ...", "- ...")
		if strings.HasPrefix(trimmed, "•") || strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "*") {
			newLines = append(newLines, line)
		} else {
			// Freeform narrative paragraph stripped
			stripped = true
		}
	}

	return briefingSection{
		sectionType: sec.sectionType,
		header:      sec.header,
		lines:       newLines,
	}, stripped
}

// filterHistoricalClaims retains only claims tagged with active goal or file references.
func filterHistoricalClaims(sec briefingSection) (briefingSection, bool) {
	var newLines []string
	filtered := false

	for _, line := range sec.lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// Only retain if it has explicit file reference or invariant marker
		if strings.Contains(line, "(`") && strings.Contains(line, "`)") {
			newLines = append(newLines, line)
		} else {
			filtered = true
		}
	}

	return briefingSection{
		sectionType: sec.sectionType,
		header:      sec.header,
		lines:       newLines,
	}, filtered
}

// filterInvariants retains only architectural invariants (Tier 1 Inviolable).
func filterInvariants(sec briefingSection) briefingSection {
	var newLines []string
	for _, line := range sec.lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(trimmed)
		if strings.Contains(lower, "invariant") || strings.Contains(lower, "rule") || strings.Contains(lower, "constraint") {
			newLines = append(newLines, line)
		}
	}
	return briefingSection{
		sectionType: sec.sectionType,
		header:      sec.header,
		lines:       newLines,
	}
}
