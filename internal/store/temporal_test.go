package store

import (
	"math"
	"path/filepath"
	"testing"
	"time"
)

func TestParseTemporalAnchor(t *testing.T) {
	refTime := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name        string
		query       string
		wantAnchor  bool
		wantClean   string
		wantOffsetD float64
		wantWindowD float64
	}{
		{
			name:        "earlier today",
			query:       "show me auth errors earlier today",
			wantAnchor:  true,
			wantClean:   "show me auth errors",
			wantOffsetD: 0.0,
			wantWindowD: 0.5,
		},
		{
			name:        "today",
			query:       "what changed today?",
			wantAnchor:  true,
			wantClean:   "what changed?",
			wantOffsetD: 0.0,
			wantWindowD: 1.0,
		},
		{
			name:        "yesterday",
			query:       "sqlite deadlock yesterday",
			wantAnchor:  true,
			wantClean:   "sqlite deadlock",
			wantOffsetD: 1.0,
			wantWindowD: 1.5,
		},
		{
			name:        "a couple of days ago",
			query:       "broken tests a couple of days ago",
			wantAnchor:  true,
			wantClean:   "broken tests",
			wantOffsetD: 2.0,
			wantWindowD: 2.0,
		},
		{
			name:        "3 days ago (numeric)",
			query:       "database migration 3 days ago",
			wantAnchor:  true,
			wantClean:   "database migration",
			wantOffsetD: 3.0,
			wantWindowD: 2.0, // max(2.0, 3*0.3 = 0.9) => 2.0
		},
		{
			name:        "10 days ago (large offset window)",
			query:       "reconcile drift 10 days ago",
			wantAnchor:  true,
			wantClean:   "reconcile drift",
			wantOffsetD: 10.0,
			wantWindowD: 3.0, // max(2.0, 10*0.3 = 3.0) => 3.0
		},
		{
			name:        "three days ago (word)",
			query:       "who touched store three days ago?",
			wantAnchor:  true,
			wantClean:   "who touched store?",
			wantOffsetD: 3.0,
			wantWindowD: 2.0,
		},
		{
			name:        "this week",
			query:       "summary of commits this week",
			wantAnchor:  true,
			wantClean:   "summary of commits",
			wantOffsetD: 0.0,
			wantWindowD: 4.0,
		},
		{
			name:        "last week",
			query:       "why did deploy fail last week?",
			wantAnchor:  true,
			wantClean:   "why did deploy fail?",
			wantOffsetD: 7.0,
			wantWindowD: 5.0,
		},
		{
			name:        "2 weeks ago",
			query:       "cloudflare worker config 2 weeks ago",
			wantAnchor:  true,
			wantClean:   "cloudflare worker config",
			wantOffsetD: 14.0,
			wantWindowD: 6.0,
		},
		{
			name:        "this month",
			query:       "performance benchmarks this month",
			wantAnchor:  true,
			wantClean:   "performance benchmarks",
			wantOffsetD: 0.0,
			wantWindowD: 15.0,
		},
		{
			name:        "last month",
			query:       "schema migrations last month",
			wantAnchor:  true,
			wantClean:   "schema migrations",
			wantOffsetD: 30.0,
			wantWindowD: 15.0,
		},
		{
			name:        "a month ago (word)",
			query:       "d1 database audit a month ago",
			wantAnchor:  true,
			wantClean:   "d1 database audit",
			wantOffsetD: 30.0,
			wantWindowD: 10.0,
		},
		{
			name:        "recently",
			query:       "recently fixed flaky tests",
			wantAnchor:  true,
			wantClean:   "fixed flaky tests",
			wantOffsetD: 2.0,
			wantWindowD: 7.0,
		},
		{
			name:        "no anchor present",
			query:       "how to use sqlite with modernc driver",
			wantAnchor:  false,
			wantClean:   "how to use sqlite with modernc driver",
			wantOffsetD: 0.0,
			wantWindowD: 0.0,
		},
		{
			name:        "empty query",
			query:       "",
			wantAnchor:  false,
			wantClean:   "",
			wantOffsetD: 0.0,
			wantWindowD: 0.0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			target, clean := ParseTemporalAnchor(tc.query, refTime)
			if !tc.wantAnchor {
				if target != nil {
					t.Fatalf("expected nil target, got %+v", target)
				}
				if clean != tc.wantClean {
					t.Fatalf("expected clean query %q, got %q", tc.wantClean, clean)
				}
				return
			}

			if target == nil {
				t.Fatalf("expected non-nil target for %q", tc.query)
			}
			if clean != tc.wantClean {
				t.Errorf("clean query mismatch: expected %q, got %q", tc.wantClean, clean)
			}

			expectedTargetDate := refTime.Add(-time.Duration(tc.wantOffsetD * 24 * float64(time.Hour)))
			if math.Abs(target.TargetDate.Sub(expectedTargetDate).Seconds()) > 1.0 {
				t.Errorf("target date mismatch: expected %v, got %v", expectedTargetDate, target.TargetDate)
			}
			if math.Abs(target.WindowDays-tc.wantWindowD) > 1e-4 {
				t.Errorf("window days mismatch: expected %f, got %f", tc.wantWindowD, target.WindowDays)
			}
		})
	}
}

