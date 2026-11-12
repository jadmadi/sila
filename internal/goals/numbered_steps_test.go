package goals

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCountNumberedSteps(t *testing.T) {
	content := `# Plan: Numbered Steps

## Execution Steps
1. Parse numbered steps
2. [x] Toggle them cleanly
3. [X] Verify parity
10. Handle multi-digit numbers
`
	met, total := countCheckboxes(content)
	if total != 4 {
		t.Errorf("countCheckboxes total = %d, expected 4", total)
	}
	if met != 2 {
		t.Errorf("countCheckboxes met = %d, expected 2", met)
	}
}

func TestCountNumberedIgnoresNonSteps(t *testing.T) {
	content := `Requires Go 1.26 or later.
1) Paren style is not a step.
1. 
2.   Real step one
   3. Indented step two
`
	met, total := countCheckboxes(content)
	if total != 2 {
		t.Errorf("countCheckboxes total = %d, expected 2", total)
	}
	if met != 0 {
		t.Errorf("countCheckboxes met = %d, expected 0", met)
	}
}

func TestStepHeadingsWinOverNumberedDetail(t *testing.T) {
	content := `# Plan

## Step 1 — Setup
1. Detail line one
2. Detail line two

## Step 2 — Build

1. More detail

## Risks and Open Questions
1. Risk one
2. Risk two
`
	met, total := countCheckboxes(content)
	if total != 2 {
		t.Fatalf("expected 2 step tasks, got %d", total)
	}
	if met != 0 {
		t.Fatalf("expected 0 met, got %d", met)
	}
}

func TestGetGoalTasksPrefersStepHeadings(t *testing.T) {
	tmp := t.TempDir()
	writeNumberedGoal(t, tmp, "step-heading-goal", `# Plan

## Step 1 — Setup
1. Detail

## Step 2 — Build
1. More detail
`)
	tasks, err := GetGoalTasks(tmp, "step-heading-goal")
	if err != nil {
		t.Fatalf("GetGoalTasks failed: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 step tasks, got %d: %+v", len(tasks), tasks)
	}
	if tasks[0].Text != "Setup" || tasks[1].Text != "Build" {
		t.Fatalf("unexpected step task texts: %+v", tasks)
	}
}

func TestToggleStepHeadingTask(t *testing.T) {
	tmp := t.TempDir()
	writeNumberedGoal(t, tmp, "step-toggle-goal", "# Plan\n\n## Step 1 — Setup\n\n## Step 2 — Build\n")
	got, err := ToggleGoalTask(tmp, "step-toggle-goal", 2, true)
	if err != nil {
		t.Fatalf("toggle check failed: %v", err)
	}
	if !got.Completed || got.Text != "Build" {
		t.Fatalf("unexpected toggled task: %+v", got)
	}
	raw, _ := os.ReadFile(filepath.Join(tmp, "goals", "step-toggle-goal", "plan.md"))
	if !strings.Contains(string(raw), "## Step 2 — Build [x]") {
		t.Fatalf("expected heading marker rewrite, got:\n%s", raw)
	}
	if strings.Contains(string(raw), "Step 1 — Setup [") {
		t.Fatalf("untargeted step must not gain a marker:\n%s", raw)
	}
	audit, err := ScanGoals(tmp)
	if err != nil {
		t.Fatalf("scan goals: %v", err)
	}
	if audit.TotalTasks != 2 || audit.CompletedTasks != 1 {
		t.Fatalf("expected total=2 completed=1, got total=%d completed=%d", audit.TotalTasks, audit.CompletedTasks)
	}
	if _, err := ToggleGoalTask(tmp, "step-toggle-goal", 2, false); err != nil {
		t.Fatalf("toggle uncheck failed: %v", err)
	}
	raw, _ = os.ReadFile(filepath.Join(tmp, "goals", "step-toggle-goal", "plan.md"))
	if !strings.Contains(string(raw), "## Step 2 — Build [ ]") {
		t.Fatalf("expected unchecked heading marker, got:\n%s", raw)
	}
}

