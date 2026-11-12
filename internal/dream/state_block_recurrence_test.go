package dream

import (
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func TestPitfallLinePrefersPromotedLesson(t *testing.T) {
	facts := []store.Fact{
		{ID: "l1", Kind: "lesson", Claim: "older draft lesson", PromotedAt: "2026-11-06T10:00:00Z"},
		{ID: "l2", Kind: "lesson", Claim: "promoted recurring pitfall", AutoPromoted: true, RecurrenceCount: 3, PromotedAt: "2026-11-06T09:00:00Z"},
	}
	if got := pitfallLine(facts); got != "promoted recurring pitfall" {
		t.Fatalf("pitfall = %q", got)
	}
}