func TestCalculateTemporalBoost(t *testing.T) {
	refTime := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	target := &TemporalTarget{
		TargetDate: refTime,
		WindowDays: 10.0,
		MaxBoost:   0.40,
		RawAnchor:  "test target",
	}

	// 1. Exact match on target date => 100% of MaxBoost (0.40)
	boostExact := CalculateTemporalBoost(refTime, target)
	if math.Abs(boostExact-0.40) > 1e-5 {
		t.Errorf("expected exact boost 0.40, got %f", boostExact)
	}

	// 2. Halfway inside window (5 days offset) => 50% of MaxBoost (0.20)
	halfDate := refTime.Add(5 * 24 * time.Hour)
	boostHalf := CalculateTemporalBoost(halfDate, target)
	if math.Abs(boostHalf-0.20) > 1e-5 {
		t.Errorf("expected half boost 0.20, got %f", boostHalf)
	}

	// 3. Exactly at window boundary (10 days offset) => 0% boost
	boundaryDate := refTime.Add(-10 * 24 * time.Hour)
	boostBound := CalculateTemporalBoost(boundaryDate, target)
	if math.Abs(boostBound-0.0) > 1e-5 {
		t.Errorf("expected boundary boost 0.0, got %f", boostBound)
	}

	// 4. Far outside window (30 days offset) => 0.0 boost
	farDate := refTime.Add(-30 * 24 * time.Hour)
	boostFar := CalculateTemporalBoost(farDate, target)
	if boostFar != 0.0 {
		t.Errorf("expected far boost 0.0, got %f", boostFar)
	}

	// 5. Zero date or nil target => 0.0 boost
	if b := CalculateTemporalBoost(time.Time{}, target); b != 0.0 {
		t.Errorf("expected zero date boost 0.0, got %f", b)
	}
	if b := CalculateTemporalBoost(refTime, nil); b != 0.0 {
		t.Errorf("expected nil target boost 0.0, got %f", b)
	}
}

func TestApplyTemporalBoost(t *testing.T) {
	score := 10.0
	boost := 0.40

	higher := ApplyTemporalBoostHigherBetter(score, boost)
	if math.Abs(higher-14.0) > 1e-5 {
		t.Errorf("expected higher score 14.0, got %f", higher)
	}

	lower := ApplyTemporalBoostLowerBetter(score, boost)
	if math.Abs(lower-6.0) > 1e-5 {
		t.Errorf("expected lower score 6.0, got %f", lower)
	}

	// Zero boost preserves score
	if ApplyTemporalBoostHigherBetter(score, 0.0) != score {
		t.Errorf("expected zero boost to preserve higher score")
	}
	if ApplyTemporalBoostLowerBetter(score, 0.0) != score {
		t.Errorf("expected zero boost to preserve lower score")
	}
}

