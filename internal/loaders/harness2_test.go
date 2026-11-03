package loaders

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHarness2Loader(t *testing.T) {
	tmp := t.TempDir()

	// 1. Valid task directory with summary.json and selection.json
	task1Dir := filepath.Join(tmp, "task_001")
	if err := os.MkdirAll(task1Dir, 0755); err != nil {
		t.Fatal(err)
	}
	summaryJSON := `{
  "task_id": "task_001",
  "mode": "parallel",
  "k": 2,
  "stages": {"inv1": {"accepted": true}, "dom1": {"accepted": true}, "agg1": {"accepted": true}},
  "selection": {"chosen": "agg1"}
}`
	if err := os.WriteFile(filepath.Join(task1Dir, "summary.json"), []byte(summaryJSON), 0644); err != nil {
		t.Fatal(err)
	}
	selectionJSON := `{
  "pool": ["base1", "inv1", "agg1"],
  "ids": {"base1": "B", "inv1": "C", "agg1": "A"},
  "chosen": "agg1",
  "rationale": "Candidate A delivered all requested worksheets with verified totals."
}`
	if err := os.WriteFile(filepath.Join(task1Dir, "selection.json"), []byte(selectionJSON), 0644); err != nil {
		t.Fatal(err)
	}

	// 2. Incomplete task directory missing summary.json (must be skipped cleanly)
	incompleteDir := filepath.Join(tmp, "task_incomplete")
	if err := os.MkdirAll(incompleteDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(incompleteDir, "notes.txt"), []byte("partial run"), 0644); err != nil {
		t.Fatal(err)
	}

	// 3. Corrupt summary.json (must be skipped cleanly)
	corruptDir := filepath.Join(tmp, "task_corrupt")
	if err := os.MkdirAll(corruptDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(corruptDir, "summary.json"), []byte("{not-json"), 0644); err != nil {
		t.Fatal(err)
	}

	// 4. Library EXPERIENCE.md file
	libDir := filepath.Join(tmp, "library", "financial_xlsx")
	if err := os.MkdirAll(libDir, 0755); err != nil {
		t.Fatal(err)
	}
	expMD := "# Experience — financial_xlsx\n\n## [task_001] (confidence: high)\n- **When:** building multi-sheet workbooks\n- **Lesson:** run scripts/check_formulas.py before finishing\n"
	if err := os.WriteFile(filepath.Join(libDir, "EXPERIENCE.md"), []byte(expMD), 0644); err != nil {
		t.Fatal(err)
	}

	ldr := NewHarness2Loader(tmp)
	if ldr.Name() != "harness2" {
		t.Fatalf("expected loader name 'harness2', got %q", ldr.Name())
	}

	cnt, err := ldr.Count(nil)
	if err != nil {
		t.Fatalf("Count error: %v", err)
	}
	if cnt != 3 {
		t.Fatalf("expected Count=3 (1 summary + 1 selection + 1 experience), got %d", cnt)
	}

	msgs, wm, err := ldr.Load("", nil)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d: %+v", len(msgs), msgs)
	}
	if wm == "" {
		t.Fatalf("expected non-empty watermark after Load")
	}
	if !strings.Contains(msgs[0].Content, "task_001") {
		t.Fatalf("unexpected msgs[0]: %+v", msgs[0])
	}
	if !strings.Contains(msgs[1].Content, "Selected candidate agg1") {
		t.Fatalf("unexpected msgs[1]: %+v", msgs[1])
	}
	if !strings.Contains(msgs[2].Content, "financial_xlsx") {
		t.Fatalf("unexpected msgs[2]: %+v", msgs[2])
	}

	// Incremental watermark load should return 0 new messages when unchanged
	msgs2, _, err := ldr.Load(wm, nil)
	if err != nil {
		t.Fatalf("incremental Load error: %v", err)
	}
	if len(msgs2) != 0 {
		t.Fatalf("expected 0 messages on unchanged watermark, got %d", len(msgs2))
	}
}

func TestHarness2LoaderCLIAndSuite(t *testing.T) {
	srcs := AllSources()
	src, ok := srcs["harness2"]
	if !ok || src == nil || src.New == nil {
		t.Fatalf("expected 'harness2' source to be registered in AllSources()")
	}
	if ResolveTool("h2") != "harness2" || ResolveTool("harness2") != "harness2" {
		t.Fatalf("expected 'h2' and 'harness2' aliases to resolve to 'harness2'")
	}
}
