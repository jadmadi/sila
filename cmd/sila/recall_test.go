package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestRecallCLIJSON(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "recall_cli.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	proj, err := st.EnsureProject(tmp)
	if err != nil {
		st.Close()
		t.Fatal(err)
	}
	if _, err := st.InsertLesson(proj.ID, store.FormatLesson("xlsx export hangs", "shared strings", "stream rows", "xlsx"), "agent", tmp, nil); err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runRecall(ui, dbPath, proj.ID, 5, false, true, false, []string{"xlsx", "export", "hangs"})
	})

	var res struct {
		Query string `json:"query"`
		Total int    `json:"total"`
		Hits  []struct {
			Kind      string `json:"kind"`
			Grounded  bool   `json:"grounded"`
			SourceRef string `json:"source_ref"`
		} `json:"hits"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("recall output is not JSON: %q: %v", out, err)
	}
	if res.Total != 1 || len(res.Hits) != 1 {
		t.Fatalf("recall total = %d, hits = %d, want 1", res.Total, len(res.Hits))
	}
	if res.Hits[0].Kind != "lesson" {
		t.Fatalf("hit kind = %q, want lesson", res.Hits[0].Kind)
	}
}

func setupRecallContextCLI(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "recall_context_cli.db")
	for _, rel := range []string{"scripts/deploy.sh", "internal/other.go"} {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("fixture"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	proj, err := st.EnsureProject(dir)
	if err != nil {
		st.Close()
		t.Fatal(err)
	}
	if _, err := st.InsertLesson(proj.ID, store.FormatLesson("deploy ordering race", "shared state", "serialize deploy steps", ""), "agent", dir, []string{"scripts/deploy.sh"}); err != nil {
		st.Close()
		t.Fatal(err)
	}
	if _, err := st.InsertLesson(proj.ID, store.FormatLesson("deploy ordering retry", "lock timeout", "retry deploy", ""), "agent", dir, []string{"internal/other.go"}); err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()
	return dbPath, proj.ID
}

type recallCLIPayload struct {
	Query string `json:"query"`
	Pack  struct {
		Items          []struct{ Kind, ID string } `json:"items"`
		Tokens         int                         `json:"tokens"`
		Omitted        int                         `json:"omitted"`
		EmptyReason    string                      `json:"empty_reason"`
		RankingVersion string                      `json:"ranking_version"`
	} `json:"pack"`
	Context struct {
		TaskPresent bool `json:"task_present"`
		PathsCount  int  `json:"paths_count"`
	} `json:"context"`
	EmptyReason string `json:"empty_reason"`
}

func TestRecallCLIContextFlags(t *testing.T) {
	dbPath, projectID := setupRecallContextCLI(t)
	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runRecallWithContext(ui, dbPath, projectID, 5, false, true, false, []string{"deploy", "ordering"}, recallContextInput{
			Task:    "zebra refresh",
			Paths:   "scripts/deploy.sh",
			Branch:  "main",
			Head:    "4f2a91c",
			Symbols: "serializeDeploy",
			Budget:  "1200",
		})
	})
	var payload recallCLIPayload
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("recall output is not JSON: %q: %v", out, err)
	}
	if payload.Pack.RankingVersion != "recall-pack/v1" {
		t.Fatalf("ranking version = %q", payload.Pack.RankingVersion)
	}
	if len(payload.Pack.Items) == 0 {
		t.Fatalf("pack is empty: %s", out)
	}
	if payload.Pack.Items[0].Kind != "lesson" {
		t.Fatalf("first pack item kind = %q, want lesson", payload.Pack.Items[0].Kind)
	}
	if !payload.Context.TaskPresent || payload.Context.PathsCount != 1 {
		t.Fatalf("context summary = %+v", payload.Context)
	}
	if strings.Contains(out, "zebra refresh") {
		t.Fatalf("JSON echoed the task: %s", out)
	}
	if strings.Contains(out, "scripts/deploy.sh") {
		t.Fatalf("JSON echoed the context path: %s", out)
	}
}

func TestRecallCLIContextFile(t *testing.T) {
	dbPath, projectID := setupRecallContextCLI(t)
	contextPath := filepath.Join(t.TempDir(), "context.json")
	payload := `{"task":"deploy ordering","paths":["scripts/deploy.sh"],"branch":"main","head":"4f2a91c","symbols":["serializeDeploy"]}`
	if err := os.WriteFile(contextPath, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runRecallWithContext(ui, dbPath, projectID, 5, false, true, false, []string{"deploy", "ordering"}, recallContextInput{
			ContextFile: contextPath,
			Budget:      "1200",
		})
	})
	var parsed recallCLIPayload
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("recall output is not JSON: %q: %v", out, err)
	}
	if parsed.Context.PathsCount != 1 || !parsed.Context.TaskPresent {
		t.Fatalf("context summary = %+v", parsed.Context)
	}
	if len(parsed.Pack.Items) == 0 {
		t.Fatalf("context file produced an empty pack: %s", out)
	}
}

func TestRecallCLIContextFileConflicts(t *testing.T) {
	contextPath := filepath.Join(t.TempDir(), "context.json")
	if err := os.WriteFile(contextPath, []byte(`{"task":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := parseRecallContextInput(recallContextInput{ContextFile: contextPath, Task: "x"}); err == nil {
		t.Fatal("expected a conflict error for context-file plus --task")
	} else if !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("error = %v, want a conflict message", err)
	}
	if _, _, _, err := parseRecallContextInput(recallContextInput{ContextFile: contextPath, Paths: "a.go"}); err == nil {
		t.Fatal("expected a conflict error for context-file plus --paths")
	}
}

