package dream

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/jadmadi/sila/internal/store"
)

// CompressionOptions configures the declarative context compression and token pruning filters.
type CompressionOptions struct {
	TargetBudget    int  // Target token budget (0 = unconstrained/adaptive)
	MaxFixtureLines int  // Max lines before folding repetitive test fixtures (default: 15)
	StripHeaders    bool // Strip copyright and license boilerplate headers
	FoldFixtures    bool // Fold large repetitive test fixtures and arrays
	CondenseStacks  bool // Condense verbose multi-frame stack traces
}

// DefaultCompressionOptions returns the standard production compression configuration.
func DefaultCompressionOptions() CompressionOptions {
	return CompressionOptions{
		TargetBudget:    0,
		MaxFixtureLines: 15,
		StripHeaders:    true,
		FoldFixtures:    true,
		CondenseStacks:  true,
	}
}

// CompressionResult encapsulates telemetry and savings metrics for compressed context.
type CompressionResult struct {
	OriginalText     string  `json:"-"`
	CompressedText   string  `json:"compressed_text"`
	OriginalTokens   int     `json:"original_tokens"`
	CompressedTokens int     `json:"compressed_tokens"`
	PrunedTokens     int     `json:"pruned_tokens"`
	SavingsRatio     float64 `json:"savings_ratio"` // e.g. 0.45 = 45% reduction
	HeadersStripped  int     `json:"headers_stripped"`
	FixturesFolded   int     `json:"fixtures_folded"`
	StacksCondensed  int     `json:"stacks_condensed"`
}

// ParseBudget parses human token budget strings like "8k", "16k", "32k", or raw integers.
func ParseBudget(budgetStr string) int {
	s := strings.TrimSpace(strings.ToLower(budgetStr))
	if s == "" {
		return 0
	}
	multiplier := 1
	if strings.HasSuffix(s, "k") {
		multiplier = 1000
		s = strings.TrimSuffix(s, "k")
	} else if strings.HasSuffix(s, "m") {
		multiplier = 1000000
		s = strings.TrimSuffix(s, "m")
	}
	val, err := strconv.Atoi(s)
	if err != nil || val <= 0 {
		return 0
	}
	return val * multiplier
}

// StripBoilerplateHeaders removes multi-line copyright, license, and SPDX headers from text.
func StripBoilerplateHeaders(text string) (string, int) {
	lines := strings.Split(text, "\n")
	var result []string
	strippedCount := 0
	inHeaderBlock := false
	var headerBuffer []string

	isBoilerplateLine := func(line string) bool {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			return true
		}
		lower := strings.ToLower(trimmed)
		return strings.Contains(lower, "spdx-license-identifier") ||
			strings.Contains(lower, "copyright (c)") ||
			strings.Contains(lower, "copyright ") ||
			strings.Contains(lower, "licensed under the apache") ||
			strings.Contains(lower, "permission is hereby granted, free of charge") ||
			strings.Contains(lower, "all rights reserved") ||
			strings.Contains(lower, "redistribution and use in source") ||
			strings.Contains(lower, "the above copyright notice") ||
			strings.Contains(lower, "mit license") ||
			strings.Contains(lower, "apache license") ||
			(inHeaderBlock && (strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") || strings.HasPrefix(trimmed, "#")))
	}

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Check if line looks like start of a license/copyright block
		lower := strings.ToLower(trimmed)
		isHeaderIndicator := strings.Contains(lower, "spdx-license-identifier") ||
			strings.Contains(lower, "copyright") ||
			strings.Contains(lower, "licensed under the apache") ||
			strings.Contains(lower, "permission is hereby granted") ||
			strings.Contains(lower, "all rights reserved")

		isComment := strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") || strings.HasPrefix(trimmed, "#")

		if !inHeaderBlock && isHeaderIndicator && isComment {
			inHeaderBlock = true
			headerBuffer = append(headerBuffer, line)
			continue
		}

		if inHeaderBlock {
			if isComment && isBoilerplateLine(line) {
				headerBuffer = append(headerBuffer, line)
				continue
			}
			// End of header block
			inHeaderBlock = false
			if len(headerBuffer) >= 2 || strings.Contains(strings.ToLower(strings.Join(headerBuffer, " ")), "spdx") {
				strippedCount++
				result = append(result, "/* [License boilerplate stripped for context budget] */")
			} else {
				// False positive, restore lines
				result = append(result, headerBuffer...)
			}
			headerBuffer = nil
			result = append(result, line)
			continue
		}

		result = append(result, line)
	}

	if inHeaderBlock {
		if len(headerBuffer) >= 2 || strings.Contains(strings.ToLower(strings.Join(headerBuffer, " ")), "spdx") {
			strippedCount++
			result = append(result, "/* [License boilerplate stripped for context budget] */")
		} else {
			result = append(result, headerBuffer...)
		}
	}

	return strings.Join(result, "\n"), strippedCount
}

