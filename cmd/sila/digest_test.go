package main

import (
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestRunDigestOutputsMarkdown(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "k.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	// Seed a message so the digest has content.
	_, _, err = st.WriteRun("", "devin", "1", []store.LoadMessage{
		{ProjectID: "p1", ContentHash: "h1", TS: "2099-01-01T00:00:00Z", Role: "assistant", Content: "future message", Tool: "devin"},
	})
	st.Close()
	if err != nil {
		t.Fatalf("WriteRun: %v", err)
	}

	origWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runDigest(ui, dbPath, "p1", "24h", "", 0, false, false, false, false, false, false, nil)
	})
	if !strings.Contains(out, "# sila · standup") {
		t.Errorf("expected markdown header, got %q", out)
	}
}

func TestRunDigestJSON(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "k.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	st.Close()

	origWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runDigest(ui, dbPath, "p1", "24h", "", 0, false, true, false, false, false, false, nil)
	})
	if !strings.Contains(out, "\"generated_at\"") {
		t.Errorf("expected JSON output with generated_at, got %q", out)
	}
}

func TestRunDigestTOON(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "k.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	st.Close()

	origWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runDigest(ui, dbPath, "p1", "24h", "", 0, false, false, true, false, false, false, nil)
	})
	if !strings.Contains(out, "digest:") {
		t.Errorf("expected TOON digest: header, got %q", out)
	}
}

// TestRunDigestHumanRendersRecap keeps `digest --human` bound to the human
// briefing rendering that `sila recap` used to own.
func TestRunDigestHumanRendersRecap(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "k.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	st.Close()

	origWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runDigest(ui, dbPath, "p1", "24h", "", 0, false, false, false, true, false, false, nil)
	})
	if !strings.Contains(out, "# sila · standup") {
		t.Errorf("expected human standup header, got %q", out)
	}
}

func seedActivityFixture(t *testing.T, dbPath string) {
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	if _, err := st.EnsureProject("/workspace/example-project"); err != nil {
		t.Fatalf("EnsureProject: %v", err)
	}

	now := time.Now().UTC()
	t1 := now.Add(-3 * time.Hour) // codex
	t2 := now.Add(-2 * time.Hour) // opencode
	t3 := now.Add(-1 * time.Hour) // muse

	_, _, _ = st.WriteRun("", "codex", "sess-a1", []store.LoadMessage{
		{ProjectID: "example-project", ContentHash: "h1", TS: t1.Format(time.RFC3339), Role: "user", Content: "Investigate why settings fail to save", Tool: "codex", SessionID: "sess-a1", SourceOrder: 1, SourceTime: store.SortableTime(t1)},
		{ProjectID: "example-project", ContentHash: "h2", TS: t1.Add(time.Minute).Format(time.RFC3339), Role: "assistant", Content: "The API rejects an empty timezone value", Tool: "codex", SessionID: "sess-a1", SourceOrder: 2, SourceTime: store.SortableTime(t1.Add(time.Minute))},
	})

	_, _, _ = st.WriteRun("", "opencode", "sess-b2", []store.LoadMessage{
		{ProjectID: "example-project", ContentHash: "h3", TS: t2.Format(time.RFC3339), Role: "user", Content: "Fix the settings form validation", Tool: "opencode", SessionID: "sess-b2", SourceOrder: 1, SourceTime: store.SortableTime(t2)},
		{ProjectID: "example-project", ContentHash: "h4", TS: t2.Add(time.Minute).Format(time.RFC3339), Role: "assistant", Content: "Added validation and tests; ready for review", Tool: "opencode", SessionID: "sess-b2", SourceOrder: 2, SourceTime: store.SortableTime(t2.Add(time.Minute))},
	})

	_, _, _ = st.WriteRun("", "muse", "sess-c3", []store.LoadMessage{
		{ProjectID: "example-project", ContentHash: "h5", TS: t3.Format(time.RFC3339), Role: "user", Content: "Check keyboard navigation in the settings form", Tool: "muse", SessionID: "sess-c3", SourceOrder: 1, SourceTime: store.SortableTime(t3)},
		{ProjectID: "example-project", ContentHash: "h6", TS: t3.Add(time.Minute).Format(time.RFC3339), Role: "assistant", Content: "Found a missing label on the timezone selector", Tool: "muse", SessionID: "sess-c3", SourceOrder: 2, SourceTime: store.SortableTime(t3.Add(time.Minute))},
	})
}

