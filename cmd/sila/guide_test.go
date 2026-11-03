package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/learn"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

// TestRenderGuideTerminal_KindLabels pins the terminal label matrix per record
// kind: a case must never print failure-mode labels.
func TestRenderGuideTerminal_KindLabels(t *testing.T) {
	ui := render.New(nil, false)

	tests := []struct {
		name   string
		record learn.OperationalLearning
		want   []string
		forbid []string
	}{
		{
			name: "case",
			record: learn.OperationalLearning{
				Kind:      learn.KindCase,
				Title:     "Dependency injection",
				Intent:    "Dependency injection",
				Approach:  "constructor injection",
				Invariant: "explicit dependencies",
			},
			want:   []string{"Intent:", "Approach:", "Invariant:", "constructor injection", "explicit dependencies"},
			forbid: []string{"Symptom:", "Cause:", "Fix:", "Rule:"},
		},
		{
			name: "lesson",
			record: learn.OperationalLearning{
				Kind:          learn.KindLesson,
				Title:         "Flaky test isolation",
				Symptom:       "Order-dependent failure",
				RootCause:     "Shared database state",
				PivotalFix:    "Add beforeEach cleanup",
				GuardrailRule: "Always reset storage",
			},
			want:   []string{"Symptom:", "Cause:", "Fix:", "Rule:", "Order-dependent failure"},
			forbid: []string{"Intent:", "Approach:", "Invariant:", "Directive:"},
		},
		{
			name: "memo",
			record: learn.OperationalLearning{
				Kind:      learn.KindMemo,
				Title:     "Always keep migrations idempotent",
				Directive: "Always keep migrations idempotent across environments",
			},
			want:   []string{"Directive:", "Always keep migrations idempotent across environments"},
			forbid: []string{"Symptom:", "Cause:", "Fix:", "Intent:", "Invariant:"},
		},
		{
			name: "decision",
			record: learn.OperationalLearning{
				Kind:      learn.KindDecision,
				Title:     "Use D1 batching",
				Directive: "Use D1 batching for bulk inserts",
			},
			want:   []string{"Decision:", "Use D1 batching for bulk inserts"},
			forbid: []string{"Symptom:", "Cause:", "Fix:", "Intent:", "Invariant:"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gb := &learn.Guidebook{
				TotalLearnings: 1,
				Sections: []learn.GuidebookSection{
					{Domain: "general", DisplayName: "General", Description: "All", Count: 1, Learnings: []learn.OperationalLearning{tc.record}},
				},
			}
			out := captureStdout(t, func() { renderGuideTerminal(ui, gb) })
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("terminal %s output missing %q:\n%s", tc.name, want, out)
				}
			}
			for _, forbidden := range tc.forbid {
				if strings.Contains(out, forbidden) {
					t.Errorf("terminal %s output leaked %q:\n%s", tc.name, forbidden, out)
				}
			}
		})
	}
}

