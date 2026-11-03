package goals

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func setupGitRepoForGoal(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runCmd := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}
	runCmd("init")
	runCmd("config", "user.name", "tester")
	runCmd("config", "user.email", "tester@example.com")
	_ = os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test Repo\n"), 0644)
	runCmd("add", "README.md")
	runCmd("commit", "-m", "chore: initial commit")
	return dir
}

func TestGoalAcceptanceProjection(t *testing.T) {
	// AC-007: All Markdown boxes checked plus implementation commit but no receipts -> implemented attribution, acceptance incomplete, zero accepted tasks.
	dir := setupGitRepoForGoal(t)
	slug := "projection-goal"
	goalDir := filepath.Join(dir, "goals", slug)
	_ = os.MkdirAll(goalDir, 0755)

	// Create work-package with 3 tasks
	wp := &WorkPackage{
		SchemaVersion: WorkPackageSchemaVersion,
		GoalSlug:      slug,
		Revision:      1,
		Purpose:       "Projection test",
		Constraints:   []string{"pure-go"},
		NonGoals:      []string{"external"},
		Tasks: []TaskContract{
			{
				ID:            "T-001",
				Title:         "Task 1",
				Context:       "C1",
				Why:           "W1",
				Files:         TaskFiles{Read: []string{"read.go"}, Edit: []string{"README.md"}},
				AcceptanceIDs: []string{"AC-001"},
				Checks: []Check{
					{ID: "C-001", Executable: "go", Args: []string{"test"}, Cwd: "cli", TimeoutSeconds: 30, ExpectedExitCode: 0, Scope: ScopeUnit, Capability: CapabilityLocal},
				},
				StopConditions: []string{"failure"},
				ReturnFormat:   "sila/task-result/v1",
			},
			{
				ID:            "T-002",
				Title:         "Task 2",
				Context:       "C2",
				Why:           "W2",
				Files:         TaskFiles{Read: []string{"read.go"}, Edit: []string{"README.md"}},
				AcceptanceIDs: []string{"AC-002"},
				Checks: []Check{
					{ID: "C-002", Executable: "go", Args: []string{"test"}, Cwd: "cli", TimeoutSeconds: 30, ExpectedExitCode: 0, Scope: ScopeUnit, Capability: CapabilityLocal},
				},
				StopConditions: []string{"failure"},
				ReturnFormat:   "sila/task-result/v1",
			},
			{
				ID:            "T-003",
				Title:         "Task 3",
				Context:       "C3",
				Why:           "W3",
				Files:         TaskFiles{Read: []string{"read.go"}, Edit: []string{"README.md"}},
				AcceptanceIDs: []string{"AC-003"},
				Checks: []Check{
					{ID: "C-003", Executable: "go", Args: []string{"test"}, Cwd: "cli", TimeoutSeconds: 30, ExpectedExitCode: 0, Scope: ScopeUnit, Capability: CapabilityLocal},
				},
				StopConditions: []string{"failure"},
				ReturnFormat:   "sila/task-result/v1",
			},
		},
		Acceptance: []AcceptanceCase{
			{ID: "AC-001", Requirement: "Req 1", Given: "ready", Action: "test", Expected: "pass", RequiredScope: ScopeUnit, Mandatory: true, CheckIDs: []string{"C-001"}},
			{ID: "AC-002", Requirement: "Req 2", Given: "ready", Action: "test", Expected: "pass", RequiredScope: ScopeUnit, Mandatory: true, CheckIDs: []string{"C-002"}},
			{ID: "AC-003", Requirement: "Req 3", Given: "ready", Action: "test", Expected: "pass", RequiredScope: ScopeUnit, Mandatory: true, CheckIDs: []string{"C-003"}},
		},
		DependencyConditions: []DependencyCondition{},
		UnresolvedDecisions:  []string{},
	}
	if err := SaveWorkPackage(dir, slug, wp); err != nil {
		t.Fatalf("save work package: %v", err)
	}

	// Create goal.md and plan.md with ALL 3 boxes checked [x]
	_ = os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte("# Goal: projection-goal\n"), 0644)
	planContent := `# Plan: projection-goal
- [x] T-001: Task 1
- [x] T-002: Task 2
- [x] T-003: Task 3
`
	_ = os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte(planContent), 0644)
	_ = os.WriteFile(filepath.Join(goalDir, "facts.md"), []byte("# Facts\n"), 0644)

	// Commit with goal attribution
	cmdAdd := exec.Command("git", "add", ".")
	cmdAdd.Dir = dir
	_ = cmdAdd.Run()
	cmdCommit := exec.Command("git", "commit", "-m", fmt.Sprintf("feat(projection): implement feature [goal: %s]", slug))
	cmdCommit.Dir = dir
	if out, err := cmdCommit.CombinedOutput(); err != nil {
		t.Fatalf("commit failed: %v\n%s", err, out)
	}

	// Scan goals
	audit, err := ScanGoals(dir)
	if err != nil {
		t.Fatalf("ScanGoals failed: %v", err)
	}
	var target *Goal
	for i := range audit.Goals {
		if audit.Goals[i].Slug == slug {
			target = &audit.Goals[i]
			break
		}
	}
	if target == nil {
		t.Fatalf("goal %s not found in audit results", slug)
	}

	// Assertions for AC-007:
	// 1. Status is "implemented" via Git attribution
	if target.Status != "implemented" {
		t.Errorf("expected Status='implemented', got %q", target.Status)
	}
	// 2. AcceptanceStatus is "incomplete" (or not_run/incomplete)
	if target.AcceptanceStatus != string(EvidenceStatusIncomplete) {
		t.Errorf("expected AcceptanceStatus='incomplete', got %q", target.AcceptanceStatus)
	}
	// 3. AcceptedTasks is 0
	if target.AcceptedTasks != 0 {
		t.Errorf("expected AcceptedTasks=0, got %d", target.AcceptedTasks)
	}
	// 4. TasksMet is 0 (derived strictly from accepted tasks)
	if target.TasksMet != 0 {
		t.Errorf("expected TasksMet=0, got %d", target.TasksMet)
	}
	// 5. TasksTotal is 3
	if target.TasksTotal != 3 {
		t.Errorf("expected TasksTotal=3, got %d", target.TasksTotal)
	}
	// 6. RequiredCases is 3, PassedCases is 0
	if target.RequiredCases != 3 || target.PassedCases != 0 {
		t.Errorf("expected RequiredCases=3, PassedCases=0, got %d/%d", target.RequiredCases, target.PassedCases)
	}

	// Verify GoalView projection
	gv, err := BuildGoalView(dir, target)
	if err != nil {
		t.Fatalf("BuildGoalView failed: %v", err)
	}
	if gv.Status != "implemented" {
		t.Errorf("GoalView Status: expected 'implemented', got %q", gv.Status)
	}
	if gv.AcceptanceStatus != string(EvidenceStatusIncomplete) {
		t.Errorf("GoalView AcceptanceStatus: expected 'incomplete', got %q", gv.AcceptanceStatus)
	}
	if gv.AcceptedTasks != 0 || gv.TasksMet != 0 {
		t.Errorf("GoalView AcceptedTasks/TasksMet: expected 0, got %d/%d", gv.AcceptedTasks, gv.TasksMet)
	}
}