func TestRunDigestActivityCLI(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "k.db")
	seedActivityFixture(t, dbPath)

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runDigest(ui, dbPath, "example-project", "24h", "", 10, false, false, false, false, true, false, nil)
	})

	// Header
	if !strings.Contains(out, "sila · activity · example-project · last 24h · indexed history") {
		t.Errorf("expected activity header, got:\n%s", out)
	}

	// Tool lines
	if !strings.Contains(out, "muse") || !strings.Contains(out, "opencode") || !strings.Contains(out, "codex") {
		t.Errorf("expected all 3 tools in output, got:\n%s", out)
	}

	// Excerpts
	if !strings.Contains(out, "Check keyboard navigation in the settings form") {
		t.Errorf("expected muse request excerpt, got:\n%s", out)
	}
	if !strings.Contains(out, "Found a missing label on the timezone selector") {
		t.Errorf("expected muse reply excerpt, got:\n%s", out)
	}

	// Inspect lines
	if !strings.Contains(out, "Inspect reply: sila search expand") {
		t.Errorf("expected Inspect reply drill-down, got:\n%s", out)
	}

	// Both excerpts carry a date-stamped time, so a reader can see that a
	// session's earliest request and latest reply are not one exchange.
	if !strings.Contains(out, "Request ") || !strings.Contains(out, "Reply   ") {
		t.Errorf("expected Request and Reply rows, got:\n%s", out)
	}
	stamp := regexp.MustCompile(`\d\d-\d\d \d\d:\d\d`)
	if got := len(stamp.FindAllString(out, -1)); got < 6 {
		t.Errorf("expected a stamp for the session and both excerpts of every session, found %d:\n%s", got, out)
	}
}

func TestRunDigestActivityJSON(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "k.db")
	seedActivityFixture(t, dbPath)

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runDigest(ui, dbPath, "example-project", "24h", "", 10, false, true, false, false, true, false, nil)
	})

	if !strings.Contains(out, `"project": "example-project"`) {
		t.Errorf("expected project in JSON, got:\n%s", out)
	}
	if !strings.Contains(out, `"total_matching": 3`) {
		t.Errorf("expected total_matching: 3 in JSON, got:\n%s", out)
	}
	if !strings.Contains(out, `"session_id": "sess-c3"`) {
		t.Errorf("expected full session_id in JSON, got:\n%s", out)
	}
	if !strings.Contains(out, `"occurrence":`) {
		t.Errorf("expected occurrence reference in JSON, got:\n%s", out)
	}
}

func TestRunDigestActivityTOON(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "k.db")
	seedActivityFixture(t, dbPath)

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runDigest(ui, dbPath, "example-project", "24h", "", 10, false, false, true, false, true, false, nil)
	})

	if !strings.Contains(out, "activity_summary:") {
		t.Errorf("expected activity_summary: object in TOON, got:\n%s", out)
	}
	if !strings.Contains(out, "sessions[3]{time,tool,session,request_id,request_occ,reply_id,reply_occ,request,reply}:") {
		t.Errorf("expected sessions table in TOON, got:\n%s", out)
	}
	if !strings.Contains(out, "sess-c3") || !strings.Contains(out, "sess-b2") || !strings.Contains(out, "sess-a1") {
		t.Errorf("expected session entries in TOON, got:\n%s", out)
	}
}

func TestRunDigestActivityToolFilter(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "k.db")
	seedActivityFixture(t, dbPath)

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runDigest(ui, dbPath, "example-project", "24h", "opencode", 10, false, false, false, false, true, false, nil)
	})

	if !strings.Contains(out, "opencode") {
		t.Errorf("expected opencode in output, got:\n%s", out)
	}
	if strings.Contains(out, "sess-c3") || strings.Contains(out, "sess-a1") {
		t.Errorf("expected only opencode session, got:\n%s", out)
	}
}

func TestRunDigestActivityEmptyView(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "k.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := st.EnsureProject("/workspace/empty-project"); err != nil {
		t.Fatalf("EnsureProject: %v", err)
	}
	st.Close()

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runDigest(ui, dbPath, "empty-project", "24h", "", 10, false, false, false, false, true, false, nil)
	})

	if !strings.Contains(out, "No recent activity in this window (indexed history).") {
		t.Errorf("expected empty activity message, got:\n%s", out)
	}
	if !strings.Contains(out, "Run `sila sync` to index recent sessions, or use `--since=7d` for a wider window.") {
		t.Errorf("expected sync hint, got:\n%s", out)
	}
}