// TestCLIGuide_CaseKindRenderingParity proves the whole pipe (store -> harvest
// -> guidebook) renders a case with case labels in every format.
func TestCLIGuide_CaseKindRenderingParity(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "guide_case_test.db")
	// Keep the harvest deterministic: scan only this database.
	t.Setenv("SILA_DB", dbPath)

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	proj, err := st.EnsureProject(tempDir)
	if err != nil {
		st.Close()
		t.Fatal(err)
	}
	if _, err := st.SaveCase(proj.ID, "Dependency injection", "constructor injection", "explicit dependencies", "architecture", "agent", tempDir, nil); err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()

	ui := render.New(nil, false)

	termOut := captureStdout(t, func() {
		runGuide(ui, dbPath, proj.ID, false, false, []string{"--domain=general"})
	})
	for _, want := range []string{"Intent:", "Approach:", "Invariant:", "constructor injection", "explicit dependencies"} {
		if !strings.Contains(termOut, want) {
			t.Errorf("case terminal missing %q:\n%s", want, termOut)
		}
	}
	for _, forbidden := range []string{"Symptom:", "Cause:", "Fix:", "Architectural procedural pattern"} {
		if strings.Contains(termOut, forbidden) {
			t.Errorf("case terminal leaked %q:\n%s", forbidden, termOut)
		}
	}

	mdOut := captureStdout(t, func() {
		runGuide(ui, dbPath, proj.ID, false, false, []string{"--format=markdown", "--domain=general"})
	})
	for _, want := range []string{"**Intent**", "**Approach**", "**Invariant**"} {
		if !strings.Contains(mdOut, want) {
			t.Errorf("case markdown missing %q:\n%s", want, mdOut)
		}
	}
	for _, forbidden := range []string{"Symptom", "Root Cause", "Pivotal Fix", "Architectural procedural pattern"} {
		if strings.Contains(mdOut, forbidden) {
			t.Errorf("case markdown leaked %q:\n%s", forbidden, mdOut)
		}
	}

	htmlOut := captureStdout(t, func() {
		runGuide(ui, dbPath, proj.ID, false, false, []string{"--format=html", "--domain=general"})
	})
	for _, want := range []string{">Intent<", ">Approach<", ">Invariant<"} {
		if !strings.Contains(htmlOut, want) {
			t.Errorf("case HTML missing %q", want)
		}
	}
	for _, forbidden := range []string{"Symptom", "Root Cause", "Pivotal Fix", "Architectural procedural pattern"} {
		if strings.Contains(htmlOut, forbidden) {
			t.Errorf("case HTML leaked %q", forbidden)
		}
	}

	jsonOut := captureStdout(t, func() {
		runGuide(ui, dbPath, proj.ID, true, false, []string{"--domain=general"})
	})
	var gb learn.Guidebook
	if err := json.Unmarshal([]byte(jsonOut), &gb); err != nil {
		t.Fatalf("expected valid guidebook JSON, got %q: %v", jsonOut, err)
	}
	if len(gb.Sections) != 1 || len(gb.Sections[0].Learnings) != 1 {
		t.Fatalf("unexpected guidebook shape: %+v", gb.Sections)
	}
	rendered := gb.Sections[0].Learnings[0]
	if rendered.Kind != learn.KindCase || rendered.Intent != "Dependency injection" || rendered.Approach != "constructor injection" || rendered.Invariant != "explicit dependencies" {
		t.Fatalf("case JSON fields = %+v", rendered)
	}
	if strings.Contains(jsonOut, `"symptom"`) || strings.Contains(jsonOut, `"pivotal_fix"`) {
		t.Errorf("case JSON leaked lesson fields:\n%s", jsonOut)
	}

	toonOut := captureStdout(t, func() {
		runGuide(ui, dbPath, proj.ID, false, true, []string{"--domain=general"})
	})
	if !strings.Contains(toonOut, "cases[1]{kind,domain,title,stack,intent,approach,invariant}:") {
		t.Errorf("case TOON missing case table:\n%s", toonOut)
	}
	for _, forbidden := range []string{"symptom", "root_cause", "pivotal_fix"} {
		if strings.Contains(toonOut, forbidden) {
			t.Errorf("case TOON leaked %q:\n%s", forbidden, toonOut)
		}
	}
}

func TestCLIGuide_TerminalAndFormats(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "guide_cli_test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	proj, _ := st.EnsureProject("/w/guideproj")
	st.Close()

	ui := render.New(nil, false)

	// 1. Terminal mode
	termOut := captureStdout(t, func() {
		runGuide(ui, dbPath, proj.ID, false, false, []string{})
	})
	if !strings.Contains(termOut, "sila · guide") {
		t.Errorf("expected terminal header, got: %s", termOut)
	}
	if !strings.Contains(termOut, "Synthetic Secret Sanitization in Test Fixtures") {
		t.Errorf("expected learning in terminal output, got: %s", termOut)
	}

	// 2. Markdown format
	mdOut := captureStdout(t, func() {
		runGuide(ui, dbPath, proj.ID, false, false, []string{"--format=markdown"})
	})
	if !strings.Contains(mdOut, "# Sila Developer Operational Guidebook") {
		t.Errorf("expected markdown header, got: %s", mdOut)
	}

	// 3. HTML format
	htmlOut := captureStdout(t, func() {
		runGuide(ui, dbPath, proj.ID, false, false, []string{"--format=html"})
	})
	if !strings.Contains(htmlOut, "<!DOCTYPE html>") {
		t.Errorf("expected html doctype, got: %s", htmlOut)
	}

	// 4. JSON format
	jsonOut := captureStdout(t, func() {
		runGuide(ui, dbPath, proj.ID, true, false, []string{})
	})
	if !strings.Contains(jsonOut, `"total_learnings":`) {
		t.Errorf("expected json output, got: %s", jsonOut)
	}

	// 5. TOON format
	toonOut := captureStdout(t, func() {
		runGuide(ui, dbPath, proj.ID, false, true, []string{})
	})
	if !strings.Contains(toonOut, "guidebook_summary:") {
		t.Errorf("expected toon output, got: %s", toonOut)
	}

	// 6. Export to file
	outFile := filepath.Join(tempDir, "exported_guide.html")
	runGuide(ui, dbPath, proj.ID, false, false, []string{"--format=html", "--out=" + outFile})
	if _, err := os.Stat(outFile); err != nil {
		t.Errorf("expected exported file to exist: %v", err)
	}
}

