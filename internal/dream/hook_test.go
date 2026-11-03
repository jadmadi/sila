package dream

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func TestSessionEndHook(t *testing.T) {
	tmpDir := t.TempDir()
	st, err := store.Open(filepath.Join(tmpDir, "hook.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Insert handoff with open tasks
	_ = st.InsertHandoff(store.Handoff{
		ProjectID: "test-proj",
		Summary:   "Building hook module",
		OpenItems: []string{"Task 1", "- [x] Done task", "Task 2"},
		Agent:     "test",
	})

	res, err := RunSessionEndHook(st, "test-proj", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.ProjectID != "test-proj" {
		t.Fatalf("expected project test-proj, got %s", res.ProjectID)
	}
	if res.UnclosedTasks != 2 {
		t.Fatalf("expected 2 unclosed tasks, got %d", res.UnclosedTasks)
	}
	if !strings.Contains(res.SummaryMessage, "Session concluded") {
		t.Fatalf("unexpected summary message: %s", res.SummaryMessage)
	}
}

func TestExtractSessionMemoCandidates(t *testing.T) {
	messages := []store.StoredMessage{
		{
			Role:    "assistant",
			Content: "We completed the migration.\nInvariant: Never import CGO SQLite packages; always use modernc.org/sqlite.\nAll tests pass.",
		},
		{
			Role:    "user",
			Content: "Great job, remember that rule: always write effect-first commit messages.",
		},
	}

	existingFacts := []store.Fact{
		{
			ProjectID: "sila",
			Claim:     "Old existing fact",
		},
	}

	candidates := ExtractSessionMemoCandidates(messages, existingFacts)
	if len(candidates) == 0 {
		t.Fatalf("expected candidate memos to be extracted, got none")
	}

	foundInvariant := false
	for _, c := range candidates {
		if strings.Contains(strings.ToLower(c), "never import cgo sqlite") {
			foundInvariant = true
			break
		}
	}
	if !foundInvariant {
		t.Fatalf("expected invariant candidate to be found, got: %v", candidates)
	}
}

func TestLiveDeltaCheckpointSuppression(t *testing.T) {
	tmpDir := t.TempDir()
	st, err := store.Open(filepath.Join(tmpDir, "hook.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	projectID := "test-proj"
	_, err = st.DB.Exec(`INSERT INTO messages(project_id, content_hash, ts, role, content, source_tools, first_seen)
		VALUES(?, 'h1', '2026-09-18T12:00:00Z', 'assistant', 'Resolved database locked error with busy_timeout', '["devin"]', '2026-09-18T12:00:00Z')`, projectID)
	if err != nil {
		t.Fatal(err)
	}

	msgs, err := st.RecentMessages(projectID, 15)
	if err != nil || len(msgs) == 0 {
		t.Fatalf("expected 1 recent message, got %d (err: %v)", len(msgs), err)
	}

	// 1. Checkpoint fingerprint is deterministic
	fp1 := computeEvidenceFingerprint(msgs)
	fp2 := computeEvidenceFingerprint(msgs)
	if fp1 != fp2 || fp1 == "" {
		t.Fatalf("expected non-empty deterministic fingerprint: %s vs %s", fp1, fp2)
	}

	// 2. Simulate checkpoint advance
	checkpointKey := "assist_live_checkpoint:" + projectID
	if err := st.SetState(checkpointKey, fp1); err != nil {
		t.Fatal(err)
	}

	storedFP, err := st.GetState(checkpointKey)
	if err != nil || storedFP != fp1 {
		t.Fatalf("expected stored fingerprint %s, got %s (err: %v)", fp1, storedFP, err)
	}

	// 3. Adding a new message alters the fingerprint
	_, err = st.DB.Exec(`INSERT INTO messages(project_id, content_hash, ts, role, content, source_tools, first_seen)
		VALUES(?, 'h2', '2026-09-18T13:00:00Z', 'assistant', 'Added new checkout idempotency guard', '["claude-code"]', '2026-09-18T13:00:00Z')`, projectID)
	if err != nil {
		t.Fatal(err)
	}

	newMsgs, _ := st.RecentMessages(projectID, 15)
	fpNew := computeEvidenceFingerprint(newMsgs)
	if fpNew == fp1 {
		t.Fatalf("expected new fingerprint to differ after new message: %s == %s", fpNew, fp1)
	}

	// 4. Changing source tool attribution alters the fingerprint
	_, err = st.DB.Exec(`UPDATE messages SET source_tools = '["claude-code", "opencode"]' WHERE content_hash = 'h2'`)
	if err != nil {
		t.Fatal(err)
	}
	toolsMsgs, _ := st.RecentMessages(projectID, 15)
	fpTools := computeEvidenceFingerprint(toolsMsgs)
	if fpTools == fpNew {
		t.Fatalf("expected updated tool attribution to alter fingerprint: %s == %s", fpTools, fpNew)
	}

	// 5. Late-arriving message with older ts but newer first_seen is included and alters fingerprint
	_, err = st.DB.Exec(`INSERT INTO messages(project_id, content_hash, ts, role, content, source_tools, first_seen)
		VALUES(?, 'h-late', '2026-09-17T10:00:00Z', 'user', 'Critical late blocker from yesterday', '["muse"]', '2026-09-18T14:00:00Z')`, projectID)
	if err != nil {
		t.Fatal(err)
	}
	lateMsgs, _ := st.RecentMessages(projectID, 15)
	if len(lateMsgs) < 3 || lateMsgs[0].ContentHash != "h-late" {
		t.Fatalf("expected late message with newest first_seen to be ranked first, got %+v", lateMsgs)
	}
	fpLate := computeEvidenceFingerprint(lateMsgs)
	if fpLate == fpTools {
		t.Fatalf("expected late message to alter fingerprint: %s == %s", fpLate, fpTools)
	}
}

func TestSessionEndHook_LessonSuggestions(t *testing.T) {
	tmpDir := t.TempDir()
	st, err := store.Open(filepath.Join(tmpDir, "hook_lessons.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	projectID := "test-proj"
	_, err = st.DB.Exec(`INSERT INTO messages(project_id, content_hash, ts, role, content, source_tools, first_seen)
		VALUES(?, 'h1', '2026-09-21T10:00:00Z', 'assistant', 'Problem: lock contention\nCause: unbuffered channel\nFix: use buffered channel of size 16', '["claude-code"]', '2026-09-21T10:00:00Z')`, projectID)
	if err != nil {
		t.Fatal(err)
	}

	res, err := RunSessionEndHook(st, projectID, "", false)
	if err != nil {
		t.Fatal(err)
	}

	foundLesson := false
	for _, s := range res.Suggestions {
		if strings.Contains(s, "Candidate lesson:") && strings.Contains(s, "lock contention") {
			foundLesson = true
			break
		}
	}
	if !foundLesson {
		t.Fatalf("expected candidate lesson suggestion in hook result, got %+v", res.Suggestions)
	}
}
