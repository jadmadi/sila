package learn

import (
	"strings"
	"unicode"
)

// Deduplication thresholds
const (
	similarityThreshold = 0.70
)

// Deduplicate merges colliding or semantically overlapping operational learnings
// across multiple repositories, boosting verified confidence when confirmed across 2+ projects.
func Deduplicate(learnings []OperationalLearning) []OperationalLearning {
	if len(learnings) <= 1 {
		return learnings
	}

	var merged []OperationalLearning

	for _, incoming := range learnings {
		matchedIdx := -1
		bestScore := 0.0

		for idx, existing := range merged {
			// Never merge across record kinds: a case has no symptom/cause/fix,
			// so a lesson merge would silently drop its intent/approach/invariant.
			if existing.EffectiveKind() != incoming.EffectiveKind() {
				continue
			}
			// Compare within same domain or general
			if existing.Domain != incoming.Domain && existing.Domain != DomainGeneral && incoming.Domain != DomainGeneral {
				continue
			}

			score := calculateSimilarity(existing, incoming)
			if score > similarityThreshold && score > bestScore {
				bestScore = score
				matchedIdx = idx
			}
		}

		if matchedIdx >= 0 {
			// Merge incoming into existing finding
			mergeFindings(&merged[matchedIdx], incoming)
		} else {
			// Normalize guardrail rule before adding
			incoming.GuardrailRule = HarmonizeRule(incoming.GuardrailRule)
			if incoming.SourceOccurrences <= 0 {
				incoming.SourceOccurrences = 1
			}
			merged = append(merged, incoming)
		}
	}

	return merged
}

// imperativeVerbs is the documented set of rule-initial action verbs. A rule
// that already starts with one is capitalized and otherwise left untouched; a
// noun phrase receives an "Ensure " prefix. "do not" is matched as two words.
var imperativeVerbs = []string{
	"ensure", "enforce", "always", "never", "do not", "avoid", "verify",
	"use", "enable", "disable", "prefer", "run", "set", "add", "remove",
	"keep", "validate", "configure", "require", "return", "wrap",
}

// startsWithImperative reports whether lower begins with a documented verb at
// a word boundary, so "settings" does not match "set" and "useful" does not
// match "use".
func startsWithImperative(lower string) bool {
	for _, verb := range imperativeVerbs {
		if lower == verb || strings.HasPrefix(lower, verb+" ") {
			return true
		}
	}
	return false
}

func capitalizeFirst(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return ""
	}
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// HarmonizeRule normalizes a guardrail rule at render time (stored data is
// never rewritten). The contract is:
//
//   - trim, and return "" for empty input;
//   - a rule already starting with a documented imperative verb is capitalized
//     and otherwise unchanged: "enable WAL..." -> "Enable WAL...";
//   - a redundant "Ensure " before another imperative is removed so the output
//     never reads "Ensure enable ..." or "Ensure Ensure ...";
//   - any remaining noun phrase is framed as a property: "explicit
//     dependencies" -> "Ensure explicit dependencies".
func HarmonizeRule(rule string) string {
	trimmed := strings.TrimSpace(rule)
	if trimmed == "" {
		return ""
	}
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "ensure ") {
		rest := strings.TrimSpace(trimmed[len("ensure "):])
		if rest == "" {
			return "Ensure"
		}
		if startsWithImperative(strings.ToLower(rest)) {
			return capitalizeFirst(rest)
		}
		return "Ensure " + rest
	}
	if startsWithImperative(lower) {
		return capitalizeFirst(trimmed)
	}
	return "Ensure " + trimmed
}

// mergeFindings combines two operational learnings, updating occurrences, tags, and confidence.
func mergeFindings(target *OperationalLearning, incoming OperationalLearning) {
	// If from distinct projects, boost confidence and count occurrences
	if incoming.ProjectID != "" && target.ProjectID != "" && incoming.ProjectID != target.ProjectID {
		target.Confidence = "verified"
	}

	incOcc := incoming.SourceOccurrences
	if incOcc <= 0 {
		incOcc = 1
	}
	target.SourceOccurrences += incOcc

	// Prefer verified confidence
	if incoming.Confidence == "verified" {
		target.Confidence = "verified"
	}

	// Merge unique tags
	tagSet := make(map[string]bool)
	for _, t := range target.Tags {
		norm := strings.ToLower(strings.TrimSpace(t))
		if norm != "" {
			tagSet[norm] = true
		}
	}
	for _, t := range incoming.Tags {
		norm := strings.ToLower(strings.TrimSpace(t))
		if norm != "" && !tagSet[norm] {
			target.Tags = append(target.Tags, t)
			tagSet[norm] = true
		}
	}

	// Keep richer content fields
	if len(incoming.RootCause) > len(target.RootCause) {
		target.RootCause = incoming.RootCause
	}
	if len(incoming.PivotalFix) > len(target.PivotalFix) {
		target.PivotalFix = incoming.PivotalFix
	}
	if len(incoming.GuardrailRule) > len(target.GuardrailRule) {
		target.GuardrailRule = HarmonizeRule(incoming.GuardrailRule)
	} else {
		target.GuardrailRule = HarmonizeRule(target.GuardrailRule)
	}

	// Prefer more specific domain over general
	if target.Domain == DomainGeneral && incoming.Domain != DomainGeneral {
		target.Domain = incoming.Domain
		target.TechStack = incoming.TechStack
	}

	// Prefer newer creation timestamp
	if incoming.CreatedAt.After(target.CreatedAt) {
		target.CreatedAt = incoming.CreatedAt
	}
}

