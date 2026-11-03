package dream

import (
	"testing"
	"time"
)

func TestFrecencyScoreDecaysWithAge(t *testing.T) {
	now := time.Now().UTC()
	fresh := frecencyScore(now.Format(time.RFC3339))
	week := frecencyScore(now.Add(-7 * 24 * time.Hour).Format(time.RFC3339))
	month := frecencyScore(now.Add(-30 * 24 * time.Hour).Format(time.RFC3339))
	year := frecencyScore(now.Add(-365 * 24 * time.Hour).Format(time.RFC3339))

	if fresh <= week || week <= month || month <= year {
		t.Fatalf("frecency must decay monotonically: fresh=%d week=%d month=%d year=%d", fresh, week, month, year)
	}
	if fresh > 10 {
		t.Fatalf("frecency = %d, want <= 10", fresh)
	}
	if year != 0 {
		t.Fatalf("year-old score = %d, want 0 after full decay", year)
	}
	if got := frecencyScore(""); got != 0 {
		t.Fatalf("empty timestamp score = %d, want 0", got)
	}
	if got := frecencyScore("not-a-timestamp"); got != 0 {
		t.Fatalf("invalid timestamp score = %d, want 0", got)
	}
}

func TestSortOmniItemsUsesFrecencyWithinEqualScore(t *testing.T) {
	items := []OmniResultItem{
		{Type: OmniTypeFact, ID: "old", Score: 3, Frecency: 1, Timestamp: "2026-01-01T00:00:00Z"},
		{Type: OmniTypeFact, ID: "fresh", Score: 3, Frecency: 9, Timestamp: "2026-09-01T00:00:00Z"},
	}
	sortOmniItems(items)
	if items[0].ID != "fresh" {
		t.Fatalf("first item = %q, want the fresher frecency tiebreak", items[0].ID)
	}
}
