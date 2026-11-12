package dream

import (
	"strings"
	"testing"
)

func compactFixture(n int) []RecallHit {
	hits := make([]RecallHit, 0, n)
	for i := 0; i < n; i++ {
		hits = append(hits, RecallHit{
			Kind:       "lesson",
			ID:         "hit-" + twoDigits(i),
			Symptom:    "symptom number " + twoDigits(i),
			Resolution: "resolution body that must not appear in compact rows",
			Timestamp:  "2026-11-06T10:00:00Z",
			Score:      float64(100 - i),
		})
	}
	return hits
}

func twoDigits(n int) string {
	if n < 10 {
		return "0" + string(rune('0'+n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

func TestCompactHitsCaps(t *testing.T) {
	result := CompactHits(compactFixture(30), 0, 0)
	if len(result.Hits) != CompactMaxRows {
		t.Fatalf("rows = %d", len(result.Hits))
	}
	if !result.Truncated {
		t.Fatal("30 hits did not report truncation")
	}
	if result.Tokens > CompactMaxTokens {
		t.Fatalf("tokens = %d", result.Tokens)
	}
	for _, row := range result.Hits {
		if strings.Contains(row.Title, "resolution body") || row.Tokens == 0 {
			t.Fatalf("row = %+v", row)
		}
	}
}

func TestCompactHitsOrderingMatchesRanking(t *testing.T) {
	hits := compactFixture(10)
	result := CompactHits(hits, 0, 0)
	for i, row := range result.Hits {
		if row.ID != hits[i].ID {
			t.Fatalf("row %d = %s, want %s", i, row.ID, hits[i].ID)
		}
		if row.Score != hits[i].Score || row.Kind != hits[i].Kind {
			t.Fatalf("row %d metadata mismatch: %+v", i, row)
		}
	}
	equal := []RecallHit{{ID: "b", Score: 1}, {ID: "a", Score: 1}}
	ordered := CompactHits(equal, 0, 0)
	if ordered.Hits[0].ID != "a" || ordered.Hits[1].ID != "b" {
		t.Fatalf("tie order = %+v", ordered.Hits)
	}
}

func TestCompactTitleTruncatesOnRuneBoundary(t *testing.T) {
	long := strings.Repeat("é", 3000)
	result := CompactHits([]RecallHit{{ID: "x", Symptom: long, Score: 2}}, 0, 0)
	title := result.Hits[0].Title
	runes := []rune(title)
	if len(runes) != CompactTitleRunes+3 {
		t.Fatalf("title runes = %d", len(runes))
	}
	if !strings.HasSuffix(title, "...") {
		t.Fatalf("title does not end with ellipsis: %q", title)
	}
}

func TestCompactHitsEmpty(t *testing.T) {
	result := CompactHits(nil, 0, 0)
	if len(result.Hits) != 0 || result.Truncated || result.Tokens != 0 {
		t.Fatalf("result = %+v", result)
	}
	if result.Hits == nil {
		t.Fatal("empty result must be an empty slice, not nil")
	}
}

func TestCompactBudgetTruncates(t *testing.T) {
	result := CompactHits(compactFixture(30), 25, 60)
	if !result.Truncated || len(result.Hits) == 0 || result.Tokens > 60 {
		t.Fatalf("result = %+v", result)
	}
}
