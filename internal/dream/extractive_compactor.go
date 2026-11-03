package dream

import (
	"fmt"
	"strings"

	"github.com/jadmadi/sila/internal/store"
)

// ExtractiveCompactionResult encapsulates telemetry and payload for A2 extractive compaction.
type ExtractiveCompactionResult struct {
	OriginalTokens int                 `json:"original_tokens"`
	FinalTokens    int                 `json:"final_tokens"`
	SavingsRatio   float64             `json:"savings_ratio"`
	Tokens         ExtractedKeepTokens `json:"tokens"`
	CompactedBody  string              `json:"compacted_body"`
}

const (
	compactedMemoryHeader = "[Compacted Memory: A2 Zero-LLM Extractive]"
	maxL0Length           = 120
	maxL1Length           = 320
)

// IsAlreadyCompacted checks if a text block has already been processed by the extractive compactor.
func IsAlreadyCompacted(text string) bool {
	trimmed := strings.TrimSpace(text)
	return strings.HasPrefix(trimmed, compactedMemoryHeader) ||
		strings.Contains(trimmed, "[Compacted Memory:") ||
		(strings.Contains(trimmed, "L0:") && strings.Contains(trimmed, "L2 (Keep-Tokens):"))
}

// CompactExtractive executes deterministic, zero-LLM extractive keep-token compaction.
// Retains L0 abstract, L1 intent summary, and L2 extracted durable keep-tokens while dropping prose.
func CompactExtractive(body string) ExtractiveCompactionResult {
	origTokens := store.EstimateTokens(len(body))

	// Guard against empty input
	if strings.TrimSpace(body) == "" {
		return ExtractiveCompactionResult{
			OriginalTokens: origTokens,
			FinalTokens:    0,
			SavingsRatio:   0,
			Tokens:         ExtractedKeepTokens{},
			CompactedBody:  "",
		}
	}

	// Idempotency check: if already compacted, do not re-process
	if IsAlreadyCompacted(body) {
		tokens := ExtractKeepTokens(body)
		return ExtractiveCompactionResult{
			OriginalTokens: origTokens,
			FinalTokens:    origTokens,
			SavingsRatio:   0,
			Tokens:         tokens,
			CompactedBody:  body,
		}
	}

	// 1. Extract high-entropy durable keep-tokens
	tokens := ExtractKeepTokens(body)

	// 2. Synthesize L0 (Abstract)
	l0 := extractL0Abstract(body)

	// 3. Synthesize L1 (Intent Summary)
	l1 := extractL1Intent(body)

	// 4. Synthesize L2 (Durable Keep-Tokens & Fact Block)
	l2 := formatL2KeepTokens(tokens)

	// 5. Assemble final compacted payload
	var b strings.Builder
	b.WriteString(compactedMemoryHeader)
	b.WriteString("\n")
	b.WriteString("L0: ")
	b.WriteString(l0)
	b.WriteString("\n")
	if l1 != "" && l1 != l0 && !strings.HasPrefix(l1, l0) {
		b.WriteString("L1: ")
		b.WriteString(l1)
		b.WriteString("\n")
	}
	if l2 != "" {
		b.WriteString("L2 (Keep-Tokens):\n")
		b.WriteString(l2)
	}

	compactedText := strings.TrimSpace(b.String())
	finalTokens := store.EstimateTokens(len(compactedText))

	savingsRatio := 0.0
	if origTokens > 0 && origTokens > finalTokens {
		savingsRatio = float64(origTokens-finalTokens) / float64(origTokens)
	}

	return ExtractiveCompactionResult{
		OriginalTokens: origTokens,
		FinalTokens:    finalTokens,
		SavingsRatio:   savingsRatio,
		Tokens:         tokens,
		CompactedBody:  compactedText,
	}
}

func extractL0Abstract(text string) string {
	lines := strings.Split(text, "\n")
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" {
			continue
		}
		// Check for markdown headings or labeled lines
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(trimmed, "#") {
			clean := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			if len(clean) > 0 {
				return truncateWithEllipsis(clean, maxL0Length)
			}
		}
		if strings.HasPrefix(lower, "intent:") || strings.HasPrefix(lower, "summary:") ||
			strings.HasPrefix(lower, "title:") || strings.HasPrefix(lower, "abstract:") ||
			strings.HasPrefix(lower, "symptom:") {
			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) == 2 {
				clean := strings.TrimSpace(parts[1])
				if clean != "" {
					return truncateWithEllipsis(clean, maxL0Length)
				}
			}
		}
	}

	// Fallback to first non-empty sentence
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" || isConversationalPadding(trimmed) {
			continue
		}
		sentences := strings.Split(trimmed, ". ")
		if len(sentences) > 0 && strings.TrimSpace(sentences[0]) != "" {
			return truncateWithEllipsis(strings.TrimSpace(sentences[0]), maxL0Length)
		}
		return truncateWithEllipsis(trimmed, maxL0Length)
	}

	return "Episodic memory record"
}

func extractL1Intent(text string) string {
	paragraphs := strings.Split(text, "\n\n")
	for _, p := range paragraphs {
		trimmed := strings.TrimSpace(p)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "```") {
			continue
		}
		lines := strings.Split(trimmed, "\n")
		var cleanLines []string
		for _, l := range lines {
			lt := strings.TrimSpace(l)
			if !isConversationalPadding(lt) && !strings.HasPrefix(lt, "$") && !strings.HasPrefix(lt, ">") {
				cleanLines = append(cleanLines, lt)
			}
		}
		if len(cleanLines) == 0 {
			continue
		}
		joined := strings.Join(cleanLines, " ")
		flattened := strings.Join(strings.Fields(joined), " ")
		if len(flattened) > 15 {
			return truncateWithEllipsis(flattened, maxL1Length)
		}
	}
	return ""
}

func formatL2KeepTokens(k ExtractedKeepTokens) string {
	var lines []string

	if len(k.FilePaths) > 0 {
		lines = append(lines, fmt.Sprintf("• Paths: %s", strings.Join(k.FilePaths, ", ")))
	}
	if len(k.ErrorCodes) > 0 {
		lines = append(lines, fmt.Sprintf("• Errors: %s", strings.Join(k.ErrorCodes, ", ")))
	}
	if len(k.Symbols) > 0 {
		lines = append(lines, fmt.Sprintf("• Symbols: %s", strings.Join(k.Symbols, ", ")))
	}
	if len(k.Commands) > 0 {
		lines = append(lines, fmt.Sprintf("• Commands: %s", strings.Join(k.Commands, ", ")))
	}
	if len(k.Identifiers) > 0 {
		lines = append(lines, fmt.Sprintf("• Identifiers: %s", strings.Join(k.Identifiers, ", ")))
	}
	if len(k.URLs) > 0 {
		lines = append(lines, fmt.Sprintf("• URLs: %s", strings.Join(k.URLs, ", ")))
	}

	return strings.Join(lines, "\n")
}

func isConversationalPadding(s string) bool {
	lower := strings.ToLower(strings.TrimSpace(s))
	paddingPrefixes := []string{
		"hello", "hi there", "sure,", "okay,", "i will now", "let me", "thinking process:",
		"i am going to", "certainly!", "as requested", "in this session", "here is the output",
		"let's proceed", "thank you", "great,", "alright,", "yes,",
	}
	for _, p := range paddingPrefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}

func truncateWithEllipsis(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return strings.TrimSpace(s[:maxLen-3]) + "..."
}