func TestCLILearn_ListAddExport(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "learn_cli_test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	proj, _ := st.EnsureProject("/w/learnproj")
	st.Close()

	ui := render.New(nil, false)

	// 1. Learn list
	listOut := captureStdout(t, func() {
		runLearn(ui, dbPath, proj.ID, "", "", "", "", "", "", "", "", false, false, []string{"list"})
	})
	if !strings.Contains(listOut, "sila · learn list") {
		t.Errorf("expected learn list header, got: %s", listOut)
	}

	// 2. Learn add via subArgs flags (backward compatibility)
	addOut := captureStdout(t, func() {
		runLearn(ui, dbPath, proj.ID, "", "", "", "", "", "", "", "", false, false, []string{
			"add",
			"--domain=testing",
			"--title=Isolated Storage Fixture Discipline",
			"--symptom=Order dependent test failure",
			"--cause=Shared DB mutation across it blocks",
			"--fix=Add beforeEach cleanup",
			"--rule=Always reset database in beforeEach",
		})
	})
	if !strings.Contains(addOut, "Operational learning saved to knowledge store") {
		t.Errorf("expected learning saved confirmation, got: %s", addOut)
	}

	// 3. Learn add via direct flags (CLI top-level dispatch from main.go)
	addDirectOut := captureStdout(t, func() {
		runLearn(ui, dbPath, proj.ID, "golang", "Direct Flag Invariant", "Parse failure", "Flags missing from main FlagSet", "Define flags in main.go", "Always register subcommand flags in root FlagSet", "go", "cli,flags", false, false, []string{"add"})
	})
	if !strings.Contains(addDirectOut, "Operational learning saved to knowledge store") {
		t.Errorf("expected direct learning saved confirmation, got: %s", addDirectOut)
	}

	// 4. Learn add JSON output
	addJSONOut := captureStdout(t, func() {
		runLearn(ui, dbPath, proj.ID, "golang", "JSON Learning Title", "Symptom JSON", "Cause JSON", "Fix JSON", "Rule JSON", "go", "", true, false, []string{"add"})
	})
	if !strings.Contains(addJSONOut, `"status": "saved"`) {
		t.Errorf("expected json saved output, got: %s", addJSONOut)
	}

	// 5. Learn export
	exportOut := captureStdout(t, func() {
		runLearn(ui, dbPath, proj.ID, "", "", "", "", "", "", "", "", false, false, []string{"export", "--format=markdown", "testing"})
	})
	if !strings.Contains(exportOut, "Isolated Storage Fixture Discipline") {
		t.Errorf("expected added learning in export output, got: %s", exportOut)
	}

	// 6. Learn distil preview
	distilOut := captureStdout(t, func() {
		runLearn(ui, dbPath, proj.ID, "", "", "", "", "", "", "", "", false, false, []string{"distil"})
	})
	if !strings.Contains(distilOut, "operational learning") {
		t.Errorf("expected distil response, got: %s", distilOut)
	}
}