// calculateSimilarity computes a weighted similarity score between two findings.
func calculateSimilarity(a, b OperationalLearning) float64 {
	// 1. Title/Symptom Jaccard similarity (weight: 0.50)
	titleScore := JaccardSimilarity(a.Title+" "+a.Symptom, b.Title+" "+b.Symptom)

	// 2. Root cause Jaccard similarity (weight: 0.30)
	causeScore := JaccardSimilarity(a.RootCause, b.RootCause)

	// 3. String LCS similarity across normalized titles (weight: 0.20)
	lcsScore := LCSSimilarity(a.Title, b.Title)

	return (0.50 * titleScore) + (0.30 * causeScore) + (0.20 * lcsScore)
}

// JaccardSimilarity calculates token set overlap between two text strings.
func JaccardSimilarity(textA, textB string) float64 {
	tokensA := tokenize(textA)
	tokensB := tokenize(textB)

	if len(tokensA) == 0 && len(tokensB) == 0 {
		return 1.0
	}
	if len(tokensA) == 0 || len(tokensB) == 0 {
		return 0.0
	}

	setA := make(map[string]bool, len(tokensA))
	for _, t := range tokensA {
		setA[t] = true
	}

	intersection := 0
	setB := make(map[string]bool, len(tokensB))
	for _, t := range tokensB {
		setB[t] = true
		if setA[t] {
			intersection++
		}
	}

	union := len(setA)
	for t := range setB {
		if !setA[t] {
			union++
		}
	}

	if union == 0 {
		return 0.0
	}
	return float64(intersection) / float64(union)
}

// LCSSimilarity calculates Longest Common Subsequence ratio between two strings:
// 2 * len(LCS) / (len(s1) + len(s2)).
func LCSSimilarity(s1, s2 string) float64 {
	clean1 := strings.ToLower(strings.TrimSpace(s1))
	clean2 := strings.ToLower(strings.TrimSpace(s2))

	l1 := len(clean1)
	l2 := len(clean2)
	if l1 == 0 && l2 == 0 {
		return 1.0
	}
	if l1 == 0 || l2 == 0 {
		return 0.0
	}

	lcsLen := lcsLength(clean1, clean2)
	return (2.0 * float64(lcsLen)) / float64(l1+l2)
}

func lcsLength(s1, s2 string) int {
	r1 := []rune(s1)
	r2 := []rune(s2)
	m := len(r1)
	n := len(r2)

	// Memory-efficient 2-row DP table
	prev := make([]int, n+1)
	curr := make([]int, n+1)

	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if r1[i-1] == r2[j-1] {
				curr[j] = prev[j-1] + 1
			} else {
				if prev[j] > curr[j-1] {
					curr[j] = prev[j]
				} else {
					curr[j] = curr[j-1]
				}
			}
		}
		copy(prev, curr)
	}

	return curr[n]
}

func stemWord(w string) string {
	if strings.HasSuffix(w, "ies") && len(w) > 4 {
		return strings.TrimSuffix(w, "ies") + "y"
	}
	if strings.HasSuffix(w, "ing") && len(w) > 5 {
		return strings.TrimSuffix(w, "ing")
	}
	if strings.HasSuffix(w, "ed") && len(w) > 4 {
		return strings.TrimSuffix(w, "ed")
	}
	if strings.HasSuffix(w, "s") && len(w) > 3 && !strings.HasSuffix(w, "ss") {
		return strings.TrimSuffix(w, "s")
	}
	return w
}

func tokenize(text string) []string {
	var tokens []string
	var current strings.Builder

	for _, r := range strings.ToLower(text) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			current.WriteRune(r)
		} else {
			if current.Len() > 0 {
				tok := stemWord(current.String())
				if len(tok) > 1 && !isStopWord(tok) {
					tokens = append(tokens, tok)
				}
				current.Reset()
			}
		}
	}
	if current.Len() > 0 {
		tok := stemWord(current.String())
		if len(tok) > 1 && !isStopWord(tok) {
			tokens = append(tokens, tok)
		}
	}
	return tokens
}

func isStopWord(w string) bool {
	switch w {
	case "a", "an", "the", "and", "or", "in", "on", "at", "to", "for", "with",
		"by", "of", "is", "are", "was", "were", "be", "been", "from", "that", "this",
		"into", "inside", "as", "it", "its":
		return true
	default:
		return false
	}
}