func TestRunDigestActivityRejectsHuman(t *testing.T) {
	if os.Getenv("TEST_SUBPROCESS_ACTIVITY_HUMAN") == "1" {
		ui := render.New(os.Stdout, true)
		runDigest(ui, "k.db", "p", "24h", "", 10, false, false, false, true, true, false, nil)
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestRunDigestActivityRejectsHuman")
	cmd.Env = append(os.Environ(), "TEST_SUBPROCESS_ACTIVITY_HUMAN=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected non-zero exit when --activity combined with --human")
	}
	if !strings.Contains(string(out), "--activity cannot be combined with --human") {
		t.Errorf("expected mutual exclusion message, got:\n%s", string(out))
	}
}

func TestRunDigestActivityRejectsGlobal(t *testing.T) {
	if os.Getenv("TEST_SUBPROCESS_ACTIVITY_GLOBAL") == "1" {
		ui := render.New(os.Stdout, true)
		runDigest(ui, "k.db", "p", "24h", "", 10, false, false, false, false, true, true, nil)
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestRunDigestActivityRejectsGlobal")
	cmd.Env = append(os.Environ(), "TEST_SUBPROCESS_ACTIVITY_GLOBAL=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected non-zero exit when --activity combined with --global")
	}
	if !strings.Contains(string(out), "--global is not supported") {
		t.Errorf("expected global unsupported message, got:\n%s", string(out))
	}
}

func TestRunDigestActivityRejectsInvalidTop(t *testing.T) {
	if os.Getenv("TEST_SUBPROCESS_ACTIVITY_TOP") == "1" {
		ui := render.New(os.Stdout, true)
		runDigest(ui, "k.db", "p", "24h", "", 0, true, false, false, false, true, false, nil)
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestRunDigestActivityRejectsInvalidTop")
	cmd.Env = append(os.Environ(), "TEST_SUBPROCESS_ACTIVITY_TOP=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected non-zero exit for top=0")
	}
	if !strings.Contains(string(out), "--top must be between 1 and 100") {
		t.Errorf("expected top range error, got:\n%s", string(out))
	}
}

func TestRunDigestActivityRejectsInvalidSince(t *testing.T) {
	if os.Getenv("TEST_SUBPROCESS_ACTIVITY_SINCE") == "1" {
		ui := render.New(os.Stdout, true)
		runDigest(ui, "k.db", "p", "invalid-since", "", 10, false, false, false, false, true, false, nil)
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestRunDigestActivityRejectsInvalidSince")
	cmd.Env = append(os.Environ(), "TEST_SUBPROCESS_ACTIVITY_SINCE=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected non-zero exit for invalid --since")
	}
	if !strings.Contains(string(out), "invalid --since") {
		t.Errorf("expected invalid since error, got:\n%s", string(out))
	}
}

func TestRunDigestActivityRejectsOutsideProject(t *testing.T) {
	if os.Getenv("TEST_SUBPROCESS_ACTIVITY_OUTSIDE") == "1" {
		ui := render.New(os.Stdout, true)
		runDigest(ui, "k.db", "", "24h", "", 10, false, false, false, false, true, false, nil)
		return
	}
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "k.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	st.Close()

	cmd := exec.Command(os.Args[0], "-test.run=TestRunDigestActivityRejectsOutsideProject")
	cmd.Env = append(os.Environ(), "TEST_SUBPROCESS_ACTIVITY_OUTSIDE=1")
	cmd.Dir = dir // temp dir has no registered project
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected non-zero exit when outside known project")
	}
	if !strings.Contains(string(out), "not inside a known project; specify --project=<id>") {
		t.Errorf("expected outside known project error, got:\n%s", string(out))
	}
}

func TestRunDigestActivityRejectsUnknownProject(t *testing.T) {
	if os.Getenv("TEST_SUBPROCESS_ACTIVITY_UNKNOWN") == "1" {
		ui := render.New(os.Stdout, true)
		runDigest(ui, "k.db", "nonexistent-project", "24h", "", 10, false, false, false, false, true, false, nil)
		return
	}
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "k.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	st.Close()

	cmd := exec.Command(os.Args[0], "-test.run=TestRunDigestActivityRejectsUnknownProject")
	cmd.Env = append(os.Environ(), "TEST_SUBPROCESS_ACTIVITY_UNKNOWN=1")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected non-zero exit for unknown project")
	}
	if !strings.Contains(string(out), "no registered project matches") {
		t.Errorf("expected no registered project error, got:\n%s", string(out))
	}
}

