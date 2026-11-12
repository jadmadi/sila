package store

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// memoryStopwords is a fixed English set dropped by the tokenizer.
var memoryStopwords = map[string]bool{
	"the": true, "and": true, "for": true, "was": true, "were": true,
	"this": true, "that": true, "with": true, "from": true, "into": true,
	"but": true, "not": true, "are": true, "you": true, "your": true,
	"have": true, "has": true, "had": true, "will": true, "would": true,
	"can": true, "could": true, "should": true, "when": true, "then": true,
	"than": true, "them": true, "they": true, "our": true, "out": true,
	"all": true, "any": true, "its": true, "it's": true, "use": true,
	"used": true, "using": true, "via": true, "per": true, "also": true,
}

// TokenizeMemoryText lowercases, splits on non [a-z0-9_.-], drops short terms
// and stopwords, deduplicates, sorts, and caps at 64 terms.
func TokenizeMemoryText(text string) []string {
	lower := strings.ToLower(text)
	fields := strings.FieldsFunc(lower, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '_' && r != '.' && r != '-'
	})
	seen := map[string]bool{}
	terms := make([]string, 0, len(fields))
	for _, field := range fields {
		if len(field) < 3 || !strings.ContainsAny(field, "abcdefghijklmnopqrstuvwxyz0123456789") || memoryStopwords[field] || seen[field] {
			continue
		}
		seen[field] = true
		terms = append(terms, field)
		if len(terms) == 64 {
			break
		}
	}
	sort.Strings(terms)
	return terms
}

// TokenJaccard returns the set overlap of two token lists.
func TokenJaccard(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	setA := map[string]bool{}
	for _, term := range a {
		setA[term] = true
	}
	setB := map[string]bool{}
	for _, term := range b {
		setB[term] = true
	}
	intersection := 0
	union := map[string]bool{}
	for term := range setA {
		union[term] = true
		if setB[term] {
			intersection++
		}
	}
	for term := range setB {
		union[term] = true
	}
	if len(union) == 0 {
		return 0
	}
	return float64(intersection) / float64(len(union))
}

var dfCache = struct {
	sync.Mutex
	values map[string]int
}{values: map[string]int{}}

// ResetDocumentFrequencyCache clears the day-scoped frequency cache.
func ResetDocumentFrequencyCache() {
	dfCache.Lock()
	defer dfCache.Unlock()
	dfCache.values = map[string]int{}
}

// DocumentFrequency counts project facts matching one term, with a day-scoped
// in-process cache. It never creates tables and works on a read-only handle.
func (s *Store) DocumentFrequency(projectID, term string) (int, error) {
	term = strings.TrimSpace(strings.ToLower(term))
	if term == "" {
		return 0, nil
	}
	key := fmt.Sprintf("%s|%s|%s", projectID, term, time.Now().UTC().Format("2006-01-02"))
	dfCache.Lock()
	if value, ok := dfCache.values[key]; ok {
		dfCache.Unlock()
		return value, nil
	}
	dfCache.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	ftsQuery := FormatFTS5Query(term)
	if ftsQuery == "" {
		return 0, nil
	}
	var count int
	err := s.DB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM facts_fts
		JOIN facts f ON f.id = facts_fts.id
		WHERE facts_fts MATCH ? AND f.project_id = ?`, ftsQuery, projectID).Scan(&count)
	if err != nil {
		return 0, err
	}
	dfCache.Lock()
	dfCache.values[key] = count
	dfCache.Unlock()
	return count, nil
}

func symmetricDifference(a, b []string) []string {
	setA := map[string]bool{}
	for _, term := range a {
		setA[term] = true
	}
	setB := map[string]bool{}
	for _, term := range b {
		setB[term] = true
	}
	var diff []string
	for term := range setA {
		if !setB[term] {
			diff = append(diff, term)
		}
	}
	for term := range setB {
		if !setA[term] {
			diff = append(diff, term)
		}
	}
	sort.Strings(diff)
	return diff
}

// BestFTSBM25 returns the best bm25 magnitude for a query in a project.
func (s *Store) BestFTSBM25(projectID, query string) (float64, bool, error) {
	ftsQuery := FormatFTS5Query(query)
	if ftsQuery == "" {
		return 0, false, nil
	}
	var rank float64
	err := s.DB.QueryRow(`
		SELECT rank FROM facts_fts
		JOIN facts f ON f.id = facts_fts.id
		WHERE facts_fts MATCH ? AND f.project_id = ?
		ORDER BY rank LIMIT 1`, ftsQuery, projectID).Scan(&rank)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return 0, false, nil
		}
		return 0, false, err
	}
	if rank >= 0 {
		return 0, true, nil
	}
	return -rank, true, nil
}

var idfVetoDisabled atomic.Bool

// SetIDFVetoEnabled applies the config switch for the near-duplicate guard.
func SetIDFVetoEnabled(enabled bool) { idfVetoDisabled.Store(!enabled) }