func TestGoalAcceptanceStaleness(t *testing.T) {
	// AC-008: Changing source after acceptance makes current evidence stale without erasing history.
	dir := setupGitRepoForGoal(t)
	slug := "staleness-goal"
	goalDir := filepath.Join(dir, "goals", slug)
	_ = os.MkdirAll(goalDir, 0755)

	wp := &WorkPackage{
		SchemaVersion: WorkPackageSchemaVersion,
		GoalSlug:      slug,
		Revision:      1,
		Purpose:       "Staleness test",
		Constraints:   []string{"pure-go"},
		NonGoals:      []string{"external"},
		Tasks: []TaskContract{
			{
				ID:            "T-001",
				Title:         "Task 1",
				Context:       "C1",
				Why:           "W1",
				Files:         TaskFiles{Read: []string{"read.go"}, Edit: []string{"README.md"}},
				AcceptanceIDs: []string{"AC-001"},
				Checks: []Check{
					{ID: "C-001", Executable: "go", Args: []string{"test"}, Cwd: "cli", TimeoutSeconds: 30, ExpectedExitCode: 0, Scope: ScopeUnit, Capability: CapabilityLocal},
				},
				StopConditions: []string{"failure"},
				ReturnFormat:   "sila/task-result/v1",
			},
		},
		Acceptance: []AcceptanceCase{
			{ID: "AC-001", Requirement: "Req 1", Given: "ready", Action: "test", Expected: "pass", RequiredScope: ScopeUnit, Mandatory: true, CheckIDs: []string{"C-001"}},
		},
		DependencyConditions: []DependencyCondition{},
		UnresolvedDecisions:  []string{},
	}
	if err := SaveWorkPackage(dir, slug, wp); err != nil {
		t.Fatalf("save work package: %v", err)
	}
	_ = os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte("# Goal: staleness-goal\n"), 0644)
	_ = os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte("# Plan\n- [ ] T-001\n"), 0644)
	_ = os.WriteFile(filepath.Join(goalDir, "facts.md"), []byte("# Facts\n"), 0644)

	// Commit initial state
	exec.Command("git", "-C", dir, "add", ".").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "chore: setup goal").Run()

	specDigest, err := WorkPackageDigest(wp)
	if err != nil {
		t.Fatalf("spec digest: %v", err)
	}

	cand, err := CaptureTaskCandidate(dir, slug)
	if err != nil {
		t.Fatalf("capture candidate: %v", err)
	}

	// Create passing receipt matching candidate
	now := time.Now().UTC()
	rPath := createReceipt(t, dir, slug, "T-001", "att-001", "AC-001", OutcomePass, now, *cand, nil, specDigest)

	// Evaluate task acceptance and save it
	sel := ReceiptSelection{
		SchemaVersion:   ReceiptSelectionSchemaVersion,
		GoalSlug:        slug,
		TaskID:          "T-001",
		SpecDigest:      specDigest,
		CandidateDigest: cand.ContentDigest,
		ReceiptPaths:    []string{rPath},
	}
	rev := AcceptanceReview{
		SchemaVersion:   AcceptanceReviewSchemaVersion,
		Reviewer:        "reviewer",
		Producer:        "producer",
		CandidateDigest: cand.ContentDigest,
		SpecDigest:      specDigest,
		Disposition:     "accept",
		ReviewedAt:      now.Add(time.Minute).Format(time.RFC3339),
	}
	taskRes, err := EvaluateTaskAcceptance(dir, slug, "T-001", sel, rev)
	if err != nil || !taskRes.Accepted {
		t.Fatalf("task acceptance failed: %v", err)
	}
	if err := SaveTaskAcceptance(dir, slug, "T-001", taskRes, &sel, &rev); err != nil {
		t.Fatalf("save task acceptance: %v", err)
	}

	// Goal acceptance is currently accepted!
	initialAcc, err := EvaluateGoalAcceptance(dir, slug)
	if err != nil {
		t.Fatalf("EvaluateGoalAcceptance failed: %v", err)
	}
	if !initialAcc.Accepted || initialAcc.EvidenceStatus != EvidenceStatusAccepted {
		t.Fatalf("expected initial goal acceptance accepted=true, got %+v", initialAcc)
	}

	// Record task acceptance file bytes
	accPath := TaskAcceptancePath(dir, slug, "T-001")
	initialAccData, err := os.ReadFile(accPath)
	if err != nil {
		t.Fatalf("read task acceptance: %v", err)
	}

	// Now modify source: touch README.md and commit to git
	_ = os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Modified source\n"), 0644)
	exec.Command("git", "-C", dir, "add", "README.md").Run()
	exec.Command("git", "-C", dir, "commit", "-m", "feat: modify source").Run()

	// Re-evaluate goal acceptance: must be STALE
	staleAcc, err := EvaluateGoalAcceptance(dir, slug)
	if err != nil {
		t.Fatalf("EvaluateGoalAcceptance failed after source change: %v", err)
	}
	if staleAcc.Accepted {
		t.Fatalf("expected accepted=false after source modification, got true")
	}
	if staleAcc.EvidenceStatus != EvidenceStatusStale {
		t.Errorf("expected evidence_status='stale', got %q", staleAcc.EvidenceStatus)
	}

	// Verify historical acceptance file was preserved and NOT deleted or overwritten
	retainedData, err := os.ReadFile(accPath)
	if err != nil {
		t.Fatalf("task acceptance file missing after staleness check: %v", err)
	}
	if string(retainedData) != string(initialAccData) {
		t.Errorf("historical task acceptance file was modified; expected byte-preservation")
	}
}

