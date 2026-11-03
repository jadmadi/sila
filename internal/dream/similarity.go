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
	return 2 * float64(inter) / float64(len(ta)+len(tb))
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
