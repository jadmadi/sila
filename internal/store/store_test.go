package store

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenMigrateAndInsert(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	var got int
	nr, mg, err := s.WriteRun("", "devin", "9", []LoadMessage{
		{ProjectID: "mockapp", ContentHash: "h", TS: "2026-08-01T00:00:00Z", Role: "assistant", Content: "x", Tool: "devin"},
		{ProjectID: "mockapp", ContentHash: "h", TS: "2026-08-01T00:00:00Z", Role: "assistant", Content: "x", Tool: "mimocode"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if nr != 1 || mg != 1 {
		t.Fatalf("expected 1 new + 1 merged, got %d new / %d merged", nr, mg)
	}
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&got)
	if got != 1 {
		t.Fatalf("expected 1 message after dedup, got %d", got)
	}
	var tools string
	_ = s.DB.QueryRow(`SELECT source_tools FROM messages`).Scan(&tools)
	if !strings.Contains(tools, "devin") || !strings.Contains(tools, "mimocode") {
		t.Fatalf("source_tools should contain both tools, got %q", tools)
	}

	// watermark persisted in the same tx
	wm, _ := s.GetWatermark("", "devin")
	if wm != "9" {
		t.Fatalf("watermark = %q, want 9", wm)
	}
}

func TestWriteRunScopedUsesSeparateWatermarkKey(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	_, _, err = s.WriteRunScoped("", "git", "git:demo", "1700000000", []LoadMessage{
		{ProjectID: "demo", ContentHash: "h1", TS: "2026-09-01T00:00:00Z", Role: "commit", Content: "x", Tool: "git"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if wm, _ := s.GetWatermark("", "git"); wm != "" {
		t.Fatalf("shared git watermark should stay unset, got %q", wm)
	}
	if wm, _ := s.GetWatermark("", "git:demo"); wm != "1700000000" {
		t.Fatalf("scoped git watermark = %q, want 1700000000", wm)
	}
	var tools string
	_ = s.DB.QueryRow(`SELECT source_tools FROM messages`).Scan(&tools)
	if !strings.Contains(tools, "git") {
		t.Fatalf("provenance tool must stay 'git', got %q", tools)
	}
}

func TestWatermarkRoundTrip(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.SetWatermark("run", "devin", "42")
	got, _ := s.GetWatermark("run", "devin")
	if got != "42" {
		t.Fatalf("watermark = %q, want 42", got)
	}
	empty, _ := s.GetWatermark("run", "missing")
	if empty != "" {
		t.Fatalf("expected empty watermark, got %q", empty)
	}
}

func TestWatermarkMonotonicNonRegression(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.SetWatermark("", "mimocode", "1000"); err != nil {
		t.Fatal(err)
	}

	if err := s.SetWatermark("", "mimocode", "500"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetWatermark("", "mimocode")
	if got != "1000" {
		t.Fatalf("watermark regressed to %q, want 1000", got)
	}

	if err := s.SetWatermark("", "mimocode", "0"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetWatermark("", "mimocode")
	if got != "1000" {
		t.Fatalf("watermark regressed to %q, want 1000", got)
	}

	s.WriteRun("", "mimocode", "0", nil)
	got, _ = s.GetWatermark("", "mimocode")
	if got != "1000" {
		t.Fatalf("WriteRun regressed watermark to %q, want 1000", got)
	}

	s.WriteRun("", "mimocode", "800", nil)
	got, _ = s.GetWatermark("", "mimocode")
	if got != "1000" {
		t.Fatalf("WriteRun regressed watermark to %q, want 1000", got)
	}

	s.WriteRun("", "mimocode", "2000", nil)
	got, _ = s.GetWatermark("", "mimocode")
	if got != "2000" {
		t.Fatalf("watermark = %q, want 2000", got)
	}
}

func TestStoreOpenSelfHealsZeroWatermarks(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "heal.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = s.DB.Exec(`INSERT INTO state(key,value,updated_at) VALUES('watermark:mimocode','0','2026-09-07T12:00:00Z')`)
	s.Close()

	s2, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()

	got, _ := s2.GetWatermark("", "mimocode")
	if got != "" {
		t.Fatalf("expected empty watermark after self-healing, got %q", got)
	}
	wmTimes, _ := s2.WatermarkTimes()
	if _, ok := wmTimes["mimocode"]; ok {
		t.Fatalf("corrupted '0' watermark still present in WatermarkTimes: %+v", wmTimes)
	}
}

func TestProjectsWithCounts(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if _, err := s.EnsureProject("/repo/a"); err != nil {
		t.Fatal(err)
	}
	s.WriteRun("", "devin", "9", []LoadMessage{
		{ProjectID: "a", ContentHash: "h1", TS: "t", Role: "assistant", Content: "c", Tool: "devin"},
	})
	s.UpsertFact(Fact{ID: "f1", ProjectID: "a", Claim: "x", Confidence: "mid", Verified: true})

	stats, err := s.ProjectsWithCounts()
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 || stats[0].ID != "a" {
		t.Fatalf("expected 1 project, got %+v", stats)
	}
	if stats[0].Messages != 1 || stats[0].Facts != 1 || stats[0].Verified != 1 {
		t.Fatalf("counts wrong: %+v", stats[0])
	}
}

func TestProjectIDSlug(t *testing.T) {
	a := ProjectID("/home/u/app")
	b := ProjectID("/home/u/app")
	if a != b {
		t.Fatalf("same path should yield same id: %q vs %q", a, b)
	}
	if ProjectID("/home/u/sila") != "sila" {
		t.Fatalf("expected dir-name slug, got %q", ProjectID("/home/u/sila"))
	}
	// ProjectID is a pure slug; distinct-path collision suffixing is resolved in
	// EnsureProject (see TestEnsureProjectCollisionSuffix).
	if ProjectID("/home/u/app") != ProjectID("/other/app") {
		t.Fatalf("ProjectID must be a pure slug, independent of path")
	}
}

func TestEnsureProjectCollisionSuffix(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	p1, err := s.EnsureProject("/home/u/mockapp")
	if err != nil {
		t.Fatal(err)
	}
	p2, err := s.EnsureProject("/otherw/mockapp")
	if err != nil {
		t.Fatal(err)
	}
	if p1.ID == p2.ID {
		t.Fatalf("two paths with same dir name must get distinct ids: %q vs %q", p1.ID, p2.ID)
	}
	if !strings.HasPrefix(p2.ID, p1.ID) {
		t.Fatalf("collided id %q should prefix with %q", p2.ID, p1.ID)
	}

	// same path returns stable, non-duplicate id
	p3, err := s.EnsureProject("/home/u/mockapp")
	if err != nil {
		t.Fatal(err)
	}
	if p3.ID != p1.ID {
		t.Fatalf("re-query of same path returned different id")
	}
}

func TestMessageWithContext(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	p, err := s.EnsureProject("/workspace/myproj")
	if err != nil {
		t.Fatal(err)
	}

	// Insert 5 sequential messages from one recorded session
	msgs := []LoadMessage{
		{ProjectID: p.ID, ContentHash: "h1", TS: "2026-08-01T10:00:00Z", Role: "user", Content: "First msg", Tool: "devin", SessionID: "sess-1"},
		{ProjectID: p.ID, ContentHash: "h2", TS: "2026-08-01T10:01:00Z", Role: "assistant", Content: "Second msg", Tool: "devin", SessionID: "sess-1"},
		{ProjectID: p.ID, ContentHash: "h3", TS: "2026-08-01T10:02:00Z", Role: "user", Content: "Third target msg", Tool: "devin", SessionID: "sess-1"},
		{ProjectID: p.ID, ContentHash: "h4", TS: "2026-08-01T10:03:00Z", Role: "assistant", Content: "Fourth msg", Tool: "devin", SessionID: "sess-1"},
		{ProjectID: p.ID, ContentHash: "h5", TS: "2026-08-01T10:04:00Z", Role: "user", Content: "Fifth msg", Tool: "devin", SessionID: "sess-1"},
	}
	_, _, err = s.WriteRun("", "devin", "1", msgs)
	if err != nil {
		t.Fatal(err)
	}

	// Search for target message ID
	searchResults, err := s.SearchMessagesFTS("target", SearchMessagesOptions{Project: p.ID})
	if err != nil || len(searchResults) == 0 {
		t.Fatalf("failed finding target message: %v (results: %d)", err, len(searchResults))
	}
	targetID := searchResults[0].ID

	ctx, err := s.MessageWithContext(targetID, 2)
	if err != nil {
		t.Fatalf("MessageWithContext failed: %v", err)
	}

	if !ctx.SessionAvailable || ctx.SessionID != "sess-1" {
		t.Fatalf("session provenance missing: %+v", ctx)
	}
	if ctx.TargetMessage.Content != "Third target msg" {
		t.Fatalf("expected target message 'Third target msg', got %q", ctx.TargetMessage.Content)
	}
	if len(ctx.Before) != 2 {
		t.Fatalf("expected 2 before messages, got %d", len(ctx.Before))
	}
	if ctx.Before[0].Content != "First msg" || ctx.Before[1].Content != "Second msg" {
		t.Fatalf("unexpected before messages order: %+v", ctx.Before)
	}
	if len(ctx.After) != 2 {
		t.Fatalf("expected 2 after messages, got %d", len(ctx.After))
	}
	if ctx.After[0].Content != "Fourth msg" || ctx.After[1].Content != "Fifth msg" {
		t.Fatalf("unexpected after messages order: %+v", ctx.After)
	}
}

// TestMessageWithContextCrossSessionIsolation reproduces the codex-eval F02
// fixture: an unrelated message inserted later in the same project must not
// appear as the apparent continuation of an earlier conversation.
func TestMessageWithContextCrossSessionIsolation(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	p, err := s.EnsureProject("/workspace/isolation")
	if err != nil {
		t.Fatal(err)
	}
	write := func(msgs []LoadMessage) {
		t.Helper()
		if _, _, err := s.WriteRun("", "codex", "1", msgs); err != nil {
			t.Fatal(err)
		}
	}
	// Newer message first (session A), then an unrelated older message
	// (session B) inserted after it, as the eval fixture did.
	write([]LoadMessage{
		{ProjectID: p.ID, ContentHash: "newer", TS: "2026-09-15T12:00:00Z", Role: "assistant", Content: "newer session A message", Tool: "codex", SessionID: "sess-A"},
	})
	write([]LoadMessage{
		{ProjectID: p.ID, ContentHash: "older", TS: "2026-09-14T09:00:00Z", Role: "user", Content: "unrelated older session B message", Tool: "codex", SessionID: "sess-B"},
	})

	var targetID int64
	if err := s.DB.QueryRow(`SELECT id FROM messages WHERE content_hash='newer'`).Scan(&targetID); err != nil {
		t.Fatal(err)
	}

	ctx, err := s.MessageWithContext(targetID, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(ctx.After) != 0 {
		t.Fatalf("unrelated session leaked into after window: %+v", ctx.After)
	}
	if ctx.SessionID != "sess-A" {
		t.Fatalf("session = %q, want sess-A", ctx.SessionID)
	}
}

// TestMessageWithContextUnavailableForLegacyRows verifies the honest
// empty-state for records without source session provenance.
func TestMessageWithContextUnavailableForLegacyRows(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	p, err := s.EnsureProject("/workspace/legacy")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.WriteRun("", "devin", "1", []LoadMessage{
		{ProjectID: p.ID, ContentHash: "h1", TS: "2026-08-01T10:00:00Z", Role: "user", Content: "First msg", Tool: "devin"},
		{ProjectID: p.ID, ContentHash: "h2", TS: "2026-08-01T10:01:00Z", Role: "assistant", Content: "Second msg", Tool: "devin"},
	})
	if err != nil {
		t.Fatal(err)
	}

	var targetID int64
	if err := s.DB.QueryRow(`SELECT id FROM messages WHERE content_hash='h2'`).Scan(&targetID); err != nil {
		t.Fatal(err)
	}
	ctx, err := s.MessageWithContext(targetID, 3)
	if err != nil {
		t.Fatal(err)
	}
	if ctx.SessionAvailable || ctx.SessionID != "" {
		t.Fatalf("legacy row must not claim a session: %+v", ctx)
	}
	if len(ctx.Before) != 0 || len(ctx.After) != 0 {
		t.Fatalf("legacy row must not present insertion neighbors as conversation: %+v", ctx)
	}
	if ctx.Note == "" {
		t.Fatal("expected a conversation-unavailable note")
	}
}

// TestMessageOccurrenceSelection covers deduplicated text with multiple
// recorded occurrences: the selector chooses which session gets expanded.
func TestMessageOccurrenceSelection(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	p, err := s.EnsureProject("/workspace/dupes")
	if err != nil {
		t.Fatal(err)
	}
	// Same text from two different sessions collapses to one message row.
	hash := "shared-text-hash"
	if _, _, err := s.WriteRun("", "codex", "1", []LoadMessage{
		{ProjectID: p.ID, ContentHash: hash, TS: "2026-09-01T10:00:00Z", Role: "user", Content: "shared text", Tool: "codex", SessionID: "sess-1"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.WriteRun("", "codex", "1", []LoadMessage{
		{ProjectID: p.ID, ContentHash: hash, TS: "2026-09-02T10:00:00Z", Role: "user", Content: "shared text", Tool: "codex", SessionID: "sess-2"},
	}); err != nil {
		t.Fatal(err)
	}

	var targetID int64
	if err := s.DB.QueryRow(`SELECT id FROM messages WHERE content='shared text'`).Scan(&targetID); err != nil {
		t.Fatal(err)
	}

	ctx, err := s.MessageInContext(targetID, 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	if ctx.SelectedOccurrence != 2 || ctx.SessionID != "sess-2" {
		t.Fatalf("occurrence selection wrong: selected=%d session=%q", ctx.SelectedOccurrence, ctx.SessionID)
	}
	if ctx.OccurrenceCount != 2 {
		t.Fatalf("occurrence count = %d, want 2", ctx.OccurrenceCount)
	}
}

func TestStore_ProjectForDir(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	repoDir := filepath.Join(t.TempDir(), "my-app")
	subDir := filepath.Join(repoDir, "src", "components")

	p, err := s.EnsureProject(repoDir)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Exact match on repo dir
	match1, ok, err := s.ProjectForDir(repoDir)
	if err != nil || !ok || match1.ID != p.ID {
		t.Fatalf("expected exact match for %q, got ok=%v, match=%+v, err=%v", repoDir, ok, match1, err)
	}

	// 2. Subdirectory inside repo
	match2, ok, err := s.ProjectForDir(subDir)
	if err != nil || !ok || match2.ID != p.ID {
		t.Fatalf("expected subdirectory match for %q, got ok=%v, match=%+v, err=%v", subDir, ok, match2, err)
	}

	// 3. Unrelated outside dir
	outsideDir := t.TempDir()
	_, ok, err = s.ProjectForDir(outsideDir)
	if err != nil || ok {
		t.Fatalf("expected no match for outside dir, got ok=%v, err=%v", ok, err)
	}
}

// TestMessageInContextHonorsSourceOrder is the re-eval F02 reproduction:
// same-timestamp turns must follow their recorded source positions, not
// insertion order. Position 2 is inserted before position 1.
func TestMessageInContextHonorsSourceOrder(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err := s.EnsureProject("/work/order")
	if err != nil {
		t.Fatal(err)
	}
	hash := func(r, c string) string { return "ord-" + r + "-" + c }
	if _, _, err := s.WriteRun("", "devin", "1", []LoadMessage{
		{ProjectID: p.ID, ContentHash: hash("u", "pos2"), TS: "2026-09-01T10:00:00Z", Role: "user", Content: "second by position", Tool: "devin", SessionID: "s1", SourceOrder: 2},
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.WriteRun("", "devin", "1", []LoadMessage{
		{ProjectID: p.ID, ContentHash: hash("t", "target"), TS: "2026-09-01T10:00:00Z", Role: "assistant", Content: "the target", Tool: "devin", SessionID: "s1", SourceOrder: 3},
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.WriteRun("", "devin", "1", []LoadMessage{
		{ProjectID: p.ID, ContentHash: hash("u", "pos1"), TS: "2026-09-01T10:00:00Z", Role: "user", Content: "first by position", Tool: "devin", SessionID: "s1", SourceOrder: 1},
	}); err != nil {
		t.Fatal(err)
	}
	var targetID int64
	if err := s.DB.QueryRow(`SELECT id FROM messages WHERE content='the target'`).Scan(&targetID); err != nil {
		t.Fatal(err)
	}
	ctx, err := s.MessageInContext(targetID, 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ctx.Before) != 2 || ctx.Before[0].Content != "first by position" || ctx.Before[1].Content != "second by position" {
		t.Fatalf("same-time order ignored source_order: %+v", ctx.Before)
	}
}

// TestMessageInContextSeparatesToolsWithSameSessionID pins the namespace rule:
// a session-id string collision across tools must not leak another tool's
// turns into the window.
func TestMessageInContextSeparatesToolsWithSameSessionID(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err := s.EnsureProject("/work/ns")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.WriteRun("", "devin", "1", []LoadMessage{
		{ProjectID: p.ID, ContentHash: "ns-target", TS: "2026-09-01T10:00:00Z", Role: "assistant", Content: "devin target", Tool: "devin", SessionID: "shared-sess", SourceOrder: 2},
		{ProjectID: p.ID, ContentHash: "ns-after", TS: "2026-09-01T10:01:00Z", Role: "user", Content: "devin later turn", Tool: "devin", SessionID: "shared-sess", SourceOrder: 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.WriteRun("", "codex", "1", []LoadMessage{
		{ProjectID: p.ID, ContentHash: "ns-foreign", TS: "2026-09-01T10:00:30Z", Role: "user", Content: "codex foreign turn", Tool: "codex", SessionID: "shared-sess", SourceOrder: 5},
	})
	if err != nil {
		t.Fatal(err)
	}
	var targetID int64
	if err := s.DB.QueryRow(`SELECT id FROM messages WHERE content='devin target'`).Scan(&targetID); err != nil {
		t.Fatal(err)
	}
	ctx, err := s.MessageInContext(targetID, 5, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ctx.After) != 1 || ctx.After[0].Content != "devin later turn" {
		t.Fatalf("cross-tool session leak: %+v", ctx.After)
	}
}

// TestMessageInContextShowsRepeatedTargetText pins the repeated-turn rule: a
// later occurrence of the target's own content is a real conversation turn,
// not invisible.
func TestMessageInContextShowsRepeatedTargetText(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err := s.EnsureProject("/work/repeat")
	if err != nil {
		t.Fatal(err)
	}
	h := "repeat-hash"
	_, _, err = s.WriteRun("", "devin", "1", []LoadMessage{
		{ProjectID: p.ID, ContentHash: h, TS: "2026-09-01T10:00:00Z", Role: "user", Content: "yes", Tool: "devin", SessionID: "s1", SourceOrder: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.WriteRun("", "devin", "1", []LoadMessage{
		{ProjectID: p.ID, ContentHash: "mid", TS: "2026-09-01T10:00:01Z", Role: "assistant", Content: "are you sure?", Tool: "devin", SessionID: "s1", SourceOrder: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = s.WriteRun("", "devin", "1", []LoadMessage{
		{ProjectID: p.ID, ContentHash: h, TS: "2026-09-01T10:00:02Z", Role: "user", Content: "yes", Tool: "devin", SessionID: "s1", SourceOrder: 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	var targetID int64
	if err := s.DB.QueryRow(`SELECT id FROM messages WHERE content='yes'`).Scan(&targetID); err != nil {
		t.Fatal(err)
	}
	ctx, err := s.MessageInContext(targetID, 3, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(ctx.Occurrences) != 2 {
		t.Fatalf("expected 2 occurrences of the repeated text, got %d", ctx.OccurrenceCount)
	}
	// Expanding the first occurrence: the second "yes" is a later turn.
	if len(ctx.After) != 2 || ctx.After[0].Content != "are you sure?" || ctx.After[1].Content != "yes" {
		t.Fatalf("repeated target text hidden from its own window: %+v", ctx.After)
	}
}

// TestMessageInContextMixedPositionScales is the follow-up to F02: a session
// whose occurrences use two position scales (a recorded source_order beside an
// occurrence that has none) must not invert. The earlier fallback compared a
// session-local ordinal against a global row id, so the unpositioned turn could
// sort after a turn that came later.
func TestMessageInContextMixedPositionScales(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, err := s.EnsureProject("/work/mixed")
	if err != nil {
		t.Fatal(err)
	}
	// Pad the table so occurrence row ids are far larger than the ordinals used
	// below, which is what made the scales cross.
	pad := make([]LoadMessage, 0, 40)
	for i := range 40 {
		pad = append(pad, LoadMessage{
			ProjectID: p.ID, ContentHash: "pad-" + string(rune('a'+i%26)) + string(rune('a'+i/26)),
			TS: "2026-08-01T00:00:00Z", Role: "user", Content: "pad", Tool: "devin", SessionID: "other",
		})
	}
	if _, _, err := s.WriteRun("", "devin", "1", pad); err != nil {
		t.Fatal(err)
	}

	// Turn one carries no position; turn two records position 2 — the shape of
	// a Codex history prompt beside a rollout answer, at one timestamp.
	if _, _, err := s.WriteRun("", "codex", "1", []LoadMessage{
		{ProjectID: p.ID, ContentHash: "mix-1", TS: "2026-09-01T10:00:00Z", Role: "user", Content: "first turn", Tool: "codex", SessionID: "mix", SourceOrder: 0},
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.WriteRun("", "codex", "1", []LoadMessage{
		{ProjectID: p.ID, ContentHash: "mix-2", TS: "2026-09-01T10:00:00Z", Role: "assistant", Content: "second turn", Tool: "codex", SessionID: "mix", SourceOrder: 2},
	}); err != nil {
		t.Fatal(err)
	}

	var firstID, secondID int64
	if err := s.DB.QueryRow(`SELECT id FROM messages WHERE content_hash='mix-1'`).Scan(&firstID); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(`SELECT id FROM messages WHERE content_hash='mix-2'`).Scan(&secondID); err != nil {
		t.Fatal(err)
	}

	ctxFirst, err := s.MessageInContext(firstID, 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ctxFirst.After) == 0 || ctxFirst.After[0].Content != "second turn" {
		t.Fatalf("the earlier turn does not precede the later one: after=%+v", ctxFirst.After)
	}
	if len(ctxFirst.Before) != 0 {
		t.Fatalf("the earliest turn has preceding context: %+v", ctxFirst.Before)
	}

	ctxSecond, err := s.MessageInContext(secondID, 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ctxSecond.Before) != 1 || ctxSecond.Before[0].Content != "first turn" {
		t.Fatalf("the later turn does not follow the earlier one: before=%+v", ctxSecond.Before)
	}
}

// TestHandoffSupersedeIsBranchScoped is the re-eval F08 probe: a new open
// handoff superseded every open handoff in the project, so handing work over on
// one branch silently erased another branch's active work.
func TestHandoffSupersedeIsBranchScoped(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.EnsureProject("/work/branches"); err != nil {
		t.Fatal(err)
	}

	if err := s.InsertHandoff(Handoff{
		ProjectID: "branches", Agent: "devin", Branch: "feature/one",
		Summary: "work on one", CreatedAt: "2026-09-01T10:00:00Z", Status: "open",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertHandoff(Handoff{
		ProjectID: "branches", Agent: "codex", Branch: "feature/two",
		Summary: "work on two", CreatedAt: "2026-09-01T11:00:00Z", Status: "open",
	}); err != nil {
		t.Fatal(err)
	}

	all, err := s.HandoffsByProject("branches", 0)
	if err != nil {
		t.Fatal(err)
	}
	open := map[string]string{}
	for _, h := range all {
		if h.Status == "open" {
			open[h.Branch] = h.Summary
		}
	}
	if len(open) != 2 {
		t.Fatalf("branch handoffs must not supersede each other, got %+v", open)
	}

	// The branch-aware lookup returns each branch's own handoff.
	one, err := s.LatestHandoffForBranch("branches", "feature/one")
	if err != nil || one == nil || one.Summary != "work on one" {
		t.Fatalf("feature/one lookup wrong: %+v (err=%v)", one, err)
	}
	two, err := s.LatestHandoffForBranch("branches", "feature/two")
	if err != nil || two == nil || two.Summary != "work on two" {
		t.Fatalf("feature/two lookup wrong: %+v (err=%v)", two, err)
	}

	// A new handoff on feature/one supersedes only feature/one's.
	if err := s.InsertHandoff(Handoff{
		ProjectID: "branches", Agent: "devin", Branch: "feature/one",
		Summary: "more work on one", CreatedAt: "2026-09-01T12:00:00Z", Status: "open",
	}); err != nil {
		t.Fatal(err)
	}
	all, err = s.HandoffsByProject("branches", 0)
	if err != nil {
		t.Fatal(err)
	}
	open = map[string]string{}
	superseded := map[string]bool{}
	for _, h := range all {
		if h.Status == "open" {
			open[h.Branch] = h.Summary
		}
		if h.Status == "superseded" {
			superseded[h.Branch] = true
		}
	}
	if open["feature/two"] != "work on two" {
		t.Fatalf("feature/two handoff was erased by feature/one: %+v", open)
	}
	if open["feature/one"] != "more work on one" {
		t.Fatalf("feature/one not replaced: %+v", open)
	}
	if !superseded["feature/one"] {
		t.Fatalf("the replaced feature/one handoff should be superseded: %+v", superseded)
	}
}

func TestOpenSeedsFromTemplate(t *testing.T) {
	dir := t.TempDir()
	template := filepath.Join(dir, "template.db")
	tmpl, err := Open(template)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tmpl.DB.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		tmpl.Close()
		t.Fatal(err)
	}
	if err := tmpl.Close(); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SILA_TEST_DB_TEMPLATE", template)
	target := filepath.Join(dir, "seeded.db")
	st, err := Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	var version int
	if err := st.DB.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != CurrentSchemaVersion() {
		t.Fatalf("user_version = %d, want %d", version, CurrentSchemaVersion())
	}
	if _, err := st.InsertMemo("template", "seeded store is writable", "test", "", nil); err != nil {
		t.Fatalf("write on seeded store: %v", err)
	}
}