func TestAcceptanceLegacyProjection(t *testing.T) {
	// AC-009: The same derived fields appear in GoalView, audit JSON, and task list; legacy counts remain unchanged.
	dir := t.TempDir()
	slug := "legacy-projection-goal"
	goalDir := filepath.Join(dir, "goals", slug)
	_ = os.MkdirAll(goalDir, 0755)

	// Pure legacy goal: no work-package.json, just plan.md with 2 tasks, 1 checked
	_ = os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte("# Goal: legacy-projection-goal\n"), 0644)
	planContent := `# Plan
- [x] Completed task
- [ ] Remaining task
`
	_ = os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte(planContent), 0644)
	_ = os.WriteFile(filepath.Join(goalDir, "facts.md"), []byte("# Facts\n"), 0644)

	audit, err := ScanGoals(dir)
	if err != nil {
		t.Fatalf("ScanGoals failed: %v", err)
	}
	if len(audit.Goals) != 1 {
		t.Fatalf("expected 1 goal, got %d", len(audit.Goals))
	}
	g := audit.Goals[0]

	// Verify legacy counts are preserved: 1/2 tasks met
	if g.TasksTotal != 2 || g.TasksMet != 1 {
		t.Errorf("expected legacy counts TasksTotal=2, TasksMet=1, got %d/%d", g.TasksTotal, g.TasksMet)
	}
	// Acceptance status for legacy goal is not_run
	if g.AcceptanceStatus != "" && g.AcceptanceStatus != string(EvidenceStatusNotRun) {
		t.Errorf("expected empty or not_run acceptance status for legacy goal, got %q", g.AcceptanceStatus)
	}
	if g.AcceptedTasks != 0 {
		t.Errorf("expected AcceptedTasks=0 for legacy goal, got %d", g.AcceptedTasks)
	}

	// Verify GoalView preserves legacy counts
	gv, err := BuildGoalView(dir, &g)
	if err != nil {
		t.Fatalf("BuildGoalView failed: %v", err)
	}
	if gv.TasksTotal != 2 || gv.TasksMet != 1 {
		t.Errorf("GoalView legacy counts: expected 2/1, got %d/%d", gv.TasksTotal, gv.TasksMet)
	}

	// Verify JSON serialization includes the structured fields
	data, err := json.Marshal(g)
	if err != nil {
		t.Fatalf("marshal goal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw goal: %v", err)
	}
	if raw["tasks_total"] != float64(2) || raw["tasks_met"] != float64(1) {
		t.Errorf("raw JSON counts: %v", raw)
	}
}