// TestRunDigestActivityWrapsLongExcerpts pins the layout fix: a long excerpt
// must wrap under its own text column instead of running to the terminal edge,
// where in a narrow window a continuation would read as another entry.
func TestRunDigestActivityWrapsLongExcerpts(t *testing.T) {
	t.Setenv("COLUMNS", "64")
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "k.db")
	seedActivityFixture(t, dbPath)

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runDigest(ui, dbPath, "example-project", "24h", "", 10, false, false, false, false, true, false, nil)
	})

	// Indent column is 2 + 8 (label) + 11 (stamp) + 2 = 23.
	textColumn := strings.Repeat(" ", 23)
	if !strings.Contains(out, "\n"+textColumn) {
		t.Errorf("expected wrapped continuations indented to the text column:\n%s", out)
	}
	for line := range strings.SplitSeq(out, "\n") {
		if !strings.HasPrefix(line, "  ") {
			continue // the header and footer are titles, not entry text
		}
		if len(line) > 64 {
			t.Errorf("entry line is %d columns in a 64-column window: %q", len(line), line)
		}
	}
}

func TestParseHumanStandupWindow(t *testing.T) {
	cases := []struct {
		args      []string
		sinceFlag string
		expected  string
		hasErr    bool
	}{
		{nil, "", "24h", false},
		{nil, "12h", "12h", false},
		{[]string{"last", "5h"}, "", "5h", false},
		{[]string{"last", "2d"}, "", "2d", false},
		{[]string{"last", "1w"}, "", "1w", false},
		{[]string{"last", "30m"}, "", "30m", false},
		{[]string{"last", "5", "hours"}, "", "5h", false},
		{[]string{"last", "2", "days"}, "", "2d", false},
		{[]string{"last", "1", "week"}, "", "1w", false},
		{[]string{"last", "30", "minutes"}, "", "30m", false},
		{[]string{"5h"}, "", "5h", false},
		{[]string{"2d"}, "", "2d", false},
		{[]string{"1w"}, "", "1w", false},
		{[]string{"30m"}, "", "30m", false},
		{[]string{"24h"}, "", "24h", false},
		{[]string{"last", "day"}, "", "24h", false},
		{[]string{"last", "week"}, "", "1w", false},
		{[]string{"last", "month"}, "", "30d", false},
		{[]string{"last-3d"}, "", "3d", false},
		{[]string{"invalid-window-text"}, "", "", true},
	}

	for _, c := range cases {
		got, err := parseHumanStandupWindow(c.args, c.sinceFlag)
		if c.hasErr {
			if err == nil {
				t.Errorf("parseHumanStandupWindow(%v, %q) expected error, got %q", c.args, c.sinceFlag, got)
			}
		} else {
			if err != nil {
				t.Errorf("parseHumanStandupWindow(%v, %q) unexpected error: %v", c.args, c.sinceFlag, err)
			}
			if got != c.expected {
				t.Errorf("parseHumanStandupWindow(%v, %q) = %q, want %q", c.args, c.sinceFlag, got, c.expected)
			}
		}
	}
}

func TestRunStandupDefault(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "k.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	st.Close()

	origWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runStandup(ui, dbPath, "p1", "24h", "", 10, false, false, false, false, false, nil)
	})
	if !strings.Contains(out, "# sila · standup") {
		t.Errorf("expected standup header, got:\n%s", out)
	}
	if !strings.Contains(out, "Scope: p1") {
		t.Errorf("expected Scope: p1, got:\n%s", out)
	}
}

func TestRunStandupPositionalArgs(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "k.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	st.Close()

	origWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runStandup(ui, dbPath, "p1", "", "", 10, false, false, false, false, false, []string{"last", "5h"})
	})
	if !strings.Contains(out, "# sila · standup") {
		t.Errorf("expected standup header, got:\n%s", out)
	}
}