func TestCLIGuide_ExportRules(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "export_rules_test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	proj, _ := st.EnsureProject("/w/exportrulesproj")
	st.Close()

	ui := render.New(nil, false)
	targetFile := filepath.Join(tempDir, "AGENTS.md")

	// Pre-populate AGENTS.md with user content
	initialContent := "# Project Guidelines\n\nCustom user instructions.\n"
	if err := os.WriteFile(targetFile, []byte(initialContent), 0644); err != nil {
		t.Fatal(err)
	}

	// 1. Export rules for golang and security
	exportOut := captureStdout(t, func() {
		runGuide(ui, dbPath, proj.ID, false, false, []string{
			"export-rules",
			"--target=" + targetFile,
			"--domain=golang,security",
		})
	})
	if !strings.Contains(exportOut, "Exported operational guardrail rules to") {
		t.Errorf("expected export confirmation, got: %s", exportOut)
	}

	data, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatalf("reading exported target file: %v", err)
	}
	contentStr := string(data)

	// Invariants:
	// - Contains initial user content preserved
	if !strings.Contains(contentStr, "Custom user instructions.") {
		t.Errorf("user instructions were overwritten: %s", contentStr)
	}
	// - Contains managed block delimiters
	if !strings.Contains(contentStr, "<!-- sila:begin (operational-rules) -->") ||
		!strings.Contains(contentStr, "<!-- sila:end -->") {
		t.Errorf("missing managed delimiters in export: %s", contentStr)
	}
	// - Contains Golang and Security sections
	if !strings.Contains(contentStr, "### Golang Engineering Disciplines") ||
		!strings.Contains(contentStr, "### Security, Redaction & Data Sovereignty") {
		t.Errorf("missing expected domain sections in export: %s", contentStr)
	}

	// 2. Re-export with --all to test idempotency and non-destructive replacement
	runGuide(ui, dbPath, proj.ID, false, false, []string{
		"export-rules",
		"--target=" + targetFile,
		"--all",
	})

	reData, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatal(err)
	}
	reContentStr := string(reData)
	if !strings.Contains(reContentStr, "Custom user instructions.") {
		t.Errorf("user instructions lost after re-export: %s", reContentStr)
	}
	if strings.Count(reContentStr, "<!-- sila:begin (operational-rules) -->") != 1 {
		t.Errorf("expected exactly 1 begin delimiter, found %d", strings.Count(reContentStr, "<!-- sila:begin (operational-rules) -->"))
	}
}

func TestCLILearn_CompileAndSync(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "compile_sync_test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	proj, _ := st.EnsureProject("/w/compilesyncproj")

	// Save test lesson
	_, err = st.SaveLesson(proj.ID, "Goroutine leak in watcher", "missing cancel", "defer cancel()", "golang,concurrency", "antigravity", tempDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	ui := render.New(nil, false)

	// 1. `sila learn compile`
	compileOut := captureStdout(t, func() {
		runLearn(ui, dbPath, proj.ID, "", "", "", "", "", "", "", "", false, false, []string{"compile", "--format=markdown"})
	})
	if !strings.Contains(compileOut, "# Sila Developer Operational Guidebook") {
		t.Errorf("expected markdown header in compile output, got: %s", compileOut)
	}

	// 2. `sila learn compile --format=json`
	compileJSON := captureStdout(t, func() {
		runLearn(ui, dbPath, proj.ID, "", "", "", "", "", "", "", "", true, false, []string{"compile"})
	})
	if !strings.Contains(compileJSON, `"total_learnings":`) {
		t.Errorf("expected json output in compile, got: %s", compileJSON)
	}

	// 3. `sila guide sync`
	targetFile := filepath.Join(tempDir, "AGENTS.md")
	guideSyncOut := captureStdout(t, func() {
		runGuide(ui, dbPath, proj.ID, false, false, []string{"sync", "--target=" + targetFile})
	})
	if !strings.Contains(guideSyncOut, "Synchronized") && !strings.Contains(guideSyncOut, "Auto-harvested") {
		t.Errorf("expected sync output in guide sync, got: %s", guideSyncOut)
	}
	if _, err := os.Stat(targetFile); err != nil {
		t.Errorf("expected synchronized rule file to exist: %v", err)
	}

	// 4. `sila learn sync --out=...`
	outFile := filepath.Join(tempDir, "GUIDEBOOK.md")
	learnSyncOut := captureStdout(t, func() {
		runLearn(ui, dbPath, proj.ID, "", "", "", "", "", "", "", "", false, false, []string{"sync", "--out=" + outFile})
	})
	if !strings.Contains(learnSyncOut, "Operational guidebook written to") {
		t.Errorf("expected written confirmation, got: %s", learnSyncOut)
	}
	if _, err := os.Stat(outFile); err != nil {
		t.Errorf("expected exported guidebook file to exist: %v", err)
	}
}