func TestRecallCLIContextFileValidation(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	if _, _, _, err := parseRecallContextInput(recallContextInput{ContextFile: filepath.Join(dir, "missing.json")}); err == nil {
		t.Fatal("expected an error for a missing context file")
	}
	if _, _, _, err := parseRecallContextInput(recallContextInput{ContextFile: write("unknown.json", `{"task":"x","bogus":true}`)}); err == nil {
		t.Fatal("expected an error for an unknown context key")
	} else if !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("error = %v, want the offending key name", err)
	} else if strings.Contains(err.Error(), "true") {
		t.Fatalf("error leaked the offending value: %v", err)
	}
	if _, _, _, err := parseRecallContextInput(recallContextInput{ContextFile: write("bad.json", `{`)}); err == nil {
		t.Fatal("expected an error for malformed JSON")
	}
	oversized := filepath.Join(dir, "oversized.json")
	if err := os.WriteFile(oversized, bytes.Repeat([]byte("x"), recallContextMaxFileBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := parseRecallContextInput(recallContextInput{ContextFile: oversized}); err == nil {
		t.Fatal("expected an error for an oversized context file")
	}
}

func TestRecallCLIContextTraversalDiagnostics(t *testing.T) {
	_, diags, _, err := parseRecallContextInput(recallContextInput{Paths: "../secret, /etc/passwd, a/../../b"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(diags) != 3 {
		t.Fatalf("diagnostics = %v, want three rejected paths", diags)
	}
	for _, d := range diags {
		if strings.Contains(d.Reason, "secret") {
			t.Fatalf("diagnostic leaked path text: %+v", d)
		}
	}
	codes := recallDiagnosticCodes(diags)
	if !strings.Contains(codes, "traversal") || !strings.Contains(codes, "absolute_path") {
		t.Fatalf("codes = %q", codes)
	}
}

func TestRecallCLIBudget(t *testing.T) {
	if got, err := parseRecallBudget(""); err != nil || got != 1200 {
		t.Fatalf("default budget = %d, %v", got, err)
	}
	if got, err := parseRecallBudget("256"); err != nil || got != 256 {
		t.Fatalf("min budget = %d, %v", got, err)
	}
	if got, err := parseRecallBudget("8192"); err != nil || got != 8192 {
		t.Fatalf("max budget = %d, %v", got, err)
	}
	for _, bad := range []string{"255", "8193", "abc"} {
		if _, err := parseRecallBudget(bad); err == nil {
			t.Fatalf("budget %q was accepted", bad)
		}
	}
}

func TestRecallCLIFlagsRegistered(t *testing.T) {
	fs := flag.NewFlagSet("recall", flag.ContinueOnError)
	rf := newRootFlags(fs, "recall")
	if err := fs.Parse([]string{"--task=deploy ordering", "--paths=a.go,b.go", "--branch=main", "--head=4f2a91c", "--symbols=runDeploy", "--budget=1200", "--context-file=ctx.json"}); err != nil {
		t.Fatal(err)
	}
	if rf.recallTaskFlag == nil || *rf.recallTaskFlag != "deploy ordering" {
		t.Fatalf("task flag = %v", rf.recallTaskFlag)
	}
	if *rf.recallPathsFlag != "a.go,b.go" || *rf.recallSymbolsFlag != "runDeploy" {
		t.Fatalf("paths/symbols flags = %q / %q", *rf.recallPathsFlag, *rf.recallSymbolsFlag)
	}
	if *rf.branch != "main" || *rf.recallHeadFlag != "4f2a91c" || *rf.budgetFlag != "1200" || *rf.recallContextFile != "ctx.json" {
		t.Fatalf("context flags did not parse")
	}
}

func TestRecallCLIOutputPack(t *testing.T) {
	dbPath, projectID := setupRecallContextCLI(t)
	ui := render.New(os.Stdout, false)
	out := captureStdout(t, func() {
		runRecall(ui, dbPath, projectID, 5, false, false, false, []string{"deploy", "ordering"})
	})
	if !strings.Contains(out, "untrusted, possibly stale data") {
		t.Fatalf("human output missing the untrusted line: %s", out)
	}
	if !strings.Contains(out, "pack:") || !strings.Contains(out, "tokens") {
		t.Fatalf("human output missing pack metrics: %s", out)
	}
}

func TestRecallCLIOutputEmptyPack(t *testing.T) {
	dbPath, projectID := setupRecallContextCLI(t)
	ui := render.New(os.Stdout, false)
	out := captureStdout(t, func() {
		runRecall(ui, dbPath, projectID, 5, false, false, false, []string{"zebra", "quantum", "telescope"})
	})
	if !strings.Contains(out, "empty: no_candidates") {
		t.Fatalf("empty output missing the empty reason: %s", out)
	}
	if !strings.Contains(out, "untrusted, possibly stale data") {
		t.Fatalf("empty output missing the untrusted line: %s", out)
	}
}

func TestRecallPackDocsMatchBehavior(t *testing.T) {
	checks := []struct {
		path     string
		contains []string
	}{
		{"../../docs/reference.md", []string{"--context-file", "recall-pack/v1", "tierBase + 499", "empty_reason", "budget_exhausted"}},
		{"../../docs/mcp.md", []string{"Context-aware recall pack", "max_tokens", "prime_context", "budget_exhausted"}},
		{"../../README.md", []string{"--task", "--context-file", "prime_context"}},
		{"../../CHANGELOG.md", []string{"Context-aware recall packs", "recall-pack"}},
		{"../../project.jsonc", []string{"Context-Aware Recall Packs", "sila recall --task"}},
	}
	for _, check := range checks {
		raw, err := os.ReadFile(check.path)
		if err != nil {
			t.Fatalf("read %s: %v", check.path, err)
		}
		body := string(raw)
		for _, want := range check.contains {
			if !strings.Contains(body, want) {
				t.Errorf("%s does not mention %q", check.path, want)
			}
		}
	}
}

func TestRecallCLIContextPathDiagnostics(t *testing.T) {
	dbPath, projectID := setupRecallContextCLI(t)
	ui := render.New(os.Stdout, true)
	var stderr string
	captureStdout(t, func() {
		stderr = captureStderr(t, func() {
			runRecallWithContext(ui, dbPath, projectID, 5, false, true, false, []string{"deploy", "ordering"}, recallContextInput{
				Paths: "../secret.env,scripts/deploy.sh",
			})
		})
	})
	if !strings.Contains(stderr, "traversal") {
		t.Fatalf("stderr is missing the traversal diagnostic: %s", stderr)
	}
	if strings.Contains(stderr, "secret.env") {
		t.Fatalf("stderr leaked the rejected path: %s", stderr)
	}
}
