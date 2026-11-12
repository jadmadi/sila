package dream

import (
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func TestSimilarity(t *testing.T) {
	if s := Similarity("add auth middleware to the router", "add auth middleware to the router"); s != 1.0 {
		t.Fatalf("identical => want 1.0, got %v", s)
	}
	if s := Similarity("completely unrelated topic windows knitting", "add auth middleware to the router"); s != 0.0 {
		t.Fatalf("disjoint => want 0, got %v", s)
	}
	if s := Similarity("fix the auth middleware timeout", "the auth middleware timed out"); s < 0.5 {
		t.Fatalf("overlapping wording should score well, got %v", s)
	}
}

func TestMatchOverlapPicksCrossToolWithinThreshold(t *testing.T) {
	near := []store.NearMessage{
		{ID: "h_own", Content: "add auth middleware to the router", Tools: []string{"devin"}},
		{ID: "h_mimo", Content: "add auth middleware to the router", Tools: []string{"mimocode"}},
		{ID: "h_off", Content: "totally unrelated topic windows", Tools: []string{"opencode"}},
	}
	got := MatchOverlap(near, "add auth middleware to the router now", "devin", 0.7)
	if got == nil || got.ID != "h_mimo" {
		t.Fatalf("want h_mimo cross-tool match, got %+v", got)
	}
}

func TestMatchOverlapSkipsOwnToolAndSubthreshold(t *testing.T) {
	near := []store.NearMessage{
		{ID: "a", Content: "add auth middleware", Tools: []string{"devin"}}, // own tool
		{ID: "b", Content: "the weather in paris", Tools: []string{"opencode"}},
	}
	if got := MatchOverlap(near, "add auth middleware now", "devin", 0.8); got != nil {
		t.Fatalf("own-tool + no cross-tool match should be nil, got %+v", got)
	}
}

func TestSimilarityDedupGuards(t *testing.T) {
	reject := [][2]string{
		{"Fix ADR 0011 migration", "Fix ADR 0013 migration"},
		{"Stage 1 rollout", "Stage 2 rollout"},
		{"Enable read-only mode in worker", "Disable read-only mode in worker"},
		{"Asset Contribution Flow", "Asset Consumption Flow"},
	}
	for _, pair := range reject {
		if got := Similarity(pair[0], pair[1]); got >= 0.60 {
			t.Fatalf("Similarity(%q, %q) = %v, want < 0.60", pair[0], pair[1], got)
		}
	}

	if got := Similarity("fix authentication flow", "fix authentification flow"); got < 0.60 {
		t.Fatalf("genuine typo similarity = %v, want >= 0.60", got)
	}
	if got := Similarity("fix the parser", "fix parser"); got < 0.60 {
		t.Fatalf("stopword edit similarity = %v, want >= 0.60", got)
	}
}
