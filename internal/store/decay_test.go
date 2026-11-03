package store

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestDecayRetentionAnalyticalVectors(t *testing.T) {
	params := DefaultDecayParams()

	// 1. Fresh memory (age 0, access 0): retention == salience (1.0)
	scoreFresh := CalculateRetentionScore(TierEpisodic, 1.0, 0, 0, 0, 1, params)
	if math.Abs(scoreFresh-1.0) > 1e-6 {
		t.Fatalf("expected fresh retention 1.0, got %f", scoreFresh)
	}

	// 2. Working tier (half-life 1d): at age 1d, retention == 0.5
	scoreWorking1d := CalculateRetentionScore(TierWorking, 1.0, 1.0, 1.0, 0, 1, params)
	if math.Abs(scoreWorking1d-0.5) > 1e-4 {
		t.Fatalf("expected Working tier at 1d to be 0.5, got %f", scoreWorking1d)
	}

	// 3. Episodic tier (half-life 35d): at age 35d, retention == 0.5
	scoreEpisodic35d := CalculateRetentionScore(TierEpisodic, 1.0, 35.0, 35.0, 0, 1, params)
	if math.Abs(scoreEpisodic35d-0.5) > 1e-4 {
		t.Fatalf("expected Episodic tier at 35d to be 0.5, got %f", scoreEpisodic35d)
	}

	// 4. Procedural tier (half-life 90d): at age 90d, retention == 0.5
	scoreProcedural90d := CalculateRetentionScore(TierProcedural, 1.0, 90.0, 90.0, 0, 1, params)
	if math.Abs(scoreProcedural90d-0.5) > 1e-4 {
		t.Fatalf("expected Procedural tier at 90d to be 0.5, got %f", scoreProcedural90d)
	}

	// 5. Semantic tier (strictly zero decay): at age 1000d, retention == 1.0
	scoreSemantic1000d := CalculateRetentionScore(TierSemantic, 1.0, 1000.0, 1000.0, 0, 1, params)
	if math.Abs(scoreSemantic1000d-1.0) > 1e-6 {
		t.Fatalf("expected Semantic tier at 1000d to be 1.0, got %f", scoreSemantic1000d)
	}
}

func TestDecayAccessReinforcement(t *testing.T) {
	params := DefaultDecayParams()

	// Compare same age (35d) with 0 access vs 5 accesses vs 50 accesses
	zeroAccess := CalculateRetentionScore(TierEpisodic, 1.0, 35.0, 35.0, 0, 1, params)
	lowAccess := CalculateRetentionScore(TierEpisodic, 1.0, 35.0, 1.0, 5, 1, params)
	highAccess := CalculateRetentionScore(TierEpisodic, 1.0, 35.0, 1.0, 50, 1, params)

	if lowAccess <= zeroAccess {
		t.Fatalf("expected lowAccess (%f) > zeroAccess (%f)", lowAccess, zeroAccess)
	}
	if highAccess <= lowAccess {
		t.Fatalf("expected highAccess (%f) > lowAccess (%f)", highAccess, lowAccess)
	}

	// Verify logarithmic dampening: doubling access from 50 to 100 should not double the score
	veryHighAccess := CalculateRetentionScore(TierEpisodic, 1.0, 35.0, 1.0, 100, 1, params)
	diff1 := highAccess - lowAccess
	diff2 := veryHighAccess - highAccess
	if diff2 >= diff1 {
		t.Fatalf("expected diminishing returns: diff2 (%f) should be < diff1 (%f)", diff2, diff1)
	}
}

