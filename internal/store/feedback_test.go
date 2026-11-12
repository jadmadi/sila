package store

import (
	"path/filepath"
	"testing"
)

func TestFeedbackStorage(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sila.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	// 1. Insert feedback items
	fb1 := &Feedback{
		ProjectID: "example.org",
		Agent:     "claude-code",
		Category:  "idea",
		Content:   "Add an interactive preview for goals before execution",
	}
	if err := st.SaveFeedback(fb1); err != nil {
		t.Fatalf("SaveFeedback fb1: %v", err)
	}

	fb2 := &Feedback{
		ProjectID: "example-app-App",
		Agent:     "devin",
		Category:  "issue",
		Content:   "Timeout occurred while crawling remote docs with 50 pages",
	}
	if err := st.SaveFeedback(fb2); err != nil {
		t.Fatalf("SaveFeedback fb2: %v", err)
	}

	fb3 := &Feedback{
		ProjectID: "sila",
		Agent:     "antigravity",
		Category:  "praise",
		Content:   "sila prime instant briefing saves over 85% session startup time!",
	}
	if err := st.SaveFeedback(fb3); err != nil {
		t.Fatalf("SaveFeedback fb3: %v", err)
	}

	// 2. List all
	all, err := st.ListFeedback("", "", 10)
	if err != nil {
		t.Fatalf("ListFeedback all: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 items, got %d", len(all))
	}

	// 3. Filter by project
	marofItems, err := st.ListFeedback("example.org", "", 10)
	if err != nil {
		t.Fatalf("ListFeedback marof: %v", err)
	}
	if len(marofItems) != 1 || marofItems[0].Category != "idea" {
		t.Fatalf("unexpected marof items: %+v", marofItems)
	}

	// 4. Filter by category
	issues, err := st.ListFeedback("", "issue", 10)
	if err != nil {
		t.Fatalf("ListFeedback issues: %v", err)
	}
	if len(issues) != 1 || issues[0].Agent != "devin" {
		t.Fatalf("unexpected issues: %+v", issues)
	}

	// 5. Check stats
	stats, err := st.FeedbackStats()
	if err != nil {
		t.Fatalf("FeedbackStats: %v", err)
	}
	if stats["idea"] != 1 || stats["issue"] != 1 || stats["praise"] != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}

	// 6. Test ResolveFeedback
	err = st.ResolveFeedback(fb1.ID, "@antigravity", "interactive-preview", "Implemented in PR #42")
	if err != nil {
		t.Fatalf("ResolveFeedback: %v", err)
	}

	resolved, err := st.GetFeedback(fb1.ID)
	if err != nil {
		t.Fatalf("GetFeedback: %v", err)
	}
	if resolved.Status != "closed" || resolved.ResolvedBy != "@antigravity" || resolved.ResolvedGoal != "interactive-preview" {
		t.Fatalf("unexpected resolved state: %+v", resolved)
	}

	// 7. Test QueryFeedback with Status filter
	openItems, err := st.QueryFeedback(FeedbackFilter{Status: "open"})
	if err != nil {
		t.Fatalf("QueryFeedback open: %v", err)
	}
	if len(openItems) != 2 {
		t.Fatalf("expected 2 open items, got %d", len(openItems))
	}

	closedItems, err := st.QueryFeedback(FeedbackFilter{Status: "closed"})
	if err != nil {
		t.Fatalf("QueryFeedback closed: %v", err)
	}
	if len(closedItems) != 1 || closedItems[0].ID != fb1.ID {
		t.Fatalf("expected 1 closed item with id %s, got %v", fb1.ID, closedItems)
	}

	// 8. Test Status stats
	statusStats, err := st.FeedbackStatusStats()
	if err != nil {
		t.Fatalf("FeedbackStatusStats: %v", err)
	}
	if statusStats["open"] != 2 || statusStats["closed"] != 1 {
		t.Fatalf("unexpected status stats: %+v", statusStats)
	}

	// 9. Test ReopenFeedback
	err = st.ReopenFeedback(fb1.ID)
	if err != nil {
		t.Fatalf("ReopenFeedback: %v", err)
	}
	reopened, err := st.GetFeedback(fb1.ID)
	if err != nil {
		t.Fatalf("GetFeedback reopened: %v", err)
	}
	if reopened.Status != "open" || reopened.ResolvedAt != "" {
		t.Fatalf("expected open status after reopen, got %+v", reopened)
	}

	// 10. Test UpdateFeedbackNote
	err = st.UpdateFeedbackNote(fb1.ID, "GitHub Issue: https://github.com/jadmadi/sila/issues/42")
	if err != nil {
		t.Fatalf("UpdateFeedbackNote: %v", err)
	}
	updated, err := st.GetFeedback(fb1.ID)
	if err != nil {
		t.Fatalf("GetFeedback updated: %v", err)
	}
	if updated.ResolutionNote != "GitHub Issue: https://github.com/jadmadi/sila/issues/42" {
		t.Fatalf("unexpected note: %s", updated.ResolutionNote)
	}
}

// TestProductFeedbackUnchanged pins that relevance feedback does not touch the
// existing product-feedback table, its categories, or its resolution flow.
func TestProductFeedbackUnchanged(t *testing.T) {
	st := openMemoryStore(t)
	product := &Feedback{
		ProjectID: "proj",
		Agent:     "agent-a",
		Category:  "issue",
		Content:   "product feedback still works",
	}
	if err := st.RecordFeedback(product); err != nil {
		t.Fatalf("RecordFeedback: %v", err)
	}
	stored, err := st.GetFeedback(product.ID)
	if err != nil {
		t.Fatalf("GetFeedback: %v", err)
	}
	if stored.Content != product.Content || stored.Category != "issue" || stored.Status != "open" {
		t.Fatalf("product feedback changed: %+v", stored)
	}
	if err := st.ResolveFeedback(product.ID, "agent-a", "", "done"); err != nil {
		t.Fatalf("ResolveFeedback: %v", err)
	}
	resolved, err := st.GetFeedback(product.ID)
	if err != nil || resolved.Status != "closed" || resolved.ResolutionNote != "done" {
		t.Fatalf("resolved product feedback: %+v err=%v", resolved, err)
	}

	rec := seedFeedbackMemory(t, st, "proj", "Product feedback isolation memory")
	recordFeedback(t, st, MemoryFeedbackInput{
		ProjectID: "proj", MemoryID: rec.ID, Signal: MemoryFeedbackHelped,
		Agent: "agent-a", Unbound: true,
	}, feedbackClock())
	var productRows int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM feedback`).Scan(&productRows); err != nil {
		t.Fatal(err)
	}
	if productRows != 1 {
		t.Fatalf("relevance feedback wrote %d product feedback rows, want 1", productRows)
	}
}
