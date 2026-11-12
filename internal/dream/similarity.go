package dream

import (
	"slices"
	"strings"

	"github.com/jadmadi/sila/internal/store"
)

// tokenize lowercases and splits non-alphanumeric runs into a frequency map.
func tokenize(s string) map[string]int {
	out := map[string]int{}
	for w := range strings.FieldsSeq(strings.ToLower(s)) {
		w = strings.Trim(w, ".,;:!?()[]{}\"'`-")
		if w != "" {
			out[w]++
		}
	}
	return out
}

// Similarity is the Dice coefficient over token sets, returning 0..1. Two
// identical texts score 1.0; disjoint texts score 0.
func Similarity(a, b string) float64 {
	ta, tb := tokenize(a), tokenize(b)
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	inter := 0
	for w, n := range ta {
		if m, ok := tb[w]; ok {
			if n < m {
				inter += n
			} else {
				inter += m
			}
		}
	}
	score := 2 * float64(inter) / float64(len(ta)+len(tb))
	if score >= 0.5 && (numericTokensDiffer(a, b) || isSingleContentTokenSwap(a, b)) {
		return min(score, 0.55)
	}
	return score
}

// MatchOverlap returns the best cross-tool near-message whose similarity to
// content meets the threshold, ignoring messages already contributed by
// ourTool. Returns nil when nothing qualifies.
func MatchOverlap(near []store.NearMessage, content, ourTool string, threshold float64) *store.NearMessage {
	var best *store.NearMessage
	var bestSim float64
	for i := range near {
		nm := &near[i]
		if containsTool(nm.Tools, ourTool) {
			continue
		}
		s := Similarity(nm.Content, content)
		if s >= threshold && s > bestSim {
			best, bestSim = nm, s
		}
	}
	return best
}

func containsTool(tools []string, tool string) bool {
	return slices.Contains(tools, tool)
}

// numericTokensDiffer reports whether both texts carry numbers and their number
// sets differ, so ADR 0011 and ADR 0013 are not treated as the same record.
func numericTokensDiffer(a, b string) bool {
	na, nb := numericSets(a), numericSets(b)
	if len(na) == 0 || len(nb) == 0 || len(na) != len(nb) {
		return len(na) > 0 && len(nb) > 0 && len(na) != len(nb)
	}
	for n := range na {
		if !nb[n] {
			return true
		}
	}
	return false
}

func numericSets(s string) map[string]bool {
	out := map[string]bool{}
	for _, word := range guardWords(s) {
		if isAllDigits(word) {
			out[word] = true
		}
	}
	return out
}

// isSingleContentTokenSwap reports whether two same-length token sequences
// differ at exactly one non-stopword, non-numeric position with a low
// Jaro-Winkler score, such as enable/disable or contribution/consumption.
func isSingleContentTokenSwap(a, b string) bool {
	wa, wb := guardWords(a), guardWords(b)
	if len(wa) != len(wb) || len(wa) < 2 {
		return false
	}
	diff := 0
	var x, y string
	for i := range wa {
		if wa[i] != wb[i] {
			diff++
			x, y = wa[i], wb[i]
			if diff > 1 {
				return false
			}
		}
	}
	if diff != 1 || isGuardStopword(x) || isGuardStopword(y) || isAllDigits(x) || isAllDigits(y) {
		return false
	}
	if len(x) < 3 || len(y) < 3 {
		return false
	}
	return jaroWinkler(x, y) < 0.88
}

func guardWords(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-')
	})
}

func isGuardStopword(s string) bool {
	switch s {
	case "the", "a", "an", "in", "on", "at", "to", "for", "of", "and", "or",
		"is", "are", "was", "were", "be", "been", "being", "with", "by", "from",
		"as", "that", "this", "it", "its", "into", "over", "after", "before",
		"under", "up", "down", "out", "off", "then", "than", "so", "but", "not",
		"no", "do", "does", "did", "can", "could", "should", "would", "may",
		"might", "must", "will", "just", "also", "very", "more", "most", "less",
		"least":
		return true
	}
	return false
}

func jaroWinkler(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 || len(rb) == 0 {
		return 0
	}
	if string(ra) == string(rb) {
		return 1
	}
	distance := max(len(ra), len(rb))/2 - 1
	if distance < 0 {
		distance = 0
	}
	aMatched := make([]bool, len(ra))
	bMatched := make([]bool, len(rb))
	matches := 0
	for i := range ra {
		start := i - distance
		if start < 0 {
			start = 0
		}
		end := i + distance + 1
		if end > len(rb) {
			end = len(rb)
		}
		for j := start; j < end; j++ {
			if bMatched[j] || ra[i] != rb[j] {
				continue
			}
			aMatched[i] = true
			bMatched[j] = true
			matches++
			break
		}
	}
	if matches == 0 {
		return 0
	}
	transpositions := 0
	k := 0
	for i := range ra {
		if !aMatched[i] {
			continue
		}
		for !bMatched[k] {
			k++
		}
		if ra[i] != rb[k] {
			transpositions++
		}
		k++
	}
	m := float64(matches)
	jaro := (m/float64(len(ra)) + m/float64(len(rb)) + (m-float64(transpositions)/2)/m) / 3
	prefix := 0
	for prefix < len(ra) && prefix < len(rb) && prefix < 4 && ra[prefix] == rb[prefix] {
		prefix++
	}
	return jaro + float64(prefix)*0.1*(1-jaro)
}