func TestDecayMultiActorBreadthAmplification(t *testing.T) {
	params := DefaultDecayParams()

	// Same age (35d), same access count (10), same days since access (2.0)
	// but 1 tool vs 2 tools vs 4 tools
	singleTool := CalculateRetentionScore(TierEpisodic, 1.0, 35.0, 2.0, 10, 1, params)
	twoTools := CalculateRetentionScore(TierEpisodic, 1.0, 35.0, 2.0, 10, 2, params)
	fourTools := CalculateRetentionScore(TierEpisodic, 1.0, 35.0, 2.0, 10, 4, params)

	if twoTools <= singleTool {
		t.Fatalf("expected twoTools (%f) > singleTool (%f)", twoTools, singleTool)
	}
	if fourTools <= twoTools {
		t.Fatalf("expected fourTools (%f) > twoTools (%f)", fourTools, twoTools)
	}

	// Verify exact breadth calculation:
	// breadth(2 tools) = 1 + 0.35 * ln(1 + (2-1)) = 1 + 0.35 * ln(2) ~= 1.2426
	// reinforcement portion must scale by exactly breadth ratio
	base := 1.0 * math.Exp(-TierLambda(TierEpisodic)*35.0)
	reinfSingle := singleTool - base
	reinfTwo := twoTools - base
	expectedBreadth2 := 1.0 + 0.35*math.Log(2.0)
	ratio := reinfTwo / reinfSingle
	if math.Abs(ratio-expectedBreadth2) > 1e-4 {
		t.Fatalf("expected reinforcement breadth scaling %f, got %f", expectedBreadth2, ratio)
	}
}

func TestDecaySalienceClamping(t *testing.T) {
	if got := ClampSalience(-5.0); got != 1.0 {
		t.Fatalf("expected baseline 1.0 for negative, got %f", got)
	}
	if got := ClampSalience(0.0); got != 1.0 {
		t.Fatalf("expected baseline 1.0 for zero, got %f", got)
	}
	if got := ClampSalience(0.10); got != 0.25 {
		t.Fatalf("expected floor 0.25, got %f", got)
	}
	if got := ClampSalience(1.5); got != 1.5 {
		t.Fatalf("expected 1.5, got %f", got)
	}
	if got := ClampSalience(3.5); got != 2.0 {
		t.Fatalf("expected ceiling 2.0, got %f", got)
	}
}

func TestCalculateFactRetentionHelper(t *testing.T) {
	now := time.Now().UTC()
	params := DefaultDecayParams()

	// Semantic invariant fact: always >= 1.0
	fSemantic := Fact{
		ID:         "fact_semantic",
		Claim:      "decision: SQLite WAL mode is mandatory",
		Kind:       "invariant",
		FirstSeen:  now.Add(-365 * 24 * time.Hour).Format(time.RFC3339),
		Salience:   1.0,
		Confidence: "invariant",
	}
	retSemantic := CalculateFactRetention(fSemantic, now, params)
	if retSemantic < 1.0 {
		t.Fatalf("expected semantic fact retention >= 1.0, got %f", retSemantic)
	}

	// Episodic fact 70 days old without access: decay through two half-lives (0.25)
	fEpisodic := Fact{
		ID:         "fact_episodic",
		Claim:      "Temporary bug in build",
		FirstSeen:  now.Add(-70 * 24 * time.Hour).Format(time.RFC3339),
		Tier:       "episodic",
		Salience:   1.0,
		Confidence: "grounded",
	}
	retEpisodic := CalculateFactRetention(fEpisodic, now, params)
	if math.Abs(retEpisodic-0.25) > 0.05 {
		t.Fatalf("expected episodic retention near 0.25, got %f", retEpisodic)
	}
}

