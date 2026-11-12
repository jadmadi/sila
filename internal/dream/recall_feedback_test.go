package dream

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

func feedbackRecallStore(t *testing.T) (*store.Store, store.Project) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "feedback-recall.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	project, err := st.EnsureProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cache.go"), []byte("package cache"), 0o644); err != nil {
		t.Fatal(err)
	}
	return st, project
}

func recallFeedbackClock() time.Time {
	return time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
}

func TestRecallFeedbackAdjustment(t *testing.T) {
	st, project := feedbackRecallStore(t)
	now := recallFeedbackClock()
	for _, fix := range []string{"use singleflight alpha", "use singleflight beta"} {
		if _, err := st.InsertLesson(project.ID, store.FormatLesson(
			"cache stampede symptom", "thundering herd", fix, "cache"), "agent", project.Path, []string{"cache.go"}); err != nil {
			t.Fatal(err)
		}
	}

	baseline, err := RecallSolutions(st, "cache stampede symptom", RecallOptions{
		Project: project.ID, Limit: 10, SkipRetrieval: true, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(baseline.Hits) < 2 {
		t.Fatalf("baseline hits = %d, want two lessons", len(baseline.Hits))
	}
	for _, hit := range baseline.Hits {
		if hit.MemoryID == "" {
			t.Fatalf("lesson hit %s has no memory id: %+v", hit.ID, hit)
		}
	}
	replay, err := RecallSolutions(st, "cache stampede symptom", RecallOptions{
		Project: project.ID, Limit: 10, SkipRetrieval: true, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if replay.Hits[0].ID != baseline.Hits[0].ID || replay.Hits[0].Score != baseline.Hits[0].Score {
		t.Fatalf("recall without feedback is not deterministic: %+v vs %+v", baseline.Hits[0], replay.Hits[0])
	}

	// Record a pack identity for the baseline items, then verify the second
	// lesson. Feedback must lift it above the tied first lesson.
	pack := BuildRecallPack(baseline.Hits, RecallPackDefaultTokens, RecallPackDefaultItems)
	if err := RecordPackRetrieval(st, &pack, RecallRetrievalOptions{
		Project: project.ID, Agent: "agent-a", Query: "cache stampede symptom", Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	if pack.RetrievalID == "" {
		t.Fatal("pack has no retrieval id")
	}
	target := baseline.Hits[1]
	if _, err := st.RecordMemoryFeedbackAt(context.Background(), store.MemoryFeedbackInput{
		ProjectID: project.ID, MemoryID: target.MemoryID, RetrievalID: pack.RetrievalID,
		Signal: store.MemoryFeedbackVerified, Agent: "agent-a",
	}, now); err != nil {
		t.Fatal(err)
	}

	adjusted, err := RecallSolutions(st, "cache stampede symptom", RecallOptions{
		Project: project.ID, Limit: 10, SkipRetrieval: true, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(adjusted.Hits) == 0 || adjusted.Hits[0].MemoryID != target.MemoryID {
		t.Fatalf("feedback did not reorder recall: %+v", adjusted.Hits)
	}
	if adjusted.Hits[0].Score <= baseline.Hits[1].Score {
		t.Fatalf("adjusted score %v did not exceed baseline %v", adjusted.Hits[0].Score, baseline.Hits[1].Score)
	}
	if len(adjusted.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", adjusted.Warnings)
	}
}

func TestRecallFeedbackTierClamp(t *testing.T) {
	st, project := feedbackRecallStore(t)
	now := recallFeedbackClock()
	if _, err := st.InsertLesson(project.ID, store.FormatLesson(
		"tier clamp symptom", "tier cause", "tier fix", "tier"), "agent", project.Path, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertMemo(project.ID, "tier clamp fact", "agent", "", nil); err != nil {
		t.Fatal(err)
	}
	baseline, err := RecallSolutions(st, "tier clamp", RecallOptions{
		Project: project.ID, Limit: 10, SkipRetrieval: true, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	pack := BuildRecallPack(baseline.Hits, RecallPackDefaultTokens, RecallPackDefaultItems)
	if err := RecordPackRetrieval(st, &pack, RecallRetrievalOptions{
		Project: project.ID, Agent: "agent-a", Query: "tier clamp", Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	var fact, lesson *RecallHit
	for i := range baseline.Hits {
		switch baseline.Hits[i].Kind {
		case "fact":
			fact = &baseline.Hits[i]
		case "lesson":
			lesson = &baseline.Hits[i]
		}
	}
	if fact == nil || lesson == nil || fact.MemoryID == "" {
		t.Fatalf("tier fixture hits: %+v", baseline.Hits)
	}
	for _, agent := range []string{"agent-a", "agent-b"} {
		if _, err := st.RecordMemoryFeedbackAt(context.Background(), store.MemoryFeedbackInput{
			ProjectID: project.ID, MemoryID: fact.MemoryID, RetrievalID: pack.RetrievalID,
			Signal: store.MemoryFeedbackVerified, Agent: agent,
		}, now); err != nil {
			t.Fatal(err)
		}
		if _, err := st.RecordMemoryFeedbackAt(context.Background(), store.MemoryFeedbackInput{
			ProjectID: project.ID, MemoryID: lesson.MemoryID, RetrievalID: pack.RetrievalID,
			Signal: store.MemoryFeedbackWrong, Agent: agent,
		}, now); err != nil {
			t.Fatal(err)
		}
	}

	adjusted, err := RecallSolutions(st, "tier clamp", RecallOptions{
		Project: project.ID, Limit: 10, SkipRetrieval: true, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, hit := range adjusted.Hits {
		switch hit.Kind {
		case "fact":
			if hit.Score < recallFactBase || hit.Score > recallFactBase+recallTierSpan {
				t.Fatalf("fact left its tier: %v", hit.Score)
			}
		case "lesson":
			if hit.Score < recallLessonBase || hit.Score > recallLessonBase+recallTierSpan {
				t.Fatalf("lesson left its tier: %v", hit.Score)
			}
		}
	}
	if len(adjusted.Hits) > 0 && adjusted.Hits[0].Kind != "lesson" {
		t.Fatalf("feedback promoted a fact above a lesson: %+v", adjusted.Hits)
	}
}

func TestRecallFeedbackReadFailure(t *testing.T) {
	st, project := feedbackRecallStore(t)
	now := recallFeedbackClock()
	if _, err := st.InsertLesson(project.ID, store.FormatLesson(
		"read failure symptom", "read failure cause", "read failure fix", "failure"), "agent", project.Path, nil); err != nil {
		t.Fatal(err)
	}
	baseline, err := RecallSolutions(st, "read failure", RecallOptions{
		Project: project.ID, Limit: 10, SkipRetrieval: true, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`DROP TABLE memory_feedback`); err != nil {
		t.Fatal(err)
	}
	degraded, err := RecallSolutions(st, "read failure", RecallOptions{
		Project: project.ID, Limit: 10, SkipRetrieval: true, Now: now,
	})
	if err != nil {
		t.Fatalf("recall failed instead of degrading: %v", err)
	}
	if len(degraded.Hits) != len(baseline.Hits) || degraded.Hits[0].Score != baseline.Hits[0].Score {
		t.Fatalf("degraded ranking changed: %+v vs %+v", degraded.Hits, baseline.Hits)
	}
	found := false
	for _, warning := range degraded.Warnings {
		if strings.Contains(warning, "feedback adjustment unavailable") {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing degradation warning: %v", degraded.Warnings)
	}
}

func TestRecallRetrievalRecorded(t *testing.T) {
	st, project := feedbackRecallStore(t)
	now := recallFeedbackClock()
	if _, err := st.InsertLesson(project.ID, store.FormatLesson(
		"retrieval recorded symptom", "cause", "fix", "ledger"), "agent", project.Path, nil); err != nil {
		t.Fatal(err)
	}
	res, err := RecallSolutions(st, "retrieval recorded", RecallOptions{
		Project: project.ID, Limit: 10, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Pack == nil || res.Pack.RetrievalID == "" {
		t.Fatalf("pack has no retrieval id: %+v", res.Pack)
	}
	row, err := st.MemoryRetrievalByID(res.Pack.RetrievalID)
	if err != nil {
		t.Fatal(err)
	}
	if row.ItemCount != len(res.Pack.Items) {
		t.Fatalf("ledger item count = %d, pack items = %d", row.ItemCount, len(res.Pack.Items))
	}
	items, err := st.MemoryRetrievalItems(res.Pack.RetrievalID)
	if err != nil {
		t.Fatal(err)
	}
	for i, item := range res.Pack.Items {
		if i >= len(items) {
			t.Fatalf("ledger has %d items, pack has %d", len(items), len(res.Pack.Items))
		}
		if items[i].MemoryID != item.MemoryID {
			t.Fatalf("ledger item %d = %q, pack = %q", i, items[i].MemoryID, item.MemoryID)
		}
	}

	empty, err := RecallSolutions(st, "", RecallOptions{Project: project.ID, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if empty.Pack == nil || empty.Pack.RetrievalID == "" {
		t.Fatalf("empty pack has no retrieval id: %+v", empty.Pack)
	}
	emptyRow, err := st.MemoryRetrievalByID(empty.Pack.RetrievalID)
	if err != nil {
		t.Fatal(err)
	}
	if emptyRow.ItemCount != 0 {
		t.Fatalf("empty retrieval item count = %d", emptyRow.ItemCount)
	}
}

func TestPrimePackRetrievalRecorded(t *testing.T) {
	st, project := feedbackRecallStore(t)
	if _, err := st.InsertLesson(project.ID, store.FormatLesson(
		"prime retrieval seam symptom", "cause", "fix", "prime"), "agent", project.Path, nil); err != nil {
		t.Fatal(err)
	}
	ctx := RecallContext{Task: "prime retrieval seam"}
	first, err := BuildRecallPackForContext(st, project.ID, project.Path, ctx, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildRecallPackForContext(st, project.ID, project.Path, ctx, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if first == nil || second == nil || first.RetrievalID == "" || second.RetrievalID == "" {
		t.Fatalf("prime packs have no retrieval ids: %+v / %+v", first, second)
	}
	if first.RetrievalID == second.RetrievalID {
		t.Fatal("prime reuse did not mint a new retrieval id")
	}
	if len(first.Items) != len(second.Items) {
		t.Fatalf("prime pack size changed: %d != %d", len(first.Items), len(second.Items))
	}
	for i := range first.Items {
		if first.Items[i].MemoryID != second.Items[i].MemoryID || first.Items[i].Score != second.Items[i].Score {
			t.Fatalf("prime ranking changed: %+v vs %+v", first.Items[i], second.Items[i])
		}
	}
	row, err := st.MemoryRetrievalByID(first.RetrievalID)
	if err != nil {
		t.Fatal(err)
	}
	if row.ItemCount != len(first.Items) {
		t.Fatalf("prime ledger item count = %d, pack items = %d", row.ItemCount, len(first.Items))
	}
}
