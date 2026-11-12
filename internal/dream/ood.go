package dream

import (
	"sort"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

// OODVerdict is the out-of-domain read guard result.
type OODVerdict struct {
	OutOfDomain bool     `json:"out_of_domain"`
	Reason      string   `json:"reason,omitempty"`
	Missed      int      `json:"missed_terms,omitempty"`
	Hint        string   `json:"hint,omitempty"`
	Terms       []string `json:"terms,omitempty"`
}

// EvaluateOOD checks whether a query has any lexical foothold in the project.
// It fails open on errors or when it exceeds its time budget.
func EvaluateOOD(st *store.Store, projectSel, query string, theta float64) OODVerdict {
	start := time.Now()
	if st == nil {
		return OODVerdict{}
	}
	projectID, err := resolveOODProject(st, projectSel)
	if err != nil || projectID == "" {
		return OODVerdict{}
	}
	terms := oodTerms(query)
	if len(terms) == 0 {
		return OODVerdict{}
	}
	missed := 0
	for _, term := range terms {
		if time.Since(start) > 10*time.Millisecond {
			return OODVerdict{}
		}
		df, dfErr := st.DocumentFrequency(projectID, term)
		if dfErr != nil {
			return OODVerdict{}
		}
		if df == 0 {
			missed++
		}
	}
	verdict := OODVerdict{Terms: terms, Missed: missed, Hint: "read the live source files"}
	if missed == len(terms) {
		verdict.OutOfDomain = true
		verdict.Reason = "no_term_match"
		return verdict
	}
	bm25, found, err := st.BestFTSBM25(projectID, query)
	if err != nil || !found {
		return verdict
	}
	normalized := bm25 / (1 + bm25)
	if missed > 0 && normalized < theta {
		verdict.OutOfDomain = true
		verdict.Reason = "below_floor"
	}
	return verdict
}

func resolveOODProject(st *store.Store, projectSel string) (string, error) {
	if strings.TrimSpace(projectSel) == "" {
		return "", nil
	}
	ids, err := st.MatchingProjectIDs(projectSel)
	if err != nil || len(ids) == 0 {
		return "", err
	}
	return ids[0], nil
}

func oodTerms(query string) []string {
	tokens := store.TokenizeMemoryText(query)
	var terms []string
	for _, term := range tokens {
		if len(term) >= 4 {
			terms = append(terms, term)
		}
	}
	sort.SliceStable(terms, func(i, j int) bool {
		if len(terms[i]) == len(terms[j]) {
			return terms[i] < terms[j]
		}
		return len(terms[i]) > len(terms[j])
	})
	if len(terms) > 8 {
		terms = terms[:8]
	}
	return terms
}
