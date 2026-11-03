package goals

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupTestGoalWithWorkPackage(t *testing.T, tmpDir, slug string) *WorkPackage {
	t.Helper()
	goalDir := filepath.Join(tmpDir, "goals", slug)
	_ = os.MkdirAll(goalDir, 0755)

	pkg := validWorkPackageFixture()
	pkg.GoalSlug = slug
	pkg.Tasks = []TaskContract{
		{
			ID:            "T-001",
			Title:         "Task Alpha",
			Context:       "Context A",
			Why:           "Why A",
			DependsOn:     []string{},
			Files:         TaskFiles{Edit: []string{"a.go"}},
			Steps:         []Step{{ID: "S-001", Action: "Act", Expected: "Exp", OnFailure: "Fail"}},
			Checks:        []Check{{ID: "C-001", Executable: "go", TimeoutSeconds: 10, Scope: ScopeUnit, Capability: CapabilityLocal, ExpectedExitCode: 0}},
			AcceptanceIDs: []string{"AC-001"},
			ReturnFormat:  TaskResultReturnFormat,
		},
		{
			ID:            "T-002",
			Title:         "Task Beta",
			Context:       "Context B",
			Why:           "Why B",
			DependsOn:     []string{"T-001"},
			Files:         TaskFiles{Edit: []string{"b.go"}},
			Steps:         []Step{{ID: "S-001", Action: "Act", Expected: "Exp", OnFailure: "Fail"}},
			Checks:        []Check{{ID: "C-002", Executable: "go", TimeoutSeconds: 10, Scope: ScopeUnit, Capability: CapabilityLocal, ExpectedExitCode: 0}},
			AcceptanceIDs: []string{"AC-002"},
			ReturnFormat:  TaskResultReturnFormat,
		},
	}
	pkg.Acceptance = []AcceptanceCase{
		{
			ID:            "AC-001",
			Requirement:   "Req A",
			Given:         "Given A",
			Action:        "Act A",
			Expected:      "Exp A",
			RequiredScope: ScopeUnit,
			Mandatory:     true,
			CheckIDs:      []string{"C-001"},
		},
		{
			ID:            "AC-002",
			Requirement:   "Req B",
			Given:         "Given B",
			Action:        "Act B",
			Expected:      "Exp B",
			RequiredScope: ScopeUnit,
			Mandatory:     true,
			CheckIDs:      []string{"C-002"},
		},
	}

	data, err := json.MarshalIndent(pkg, "", "  ")
	if err != nil {
		t.Fatalf("marshal package: %v", err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "work-package.json"), data, 0644); err != nil {
		t.Fatalf("write work-package.json: %v", err)
	}
	_ = os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte("# Goal: "+slug+"\n"), 0644)
	return pkg
}