func TestParseDocTimestamp(t *testing.T) {
	tsRFC3339 := "2026-09-30T12:00:00Z"
	t1, ok1 := ParseDocTimestamp(tsRFC3339)
	if !ok1 || t1.Year() != 2026 || t1.Day() != 30 {
		t.Errorf("failed to parse RFC3339: %v, %t", t1, ok1)
	}

	tsDateOnly := "2026-09-25"
	t2, ok2 := ParseDocTimestamp(tsDateOnly)
	if !ok2 || t2.Year() != 2026 || t2.Month() != 9 || t2.Day() != 25 {
		t.Errorf("failed to parse date-only: %v, %t", t2, ok2)
	}

	_, ok3 := ParseDocTimestamp("")
	if ok3 {
		t.Errorf("expected false for empty timestamp")
	}

	_, ok4 := ParseDocTimestamp("not a timestamp")
	if ok4 {
		t.Errorf("expected false for invalid timestamp")
	}
}

func TestTemporalDisambiguationMessagesAndFacts(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test_temporal_disambiguation.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	t3DaysAgo := now.Add(-3 * 24 * time.Hour).Format(time.RFC3339)
	t45DaysAgo := now.Add(-45 * 24 * time.Hour).Format(time.RFC3339)

	proj, err := st.EnsureProject(filepath.Join(tmp, "my-proj"))
	if err != nil {
		t.Fatalf("EnsureProject failed: %v", err)
	}

	// 1. Insert two messages with identical content: 3 days ago vs 45 days ago
	_, _, err = st.WriteRun("run-old", "claude-code", "sess-old", []LoadMessage{
		{ProjectID: proj.ID, ContentHash: "hash-old", TS: t45DaysAgo, Role: "assistant", Content: "investigating database migration errors and schema rollback"},
	})
	if err != nil {
		t.Fatalf("WriteRun old failed: %v", err)
	}

	_, _, err = st.WriteRun("run-recent", "claude-code", "sess-recent", []LoadMessage{
		{ProjectID: proj.ID, ContentHash: "hash-recent", TS: t3DaysAgo, Role: "assistant", Content: "investigating database migration errors and schema rollback"},
	})
	if err != nil {
		t.Fatalf("WriteRun recent failed: %v", err)
	}

	// Query: "database migration 3 days ago"
	msgs, err := st.SearchMessagesFTS("database migration 3 days ago", SearchMessagesOptions{Project: proj.ID})
	if err != nil {
		t.Fatalf("SearchMessagesFTS failed: %v", err)
	}
	if len(msgs) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(msgs))
	}
	if msgs[0].TS != t3DaysAgo {
		t.Errorf("expected 3-day-old message to rank first, got TS: %s (wanted %s)", msgs[0].TS, t3DaysAgo)
	}

	// 2. Insert two facts matching the query: 3 days ago vs 45 days ago
	if err := st.UpsertFact(Fact{
		ID:           "fact-old",
		ProjectID:    proj.ID,
		Claim:        "database migration requires schema lock and backup verification for staging",
		Confidence:   "high",
		FirstSeen:    t45DaysAgo,
		ReviewStatus: ReviewApproved,
	}); err != nil {
		t.Fatalf("UpsertFact old failed: %v", err)
	}

	if err := st.UpsertFact(Fact{
		ID:           "fact-recent",
		ProjectID:    proj.ID,
		Claim:        "database migration requires schema lock and backup verification for production",
		Confidence:   "high",
		FirstSeen:    t3DaysAgo,
		ReviewStatus: ReviewApproved,
	}); err != nil {
		t.Fatalf("UpsertFact recent failed: %v", err)
	}

	facts, err := st.SearchFactsFTS("database migration 3 days ago", proj.ID, false, 10)
	if err != nil {
		t.Fatalf("SearchFactsFTS failed: %v", err)
	}
	if len(facts) < 2 {
		t.Fatalf("expected at least 2 facts, got %d", len(facts))
	}
	if facts[0].ID != "fact-recent" {
		t.Errorf("expected fact-recent to rank first, got %s", facts[0].ID)
	}
}