func TestCountMixedFileStaysCheckboxOnly(t *testing.T) {
	content := `- [x] Checkbox done
- [ ] Checkbox open
1. Numbered must not double-count
2. [x] Numbered checked must not count either
`
	met, total := countCheckboxes(content)
	if total != 2 {
		t.Errorf("mixed file total = %d, expected checkbox-only 2", total)
	}
	if met != 1 {
		t.Errorf("mixed file met = %d, expected checkbox-only 1", met)
	}
}

func writeNumberedGoal(t *testing.T, root, slug, plan string) {
	t.Helper()
	dir := filepath.Join(root, "goals", slug)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "goal.md"), []byte("# Goal: "+slug+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "facts.md"), []byte("# Facts\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte(plan), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestGetGoalTasksNumbered(t *testing.T) {
	tmp := t.TempDir()
	writeNumberedGoal(t, tmp, "numbered-goal", `# Plan

## Steps
1. First step: parse me
2. [x] Second step done
   3. Indented third step
`)
	tasks, err := GetGoalTasks(tmp, "numbered-goal")
	if err != nil {
		t.Fatalf("GetGoalTasks failed: %v", err)
	}
	if len(tasks) != 3 {
		t.Fatalf("expected 3 tasks, got %d: %+v", len(tasks), tasks)
	}
	if tasks[0].Completed || !tasks[1].Completed || tasks[2].Completed {
		t.Errorf("unexpected completion states: %+v", tasks)
	}
	if !strings.Contains(tasks[0].Text, "First step") {
		t.Errorf("unexpected task 1 text: %q", tasks[0].Text)
	}
	if tasks[0].Index != 1 || tasks[1].Index != 2 || tasks[2].Index != 3 {
		t.Errorf("unexpected task indices: %+v", tasks)
	}
}

func TestScanGoalsNumberedProgress(t *testing.T) {
	tmp := t.TempDir()
	writeNumberedGoal(t, tmp, "numbered-scan", `# Plan

## Steps
1. [x] Done step
2. Open step
3. Open step
`)
	audit, err := ScanGoals(tmp)
	if err != nil {
		t.Fatalf("scan goals: %v", err)
	}
	if audit.TotalTasks != 3 || audit.CompletedTasks != 1 {
		t.Errorf("expected total=3 completed=1, got total=%d completed=%d", audit.TotalTasks, audit.CompletedTasks)
	}
}

func TestToggleNumberedCheckUncheck(t *testing.T) {
	tmp := t.TempDir()
	writeNumberedGoal(t, tmp, "numbered-toggle", `# Plan

## Steps
1. First step
2. Second step
`)
	got, err := ToggleGoalTask(tmp, "numbered-toggle", 2, true)
	if err != nil {
		t.Fatalf("toggle check failed: %v", err)
	}
	if !got.Completed || !strings.Contains(got.Text, "Second step") {
		t.Errorf("unexpected toggled task: %+v", got)
	}
	raw, _ := os.ReadFile(filepath.Join(tmp, "goals", "numbered-toggle", "plan.md"))
	if !strings.Contains(string(raw), "2. [x] Second step") {
		t.Errorf("expected numbered marker rewrite, got:\n%s", raw)
	}
	if strings.Contains(string(raw), "1. [") {
		t.Errorf("untargeted step must not gain a marker:\n%s", raw)
	}

	audit, err := ScanGoals(tmp)
	if err != nil {
		t.Fatalf("scan goals: %v", err)
	}
	if audit.CompletedTasks != 1 {
		t.Errorf("expected 1 completed after toggle, got %d", audit.CompletedTasks)
	}

	if _, err := ToggleGoalTask(tmp, "numbered-toggle", 2, false); err != nil {
		t.Fatalf("toggle uncheck failed: %v", err)
	}
	raw, _ = os.ReadFile(filepath.Join(tmp, "goals", "numbered-toggle", "plan.md"))
	if !strings.Contains(string(raw), "2. [ ] Second step") {
		t.Errorf("expected unchecked marker rewrite, got:\n%s", raw)
	}
}

