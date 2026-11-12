package dream

import (
	"context"
	"encoding/json"
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

func TestRecallContextRanking(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "recall_context.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p, err := st.EnsureProject(tmp)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"scripts/deploy.sh", "internal/unrelated.go"} {
		full := filepath.Join(tmp, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("fixture"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	relevant, err := st.InsertMemo(p.ID, "deploy ordering migration race", "agent", tmp, []string{"scripts/deploy.sh"})
	if err != nil {
		t.Fatal(err)
	}
	unrelated, err := st.InsertMemo(p.ID, "deploy ordering migration retry", "agent", tmp, []string{"internal/unrelated.go"})
	if err != nil {
		t.Fatal(err)
	}

	res, err := RecallSolutions(st, "deploy ordering", RecallOptions{
		Project:    p.ID,
		ProjectDir: tmp,
		Limit:      5,
		Context:    &RecallContext{Paths: []string{"scripts/deploy.sh"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) < 2 {
		t.Fatalf("hits = %d, want both memories", len(res.Hits))
	}
	if res.Hits[0].ID != relevant.ID {
		t.Fatalf("first hit = %s, want path-relevant %s", res.Hits[0].ID, relevant.ID)
	}
	base := res.Hits[1].Score
	boosted := res.Hits[0].Score
	if diff := boosted - base; diff < 50 || diff > 300 {
		t.Fatalf("context boost = %.0f, want a bounded positive boost", diff)
	}
	_ = unrelated
}

func TestRecallContextTierClamp(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "recall_tier.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	pSel, err := st.EnsureProject(tmp)
	if err != nil {
		t.Fatal(err)
	}
	otherDir := filepath.Join(tmp, "other")
	if err := os.MkdirAll(otherDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pOther, err := st.EnsureProject(otherDir)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, rel := range []string{"a.go", "b.go", "c.go", "d.go"} {
		if err := os.WriteFile(filepath.Join(tmp, rel), []byte("package x"), 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, rel)
	}
	if _, err := st.InsertMemo(pSel.ID, "deploy ordering migration race", "agent", tmp, files); err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertLesson(pOther.ID, store.FormatLesson("deploy ordering migration race", "shared state", "serialize writes", ""), "agent", otherDir, nil); err != nil {
		t.Fatal(err)
	}

	res, err := RecallSolutions(st, "deploy ordering", RecallOptions{
		Project:    pSel.ID,
		ProjectDir: tmp,
		Limit:      10,
		Context:    &RecallContext{Paths: files},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) < 2 {
		t.Fatalf("hits = %d, want a lesson and a fact", len(res.Hits))
	}
	if res.Hits[0].Kind != "lesson" {
		t.Fatalf("first hit = %s (score %.0f), a boosted fact crossed the lesson tier", res.Hits[0].Kind, res.Hits[0].Score)
	}
	for _, h := range res.Hits {
		if h.Kind == "fact" && h.Score > recallFactBase+recallTierSpan {
			t.Fatalf("fact score = %.0f, want <= %.0f", h.Score, recallFactBase+recallTierSpan)
		}
	}
}

func TestRecallWithoutContextUnchanged(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "recall_compat.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p, err := st.EnsureProject(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertMemo(p.ID, "deploy ordering migration race", "agent", "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertMemo(p.ID, "deploy ordering migration retry", "agent", "", nil); err != nil {
		t.Fatal(err)
	}

	first, err := RecallSolutions(st, "deploy ordering", RecallOptions{Project: p.ID, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	second, err := RecallSolutions(st, "deploy ordering", RecallOptions{Project: p.ID, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	// Each call records its own retrieval identity; the ranking and the pack
	// items must stay byte-identical once that per-call identity is cleared.
	if first.Pack == nil || first.Pack.RetrievalID == "" || second.Pack == nil || second.Pack.RetrievalID == "" {
		t.Fatalf("recall did not record retrieval identities: %+v / %+v", first.Pack, second.Pack)
	}
	first.Pack.RetrievalID = ""
	second.Pack.RetrievalID = ""
	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	if string(firstJSON) != string(secondJSON) {
		t.Fatal("identical requests returned different JSON")
	}

	withEmptyContext, err := RecallSolutions(st, "deploy ordering", RecallOptions{Project: p.ID, Limit: 5, Context: &RecallContext{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(withEmptyContext.Hits) != len(first.Hits) {
		t.Fatalf("empty context changed hit count: %d != %d", len(withEmptyContext.Hits), len(first.Hits))
	}
	for i := range first.Hits {
		if withEmptyContext.Hits[i].ID != first.Hits[i].ID || withEmptyContext.Hits[i].Score != first.Hits[i].Score {
			t.Fatalf("empty context changed hit %d: %+v != %+v", i, withEmptyContext.Hits[i], first.Hits[i])
		}
	}
}

func TestRecallEmptyPack(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "recall_empty_pack.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.EnsureProject(tmp); err != nil {
		t.Fatal(err)
	}
	res, err := RecallSolutions(st, "zebra quantum telescope", RecallOptions{Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if res.Pack == nil {
		t.Fatal("empty recall returned no pack")
	}
	if len(res.Pack.Items) != 0 {
		t.Fatalf("items = %d, want 0", len(res.Pack.Items))
	}
	if res.Pack.EmptyReason != RecallEmptyNoCandidates {
		t.Fatalf("empty reason = %q, want %q", res.Pack.EmptyReason, RecallEmptyNoCandidates)
	}
	if res.Pack.RankingVersion != RankingVersion {
		t.Fatalf("ranking version = %q", res.Pack.RankingVersion)
	}
}

func TestRecallContextSemanticTierBoundary(t *testing.T) {
	t.Setenv("SILA_ASSIST_ENABLED", "true")
	t.Setenv("SILA_ASSIST_PROVIDER", "mock")
	t.Setenv("SILA_ASSIST_SCOPES", "search,all")
	t.Setenv("SILA_ASSIST_PROJECTS", "*")

	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "recall_semantic.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	pSel, err := st.EnsureProject(tmp)
	if err != nil {
		t.Fatal(err)
	}
	otherDir := filepath.Join(tmp, "other")
	if err := os.MkdirAll(otherDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pOther, err := st.EnsureProject(otherDir)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, rel := range []string{"a.go", "b.go", "c.go", "d.go"} {
		if err := os.WriteFile(filepath.Join(tmp, rel), []byte("package x"), 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, rel)
	}
	if _, err := st.InsertMemo(pSel.ID, "deploy ordering migration race", "agent", tmp, files); err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertLesson(pOther.ID, store.FormatLesson("deploy ordering migration race", "shared state", "serialize writes", ""), "agent", otherDir, nil); err != nil {
		t.Fatal(err)
	}

	res, err := RecallSolutions(st, "deploy ordering", RecallOptions{
		Project:    pSel.ID,
		ProjectDir: tmp,
		Limit:      10,
		Semantic:   true,
		Context:    &RecallContext{Paths: files},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Hits[0].Kind != "lesson" {
		t.Fatalf("first hit = %s, semantic rerank crossed the lesson tier", res.Hits[0].Kind)
	}
	for _, h := range res.Hits {
		if h.Kind == "fact" && h.Score > recallFactBase+recallTierSpan {
			t.Fatalf("semantic fact score = %.0f, want <= %.0f", h.Score, recallFactBase+recallTierSpan)
		}
	}
}

func TestRecallContextSemanticFailurePreservesPack(t *testing.T) {
	t.Setenv("SILA_ASSIST_ENABLED", "false")

	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "recall_semantic_fail.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p, err := st.EnsureProject(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertMemo(p.ID, "deploy ordering migration race", "agent", "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertMemo(p.ID, "deploy ordering migration retry", "agent", "", nil); err != nil {
		t.Fatal(err)
	}

	res, err := RecallSolutions(st, "deploy ordering", RecallOptions{
		Project:  p.ID,
		Limit:    5,
		Semantic: true,
		Context:  &RecallContext{Task: "deploy ordering"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) == 0 {
		t.Fatal("semantic provider failure produced no warning")
	}
	if res.Pack == nil || len(res.Pack.Items) == 0 {
		t.Fatal("semantic failure dropped the deterministic pack")
	}
}

func archivedMemoryFixture(t *testing.T, st *store.Store, projectID, title, summary string) *store.MemoryRecord {
	t.Helper()
	env := &store.MemoryEnvelope{
		SchemaVersion: store.MemorySchemaVersion,
		ProjectID:     projectID,
		Kind:          store.MemoryKindRootCause,
		Title:         title,
		Summary:       summary,
		Assertions: []store.MemoryAssertion{{
			ID:            "A-001",
			AssertionType: store.MemoryAssertionDescriptive,
			Predicate:     "causes",
			Statement:     summary,
			Subject:       store.MemorySubject{ContextEntity: "project", Value: projectID},
			Object:        store.MemoryObject{Literal: &store.MemoryLiteral{Type: "string", Value: "fix"}},
			Confidence:    0.8,
			Importance:    4,
			EvidenceRefs:  []string{"E-001"},
		}},
		Evidence: []store.MemoryEvidence{{
			ID:      "E-001",
			Kind:    store.MemoryEvidenceAgentInitiated,
			Summary: "Recorded during a lifecycle test.",
		}},
	}
	rec, _, err := st.SaveMemoryEnvelope(context.Background(), projectID, env, store.Provenance{})
	if err != nil {
		t.Fatalf("save memory: %v", err)
	}
	return rec
}

func TestRecallExcludesArchived(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "recall-archive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rec := archivedMemoryFixture(t, st, "proj", "Zebra archive needle", "Zebra archive needle resolution")
	if _, err := st.ArchiveMemory(context.Background(), "proj", rec.ID, store.ArchiveAuthorityUserRequest, store.ArchiveReasonOutdated, "", "user", ""); err != nil {
		t.Fatal(err)
	}
	res, err := RecallSolutions(st, "zebra archive needle", RecallOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, hit := range res.Hits {
		if hit.ID == rec.FactID || hit.ID == rec.ID {
			t.Fatalf("archived memory returned in recall: %+v", hit)
		}
	}
	if res.Pack != nil {
		for _, item := range res.Pack.Items {
			if item.ID == rec.FactID || item.ID == rec.ID {
				t.Fatalf("archived memory returned in recall pack: %+v", item)
			}
		}
	}
}

func TestIncludeArchivedOptIn(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "recall-include.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rec := archivedMemoryFixture(t, st, "proj", "Zebra include needle", "Zebra include needle resolution")
	if _, err := st.ArchiveMemory(context.Background(), "proj", rec.ID, store.ArchiveAuthorityUserRequest, store.ArchiveReasonOutdated, "", "user", ""); err != nil {
		t.Fatal(err)
	}
	res, err := RecallSolutions(st, "zebra include needle", RecallOptions{Limit: 10, IncludeArchived: true})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, hit := range res.Hits {
		if hit.ID == rec.FactID {
			found = true
		}
	}
	if !found {
		t.Fatalf("include-archived recall did not return the record: %+v", res.Hits)
	}
}

func TestSearchKnowledgeExcludesArchived(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "search-archive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rec := archivedMemoryFixture(t, st, "proj", "Quokka knowledge needle", "Quokka knowledge needle resolution")
	facts, err := QueryStoreFactsPaged(st, []string{"quokka", "knowledge"}, "proj", "", false, false, false, 10, 0)
	if err != nil || len(facts) == 0 {
		t.Fatalf("default search before archive = %+v err=%v", facts, err)
	}
	if _, err := st.ArchiveMemory(context.Background(), "proj", rec.ID, store.ArchiveAuthorityUserRequest, store.ArchiveReasonOutdated, "", "user", ""); err != nil {
		t.Fatal(err)
	}
	facts, err = QueryStoreFactsPaged(st, []string{"quokka", "knowledge"}, "proj", "", false, false, false, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, fact := range facts {
		if fact.ID == rec.FactID {
			t.Fatalf("archived fact returned by search: %+v", fact)
		}
	}
	facts, err = QueryStoreFactsPagedIncludingArchived(st, []string{"quokka", "knowledge"}, "proj", "", false, false, false, 10, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, fact := range facts {
		if fact.ID == rec.FactID {
			found = true
		}
	}
	if !found {
		t.Fatalf("include-archived search did not return the fact: %+v", facts)
	}
}

func TestRestoredMemoryFeedbackReturns(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "recall-restore.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rec := archivedMemoryFixture(t, st, "proj", "Restored ranking needle", "Restored ranking needle resolution")
	if _, err := st.ArchiveMemory(context.Background(), "proj", rec.ID, store.ArchiveAuthorityUserRequest, store.ArchiveReasonOutdated, "", "user", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RestoreMemory(context.Background(), "proj", rec.ID, "user", store.ArchiveAuthorityUserRequest, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	res, err := RecallSolutions(st, "restored ranking needle", RecallOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, hit := range res.Hits {
		if hit.ID == rec.FactID {
			found = true
		}
	}
	if !found {
		t.Fatalf("restored memory did not return to recall: %+v", res.Hits)
	}
}

func TestOmniSearchExcludesArchived(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "omni-archive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rec := archivedMemoryFixture(t, st, "proj", "Quokka omni needle", "Quokka omni needle resolution")
	res, err := OmniSearchFiltered(st, "quokka omni needle", OmniOptions{Project: "proj", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if !omniContainsFact(res, rec.FactID) {
		t.Fatalf("omni search before archive did not include the fact: %+v", res.Items)
	}
	if _, err := st.ArchiveMemory(context.Background(), "proj", rec.ID, store.ArchiveAuthorityUserRequest, store.ArchiveReasonOutdated, "", "user", ""); err != nil {
		t.Fatal(err)
	}
	res, err = OmniSearchFiltered(st, "quokka omni needle", OmniOptions{Project: "proj", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if omniContainsFact(res, rec.FactID) {
		t.Fatalf("omni search included archived fact: %+v", res.Items)
	}
	res, err = OmniSearchFiltered(st, "quokka omni needle", OmniOptions{Project: "proj", Limit: 10, IncludeArchived: true})
	if err != nil {
		t.Fatal(err)
	}
	if !omniContainsFact(res, rec.FactID) {
		t.Fatalf("include-archived omni search did not include the fact: %+v", res.Items)
	}
}

func omniContainsFact(res *OmniSearchResult, id string) bool {
	for _, item := range res.Items {
		if item.ID == id {
			return true
		}
	}
	return false
}
