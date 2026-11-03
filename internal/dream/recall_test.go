package dream

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

func TestRecallSolutionsRanksLessonsFirst(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "recall.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	p1, err := st.EnsureProject(tmp)
	if err != nil {
		t.Fatal(err)
	}
	otherDir := filepath.Join(tmp, "other")
	if err := os.MkdirAll(otherDir, 0o755); err != nil {
		t.Fatal(err)
	}
	p2, err := st.EnsureProject(otherDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(otherDir, "export.ts"), []byte("stream rows"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A grounded lesson in the second project, a plain lesson in the first.
	if _, err := st.InsertLesson(p2.ID, store.FormatLesson("xlsx export hangs", "shared strings", "stream rows with ExcelJS", "xlsx"), "agent", otherDir, []string{"export.ts"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertLesson(p1.ID, store.FormatLesson("xlsx export hangs", "shared strings", "another fix", "xlsx"), "agent", tmp, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertMemo(p1.ID, "xlsx exports must stream rows", "agent", "", nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.WriteRun("", "opencode", "1", []store.LoadMessage{{
		ProjectID:   p1.ID,
		ContentHash: "xlsx-msg",
		TS:          "2026-09-19T10:00:00Z",
		Role:        "assistant",
		Content:     "The xlsx export hangs because shared strings are held in memory; streaming fixes it.",
		Tool:        "opencode",
	}}); err != nil {
		t.Fatal(err)
	}

	res, err := RecallSolutions(st, "xlsx export hangs", RecallOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) < 3 {
		t.Fatalf("hits = %d, want two lessons plus a claim and a transcript", len(res.Hits))
	}
	if res.Hits[0].Kind != "lesson" {
		t.Fatalf("first hit kind = %q, want lesson", res.Hits[0].Kind)
	}
	var groundedLesson *RecallHit
	for i := range res.Hits {
		if res.Hits[i].Kind == "lesson" && res.Hits[i].ProjectID == p2.ID {
			groundedLesson = &res.Hits[i]
		}
	}
	if groundedLesson == nil {
		t.Fatal("no lesson hit for the project with evidence")
	}
	if !groundedLesson.Grounded {
		t.Fatal("lesson hit is not grounded; evidence file exists")
	}
	if !strings.Contains(groundedLesson.SourceRef, "sila lesson show") {
		t.Fatalf("source ref = %q, want a lesson show hint", groundedLesson.SourceRef)
	}

	// Project boost: p2's lesson ranks first when p2 is selected.
	boosted, err := RecallSolutions(st, "xlsx export hangs", RecallOptions{Project: p2.ID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(boosted.Hits) == 0 || boosted.Hits[0].ProjectID != p2.ID {
		t.Fatalf("boosted first hit = %+v, want project %s", boosted.Hits[0], p2.ID)
	}

	// Empty and unmatched queries return valid empty results.
	empty, err := RecallSolutions(st, "", RecallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Hits) != 0 {
		t.Fatalf("empty query hits = %d, want 0", len(empty.Hits))
	}
	none, err := RecallSolutions(st, "kubernetes ingress tls", RecallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(none.Hits) != 0 {
		t.Fatalf("unrelated query hits = %d, want 0", len(none.Hits))
	}
}

func TestRecallSolutionsRanksCases(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "recall_cases.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	p1, err := st.EnsureProject(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "pool.go"), []byte("package pool"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Insert an architectural case
	caseContent := store.FormatCase("SQLite connection pooling", "single-writer WAL mode with busy timeout", "only one write transaction at a time", "sqlite,wal")
	if _, err := st.InsertCase(p1.ID, caseContent, "agent", tmp, []string{"pool.go"}); err != nil {
		t.Fatal(err)
	}

	// Insert a solution lesson
	lessonContent := store.FormatLesson("SQLite database locked", "concurrent write attempts", "use single connection pool with busy timeout", "sqlite,lock")
	if _, err := st.InsertLesson(p1.ID, lessonContent, "agent", tmp, []string{"pool.go"}); err != nil {
		t.Fatal(err)
	}

	res, err := RecallSolutions(st, "SQLite connection pooling WAL", RecallOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) == 0 {
		t.Fatal("expected hits, got 0")
	}

	foundCase := false
	for _, h := range res.Hits {
		if h.Kind == "case" {
			foundCase = true
			if h.Symptom != "SQLite connection pooling" {
				t.Fatalf("case intent = %q, want SQLite connection pooling", h.Symptom)
			}
			if h.Cause != "single-writer WAL mode with busy timeout" {
				t.Fatalf("case approach = %q", h.Cause)
			}
			if h.Fix != "only one write transaction at a time" {
				t.Fatalf("case invariant = %q", h.Fix)
			}
			if !h.Grounded {
				t.Fatal("case hit is not grounded; evidence file exists")
			}
			if !strings.Contains(h.SourceRef, "sila case show") {
				t.Fatalf("source ref = %q, want sila case show", h.SourceRef)
			}
		}
	}
	if !foundCase {
		t.Fatal("no case hit found in recall results")
	}

	// Verify categorization
	if len(res.Cases) == 0 {
		t.Fatal("res.Cases is empty, want categorized cases")
	}
}

func TestRecallSolutionsTemporalProximityBoost(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "recall_temporal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	p, err := st.EnsureProject(tmp)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	tYesterday := now.Add(-24 * time.Hour).Format(time.RFC3339)
	t30DaysAgo := now.Add(-30 * 24 * time.Hour).Format(time.RFC3339)

	// Older lesson (30 days ago)
	if err := st.UpsertFact(store.Fact{
		ID:           "lesson-old",
		ProjectID:    p.ID,
		Claim:        "LESSON: gopls crash on cyclic imports | CAUSE: recursive AST walking | FIX: break cycle with interface",
		Kind:         "lesson",
		Confidence:   "high",
		FirstSeen:    t30DaysAgo,
		ReviewStatus: store.ReviewApproved,
	}); err != nil {
		t.Fatal(err)
	}

	// Recent lesson (yesterday)
	if err := st.UpsertFact(store.Fact{
		ID:           "lesson-recent",
		ProjectID:    p.ID,
		Claim:        "LESSON: gopls crash on cyclic imports | CAUSE: recursive AST walking | FIX: update gopls to v0.18",
		Kind:         "lesson",
		Confidence:   "high",
		FirstSeen:    tYesterday,
		ReviewStatus: store.ReviewApproved,
	}); err != nil {
		t.Fatal(err)
	}

	// Query with temporal anchor: "gopls crash yesterday"
	res, err := RecallSolutions(st, "gopls crash yesterday", RecallOptions{Project: p.ID, Limit: 5})
	if err != nil {
		t.Fatalf("RecallSolutions failed: %v", err)
	}
	if len(res.Hits) < 2 {
		t.Fatalf("expected at least 2 hits, got %d", len(res.Hits))
	}

	// The lesson from yesterday must rank first
	if res.Hits[0].ID != "lesson-recent" {
		t.Errorf("expected lesson-recent to rank first, got %s", res.Hits[0].ID)
	}
	if res.Hits[0].Score <= res.Hits[1].Score {
		t.Errorf("expected recent hit score (%f) to exceed older hit score (%f)", res.Hits[0].Score, res.Hits[1].Score)
	}
}