func TestRunStandupGlobal(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "k.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	if _, err := st.EnsureProject("/workspace/proj-a"); err != nil {
		t.Fatalf("EnsureProject proj-a: %v", err)
	}
	if _, err := st.EnsureProject("/workspace/proj-b"); err != nil {
		t.Fatalf("EnsureProject proj-b: %v", err)
	}

	now := time.Now().UTC()
	_ = st.UpsertFact(store.Fact{
		ID: "fa", ProjectID: "proj-a", Claim: "Decision A: use pure-Go SQLite.",
		Kind: "invariant", PromotedAt: now.Add(-1 * time.Hour).Format(time.RFC3339),
	})
	_ = st.UpsertFact(store.Fact{
		ID: "fb", ProjectID: "proj-b", Claim: "Decision B: use FTS5 for fast text search.",
		Kind: "invariant", PromotedAt: now.Add(-30 * time.Minute).Format(time.RFC3339),
	})
	_ = st.InsertHandoff(store.Handoff{
		ID: "ha", ProjectID: "proj-a", Agent: "devin", Branch: "main",
		Summary: "handoff a", OpenItems: []string{"task for proj a"},
		CreatedAt: now.Add(-40 * time.Minute).Format(time.RFC3339), Status: "open",
	})
	_ = st.InsertHandoff(store.Handoff{
		ID: "hb", ProjectID: "proj-b", Agent: "claude", Branch: "main",
		Summary: "handoff b", OpenItems: []string{"task for proj b"},
		CreatedAt: now.Add(-20 * time.Minute).Format(time.RFC3339), Status: "open",
	})
	st.Close()

	origWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runStandup(ui, dbPath, "", "", "", 10, false, false, false, false, true, []string{"last", "2d"})
	})

	if !strings.Contains(out, "Scope: all projects") {
		t.Errorf("expected Scope: all projects, got:\n%s", out)
	}
	if !strings.Contains(out, "[proj-a]") || !strings.Contains(out, "[proj-b]") {
		t.Errorf("expected [proj-a] and [proj-b] tags in global output, got:\n%s", out)
	}
	if !strings.Contains(out, "task for proj a") || !strings.Contains(out, "task for proj b") {
		t.Errorf("expected open items from both projects in global output, got:\n%s", out)
	}
}

func TestRunStandupTOON(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "k.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	st.Close()

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runStandup(ui, dbPath, "p1", "24h", "", 10, false, false, true, false, false, nil)
	})
	if !strings.Contains(out, "standup_summary:") {
		t.Errorf("expected standup_summary in TOON, got:\n%s", out)
	}
}

func TestReorderFlagsWithBoolFlags(t *testing.T) {
	fs := flag.NewFlagSet("standup", flag.ContinueOnError)
	globalFlag := fs.Bool("global", false, "global")
	fs.BoolVar(globalFlag, "g", false, "shorthand")
	projectFlag := fs.String("project", "", "project")
	_ = projectFlag
	_ = fs.Bool("json", false, "json")

	// 1. --global followed by positional "last 5h"
	flags, pos := reorderFlags(fs, []string{"--global", "last", "5h"})
	if len(flags) != 1 || flags[0] != "--global" {
		t.Errorf("flags = %v, want [--global]", flags)
	}
	if len(pos) != 2 || pos[0] != "last" || pos[1] != "5h" {
		t.Errorf("pos = %v, want [last 5h]", pos)
	}

	// 2. -g followed by positional "last 2d"
	flags, pos = reorderFlags(fs, []string{"-g", "last", "2d"})
	if len(flags) != 1 || flags[0] != "-g" {
		t.Errorf("flags = %v, want [-g]", flags)
	}
	if len(pos) != 2 || pos[0] != "last" || pos[1] != "2d" {
		t.Errorf("pos = %v, want [last 2d]", pos)
	}

	// 3. String flag --project consumes next arg, leaves positionals intact
	flags, pos = reorderFlags(fs, []string{"--project", "sila", "last", "5h"})
	if len(flags) != 2 || flags[0] != "--project" || flags[1] != "sila" {
		t.Errorf("flags = %v, want [--project sila]", flags)
	}
	if len(pos) != 2 || pos[0] != "last" || pos[1] != "5h" {
		t.Errorf("pos = %v, want [last 5h]", pos)
	}

	// 4. Flags after positionals
	flags, pos = reorderFlags(fs, []string{"last", "5h", "-g"})
	if len(flags) != 1 || flags[0] != "-g" {
		t.Errorf("flags = %v, want [-g]", flags)
	}
	if len(pos) != 2 || pos[0] != "last" || pos[1] != "5h" {
		t.Errorf("pos = %v, want [last 5h]", pos)
	}
}