// FoldTestFixtures folds large repetitive literal arrays, JSON test fixtures, or SQL strings exceeding maxLines.
func FoldTestFixtures(text string, maxLines int) (string, int) {
	if maxLines <= 0 {
		maxLines = 15
	}
	lines := strings.Split(text, "\n")
	var result []string
	foldedCount := 0

	i := 0
	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Detect start of array or JSON fixture
		isArrayOrFixtureStart := strings.HasSuffix(trimmed, "[") ||
			strings.HasSuffix(trimmed, "{") ||
			strings.HasSuffix(trimmed, "[]string{") ||
			strings.HasSuffix(trimmed, "[]int{") ||
			strings.HasSuffix(trimmed, "[]byte{") ||
			strings.HasSuffix(trimmed, "map[string]any{") ||
			strings.HasPrefix(trimmed, "INSERT INTO ")

		if isArrayOrFixtureStart {
			// Scan ahead to find end of block
			startIdx := i
			endIdx := -1
			bracketCount := 0
			isRepetitive := false
			repetitiveLines := 0

			for j := startIdx; j < len(lines); j++ {
				curTrimmed := strings.TrimSpace(lines[j])
				bracketCount += (strings.Count(curTrimmed, "{") + strings.Count(curTrimmed, "[")) -
					(strings.Count(curTrimmed, "}") + strings.Count(curTrimmed, "]"))

				// Check if contents are data-like
				if j > startIdx {
					if strings.HasPrefix(curTrimmed, "{") || strings.HasPrefix(curTrimmed, "\"") ||
						strings.HasPrefix(curTrimmed, "(") || strings.Contains(curTrimmed, "\": ") {
						repetitiveLines++
					}
				}

				if bracketCount <= 0 && j > startIdx {
					endIdx = j
					break
				}
				if j-startIdx > 200 { // Bound scan window
					break
				}
			}

			if repetitiveLines >= 5 {
				isRepetitive = true
			}

			blockSize := endIdx - startIdx + 1
			if endIdx > 0 && blockSize >= maxLines && isRepetitive {
				// Keep first 2 lines
				result = append(result, lines[startIdx], lines[startIdx+1])
				foldedLines := blockSize - 4
				if foldedLines > 0 {
					result = append(result, fmt.Sprintf("  /* ... [%d items/lines folded for context budget] ... */", foldedLines))
					foldedCount++
					// Keep last 2 lines
					result = append(result, lines[endIdx-1], lines[endIdx])
					i = endIdx + 1
					continue
				}
			}
		}

		result = append(result, line)
		i++
	}

	return strings.Join(result, "\n"), foldedCount
}

// CondenseStackTraces compresses verbose multi-frame stack traces down to the root error and outer frames.
func CondenseStackTraces(text string) (string, int) {
	lines := strings.Split(text, "\n")
	var result []string
	condensedCount := 0

	i := 0
	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Detect Go panic stack trace or Python traceback or Node error stack
		isGoPanic := strings.HasPrefix(trimmed, "panic:") || strings.HasPrefix(trimmed, "goroutine ")
		isPyTraceback := strings.HasPrefix(trimmed, "Traceback (most recent call last):")
		isNodeStack := strings.HasPrefix(trimmed, "Error: ") || strings.HasPrefix(trimmed, "TypeError: ")

		if isGoPanic {
			startIdx := i
			var stackLines []string
			for j := startIdx; j < len(lines); j++ {
				cur := lines[j]
				curTrimmed := strings.TrimSpace(cur)
				if j > startIdx && curTrimmed == "" && j+1 < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[j+1]), "\t") && !strings.HasPrefix(strings.TrimSpace(lines[j+1]), "goroutine ") {
					break
				}
				if j > startIdx && !strings.HasPrefix(cur, "\t") && !strings.HasPrefix(curTrimmed, "goroutine ") && !strings.Contains(cur, "(") && !strings.HasPrefix(curTrimmed, "created by ") {
					if !strings.HasPrefix(curTrimmed, "panic:") {
						break
					}
				}
				stackLines = append(stackLines, cur)
			}

			if len(stackLines) >= 8 {
				// Keep panic line and top 2 frames (4 lines), fold middle, keep bottom 2 lines
				result = append(result, stackLines[0])
				if len(stackLines) > 1 {
					result = append(result, stackLines[1])
				}
				if len(stackLines) > 2 {
					result = append(result, stackLines[2])
				}
				foldedFrames := (len(stackLines) - 4) / 2
				if foldedFrames > 0 {
					result = append(result, fmt.Sprintf("    ... [%d intermediate stack frames condensed] ...", foldedFrames))
					condensedCount++
					result = append(result, stackLines[len(stackLines)-2], stackLines[len(stackLines)-1])
					i = startIdx + len(stackLines)
					continue
				}
			}
		} else if isPyTraceback {
			startIdx := i
			var stackLines []string
			for j := startIdx; j < len(lines); j++ {
				cur := lines[j]
				if j > startIdx && !strings.HasPrefix(cur, "  ") && !strings.HasPrefix(cur, "File ") {
					stackLines = append(stackLines, cur) // Append exception message line
					break
				}
				stackLines = append(stackLines, cur)
			}

			if len(stackLines) >= 6 {
				result = append(result, stackLines[0])
				result = append(result, stackLines[1])
				folded := len(stackLines) - 4
				if folded > 0 {
					result = append(result, fmt.Sprintf("    ... [%d intermediate frames condensed] ...", folded))
					condensedCount++
					result = append(result, stackLines[len(stackLines)-2], stackLines[len(stackLines)-1])
					i = startIdx + len(stackLines)
					continue
				}
			}
		} else if isNodeStack && i+1 < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i+1]), "at ") {
			startIdx := i
			var stackLines []string
			for j := startIdx; j < len(lines); j++ {
				cur := lines[j]
				curTrimmed := strings.TrimSpace(cur)
				if j > startIdx && !strings.HasPrefix(curTrimmed, "at ") {
					break
				}
				stackLines = append(stackLines, cur)
			}

			if len(stackLines) >= 6 {
				result = append(result, stackLines[0]) // Error: ...
				result = append(result, stackLines[1]) // at first caller
				folded := len(stackLines) - 3
				if folded > 0 {
					result = append(result, fmt.Sprintf("    ... [%d stack frames condensed] ...", folded))
					condensedCount++
					result = append(result, stackLines[len(stackLines)-1])
					i = startIdx + len(stackLines)
					continue
				}
			}
		}

		result = append(result, line)
		i++
	}

	return strings.Join(result, "\n"), condensedCount
}

