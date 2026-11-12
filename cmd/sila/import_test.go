package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/importmem"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func writeImportFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func importCLISetup(t *testing.T) (dbPath, repo, home string) {
	t.Helper()
	root := t.TempDir()
	repo = filepath.Join(root, "repo")
	home = filepath.Join(root, "home")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath = filepath.Join(root, "sila.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnsureProject(repo); err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()
	t.Setenv("HOME", home)
	t.Chdir(repo)
	return dbPath, repo, home
}

func importCLIMemoryCount(t *testing.T, dbPath, projectID string) int {
	t.Helper()
	st, err := store.OpenRO(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	records, err := st.ListMemoryRecords(store.MemoryListOptions{ProjectID: projectID, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	return len(records)
}

func importCLIProjectID(t *testing.T, dbPath, repo string) string {
	t.Helper()
	st, err := store.OpenRO(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	project, ok, err := st.ProjectForDir(repo)
	if err != nil || !ok {
		t.Fatalf("project: ok=%v err=%v", ok, err)
	}
	return project.ID
}

func TestImportCLIDryRun(t *testing.T) {
	dbPath, repo, home := importCLISetup(t)
	writeImportFixture(t, filepath.Join(home, ".claude", "projects", "p", "memory", "notes.md"),
		"## Decisions\n- keep the local store authoritative\n")
	ui := render.New(os.Stdout, true)
	code := 0
	out := captureStdout(t, func() {
		code = runImport(ui, dbPath, importCLIOptions{from: "all", jsonOut: false})
	})
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(out, "dry run") || !strings.Contains(out, "Candidates: 1") {
		t.Fatalf("dry-run output: %s", out)
	}
	if strings.Contains(out, "keep the local store") || strings.Contains(out, home) {
		t.Fatalf("dry-run echoed content or home path: %s", out)
	}
	if count := importCLIMemoryCount(t, dbPath, importCLIProjectID(t, dbPath, repo)); count != 0 {
		t.Fatalf("dry run wrote %d memories", count)
	}

	raw := captureStdout(t, func() {
		code = runImport(ui, dbPath, importCLIOptions{from: "all", jsonOut: true})
	})
	if code != 0 {
		t.Fatalf("json exit = %d", code)
	}
	var report importmem.ImportReport
	if err := json.Unmarshal([]byte(raw), &report); err != nil {
		t.Fatalf("json report: %v\n%s", err, raw)
	}
	if report.Status != "dry_run" || report.Planned != 1 || report.Created != 0 || report.FilesScanned != 1 {
		t.Fatalf("report: %+v", report)
	}
	if strings.Contains(raw, "keep the local store") {
		t.Fatalf("json echoed content: %s", raw)
	}
}

func TestImportCLIYes(t *testing.T) {
	dbPath, repo, home := importCLISetup(t)
	writeImportFixture(t, filepath.Join(home, ".codex", "memories", "codex.md"),
		"## Lessons\n- wrap errors with context\n")
	ui := render.New(os.Stdout, true)
	code := 0
	out := captureStdout(t, func() {
		code = runImport(ui, dbPath, importCLIOptions{from: "codex", yes: true})
	})
	if code != 0 {
		t.Fatalf("exit = %d, output: %s", code, out)
	}
	if count := importCLIMemoryCount(t, dbPath, importCLIProjectID(t, dbPath, repo)); count != 1 {
		t.Fatalf("count = %d", count)
	}

	raw := captureStdout(t, func() {
		code = runImport(ui, dbPath, importCLIOptions{from: "codex", yes: true, jsonOut: true})
	})
	if code != 0 {
		t.Fatalf("replay exit = %d", code)
	}
	var report importmem.ImportReport
	if err := json.Unmarshal([]byte(raw), &report); err != nil {
		t.Fatal(err)
	}
	if report.Created != 0 || report.Replayed != 1 {
		t.Fatalf("replay report: %+v", report)
	}
	if count := importCLIMemoryCount(t, dbPath, importCLIProjectID(t, dbPath, repo)); count != 1 {
		t.Fatalf("replay wrote a duplicate: %d", count)
	}
}

func TestImportCLIConflicts(t *testing.T) {
	ui := render.New(os.Stdout, true)
	cases := []struct {
		name string
		opts importCLIOptions
		want string
	}{
		{"yes and dry-run", importCLIOptions{yes: true, dryRun: true}, "mutually exclusive"},
		{"max files high", importCLIOptions{yes: true, maxFiles: 201}, "IMPORT_BOUND"},
		{"max files negative", importCLIOptions{yes: true, maxFiles: -1}, "IMPORT_BOUND"},
		{"bad source", importCLIOptions{yes: true, from: "cursor"}, "IMPORT_PROJECT"},
		{"instructions refused", importCLIOptions{yes: true, includeInstructions: true}, "IMPORT_INSTRUCTIONS_REFUSED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code := 0
			errOut := captureStderr(t, func() {
				code = runImport(ui, filepath.Join(t.TempDir(), "missing.db"), tc.opts)
			})
			if code != 2 {
				t.Fatalf("exit = %d", code)
			}
			if !strings.Contains(errOut, tc.want) {
				t.Fatalf("stderr %q does not contain %q", errOut, tc.want)
			}
		})
	}
}

func TestImportCLIProjectResolution(t *testing.T) {
	dbPath, repo, home := importCLISetup(t)
	writeImportFixture(t, filepath.Join(home, ".grok", "memory", "grok.md"), "## Preferences\n- prefer short lines\n")
	ui := render.New(os.Stdout, true)
	projectID := importCLIProjectID(t, dbPath, repo)

	outside := t.TempDir()
	t.Chdir(outside)
	code := 0
	errOut := captureStderr(t, func() {
		code = runImport(ui, dbPath, importCLIOptions{from: "grok", yes: true})
	})
	if code != 2 || !strings.Contains(errOut, "IMPORT_PROJECT") {
		t.Fatalf("unregistered cwd: exit=%d stderr=%q", code, errOut)
	}

	code = 0
	captureStdout(t, func() {
		code = runImport(ui, dbPath, importCLIOptions{from: "grok", yes: true, project: projectID})
	})
	if code != 0 {
		t.Fatalf("explicit project exit = %d", code)
	}

	// Two registered repositories sharing a basename are ambiguous by name.
	parentA := filepath.Join(t.TempDir(), "a")
	parentB := filepath.Join(t.TempDir(), "b")
	pathA := filepath.Join(parentA, "same-name")
	pathB := filepath.Join(parentB, "same-name")
	for _, path := range []string{pathA, pathB} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		st, err := store.Open(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.EnsureProject(path); err != nil {
			st.Close()
			t.Fatal(err)
		}
		st.Close()
	}
	// Re-key the two projects so neither owns the shared basename as an exact
	// ID; the resolver must then report the basename as ambiguous.
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`UPDATE projects SET id = 'amb-a' WHERE path = ?`, store.CanonicalProjectPath(pathA)); err != nil {
		st.Close()
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`UPDATE projects SET id = 'amb-b' WHERE path = ?`, store.CanonicalProjectPath(pathB)); err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()

	code = 0
	errOut = captureStderr(t, func() {
		code = runImport(ui, dbPath, importCLIOptions{from: "grok", yes: true, project: "same-name"})
	})
	if code != 2 || !strings.Contains(errOut, "IMPORT_PROJECT") {
		t.Fatalf("ambiguous project: exit=%d stderr=%q", code, errOut)
	}
}

func TestImportOutputPrivacy(t *testing.T) {
	dbPath, repo, home := importCLISetup(t)
	token := "ghp_" + strings.Repeat("z", 36)
	writeImportFixture(t, filepath.Join(home, ".claude", "projects", "p", "memory", "notes.md"),
		"## Lessons\n- contact alice@example.com about "+token+" and /opt/private/vault\n"+
			"- see "+repo+"/internal/store/memory.go for details\n")
	ui := render.New(os.Stdout, true)

	out := captureStdout(t, func() {
		if code := runImport(ui, dbPath, importCLIOptions{from: "claude", yes: true}); code != 0 {
			t.Fatalf("text exit = %d", code)
		}
	})
	raw := captureStdout(t, func() {
		if code := runImport(ui, dbPath, importCLIOptions{from: "claude", yes: true, jsonOut: true}); code != 0 {
			t.Fatalf("json exit = %d", code)
		}
	})
	for label, output := range map[string]string{"text": out, "json": raw} {
		for _, forbidden := range []string{"alice@example.com", token, "/opt/private", home, repo} {
			if strings.Contains(output, forbidden) {
				t.Fatalf("%s output leaked %q: %s", label, forbidden, output)
			}
		}
	}

	st, err := store.OpenRO(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	records, err := st.ListMemoryRecords(store.MemoryListOptions{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("records = %d", len(records))
	}
	markers := 0
	for _, rec := range records {
		for _, forbidden := range []string{"alice@example.com", token, "/opt/private", home, repo} {
			if strings.Contains(rec.Summary, forbidden) {
				t.Fatalf("stored summary leaked %q: %q", forbidden, rec.Summary)
			}
		}
		if strings.Contains(rec.Summary, "[REDACTED_") {
			markers++
		}
	}
	if markers == 0 {
		t.Fatal("no stored summary carries a redaction marker")
	}
}

func TestImportRegressionParity(t *testing.T) {
	dbPath, repo, home := importCLISetup(t)
	writeImportFixture(t, filepath.Join(home, ".claude", "projects", "p", "memory", "notes.md"),
		"## Fixes\n- check the exit code before continuing\n")
	ui := render.New(os.Stdout, true)
	projectID := importCLIProjectID(t, dbPath, repo)
	captureStdout(t, func() {
		if code := runImport(ui, dbPath, importCLIOptions{from: "claude", yes: true}); code != 0 {
			t.Fatalf("import exit = %d", code)
		}
		runMemo(ui, dbPath, projectID, "tester", "", "", memoOptions{}, []string{"add", "Legacy memo still writes a typed record"})
	})
	if count := importCLIMemoryCount(t, dbPath, projectID); count != 2 {
		t.Fatalf("typed memory count = %d, want import + memo", count)
	}
}

func TestImportCLINoNetworkSurface(t *testing.T) {
	raw, err := os.ReadFile("import.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, forbidden := range []string{`"net/http"`, `"net/url"`, `"os/exec"`, "internal/llm", "internal/provider"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("import.go contains forbidden import %q", forbidden)
		}
	}
}