func TestContractTaskView(t *testing.T) {
	tmpDir := t.TempDir()
	slug := "contract-view-goal"
	_ = os.MkdirAll(filepath.Join(tmpDir, "goals"), 0755)

	pkg := setupTestGoalWithWorkPackage(t, tmpDir, slug)

	// AC-007: Expose structured task IDs and titles through GetGoalTasks
	tasks, err := GetGoalTasks(tmpDir, slug)
	if err != nil {
		t.Fatalf("GetGoalTasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(tasks))
	}
	if tasks[0].ID != "T-001" || tasks[0].Text != "Task Alpha" {
		t.Errorf("task[0] mismatch: %+v", tasks[0])
	}
	if tasks[1].ID != "T-002" || tasks[1].Text != "Task Beta" {
		t.Errorf("task[1] mismatch: %+v", tasks[1])
	}
	if tasks[0].Completed || tasks[1].Completed {
		t.Errorf("structured tasks must not be marked completed without verified evidence")
	}

	// AC-007: Reordering plan.md checkboxes does NOT alter structured task IDs or totals
	planPath := filepath.Join(tmpDir, "goals", slug, "plan.md")
	reorderedPlan := `# Plan
- [x] Unrelated step C
- [ ] Task Beta
- [x] Task Alpha
`
	if err := os.WriteFile(planPath, []byte(reorderedPlan), 0644); err != nil {
		t.Fatalf("write reordered plan: %v", err)
	}

	tasksAfterPlan, err := GetGoalTasks(tmpDir, slug)
	if err != nil {
		t.Fatalf("GetGoalTasks after plan edit: %v", err)
	}
	if len(tasksAfterPlan) != 2 {
		t.Fatalf("expected structured total to remain 2, got %d", len(tasksAfterPlan))
	}
	if tasksAfterPlan[0].ID != pkg.Tasks[0].ID || tasksAfterPlan[1].ID != pkg.Tasks[1].ID {
		t.Errorf("reordered plan checkboxes altered structured IDs! got: %+v", tasksAfterPlan)
	}
}

func TestContractTasksWithDirectoryUsesEvidenceAcceptancePath(t *testing.T) {
	tmpDir := t.TempDir()
	slug := "evidence-acceptance-goal"
	pkg := setupTestGoalWithWorkPackage(t, tmpDir, slug)
	goalDir := filepath.Join(tmpDir, "goals", slug)
	_ = os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte("# Plan\n- [x] Done\n"), 0644)
	_ = os.WriteFile(filepath.Join(goalDir, "facts.md"), []byte("# Facts\n"), 0644)

	digest, err := WorkPackageDigest(pkg)
	if err != nil {
		t.Fatal(err)
	}
	res := &AcceptanceResult{
		SchemaVersion:  AcceptanceResultSchemaVersion,
		GoalSlug:       slug,
		TaskID:         "T-001",
		Accepted:       true,
		EvidenceStatus: EvidenceStatusAccepted,
		SpecDigest:     digest,
		ReviewedAt:     "2026-10-20T00:00:00Z",
	}
	if err := SaveTaskAcceptance(tmpDir, slug, "T-001", res, nil, nil); err != nil {
		t.Fatalf("SaveTaskAcceptance: %v", err)
	}
	if !fileExists(filepath.Join(goalDir, "evidence", "T-001", "acceptance.json")) {
		t.Fatal("expected acceptance record at the canonical evidence path")
	}

	tasks, err := LoadWorkPackageTasksIfPresent(goalDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 || !tasks[0].Completed {
		t.Fatalf("expected T-001 completed via evidence acceptance, got %+v", tasks)
	}
	if tasks[1].Completed {
		t.Errorf("T-002 must remain incomplete, got %+v", tasks[1])
	}

	audit, err := ScanGoals(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	var goal *Goal
	for i := range audit.Goals {
		if audit.Goals[i].Slug == slug {
			goal = &audit.Goals[i]
			break
		}
	}
	if goal == nil {
		t.Fatalf("goal %q not found in audit", slug)
	}
	if goal.TasksMet != 1 || goal.TasksTotal != 2 {
		t.Errorf("expected ScanGoals tasks 1/2, got %d/%d", goal.TasksMet, goal.TasksTotal)
	}
}

func TestContractTaskMutation(t *testing.T) {
	tmpDir := t.TempDir()
	slug := "contract-mutation-goal"
	_ = os.MkdirAll(filepath.Join(tmpDir, "goals"), 0755)

	setupTestGoalWithWorkPackage(t, tmpDir, slug)

	// AC-008: Attempting to toggle a structured task via ToggleGoalTasks must fail with TASK_EVIDENCE_REQUIRED
	_, err := ToggleGoalTask(tmpDir, slug, 1, true)
	if err == nil {
		t.Fatalf("expected ToggleGoalTask to fail on structured goal, got nil")
	}
	if !strings.Contains(err.Error(), "TASK_EVIDENCE_REQUIRED") {
		t.Errorf("expected 'TASK_EVIDENCE_REQUIRED' in error, got: %v", err)
	}

	// AC-008: Editing [x] in plan.md does not mark the structured task complete
	planPath := filepath.Join(tmpDir, "goals", slug, "plan.md")
	_ = os.WriteFile(planPath, []byte("- [x] Task Alpha\n- [x] Task Beta\n"), 0644)

	tasks, err := GetGoalTasks(tmpDir, slug)
	if err != nil {
		t.Fatalf("GetGoalTasks: %v", err)
	}
	for _, tk := range tasks {
		if tk.Completed {
			t.Errorf("task %s was marked completed by plan.md checkbox edit!", tk.ID)
		}
	}
}

func TestLegacyTaskCompatibility(t *testing.T) {
	tmpDir := t.TempDir()
	slug := "legacy-goal"
	_ = os.MkdirAll(filepath.Join(tmpDir, "goals", slug), 0755)

	// Legacy goal without work-package.json
	planContent := `# Plan
- [x] Step 1: Initial setup
- [ ] Step 2: Implementation
- [ ] Step 3: Verification
`
	_ = os.WriteFile(filepath.Join(tmpDir, "goals", slug, "goal.md"), []byte("# Goal\n"), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "goals", slug, "plan.md"), []byte(planContent), 0644)

	// AC-009: Legacy goal produces identical task list, counts, and empty ID
	tasks, err := GetGoalTasks(tmpDir, slug)
	if err != nil {
		t.Fatalf("GetGoalTasks legacy: %v", err)
	}
	if len(tasks) != 3 {
		t.Fatalf("expected 3 legacy tasks, got %d", len(tasks))
	}
	if tasks[0].ID != "" {
		t.Errorf("legacy task should have empty ID, got %q", tasks[0].ID)
	}
	if !tasks[0].Completed || tasks[1].Completed || tasks[2].Completed {
		t.Errorf("legacy completion state mismatch: %+v", tasks)
	}

	// AC-009: Malformed structured JSON returns an error rather than falling back to plan.md
	corruptSlug := "corrupt-goal"
	corruptDir := filepath.Join(tmpDir, "goals", corruptSlug)
	_ = os.MkdirAll(corruptDir, 0755)
	_ = os.WriteFile(filepath.Join(corruptDir, "goal.md"), []byte("# Goal\n"), 0644)
	_ = os.WriteFile(filepath.Join(corruptDir, "plan.md"), []byte(planContent), 0644)
	_ = os.WriteFile(filepath.Join(corruptDir, "work-package.json"), []byte(`{"schema_version":`), 0644)

	_, err = GetGoalTasks(tmpDir, corruptSlug)
	if err == nil {
		t.Fatalf("expected error loading corrupt work-package.json, got nil")
	}
	if !strings.Contains(err.Error(), "corrupt work-package.json") {
		t.Errorf("expected 'corrupt work-package.json' in error, got: %v", err)
	}

	// BuildGoalView must also return an error when work-package.json is corrupt
	g := &Goal{Slug: corruptSlug, Title: "Corrupt Goal"}
	_, err = BuildGoalView(tmpDir, g)
	if err == nil {
		t.Fatalf("expected BuildGoalView to fail on corrupt work-package.json, got nil")
	}
	if !strings.Contains(err.Error(), "read goal tasks") {
		t.Errorf("expected 'read goal tasks' in BuildGoalView error, got: %v", err)
	}
}