func TestToggleNumberedPreservesNumbering(t *testing.T) {
	tmp := t.TempDir()
	writeNumberedGoal(t, tmp, "numbered-multi", "# Plan\n\n10. Tenth step\n007. Padded step\n")
	if _, err := ToggleGoalTasks(tmp, "numbered-multi", []int{1, 2}, true); err != nil {
		t.Fatalf("batch toggle failed: %v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(tmp, "goals", "numbered-multi", "plan.md"))
	if !strings.Contains(string(raw), "10. [x] Tenth step") || !strings.Contains(string(raw), "007. [x] Padded step") {
		t.Errorf("expected original numbers preserved, got:\n%s", raw)
	}
}

func TestToggleMixedFileCheckboxWins(t *testing.T) {
	tmp := t.TempDir()
	writeNumberedGoal(t, tmp, "mixed-goal", "# Plan\n\n- [ ] Checkbox task\n1. Numbered line stays text\n")
	tasks, err := GetGoalTasks(tmp, "mixed-goal")
	if err != nil {
		t.Fatalf("GetGoalTasks failed: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Text != "Checkbox task" {
		t.Fatalf("expected only the checkbox task, got %+v", tasks)
	}
	if _, err := ToggleGoalTask(tmp, "mixed-goal", 1, true); err != nil {
		t.Fatalf("toggle failed: %v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(tmp, "goals", "mixed-goal", "plan.md"))
	if !strings.Contains(string(raw), "- [x] Checkbox task") {
		t.Errorf("expected checkbox toggled, got:\n%s", raw)
	}
	if strings.Contains(string(raw), "1. [x]") {
		t.Errorf("numbered line must stay untouched in checkbox mode:\n%s", raw)
	}
}

func TestFenceAwareCheckboxesAndSteps(t *testing.T) {
	plan := "# Plan\n\n" +
		"````markdown\n" +
		"```\n" +
		"- [ ] inner fenced task\n" +
		"## Step 1: Inner heading\n" +
		"1. Inner numbered\n" +
		"```\n" +
		"- [ ] still fenced outer task\n" +
		"````\n\n" +
		"- [ ] real one\n" +
		"- [x] real two\n"

	met, total := countCheckboxes(plan)
	if met != 1 || total != 2 {
		t.Fatalf("countCheckboxes = %d/%d, want 1/2", met, total)
	}
	if usesStepHeadings(plan) {
		t.Fatal("fenced step heading was treated as a live heading")
	}
	tasks := parseTasksFromContent(plan, "plan.md", 1)
	if len(tasks) != 2 {
		t.Fatalf("parseTasksFromContent returned %d tasks, want 2: %+v", len(tasks), tasks)
	}
	if tasks[0].Text != "real one" || !tasks[1].Completed {
		t.Fatalf("unexpected tasks: %+v", tasks)
	}

	tmp := t.TempDir()
	writeNumberedGoal(t, tmp, "fence-goal", plan)
	toggled, err := ToggleGoalTask(tmp, "fence-goal", 1, true)
	if err != nil {
		t.Fatal(err)
	}
	if toggled.Text != "real one" {
		t.Fatalf("toggled %q, want the first live task", toggled.Text)
	}
	data, err := os.ReadFile(filepath.Join(tmp, "goals", "fence-goal", "plan.md"))
	if err != nil {
		t.Fatal(err)
	}
	updated := string(data)
	if !strings.Contains(updated, "- [x] real one") {
		t.Fatalf("live task was not toggled: %s", updated)
	}
	if strings.Contains(updated, "- [x] inner fenced task") || strings.Contains(updated, "- [x] still fenced outer task") {
		t.Fatalf("fenced task was toggled: %s", updated)
	}

	tilde := "# Plan\n\n~~~\n- [ ] fenced tilde\n## Step 9: Fenced\n~~~\n- [ ] live tilde task\n"
	met, total = countCheckboxes(tilde)
	if met != 0 || total != 1 {
		t.Fatalf("tilde countCheckboxes = %d/%d, want 0/1", met, total)
	}
	if usesStepHeadings(tilde) {
		t.Fatal("fenced tilde step heading was treated as a live heading")
	}
}