// CompressContext applies declarative token pruning and context compression to briefing text.
func CompressContext(text string, opts CompressionOptions) CompressionResult {
	origTokens := store.EstimateTokens(len(text))
	currText := text
	headersStripped := 0
	fixturesFolded := 0
	stacksCondensed := 0

	if opts.StripHeaders {
		var hCount int
		currText, hCount = StripBoilerplateHeaders(currText)
		headersStripped += hCount
	}

	if opts.FoldFixtures {
		var fCount int
		currText, fCount = FoldTestFixtures(currText, opts.MaxFixtureLines)
		fixturesFolded += fCount
	}

	if opts.CondenseStacks {
		var sCount int
		currText, sCount = CondenseStackTraces(currText)
		stacksCondensed += sCount
	}

	currTokens := store.EstimateTokens(len(currText))

	// Adaptive budget enforcement: if a target token budget is specified and we still exceed it,
	// prune low-priority file-referenced claims while strictly preserving invariants and handoffs.
	if opts.TargetBudget > 0 && currTokens > opts.TargetBudget {
		currText = pruneLowPrioritySections(currText, opts.TargetBudget)
		currTokens = store.EstimateTokens(len(currText))
	}

	prunedTokens := origTokens - currTokens
	if prunedTokens < 0 {
		prunedTokens = 0
	}

	savingsRatio := 0.0
	if origTokens > 0 {
		savingsRatio = float64(prunedTokens) / float64(origTokens)
	}

	return CompressionResult{
		OriginalText:     text,
		CompressedText:   currText,
		OriginalTokens:   origTokens,
		CompressedTokens: currTokens,
		PrunedTokens:     prunedTokens,
		SavingsRatio:     savingsRatio,
		HeadersStripped:  headersStripped,
		FixturesFolded:   fixturesFolded,
		StacksCondensed:  stacksCondensed,
	}
}

// pruneLowPrioritySections adaptively truncates low-priority observed claims to satisfy budget.
func pruneLowPrioritySections(text string, budget int) string {
	claimSectionHeader := "**File-Referenced Claims**"
	idx := strings.Index(text, claimSectionHeader)
	if idx < 0 {
		return text
	}

	// Split at claimSectionHeader
	before := text[:idx]
	after := text[idx:]

	// Find the end of the claims section (next empty line or double newline followed by **)
	nextSection := strings.Index(after[len(claimSectionHeader):], "\n\n**")
	var claimsBlock, remainder string
	if nextSection >= 0 {
		endPos := len(claimSectionHeader) + nextSection
		claimsBlock = after[:endPos]
		remainder = after[endPos:]
	} else {
		claimsBlock = after
		remainder = ""
	}

	claimLines := strings.Split(claimsBlock, "\n")
	var keptClaims []string
	omitted := 0
	for _, l := range claimLines {
		if strings.HasPrefix(l, "• ") {
			if len(keptClaims) < 3 {
				keptClaims = append(keptClaims, l)
			} else {
				omitted++
			}
		} else {
			keptClaims = append(keptClaims, l)
		}
	}

	if omitted > 0 {
		keptClaims = append(keptClaims, fmt.Sprintf("• ... [%d file-referenced claims omitted to preserve %d token context budget] ...", omitted, budget))
	}

	newClaimsBlock := strings.Join(keptClaims, "\n")
	return before + newClaimsBlock + remainder
}

// FormatCompressionSummary returns a compact human-facing badge for compression telemetry.
func (c *CompressionResult) FormatCompressionSummary() string {
	if c == nil || c.PrunedTokens <= 0 {
		return ""
	}
	return fmt.Sprintf("~%d tokens pruned (%.1f%% reduction)", c.PrunedTokens, c.SavingsRatio*100)
}
