package ingest

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/loaders"
	"github.com/jadmadi/sila/internal/store"
)

// initGitRepo creates a temp repository with one commit and returns its path.
func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "a.txt")
	run("commit", "-qm", "feat: initial work")
	return dir
}

// TestScopedGitIngestUsesPerRepoWatermark covers the project-scoped git path:
// the selector must resolve to the repository directory (so commits actually
// load) and the cursor must be tracked per repository, leaving the shared git
// watermark untouched.
func TestScopedGitIngestUsesPerRepoWatermark(t *testing.T) {
	repo := initGitRepo(t)
	st, err := store.Open(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p, err := st.EnsureProject(repo)
	if err != nil {
		t.Fatal(err)
	}

	srcMap := map[string]*loaders.Source{
		"git": {Name: "git", New: loaders.NewGitLoader},
	}
	results, err := runWithSources(st, srcMap, Options{ProjectSel: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Error != nil {
		t.Fatalf("git ingest failed: %+v", results)
	}
	if results[0].NewRecords < 1 {
		t.Fatalf("expected the repository commit to be ingested, got %+v", results[0])
	}

	scopeKey := "git:" + p.ID
	if wm, _ := st.GetWatermark("", scopeKey); wm == "" {
		t.Fatalf("expected scoped watermark %q to advance", scopeKey)
	}
	if wm, _ := st.GetWatermark("", "git"); wm != "" {
		t.Fatalf("shared git watermark should stay unset, got %q", wm)
	}

	again, err := runWithSources(st, srcMap, Options{ProjectSel: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	if again[0].NewRecords != 0 {
		t.Fatalf("expected a no-op re-run, got %+v", again[0])
	}
}

func TestContentHashDeterministic(t *testing.T) {
	if store.ContentHash("user", "x") != store.ContentHash("user", "x") {
		t.Fatal("hash not deterministic")
	}
	if store.ContentHash("user", "x") == store.ContentHash("assistant", "x") {
		t.Fatal("role must affect hash")
	}
}

// TestRunOverSyntheticJSONL runs a hermetic ingest over a temp-file JSONL
// source, verifying: a tool with no data yields a result without error and a
// watermark; re-running is idempotent on the message table.
func TestRunOverSyntheticJSONL(t *testing.T) {
	dir := t.TempDir()
	srcFile := filepath.Join(dir, "hist.jsonl")
	os.WriteFile(srcFile, []byte("{\"session_id\":\"a\",\"ts\":1000,\"text\":\"do x\"}\n{\"session_id\":\"a\",\"ts\":2000,\"text\":\"do y\"}\n"), 0o644)

	// hermetically scope the source map to exactly two tools over temp paths
	mapSrc := map[string]*loaders.Source{
		"codex": {Name: "codex", File: srcFile, New: loaders.NewCodexLoader},
		"devin": {Name: "devin", DataDir: filepath.Join(dir, "no-devin"), New: loaders.NewDevinLoader},
	}

	st, err := store.Open(filepath.Join(dir, "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	results, err := runWithSources(st, mapSrc, Options{})
	if err != nil {
		t.Fatal(err)
	}
	// only the two requested-by-map tools run (empty Tools = all in map)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Error != nil && results[1].Error != nil {
		t.Fatalf("codex should have data; devin missing-source is expected: %+v", results)
	}

	var n int
	st.DB.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&n)
	if n != 2 {
		t.Fatalf("expected 2 message rows, got %d", n)
	}

	// re-run: dedup keeps rows stable
	if _, err := runWithSources(st, mapSrc, Options{}); err != nil {
		t.Fatal(err)
	}
	st.DB.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&n)
	if n != 2 {
		t.Fatalf("expected still 2 message rows after re-run, got %d", n)
	}

	wm, _ := st.GetWatermark("", "codex")
	if wm == "" {
		t.Fatal("expected codex watermark to be set")
	}
}

func TestMissingSourceError(t *testing.T) {
	dir := t.TempDir()
	mapSrc := map[string]*loaders.Source{
		"devin": {Name: "devin", DataDir: filepath.Join(dir, "absent"), New: loaders.NewDevinLoader},
	}
	st, err := store.Open(filepath.Join(dir, "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	results, err := runWithSources(st, mapSrc, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Error == nil {
		t.Fatalf("absent source should surface errMissingDB: %+v", results)
	}
}

func TestDirMatches(t *testing.T) {
	cases := []struct {
		dir, sel string
		want     bool
	}{
		{"/home/u/sila", "sila", true},
		{"/home/u/sila-old", "sila", false},
		{"/home/u/sila", "sila-old", false},
		{"/home/u/app", "sila-old", false},
		{"/home/u/sila", "/home/u/sila", true},
	}
	for _, c := range cases {
		if got := dirMatches(c.dir, c.sel); got != c.want {
			t.Errorf("dirMatches(%q,%q)=%v, want %v", c.dir, c.sel, got, c.want)
		}
	}
}

func TestScopedIngestDoesNotAdvanceGlobalWatermark(t *testing.T) {
	dir := t.TempDir()
	srcFile := filepath.Join(dir, "hist.jsonl")
	os.WriteFile(srcFile, []byte("{\"session_id\":\"a\",\"ts\":1000,\"text\":\"do x\"}\n"), 0o644)

	mapSrc := map[string]*loaders.Source{
		"codex": {Name: "codex", File: srcFile, New: loaders.NewCodexLoader},
	}

	st, err := store.Open(filepath.Join(dir, "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	results, err := runWithSources(st, mapSrc, Options{ProjectSel: "other"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	wm, _ := st.GetWatermark("", "codex")
	if wm != "" {
		t.Fatalf("expected global watermark to remain unset under scoped ingest, got %q", wm)
	}
}

func TestIngestOnProgressCallback(t *testing.T) {
	dir := t.TempDir()
	srcFile := filepath.Join(dir, "hist.jsonl")
	os.WriteFile(srcFile, []byte("{\"session_id\":\"a\",\"ts\":1000,\"text\":\"do x\"}\n"), 0o644)

	mapSrc := map[string]*loaders.Source{
		"codex": {Name: "codex", File: srcFile, New: loaders.NewCodexLoader},
	}

	st, err := store.Open(filepath.Join(dir, "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	var events []ToolProgress
	opts := Options{
		OnProgress: func(p ToolProgress) {
			events = append(events, p)
		},
	}
	results, err := runWithSources(st, mapSrc, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if len(events) < 4 {
		t.Fatalf("expected at least 4 progress events (scanning, scanned, committing, committed), got %d: %+v", len(events), events)
	}
	phases := map[string]bool{}
	for _, e := range events {
		phases[e.Phase] = true
		if e.Tool != "codex" {
			t.Errorf("expected tool codex, got %s", e.Tool)
		}
	}
	for _, expectedPhase := range []string{"scanning", "scanned", "committing", "committed"} {
		if !phases[expectedPhase] {
			t.Errorf("missing expected progress phase %q", expectedPhase)
		}
	}
}

func TestIngestMissingHarnessNamedInline(t *testing.T) {
	dir := t.TempDir()
	mapSrc := map[string]*loaders.Source{
		"devin": {Name: "devin", DataDir: filepath.Join(dir, "absent"), New: loaders.NewDevinLoader},
	}
	st, err := store.Open(filepath.Join(dir, "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	var buf bytes.Buffer
	if _, err := runWithSources(st, mapSrc, Options{Out: &buf}); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	// AC-F7-a: the warning names the harness.
	// AC-F7-b: non-verbose output stays the same shape otherwise.
	want := "1 harness sources not found: devin (run 'sila sync --verbose' to list)\n"
	if got != want {
		t.Fatalf("missing harness warning = %q, want %q", got, want)
	}
	if !strings.Contains(got, "devin") {
		t.Fatalf("expected missing harness name 'devin' in warning, got: %q", got)
	}
}