func TestDecayMigration25Schema(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "decay_test.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer st.Close()

	var uv int
	if err := st.DB.QueryRow("PRAGMA user_version").Scan(&uv); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if uv < 25 {
		t.Fatalf("expected user_version >= 25, got %d", uv)
	}

	// Verify columns exist on facts
	cols := []string{"access_count", "last_accessed_at", "distinct_tools", "tier", "salience", "is_stale"}
	for _, col := range cols {
		var cnt int
		err := st.DB.QueryRow(fmt.Sprintf("SELECT count(*) FROM pragma_table_info('facts') WHERE name='%s'", col)).Scan(&cnt)
		if err != nil || cnt == 0 {
			t.Fatalf("expected column %s on facts table, err: %v", col, err)
		}
	}

	// Verify fact_tool_access table exists
	var tableCnt int
	err = st.DB.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='fact_tool_access'`).Scan(&tableCnt)
	if err != nil || tableCnt == 0 {
		t.Fatalf("expected fact_tool_access table, err: %v", err)
	}
}

func TestBumpFactAccessSyncAndCooldown(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "bump_test.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer st.Close()

	f := Fact{
		ID:            "fact_bump_1",
		ProjectID:     "test_proj",
		Claim:         "Test bump claim",
		Confidence:    "grounded",
		ReviewStatus:  ReviewApproved,
		FileRefsExist: true,
		Tools:         []string{"claude"},
	}
	if err := st.UpsertFact(f); err != nil {
		t.Fatalf("UpsertFact failed: %v", err)
	}

	ResetAccessCooldownForTest()
	ctx := context.Background()

	// Initial bump from Claude
	if err := st.BumpFactAccessSync(ctx, f.ID, "claude"); err != nil {
		t.Fatalf("BumpFactAccessSync 1 failed: %v", err)
	}

	// Retrieve updated fact
	facts, err := st.FactsByProject("test_proj")
	if err != nil || len(facts) != 1 {
		t.Fatalf("FactsByProject failed: %v", err)
	}
	if facts[0].AccessCount != 1 {
		t.Fatalf("expected access_count 1, got %d", facts[0].AccessCount)
	}
	if facts[0].DistinctTools != 1 {
		t.Fatalf("expected distinct_tools 1, got %d", facts[0].DistinctTools)
	}

	// Second bump with same tool within cooldown is throttled via BumpFactAccess
	if err := st.BumpFactAccess(ctx, f.ID, "claude"); err != nil {
		t.Fatalf("BumpFactAccess failed: %v", err)
	}
	// Verify count is still 1
	facts2, _ := st.FactsByProject("test_proj")
	if facts2[0].AccessCount != 1 {
		t.Fatalf("expected access_count still 1 due to cooldown, got %d", facts2[0].AccessCount)
	}

	// Bump with a SECOND distinct tool (devin)
	ResetAccessCooldownForTest()
	if err := st.BumpFactAccessSync(ctx, f.ID, "devin"); err != nil {
		t.Fatalf("BumpFactAccessSync devin failed: %v", err)
	}
	facts3, _ := st.FactsByProject("test_proj")
	if facts3[0].AccessCount != 2 {
		t.Fatalf("expected access_count 2, got %d", facts3[0].AccessCount)
	}
	if facts3[0].DistinctTools != 2 {
		t.Fatalf("expected distinct_tools 2, got %d", facts3[0].DistinctTools)
	}
}

func TestBumpFactAccessConcurrency(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "concurrency_test.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer st.Close()

	// Seed 5 facts
	for i := 1; i <= 5; i++ {
		f := Fact{
			ID:            fmt.Sprintf("fact_c_%d", i),
			ProjectID:     "conc_proj",
			Claim:         fmt.Sprintf("Concurrent fact %d", i),
			ReviewStatus:  ReviewApproved,
			FileRefsExist: true,
		}
		if err := st.UpsertFact(f); err != nil {
			t.Fatalf("UpsertFact failed: %v", err)
		}
	}

	ResetAccessCooldownForTest()
	var wg sync.WaitGroup
	workers := 20
	iterations := 50

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			tool := fmt.Sprintf("tool_%d", workerID%4)
			for it := 0; it < iterations; it++ {
				factID := fmt.Sprintf("fact_c_%d", (it%5)+1)
				_ = st.BumpFactAccess(context.Background(), factID, tool)
			}
		}(w)
	}
	wg.Wait()
}

func TestRecordFactFeedback(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "feedback_test.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer st.Close()

	f := Fact{
		ID:            "fact_fb_1",
		ProjectID:     "fb_proj",
		Claim:         "Test feedback salience",
		ReviewStatus:  ReviewApproved,
		FileRefsExist: true,
		Salience:      1.0,
	}
	if err := st.UpsertFact(f); err != nil {
		t.Fatalf("UpsertFact failed: %v", err)
	}

	ctx := context.Background()

	// 1. Helpful: +0.25 -> 1.25
	if err := st.RecordFactFeedback(ctx, f.ID, "helpful"); err != nil {
		t.Fatalf("RecordFactFeedback helpful failed: %v", err)
	}
	facts, _ := st.FactsByProject("fb_proj")
	if math.Abs(facts[0].Salience-1.25) > 1e-4 {
		t.Fatalf("expected salience 1.25, got %f", facts[0].Salience)
	}

	// 2. Not helpful: -0.25 -> 1.00
	if err := st.RecordFactFeedback(ctx, f.ID, "not_helpful"); err != nil {
		t.Fatalf("RecordFactFeedback not_helpful failed: %v", err)
	}
	facts, _ = st.FactsByProject("fb_proj")
	if math.Abs(facts[0].Salience-1.00) > 1e-4 {
		t.Fatalf("expected salience 1.00, got %f", facts[0].Salience)
	}

	// 3. Stale: salience = 0.25 (floor) and is_stale = 1
	if err := st.RecordFactFeedback(ctx, f.ID, "stale"); err != nil {
		t.Fatalf("RecordFactFeedback stale failed: %v", err)
	}
	facts, _ = st.FactsByProject("fb_proj")
	if math.Abs(facts[0].Salience-0.25) > 1e-4 {
		t.Fatalf("expected salience 0.25, got %f", facts[0].Salience)
	}
	if !facts[0].IsStale() {
		t.Fatalf("expected fact to be marked stale after stale feedback")
	}

	// 4. SaveFeedback with FactID
	fb := &Feedback{
		ProjectID: "fb_proj",
		Agent:     "test_agent",
		Category:  "helpful",
		Content:   "This was actually helpful now",
		FactID:    f.ID,
	}
	if err := st.RecordFeedback(fb); err != nil {
		t.Fatalf("RecordFeedback with FactID failed: %v", err)
	}
	facts, _ = st.FactsByProject("fb_proj")
	if math.Abs(facts[0].Salience-0.50) > 1e-4 {
		t.Fatalf("expected salience 0.50 after helpful feedback, got %f", facts[0].Salience)
	}
}

func TestTopFactsRankingByRetention(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "top_facts_test.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()

	// Fact A: cold episodic fact, age 100 days, 0 access -> very low retention (~0.13)
	fA := Fact{
		ID:            "fact_cold",
		ProjectID:     "proj_rank",
		Claim:         "Cold claim from long ago",
		ReviewStatus:  ReviewApproved,
		FileRefsExist: true,
		FirstSeen:     now.Add(-100 * 24 * time.Hour).Format(time.RFC3339),
		Tier:          "episodic",
		Salience:      1.0,
	}
	// Fact B: fresh episodic fact, age 2 days, 10 accesses across 3 tools -> high retention
	fB := Fact{
		ID:             "fact_hot",
		ProjectID:      "proj_rank",
		Claim:          "Hot frequently referenced claim",
		ReviewStatus:   ReviewApproved,
		FileRefsExist:  true,
		FirstSeen:      now.Add(-2 * 24 * time.Hour).Format(time.RFC3339),
		LastAccessedAt: now.Format(time.RFC3339),
		AccessCount:    10,
		DistinctTools:  3,
		Tier:           "episodic",
		Salience:       1.0,
	}
	// Fact C: semantic invariant, age 200 days -> retention >= 1.0 (authority)
	fC := Fact{
		ID:           "fact_semantic_top",
		ProjectID:    "proj_rank",
		Claim:        "decision: Canonical architecture invariant",
		ReviewStatus: ReviewApproved,
		Kind:         "invariant",
		Confidence:   "invariant",
		FirstSeen:    now.Add(-200 * 24 * time.Hour).Format(time.RFC3339),
		Tier:         "semantic",
		Salience:     1.0,
	}

	_ = st.UpsertFact(fA)
	_ = st.UpsertFact(fB)
	_ = st.UpsertFact(fC)

	top, err := st.TopFactsForProject("proj_rank", 5)
	if err != nil {
		t.Fatalf("TopFactsForProject failed: %v", err)
	}
	if len(top) < 3 {
		t.Fatalf("expected at least 3 top facts, got %d", len(top))
	}

	// Invariant (authority) must come first
	if top[0].ID != "fact_semantic_top" {
		t.Fatalf("expected top[0] to be invariant fact_semantic_top, got %s", top[0].ID)
	}
	// Hot claim with higher retention must precede cold claim
	if top[1].ID != "fact_hot" {
		t.Fatalf("expected top[1] to be fact_hot, got %s", top[1].ID)
	}
	if top[2].ID != "fact_cold" {
		t.Fatalf("expected top[2] to be fact_cold, got %s", top[2].ID)
	}
}
