package dream

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

func seedDigestStore(t *testing.T) *store.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "digest.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	// Insert messages from two tools within the last hour.
	_, _, err = st.WriteRun("", "devin", "1", []store.LoadMessage{
		{ProjectID: "p1", ContentHash: "h1", TS: now, Role: "assistant", Content: "Refactored auth module", Tool: "devin"},
		{ProjectID: "p1", ContentHash: "h2", TS: now, Role: "user", Content: "ship it", Tool: "devin"},
	})
	if err != nil {
		t.Fatalf("WriteRun devin: %v", err)
	}
	_, _, err = st.WriteRun("", "claude", "1", []store.LoadMessage{
		{ProjectID: "p1", ContentHash: "h3", TS: now, Role: "assistant", Content: "Added test coverage for FTS5", Tool: "claude"},
	})
	if err != nil {
		t.Fatalf("WriteRun claude: %v", err)
	}

	// Insert a memo (manual_memo) — should appear in new invariants.
	if _, err := st.InsertMemo("p1", "Always use pure-Go SQLite", "devin", "/tmp/p1", []string{"internal/store/store.go"}); err != nil {
		t.Fatalf("InsertMemo: %v", err)
	}

	// Insert an open handoff.
	if err := st.InsertHandoff(store.Handoff{
		ProjectID: "p1", Agent: "devin", Branch: "main",
		Summary:   "Refactored auth, tests pending",
		OpenItems: []string{"Add e2e tests for token refresh"},
		Status:    "open",
	}); err != nil {
		t.Fatalf("InsertHandoff: %v", err)
	}

	return st
}

func TestBuildDigestAggregatesActivity(t *testing.T) {
	st := seedDigestStore(t)
	defer st.Close()

	report, err := BuildDigest(st, "p1", "24h", "")
	if err != nil {
		t.Fatalf("BuildDigest: %v", err)
	}
	if report.TotalMessages != 3 {
		t.Errorf("TotalMessages = %d, want 3", report.TotalMessages)
	}
	if len(report.ToolActivity) != 2 {
		t.Errorf("ToolActivity = %d tools, want 2", len(report.ToolActivity))
	}
	// devin has 2 messages, claude has 1 — devin should be first.
	if report.ToolActivity[0].Tool != "devin" || report.ToolActivity[0].Messages != 2 {
		t.Errorf("top tool = %+v, want devin/2", report.ToolActivity[0])
	}
	if report.TotalNewMemos != 1 {
		t.Errorf("TotalNewMemos = %d, want 1", report.TotalNewMemos)
	}
	if len(report.NewInvariants) < 1 {
		t.Errorf("NewInvariants = %d, want >= 1", len(report.NewInvariants))
	}
	if report.TotalHandoffs != 1 || report.OpenHandoffs != 1 {
		t.Errorf("Handoffs = %d/%d, want 1/1", report.TotalHandoffs, report.OpenHandoffs)
	}
	if len(report.OpenHandoffItems) != 1 {
		t.Errorf("OpenHandoffItems = %d, want 1", len(report.OpenHandoffItems))
	}
}

func TestBuildDigestEmptyWindow(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "empty.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	// Insert a message from 30 days ago.
	old := time.Now().UTC().Add(-30 * 24 * time.Hour).Format(time.RFC3339)
	_, _, err = st.WriteRun("", "devin", "1", []store.LoadMessage{
		{ProjectID: "p1", ContentHash: "h1", TS: old, Role: "assistant", Content: "old message", Tool: "devin"},
	})
	if err != nil {
		t.Fatalf("WriteRun: %v", err)
	}

	report, err := BuildDigest(st, "p1", "24h", "")
	if err != nil {
		t.Fatalf("BuildDigest: %v", err)
	}
	if report.TotalMessages != 0 {
		t.Errorf("TotalMessages = %d, want 0 for 24h window on 30d-old data", report.TotalMessages)
	}
	if len(report.ToolActivity) != 0 {
		t.Errorf("ToolActivity = %d, want 0", len(report.ToolActivity))
	}
}

func TestParseDigestDuration(t *testing.T) {
	cases := map[string]time.Duration{
		"24h": 24 * time.Hour,
		"7d":  7 * 24 * time.Hour,
		"2w":  14 * 24 * time.Hour,
		"30m": 30 * time.Minute,
	}
	for in, want := range cases {
		got, err := parseDigestDuration(in)
		if err != nil || got != want {
			t.Errorf("parseDigestDuration(%q) = %v (%v), want %v", in, got, err, want)
		}
	}
	// Empty and bad inputs return errors; computeSinceRFC3339 applies the 24h fallback.
	if _, err := parseDigestDuration(""); err == nil {
		t.Error("parseDigestDuration(\"\") should error")
	}
	if _, err := parseDigestDuration("bad"); err == nil {
		t.Error("parseDigestDuration(\"bad\") should error")
	}
}

func TestRenderDigestMarkdownSections(t *testing.T) {
	st := seedDigestStore(t)
	defer st.Close()
	report, err := BuildDigest(st, "p1", "24h", "")
	if err != nil {
		t.Fatalf("BuildDigest: %v", err)
	}
	md := RenderDigestMarkdown(report)
	required := []string{
		"# sila · standup",
		"## Active Sessions by Tool",
		"## New Architectural Invariants & Memos",
		"## Closed Goals & Tasks",
		"## Open Handoffs",
		"## Stale Claims Alert",
		"devin",
		"claude",
	}
	for _, s := range required {
		if !strings.Contains(md, s) {
			t.Errorf("markdown missing %q", s)
		}
	}
}

func TestRenderDigestMarkdownEmpty(t *testing.T) {
	md := RenderDigestMarkdown(&DigestReport{
		Since: time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339),
	})
	if !strings.Contains(md, "_No messages recorded") {
		t.Errorf("empty digest should have definite empty state, got:\n%s", md)
	}
	if !strings.Contains(md, "_No new invariants") {
		t.Errorf("empty digest should mention no invariants")
	}
	if !strings.Contains(md, "_All claims grounded") {
		t.Errorf("empty digest should report no stale claims")
	}
}

func TestRenderDigestTOON(t *testing.T) {
	st := seedDigestStore(t)
	defer st.Close()
	report, err := BuildDigest(st, "p1", "24h", "")
	if err != nil {
		t.Fatalf("BuildDigest: %v", err)
	}
	toon := RenderDigestTOON(report)
	if !strings.Contains(toon, "digest:") {
		t.Errorf("TOON missing digest object header")
	}
	if !strings.Contains(toon, "tools[2]{tool,messages,last}:") {
		t.Errorf("TOON missing tools table header, got:\n%s", toon)
	}
	if !strings.Contains(toon, "devin") {
		t.Errorf("TOON missing devin entry")
	}
}

func TestRenderDigestTOONEmpty(t *testing.T) {
	toon := RenderDigestTOON(&DigestReport{})
	if !strings.Contains(toon, "digest:") {
		t.Errorf("empty TOON should still have digest header")
	}
	if !strings.Contains(toon, "tools[0]") {
		t.Errorf("empty TOON should have tools[0] definite empty state")
	}
}
