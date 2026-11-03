package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestObservabilityNilStore(t *testing.T) {
	var s *Store
	counts, total, err := s.RecentDailySessionActivity("", 7)
	if err != nil || len(counts) != 7 || total != 0 {
		t.Fatalf("unexpected result on nil store: counts=%v, total=%d, err=%v", counts, total, err)
	}

	claims, grounded, tc, tg, err := s.DailyFactTrend("", 7)
	if err != nil || len(claims) != 7 || len(grounded) != 7 || tc != 0 || tg != 0 {
		t.Fatalf("unexpected result on nil store: claims=%v, grounded=%v, tc=%d, tg=%d, err=%v", claims, grounded, tc, tg, err)
	}

	trend, pages, bytes, err := s.DailyStorageGrowthTrend(7)
	if err != nil || len(trend) != 7 || pages != 0 || bytes != 0 {
		t.Fatalf("unexpected result on nil store: trend=%v, pages=%d, bytes=%d, err=%v", trend, pages, bytes, err)
	}
}

func TestObservabilityDailyTrends(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "obs_test.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	today := now.Format("2006-01-02")
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")

	// Insert test sessions using WriteRun
	tToday := now
	tYesterday := now.AddDate(0, 0, -1)

	_, _, err = st.WriteRun("", "claude-code", "sess1", []LoadMessage{
		{
			ProjectID:   "proj1",
			ContentHash: "hash1",
			TS:          today + "T10:00:00Z",
			Role:        "user",
			Content:     "hello today",
			Tool:        "claude-code",
			SessionID:   "sess1",
			SourceOrder: 1,
			SourceTime:  SortableTime(tToday),
		},
	})
	if err != nil {
		t.Fatalf("WriteRun sess1: %v", err)
	}

	_, _, err = st.WriteRun("", "claude-code", "sess2", []LoadMessage{
		{
			ProjectID:   "proj1",
			ContentHash: "hash2",
			TS:          yesterday + "T10:00:00Z",
			Role:        "assistant",
			Content:     "world yesterday",
			Tool:        "claude-code",
			SessionID:   "sess2",
			SourceOrder: 1,
			SourceTime:  SortableTime(tYesterday),
		},
	})
	if err != nil {
		t.Fatalf("WriteRun sess2: %v", err)
	}

	// Insert facts: 1 grounded (verified=1), 1 ungrounded (verified=0)
	f1 := Fact{
		ID:         "fact1",
		ProjectID:  "proj1",
		Claim:      "Test grounded claim",
		Confidence: "0.9",
		Verified:   true,
		FirstSeen:  today + "T10:00:00Z",
	}
	if err := st.UpsertFact(f1); err != nil {
		t.Fatalf("UpsertFact: %v", err)
	}

	f2 := Fact{
		ID:         "fact2",
		ProjectID:  "proj1",
		Claim:      "Test ungrounded claim",
		Confidence: "0.5",
		Verified:   false,
		FirstSeen:  yesterday + "T10:00:00Z",
	}
	if err := st.UpsertFact(f2); err != nil {
		t.Fatalf("UpsertFact: %v", err)
	}

	// 1. Session activity trend
	counts, total, err := st.RecentDailySessionActivity("proj1", 7)
	if err != nil {
		t.Fatalf("RecentDailySessionActivity: %v", err)
	}
	if len(counts) != 7 {
		t.Fatalf("expected 7 days, got %d", len(counts))
	}
	if total != 2 {
		t.Fatalf("expected total 2 sessions, got %d (counts: %v)", total, counts)
	}
	if counts[6] != 1 || counts[5] != 1 {
		t.Errorf("expected counts on last two days, got %v", counts)
	}

	// 2. Fact trend
	claims, grounded, totalClaims, totalGrounded, err := st.DailyFactTrend("proj1", 7)
	if err != nil {
		t.Fatalf("DailyFactTrend: %v", err)
	}
	if totalClaims != 2 || totalGrounded != 1 {
		t.Errorf("expected 2 claims, 1 grounded; got %d claims, %d grounded", totalClaims, totalGrounded)
	}
	if claims[6] != 1 || grounded[6] != 1 {
		t.Errorf("expected today claim to be verified, got claims=%v, grounded=%v", claims, grounded)
	}
	if claims[5] != 1 || grounded[5] != 0 {
		t.Errorf("expected yesterday claim to be unverified, got claims=%v, grounded=%v", claims, grounded)
	}

	// 3. Storage growth trend
	trend, pages, bytes, err := st.DailyStorageGrowthTrend(7)
	if err != nil {
		t.Fatalf("DailyStorageGrowthTrend: %v", err)
	}
	if len(trend) != 7 {
		t.Errorf("expected 7 trend values, got %d", len(trend))
	}
	if pages <= 0 || bytes <= 0 {
		t.Errorf("expected positive pages and bytes, got pages=%d, bytes=%d", pages, bytes)
	}
}
